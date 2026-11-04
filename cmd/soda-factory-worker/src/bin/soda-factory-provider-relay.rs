#![cfg(target_os = "linux")]
//! Byte-only bridge between a private worker loopback and one owner Unix
//! socket. The desktop owner, not this untrusted worker, authenticates each
//! request and applies provider, run, route and credential policy.
use std::io;
use std::net::Ipv4Addr;
use std::os::fd::AsRawFd;
use std::os::unix::fs::{FileTypeExt, MetadataExt};
use std::path::Path;
use std::time::Duration;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream, UnixStream};
use tokio::task::JoinSet;

const OWNER: &str = "/run/factory-provider.sock";
const PORT: u16 = 37377;
const MAX_REQUEST_BYTES: u64 = 4 * 1024 * 1024;
const MAX_RESPONSE_BYTES: u64 = 16 * 1024 * 1024;
const MAX_CONCURRENT: usize = 8;
const MAX_CONNECTIONS: usize = 64;
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
    if args.len() != 3
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
        || unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1
        || std::env::var_os("DBUS_SESSION_BUS_ADDRESS").is_some()
        || std::env::var_os("XDG_RUNTIME_DIR").is_some()
    {
        return Err(refused("provider relay service identity invalid"));
    }
    let directory = std::env::var_os("RUNTIME_DIRECTORY")
        .ok_or_else(|| refused("provider relay runtime missing"))?;
    let directory = Path::new(&directory);
    if directory.parent() != Some(Path::new("/run/soda-factory-relay"))
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
    let metadata = Path::new(OWNER).symlink_metadata()?;
    if !metadata.file_type().is_socket()
        || metadata.uid() != desktop_uid
        || metadata.mode() & 0o7777 != 0o666
    {
        return Err(refused("provider relay owner socket invalid"));
    }
    Ok(())
}

fn owner_peer(stream: &UnixStream, desktop_uid: u32) -> io::Result<()> {
    let mut peer = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut length = std::mem::size_of_val(&peer) as libc::socklen_t;
    if unsafe {
        libc::getsockopt(
            stream.as_raw_fd(),
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&mut peer as *mut libc::ucred).cast(),
            &mut length,
        )
    } != 0
        || length as usize != std::mem::size_of_val(&peer)
        || peer.uid != desktop_uid
        || peer.pid <= 0
    {
        return Err(refused("provider relay owner peer invalid"));
    }
    Ok(())
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

async fn transfer(client: TcpStream, desktop_uid: u32) -> io::Result<()> {
    owner_socket(desktop_uid)?;
    let owner = tokio::time::timeout(Duration::from_secs(3), UnixStream::connect(OWNER))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "provider owner unavailable"))??;
    owner_peer(&owner, desktop_uid)?;
    let (client_read, client_write) = client.into_split();
    let (owner_read, owner_write) = owner.into_split();
    let request = copy_limited(client_read, owner_write, MAX_REQUEST_BYTES);
    let response = copy_limited(owner_read, client_write, MAX_RESPONSE_BYTES);
    tokio::pin!(request);
    tokio::pin!(response);
    tokio::select! {
        result = &mut request => { result?; response.await }
        result = &mut response => result,
    }
}

async fn serve() -> io::Result<()> {
    let desktop_uid = identity()?;
    owner_socket(desktop_uid)?;
    let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, PORT)).await?;
    let deadline = tokio::time::Instant::now() + LIFETIME;
    let mut tasks = JoinSet::new();
    let mut count = 0;
    loop {
        if count == MAX_CONNECTIONS {
            break;
        }
        tokio::select! {
            biased;
            _ = tokio::time::sleep_until(deadline) => break,
            Some(result) = tasks.join_next(), if !tasks.is_empty() => {
                if let Err(error) = result {
                    eprintln!("Factory provider relay connection task failed: {error}");
                }
            }
            accepted = listener.accept() => {
                let (client, _) = accepted?;
                count += 1;
                if tasks.len() < MAX_CONCURRENT {
                    tasks.spawn(async move {
                        if let Err(error) = transfer(client, desktop_uid).await {
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
