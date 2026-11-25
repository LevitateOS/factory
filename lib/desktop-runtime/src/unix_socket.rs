//! Unix-domain socket facts that differ between Linux and macOS.
use std::io;
use std::os::fd::{FromRawFd, OwnedFd, RawFd};

/// Kernel-attested process ID and effective UID of a connected peer.
pub(crate) struct Peer {
    pub(crate) pid: i32,
    pub(crate) uid: u32,
}

#[cfg(target_os = "linux")]
pub(crate) fn peer(fd: RawFd) -> Option<Peer> {
    let mut peer = libc::ucred {
        pid: 0,
        uid: u32::MAX,
        gid: 0,
    };
    let mut length = std::mem::size_of_val(&peer) as libc::socklen_t;
    let valid = unsafe {
        libc::getsockopt(
            fd,
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&mut peer as *mut libc::ucred).cast(),
            &mut length,
        ) == 0
    };
    (valid && length as usize == std::mem::size_of_val(&peer)).then_some(Peer {
        pid: peer.pid,
        uid: peer.uid,
    })
}

#[cfg(target_os = "macos")]
pub(crate) fn peer(fd: RawFd) -> Option<Peer> {
    let (mut uid, mut gid) = (u32::MAX, u32::MAX);
    if unsafe { libc::getpeereid(fd, &mut uid, &mut gid) } != 0 {
        return None;
    }
    let mut pid: libc::pid_t = 0;
    let mut length = std::mem::size_of_val(&pid) as libc::socklen_t;
    let valid = unsafe {
        libc::getsockopt(
            fd,
            libc::SOL_LOCAL,
            libc::LOCAL_PEERPID,
            (&mut pid as *mut libc::pid_t).cast(),
            &mut length,
        ) == 0
    };
    (valid && length as usize == std::mem::size_of_val(&pid)).then_some(Peer { pid, uid })
}

/// A close-on-exec, nonblocking `AF_UNIX` stream socket.
pub(crate) fn nonblocking_stream() -> io::Result<OwnedFd> {
    #[cfg(target_os = "linux")]
    let fd = unsafe {
        libc::socket(
            libc::AF_UNIX,
            libc::SOCK_STREAM | libc::SOCK_CLOEXEC | libc::SOCK_NONBLOCK,
            0,
        )
    };
    // macOS has no atomic socket flags; set them before the descriptor is shared.
    #[cfg(target_os = "macos")]
    let fd = unsafe { libc::socket(libc::AF_UNIX, libc::SOCK_STREAM, 0) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    let owned = unsafe { OwnedFd::from_raw_fd(fd) };
    #[cfg(target_os = "macos")]
    unsafe {
        if libc::fcntl(fd, libc::F_SETFD, libc::FD_CLOEXEC) != 0
            || libc::fcntl(
                fd,
                libc::F_SETFL,
                libc::fcntl(fd, libc::F_GETFL) | libc::O_NONBLOCK,
            ) != 0
        {
            return Err(io::Error::last_os_error());
        }
    }
    Ok(owned)
}
