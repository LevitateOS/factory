use super::{Failure, Result};
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Read {
    IssueCreatorPermission {
        repository: String,
        number: u32,
        creator: String,
    },
    TargetTip {
        repository: String,
        target_ref: String,
    },
    SourceBundle {
        repository: String,
        base: String,
        commit: String,
        target_ref: String,
    },
    SetupSource {
        repository: String,
        commit: String,
        target_ref: String,
    },
    PublicationRefs {
        repository: String,
        name: String,
        base_branch: String,
    },
    Identity,
    Binding,
    PullRequiredChecks {
        repository: String,
        number: u32,
        head: String,
        base: String,
    },
    AutomationReview {
        repository: String,
        number: u32,
    },
    DeliveryOpinions {
        repository: String,
        number: u32,
    },
    AutomationChecks {
        repository: String,
        number: u32,
    },
    CanonicalRepositories {
        page: u32,
        limit: u32,
    },
    CanonicalIssues {
        repository: String,
        page: u32,
        limit: u32,
        state: String,
    },
    CanonicalIssue {
        repository: String,
        number: u32,
    },
    Repositories {
        page: u32,
        limit: u32,
    },
    Repository {
        repository: String,
    },
    AutomationInputs {
        repository: String,
        number: u32,
        #[serde(default)]
        comments: Vec<String>,
        target_ref: String,
    },
    BranchCreation {
        repository: String,
        name: String,
        commit: String,
    },
    WriteCapabilities {
        repository: String,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        number: Option<u32>,
        #[serde(default)]
        pull: bool,
    },
    MergePreparation {
        repository: String,
        number: u32,
    },
    MergeCompletion {
        repository: String,
        number: u32,
    },
    InlinePreparation {
        repository: String,
        number: u32,
        anchor: super::InlineAnchor,
    },
    InlinePreservation {
        repository: String,
        number: u32,
        review_node_id: String,
        comment_node_id: String,
    },
    FindRepository {
        name: String,
    },
    Issues {
        repository: String,
        page: u32,
        limit: u32,
        state: String,
    },
    Issue {
        repository: String,
        number: u32,
    },
    Pulls {
        repository: String,
        page: u32,
        limit: u32,
        state: String,
    },
    Pull {
        repository: String,
        number: u32,
    },
    PullDiff {
        repository: String,
        number: u32,
        base: String,
        head: String,
    },
    PullFiles {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
        base: String,
        head: String,
    },
    PullCommits {
        repository: String,
        number: u32,
        limit: u32,
        head: String,
        #[serde(default)]
        before: String,
    },
    PullReviews {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
    },
    PullReview {
        repository: String,
        number: u32,
        review_id: String,
    },
    PullReviewComments {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
    },
    PullReviewComment {
        repository: String,
        number: u32,
        comment_id: String,
    },
    PullThreads {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
        thread_id: String,
    },
    PullReviewRequests {
        repository: String,
        number: u64,
        target: Option<super::ReviewerTarget>,
    },
    ReviewerLookup {
        repository: String,
        number: u32,
        reviewer_kind: String,
        name: String,
    },
    PullReviewPreparation {
        repository: String,
        number: u32,
    },
    PullPendingReviewSnapshot {
        repository: String,
        number: u32,
    },
    PullReviewPublication {
        repository: String,
        number: u32,
        review_id: String,
        review_node_id: String,
    },
    PullBranches {
        repository: String,
        head_repository: String,
        base_branch: String,
        head_branch: String,
    },
    PullCheckRuns {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
        head: String,
    },
    PullCommitStatuses {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
        head: String,
    },
    WorkflowRuns {
        repository: String,
        page: u32,
        limit: u32,
        head: String,
    },
    WorkflowRunJobs {
        repository: String,
        run_id: String,
        attempt: String,
        head: String,
        page: u32,
        limit: u32,
    },
    PullProtection {
        repository: String,
        number: u32,
        head: String,
    },
    PullWorkflowJobs {
        repository: String,
        number: u32,
        head: String,
        run_page: u32,
        page: u32,
        limit: u32,
        #[serde(default)]
        run_id: String,
        #[serde(default)]
        attempt: String,
    },
    IssueComments {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
    },
    PullComments {
        repository: String,
        number: u32,
        page: u32,
        limit: u32,
    },
}

pub(crate) fn remote_id(value: &str) -> bool {
    value
        .parse::<i64>()
        .is_ok_and(|id| id > 0 && id.to_string() == value)
}
pub(super) fn node_id(value: &str) -> bool {
    !value.is_empty() && value.len() <= 1024 && value.bytes().all(|byte| (33..=126).contains(&byte))
}
pub(crate) fn commit(value: &str) -> bool {
    matches!(value.len(), 40 | 64)
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
pub(super) fn branch_name(value: &str) -> bool {
    !value.is_empty()
        && "refs/heads/".len() + value.len() <= 512
        && value.trim() == value
        && !value.ends_with('.')
        && !value.contains("..")
        && !value.contains("@{")
        && !value.chars().any(|c| " ~^:?*[\\".contains(c))
        && !value.bytes().any(|b| b < 32 || b == 127)
        && value
            .split('/')
            .all(|p| !p.is_empty() && !p.starts_with('.') && !p.ends_with(".lock"))
}
impl Read {
    /// The one defining selector contract, also consumed by native IPC adapters.
    pub fn validate(&self) -> Result<()> {
        if let Self::IssueCreatorPermission { creator, .. } = self {
            if !remote_id(creator) { return Err(Failure::invalid()); }
        }
        if let Self::TargetTip { target_ref, .. } = self {
            if !target_ref.strip_prefix("refs/heads/").is_some_and(branch_name) {
                return Err(Failure::invalid());
            }
        }
        if let Self::SourceBundle {
            base,
            commit: oid,
            target_ref,
            ..
        } = self
        {
            if !base.is_empty() && !commit(base)
                || !commit(oid)
                || !target_ref
                    .strip_prefix("refs/heads/")
                    .is_some_and(branch_name)
            {
                return Err(Failure::invalid());
            }
        }
        if let Self::SetupSource { commit: oid, target_ref, .. } = self {
            if !commit(oid) || !target_ref.strip_prefix("refs/heads/").is_some_and(branch_name) {
                return Err(Failure::invalid());
            }
        }
        if let Self::PublicationRefs {
            name, base_branch, ..
        } = self
        {
            if !branch_name(name) || !branch_name(base_branch) || name == base_branch {
                return Err(Failure::invalid());
            }
        }
        if let Self::PullRequiredChecks { head, base, .. } = self {
            if !commit(head) || !commit(base) {
                return Err(Failure::invalid());
            }
        }
        if let Self::AutomationInputs {
            number,
            comments,
            target_ref,
            ..
        } = self
        {
            let unique: std::collections::BTreeSet<_> = comments.iter().collect();
            if *number > i32::MAX as u32
                || comments.len() > 50
                || unique.len() != comments.len()
                || comments.iter().any(|id| !remote_id(id))
                || !target_ref
                    .strip_prefix("refs/heads/")
                    .is_some_and(branch_name)
            {
                return Err(Failure::invalid());
            }
        }
        if let Self::InlinePreservation {
            review_node_id,
            comment_node_id,
            ..
        } = self
        {
            if !node_id(review_node_id)
                || !node_id(comment_node_id)
                || review_node_id == comment_node_id
            {
                return Err(Failure::invalid());
            }
        }
        if let Self::InlinePreparation { anchor, .. } = self {
            anchor.validate()?
        }
        if let Self::BranchCreation {
            name, commit: oid, ..
        } = self
        {
            if !branch_name(name) || !commit(oid) {
                return Err(Failure::invalid());
            }
        }
        if let Self::PullReviewPublication { review_node_id, .. } = self {
            if !node_id(review_node_id) {
                return Err(Failure::invalid());
            }
        }
        if let Self::PullBranches {
            repository,
            head_repository,
            base_branch,
            head_branch,
        } = self
        {
            super::PullBranchRead {
                repository: repository.clone(),
                head_repository: head_repository.clone(),
                base_branch: base_branch.clone(),
                head_branch: head_branch.clone(),
            }
            .validate()?;
        }
        if let Self::PullReviewRequests {
            repository,
            number,
            target,
        } = self
        {
            super::ReviewerRead {
                repository: repository.clone(),
                number: *number,
                target: target.clone(),
            }
            .validate()?;
        }
        if let Self::FindRepository { name } = self {
            if !repository_name(name) {
                return Err(Failure::invalid());
            }
        }
        let value = serde_json::to_value(self).map_err(|_| Failure::invalid())?;
        if let Self::ReviewerLookup {
            reviewer_kind,
            name,
            ..
        } = self
        {
            if !super::review_requests::valid_lookup(reviewer_kind, name) {
                return Err(Failure::invalid());
            }
        }
        if let Self::WriteCapabilities { number, pull, .. } = self {
            if number.is_some_and(|n| n > i32::MAX as u32) || *pull && number.is_none() {
                return Err(Failure::invalid());
            }
        }
        if let Some(repo) = value.get("repository") {
            if !repo.as_str().is_some_and(remote_id) {
                return Err(Failure::invalid());
            }
        }
        if value
            .get("number")
            .is_some_and(|n| n.as_u64().is_none_or(|n| n == 0))
        {
            return Err(Failure::invalid());
        }
        for key in ["review_id", "comment_id", "run_id", "attempt"] {
            if value.get(key).is_some_and(|id| {
                !id.as_str().is_some_and(|s| {
                    remote_id(s) || matches!(self, Self::PullWorkflowJobs { .. }) && s.is_empty()
                })
            }) {
                return Err(Failure::invalid());
            }
        }
        if value
            .get("state")
            .is_some_and(|s| !matches!(s.as_str(), Some("" | "open" | "closed" | "all")))
        {
            return Err(Failure::invalid());
        }
        let files = matches!(self, Self::PullFiles { .. });
        if let Self::PullThreads {
            thread_id, limit, ..
        } = self
        {
            if !thread_id.is_empty()
                && (!node_id(thread_id) || thread_id.bytes().all(|b| b.is_ascii_digit()))
                || *limit > 20
            {
                return Err(Failure::invalid());
            }
        }
        if let Some(page) = value.get("page") {
            if page
                .as_u64()
                .is_none_or(|page| page == 0 || page > if files { 3000 } else { 1_000_000 })
                || value["limit"]
                    .as_u64()
                    .is_none_or(|limit| limit == 0 || limit > if files { 10 } else { 50 })
            {
                return Err(Failure::invalid());
            }
        }
        let base = value["base"].as_str().unwrap_or("");
        let head = value["head"].as_str().unwrap_or("");
        if let Self::PullWorkflowJobs {
            run_page,
            page,
            run_id,
            attempt,
            ..
        } = self
        {
            if *run_page == 0
                || *run_page > 1_000_000
                || !(head.is_empty() && *run_page == 1 && *page == 1 || commit(head))
                || !(run_id.is_empty() && attempt.is_empty() && *page == 1
                    || remote_id(run_id) && remote_id(attempt))
            {
                return Err(Failure::invalid());
            }
        }
        if matches!(self, Self::PullDiff { .. } | Self::PullFiles { .. })
            && (!(base.is_empty() && head.is_empty() || commit(base) && commit(head))
                || files && value["page"] != 1 && base.is_empty())
        {
            return Err(Failure::invalid());
        }
        if let Self::PullCommits {
            head,
            before,
            limit,
            ..
        } = self
        {
            if !commit(head)
                || *limit == 0
                || *limit > 20
                || before.len() > 1024
                || before.chars().any(char::is_control)
            {
                return Err(Failure::invalid());
            }
        }
        if matches!(
            self,
            Self::PullCheckRuns { .. } | Self::PullCommitStatuses { .. }
        ) && !(head.is_empty() && value["page"] == 1 || commit(head))
        {
            return Err(Failure::invalid());
        }
        if matches!(
            self,
            Self::WorkflowRuns { .. } | Self::PullProtection { .. }
        ) && !head.is_empty()
            && !commit(head)
            || matches!(self, Self::WorkflowRunJobs { .. }) && !commit(head)
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
    pub(crate) fn value(&self) -> Value {
        serde_json::to_value(self).expect("fixed native selector serialization")
    }
}
pub(super) fn repository_name(name: &str) -> bool {
    name.split_once('/').is_some_and(|(owner, repo)| {
        [owner, repo].iter().all(|part| {
            !part.is_empty()
                && part.len() <= 100
                && !matches!(*part, "." | "..")
                && part
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_' | b'.'))
        })
    })
}
