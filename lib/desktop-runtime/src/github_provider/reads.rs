use super::request::remote_id;
use super::transport::Response;
use super::{records as r, Client, Failure, Read, Result};
use crate::credentials::Token;
use serde_json::{json, Value};
use std::collections::HashSet;
use std::time::Duration;

impl Client {
    /// Native semantic reads with an opaque token. This does not store credentials,
    /// select a product actor, or accept arbitrary URLs. Canonical selectors are
    /// resolved by the Go registry consumer before entering this provider port.
    pub async fn observe(&self, token: Token, expected: &str, query: &Read) -> Result<Value> {
        self.read_account(token.as_bytes(), expected, query).await
    }
    pub(crate) async fn read_account(
        &self,
        token: &[u8],
        expected: &str,
        query: &Read,
    ) -> Result<Value> {
        query.validate()?;
        if !remote_id(expected) {
            return Err(Failure::invalid());
        }
        let q = query.value();
        let kind = r::text(&q, "kind")?;
        if matches!(
            kind,
            "binding" | "canonical_repositories" | "canonical_issues" | "canonical_issue"
        ) {
            return Err(Failure::invalid());
        }
        tokio::time::timeout(Duration::from_secs(8), async {
            let identity = self.identity(token).await?;
            if identity.external_id != expected {
                return Err(Failure::new(409, "github_account_changed"));
            }
            let mut result = json!({"kind":kind,"account":identity});
            match query {
                Read::Identity => {}
                Read::Repositories { page, limit } => {
                    let response = self
                        .get(
                            "/user/repos",
                            &[
                                ("page", page.to_string()),
                                ("per_page", limit.to_string()),
                                ("visibility", "all".into()),
                                (
                                    "affiliation",
                                    "owner,collaborator,organization_member".into(),
                                ),
                                ("sort", "created".into()),
                                ("direction", "asc".into()),
                            ],
                            token,
                        )
                        .await?;
                    let items = page_items(&response, *page, *limit, false)?;
                    let mut seen = HashSet::new();
                    let mut projected = Vec::with_capacity(items.len());
                    for item in items {
                        unique(&mut seen, &r::id(&item["id"])?)?;
                        projected.push(r::repository(item)?);
                    }
                    result["items"] = json!(projected);
                    result["next_page"] = json!(response.next);
                }
                Read::Repository { repository } => {
                    result["repository"] = self.repository(repository, token).await?;
                }
                Read::FindRepository { name } => {
                    let response = self.get(&format!("/repos/{name}"), &[], token).await?;
                    let observed = r::repository(&response.value)?;
                    if !r::text(&observed, "name")?.eq_ignore_ascii_case(name) {
                        return Err(Failure::protocol());
                    }
                    result["repository"] = observed;
                }
                _ => {
                    let repository = r::text(&q, "repository")?;
                    let reader = Reader::new(self, token, repository).await?;
                    let (field, value) = match kind {
                        "pull_required_checks" => {
                            ("required_checks", reader.required_checks(&q).await?)
                        }
                        "automation_review" => (
                            "automation_review",
                            reader
                                .automation_review(
                                    q["number"].as_u64().ok_or_else(Failure::invalid)?,
                                )
                                .await?,
                        ),
                        "automation_checks" => (
                            "automation_checks",
                            reader
                                .automation_checks(
                                    q["number"].as_u64().ok_or_else(Failure::invalid)?,
                                )
                                .await?,
                        ),
                        "automation_inputs" => {
                            let Read::AutomationInputs {
                                number,
                                comments,
                                target_ref,
                                ..
                            } = query
                            else {
                                return Err(Failure::invalid());
                            };
                            (
                                "inputs",
                                reader
                                    .automation_inputs(*number, comments, target_ref)
                                    .await?,
                            )
                        }
                        "write_capabilities" => {
                            let Read::WriteCapabilities { number, pull, .. } = query else {
                                return Err(Failure::invalid());
                            };
                            (
                                "capabilities",
                                reader.write_capabilities(*number, *pull).await?,
                            )
                        }
                        "merge_preparation" => (
                            "preparation",
                            reader
                                .merge_preparation(
                                    q["number"].as_u64().ok_or_else(Failure::invalid)? as u32,
                                )
                                .await?,
                        ),
                        "inline_preservation" => {
                            let Read::InlinePreservation {
                                number,
                                review_node_id,
                                comment_node_id,
                                ..
                            } = query
                            else {
                                return Err(Failure::invalid());
                            };
                            (
                                "preservation",
                                reader
                                    .inline_preservation(
                                        u64::from(*number),
                                        review_node_id,
                                        comment_node_id,
                                        identity
                                            .node_id
                                            .as_deref()
                                            .ok_or_else(Failure::protocol)?,
                                        &identity.external_id,
                                    )
                                    .await?,
                            )
                        }
                        "inline_preparation" => {
                            let Read::InlinePreparation { number, anchor, .. } = query else {
                                return Err(Failure::invalid());
                            };
                            (
                                "preparation",
                                reader
                                    .inline_preparation(
                                        u64::from(*number),
                                        anchor,
                                        identity
                                            .node_id
                                            .as_deref()
                                            .ok_or_else(Failure::protocol)?,
                                        &identity.external_id,
                                    )
                                    .await?,
                            )
                        }
                        "branch_creation" => (
                            "preparation",
                            reader
                                .branch_creation(
                                    r::text(&q, "name")?,
                                    r::text(&q, "commit")?,
                                    &identity.external_id,
                                    identity.node_id.as_deref().ok_or_else(Failure::protocol)?,
                                )
                                .await?,
                        ),
                        "pull_review_publication" => (
                            "publication",
                            reader
                                .review_publication(
                                    q["number"].as_u64().ok_or_else(Failure::invalid)?,
                                    r::text(&q, "review_id")?,
                                    r::text(&q, "review_node_id")?,
                                    identity.node_id.as_deref().ok_or_else(Failure::protocol)?,
                                    &identity.external_id,
                                )
                                .await?,
                        ),
                        "pull_pending_review_snapshot" => (
                            "preparation",
                            reader
                                .pending_review_snapshot(
                                    q["number"].as_u64().ok_or_else(Failure::invalid)?,
                                    identity.node_id.as_deref().ok_or_else(Failure::protocol)?,
                                    &identity.external_id,
                                )
                                .await?,
                        ),
                        "pull_review_preparation" => (
                            "preparation",
                            reader
                                .review_preparation(
                                    q["number"].as_u64().ok_or_else(Failure::invalid)?,
                                    identity.node_id.as_deref().ok_or_else(Failure::protocol)?,
                                    &identity.external_id,
                                )
                                .await?,
                        ),
                        "pull_branches" => {
                            let Read::PullBranches {
                                repository,
                                head_repository,
                                base_branch,
                                head_branch,
                            } = query
                            else {
                                return Err(Failure::invalid());
                            };
                            let selector = super::PullBranchRead {
                                repository: repository.clone(),
                                head_repository: head_repository.clone(),
                                base_branch: base_branch.clone(),
                                head_branch: head_branch.clone(),
                            };
                            ("branches", reader.pull_branches(&selector).await?)
                        }
                        "issues" | "issue" => ("issues", reader.subjects(&q, false).await?),
                        "pulls" | "pull" => ("pulls", reader.subjects(&q, true).await?),
                        "issue_comments" | "pull_comments" => {
                            ("discussion", reader.comments(&q).await?)
                        }
                        "pull_reviews" | "pull_review" => {
                            ("reviews", reader.reviews(&q, false).await?)
                        }
                        "pull_review_comments" | "pull_review_comment" => {
                            ("review_comments", reader.reviews(&q, true).await?)
                        }
                        "pull_threads" => {
                            let Read::PullThreads {
                                repository,
                                number,
                                page,
                                limit,
                                thread_id,
                            } = query
                            else {
                                return Err(Failure::invalid());
                            };
                            let selector = super::ThreadRead {
                                repository: repository.clone(),
                                number: u64::from(*number),
                                page: *page,
                                limit: *limit,
                                thread_id: thread_id.clone(),
                            };
                            ("threads", reader.graph_threads(&selector).await?)
                        }
                        "reviewer_lookup" => {
                            let Read::ReviewerLookup {
                                number,
                                reviewer_kind,
                                name,
                                ..
                            } = query
                            else {
                                return Err(Failure::invalid());
                            };
                            (
                                "reviewer_lookup",
                                reader.reviewer_lookup(*number, reviewer_kind, name).await?,
                            )
                        }
                        "pull_review_requests" => {
                            let Read::PullReviewRequests {
                                repository,
                                number,
                                target,
                            } = query
                            else {
                                return Err(Failure::invalid());
                            };
                            let selector = super::ReviewerRead {
                                repository: repository.clone(),
                                number: *number,
                                target: target.clone(),
                            };
                            ("review_requests", reader.review_requests(&selector).await?)
                        }
                        "workflow_runs" => ("workflow_runs", reader.workflow_runs(&q).await?),
                        "workflow_run_jobs" => ("jobs", reader.workflow_jobs(&q).await?),
                        "pull_workflow_jobs" => ("jobs", reader.pull_workflow_jobs(&q).await?),
                        "pull_protection" => ("protection", reader.protection(&q).await?),
                        "pull_check_runs" | "pull_commit_statuses" => (
                            if kind == "pull_check_runs" {
                                "checks"
                            } else {
                                "statuses"
                            },
                            reader.checks(&q).await?,
                        ),
                        "pull_diff" | "pull_files" => (
                            if kind == "pull_diff" { "diff" } else { "files" },
                            reader.diff(&q).await?,
                        ),
                        "pull_commits" => ("commits", reader.pull_commits(&q).await?),
                        _ => return Err(Failure::invalid()),
                    };
                    result[field] = value;
                }
            }
            self.current()?;
            Ok(result)
        })
        .await
        .map_err(|_| Failure::new(504, "github_deadline"))?
    }
    pub(super) async fn repository(&self, id: &str, token: &[u8]) -> Result<Value> {
        let response = self.get(&format!("/repositories/{id}"), &[], token).await?;
        let snapshot = r::repository(&response.value)?;
        if snapshot["external_id"] != id {
            return Err(Failure::protocol());
        }
        Ok(snapshot)
    }
}
pub(super) struct Reader<'a> {
    pub client: &'a Client,
    pub token: &'a [u8],
    pub repository: &'a str,
    pub name: String,
    pub prefix: String,
}
impl<'a> Reader<'a> {
    pub(super) async fn new(
        client: &'a Client,
        token: &'a [u8],
        repository: &'a str,
    ) -> Result<Self> {
        let raw = client.repository(repository, token).await?;
        let name = r::text(&raw, "name")?;
        if !super::request::repository_name(name) {
            return Err(Failure::protocol());
        }
        Ok(Self {
            client,
            token,
            repository,
            name: name.into(),
            prefix: format!("/repos/{name}"),
        })
    }
    pub async fn get(&self, path: &str, query: &[(&str, String)]) -> Result<Response> {
        self.client
            .get(&format!("{}{path}", self.prefix), query, self.token)
            .await
    }
    pub fn url(&self, path: &str) -> String {
        format!("https://api.github.com{}{path}", self.prefix)
    }
    pub async fn recheck(&self) -> Result<()> {
        let current = self.get("", &[]).await?;
        if r::repository(&current.value)?["external_id"] != self.repository {
            return Err(Failure::new(404, "github_not_visible"));
        }
        self.client.current()
    }
    fn valid_subject(&self, raw: &Value, pull: bool, number: Option<u64>) -> Result<()> {
        r::id(&raw["id"])?;
        let n = raw["number"]
            .as_u64()
            .filter(|n| *n > 0 && *n <= i64::MAX as u64)
            .ok_or_else(Failure::protocol)?;
        r::text(raw, "title")?;
        if number.is_some_and(|number| n != number)
            || !matches!(raw["state"].as_str(), Some("open" | "closed"))
        {
            return Err(Failure::protocol());
        }
        if pull {
            if r::id(&raw["base"]["repo"]["id"])? != self.repository
                || raw["url"] != self.url(&format!("/pulls/{n}"))
            {
                return Err(Failure::protocol());
            }
        } else if raw["repository_url"] != self.url("")
            || !(raw["pull_request"].is_null() || raw["pull_request"].is_object())
        {
            return Err(Failure::protocol());
        }
        Ok(())
    }
    pub async fn subject(&self, pull: bool, number: u64) -> Result<Value> {
        let raw = self.raw_subject(pull, number).await?;
        if !pull && !raw["pull_request"].is_null() {
            return Err(Failure::new(404, "github_not_visible"));
        }
        Ok(
            json!({"kind":if pull {"pull"}else{"issue"},"external_id":r::id(&raw["id"])?,"number":number}),
        )
    }
    pub async fn raw_subject(&self, pull: bool, number: u64) -> Result<Value> {
        let raw = self
            .get(
                &format!("/{}/{number}", if pull { "pulls" } else { "issues" }),
                &[],
            )
            .await?
            .value;
        self.valid_subject(&raw, pull, Some(number))?;
        Ok(raw)
    }
    async fn subjects(&self, q: &Value, pull: bool) -> Result<Value> {
        let number = q["number"].as_u64();
        let (items, next) = if let Some(number) = number {
            (vec![self.raw_subject(pull, number).await?], 0)
        } else {
            let (page, limit) = paging(q);
            let response = self
                .get(
                    if pull { "/pulls" } else { "/issues" },
                    &[
                        ("page", page.to_string()),
                        ("per_page", limit.to_string()),
                        ("state", state(q).into()),
                        ("sort", "created".into()),
                        ("direction", "asc".into()),
                    ],
                )
                .await?;
            (
                page_items(&response, page, limit, false)?.clone(),
                response.next,
            )
        };
        let (mut seen, mut numbers) = (HashSet::new(), HashSet::new());
        let mut projected = Vec::new();
        for item in items {
            self.valid_subject(&item, pull, number)?;
            unique(&mut seen, &r::id(&item["id"])?)?;
            unique(&mut numbers, &item["number"].to_string())?;
            if number.is_none() && state(q) != "all" && item["state"] != state(q) {
                return Err(Failure::protocol());
            }
            if !pull && !item["pull_request"].is_null() {
                if number.is_some() {
                    return Err(Failure::new(404, "github_not_visible"));
                }
                continue;
            }
            projected.push(if pull {
                r::pull(&item)?
            } else {
                r::issue(&item)?
            });
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"items":projected,"next_page":next}),
        )
    }
    async fn comments(&self, q: &Value) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let pull = q["kind"] == "pull_comments";
        let before = self.subject(pull, number).await?;
        let (page, limit) = paging(q);
        let response = self
            .get(
                &format!("/issues/{number}/comments"),
                &[
                    ("page", page.to_string()),
                    ("per_page", limit.to_string()),
                    ("sort", "created".into()),
                    ("direction", "asc".into()),
                ],
            )
            .await?;
        let mut seen = HashSet::new();
        let mut projected = Vec::new();
        for item in page_items(&response, page, limit, false)? {
            let id = r::id(&item["id"])?;
            unique(&mut seen, &id)?;
            if item["issue_url"] != self.url(&format!("/issues/{number}"))
                || item["url"] != self.url(&format!("/issues/comments/{id}"))
            {
                return Err(Failure::protocol());
            }
            projected.push(r::comment(item)?);
        }
        if self.subject(pull, number).await? != before {
            return Err(Failure::new(404, "github_not_visible"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"subject":before,"items":projected,"next_page":response.next}),
        )
    }
    pub(super) async fn reviews(&self, q: &Value, comments: bool) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let before = self.subject(true, number).await?;
        let selected = q[if comments { "comment_id" } else { "review_id" }].as_str();
        let (page, limit) = if selected.is_some() {
            (0, 1)
        } else {
            paging(q)
        };
        let suffix = if comments { "comments" } else { "reviews" };
        let target = match selected {
            Some(id) if comments => format!("/pulls/comments/{id}"),
            Some(id) => format!("/pulls/{number}/reviews/{id}"),
            None => format!("/pulls/{number}/{suffix}"),
        };
        let response = self
            .get(
                &target,
                &if selected.is_some() {
                    vec![]
                } else {
                    vec![
                        ("page", page.to_string()),
                        ("per_page", limit.to_string()),
                        ("sort", "created".into()),
                        ("direction", "asc".into()),
                    ]
                },
            )
            .await?;
        let items = if selected.is_some() {
            if response.next != 0 {
                return Err(Failure::protocol());
            }
            vec![response.value.clone()]
        } else {
            page_items(&response, page, limit, comments)?.clone()
        };
        let mut projected = Vec::new();
        let mut seen = HashSet::new();
        for item in items {
            let id = r::id(&item["id"])?;
            unique(&mut seen, &id)?;
            if selected.is_some_and(|selected| selected != id) {
                return Err(Failure::protocol());
            }
            if item["pull_request_url"] != self.url(&format!("/pulls/{number}")) {
                return Err(if comments {
                    Failure::new(404, "github_not_visible")
                } else {
                    Failure::protocol()
                });
            }
            if comments && item["url"] != self.url(&format!("/pulls/comments/{id}")) {
                return Err(Failure::protocol());
            }
            projected.push(if comments {
                r::review_comment(&item)?
            } else {
                r::review(&item)?
            });
        }
        if self.subject(true, number).await? != before {
            return Err(Failure::new(404, "github_not_visible"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"pull":before,"items":projected,"next_page":response.next}),
        )
    }
    async fn checks(&self, q: &Value) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let before = self.check_head(number).await?;
        let head = r::text(&before, "head")?;
        if q["head"] != "" && q["head"] != head {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        let checks = q["kind"] == "pull_check_runs";
        let (page, limit) = paging(q);
        let mut params = vec![("page", page.to_string()), ("per_page", limit.to_string())];
        if checks {
            params.push(("filter", "all".into()));
        }
        let mut response = self
            .get(
                &if checks {
                    format!("/commits/{head}/check-runs")
                } else {
                    format!("/commits/{head}/statuses")
                },
                &params,
            )
            .await?;
        let total = if checks {
            let n = response.value["total_count"].clone();
            if !n.is_null() && n.as_i64().is_none_or(|n| n < 0) {
                return Err(Failure::protocol());
            }
            n
        } else {
            Value::Null
        };
        if checks {
            response.value = response.value["check_runs"].take();
        }
        let mut projected = Vec::new();
        let mut seen = HashSet::new();
        for item in page_items(&response, page, limit, true)? {
            let id = r::id(&item["id"])?;
            unique(&mut seen, &id)?;
            if item["url"]
                != self.url(&if checks {
                    format!("/check-runs/{id}")
                } else {
                    format!("/statuses/{head}")
                })
                || checks && item["head_sha"] != head
            {
                return Err(Failure::protocol());
            }
            projected.push(if checks {
                r::check(item, head)?
            } else {
                r::status(item, head)?
            });
        }
        if self.check_head(number).await? != before {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        self.recheck().await?;
        let mut result = json!({"repository":self.repository,"name":self.name,"pull":before["pull"],"head":head,"base":before["base"],"branch":before["branch"],"items":projected,"next_page":response.next});
        if checks {
            result["total"] = total;
        }
        Ok(result)
    }
    pub(super) async fn check_head(&self, number: u64) -> Result<Value> {
        let raw = self.raw_subject(true, number).await?;
        self.protection_binding(&raw)
    }
}
pub(super) fn paging(q: &Value) -> (u32, u32) {
    (
        q["page"].as_u64().unwrap_or(0) as u32,
        q["limit"].as_u64().unwrap_or(0) as u32,
    )
}
fn state(q: &Value) -> &str {
    q["state"]
        .as_str()
        .filter(|s| !s.is_empty())
        .unwrap_or("open")
}
pub(super) fn page_items(
    response: &Response,
    page: u32,
    limit: u32,
    nonempty_next: bool,
) -> Result<&Vec<Value>> {
    let items = response.value.as_array().ok_or_else(Failure::protocol)?;
    if items.len() > limit as usize
        || response.next != 0
            && (response.next <= page
                || response.next > 1_000_000
                || nonempty_next && items.is_empty())
    {
        return Err(Failure::protocol());
    }
    Ok(items)
}
pub(super) fn unique(seen: &mut HashSet<String>, id: &str) -> Result<()> {
    if seen.insert(id.into()) {
        Ok(())
    } else {
        Err(Failure::protocol())
    }
}
