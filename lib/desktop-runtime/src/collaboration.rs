//! Authenticated native host effects for the Go product owner. Collaboration
//! selectors are semantic and require a native operation. The separate fixed
//! MCP route derives from a caller-supplied scope; Go must validate its ledger
//! capability and current run before asking for that secret.
use crate::credentials::{Credentials, Provider, TokenCapability};
use crate::github_provider::{
    BranchCreate, BranchPublish, Client, Comment, DiscussionEdit, DiscussionLock, DiscussionState,
    Failure, InlineCreate, InlineDraft, IssueCreate, IssueDependency, PendingReviewSubmit, PullCreate, PullMerge,
    Read, ReviewCreate, ReviewRequestChange, ThreadResolution, WriteOperation, WriteOutcome,
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

mod receipts;

type Result<T> = std::result::Result<T, String>;
const MAX_OPERATIONS: usize = 16;
const LIFETIME: Duration = Duration::from_secs(20);
const CALL_TIMEOUT: Duration = Duration::from_secs(8);
const CONNECTION_TIMEOUT: Duration = Duration::from_secs(16);

struct Scope {
    credentials: Arc<Credentials>,
    capability: TokenCapability,
    fountain: Option<Arc<crate::fountain_provider::Client>>,
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
    execution_error: Arc<Mutex<crate::supervisor::ExecutionReadiness>>,
    retired: watch::Sender<bool>,
    scopes: Mutex<BTreeMap<String, Weak<Scope>>>,
    background_credentials: RwLock<Option<Arc<Credentials>>>,
    slots: Arc<Semaphore>,
    finalizations: jobs::Sender<Finalize>,
    executions: Arc<Mutex<crate::factory_provider::Executions>>,
    judgments: Mutex<crate::factory_provider::judgment::Judgments>,
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
            .error()
        {
            Some(error) => Err(error.to_owned()),
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
            crate::factory_provider::Tools::new(
                Some(directory),
                self.0
                    .channel
                    .socket
                    .parent()
                    .ok_or("native profile unavailable")?,
            )
            .map_err(|error| error.to_string())?,
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
    pub fn execution_tools(&self) -> Result<Arc<crate::factory_provider::Tools>> {
        if !self.0.alive.load(Ordering::SeqCst) { return Err("native tool owner retired".into()); }
        self.0.tools.read().map_err(|_| "native tools unavailable")?
            .clone().ok_or_else(|| "native tools unavailable".into())
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
    pub fn enroll_credentials(&self, credentials: Arc<Credentials>) -> Result<()> {
        if !self.0.alive.load(Ordering::SeqCst) || !credentials.is_profile(&self.0.channel.profile) {
            return Err("native credential owner unavailable".into());
        }
        *self.0.background_credentials.write().map_err(|_| "native credential owner unavailable")? = Some(credentials);
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
        self.admit_scoped(products, credentials, capability, repository, None)
    }
    pub(crate) fn admit_fountain(
        &self,
        products: Products,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        repository: String,
        client: Arc<crate::fountain_provider::Client>,
    ) -> Result<NativeRequest> {
        self.admit_scoped(products, credentials, capability, Some(repository), Some(client))
    }
    fn admit_scoped(
        &self,
        products: Products,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        repository: Option<String>,
        fountain: Option<Arc<crate::fountain_provider::Client>>,
    ) -> Result<NativeRequest> {
        let account = capability.account();
        let provider = match &fountain {
            None => account.provider == Provider::Github && account.origin == "https://github.com",
            Some(client) => account.provider == Provider::Fountain
                && client.connection() == account.connection
                && client.origin() == account.origin && repository.is_some(),
        };
        if !self.0.alive.load(Ordering::SeqCst)
            || !credentials.is_profile(&self.0.channel.profile)
            || !provider
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
            credentials: credentials.clone(),
            capability,
            fountain,
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
        self.enroll_credentials(credentials)?;
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
        execution_error: Arc<Mutex<crate::supervisor::ExecutionReadiness>>,
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
        let tools = crate::factory_provider::Tools::discover(
            channel
                .socket
                .parent()
                .ok_or("native profile unavailable")?,
        )
        .ok()
        .map(Arc::new);
        let port = Port(Arc::new(State {
            channel,
            signer,
            provider,
            alive,
            execution_error,
            retired,
            scopes: Mutex::new(BTreeMap::new()),
            background_credentials: RwLock::new(None),
            slots: Arc::new(Semaphore::new(MAX_OPERATIONS)),
            finalizations,
            executions,
            judgments: Mutex::new(crate::factory_provider::judgment::Judgments::default()),
            tools: RwLock::new(tools),
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
                let tool_state = state.clone();
                tasks.spawn(async move {
                    let mut refresh = tokio::time::interval(crate::factory_provider::Tools::REFRESH);
                    refresh.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
                    loop {
                        refresh.tick().await;
                        let tools = tool_state.tools.read().ok().and_then(|tools| tools.clone());
                        if let Some(tools) = tools {
                            // Check/update the selected installation outside product RPCs.
                            // Retirement cancels pending downloads with this owner.
                            let _ = tools.refresh().await;
                        }
                    }
                });
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
                                let _ = tokio::time::timeout(CONNECTION_TIMEOUT, http1::Builder::new()
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
                // Prepared checkout custody can still need the credential
                // transport hosted on this executor to settle a host claim.
                // Join it while this reactor is alive, after refusing ingress.
                let preparations = state.preparations.clone();
                let retired = tokio::task::spawn_blocking(move || preparations.retire_all())
                    .await.map_err(|_| "native preparation owner failed".to_owned())?
                    .map_err(|error| error.to_string());
                result.and(retired)
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
        let judgments = self
            .port
            .0
            .judgments
            .lock()
            .map_err(|_| "native judgment owner unavailable".to_owned())
            .and_then(|mut owner| owner.retire().map_err(|error| error.to_string()));
        joined.and(preparations).and(judgments)
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
        if let Some(client) = &job.scope.fountain {
            client.product_current(job.scope.repository.as_deref().ok_or("native repository unavailable")?)
                .await.map_err(|_| "Fountain enrollment is no longer current".to_owned())?;
        }
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
struct BackgroundCall {
    #[serde(default)]
    fountain_repository: Option<String>,
    connection: String,
    account: String,
    repository: String,
    call: BackgroundEffect,
}
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum BackgroundEffect {
    WriteOutcome { command_id: String, digest: String, not_after: u64 },
    CreateComment {
        command_id: String,
        digest: String,
        not_after: u64,
        comment: Comment,
    },
    SetIssueDependency {
        command_id: String,
        digest: String,
        not_after: u64,
        dependency: IssueDependency,
    },
    EditDiscussion {
        command_id: String,
        digest: String,
        not_after: u64,
        edit: DiscussionEdit,
    },
    CreateIssue {
        command_id: String,
        digest: String,
        not_after: u64,
        issue: IssueCreate,
    },
    MergePull {
        command_id: String,
        digest: String,
        not_after: u64,
        merge: PullMerge,
    },
    PublishBranch {
        command_id: String,
        digest: String,
        not_after: u64,
        branch: BranchPublish,
    },
    CreatePull {
        command_id: String,
        digest: String,
        not_after: u64,
        pull: PullCreate,
    },
    Current,
    Read {
        query: Read,
        #[serde(default)]
        version: u8,
    },
    CreateReview {
        command_id: String,
        digest: String,
        not_after: u64,
        review: ReviewCreate,
    },
}

async fn background_read(
    state: &State,
    body: &[u8],
    started: Instant,
) -> std::result::Result<Value, Failure> {
    let call: BackgroundCall = serde_json::from_slice(body).map_err(|_| Failure::invalid())?;
    if matches!(&call.call, BackgroundEffect::Read { version, .. } if *version > 1) { return Err(Failure::invalid()); }
    if !opaque_id(&call.connection) || !remote_id(&call.account) || !remote_id(&call.repository) {
        return Err(Failure::invalid());
    }
    let effect = match call.call {
        BackgroundEffect::WriteOutcome {command_id,digest,not_after} => Operation::Receipt(Descriptor {command_id,digest,not_after}),
        BackgroundEffect::CreateComment {
            command_id,
            digest,
            not_after,
            comment,
        } if comment.subject == "issue" => Operation::Mutation(Command {
            descriptor: Descriptor { command_id, digest, not_after },
            operation: WriteOperation::CreateComment(comment),
        }),
        BackgroundEffect::SetIssueDependency {
            command_id,
            digest,
            not_after,
            dependency,
        } => Operation::Mutation(Command {
            descriptor: Descriptor {
                command_id,
                digest,
                not_after,
            },
            operation: WriteOperation::SetIssueDependency(dependency),
        }),
        BackgroundEffect::EditDiscussion {
            command_id,
            digest,
            not_after,
            edit,
        } if edit.subject == "issue" => Operation::Mutation(Command {
            descriptor: Descriptor {
                command_id,
                digest,
                not_after,
            },
            operation: WriteOperation::EditDiscussion(edit),
        }),
        BackgroundEffect::CreateIssue {
            command_id,
            digest,
            not_after,
            issue,
        } => Operation::Mutation(Command {
            descriptor: Descriptor {
                command_id,
                digest,
                not_after,
            },
            operation: WriteOperation::CreateIssue(issue),
        }),
        BackgroundEffect::MergePull {
            command_id,
            digest,
            not_after,
            merge,
        } => Operation::Mutation(Command {
            descriptor: Descriptor {
                command_id,
                digest,
                not_after,
            },
            operation: WriteOperation::MergePull(merge),
        }),
        BackgroundEffect::PublishBranch {
            command_id,
            digest,
            not_after,
            branch,
        } => Operation::Mutation(Command {
            descriptor: Descriptor {
                command_id,
                digest,
                not_after,
            },
            operation: WriteOperation::PublishBranch(branch),
        }),
        BackgroundEffect::CreatePull {
            command_id,
            digest,
            not_after,
            pull,
        } => Operation::Mutation(Command {
            descriptor: Descriptor {
                command_id,
                digest,
                not_after,
            },
            operation: WriteOperation::CreatePull(pull),
        }),
        BackgroundEffect::CreateReview {
            command_id,
            digest,
            not_after,
            review,
        } => Operation::Mutation(Command {
            descriptor: Descriptor {
                command_id,
                digest,
                not_after,
            },
            operation: WriteOperation::CreateReview(review),
        }),
        BackgroundEffect::Current => Operation::Current,
        BackgroundEffect::Read { query, version }
            if matches!(
                &query,
                Read::PullFiles { base, head, page: 1, limit: 1..=10, .. }
                    if !base.is_empty() && !head.is_empty()
            ) =>
        {
            Operation::Read(query, version)
        }
        BackgroundEffect::Read { query, version }
            if matches!(
                query,
                Read::Identity
                    | Read::OperationGrant { .. }
                    | Read::TargetTip { .. }
                    | Read::SourceBundle { .. }
                    | Read::SetupSource { .. }
                    | Read::PublicationRefs { .. }
                    | Read::PullBranches { .. }
                    | Read::Pull { .. }
                    | Read::Issue { .. }
                    | Read::Issues { .. }
                    | Read::IssueCreatorPermission { .. }
                    | Read::Repository { .. }
                    | Read::AutomationInputs { .. }
                    | Read::AutomationChecks { .. }
                    | Read::AutomationReview { .. }
                    | Read::DeliveryOpinions { .. }
                    | Read::DeliveryDiscussion { .. }
                    | Read::ExternalPullCandidates { .. }
                    | Read::PullComments { .. }
                    | Read::PullReviews { .. }
                    | Read::PullThreads { .. }
                    | Read::MergePreparation { .. }
                    | Read::MergeReadiness { .. }
                    | Read::MergeCompletion { .. }
                    | Read::PullRequiredChecks { .. }
                    | Read::WriteCapabilities { .. }
                    | Read::PullReview { .. }
                    | Read::PullPendingReviewSnapshot { .. }
            ) =>
        {
            Operation::Read(query, version)
        }
        _ => return Err(Failure::new(403, "native_background_read_unavailable")),
    };
    if !matches!(
        &effect,
        Operation::Mutation(Command {
            operation: WriteOperation::CreateReview(..)
                | WriteOperation::CreateComment(..)
                | WriteOperation::CreateIssue(..)
                | WriteOperation::PublishBranch(..)
                | WriteOperation::CreatePull(..)
                | WriteOperation::MergePull(..),
            ..
        })
    ) && body.len() > 8192
    {
        return Err(Failure::invalid());
    }
    let credentials = state
        .background_credentials
        .read()
        .map_err(|_| Failure::unavailable())?
        .clone()
        .ok_or_else(|| Failure::new(401, "native_account_owner_unavailable"))?;
    let permit = state
        .slots
        .clone()
        .try_acquire_owned()
        .map_err(|_| Failure::unavailable())?;
    let deadline = started + effect.timeout();
    let work = async {
        let capability = credentials
            .capability(&call.connection)
            .await
            .map_err(|_| Failure::new(401, "native_credential_unavailable"))?;
        let account = capability.account();
        if !credentials.is_profile(&state.channel.profile) || account.account != call.account {
            return Err(Failure::new(401, "native_account_changed"));
        }
        let fountain = match account.provider {
            Provider::Github if account.origin == "https://github.com" && call.fountain_repository.is_none() => None,
            Provider::Fountain => {
                let name = call.fountain_repository.as_deref().ok_or_else(Failure::invalid)?;
                let (owner, repository) = name.split_once('/').ok_or_else(Failure::invalid)?;
                let profile = state.channel.socket.parent().ok_or_else(Failure::invalid)?;
                let retained = credentials.capability(&call.connection).await.map_err(|_| Failure::new(401,"native_credential_unavailable"))?;
                let client = crate::fountain_provider::retained_client(credentials.clone(), retained, profile,
                    &state.channel.profile, owner, repository).await.map_err(|error| Failure::new(error.status,error.code))?;
                client.product_current(&call.repository).await.map_err(|error| Failure::new(error.status,error.code))?;
                Some(Arc::new(client))
            }
            _ => return Err(Failure::new(401,"native_account_changed")),
        };
        let (cancelled, _) = watch::channel(false);
        let scope = Arc::new(Scope {
            credentials,
            capability,
            fountain,
            repository: Some(call.repository),
            deadline,
            cancelled,
            write_claimed: AtomicBool::new(false),
            _permit: permit,
        });
        observe_scope(state, scope, &effect).await
    };
    tokio::time::timeout_at(deadline.into(), work)
        .await
        .map_err(|_| Failure::unavailable())?
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
    WriteOutcome { command_id: String, digest: String, not_after: u64 },
    SetIssueDependency {
        command_id: String,
        digest: String,
        not_after: u64,
        dependency: IssueDependency,
    },
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
    PublishBranch {
        command_id: String,
        digest: String,
        not_after: u64,
        branch: BranchPublish,
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
        #[serde(default)]
        version: u8,
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
// Wire repetition ends here: admission and custody see one descriptor per command.
#[derive(Clone, serde::Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Descriptor {
    command_id: String,
    digest: String,
    not_after: u64,
}
struct Command {
    descriptor: Descriptor,
    operation: WriteOperation,
}
enum Operation {
    Receipt(Descriptor),
    Current,
    Read(Read, u8),
    Mutation(Command),
}
impl Operation {
    fn timeout(&self) -> Duration {
        match self {
            Self::Read(query, _) => query.timeout(),
            _ => CALL_TIMEOUT,
        }
    }
}
impl From<Effect> for Operation {
    fn from(effect: Effect) -> Self {
        match effect {
            Effect::WriteOutcome {command_id,digest,not_after} => Self::Receipt(Descriptor {command_id,digest,not_after}),
            Effect::Current => Self::Current,
            Effect::Read { query, version } => Self::Read(query, version),
            Effect::SetIssueDependency {
                command_id,
                digest,
                not_after,
                dependency,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::SetIssueDependency(dependency),
            }),
            Effect::CreateInline {
                command_id,
                digest,
                not_after,
                inline,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::CreateInline(inline),
            }),
            Effect::MergePull {
                command_id,
                digest,
                not_after,
                merge,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::MergePull(merge),
            }),
            Effect::AppendPendingThread {
                command_id,
                digest,
                not_after,
                inline,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::AppendPendingThread(inline),
            }),
            Effect::PublishBranch {
                command_id,
                digest,
                not_after,
                branch,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::PublishBranch(branch),
            }),
            Effect::CreateBranch {
                command_id,
                digest,
                not_after,
                branch,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::CreateBranch(branch),
            }),
            Effect::CreateComment {
                command_id,
                digest,
                not_after,
                comment,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::CreateComment(comment),
            }),
            Effect::CreateIssue {
                command_id,
                digest,
                not_after,
                issue,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::CreateIssue(issue),
            }),
            Effect::CreatePull {
                command_id,
                digest,
                not_after,
                pull,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::CreatePull(pull),
            }),
            Effect::CreateReview {
                command_id,
                digest,
                not_after,
                review,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::CreateReview(review),
            }),
            Effect::SubmitPendingReview {
                command_id,
                digest,
                not_after,
                review,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::SubmitPendingReview(review),
            }),
            Effect::SetDiscussionState {
                command_id,
                digest,
                not_after,
                state,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::SetDiscussionState(state),
            }),
            Effect::EditDiscussion {
                command_id,
                digest,
                not_after,
                edit,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::EditDiscussion(edit),
            }),
            Effect::SetDiscussionLock {
                command_id,
                digest,
                not_after,
                lock,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::SetDiscussionLock(lock),
            }),
            Effect::SetThreadResolution {
                command_id,
                digest,
                not_after,
                thread,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::SetThreadResolution(thread),
            }),
            Effect::SetReviewRequest {
                command_id,
                digest,
                not_after,
                request,
            } => Self::Mutation(Command {
                descriptor: Descriptor {
                    command_id,
                    digest,
                    not_after,
                },
                operation: WriteOperation::SetReviewRequest(request),
            }),
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
    let allowance = match one(&request, "x-factory-wire-allowance") {
        Some(value) => value.parse::<u32>().ok().filter(|limit| *limit <= 384),
        None => Some(384),
    };
    let work = crate::provider_contract::operation::Operation::new(allowance.unwrap_or(0), CONNECTION_TIMEOUT);
    let response = tokio::time::timeout(CONNECTION_TIMEOUT, work.owned_scope(dispatch(request, &state)))
        .await
        .unwrap_or_else(|_| Err(Failure::new(504, "native_collaboration_deadline")));
    let attempts = work.close();
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
    response.headers_mut().insert("x-factory-wire-attempts", attempts.to_string().parse().unwrap());
    Ok(response)
}
async fn dispatch(
    request: Request<Incoming>,
    state: &State,
) -> std::result::Result<Value, Failure> {
    let started = Instant::now();
    let deadline = started + CALL_TIMEOUT;
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
    let background = target == Some("/api/native/collaboration/background");
    let signing = target == Some("/api/native/mcp-sign");
    let verifying = target == Some("/api/native/mcp-verify");
    let execution = target == Some("/api/native/execution");
    let preparation = target == Some("/api/native/preparation");
    if request.method() != "POST"
        || !matches!(
            target,
            Some(
                "/api/native/collaboration"
                    | "/api/native/collaboration/background"
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
        soda_execution_protocol::MAX_PREPARATION_REQUEST
    } else if execution {
        soda_execution_protocol::MAX_MUSE_INPUT
    } else if background {
        1024 * 1024
    } else if signing || verifying {
        8192
    } else {
        512 * 1024
    };
    let body = tokio::time::timeout_at(
        deadline.into(),
        Limited::new(request.into_body(), body_limit).collect(),
    )
    .await
    .map_err(|_| Failure::new(504, "native_collaboration_deadline"))?
    .map_err(|_| Failure::invalid())?
    .to_bytes();
    if background {
        return background_read(state, &body, started).await;
    }
    if preparation {
        let call = serde_json::from_slice(&body).map_err(|_| Failure::invalid())?;
        let execution_available = state
            .execution_error
            .lock()
            .is_ok_and(|readiness| readiness.is_ready());
        let credentials = state.background_credentials.read().map_err(|_|Failure::unavailable())?.clone();
        let work = state
            .preparations
            .clone()
            .callback(call, execution_available, credentials);
        return tokio::time::timeout_at(deadline.into(), work)
            .await
            .map_err(|_| Failure::new(504, "native_collaboration_deadline"))?;
    }
    if execution {
        let call = serde_json::from_slice(&body).map_err(|_| Failure::invalid())?;
        let callbacks = crate::factory_provider::port::Callbacks {
            channel: &state.channel,
            signer: &state.signer,
            alive: &state.alive,
            execution_error: &state.execution_error,
            executions: &state.executions,
            judgments: &state.judgments,
            tools: &state.tools,
            preparations: &state.preparations,
            execution_accounts: &state.execution_accounts,
            credentials: &state.background_credentials,
            work: &state.work,
        };
        return tokio::time::timeout_at(deadline.into(), callbacks.call(call))
            .await
            .map_err(|_| Failure::new(504, "native_collaboration_deadline"))?;
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
    let effect = Operation::from(call.call);
    let descriptor_limit = match &effect {
        Operation::Read(query, _) => query.request_limit(),
        _ => 8192,
    };
    if !matches!(
        &effect,
        Operation::Mutation(Command {
            operation: WriteOperation::CreateComment(..)
                | WriteOperation::PublishBranch(..)
                | WriteOperation::MergePull(..)
                | WriteOperation::CreateInline(..)
                | WriteOperation::AppendPendingThread(..)
                | WriteOperation::CreateIssue(..)
                | WriteOperation::CreatePull(..)
                | WriteOperation::CreateReview(..)
                | WriteOperation::SubmitPendingReview(..)
                | WriteOperation::SetIssueDependency(..)
                | WriteOperation::EditDiscussion(..)
                | WriteOperation::SetDiscussionLock(..),
            ..
        })
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
    tokio::time::timeout_at(
        (started + effect.timeout()).into(),
        observe_scope(state, scope, &effect),
    )
    .await
    .map_err(|_| Failure::new(504, "native_collaboration_deadline"))?
}

fn validate_mutation(scope: &Scope, command: &Command) -> std::result::Result<(), Failure> {
    let repository = command.operation.validate()?;
    let Descriptor {
        command_id,
        digest,
        not_after,
    } = &command.descriptor;
    if !opaque_id(command_id)
        || digest.len() != 64
        || !digest
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        || *not_after == 0
    {
        return Err(Failure::new(400, "invalid_native_write"));
    }
    if scope.repository.as_deref() != Some(repository) {
        return Err(Failure::new(403, "native_repository_scope"));
    }
    Ok(())
}

async fn observe_scope(
    state: &State,
    scope: Arc<Scope>,
    effect: &Operation,
) -> std::result::Result<Value, Failure> {
    if scope.fountain.is_some() && !matches!(effect, Operation::Current | Operation::Read(..) | Operation::Receipt(..) | Operation::Mutation(Command{operation:WriteOperation::PublishBranch(_) | WriteOperation::CreatePull(_) | WriteOperation::CreateReview(_) | WriteOperation::MergePull(_) | WriteOperation::CreateIssue(_) | WriteOperation::CreateComment(_) | WriteOperation::EditDiscussion(_) | WriteOperation::SetDiscussionState(_) | WriteOperation::SetDiscussionLock(_),..})) {
        return Err(Failure::new(503, "fountain_product_write_unavailable"));
    }
    if let Operation::Mutation(command) = effect {
        validate_mutation(&scope, command)?;
    }
    if let Operation::Receipt(descriptor) = effect {
        if !opaque_id(&descriptor.command_id) || descriptor.digest.len()!=64 || !descriptor.digest.bytes().all(|b|b.is_ascii_digit()||(b'a'..=b'f').contains(&b)) || descriptor.not_after==0 {return Err(Failure::invalid());}
    }
    if let Operation::Read(query, version) = effect {
        if *version > 1 { return Err(Failure::invalid()); }
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
    if let Some(work) = crate::provider_contract::operation::Operation::current() {
        let limit = match effect { Operation::Read(query, _) => query.wire_limit(), _ => 64 };
        work.restrict(limit, scope.deadline.min(Instant::now() + effect.timeout()));
    }
    let mut cancelled = scope.cancelled.subscribe();
    let mut retired = state.retired.subscribe();
    let work = async {
        if let Some(client) = &scope.fountain {
            let repository = scope.repository.as_deref().ok_or_else(Failure::invalid)?;
            return match effect {
                Operation::Current => client.product_current(repository).await.map(|_|Value::Null).map_err(|e|Failure::new(e.status,e.code)),
                Operation::Read(query, _) => client.product_read(repository, query).await.map_err(|e|Failure::new(e.status,e.code)),
                Operation::Receipt(descriptor) => {
                    let prior=receipts::lookup(state,&scope,descriptor)?.into_wire();
                    if !prior.is_null() && prior["state"]!="indeterminate" {return Ok(prior);}
                    let profile=state.channel.socket.parent().ok_or_else(Failure::unavailable)?;
                    if client.issue_collaboration_only() {
                        return match client.recover_issue_collaboration(profile,&descriptor.command_id,&descriptor.digest,descriptor.not_after).await.map_err(|e|Failure::new(e.status,e.code))? {
                            Some((success,outcome))=>Ok(write_reply(descriptor,success,outcome.into())),None=>Ok(prior),
                        };
                    }
                    match client.recover_publication(profile,&descriptor.command_id,&descriptor.digest,descriptor.not_after).await.map_err(|e|Failure::new(e.status,e.code))? {
                        Some(crate::fountain_provider::PublicationOutcome::Ref(outcome))=>Ok(write_reply(descriptor,"applied",outcome)),
                        Some(crate::fountain_provider::PublicationOutcome::Merge(outcome))=>Ok(write_reply(descriptor,"applied",outcome)),
                        Some(crate::fountain_provider::PublicationOutcome::Pull(outcome)) | Some(crate::fountain_provider::PublicationOutcome::Review(outcome))=>Ok(write_reply(descriptor,"created",outcome)),None=>Ok(prior)
                    }
                }
                Operation::Mutation(command) => {
                    if scope.write_claimed.swap(true,Ordering::SeqCst){return Err(Failure::new(409,"native_effect_already_claimed"));}
                    let attempt=receipts::Attempt::reserve(state,&scope,command)?;
                    let profile=state.channel.socket.parent().ok_or_else(Failure::unavailable)?;
                    let d=&command.descriptor;
                    let (success,result)=match &command.operation {
                        WriteOperation::PublishBranch(branch)=>("applied",client.publish_candidate(profile,&d.command_id,&d.digest,d.not_after,branch).await),
                        WriteOperation::CreatePull(pull)=>("created",client.create_pull(profile,&d.command_id,&d.digest,d.not_after,pull).await),
                        WriteOperation::CreateReview(review)=>("created",client.create_review(profile,&d.command_id,&d.digest,d.not_after,review).await),
                        WriteOperation::MergePull(merge)=>("applied",client.merge_pull(profile,&d.command_id,&d.digest,d.not_after,merge).await),
                        WriteOperation::CreateIssue(_) | WriteOperation::CreateComment(_)=>("created",client.collaborate_issue(profile,&d.command_id,&d.digest,d.not_after,&command.operation).await.into()),
                        WriteOperation::EditDiscussion(_) | WriteOperation::SetDiscussionState(_) | WriteOperation::SetDiscussionLock(_)=>("applied",client.collaborate_issue(profile,&d.command_id,&d.digest,d.not_after,&command.operation).await.into()),
                        _=>unreachable!()
                    };
                    return attempt.finish(write_reply(d,success,result));
                }
            };
        }
        scope
        .credentials
        .with_token_operation_timeout(&scope.capability, effect.timeout(), async |token: &[u8]| match effect {
            Operation::Current => Ok(Value::Null),
            Operation::Read(query, _) => {
                state
                    .provider
                    .read_account(token, &scope.capability.account().account, query)
                    .await
            }
            Operation::Mutation(command) => mutate(state, &scope, token, command).await,
            Operation::Receipt(descriptor) => receipts::lookup(state,&scope,descriptor).map(receipts::Lookup::into_wire),
        }).await.map_err(|_| Failure::new(401, "native_credential_unavailable"))?
    };
    let observation = tokio::select! {
        biased;
        _ = cancelled.wait_for(|cancelled| *cancelled) => return Err(Failure::new(401, "native_operation_retired")),
        _ = retired.wait_for(|retired| *retired) => return Err(Failure::unavailable()),
        _ = tokio::time::sleep_until(scope.deadline.into()) => return Err(Failure::new(401, "native_operation_retired")),
        result = work => result?,
    };
    if !scope.current() || !state.alive.load(Ordering::SeqCst) {
        return Err(Failure::new(401, "native_operation_retired"));
    }
    let observation = match effect {
        Operation::Read(query, version) => query.observation(observation, *version)?,
        _ => observation,
    };
    let account = scope.capability.account();
    Ok(
        json!({"connection":{"connection":account.connection,"provider":account.provider,"origin":account.origin,"account":account.account},"repository":scope.repository,"observation":observation}),
    )
}

async fn mutate(
    state: &State,
    scope: &Scope,
    token: &[u8],
    command: &Command,
) -> std::result::Result<Value, Failure> {
    let Descriptor {
        command_id,
        digest,
        not_after,
    } = &command.descriptor;
    if scope.write_claimed.swap(true, Ordering::SeqCst) {
        return Err(Failure::new(409, "native_effect_already_claimed"));
    }
    let attempt = receipts::Attempt::reserve(state,scope,command)?;
    let measured = Instant::now();
    let wall = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| Failure::unavailable())?;
    if *not_after > wall.as_secs().saturating_add(300) {
        return attempt.finish(json!({"command_id":command_id,"digest":digest,"state":"not_sent","receipt":null,"failure":{"status":400,"code":"invalid_native_write","retry_after":null,"reset":null}}));
    }
    let deadline = measured + Duration::from_secs(*not_after).saturating_sub(wall);
    let account = &scope.capability.account().account;
    let (success, outcome) = command
        .operation
        .apply(&state.provider, token, account, deadline)
        .await;
    attempt.finish(write_reply(&command.descriptor,success,outcome))
}

fn write_reply(descriptor:&Descriptor,success:&str,outcome:WriteOutcome)->Value {
    let (state, receipt, failure) = match outcome {
        WriteOutcome::Applied(receipt) => (success, Some(receipt), None),
        WriteOutcome::NotSent(failure) => ("not_sent", None, Some(failure)),
        WriteOutcome::Indeterminate(failure) => ("indeterminate", None, Some(failure)),
    };
    let failure = failure.map(
        |f| json!({"status":f.status,"code":f.code,"retry_after":f.retry_after,"reset":f.reset}),
    );
    json!({"command_id":descriptor.command_id,"digest":descriptor.digest,"state":state,"receipt":receipt,"failure":failure})
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
