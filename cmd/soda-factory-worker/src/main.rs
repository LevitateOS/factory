#![cfg(target_os = "linux")]
//! One socket activation is one isolated worker lifetime. No execution request
//! is admitted until the coordinator, worktree and credential broker are wired.
use serde::{Deserialize, Serialize};
use std::io::{self, Read, Write};
use std::os::fd::RawFd;

const MAX_REQUEST: usize = 256;
const DEADLINE_SECONDS: libc::time_t = 3;

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Request {
    Attest { version: u8 },
}

#[derive(Serialize)]
struct Attestation {
    kind: &'static str,
    version: u8,
    worker_uid: u32,
    peer_uid: u32,
    no_new_privileges: bool,
    execution_available: bool,
}

fn socket(fd: RawFd) -> io::Result<libc::ucred> {
    let mut kind = 0;
    let mut kind_len = std::mem::size_of_val(&kind) as libc::socklen_t;
    let mut peer = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut peer_len = std::mem::size_of_val(&peer) as libc::socklen_t;
    let timeout = libc::timeval {
        tv_sec: DEADLINE_SECONDS,
        tv_usec: 0,
    };
    // SAFETY: all pointers refer to initialized, correctly sized stack values.
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
    {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "worker socket admission failed",
        ));
    }
    Ok(peer)
}

fn serve() -> io::Result<()> {
    let peer = socket(0)?;
    // The worker must be a distinct unprivileged identity with no desktop
    // session inherited. The root-owned system service supplies the stronger
    // filesystem, process and cgroup confinement checked by D3 acceptance.
    let uid = unsafe { libc::geteuid() };
    for (invalid, reason) in [
        (uid == 0, "worker has root identity"),
        (uid == peer.uid, "worker shares desktop peer identity"),
        (unsafe { libc::getuid() } != uid, "worker real UID differs"),
        (
            unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1,
            "worker can gain privileges",
        ),
        (
            std::env::var_os("DBUS_SESSION_BUS_ADDRESS").is_some(),
            "worker inherited session bus",
        ),
        (
            std::env::var_os("XDG_RUNTIME_DIR").is_some(),
            "worker inherited desktop runtime",
        ),
    ] {
        if invalid {
            return Err(io::Error::new(io::ErrorKind::PermissionDenied, reason));
        }
    }
    let mut input = io::stdin().lock();
    let mut length = [0u8; 4];
    input.read_exact(&mut length)?;
    let length = u32::from_le_bytes(length) as usize;
    if length == 0 || length > MAX_REQUEST {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "worker request bound",
        ));
    }
    let mut bytes = vec![0u8; length];
    input.read_exact(&mut bytes)?;
    let request: Request = serde_json::from_slice(&bytes)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "worker request shape"))?;
    match request {
        Request::Attest { version: 1 } => (),
        _ => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "worker protocol version",
            ))
        }
    }
    // One connection carries exactly one bounded request and response. Ignore
    // subsequent input even if a peer keeps the stream open.
    unsafe { libc::shutdown(0, libc::SHUT_RD) };
    let response = serde_json::to_vec(&Attestation {
        kind: "attest",
        version: 1,
        worker_uid: uid,
        peer_uid: peer.uid,
        no_new_privileges: true,
        execution_available: false,
    })?;
    let mut output = io::stdout().lock();
    output.write_all(&(response.len() as u32).to_le_bytes())?;
    output.write_all(&response)?;
    output.flush()
}

fn main() {
    if let Err(error) = serve() {
        eprintln!("Factory worker refused request: {error}");
        std::process::exit(1);
    }
}
