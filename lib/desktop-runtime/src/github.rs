//! Native credential admission and credential-free Go identity projection.
//! Authenticated GitHub HTTP/decoding belongs entirely to the headless provider.
use crate::credentials::{
    Account, Credentials, Error as CredentialError, Provider, Token, TokenCapability,
};
use crate::supervisor::{NativeChannel, NativeSupervisor};
use serde_json::{json, Value};
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
    RefreshClientRejected,
    Provider(u16),
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Credential(error) => write!(f, "{error}"),
            Self::Invalid => f.write_str("invalid native GitHub read"),
            Self::Unavailable => f.write_str("native GitHub transport is unavailable"),
            Self::Protocol => f.write_str("invalid native GitHub response"),
            Self::RefreshClientRejected => {
                f.write_str("GitHub rejected the refresh client credentials")
            }
            Self::Provider(status) => write!(f, "GitHub verification refused (HTTP {status})"),
        }
    }
}
impl std::error::Error for Error {}

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
    pub fn github_verify_grant(
        &self,
        grant: crate::credentials::Grant,
    ) -> impl std::future::Future<Output = Result<(Account, crate::credentials::Grant), Error>>
    {
        let verify = self.github_verify(grant.access);
        async move {
            let (account, access) = verify.await?;
            Ok((account, crate::credentials::Grant { access, ..grant }))
        }
    }
    pub fn github_verify(
        &self,
        token: Token,
    ) -> impl std::future::Future<Output = Result<(Account, Token), Error>> {
        let client = self.github_client();
        async move {
            client
                .map_err(|_| Error::Unavailable)?
                .verify(token)
                .await
                .map_err(provider_error)
        }
    }
    pub fn github_read<'a>(
        &self,
        credentials: &'a Credentials,
        capability: &'a TokenCapability,
        query: Read,
    ) -> impl std::future::Future<Output = Result<Reply, Error>> + 'a {
        let client = self.github_client();
        let canonical = matches!(
            &query,
            Read::Binding
                | Read::CanonicalRepositories { .. }
                | Read::CanonicalIssues { .. }
                | Read::CanonicalIssue { .. }
        );
        let channel = canonical.then(|| self.github_channel());
        async move {
            query.validate().map_err(|_| Error::Invalid)?;
            let account = capability.account();
            if account.provider != Provider::Github || account.origin != "https://github.com" {
                return Err(Error::Invalid);
            }
            let client = client.map_err(|_| Error::Unavailable)?;
            let channel = if let Some(channel) = channel {
                let (receive, _) = channel.map_err(|_| Error::Unavailable)?;
                let channel = tokio::time::timeout(Duration::from_secs(3), receive)
                    .await
                    .map_err(|_| Error::Unavailable)?
                    .map_err(|_| Error::Unavailable)?
                    .map_err(|_| Error::Unavailable)?;
                if !credentials.is_profile(&channel.profile) {
                    return Err(Error::Invalid);
                }
                Some(channel)
            } else {
                None
            };
            let operation = crate::provider_contract::operation::Operation::new(query.wire_limit(), query.timeout());
            let result = operation.owned_scope(client
                .scope(async {
                    credentials
                        .with_token_operation(capability, async |token| {
                            let mut reply = if let Some(channel) = &channel {
                                canonical_read(&client, channel, token, account, &query).await?
                            } else {
                                match client.read_account(token, &account.account, &query).await {
                                    Ok(body) => Reply { status: 200, body },
                                    Err(failure) => failure_reply(failure),
                                }
                            };
                            if reply.status == 200 {
                                reply.body["connection"] = json!(account.connection);
                            }
                            if serde_json::to_vec(&reply.body)
                                .map_err(|_| Error::Protocol)?
                                .len()
                                > 2 * 1024 * 1024
                            {
                                return Ok(Reply {
                                    status: 413,
                                    body: json!({"error":"github_response_too_large"}),
                                });
                            }
                            Ok(reply)
                        })
                        .await
                        .map_err(Error::Credential)?
                })
                ).await.map_err(provider_error)??;
            Ok(result)
        }
    }
}

pub(crate) fn provider_error(failure: crate::github_provider::Failure) -> Error {
    if failure.code == "github_unavailable" {
        Error::Unavailable
    } else if failure.code == "github_refresh_client_rejected" {
        Error::RefreshClientRejected
    } else {
        Error::Provider(failure.status)
    }
}
fn failure_reply(failure: crate::github_provider::Failure) -> Reply {
    let mut body = json!({"error":failure.code});
    if let Some(value) = failure.retry_after {
        body["retry_after"] = json!(value);
    }
    if let Some(value) = failure.reset {
        body["reset"] = json!(value);
    }
    Reply {
        status: failure.status,
        body,
    }
}
async fn observation(channel: &NativeChannel, body: &Value) -> Result<Reply, Error> {
    let bytes = serde_json::to_vec(body).map_err(|_| Error::Invalid)?;
    if bytes.len() > 2 * 1024 * 1024 {
        return Err(Error::Invalid);
    }
    let response = soda_unix_http::request_authenticated_async(
        &channel.socket,
        "POST",
        "/api/native/collaboration/observe",
        "soda-native",
        &bytes,
        &channel.key,
        &[("X-Soda-Native-Instance", &channel.instance)],
        Duration::from_secs(7),
        soda_unix_http::Limits {
            header_bytes: 16384,
            body_bytes: 2 * 1024 * 1024,
        },
    )
    .await
    .map_err(|_| Error::Unavailable)?;
    let body: Value = serde_json::from_slice(&response.body).map_err(|_| Error::Protocol)?;
    if !body.is_object() || response.status != 200 && !(400..=599).contains(&response.status) {
        return Err(Error::Protocol);
    }
    Ok(Reply {
        status: response.status,
        body,
    })
}
async fn canonical_read(
    client: &crate::github_provider::Client,
    channel: &NativeChannel,
    token: &[u8],
    account: &crate::credentials::AccountStatus,
    query: &Read,
) -> Result<Reply, Error> {
    let q = query.value();
    let kind = q["kind"].clone();
    let mut captured_identity = None;
    let raw_query = match query {
        Read::Binding => Read::Identity,
        Read::CanonicalRepositories { page, limit } => Read::Repositories {
            page: *page,
            limit: *limit,
        },
        Read::CanonicalIssues { repository, .. } | Read::CanonicalIssue { repository, .. } => {
            let identity = match client.identity(token).await {
                Ok(body) => body,
                Err(failure) => return Ok(failure_reply(failure)),
            };
            if identity.external_id != account.account {
                return Ok(failure_reply(crate::github_provider::Failure::new(409, "github_account_changed")));
            }
            let observed = serde_json::to_value(&identity).map_err(|_| Error::Protocol)?;
            captured_identity = Some(identity);
            let mut resolved=observation(channel,&json!({"kind":"resolve_repository","connection":account.connection,"account":observed,"repository":repository})).await?;
            if resolved.status != 200 {
                return Ok(resolved);
            }
            resolved.body["account"] = observed;
            let repo = &resolved.body["repository"];
            if !canonical_binding_matches(&resolved.body, account, &channel.profile)
                || repo["id"] != *repository
                || repo["host"] != resolved.body["binding"]["host"]["id"]
                || !canonical_id(&repo["external_id"])
            {
                return Err(Error::Protocol);
            }
            let remote = repo["external_id"].as_str().unwrap().to_owned();
            match query {
                Read::CanonicalIssues {
                    page, limit, state, ..
                } => Read::Issues {
                    repository: remote,
                    page: *page,
                    limit: *limit,
                    state: state.clone(),
                },
                Read::CanonicalIssue { number, .. } => Read::Issue {
                    repository: remote,
                    number: *number,
                },
                _ => unreachable!(),
            }
        }
        _ => return Err(Error::Invalid),
    };
    let mut result = match client
        .read_account_with_identity(token, &account.account, &raw_query, captured_identity)
        .await
    {
        Ok(body) => body,
        Err(failure) => return Ok(failure_reply(failure)),
    };
    let mut facts = json!({"connection":account.connection,"account":result["account"]});
    match query {
        Read::Binding => facts["kind"] = json!("binding"),
        Read::CanonicalRepositories { page, limit } => {
            facts["kind"] = json!("repositories");
            facts["page"] = json!(page);
            facts["limit"] = json!(limit);
            facts["next_page"] = result["next_page"].clone();
            let items = result["items"].as_array().ok_or(Error::Protocol)?;
            facts["items"] = json!(items
                .iter()
                .map(|item| json!({"external_id":item["external_id"],"name":item["name"]}))
                .collect::<Vec<_>>());
        }
        Read::CanonicalIssues {
            repository,
            page,
            limit,
            state,
        } => {
            facts["kind"] = json!("issues");
            facts["repository"] = json!(repository);
            facts["page"] = json!(page);
            facts["limit"] = json!(limit);
            facts["state"] = json!(state);
            facts["issues"] = result["issues"].take();
        }
        Read::CanonicalIssue { repository, number } => {
            facts["kind"] = json!("issues");
            facts["repository"] = json!(repository);
            facts["page"] = json!(1);
            facts["limit"] = json!(1);
            facts["number"] = json!(number);
            facts["issues"] = result["issues"].take();
        }
        _ => return Err(Error::Invalid),
    }
    let mapped = observation(channel, &facts).await?;
    if mapped.status != 200 {
        return Ok(mapped);
    }
    // Canonical pages contain only the Go projection. Do not also return the
    // raw provider catalogue or accidentally expose two competing page shapes.
    let mut result =
        json!({"kind":kind,"account":result["account"],"connection":account.connection});
    for (key, value) in mapped.body.as_object().ok_or(Error::Protocol)? {
        result[key] = value.clone();
    }
    result["kind"] = kind;
    result["connection"] = json!(account.connection);
    if let Some(repository) = q.get("repository") {
        result["repository"] = repository.clone();
    }
    if !canonical_binding_matches(&result, account, &channel.profile) {
        return Err(Error::Protocol);
    }
    match query {
        Read::CanonicalRepositories { page, limit }
            if !canonical_page_matches(&result, *page as u64, *limit as u64) =>
        {
            return Err(Error::Protocol)
        }
        Read::CanonicalIssues {
            repository,
            page,
            limit,
            state,
        } if !canonical_issue_page_matches(
            &result,
            &json!(repository),
            None,
            *page as u64,
            *limit as u64,
            state,
        ) =>
        {
            return Err(Error::Protocol)
        }
        Read::CanonicalIssue { repository, number }
            if !canonical_issue_page_matches(
                &result,
                &json!(repository),
                Some(*number as u64),
                1,
                1,
                "",
            ) =>
        {
            return Err(Error::Protocol)
        }
        _ => {}
    }
    client.current().map_err(provider_error)?;
    Ok(Reply {
        status: 200,
        body: result,
    })
}

/// Captured provider verifier, independent of the shell/child owner mutex.
pub struct Verifier {
    pub(crate) client: crate::github_provider::Client,
}
impl Verifier {
    pub async fn verify(
        self,
        grant: crate::credentials::Grant,
    ) -> Result<(Account, crate::credentials::Grant), Error> {
        let (account, access) = self
            .client
            .verify(grant.access)
            .await
            .map_err(provider_error)?;
        Ok((account, crate::credentials::Grant { access, ..grant }))
    }
}
