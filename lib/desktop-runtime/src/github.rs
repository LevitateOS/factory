//! Typed native reads over the owned Go channel. No browser token getter.
use crate::credentials::{
    Account, Credentials, Error as CredentialError, Provider, Token, TokenCapability,
};
use crate::supervisor::{NativeChannel, NativeSupervisor};
use serde_json::Value;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

pub use crate::github_provider::Read;
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

pub(crate) fn verified_account(remote: &str, login: &str) -> Result<Account, Error> {
    if remote
        .parse::<i64>()
        .ok()
        .is_none_or(|id| id < 1 || id.to_string() != remote)
        || login.is_empty()
    {
        return Err(Error::Protocol);
    }
    Account::new(Provider::Github, "https://github.com", remote)
        .and_then(|account| account.with_label(login))
        .map_err(|_| Error::Protocol)
}

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

fn canonical_id(value: &Value) -> bool {
    value.as_str().is_some_and(|s| {
        s.parse::<i64>()
            .is_ok_and(|id| id > 0 && id.to_string() == s)
    })
}
fn registry_id(value: &Value) -> bool {
    value.as_str().is_some_and(|s| {
        s.len() == 32
            && s.bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    })
}
fn canonical_binding_matches(
    body: &Value,
    account: &crate::credentials::AccountStatus,
    profile: &str,
) -> bool {
    let b = &body["binding"];
    let host = &b["host"]["id"];
    registry_id(host)
        && b["host"]["provider"] == "github"
        && b["host"]["api_base"] == "https://api.github.com"
        && b["profile"]["id"] == profile
        && b["connection"]["profile"] == profile
        && b["connection"]["id"] == account.connection
        && b["connection"]["host"] == *host
        && b["actor"]["host"] == *host
        && canonical_id(&b["actor"]["id"])
        && b["connection"]["actor"] == b["actor"]["id"]
        && b["actor"]["external_id"] == account.account
        && b["actor"]["login"] == body["account"]["login"]
        && b["actor"]["login"]
            .as_str()
            .is_some_and(|s| !s.is_empty() && s.len() <= 512 && !s.chars().any(char::is_control))
}
fn canonical_page_matches(body: &Value, page: u64, limit: u64) -> bool {
    let p = &body["repositories"];
    let Some(items) = p["items"].as_array().filter(|v| v.len() as u64 <= limit) else {
        return false;
    };
    let Some(next) = p["next"].as_str() else {
        return false;
    };
    let mut ids = std::collections::HashSet::new();
    let mut external = std::collections::HashSet::new();
    p["observation"]["state"] == "available"
        && p["observation"]["revision"]
            .as_str()
            .is_some_and(|s| s.len() == 64 && s.bytes().all(|b| b.is_ascii_hexdigit()))
        && p["observation"]["observed_at"]
            .as_str()
            .is_some_and(|s| !s.is_empty())
        && p["complete"] == next.is_empty()
        && (next.is_empty()
            || next
                .parse::<u64>()
                .is_ok_and(|n| n > page && n <= 1_000_000 && n.to_string() == next))
        && items.iter().all(|r| {
            canonical_id(&r["id"])
                && canonical_id(&r["external_id"])
                && r["host"] == body["binding"]["host"]["id"]
                && ids.insert(r["id"].as_str().unwrap())
                && external.insert(r["external_id"].as_str().unwrap())
                && r["name"].as_str().is_some_and(|s| {
                    !s.is_empty() && s.chars().count() <= 512 && !s.chars().any(char::is_control)
                })
        })
}

fn canonical_actor_matches(value: &Value, host: &Value) -> bool {
    value.is_null()
        || canonical_id(&value["id"])
            && canonical_id(&value["external_id"])
            && value["host"] == *host
            && value["login"]
                .as_str()
                .is_some_and(|s| !s.is_empty() && !s.chars().any(char::is_control))
}
fn canonical_issue_page_matches(
    body: &Value,
    repository: &Value,
    number: Option<u64>,
    page: u64,
    limit: u64,
    state: &str,
) -> bool {
    let p = &body["issues"];
    let Some(items) = p["items"].as_array().filter(|v| v.len() as u64 <= limit) else {
        return false;
    };
    let Some(next) = p["next"].as_str() else {
        return false;
    };
    let host = &body["binding"]["host"]["id"];
    let observed = |v: &Value| {
        v["state"] == "available"
            && v["revision"]
                .as_str()
                .is_some_and(|s| s.len() == 64 && s.bytes().all(|b| b.is_ascii_hexdigit()))
            && v["observed_at"].as_str().is_some_and(|s| !s.is_empty())
    };
    let mut ids = std::collections::HashSet::new();
    let mut numbers = std::collections::HashSet::new();
    body["repository"] == *repository
        && observed(&p["observation"])
        && p["complete"] == next.is_empty()
        && (next.is_empty()
            || next
                .parse::<u64>()
                .is_ok_and(|n| n > page && n <= 1_000_000 && n.to_string() == next))
        && number.is_none_or(|_| next.is_empty() && items.len() == 1)
        && items.iter().all(|item| {
            let r = &item["ref"];
            r["kind"] == "issue"
                && r["repository"] == *repository
                && canonical_id(&r["external_id"])
                && canonical_id(&r["number"])
                && ids.insert(r["external_id"].as_str().unwrap())
                && numbers.insert(r["number"].as_str().unwrap())
                && number.is_none_or(|n| r["number"] == n.to_string())
                && observed(&item["observation"])
                && item["title"].is_string()
                && item["body"].is_string()
                && matches!(item["state"].as_str(), Some("open" | "closed"))
                && (number.is_some()
                    || state == "all"
                    || item["state"] == if state.is_empty() { "open" } else { state })
                && (item["locked"].is_null() || item["locked"].is_boolean())
                && canonical_actor_matches(&item["author"], host)
                && (item["assignees"].is_null()
                    || item["assignees"].as_array().is_some_and(|a| {
                        a.len() <= 1000
                            && a.iter()
                                .all(|v| !v.is_null() && canonical_actor_matches(v, host))
                    }))
                && (item["labels"].is_null()
                    || item["labels"].as_array().is_some_and(|a| {
                        a.len() <= 1000
                            && a.iter().all(|v| {
                                canonical_id(&v["external_id"])
                                    && v["name"].is_string()
                                    && v["color"].is_string()
                            })
                    }))
                && ["created_at", "updated_at", "closed_at"]
                    .iter()
                    .all(|key| item[*key].is_null() || item[*key].is_string())
                && item["capabilities"].is_null()
        })
}

impl NativeSupervisor {
    /// Preserve the complete unpersisted grant while verifying its access token.
    /// Failure drops access and refresh secrets together; storage remains native.
    pub fn github_verify_grant(
        &self,
        grant: crate::credentials::Grant,
    ) -> impl std::future::Future<Output = Result<(Account, crate::credentials::Grant), Error>>
    {
        let crate::credentials::Grant {
            access,
            refresh,
            expires_at,
            refresh_expires_at,
            scope,
        } = grant;
        let verify = self.github_verify(access);
        async move {
            let (account, access) = verify.await?;
            Ok((
                account,
                crate::credentials::Grant {
                    access,
                    refresh,
                    expires_at,
                    refresh_expires_at,
                    scope,
                },
            ))
        }
    }

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
                verify_on(&channel, &alive, token).await
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
                | Read::CanonicalRepositories { page, limit }
                | Read::CanonicalIssues { page, limit, .. }
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
                | Read::CanonicalIssues { repository, .. }
                | Read::CanonicalIssue { repository, .. }
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
                | Read::CanonicalIssue { number: 0, .. }
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
                Read::Issues { state, .. }
                | Read::CanonicalIssues { state, .. }
                | Read::Pulls { state, .. }
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
            let canonical = matches!(
                kind.as_str(),
                Some("binding" | "canonical_repositories" | "canonical_issues" | "canonical_issue")
            );
            let canonical_issue =
                matches!(kind.as_str(), Some("canonical_issues" | "canonical_issue")).then(|| {
                    (
                        body["repository"].clone(),
                        body["number"].as_u64(),
                        body["page"].as_u64().unwrap_or(1),
                        body["limit"].as_u64().unwrap_or(1),
                        body["state"].as_str().unwrap_or("").to_owned(),
                    )
                });
            let canonical_page = (kind == "canonical_repositories").then(|| {
                (
                    body["page"].as_u64().unwrap(),
                    body["limit"].as_u64().unwrap(),
                )
            });
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
            if canonical && !credentials.is_profile(&channel.profile) {
                return Err(Error::Invalid);
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
                            || canonical
                                && !canonical_binding_matches(&body, account, &channel.profile)
                            || canonical_page.is_some_and(|(page, limit)| {
                                !canonical_page_matches(&body, page, limit)
                            })
                            || canonical_issue.as_ref().is_some_and(
                                |(repository, number, page, limit, state)| {
                                    !canonical_issue_page_matches(
                                        &body, repository, *number, *page, *limit, state,
                                    )
                                },
                            )
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

async fn verify_on(
    channel: &NativeChannel,
    alive: &AtomicBool,
    token: Token,
) -> Result<(Account, Token), Error> {
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
    let body: Value = serde_json::from_slice(&response.body).map_err(|_| Error::Protocol)?;
    let remote = body["account"]["external_id"]
        .as_str()
        .ok_or(Error::Protocol)?;
    if body["kind"] != "verify" || body.get("connection").is_some() {
        return Err(Error::Protocol);
    }
    let account = verified_account(
        remote,
        body["account"]["login"].as_str().ok_or(Error::Protocol)?,
    )?;
    Ok((account, token))
}

/// Captured native identity verification. Not serializable and independent of
/// the supervisor mutex while device authorization or remote I/O is pending.
pub struct Verifier {
    channel: NativeChannel,
    alive: Arc<AtomicBool>,
}
impl Verifier {
    pub(crate) fn new(channel: NativeChannel, alive: Arc<AtomicBool>) -> Self {
        Self { channel, alive }
    }
    pub async fn verify(
        self,
        grant: crate::credentials::Grant,
    ) -> Result<(Account, crate::credentials::Grant), Error> {
        let (account, access) = verify_on(&self.channel, &self.alive, grant.access).await?;
        Ok((account, crate::credentials::Grant { access, ..grant }))
    }
}
