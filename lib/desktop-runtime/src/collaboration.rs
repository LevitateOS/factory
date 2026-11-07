//! Authenticated native host effects for the Go product owner. Collaboration
//! selectors are semantic and require a native operation. The separate fixed
//! MCP route derives from a caller-supplied scope; Go must validate its ledger
//! capability and current run before asking for that secret.
use crate::credentials::{Credentials, Provider, TokenCapability};
use crate::github_provider::{
    BranchCreate, Client, Comment, DiscussionEdit, DiscussionLock, DiscussionState, Failure,
    InlineCreate, InlineDraft, IssueCreate, PendingReviewSubmit, PullCreate, PullMerge, Read,
    ReviewCreate, ReviewRequestChange, ThreadResolution, WriteOutcome,
};
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
use std::sync::{mpsc, Arc, Mutex, RwLock, Weak};
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
    execution_error: Arc<Mutex<Option<String>>>,
    retired: watch::Sender<bool>,
    scopes: Mutex<BTreeMap<String, Weak<Scope>>>,
    slots: Arc<Semaphore>,
    finalizations: jobs::Sender<Finalize>,
    executions: Arc<Mutex<crate::factory_provider::Executions>>,
    tools: RwLock<Option<Arc<crate::factory_provider::Tools>>>,
    preparations: Arc<crate::factory_provider::preparations::Preparations>,
    execution_accounts:
        Arc<RwLock<BTreeMap<String, Arc<crate::factory_provider::launch::Account>>>>,
    work: Arc<crate::factory_provider::work::Work>,
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
    fn require_execution(&self) -> Result<()> {
        match self
            .0
            .execution_error
            .lock()
            .map_err(|_| "native execution readiness unavailable")?
            .as_ref()
        {
            Some(error) => Err(error.clone()),
            None => Ok(()),
        }
    }
    pub(crate) fn register_recovery_checkout(&self, path: &Path) -> Result<String> {
        self.0
            .work
            .register(path)
            .map_err(|error| error.to_string())
    }
    pub(crate) fn execution_source(
        &self,
        intent: &crate::factory_provider::LaunchIntent,
        bundle: &[u8],
    ) -> Result<soda_execution_protocol::PreparedSourceRef> {
        self.require_execution()?;
        let (captured, source) = self
            .0
            .preparations
            .execution_material(intent)
            .map_err(|error| error.to_string())?;
        if captured != bundle {
            return Err("native launch source differs".into());
        }
        Ok(source)
    }
    pub(crate) fn enroll_execution_account(
        &self,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
    ) -> Result<()> {
        if !self.0.alive.load(Ordering::SeqCst) || !credentials.is_profile(&self.0.channel.profile)
        {
            return Err("native execution enrollment unavailable".into());
        }
        let account = crate::factory_provider::launch::Account::new(credentials, capability)
            .map_err(|error| error.to_string())?;
        let mut accounts = self
            .0
            .execution_accounts
            .write()
            .map_err(|_| "native execution accounts unavailable")?;
        if accounts.len() >= 8 && !accounts.contains_key(account.connection()) {
            return Err("native execution account capacity retained".into());
        }
        accounts.insert(account.connection().to_owned(), Arc::new(account));
        Ok(())
    }
    pub async fn select_execution_tools(
        &self,
        directory: &Path,
    ) -> Result<crate::factory_provider::ToolSelection> {
        if !self.0.alive.load(Ordering::SeqCst) {
            return Err("native tool owner retired".into());
        }
        let tools = Arc::new(
            crate::factory_provider::Tools::new(directory).map_err(|error| error.to_string())?,
        );
        let verified = tools.inspect().await.map_err(|error| error.to_string())?;
        let mut selection = self
            .0
            .tools
            .write()
            .map_err(|_| "native tool selection unavailable")?;
        if !self.0.alive.load(Ordering::SeqCst) {
            return Err("native tool owner retired".into());
        }
        *selection = Some(tools);
        Ok(verified)
    }
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
        self.0.preparations.cancel_all();
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
    pub fn start(
        channel: NativeChannel,
        provider: Client,
        alive: Arc<AtomicBool>,
        executions: Arc<Mutex<crate::factory_provider::Executions>>,
        execution_error: Arc<Mutex<Option<String>>>,
    ) -> Result<Self> {
        let signer = Signer::open(
            channel
                .socket
                .parent()
                .ok_or("native MCP profile unavailable")?,
        )?;
        let preparations = Arc::new(
            crate::factory_provider::preparations::Preparations::new(&channel, alive.clone())
                .map_err(|error| error.to_string())?,
        );
        let (retired, mut retirement) = watch::channel(false);
        let work = Arc::new(
            crate::factory_provider::work::Work::new(
                channel
                    .socket
                    .parent()
                    .ok_or("native recovery profile unavailable")?,
                &channel.profile,
                alive.clone(),
            )
            .map_err(|error| error.to_string())?,
        );
        let (finalizations, mut finalization_jobs) = jobs::channel::<Finalize>(MAX_OPERATIONS);
        let port = Port(Arc::new(State {
            channel,
            signer,
            provider,
            alive,
            execution_error,
            retired,
            scopes: Mutex::new(BTreeMap::new()),
            slots: Arc::new(Semaphore::new(MAX_OPERATIONS)),
            finalizations,
            executions,
            tools: RwLock::new(
                crate::factory_provider::Tools::discover()
                    .ok()
                    .map(Arc::new),
            ),
            preparations,
            execution_accounts: Arc::new(RwLock::new(BTreeMap::new())),
            work,
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
                if let Some(tools) = state.tools.read().ok().and_then(|tools| tools.clone()) {
                    // Verify a discovered installation outside bounded product RPCs.
                    // Browsing stays available while its initial ELF hash is read.
                    tasks.spawn(async move { let _ = tools.inspect().await; });
                }
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
        let mut joined = Ok(());
        if let Some(thread) = self.thread.take() {
            joined = thread
                .join()
                .map_err(|_| "native collaboration owner failed".to_owned())
                .and_then(|result| result);
        }
        let preparations = self
            .port
            .0
            .preparations
            .retire_all()
            .map_err(|error| error.to_string());
        joined.and(preparations)
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
    CreateInline {
        command_id: String,
        digest: String,
        not_after: u64,
        inline: InlineCreate,
    },
    MergePull {
        command_id: String,
        digest: String,
        not_after: u64,
        merge: PullMerge,
    },
    AppendPendingThread {
        command_id: String,
        digest: String,
        not_after: u64,
        inline: InlineDraft,
    },
    CreateBranch {
        command_id: String,
        digest: String,
        not_after: u64,
        branch: BranchCreate,
    },
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
    CreateIssue {
        command_id: String,
        digest: String,
        not_after: u64,
        issue: IssueCreate,
    },
    CreatePull {
        command_id: String,
        digest: String,
        not_after: u64,
        pull: PullCreate,
    },
    CreateReview {
        command_id: String,
        digest: String,
        not_after: u64,
        review: ReviewCreate,
    },
    SubmitPendingReview {
        command_id: String,
        digest: String,
        not_after: u64,
        review: PendingReviewSubmit,
    },
    SetDiscussionState {
        command_id: String,
        digest: String,
        not_after: u64,
        state: DiscussionState,
    },
    EditDiscussion {
        command_id: String,
        digest: String,
        not_after: u64,
        edit: DiscussionEdit,
    },
    SetDiscussionLock {
        command_id: String,
        digest: String,
        not_after: u64,
        lock: DiscussionLock,
    },
    SetThreadResolution {
        command_id: String,
        digest: String,
        not_after: u64,
        thread: ThreadResolution,
    },
    SetReviewRequest {
        command_id: String,
        digest: String,
        not_after: u64,
        request: ReviewRequestChange,
    },
}
impl Effect {
    fn mutation(&self) -> Option<(&str, &str, u64, &str)> {
        match self {
            Self::CreateInline {
                command_id,
                digest,
                not_after,
                inline,
            } => Some((command_id, digest, *not_after, &inline.repository)),
            Self::MergePull {
                command_id,
                digest,
                not_after,
                merge,
            } => Some((command_id, digest, *not_after, &merge.repository)),
            Self::AppendPendingThread {
                command_id,
                digest,
                not_after,
                inline,
            } => Some((command_id, digest, *not_after, &inline.repository)),
            Self::CreateBranch {
                command_id,
                digest,
                not_after,
                branch,
            } => Some((command_id, digest, *not_after, &branch.repository)),
            Self::CreateComment {
                command_id,
                digest,
                not_after,
                comment,
            } => Some((command_id, digest, *not_after, &comment.repository)),
            Self::CreateIssue {
                command_id,
                digest,
                not_after,
                issue,
            } => Some((command_id, digest, *not_after, &issue.repository)),
            Self::CreatePull {
                command_id,
                digest,
                not_after,
                pull,
            } => Some((command_id, digest, *not_after, &pull.repository)),
            Self::CreateReview {
                command_id,
                digest,
                not_after,
                review,
            } => Some((command_id, digest, *not_after, &review.repository)),
            Self::SubmitPendingReview {
                command_id,
                digest,
                not_after,
                review,
            } => Some((command_id, digest, *not_after, &review.repository)),
            Self::SetDiscussionState {
                command_id,
                digest,
                not_after,
                state,
            } => Some((command_id, digest, *not_after, &state.repository)),
            Self::EditDiscussion {
                command_id,
                digest,
                not_after,
                edit,
            } => Some((command_id, digest, *not_after, &edit.repository)),
            Self::SetDiscussionLock {
                command_id,
                digest,
                not_after,
                lock,
            } => Some((command_id, digest, *not_after, &lock.repository)),
            Self::SetThreadResolution {
                command_id,
                digest,
                not_after,
                thread,
            } => Some((command_id, digest, *not_after, &thread.repository)),
            Self::SetReviewRequest {
                command_id,
                digest,
                not_after,
                request,
            } => Some((command_id, digest, *not_after, &request.repository)),
            _ => None,
        }
    }
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
    let execution = target == Some("/api/native/execution");
    let preparation = target == Some("/api/native/preparation");
    if request.method() != "POST"
        || !matches!(
            target,
            Some(
                "/api/native/collaboration"
                    | "/api/native/mcp-sign"
                    | "/api/native/mcp-verify"
                    | "/api/native/execution"
                    | "/api/native/preparation"
            )
        )
        || request.uri().scheme().is_some()
        || request.uri().authority().is_some()
        || one(&request, "host") != Some("soda-native")
        || one(&request, "content-type") != Some("application/json")
    {
        return Err(Failure::invalid());
    }
    let body_limit = if preparation {
        1024 * 1024
    } else if execution {
        512 * 1024
    } else if signing || verifying {
        8192
    } else {
        512 * 1024
    };
    let body = Limited::new(request.into_body(), body_limit)
        .collect()
        .await
        .map_err(|_| Failure::invalid())?
        .to_bytes();
    if preparation {
        #[derive(Deserialize)]
        #[serde(tag = "operation", rename_all = "snake_case", deny_unknown_fields)]
        enum PreparationCall {
            Prepare { request: Value },
            Candidate { request: Value, source: Value },
            Context { request: Value },
            Inspect { address: Value },
            Retire { address: Value },
        }
        let call: PreparationCall =
            serde_json::from_slice(&body).map_err(|_| Failure::invalid())?;
        if matches!(
            &call,
            PreparationCall::Prepare { .. } | PreparationCall::Candidate { .. }
        ) && state
            .execution_error
            .lock()
            .map_or(true, |error| error.is_some())
        {
            return Err(Failure::new(503, "native_execution_unavailable"));
        }
        let owner = state.preparations.clone();
        return tokio::task::spawn_blocking(move || match call {
            PreparationCall::Prepare { request } => owner.prepare(request),
            PreparationCall::Candidate { request, source } => {
                owner.prepare_candidate(request, &source)
            }
            PreparationCall::Context { request } => owner.context(&request),
            PreparationCall::Inspect { address } => owner.inspect(&address),
            PreparationCall::Retire { address } => owner.retire(&address),
        })
        .await
        .map_err(|_| Failure::unavailable())?
        .map_err(|error| {
            eprintln!("native preparation callback: {error}");
            Failure::new(503, "native_preparation_unavailable")
        });
    }
    if execution {
        let call: crate::factory_provider::port::Call =
            serde_json::from_slice(&body).map_err(|_| Failure::invalid())?;
        if matches!(&call, crate::factory_provider::port::Call::InspectMuseLogin) {
            let connection = crate::factory_provider::muse_connection(&state.channel.profile);
            let available = crate::factory_provider::muse_helper().is_ok()
                && state.tools.read().is_ok_and(|tools| tools.is_some())
                && state
                    .execution_error
                    .lock()
                    .is_ok_and(|error| error.is_none());
            return Ok(json!({"connection":connection,"label":"This Muse login",
                "native":{"profile":state.channel.profile,"authentication":"muse_login"},
                "execution_available":available}));
        }
        if matches!(&call, crate::factory_provider::port::Call::Launch { .. })
            && state
                .execution_error
                .lock()
                .map_or(true, |error| error.is_some())
        {
            return Err(Failure::new(503, "native_execution_unavailable"));
        }
        if let crate::factory_provider::port::Call::InspectAccount { connection } = call {
            let account = state
                .execution_accounts
                .read()
                .map_err(|_| Failure::unavailable())?
                .get(&connection)
                .cloned()
                .ok_or_else(Failure::unavailable)?;
            let observed = account
                .observe(&state.channel.profile)
                .await
                .map_err(|_| Failure::unavailable())?;
            if !state.alive.load(Ordering::SeqCst) {
                return Err(Failure::unavailable());
            }
            return Ok(observed);
        }
        if let crate::factory_provider::port::Call::Recover {
            intent,
            checkout_id,
        } = call
        {
            let owner = state.work.clone();
            let preparations = state.preparations.clone();
            return tokio::task::spawn_blocking(move || {
                owner.recover(&intent, &checkout_id, &preparations)
            })
            .await
            .map_err(|_| Failure::unavailable())?
            .map_err(|error| {
                eprintln!("native work recovery: {error}");
                Failure::new(503, "native_work_recovery_unavailable")
            });
        }
        if let crate::factory_provider::port::Call::Launch {
            intent,
            prompt,
            capability,
            secret,
            scope_digest,
            tools,
        } = call
        {
            if !state
                .signer
                .verify(&capability, &scope_digest, &secret)
                .map_err(|_| Failure::unavailable())?
            {
                return Err(Failure::invalid());
            }
            return crate::factory_provider::launch::launch(
                state.channel.clone(),
                state.alive.clone(),
                state.executions.clone(),
                state.preparations.clone(),
                state.execution_accounts.clone(),
                crate::factory_provider::launch::Input {
                    intent,
                    prompt,
                    capability,
                    secret,
                    tools,
                },
            )
            .await
            .map_err(|error| {
                eprintln!("native launch callback: {error}");
                Failure::new(503, "native_launch_unavailable")
            });
        }
        let tools = state
            .tools
            .read()
            .map_err(|_| Failure::unavailable())?
            .clone();
        return crate::factory_provider::port::call(
            state.executions.clone(),
            state.alive.clone(),
            tools,
            state
                .channel
                .socket
                .parent()
                .ok_or_else(Failure::unavailable)?
                .to_path_buf(),
            state.channel.profile.clone(),
            call,
        )
        .await
        .map_err(|error| {
            eprintln!("native execution callback: {error}");
            Failure::new(503, "native_execution_unavailable")
        });
    }
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
    let descriptor_limit = match &call.call {
        Effect::Read {
            query: Read::InlinePreparation { .. },
        } => 64 * 1024,
        _ => 8192,
    };
    if !matches!(
        &call.call,
        Effect::CreateComment { .. }
            | Effect::MergePull { .. }
            | Effect::CreateInline { .. }
            | Effect::AppendPendingThread { .. }
            | Effect::CreateIssue { .. }
            | Effect::CreatePull { .. }
            | Effect::CreateReview { .. }
            | Effect::SubmitPendingReview { .. }
            | Effect::EditDiscussion { .. }
            | Effect::SetDiscussionLock { .. }
    ) && body.len() > descriptor_limit
    {
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
    if let Some((command_id, digest, not_after, repository)) = call.call.mutation() {
        match &call.call {
            Effect::CreateInline { inline, .. } => inline.validate()?,
            Effect::MergePull { merge, .. } => merge.validate()?,
            Effect::CreateComment { comment, .. } => comment.validate()?,
            Effect::AppendPendingThread { inline, .. } => inline.validate()?,
            Effect::CreateIssue { issue, .. } => issue.validate()?,
            Effect::CreateBranch { branch, .. } => branch.validate()?,
            Effect::CreatePull { pull, .. } => pull.validate()?,
            Effect::CreateReview { review, .. } => review.validate()?,
            Effect::SubmitPendingReview { review, .. } => review.validate()?,
            Effect::SetDiscussionState { state, .. } => state.validate()?,
            Effect::EditDiscussion { edit, .. } => edit.validate()?,
            Effect::SetDiscussionLock { lock, .. } => lock.validate()?,
            Effect::SetThreadResolution { thread, .. } => thread.validate()?,
            Effect::SetReviewRequest { request, .. } => request.validate()?,
            _ => unreachable!(),
        }
        if !opaque_id(command_id)
            || digest.len() != 64
            || !digest
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
            || not_after == 0
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        if scope.repository.as_deref() != Some(repository) {
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
            effect => mutate(&state.provider, &scope, token, effect).await,
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

async fn mutate(
    provider: &Client,
    scope: &Scope,
    token: &[u8],
    effect: &Effect,
) -> std::result::Result<Value, Failure> {
    let (command_id, digest, not_after, _) = effect.mutation().ok_or_else(Failure::invalid)?;
    if scope.write_claimed.swap(true, Ordering::SeqCst) {
        return Err(Failure::new(409, "native_effect_already_claimed"));
    }
    let measured = Instant::now();
    let wall = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| Failure::unavailable())?;
    if not_after > wall.as_secs().saturating_add(300) {
        return Err(Failure::new(400, "invalid_native_write"));
    }
    let deadline = measured + Duration::from_secs(not_after).saturating_sub(wall);
    let account = &scope.capability.account().account;
    let (success, outcome) = match effect {
        Effect::SubmitPendingReview { review, .. } => (
            "applied",
            provider
                .review_submit_account(token, account, review, Some(deadline))
                .await,
        ),
        Effect::SetDiscussionState { state, .. } => (
            "applied",
            provider
                .state_account(token, account, state, Some(deadline))
                .await,
        ),
        Effect::EditDiscussion { edit, .. } => (
            "applied",
            provider
                .edit_account(token, account, edit, Some(deadline))
                .await,
        ),
        Effect::SetDiscussionLock { lock, .. } => (
            "applied",
            provider
                .lock_account(token, account, lock, Some(deadline))
                .await,
        ),
        Effect::SetThreadResolution { thread, .. } => (
            "applied",
            provider
                .thread_resolution_account(token, account, thread, Some(deadline))
                .await,
        ),
        Effect::SetReviewRequest { request, .. } => (
            "applied",
            provider
                .review_request_account(token, account, request, Some(deadline))
                .await,
        ),
        Effect::MergePull { merge, .. } => (
            "applied",
            provider
                .merge_account(token, account, merge, Some(deadline))
                .await,
        ),
        Effect::CreateInline { inline, .. } => (
            "created",
            provider
                .inline_create_account(token, account, inline, Some(deadline))
                .await
                .into(),
        ),
        Effect::AppendPendingThread { inline, .. } => (
            "created",
            provider
                .inline_draft_account(token, account, inline, Some(deadline))
                .await
                .into(),
        ),
        Effect::CreateBranch { branch, .. } => (
            "created",
            provider
                .branch_create_account(token, account, branch, Some(deadline))
                .await
                .into(),
        ),
        Effect::CreateComment { comment, .. } => (
            "created",
            provider
                .comment_account(token, account, comment, Some(deadline))
                .await
                .into(),
        ),
        Effect::CreateIssue { issue, .. } => (
            "created",
            provider
                .issue_create_account(token, account, issue, Some(deadline))
                .await
                .into(),
        ),
        Effect::CreatePull { pull, .. } => (
            "created",
            provider
                .pull_create_account(token, account, pull, Some(deadline))
                .await
                .into(),
        ),
        Effect::CreateReview { review, .. } => (
            "created",
            provider
                .review_create_account(token, account, review, Some(deadline))
                .await
                .into(),
        ),
        _ => return Err(Failure::invalid()),
    };
    let (state, receipt, failure) = match outcome {
        WriteOutcome::Applied(receipt) => (success, Some(receipt), None),
        WriteOutcome::NotSent(failure) => ("not_sent", None, Some(failure)),
        WriteOutcome::Indeterminate(failure) => ("indeterminate", None, Some(failure)),
    };
    let failure = failure.map(
        |f| json!({"status":f.status,"code":f.code,"retry_after":f.retry_after,"reset":f.reset}),
    );
    Ok(
        json!({"command_id":command_id,"digest":digest,"state":state,"receipt":receipt,"failure":failure}),
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
