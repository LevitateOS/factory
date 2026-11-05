use super::Failure;
use serde_json::Value;

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
