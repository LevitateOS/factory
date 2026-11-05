//! Compare private Go ledger observations with native evidence. These reads
//! grant no launch, broker retirement or product outcome/settlement authority.
use super::{
    read_custody, read_intent, read_retained, AgentRun, CustodyObservation, CustodyState,
    LaunchIntent, ReceiptScope, Role,
};
use crate::product::Products;
use crate::supervisor::NativeChannel;
use serde::Deserialize;
use serde_json::Value;
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
    #[serde(rename = "reconciled")]
    _reconciled: bool,
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
    #[serde(rename = "retirement")]
    _retirement: String,
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
    process: Option<RecordedProcess>,
    custody: RecordedCustody,
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
    context: RecoveryContext,
    run: RecordedRun,
}

/// Missing evidence remains unknown. A receipt proves its retained observation,
/// never current liveness, root retirement, credential release or Go settlement.
pub struct NativeRecoveryEvidence {
    pub intent: Option<LaunchIntent>,
    pub receipt: Option<AgentRun>,
    pub custody: Option<CustodyObservation>,
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
            || native.trust_mode != "isolated_system_worker"
        {
            return Err(refused("native recovery ledger binding differs"));
        }
        let recovery = Self { context, run };
        recovery.scope()?;
        Ok(recovery)
    }

    fn scope(&self) -> io::Result<ReceiptScope<'_>> {
        let role = match self.run.role.as_str() {
            "soda-coder" | "coder" => Role::Coder,
            "soda-reviewer" | "reviewer" => Role::Reviewer,
            _ => return Err(refused("native recovery worker role unsupported")),
        };
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
        let receipt = read_retained(&self.context.directory, &scope)?;
        if let Some(receipt) = &receipt {
            self.compare_process(receipt)?;
        }
        let expected = &self.run.native.provenance.custody;
        // A missing captured connection cannot be replaced by a selector read
        // from an unrelated local journal. It remains unobserved.
        let custody = if expected.connection.is_empty() {
            None
        } else {
            read_custody(&self.context.directory, &scope, &expected.connection)?
        };
        if let Some(observed) = &custody {
            if observed.acquisition_id != expected.acquisition_id
                || observed.generation.to_string() != expected.generation
                || expected.state == "released" && observed.state != CustodyState::Released
                || self.run.native.sealed && !observed.sealed
            {
                return Err(refused("native recovery custody differs"));
            }
        } else if expected.state == "released" {
            return Err(refused("native recovery released custody unconfirmed"));
        }
        self.context.current()?;
        Ok(NativeRecoveryEvidence {
            intent,
            receipt,
            custody,
        })
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
}
