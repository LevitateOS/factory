//! Stable native collaboration contracts. Provider transports and product authority stay outside.
mod payloads;
pub(crate) mod read;
pub use payloads::*;
pub use read::Read;
use serde_json::Value;

/// Safe provider outcome. Never retains an upstream error, URL, body or grant.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Failure {
    pub status: u16,
    pub code: &'static str,
    pub retry_after: Option<u64>,
    pub reset: Option<u64>,
}
impl Failure {
    pub(crate) fn new(status: u16, code: &'static str) -> Self {
        Self {
            status,
            code,
            retry_after: None,
            reset: None,
        }
    }
    pub(crate) fn unavailable() -> Self {
        Self::new(503, "github_unavailable")
    }
    pub(crate) fn invalid() -> Self {
        Self::new(400, "invalid_native_read")
    }
    pub(crate) fn protocol() -> Self {
        Self::new(502, "invalid_github_response")
    }
}
impl std::fmt::Display for Failure {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.code)
    }
}
impl std::error::Error for Failure {}
pub(crate) type Result<T> = std::result::Result<T, Failure>;

/// Causal result of one native creation attempt. Every error after dispatch
/// starts remains uncertain; the Go command journal owns durable reconciliation.
pub enum CreateOutcome {
    NotSent(Failure),
    Indeterminate(Failure),
    Created(Value),
}

/// Causal result of a one-shot update, without claiming a timeline event or
/// remote retry/deduplication guarantee. The Go journal retains its uncertainty.
pub enum WriteOutcome {
    NotSent(Failure),
    Indeterminate(Failure),
    Applied(Value),
}

impl From<CreateOutcome> for WriteOutcome {
    fn from(outcome: CreateOutcome) -> Self {
        match outcome {
            CreateOutcome::Created(value) => Self::Applied(value),
            CreateOutcome::NotSent(failure) => Self::NotSent(failure),
            CreateOutcome::Indeterminate(failure) => Self::Indeterminate(failure),
        }
    }
}

pub(crate) fn valid_reviewer_lookup(kind: &str, name: &str) -> bool {
    if kind == "user" {
        !name.is_empty()
            && name.len() <= 39
            && name.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-')
    } else {
        kind == "team"
            && !name.is_empty()
            && name.len() <= 100
            && name
                .chars()
                .all(|c| c.is_alphanumeric() || matches!(c, '-' | '_' | '.'))
    }
}

/// Files interpreted by the shared setup recipe, independent of provider fetching.
pub const SETUP_FILES: [&str; 4] = [
    "setup.sh",
    "check.sh",
    "setup-network.json",
    "setup-limits.json",
];
