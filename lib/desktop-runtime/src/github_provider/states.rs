//! Direct human close/reopen only. No comment, merge, branch synchronization,
//! retry authorization or atomic provider revision/transfer condition is implied.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, Failure,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::time::Instant;

const CLOSE_ISSUE: &str = "mutation FactoryCloseIssue($subject:ID!){closeIssue(input:{issueId:$subject}){issue{id fullDatabaseId number state url updatedAt repository{id nameWithOwner}}}}";
const REOPEN_ISSUE: &str = "mutation FactoryReopenIssue($subject:ID!){reopenIssue(input:{issueId:$subject}){issue{id fullDatabaseId number state url updatedAt repository{id nameWithOwner}}}}";
const CLOSE_PULL: &str = "mutation FactoryClosePull($subject:ID!){closePullRequest(input:{pullRequestId:$subject}){pullRequest{id fullDatabaseId number state merged url updatedAt repository{id nameWithOwner}}}}";
const REOPEN_PULL: &str = "mutation FactoryReopenPull($subject:ID!){reopenPullRequest(input:{pullRequestId:$subject}){pullRequest{id fullDatabaseId number state merged url updatedAt repository{id nameWithOwner}}}}";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct DiscussionState {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub close: bool,
}
impl DiscussionState {
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}
pub use super::writes::WriteOutcome as StateOutcome;

// Private finite selector; the transport still accepts no arbitrary mutation.
#[derive(Clone, Copy)]
pub(super) struct StateMutation {
    pub pull: bool,
    pub close: bool,
}
impl StateMutation {
    pub(super) fn query(self) -> &'static str {
        match (self.pull, self.close) {
            (false, true) => CLOSE_ISSUE,
            (false, false) => REOPEN_ISSUE,
            (true, true) => CLOSE_PULL,
            (true, false) => REOPEN_PULL,
        }
    }
    fn payload(self) -> (&'static str, &'static str) {
        match (self.pull, self.close) {
            (false, true) => ("closeIssue", "issue"),
            (false, false) => ("reopenIssue", "issue"),
            (true, true) => ("closePullRequest", "pullRequest"),
            (true, false) => ("reopenPullRequest", "pullRequest"),
        }
    }
}
impl Client {
    pub async fn set_discussion_state(
        &self,
        token: Token,
        expected: &str,
        state: &DiscussionState,
    ) -> StateOutcome {
        self.state_account(token.as_bytes(), expected, state, None)
            .await
    }
    pub(crate) async fn state_account(
        &self,
        token: &[u8],
        expected: &str,
        state: &DiscussionState,
        deadline: Option<Instant>,
    ) -> StateOutcome {
        self.run_mutation(deadline, |mutation| async move {
            state.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let reader = Reader::new(self, token, &state.repository).await?;
            let mutation = StateMutation {
                pull: state.subject == "pull",
                close: state.close,
            };
            let subject = reader.raw_subject(mutation.pull, state.number).await?;
            if r::id(&subject["id"])? != state.subject_id
                || !mutation.pull && !subject["pull_request"].is_null()
            {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            let subject_node = r::text(&subject, "node_id")?;
            if !node_id(subject_node) {
                return Err(Failure::protocol());
            }
            if mutation.pull && subject["merged"].as_bool() != Some(false) {
                return Err(Failure::new(409, "github_pull_state_unavailable"));
            }
            let original = r::text(&subject, "state")?;
            if !matches!(original, "open" | "closed") {
                return Err(Failure::protocol());
            }
            if (original == "closed") == state.close {
                return Err(Failure::new(409, "github_state_already_current"));
            }
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != state.repository {
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
            permit.dispatch("github_state_expired")?;
            let response = self.post_state(subject_node, mutation, token).await?;
            let (field, kind) = mutation.payload();
            let raw = &response.value["data"][field][kind];
            if raw["id"] != subject_node
                || r::big_id(&raw["fullDatabaseId"])? != state.subject_id
                || raw["number"].as_u64() != Some(state.number)
                || raw["repository"]["id"] != repository_node
                || raw["state"] != if state.close { "CLOSED" } else { "OPEN" }
                || mutation.pull && raw["merged"].as_bool() != Some(false)
            {
                return Err(Failure::protocol());
            }
            let name = r::text(&raw["repository"], "nameWithOwner")?;
            if !super::request::repository_name(name)
                || raw["url"]
                    != format!(
                        "https://github.com/{name}/{}/{number}",
                        if mutation.pull { "pull" } else { "issues" },
                        number = state.number
                    )
            {
                return Err(Failure::protocol());
            }
            let date = json!({"updated_at":raw["updatedAt"]});
            // This is the causal mutation response under the verified account,
            // not a claim that its snapshot attributes a timeline event to us.
            let receipt = json!({"account":identity,"repository":state.repository,"repository_node_id":repository_node,
                "subject":state.subject,"subject_id":state.subject_id,"subject_node_id":subject_node,"number":state.number,
                "close":state.close,"state":raw["state"],"url":raw["url"],"updated_at":r::date(&date,"updated_at")?,
                "provider_response":{"protocol":"github_graphql_discussion_state","mutation":field,"status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.update()
    }
}

impl Client {
    pub(super) async fn post_state(
        &self,
        node: &str,
        mutation: super::states::StateMutation,
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
                        mutation.query(),
                        serde_json::json!({"subject":node}),
                    )?,
                    1024 * 1024,
                ))
            },
            false,
        )
        .await
    }
}
