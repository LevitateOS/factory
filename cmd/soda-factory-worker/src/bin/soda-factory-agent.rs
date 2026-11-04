#![cfg(target_os = "linux")]
//! One exact-run isolated service boundary. This attests role, mounted source
//! and desktop peer; it does not yet admit or launch an agent process.
use serde::{Deserialize, Serialize};
use soda_native_process::{Executable, Invocation, Limits, Processes, StopReason};
use std::collections::BTreeMap;
use std::ffi::OsString;
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

const SOURCE: &str = "/run/factory-source";
const ROLE: &str = "/run/factory-role";
const PRIVATE: &str = "/run/factory-agent-private";
const CGROUP: &str = "/sys/fs/cgroup/factory";
const MAX_REQUEST: usize = 256;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    kind: String,
    version: u8,
    run: String,
}

#[derive(Serialize)]
struct Attestation<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    candidate: &'a str,
    role: &'a str,
    worker_uid: u32,
    peer_uid: u32,
    readonly_source: bool,
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

fn manifest(run: &str) -> io::Result<(String, String)> {
    let path = Path::new(ROLE);
    let metadata = path.symlink_metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || metadata.len() > 192
        || !readonly(path)?
    {
        return Err(refused("agent role mount invalid"));
    }
    let mut bytes = Vec::new();
    File::open(path)?.take(193).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() {
        return Err(refused("agent role changed during read"));
    }
    let value = std::str::from_utf8(&bytes).map_err(|_| refused("agent role encoding invalid"))?;
    let mut lines = value.split_terminator('\n');
    let role = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_ROLE="))
        .ok_or_else(|| refused("agent role missing"))?;
    let candidate = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_CANDIDATE="))
        .ok_or_else(|| refused("agent candidate missing"))?;
    let digest = lines
        .next()
        .and_then(|value| value.strip_prefix("FACTORY_BUNDLE_SHA256="))
        .ok_or_else(|| refused("agent digest missing"))?;
    if !matches!(role, "reviewer" | "coder")
        || !id(candidate, 40)
        || !id(digest, 64)
        || lines.next().is_some()
        || !value.ends_with('\n')
        || !id(run, 32)
    {
        return Err(refused("agent role manifest invalid"));
    }
    Ok((role.into(), candidate.into()))
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
    let executable = Executable::capture(Path::new("/usr/bin/git"))
        .map_err(|_| refused("agent Git executable unavailable"))?;
    let environment = BTreeMap::from([
        (OsString::from("GIT_CONFIG_NOSYSTEM"), OsString::from("1")),
        (
            OsString::from("GIT_CONFIG_GLOBAL"),
            OsString::from("/dev/null"),
        ),
        (OsString::from("GIT_TERMINAL_PROMPT"), OsString::from("0")),
    ]);
    let process = processes
        .launch(Invocation {
            executable: &executable,
            directory: Path::new(PRIVATE),
            arguments: [
                "-C",
                SOURCE,
                "-c",
                "safe.directory=/run/factory-source",
                "rev-parse",
                "HEAD",
            ]
            .into_iter()
            .map(OsString::from)
            .collect(),
            environment,
            limits: Limits {
                duration: Duration::from_secs(3),
                output_bytes: 4096,
                memory_bytes: 128 * 1024 * 1024,
                processes: 8,
                cpu_percent: 100,
            },
            columns: 80,
            rows: 24,
        })
        .map_err(|_| refused("agent Git scope unavailable"))?;
    let observed = process.wait(Duration::from_secs(5));
    let output = if observed.finished && observed.tree_retired && observed.output_complete {
        fs::read(&observed.output)?
    } else {
        return Err(refused("agent Git scope did not retire"));
    };
    if observed.reason != Some(StopReason::Exited)
        || observed.exit.and_then(|exit| exit.code) != Some(0)
        || (output != format!("{candidate}\r\n").as_bytes()
            && output != format!("{candidate}\n").as_bytes())
    {
        return Err(refused("agent source candidate invalid"));
    }
    Ok(())
}

fn listener(run: &str, desktop_uid: u32) -> io::Result<UnixListener> {
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
        || socket.uid() != desktop_uid
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
    if listener.local_addr()?.as_pathname() != Some(path.as_path()) {
        return Err(refused("agent listener path invalid"));
    }
    listener.set_nonblocking(true)?;
    Ok(listener)
}

fn peer(stream: &UnixStream, desktop_uid: u32) -> bool {
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
    // The desktop PID is outside the private PID namespace and may map to 0.
    valid && length as usize == std::mem::size_of_val(&peer) && peer.uid == desktop_uid
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
    let (role, candidate) = manifest(&args[1])?;
    source(&candidate, &mut processes)?;
    let listener = listener(&args[1], desktop_uid)?;
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
    if !peer(&stream, desktop_uid) {
        return Err(refused("agent desktop peer invalid"));
    }
    stream.set_read_timeout(Some(Duration::from_secs(3)))?;
    stream.set_write_timeout(Some(Duration::from_secs(3)))?;
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
    if request.kind != "attest_agent" || request.version != 1 || request.run != args[1] {
        return Err(refused("agent request identity invalid"));
    }
    let reply = serde_json::to_vec(&Attestation {
        kind: "agent_attested",
        version: 1,
        run: &args[1],
        candidate: &candidate,
        role: &role,
        worker_uid,
        peer_uid: desktop_uid,
        readonly_source: true,
        execution_available: false,
    })?;
    stream.write_all(&(reply.len() as u32).to_le_bytes())?;
    stream.write_all(&reply)
}

fn main() {
    if let Err(error) = serve() {
        eprintln!("Factory agent boundary refused: {error}");
        std::process::exit(1);
    }
}
