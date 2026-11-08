//! Credential-free HTTPS CONNECT for one approved preparation. The isolated
//! loopback relay carries bytes; this owner admits the exact peer and pins each
//! destination to resolved public addresses. It never borrows provider custody.
use super::AgentCancellation;
use std::fs::Metadata;
use std::io;
use std::net::{IpAddr, SocketAddr};
use std::os::unix::net::UnixListener as StdListener;
use std::path::PathBuf;
use std::sync::{
    atomic::{AtomicU64, Ordering},
    mpsc, Arc,
};
use std::thread::{self, JoinHandle};
use std::time::Duration;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};
use tokio::net::{TcpStream, UnixListener, UnixStream};
use tokio::sync::watch;
use tokio::task::JoinSet;

const BYTES: u64 = 512 * 1024 * 1024;
fn charge(bytes: &AtomicU64, count: usize) -> io::Result<()> {
    if bytes.fetch_add(count as u64, Ordering::SeqCst).saturating_add(count as u64) > BYTES {
        return Err(io::Error::new(io::ErrorKind::QuotaExceeded, "setup HTTPS transfer budget exceeded"));
    }
    Ok(())
}
fn denied() -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, "approved setup public HTTPS unavailable")
}

pub(super) struct SetupNetwork {
    listener: Option<StdListener>,
    path: PathBuf,
    parent: PathBuf,
    original: Metadata,
    stopped: Option<watch::Sender<bool>>,
    thread: Option<JoinHandle<io::Result<()>>>,
}
impl SetupNetwork {
    pub fn bind(run: &str) -> io::Result<Self> {
        let (listener, path, parent, original) = super::owner::bind_run_socket(run)?;
        Ok(Self {
            listener: Some(listener),
            path,
            parent,
            original,
            stopped: None,
            thread: None,
        })
    }
    pub fn start(
        &mut self,
        uid: u32,
        cancellation: AgentCancellation,
        duration: Duration,
    ) -> io::Result<()> {
        if uid == 0
            || uid == unsafe { libc::geteuid() }
            || duration.is_zero()
            || duration > Duration::from_secs(3660)
        {
            return Err(denied());
        }
        let listener = self.listener.take().ok_or_else(denied)?;
        let (stopped, mut stopping) = watch::channel(false);
        let (started, ready) = mpsc::sync_channel(1);
        self.stopped = Some(stopped);
        self.thread = Some(thread::Builder::new().name("factory-setup-https".into()).spawn(move || {
            let runtime = tokio::runtime::Builder::new_current_thread().enable_all().build()?;
            runtime.block_on(async move {
                let listener = UnixListener::from_std(listener)?;
                let bytes = Arc::new(AtomicU64::new(0));
                let mut jobs: JoinSet<io::Result<()>> = JoinSet::new();
                let deadline = tokio::time::Instant::now() + duration;
                let mut check = tokio::time::interval(Duration::from_millis(50));
                let mut count = 0;
                let _ = started.send(());
                let result = loop {
                    tokio::select! {
                        biased;
                        _ = stopping.changed() => break Ok(()),
                        _ = check.tick() => if cancellation.cancelled() { break Ok(()); },
                        _ = tokio::time::sleep_until(deadline) => break Ok(()),
                        result = jobs.join_next(), if !jobs.is_empty() => {
                            if let Some(Ok(Err(error))) = result {
                                if error.kind() == io::ErrorKind::QuotaExceeded { break Err(error); }
                            }
                        },
                        accepted = listener.accept() => {
                            let (stream, _) = match accepted { Ok(value) => value, Err(error) => break Err(error) };
                            if !stream.peer_cred().is_ok_and(|peer| peer.uid() == uid) { continue; }
                            count += 1;
                            if count > 1024 { break Err(denied()); }
                            if jobs.len() >= 8 { continue; }
                            let bytes = bytes.clone();
                            jobs.spawn(async move {
                                tokio::time::timeout(Duration::from_secs(120), connection(stream, bytes)).await
                                    .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "setup connection timed out"))?
                            });
                        }
                    }
                };
                jobs.abort_all();
                while jobs.join_next().await.is_some() {}
                result
            })
        })?);
        ready
            .recv_timeout(Duration::from_secs(3))
            .map_err(|_| denied())
    }
    pub fn stop(&mut self) -> io::Result<()> {
        if let Some(stopped) = self.stopped.take() {
            let _ = stopped.send(true);
        }
        self.listener.take();
        let result = self
            .thread
            .take()
            .map(|job| job.join().map_err(|_| denied())?)
            .transpose();
        let cleanup = super::owner::cleanup(&self.path, &self.parent, &self.original);
        result.and(cleanup)
    }
}
impl Drop for SetupNetwork {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}

// Admit ordinary public unicast, excluding private, local, multicast,
// documentation and transition ranges that can encode a private destination.
fn public(address: IpAddr) -> bool {
    match address {
        IpAddr::V4(address) => {
            let [a, b, c, _] = address.octets();
            !(a == 0
                || a == 10
                || a == 127
                || a >= 224
                || a == 100 && (64..=127).contains(&b)
                || a == 169 && b == 254
                || a == 172 && (16..=31).contains(&b)
                || a == 192 && (b == 168 || b == 0 && (c == 0 || c == 2))
                || a == 198 && (b == 18 || b == 19 || b == 51 && c == 100)
                || a == 203 && b == 0 && c == 113)
        }
        IpAddr::V6(address) => {
            let [a, b, _, _, _, _, _, _] = address.segments();
            a & 0xe000 == 0x2000
                && a != 0x2002
                && a != 0x3fff
                && !(a == 0x2001 && (b < 0x0200 || b == 0x0db8))
        }
    }
}
async fn target(authority: &str) -> io::Result<Vec<SocketAddr>> {
    if authority.len() > 300 || !authority.ends_with(":443") {
        return Err(denied());
    }
    let url = url::Url::parse(&format!("https://{authority}")).map_err(|_| denied())?;
    if !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
        || url.port_or_known_default() != Some(443)
    {
        return Err(denied());
    }
    let addresses: Vec<_> = match url.host().ok_or_else(denied)? {
        url::Host::Ipv4(address) => vec![SocketAddr::new(address.into(), 443)],
        url::Host::Ipv6(address) => vec![SocketAddr::new(address.into(), 443)],
        url::Host::Domain(host) => {
            tokio::time::timeout(Duration::from_secs(4), tokio::net::lookup_host((host, 443)))
                .await
                .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "setup DNS timed out"))??
                .take(65)
                .collect()
        }
    };
    if addresses.is_empty()
        || addresses.len() > 64
        || addresses.iter().any(|address| !public(address.ip()))
    {
        return Err(denied());
    }
    Ok(addresses)
}
async fn copy<R: AsyncRead + Unpin, W: AsyncWrite + Unpin>(
    mut reader: R,
    mut writer: W,
    bytes: Arc<AtomicU64>,
) -> io::Result<()> {
    let mut buffer = [0u8; 16384];
    loop {
        let count = tokio::time::timeout(Duration::from_secs(30), reader.read(&mut buffer))
            .await
            .map_err(|_| denied())??;
        if count == 0 {
            return writer.shutdown().await;
        }
        charge(&bytes, count)?;
        tokio::time::timeout(Duration::from_secs(30), writer.write_all(&buffer[..count]))
            .await
            .map_err(|_| denied())??;
    }
}
async fn connection(mut stream: UnixStream, bytes: Arc<AtomicU64>) -> io::Result<()> {
    let mut header = Vec::new();
    let end = loop {
        let mut buffer = [0u8; 1024];
        let count = tokio::time::timeout(Duration::from_secs(5), stream.read(&mut buffer))
            .await
            .map_err(|_| denied())??;
        if count == 0 {
            return Ok(());
        } // The relay's owner-liveness connection.
        header.extend_from_slice(&buffer[..count]);
        if header.len() > 8192 {
            return Err(denied());
        }
        if let Some(index) = header.windows(4).position(|bytes| bytes == b"\r\n\r\n") {
            break index + 4;
        }
    };
    let request = std::str::from_utf8(&header[..end]).map_err(|_| denied())?;
    let mut fields = request
        .lines()
        .next()
        .ok_or_else(denied)?
        .split_ascii_whitespace();
    let method = fields.next();
    let authority = fields.next().ok_or_else(denied)?;
    if method != Some("CONNECT") || fields.next() != Some("HTTP/1.1") || fields.next().is_some() {
        stream
            .write_all(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
            .await?;
        return Err(denied());
    }
    let addresses = match target(authority).await {
        Ok(addresses) => addresses,
        Err(error) => {
            let response = match error.kind() {
                io::ErrorKind::PermissionDenied => b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n".as_slice(),
                io::ErrorKind::TimedOut => b"HTTP/1.1 504 Gateway Timeout\r\nContent-Length: 0\r\n\r\n".as_slice(),
                _ => b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n".as_slice(),
            };
            stream
                .write_all(response)
                .await?;
            return Err(error);
        }
    };
    let mut connected = None;
    for address in addresses {
        if let Ok(Ok(stream)) =
            tokio::time::timeout(Duration::from_secs(3), TcpStream::connect(address)).await
        {
            connected = Some(stream);
            break;
        }
    }
    let Some(mut remote) = connected else {
        stream.write_all(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n").await?;
        return Err(io::Error::new(io::ErrorKind::NotConnected, "setup destination unavailable"));
    };
    stream
        .write_all(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        .await?;
    if end < header.len() {
        charge(&bytes, header.len() - end)?;
        remote.write_all(&header[end..]).await?;
    }
    let (client_read, client_write) = stream.into_split();
    let (remote_read, remote_write) = remote.into_split();
    tokio::try_join!(
        copy(client_read, remote_write, bytes.clone()),
        copy(remote_read, client_write, bytes)
    )?;
    Ok(())
}
