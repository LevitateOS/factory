//! Own credential-free preparation jobs and their private immutable inputs.
//! Go owns approval and enrollment. Request drop never owns an admitted job.
use super::{AgentCancellation, Broker, Role};
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
    serde_json::from_slice(&load_bytes(path, 2 * 1024 * 1024)?).map_err(io::Error::other)
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
        let role = match text(meta, "role", 32)? {
            "soda-coder" => Role::Coder,
            "soda-reviewer" => Role::Reviewer,
            _ => return Err(denied()),
        };
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
    fn state(&self, completion: Option<&Completion>, retired: bool) -> Value {
        let receipt = completion.and_then(|value| value.receipt.as_ref());
        let phase = if retired {
            "stopped"
        } else if let Some(receipt) = receipt {
            if !receipt.missing.is_empty() {
                "waiting"
            } else if !receipt.output_truncated
                && receipt.setup_exit == Some(0)
                && receipt.check_exit == Some(0)
            {
                "ready"
            } else {
                "failed"
            }
        } else if completion.is_some() {
            "interrupted"
        } else {
            "running"
        };
        let tools: Vec<_> = receipt
            .into_iter()
            .flat_map(|value| value.tools.iter())
            .map(|(name, path)| json!({"name":name,"path":path}))
            .collect();
        let source_sha = completion
            .and_then(|value| value.source_sha256.as_deref())
            .unwrap_or(if self.derived.is_none() {
                &self.bundle_sha
            } else {
                ""
            });
        json!({"id":self.id,"project":self.workspace,"role":match self.role {Role::Coder=>"soda-coder",Role::Reviewer=>"soda-reviewer"},
            "source_commit":self.source,"setup_digest":self.setup,"source_bundle_sha256":source_sha,"native":self.native,
            "phase":phase,"ready":phase=="ready","stopped":retired,"retirement":if retired {"confirmed"} else {""},
            "setup_exit":receipt.and_then(|value|value.setup_exit),"check_exit":receipt.and_then(|value|value.check_exit),"tools":tools,
            "missing":receipt.map(|value|value.missing.join(", ")).unwrap_or_default(),
            "output":receipt.map(|value|value.output.clone()).or_else(||completion.and_then(|value|value.error.clone())).unwrap_or_default()})
    }
}
#[derive(Clone, Serialize, Deserialize)]
struct Completion {
    receipt: Option<SetupReceipt>,
    error: Option<String>,
    #[serde(default)]
    source_sha256: Option<String>,
}
struct Entry {
    input: Arc<Input>,
    completion: Arc<Mutex<Option<Completion>>>,
    job: Option<JoinHandle<()>>,
    cancellation: AgentCancellation,
    retired: bool,
}
pub(crate) struct Preparations {
    directory: PathBuf,
    profile: String,
    instance: String,
    alive: Arc<AtomicBool>,
    entries: Mutex<BTreeMap<String, Entry>>,
}
impl Preparations {
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
        if self.inspect(&address)?["ready"] != true {
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
        })
    }
    pub(crate) fn prepare(&self, request: Value) -> io::Result<Value> {
        let input = Arc::new(Input::decode(request, &self.profile, &self.instance)?);
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
        if !existing && fs::read_dir(&self.directory)?.take(65).count() >= 64 {
            return Err(io::Error::other(
                "native preparation storage capacity retained",
            ));
        }
        private_dir(&directory).map_err(|_| denied())?;
        let mut retired = false;
        let completion = if existing {
            let prior: Value = load(&directory.join("request.json"))?;
            if prior != input.request {
                return Err(denied());
            }
            retired = directory.join("retired.json").try_exists()?;
            if retired {
                let marker: Value = load(&directory.join("retired.json"))?;
                if marker["root"] != input.key
                    || marker["bundle"] != digest(&input.effective_bundle(&directory)?)
                {
                    return Err(denied());
                }
            }
            Some(match load(&directory.join("completion.json")) {
                Ok(value) => value,
                Err(error) if error.kind() == io::ErrorKind::NotFound => Completion {
                    receipt: None,
                    error: Some("interrupted native preparation requires a fresh identity".into()),
                    source_sha256: None,
                },
                Err(error) => return Err(error),
            })
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
                        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                            if !alive.load(Ordering::SeqCst) || cancellation.cancelled() {
                                return Err(denied());
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
                                (input.bundle.clone(), cancellation)
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
                                receipt: Some(receipt),
                                error: None,
                                source_sha256: Some(sha256),
                            },
                            Err(error) => Completion {
                                receipt: None,
                                error: Some(error.to_string()),
                                source_sha256: None,
                            },
                        };
                        if save(&directory.join("completion.json"), &result).is_err() {
                            result = Completion {
                                receipt: None,
                                error: Some("native setup receipt was not durable".into()),
                                source_sha256: None,
                            };
                        }
                        *completion.lock().unwrap_or_else(|error| error.into_inner()) =
                            Some(result);
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
                retired,
            },
        );
        drop(entries);
        self.inspect(&address)
    }
    fn key(&self, address: &Value) -> io::Result<String> {
        let native = &address["native"];
        if text(native, "profile", 32)? != self.profile
            || text(native, "instance_id", 32)? != self.instance
        {
            return Err(denied());
        }
        text(native, "worktree_id", 128)?;
        Ok(digest(&serde_json::to_vec(native).map_err(io::Error::other)?)[..32].into())
    }
    pub(crate) fn prepare_candidate(&self, request: Value, source: &Value) -> io::Result<Value> {
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
        if self.inspect(source)?["ready"] != true {
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
        self.prepare(standing)
    }
    pub(crate) fn inspect(&self, address: &Value) -> io::Result<Value> {
        let key = self.key(address)?;
        let (input, completion, cancel, retired) = {
            let entries = self.entries.lock().map_err(|_| denied())?;
            let entry = entries.get(&key).ok_or_else(|| {
                io::Error::new(io::ErrorKind::NotFound, "native preparation not observed")
            })?;
            if address["id"] != entry.input.id || address["workspace"] != entry.input.workspace {
                return Err(denied());
            }
            let completion = entry.completion.lock().map_err(|_| denied())?.clone();
            (
                entry.input.clone(),
                completion,
                entry.cancellation.clone(),
                entry.retired,
            )
        };
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        if !retired
            && completion
                .as_ref()
                .is_some_and(|value| value.receipt.is_some())
        {
            let receipt = Broker::read_source_setup(
                &key,
                &self.profile,
                &digest(&input.effective_bundle(&self.directory.join(&key))?),
                &input.source,
                cancel,
            )?;
            if receipt.setup_sha256 != input.setup
                || completion.as_ref().and_then(|value| value.receipt.as_ref()) != Some(&receipt)
            {
                return Err(denied());
            }
        }
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        Ok(input.state(completion.as_ref(), retired))
    }
    pub(crate) fn context(&self, request: &Value) -> io::Result<Value> {
        let meta = &request["input"];
        let address =
            json!({"id":meta["id"],"workspace":meta["project"],"native":request["native"]});
        if self.inspect(&address)?["ready"] != true {
            return Err(denied());
        }
        let key = self.key(&address)?;
        let (input, cancellation) = {
            let entries = self.entries.lock().map_err(|_| denied())?;
            let entry = entries.get(&key).ok_or_else(denied)?;
            (entry.input.clone(), entry.cancellation.clone())
        };
        let role = match input.role {
            Role::Coder => "soda-coder",
            Role::Reviewer => "soda-reviewer",
        };
        if meta["source_commit"] != input.source || request["role"] != role {
            return Err(denied());
        }
        let selection: soda_execution_protocol::ContextSelection = serde_json::from_value(json!({
            "source_commit":meta["source_commit"],"approved_base":meta["approved_base"],
            "diff_base":meta["diff_base"],"candidate":meta["candidate"],"paths":meta["paths"]
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
            + context.files.iter().map(|f| f.content.len()).sum::<usize>();
        if total > soda_execution_protocol::MAX_CONTEXT_BYTES
            || context.files.len() > 12
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
            "setup":encode(setup),"check":encode(check),"files":files,"diff":encode(&context.diff)
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
        let key = self.key(address)?;
        let (input, job) = {
            let mut entries = self.entries.lock().map_err(|_| denied())?;
            let entry = entries.get_mut(&key).ok_or_else(denied)?;
            if address["id"] != entry.input.id || address["workspace"] != entry.input.workspace {
                return Err(denied());
            }
            entry.cancellation.cancel();
            (entry.input.clone(), entry.job.take())
        };
        if let Some(job) = job {
            job.join().map_err(|_| denied())?;
        }
        let sha256 = digest(&input.effective_bundle(&self.directory.join(&key))?);
        Broker::retire_unlaunched(&key, &self.profile, &sha256)?;
        let marker = self.directory.join(&key).join("retired.json");
        if !marker.try_exists()? {
            save(&marker, &json!({"root":key,"bundle":sha256}))?;
        }
        let mut entries = self.entries.lock().map_err(|_| denied())?;
        let entry = entries.get_mut(&key).ok_or_else(denied)?;
        entry.retired = true;
        let completion = entry.completion.lock().map_err(|_| denied())?.clone();
        Ok(input.state(completion.as_ref(), true))
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
