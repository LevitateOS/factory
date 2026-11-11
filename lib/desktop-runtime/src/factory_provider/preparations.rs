//! Own credential-free preparation jobs and their private immutable inputs.
//! Go owns approval and enrollment. Request drop never owns an admitted job.
use super::{AgentCancellation, Broker, Role};
use crate::credentials::Credentials;
use crate::private_fs::{private_dir, read_bytes as load_bytes, write_bytes as save_bytes};
use crate::supervisor::NativeChannel;
use base64::Engine;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use soda_execution_protocol::{ApprovedFiles, SetupReceipt, MAX_STAGE_BUNDLE};
use std::collections::BTreeMap;
use std::fs;
use std::io;
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

fn denied() -> io::Error {
    io::Error::new(
        io::ErrorKind::PermissionDenied,
        "native preparation scope unavailable",
    )
}
fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
fn text<'a>(value: &'a Value, name: &str, max: usize) -> io::Result<&'a str> {
    value
        .get(name)
        .and_then(Value::as_str)
        .filter(|value| {
            !value.is_empty() && value.len() <= max && !value.chars().any(char::is_control)
        })
        .ok_or_else(denied)
}
fn save(path: &Path, value: &impl Serialize) -> io::Result<()> {
    let bytes = serde_json::to_vec(value).map_err(io::Error::other)?;
    save_bytes(path, &bytes)
}
fn load<T: serde::de::DeserializeOwned>(path: &Path) -> io::Result<T> {
    serde_json::from_slice(&load_bytes(
        path,
        soda_execution_protocol::MAX_PREPARATION_REQUEST,
    )?)
    .map_err(io::Error::other)
}
struct DerivedSource {
    key: String,
    sha256: String,
    base: String,
    delta: Vec<u8>,
    not_after: OffsetDateTime,
}
struct Input {
    request: Value,
    key: String,
    id: String,
    workspace: String,
    role: Role,
    source: String,
    setup: String,
    native: Value,
    bundle: Vec<u8>,
    bundle_sha: String,
    approved: ApprovedFiles,
    derived: Option<DerivedSource>,
}
impl Input {
    fn controller_request(&self) -> &Value {
        self.request
            .get("candidate_origin")
            .unwrap_or(&self.request)
    }
    fn controlled(&self) -> bool {
        self.controller_request()
            .get("controller")
            .is_some_and(|value| !value.is_null())
    }
    fn effective_bundle(&self, directory: &Path) -> io::Result<Vec<u8>> {
        if self.derived.is_some() {
            match load_bytes(&directory.join("source.bundle"), MAX_STAGE_BUNDLE) {
                Ok(bytes) if !bytes.is_empty() => return Ok(bytes),
                Err(error) if error.kind() == io::ErrorKind::NotFound => (),
                _ => return Err(denied()),
            }
        }
        Ok(self.bundle.clone())
    }
    fn decode(request: Value, profile: &str, instance: &str) -> io::Result<Self> {
        let native = request.get("native").ok_or_else(denied)?.clone();
        if text(&native, "profile", 32)? != profile || text(&native, "instance_id", 32)? != instance
        {
            return Err(denied());
        }
        text(&native, "worktree_id", 128)?;
        let meta = &request["input"]["preparation"];
        let id = text(meta, "id", 25)?.to_owned();
        let workspace = text(meta, "project", 25)?.to_owned();
        if !id.starts_with('f')
            || !super::hex(&id[1..], 24)
            || !workspace.starts_with('p')
            || !super::hex(&workspace[1..], 24)
        {
            return Err(denied());
        }
        let role = Role::from_product(text(meta, "role", 32)?).ok_or_else(denied)?;
        let source = text(meta, "source_commit", 40)?.to_owned();
        let setup = text(meta, "setup_digest", 64)?.to_owned();
        if !super::hex(&source, 40)
            || !super::hex(&setup, 64)
            || meta
                .get("credential")
                .and_then(Value::as_str)
                .is_some_and(|value| !value.is_empty())
        {
            return Err(denied());
        }
        let data = &request["input"]["setup"];
        let decode = |value: &Value| -> io::Result<Vec<u8>> {
            base64::engine::general_purpose::STANDARD
                .decode(value.as_str().ok_or_else(denied)?)
                .map_err(|_| denied())
        };
        let bundle = decode(&data["bundle"])?;
        if bundle.is_empty() || bundle.len() > MAX_STAGE_BUNDLE {
            return Err(denied());
        }
        let mut files = BTreeMap::new();
        for (name, content) in data["files"].as_object().ok_or_else(denied)? {
            files.insert(name.clone(), decode(content)?);
        }
        let tools = match meta.get("tools") {
            None | Some(Value::Null) => Vec::new(),
            Some(value) => serde_json::from_value(value.clone()).map_err(|_| denied())?,
        };
        let approved = ApprovedFiles { files, tools };
        if !approved.valid() || approved.digest() != setup {
            return Err(denied());
        }
        let key = digest(&serde_json::to_vec(&native).map_err(io::Error::other)?)[..32].to_owned();
        let bundle_sha = digest(&bundle);
        let derived = if let Some(origin) = request.get("candidate_origin") {
            let binding = request.get("candidate_source").ok_or_else(denied)?;
            let key = text(binding, "key", 32)?.to_owned();
            let sha256 = text(binding, "sha256", 64)?.to_owned();
            let base = text(binding, "base", 40)?.to_owned();
            let delta = match &origin["input"]["bundle"] {
                Value::Null => Vec::new(),
                value => decode(value)?,
            };
            let not_after =
                OffsetDateTime::parse(text(&origin["input"], "deadline", 64)?, &Rfc3339)
                    .map_err(|_| denied())?;
            if !super::hex(&key, 32)
                || !super::hex(&sha256, 64)
                || !super::hex(&base, 40)
                || sha256 != bundle_sha
                || delta.len() > MAX_STAGE_BUNDLE
                || delta.is_empty() && base != source
            {
                return Err(denied());
            }
            Some(DerivedSource {
                key,
                sha256,
                base,
                delta,
                not_after,
            })
        } else {
            None
        };
        Ok(Self {
            request,
            key,
            id,
            workspace,
            role,
            source,
            setup,
            native,
            bundle,
            bundle_sha,
            approved,
            derived,
        })
    }
    /// True when a retired.json exists for this input. A marker naming another
    /// root or source bundle is refused. This is the only reading of the marker.
    fn retired(&self, directory: &Path) -> io::Result<bool> {
        let marker = directory.join("retired.json");
        if !marker.try_exists()? {
            return Ok(false);
        }
        let marker: Value = load(&marker)?;
        if marker["root"] != self.key
            || marker["bundle"] != digest(&self.effective_bundle(directory)?)
        {
            return Err(denied());
        }
        Ok(true)
    }
}
const RESTARTED: &str = "interrupted native preparation requires a fresh identity";
/// Why a preparation was interrupted. Only the error text reaches Go.
#[derive(Clone)]
enum Cause {
    /// This process failed to finish setup (broker, setup or journal error).
    Process(String),
    /// A restart found a journal without completion.json.
    Restart,
}
impl Cause {
    fn text(&self) -> &str {
        match self {
            Self::Process(error) => error,
            Self::Restart => RESTARTED,
        }
    }
}
#[derive(Clone)]
enum Outcome {
    Receipt(SetupReceipt),
    Interrupted(Cause),
}
/// completion.json. Stored as `{receipt, error, source_sha256}`.
#[derive(Clone, Serialize, Deserialize)]
#[serde(from = "StoredCompletion", into = "StoredCompletion")]
struct Completion {
    outcome: Outcome,
    source_sha256: Option<String>,
}
#[derive(Serialize, Deserialize)]
struct StoredCompletion {
    receipt: Option<SetupReceipt>,
    error: Option<String>,
    #[serde(default)]
    source_sha256: Option<String>,
}
impl From<StoredCompletion> for Completion {
    fn from(stored: StoredCompletion) -> Self {
        let outcome = match stored.receipt {
            Some(receipt) => Outcome::Receipt(receipt),
            None => Outcome::Interrupted(Cause::Process(stored.error.unwrap_or_default())),
        };
        Self {
            outcome,
            source_sha256: stored.source_sha256,
        }
    }
}
impl From<Completion> for StoredCompletion {
    fn from(completion: Completion) -> Self {
        let (receipt, error) = match completion.outcome {
            Outcome::Receipt(receipt) => (Some(receipt), None),
            Outcome::Interrupted(cause) => (None, Some(cause.text().to_owned())),
        };
        Self {
            receipt,
            error,
            source_sha256: completion.source_sha256,
        }
    }
}
impl Completion {
    fn failed(error: impl Into<String>) -> Self {
        Self {
            outcome: Outcome::Interrupted(Cause::Process(error.into())),
            source_sha256: None,
        }
    }
    fn restarted() -> Self {
        Self {
            outcome: Outcome::Interrupted(Cause::Restart),
            source_sha256: None,
        }
    }
}
fn load_completion(directory: &Path) -> io::Result<Option<Completion>> {
    match load(&directory.join("completion.json")) {
        Ok(value) => Ok(Some(value)),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error),
    }
}
/// The lifecycle of one preparation, derived from its journal.
enum Phase {
    /// Fenced before any request was admitted (controller-not-started journal).
    NotStarted,
    Running,
    Ready(SetupReceipt),
    Waiting(SetupReceipt),
    Failed(SetupReceipt),
    Interrupted(Cause),
    /// Retirement confirmed; `prior` is what setup had recorded, if anything.
    Stopped {
        prior: Option<Outcome>,
    },
}
impl Phase {
    fn of(completion: Option<Completion>, retired: bool) -> Self {
        let outcome = completion.map(|completion| completion.outcome);
        if retired {
            return Self::Stopped { prior: outcome };
        }
        match outcome {
            None => Self::Running,
            Some(Outcome::Interrupted(cause)) => Self::Interrupted(cause),
            Some(Outcome::Receipt(receipt)) => {
                if !receipt.missing.is_empty() {
                    Self::Waiting(receipt)
                } else if !receipt.output_truncated
                    && receipt.setup_exit == Some(0)
                    && receipt.check_exit == Some(0)
                {
                    Self::Ready(receipt)
                } else {
                    Self::Failed(receipt)
                }
            }
        }
    }
    fn stopped(&self) -> bool {
        matches!(self, Self::NotStarted | Self::Stopped { .. })
    }
    fn name(&self) -> &'static str {
        match self {
            Self::NotStarted | Self::Stopped { .. } => "stopped",
            Self::Running => "running",
            Self::Ready(_) => "ready",
            Self::Waiting(_) => "waiting",
            Self::Failed(_) => "failed",
            Self::Interrupted(_) => "interrupted",
        }
    }
    fn receipt(&self) -> Option<&SetupReceipt> {
        match self {
            Self::Ready(receipt)
            | Self::Waiting(receipt)
            | Self::Failed(receipt)
            | Self::Stopped {
                prior: Some(Outcome::Receipt(receipt)),
            } => Some(receipt),
            _ => None,
        }
    }
    fn error(&self) -> Option<&str> {
        match self {
            Self::Interrupted(cause)
            | Self::Stopped {
                prior: Some(Outcome::Interrupted(cause)),
            } => Some(cause.text()),
            _ => None,
        }
    }
}
enum Subject {
    Fence(Value),
    Input(Arc<Input>),
}
/// A preparation's phase with what Go is told about it.
struct Status {
    subject: Subject,
    phase: Phase,
    source_sha256: Option<String>,
}
impl Status {
    fn fence(address: &Value) -> Self {
        Self {
            subject: Subject::Fence(address.clone()),
            phase: Phase::NotStarted,
            source_sha256: None,
        }
    }
    fn of(input: &Arc<Input>, completion: Option<Completion>, retired: bool) -> Self {
        Self {
            subject: Subject::Input(input.clone()),
            source_sha256: completion
                .as_ref()
                .and_then(|completion| completion.source_sha256.clone()),
            phase: Phase::of(completion, retired),
        }
    }
    /// The one derivation of a phase from the journal. `live` is the running
    /// entry's completion when there is one (memory leads disk until it is
    /// durable); otherwise completion.json is read.
    fn load(
        input: &Arc<Input>,
        directory: &Path,
        live: Option<Option<Completion>>,
    ) -> io::Result<Self> {
        let retired = input.retired(directory)?;
        let completion = match live {
            Some(completion) => completion,
            None => load_completion(directory)?,
        };
        Ok(Self::of(input, completion, retired))
    }
    fn to_wire(&self) -> Value {
        let input = match &self.subject {
            Subject::Fence(address) => {
                return json!({"id":address["id"],"project":address["workspace"],"native":address["native"],
                    "phase":"stopped","ready":false,"stopped":true,"retirement":"confirmed"})
            }
            Subject::Input(input) => input,
        };
        let phase = &self.phase;
        let receipt = phase.receipt();
        let tools: Vec<_> = receipt
            .into_iter()
            .flat_map(|value| value.tools.iter())
            .map(|(name, path)| json!({"name":name,"path":path}))
            .collect();
        let source_sha = self
            .source_sha256
            .as_deref()
            .unwrap_or(if input.derived.is_none() {
                &input.bundle_sha
            } else {
                ""
            });
        let stopped = phase.stopped();
        json!({"id":input.id,"project":input.workspace,"role":input.role.product(),
            "source_commit":input.source,"setup_digest":input.setup,"source_bundle_sha256":source_sha,"native":input.native,
            "phase":phase.name(),"ready":matches!(phase,Phase::Ready(_)),"stopped":stopped,"retirement":if stopped {"confirmed"} else {""},
            "setup_exit":receipt.and_then(|value|value.setup_exit),"check_exit":receipt.and_then(|value|value.check_exit),"tools":tools,
            "missing":receipt.map(|value|value.missing.join(", ")).unwrap_or_default(),
            "output":receipt.map(|value|value.output.clone()).or_else(||phase.error().map(str::to_owned)).unwrap_or_default()})
    }
}
struct Entry {
    input: Arc<Input>,
    completion: Arc<Mutex<Option<Completion>>>,
    job: Option<JoinHandle<()>>,
    cancellation: AgentCancellation,
}
pub(crate) struct Preparations {
    directory: PathBuf,
    profile: String,
    instance: String,
    alive: Arc<AtomicBool>,
    entries: Mutex<BTreeMap<String, Entry>>,
    channel: NativeChannel,
}
#[derive(Clone)]
struct Authority {
    credentials: Arc<Credentials>,
    channel: NativeChannel,
    runtime: tokio::runtime::Handle,
}

fn retire_input(input: &Input, directory: &Path, profile: &str) -> io::Result<Vec<u8>> {
    let marker = directory.join("retired.json");
    if !input.retired(directory)? {
        let sha256 = digest(&input.effective_bundle(directory)?);
        Broker::retire_unlaunched(&input.key, profile, &sha256)?;
        save(&marker, &json!({"root":input.key,"bundle":sha256}))?;
    }
    load_bytes(&marker, 1024)
}
impl Preparations {
    fn storage_at_capacity(&self) -> io::Result<bool> {
        let mut unsettled = 0;
        for entry in fs::read_dir(&self.directory)? {
            let entry = entry?;
            let directory = entry.path();
            if !directory.join("retired.json").try_exists()? {
                unsettled += 1;
                if unsettled >= 64 {
                    return Ok(true);
                }
                continue;
            }
            // Retain every original journal. Only a matching physical retirement
            // receipt releases its slot; absence or corrupt history cannot.
            let metadata = directory.symlink_metadata()?;
            if !metadata.is_dir()
                || metadata.uid() != unsafe { libc::geteuid() }
                || metadata.mode() & 0o077 != 0
            {
                return Err(denied());
            }
            let marker: Value = load(&directory.join("retired.json"))?;
            let address = if marker["kind"] == "not_started" {
                marker["address"].clone()
            } else {
                let request: Value = load(&directory.join("request.json"))?;
                json!({"id":request["input"]["preparation"]["id"],
                    "workspace":request["input"]["preparation"]["project"],"native":request["native"]})
            };
            if entry.file_name().to_str() != Some(self.key(&address)?.as_str())
                || !self.retained(&address)?.phase.stopped()
            {
                return Err(denied());
            }
        }
        Ok(false)
    }

    pub(crate) fn recovery_bundle(&self, intent: &super::LaunchIntent) -> io::Result<Vec<u8>> {
        let scope = intent.scope()?;
        if scope.profile != self.profile {
            return Err(denied());
        }
        let native = json!({"profile":scope.profile,"instance_id":intent.instance(),"worktree_id":intent.worktree()});
        let key = digest(&serde_json::to_vec(&native).map_err(io::Error::other)?)[..32].to_owned();
        let directory = self.directory.join(key);
        let meta = directory.symlink_metadata()?;
        if !meta.is_dir() || meta.uid() != unsafe { libc::geteuid() } || meta.mode() & 0o077 != 0 {
            return Err(denied());
        }
        let input = Input::decode(
            load(&directory.join("request.json"))?,
            &self.profile,
            intent.instance(),
        )?;
        let bundle = input.effective_bundle(&directory)?;
        if input.native != native
            || input.workspace != intent.workspace()
            || input.source != scope.candidate
            || input.role != scope.role
            || digest(&bundle) != scope.source_sha256
        {
            return Err(denied());
        }
        Ok(bundle)
    }
    pub(crate) fn execution_material(
        &self,
        intent: &super::LaunchIntent,
    ) -> io::Result<(Vec<u8>, soda_execution_protocol::PreparedSourceRef)> {
        let scope = intent.scope()?;
        let native = json!({"profile":scope.profile,"instance_id":intent.instance(),"worktree_id":intent.worktree()});
        let key = self.key(&json!({"native":native}))?;
        let input = {
            let entries = self.entries.lock().map_err(|_| denied())?;
            entries.get(&key).ok_or_else(denied)?.input.clone()
        };
        let bundle = input.effective_bundle(&self.directory.join(&key))?;
        if input.workspace != intent.workspace()
            || input.source != scope.candidate
            || input.role != scope.role
            || digest(&bundle) != scope.source_sha256
        {
            return Err(denied());
        }
        let address = json!({"id":input.id,"workspace":input.workspace,"native":native});
        if !matches!(self.status(&address)?.phase, Phase::Ready(_)) {
            return Err(denied());
        }
        Ok((
            bundle,
            soda_execution_protocol::PreparedSourceRef {
                run: key,
                candidate: input.source.clone(),
            },
        ))
    }
    pub(crate) fn new(channel: &NativeChannel, alive: Arc<AtomicBool>) -> io::Result<Self> {
        let directory = channel
            .socket
            .parent()
            .ok_or_else(denied)?
            .join("native-preparations");
        private_dir(&directory).map_err(|_| denied())?;
        Ok(Self {
            directory,
            profile: channel.profile.clone(),
            instance: channel.instance.clone(),
            alive,
            entries: Mutex::new(BTreeMap::new()),
            channel: channel.clone(),
        })
    }
    fn prepare(&self, request: Value, authority: Option<Authority>) -> io::Result<Value> {
        let input = Arc::new(Input::decode(request, &self.profile, &self.instance)?);
        if input.controlled() && authority.is_none() {
            return Err(denied());
        }
        let mut entries = self.entries.lock().map_err(|_| denied())?;
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        if let Some(entry) = entries.get(&input.key) {
            if entry.input.request != input.request {
                return Err(denied());
            }
            let address = json!({"id":input.id,"workspace":input.workspace,"native":input.native});
            drop(entries);
            return self.inspect(&address);
        }
        if entries.len() >= 64 {
            return Err(io::Error::other("native preparation capacity retained"));
        }
        let directory = self.directory.join(&input.key);
        let existing = directory.try_exists()?;
        if !existing && self.storage_at_capacity()? {
            return Err(io::Error::other(
                "native preparation storage capacity retained",
            ));
        }
        private_dir(&directory).map_err(|_| denied())?;
        let completion = if existing {
            let prior: Value = load(&directory.join("request.json"))?;
            if prior != input.request {
                return Err(denied());
            }
            input.retired(&directory)?;
            Some(load_completion(&directory)?.unwrap_or_else(Completion::restarted))
        } else {
            save(&directory.join("request.json"), &input.request)?;
            None
        };
        let completion = Arc::new(Mutex::new(completion));
        let cancellation = AgentCancellation::default();
        let job = if existing {
            None
        } else {
            let input = input.clone();
            let completion = completion.clone();
            let cancellation = cancellation.clone();
            let alive = self.alive.clone();
            let profile = self.profile.clone();
            Some(
                thread::Builder::new()
                    .name("factory-native-setup".into())
                    .spawn(move || {
                        let mut lease = None;
                        let mut admitted = false;
                        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                            if !alive.load(Ordering::SeqCst) || cancellation.cancelled() {
                                return Err(denied());
                            }
                            if input.controlled() {
                                let authority = authority.as_ref().ok_or_else(denied)?;
                                let root = authority.channel.socket.parent().ok_or_else(denied)?.to_path_buf();
                                lease = Some(authority.runtime.block_on(super::controller::Lease::admit_preparation(
                                    authority.credentials.clone(),root,input.controller_request(),&input.key,alive.clone(),authority.channel.clone()))?);
                                lease.as_mut().ok_or_else(denied)?.enter(cancellation.clone())?;
                                admitted = true;
                            }
                            let (bundle, cancellation) = if let Some(derived) = &input.derived {
                                let remaining = derived.not_after - OffsetDateTime::now_utc();
                                let milliseconds = u64::try_from(remaining.whole_milliseconds())
                                    .map_err(|_| denied())?;
                                let cancellation = cancellation.with_deadline(
                                    Instant::now()
                                        + Duration::from_millis(
                                            milliseconds.min(4 * 60 * 60 * 1000),
                                        ),
                                );
                                if cancellation.cancelled() {
                                    return Err(denied());
                                }
                                let bundle = if derived.delta.is_empty() {
                                    input.bundle.clone()
                                } else {
                                    Broker::complete_candidate(
                                        &derived.key,
                                        &profile,
                                        &derived.sha256,
                                        &derived.base,
                                        &input.source,
                                        &derived.delta,
                                        cancellation.clone(),
                                    )?
                                };
                                save_bytes(&directory.join("source.bundle"), &bundle)?;
                                (bundle, cancellation)
                            } else {
                                (input.bundle.clone(), cancellation.clone())
                            };
                            if cancellation.cancelled() {
                                return Err(denied());
                            }
                            let receipt = Broker::prepare_source(
                                &input.key,
                                &profile,
                                input.role,
                                &bundle,
                                &input.approved,
                                &input.source,
                                cancellation.clone(),
                            )?;
                            if cancellation.cancelled() {
                                return Err(denied());
                            }
                            Ok((receipt, digest(&bundle)))
                        }))
                        .unwrap_or_else(|_| Err(io::Error::other("native setup job failed")));
                        let mut result = match result {
                            Ok((receipt, sha256)) => Completion {
                                outcome: Outcome::Receipt(receipt),
                                source_sha256: Some(sha256),
                            },
                            Err(error) => Completion::failed(error.to_string()),
                        };
                        if save(&directory.join("completion.json"), &result).is_err() {
                            result = Completion::failed("native setup receipt was not durable");
                        }
                        *completion.lock().unwrap_or_else(|error| error.into_inner()) =
                            Some(result);
                        if input.controlled() {
                            if admitted {
                                let ready = completion.lock().unwrap_or_else(|e|e.into_inner()).as_ref().is_some_and(|c|matches!(c.outcome, Outcome::Receipt(_)));
                                // The claim owns the prepared checkout as well as
                                // setup. Keep its watcher until physical retirement.
                                if ready {
                                    while alive.load(Ordering::SeqCst) && !cancellation.cancelled() { thread::sleep(Duration::from_millis(50)); }
                                }
                            }
                            let retired = if admitted { retire_input(&input,&directory,&profile).ok() } else { None };
                            drop(lease); // Join the watcher and retain exact no-start evidence.
                            if let Some(authority) = authority {
                                if let Some(root) = authority.channel.socket.parent() {
                                    if let Err(error) = authority.runtime.block_on(super::controller::recover_preparation(authority.credentials,root,input.controller_request(),&input.key,retired.as_deref())) {
                                        eprintln!("native preparation host retirement requires reconciliation: {error}");
                                    }
                                }
                            }
                        }
                    })?,
            )
        };
        let address = json!({"id":input.id,"workspace":input.workspace,"native":input.native});
        entries.insert(
            input.key.clone(),
            Entry {
                input,
                completion,
                job,
                cancellation,
            },
        );
        drop(entries);
        self.inspect(&address)
    }
    fn key(&self, address: &Value) -> io::Result<String> {
        let native = &address["native"];
        if text(native, "profile", 32)? != self.profile
            || !super::hex(text(native, "instance_id", 32)?, 32)
        {
            return Err(denied());
        }
        text(native, "worktree_id", 128)?;
        Ok(digest(&serde_json::to_vec(native).map_err(io::Error::other)?)[..32].into())
    }
    fn prepare_candidate(
        &self,
        request: Value,
        source: &Value,
        authority: Option<Authority>,
    ) -> io::Result<Value> {
        let address = json!({"id":request["input"]["preparation"]["id"],"workspace":request["input"]["preparation"]["project"],"native":request["native"]});
        let key = self.key(&address)?;
        {
            let entries = self.entries.lock().map_err(|_| denied())?;
            if let Some(entry) = entries.get(&key) {
                if entry.input.request["candidate_origin"] != request
                    || entry.input.request["candidate_source"]["address"] != *source
                {
                    return Err(denied());
                }
                drop(entries);
                return self.inspect(&address);
            }
        }
        if !matches!(self.status(source)?.phase, Phase::Ready(_)) {
            return Err(denied());
        }
        let source_key = self.key(source)?;
        let base = {
            let entries = self.entries.lock().map_err(|_| denied())?;
            entries.get(&source_key).ok_or_else(denied)?.input.clone()
        };
        let target = &request["input"]["preparation"];
        let approved = &base.request["input"]["preparation"];
        if request["input"]["source_preparation"] != base.id
            || target["project"] != base.workspace
            || target["id"] == base.id
            || target["setup_digest"] != approved["setup_digest"]
            || target["requirements"] != approved["requirements"]
            || target["approval"] != approved["approval"]
            || target["tools"] != approved["tools"]
        {
            return Err(denied());
        }
        let bundle = base.effective_bundle(&self.directory.join(&source_key))?;
        let encode = |bytes: &[u8]| base64::engine::general_purpose::STANDARD.encode(bytes);
        let files: BTreeMap<_, _> = base
            .approved
            .files
            .iter()
            .map(|(name, bytes)| (name.clone(), encode(bytes)))
            .collect();
        let standing = json!({"native":request["native"],"input":{"preparation":target,"setup":{"files":files,"bundle":encode(&bundle)}},
            "candidate_origin":request,"candidate_source":{"key":source_key,"base":base.source,"sha256":digest(&bundle),"address":source}});
        self.prepare(standing, authority)
    }
    pub(crate) fn inspect(&self, address: &Value) -> io::Result<Value> {
        Ok(self.status(address)?.to_wire())
    }
    fn status(&self, address: &Value) -> io::Result<Status> {
        if address["native"]["instance_id"] != self.instance {
            return self.retained(address);
        }
        let key = self.key(address)?;
        let (input, completion, cancel) = {
            let entries = self.entries.lock().map_err(|_| denied())?;
            let Some(entry) = entries.get(&key) else {
                drop(entries);
                return self.retained(address);
            };
            if address["id"] != entry.input.id || address["workspace"] != entry.input.workspace {
                return Err(denied());
            }
            let completion = entry.completion.lock().map_err(|_| denied())?.clone();
            (entry.input.clone(), completion, entry.cancellation.clone())
        };
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        let directory = self.directory.join(&key);
        let status = Status::load(&input, &directory, Some(completion))?;
        if let Phase::Ready(prior) | Phase::Waiting(prior) | Phase::Failed(prior) = &status.phase {
            let receipt = Broker::read_source_setup(
                &key,
                &self.profile,
                &digest(&input.effective_bundle(&directory)?),
                &input.source,
                cancel,
            )?;
            if receipt.setup_sha256 != input.setup || receipt != *prior {
                return Err(denied());
            }
        }
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        Ok(status)
    }
    pub(crate) fn context(&self, request: &Value) -> io::Result<Value> {
        let meta = &request["input"];
        let address =
            json!({"id":meta["id"],"workspace":meta["project"],"native":request["native"]});
        if !matches!(self.status(&address)?.phase, Phase::Ready(_)) {
            return Err(denied());
        }
        let key = self.key(&address)?;
        let (input, cancellation) = {
            let entries = self.entries.lock().map_err(|_| denied())?;
            let entry = entries.get(&key).ok_or_else(denied)?;
            (entry.input.clone(), entry.cancellation.clone())
        };
        let role = input.role.product();
        if meta["source_commit"] != input.source || request["role"] != role {
            return Err(denied());
        }
        let selection: soda_execution_protocol::ContextSelection = serde_json::from_value(json!({
            "source_commit":meta["source_commit"],"approved_base":meta["approved_base"],
            "diff_base":meta["diff_base"],"candidate":meta["candidate"],"paths":meta["paths"],
            "directory":meta["directory"],"page":meta["page"]
        }))
        .map_err(|_| denied())?;
        if !selection.valid() {
            return Err(denied());
        }
        let context = Broker::read_source_context(
            &key,
            &self.profile,
            &digest(&input.effective_bundle(&self.directory.join(&key))?),
            &input.source,
            &selection,
            cancellation,
        )?;
        let setup = input.approved.files.get("setup.sh").ok_or_else(denied)?;
        let check = input.approved.files.get("check.sh").ok_or_else(denied)?;
        let total = setup.len()
            + check.len()
            + context.diff.len()
            + context.files.iter().map(|f| f.content.len()).sum::<usize>()
            + context
                .entries
                .iter()
                .map(|e| e.path.len() + e.kind.len())
                .sum::<usize>();
        if total > soda_execution_protocol::MAX_CONTEXT_BYTES
            || context.files.len() > 12
            || context.entries.len() > soda_execution_protocol::MAX_CONTEXT_ENTRIES
            || std::str::from_utf8(setup).is_err()
            || std::str::from_utf8(check).is_err()
            || std::str::from_utf8(&context.diff).is_err()
            || context.files.iter().any(|f| {
                !soda_execution_protocol::ContextSelection::valid_path(&f.path)
                    || f.content.len() > 32768
                    || std::str::from_utf8(&f.content).is_err()
            })
        {
            return Err(denied());
        }
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        let encode = |bytes: &[u8]| base64::engine::general_purpose::STANDARD.encode(bytes);
        let files: Vec<_> = context
            .files
            .into_iter()
            .map(|f| json!({"path":f.path,"content":encode(&f.content)}))
            .collect();
        Ok(json!({"native":input.native,"material":{
            "project":input.workspace,"id":input.id,"role":role,"source_commit":input.source,
            "approved_base":selection.approved_base,"diff_base":selection.diff_base,"candidate":selection.candidate,
            "setup":encode(setup),"check":encode(check),"files":files,"diff":encode(&context.diff),
            "directory":selection.directory,"entries":context.entries,"page":context.page
        }}))
    }
    pub(crate) fn cancel_all(&self) {
        let entries = self
            .entries
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        for entry in entries.values() {
            entry.cancellation.cancel();
        }
    }
    pub(crate) fn retire(&self, address: &Value) -> io::Result<Value> {
        Ok(self.retire_status(address)?.to_wire())
    }
    fn retire_status(&self, address: &Value) -> io::Result<Status> {
        if address["native"]["instance_id"] != self.instance {
            return self.fence_unstarted(address);
        }
        let key = self.key(address)?;
        let (input, job) = {
            let mut entries = self.entries.lock().map_err(|_| denied())?;
            let Some(entry) = entries.get_mut(&key) else {
                drop(entries);
                return self.fence_unstarted(address);
            };
            if address["id"] != entry.input.id || address["workspace"] != entry.input.workspace {
                return Err(denied());
            }
            entry.cancellation.cancel();
            (entry.input.clone(), entry.job.take())
        };
        if let Some(job) = job {
            job.join().map_err(|_| denied())?;
        }
        retire_input(&input, &self.directory.join(&key), &self.profile)?;
        let entries = self.entries.lock().map_err(|_| denied())?;
        let entry = entries.get(&key).ok_or_else(denied)?;
        let completion = entry.completion.lock().map_err(|_| denied())?.clone();
        Ok(Status::of(&input, completion, true))
    }

    fn fence_unstarted(&self, address: &Value) -> io::Result<Status> {
        let key = self.key(address)?;
        let entries = self.entries.lock().map_err(|_| denied())?;
        if !self.alive.load(Ordering::SeqCst) || entries.contains_key(&key) {
            return Err(denied());
        }
        let id = text(address, "id", 25)?;
        let workspace = text(address, "workspace", 25)?;
        if !id.starts_with('f') || !super::hex(&id[1..], 24)
            || !workspace.starts_with('p') || !super::hex(&workspace[1..], 24)
        {
            return Err(denied());
        }
        let directory = self.directory.join(&key);
        if !directory.try_exists()? {
            // prepare holds this same lock and retains request.json before
            // admitting any constructor or remote claim. This terminal journal
            // occupies the original key, so a later identical send cannot start.
            private_dir(&directory).map_err(|_| denied())?;
            save(&directory.join("retired.json"),
                &json!({"kind":"not_started","root":key,"address":address}))?;
        }
        drop(entries);
        self.retained(address)
    }

    // Adopt an original owner's confirmed retirement, including for storage
    // accounting. This read never opens a workspace or renews its authority.
    fn retained(&self, address: &Value) -> io::Result<Status> {
        let native = &address["native"];
        let instance = text(native, "instance_id", 32)?;
        if !self.alive.load(Ordering::SeqCst)
            || native["profile"] != self.profile
            || !super::hex(instance, 32)
        {
            return Err(denied());
        }
        let key = digest(&serde_json::to_vec(native).map_err(io::Error::other)?)[..32].to_owned();
        let directory = self.directory.join(&key);
        let marker: Value = load(&directory.join("retired.json"))?;
        if marker["kind"] == "not_started" {
            if marker["root"] != key || marker["address"] != *address
                || directory.join("request.json").try_exists()?
            {
                return Err(denied());
            }
            return Ok(Status::fence(address));
        }
        let input = Arc::new(Input::decode(
            load(&directory.join("request.json"))?,
            &self.profile,
            instance,
        )?);
        if input.native != *native
            || address["id"] != input.id
            || address["workspace"] != input.workspace
            || input.key != key
        {
            return Err(denied());
        }
        let status = Status::load(&input, &directory, None)?;
        if !status.phase.stopped() {
            return Err(denied());
        }
        Ok(status)
    }
    pub(crate) fn retire_all(&self) -> io::Result<()> {
        self.cancel_all();
        let addresses:Vec<_>=self.entries.lock().map_err(|_|denied())?.values().map(|entry|json!({"id":entry.input.id,"workspace":entry.input.workspace,"native":entry.input.native})).collect();
        let mut failure = false;
        for address in addresses {
            if self.retire(&address).is_err() {
                failure = true;
            }
        }
        if failure {
            Err(io::Error::other(
                "native preparation retirement requires reconciliation",
            ))
        } else {
            Ok(())
        }
    }
}

#[derive(Deserialize)]
#[serde(tag = "operation", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum Call {
    Prepare { request: Value },
    Candidate { request: Value, source: Value },
    Context { request: Value },
    Inspect { address: Value },
    Retire { address: Value },
}

impl Preparations {
    pub(crate) async fn callback(
        self: Arc<Self>,
        call: Call,
        execution_available: bool,
        credentials: Option<Arc<Credentials>>,
    ) -> std::result::Result<Value, crate::github_provider::Failure> {
        use crate::github_provider::Failure;
        if matches!(&call, Call::Prepare { .. } | Call::Candidate { .. }) && !execution_available {
            return Err(Failure::new(503, "native_execution_unavailable"));
        }
        let address = match &call {
            Call::Inspect { address } | Call::Retire { address } => Some(address.clone()),
            _ => None,
        };
        let recovery_credentials = credentials.clone();
        let authority = credentials.map(|credentials| Authority {
            credentials,
            channel: self.channel.clone(),
            runtime: tokio::runtime::Handle::current(),
        });
        let owner = self.clone();
        let (result, status) = tokio::task::spawn_blocking(move || {
            let reported = |status: Status| (status.to_wire(), Some(status));
            Ok(match call {
                Call::Prepare { request } => (owner.prepare(request, authority)?, None),
                Call::Candidate { request, source } => {
                    (owner.prepare_candidate(request, &source, authority)?, None)
                }
                Call::Context { request } => (owner.context(&request)?, None),
                Call::Inspect { address } => reported(owner.status(&address)?),
                Call::Retire { address } => reported(owner.retire_status(&address)?),
            })
        })
        .await
        .map_err(|_| Failure::unavailable())?
        .map_err(|error: io::Error| {
            eprintln!("native preparation callback: {error}");
            Failure::new(503, "native_preparation_unavailable")
        })?;
        if let (Some(address), Some(status)) = (address, status) {
            if status.phase.stopped() {
                let key = self.key(&address).map_err(|_| Failure::unavailable())?;
                let directory = self.directory.join(&key);
                let marker = load_bytes(&directory.join("retired.json"), 1024)
                    .map_err(|_| Failure::unavailable())?;
                if matches!(status.phase, Phase::NotStarted) {
                    // The exact receiver fence proves that no request/claim was
                    // admitted. Reading it grants no new host operation.
                    self.retained(&address)
                        .map_err(|_| Failure::unavailable())?;
                    return Ok(result);
                }
                let request: Value =
                    load(&directory.join("request.json")).map_err(|_| Failure::unavailable())?;
                let input = Input::decode(
                    request,
                    &self.profile,
                    address["native"]["instance_id"]
                        .as_str()
                        .ok_or_else(Failure::invalid)?,
                )
                .map_err(|_| Failure::unavailable())?;
                if input.controlled() {
                    if input.key != key
                        || input.id != address["id"]
                        || input.workspace != address["workspace"]
                        || input.native != address["native"]
                    {
                        return Err(Failure::unavailable());
                    }
                    let credentials = recovery_credentials.ok_or_else(Failure::unavailable)?;
                    super::controller::recover_preparation(
                        credentials,
                        self.channel
                            .socket
                            .parent()
                            .ok_or_else(Failure::unavailable)?,
                        input.controller_request(),
                        &key,
                        Some(&marker),
                    )
                    .await
                    .map_err(|_| Failure::unavailable())?;
                }
            }
        }
        Ok(result)
    }
}
