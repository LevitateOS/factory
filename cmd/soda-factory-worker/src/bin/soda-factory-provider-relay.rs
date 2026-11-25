#![cfg(unix)]
//! Byte-only bridge between a private worker loopback and one owner Unix
//! socket. The desktop owner, not this untrusted worker, authenticates each
//! request and applies provider, run, route and credential policy. On macOS
//! the loopback listener belongs to the root unit host, which hands over only
//! connections held by this run's preparer or agent pool user.
use std::io;
use std::net::Ipv4Addr;
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::{FileTypeExt, MetadataExt};
use std::path::Path;
use std::time::Duration;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream, UnixStream};
use tokio::task::JoinSet;

#[allow(non_snake_case)]
fn OWNER() -> &'static str {
    soda_execution_protocol::host::unit("factory-provider.sock")
}
const MAX_REQUEST_BYTES: u64 = 4 * 1024 * 1024;
const MAX_RESPONSE_BYTES: u64 = 16 * 1024 * 1024;
const MAX_CONCURRENT: usize = 8;
const MAX_CONNECTIONS: usize = 512;
const LIFETIME: Duration = Duration::from_secs(4 * 60 * 60);
const IDLE: Duration = Duration::from_secs(120);

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn identity() -> io::Result<u32> {
    let args: Vec<String> = std::env::args_os()
        .map(|value| {
            value
                .into_string()
                .map_err(|_| refused("provider relay argument encoding invalid"))
        })
        .collect::<io::Result<_>>()?;
    if !(args.len() == 3 || args.len() == 4 && args[3] == "setup")
        || args[1].len() != 32
        || !args[1]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Err(refused("provider relay run invalid"));
    }
    let desktop_uid = args[2]
        .parse::<u32>()
        .map_err(|_| refused("provider relay desktop UID invalid"))?;
    if args[2] != desktop_uid.to_string() || desktop_uid == 0 {
        return Err(refused("provider relay desktop UID invalid"));
    }
    let uid = unsafe { libc::geteuid() };
    if uid == 0
        || uid == desktop_uid
        || unsafe { libc::getuid() } != uid
        || !soda_execution_protocol::host::unit_confined()
        || std::env::var_os("DBUS_SESSION_BUS_ADDRESS").is_some()
        || std::env::var_os("XDG_RUNTIME_DIR").is_some()
    {
        return Err(refused("provider relay service identity invalid"));
    }
    let directory = std::env::var_os("RUNTIME_DIRECTORY")
        .ok_or_else(|| refused("provider relay runtime missing"))?;
    let directory = Path::new(&directory);
    if directory.parent() != Some(Path::new(soda_execution_protocol::host::RELAY_RUNTIME))
        || directory.file_name() != Some(std::ffi::OsStr::new(&args[1]))
    {
        return Err(refused("provider relay runtime path invalid"));
    }
    let metadata = directory.symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != uid || metadata.mode() & 0o7777 != 0o700 {
        return Err(refused("provider relay runtime permissions invalid"));
    }
    Ok(desktop_uid)
}

fn owner_socket(desktop_uid: u32) -> io::Result<()> {
    let metadata = Path::new(OWNER()).symlink_metadata()?;
    if !metadata.file_type().is_socket()
        || metadata.uid() != desktop_uid
        || metadata.mode() & 0o7777 != 0o666
    {
        return Err(refused("provider relay owner socket invalid"));
    }
    Ok(())
}

fn owner_peer(stream: &UnixStream, desktop_uid: u32) -> io::Result<libc::pid_t> {
    match soda_execution_protocol::host::peer_credentials(stream.as_raw_fd()) {
        Some((pid, uid)) if uid == desktop_uid && pid > 0 => Ok(pid),
        _ => Err(refused("provider relay owner peer invalid")),
    }
}

/// The exact desktop process that bound the owner socket. An inode-only
/// check cannot distinguish a dead owner from an abandoned socket.
struct Owner {
    #[cfg(target_os = "linux")]
    pidfd: OwnedFd,
    #[cfg(target_os = "macos")]
    pid: libc::pid_t,
}

async fn owner_liveness(desktop_uid: u32) -> io::Result<Owner> {
    let stream = tokio::time::timeout(Duration::from_secs(3), UnixStream::connect(OWNER()))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "provider owner unavailable"))??;
    let pid = owner_peer(&stream, desktop_uid)?;
    #[cfg(target_os = "linux")]
    {
        let fd = unsafe { libc::syscall(libc::SYS_pidfd_open, pid, 0) };
        if fd < 0 {
            return Err(refused("provider owner process handle unavailable"));
        }
        return Ok(Owner { pidfd: unsafe { OwnedFd::from_raw_fd(fd as i32) } });
    }
    #[cfg(target_os = "macos")]
    return Ok(Owner { pid });
}

fn owner_gone(owner: &Owner) -> io::Result<bool> {
    #[cfg(target_os = "linux")]
    {
        let mut watched = libc::pollfd {
            fd: owner.pidfd.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        let ready = unsafe { libc::poll(&mut watched, 1, 0) };
        if ready < 0 {
            return Err(io::Error::last_os_error());
        }
        return Ok(ready != 0);
    }
    // The confined relay may not inspect or signal the desktop process; the
    // kernel still reports a missing PID before applying that policy.
    #[cfg(target_os = "macos")]
    {
        let result = unsafe { libc::kill(owner.pid, 0) };
        return Ok(result != 0 && io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH));
    }
}

fn notify_ready() -> io::Result<()> {
    let socket = std::env::var_os("NOTIFY_SOCKET")
        .ok_or_else(|| refused("provider relay readiness socket missing"))?;
    let socket = socket.as_bytes();
    let mut address: libc::sockaddr_un = unsafe { std::mem::zeroed() };
    address.sun_family = libc::AF_UNIX as libc::sa_family_t;
    let abstract_socket = cfg!(target_os = "linux") && socket.first() == Some(&b'@');
    let name = if abstract_socket {
        &socket[1..]
    } else {
        socket
    };
    if name.is_empty() || name.len() >= address.sun_path.len() {
        return Err(refused("provider relay readiness socket invalid"));
    }
    let start = usize::from(abstract_socket);
    for (index, byte) in name.iter().enumerate() {
        address.sun_path[start + index] = *byte as libc::c_char;
    }
    let length = std::mem::offset_of!(libc::sockaddr_un, sun_path)
        + start
        + name.len()
        + usize::from(!abstract_socket);
    let fd = unsafe { libc::socket(libc::AF_UNIX, libc::SOCK_DGRAM, 0) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    let fd = unsafe { OwnedFd::from_raw_fd(fd) };
    if unsafe { libc::fcntl(fd.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) } != 0 {
        return Err(io::Error::last_os_error());
    }
    let message = b"READY=1";
    let result = unsafe {
        libc::sendto(
            fd.as_raw_fd(),
            message.as_ptr().cast(),
            message.len(),
            0,
            (&address as *const libc::sockaddr_un).cast(),
            length as libc::socklen_t,
        )
    };
    if result != message.len() as isize {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

/// Client connections: the private namespace's fixed loopback port on Linux;
/// on macOS, connections the root unit host authenticated and passed over
/// the inherited fd 3.
enum Clients {
    Listener(TcpListener),
    #[cfg_attr(target_os = "linux", allow(dead_code))]
    Handed(UnixStream),
}
impl Clients {
    async fn open() -> io::Result<Self> {
        if cfg!(target_os = "macos") {
            if std::env::var("LISTEN_FDS").ok().as_deref() != Some("1") {
                return Err(refused("provider relay connection channel missing"));
            }
            let channel = unsafe { std::os::unix::net::UnixStream::from_raw_fd(3) };
            if unsafe { libc::fcntl(3, libc::F_SETFD, libc::FD_CLOEXEC) } != 0 {
                return Err(io::Error::last_os_error());
            }
            channel.set_nonblocking(true)?;
            return Ok(Self::Handed(UnixStream::from_std(channel)?));
        }
        let port = soda_execution_protocol::host::LINUX_RELAY_PORT;
        Ok(Self::Listener(TcpListener::bind((Ipv4Addr::LOCALHOST, port)).await?))
    }
    async fn accept(&self) -> io::Result<TcpStream> {
        match self {
            Self::Listener(listener) => Ok(listener.accept().await?.0),
            Self::Handed(channel) => loop {
                channel.readable().await?;
                match channel.try_io(tokio::io::Interest::READABLE, || receive_fd(channel.as_raw_fd())) {
                    Ok(fd) => {
                        let stream = std::net::TcpStream::from(fd);
                        stream.set_nonblocking(true)?;
                        return TcpStream::from_std(stream);
                    }
                    Err(error) if error.kind() == io::ErrorKind::WouldBlock => continue,
                    Err(error) => return Err(error),
                }
            },
        }
    }
}

fn receive_fd(channel: std::os::fd::RawFd) -> io::Result<OwnedFd> {
    let mut byte = [0u8; 1];
    let mut iov = libc::iovec {
        iov_base: byte.as_mut_ptr().cast(),
        iov_len: 1,
    };
    let space = unsafe { libc::CMSG_SPACE(std::mem::size_of::<i32>() as u32) } as usize;
    let mut control = vec![0u8; space];
    let mut message: libc::msghdr = unsafe { std::mem::zeroed() };
    message.msg_iov = &mut iov;
    message.msg_iovlen = 1;
    message.msg_control = control.as_mut_ptr().cast();
    message.msg_controllen = space as _;
    let read = unsafe { libc::recvmsg(channel, &mut message, 0) };
    if read < 0 {
        return Err(io::Error::last_os_error());
    }
    if read == 0 {
        return Err(io::Error::from(io::ErrorKind::UnexpectedEof));
    }
    unsafe {
        let header = libc::CMSG_FIRSTHDR(&message);
        if header.is_null()
            || (*header).cmsg_level != libc::SOL_SOCKET
            || (*header).cmsg_type != libc::SCM_RIGHTS
        {
            return Err(refused("provider relay handed connection invalid"));
        }
        let fd = std::ptr::read_unaligned(libc::CMSG_DATA(header).cast::<i32>());
        let fd = OwnedFd::from_raw_fd(fd);
        libc::fcntl(fd.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC);
        Ok(fd)
    }
}

async fn copy_limited<R: AsyncRead + Unpin, W: AsyncWrite + Unpin>(
    mut source: R,
    mut destination: W,
    limit: u64,
) -> io::Result<()> {
    let mut total = 0u64;
    let mut buffer = [0u8; 16384];
    loop {
        let n = tokio::time::timeout(IDLE, source.read(&mut buffer))
            .await
            .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "provider relay idle"))??;
        if n == 0 {
            destination.shutdown().await?;
            return Ok(());
        }
        total = total.saturating_add(n as u64);
        if total > limit {
            return Err(refused("provider relay byte bound exceeded"));
        }
        tokio::time::timeout(IDLE, destination.write_all(&buffer[..n]))
            .await
            .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "provider relay write idle"))??;
    }
}

async fn transfer(client: TcpStream, desktop_uid: u32, setup: bool) -> io::Result<()> {
    owner_socket(desktop_uid)?;
    let owner = tokio::time::timeout(Duration::from_secs(3), UnixStream::connect(OWNER()))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "provider owner unavailable"))??;
    owner_peer(&owner, desktop_uid)?;
    let (client_read, client_write) = client.into_split();
    let (owner_read, owner_write) = owner.into_split();
    let request = copy_limited(client_read, owner_write, MAX_REQUEST_BYTES);
    let response = copy_limited(
        owner_read,
        client_write,
        if setup {
            512 * 1024 * 1024
        } else {
            MAX_RESPONSE_BYTES
        },
    );
    tokio::pin!(request);
    tokio::pin!(response);
    tokio::select! {
        result = &mut request => { result?; response.await }
        result = &mut response => result,
    }
}

async fn serve() -> io::Result<()> {
    let desktop_uid = identity()?;
    let setup = std::env::args().nth(3).as_deref() == Some("setup");
    owner_socket(desktop_uid)?;
    let owner = owner_liveness(desktop_uid).await?;
    let clients = Clients::open().await?;
    notify_ready()?;
    let deadline = tokio::time::Instant::now() + LIFETIME;
    let mut monitor = tokio::time::interval(Duration::from_secs(1));
    let mut tasks = JoinSet::new();
    let mut count = 0;
    loop {
        if count == MAX_CONNECTIONS {
            break;
        }
        tokio::select! {
            biased;
            _ = monitor.tick() => {
                if owner_gone(&owner)? {
                    break;
                }
            }
            _ = tokio::time::sleep_until(deadline) => break,
            Some(result) = tasks.join_next(), if !tasks.is_empty() => {
                if let Err(error) = result {
                    eprintln!("Factory provider relay connection task failed: {error}");
                }
            }
            accepted = clients.accept() => {
                let client = accepted?;
                count += 1;
                if tasks.len() < MAX_CONCURRENT {
                    tasks.spawn(async move {
                        if let Err(error) = transfer(client, desktop_uid, setup).await {
                            eprintln!("Factory provider relay connection refused: {error}");
                        }
                    });
                }
            }
        }
    }
    tasks.abort_all();
    while tasks.join_next().await.is_some() {}
    Ok(())
}

#[tokio::main(flavor = "current_thread")]
async fn main() {
    if let Err(error) = serve().await {
        eprintln!("Factory provider relay refused service: {error}");
        std::process::exit(1);
    }
}
