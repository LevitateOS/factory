#![cfg(target_os = "linux")]
//! Stages bounded, credential-free Git bundle bytes and asks fixed unprivileged
//! units to prepare reviewer checkouts. This is not agent execution admission.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, RawFd};
use std::os::unix::fs::{DirBuilderExt, FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

const SOCKET: &str = "/run/soda-factory/broker.sock";
const STATE: &str = "/run/soda-factory-broker";
const MAX_REQUEST: usize = 768;
const MAX_BUNDLE: usize = 512 * 1024;
const MAX_PROMPT: usize = 320 * 1024;
const MAX_EXECUTABLE: u64 = 512 * 1024 * 1024;
const MAX_OUTPUT: usize = 16 * 1024 * 1024;
const MAX_AGENT_REPLY: usize = 1024;
const MAX_RUNS: usize = 64;
// Keep this reservation aligned with preparer@.service MemoryMax.
const MAX_PREPARATION_CHARGE: u64 = 128 * 1024 * 1024;
const MIN_RUNTIME_RESERVE: u64 = 256 * 1024 * 1024;

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Request {
    StageBundle {
        version: u8,
        run: String,
        profile: String,
        role: String,
        tool_sha256: String,
        prompt_sha256: String,
        length: usize,
        sha256: String,
    },
    RetireBundle {
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
struct ProfileRun<'a> {
    kind: &'static str,
    version: u8,
    profile: &'a str,
    run: Option<&'a str>,
    sha256: Option<&'a str>,
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
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct AgentExecutionReply {
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
    invocation: String,
    system_instance: String,
    start_identity: String,
    namespace_pid: u32,
    started_unix_ms: u64,
    reason: String,
    exit_code: Option<i32>,
    exit_signal: Option<i32>,
    tree_retired: bool,
    output_complete: bool,
    output_length: usize,
    output_sha256: String,
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

fn prepare(run: &str, profile: &str, digest: &str, candidate: &str) -> io::Result<()> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("reviewer source bundle missing"));
    }
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
            return Ok(());
        }
        clean_preparation(&preparations, run)?;
    }
    runtime_capacity(MAX_PREPARATION_CHARGE)?;
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
    let result = (|| {
        manager("start", "preparer", run)?;
        prepared_checkout(&directory)?;
        private_dir(&output, 0o777)?;
        runtime_capacity(0)?;
        fs::set_permissions(&output, fs::Permissions::from_mode(0o555))?;
        File::open(&directory)?.sync_all()
    })();
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
    let _lock = lock(&runs)?;
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
    manager_unit("start", &socket_unit)?;
    let socket_path = directory.join("reviewer.sock");
    let metadata = socket_path.symlink_metadata()?;
    if !metadata.file_type().is_socket() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o600
    {
        return Err(denied("reviewer run socket invalid"));
    }
    let mut stream = UnixStream::connect(socket_path)?;
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
    stream.read_exact(&mut header)?;
    let length = u32::from_le_bytes(header) as usize;
    if length == 0 || length > 512 {
        return Err(denied("reviewer attestation bound"));
    }
    let mut bytes = vec![0; length];
    stream.read_exact(&mut bytes)?;
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
    let _lock = lock(&runs)?;
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
    if let Err(error) = manager("start", "provider-relay", run) {
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
    verified.map(|uid| (uid, role))
}

fn agent_manifest(
    role: &str,
    profile: &str,
    candidate: &str,
    digest: &str,
    tool: &str,
    prompt: &str,
) -> String {
    format!("FACTORY_ROLE={role}\nFACTORY_PROFILE={profile}\nFACTORY_CANDIDATE={candidate}\nFACTORY_BUNDLE_SHA256={digest}\nFACTORY_TOOL_SHA256={tool}\nFACTORY_PROMPT_SHA256={prompt}\n")
}

fn arm_agent(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    role: &str,
    expected_tool: &str,
    expected_prompt: &str,
) -> io::Result<(String, String)> {
    if !matches!(role, "reviewer" | "coder") {
        return Err(denied("agent role invalid"));
    }
    let runs = state()?;
    let _lock = lock(&runs)?;
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
        manager_unit("start", &format!("soda-factory-agent@{run}.socket"))?;
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
    )?;
    let socket = PathBuf::from(format!("/run/soda-factory/agents/{run}.sock"));
    let metadata = socket.symlink_metadata()?;
    if !metadata.file_type().is_socket() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o600
    {
        return Err(denied("agent socket identity invalid"));
    }
    let mut stream = UnixStream::connect(&socket)?;
    stream.set_read_timeout(Some(Duration::from_secs(60)))?;
    stream.set_write_timeout(Some(Duration::from_secs(60)))?;
    let mut ready = [0u8; 1];
    stream.read_exact(&mut ready)?;
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
    stream.read_exact(&mut header)?;
    let length = u32::from_le_bytes(header) as usize;
    if length == 0 || length > MAX_REQUEST {
        return Err(denied("agent attestation reply bound invalid"));
    }
    let mut bytes = vec![0; length];
    stream.read_exact(&mut bytes)?;
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

fn client_closed() -> io::Result<bool> {
    let mut byte = [0u8; 1];
    let received = unsafe {
        libc::recv(
            0,
            byte.as_mut_ptr().cast(),
            1,
            libc::MSG_PEEK | libc::MSG_DONTWAIT,
        )
    };
    if received >= 0 {
        return Ok(true);
    }
    let error = io::Error::last_os_error();
    if error.kind() == io::ErrorKind::WouldBlock {
        Ok(false)
    } else {
        Err(error)
    }
}

fn read_worker(stream: &mut UnixStream, bytes: &mut [u8], deadline: Instant) -> io::Result<()> {
    let mut offset = 0;
    stream.set_read_timeout(Some(Duration::from_millis(100)))?;
    while offset < bytes.len() {
        if client_closed()? || Instant::now() >= deadline {
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
    arm_agent(
        run,
        profile,
        bundle_sha256,
        candidate,
        role,
        tool_sha256,
        prompt_sha256,
    )?;
    let directory = PathBuf::from(STATE).join("preparations").join(run);
    let result = (|| {
        private_dir(&directory, 0o700)?;
        let path = PathBuf::from(format!("/run/soda-factory/agents/{run}.sock"));
        let mut worker = UnixStream::connect(&path)?;
        worker.set_read_timeout(Some(Duration::from_secs(60)))?;
        worker.set_write_timeout(Some(Duration::from_secs(120)))?;
        let mut ready = [0u8; 1];
        worker.read_exact(&mut ready)?;
        if ready != *b"R" {
            return Err(denied("agent execution readiness invalid"));
        }
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
        let bytes = serde_json::to_vec(request)?;
        if bytes.is_empty() || bytes.len() > 512 {
            return Err(denied("agent execution request bound invalid"));
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
        let mut header = [0u8; 4];
        read_worker(&mut worker, &mut header, deadline)?;
        let length = u32::from_le_bytes(header) as usize;
        if length == 0 || length > MAX_AGENT_REPLY {
            return Err(denied("agent execution reply bound invalid"));
        }
        let mut bytes = vec![0u8; length];
        read_worker(&mut worker, &mut bytes, deadline)?;
        let reply: AgentExecutionReply = serde_json::from_slice(&bytes)
            .map_err(|_| denied("agent execution reply shape invalid"))?;
        if reply.kind != "agent_executed"
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
            || !id(&reply.invocation, 32)
            || !boot_id(&reply.system_instance)
            || !matches!(reply.start_identity.parse::<u64>(), Ok(ticks) if ticks > 0)
            || reply.namespace_pid == 0
            || reply.started_unix_ms == 0
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
        read_worker(&mut worker, &mut body, deadline)?;
        if hash(&body) != reply.output_sha256 || client_closed()? {
            return Err(denied("agent execution output invalid"));
        }
        output.write_all(&(bytes.len() as u32).to_le_bytes())?;
        output.write_all(&bytes)?;
        output.write_all(&body)?;
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

fn retire(run: &str, profile: &str, digest: &str) -> io::Result<bool> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    let preparations = preparations()?;
    let retiring = runs.join(format!(".retiring-{run}-{profile}-{digest}"));
    for entry in fs::read_dir(&runs)? {
        let entry = entry?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if retiring_run(&name) == Some(run) {
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
    let directory = runs.join(run);
    let metadata = match directory.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            if preparation_run(&preparations, run)?.is_some() {
                return Err(denied("orphan preparation cannot retire"));
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
    read_digest(&directory, "owner.tool_sha256")?;
    read_digest(&directory, "owner.prompt_sha256")?;
    if !verified_bundle(&directory, digest)? {
        return Err(denied("staged bundle missing"));
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

// Return one exact selector at a time to stay inside the fixed 768-byte wire
// bound. The desktop supervisor holds its profile lock while consuming these;
// the root broker validates every selector from its own private declarations.
fn next_profile_run(profile: &str) -> io::Result<Option<(String, String)>> {
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
            return Ok(Some((run.to_owned(), digest.to_owned())));
        }
        if id(&name, 32) {
            private_dir(&path, 0o555)?;
            if read_profile(&path)? != profile {
                continue;
            }
            read_role(&path)?;
            read_digest(&path, "owner.tool_sha256")?;
            read_digest(&path, "owner.prompt_sha256")?;
            let digest =
                staged_digest(&path)?.ok_or_else(|| denied("staged run bundle missing"))?;
            return Ok(Some((name, digest)));
        }
        return Err(denied("broker run entry invalid"));
    }
    Ok(None)
}

fn stage(
    run: &str,
    profile: &str,
    role: &str,
    tool_sha256: &str,
    prompt_sha256: &str,
    bytes: &[u8],
    digest: &str,
) -> io::Result<()> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    for entry in fs::read_dir(&runs)? {
        let name = entry?.file_name();
        if retiring_run(&name.to_string_lossy()) == Some(run) {
            return Err(denied("run retirement pending"));
        }
    }
    let final_dir = runs.join(run);
    if final_dir.exists() {
        private_dir(&final_dir, 0o555)?;
        verified_profile(&final_dir, profile)?;
        verified_role(&final_dir, role)?;
        verified_digest(&final_dir, "owner.tool_sha256", tool_sha256)?;
        verified_digest(&final_dir, "owner.prompt_sha256", prompt_sha256)?;
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
        let mut tool_declaration = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o444)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(temp.join("owner.tool_sha256"))?;
        tool_declaration.write_all(format!("{tool_sha256}\n").as_bytes())?;
        tool_declaration.sync_all()?;
        tool_declaration.set_permissions(fs::Permissions::from_mode(0o444))?;
        let mut prompt_declaration = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o444)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(temp.join("owner.prompt_sha256"))?;
        prompt_declaration.write_all(format!("{prompt_sha256}\n").as_bytes())?;
        prompt_declaration.sync_all()?;
        prompt_declaration.set_permissions(fs::Permissions::from_mode(0o444))?;
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
        Request::StageBundle {
            version,
            run,
            profile,
            role,
            tool_sha256,
            prompt_sha256,
            length,
            sha256,
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
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            stage(
                &run,
                &profile,
                &role,
                &tool_sha256,
                &prompt_sha256,
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
            let removed = retire(&run, &profile, &sha256)?;
            serde_json::to_vec(&Retired {
                kind: "retired",
                version: 1,
                run: &run,
                profile: &profile,
                removed,
            })?
        }
        Request::NextProfileRun { version, profile } => {
            if version != 1 || !id(&profile, 32) {
                return Err(denied("broker profile inspection declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let next = next_profile_run(&profile)?;
            serde_json::to_vec(&ProfileRun {
                kind: "profile_run",
                version: 1,
                profile: &profile,
                run: next.as_ref().map(|(run, _)| run.as_str()),
                sha256: next.as_ref().map(|(_, sha256)| sha256.as_str()),
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
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            prepare(&run, &profile, &sha256, &candidate)?;
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
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
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
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
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
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let (tool_sha256, prompt_sha256) = arm_agent(
                &run,
                &profile,
                &sha256,
                &candidate,
                &role,
                &tool_sha256,
                &prompt_sha256,
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
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
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
        } => {
            if version != 1
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
                version: 1,
                run: &run,
                profile: &profile,
                candidate: &candidate,
                tool_sha256: &tool_sha256,
                prompt_sha256: &prompt_sha256,
                executable_length,
                prompt_length,
                placeholder: &placeholder,
                duration_ms,
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
