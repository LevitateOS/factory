//! One fixed thread mutation. Scope/action checks are preflights, not atomic
//! conversation/repository conditions or remote retry/deduplication authority.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, Failure, ThreadRead, WriteOutcome,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::time::Instant;

const RESULT: &str = "thread{id isResolved isOutdated resolvedBy{id databaseId login} pullRequest{id fullDatabaseId number url repository{id nameWithOwner}} repository{id}}";
pub(super) fn mutation(resolved: bool) -> String {
    if resolved {
        "mutation FactoryResolveThread($input:ResolveReviewThreadInput!){resolveReviewThread(input:$input){RESULT}}"
    } else {
        "mutation FactoryUnresolveThread($input:UnresolveReviewThreadInput!){unresolveReviewThread(input:$input){RESULT}}"
    }.replace("RESULT",RESULT)
}

// Carries an exact caller-supplied original context, never reconstructed from
// current PR refs. The present GitHub reader cannot prove this two-commit pair.
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
    pub fn validate(&self) -> Result<(), Failure> {
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
impl Client {
    pub async fn set_thread_resolution(
        &self,
        token: Token,
        expected: &str,
        q: &ThreadResolution,
    ) -> WriteOutcome {
        self.thread_resolution_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn thread_resolution_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &ThreadResolution,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        self.run_mutation(deadline, |mutation| async move {
            q.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let account_node = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let pull = reader.raw_subject(true, q.number).await?;
            if r::id(&pull["id"])? != q.pull_id {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            let pull_node = r::text(&pull, "node_id")?;
            if !node_id(pull_node) {
                return Err(Failure::protocol());
            }
            match pull["locked"].as_bool() {
                Some(false) => {}
                Some(true) => return Err(Failure::new(403, "github_discussion_locked")),
                None => return Err(Failure::protocol()),
            }
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != q.repository {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            let repository_node = r::text(&repository, "node_id")?;
            if !node_id(repository_node) {
                return Err(Failure::protocol());
            }
            if repository["archived"].as_bool() != Some(false)
                || repository["disabled"].as_bool() != Some(false)
            {
                return Err(Failure::new(403, "github_repository_read_only"));
            }
            let observed = reader
                .graph_threads(&ThreadRead {
                    repository: q.repository.clone(),
                    number: q.number,
                    page: 1,
                    limit: 1,
                    thread_id: q.thread_id.clone(),
                })
                .await?;
            if observed["pull"]["external_id"] != q.pull_id {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            let thread = &observed["items"][0];
            if thread["external_id"] != q.thread_id {
                return Err(Failure::protocol());
            }
            let state = match thread["resolution"].as_str() {
                Some("resolved") => true,
                Some("unresolved") => false,
                _ => return Err(Failure::protocol()),
            };
            // This is an explicit no-send result, never a receipt for an earlier
            // uncertain command. Matching current state cannot attribute a write.
            if state == q.resolved {
                return Err(Failure::new(409, "github_thread_already_desired"));
            }
            let action = if q.resolved {
                "viewer_can_resolve"
            } else {
                "viewer_can_unresolve"
            };
            if thread[action].as_bool() != Some(true) {
                return Err(Failure::new(403, "github_thread_resolution_denied"));
            }
            let proof = permit.dispatch("github_thread_expired")?;
            let response = self
                .post_thread_resolution(&proof, &q.thread_id, q.resolved, token)
                .await?;
            let mutation = if q.resolved {
                "resolveReviewThread"
            } else {
                "unresolveReviewThread"
            };
            let raw = &response.value["data"][mutation]["thread"];
            if raw["id"] != q.thread_id
                || raw["isResolved"].as_bool() != Some(q.resolved)
                || raw["isOutdated"].as_bool().is_none()
                || raw["pullRequest"]["id"] != pull_node
                || r::big_id(&raw["pullRequest"]["fullDatabaseId"])? != q.pull_id
                || raw["pullRequest"]["number"].as_u64() != Some(q.number)
                || raw["pullRequest"]["repository"]["id"] != repository_node
                || raw["repository"]["id"] != repository_node
            {
                return Err(Failure::protocol());
            }
            let resolver = match raw.get("resolvedBy") {
                Some(Value::Null) if !q.resolved => Value::Null,
                Some(actor)
                    if q.resolved
                        && actor["id"] == account_node
                        && r::id(&actor["databaseId"])? == expected =>
                {
                    json!({"external_id":expected,"node_id":account_node,"login":r::nonempty(actor,"login")?})
                }
                _ => return Err(Failure::protocol()),
            };
            let name = r::text(&raw["pullRequest"]["repository"], "nameWithOwner")?;
            if !super::request::repository_name(name)
                || raw["pullRequest"]["url"]
                    != format!("https://github.com/{name}/pull/{}", q.number)
            {
                return Err(Failure::protocol());
            }
            let receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":repository_node,"pull_id":q.pull_id,"pull_node_id":pull_node,"number":q.number,
                "thread_id":q.thread_id,"resolved":q.resolved,"original_anchor":null,"outdated":raw["isOutdated"],"resolved_by":resolver,"pull_url":raw["pullRequest"]["url"],
                "provider_response":{"protocol":"github_graphql_thread_resolution","mutation":mutation,"status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.update()
    }
}

impl Client {
    pub(super) async fn post_thread_resolution(
        &self,
        proof: &super::writes::Dispatched<'_>,
        node: &str,
        resolved: bool,
        token: &[u8],
    ) -> super::Result<super::transport::MutationResponse> {
        let node = node.to_owned();

        let token = zeroize::Zeroizing::new(token.to_vec());
        self.provider_request(
            move |http| {
                if !super::request::node_id(&node) {
                    return Err(Failure::new(400, "invalid_native_write"));
                }
                Ok((
                    super::transport::graphql(
                        http,
                        &token,
                        &mutation(resolved),
                        serde_json::json!({"input":{"threadId":node}}),
                    )?,
                    1024 * 1024,
                ))
            },
            Some(proof),
        )
        .await
    }
}
