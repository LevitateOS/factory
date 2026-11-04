use super::{Failure, Result};
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Read {
    Identity,
    Binding,
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
pub(crate) fn commit(value: &str) -> bool {
    matches!(value.len(), 40 | 64)
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
impl Read {
    /// The one defining selector contract, also consumed by native IPC adapters.
    pub fn validate(&self) -> Result<()> {
        let value = serde_json::to_value(self).map_err(|_| Failure::invalid())?;
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
        for key in ["review_id", "comment_id"] {
            if value
                .get(key)
                .is_some_and(|id| !id.as_str().is_some_and(remote_id))
            {
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
        if matches!(self, Self::PullDiff { .. } | Self::PullFiles { .. })
            && (!(base.is_empty() && head.is_empty() || commit(base) && commit(head))
                || files && value["page"] != 1 && base.is_empty())
        {
            return Err(Failure::invalid());
        }
        if matches!(
            self,
            Self::PullCheckRuns { .. } | Self::PullCommitStatuses { .. }
        ) && !(head.is_empty() && value["page"] == 1 || commit(head))
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
    pub(crate) fn value(&self) -> Value {
        serde_json::to_value(self).expect("fixed native selector serialization")
    }
}
