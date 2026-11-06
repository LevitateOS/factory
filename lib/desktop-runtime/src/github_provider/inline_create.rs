use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id, remote_id},
    Client, CreateOutcome, Failure, InlineAnchor, Result,
};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::time::Instant;

const REVIEW:&str="mutation FactoryCreateInlineReview($input:AddPullRequestReviewInput!){addPullRequestReview(input:$input){pullRequestReview{id fullDatabaseId state body author{__typename ... on User{id}} repository{id} pullRequest{id fullDatabaseId number headRefOid repository{id}} commit{oid repository{id}} comments(first:2){totalCount pageInfo{hasNextPage endCursor} nodes{id fullDatabaseId body state path line startLine subjectType author{__typename ... on User{id}} repository{id} pullRequest{id} commit{oid repository{id}} originalCommit{oid repository{id}} replyTo{id} createdAt updatedAt publishedAt}} submittedAt}}}";

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
    fn thread(&self, path: &str) -> Value {
        let side = if self.anchor.side == "old" {
            "LEFT"
        } else {
            "RIGHT"
        };
        let mut v = json!({"path":path,"body":self.body,"side":side});
        if self.anchor.scope == "lines" {
            v["line"] = json!(self.anchor.end_line);
            if self.anchor.start_line != self.anchor.end_line {
                v["startLine"] = json!(self.anchor.start_line);
                v["startSide"] = json!(side);
            }
        }
        v
    }
}
impl Client {
    pub(crate) async fn inline_create_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &InlineCreate,
        deadline: Option<Instant>,
    ) -> CreateOutcome {
        let operation = |mutation: super::writes::Mutation| async move {
            q.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let before = reader
                .inline_preparation(q.number.into(), &q.anchor, actor, expected)
                .await?;
            if before["repository_node_id"] != q.repository_node_id
                || before["pull"]["external_id"] != q.pull_id
                || before["pull"]["node_id"] != q.pull_node_id
                || before["sha256"] != q.preparation_sha256
            {
                return Err(Failure::new(409, "github_inline_preparation_changed"));
            }
            if !before["pending"].is_null() {
                return Err(Failure::new(409, "github_pending_review_changed"));
            }
            if before["archived"] != false
                || before["disabled"] != false
                || before["pull"]["state"] != "open"
                || before["pull"]["locked"] != false
                || before["pull"]["merged"] != false
            {
                return Err(Failure::new(403, "github_inline_write_unavailable"));
            }
            let file = &before["diff"]["file"];
            let path = if r::text(file, "new_path")?.is_empty() {
                r::text(file, "old_path")?
            } else {
                r::text(file, "new_path")?
            };
            let line = if q.anchor.scope == "lines" {
                json!(q.anchor.end_line)
            } else {
                Value::Null
            };
            let start = if q.anchor.scope == "lines" && q.anchor.start_line != q.anchor.end_line {
                json!(q.anchor.start_line)
            } else {
                Value::Null
            };
            let subject = if q.anchor.scope == "file" {
                "file"
            } else {
                "line"
            };
            let held = zeroize::Zeroizing::new(token.to_vec());
            let mut input = json!({"pullRequestId":q.pull_node_id,"commitOID":q.anchor.head,"body":"","threads":[q.thread(path)]});
            if !q.draft {
                input["event"] = json!("COMMENT");
            }
            permit.dispatch("github_inline_creation_expired")?;
            let response = self
                .provider_request(
                    move |http| {
                        Ok((
                            super::transport::graphql(http, &held, REVIEW, json!({"input":input}))?,
                            1024 * 1024,
                        ))
                    },
                    false,
                )
                .await?;
            let review = &response.value["data"]["addPullRequestReview"]["pullRequestReview"];
            let state = if q.draft { "PENDING" } else { "COMMENTED" };
            let comment_state = if q.draft { "PENDING" } else { "SUBMITTED" };
            if !r::text(review, "id").is_ok_and(node_id)
                || review["state"] != state
                || review["body"] != ""
                || review["submittedAt"].is_null() != q.draft
                || review["author"]["__typename"] != "User"
                || review["author"]["id"] != actor
                || review["repository"]["id"] != q.repository_node_id
                || review["pullRequest"]["id"] != q.pull_node_id
                || r::big_id(&review["pullRequest"]["fullDatabaseId"])? != q.pull_id
                || review["pullRequest"]["number"].as_u64() != Some(q.number.into())
                || review["pullRequest"]["headRefOid"] != q.anchor.head
                || review["pullRequest"]["repository"]["id"] != q.repository_node_id
                || review["commit"]["oid"] != q.anchor.head
                || review["commit"]["repository"]["id"] != q.repository_node_id
            {
                return Err(Failure::protocol());
            }
            let nodes = review["comments"]["nodes"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            if nodes.len() != 1
                || review["comments"]["totalCount"] != 1
                || r::graphql_continuation(&review["comments"])?.is_some()
            {
                return Err(Failure::protocol());
            }
            let c = &nodes[0];
            if !r::text(c, "id").is_ok_and(node_id)
                || c["body"] != q.body
                || c["state"] != comment_state
                || c["path"] != path
                || c["line"] != line
                || c["startLine"] != start
                || c["subjectType"] != subject.to_ascii_uppercase()
                || c["author"]["__typename"] != "User"
                || c["author"]["id"] != actor
                || c["repository"]["id"] != q.repository_node_id
                || c["pullRequest"]["id"] != q.pull_node_id
                || !c["replyTo"].is_null()
                || c["publishedAt"].is_null() != q.draft
            {
                return Err(Failure::protocol());
            }
            for key in ["commit", "originalCommit"] {
                if c[key]["oid"] != q.anchor.head
                    || c[key]["repository"]["id"] != q.repository_node_id
                {
                    return Err(Failure::protocol());
                }
            }
            let dates = json!({"created_at":c["createdAt"],"updated_at":c["updatedAt"],"published_at":c["publishedAt"],"submitted_at":review["submittedAt"]});
            r::date(&dates, "published_at")?;
            r::date(&dates, "submitted_at")?;
            let c = json!({"node_id":c["id"],"external_id":if c["fullDatabaseId"].is_null(){None}else{Some(r::big_id(&c["fullDatabaseId"])?)},"body_sha256":super::transport::sha256(q.body.as_bytes()),"created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?,"request_id":response.request_id,"response_sha256":response.response_sha256});
            let comment = c;
            let review_node = Some(r::text(review, "id")?.to_owned());
            let review_id = Some(r::big_id(&review["fullDatabaseId"])?);
            let (protocol, status) = ("github_graphql_create_inline_review", 200);
            let receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":q.repository_node_id,"pull_id":q.pull_id,"pull_node_id":q.pull_node_id,"number":q.number,"head":q.anchor.head,"preparation_sha256":q.preparation_sha256,"selection_sha256":super::transport::sha256(&serde_json::to_vec(&q.anchor).map_err(|_|Failure::protocol())?),"draft":q.draft,"review_node_id":review_node,"review_id":review_id,"comment_node_id":comment["node_id"],"comment_id":comment["external_id"],"body_sha256":comment["body_sha256"],"selection_condition":"bounded_preflight_and_causal_reply","provider_response":{"protocol":protocol,"status":status,"request_id":comment["request_id"],"response_sha256":comment["response_sha256"]}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        };
        self.run_mutation(deadline, operation).await.creation()
    }
}
