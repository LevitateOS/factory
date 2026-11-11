//! Bounded wire shapes and pure bounds. Product observations are nonsecret;
//! private Muse input may carry only an ephemeral run MCP capability, never an
//! account credential. Callers independently admit peers, processes and authority.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
mod muse;
pub mod workspace;
pub use muse::{MuseInvocation, MuseMcp, MuseObservation, MAX_MUSE_INPUT, MAX_MUSE_MODEL_STEPS};

pub const MAX_STAGE_BUNDLE: usize = 64 * 1024 * 1024;
pub const MAX_PREPARATION_REQUEST: usize = MAX_STAGE_BUNDLE.div_ceil(3) * 4 + 1024 * 1024;
pub const MAX_TASK_MANIFEST: usize = 64 * 1024;
pub const MAX_PROMPT: usize = 320 * 1024;
pub const MAX_EXECUTABLE: u64 = 512 * 1024 * 1024;
pub const MAX_BROKER_FRAME: usize = 1024;
pub const MAX_AGENT_REQUEST: usize = 768;
pub const MAX_OUTPUT: usize = 16 * 1024 * 1024;
pub const MAX_CANDIDATE_BUNDLE: usize = 4 * 1024 * 1024;
pub const MAX_EXECUTION_REPLY: usize = 1536;
pub const MAX_START_REPLY: usize = 1024;
pub const MAX_OBSERVATION_QUERIES: u64 = 65_536;
pub const OBSERVATION_REQUEST_BYTES: usize = 9;
pub const OBSERVATION_MARKER: u8 = b'O';
// Same fixed-size control frame, with eight zero bytes and no new selector.
pub const CANCELLATION_MARKER: u8 = b'C';
pub const MAX_APPROVED_BYTES: usize = 128 * 1024;
pub const MAX_SETUP_FRAME: usize = 600 * 1024;
pub const MAX_SETUP_OUTPUT: usize = 256 * 1024;
// Shared snapshot envelope: preparation and worker transfer must agree.
pub const MAX_PREPARED_TREE_BYTES: u64 = 1024 * 1024 * 1024;
pub const MAX_PREPARED_TREE_ENTRIES: usize = 100_000;

// Both preparation and worker use their own isolated HOME. Never borrow the
// desktop's PATH or tool configuration when replaying an approved environment.
pub fn setup_path(home: &str) -> String {
    format!("{home}/.local/bin:{home}/.cargo/bin:{home}/.bun/bin:/usr/bin:/bin:/usr/local/bin")
}
#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct PreparedSourceRef {
    pub run: String,
    pub candidate: String,
}

pub const MAX_CONTEXT_BYTES: usize = 128 * 1024;
pub const MAX_CONTEXT_REQUEST: usize = 8192;
#[derive(Deserialize, Serialize)]
#[serde(tag = "operation", rename_all = "snake_case", deny_unknown_fields)]
pub enum PreparedSourceOperation {
    Context {
        selection: ContextSelection,
    },
    Candidate {
        base: String,
        candidate: String,
        delta_length: usize,
        delta_sha256: String,
    },
}
#[derive(Deserialize, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum PreparedSourceResult {
    Context {
        context: SourceContext,
    },
    Candidate {
        source_commit: String,
        bundle: Vec<u8>,
        sha256: String,
    },
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ContextSelection {
    pub source_commit: String,
    pub approved_base: String,
    pub diff_base: String,
    pub candidate: String,
    pub paths: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub directory: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub page: Option<ContextReadPage>,
}
impl ContextSelection {
    pub fn valid(&self) -> bool {
        [
            &self.source_commit,
            &self.approved_base,
            &self.diff_base,
            &self.candidate,
        ]
        .into_iter()
        .all(|value| {
            value.len() == 40
                && value
                    .bytes()
                    .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        }) && self
            .directory
            .as_ref()
            .is_none_or(|path| path.is_empty() || Self::valid_path(path))
            && self.paths.len() <= 12
            && self.page.as_ref().is_none_or(|page| {
                self.directory.is_none()
                    && self.paths.len() == 1
                    && self.paths[0] == page.path
                    && page.valid()
            })
            && self
                .paths
                .iter()
                .enumerate()
                .all(|(i, path)| Self::valid_path(path) && !self.paths[..i].contains(path))
    }
    pub fn valid_path(path: &str) -> bool {
        !path.is_empty()
            && path.len() <= 256
            && !path.contains('\\')
            && !path.chars().any(char::is_control)
            && path
                .split('/')
                .all(|part| !part.is_empty() && !matches!(part, "." | ".." | ".git" | ".soda-home"))
    }
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ContextFile {
    pub path: String,
    pub content: Vec<u8>,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct SourceContext {
    pub files: Vec<ContextFile>,
    pub diff: Vec<u8>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub entries: Vec<ContextEntry>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub page: Option<ContextPage>,
}

pub const MAX_CONTEXT_FILE_BYTES: usize = 32 * 1024;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ContextReadPage {
    pub path: String,
    pub offset: u64,
    pub limit: usize,
}
impl ContextReadPage {
    pub fn valid(&self) -> bool {
        ContextSelection::valid_path(&self.path)
            && self.offset <= MAX_PREPARED_TREE_BYTES
            && (4..=MAX_CONTEXT_FILE_BYTES).contains(&self.limit)
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ContextPage {
    pub path: String,
    pub offset: u64,
    pub total_bytes: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub next_offset: Option<u64>,
}

pub const MAX_CONTEXT_ENTRIES: usize = 256;

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ContextEntry {
    pub path: String,
    pub kind: String,
}

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ApprovedFiles {
    pub files: BTreeMap<String, Vec<u8>>,
    pub tools: Vec<String>,
}

/// Optional reviewed `setup-limits.json` is part of the existing approved-file
/// digest. Absence preserves historical preparation limits.
#[derive(Clone, Copy, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SetupLimits {
    pub setup_seconds: u32,
    pub check_seconds: u32,
}
impl Default for SetupLimits {
    fn default() -> Self {
        Self {
            setup_seconds: 10,
            check_seconds: 10,
        }
    }
}
impl SetupLimits {
    pub const MAX_ENTRY_SECONDS: u32 = 1800;
    pub const MAX_PREPARATION_SECONDS: u64 = 2 * Self::MAX_ENTRY_SECONDS as u64 + 90;

    pub fn preparation_seconds(self) -> u64 {
        self.setup_seconds as u64 + self.check_seconds as u64 + 30
    }
}
impl ApprovedFiles {
    pub fn public_https(&self) -> Option<bool> {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Network {
            access: String,
        }
        match self.files.get("setup-network.json") {
            None => Some(false),
            Some(bytes) => serde_json::from_slice::<Network>(bytes)
                .ok()
                .and_then(|network|match network.access.as_str() {"none"=>Some(false),"public_https"=>Some(true),_=>None}),
        }
    }
    pub fn limits(&self) -> Option<SetupLimits> {
        let limits = match self.files.get("setup-limits.json") {
            Some(bytes) => serde_json::from_slice::<SetupLimits>(bytes).ok()?,
            None => SetupLimits::default(),
        };
        ((1..=SetupLimits::MAX_ENTRY_SECONDS).contains(&limits.setup_seconds)
            && (1..=SetupLimits::MAX_ENTRY_SECONDS).contains(&limits.check_seconds))
        .then_some(limits)
    }
    pub fn valid_name(name: &str) -> bool {
        !name.is_empty()
            && name.len() <= 64
            && name.as_bytes()[0].is_ascii_alphanumeric()
            && !name.contains("..")
            && name
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b'.'))
    }
    pub fn valid(&self) -> bool {
        (2..=8).contains(&self.files.len())
            && self.files.contains_key("setup.sh")
            && self.files.contains_key("check.sh")
            && self.limits().is_some()
            && self.public_https().is_some()
            && self
                .files
                .iter()
                .all(|(name, bytes)| Self::valid_name(name) && (1..=32768).contains(&bytes.len()))
            && self.files.values().map(Vec::len).sum::<usize>() <= MAX_APPROVED_BYTES
            && self.tools.len() <= 8
            && self.tools.iter().all(|name| {
                !name.is_empty()
                    && name.len() <= 32
                    && name.bytes().all(|byte| {
                        byte.is_ascii_lowercase()
                            || byte.is_ascii_digit()
                            || matches!(byte, b'+' | b'_' | b'.' | b'-')
                    })
            })
    }
    pub fn digest(&self) -> String {
        let mut hash = Sha256::new();
        for (name, bytes) in &self.files {
            hash.update(name.as_bytes());
            hash.update([0]);
            hash.update(bytes);
        }
        hash.finalize()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    }
}
#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SetupReceipt {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub prepared_source: Option<PreparedSourceRef>,
    pub worker_uid: u32,
    pub setup_sha256: String,
    pub source_commit: String,
    pub setup_exit: Option<i32>,
    pub check_exit: Option<i32>,
    pub tools: BTreeMap<String, String>,
    pub missing: Vec<String>,
    pub output: String,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub output_truncated: bool,
}

/// Root-owned reference to a successful immutable setup, fenced against replacement.
#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct CapturedSetup {
    pub source: PreparedSourceRef,
    pub source_device: u64,
    pub source_inode: u64,
    pub receipt: SetupReceipt,
}

/// Captured run choices, never inherited from a CLI home or machine default.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct HarnessProfile {
    pub model: String,
    pub effort: String,
    pub contributor: bool,
}
impl HarnessProfile {
    pub fn valid(&self) -> bool {
        self.model == "muse-spark-1.3-contributor"
            && matches!(
                self.effort.as_str(),
                "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra"
            )
    }
    /// Component callers without a Go intent retain their previous CLI defaults.
    pub fn legacy() -> Self {
        Self {
            model: "muse-spark-1.3-contributor".into(),
            effort: "high".into(),
            contributor: true,
        }
    }
}
/// Why a supervised process stopped. `Unconfirmed` is written when no reason
/// was observed; Muse receipts do not accept it.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum StopReason {
    Exited,
    Cancelled,
    Deadline,
    OutputLimit,
    OutputUnavailable,
    InputUnavailable,
    ObservationUnavailable,
    Unconfirmed,
}
impl StopReason {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Exited => "exited",
            Self::Cancelled => "cancelled",
            Self::Deadline => "deadline",
            Self::OutputLimit => "output_limit",
            Self::OutputUnavailable => "output_unavailable",
            Self::InputUnavailable => "input_unavailable",
            Self::ObservationUnavailable => "observation_unavailable",
            Self::Unconfirmed => "unconfirmed",
        }
    }
}

/// `Unavailable` is read-only: persisted receipts may carry it, no producer emits it.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CandidateState {
    NotApplicable,
    Unchanged,
    Dirty,
    Captured,
    Unavailable,
}
impl CandidateState {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::NotApplicable => "not_applicable",
            Self::Unchanged => "unchanged",
            Self::Dirty => "dirty",
            Self::Captured => "captured",
            Self::Unavailable => "unavailable",
        }
    }
}

/// Worker role on the wire and in receipts ("reviewer"/"coder"). The Go product
/// names ("soda-reviewer"/"soda-coder") are separate and parsed explicitly.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Role {
    Reviewer,
    Coder,
}
impl Role {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Reviewer => "reviewer",
            Self::Coder => "coder",
        }
    }
    pub fn product(self) -> &'static str {
        match self {
            Self::Reviewer => "soda-reviewer",
            Self::Coder => "soda-coder",
        }
    }
    pub fn from_product(name: &str) -> Option<Self> {
        match name {
            "soda-reviewer" => Some(Self::Reviewer),
            "soda-coder" => Some(Self::Coder),
            _ => None,
        }
    }
    pub fn from_wire(name: &str) -> Option<Self> {
        match name {
            "reviewer" => Some(Self::Reviewer),
            "coder" => Some(Self::Coder),
            _ => None,
        }
    }
    /// Accepts both the product and the worker spelling.
    pub fn from_any(name: &str) -> Option<Self> {
        Self::from_product(name).or_else(|| Self::from_wire(name))
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct AgentStarted<S = String> {
    pub kind: S,
    pub version: u8,
    pub run: S,
    pub profile: S,
    pub candidate: S,
    pub role: Role,
    pub tool_sha256: S,
    pub prompt_sha256: S,
    pub worker_uid: u32,
    pub peer_uid: u32,
    pub invocation: S,
    pub system_instance: S,
    pub start_identity: S,
    pub namespace_pid: u32,
    pub started_unix_ms: u64,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct AgentExecution<S = String> {
    pub kind: S,
    pub version: u8,
    pub run: S,
    pub profile: S,
    pub candidate: S,
    pub role: Role,
    pub tool_sha256: S,
    pub prompt_sha256: S,
    pub worker_uid: u32,
    pub peer_uid: u32,
    pub invocation: S,
    pub system_instance: S,
    pub start_identity: S,
    pub namespace_pid: u32,
    pub started_unix_ms: u64,
    pub reason: StopReason,
    pub exit_code: Option<i32>,
    pub exit_signal: Option<i32>,
    pub tree_retired: bool,
    pub output_complete: bool,
    pub output_length: usize,
    pub output_sha256: S,
    pub candidate_state: CandidateState,
    pub result_candidate: Option<S>,
    pub bundle_length: usize,
    pub bundle_sha256: Option<S>,
}

/// What the supervising process expects an agent worker to report.
pub struct AgentExpectation<'a> {
    pub run: &'a str,
    pub profile: &'a str,
    pub candidate: &'a str,
    pub role: Role,
    pub tool_sha256: &'a str,
    pub prompt_sha256: &'a str,
    /// The supervisor's own uid; the worker must run as a different one.
    pub supervisor_uid: u32,
}

fn boot_id(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
}

impl<S: AsRef<str>> AgentStarted<S> {
    pub fn valid(&self, expected: &AgentExpectation<'_>) -> bool {
        self.kind.as_ref() == "agent_started"
            && self.version == 1
            && self.run.as_ref() == expected.run
            && self.profile.as_ref() == expected.profile
            && self.candidate.as_ref() == expected.candidate
            && self.role == expected.role
            && self.tool_sha256.as_ref() == expected.tool_sha256
            && self.prompt_sha256.as_ref() == expected.prompt_sha256
            && self.worker_uid != 0
            && self.worker_uid != expected.supervisor_uid
            && self.peer_uid == 0
            && hex(self.invocation.as_ref(), 32)
            && boot_id(self.system_instance.as_ref())
            && matches!(self.start_identity.as_ref().parse::<u64>(), Ok(ticks) if ticks > 0)
            && self.namespace_pid != 0
            && self.started_unix_ms != 0
    }
}

impl<S: AsRef<str>> AgentExecution<S> {
    pub fn candidate_valid(&self) -> bool {
        candidate_result_valid(
            self.role,
            self.candidate.as_ref(),
            self.candidate_state,
            self.result_candidate.as_ref().map(AsRef::as_ref),
            self.bundle_length,
            self.bundle_sha256.as_ref().map(AsRef::as_ref),
        )
    }
    /// Full admission of an execution reply against the expectation and the
    /// start announcement of the same process.
    pub fn valid(&self, expected: &AgentExpectation<'_>, started: &AgentStarted<S>) -> bool {
        self.kind.as_ref() == "agent_executed"
            && self.version == 3
            && self.run.as_ref() == expected.run
            && self.profile.as_ref() == expected.profile
            && self.candidate.as_ref() == expected.candidate
            && self.role == expected.role
            && self.tool_sha256.as_ref() == expected.tool_sha256
            && self.prompt_sha256.as_ref() == expected.prompt_sha256
            && self.worker_uid != 0
            && self.worker_uid != expected.supervisor_uid
            && self.peer_uid == 0
            && hex(self.invocation.as_ref(), 32)
            && boot_id(self.system_instance.as_ref())
            && matches!(self.start_identity.as_ref().parse::<u64>(), Ok(ticks) if ticks > 0)
            && self.namespace_pid != 0
            && self.started_unix_ms != 0
            && self.worker_uid == started.worker_uid
            && self.invocation.as_ref() == started.invocation.as_ref()
            && self.system_instance.as_ref() == started.system_instance.as_ref()
            && self.start_identity.as_ref() == started.start_identity.as_ref()
            && self.namespace_pid == started.namespace_pid
            && self.started_unix_ms == started.started_unix_ms
            && self.tree_retired
            && self.output_length <= MAX_OUTPUT
            && hex(self.output_sha256.as_ref(), 64)
            && self.candidate_valid()
            && self.exit_code.is_none_or(|code| (0..=255).contains(&code))
            && self
                .exit_signal
                .is_none_or(|signal| (1..=64).contains(&signal))
            && !(self.exit_code.is_some() && self.exit_signal.is_some())
            && (self.reason != StopReason::Exited
                || self.exit_code.is_some()
                || self.exit_signal.is_some())
    }
}

pub fn candidate_result_valid(
    role: Role,
    candidate: &str,
    state: CandidateState,
    result: Option<&str>,
    bundle_length: usize,
    bundle_sha256: Option<&str>,
) -> bool {
    if role == Role::Reviewer {
        return state == CandidateState::NotApplicable
            && result.is_none()
            && bundle_length == 0
            && bundle_sha256.is_none();
    }
    if state == CandidateState::Captured {
        return result.is_some_and(|commit| hex(commit, 40) && commit != candidate)
            && (1..=MAX_CANDIDATE_BUNDLE).contains(&bundle_length)
            && bundle_sha256.is_some_and(|digest| hex(digest, 64));
    }
    matches!(
        state,
        CandidateState::Unchanged | CandidateState::Dirty | CandidateState::Unavailable
    ) && result.is_none()
        && bundle_length == 0
        && bundle_sha256.is_none()
}

fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct AgentObserved {
    pub kind: String,
    pub version: u8,
    pub sequence: u64,
    pub live: bool,
}

/// What the root broker's restart inspection found for one profile run.
/// Only the two unlaunched states may be retired without a recovered outcome.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ProfileRunState {
    /// Staged with no launch declaration or consumed marker. A run staged
    /// without a lifecycle declaration also reports this; the broker still
    /// refuses to retire it as unlaunched.
    Staged,
    /// Staged with a launched or torn declaration or a consumed marker.
    Launched,
    /// An interrupted retirement whose retained declaration proves no launch.
    RetiringUnlaunched,
    /// An interrupted retirement that cannot prove it was never launched.
    Retiring,
}
