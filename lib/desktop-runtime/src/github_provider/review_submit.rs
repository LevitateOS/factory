//! One submission of the exact saved pending review. Persist its valid causal
//! primary before separate publication verification; current reads cannot
//! attribute a lost reply. Every product attempt requires the Go dispatch fence.
use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id, remote_id},
    Client, Failure, Result, WriteOutcome,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::time::Instant;

pub(super) const SUBMIT:&str="mutation FactorySubmitPendingReview($input:SubmitPullRequestReviewInput!){submitPullRequestReview(input:$input){pullRequestReview{id fullDatabaseId state body author{__typename login ... on User{id}} repository{id} pullRequest{id fullDatabaseId number headRefOid repository{id}} commit{oid repository{id}} comments{totalCount} createdAt updatedAt submittedAt}}}";
const UPDATE:&str="mutation FactoryEditPendingReview($input:UpdatePullRequestReviewInput!){updatePullRequestReview(input:$input){pullRequestReview{id fullDatabaseId state body author{__typename login ... on User{id}} repository{id} pullRequest{id fullDatabaseId number headRefOid repository{id}} commit{oid repository{id}} comments{totalCount} createdAt updatedAt submittedAt}}}";
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
    pub(super) fn wire_event(&self) -> &'static str {
        match self.event.as_str() {
            "comment" => "COMMENT",
            "approve" => "APPROVE",
            _ => "REQUEST_CHANGES",
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
    pub async fn submit_pending_review(
        &self,
        token: Token,
        expected: &str,
        q: &PendingReviewSubmit,
    ) -> WriteOutcome {
        self.review_submit_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn review_submit_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &PendingReviewSubmit,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        self.run_mutation(deadline, |mutation| async move {
            let until = mutation.deadline;
            q.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_review_submission_expired"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let before = reader
                .pending_review_snapshot(q.number, actor, expected)
                .await?;
            super::review_preparation::admit_verdict(&q.event, &before)?;
            if before["repository_node_id"] != q.repository_node_id
                || before["pull"]["external_id"] != q.pull_id
                || before["pull"]["node_id"] != q.pull_node_id
                || before["pull"]["head"] != q.head
            {
                return Err(Failure::new(409, "github_review_inputs_changed"));
            }
            if before["pending"].is_null()
                || before["pending"]["node_id"] != q.review_node_id
                || before["pending"]["external_id"].as_str() != q.review_id.as_deref()
                || before["pending"]["commit"] != q.review_commit
                || before["pending_sha256"] != q.pending_sha256
                || before["comments_snapshot"]["identity_writing_sha256"] != q.comments_sha256
                || before["comments_snapshot"]["count"].as_u64() != Some(q.comments_count)
                || before["comments_snapshot"]["complete"] != true
            {
                return Err(Failure::new(409, "github_pending_review_changed"));
            }
            if before["archived"].as_bool() != Some(false)
                || before["disabled"].as_bool() != Some(false)
                || before["pull"]["state"] != "open"
                || before["pull"]["locked"].as_bool() != Some(false)
                || before["pull"]["merged"].as_bool() != Some(false)
            {
                return Err(Failure::new(403, "github_review_write_unavailable"));
            }
            let body = q
                .body
                .as_deref()
                .unwrap_or(r::text(&before["pending"], "body")?);
            if body.trim().is_empty()
                && (q.event == "request_changes" || q.event == "comment" && q.comments_count == 0)
            {
                return Err(Failure::new(409, "github_review_body_required"));
            }
            permit.dispatch("github_review_submission_expired")?;
            let response = self.post_pending_review(q, token).await?;
            let raw = &response.value["data"][if q.event == "draft" { "updatePullRequestReview" } else { "submitPullRequestReview" }]["pullRequestReview"];
            let id = r::big_id(&raw["fullDatabaseId"])?;
            if raw["id"] != q.review_node_id
                || q.review_id
                    .as_deref()
                    .is_some_and(|expected| expected != id)
                || if q.event == "draft" { !matches!(raw["state"].as_str(), Some("PENDING" | "COMMENTED" | "APPROVED" | "CHANGES_REQUESTED" | "DISMISSED")) } else { raw["state"] != q.state() }
                || raw["body"] != body
                || raw["author"]["__typename"] != "User"
                || raw["author"]["id"] != actor
                || raw["repository"]["id"] != q.repository_node_id
                || raw["pullRequest"]["id"] != q.pull_node_id
                || r::big_id(&raw["pullRequest"]["fullDatabaseId"])? != q.pull_id
                || raw["pullRequest"]["number"].as_u64() != Some(q.number)
                || raw["pullRequest"]["headRefOid"] != q.head
                || raw["pullRequest"]["repository"]["id"] != q.repository_node_id
                || raw["commit"]["oid"] != q.review_commit
                || raw["commit"]["repository"]["id"] != q.repository_node_id
            {
                return Err(Failure::protocol());
            }
            let comments = raw["comments"]["totalCount"]
                .as_u64()
                .filter(|n| *n <= i64::MAX as u64)
                .ok_or_else(Failure::protocol)?;
            let dates = json!({"created_at":raw["createdAt"],"updated_at":raw["updatedAt"],"submitted_at":raw["submittedAt"]});
            let submitted = r::date(&dates, "submitted_at")?;
            if q.event != "draft" && submitted.is_null() || (raw["state"] == "PENDING") != submitted.is_null() {
                return Err(Failure::protocol());
            }
            let receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":q.repository_node_id,"pull_id":q.pull_id,"pull_node_id":q.pull_node_id,"number":q.number,"head":q.head,"head_condition":"preflight_and_causal_reply","event":q.event,"body_source":if q.body.is_some(){"explicit"}else{"unchanged_pending"},"pending":{"external_id":q.review_id,"node_id":q.review_node_id,"sha256":q.pending_sha256},"expected_comments":{"count":q.comments_count,"identity_writing_sha256":q.comments_sha256},"review":{"external_id":id,"node_id":raw["id"],"state":r::text(raw,"state")?.to_ascii_lowercase(),"body_sha256":super::transport::sha256(body.as_bytes()),"commit":q.review_commit,"comments_count":comments,"author_node_id":actor,"created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?,"submitted_at":submitted},"publication_verification":"required","provider_response":{"protocol":if q.event=="draft"{"github_graphql_update_pending_review"}else{"github_graphql_submit_review"},"status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.update()
    }
}

impl Client {
    pub(super) async fn post_pending_review(
        &self,
        q: &super::PendingReviewSubmit,
        token: &[u8],
    ) -> super::Result<super::transport::MutationResponse> {
        let review = q.review_node_id.clone();
        let event = q.wire_event();
        let draft = q.event == "draft";
        let body = q.body.clone();
        let token = zeroize::Zeroizing::new(token.to_vec());
        self.provider_request(
            move |http| {
                let mut input = serde_json::Map::new();
                input.insert("pullRequestReviewId".into(), serde_json::json!(review));
                if !draft {
                    input.insert("event".into(), serde_json::json!(event));
                }
                if let Some(body) = body {
                    input.insert("body".into(), serde_json::json!(body));
                }
                Ok((
                    super::transport::graphql(
                        http,
                        &token,
                        if draft { UPDATE } else { SUBMIT },
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
