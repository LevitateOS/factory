//! One new thread on an exact own pending review. Causal creation and later
//! preservation/privacy verification are separate; no implicit submit or retry.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, CreateOutcome, Failure,
};
use crate::credentials::Token;
use serde_json::{json, Value};
use std::time::Instant;

pub(super) const CREATE: &str = "mutation FactoryAppendPendingThread($input:AddPullRequestReviewThreadInput!){addPullRequestReviewThread(input:$input){thread{id path diffSide startDiffSide line startLine subjectType repository{id} pullRequest{id fullDatabaseId number repository{id}} comments(first:2){totalCount pageInfo{hasNextPage endCursor} nodes{id fullDatabaseId body state path line startLine subjectType author{__typename ... on User{id}} repository{id} pullRequest{id} pullRequestReview{id fullDatabaseId state body commit{oid repository{id}} submittedAt comments{totalCount}} commit{oid repository{id}} originalCommit{oid repository{id}} replyTo{id} createdAt updatedAt publishedAt}}}}}";
pub use crate::provider_contract::InlineDraft;

impl InlineDraft {

    pub(super) fn input(&self, path: &str) -> Value {
        let side = if self.anchor.side == "old" {
            "LEFT"
        } else {
            "RIGHT"
        };
        let mut input = json!({"pullRequestReviewId":self.review_node_id,"body":self.body,"path":path,"side":side,"subjectType":if self.anchor.scope=="file"{"FILE"}else{"LINE"}});
        if self.anchor.scope == "lines" {
            input["line"] = json!(self.anchor.end_line);
            if self.anchor.start_line != self.anchor.end_line {
                input["startLine"] = json!(self.anchor.start_line);
                input["startSide"] = json!(side);
            }
        }
        input
    }
}
impl Client {
    pub async fn append_pending_thread(
        &self,
        token: Token,
        expected: &str,
        q: &InlineDraft,
    ) -> CreateOutcome {
        self.inline_draft_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn inline_draft_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &InlineDraft,
        deadline: Option<Instant>,
    ) -> CreateOutcome {
        self.run_mutation(deadline, |mutation| async move {
            q.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let before = reader
                .inline_preparation(q.number, &q.anchor, actor, expected)
                .await?;
            if before["comments_snapshot"]["count"]
                .as_u64()
                .is_none_or(|n| n >= 1000)
            {
                return Err(Failure::new(413, "github_inline_pending_capacity"));
            }
            if before["repository_node_id"] != q.repository_node_id
                || before["pull"]["external_id"] != q.pull_id
                || before["pull"]["node_id"] != q.pull_node_id
                || before["sha256"] != q.preparation_sha256
            {
                return Err(Failure::new(409, "github_inline_preparation_changed"));
            }
            if before["pending"]["node_id"] != q.review_node_id
                || before["pending"]["commit"] != q.anchor.head
                || before["pending"]["state"] != "pending"
            {
                return Err(Failure::new(
                    409,
                    "github_inline_pending_review_unavailable",
                ));
            }
            if before["archived"].as_bool() != Some(false)
                || before["disabled"].as_bool() != Some(false)
                || before["pull"]["state"] != "open"
                || before["pull"]["locked"].as_bool() != Some(false)
                || before["pull"]["merged"].as_bool() != Some(false)
            {
                return Err(Failure::new(403, "github_inline_write_unavailable"));
            }
            // GitHub positions both sides under the current filename on rename.
            // The immutable requested old-side path remains in the saved selector.
            let file = &before["diff"]["file"];
            let path = if r::text(file, "new_path")?.is_empty() {
                r::text(file, "old_path")?
            } else {
                r::text(file, "new_path")?
            };
            let proof = permit.dispatch("github_inline_creation_expired")?;
            let response = self.post_inline_draft(&proof, q, path, token).await?;
            let thread = &response.value["data"]["addPullRequestReviewThread"]["thread"];
            let side = if q.anchor.side == "old" {
                "LEFT"
            } else {
                "RIGHT"
            };
            let line = if q.anchor.scope == "file" {
                Value::Null
            } else {
                json!(q.anchor.end_line)
            };
            let start = if q.anchor.scope == "file" || q.anchor.start_line == q.anchor.end_line {
                Value::Null
            } else {
                json!(q.anchor.start_line)
            };
            let subject = if q.anchor.scope == "file" {
                "FILE"
            } else {
                "LINE"
            };
            // GitHub can give a FILE thread a display line (currently 1),
            // while its root comment correctly has no line anchor. That hint
            // is not part of the requested file-level selection.
            let thread_line_matches = if q.anchor.scope == "file" {
                thread["line"].is_null()
                    || thread["line"].as_u64().is_some_and(|n| n > 0 && n <= i64::MAX as u64)
            } else {
                thread["line"] == line
            };
            if !r::text(thread, "id").is_ok_and(node_id)
                || thread["path"] != path
                || thread["diffSide"] != side
                || !thread_line_matches
                || thread["startLine"] != start
                    && !(start.is_null()
                        && q.anchor.scope == "lines"
                        && thread["startLine"] == line)
                || thread["startDiffSide"]
                    != if start.is_null() {
                        Value::Null
                    } else {
                        json!(side)
                    }
                || thread["subjectType"] != subject
                || thread["repository"]["id"] != q.repository_node_id
                || thread["pullRequest"]["id"] != q.pull_node_id
                || r::big_id(&thread["pullRequest"]["fullDatabaseId"])? != q.pull_id
                || thread["pullRequest"]["number"].as_u64() != Some(q.number)
                || thread["pullRequest"]["repository"]["id"] != q.repository_node_id
            {
                return Err(Failure::protocol());
            }
            let nodes = thread["comments"]["nodes"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            if nodes.len() != 1
                || thread["comments"]["totalCount"].as_u64() != Some(1)
                || r::graphql_continuation(&thread["comments"])?.is_some()
            {
                return Err(Failure::protocol());
            }
            let c = &nodes[0];
            let review = &c["pullRequestReview"];
            if !r::text(c, "id").is_ok_and(node_id)
                || c["body"] != q.body
                || c["path"] != path
                || c["line"] != line
                || c["startLine"] != start
                || c["subjectType"] != subject
                || c["author"]["__typename"] != "User"
                || c["author"]["id"] != actor
                || c["repository"]["id"] != q.repository_node_id
                || c["pullRequest"]["id"] != q.pull_node_id
                || review["id"] != q.review_node_id
                || !c["replyTo"].is_null()
            {
                return Err(Failure::protocol());
            }
            for field in ["commit", "originalCommit"] {
                if c[field]["oid"] != q.anchor.head
                    || c[field]["repository"]["id"] != q.repository_node_id
                {
                    return Err(Failure::protocol());
                }
            }
            if review["commit"]["oid"] != q.anchor.head
                || review["commit"]["repository"]["id"] != q.repository_node_id
            {
                return Err(Failure::protocol());
            }
            let numeric = if c["fullDatabaseId"].is_null() {
                Value::Null
            } else {
                json!(r::big_id(&c["fullDatabaseId"])?)
            };
            let review_numeric = if review["fullDatabaseId"].is_null() {
                Value::Null
            } else {
                json!(r::big_id(&review["fullDatabaseId"])?)
            };
            if before["pending"]["external_id"]
                .as_str()
                .is_some_and(|id| review_numeric.as_str() != Some(id))
            {
                return Err(Failure::protocol());
            }
            let review_state = r::text(review, "state")?;
            let comment_state = r::text(c, "state")?;
            if !matches!(
                review_state,
                "PENDING" | "COMMENTED" | "APPROVED" | "CHANGES_REQUESTED" | "DISMISSED"
            ) || !matches!(comment_state, "PENDING" | "SUBMITTED")
            {
                return Err(Failure::protocol());
            }
            let dates = json!({"created":c["createdAt"],"updated":c["updatedAt"],"published":c["publishedAt"],"submitted":review["submittedAt"]});
            let receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":q.repository_node_id,"pull_id":q.pull_id,"pull_node_id":q.pull_node_id,"number":q.number,"head":q.anchor.head,"preparation_sha256":q.preparation_sha256,"selection_sha256":super::transport::sha256(&serde_json::to_vec(&q.anchor).map_err(|_|Failure::protocol())?),"selection_condition":"bounded_preflight_and_causal_reply","thread_node_id":thread["id"],"comment":{"node_id":c["id"],"external_id":numeric,"body_sha256":super::transport::sha256(q.body.as_bytes()),"state":comment_state.to_ascii_lowercase(),"created_at":r::date(&dates,"created")?,"updated_at":r::date(&dates,"updated")?,"published_at":r::date(&dates,"published")?},"review":{"node_id":q.review_node_id,"external_id":review_numeric,"state":review_state.to_ascii_lowercase(),"commit":q.anchor.head,"body_sha256":super::transport::sha256(r::text(review,"body")?.as_bytes()),"comments_count":review["comments"]["totalCount"].as_u64().ok_or_else(Failure::protocol)?,"submitted_at":r::date(&dates,"submitted")?},"expected_pending":{"sha256":before["pending_sha256"],"body_sha256":super::transport::sha256(r::text(&before["pending"],"body")?.as_bytes()),"comments_count":before["comments_snapshot"]["count"],"comments_sha256":before["comments_snapshot"]["identity_writing_sha256"]},"preservation_verification":"required","provider_response":{"protocol":"github_graphql_append_pending_thread","status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.creation()
    }
}

impl Client {
    pub(super) async fn post_inline_draft(
        &self,
        proof: &super::writes::Dispatched<'_>,
        q: &super::InlineDraft,
        path: &str,
        token: &[u8],
    ) -> super::Result<super::transport::MutationResponse> {
        let input = q.input(path);
        let token = zeroize::Zeroizing::new(token.to_vec());
        self.provider_request(
            move |http| {
                Ok((
                    super::transport::graphql(
                        http,
                        &token,
                        CREATE,
                        serde_json::json!({"input":input}),
                    )?,
                    1024 * 1024,
                ))
            },
            Some(proof),
        )
        .await
    }
}
