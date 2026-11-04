#![cfg(target_os = "linux")]
//! Credential-free, run-specific reviewer attestation. The root broker owns
//! socket activation and the exact read-only checkout bind. No agent launch.
use serde::{Deserialize, Serialize};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::fs::MetadataExt;
use std::os::unix::net::UnixListener;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::Duration;

const WORKTREE: &str = "/run/factory-worktree";
const MAX_REQUEST: usize = 256;

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Request {
    AttestRun {
        version: u8,
        run: String,
        candidate: String,
    },
}

#[derive(Serialize)]
struct Attestation<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    candidate: &'a str,
    worker_uid: u32,
    peer_uid: u32,
    readonly_worktree: bool,
    execution_available: bool,
}

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn identity(run: &str) -> io::Result<u32> {
    let uid = unsafe { libc::geteuid() };
    if uid == 0
        || unsafe { libc::getuid() } != uid
        || unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1
        || std::env::var_os("DBUS_SESSION_BUS_ADDRESS").is_some()
        || std::env::var_os("XDG_RUNTIME_DIR").is_some()
    {
        return Err(refused("reviewer service identity invalid"));
    }
    let path =
        std::env::var_os("RUNTIME_DIRECTORY").ok_or_else(|| refused("reviewer runtime missing"))?;
    let path = Path::new(&path);
    if path.parent() != Some(Path::new("/run/soda-factory-reviewer"))
        || path.file_name() != Some(std::ffi::OsStr::new(run))
    {
        return Err(refused("reviewer runtime path invalid"));
    }
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != uid || metadata.mode() & 0o7777 != 0o700 {
        return Err(refused("reviewer runtime permissions invalid"));
    }
    Ok(uid)
}

fn listener(run: &str) -> io::Result<UnixListener> {
    if std::env::var("LISTEN_PID").ok().as_deref() != Some(&std::process::id().to_string())
        || std::env::var("LISTEN_FDS").ok().as_deref() != Some("1")
    {
        return Err(refused("reviewer socket activation invalid"));
    }
    let expected = format!("/run/soda-factory-broker/preparations/{run}/reviewer.sock");
    // SAFETY: systemd's validated LISTEN_PID and LISTEN_FDS grant fd 3 to this
    // process. UnixListener takes ownership exactly once.
    let listener = unsafe { UnixListener::from_raw_fd(3) };
    if listener.local_addr()?.as_pathname() != Some(Path::new(&expected)) {
        return Err(refused("reviewer socket path invalid"));
    }
    let mut accepting = 0;
    let mut length = std::mem::size_of_val(&accepting) as libc::socklen_t;
    if unsafe {
        libc::getsockopt(
            listener.as_raw_fd(),
            libc::SOL_SOCKET,
            libc::SO_ACCEPTCONN,
            (&mut accepting as *mut i32).cast(),
            &mut length,
        )
    } != 0
        || length as usize != std::mem::size_of_val(&accepting)
        || accepting != 1
    {
        return Err(refused("reviewer listener invalid"));
    }
    Ok(listener)
}

fn peer(fd: i32) -> io::Result<libc::ucred> {
    let mut peer = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut length = std::mem::size_of_val(&peer) as libc::socklen_t;
    if unsafe {
        libc::getsockopt(
            fd,
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&mut peer as *mut libc::ucred).cast(),
            &mut length,
        )
    } != 0
        || length as usize != std::mem::size_of_val(&peer)
        || peer.uid != 0
        || peer.pid <= 0
    {
        return Err(refused("reviewer peer is not the root broker"));
    }
    Ok(peer)
}

fn checkout(candidate: &str) -> io::Result<()> {
    let metadata = Path::new(WORKTREE).symlink_metadata()?;
    if !metadata.is_dir() || metadata.mode() & 0o7777 != 0o755 {
        return Err(refused("reviewer checkout mount invalid"));
    }
    let path = std::ffi::CString::new(WORKTREE).expect("fixed worktree path");
    let mut stat = unsafe { std::mem::zeroed::<libc::statvfs>() };
    if unsafe { libc::statvfs(path.as_ptr(), &mut stat) } != 0 || stat.f_flag & libc::ST_RDONLY == 0
    {
        return Err(refused("reviewer checkout is writable"));
    }
    let mut child = Command::new("/usr/bin/timeout")
        .env_clear()
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .args([
            "-k",
            "1s",
            "3s",
            "/usr/bin/git",
            "-c",
            "safe.directory=/run/factory-worktree",
            "-C",
            WORKTREE,
            "rev-parse",
            "--verify",
            "HEAD^{commit}",
        ])
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()?;
    let mut output = Vec::with_capacity(42);
    let read = child
        .stdout
        .take()
        .ok_or_else(|| refused("reviewer Git output unavailable"))?
        .take(42)
        .read_to_end(&mut output);
    if read.is_err() || output.len() > 41 {
        let _ = child.kill();
        let _ = child.wait();
        return Err(refused("reviewer Git output invalid"));
    }
    let status = child.wait()?;
    if !status.success() || output != format!("{candidate}\n").as_bytes() {
        return Err(refused("reviewer checkout candidate differs"));
    }
    Ok(())
}

fn serve() -> io::Result<()> {
    let args: Vec<String> = std::env::args_os()
        .map(|value| {
            value
                .into_string()
                .map_err(|_| refused("reviewer argument encoding invalid"))
        })
        .collect::<io::Result<_>>()?;
    if args.len() != 2 || !hex(&args[1], 32) {
        return Err(refused("reviewer run invalid"));
    }
    let uid = identity(&args[1])?;
    let listener = listener(&args[1])?;
    let (mut stream, _) = listener.accept()?;
    stream.set_read_timeout(Some(Duration::from_secs(3)))?;
    stream.set_write_timeout(Some(Duration::from_secs(3)))?;
    let broker = peer(stream.as_raw_fd())?;
    let mut header = [0u8; 4];
    stream.read_exact(&mut header)?;
    let length = u32::from_le_bytes(header) as usize;
    if length == 0 || length > MAX_REQUEST {
        return Err(refused("reviewer request bound"));
    }
    let mut bytes = vec![0; length];
    stream.read_exact(&mut bytes)?;
    let request: Request =
        serde_json::from_slice(&bytes).map_err(|_| refused("reviewer request shape invalid"))?;
    let Request::AttestRun {
        version,
        run,
        candidate,
    } = request;
    if version != 1 || run != args[1] || !hex(&candidate, 40) {
        return Err(refused("reviewer request identity invalid"));
    }
    checkout(&candidate)?;
    let response = serde_json::to_vec(&Attestation {
        kind: "run_attested",
        version: 1,
        run: &run,
        candidate: &candidate,
        worker_uid: uid,
        peer_uid: broker.uid,
        readonly_worktree: true,
        execution_available: false,
    })?;
    stream.write_all(&(response.len() as u32).to_le_bytes())?;
    stream.write_all(&response)
}

fn main() {
    if let Err(error) = serve() {
        eprintln!("Factory reviewer refused attestation: {error}");
        std::process::exit(1);
    }
}
