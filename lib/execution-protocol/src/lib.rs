//! Nonsecret wire shapes and pure bounds. Callers independently admit peers,
//! processes and authority before using these observations.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;

pub const MAX_STAGE_BUNDLE: usize = 512 * 1024;
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
#[derive(Clone, Deserialize, Serialize)]
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
        }) && self.paths.len() <= 12
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

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct AgentStarted<S = String> {
    pub kind: S,
    pub version: u8,
    pub run: S,
    pub profile: S,
    pub candidate: S,
    pub role: S,
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
    pub role: S,
    pub tool_sha256: S,
    pub prompt_sha256: S,
    pub worker_uid: u32,
    pub peer_uid: u32,
    pub invocation: S,
    pub system_instance: S,
    pub start_identity: S,
    pub namespace_pid: u32,
    pub started_unix_ms: u64,
    pub reason: S,
    pub exit_code: Option<i32>,
    pub exit_signal: Option<i32>,
    pub tree_retired: bool,
    pub output_complete: bool,
    pub output_length: usize,
    pub output_sha256: S,
    pub candidate_state: S,
    pub result_candidate: Option<S>,
    pub bundle_length: usize,
    pub bundle_sha256: Option<S>,
}

impl<S: AsRef<str>> AgentExecution<S> {
    pub fn candidate_valid(&self) -> bool {
        candidate_result_valid(
            self.role.as_ref(),
            self.candidate.as_ref(),
            self.candidate_state.as_ref(),
            self.result_candidate.as_ref().map(AsRef::as_ref),
            self.bundle_length,
            self.bundle_sha256.as_ref().map(AsRef::as_ref),
        )
    }
}

pub fn candidate_result_valid(
    role: &str,
    candidate: &str,
    state: &str,
    result: Option<&str>,
    bundle_length: usize,
    bundle_sha256: Option<&str>,
) -> bool {
    if role == "reviewer" {
        return state == "not_applicable"
            && result.is_none()
            && bundle_length == 0
            && bundle_sha256.is_none();
    }
    if role != "coder" {
        return false;
    }
    if state == "captured" {
        return result.is_some_and(|commit| hex(commit, 40) && commit != candidate)
            && (1..=MAX_CANDIDATE_BUNDLE).contains(&bundle_length)
            && bundle_sha256.is_some_and(|digest| hex(digest, 64));
    }
    matches!(state, "unchanged" | "dirty" | "unavailable")
        && result.is_none()
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
