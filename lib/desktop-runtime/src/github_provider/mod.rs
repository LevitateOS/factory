//! Complete native GitHub session. No Tauri, product SQL, or browser HTTP bridge.
//! The transport owner is joined on retirement, including its TLS/HTTP drivers.
mod automation_checks;
mod delivery_opinions;
mod delivery_discussion;
mod automation_inputs;
mod branch_create;
mod capabilities;
mod check_gate;
mod comments;
mod commits;
mod diff;
mod edits;
mod dependencies;
mod evidence;
mod external_pulls;
mod git_fetch;
mod git_publish;
mod inline_create;
mod inline_draft;
mod inline_preparation;
mod issue_create;
mod locks;
mod merges;
mod oauth;
mod patch;
mod protection;
mod pull_branches;
mod pull_create;
mod reads;
mod records;
mod request;
mod review_create;
mod review_preparation;
mod review_request_effect;
mod review_requests;
mod review_submit;
mod review_threads;
mod states;
mod thread_resolution;
mod transport;
mod traffic_budget;
mod writes;

use crate::credentials::{Account, Provider, Token};
pub use branch_create::BranchCreate;
pub use comments::Comment;
pub use edits::DiscussionEdit;
pub use dependencies::IssueDependency;
pub use git_publish::BranchPublish;
pub use inline_create::InlineCreate;
pub use inline_draft::InlineDraft;
pub use inline_preparation::InlineAnchor;
pub use issue_create::IssueCreate;
pub use locks::DiscussionLock;
pub use merges::PullMerge;
pub use states::{DiscussionState, StateOutcome};
pub use writes::{CreateOutcome, WriteOutcome};
pub(crate) use writes::WriteOperation;
pub type CommentOutcome = CreateOutcome;
pub use oauth::{DeviceAuthorization, DevicePrompt};
pub use pull_branches::PullBranchRead;
pub use pull_create::{PullBranchPin, PullCreate};
pub use request::Read;
pub use review_create::ReviewCreate;
pub use review_request_effect::ReviewRequestChange;
pub use review_requests::{ReviewerRead, ReviewerTarget};
pub use review_submit::PendingReviewSubmit;
pub use review_threads::ThreadRead;
use serde::Serialize;
pub use thread_resolution::{OriginalThreadAnchor, ThreadResolution};
pub use transport::{Client, Owner, TrafficSnapshot};

pub const API_VERSION: &str = "2026-03-10";
pub const OAUTH_CLIENT_ID: &str = "Ov23liI9YQMPuYjBL6nn";

pub use crate::provider_contract::Failure;
pub(crate) use crate::provider_contract::Result;

/// Provider-local identity. Canonical identity and connection binding stay in Go.
#[derive(Serialize)]
pub struct Identity {
    pub external_id: String,
    pub login: String,
    #[serde(skip_serializing)]
    pub(crate) node_id: Option<String>,
}
impl Identity {
    pub(crate) fn account(&self) -> Result<Account> {
        Account::new(Provider::Github, "https://github.com", &self.external_id)
            .and_then(|value| value.with_label(&self.login))
            .map_err(|_| Failure::protocol())
    }
}
impl Client {
    pub(crate) async fn identity(&self, token: &[u8]) -> Result<Identity> {
        let raw = self.get("/user", &[], token).await?.value;
        let id = raw["id"]
            .as_i64()
            .filter(|id| *id > 0)
            .ok_or_else(Failure::protocol)?;
        let login = raw["login"]
            .as_str()
            .filter(|login| !login.is_empty())
            .ok_or_else(Failure::protocol)?;
        let identity = Identity {
            external_id: id.to_string(),
            login: login.into(),
            node_id: raw["node_id"]
                .as_str()
                .filter(|id| request::node_id(id))
                .map(str::to_owned),
        };
        identity.account()?;
        self.current()?;
        Ok(identity)
    }
    /// An unpersisted token must be verified through /user before native custody.
    pub async fn verify(&self, token: Token) -> Result<(Account, Token)> {
        let identity = self.identity(token.as_bytes()).await?;
        Ok((identity.account()?, token))
    }
}
