//! Native effects behind Go's current recorded launch admission.
use super::{Budget, ExecutionStart, Executions, LaunchIntent, Policy, WorkerMcp};
use crate::credentials::{Credentials, Provider, TokenCapability};
use crate::supervisor::NativeChannel;
use base64::Engine;
use serde_json::{json, Value};
use std::collections::BTreeMap;
use std::io;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex, RwLock,
};

pub(crate) struct Account {
    credentials: Arc<Credentials>,
    capability: TokenCapability,
}
impl Account {
    pub(crate) fn new(
        credentials: Arc<Credentials>,
        capability: TokenCapability,
    ) -> io::Result<Self> {
        if capability.account().provider != Provider::Muse
            || capability.account().origin != "https://api.meta.ai"
        {
            return Err(denied());
        }
        Ok(Self {
            credentials,
            capability,
        })
    }
    pub(crate) fn connection(&self) -> &str {
        &self.capability.account().connection
    }
    pub(crate) async fn observe(&self, profile: &str) -> io::Result<Value> {
        let current = self
            .credentials
            .renew_capability(&self.capability)
            .await
            .map_err(|_| denied())?;
        let binding = self
            .credentials
            .credential_fingerprint(&current)
            .map_err(|_| denied())?;
        Ok(json!({"profile":profile,"connection":self.connection(),"credential_sha256":binding}))
    }
}
fn denied() -> io::Error {
    io::Error::new(
        io::ErrorKind::PermissionDenied,
        "native execution enrollment or captured launch unavailable",
    )
}
pub(crate) struct Input {
    pub intent: String,
    pub manifest: String,
    pub prompt: String,
    pub capability: String,
    pub secret: String,
    pub tools: Vec<String>,
}

pub(crate) async fn launch(
    channel: NativeChannel,
    alive: Arc<AtomicBool>,
    executions: Arc<Mutex<Executions>>,
    preparations: Arc<super::preparations::Preparations>,
    accounts: Arc<RwLock<BTreeMap<String, Arc<Account>>>>,
    input: Input,
) -> io::Result<Value> {
    let intent = LaunchIntent::from_go(&input.intent)?;
    if input.manifest.len() > soda_execution_protocol::MAX_TASK_MANIFEST.div_ceil(3) * 4 {
        return Err(denied());
    }
    let manifest = base64::engine::general_purpose::STANDARD
        .decode(input.manifest)
        .map_err(|_| denied())?;
    intent.validate_manifest(&manifest)?;
    if !manifest.is_empty() && !intent.muse_login() {
        return Err(denied());
    }
    let scope = intent.scope()?;
    if !alive.load(Ordering::SeqCst)
        || intent.instance() != channel.instance
        || scope.profile != channel.profile
        || input.capability != intent.run()
    {
        return Err(denied());
    }
    let prompt = base64::engine::general_purpose::STANDARD
        .decode(input.prompt)
        .map_err(|_| denied())?;
    if prompt.is_empty() || prompt.len() > soda_execution_protocol::MAX_PROMPT {
        return Err(denied());
    }
    let selected = intent.clone();
    let (bundle, preparation) =
        tokio::task::spawn_blocking(move || preparations.execution_material(&selected))
            .await
            .map_err(|_| denied())??;
    let mcp = WorkerMcp::connected(
        channel.clone(),
        &intent,
        &input.tools,
        &input.capability,
        &input.secret,
    )?;
    if intent.muse_login() {
        if intent.connection() != super::muse_connection(&channel.profile) {
            return Err(denied());
        }
        let directory = channel.socket.parent().ok_or_else(denied)?.to_path_buf();
        let helper = super::muse_helper()?;
        let binding = super::ExecutionBinding::from_intent(&intent)?;
        let digest = intent.digest();
        let mut executions = executions.lock().map_err(|_| denied())?;
        if !alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        executions.ensure_attempt(binding, move |cancel| {
            super::muse_attempt::MuseAttempt::start(
                directory,
                helper,
                intent,
                bundle,
                prompt,
                manifest,
                preparation,
                mcp,
                cancel,
            )
            .map(super::executions::OwnedAttempt::Muse)
        })?;
        return Ok(json!({"accepted":true,"intent_digest":digest}));
    }
    let account = accounts
        .read()
        .map_err(|_| denied())?
        .get(intent.connection())
        .cloned()
        .ok_or_else(denied)?;
    let capability = account
        .credentials
        .renew_capability(&account.capability)
        .await
        .map_err(|_| denied())?;
    if let Some(expected) = intent.credential_sha256() {
        if account
            .credentials
            .credential_fingerprint(&capability)
            .map_err(|_| denied())?
            != expected
        {
            return Err(denied());
        }
    }
    let policy = Policy::new(
        scope.run,
        scope.profile,
        scope.source_sha256,
        scope.candidate,
        intent.connection(),
        scope.role,
        super::MAX_RUN,
        Budget::new(
            super::MAX_REQUESTS,
            super::MAX_INPUT_BYTES,
            super::MAX_OUTPUT_TOKENS,
        )
        .map_err(|_| denied())?,
    )
    .map_err(|_| denied())?;
    let directory = channel.socket.parent().ok_or_else(denied)?.to_path_buf();
    let executable = intent.executable().to_path_buf();
    let digest = intent.digest();
    if !alive.load(Ordering::SeqCst) {
        return Err(denied());
    }
    let mut executions = executions.lock().map_err(|_| denied())?;
    if !alive.load(Ordering::SeqCst) {
        return Err(denied());
    }
    executions.launch(
        directory,
        ExecutionStart {
            intent,
            manifest,
            policy,
            credentials: account.credentials.clone(),
            capability,
            bundle,
            prompt,
            executable,
            mcp,
        },
        preparation,
    )?;
    Ok(json!({"accepted":true,"intent_digest":digest}))
}
