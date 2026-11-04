//! Desktop-owned, run-specific Unix endpoint. The relay system unit mounts
//! this socket; only its attested UID may request fixed Meta HTTPS I/O.
use super::{Admitted, Broker, Policy, RelayAttestation, Route};
use crate::credentials::{Credentials, Provider, TokenCapability};
use reqwest::{header, redirect, Client};
use std::fs::{self, DirBuilder, Metadata};
use std::io;
use std::os::fd::AsRawFd;
use std::os::unix::fs::{DirBuilderExt, FileTypeExt, MetadataExt, PermissionsExt};
use std::os::unix::net::UnixListener as StdUnixListener;
use std::path::{Path, PathBuf};
use std::sync::{mpsc, Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{UnixListener, UnixStream};
use tokio::sync::watch;
use tokio::task::JoinSet;
use zeroize::Zeroizing;

const MAX_HEADER: usize = 16 * 1024;
const MAX_BODY: usize = 256 * 1024;
const MAX_RESPONSE: usize = 16 * 1024 * 1024;
const MAX_CONNECTIONS: usize = 32;
const MAX_IN_FLIGHT: usize = 4;
const REQUEST_TIME: Duration = Duration::from_secs(8);
const PROVIDER_TIME: Duration = Duration::from_secs(5 * 60);

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn owned_directory(path: &Path, uid: u32) -> io::Result<()> {
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != uid || metadata.mode() & 0o7777 != 0o700 {
        return Err(refused("provider owner directory invalid"));
    }
    Ok(())
}

fn cleanup(path: &Path, parent: &Path, original: &Metadata) {
    if path.symlink_metadata().is_ok_and(|current| {
        current.file_type().is_socket()
            && current.dev() == original.dev()
            && current.ino() == original.ino()
    }) {
        let _ = fs::remove_file(path);
    }
    let _ = fs::remove_dir(parent);
}

/// Bound before the broker starts the relay, then activated only with the
/// broker-attested dynamic UID. No request is served in the bound state.
pub struct BoundOwner {
    listener: Option<StdUnixListener>,
    path: PathBuf,
    parent: PathBuf,
    original: Metadata,
    policy: Option<Policy>,
    credentials: Arc<Credentials>,
    capability: Arc<TokenCapability>,
    placeholder: Zeroizing<String>,
    desktop_uid: u32,
    cleanup: bool,
}

impl BoundOwner {
    pub fn bind(
        policy: Policy,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
    ) -> io::Result<Self> {
        if capability.account().provider != Provider::Muse
            || capability.account().origin != "https://api.meta.ai"
        {
            return Err(refused("provider credential scope invalid"));
        }
        let uid = unsafe { libc::geteuid() };
        if uid == 0 || unsafe { libc::getuid() } != uid {
            return Err(refused("provider owner desktop identity invalid"));
        }
        let runtime = PathBuf::from(format!("/run/user/{uid}"));
        if std::env::var_os("XDG_RUNTIME_DIR").as_deref() != Some(runtime.as_os_str()) {
            return Err(refused("provider owner runtime path invalid"));
        }
        owned_directory(&runtime, uid)?;
        let parent = runtime.join("soda-factory-provider");
        match DirBuilder::new().mode(0o700).create(&parent) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
        owned_directory(&parent, uid)?;
        let path = parent.join(format!("{}.sock", policy.run()));
        match path.symlink_metadata() {
            Err(error) if error.kind() == io::ErrorKind::NotFound => (),
            Err(error) => return Err(error),
            Ok(_) => return Err(refused("provider owner run socket already exists")),
        }
        let listener = StdUnixListener::bind(&path)?;
        if let Err(error) = fs::set_permissions(&path, fs::Permissions::from_mode(0o666)) {
            let _ = fs::remove_file(&path);
            return Err(error);
        }
        let original = match path.symlink_metadata() {
            Ok(metadata) => metadata,
            Err(error) => {
                let _ = fs::remove_file(&path);
                return Err(error);
            }
        };
        if !original.file_type().is_socket()
            || original.uid() != uid
            || original.mode() & 0o7777 != 0o666
        {
            let _ = fs::remove_file(&path);
            return Err(refused("provider owner socket identity invalid"));
        }
        if let Err(error) = listener.set_nonblocking(true) {
            cleanup(&path, &parent, &original);
            return Err(error);
        }
        let mut random = [0u8; 32];
        if getrandom::fill(&mut random).is_err() {
            cleanup(&path, &parent, &original);
            return Err(refused("provider owner placeholder unavailable"));
        }
        let placeholder = Zeroizing::new(random.iter().map(|byte| format!("{byte:02x}")).collect());
        Ok(Self {
            listener: Some(listener),
            path,
            parent,
            original,
            policy: Some(policy),
            credentials,
            capability: Arc::new(capability),
            placeholder,
            desktop_uid: uid,
            cleanup: true,
        })
    }

    /// Scoped, non-account placeholder supplied to the isolated CLI's stdin.
    pub fn placeholder(&self) -> &str {
        &self.placeholder
    }

    pub fn activate(mut self, attestation: RelayAttestation) -> io::Result<Owner> {
        let policy = self
            .policy
            .as_ref()
            .ok_or_else(|| refused("provider run already active"))?;
        if attestation.run() != policy.run()
            || attestation.profile() != policy.profile()
            || attestation.candidate() != policy.candidate()
            || attestation.relay_uid() == 0
            || attestation.relay_uid() == self.desktop_uid
        {
            return Err(refused("provider relay identity invalid"));
        }
        let relay_uid = attestation.relay_uid();
        let listener = self
            .listener
            .take()
            .ok_or_else(|| refused("provider owner already active"))?;
        let policy = self
            .policy
            .take()
            .ok_or_else(|| refused("provider run already active"))?;
        let (retired, retirement) = watch::channel(false);
        let (send, ready) = mpsc::sync_channel::<io::Result<()>>(1);
        let state = Arc::new(State {
            policy: Mutex::new(policy),
            credentials: self.credentials.clone(),
            capability: self.capability.clone(),
            placeholder: self.placeholder.clone(),
            relay_uid,
        });
        let thread = thread::Builder::new()
            .name("factory-provider-owner".into())
            .spawn(move || {
                let runtime = tokio::runtime::Builder::new_current_thread()
                    .enable_all()
                    .build()
                    .map_err(|_| refused("provider owner runtime unavailable"))?;
                runtime.block_on(async move {
                    let listener = UnixListener::from_std(listener)?;
                    let http = Client::builder()
                        .https_only(true)
                        .no_proxy()
                        .redirect(redirect::Policy::none())
                        .retry(reqwest::retry::never())
                        .connect_timeout(Duration::from_secs(5))
                        .timeout(PROVIDER_TIME)
                        .build()
                        .map_err(|_| refused("provider owner transport unavailable"))?;
                    let _ = send.send(Ok(()));
                    serve(listener, state, http, retirement).await
                })
            })?;
        match ready.recv_timeout(Duration::from_secs(3)) {
            Ok(Ok(())) => {
                self.cleanup = false;
                Ok(Owner {
                    path: self.path.clone(),
                    parent: self.parent.clone(),
                    original: self.original.clone(),
                    placeholder: self.placeholder.clone(),
                    attestation: Some(attestation),
                    retired,
                    thread: Some(thread),
                })
            }
            _ => {
                retired.send_replace(true);
                let _ = thread.join();
                Err(refused("provider owner did not start"))
            }
        }
    }
}

impl Drop for BoundOwner {
    fn drop(&mut self) {
        if self.cleanup {
            cleanup(&self.path, &self.parent, &self.original);
        }
    }
}

pub struct Owner {
    path: PathBuf,
    parent: PathBuf,
    original: Metadata,
    placeholder: Zeroizing<String>,
    attestation: Option<RelayAttestation>,
    retired: watch::Sender<bool>,
    thread: Option<JoinHandle<io::Result<()>>>,
}
impl Owner {
    pub fn placeholder(&self) -> &str {
        &self.placeholder
    }

    pub fn running(&self) -> bool {
        self.thread
            .as_ref()
            .is_some_and(|thread| !thread.is_finished())
    }

    pub fn stop(&mut self) -> io::Result<()> {
        self.retired.send_replace(true);
        let result = if let Some(thread) = self.thread.take() {
            thread
                .join()
                .unwrap_or_else(|_| Err(refused("provider owner thread failed")))
        } else {
            Ok(())
        };
        cleanup(&self.path, &self.parent, &self.original);
        result
    }

    /// Retire local token use first, then ask the broker to stop the exact
    /// relay and remove its staged run. A failed broker call remains retryable.
    pub fn retire(&mut self) -> io::Result<()> {
        let stopped = self.stop();
        let attestation = self
            .attestation
            .as_ref()
            .ok_or_else(|| refused("provider run already retired"))?;
        Broker::retire_bundle(
            attestation.run(),
            attestation.profile(),
            attestation.sha256(),
        )?;
        self.attestation.take();
        stopped
    }
}
impl Drop for Owner {
    fn drop(&mut self) {
        let _ = self.retire();
    }
}

struct State {
    policy: Mutex<Policy>,
    credentials: Arc<Credentials>,
    capability: Arc<TokenCapability>,
    placeholder: Zeroizing<String>,
    relay_uid: u32,
}

async fn serve(
    listener: UnixListener,
    state: Arc<State>,
    http: Client,
    mut retirement: watch::Receiver<bool>,
) -> io::Result<()> {
    let remaining = state
        .policy
        .lock()
        .map_err(|_| refused("provider run state unavailable"))?
        .remaining()
        .map_err(|_| refused("provider run already retired"))?;
    let deadline = tokio::time::Instant::now() + remaining;
    let mut tasks = JoinSet::new();
    let mut count = 0;
    loop {
        if count == MAX_CONNECTIONS {
            break;
        }
        tokio::select! {
            biased;
            _ = retirement.changed() => break,
            _ = tokio::time::sleep_until(deadline) => break,
            _ = tasks.join_next(), if !tasks.is_empty() => {},
            accepted = listener.accept() => {
                let (stream, _) = match accepted {
                    Ok(accepted) => accepted,
                    Err(error) => {
                        tasks.shutdown().await;
                        return Err(error);
                    }
                };
                count += 1;
                if tasks.len() < MAX_IN_FLIGHT {
                    let state = state.clone();
                    let http = http.clone();
                    tasks.spawn(async move { handle(stream, state, http).await });
                }
            }
        }
    }
    if let Ok(mut policy) = state.policy.lock() {
        policy.retire();
    }
    tasks.shutdown().await;
    Ok(())
}

fn peer(stream: &UnixStream, relay_uid: u32) -> bool {
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
    valid
        && length as usize == std::mem::size_of_val(&peer)
        && peer.pid > 0
        && peer.uid == relay_uid
}

struct Request {
    method: String,
    path: String,
    body: Vec<u8>,
}

async fn read_request(stream: &mut UnixStream, placeholder: &str) -> io::Result<Request> {
    let mut header = Vec::new();
    let mut byte = [0u8; 1];
    while header.len() < MAX_HEADER && !header.ends_with(b"\r\n\r\n") {
        stream.read_exact(&mut byte).await?;
        header.push(byte[0]);
    }
    if !header.ends_with(b"\r\n\r\n") {
        return Err(refused("provider request header bound exceeded"));
    }
    let header = std::str::from_utf8(&header)
        .map_err(|_| refused("provider request header encoding invalid"))?;
    let mut lines = header.split("\r\n");
    let first = lines
        .next()
        .ok_or_else(|| refused("provider request line missing"))?;
    let mut parts = first.split(' ');
    let method = parts.next().unwrap_or("");
    let path = parts.next().unwrap_or("");
    if !matches!(method, "GET" | "POST")
        || !matches!(path, "/muse-code/models" | "/responses" | "/v1/responses")
        || parts.next() != Some("HTTP/1.1")
        || parts.next().is_some()
    {
        return Err(refused("provider request line invalid"));
    }
    let mut authorization = None;
    let mut length = None;
    let mut content_type = None;
    let mut host = None;
    for line in lines.filter(|line| !line.is_empty()) {
        if line.starts_with([' ', '\t']) {
            return Err(refused("provider request folded header invalid"));
        }
        let (name, value) = line
            .split_once(':')
            .ok_or_else(|| refused("provider request header invalid"))?;
        let value = value.trim();
        match name.to_ascii_lowercase().as_str() {
            "authorization" if authorization.replace(value).is_some() => {
                return Err(refused("provider authorization repeated"))
            }
            "authorization" => (),
            "content-length" if length.replace(value).is_some() => {
                return Err(refused("provider length repeated"))
            }
            "content-length" => (),
            "content-type" if content_type.replace(value).is_some() => {
                return Err(refused("provider type repeated"))
            }
            "content-type" => (),
            "host" if host.replace(value).is_some() => {
                return Err(refused("provider host repeated"))
            }
            "host" => (),
            "transfer-encoding"
            | "content-encoding"
            | "expect"
            | "upgrade"
            | "proxy-authorization" => return Err(refused("provider request header unsupported")),
            _ => (),
        }
    }
    if host.is_none()
        || authorization != Some(format!("Bearer {placeholder}").as_str())
        || (method == "POST"
            && !content_type.is_some_and(|value| value.starts_with("application/json")))
    {
        return Err(refused("provider request authorization or type invalid"));
    }
    let length = match length {
        Some(value) if !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit()) => {
            value
                .parse::<usize>()
                .map_err(|_| refused("provider request length invalid"))?
        }
        None if method == "GET" => 0,
        _ => return Err(refused("provider request length missing")),
    };
    if length > MAX_BODY || (method == "GET" && length != 0) || (method == "POST" && length == 0) {
        return Err(refused("provider request body bound invalid"));
    }
    let mut body = vec![0u8; length];
    stream.read_exact(&mut body).await?;
    Ok(Request {
        method: method.into(),
        path: path.into(),
        body,
    })
}

async fn write_error(stream: &mut UnixStream, status: u16) -> io::Result<()> {
    let code = match status {
        400 => "invalid_request",
        401 | 403 => "provider_run_refused",
        410 => "provider_run_retired",
        429 => "provider_budget_exhausted",
        404 | 502 => "provider_unavailable",
        _ => "provider_unavailable",
    };
    let status_line = match status {
        400 => "400 Bad Request",
        401 => "401 Unauthorized",
        403 => "403 Forbidden",
        404 => "404 Not Found",
        410 => "410 Gone",
        429 => "429 Too Many Requests",
        502 => "502 Bad Gateway",
        _ => "503 Service Unavailable",
    };
    let body = format!("{{\"error\":{{\"code\":\"{code}\"}}}}");
    let header = format!("HTTP/1.1 {status_line}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\nCache-Control: no-store\r\n\r\n", body.len());
    stream.write_all(header.as_bytes()).await?;
    stream.write_all(body.as_bytes()).await
}

enum Upstream {
    Success,
    Status(u16),
    Unavailable,
}

async fn handle(mut stream: UnixStream, state: Arc<State>, http: Client) {
    if !peer(&stream, state.relay_uid) {
        return;
    }
    let request =
        match tokio::time::timeout(REQUEST_TIME, read_request(&mut stream, &state.placeholder))
            .await
        {
            Ok(Ok(request)) => request,
            _ => {
                let _ = write_error(&mut stream, 400).await;
                return;
            }
        };
    let admission = {
        let Ok(mut policy) = state.policy.lock() else {
            return;
        };
        match policy.admit(&request.method, &request.path, &request.body) {
            Ok(admitted) => match policy.remaining() {
                Ok(duration) => Ok((admitted, duration.min(PROVIDER_TIME))),
                Err(_) => Err(410),
            },
            Err(super::Error::Invalid) => Err(403),
            Err(super::Error::Retired) => Err(410),
            Err(super::Error::Budget) => Err(429),
        }
    };
    let (admitted, duration) = match admission {
        Ok(value) => value,
        Err(status) => {
            let _ = write_error(&mut stream, status).await;
            return;
        }
    };
    let current = match state.credentials.renew_capability(&state.capability).await {
        Ok(current) => current,
        Err(_) => {
            let _ = write_error(&mut stream, 503).await;
            return;
        }
    };
    let mut began = false;
    let result = state
        .credentials
        .with_token_stream(&current, duration, async |token| {
            forward(&http, &admitted, token, &mut stream, &mut began).await
        })
        .await;
    match result {
        Ok(Ok(Upstream::Success)) if began => {
            let _ = stream.write_all(b"0\r\n\r\n").await;
        }
        Ok(Ok(Upstream::Status(status))) => {
            let status = if matches!(status, 400 | 401 | 403 | 404 | 429) {
                status
            } else {
                502
            };
            let _ = write_error(&mut stream, status).await;
        }
        _ if !began => {
            let _ = write_error(&mut stream, 503).await;
        }
        _ => {}
    }
}

async fn forward(
    http: &Client,
    admitted: &Admitted,
    token: &[u8],
    stream: &mut UnixStream,
    began: &mut bool,
) -> io::Result<Upstream> {
    let token =
        std::str::from_utf8(token).map_err(|_| refused("provider token encoding invalid"))?;
    let mut authorization = header::HeaderValue::from_str(&format!("Bearer {token}"))
        .map_err(|_| refused("provider token header invalid"))?;
    authorization.set_sensitive(true);
    let request = match admitted.route {
        Route::Models => http.get(admitted.route.upstream()),
        Route::Responses => http
            .post(admitted.route.upstream())
            .header(header::CONTENT_TYPE, "application/json")
            .body(admitted.body.clone()),
    };
    let mut response = match request
        .header(header::AUTHORIZATION, authorization)
        .send()
        .await
    {
        Ok(response) => response,
        Err(_) => return Ok(Upstream::Unavailable),
    };
    let status = response.status().as_u16();
    if !response.status().is_success() {
        return Ok(Upstream::Status(status));
    }
    let expected_type = match admitted.route {
        Route::Models => "application/json",
        Route::Responses => "text/event-stream",
    };
    if !response
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.starts_with(expected_type))
    {
        return Ok(Upstream::Unavailable);
    }
    let header = format!("HTTP/1.1 200 OK\r\nContent-Type: {expected_type}\r\nTransfer-Encoding: chunked\r\nConnection: close\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\n\r\n");
    stream.write_all(header.as_bytes()).await?;
    *began = true;
    let mut total = header.len() + 5; // Include HTTP headers and the final chunk.
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| refused("provider response interrupted"))?
    {
        let size = format!("{:x}\r\n", chunk.len());
        total = total.saturating_add(size.len() + chunk.len() + 2);
        if total > MAX_RESPONSE {
            return Err(refused("provider response bound exceeded"));
        }
        stream.write_all(size.as_bytes()).await?;
        stream.write_all(&chunk).await?;
        stream.write_all(b"\r\n").await?;
    }
    Ok(Upstream::Success)
}
