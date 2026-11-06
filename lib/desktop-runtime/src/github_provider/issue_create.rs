//! Plain human issue creation under the complete native provider owner. The Go
//! journal must win its durable reservation before calling this one-shot effect.
use super::{
    records as r,
    request::{node_id, remote_id},
    Client, CreateOutcome, Failure,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::time::Instant;

pub(super) const CREATE_ISSUE:&str="mutation FactoryCreateIssue($repository:ID!,$title:String!,$body:String!){createIssue(input:{repositoryId:$repository,title:$title,body:$body}){issue{id fullDatabaseId number title body url createdAt updatedAt repository{id nameWithOwner} author{login ... on User{id} ... on Bot{id}}}}}";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct IssueCreate {
    pub repository: String,
    pub title: String,
    pub body: String,
}
impl IssueCreate {
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || self.title.is_empty()
            || self.title.trim() != self.title
            || self.title.len() > 255
            || self.title.contains('\0')
            || self.body.len() > 65536
            || self.body.contains('\0')
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}
impl Client {
    pub async fn create_issue(
        &self,
        token: Token,
        expected: &str,
        issue: &IssueCreate,
    ) -> CreateOutcome {
        self.issue_create_account(token.as_bytes(), expected, issue, None)
            .await
    }
    pub(crate) async fn issue_create_account(
        &self,
        token: &[u8],
        expected: &str,
        issue: &IssueCreate,
        deadline: Option<Instant>,
    ) -> CreateOutcome {
        self.run_mutation(deadline, |mutation| async move {
            let until = mutation.deadline;
            issue.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_issue_creation_expired"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor_node = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let repository = self
                .get(&format!("/repositories/{}", issue.repository), &[], token)
                .await?
                .value;
            if r::id(&repository["id"])? != issue.repository {
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
            if repository["has_issues"].as_bool() != Some(true) {
                return Err(Failure::new(403, "github_issues_unavailable"));
            }
            permit.dispatch("github_issue_creation_expired")?;
            let response = self
                .post_issue(repository_node, &issue.title, &issue.body, token)
                .await?;
            let raw = &response.value["data"]["createIssue"]["issue"];
            let id = r::big_id(&raw["fullDatabaseId"])?;
            let number = raw["number"]
                .as_u64()
                .filter(|n| *n > 0 && *n <= i64::MAX as u64)
                .ok_or_else(Failure::protocol)?;
            if !r::text(raw, "id").is_ok_and(node_id)
                || raw["repository"]["id"] != repository_node
                || raw["author"]["id"] != actor_node
                || raw["title"].as_str() != Some(issue.title.as_str())
                || raw["body"].as_str() != Some(issue.body.as_str())
            {
                return Err(Failure::protocol());
            }
            let name = r::text(&raw["repository"], "nameWithOwner")?;
            if !super::request::repository_name(name)
                || raw["url"] != format!("https://github.com/{name}/issues/{number}")
            {
                return Err(Failure::protocol());
            }
            let dates = json!({"created_at":raw["createdAt"],"updated_at":raw["updatedAt"]});
            let receipt = json!({"account":identity,"repository":issue.repository,"repository_node_id":repository_node,
                "issue":{"external_id":id,"node_id":raw["id"],"number":number,"url":raw["url"],
                "author":{"external_id":identity.external_id,"login":r::nonempty(&raw["author"],"login")?},"author_node_id":actor_node,
                "title_sha256":super::transport::sha256(issue.title.as_bytes()),"body_sha256":super::transport::sha256(issue.body.as_bytes()),
                "created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?},
                "provider_response":{"protocol":"github_graphql_create_issue","status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.creation()
    }
}

impl Client {
    pub(super) async fn post_issue(
        &self,
        node: &str,
        title: &str,
        body: &str,
        token: &[u8],
    ) -> super::Result<super::transport::MutationResponse> {
        let node = node.to_owned();
        let title = title.to_owned();
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
                        CREATE_ISSUE,
                        serde_json::json!({"repository":node,"title":title,"body":body}),
                    )?,
                    1024 * 1024,
                ))
            },
            false,
        )
        .await
    }
}
