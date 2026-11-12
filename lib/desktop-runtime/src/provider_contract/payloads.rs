use super::read::{branch_name, commit, node_id, remote_id};
use super::{Failure, Result};
use base64::Engine;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use soda_execution_protocol::MAX_STAGE_BUNDLE;
use std::collections::BTreeSet;

pub(crate) fn inline_path(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 4096
        && !value.contains('\0')
        && !value.starts_with('/')
        && value.split('/').all(|p| !matches!(p, "" | "." | ".."))
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BranchCreate {
    pub repository: String,
    pub repository_node_id: String,
    /// An unqualified literal branch name; the transport always supplies refs/heads/.
    pub name: String,
    pub commit: String,
}

impl BranchCreate {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !node_id(&self.repository_node_id)
            || !branch_name(&self.name)
            || !commit(&self.commit)
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Comment {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub body: String,
}

impl Comment {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.body.len() > 64 * 1024
            || self.body.trim().is_empty()
            || self.body.contains('\0')
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct DiscussionEdit {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub title: Option<String>,
    pub body: Option<String>,
    pub original_title: String,
    pub original_body_sha256: String,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub require_unchanged: bool,
}

impl DiscussionEdit {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.title.is_none() && self.body.is_none()
            || self
                .title
                .as_ref()
                .is_some_and(|s| s.len() > 255 || s.trim().is_empty() || s.contains('\0'))
            || self
                .body
                .as_ref()
                .is_some_and(|s| s.len() > 65536 || s.contains('\0'))
            || self.original_title.len() > 65536
            || self.original_title.contains('\0')
            || self.original_body_sha256.len() != 64
            || !self
                .original_body_sha256
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct IssueDependency {
    pub repository: String,
    pub issue_id: String,
    pub number: u32,
    pub blocking_id: String,
    pub blocking_number: u32,
    pub expected: Vec<String>,
    pub remove: bool,
}

impl IssueDependency {
    pub fn validate(&self) -> Result<()> {
        let set: BTreeSet<_> = self.expected.iter().collect();
        if !remote_id(&self.repository)
            || !remote_id(&self.issue_id)
            || !remote_id(&self.blocking_id)
            || self.issue_id == self.blocking_id
            || self.number == 0
            || self.blocking_number == 0
            || self.number == self.blocking_number
            || self.expected.len() > 50
            || set.len() != self.expected.len()
            || self
                .expected
                .iter()
                .any(|id| !remote_id(id) || id == &self.issue_id)
            || self.expected.contains(&self.blocking_id) != self.remove
        {
            return Err(Failure::new(400, "invalid_native_dependency"));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BranchCorrection {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub repository: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub fork_consent: String,
    pub number: u32,
    pub expected_author_id: i64,
    pub expected_pr_id: i64,
    pub expected_issue_id: i64,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BranchPublish {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub controller_grant_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source_connection: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pull_request: Option<BranchCorrection>,
    pub repository: String,
    pub branch: String,
    pub expected_old: String,
    pub candidate: String,
    pub base: String,
    pub comparison_branch: String,
    pub comparison_oid: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub comparison_repository: String,
    pub bundle_sha256: String,
    pub bundle: String,
}

impl BranchPublish {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !branch_name(&self.branch)
            || !branch_name(&self.comparison_branch)
            || self.branch == self.comparison_branch
            || !commit(&self.candidate)
            || !commit(&self.base)
            || !commit(&self.comparison_oid)
            || !(self.expected_old.is_empty() || commit(&self.expected_old))
            || self.expected_old == self.candidate
            || self.pull_request.is_none() && !self.comparison_repository.is_empty()
            || self.pull_request.as_ref().is_some_and(|c| {
                self.expected_old.is_empty()
                    || c.number == 0
                    || c.expected_author_id <= 0
                    || c.expected_pr_id <= 0
                    || c.expected_issue_id <= 0
                    || if self.controller_grant_id.is_empty() {
                        !self.source_connection.is_empty()
                            || !remote_id(&c.repository)
                            || c.repository == self.repository
                            || self.comparison_repository != c.repository
                            || c.fork_consent.len() != 32
                            || !c
                                .fork_consent
                                .bytes()
                                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
                    } else {
                        !c.repository.is_empty()
                            || !c.fork_consent.is_empty()
                            || !self.comparison_repository.is_empty()
                    }
            })
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        self.pack().map(|_| ())
    }
    pub(crate) fn pack(&self) -> Result<Vec<u8>> {
        if self.bundle.is_empty() || self.bundle.len() > MAX_STAGE_BUNDLE.div_ceil(3) * 4 {
            return Err(Failure::new(413, "github_candidate_too_large"));
        }
        let bundle = base64::engine::general_purpose::STANDARD
            .decode(&self.bundle)
            .map_err(|_| Failure::invalid())?;
        if bundle.len() > MAX_STAGE_BUNDLE
            || Sha256::digest(&bundle)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect::<String>()
                != self.bundle_sha256
        {
            return Err(Failure::new(400, "github_candidate_digest_differs"));
        }
        let end = bundle
            .windows(2)
            .position(|bytes| bytes == b"\n\n")
            .ok_or_else(Failure::invalid)?;
        let header = std::str::from_utf8(&bundle[..end]).map_err(|_| Failure::invalid())?;
        let mut lines = header.lines();
        if lines.next() != Some("# v2 git bundle") {
            return Err(Failure::new(400, "github_candidate_bundle_unsupported"));
        }
        let mut target = false;
        let mut prerequisite = false;
        for line in lines {
            if let Some(value) = line.strip_prefix('-') {
                let (oid, _) = value.split_once(' ').ok_or_else(Failure::invalid)?;
                if prerequisite || oid != self.base {
                    return Err(Failure::invalid());
                }
                prerequisite = true;
            } else {
                if target || line != format!("{} HEAD", self.candidate) {
                    return Err(Failure::invalid());
                }
                target = true;
            }
        }
        let pack = &bundle[end + 2..];
        if !target || pack.len() < 32 || &pack[..4] != b"PACK" {
            return Err(Failure::invalid());
        }
        Ok(pack.to_vec())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct InlineCreate {
    pub repository: String,
    pub repository_node_id: String,
    pub pull_id: String,
    pub pull_node_id: String,
    pub number: u32,
    pub anchor: InlineAnchor,
    pub preparation_sha256: String,
    pub draft: bool,
    pub body: String,
}

impl InlineCreate {
    pub fn validate(&self) -> Result<()> {
        self.anchor.validate()?;
        if !remote_id(&self.repository)
            || !node_id(&self.repository_node_id)
            || !remote_id(&self.pull_id)
            || !node_id(&self.pull_node_id)
            || self.number == 0
            || self.anchor.end_line > i32::MAX as u64
            || self.preparation_sha256.len() != 64
            || !commit(&self.preparation_sha256)
            || self.body.len() > 65536
            || self.body.contains('\0')
            || self.body.trim().is_empty()
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct InlineDraft {
    pub repository: String,
    pub repository_node_id: String,
    pub pull_id: String,
    pub pull_node_id: String,
    pub number: u64,
    pub anchor: InlineAnchor,
    pub review_node_id: String,
    pub preparation_sha256: String,
    pub body: String,
}

impl InlineDraft {
    pub fn validate(&self) -> Result<()> {
        self.anchor.validate()?;
        if !remote_id(&self.repository)
            || !remote_id(&self.pull_id)
            || !node_id(&self.repository_node_id)
            || !node_id(&self.pull_node_id)
            || !node_id(&self.review_node_id)
            || self.number == 0
            || self.number > u32::MAX as u64
            || self.preparation_sha256.len() != 64
            || !commit(&self.preparation_sha256)
            || self.body.len() > 65536
            || self.body.contains('\0')
            || self.body.trim().is_empty()
            || self.anchor.end_line > i32::MAX as u64
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct InlineAnchor {
    pub base: String,
    pub head: String,
    pub path: String,
    pub side: String,
    pub scope: String,
    pub start_line: u64,
    pub end_line: u64,
}

impl InlineAnchor {
    pub fn validate(&self) -> Result<()> {
        if !commit(&self.base)
            || !commit(&self.head)
            || !inline_path(&self.path)
            || !matches!(self.side.as_str(), "old" | "new")
            || !(self.scope == "file" && self.start_line == 0 && self.end_line == 0
                || self.scope == "lines"
                    && self.start_line > 0
                    && self.end_line >= self.start_line
                    && self.end_line <= 9_007_199_254_740_991)
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct IssueCreate {
    pub repository: String,
    pub title: String,
    pub body: String,
}

impl IssueCreate {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || self.title.is_empty()
            || self.title.trim() != self.title
            || self.title.len() > 255
            || self.title.contains('\0')
            || self.body.len() > 65536
            || self.body.contains('\0')
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct DiscussionLock {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub lock: bool,
    pub reason: String,
}

impl DiscussionLock {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.reason.len() > 65536
            || self.reason.contains('\0')
            || !self.lock && !self.reason.is_empty()
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullMerge {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub controller_grant_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source_connection: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub head_ref: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub base_ref: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub host_revision: Option<i64>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub checks: Vec<Value>,
    pub repository: String,
    pub repository_node_id: String,
    pub pull_id: String,
    pub pull_node_id: String,
    pub number: u32,
    pub head: String,
    pub base: String,
    pub method: String,
    pub message: String,
}

impl PullMerge {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.pull_id)
            || !node_id(&self.repository_node_id)
            || !node_id(&self.pull_node_id)
            || self.number == 0
            || !commit(&self.head)
            || !commit(&self.base)
            || !matches!(
                self.method.as_str(),
                "merge" | "squash" | "rebase" | "fast-forward-only"
            )
            || self.message.len() > 65536
            || self.message.contains('\0')
            || if matches!(self.method.as_str(), "rebase" | "fast-forward-only") {
                !self.message.is_empty()
            } else {
                self.message.trim().is_empty()
            }
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct DiscussionState {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub close: bool,
}

impl DiscussionState {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullBranchRead {
    pub repository: String,
    pub head_repository: String,
    pub base_branch: String,
    pub head_branch: String,
}

impl PullBranchRead {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.head_repository)
            || !branch_name(&self.base_branch)
            || !branch_name(&self.head_branch)
            || self.repository == self.head_repository && self.base_branch == self.head_branch
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullBranchPin {
    pub repository_node_id: String,
    pub owner_node_id: String,
    pub reference_node_id: String,
    pub commit: String,
}

impl PullBranchPin {
    pub(crate) fn valid(&self) -> bool {
        node_id(&self.repository_node_id)
            && node_id(&self.owner_node_id)
            && node_id(&self.reference_node_id)
            && commit(&self.commit)
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullCreate {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub controller_grant_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source_connection: String,
    pub repository: String,
    pub head_repository: String,
    pub base_branch: String,
    pub head_branch: String,
    pub base_pin: PullBranchPin,
    pub head_pin: PullBranchPin,
    pub title: String,
    pub body: String,
    pub allow_maintainer_edit: bool,
}

impl PullCreate {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.head_repository)
            || !branch_name(&self.base_branch)
            || !branch_name(&self.head_branch)
            || self.repository == self.head_repository && self.base_branch == self.head_branch
            || !self.base_pin.valid()
            || !self.head_pin.valid()
            || self.base_pin.reference_node_id == self.head_pin.reference_node_id
            || (self.repository == self.head_repository)
                != (self.base_pin.repository_node_id == self.head_pin.repository_node_id)
            || self.repository == self.head_repository
                && self.base_pin.owner_node_id != self.head_pin.owner_node_id
            || self.title.is_empty()
            || self.title.trim() != self.title
            || self.title.len() > 255
            || self.title.contains('\0')
            || self.body.len() > 65536
            || self.body.contains('\0')
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        // These literal branch names can be ambiguous with the provider's ref
        // qualification rules. Never silently reinterpret a saved selector.
        if self.base_branch.starts_with("refs/") || self.head_branch.starts_with("refs/") {
            return Err(Failure::new(
                409,
                "github_pull_branch_qualification_unsupported",
            ));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewCreate {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub controller_grant_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source_connection: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub author_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub head_ref: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub base_ref: String,
    pub repository: String,
    pub repository_node_id: String,
    pub pull_id: String,
    pub pull_node_id: String,
    pub number: u64,
    pub head: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub base: String,
    pub event: String,
    pub body: String,
}

impl ReviewCreate {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.pull_id)
            || !node_id(&self.repository_node_id)
            || !node_id(&self.pull_node_id)
            || self.number == 0
            || self.number > i64::MAX as u64
            || !commit(&self.head)
            || !self.base.is_empty() && !commit(&self.base)
            || !matches!(
                self.event.as_str(),
                "draft" | "comment" | "approve" | "request_changes"
            )
            || self.body.len() > 65536
            || self.body.contains('\0')
            || self.event == "request_changes" && self.body.trim().is_empty()
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewRequestChange {
    pub repository: String,
    pub pull_id: String,
    pub number: u64,
    pub target: ReviewerTarget,
    pub requested: bool,
}

impl ReviewRequestChange {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.pull_id)
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.target.validate().is_err()
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum ReviewerTarget {
    User { external_id: String },
    Team { external_id: String },
}

impl ReviewerTarget {
    pub(crate) fn validate(&self) -> Result<()> {
        let valid = match self {
            Self::User { external_id } => remote_id(external_id),
            Self::Team { external_id } => {
                node_id(external_id)
                    && external_id.len() <= 512
                    && !external_id.bytes().all(|b| b.is_ascii_digit())
            }
        };
        if !valid {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewerRead {
    pub repository: String,
    pub number: u64,
    pub target: Option<ReviewerTarget>,
}

impl ReviewerRead {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository) || self.number == 0 || self.number > i64::MAX as u64 {
            return Err(Failure::invalid());
        }
        if let Some(target) = &self.target {
            target.validate()?;
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PendingReviewSubmit {
    pub repository: String,
    pub repository_node_id: String,
    pub pull_id: String,
    pub pull_node_id: String,
    pub number: u64,
    pub head: String,
    pub review_id: Option<String>,
    pub review_node_id: String,
    pub review_commit: String,
    pub pending_sha256: String,
    pub comments_sha256: String,
    pub comments_count: u64,
    pub event: String,
    /// None preserves the pending summary; Some("") explicitly clears it.
    pub body: Option<String>,
}

impl PendingReviewSubmit {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !node_id(&self.repository_node_id)
            || !remote_id(&self.pull_id)
            || !node_id(&self.pull_node_id)
            || self.number == 0
            || self.number > i64::MAX as u64
            || !commit(&self.head)
            || !node_id(&self.review_node_id)
            || self.review_id.as_ref().is_some_and(|id| !remote_id(id))
            || !commit(&self.review_commit)
            || self.pending_sha256.len() != 64
            || !commit(&self.pending_sha256)
            || self.comments_sha256.len() != 64
            || !commit(&self.comments_sha256)
            || self.comments_count > 1000
            || !matches!(
                self.event.as_str(),
                "draft" | "comment" | "approve" | "request_changes"
            )
            || self.event == "draft" && self.body.is_none()
            || self
                .body
                .as_ref()
                .is_some_and(|b| b.len() > 65536 || b.contains('\0'))
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ThreadRead {
    pub repository: String,
    pub number: u64,
    pub page: u32,
    pub limit: u32,
    pub thread_id: String,
}

impl ThreadRead {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.page == 0
            || self.page > 1_000_000
            || self.limit == 0
            || self.limit > 20
            || !self.thread_id.is_empty()
                && (!node_id(&self.thread_id) || self.thread_id.bytes().all(|b| b.is_ascii_digit()))
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct OriginalThreadAnchor {
    pub base: String,
    pub head: String,
    pub path: String,
    pub side: String,
    pub scope: String,
    pub start_line: i64,
    pub end_line: i64,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ThreadResolution {
    pub repository: String,
    pub pull_id: String,
    pub number: u64,
    pub thread_id: String,
    pub resolved: bool,
    pub original_anchor: Option<OriginalThreadAnchor>,
}

impl ThreadResolution {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.pull_id)
            || self.number == 0
            || self.number > i64::MAX as u64
            || !node_id(&self.thread_id)
            || self.thread_id.bytes().all(|b| b.is_ascii_digit())
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        if self.original_anchor.is_some() {
            return Err(Failure::new(409, "github_original_anchor_unobserved"));
        }
        Ok(())
    }
}

/// Fixed native write vocabulary, independent of callback wire encoding and custody.
#[derive(serde::Serialize)]
pub(crate) enum WriteOperation {
    SetIssueDependency(IssueDependency),
    CreateInline(InlineCreate),
    MergePull(PullMerge),
    AppendPendingThread(InlineDraft),
    PublishBranch(BranchPublish),
    CreateBranch(BranchCreate),
    CreateComment(Comment),
    CreateIssue(IssueCreate),
    CreatePull(PullCreate),
    CreateReview(ReviewCreate),
    SubmitPendingReview(PendingReviewSubmit),
    SetDiscussionState(DiscussionState),
    EditDiscussion(DiscussionEdit),
    SetDiscussionLock(DiscussionLock),
    SetThreadResolution(ThreadResolution),
    SetReviewRequest(ReviewRequestChange),
}
impl WriteOperation {
    pub(crate) fn validate(&self) -> Result<&str> {
        match self {
            Self::SetIssueDependency(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::CreateInline(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::MergePull(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::AppendPendingThread(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::PublishBranch(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::CreateBranch(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::CreateComment(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::CreateIssue(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::CreatePull(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::CreateReview(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::SubmitPendingReview(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::SetDiscussionState(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::EditDiscussion(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::SetDiscussionLock(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::SetThreadResolution(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
            Self::SetReviewRequest(value) => {
                value.validate()?;
                Ok(&value.repository)
            }
        }
    }
}
