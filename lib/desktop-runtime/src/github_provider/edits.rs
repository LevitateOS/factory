//! Exact partial title/body updates. The original-content check is a preflight,
//! never a provider CAS, transfer condition, remote journal or retry grant.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, Failure, WriteOutcome,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::time::{Duration, Instant};

pub(super) const EDIT_ISSUE: &str = "mutation FactoryEditIssue($input:UpdateIssueInput!){updateIssue(input:$input){issue{id fullDatabaseId number title body url updatedAt repository{id nameWithOwner}}}}";
pub(super) const EDIT_PULL: &str = "mutation FactoryEditPull($input:UpdatePullRequestInput!){updatePullRequest(input:$input){pullRequest{id fullDatabaseId number title body url updatedAt repository{id nameWithOwner}}}}";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct DiscussionEdit {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub title: Option<String>,
    pub body: Option<String>,
    pub original_title: String,
    pub original_body_sha256: String,
}
impl DiscussionEdit {
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.title.is_none() && self.body.is_none()
            || self
                .title
                .as_ref()
                .is_some_and(|s| s.len() > 255 || s.trim().is_empty() || s.contains('\0'))
            || self
                .body
                .as_ref()
                .is_some_and(|s| s.len() > 65536 || s.contains('\0'))
            || self.original_title.len() > 65536
            || self.original_title.contains('\0')
            || self.original_body_sha256.len() != 64
            || !self
                .original_body_sha256
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}
impl Client {
    pub async fn edit_discussion(
        &self,
        token: Token,
        expected: &str,
        edit: &DiscussionEdit,
    ) -> WriteOutcome {
        self.edit_account(token.as_bytes(), expected, edit, None)
            .await
    }
    pub(crate) async fn edit_account(
        &self,
        token: &[u8],
        expected: &str,
        edit: &DiscussionEdit,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        let mut attempted = false;
        let until = deadline
            .unwrap_or_else(|| Instant::now() + Duration::from_secs(8))
            .min(Instant::now() + Duration::from_secs(8));
        let work = async {
            edit.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            self.current()?;
            let mut next = self.mutations.lock().await;
            self.delay(next.saturating_duration_since(Instant::now()))
                .await?;
            let identity = self.identity(token).await?;
            if identity.external_id != expected {
                return Err(Failure::new(409, "github_account_changed"));
            }
            let reader = Reader::new(self, token, &edit.repository).await?;
            let pull = edit.subject == "pull";
            let subject = reader.raw_subject(pull, edit.number).await?;
            if r::id(&subject["id"])? != edit.subject_id
                || !pull && !subject["pull_request"].is_null()
            {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            let subject_node = r::text(&subject, "node_id")?;
            if !node_id(subject_node) {
                return Err(Failure::protocol());
            }
            let original_body = match subject.get("body") {
                Some(Value::String(body)) => body.as_str(),
                Some(Value::Null) => "",
                _ => return Err(Failure::protocol()),
            };
            if subject["title"] != edit.original_title
                || super::transport::sha256(original_body.as_bytes()) != edit.original_body_sha256
            {
                return Err(Failure::new(409, "github_edit_base_changed"));
            }
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != edit.repository {
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
            self.current()?;
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_edit_expired"));
            }
            *next = Instant::now() + Duration::from_secs(1);
            attempted = true;
            let response = self
                .post_edit(
                    subject_node,
                    pull,
                    edit.title.as_deref(),
                    edit.body.as_deref(),
                    token,
                )
                .await?;
            let (mutation, kind) = if pull {
                ("updatePullRequest", "pullRequest")
            } else {
                ("updateIssue", "issue")
            };
            let raw = &response.value["data"][mutation][kind];
            let expected_title = edit.title.as_deref().unwrap_or(&edit.original_title);
            let expected_body = edit.body.as_deref().unwrap_or(original_body);
            if raw["id"] != subject_node
                || r::big_id(&raw["fullDatabaseId"])? != edit.subject_id
                || raw["number"].as_u64() != Some(edit.number)
                || raw["repository"]["id"] != repository_node
                || raw["title"].as_str() != Some(expected_title)
                || raw["body"].as_str() != Some(expected_body)
            {
                return Err(Failure::protocol());
            }
            let name = r::text(&raw["repository"], "nameWithOwner")?;
            if !super::request::repository_name(name)
                || raw["url"]
                    != format!(
                        "https://github.com/{name}/{}/{number}",
                        if pull { "pull" } else { "issues" },
                        number = edit.number
                    )
            {
                return Err(Failure::protocol());
            }
            let date = json!({"updated_at":raw["updatedAt"]});
            let receipt = json!({"account":identity,"repository":edit.repository,"repository_node_id":repository_node,
                "subject":edit.subject,"subject_id":edit.subject_id,"subject_node_id":subject_node,"number":edit.number,
                "edited_title":edit.title.is_some(),"edited_body":edit.body.is_some(),"title_sha256":super::transport::sha256(expected_title.as_bytes()),
                "body_sha256":super::transport::sha256(expected_body.as_bytes()),"url":raw["url"],"updated_at":r::date(&date,"updated_at")?,
                "provider_response":{"protocol":"github_graphql_discussion_edit","mutation":mutation,"status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            if serde_json::to_vec(&receipt)
                .map_err(|_| Failure::protocol())?
                .len()
                > 4096
            {
                return Err(Failure::new(413, "github_receipt_too_large"));
            }
            self.current()?;
            Ok(receipt)
        };
        let result = self
            .scope(async {
                tokio::time::timeout_at(until.into(), work)
                    .await
                    .unwrap_or_else(|_| Err(Failure::new(504, "github_deadline")))
            })
            .await
            .and_then(|r| r);
        match result {
            Ok(r) => WriteOutcome::Applied(r),
            Err(f) if attempted => WriteOutcome::Indeterminate(f),
            Err(f) => WriteOutcome::NotSent(f),
        }
    }
}
