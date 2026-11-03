//! Typed native reads over the owned Go channel. No browser token getter.
use crate::credentials::{
    Account, Credentials, Error as CredentialError, Provider, Token, TokenCapability,
};
use crate::supervisor::NativeSupervisor;
use serde::Serialize;
use serde_json::Value;
use std::sync::atomic::Ordering;
use std::time::Duration;

#[derive(Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum Read {
    Identity,
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
pub struct Reply {
    pub status: u16,
    pub body: Value,
}
#[derive(Debug, PartialEq, Eq)]
pub enum Error {
    Credential(CredentialError),
    Invalid,
    Unavailable,
    Protocol,
    Provider(u16),
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Credential(error) => write!(f, "{error}"),
            Self::Invalid => f.write_str("invalid native GitHub read"),
            Self::Unavailable => f.write_str("native GitHub transport is unavailable"),
            Self::Protocol => f.write_str("invalid native GitHub response"),
            Self::Provider(status) => write!(f, "GitHub verification refused (HTTP {status})"),
        }
    }
}
impl std::error::Error for Error {}

fn commit_id(value: &str) -> bool {
    matches!(value.len(), 40 | 64)
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

fn file_page_matches(value: &Value, page: u64, limit: u64) -> bool {
    let Some(total) = value["total"].as_u64().filter(|v| *v <= 3000) else {
        return false;
    };
    let offset = (page - 1) * limit;
    if offset >= total && !(page == 1 && total == 0) {
        return false;
    }
    let count = limit.min(total - offset);
    let next = if offset + count < total { page + 1 } else { 0 };
    value["page"] == page
        && value["limit"] == limit
        && value["next_page"] == next
        && value["items"]
            .as_array()
            .is_some_and(|items| items.len() as u64 == count)
}

impl NativeSupervisor {
    /// Verify an unpersisted token before native login calls Credentials::store.
    /// Account and Token have neither Serialize nor Debug; no browser receives
    /// them. Refusal/cancellation drops the token without touching OS custody.
    /// The future owns channel inputs so stopping the supervisor can proceed.
    pub fn github_verify(
        &self,
        token: Token,
    ) -> impl std::future::Future<Output = Result<(Account, Token), Error>> {
        let channel = self.github_channel();
        async move {
            tokio::time::timeout(Duration::from_secs(8), async move {
                let (receive, alive) = channel.map_err(|_| Error::Unavailable)?;
                let channel = receive
                    .await
                    .map_err(|_| Error::Unavailable)?
                    .map_err(|_| Error::Unavailable)?;
                if !alive.load(Ordering::SeqCst) {
                    return Err(Error::Unavailable);
                }
                let value = std::str::from_utf8(token.as_bytes()).map_err(|_| Error::Invalid)?;
                let response = soda_unix_http::request_authenticated_async(
                    &channel.socket,
                    "POST",
                    "/api/native/github/read",
                    "soda-native",
                    br#"{"kind":"verify"}"#,
                    &channel.key,
                    &[
                        ("X-Soda-Native-Instance", &channel.instance),
                        ("X-Soda-GitHub-Credential", value),
                    ],
                    Duration::from_secs(7),
                    soda_unix_http::Limits {
                        header_bytes: 16384,
                        body_bytes: 4096,
                    },
                )
                .await
                .map_err(|_| Error::Unavailable)?;
                if !alive.load(Ordering::SeqCst) {
                    return Err(Error::Unavailable);
                }
                if response.status != 200 {
                    return Err(if (400..=599).contains(&response.status) {
                        Error::Provider(response.status)
                    } else {
                        Error::Protocol
                    });
                }
                let body: Value =
                    serde_json::from_slice(&response.body).map_err(|_| Error::Protocol)?;
                let remote = body["account"]["external_id"]
                    .as_str()
                    .ok_or(Error::Protocol)?;
                if body["kind"] != "verify"
                    || body.get("connection").is_some()
                    || remote
                        .parse::<i64>()
                        .ok()
                        .is_none_or(|id| id < 1 || id.to_string() != remote)
                    || body["account"]["login"]
                        .as_str()
                        .is_none_or(|login| login.is_empty())
                {
                    return Err(Error::Protocol);
                }
                let account = Account::new(Provider::Github, "https://github.com", remote)
                    .map_err(|_| Error::Protocol)?;
                Ok((account, token))
            })
            .await
            .map_err(|_| Error::Unavailable)?
        }
    }

    /// The future owns its channel inputs and does not borrow the supervisor.
    /// Host retirement can proceed while it waits; alive/instance/key and the
    /// native credential lease remain mandatory before returning a result.
    pub fn github_read<'a>(
        &self,
        credentials: &'a Credentials,
        capability: &'a TokenCapability,
        query: Read,
    ) -> impl std::future::Future<Output = Result<Reply, Error>> + 'a {
        let channel = self.github_channel();
        async move {
            let account = capability.account();
            if account.provider != Provider::Github || account.origin != "https://github.com" {
                return Err(Error::Invalid);
            }
            match &query {
                Read::Repositories { page, limit }
                | Read::Issues { page, limit, .. }
                | Read::Pulls { page, limit, .. }
                | Read::PullReviews { page, limit, .. }
                | Read::PullReviewComments { page, limit, .. }
                | Read::PullCheckRuns { page, limit, .. }
                | Read::PullCommitStatuses { page, limit, .. }
                | Read::IssueComments { page, limit, .. }
                | Read::PullComments { page, limit, .. }
                    if *page == 0 || *page > 1_000_000 || *limit == 0 || *limit > 50 =>
                {
                    return Err(Error::Invalid)
                }
                _ => (),
            }
            if let Read::PullFiles {
                page,
                limit,
                base,
                head,
                ..
            } = &query
            {
                if *page == 0
                    || *page > 3000
                    || *limit == 0
                    || *limit > 10
                    || *page > 1 && base.is_empty()
                    || !(base.is_empty() && head.is_empty() || commit_id(base) && commit_id(head))
                {
                    return Err(Error::Invalid);
                }
            }
            if let Read::PullCheckRuns { page, head, .. }
            | Read::PullCommitStatuses { page, head, .. } = &query
            {
                if head.is_empty() && *page != 1 || !head.is_empty() && !commit_id(head) {
                    return Err(Error::Invalid);
                }
            }
            match &query {
                Read::Repository { repository }
                | Read::Issues { repository, .. }
                | Read::Issue { repository, .. }
                | Read::Pulls { repository, .. }
                | Read::Pull { repository, .. }
                | Read::PullDiff { repository, .. }
                | Read::PullFiles { repository, .. }
                | Read::PullReviews { repository, .. }
                | Read::PullReview { repository, .. }
                | Read::PullReviewComments { repository, .. }
                | Read::PullReviewComment { repository, .. }
                | Read::PullCheckRuns { repository, .. }
                | Read::PullCommitStatuses { repository, .. }
                | Read::IssueComments { repository, .. }
                | Read::PullComments { repository, .. }
                    if repository.parse::<u64>().ok().is_none_or(|v| {
                        v == 0 || v > i64::MAX as u64 || v.to_string() != *repository
                    }) =>
                {
                    return Err(Error::Invalid)
                }
                _ => (),
            }
            match &query {
                Read::Issue { number: 0, .. }
                | Read::Pull { number: 0, .. }
                | Read::PullDiff { number: 0, .. }
                | Read::PullFiles { number: 0, .. }
                | Read::PullReviews { number: 0, .. }
                | Read::PullReview { number: 0, .. }
                | Read::PullReviewComments { number: 0, .. }
                | Read::PullReviewComment { number: 0, .. }
                | Read::PullCheckRuns { number: 0, .. }
                | Read::PullCommitStatuses { number: 0, .. }
                | Read::IssueComments { number: 0, .. }
                | Read::PullComments { number: 0, .. } => return Err(Error::Invalid),
                Read::Issues { state, .. } | Read::Pulls { state, .. }
                    if !matches!(state.as_str(), "" | "open" | "closed" | "all") =>
                {
                    return Err(Error::Invalid)
                }
                Read::PullDiff { base, head, .. }
                    if !(base.is_empty() && head.is_empty()
                        || commit_id(base) && commit_id(head)) =>
                {
                    return Err(Error::Invalid)
                }
                Read::PullReview { review_id, .. }
                    if review_id.parse::<u64>().ok().is_none_or(|v| {
                        v == 0 || v > i64::MAX as u64 || v.to_string() != *review_id
                    }) =>
                {
                    return Err(Error::Invalid)
                }
                Read::PullReviewComment { comment_id, .. }
                    if comment_id.parse::<u64>().ok().is_none_or(|v| {
                        v == 0 || v > i64::MAX as u64 || v.to_string() != *comment_id
                    }) =>
                {
                    return Err(Error::Invalid)
                }
                _ => (),
            }
            let mut body = serde_json::to_value(query).map_err(|_| Error::Invalid)?;
            body["account"] = Value::String(account.account.clone());
            body["connection"] = Value::String(account.connection.clone());
            let kind = body["kind"].clone();
            let resource_scope = match kind.as_str() {
                Some("issues" | "issue") => Some(("issues", body["repository"].clone())),
                Some("pulls" | "pull") => Some(("pulls", body["repository"].clone())),
                Some("pull_reviews" | "pull_review") => {
                    Some(("reviews", body["repository"].clone()))
                }
                Some("pull_review_comments" | "pull_review_comment") => {
                    Some(("review_comments", body["repository"].clone()))
                }
                Some("pull_check_runs") => Some(("checks", body["repository"].clone())),
                Some("pull_commit_statuses") => Some(("statuses", body["repository"].clone())),
                Some("pull_diff") => Some(("diff", body["repository"].clone())),
                Some("issue_comments" | "pull_comments") => {
                    Some(("discussion", body["repository"].clone()))
                }
                _ => None,
            };
            let discussion_parent = match kind.as_str() {
                Some("issue_comments") => Some(("issue", body["number"].clone())),
                Some("pull_comments") => Some(("pull", body["number"].clone())),
                _ => None,
            };
            let diff_pin = (kind == "pull_diff" || kind == "pull_files").then(|| {
                (
                    body["repository"].clone(),
                    body["number"].clone(),
                    body["base"].clone(),
                    body["head"].clone(),
                )
            });
            let file_page = (kind == "pull_files").then(|| {
                (
                    body["page"].as_u64().unwrap(),
                    body["limit"].as_u64().unwrap(),
                )
            });
            let review_parent = match kind.as_str() {
                Some("pull_reviews") => Some((
                    "reviews",
                    body["number"].clone(),
                    None,
                    body["limit"].as_u64().unwrap(),
                )),
                Some("pull_review") => Some((
                    "reviews",
                    body["number"].clone(),
                    Some(body["review_id"].clone()),
                    1,
                )),
                Some("pull_review_comments") => Some((
                    "review_comments",
                    body["number"].clone(),
                    None,
                    body["limit"].as_u64().unwrap(),
                )),
                Some("pull_review_comment") => Some((
                    "review_comments",
                    body["number"].clone(),
                    Some(body["comment_id"].clone()),
                    1,
                )),
                _ => None,
            };
            let check_scope =
                (kind == "pull_check_runs" || kind == "pull_commit_statuses").then(|| {
                    (
                        if kind == "pull_commit_statuses" {
                            "statuses"
                        } else {
                            "checks"
                        },
                        body["number"].clone(),
                        body["head"].clone(),
                        body["limit"].as_u64().unwrap(),
                    )
                });
            let body = serde_json::to_vec(&body).map_err(|_| Error::Invalid)?;
            let (receive, alive) = channel.map_err(|_| Error::Unavailable)?;
            let channel = tokio::time::timeout(Duration::from_secs(3), receive)
                .await
                .map_err(|_| Error::Unavailable)?
                .map_err(|_| Error::Unavailable)?
                .map_err(|_| Error::Unavailable)?;
            if !alive.load(Ordering::SeqCst) {
                return Err(Error::Unavailable);
            }
            let response = credentials
                .with_token_operation(capability, async |token| {
                    if !alive.load(Ordering::SeqCst) {
                        return Err(Error::Unavailable);
                    }
                    let token = std::str::from_utf8(token).map_err(|_| Error::Invalid)?;
                    let response = soda_unix_http::request_authenticated_async(
                        &channel.socket,
                        "POST",
                        "/api/native/github/read",
                        "soda-native",
                        &body,
                        &channel.key,
                        &[
                            ("X-Soda-Native-Instance", &channel.instance),
                            ("X-Soda-GitHub-Credential", token),
                        ],
                        Duration::from_secs(7),
                        soda_unix_http::Limits {
                            header_bytes: 16384,
                            body_bytes: 2 * 1024 * 1024,
                        },
                    )
                    .await
                    .map_err(|_| Error::Unavailable)?;
                    if !alive.load(Ordering::SeqCst) {
                        return Err(Error::Unavailable);
                    }
                    let body: Value =
                        serde_json::from_slice(&response.body).map_err(|_| Error::Protocol)?;
                    if !body.is_object() {
                        return Err(Error::Protocol);
                    }
                    if response.status == 200
                        && (body["connection"] != account.connection
                            || body["account"]["external_id"] != account.account
                            || body["kind"] != kind
                            || resource_scope.as_ref().is_some_and(|(field, repository)| {
                                body[*field]["repository"] != *repository
                            })
                            || discussion_parent.as_ref().is_some_and(|(kind, number)| {
                                body["discussion"]["subject"]["kind"] != *kind
                                    || body["discussion"]["subject"]["number"] != *number
                            })
                            || diff_pin
                                .as_ref()
                                .is_some_and(|(repository, number, base, head)| {
                                    let diff = if kind == "pull_files" {
                                        &body["files"]["diff"]
                                    } else {
                                        &body["diff"]
                                    };
                                    diff["repository"] != *repository
                                        || diff["pull"]["kind"] != "pull"
                                        || diff["pull"]["number"] != *number
                                        || diff["base"].as_str().is_none_or(|v| !commit_id(v))
                                        || diff["head"].as_str().is_none_or(|v| !commit_id(v))
                                        || base != ""
                                            && (diff["base"] != *base || diff["head"] != *head)
                                })
                            || file_page.is_some_and(|(page, limit)| {
                                !file_page_matches(&body["files"], page, limit)
                            })
                            || review_parent
                                .as_ref()
                                .is_some_and(|(field, number, id, limit)| {
                                    let reviews = &body[*field];
                                    reviews["pull"]["kind"] != "pull"
                                        || reviews["pull"]["number"] != *number
                                        || reviews["items"].as_array().is_none_or(|items| {
                                            items.len() as u64 > *limit
                                                || id.as_ref().is_some_and(|id| {
                                                    items.len() != 1
                                                        || items[0]["external_id"] != *id
                                                })
                                        })
                                        || id.is_some() && reviews["next_page"] != 0
                                })
                            || check_scope
                                .as_ref()
                                .is_some_and(|(field, number, head, limit)| {
                                    let checks = &body[*field];
                                    checks["pull"]["kind"] != "pull"
                                        || checks["pull"]["number"] != *number
                                        || checks["head"].as_str().is_none_or(|v| !commit_id(v))
                                        || head != "" && checks["head"] != *head
                                        || checks["items"].as_array().is_none_or(|items| {
                                            items.len() as u64 > *limit
                                                || items
                                                    .iter()
                                                    .any(|item| item["head"] != checks["head"])
                                        })
                                }))
                    {
                        return Err(Error::Protocol);
                    }
                    Ok(Reply {
                        status: response.status,
                        body,
                    })
                })
                .await
                .map_err(Error::Credential)??;
            if !alive.load(Ordering::SeqCst) {
                return Err(Error::Unavailable);
            }
            Ok(response)
        }
    }
}
