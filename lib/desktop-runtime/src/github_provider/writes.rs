use super::{Client, Failure, Identity, Result};
use serde_json::Value;
use std::future::Future;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::time::{Duration, Instant};
use tokio::sync::MutexGuard;

pub use crate::provider_contract::{CreateOutcome, WriteOutcome};

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

/// Proof that `MutationPermit::dispatch` recorded the attempt. Only `dispatch`
/// can build it, and every mutating provider request requires it.
pub(super) struct Dispatched<'a>(std::marker::PhantomData<&'a ()>);

impl MutationPermit<'_> {
    // Calling the fixed transport after this fence is an attempted effect even
    // when its reply is lost, cancelled, malformed or exceeds the receipt bound.
    pub fn dispatch(&mut self, expired: &'static str) -> Result<Dispatched<'_>> {
        self.client.current()?;
        if Instant::now() >= self.mutation.deadline {
            return Err(Failure::new(409, expired));
        }
        *self.next = Instant::now() + Duration::from_secs(1);
        self.mutation.attempted.store(true, Ordering::SeqCst);
        Ok(Dispatched(std::marker::PhantomData))
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

pub(crate) use crate::provider_contract::WriteOperation;
impl WriteOperation {
    pub(crate) async fn apply(
        &self,
        provider: &Client,
        token: &[u8],
        account: &str,
        deadline: Instant,
    ) -> (&'static str, WriteOutcome) {
        match self {
            Self::SubmitPendingReview(review) => (
                "applied",
                provider
                    .review_submit_account(token, account, review, Some(deadline))
                    .await,
            ),
            Self::SetDiscussionState(state) => (
                "applied",
                provider
                    .state_account(token, account, state, Some(deadline))
                    .await,
            ),
            Self::SetIssueDependency(dependency) => (
                "applied",
                provider
                    .dependency_account(token, account, dependency, Some(deadline))
                    .await,
            ),
            Self::EditDiscussion(edit) => (
                "applied",
                provider
                    .edit_account(token, account, edit, Some(deadline))
                    .await,
            ),
            Self::SetDiscussionLock(lock) => (
                "applied",
                provider
                    .lock_account(token, account, lock, Some(deadline))
                    .await,
            ),
            Self::SetThreadResolution(thread) => (
                "applied",
                provider
                    .thread_resolution_account(token, account, thread, Some(deadline))
                    .await,
            ),
            Self::SetReviewRequest(request) => (
                "applied",
                provider
                    .review_request_account(token, account, request, Some(deadline))
                    .await,
            ),
            Self::MergePull(merge) => (
                "applied",
                provider
                    .merge_account(token, account, merge, Some(deadline))
                    .await,
            ),
            Self::CreateInline(inline) => (
                "created",
                provider
                    .inline_create_account(token, account, inline, Some(deadline))
                    .await
                    .into(),
            ),
            Self::AppendPendingThread(inline) => (
                "created",
                provider
                    .inline_draft_account(token, account, inline, Some(deadline))
                    .await
                    .into(),
            ),
            Self::PublishBranch(branch) => (
                "applied",
                provider
                    .branch_publish_account(token, account, branch, Some(deadline))
                    .await,
            ),
            Self::CreateBranch(branch) => (
                "created",
                provider
                    .branch_create_account(token, account, branch, Some(deadline))
                    .await
                    .into(),
            ),
            Self::CreateComment(comment) => (
                "created",
                provider
                    .comment_account(token, account, comment, Some(deadline))
                    .await
                    .into(),
            ),
            Self::CreateIssue(issue) => (
                "created",
                provider
                    .issue_create_account(token, account, issue, Some(deadline))
                    .await
                    .into(),
            ),
            Self::CreatePull(pull) => (
                "created",
                provider
                    .pull_create_account(token, account, pull, Some(deadline))
                    .await
                    .into(),
            ),
            Self::CreateReview(review) => (
                "created",
                provider
                    .review_create_account(token, account, review, Some(deadline))
                    .await
                    .into(),
            ),
        }
    }
}
