#![cfg(target_os = "linux")]
//! Stages bounded, credential-free Git bundle bytes and asks fixed unprivileged
//! units to prepare reviewer checkouts. This is not agent execution admission.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use soda_execution_protocol::{
    AgentExecution as AgentExecutionReply, AgentStarted as AgentStartedReply, MAX_EXECUTABLE,
    MAX_EXECUTION_REPLY as MAX_AGENT_REPLY, MAX_OUTPUT, MAX_PROMPT, MAX_STAGE_BUNDLE as MAX_BUNDLE,
    MAX_START_REPLY,
};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, RawFd};
use std::os::unix::fs::{DirBuilderExt, FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};
mod agent_observation;
mod preparation_quota;
mod source_binding;
mod task_receipts;
mod task_workspace;

const SOCKET: &str = "/run/soda-factory/broker.sock";
const STATE: &str = "/run/soda-factory-broker";
const MAX_REQUEST: usize = soda_execution_protocol::MAX_BROKER_FRAME;
const MAX_RUNS: usize = 64;
// Keep this reservation aligned with preparer@.service MemoryMax.
const MAX_PREPARATION_CHARGE: u64 = preparation_quota::MAX_ACTIVE_BYTES;
const MIN_RUNTIME_RESERVE: u64 = 256 * 1024 * 1024;

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Request {
    ReadTaskReceipt {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        manifest_digest: String,
        invocation: String,
        command_length: usize,
    },
    TaskWorkspace {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
        tool_sha256: String,
        prompt_sha256: String,
        manifest_length: usize,
        manifest_digest: String,
        invocation: String,
    },
    CompleteCandidate {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        base: String,
        candidate: String,
        length: usize,
        delta_sha256: String,
    },
    ReadContext {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
        length: usize,
    },
    StagePreparation {
        version: u8,
        run: String,
        profile: String,
        role: String,
        length: usize,
        sha256: String,
        setup_length: usize,
        setup_sha256: String,
    },
    ReadSetup {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
    },
    StageBundle {
        version: u8,
        run: String,
        profile: String,
        role: String,
        tool_sha256: String,
        prompt_sha256: String,
        length: usize,
        sha256: String,
        #[serde(default)]
        preparation: Option<soda_execution_protocol::PreparedSourceRef>,
    },
    RetireBundle {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
    },
    RetireUnlaunched {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
    },
    RetireLaunched {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
    },
    FinishRetiring {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
    },
    QuiesceRun {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
    },
    NextProfileRun {
        version: u8,
        profile: String,
    },
    PrepareReviewer {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
    },
    AttestReviewer {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
    },
    AttestProviderRelay {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
    },
    AttestSetupNetwork {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        setup_sha256: String,
    },
    ArmAgent {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
        role: String,
        tool_sha256: String,
        prompt_sha256: String,
    },
    AttestAgent {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
        role: String,
        tool_sha256: String,
        prompt_sha256: String,
    },
    ExecuteAgent {
        version: u8,
        run: String,
        profile: String,
        sha256: String,
        candidate: String,
        role: String,
        tool_sha256: String,
        prompt_sha256: String,
        executable_length: u64,
        prompt_length: usize,
        placeholder: String,
        duration_ms: u64,
        #[serde(default = "soda_execution_protocol::HarnessProfile::legacy")]
        harness_profile: soda_execution_protocol::HarnessProfile,
    },
}

#[derive(Serialize)]
struct Staged<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    role: &'a str,
    tool_sha256: &'a str,
    prompt_sha256: &'a str,
    sha256: &'a str,
}

#[derive(Serialize)]
struct Retired<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    removed: bool,
}

#[derive(Serialize)]
struct Quiesced<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    sha256: &'a str,
}

#[derive(Serialize)]
struct ProfileRun<'a> {
    kind: &'static str,
    version: u8,
    profile: &'a str,
    run: Option<&'a str>,
    sha256: Option<&'a str>,
    launched: bool,
    retiring: bool,
}

#[derive(Serialize)]
struct Prepared<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    sha256: &'a str,
    candidate: &'a str,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ReviewerReply {
    kind: String,
    version: u8,
    run: String,
    candidate: String,
    worker_uid: u32,
    peer_uid: u32,
    readonly_worktree: bool,
    execution_available: bool,
}

#[derive(Serialize)]
struct ReviewerAttested<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    candidate: &'a str,
    worker_uid: u32,
    readonly_worktree: bool,
    execution_available: bool,
}

#[derive(Serialize)]
struct ProviderRelayAttested<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    role: &'a str,
    candidate: &'a str,
    relay_uid: u32,
    execution_available: bool,
}
#[derive(Serialize)]
struct SetupNetworkAttested<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    sha256: &'a str,
    setup_sha256: &'a str,
    relay_uid: u32,
}

#[derive(Serialize)]
struct AgentArmed<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    candidate: &'a str,
    role: &'a str,
    tool_sha256: &'a str,
    prompt_sha256: &'a str,
    execution_available: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AgentReply {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    candidate: String,
    role: String,
    tool_sha256: String,
    prompt_sha256: String,
    worker_uid: u32,
    peer_uid: u32,
    readonly_source: bool,
    coder_workspace: bool,
    execution_available: bool,
}

#[derive(Serialize)]
struct AgentAttested<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    candidate: &'a str,
    role: &'a str,
    tool_sha256: &'a str,
    prompt_sha256: &'a str,
    worker_uid: u32,
    peer_uid: u32,
    readonly_source: bool,
    coder_workspace: bool,
    execution_available: bool,
}

#[derive(Serialize)]
struct AgentRequest<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
}

#[derive(Serialize)]
struct AgentExecutionRequest<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    profile: &'a str,
    candidate: &'a str,
    tool_sha256: &'a str,
    prompt_sha256: &'a str,
    executable_length: u64,
    prompt_length: usize,
    placeholder: &'a str,
    duration_ms: u64,
    harness_profile: &'a soda_execution_protocol::HarnessProfile,
}

#[derive(Serialize)]
struct ReviewerRequest<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    candidate: &'a str,
}

fn denied(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn id(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn boot_id(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
}

fn private_dir(path: &Path, mode: u32) -> io::Result<()> {
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.file_type().is_symlink()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != mode
    {
        return Err(denied("broker state permissions invalid"));
    }
    Ok(())
}

fn socket(fd: RawFd) -> io::Result<u32> {
    if unsafe { libc::geteuid() } != 0 || unsafe { libc::getuid() } != 0 {
        return Err(denied("broker requires root-owned service"));
    }
    if unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1 {
        return Err(denied("broker can gain privileges"));
    }
    let parent = Path::new(SOCKET).parent().expect("fixed socket path");
    let parent_meta = parent.symlink_metadata()?;
    if !parent_meta.is_dir() || parent_meta.uid() != 0 || parent_meta.mode() & 0o7777 != 0o755 {
        return Err(denied("broker socket parent invalid"));
    }
    let socket_meta = Path::new(SOCKET).symlink_metadata()?;
    if !socket_meta.file_type().is_socket() || socket_meta.mode() & 0o7777 != 0o600 {
        return Err(denied("broker socket permissions invalid"));
    }
    let mut kind = 0;
    let mut kind_len = std::mem::size_of_val(&kind) as libc::socklen_t;
    let mut peer = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut peer_len = std::mem::size_of_val(&peer) as libc::socklen_t;
    let timeout = libc::timeval {
        tv_sec: 3,
        tv_usec: 0,
    };
    // SAFETY: all option pointers refer to initialized stack values.
    let valid = unsafe {
        libc::getsockopt(
            fd,
            libc::SOL_SOCKET,
            libc::SO_TYPE,
            (&mut kind as *mut i32).cast(),
            &mut kind_len,
        ) == 0
            && libc::getsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_PEERCRED,
                (&mut peer as *mut libc::ucred).cast(),
                &mut peer_len,
            ) == 0
            && libc::setsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_RCVTIMEO,
                (&timeout as *const libc::timeval).cast(),
                std::mem::size_of_val(&timeout) as libc::socklen_t,
            ) == 0
            && libc::setsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_SNDTIMEO,
                (&timeout as *const libc::timeval).cast(),
                std::mem::size_of_val(&timeout) as libc::socklen_t,
            ) == 0
    };
    if !valid
        || kind_len as usize != std::mem::size_of_val(&kind)
        || peer_len as usize != std::mem::size_of_val(&peer)
        || kind != libc::SOCK_STREAM
        || peer.pid <= 0
        || peer.uid == 0
        || peer.uid != socket_meta.uid()
    {
        return Err(denied("broker peer admission failed"));
    }
    Ok(peer.uid)
}

fn state() -> io::Result<PathBuf> {
    if std::env::var_os("RUNTIME_DIRECTORY").as_deref() != Some(std::ffi::OsStr::new(STATE)) {
        return Err(denied("broker runtime directory missing"));
    }
    let state = PathBuf::from(STATE);
    private_dir(&state, 0o700)?;
    let runs = state.join("runs");
    if !runs.exists() {
        match DirBuilder::new().mode(0o700).create(&runs) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
    }
    private_dir(&runs, 0o700)?;
    let preparations = state.join("preparations");
    if !preparations.exists() {
        match DirBuilder::new().mode(0o700).create(&preparations) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
    }
    private_dir(&preparations, 0o700)?;
    Ok(runs)
}

fn preparations() -> io::Result<PathBuf> {
    let directory = Path::new(STATE).join("preparations");
    private_dir(&directory, 0o700)?;
    Ok(directory)
}

fn staged_digest(directory: &Path) -> io::Result<Option<String>> {
    let bundle_path = directory.join("bundle");
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(bundle_path)
    {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || !(1..=MAX_BUNDLE as u64).contains(&metadata.len())
    {
        return Err(denied("staged bundle metadata invalid"));
    }
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    file.take(MAX_BUNDLE as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() {
        return Err(denied("staged bundle length changed"));
    }
    Ok(Some(hash(&bytes)))
}

fn verified_bundle(directory: &Path, digest: &str) -> io::Result<bool> {
    match staged_digest(directory)? {
        Some(actual) if actual == digest => Ok(true),
        Some(_) => Err(denied("staged bundle digest mismatch")),
        None => Ok(false),
    }
}

fn read_profile(directory: &Path) -> io::Result<String> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join("owner.profile"))?;
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || metadata.len() != 33
    {
        return Err(denied("staged profile declaration invalid"));
    }
    let mut bytes = Vec::with_capacity(33);
    file.take(34).read_to_end(&mut bytes)?;
    if bytes.len() != 33 || bytes[32] != b'\n' {
        return Err(denied("staged profile declaration invalid"));
    }
    let profile = std::str::from_utf8(&bytes[..32])
        .map_err(|_| denied("staged profile declaration invalid"))?;
    if !id(profile, 32) {
        return Err(denied("staged profile declaration invalid"));
    }
    Ok(profile.to_owned())
}

fn verified_profile(directory: &Path, profile: &str) -> io::Result<()> {
    if read_profile(directory)? != profile {
        return Err(denied("run belongs to another profile"));
    }
    Ok(())
}

fn read_role(directory: &Path) -> io::Result<String> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join("owner.role"))?;
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || !matches!(metadata.len(), 6 | 9)
    {
        return Err(denied("staged role declaration invalid"));
    }
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    file.take(10).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() {
        return Err(denied("staged role changed during read"));
    }
    match bytes.as_slice() {
        b"coder\n" => Ok("coder".into()),
        b"reviewer\n" => Ok("reviewer".into()),
        _ => Err(denied("staged role declaration invalid")),
    }
}

fn verified_role(directory: &Path, role: &str) -> io::Result<()> {
    if read_role(directory)? != role {
        return Err(denied("run belongs to another role"));
    }
    Ok(())
}

fn read_digest(directory: &Path, name: &'static str) -> io::Result<String> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join(name))?;
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || metadata.len() != 65
    {
        return Err(denied("staged digest declaration invalid"));
    }
    let mut bytes = Vec::with_capacity(65);
    file.take(66).read_to_end(&mut bytes)?;
    if bytes.len() != 65 || bytes[64] != b'\n' {
        return Err(denied("staged digest declaration changed"));
    }
    let digest = std::str::from_utf8(&bytes[..64])
        .map_err(|_| denied("staged digest declaration invalid"))?;
    if !id(digest, 64) {
        return Err(denied("staged digest declaration invalid"));
    }
    Ok(digest.into())
}

fn verified_digest(directory: &Path, name: &'static str, digest: &str) -> io::Result<()> {
    if read_digest(directory, name)? != digest {
        return Err(denied("run belongs to another digest"));
    }
    Ok(())
}

fn manifest(candidate: &str, digest: &str) -> String {
    format!("FACTORY_CANDIDATE={candidate}\nFACTORY_BUNDLE_SHA256={digest}\n")
}

fn read_manifest(path: &Path) -> io::Result<Option<String>> {
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)
    {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o400
        || metadata.len() > 192
    {
        return Err(denied("preparation manifest invalid"));
    }
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    file.take(193).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() {
        return Err(denied("preparation manifest changed"));
    }
    Ok(String::from_utf8(bytes).ok())
}

fn manifest_identity(value: &str) -> Option<(&str, &str)> {
    let (candidate, digest) = value
        .strip_prefix("FACTORY_CANDIDATE=")?
        .split_once("\nFACTORY_BUNDLE_SHA256=")?;
    let digest = digest.strip_suffix('\n')?;
    (id(candidate, 40) && id(digest, 64)).then_some((candidate, digest))
}

fn manager_unit(action: &str, unit: &str) -> io::Result<()> {
    let status = Command::new("/usr/bin/systemctl")
        .env_clear()
        .args(["--system", "--no-ask-password", action, unit])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()?;
    if !status.success() {
        return Err(denied("fixed preparation service failed"));
    }
    Ok(())
}

fn manager(action: &str, unit: &str, run: &str) -> io::Result<()> {
    manager_unit(action, &format!("soda-factory-{unit}@{run}.service"))
}

// Startup requests keep their read half open so EOF means the native owner
// cancelled. Extra client bytes are not another operation on this connection.
fn preparation_client() -> io::Result<()> {
    let mut byte = 0u8;
    let received = unsafe {
        libc::recv(
            0,
            (&mut byte as *mut u8).cast(),
            1,
            libc::MSG_PEEK | libc::MSG_DONTWAIT,
        )
    };
    if received >= 0 {
        return Err(denied("preparation caller left or sent extra bytes"));
    }
    let error = io::Error::last_os_error();
    if matches!(
        error.kind(),
        io::ErrorKind::WouldBlock | io::ErrorKind::Interrupted
    ) {
        Ok(())
    } else {
        Err(error)
    }
}

fn start_preparation(unit: &str, seconds: u64) -> io::Result<()> {
    preparation_client()?;
    let mut child = Command::new("/usr/bin/systemctl")
        .env_clear()
        .args(["--system", "--no-ask-password", "start", unit])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()?;
    let deadline = Instant::now() + Duration::from_secs(seconds);
    let result = (|| loop {
        preparation_client()?;
        if Instant::now() >= deadline {
            return Err(denied("preparation startup timed out"));
        }
        if let Some(status) = child.try_wait()? {
            return if status.success() {
                Ok(())
            } else {
                Err(denied("preparation startup failed"))
            };
        }
        std::thread::sleep(Duration::from_millis(20));
    })();
    if result.is_err() {
        // Stop the exact service job and join our manager child even when
        // polling fails. Cleanup continues after caller cancellation.
        let stopped = manager_unit("stop", unit);
        let _ = child.kill();
        let joined = child.wait();
        stopped?;
        joined?;
    }
    result
}

fn read_preparation(
    stream: &mut UnixStream,
    bytes: &mut [u8],
    deadline: Instant,
) -> io::Result<()> {
    stream.set_read_timeout(Some(Duration::from_millis(100)))?;
    let mut offset = 0;
    while offset < bytes.len() {
        preparation_client()?;
        if Instant::now() >= deadline {
            return Err(denied("preparation attestation timed out"));
        }
        match stream.read(&mut bytes[offset..]) {
            Ok(0) => return Err(io::Error::from(io::ErrorKind::UnexpectedEof)),
            Ok(length) => offset += length,
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::WouldBlock
                        | io::ErrorKind::TimedOut
                        | io::ErrorKind::Interrupted
                ) =>
            {
                ()
            }
            Err(error) => return Err(error),
        }
    }
    Ok(())
}

fn stop_reviewer(run: &str) -> io::Result<()> {
    manager_unit("stop", &format!("soda-factory-reviewer@{run}.service"))?;
    manager_unit("stop", &format!("soda-factory-reviewer@{run}.socket"))
}

fn relay_runtime(run: &str) -> PathBuf {
    Path::new("/run/soda-factory-relay").join(run)
}

fn stop_relay(run: &str) -> io::Result<()> {
    let runtime = relay_runtime(run);
    match runtime.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
        Ok(_) => (),
    }
    manager("stop", "provider-relay", run)?;
    match runtime.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
        Ok(_) => Err(denied("provider relay runtime survived stop")),
    }
}

fn stop_agent(run: &str, directory: &Path) -> io::Result<()> {
    let role = directory.join("agent.role");
    match role.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
        Ok(metadata)
            if metadata.is_file()
                && metadata.uid() == 0
                && matches!(metadata.mode() & 0o7777, 0o400 | 0o444) =>
        {
            ()
        }
        Ok(_) => return Err(denied("agent role manifest invalid")),
    }
    manager_unit("stop", &format!("soda-factory-agent@{run}.socket"))?;
    manager_unit("stop", &format!("soda-factory-agent@{run}.service"))?;
    let socket = Path::new("/run/soda-factory/agents").join(format!("{run}.sock"));
    if socket.exists() {
        return Err(denied("agent socket survived stop"));
    }
    let runtime = Path::new("/run/soda-factory-agent").join(run);
    if runtime.exists() {
        return Err(denied("agent runtime survived stop"));
    }
    Ok(())
}

fn preparation_run(preparations: &Path, run: &str) -> io::Result<Option<PathBuf>> {
    let directory = preparations.join(run);
    match directory.symlink_metadata() {
        Ok(_) => private_dir(&directory, 0o700).map(|()| Some(directory)),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error),
    }
}

fn clean_preparation(preparations: &Path, run: &str) -> io::Result<()> {
    let Some(directory) = preparation_run(preparations, run)? else {
        return Ok(());
    };
    stop_agent(run, &directory)?;
    stop_relay(run)?;
    stop_reviewer(run)?;
    manager("stop", "preparer", run)?;
    manager("stop", "context", run)?;
    source_binding::clear_context(&directory)?;
    manager("start", "preparation-cleaner", run)?;
    if fs::read_dir(&directory)?.next().is_some() {
        return Err(denied("preparation cleanup incomplete"));
    }
    fs::remove_dir(&directory)?;
    File::open(preparations)?.sync_all()
}

fn prepared_output(directory: &Path) -> io::Result<Option<u32>> {
    let output = directory.join("output");
    let metadata = match output.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let mode = metadata.mode() & 0o7777;
    if !metadata.is_dir() || metadata.uid() != 0 || !matches!(mode, 0o700 | 0o777 | 0o555) {
        return Err(denied("preparation output invalid"));
    }
    Ok(Some(mode))
}

fn prepared_checkout(directory: &Path) -> io::Result<()> {
    let metadata = directory.join("output/repo").symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() == 0 || metadata.mode() & 0o7777 != 0o755 {
        return Err(denied("prepared reviewer checkout invalid"));
    }
    Ok(())
}

fn runtime_capacity(extra: u64) -> io::Result<()> {
    let path = c"/run";
    let mut filesystem = std::mem::MaybeUninit::<libc::statfs>::uninit();
    if unsafe { libc::statfs(path.as_ptr(), filesystem.as_mut_ptr()) } != 0
        || unsafe { filesystem.assume_init() }.f_type != libc::TMPFS_MAGIC
    {
        return Err(denied("broker runtime filesystem is not tmpfs"));
    }
    let mut space = std::mem::MaybeUninit::<libc::statvfs>::uninit();
    if unsafe { libc::statvfs(path.as_ptr(), space.as_mut_ptr()) } != 0 {
        return Err(io::Error::last_os_error());
    }
    let space = unsafe { space.assume_init() };
    let block = space.f_frsize as u64;
    let total = (space.f_blocks as u64)
        .checked_mul(block)
        .ok_or_else(|| denied("broker runtime capacity invalid"))?;
    let free = (space.f_bavail as u64)
        .checked_mul(block)
        .ok_or_else(|| denied("broker runtime capacity invalid"))?;
    let reserve = (total / 4).max(MIN_RUNTIME_RESERVE);
    if free < reserve.saturating_add(extra) {
        return Err(denied("broker runtime free-space reserve unavailable"));
    }
    Ok(())
}

fn prepare(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    desktop_uid: u32,
) -> io::Result<()> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let source = runs.join(run);
    if source.join("owner.setup_sha256").try_exists()? {
        preparation_caller_current()?;
    }
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("reviewer source bundle missing"));
    }
    let seconds = if source.join("owner.setup_sha256").try_exists()? {
        source_binding::approved(&source)?
            .limits()
            .ok_or_else(|| denied("approved setup time limits invalid"))?
            .preparation_seconds()
    } else {
        90
    };
    let preparations = preparations()?;
    let expected = manifest(candidate, digest);
    if let Some(directory) = preparation_run(&preparations, run)? {
        let prior = read_manifest(&directory.join("manifest.env"))?;
        if let Some((old_candidate, old_digest)) = prior.as_deref().and_then(manifest_identity) {
            if old_candidate != candidate || old_digest != digest {
                return Err(denied("run already prepared for another candidate"));
            }
        }
        if prepared_output(&directory)? == Some(0o555) {
            if prior.as_deref() != Some(expected.as_str()) {
                return Err(denied("completed preparation manifest invalid"));
            }
            prepared_checkout(&directory)?;
            preparation_quota::capacity(&preparations, 0)?;
            if source.join("network.env").try_exists()? {
                stop_relay(run)?;
            }
            return Ok(());
        }
        clean_preparation(&preparations, run)?;
    }
    let captured = source_binding::captured(&source)?;
    // Only pure preparations can be referenced, so directory locks cannot cycle.
    // Keep the original frozen until its independent copy has finished.
    drop(_lock);
    let (_lock, _source_lock) = if let Some(value) = &captured {
        if value.source.run == run || value.source.candidate != candidate {
            return Err(denied("prepared snapshot selector differs"));
        }
        let locks = lock_run(&runs, &value.source.run)?;
        source_binding::verify_captured(value, profile, digest, &read_role(&source)?, desktop_uid)?;
        locks
    } else {
        (lock(&runs)?, None)
    };
    preparation_caller_current()?;
    let reserved = preparation_quota::capacity(&preparations, MAX_PREPARATION_CHARGE)?;
    runtime_capacity(reserved + MAX_PREPARATION_CHARGE)?;
    let directory = preparations.join(run);
    DirBuilder::new().mode(0o700).create(&directory)?;
    let output = directory.join("output");
    DirBuilder::new().mode(0o700).create(&output)?;
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o400)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join("manifest.env"))?;
    file.write_all(expected.as_bytes())?;
    file.sync_all()?;
    fs::set_permissions(&output, fs::Permissions::from_mode(0o777))?;
    File::open(&directory)?.sync_all()?;
    // Retain this run's directory lock while releasing shared admission state.
    // Other runs can prepare or retire; same-run callers wait outside that lock.
    drop(_lock);
    let network = source.join("network.env").try_exists()?;
    let started = (|| {
        if network {
            manager("is-active", "provider-relay", run)?;
        }
        start_preparation(&format!("soda-factory-preparer@{run}.service"), seconds)
    })();
    let stopped = if network { stop_relay(run) } else { Ok(()) };
    let started = started.and(stopped);
    let _lock = lock(&runs)?;
    let result = started.and_then(|()| {
        prepared_checkout(&directory)?;
        private_dir(&output, 0o777)?;
        fs::set_permissions(&output, fs::Permissions::from_mode(0o555))?;
        let reserved = preparation_quota::capacity(&preparations, 0)?;
        runtime_capacity(reserved)?;
        File::open(&directory)?.sync_all()
    });
    if result.is_err() {
        clean_preparation(&preparations, run)?;
    }
    result
}

fn attest_reviewer(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    desktop_uid: u32,
) -> io::Result<u32> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    verified_role(&source, "reviewer")?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("reviewer source bundle missing"));
    }
    let preparations = preparations()?;
    let directory = preparation_run(&preparations, run)?
        .ok_or_else(|| denied("reviewer preparation missing"))?;
    if read_manifest(&directory.join("manifest.env"))?.as_deref()
        != Some(manifest(candidate, digest).as_str())
        || prepared_output(&directory)? != Some(0o555)
    {
        return Err(denied("reviewer preparation identity invalid"));
    }
    prepared_checkout(&directory)?;
    let socket_unit = format!("soda-factory-reviewer@{run}.socket");
    start_preparation(&socket_unit, 90)?;
    let socket_path = directory.join("reviewer.sock");
    let metadata = socket_path.symlink_metadata()?;
    if !metadata.file_type().is_socket() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o600
    {
        return Err(denied("reviewer run socket invalid"));
    }
    let mut stream = UnixStream::connect(socket_path)?;
    let deadline = Instant::now() + Duration::from_secs(5);
    stream.set_read_timeout(Some(Duration::from_secs(5)))?;
    stream.set_write_timeout(Some(Duration::from_secs(3)))?;
    let request = serde_json::to_vec(&ReviewerRequest {
        kind: "attest_run",
        version: 1,
        run,
        candidate,
    })?;
    stream.write_all(&(request.len() as u32).to_le_bytes())?;
    stream.write_all(&request)?;
    let mut header = [0u8; 4];
    read_preparation(&mut stream, &mut header, deadline)?;
    let length = u32::from_le_bytes(header) as usize;
    if length == 0 || length > 512 {
        return Err(denied("reviewer attestation bound"));
    }
    let mut bytes = vec![0; length];
    read_preparation(&mut stream, &mut bytes, deadline)?;
    let reply: ReviewerReply =
        serde_json::from_slice(&bytes).map_err(|_| denied("reviewer attestation shape invalid"))?;
    if reply.kind != "run_attested"
        || reply.version != 1
        || reply.run != run
        || reply.candidate != candidate
        || reply.worker_uid == 0
        || reply.worker_uid == desktop_uid
        || reply.peer_uid != 0
        || !reply.readonly_worktree
        || reply.execution_available
    {
        return Err(denied("reviewer attestation identity invalid"));
    }
    Ok(reply.worker_uid)
}

fn attest_provider_relay(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    desktop_uid: u32,
) -> io::Result<(u32, String)> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    let role = read_role(&source)?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("provider relay source bundle missing"));
    }
    let preparations = preparations()?;
    let directory = preparation_run(&preparations, run)?
        .ok_or_else(|| denied("provider relay preparation missing"))?;
    if read_manifest(&directory.join("manifest.env"))?.as_deref()
        != Some(manifest(candidate, digest).as_str())
        || prepared_output(&directory)? != Some(0o555)
    {
        return Err(denied("provider relay preparation identity invalid"));
    }
    prepared_checkout(&directory)?;
    start_provider_relay(run, desktop_uid).map(|uid| (uid, role))
}

fn start_provider_relay(run: &str, desktop_uid: u32) -> io::Result<u32> {
    if let Err(error) = start_preparation(&format!("soda-factory-provider-relay@{run}.service"), 90)
    {
        manager("stop", "provider-relay", run)?;
        return Err(error);
    }
    let verified = (|| {
        manager("is-active", "provider-relay", run)?;
        let metadata = relay_runtime(run).symlink_metadata()?;
        let relay_uid = metadata.uid();
        if !metadata.is_dir()
            || metadata.mode() & 0o7777 != 0o700
            || relay_uid == 0
            || relay_uid == desktop_uid
        {
            return Err(denied("provider relay runtime identity invalid"));
        }
        Ok(relay_uid)
    })();
    if verified.is_err() {
        manager("stop", "provider-relay", run)?;
    }
    verified
}

fn attest_setup_network(
    run: &str,
    profile: &str,
    digest: &str,
    setup: &str,
    desktop_uid: u32,
) -> io::Result<u32> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let source = runs.join(run);
    preparation_caller_current()?;
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    let approved = source_binding::approved(&source)?;
    if approved.digest() != setup
        || approved.public_https() != Some(true)
        || source.join("owner.tool_sha256").try_exists()?
        || !verified_bundle(&source, digest)?
        || fs::read(source.join("network.env"))? != b"FACTORY_RELAY_MODE=setup\n"
    {
        return Err(denied("setup network approval differs"));
    }
    start_provider_relay(run, desktop_uid)
}

fn agent_manifest(
    role: &str,
    profile: &str,
    candidate: &str,
    digest: &str,
    tool: &str,
    prompt: &str,
) -> String {
    format!(
        "FACTORY_ROLE={role}\nFACTORY_PROFILE={profile}\nFACTORY_CANDIDATE={candidate}\nFACTORY_BUNDLE_SHA256={digest}\nFACTORY_TOOL_SHA256={tool}\nFACTORY_PROMPT_SHA256={prompt}\n"
    )
}

fn arm_agent(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    role: &str,
    expected_tool: &str,
    expected_prompt: &str,
    cancellable: bool,
) -> io::Result<(String, String)> {
    if !matches!(role, "reviewer" | "coder") {
        return Err(denied("agent role invalid"));
    }
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    verified_role(&source, role)?;
    verified_digest(&source, "owner.tool_sha256", expected_tool)?;
    verified_digest(&source, "owner.prompt_sha256", expected_prompt)?;
    let tool = expected_tool.to_owned();
    let prompt = expected_prompt.to_owned();
    if !verified_bundle(&source, digest)? {
        return Err(denied("agent source bundle missing"));
    }
    let preparations = preparations()?;
    let directory =
        preparation_run(&preparations, run)?.ok_or_else(|| denied("agent preparation missing"))?;
    match directory.join("agent.launch").symlink_metadata() {
        Ok(_) => return Err(denied("agent launch already consumed")),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    if read_manifest(&directory.join("manifest.env"))?.as_deref()
        != Some(manifest(candidate, digest).as_str())
        || prepared_output(&directory)? != Some(0o555)
    {
        return Err(denied("agent preparation identity invalid"));
    }
    prepared_checkout(&directory)?;
    manager("is-active", "provider-relay", run)?;
    let expected = agent_manifest(role, profile, candidate, digest, &tool, &prompt);
    let path = directory.join("agent.role");
    if let Ok(metadata) = path.symlink_metadata() {
        if metadata.is_file() && metadata.uid() == 0 && metadata.mode() & 0o7777 == 0o400 {
            // UMask leaves a newly created declaration at 0400 until its
            // contents have been synced and the final mode is installed.
            // Recover this interrupted write only after stopping the unit.
            stop_agent(run, &directory)?;
            fs::remove_file(&path)?;
            File::open(&directory)?.sync_all()?;
        }
    }
    let created = match path.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            let mut file = OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o444)
                .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                .open(&path)?;
            file.write_all(expected.as_bytes())?;
            file.sync_all()?;
            fs::set_permissions(&path, fs::Permissions::from_mode(0o444))?;
            File::open(&directory)?.sync_all()?;
            true
        }
        Err(error) => return Err(error),
        Ok(_) => false,
    };
    let mut socket_attempted = false;
    let checked = (|| {
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&path)?;
        let metadata = file.metadata()?;
        if !metadata.is_file()
            || metadata.uid() != 0
            || metadata.mode() & 0o7777 != 0o444
            || metadata.len() != expected.len() as u64
        {
            return Err(denied("agent role manifest invalid"));
        }
        let mut actual = String::new();
        file.take(expected.len() as u64 + 1)
            .read_to_string(&mut actual)?;
        if actual != expected {
            return Err(denied("agent role already differs"));
        }
        socket_attempted = true;
        let unit = format!("soda-factory-agent@{run}.socket");
        if cancellable {
            start_preparation(&unit, 90)?;
        } else {
            manager_unit("start", &unit)?;
        }
        let socket = Path::new("/run/soda-factory/agents").join(format!("{run}.sock"));
        let parent = socket.parent().expect("fixed agent socket parent");
        private_dir(parent, 0o755)?;
        let metadata = socket.symlink_metadata()?;
        if !metadata.file_type().is_socket()
            || metadata.uid() != 0
            || metadata.mode() & 0o7777 != 0o600
        {
            return Err(denied("agent socket identity invalid"));
        }
        Ok(())
    })();
    if checked.is_err() && (created || socket_attempted) {
        // Keep the declaration on a failed stop so retirement can still find
        // and stop a potentially armed unit on its next exact-run retry.
        if stop_agent(run, &directory).is_ok() && created {
            let _ = fs::remove_file(&path);
            let _ = File::open(&directory).and_then(|file| file.sync_all());
        }
    }
    checked.map(|()| (tool, prompt))
}

fn agent_start_time(run: &str) -> io::Result<Duration> {
    let source = PathBuf::from(STATE).join("runs").join(run);
    let seconds = if source.join("owner.setup_sha256").try_exists()? {
        source_binding::approved(&source)?
            .limits()
            .ok_or_else(|| denied("approved worker check time limit invalid"))?
            .check_seconds as u64
    } else {
        0
    };
    Ok(Duration::from_secs(seconds + 60))
}

fn attest_agent(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    role: &str,
    tool_sha256: &str,
    prompt_sha256: &str,
    desktop_uid: u32,
) -> io::Result<u32> {
    arm_agent(
        run,
        profile,
        digest,
        candidate,
        role,
        tool_sha256,
        prompt_sha256,
        true,
    )?;
    let socket = PathBuf::from(format!("/run/soda-factory/agents/{run}.sock"));
    let metadata = socket.symlink_metadata()?;
    if !metadata.file_type().is_socket() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o600
    {
        return Err(denied("agent socket identity invalid"));
    }
    let mut stream = UnixStream::connect(&socket)?;
    let duration = agent_start_time(run)?;
    let deadline = Instant::now() + duration;
    stream.set_read_timeout(Some(duration))?;
    stream.set_write_timeout(Some(Duration::from_secs(60)))?;
    let mut ready = [0u8; 1];
    read_preparation(&mut stream, &mut ready, deadline)?;
    if ready != *b"R" {
        return Err(denied("agent readiness invalid"));
    }
    let request = serde_json::to_vec(&AgentRequest {
        kind: "attest_agent",
        version: 1,
        run,
        profile,
    })?;
    stream.write_all(&(request.len() as u32).to_le_bytes())?;
    stream.write_all(&request)?;
    let mut header = [0u8; 4];
    read_preparation(&mut stream, &mut header, deadline)?;
    let length = u32::from_le_bytes(header) as usize;
    if length == 0 || length > MAX_REQUEST {
        return Err(denied("agent attestation reply bound invalid"));
    }
    let mut bytes = vec![0; length];
    read_preparation(&mut stream, &mut bytes, deadline)?;
    let reply: AgentReply = serde_json::from_slice(&bytes)
        .map_err(|_| denied("agent attestation reply shape invalid"))?;
    if reply.kind != "agent_attested"
        || reply.version != 1
        || reply.run != run
        || reply.profile != profile
        || reply.candidate != candidate
        || reply.role != role
        || reply.tool_sha256 != tool_sha256
        || reply.prompt_sha256 != prompt_sha256
        || reply.worker_uid == 0
        || reply.worker_uid == desktop_uid
        || reply.peer_uid != 0
        || !reply.readonly_source
        || reply.coder_workspace != (role == "coder")
        || reply.execution_available
    {
        return Err(denied("agent attestation identity invalid"));
    }
    Ok(reply.worker_uid)
}

fn client_read_timeout() -> io::Result<()> {
    let timeout = libc::timeval {
        tv_sec: 3,
        tv_usec: 0,
    };
    if unsafe {
        libc::setsockopt(
            0,
            libc::SOL_SOCKET,
            libc::SO_RCVTIMEO,
            (&timeout as *const libc::timeval).cast(),
            std::mem::size_of_val(&timeout) as libc::socklen_t,
        )
    } != 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

fn read_client_exact<R: Read>(
    input: &mut R,
    bytes: &mut [u8],
    deadline: Instant,
) -> io::Result<()> {
    let mut offset = 0;
    while offset < bytes.len() {
        if Instant::now() >= deadline {
            return Err(denied("broker client upload deadline exceeded"));
        }
        match input.read(&mut bytes[offset..])? {
            0 => return Err(io::Error::from(io::ErrorKind::UnexpectedEof)),
            length => offset += length,
        }
    }
    Ok(())
}

fn copy_exact<R: Read, W: Write>(
    input: &mut R,
    output: &mut W,
    mut length: u64,
    deadline: Instant,
) -> io::Result<()> {
    let mut buffer = [0u8; 65536];
    while length != 0 {
        if Instant::now() >= deadline {
            return Err(denied("agent upload deadline exceeded"));
        }
        let want = buffer.len().min(length as usize);
        let received = input.read(&mut buffer[..want])?;
        if received == 0 {
            return Err(io::Error::from(io::ErrorKind::UnexpectedEof));
        }
        output.write_all(&buffer[..received])?;
        length -= received as u64;
    }
    Ok(())
}

fn read_worker(
    stream: &mut UnixStream,
    bytes: &mut [u8],
    deadline: Instant,
    queries: &mut agent_observation::Queries,
) -> io::Result<()> {
    let mut offset = 0;
    stream.set_read_timeout(Some(Duration::from_millis(100)))?;
    while offset < bytes.len() {
        queries.pump(stream)?;
        if Instant::now() >= deadline {
            return Err(denied("agent caller left before process retirement"));
        }
        match stream.read(&mut bytes[offset..]) {
            Ok(0) => return Err(io::Error::from(io::ErrorKind::UnexpectedEof)),
            Ok(length) => offset += length,
            Err(error)
                if matches!(
                    error.kind(),
                    io::ErrorKind::WouldBlock | io::ErrorKind::TimedOut
                ) =>
            {
                ()
            }
            Err(error) => return Err(error),
        }
    }
    Ok(())
}

fn execute_agent<R: Read, W: Write>(
    input: &mut R,
    output: &mut W,
    request: &AgentExecutionRequest<'_>,
    bundle_sha256: &str,
    role: &str,
    desktop_uid: u32,
) -> io::Result<()> {
    let run = request.run;
    let profile = request.profile;
    let candidate = request.candidate;
    let tool_sha256 = request.tool_sha256;
    let prompt_sha256 = request.prompt_sha256;
    let bytes = serde_json::to_vec(request)?;
    if bytes.is_empty() || bytes.len() > soda_execution_protocol::MAX_AGENT_REQUEST {
        return Err(denied("agent execution request bound invalid"));
    }
    arm_agent(
        run,
        profile,
        bundle_sha256,
        candidate,
        role,
        tool_sha256,
        prompt_sha256,
        false,
    )?;
    let directory = PathBuf::from(STATE).join("preparations").join(run);
    let result = (|| {
        private_dir(&directory, 0o700)?;
        let path = PathBuf::from(format!("/run/soda-factory/agents/{run}.sock"));
        let mut worker = UnixStream::connect(&path)?;
        worker.set_read_timeout(Some(agent_start_time(run)?))?;
        worker.set_write_timeout(Some(Duration::from_secs(120)))?;
        let mut ready = [0u8; 1];
        worker.read_exact(&mut ready)?;
        if ready != *b"R" {
            return Err(denied("agent execution readiness invalid"));
        }
        mark_root_launch(run, profile, bundle_sha256)?;
        let marker = directory.join("agent.launch");
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o444)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&marker)?;
        file.write_all(b"consumed\n")?;
        file.sync_all()?;
        file.set_permissions(fs::Permissions::from_mode(0o444))?;
        File::open(&directory)?.sync_all()?;
        manager_unit("stop", &format!("soda-factory-agent@{run}.socket"))?;
        if path.exists() {
            return Err(denied("agent launch socket survived consumption"));
        }
        worker.write_all(&(bytes.len() as u32).to_le_bytes())?;
        worker.write_all(&bytes)?;
        let upload_deadline = Instant::now() + Duration::from_secs(120);
        copy_exact(
            input,
            &mut worker,
            request.prompt_length as u64,
            upload_deadline,
        )?;
        copy_exact(
            input,
            &mut worker,
            request.executable_length,
            upload_deadline,
        )?;
        let deadline =
            Instant::now() + Duration::from_millis(request.duration_ms) + Duration::from_secs(180);
        worker.set_write_timeout(Some(Duration::from_millis(100)))?;
        let mut queries = agent_observation::Queries::default();
        let mut header = [0u8; 4];
        read_worker(&mut worker, &mut header, deadline, &mut queries)?;
        let length = u32::from_le_bytes(header) as usize;
        if length == 0 || length > MAX_START_REPLY {
            return Err(denied("agent start announcement bound invalid"));
        }
        let mut bytes = vec![0u8; length];
        read_worker(&mut worker, &mut bytes, deadline, &mut queries)?;
        let started: AgentStartedReply = serde_json::from_slice(&bytes)
            .map_err(|_| denied("agent start announcement shape invalid"))?;
        if started.kind != "agent_started"
            || started.version != 1
            || started.run != run
            || started.profile != profile
            || started.candidate != candidate
            || started.role != role
            || started.tool_sha256 != tool_sha256
            || started.prompt_sha256 != prompt_sha256
            || started.worker_uid == 0
            || started.worker_uid == desktop_uid
            || started.peer_uid != 0
            || !id(&started.invocation, 32)
            || !boot_id(&started.system_instance)
            || !matches!(started.start_identity.parse::<u64>(), Ok(ticks) if ticks > 0)
            || started.namespace_pid == 0
            || started.started_unix_ms == 0
        {
            return Err(denied("agent start announcement identity invalid"));
        }
        output.write_all(&(bytes.len() as u32).to_le_bytes())?;
        output.write_all(&bytes)?;
        output.flush()?;
        let bytes = loop {
            read_worker(&mut worker, &mut header, deadline, &mut queries)?;
            let length = u32::from_le_bytes(header) as usize;
            if length == 0 || length > MAX_AGENT_REPLY {
                return Err(denied("agent execution reply bound invalid"));
            }
            let mut bytes = vec![0u8; length];
            read_worker(&mut worker, &mut bytes, deadline, &mut queries)?;
            if !queries.accept(&bytes)? {
                break bytes;
            }
            output.write_all(&(bytes.len() as u32).to_le_bytes())?;
            output.write_all(&bytes)?;
            output.flush()?;
        };
        let reply: AgentExecutionReply = serde_json::from_slice(&bytes)
            .map_err(|_| denied("agent execution reply shape invalid"))?;
        if reply.kind != "agent_executed"
            || reply.version != 3
            || reply.run != run
            || reply.profile != profile
            || reply.candidate != candidate
            || reply.role != role
            || reply.tool_sha256 != tool_sha256
            || reply.prompt_sha256 != prompt_sha256
            || reply.worker_uid == 0
            || reply.worker_uid == desktop_uid
            || reply.peer_uid != 0
            || !id(&reply.invocation, 32)
            || !boot_id(&reply.system_instance)
            || !matches!(reply.start_identity.parse::<u64>(), Ok(ticks) if ticks > 0)
            || reply.namespace_pid == 0
            || reply.started_unix_ms == 0
            || reply.worker_uid != started.worker_uid
            || reply.invocation != started.invocation
            || reply.system_instance != started.system_instance
            || reply.start_identity != started.start_identity
            || reply.namespace_pid != started.namespace_pid
            || reply.started_unix_ms != started.started_unix_ms
            || reply
                .exit_code
                .is_some_and(|code| !(0..=255).contains(&code))
            || reply
                .exit_signal
                .is_some_and(|signal| !(1..=64).contains(&signal))
            || reply.exit_code.is_some() && reply.exit_signal.is_some()
            || reply.reason == "exited" && reply.exit_code.is_none() && reply.exit_signal.is_none()
            || !reply.tree_retired
            || reply.output_length > MAX_OUTPUT
            || !id(&reply.output_sha256, 64)
            || !reply.candidate_valid()
            || !matches!(
                reply.reason.as_str(),
                "exited"
                    | "cancelled"
                    | "deadline"
                    | "output_limit"
                    | "output_unavailable"
                    | "input_unavailable"
                    | "observation_unavailable"
                    | "unconfirmed"
            )
        {
            return Err(denied("agent execution identity invalid"));
        }
        let mut body = vec![0u8; reply.output_length];
        read_worker(&mut worker, &mut body, deadline, &mut queries)?;
        let mut bundle = vec![0u8; reply.bundle_length];
        read_worker(&mut worker, &mut bundle, deadline, &mut queries)?;
        if hash(&body) != reply.output_sha256
            || reply
                .bundle_sha256
                .as_deref()
                .is_some_and(|digest| hash(&bundle) != digest)
        {
            return Err(denied("agent execution output invalid"));
        }
        output.write_all(&(bytes.len() as u32).to_le_bytes())?;
        output.write_all(&bytes)?;
        output.write_all(&body)?;
        output.write_all(&bundle)?;
        output.flush()
    })();
    if result.is_err() {
        let _ = stop_agent(run, &directory);
    }
    result
}

fn clean_retiring(directory: &Path, profile: &str, digest: &str) -> io::Result<()> {
    let metadata = directory.symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.uid() != 0
        || !matches!(metadata.mode() & 0o7777, 0o555 | 0o700)
    {
        return Err(denied("retiring run directory invalid"));
    }
    verified_retiring_profile(directory, profile)?;
    let has_bundle = verified_bundle(directory, digest)?;
    fs::set_permissions(directory, fs::Permissions::from_mode(0o700))?;
    if has_bundle {
        fs::remove_file(directory.join("bundle"))?;
    }
    source_binding::cleanup(directory)?;
    task_receipts::cleanup(directory)?;
    match fs::remove_file(directory.join("task.manifest")) {
        Ok(()) => (),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    match fs::remove_file(directory.join("owner.role")) {
        Ok(()) => (),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    match fs::remove_file(directory.join("owner.tool_sha256")) {
        Ok(()) => (),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    match fs::remove_file(directory.join("owner.prompt_sha256")) {
        Ok(()) => (),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    match fs::remove_file(directory.join("owner.lifecycle")) {
        Ok(()) => (),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    match fs::remove_file(directory.join("owner.profile")) {
        Ok(()) => (),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    fs::remove_dir(directory)
}

fn verified_retiring_profile(directory: &Path, profile: &str) -> io::Result<()> {
    match verified_profile(directory, profile) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            if fs::read_dir(directory)?.next().is_none() {
                Ok(())
            } else {
                Err(denied("retiring run profile missing with retained state"))
            }
        }
        Err(error) => Err(error),
    }
}

// Stop execution and credential relay without deleting evidence. A failed or
// cancelled desktop attempt cannot claim its worker result was durably stored.
fn quiesce_run(run: &str, profile: &str, digest: &str) -> io::Result<()> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    read_role(&source)?;
    source_binding::verify(&source)?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("quiesced run source missing"));
    }
    let agent_stopped = match preparation_run(&preparations()?, run)? {
        Some(directory) => stop_agent(run, &directory),
        None => Ok(()),
    };
    let relay_stopped = stop_relay(run);
    agent_stopped?;
    relay_stopped?;
    for path in [
        PathBuf::from(format!("/run/soda-factory/agents/{run}.sock")),
        Path::new("/run/soda-factory-agent").join(run),
    ] {
        match path.symlink_metadata() {
            Err(error) if error.kind() == io::ErrorKind::NotFound => (),
            Ok(_) => return Err(denied("quiesced agent boundary remains")),
            Err(error) => return Err(error),
        }
    }
    Ok(())
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum RetirementMode {
    Any,
    Unlaunched,
    Launched,
    Pending,
}

fn retire(run: &str, profile: &str, digest: &str, mode: RetirementMode) -> io::Result<bool> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let preparations = preparations()?;
    let retiring = runs.join(format!(".retiring-{run}-{profile}-{digest}"));
    for entry in fs::read_dir(&runs)? {
        let entry = entry?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if retiring_run(&name) == Some(run) {
            // A partial prior retirement no longer has a preparation marker.
            // Setup cleanup cannot prove that this run was never launched.
            if mode == RetirementMode::Unlaunched {
                return Err(denied("unlaunched retirement state unconfirmed"));
            }
            if entry.path() != retiring {
                return Err(denied("run retirement digest differs"));
            }
            verified_retiring_profile(&retiring, profile)?;
            match runs.join(run).symlink_metadata() {
                Ok(_) => return Err(denied("run retirement state conflicts")),
                Err(error) if error.kind() == io::ErrorKind::NotFound => (),
                Err(error) => return Err(error),
            }
            if preparation_run(&preparations, run)?.is_some() {
                return Err(denied("retiring run has preparation state"));
            }
            stop_relay(run)?;
            clean_retiring(&retiring, profile, digest)?;
            File::open(&runs)?.sync_all()?;
            return Ok(true);
        }
    }
    if mode == RetirementMode::Pending {
        return Ok(false);
    }
    let directory = runs.join(run);
    let metadata = match directory.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            if preparation_run(&preparations, run)?.is_some() {
                return Err(denied("orphan preparation cannot retire"));
            }
            if mode == RetirementMode::Unlaunched {
                return Ok(false);
            }
            stop_relay(run)?;
            return Ok(false);
        }
        Err(error) => return Err(error),
    };
    if !metadata.is_dir() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o555 {
        return Err(denied("staged run directory invalid"));
    }
    verified_profile(&directory, profile)?;
    read_role(&directory)?;
    source_binding::verify(&directory)?;
    if !verified_bundle(&directory, digest)? {
        return Err(denied("staged bundle missing"));
    }
    if mode == RetirementMode::Unlaunched {
        if run_lifecycle(&directory)? != Some(false) || launched_run(run)? {
            return Err(denied(
                "launched or legacy run requires outcome reconciliation",
            ));
        }
    } else if mode == RetirementMode::Launched && !launched_run(run)? {
        return Err(denied("unlaunched run has no recovered outcome"));
    }
    clean_preparation(&preparations, run)?;
    fs::rename(directory, &retiring)?;
    File::open(&runs)?.sync_all()?;
    clean_retiring(&retiring, profile, digest)?;
    File::open(&runs)?.sync_all()?;
    Ok(true)
}

fn lock(runs: &Path) -> io::Result<File> {
    let path = runs.join(".lock");
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let metadata = file.metadata()?;
    if !metadata.is_file() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o600 {
        return Err(denied("broker lock invalid"));
    }
    if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX) } != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(file)
}

// The existing immutable run directory is also its operation lock, so no lock
// files outlive retirement. A waiter must never act on a replacement directory
// after the original was renamed/removed while it waited.
fn lock_run(runs: &Path, run: &str) -> io::Result<(File, Option<File>)> {
    if !id(run, 32) {
        return Err(denied("broker run lock selector invalid"));
    }
    let global = lock(runs)?;
    let path = runs.join(run);
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(&path)
    {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok((global, None)),
        Err(error) => return Err(error),
    };
    let identity = file.metadata()?;
    if identity.uid() != 0 {
        return Err(denied("broker run lock owner invalid"));
    }
    if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0 {
        return Ok((global, Some(file)));
    }
    if io::Error::last_os_error().kind() != io::ErrorKind::WouldBlock {
        return Err(io::Error::last_os_error());
    }
    drop(global);
    loop {
        if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX) } == 0 {
            break;
        }
        let error = io::Error::last_os_error();
        if error.kind() != io::ErrorKind::Interrupted {
            return Err(error);
        }
    }
    let global = lock(runs)?;
    let current = path.symlink_metadata()?;
    if current.dev() != identity.dev() || current.ino() != identity.ino() {
        return Err(denied("broker run changed while waiting"));
    }
    Ok((global, Some(file)))
}

fn hash(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn retiring_identity(name: &str) -> Option<(&str, &str, &str)> {
    let (run, rest) = name.strip_prefix(".retiring-")?.split_once('-')?;
    let (profile, digest) = rest.split_once('-')?;
    (id(run, 32) && id(profile, 32) && id(digest, 64)).then_some((run, profile, digest))
}

fn retiring_run(name: &str) -> Option<&str> {
    retiring_identity(name).map(|(run, _, _)| run)
}

fn run_lifecycle(directory: &Path) -> io::Result<Option<bool>> {
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join("owner.lifecycle"))
    {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || !matches!(metadata.mode() & 0o7777, 0o444 | 0o600)
        || metadata.len() != 2
    {
        return Err(denied("broker run lifecycle declaration invalid"));
    }
    let mut bytes = [0u8; 2];
    (&file).read_exact(&mut bytes)?;
    if metadata.mode() & 0o7777 == 0o600 {
        // A torn transition remains launched even if its contents are not
        // complete. The worker cannot receive launch bytes before this sync.
        return Ok(Some(true));
    }
    if !matches!(bytes, [b'2' | b'3', b'\n']) {
        return Err(denied("broker run lifecycle version invalid"));
    }
    Ok(Some(bytes == *b"3\n"))
}

fn mark_root_launch(run: &str, profile: &str, digest: &str) -> io::Result<()> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    let directory = runs.join(run);
    private_dir(&directory, 0o555)?;
    verified_profile(&directory, profile)?;
    if !verified_bundle(&directory, digest)? || run_lifecycle(&directory)? != Some(false) {
        return Err(denied("broker launch fence unavailable"));
    }
    let path = directory.join("owner.lifecycle");
    let marker = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(&path)?;
    marker.set_permissions(fs::Permissions::from_mode(0o600))?;
    marker.sync_all()?;
    let mut marker = OpenOptions::new()
        .write(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(&path)?;
    marker.write_all(b"3\n")?;
    marker.sync_all()?;
    marker.set_permissions(fs::Permissions::from_mode(0o444))?;
    marker.sync_all()
}

// Return one exact selector at a time to stay inside the fixed broker frame
// bound. The desktop supervisor holds its profile lock while consuming these;
// the root broker validates every selector from its own private declarations.
fn launched_run(run: &str) -> io::Result<bool> {
    let root = state()?.join(run);
    if run_lifecycle(&root)? == Some(true) {
        return Ok(true);
    }
    let Some(directory) = preparation_run(&preparations()?, run)? else {
        return Ok(false);
    };
    match directory.join("agent.launch").symlink_metadata() {
        Ok(metadata)
            if metadata.is_file()
                && metadata.uid() == 0
                && matches!(metadata.mode() & 0o7777, 0o400 | 0o444) =>
        {
            // The marker is created before the worker receives launch bytes.
            // Even an incomplete marker means the outcome is unknown to Go.
            Ok(true)
        }
        Ok(_) => Err(denied("agent launch marker invalid")),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(false),
        Err(error) => Err(error),
    }
}

fn next_profile_run(profile: &str) -> io::Result<Option<(String, String, bool, bool)>> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    for entry in fs::read_dir(&runs)? {
        let entry = entry?;
        let name = entry
            .file_name()
            .into_string()
            .map_err(|_| denied("broker run entry name invalid"))?;
        let path = entry.path();
        if name == ".lock" {
            continue;
        }
        if name.len() == 39
            && name.starts_with('.')
            && name.ends_with(".stage")
            && id(&name[1..33], 32)
        {
            let metadata = path.symlink_metadata()?;
            if !metadata.is_dir()
                || metadata.uid() != 0
                || !matches!(metadata.mode() & 0o7777, 0o700 | 0o555)
            {
                return Err(denied("partial broker staging invalid"));
            }
            fs::set_permissions(&path, fs::Permissions::from_mode(0o700))?;
            fs::remove_dir_all(path)?;
            File::open(&runs)?.sync_all()?;
            continue;
        }
        if let Some((run, stored_profile, digest)) = retiring_identity(&name) {
            if stored_profile != profile {
                continue;
            }
            let metadata = path.symlink_metadata()?;
            if !metadata.is_dir()
                || metadata.uid() != 0
                || !matches!(metadata.mode() & 0o7777, 0o555 | 0o700)
            {
                return Err(denied("retiring run directory invalid"));
            }
            verified_retiring_profile(&path, profile)?;
            if staged_digest(&path)?.is_some_and(|actual| actual != digest) {
                return Err(denied("retiring bundle digest mismatch"));
            }
            return Ok(Some((run.to_owned(), digest.to_owned(), false, true)));
        }
        if id(&name, 32) {
            private_dir(&path, 0o555)?;
            if read_profile(&path)? != profile {
                continue;
            }
            read_role(&path)?;
            source_binding::verify(&path)?;
            let digest =
                staged_digest(&path)?.ok_or_else(|| denied("staged run bundle missing"))?;
            let launched = launched_run(&name)?;
            return Ok(Some((name, digest, launched, false)));
        }
        return Err(denied("broker run entry invalid"));
    }
    Ok(None)
}

// Check after acquiring the shared staging lock: cancelled queued preparation
// work must not appear after a later exact retirement observed its absence.
fn preparation_caller_current() -> io::Result<()> {
    let mut descriptor = libc::pollfd {
        fd: 0,
        events: 0,
        revents: 0,
    };
    if unsafe { libc::poll(&mut descriptor, 1, 0) } < 0 {
        return Err(io::Error::last_os_error());
    }
    if descriptor.revents & (libc::POLLHUP | libc::POLLERR | libc::POLLNVAL) != 0 {
        return Err(denied("native preparation caller retired"));
    }
    Ok(())
}
fn stage(
    run: &str,
    profile: &str,
    role: &str,
    binding: source_binding::Binding<'_>,
    bytes: &[u8],
    digest: &str,
) -> io::Result<()> {
    let runs = state()?;
    let (_lock, _run_lock) = lock_run(&runs, run)?;
    if matches!(
        &binding,
        source_binding::Binding::Preparation(_)
            | source_binding::Binding::Agent { setup: Some(_), .. }
    ) {
        preparation_caller_current()?;
    }
    for entry in fs::read_dir(&runs)? {
        let name = entry?.file_name();
        if retiring_run(&name.to_string_lossy()) == Some(run) {
            return Err(denied("run retirement pending"));
        }
    }
    let final_dir = runs.join(run);
    if final_dir.exists() {
        private_dir(&final_dir, 0o555)?;
        if run_lifecycle(&final_dir)? != Some(false) {
            return Err(denied("run already launched or legacy staged"));
        }
        verified_profile(&final_dir, profile)?;
        verified_role(&final_dir, role)?;
        binding.verify(&final_dir)?;
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(final_dir.join("bundle"))?;
        let metadata = file.metadata()?;
        if !metadata.is_file()
            || metadata.uid() != 0
            || metadata.mode() & 0o7777 != 0o444
            || metadata.len() != bytes.len() as u64
        {
            return Err(denied("staged bundle metadata invalid"));
        }
        let mut bundle = Vec::with_capacity(bytes.len());
        file.take(bytes.len() as u64 + 1).read_to_end(&mut bundle)?;
        if bundle.len() == bytes.len() && hash(&bundle) == digest {
            return Ok(());
        }
        return Err(denied("run identity already staged with different bytes"));
    }
    if preparation_run(&preparations()?, run)?.is_some() {
        return Err(denied("orphan preparation prevents staging"));
    }
    let mut count = 0;
    for entry in fs::read_dir(&runs)? {
        let entry = entry?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if id(&name, 32) {
            count += 1;
        } else if let Some(retiring) = retiring_run(&name) {
            if retiring == run {
                return Err(denied("run retirement pending"));
            }
            count += 1;
        } else if name.len() == 39
            && name.starts_with('.')
            && name.ends_with(".stage")
            && id(&name[1..33], 32)
        {
            fs::remove_dir_all(entry.path())?;
        }
    }
    if count >= MAX_RUNS {
        return Err(denied("broker run staging capacity reached"));
    }
    let temp = runs.join(format!(".{run}.stage"));
    DirBuilder::new().mode(0o700).create(&temp)?;
    let result = (|| {
        let bundle_path = temp.join("bundle");
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(bundle_path)?;
        file.write_all(bytes)?;
        file.sync_all()?;
        file.set_permissions(fs::Permissions::from_mode(0o444))?;
        let mut declaration = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o444)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(temp.join("owner.profile"))?;
        declaration.write_all(format!("{profile}\n").as_bytes())?;
        declaration.sync_all()?;
        declaration.set_permissions(fs::Permissions::from_mode(0o444))?;
        let mut role_declaration = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o444)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(temp.join("owner.role"))?;
        role_declaration.write_all(format!("{role}\n").as_bytes())?;
        role_declaration.sync_all()?;
        role_declaration.set_permissions(fs::Permissions::from_mode(0o444))?;
        binding.write(&temp)?;
        let mut lifecycle = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o444)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(temp.join("owner.lifecycle"))?;
        lifecycle.write_all(b"2\n")?;
        lifecycle.sync_all()?;
        lifecycle.set_permissions(fs::Permissions::from_mode(0o444))?;
        fs::set_permissions(&temp, fs::Permissions::from_mode(0o555))?;
        File::open(&temp)?.sync_all()?;
        fs::rename(&temp, &final_dir)?;
        File::open(&runs)?.sync_all()?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::set_permissions(&temp, fs::Permissions::from_mode(0o700));
        let _ = fs::remove_dir_all(&temp);
    }
    result
}

fn serve() -> io::Result<()> {
    let desktop_uid = socket(0)?;
    client_read_timeout()?;
    let mut input = io::stdin().lock();
    let mut header = [0u8; 4];
    let declaration_deadline = Instant::now() + Duration::from_secs(5);
    read_client_exact(&mut input, &mut header, declaration_deadline)?;
    let length = u32::from_le_bytes(header) as usize;
    if length == 0 || length > MAX_REQUEST {
        return Err(denied("broker request bound"));
    }
    let mut request = vec![0; length];
    read_client_exact(&mut input, &mut request, declaration_deadline)?;
    let request: Request =
        serde_json::from_slice(&request).map_err(|_| denied("broker request shape invalid"))?;
    let response = match request {
        Request::ReadTaskReceipt {
            version,
            run,
            profile,
            sha256,
            manifest_digest,
            invocation,
            command_length,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&manifest_digest, 64)
                || invocation.is_empty()
                || invocation.len() > 128
                || invocation.chars().any(char::is_control)
                || !(1..=soda_execution_protocol::workspace::MAX_COMMAND).contains(&command_length)
            {
                return Err(denied("task receipt declaration invalid"));
            }
            let mut command = vec![0; command_length];
            read_client_exact(
                &mut input,
                &mut command,
                Instant::now() + Duration::from_secs(3),
            )?;
            let command: soda_execution_protocol::workspace::Command =
                serde_json::from_slice(&command)
                    .map_err(|_| denied("task receipt command invalid"))?;
            let runs = state()?;
            let (_lock, _run_lock) = lock_run(&runs, &run)?;
            let directory = runs.join(&run);
            private_dir(&directory, 0o555)?;
            verified_profile(&directory, &profile)?;
            if !verified_bundle(&directory, &sha256)? {
                return Err(denied("task receipt source missing"));
            }
            task_receipts::verify(&directory, &manifest_digest, &invocation)?;
            task_receipts::read(&directory, &command)?
                .ok_or_else(|| denied("task receipt pending or missing"))?
        }
        Request::TaskWorkspace {
            version,
            run,
            profile,
            sha256,
            candidate,
            tool_sha256,
            prompt_sha256,
            manifest_length,
            manifest_digest,
            invocation,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
                || !id(&tool_sha256, 64)
                || !id(&prompt_sha256, 64)
                || !id(&manifest_digest, 64)
                || !(1..=soda_execution_protocol::MAX_TASK_MANIFEST).contains(&manifest_length)
                || invocation.is_empty()
                || invocation.len() > 128
                || invocation.chars().any(char::is_control)
            {
                return Err(denied("task workspace declaration invalid"));
            }
            let mut manifest = vec![0; manifest_length];
            read_client_exact(
                &mut input,
                &mut manifest,
                Instant::now() + Duration::from_secs(30),
            )?;
            if hash(&manifest) != manifest_digest {
                return Err(denied("task manifest digest differs"));
            }
            return task_workspace::serve(
                &mut input,
                &mut io::stdout().lock(),
                &run,
                &profile,
                &sha256,
                &candidate,
                &tool_sha256,
                &prompt_sha256,
                &manifest_digest,
                &manifest,
                &invocation,
            );
        }
        Request::StagePreparation {
            version,
            run,
            profile,
            role,
            length,
            sha256,
            setup_length,
            setup_sha256,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !matches!(role.as_str(), "coder" | "reviewer")
                || !(1..=MAX_BUNDLE).contains(&length)
                || !id(&sha256, 64)
                || !id(&setup_sha256, 64)
                || !(1..=soda_execution_protocol::MAX_SETUP_FRAME).contains(&setup_length)
            {
                return Err(denied("source preparation declaration invalid"));
            }
            let mut bundle = vec![0; length];
            let mut setup = vec![0; setup_length];
            let deadline = Instant::now() + Duration::from_secs(30);
            read_client_exact(&mut input, &mut bundle, deadline)?;
            read_client_exact(&mut input, &mut setup, deadline)?;
            let approved: soda_execution_protocol::ApprovedFiles =
                serde_json::from_slice(&setup)
                    .map_err(|_| denied("approved setup shape invalid"))?;
            if hash(&bundle) != sha256 || !approved.valid() || approved.digest() != setup_sha256 {
                return Err(denied("approved source or setup differs"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            stage(
                &run,
                &profile,
                &role,
                source_binding::Binding::Preparation(&approved),
                &bundle,
                &sha256,
            )?;
            serde_json::to_vec(
                &serde_json::json!({"kind":"preparation_staged","version":1,"run":run,"profile":profile,"sha256":sha256,"setup_sha256":setup_sha256}),
            )?
        }
        Request::CompleteCandidate {
            version,
            run,
            profile,
            sha256,
            base,
            candidate,
            length,
            delta_sha256,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&base, 40)
                || !id(&candidate, 40)
                || !id(&delta_sha256, 64)
                || !(1..=MAX_BUNDLE).contains(&length)
            {
                return Err(denied("candidate import selector invalid"));
            }
            let mut delta = vec![0; length];
            read_client_exact(
                &mut input,
                &mut delta,
                Instant::now() + Duration::from_secs(30),
            )?;
            if hash(&delta) != delta_sha256 {
                return Err(denied("candidate input differs"));
            }
            serde_json::to_vec(&source_binding::read(
                &run,
                &profile,
                &sha256,
                &base,
                &soda_execution_protocol::PreparedSourceOperation::Candidate {
                    base: base.clone(),
                    candidate,
                    delta_length: length,
                    delta_sha256,
                },
                &delta,
                desktop_uid,
            )?)?
        }
        Request::ReadContext {
            version,
            run,
            profile,
            sha256,
            candidate,
            length,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
                || !(1..=soda_execution_protocol::MAX_CONTEXT_REQUEST).contains(&length)
            {
                return Err(denied("context read selector invalid"));
            }
            let mut bytes = vec![0; length];
            read_client_exact(
                &mut input,
                &mut bytes,
                Instant::now() + Duration::from_secs(5),
            )?;
            let selection: soda_execution_protocol::ContextSelection =
                serde_json::from_slice(&bytes).map_err(|_| denied("context selection invalid"))?;
            if !selection.valid() || selection.source_commit != candidate {
                return Err(denied("context source differs"));
            }
            serde_json::to_vec(&source_binding::read(
                &run,
                &profile,
                &sha256,
                &candidate,
                &soda_execution_protocol::PreparedSourceOperation::Context { selection },
                &[],
                desktop_uid,
            )?)?
        }
        Request::ReadSetup {
            version,
            run,
            profile,
            sha256,
            candidate,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
            {
                return Err(denied("setup read selector invalid"));
            }
            serde_json::to_vec(&source_binding::receipt(
                &run,
                &profile,
                &sha256,
                &candidate,
                desktop_uid,
            )?)?
        }
        Request::StageBundle {
            version,
            run,
            profile,
            role,
            tool_sha256,
            prompt_sha256,
            length,
            sha256,
            preparation,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !matches!(role.as_str(), "reviewer" | "coder")
                || !id(&tool_sha256, 64)
                || !id(&prompt_sha256, 64)
                || !id(&sha256, 64)
                || !(1..=MAX_BUNDLE).contains(&length)
            {
                return Err(denied("broker bundle declaration invalid"));
            }
            let mut bundle = vec![0; length];
            read_client_exact(
                &mut input,
                &mut bundle,
                Instant::now() + Duration::from_secs(30),
            )?;
            if hash(&bundle) != sha256 {
                return Err(denied("broker bundle digest mismatch"));
            }
            let setup = preparation
                .as_ref()
                .map(|source| {
                    source_binding::agent_setup(source, &profile, &sha256, &role, desktop_uid)
                })
                .transpose()?;
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            stage(
                &run,
                &profile,
                &role,
                source_binding::Binding::Agent {
                    tool: &tool_sha256,
                    prompt: &prompt_sha256,
                    setup: setup.as_ref(),
                },
                &bundle,
                &sha256,
            )?;
            serde_json::to_vec(&Staged {
                kind: "staged",
                version: 1,
                run: &run,
                profile: &profile,
                role: &role,
                tool_sha256: &tool_sha256,
                prompt_sha256: &prompt_sha256,
                sha256: &sha256,
            })?
        }
        Request::RetireBundle {
            version,
            run,
            profile,
            sha256,
        } => {
            if version != 1 || !id(&run, 32) || !id(&profile, 32) || !id(&sha256, 64) {
                return Err(denied("broker retirement declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let removed = retire(&run, &profile, &sha256, RetirementMode::Any)?;
            serde_json::to_vec(&Retired {
                kind: "retired",
                version: 1,
                run: &run,
                profile: &profile,
                removed,
            })?
        }
        Request::RetireUnlaunched {
            version,
            run,
            profile,
            sha256,
        } => {
            if version != 1 || !id(&run, 32) || !id(&profile, 32) || !id(&sha256, 64) {
                return Err(denied("broker unlaunched retirement declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let removed = retire(&run, &profile, &sha256, RetirementMode::Unlaunched)?;
            serde_json::to_vec(&Retired {
                kind: "retired",
                version: 1,
                run: &run,
                profile: &profile,
                removed,
            })?
        }
        Request::RetireLaunched {
            version,
            run,
            profile,
            sha256,
        } => {
            if version != 1 || !id(&run, 32) || !id(&profile, 32) || !id(&sha256, 64) {
                return Err(denied("broker launched retirement declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let removed = retire(&run, &profile, &sha256, RetirementMode::Launched)?;
            serde_json::to_vec(&Retired {
                kind: "retired",
                version: 1,
                run: &run,
                profile: &profile,
                removed,
            })?
        }
        Request::FinishRetiring {
            version,
            run,
            profile,
            sha256,
        } => {
            if version != 1 || !id(&run, 32) || !id(&profile, 32) || !id(&sha256, 64) {
                return Err(denied("broker pending retirement declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let removed = retire(&run, &profile, &sha256, RetirementMode::Pending)?;
            serde_json::to_vec(&Retired {
                kind: "retired",
                version: 1,
                run: &run,
                profile: &profile,
                removed,
            })?
        }
        Request::QuiesceRun {
            version,
            run,
            profile,
            sha256,
        } => {
            if version != 1 || !id(&run, 32) || !id(&profile, 32) || !id(&sha256, 64) {
                return Err(denied("broker quiescence declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            quiesce_run(&run, &profile, &sha256)?;
            serde_json::to_vec(&Quiesced {
                kind: "quiesced",
                version: 1,
                run: &run,
                profile: &profile,
                sha256: &sha256,
            })?
        }
        Request::NextProfileRun { version, profile } => {
            if version != 2 || !id(&profile, 32) {
                return Err(denied("broker profile inspection declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let next = next_profile_run(&profile)?;
            serde_json::to_vec(&ProfileRun {
                kind: "profile_run",
                version: 2,
                profile: &profile,
                run: next.as_ref().map(|(run, _, _, _)| run.as_str()),
                sha256: next.as_ref().map(|(_, sha256, _, _)| sha256.as_str()),
                launched: next.as_ref().is_some_and(|(_, _, launched, _)| *launched),
                retiring: next.as_ref().is_some_and(|(_, _, _, retiring)| *retiring),
            })?
        }
        Request::PrepareReviewer {
            version,
            run,
            profile,
            sha256,
            candidate,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
            {
                return Err(denied("broker reviewer preparation declaration invalid"));
            }
            prepare(&run, &profile, &sha256, &candidate, desktop_uid)?;
            serde_json::to_vec(&Prepared {
                kind: "prepared",
                version: 1,
                run: &run,
                profile: &profile,
                sha256: &sha256,
                candidate: &candidate,
            })?
        }
        Request::AttestReviewer {
            version,
            run,
            profile,
            sha256,
            candidate,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
            {
                return Err(denied("broker reviewer attestation declaration invalid"));
            }
            let worker_uid = attest_reviewer(&run, &profile, &sha256, &candidate, desktop_uid)?;
            serde_json::to_vec(&ReviewerAttested {
                kind: "reviewer_attested",
                version: 1,
                run: &run,
                profile: &profile,
                candidate: &candidate,
                worker_uid,
                readonly_worktree: true,
                execution_available: false,
            })?
        }
        Request::AttestSetupNetwork {
            version,
            run,
            profile,
            sha256,
            setup_sha256,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&setup_sha256, 64)
            {
                return Err(denied("setup network declaration invalid"));
            }
            let relay_uid =
                attest_setup_network(&run, &profile, &sha256, &setup_sha256, desktop_uid)?;
            serde_json::to_vec(&SetupNetworkAttested {
                kind: "setup_network_attested",
                version: 1,
                run: &run,
                profile: &profile,
                sha256: &sha256,
                setup_sha256: &setup_sha256,
                relay_uid,
            })?
        }
        Request::AttestProviderRelay {
            version,
            run,
            profile,
            sha256,
            candidate,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
            {
                return Err(denied("broker provider relay declaration invalid"));
            }
            let (relay_uid, role) =
                attest_provider_relay(&run, &profile, &sha256, &candidate, desktop_uid)?;
            serde_json::to_vec(&ProviderRelayAttested {
                kind: "provider_relay_attested",
                version: 1,
                run: &run,
                profile: &profile,
                role: &role,
                candidate: &candidate,
                relay_uid,
                execution_available: false,
            })?
        }
        Request::ArmAgent {
            version,
            run,
            profile,
            sha256,
            candidate,
            role,
            tool_sha256,
            prompt_sha256,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
                || !matches!(role.as_str(), "reviewer" | "coder")
                || !id(&tool_sha256, 64)
                || !id(&prompt_sha256, 64)
            {
                return Err(denied("broker agent declaration invalid"));
            }
            let (tool_sha256, prompt_sha256) = arm_agent(
                &run,
                &profile,
                &sha256,
                &candidate,
                &role,
                &tool_sha256,
                &prompt_sha256,
                true,
            )?;
            serde_json::to_vec(&AgentArmed {
                kind: "agent_armed",
                version: 1,
                run: &run,
                profile: &profile,
                candidate: &candidate,
                role: &role,
                tool_sha256: &tool_sha256,
                prompt_sha256: &prompt_sha256,
                execution_available: false,
            })?
        }
        Request::AttestAgent {
            version,
            run,
            profile,
            sha256,
            candidate,
            role,
            tool_sha256,
            prompt_sha256,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
                || !matches!(role.as_str(), "reviewer" | "coder")
                || !id(&tool_sha256, 64)
                || !id(&prompt_sha256, 64)
            {
                return Err(denied("broker agent attestation declaration invalid"));
            }
            let worker_uid = attest_agent(
                &run,
                &profile,
                &sha256,
                &candidate,
                &role,
                &tool_sha256,
                &prompt_sha256,
                desktop_uid,
            )?;
            serde_json::to_vec(&AgentAttested {
                kind: "agent_attested",
                version: 1,
                run: &run,
                profile: &profile,
                candidate: &candidate,
                role: &role,
                tool_sha256: &tool_sha256,
                prompt_sha256: &prompt_sha256,
                worker_uid,
                peer_uid: 0,
                readonly_source: true,
                coder_workspace: role == "coder",
                execution_available: false,
            })?
        }
        Request::ExecuteAgent {
            version,
            run,
            profile,
            sha256,
            candidate,
            role,
            tool_sha256,
            prompt_sha256,
            executable_length,
            prompt_length,
            placeholder,
            duration_ms,
            harness_profile,
        } => {
            if !harness_profile.valid()
                || version != 3
                || !id(&run, 32)
                || !id(&profile, 32)
                || !id(&sha256, 64)
                || !id(&candidate, 40)
                || !matches!(role.as_str(), "reviewer" | "coder")
                || !id(&tool_sha256, 64)
                || !id(&prompt_sha256, 64)
                || !(4..=MAX_EXECUTABLE).contains(&executable_length)
                || !(1..=MAX_PROMPT).contains(&prompt_length)
                || !id(&placeholder, 64)
                || !(1..=4 * 60 * 60 * 1000).contains(&duration_ms)
            {
                return Err(denied("broker execution declaration invalid"));
            }
            let worker_request = AgentExecutionRequest {
                kind: "execute_agent",
                version: 3,
                run: &run,
                profile: &profile,
                candidate: &candidate,
                tool_sha256: &tool_sha256,
                prompt_sha256: &prompt_sha256,
                executable_length,
                prompt_length,
                placeholder: &placeholder,
                duration_ms,
                harness_profile: &harness_profile,
            };
            return execute_agent(
                &mut input,
                &mut io::stdout().lock(),
                &worker_request,
                &sha256,
                &role,
                desktop_uid,
            );
        }
    };
    let mut output = io::stdout().lock();
    output.write_all(&(response.len() as u32).to_le_bytes())?;
    output.write_all(&response)?;
    output.flush()
}

fn main() {
    if let Err(error) = serve() {
        eprintln!("Factory broker refused request: {error}");
        std::process::exit(1);
    }
}
