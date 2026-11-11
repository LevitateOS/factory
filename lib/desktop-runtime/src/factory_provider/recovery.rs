//! Compare private Go ledger observations with native evidence. Reads grant no
//! launch or settlement authority. Startup-only recovery separately retires
//! exact sealed resources before the profile owner publishes readiness.
use super::{
    read_custody, read_intent, read_retained, AgentRun, CustodyObservation, CustodyState,
    LaunchIntent, ReceiptScope, Role,
};
use crate::product::Products;
use crate::supervisor::NativeChannel;
use serde::Deserialize;
use serde_json::Value;
use soda_execution_protocol::{CandidateState, ProfileRunState};
use std::io;
use std::path::PathBuf;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

#[derive(Clone)]
pub(crate) struct RecoveryContext {
    directory: PathBuf,
    profile: String,
    instance: String,
    alive: Arc<AtomicBool>,
}
impl RecoveryContext {
    pub(crate) fn new(channel: &NativeChannel, alive: Arc<AtomicBool>) -> Self {
        Self {
            directory: channel
                .socket
                .parent()
                .expect("owned native socket parent")
                .into(),
            profile: channel.profile.clone(),
            instance: channel.instance.clone(),
            alive,
        }
    }
    fn current(&self) -> io::Result<()> {
        if !self.alive.load(Ordering::SeqCst) {
            return Err(refused("native recovery owner retired"));
        }
        Ok(())
    }
}

// Boundary DTOs for native_execution_recovery.go. Go validates its canonical
// record and placement; Rust checks the captured response and local selectors.
// Underscored fields check wire shape without interpreting product state here.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Envelope {
    kind: String,
    version: u8,
    profile: String,
    instance: String,
    run: RecordedRun,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedRun {
    id: String,
    workspace: String,
    role: String,
    source_commit: String,
    deadline: String,
    #[serde(rename = "context_pending")]
    _context_pending: bool,
    reconciled: bool,
    #[serde(rename = "outcome")]
    _outcome: String,
    native: RecordedNative,
    launch: Option<Value>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedNative {
    #[serde(default)]
    intent: String,
    tool: RecordedTool,
    bundle_sha256: String,
    prompt_sha256: String,
    provenance: RecordedProvenance,
    retirement: String,
    sealed: bool,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedTool {
    #[serde(rename = "harness")]
    _harness: String,
    #[serde(rename = "version")]
    _version: String,
    sha256: String,
    executable: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedProvenance {
    trust_mode: String,
    profile: String,
    instance_id: String,
    worktree_id: String,
    broker_run: String,
    isolation: Option<RecordedIsolation>,
    connector: Option<RecordedConnector>,
    process: Option<RecordedProcess>,
    custody: RecordedCustody,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedConnector {
    uid: u32,
    service: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedIsolation {
    desktop_uid: u32,
    worker_uid: u32,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedProcess {
    invocation_id: String,
    system_instance: String,
    start_identity: String,
    pid: u32,
    started_at: String,
    stopped_at: Option<String>,
    executable: String,
    executable_sha256: String,
    #[serde(rename = "exit_code")]
    _exit_code: Option<i32>,
    #[serde(rename = "exit_signal")]
    _exit_signal: Option<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordedCustody {
    state: String,
    acquisition_id: String,
    connection: String,
    generation: String,
}

/// A validated channel/scope snapshot, not a current product capability. Its
/// original instance/worktree and optional placement are retained unchanged.
pub struct NativeRecovery {
    products: Products,
    context: RecoveryContext,
    run: RecordedRun,
}

/// Missing evidence remains unknown. A receipt proves its retained observation,
/// never current liveness, root retirement, credential release or Go settlement.
pub struct NativeRecoveryEvidence {
    pub intent: Option<LaunchIntent>,
    pub receipt: Option<AgentRun>,
    pub custody: Option<CustodyObservation>,
    pub muse: Option<super::MuseRun>,
}

impl NativeRecovery {
    pub(crate) async fn read(products: Products, broker_run: &str) -> io::Result<Self> {
        let context = products.recovery_context();
        context.current()?;
        let response = products
            .recovery_request(broker_run)
            .await
            .map_err(|_| refused("native recovery ledger unavailable"))?;
        context.current()?;
        // Intent JSON is itself a retained string, alongside the captured
        // tool/process paths; bound their combined escaped representation.
        if response.status != 200 || response.body.len() > 65536 {
            return Err(refused("native recovery ledger not observed"));
        }
        let envelope: Envelope = serde_json::from_slice(&response.body)
            .map_err(|_| refused("native recovery ledger shape invalid"))?;
        let run = envelope.run;
        let native = &run.native.provenance;
        if envelope.kind != "native_run_recovery"
            || envelope.version != 1
            || envelope.profile != context.profile
            || envelope.instance != context.instance
            || native.profile != context.profile
            || native.broker_run != broker_run
            || !matches!(
                native.trust_mode.as_str(),
                "isolated_system_worker" | "cli_managed_provider"
            )
        {
            return Err(refused("native recovery ledger binding differs"));
        }
        let recovery = Self {
            products,
            context,
            run,
        };
        recovery.scope()?;
        Ok(recovery)
    }

    fn scope(&self) -> io::Result<ReceiptScope<'_>> {
        let role = Role::from_any(&self.run.role)
            .ok_or_else(|| refused("native recovery worker role unsupported"))?;
        ReceiptScope::new(
            &self.run.native.provenance.broker_run,
            &self.context.profile,
            &self.run.native.bundle_sha256,
            &self.run.source_commit,
            role,
            &self.run.native.tool.sha256,
            &self.run.native.prompt_sha256,
        )
    }

    pub fn run(&self) -> &str {
        &self.run.id
    }
    pub fn workspace(&self) -> &str {
        &self.run.workspace
    }
    pub fn broker_run(&self) -> &str {
        &self.run.native.provenance.broker_run
    }
    pub fn original_instance(&self) -> &str {
        &self.run.native.provenance.instance_id
    }
    pub fn worktree(&self) -> &str {
        &self.run.native.provenance.worktree_id
    }
    pub fn recorded_placement(&self) -> Option<&Value> {
        self.run.launch.as_ref()
    }

    /// Read only from the captured owned profile, never a supplied path. Do not
    /// use this snapshot to skip Go's fresh transactional outcome validation.
    pub fn read_evidence(&self) -> io::Result<NativeRecoveryEvidence> {
        self.context.current()?;
        let scope = self.scope()?;
        let intent = read_intent(&self.context.directory, &scope)?;
        match (&intent, self.run.native.intent.is_empty()) {
            (None, true) => (), // Historical absence is still unknown.
            (Some(intent), false)
                if intent.raw() == self.run.native.intent
                    && intent.matches_record(
                        &self.run.id,
                        &self.run.workspace,
                        &self.run.native.provenance.instance_id,
                        &self.run.native.provenance.worktree_id,
                        &self.run.deadline,
                        self.run.launch.as_ref(),
                    ) =>
            {
                ()
            }
            _ => return Err(refused("native recovery intent unconfirmed or differs")),
        }
        if let Some(intent) = intent.as_ref().filter(|intent| intent.muse_login()) {
            let muse = super::muse_receipt::retained(&self.context.directory, intent)?;
            if let Some(result) = &muse {
                self.compare_muse(&result.process)?;
            }
            self.context.current()?;
            return Ok(NativeRecoveryEvidence {
                intent: Some(intent.clone()),
                receipt: None,
                custody: None,
                muse,
            });
        }
        let receipt = read_retained(&self.context.directory, &scope)?;
        if let Some(receipt) = &receipt {
            self.compare_process(receipt)?;
        }
        let expected = &self.run.native.provenance.custody;
        // The verified intent can bind the first acquisition not yet in Go.
        // An unrelated local journal never fills a missing captured connection.
        let connection = if !expected.connection.is_empty() {
            Some(expected.connection.as_str())
        } else {
            intent.as_ref().map(LaunchIntent::connection)
        };
        let custody = if let Some(connection) = connection {
            read_custody(&self.context.directory, &scope, connection)?
        } else {
            None
        };
        if let Some(observed) = &custody {
            if !expected.acquisition_id.is_empty()
                && (observed.acquisition_id != expected.acquisition_id
                    || observed.generation.to_string() != expected.generation)
                || expected.state == CustodyState::Released.as_str()
                    && observed.state != CustodyState::Released
                || self.run.native.sealed && !observed.sealed()
            {
                return Err(refused("native recovery custody differs"));
            }
        } else if expected.state == CustodyState::Released.as_str() {
            return Err(refused("native recovery released custody unconfirmed"));
        }
        self.context.current()?;
        Ok(NativeRecoveryEvidence {
            intent,
            receipt,
            custody,
            muse: None,
        })
    }

    /// Report retained native facts through the same joined private owner.
    /// The Go transaction rechecks its current row and immutable intent. This
    /// does not retire the broker root or settle an outcome/reservation.
    pub async fn record_evidence(&self) -> io::Result<()> {
        let evidence = self.read_evidence()?;
        let intent = evidence
            .intent
            .ok_or_else(|| refused("native recovery intent not observed"))?;
        if intent.muse_login() {
            let result = evidence
                .muse
                .ok_or_else(|| refused("native CLI receipt not observed"))?;
            if !result.root_retired && self.original_instance() == self.context.instance {
                return Err(refused("current CLI MCP owner retirement unconfirmed"));
            }
            super::muse_receipt::service_stopped(&self.context.directory, &result.process)?;
            let process = &result.process;
            let body = serde_json::to_vec(&serde_json::json!({"intent":intent.raw(),
                "custody":super::custody::not_acquired_wire(true),
                "receipt":{"worker_uid":0,"connector_uid":process.uid,"service":process.service,"namespace_pid":0,"host_pid":process.pid,
                    "invocation":process.invocation,"system_instance":process.system_instance,"start_identity":process.start_identity,
                    "started_unix_ms":process.started_unix_ms,"stopped_unix_ms":process.stopped_unix_ms,"exit_code":process.exit_code,"exit_signal":process.exit_signal}}))?;
            self.context.current()?;
            let response = self
                .products
                .record_recovery(&body)
                .await
                .map_err(|_| refused("native CLI observation unavailable"))?;
            self.context.current()?;
            return self.acknowledgement(response, "native_observation_recorded");
        }
        let receipt = evidence
            .receipt
            .ok_or_else(|| refused("native recovery receipt not observed"))?;
        let custody = evidence.custody.as_ref().map(CustodyObservation::wire);
        let body = serde_json::to_vec(&serde_json::json!({"intent":intent.raw(),"custody":custody,"receipt":{
            "worker_uid":receipt.worker_uid,"invocation":receipt.process.invocation,
            "system_instance":receipt.process.system_instance,"start_identity":receipt.process.start_identity,
            "namespace_pid":receipt.process.namespace_pid,"started_unix_ms":receipt.process.started_unix_ms,
            "exit_code":receipt.exit_code,"exit_signal":receipt.exit_signal,
        }})).map_err(|_|refused("native recovery observation encoding invalid"))?;
        self.context.current()?;
        let response = self
            .products
            .record_recovery(&body)
            .await
            .map_err(|_| refused("native recovery observation unavailable"))?;
        self.context.current()?;
        self.acknowledgement(response, "native_observation_recorded")
    }

    fn acknowledgement(&self, response: soda_unix_http::Response, status: &str) -> io::Result<()> {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Ack {
            status: String,
            instance: String,
            profile: String,
            run: String,
        }
        if response.status != 200 || response.body.len() > 1024 {
            return Err(refused("native recovery observation refused"));
        }
        let ack: Ack = serde_json::from_slice(&response.body)
            .map_err(|_| refused("native recovery acknowledgement invalid"))?;
        if ack.status != status
            || ack.instance != self.context.instance
            || ack.profile != self.context.profile
            || ack.run != self.run.id
        {
            return Err(refused("native recovery acknowledgement binding differs"));
        }
        Ok(())
    }

    /// Called only by the profile lifecycle owner before publishing startup.
    /// Complete retained work and a prior local seal are required; no missing
    /// evidence is repaired by assuming the old owner is gone.
    async fn recover_root(&self) -> io::Result<()> {
        let evidence = self.read_evidence()?;
        let intent = evidence
            .intent
            .ok_or_else(|| refused("native recovery intent missing"))?;
        if intent.muse_login() {
            let result = evidence
                .muse
                .ok_or_else(|| refused("native CLI receipt missing"))?;
            if !result.root_retired && self.original_instance() == self.context.instance {
                return Err(refused("current CLI MCP owner retirement unconfirmed"));
            }
            super::muse_receipt::service_stopped(&self.context.directory, &result.process)?;
            if !self.run.reconciled {
                self.record_evidence().await?;
            }
            self.context.current()?;
            if intent.has_tasks() {
                if !result.root_retired {
                    super::Broker::recover_task_receipts(
                        &self.context.directory,
                        &intent.scope()?,
                        &intent.retained_manifest(&self.context.directory)?,
                        &soda_execution_protocol::workspace::Parent::from_muse(&result.process),
                    )?;
                }
                super::Broker::retire_bundle(
                    self.broker_run(),
                    &self.context.profile,
                    &self.run.native.bundle_sha256,
                )?;
            } else {
                super::Broker::retire_unlaunched(
                    self.broker_run(),
                    &self.context.profile,
                    &self.run.native.bundle_sha256,
                )?;
            }
            super::muse_receipt::mark_retired(&self.context.directory, &intent, &result.process)?;
            return self
                .record_root(&intent, &result.process.invocation, "")
                .await;
        }
        let receipt = evidence
            .receipt
            .ok_or_else(|| refused("native recovery receipt missing"))?;
        let custody = evidence
            .custody
            .ok_or_else(|| refused("native recovery acquisition missing"))?;
        if !custody.sealed()
            || !receipt.output_complete
            || Role::from_any(&self.run.role) != Some(Role::Reviewer)
                && !matches!(
                    receipt.candidate_state,
                    CandidateState::Captured | CandidateState::Unchanged
                )
        {
            return Err(refused(
                "native recovery local seal or complete work unconfirmed",
            ));
        }
        if self.run.reconciled {
            if self.run.native.retirement != "retired" || custody.state != CustodyState::Released {
                return Err(refused("settled native recovery facts differ"));
            }
        } else {
            self.record_evidence().await?;
        }
        self.context.current()?;
        super::Broker::retire_launched(
            self.broker_run(),
            &self.context.profile,
            &self.run.native.bundle_sha256,
        )?;
        self.context.current()?;
        super::custody::release_recovered(&self.context.directory, &self.scope()?, &custody)?;
        if !self.run.reconciled {
            self.record_evidence().await?;
        }
        self.record_root(
            &intent,
            &receipt.process.invocation,
            &custody.acquisition_id,
        )
        .await
    }
    async fn record_root(
        &self,
        intent: &LaunchIntent,
        invocation: &str,
        acquisition: &str,
    ) -> io::Result<()> {
        let body = serde_json::to_vec(
            &serde_json::json!({"intent":intent.raw(),"invocation":invocation,"acquisition_id":acquisition}),
        )?;
        let response = self
            .products
            .retire_recovery(&body)
            .await
            .map_err(|_| refused("native root retirement record unavailable"))?;
        self.context.current()?;
        self.acknowledgement(response, "native_root_retirement_recorded")
    }

    fn compare_process(&self, observed: &AgentRun) -> io::Result<()> {
        let native = &self.run.native.provenance;
        if let Some(process) = &native.process {
            let started = OffsetDateTime::parse(&process.started_at, &Rfc3339)
                .map_err(|_| refused("native recovery process time invalid"))?;
            let exact_time = started.unix_timestamp_nanos()
                == i128::from(observed.process.started_unix_ms) * 1_000_000;
            let isolation = native
                .isolation
                .as_ref()
                .ok_or_else(|| refused("native recovery isolation unobserved"))?;
            if process.invocation_id != observed.process.invocation
                || process.system_instance != observed.process.system_instance
                || process.start_identity != observed.process.start_identity
                || process.pid != observed.process.namespace_pid
                || !exact_time
                || process.executable != self.run.native.tool.executable
                || process.executable_sha256 != self.run.native.tool.sha256
                || isolation.desktop_uid != unsafe { libc::geteuid() }
                || isolation.worker_uid != observed.worker_uid
            {
                return Err(refused("native recovery process anchor differs"));
            }
        }
        // Canonical outcomes and terminal-field advancement belong to Go's
        // transaction owner. This method compares the immutable native anchor.
        Ok(())
    }
    fn compare_muse(&self, observed: &soda_execution_protocol::MuseObservation) -> io::Result<()> {
        let native = &self.run.native.provenance;
        if native.trust_mode != "cli_managed_provider"
            || native.isolation.is_some()
            || native.custody.state != CustodyState::NotAcquired.as_str()
            || !native.custody.acquisition_id.is_empty()
            || !native.custody.connection.is_empty()
            || native.custody.generation != "0"
        {
            return Err(refused("CLI custody or trust mode differs"));
        }
        if let Some(process) = &native.process {
            let started = OffsetDateTime::parse(&process.started_at, &Rfc3339)
                .map_err(|_| refused("CLI process time invalid"))?;
            if let Some(stopped) = &process.stopped_at {
                let stopped = OffsetDateTime::parse(stopped, &Rfc3339)
                    .map_err(|_| refused("CLI stop time invalid"))?;
                if observed
                    .stopped_unix_ms
                    .map(|ms| i128::from(ms) * 1_000_000)
                    != Some(stopped.unix_timestamp_nanos())
                {
                    return Err(refused("CLI stop time differs"));
                }
            }
            let connector = native
                .connector
                .as_ref()
                .ok_or_else(|| refused("CLI connector identity missing"))?;
            if process.invocation_id != observed.invocation
                || process.system_instance != observed.system_instance
                || process.start_identity != observed.start_identity
                || process.pid != observed.pid
                || started.unix_timestamp_nanos()
                    != i128::from(observed.started_unix_ms) * 1_000_000
                || process.executable != self.run.native.tool.executable
                || process.executable_sha256 != observed.executable_sha256
                || connector.uid != observed.uid
                || connector.service != observed.service
            {
                return Err(refused("CLI process anchor differs"));
            }
        } else if native.connector.is_some() {
            return Err(refused("CLI connector lacks process anchor"));
        }
        Ok(())
    }
}

/// Startup-only reconciliation. The caller retains the exclusive profile lock,
/// Go Store is available, and product/provider startup has not been published.
pub(crate) async fn recover_profile(products: Products, profile: &str) -> io::Result<usize> {
    let captured = products.recovery_context();
    captured.current()?;
    if captured.profile != profile {
        return Err(refused("native recovery profile differs"));
    }
    let mut seen = std::collections::BTreeSet::new();
    for _ in 0..64 {
        let Some((run, source, state)) = super::Broker::next_profile_state(profile)? else {
            break;
        };
        if !seen.insert(run.clone()) {
            return Err(refused("native root recovery did not advance"));
        }
        let cli = super::intent::read_run(&captured.directory, profile, &run)?
            .is_some_and(|intent| intent.muse_login());
        if cli {
            let recovered = NativeRecovery::read(products.clone(), &run).await?;
            if recovered.run.native.bundle_sha256 != source {
                return Err(refused("CLI recovery source differs"));
            }
            recovered.recover_root().await?;
        } else if matches!(
            state,
            ProfileRunState::Staged | ProfileRunState::RetiringUnlaunched
        ) {
            // A launched or unproven retirement keeps Go recovery below.
            if !super::Broker::retire_unlaunched(&run, profile, &source)? {
                return Err(refused("native staging recovery did not advance"));
            }
        } else {
            let recovered = NativeRecovery::read(products.clone(), &run).await?;
            if recovered.run.native.bundle_sha256 != source {
                return Err(refused("native root recovery source differs"));
            }
            recovered.recover_root().await?;
        }
    }
    if super::Broker::next_profile_run(profile)?.is_some() {
        return Err(refused("native root recovery capacity exceeded"));
    }
    // Also finish a lost Go acknowledgement after the root already disappeared.
    let context = products.recovery_context();
    let mut cursor = "0".to_owned();
    loop {
        context.current()?;
        let response = products
            .pending_recovery(&cursor)
            .await
            .map_err(|_| refused("native pending recovery unavailable"))?;
        if response.status != 200 || response.body.len() > 16384 {
            return Err(refused("native pending recovery refused"));
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Candidate {
            broker_run: String,
            source_sha256: String,
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Page {
            kind: String,
            version: u8,
            profile: String,
            instance: String,
            items: Vec<Candidate>,
            next: String,
        }
        let page: Page = serde_json::from_slice(&response.body)
            .map_err(|_| refused("native pending recovery shape invalid"))?;
        if page.kind != "native_recovery_pending"
            || page.version != 1
            || page.profile != context.profile
            || page.instance != context.instance
            || page.items.len() > 32
            || !page.next.is_empty()
                && !page.next.parse::<i64>().is_ok_and(|value| {
                    value > cursor.parse::<i64>().unwrap_or(-1) && value.to_string() == page.next
                })
        {
            return Err(refused("native pending recovery binding differs"));
        }
        for candidate in page.items {
            let recovered = NativeRecovery::read(products.clone(), &candidate.broker_run).await?;
            if recovered.run.native.bundle_sha256 != candidate.source_sha256 {
                return Err(refused("native pending recovery source differs"));
            }
            recovered.recover_root().await?;
            seen.insert(candidate.broker_run);
        }
        if page.next.is_empty() {
            break;
        }
        cursor = page.next;
    }
    context.current()?;
    Ok(seen.len())
}
