//! Authenticated native host effects for the Go product owner. Collaboration
//! selectors are semantic and require a native operation. The separate fixed
//! MCP route derives from a caller-supplied scope; Go must validate its ledger
//! capability and current run before asking for that secret.
use crate::credentials::{Credentials, Provider, TokenCapability};
use crate::github_provider::{Client, Comment, CommentOutcome, Failure, Read};
use crate::mcp_signing::Signer;
use crate::product::Products;
use crate::supervisor::NativeChannel;
use http_body_util::{BodyExt, Full, Limited};
use hyper::body::{Bytes, Incoming};
use hyper::server::conn::http1;
use hyper::service::service_fn;
use hyper::{Request, Response, StatusCode};
use hyper_util::rt::TokioIo;
use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::BTreeMap;
use std::convert::Infallible;
use std::fs::{self, Metadata};
use std::os::unix::fs::{FileTypeExt, MetadataExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{mpsc, Arc, Mutex, Weak};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};
use tokio::net::UnixListener;
use tokio::sync::{mpsc as jobs, oneshot, watch, OwnedSemaphorePermit, Semaphore};
use tokio::task::JoinSet;

type Result<T> = std::result::Result<T, String>;
const MAX_OPERATIONS: usize = 16;
const LIFETIME: Duration = Duration::from_secs(20);
const CALL_TIMEOUT: Duration = Duration::from_secs(8);

struct Scope {
    credentials: Arc<Credentials>,
    capability: TokenCapability,
    repository: Option<String>,
    deadline: Instant,
    cancelled: watch::Sender<bool>,
    write_claimed: AtomicBool,
    _permit: OwnedSemaphorePermit,
}
impl Scope {
    fn current(&self) -> bool {
        !*self.cancelled.borrow() && Instant::now() < self.deadline
    }
}
struct State {
    channel: NativeChannel,
    signer: Signer,
    provider: Client,
    alive: Arc<AtomicBool>,
    retired: watch::Sender<bool>,
    scopes: Mutex<BTreeMap<String, Weak<Scope>>>,
    slots: Arc<Semaphore>,
    finalizations: jobs::Sender<Finalize>,
}
struct Finalize {
    scope: Arc<Scope>,
    reply: oneshot::Sender<Result<()>>,
}
#[derive(Clone)]
pub(crate) struct Port(Arc<State>);

/// One native-only product request. No serialization, cloning, capability getter
/// or browser-supplied operation key. Drop cancels even an unpolled request.
pub struct NativeRequest {
    id: String,
    scope: Arc<Scope>,
    port: Port,
    products: Products,
}
impl NativeRequest {
    pub fn request(
        self,
        method: &str,
        target: &str,
        body: &[u8],
    ) -> impl std::future::Future<Output = Result<soda_unix_http::Response>> {
        let request = self
            .products
            .admitted_request(method, target, body, &self.id);
        async move {
            let mut cancelled = self.scope.cancelled.subscribe();
            let work = async {
                let response = request.await?;
                // Final custody validation also runs on the joined native
                // owner, rather than leaving keyring work on a caller runtime.
                self.port.finalize(self.scope.clone()).await?;
                Ok(response)
            };
            tokio::select! {
                biased;
                _ = cancelled.wait_for(|cancelled| *cancelled) => return Err("native operation cancelled".into()),
                _ = tokio::time::sleep_until(self.scope.deadline.into()) => return Err("native operation expired".into()),
                result = work => result,
            }
        }
    }
}
impl Drop for NativeRequest {
    fn drop(&mut self) {
        self.scope.cancelled.send_replace(true);
        if let Ok(mut scopes) = self.port.0.scopes.lock() {
            scopes.remove(&self.id);
        }
    }
}
impl Port {
    async fn finalize(&self, scope: Arc<Scope>) -> Result<()> {
        if !scope.current() || !self.0.alive.load(Ordering::SeqCst) {
            return Err("native operation retired".into());
        }
        let (reply, response) = oneshot::channel();
        self.0
            .finalizations
            .try_send(Finalize {
                scope: scope.clone(),
                reply,
            })
            .map_err(|_| "native final validation unavailable")?;
        response
            .await
            .map_err(|_| "native final validation retired")??;
        if !scope.current() || !self.0.alive.load(Ordering::SeqCst) {
            return Err("native operation retired".into());
        }
        Ok(())
    }
    pub fn retire(&self) {
        self.0.alive.store(false, Ordering::SeqCst);
        self.0.slots.close();
        self.0.retired.send_replace(true);
        if let Ok(mut scopes) = self.0.scopes.lock() {
            for scope in scopes.values().filter_map(Weak::upgrade) {
                scope.cancelled.send_replace(true);
            }
            scopes.clear();
        }
    }
    pub fn admit(
        &self,
        products: Products,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        repository: Option<String>,
    ) -> Result<NativeRequest> {
        let account = capability.account();
        if !self.0.alive.load(Ordering::SeqCst)
            || !credentials.is_profile(&self.0.channel.profile)
            || account.provider != Provider::Github
            || account.origin != "https://github.com"
            || !remote_id(&account.account)
            || repository.as_ref().is_some_and(|id| !remote_id(id))
        {
            return Err("invalid native operation scope".into());
        }
        let permit = self
            .0
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| "native operation limit or retirement reached")?;
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).map_err(|_| "native operation admission unavailable")?;
        let id: String = random.iter().map(|b| format!("{b:02x}")).collect();
        let (cancelled, _) = watch::channel(false);
        let scope = Arc::new(Scope {
            credentials,
            capability,
            repository,
            deadline: Instant::now() + LIFETIME,
            cancelled,
            write_claimed: AtomicBool::new(false),
            _permit: permit,
        });
        let mut scopes = self
            .0
            .scopes
            .lock()
            .map_err(|_| "native operation admission unavailable")?;
        scopes.retain(|_, scope| scope.strong_count() > 0);
        if scopes.contains_key(&id) || !self.0.alive.load(Ordering::SeqCst) {
            return Err("native operation admission unavailable".into());
        }
        scopes.insert(id.clone(), Arc::downgrade(&scope));
        Ok(NativeRequest {
            id,
            scope,
            port: self.clone(),
            products,
        })
    }
}

pub(crate) struct Owner {
    pub port: Port,
    thread: Option<JoinHandle<Result<()>>>,
}
impl Owner {
    /// The supervisor holds its exclusive profile lock until this owner joins.
    pub fn start(channel: NativeChannel, provider: Client, alive: Arc<AtomicBool>) -> Result<Self> {
        let signer = Signer::open(
            channel
                .socket
                .parent()
                .ok_or("native MCP profile unavailable")?,
        )?;
        let (retired, mut retirement) = watch::channel(false);
        let (finalizations, mut finalization_jobs) = jobs::channel::<Finalize>(MAX_OPERATIONS);
        let port = Port(Arc::new(State {
            channel,
            signer,
            provider,
            alive,
            retired,
            scopes: Mutex::new(BTreeMap::new()),
            slots: Arc::new(Semaphore::new(MAX_OPERATIONS)),
            finalizations,
        }));
        let state = port.0.clone();
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|_| "native collaboration runtime unavailable")?;
        let (started, ready) = mpsc::channel();
        let thread = thread::Builder::new().name("factory-native-collaboration".into()).spawn(move || {
            runtime.block_on(async move {
                let (listener, _socket) = match bind(&state.channel.socket.with_file_name("host.sock")).await {
                    Ok(bound) => bound,
                    Err(error) => { let _ = started.send(Err(error.clone())); return Err(error); }
                };
                if started.send(Ok(())).is_err() { return Ok(()); }
                let connections = Arc::new(Semaphore::new(MAX_OPERATIONS));
                let mut tasks = JoinSet::new();
                let result = loop {
                    tokio::select! {
                        biased;
                        _ = retirement.changed() => break Ok(()),
                        Some(job) = finalization_jobs.recv() => { tasks.spawn(finalize(job)); }
                        accepted = listener.accept() => {
                            let (stream, _) = match accepted { Ok(stream) => stream, Err(_) => break Err("native collaboration listener stopped".into()) };
                            if !stream.peer_cred().is_ok_and(|peer| peer.uid() == unsafe { libc::geteuid() }) { continue; }
                            let Ok(permit) = connections.clone().try_acquire_owned() else { continue; };
                            let state = state.clone();
                            tasks.spawn(async move {
                                let _permit = permit;
                                let service = service_fn(move |request| serve(request, state.clone()));
                                let _ = tokio::time::timeout(Duration::from_secs(9), http1::Builder::new()
                                    .keep_alive(false).max_headers(32).max_buf_size(16384)
                                    .serve_connection(TokioIo::new(stream), service)).await;
                            });
                        }
                        _ = tasks.join_next(), if !tasks.is_empty() => {}
                    }
                };
                drop(listener);
                finalization_jobs.close();
                while let Ok(job) = finalization_jobs.try_recv() { drop(job); }
                tasks.shutdown().await;
                result
            })
        }).map_err(|_| "native collaboration owner unavailable")?;
        let mut owner = Self {
            port,
            thread: Some(thread),
        };
        match ready.recv() {
            Ok(Ok(())) => Ok(owner),
            _ => {
                let _ = owner.stop();
                Err("native collaboration listener unavailable".into())
            }
        }
    }
    pub fn check_running(&self) -> Result<()> {
        if self
            .thread
            .as_ref()
            .is_none_or(|thread| thread.is_finished())
        {
            Err("native collaboration owner stopped".into())
        } else {
            Ok(())
        }
    }
    pub fn stop(&mut self) -> Result<()> {
        self.port.retire();
        if let Some(thread) = self.thread.take() {
            thread
                .join()
                .map_err(|_| "native collaboration owner failed")??;
        }
        Ok(())
    }
}
impl Drop for Owner {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}

async fn finalize(job: Finalize) {
    let mut cancelled = job.scope.cancelled.subscribe();
    let work = async {
        job.scope
            .credentials
            .with_token_operation(&job.scope.capability, async |_| ())
            .await
            .map_err(|_| "native operation credential retired".into())
    };
    let result = tokio::select! {
        biased;
        _ = cancelled.wait_for(|cancelled| *cancelled) => Err("native operation cancelled".into()),
        _ = tokio::time::sleep_until(job.scope.deadline.into()) => Err("native operation expired".into()),
        result = work => result,
    };
    let _ = job.reply.send(result);
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Call {
    operation: String,
    call: Effect,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SignCall {
    capability_id: String,
    scope_digest: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct VerifyCall {
    capability_id: String,
    scope_digest: String,
    secret: String,
}
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Effect {
    Current,
    Read {
        query: Read,
    },
    CreateComment {
        command_id: String,
        digest: String,
        not_after: u64,
        comment: Comment,
    },
}
fn remote_id(value: &str) -> bool {
    value
        .parse::<i64>()
        .is_ok_and(|id| id > 0 && id.to_string() == value)
}
fn opaque_id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn one<'a>(request: &'a Request<Incoming>, name: &str) -> Option<&'a str> {
    let mut values = request.headers().get_all(name).iter();
    let value = values.next()?.to_str().ok()?;
    if values.next().is_some() {
        None
    } else {
        Some(value)
    }
}
fn equal(a: &str, b: &str) -> bool {
    a.len() == b.len()
        && a.bytes()
            .zip(b.bytes())
            .fold(0u8, |sum, (a, b)| sum | (a ^ b))
            == 0
}
async fn serve(
    request: Request<Incoming>,
    state: Arc<State>,
) -> std::result::Result<Response<Full<Bytes>>, Infallible> {
    let response = tokio::time::timeout(CALL_TIMEOUT, dispatch(request, &state))
        .await
        .unwrap_or_else(|_| Err(Failure::new(504, "native_collaboration_deadline")));
    // Recheck immediately before serialization; a retired owner never returns
    // a formerly successful provider observation.
    let response = if state.alive.load(Ordering::SeqCst) {
        response
    } else {
        Err(Failure::unavailable())
    };
    let (status, body) = match response {
        Ok(value) => (200, value),
        Err(error) => (
            error.status,
            json!({"error":error.code,"retry_after":error.retry_after,"reset":error.reset}),
        ),
    };
    let bytes = serde_json::to_vec(&body)
        .ok()
        .filter(|body| body.len() <= 2 * 1024 * 1024);
    let (status, bytes) = bytes
        .map(|body| (status, body))
        .unwrap_or_else(|| (502, b"{\"error\":\"native_response_bound\"}".to_vec()));
    let mut response = Response::new(Full::new(Bytes::from(bytes)));
    *response.status_mut() = StatusCode::from_u16(status).unwrap_or(StatusCode::BAD_GATEWAY);
    response
        .headers_mut()
        .insert("content-type", "application/json".parse().unwrap());
    response
        .headers_mut()
        .insert("cache-control", "no-store".parse().unwrap());
    Ok(response)
}
async fn dispatch(
    request: Request<Incoming>,
    state: &State,
) -> std::result::Result<Value, Failure> {
    if !state.alive.load(Ordering::SeqCst) {
        return Err(Failure::unavailable());
    }
    let browser = request
        .headers()
        .keys()
        .any(|key| key == "origin" || key.as_str().starts_with("sec-fetch-"));
    if browser {
        return Err(Failure::new(403, "private_channel_required"));
    }
    let key = one(&request, "authorization")
        .and_then(|v| v.strip_prefix("Bearer "))
        .unwrap_or("");
    if !equal(key, &state.channel.key)
        || one(&request, "x-soda-native-instance") != Some(state.channel.instance.as_str())
    {
        return Err(Failure::new(401, "native_channel_required"));
    }
    let target = request.uri().path_and_query().map(|target| target.as_str());
    let signing = target == Some("/api/native/mcp-sign");
    let verifying = target == Some("/api/native/mcp-verify");
    if request.method() != "POST"
        || !matches!(
            target,
            Some("/api/native/collaboration" | "/api/native/mcp-sign" | "/api/native/mcp-verify")
        )
        || request.uri().scheme().is_some()
        || request.uri().authority().is_some()
        || one(&request, "host") != Some("soda-native")
        || one(&request, "content-type") != Some("application/json")
    {
        return Err(Failure::invalid());
    }
    let body_limit = if signing || verifying {
        8192
    } else {
        512 * 1024
    };
    let body = Limited::new(request.into_body(), body_limit)
        .collect()
        .await
        .map_err(|_| Failure::invalid())?
        .to_bytes();
    if signing {
        let call: SignCall = serde_json::from_slice(&body).map_err(|_| Failure::invalid())?;
        let secret = state
            .signer
            .derive(&call.capability_id, &call.scope_digest)
            .map_err(|_| Failure::invalid())?;
        return Ok(json!({"secret":secret}));
    }
    if verifying {
        let call: VerifyCall = serde_json::from_slice(&body).map_err(|_| Failure::invalid())?;
        let valid = state
            .signer
            .verify(&call.capability_id, &call.scope_digest, &call.secret)
            .map_err(|_| Failure::invalid())?;
        return Ok(json!({"valid":valid}));
    }
    let call: Call = serde_json::from_slice(&body).map_err(|_| Failure::invalid())?;
    if !matches!(&call.call, Effect::CreateComment { .. }) && body.len() > 8192 {
        return Err(Failure::invalid());
    }
    if !opaque_id(&call.operation) {
        return Err(Failure::invalid());
    }
    let scope = state
        .scopes
        .lock()
        .map_err(|_| Failure::unavailable())?
        .get(&call.operation)
        .and_then(Weak::upgrade)
        .filter(|scope| scope.current())
        .ok_or_else(|| Failure::new(401, "native_operation_retired"))?;
    if let Effect::Read { query } = &call.call {
        query.validate()?;
        let value = query.value();
        let identity = matches!(query, Read::Identity);
        if !identity
            && (value["repository"].as_str().is_none()
                || value["repository"].as_str() != scope.repository.as_deref()
                || matches!(
                    query,
                    Read::CanonicalIssues { .. } | Read::CanonicalIssue { .. }
                ))
        {
            return Err(Failure::new(403, "native_repository_scope"));
        }
    }
    if let Effect::CreateComment {
        command_id,
        digest,
        not_after,
        comment,
    } = &call.call
    {
        comment.validate()?;
        if !opaque_id(command_id)
            || digest.len() != 64
            || !digest
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
            || *not_after == 0
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        if scope.repository.as_deref() != Some(comment.repository.as_str()) {
            return Err(Failure::new(403, "native_repository_scope"));
        }
    }
    let mut cancelled = scope.cancelled.subscribe();
    let mut retired = state.retired.subscribe();
    let work = scope
        .credentials
        .with_token_operation(&scope.capability, async |token: &[u8]| match &call.call {
            Effect::Current => Ok(Value::Null),
            Effect::Read { query } => {
                state
                    .provider
                    .read_account(token, &scope.capability.account().account, query)
                    .await
            }
            Effect::CreateComment { command_id, digest, not_after, comment } => {
                if scope.write_claimed.swap(true, Ordering::SeqCst) {
                    return Err(Failure::new(409,"native_effect_already_claimed"));
                }
                let measured = Instant::now();
                let wall = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map_err(|_|Failure::unavailable())?;
                if *not_after > wall.as_secs().saturating_add(300) { return Err(Failure::new(400,"invalid_native_write")); }
                let deadline = measured + Duration::from_secs(*not_after).saturating_sub(wall);
                let (status, receipt, failure) = match state.provider.comment_account(token, &scope.capability.account().account, comment, Some(deadline)).await {
                    CommentOutcome::Created(receipt) => ("created", Some(receipt), None),
                    CommentOutcome::NotSent(failure) => ("not_sent", None, Some(failure)),
                    CommentOutcome::Indeterminate(failure) => ("indeterminate", None, Some(failure)),
                };
                let failure = failure.map(|f|json!({"status":f.status,"code":f.code,"retry_after":f.retry_after,"reset":f.reset}));
                Ok(json!({"command_id":command_id,"digest":digest,"state":status,"receipt":receipt,"failure":failure}))
            }
        });
    let observation = tokio::select! {
        biased;
        _ = cancelled.wait_for(|cancelled| *cancelled) => return Err(Failure::new(401, "native_operation_retired")),
        _ = retired.wait_for(|retired| *retired) => return Err(Failure::unavailable()),
        _ = tokio::time::sleep_until(scope.deadline.into()) => return Err(Failure::new(401, "native_operation_retired")),
        result = work => result.map_err(|_| Failure::new(401, "native_credential_unavailable"))??,
    };
    if !scope.current() || !state.alive.load(Ordering::SeqCst) {
        return Err(Failure::new(401, "native_operation_retired"));
    }
    let account = scope.capability.account();
    Ok(
        json!({"connection":{"connection":account.connection,"provider":account.provider,"origin":account.origin,"account":account.account},"repository":scope.repository,"observation":observation}),
    )
}

// Crash recovery is allowed only under the supervisor's exclusive profile lock,
// after a refused connect to the exact owned inode. Cleanup never unlinks a
// replacement endpoint or follows a symlink.
struct Socket {
    path: PathBuf,
    metadata: Metadata,
}
impl Drop for Socket {
    fn drop(&mut self) {
        if fs::symlink_metadata(&self.path).is_ok_and(|current| same_file(&self.metadata, &current))
        {
            let _ = fs::remove_file(&self.path);
        }
    }
}
fn same_file(a: &Metadata, b: &Metadata) -> bool {
    a.dev() == b.dev() && a.ino() == b.ino()
}
async fn bind(path: &Path) -> Result<(UnixListener, Socket)> {
    match fs::symlink_metadata(path) {
        Ok(old) => {
            if !old.file_type().is_socket()
                || old.uid() != unsafe { libc::geteuid() }
                || old.mode() & 0o077 != 0
            {
                return Err("native collaboration socket contains another resource".into());
            }
            match tokio::time::timeout(
                Duration::from_millis(250),
                tokio::net::UnixStream::connect(path),
            )
            .await
            {
                Ok(Err(error)) if error.raw_os_error() == Some(libc::ECONNREFUSED) => (),
                _ => return Err("native collaboration socket ownership uncertain".into()),
            }
            let current =
                fs::symlink_metadata(path).map_err(|_| "native collaboration socket changed")?;
            if !same_file(&old, &current) {
                return Err("native collaboration socket changed".into());
            }
            fs::remove_file(path).map_err(|_| "native collaboration socket unavailable")?;
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
        Err(_) => return Err("native collaboration socket unavailable".into()),
    }
    let listener = UnixListener::bind(path).map_err(|_| "native collaboration bind failed")?;
    let metadata = fs::symlink_metadata(path)
        .map_err(|_| "native collaboration socket identity unavailable")?;
    let socket = Socket {
        path: path.into(),
        metadata,
    };
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))
        .map_err(|_| "native collaboration socket permissions unavailable")?;
    Ok((listener, socket))
}
