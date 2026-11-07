//! One new review over a saved PR/head. Existing pending reviews require their
//! separate submission owner. No remote deduplication or atomic head condition.
use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id, remote_id},
    Client, CreateOutcome, Failure, Result,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::time::Instant;

pub(super) const CREATE:&str="mutation FactoryCreateReview($input:AddPullRequestReviewInput!){addPullRequestReview(input:$input){pullRequestReview{id fullDatabaseId state body url author{__typename login ... on User{id}} repository{id} pullRequest{id fullDatabaseId number headRefOid baseRefOid repository{id}} commit{oid repository{id}} comments{totalCount} createdAt updatedAt submittedAt}}}";
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewCreate {
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
    pub(super) fn wire_event(&self) -> Option<&'static str> {
        match self.event.as_str() {
            "draft" => None,
            "comment" => Some("COMMENT"),
            "approve" => Some("APPROVE"),
            "request_changes" => Some("REQUEST_CHANGES"),
            _ => None,
        }
    }
    fn state(&self) -> &'static str {
        match self.event.as_str() {
            "draft" => "PENDING",
            "comment" => "COMMENTED",
            "approve" => "APPROVED",
            _ => "CHANGES_REQUESTED",
        }
    }
}
impl Client {
    pub async fn create_review(
        &self,
        token: Token,
        expected: &str,
        q: &ReviewCreate,
    ) -> CreateOutcome {
        self.review_create_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn review_create_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &ReviewCreate,
        deadline: Option<Instant>,
    ) -> CreateOutcome {
        self.run_mutation(deadline, |mutation| async move {
            let until = mutation.deadline;
            q.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_review_creation_expired"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let before = reader.review_preparation(q.number, actor, expected).await?;
            super::review_preparation::admit_verdict(&q.event, &before)?;
            if before["repository_node_id"] != q.repository_node_id
                || before["pull"]["external_id"] != q.pull_id
                || before["pull"]["node_id"] != q.pull_node_id
                || before["pull"]["head"] != q.head
            {
                return Err(Failure::new(409, "github_review_inputs_changed"));
            }
            if !before["pending"].is_null() {
                return Err(Failure::new(
                    409,
                    "github_pending_review_requires_submission",
                ));
            }
            if before["archived"].as_bool() != Some(false)
                || before["disabled"].as_bool() != Some(false)
                || before["pull"]["state"] != "open"
                || before["pull"]["locked"].as_bool() != Some(false)
                || before["pull"]["merged"].as_bool() != Some(false)
            {
                return Err(Failure::new(403, "github_review_write_unavailable"));
            }
            if q.event == "comment" && q.body.trim().is_empty() {
                return Err(Failure::new(409, "github_review_body_required"));
            }
            let diff_base = if !q.base.is_empty() {
                let pull=reader.raw_subject(true,q.number).await?;
                if pull["head"]["sha"]!=q.head || pull["base"]["sha"]!=q.base {
                    return Err(Failure::new(409,"github_review_inputs_changed"));
                }
                Some(reader.merge_base(&q.base,&q.head).await?)
            } else { None };
            permit.dispatch("github_review_creation_expired")?;
            let response = self.post_review(q, token).await?;
            let raw = &response.value["data"]["addPullRequestReview"]["pullRequestReview"];
            let id = r::big_id(&raw["fullDatabaseId"])?;
            if !r::text(raw, "id").is_ok_and(node_id)
                || raw["state"] != q.state()
                || raw["body"] != q.body
                || raw["author"]["__typename"] != "User"
                || raw["author"]["id"] != actor
                || raw["repository"]["id"] != q.repository_node_id
                || raw["pullRequest"]["id"] != q.pull_node_id
                || r::big_id(&raw["pullRequest"]["fullDatabaseId"])? != q.pull_id
                || raw["pullRequest"]["number"].as_u64() != Some(q.number)
                || raw["pullRequest"]["headRefOid"] != q.head
                || !q.base.is_empty() && raw["pullRequest"]["baseRefOid"]!=q.base
                || raw["pullRequest"]["repository"]["id"] != q.repository_node_id
                || raw["commit"]["oid"] != q.head
                || raw["commit"]["repository"]["id"] != q.repository_node_id
                || raw["comments"]["totalCount"].as_u64() != Some(0)
            {
                return Err(Failure::protocol());
            }
            if raw["url"]
                != format!(
                    "https://github.com/{}/pull/{}#pullrequestreview-{id}",
                    reader.name, q.number
                )
            {
                return Err(Failure::protocol());
            }
            let dates = json!({"created_at":raw["createdAt"],"updated_at":raw["updatedAt"],"submitted_at":raw["submittedAt"]});
            let submitted = r::date(&dates, "submitted_at")?;
            if (q.event == "draft") != submitted.is_null() {
                return Err(Failure::protocol());
            }
            let mut receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":q.repository_node_id,"pull_id":q.pull_id,"pull_node_id":q.pull_node_id,"number":q.number,"head":q.head,"event":q.event,"head_condition":"preflight_and_causal_reply","existing_pending":"not_observed_before_send","review":{"external_id":id,"node_id":raw["id"],"state":q.state().to_ascii_lowercase(),"body_sha256":super::transport::sha256(q.body.as_bytes()),"commit":q.head,"comments_count":0,"author_node_id":actor,"created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?,"submitted_at":submitted},"provider_response":{"protocol":"github_graphql_create_review","status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            if !q.base.is_empty() { receipt["base"]=json!(q.base); }
            if let Some(diff_base) = diff_base { receipt["diff_base"]=json!(diff_base); }
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.creation()
    }
}

impl Client {
    pub(super) async fn post_review(
        &self,
        q: &super::ReviewCreate,
        token: &[u8],
    ) -> super::Result<super::transport::MutationResponse> {
        let pull = q.pull_node_id.clone();
        let head = q.head.clone();
        let event = q.wire_event();
        let body = q.body.clone();
        let token = zeroize::Zeroizing::new(token.to_vec());
        self.provider_request(
            move |http| {
                let mut input = serde_json::Map::new();
                input.insert("pullRequestId".into(), serde_json::json!(pull));
                input.insert("commitOID".into(), serde_json::json!(head));
                input.insert("body".into(), serde_json::json!(body));
                if let Some(event) = event {
                    input.insert("event".into(), serde_json::json!(event));
                }
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
            false,
        )
        .await
    }
}
