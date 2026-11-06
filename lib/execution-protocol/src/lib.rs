//! Nonsecret wire shapes and pure bounds. Callers independently admit peers,
//! processes and authority before using these observations.
use serde::{Deserialize, Serialize};

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
