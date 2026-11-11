//! One bounded provider effect. Go must commit its exact intent and win the
//! durable one-shot reservation before invoking this operation. Nothing here
//! provides retry authorization, a remote idempotency key or conditional writes.
use super::{reads::Reader, records as r, request::remote_id, Client, CreateOutcome, Failure};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::time::Instant;

// A fixed mutation addresses the exact node learned from the same REST subject
// whose immutable database identity was checked. No owner/name selects a write.
// clientMutationId is not a documented deduplication key and is not used as one.
pub(super) const CREATE_COMMENT: &str = "mutation FactoryCreateComment($subject:ID!,$body:String!){addComment(input:{subjectId:$subject,body:$body}){subject{__typename id ... on Issue{url} ... on PullRequest{url}} commentEdge{node{id fullDatabaseId body createdAt updatedAt url minimizedReason author{login ... on User{id} ... on Bot{id}}}}}}";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Comment {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub body: String,
}
impl Comment {
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.body.len() > 64 * 1024
            || self.body.trim().is_empty()
            || self.body.contains('\0')
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

impl Client {
    /// Native integration surface, matching semantic reads: no URL, method,
    /// canonical mapping, browser capability or SQL authority enters this port.
    pub async fn create_comment(
        &self,
        token: Token,
        expected: &str,
        comment: &Comment,
    ) -> CreateOutcome {
        self.comment_account(token.as_bytes(), expected, comment, None)
            .await
    }
    pub(crate) async fn comment_account(
        &self,
        token: &[u8],
        expected: &str,
        comment: &Comment,
        deadline: Option<Instant>,
    ) -> CreateOutcome {
        self.run_mutation(deadline, |mutation| async move {
            let until = mutation.deadline;
            comment.validate()?;
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_comment_expired"));
            }
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor_node = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &comment.repository).await?;
            let pull = comment.subject == "pull";
            let subject = reader.raw_subject(pull, comment.number).await?;
            if r::id(&subject["id"])? != comment.subject_id
                || !pull && !subject["pull_request"].is_null()
            {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            let subject_node = r::text(&subject, "node_id")?;
            if !super::request::node_id(subject_node) {
                return Err(Failure::protocol());
            }
            // A lock can still change at the provider before POST. This is a
            // conservative preflight, never an atomic provider revision guard.
            if subject["locked"].as_bool() != Some(false) {
                return Err(Failure::new(403, "github_discussion_locked"));
            }
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != comment.repository {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            if repository["archived"].as_bool() != Some(false)
                || repository["disabled"].as_bool() != Some(false)
            {
                return Err(Failure::new(403, "github_repository_read_only"));
            }
            let proof = permit.dispatch("github_comment_expired")?;
            let response = self
                .post_comment(&proof, subject_node, &comment.body, token)
                .await?;
            let payload = &response.value["data"]["addComment"];
            let raw = &payload["commentEdge"]["node"];
            let id = r::big_id(&raw["fullDatabaseId"])?;
            if payload["subject"]["id"] != subject_node
                || payload["subject"]["__typename"] != if pull { "PullRequest" } else { "Issue" }
                || !r::text(raw, "id").is_ok_and(super::request::node_id)
                || raw["body"].as_str() != Some(comment.body.as_str())
                || raw["author"]["id"] != actor_node
            {
                return Err(Failure::protocol());
            }
            let parent = r::text(&payload["subject"], "url")?;
            let url = url::Url::parse(parent).map_err(|_| Failure::protocol())?;
            let parts: Vec<_> = url.path().trim_start_matches('/').split('/').collect();
            if url.scheme() != "https"
                || url.host_str() != Some("github.com")
                || url.port().is_some()
                || !url.username().is_empty()
                || url.password().is_some()
                || url.query().is_some()
                || url.fragment().is_some()
                || parts.len() != 4
                || !super::request::repository_name(&format!("{}/{}", parts[0], parts[1]))
                || parts[2] != if pull { "pull" } else { "issues" }
                || parts[3] != comment.number.to_string()
            {
                return Err(Failure::protocol());
            }
            if raw["url"] != format!("{parent}#issuecomment-{id}") {
                return Err(Failure::protocol());
            }
            // The immutable Go capture already retains the exact human writing.
            // Keep provider-returned metadata and causal POST evidence bounded,
            // rather than duplicating up to 64 KiB into its 64 KiB outcome.
            let dates = json!({"created_at":raw["createdAt"],"updated_at":raw["updatedAt"]});
            let projected = json!({"external_id":id,"node_id":raw["id"],
                "author":{"external_id":identity.external_id,"login":r::nonempty(&raw["author"],"login")?},"author_node_id":actor_node,
                "created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?,
                "minimized_reason":r::string(raw,"minimizedReason")?,"body_sha256":super::transport::sha256(comment.body.as_bytes()),
                "url":raw["url"],"issue_url":parent});
            self.current()?;
            let receipt = json!({"account":identity,"repository":comment.repository,"subject":comment.subject,
                "subject_id":comment.subject_id,"subject_node_id":subject_node,"number":comment.number,"comment":projected,
                "provider_response":{"protocol":"github_graphql_add_comment","status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            Ok(receipt)
        }).await.creation()
    }
}

impl Client {
    pub(super) async fn post_comment(
        &self,
        proof: &super::writes::Dispatched<'_>,
        node: &str,
        body: &str,
        token: &[u8],
    ) -> super::Result<super::transport::MutationResponse> {
        let node = node.to_owned();
        let body = body.to_owned();
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
                        CREATE_COMMENT,
                        serde_json::json!({"subject":node,"body":body}),
                    )?,
                    1024 * 1024,
                ))
            },
            Some(proof),
        )
        .await
    }
}
