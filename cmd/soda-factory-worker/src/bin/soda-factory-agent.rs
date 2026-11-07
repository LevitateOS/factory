#![cfg(target_os = "linux")]
//! One exact-run isolated service boundary for attestation and a broker-consumed
//! agent process. Product admission and credential custody remain separate.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use soda_execution_protocol::{
    AgentExecution as Execution, AgentStarted as Started, MAX_EXECUTABLE, MAX_EXECUTION_REPLY,
    MAX_OUTPUT, MAX_PROMPT, MAX_START_REPLY,
};
use soda_native_process::{Executable, Invocation, Limits, Processes, StopReason};
use std::collections::BTreeMap;
use std::ffi::OsString;
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd, RawFd};
use std::os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant, UNIX_EPOCH};
#[path = "../agent_candidate.rs"]
mod agent_candidate;
#[path = "../agent_environment.rs"]
mod agent_environment;
#[path = "../agent_observation.rs"]
mod agent_observation;
#[path = "../agent_workspace.rs"]
mod agent_workspace;
use agent_candidate::{repository_identity, Candidate, RepositoryIdentity};

const SOURCE: &str = "/run/factory-source";
const WORKSPACE: &str = "/run/factory-agent-private/run/workspace";
const ROLE: &str = "/run/factory-role";
const PRIVATE: &str = "/run/factory-agent-private/run";
const PRIVATE_MOUNT: &str = "/run/factory-agent-private";
const PRIVATE_MAX_BYTES: u64 = 3 * 1024 * 1024 * 1024;
const PRIVATE_TMP: &str = "/run/factory-agent-private/run/tmp";
const PRIVATE_HOME: &str = "/run/factory-agent-private/run/home";
const CGROUP: &str = "/sys/fs/cgroup/factory";
const MAX_REQUEST: usize = soda_execution_protocol::MAX_AGENT_REQUEST;
const MAX_EXECUTION: Duration = Duration::from_secs(4 * 60 * 60);
const PROMPT: &str = "/run/factory-agent-private/run/agent.prompt";

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Request {
    TaskWorkspace {
        version: u8,
        run: String,
        profile: String,
        candidate: String,
        manifest_digest: String,
    },
    AttestAgent {
        version: u8,
        run: String,
        profile: String,
    },
    ExecuteAgent {
        version: u8,
        run: String,
        profile: String,
        candidate: String,
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
struct Attestation<'a> {
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

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn id(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn readonly(path: &Path) -> io::Result<bool> {
    let path = std::ffi::CString::new(path.as_os_str().as_encoded_bytes())
        .map_err(|_| refused("agent mount path invalid"))?;
    let mut stat = std::mem::MaybeUninit::<libc::statvfs>::uninit();
    if unsafe { libc::statvfs(path.as_ptr(), stat.as_mut_ptr()) } != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(unsafe { stat.assume_init() }.f_flag & libc::ST_RDONLY as u64 != 0)
}

fn close_on_exec(fd: RawFd) -> io::Result<()> {
    let flags = unsafe { libc::fcntl(fd, libc::F_GETFD) };
    if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFD, flags | libc::FD_CLOEXEC) } < 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

fn bounded_tmpfs(path: &str, max_bytes: u64) -> io::Result<()> {
    let mount = Path::new(path);
    let metadata = mount.symlink_metadata()?;
    let parent = mount
        .parent()
        .ok_or_else(|| refused("agent storage mount path invalid"))?
        .symlink_metadata()?;
    let path = std::ffi::CString::new(path).map_err(|_| refused("agent storage path invalid"))?;
    let mut filesystem = std::mem::MaybeUninit::<libc::statfs>::uninit();
    let mut capacity = std::mem::MaybeUninit::<libc::statvfs>::uninit();
    if !metadata.is_dir()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o1777
        || metadata.dev() == parent.dev()
        || unsafe { libc::statfs(path.as_ptr(), filesystem.as_mut_ptr()) } != 0
        || unsafe { filesystem.assume_init() }.f_type != libc::TMPFS_MAGIC
        || unsafe { libc::statvfs(path.as_ptr(), capacity.as_mut_ptr()) } != 0
    {
        return Err(refused("agent storage mount invalid"));
    }
    let capacity = unsafe { capacity.assume_init() };
    if capacity.f_blocks.saturating_mul(capacity.f_frsize) > max_bytes {
        return Err(refused("agent storage mount capacity invalid"));
    }
    Ok(())
}

fn inaccessible_storage(path: &str) -> io::Result<()> {
    let metadata = Path::new(path).symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0 {
        return Err(refused("agent scratch storage remains accessible"));
    }
    Ok(())
}

fn identity(run: &str, desktop_uid: u32) -> io::Result<u32> {
    let uid = unsafe { libc::geteuid() };
    if uid == 0
        || uid == desktop_uid
        || unsafe { libc::getuid() } != uid
        || unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1
        || std::env::var_os("DBUS_SESSION_BUS_ADDRESS").is_some()
        || std::env::var_os("XDG_RUNTIME_DIR").is_some()
    {
        return Err(refused("agent service identity invalid"));
    }
    let runtime = std::env::var_os("RUNTIME_DIRECTORY")
        .ok_or_else(|| refused("agent private runtime missing"))?;
    let runtime = Path::new(&runtime);
    if runtime.parent() != Some(Path::new("/run/soda-factory-agent"))
        || runtime.file_name() != Some(std::ffi::OsStr::new(run))
    {
        return Err(refused("agent private runtime path invalid"));
    }
    let hidden = Path::new("/run/soda-factory-agent");
    let hidden_metadata = hidden.symlink_metadata()?;
    if !hidden_metadata.is_dir()
        || hidden_metadata.uid() != 0
        || hidden_metadata.mode() & 0o7777 != 0o700
        || !matches!(
            runtime.symlink_metadata(),
            Err(ref error) if error.kind() == io::ErrorKind::PermissionDenied
        )
    {
        return Err(refused("agent host runtime path visible"));
    }
    bounded_tmpfs(PRIVATE_MOUNT, PRIVATE_MAX_BYTES)?;
    for path in ["/tmp", "/var/tmp", "/dev/shm", "/run/lock"] {
        inaccessible_storage(path)?;
    }
    fs::create_dir(PRIVATE)?;
    fs::create_dir(PRIVATE_TMP)?;
    fs::create_dir(PRIVATE_HOME)?;
    let metadata = Path::new(PRIVATE).symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != uid || metadata.mode() & 0o7777 != 0o700 {
        return Err(refused("agent private runtime identity invalid"));
    }
    Ok(uid)
}

fn process_owner(run: &str, worker_uid: u32) -> io::Result<Processes> {
    let value = fs::read_to_string("/proc/self/cgroup")?;
    let path = value
        .strip_prefix("0::")
        .and_then(|value| value.strip_suffix('\n'))
        .ok_or_else(|| refused("agent cgroup declaration invalid"))?;
    let child = Path::new(path);
    let parent = child
        .parent()
        .ok_or_else(|| refused("agent delegation parent missing"))?;
    let unit = format!("soda-factory-agent@{run}.service");
    if value.len() > 4096
        || path.contains('\n')
        || child.file_name() != Some(std::ffi::OsStr::new("init"))
        || parent.file_name() != Some(std::ffi::OsStr::new(&unit))
        || !child.is_absolute()
        || child.components().any(|component| {
            !matches!(
                component,
                std::path::Component::RootDir | std::path::Component::Normal(_)
            )
        })
    {
        return Err(refused("agent delegation cgroup invalid"));
    }
    if parent != Path::new(&format!("/system.slice/{unit}")) {
        return Err(refused("agent system cgroup identity invalid"));
    }
    let delegated = Path::new(CGROUP);
    let cgroup_root = std::ffi::CString::new("/sys/fs/cgroup").expect("fixed cgroup path");
    let mut stat = std::mem::MaybeUninit::<libc::statfs>::uninit();
    if unsafe { libc::statfs(cgroup_root.as_ptr(), stat.as_mut_ptr()) } != 0
        || unsafe { stat.assume_init() }.f_type != libc::TMPFS_MAGIC
    {
        return Err(refused("agent host cgroups visible"));
    }
    let metadata = delegated.symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != worker_uid {
        return Err(refused("agent delegation owner invalid"));
    }
    if !fs::read_to_string(delegated.join("cgroup.procs"))?
        .trim()
        .is_empty()
    {
        return Err(refused("agent delegation root populated"));
    }
    OpenOptions::new()
        .write(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(delegated.join("cgroup.subtree_control"))?
        .write_all(b"+cpu +memory +pids")?;
    Processes::open(Path::new(PRIVATE), delegated, run)
        .map_err(|_| refused("agent process scope unavailable"))
}

fn manifest(run: &str) -> io::Result<(String, String, String, String, String)> {
    let path = Path::new(ROLE);
    let metadata = path.symlink_metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || metadata.len() > 512
        || !readonly(path)?
    {
        return Err(refused("agent role mount invalid"));
    }
    let mut bytes = Vec::new();
    File::open(path)?.take(513).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() {
        return Err(refused("agent role changed during read"));
    }
    let value = std::str::from_utf8(&bytes).map_err(|_| refused("agent role encoding invalid"))?;
    let mut lines = value.split_terminator('\n');
    let role = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_ROLE="))
        .ok_or_else(|| refused("agent role missing"))?;
    let profile = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_PROFILE="))
        .ok_or_else(|| refused("agent profile missing"))?;
    let candidate = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_CANDIDATE="))
        .ok_or_else(|| refused("agent candidate missing"))?;
    let digest = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_BUNDLE_SHA256="))
        .ok_or_else(|| refused("agent digest missing"))?;
    let tool_sha256 = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_TOOL_SHA256="))
        .ok_or_else(|| refused("agent tool digest missing"))?;
    let prompt_sha256 = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_PROMPT_SHA256="))
        .ok_or_else(|| refused("agent prompt digest missing"))?;
    if !matches!(role, "reviewer" | "coder")
        || !id(profile, 32)
        || !id(candidate, 40)
        || !id(digest, 64)
        || !id(tool_sha256, 64)
        || !id(prompt_sha256, 64)
        || lines.next().is_some()
        || !value.ends_with('\n')
        || !id(run, 32)
    {
        return Err(refused("agent role manifest invalid"));
    }
    Ok((
        role.into(),
        profile.into(),
        candidate.into(),
        tool_sha256.into(),
        prompt_sha256.into(),
    ))
}

fn git(processes: &mut Processes, arguments: &[&str], duration: Duration) -> io::Result<Vec<u8>> {
    let executable = Executable::capture(Path::new("/usr/bin/git"))
        .map_err(|_| refused("agent Git executable unavailable"))?;
    let environment = BTreeMap::from([
        (OsString::from("TMPDIR"), OsString::from(PRIVATE_TMP)),
        (OsString::from("GIT_CONFIG_NOSYSTEM"), OsString::from("1")),
        (
            OsString::from("GIT_CONFIG_GLOBAL"),
            OsString::from("/dev/null"),
        ),
        (OsString::from("GIT_TERMINAL_PROMPT"), OsString::from("0")),
        (OsString::from("GIT_LFS_SKIP_SMUDGE"), OsString::from("1")),
        (OsString::from("GIT_ATTR_NOSYSTEM"), OsString::from("1")),
        (
            OsString::from("GIT_NO_REPLACE_OBJECTS"),
            OsString::from("1"),
        ),
    ]);
    let arguments = [
        "-c",
        "core.autocrlf=false",
        "-c",
        "core.eol=lf",
        "-c",
        "core.attributesFile=/dev/null",
    ]
    .into_iter()
    .chain(arguments.iter().copied())
    .map(OsString::from)
    .collect();
    let process = processes
        .launch(Invocation {
            executable: &executable,
            directory: Path::new(PRIVATE),
            arguments,
            environment,
            limits: Limits {
                duration,
                output_bytes: 4096,
                memory_bytes: 256 * 1024 * 1024,
                processes: 16,
                cpu_percent: 100,
            },
            columns: 80,
            rows: 24,
        })
        .map_err(|_| refused("agent Git scope unavailable"))?;
    let observed = process.wait(duration + Duration::from_secs(2));
    if !observed.finished || !observed.tree_retired || !observed.output_complete {
        return Err(refused("agent Git scope did not retire"));
    }
    if observed.reason != Some(StopReason::Exited)
        || observed.exit.and_then(|exit| exit.code) != Some(0)
    {
        return Err(refused("agent Git command failed"));
    }
    fs::read(&observed.output)
}

fn head(path: &str, candidate: &str, processes: &mut Processes) -> io::Result<()> {
    let safe = format!("safe.directory={path}");
    let output = git(
        processes,
        &["-C", path, "-c", &safe, "rev-parse", "HEAD"],
        Duration::from_secs(3),
    )?;
    if output != format!("{candidate}\r\n").as_bytes()
        && output != format!("{candidate}\n").as_bytes()
    {
        return Err(refused("agent source candidate invalid"));
    }
    Ok(())
}

fn source(candidate: &str, processes: &mut Processes) -> io::Result<()> {
    let path = Path::new(SOURCE);
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.uid() == 0
        || metadata.mode() & 0o7777 != 0o755
        || !readonly(path)?
    {
        return Err(refused("agent source mount invalid"));
    }
    head(SOURCE, candidate, processes)?;
    Ok(())
}

fn coder_workspace(
    candidate: &str,
    worker_uid: u32,
    processes: &mut Processes,
    prepared: bool,
) -> io::Result<()> {
    match Path::new(WORKSPACE).symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
        Ok(_) => return Err(refused("agent coder workspace already exists")),
    }
    if prepared {
        fs::create_dir(WORKSPACE)?;
        agent_environment::copy(Path::new(SOURCE), Path::new(WORKSPACE))?;
    } else {
        git(
            processes,
            &[
                "-c",
                "safe.directory=/run/factory-source",
                "clone",
                "--quiet",
                "--no-local",
                "--no-checkout",
                "--",
                SOURCE,
                WORKSPACE,
            ],
            Duration::from_secs(15),
        )?;
    }
    let metadata = Path::new(WORKSPACE).symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.uid() != worker_uid
        || metadata.mode() & 0o7777 != 0o700
        || readonly(Path::new(WORKSPACE))?
    {
        return Err(refused("agent coder workspace invalid"));
    }
    head(WORKSPACE, candidate, processes)?;
    git(
        processes,
        &["-C", WORKSPACE, "reset", "--hard", "--quiet", "HEAD"],
        Duration::from_secs(15),
    )?;
    if !prepared
        && !git(
            processes,
            &["-C", WORKSPACE, "status", "--porcelain", "-z"],
            Duration::from_secs(3),
        )?
        .is_empty()
    {
        return Err(refused("agent coder workspace dirty"));
    }
    Ok(())
}

fn listener(run: &str) -> io::Result<UnixListener> {
    // PrivatePIDs translates getpid(), while systemd set LISTEN_PID before
    // entering that namespace. The inherited FD itself is checked below.
    let listen_pid = std::env::var("LISTEN_PID")
        .ok()
        .and_then(|value| value.parse::<u32>().ok());
    if listen_pid.is_none_or(|pid| pid == 0)
        || std::env::var("LISTEN_FDS").ok().as_deref() != Some("1")
    {
        return Err(refused("agent socket activation missing"));
    }
    let path = PathBuf::from(format!("/run/soda-factory/agents/{run}.sock"));
    let parent = path.parent().expect("fixed agent socket parent");
    let directory = parent.symlink_metadata()?;
    let socket = path.symlink_metadata()?;
    if !directory.is_dir()
        || directory.uid() != 0
        || directory.mode() & 0o7777 != 0o755
        || !socket.file_type().is_socket()
        || socket.uid() != 0
        || socket.mode() & 0o7777 != 0o600
    {
        return Err(refused("agent socket path invalid"));
    }
    let mut kind = 0;
    let mut kind_length = std::mem::size_of_val(&kind) as libc::socklen_t;
    let mut listening = 0;
    let mut listen_length = std::mem::size_of_val(&listening) as libc::socklen_t;
    if unsafe {
        libc::getsockopt(
            3,
            libc::SOL_SOCKET,
            libc::SO_TYPE,
            (&mut kind as *mut i32).cast(),
            &mut kind_length,
        )
    } != 0
        || unsafe {
            libc::getsockopt(
                3,
                libc::SOL_SOCKET,
                libc::SO_ACCEPTCONN,
                (&mut listening as *mut i32).cast(),
                &mut listen_length,
            )
        } != 0
        || kind != libc::SOCK_STREAM
        || listening != 1
        || kind_length as usize != std::mem::size_of_val(&kind)
        || listen_length as usize != std::mem::size_of_val(&listening)
    {
        return Err(refused("agent listener invalid"));
    }
    let listener = unsafe { UnixListener::from_raw_fd(3) };
    close_on_exec(listener.as_raw_fd())?;
    if listener.local_addr()?.as_pathname() != Some(path.as_path()) {
        return Err(refused("agent listener path invalid"));
    }
    listener.set_nonblocking(true)?;
    Ok(listener)
}

fn peer(stream: &UnixStream) -> bool {
    let mut peer = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut length = std::mem::size_of_val(&peer) as libc::socklen_t;
    let valid = unsafe {
        libc::getsockopt(
            stream.as_raw_fd(),
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&mut peer as *mut libc::ucred).cast(),
            &mut length,
        ) == 0
    };
    // The broker PID is outside the private PID namespace and may map to 0.
    valid && length as usize == std::mem::size_of_val(&peer) && peer.uid == 0
}

fn sha256(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

struct PromptFile;
impl Drop for PromptFile {
    fn drop(&mut self) {
        let _ = fs::remove_file(PROMPT);
    }
}

fn private_prompt(bytes: &[u8]) -> io::Result<PromptFile> {
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(PROMPT)?;
    let result = (|| {
        file.write_all(bytes)?;
        file.sync_all()?;
        file.set_permissions(fs::Permissions::from_mode(0o400))
    })();
    if result.is_err() {
        let _ = fs::remove_file(PROMPT);
    }
    result.map(|()| PromptFile)
}

struct TimedInput<'a> {
    stream: &'a mut UnixStream,
    deadline: Instant,
}
impl Read for TimedInput<'_> {
    fn read(&mut self, bytes: &mut [u8]) -> io::Result<usize> {
        let remaining = self
            .deadline
            .checked_duration_since(Instant::now())
            .ok_or_else(|| {
                io::Error::new(io::ErrorKind::TimedOut, "agent executable import expired")
            })?;
        self.stream
            .set_read_timeout(Some(remaining.min(Duration::from_secs(3))))?;
        self.stream.read(bytes)
    }
}

fn reason(reason: Option<StopReason>) -> &'static str {
    match reason {
        Some(StopReason::Exited) => "exited",
        Some(StopReason::Cancelled) => "cancelled",
        Some(StopReason::Deadline) => "deadline",
        Some(StopReason::OutputLimit) => "output_limit",
        Some(StopReason::OutputUnavailable) => "output_unavailable",
        Some(StopReason::InputUnavailable) => "input_unavailable",
        Some(StopReason::ObservationUnavailable) => "observation_unavailable",
        None => "unconfirmed",
    }
}

fn peer_disconnected(stream: &UnixStream) -> io::Result<bool> {
    let mut byte = [0u8; 1];
    let received = unsafe {
        libc::recv(
            stream.as_raw_fd(),
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

#[allow(clippy::too_many_arguments)]
fn execute_agent(
    stream: &mut UnixStream,
    processes: &mut Processes,
    run: &str,
    profile: &str,
    candidate: &str,
    role: &str,
    tool_sha256: &str,
    prompt_sha256: &str,
    worker_uid: u32,
    repository_identity: Option<&RepositoryIdentity>,
    executable_length: u64,
    prompt_length: usize,
    placeholder: &str,
    duration_ms: u64,
    harness_profile: &soda_execution_protocol::HarnessProfile,
) -> io::Result<()> {
    if !harness_profile.valid()
        || !(1..=MAX_PROMPT).contains(&prompt_length)
        || !(4..=MAX_EXECUTABLE).contains(&executable_length)
        || !id(placeholder, 64)
        || !(1..=MAX_EXECUTION.as_millis() as u64).contains(&duration_ms)
    {
        return Err(refused("agent execution bounds invalid"));
    }
    let mut prompt = vec![0u8; prompt_length];
    stream.read_exact(&mut prompt)?;
    if sha256(&prompt) != prompt_sha256
        || std::str::from_utf8(&prompt).is_err()
        || prompt.contains(&0)
    {
        return Err(refused("agent prompt differs from admitted bytes"));
    }
    let prompt_file = private_prompt(&prompt)?;
    let mut timed = TimedInput {
        stream,
        deadline: Instant::now() + Duration::from_secs(120),
    };
    let executable = Executable::from_stream(
        &mut timed,
        executable_length,
        Path::new("/run/factory-captured-executable"),
        tool_sha256,
    )
    .map_err(|_| refused("agent executable differs from admitted bytes"))?;
    stream.set_read_timeout(Some(Duration::from_secs(3)))?;
    if peer_disconnected(stream)? {
        return Err(refused("agent request contains extra bytes or closed peer"));
    }
    let workspace = if role == "coder" { WORKSPACE } else { SOURCE };
    let mut arguments = [
        "exec",
        "--json",
        "--api-key-stdin",
        "--provider",
        "meta",
        "--model",
        harness_profile.model.as_str(),
        "--reasoning-effort",
        harness_profile.effort.as_str(),
        "--contributor-mode",
        if harness_profile.contributor {
            "on"
        } else {
            "off"
        },
        "--base-url",
        "http://127.0.0.1:37377",
        "--workspace",
        workspace,
        "--worktree",
        "off",
        "--prompt-file",
        PROMPT,
        "--max-model-steps",
        "16",
        "--max-tool-output-bytes",
        "65536",
        "--no-parallel-tool-calls",
        "--disable-web-tools",
        "--disable-reminders",
        "--disable-request-user-input",
        "--no-foreign-personal-context",
        "--no-session-log",
        "--approval-mode",
        "never",
    ]
    .into_iter()
    .map(OsString::from)
    .collect::<Vec<_>>();
    if role == "reviewer" {
        arguments.extend(["--disable-shell", "--disable-write"].map(OsString::from));
    }
    arguments.extend([
        OsString::from("--config"),
        OsString::from(r#"mcpServers.factory={"enabled":true,"transport":"streamable_http","url":"http://127.0.0.1:37377/mcp","headers":{"Authorization":"${SODA_FACTORY_RUN_AUTH}"}}"#),
    ]);
    let environment = BTreeMap::from([
        (
            OsString::from("SODA_FACTORY_RUN_AUTH"),
            OsString::from(format!("Bearer {placeholder}")),
        ),
        (
            OsString::from("PATH"),
            OsString::from(soda_execution_protocol::setup_path(PRIVATE_HOME)),
        ),
        (OsString::from("TMPDIR"), OsString::from(PRIVATE_TMP)),
        (OsString::from("HOME"), OsString::from(PRIVATE_HOME)),
        (
            OsString::from("XDG_CONFIG_HOME"),
            OsString::from(format!("{PRIVATE_HOME}/.config")),
        ),
        (
            OsString::from("XDG_CACHE_HOME"),
            OsString::from(format!("{PRIVATE_HOME}/.cache")),
        ),
        (
            OsString::from("XDG_STATE_HOME"),
            OsString::from(format!("{PRIVATE_HOME}/.local/state")),
        ),
    ]);
    let process = processes
        .launch_with_stdin(
            Invocation {
                executable: &executable,
                directory: Path::new(if role == "coder" { WORKSPACE } else { PRIVATE }),
                arguments,
                environment,
                limits: Limits {
                    duration: Duration::from_millis(duration_ms),
                    output_bytes: MAX_OUTPUT,
                    memory_bytes: 768 * 1024 * 1024,
                    processes: 64,
                    cpu_percent: 100,
                },
                columns: 80,
                rows: 24,
            },
            format!("{placeholder}\n").into_bytes(),
        )
        .map_err(|_| refused("agent process launch unavailable"))?;
    let started = process.observe();
    let started_unix_ms = started
        .started_at
        .duration_since(UNIX_EPOCH)
        .map_err(|_| refused("agent process start time invalid"))?
        .as_millis()
        .try_into()
        .map_err(|_| refused("agent process start time invalid"))?;
    let announcement = serde_json::to_vec(&Started {
        kind: "agent_started",
        version: 1,
        run,
        profile,
        candidate,
        role,
        tool_sha256,
        prompt_sha256,
        worker_uid,
        peer_uid: 0,
        invocation: &started.invocation,
        system_instance: &started.system_instance,
        start_identity: &started.start_identity,
        namespace_pid: started.pid,
        started_unix_ms,
    })?;
    if announcement.len() > MAX_START_REPLY {
        return Err(refused("agent start announcement bound invalid"));
    }
    stream.write_all(&(announcement.len() as u32).to_le_bytes())?;
    stream.write_all(&announcement)?;
    let deadline = Instant::now() + Duration::from_millis(duration_ms) + Duration::from_secs(3);
    let mut queries = agent_observation::Queries::default();
    let observed = loop {
        let observed = process.wait(Duration::from_millis(100));
        if queries.poll(stream, &process)? {
            process.cancel();
        }
        if observed.finished {
            if process
                .is_live()
                .map_err(|_| refused("agent final process observation unavailable"))?
            {
                return Err(refused("agent final process remains live"));
            }
            break observed;
        }
        if Instant::now() >= deadline {
            process.cancel();
        }
    };
    if !observed.tree_retired {
        return Err(refused("agent process tree retirement unconfirmed"));
    }
    let captured = match repository_identity {
        Some(identity) => agent_candidate::capture(
            Path::new(WORKSPACE),
            candidate,
            Path::new(PRIVATE).join("candidate.bundle").as_path(),
            identity,
            |arguments, duration| git(processes, arguments, duration),
        ),
        None => Ok(Candidate::NotApplicable),
    };
    processes
        .retire()
        .map_err(|_| refused("agent process tree retirement unconfirmed"))?;
    let captured = captured.map_err(|_| refused("agent candidate capture unconfirmed"))?;
    if matches!(captured, Candidate::Dirty) {
        return Err(refused("agent coder result remains uncheckpointed"));
    }
    let output = fs::read(&observed.output)?;
    if output.len() > MAX_OUTPUT {
        return Err(refused("agent output bound invalid"));
    }
    let output_sha256 = sha256(&output);
    let (candidate_state, result_candidate, bundle, bundle_sha256): (
        &str,
        Option<&str>,
        &[u8],
        Option<&str>,
    ) = match &captured {
        Candidate::NotApplicable => ("not_applicable", None, &[], None),
        Candidate::Unchanged => ("unchanged", None, &[], None),
        Candidate::Dirty => ("dirty", None, &[], None),
        Candidate::Captured {
            commit,
            bundle,
            sha256,
        } => ("captured", Some(commit), bundle, Some(sha256)),
    };
    let started_unix_ms = observed
        .started_at
        .duration_since(UNIX_EPOCH)
        .map_err(|_| refused("agent process start time invalid"))?
        .as_millis()
        .try_into()
        .map_err(|_| refused("agent process start time invalid"))?;
    let reply = serde_json::to_vec(&Execution {
        kind: "agent_executed",
        version: 3,
        run,
        profile,
        candidate,
        role,
        tool_sha256,
        prompt_sha256,
        worker_uid,
        peer_uid: 0,
        invocation: &observed.invocation,
        system_instance: &observed.system_instance,
        start_identity: &observed.start_identity,
        namespace_pid: observed.pid,
        started_unix_ms,
        reason: reason(observed.reason),
        exit_code: observed.exit.and_then(|exit| exit.code),
        exit_signal: observed.exit.and_then(|exit| exit.signal),
        tree_retired: true,
        output_complete: observed.output_complete,
        output_length: output.len(),
        output_sha256: &output_sha256,
        candidate_state,
        result_candidate,
        bundle_length: bundle.len(),
        bundle_sha256,
    })?;
    if reply.len() > MAX_EXECUTION_REPLY {
        return Err(refused("agent execution reply bound invalid"));
    }
    drop(prompt_file);
    stream.write_all(&(reply.len() as u32).to_le_bytes())?;
    stream.write_all(&reply)?;
    stream.write_all(&output)?;
    stream.write_all(bundle)
}

fn serve() -> io::Result<()> {
    let args: Vec<String> = std::env::args_os()
        .map(|value| {
            value
                .into_string()
                .map_err(|_| refused("agent argument encoding invalid"))
        })
        .collect::<io::Result<_>>()?;
    if args.len() != 3 || !id(&args[1], 32) {
        return Err(refused("agent run invalid"));
    }
    let desktop_uid = args[2]
        .parse::<u32>()
        .map_err(|_| refused("agent desktop UID invalid"))?;
    if desktop_uid == 0 || args[2] != desktop_uid.to_string() {
        return Err(refused("agent desktop UID invalid"));
    }
    let worker_uid = identity(&args[1], desktop_uid)?;
    let mut processes = process_owner(&args[1], worker_uid)?;
    let (role, profile, candidate, tool_sha256, prompt_sha256) = manifest(&args[1])?;
    source(&candidate, &mut processes)?;
    let approved = agent_environment::approved()?;
    if role == "coder" {
        coder_workspace(&candidate, worker_uid, &mut processes, approved.is_some())?;
    }
    if let Some(approved) = &approved {
        let workspace = if role == "coder" { WORKSPACE } else { SOURCE };
        agent_environment::check(&mut processes, Path::new(workspace), approved)?;
        head(workspace, &candidate, &mut processes)?;
        let safe = format!("safe.directory={workspace}");
        git(
            &mut processes,
            &[
                "-C",
                workspace,
                "-c",
                &safe,
                "-c",
                "core.fsmonitor=false",
                "-c",
                "core.hooksPath=/dev/null",
                "diff",
                "--no-ext-diff",
                "--no-textconv",
                "--quiet",
                "HEAD",
                "--",
            ],
            Duration::from_secs(3),
        )?;
    }
    let candidate_identity = if role == "coder" {
        Some(repository_identity(Path::new(WORKSPACE))?)
    } else {
        None
    };
    let listener = listener(&args[1])?;
    let deadline = Instant::now() + Duration::from_secs(10);
    let (mut stream, _) = loop {
        match listener.accept() {
            Ok(accepted) => break accepted,
            Err(error)
                if error.kind() == io::ErrorKind::WouldBlock && Instant::now() < deadline =>
            {
                std::thread::sleep(Duration::from_millis(10));
            }
            Err(error) => return Err(error),
        }
    };
    if !peer(&stream) {
        return Err(refused("agent broker peer invalid"));
    }
    close_on_exec(stream.as_raw_fd())?;
    stream.set_read_timeout(Some(Duration::from_secs(3)))?;
    stream.set_write_timeout(Some(Duration::from_secs(3)))?;
    // The broker may now consume this run's socket before sending executable
    // bytes. An accepted connection remains usable after the listener stops.
    stream.write_all(b"R")?;
    let mut length = [0u8; 4];
    stream.read_exact(&mut length)?;
    let length = u32::from_le_bytes(length) as usize;
    if length == 0 || length > MAX_REQUEST {
        return Err(refused("agent request bound invalid"));
    }
    let mut bytes = vec![0u8; length];
    stream.read_exact(&mut bytes)?;
    let request: Request =
        serde_json::from_slice(&bytes).map_err(|_| refused("agent request shape invalid"))?;
    match request {
        Request::TaskWorkspace {
            version,
            run,
            profile: requested_profile,
            candidate: requested_candidate,
            manifest_digest,
        } => {
            if version != 1
                || run != args[1]
                || requested_profile != profile
                || requested_candidate != candidate
                || role != "coder"
                || !id(&manifest_digest, 64)
            {
                return Err(refused("task workspace binding invalid"));
            }
            let result = agent_workspace::serve(
                &mut stream,
                &mut processes,
                &run,
                &candidate,
                &manifest_digest,
                candidate_identity
                    .as_ref()
                    .ok_or_else(|| refused("task workspace identity missing"))?,
            );
            processes
                .retire()
                .map_err(|_| refused("task workspace retirement unconfirmed"))?;
            result
        }
        Request::AttestAgent {
            version,
            run,
            profile: requested_profile,
        } => {
            if version != 1 || run != args[1] || requested_profile != profile {
                return Err(refused("agent attestation identity invalid"));
            }
            let reply = serde_json::to_vec(&Attestation {
                kind: "agent_attested",
                version: 1,
                run: &args[1],
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
            })?;
            stream.write_all(&(reply.len() as u32).to_le_bytes())?;
            stream.write_all(&reply)
        }
        Request::ExecuteAgent {
            version,
            run,
            profile: requested_profile,
            candidate: requested_candidate,
            tool_sha256: requested_tool,
            prompt_sha256: requested_prompt,
            executable_length,
            prompt_length,
            placeholder,
            duration_ms,
            harness_profile,
        } => {
            if version != 3
                || run != args[1]
                || requested_profile != profile
                || requested_candidate != candidate
                || requested_tool != tool_sha256
                || requested_prompt != prompt_sha256
            {
                return Err(refused("agent execution identity invalid"));
            }
            execute_agent(
                &mut stream,
                &mut processes,
                &run,
                &profile,
                &candidate,
                &role,
                &tool_sha256,
                &prompt_sha256,
                worker_uid,
                candidate_identity.as_ref(),
                executable_length,
                prompt_length,
                &placeholder,
                duration_ms,
                &harness_profile,
            )
        }
    }
}

fn main() {
    if let Err(error) = serve() {
        eprintln!("Factory agent boundary refused: {error}");
        std::process::exit(1);
    }
}
