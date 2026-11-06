use super::{Client, Failure, Identity, Result};
use serde_json::Value;
use std::future::Future;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::time::{Duration, Instant};
use tokio::sync::MutexGuard;

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

pub(super) struct Mutation {
    pub deadline: Instant,
    attempted: Arc<AtomicBool>,
}

pub(super) struct MutationPermit<'a> {
    client: &'a Client,
    next: MutexGuard<'a, Instant>,
    mutation: Mutation,
}

impl Mutation {
    pub async fn admit<'a>(
        self,
        client: &'a Client,
        token: &[u8],
        expected: &str,
    ) -> Result<(MutationPermit<'a>, Identity)> {
        client.current()?;
        let next = client.mutations.lock().await;
        client
            .delay(next.saturating_duration_since(Instant::now()))
            .await?;
        let identity = client.identity(token).await?;
        if identity.external_id != expected {
            return Err(Failure::new(409, "github_account_changed"));
        }
        Ok((
            MutationPermit {
                client,
                next,
                mutation: self,
            },
            identity,
        ))
    }
}

impl MutationPermit<'_> {
    // Calling the fixed transport after this fence is an attempted effect even
    // when its reply is lost, cancelled, malformed or exceeds the receipt bound.
    pub fn dispatch(&mut self, expired: &'static str) -> Result<()> {
        self.client.current()?;
        if Instant::now() >= self.mutation.deadline {
            return Err(Failure::new(409, expired));
        }
        *self.next = Instant::now() + Duration::from_secs(1);
        self.mutation.attempted.store(true, Ordering::SeqCst);
        Ok(())
    }
}

pub(super) struct MutationResult {
    result: Result<Value>,
    attempted: bool,
}
impl MutationResult {
    pub fn creation(self) -> CreateOutcome {
        match self.result {
            Ok(value) => CreateOutcome::Created(value),
            Err(failure) if self.attempted => CreateOutcome::Indeterminate(failure),
            Err(failure) => CreateOutcome::NotSent(failure),
        }
    }
    pub fn update(self) -> WriteOutcome {
        match self.result {
            Ok(value) => WriteOutcome::Applied(value),
            Err(failure) if self.attempted => WriteOutcome::Indeterminate(failure),
            Err(failure) => WriteOutcome::NotSent(failure),
        }
    }
}

impl Client {
    pub(super) async fn run_mutation<F, Work>(
        &self,
        deadline: Option<Instant>,
        operation: F,
    ) -> MutationResult
    where
        F: FnOnce(Mutation) -> Work,
        Work: Future<Output = Result<Value>>,
    {
        let deadline = deadline
            .unwrap_or_else(|| Instant::now() + Duration::from_secs(8))
            .min(Instant::now() + Duration::from_secs(8));
        let attempted = Arc::new(AtomicBool::new(false));
        let work = operation(Mutation {
            deadline,
            attempted: attempted.clone(),
        });
        let result = self
            .scope(async {
                tokio::time::timeout_at(deadline.into(), work)
                    .await
                    .unwrap_or_else(|_| Err(Failure::new(504, "github_deadline")))
            })
            .await
            .and_then(|result| result);
        MutationResult {
            result,
            attempted: attempted.load(Ordering::SeqCst),
        }
    }
}

pub(super) fn check_receipt(receipt: &Value) -> Result<()> {
    if serde_json::to_vec(receipt)
        .map_err(|_| Failure::protocol())?
        .len()
        > 4096
    {
        return Err(Failure::new(413, "github_receipt_too_large"));
    }
    Ok(())
}
