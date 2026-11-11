//! One direct issue/PR lock effect. No implicit unlock/relock sequence, content
//! change, remote retry authorization or atomic repository condition is implied.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, Failure, WriteOutcome,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::time::Instant;

pub(super) const LOCK:&str="mutation FactoryLockDiscussion($input:LockLockableInput!){lockLockable(input:$input){actor{login ... on User{id} ... on Bot{id}} lockedRecord{__typename locked activeLockReason ... on Issue{id fullDatabaseId number url updatedAt repository{id nameWithOwner}} ... on PullRequest{id fullDatabaseId number url updatedAt repository{id nameWithOwner}}}}}";
pub(super) const UNLOCK:&str="mutation FactoryUnlockDiscussion($input:UnlockLockableInput!){unlockLockable(input:$input){actor{login ... on User{id} ... on Bot{id}} unlockedRecord{__typename locked activeLockReason ... on Issue{id fullDatabaseId number url updatedAt repository{id nameWithOwner}} ... on PullRequest{id fullDatabaseId number url updatedAt repository{id nameWithOwner}}}}}";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct DiscussionLock {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub lock: bool,
    pub reason: String,
}
impl DiscussionLock {
    // Generic human reasons remain intact through journal/native admission.
    // Provider-specific support is a known no-send outcome of the effect below.
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.reason.len() > 65536
            || self.reason.contains('\0')
            || !self.lock && !self.reason.is_empty()
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
    fn provider_reason(&self) -> Result<Option<&'static str>, Failure> {
        match self.reason.as_str() {
            "" => Ok(None),
            "off-topic" => Ok(Some("OFF_TOPIC")),
            "resolved" => Ok(Some("RESOLVED")),
            "spam" => Ok(Some("SPAM")),
            "too heated" => Ok(Some("TOO_HEATED")),
            _ => Err(Failure::new(400, "github_lock_reason_unsupported")),
        }
    }
}
impl Client {
    pub async fn set_discussion_lock(
        &self,
        token: Token,
        expected: &str,
        lock: &DiscussionLock,
    ) -> WriteOutcome {
        self.lock_account(token.as_bytes(), expected, lock, None)
            .await
    }
    pub(crate) async fn lock_account(
        &self,
        token: &[u8],
        expected: &str,
        lock: &DiscussionLock,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        self.run_mutation(deadline, |mutation| async move {
            lock.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let reason = lock.provider_reason()?;
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor_node = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &lock.repository).await?;
            let pull = lock.subject == "pull";
            let subject = reader.raw_subject(pull, lock.number).await?;
            if r::id(&subject["id"])? != lock.subject_id
                || !pull && !subject["pull_request"].is_null()
            {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            let subject_node = r::text(&subject, "node_id")?;
            if !node_id(subject_node) || subject["locked"].as_bool().is_none() {
                return Err(Failure::protocol());
            }
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != lock.repository {
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
            let proof = permit.dispatch("github_lock_expired")?;
            let response = self
                .post_lock(&proof, subject_node, lock.lock, reason, token)
                .await?;
            let (mutation, field) = if lock.lock {
                ("lockLockable", "lockedRecord")
            } else {
                ("unlockLockable", "unlockedRecord")
            };
            let payload = &response.value["data"][mutation];
            let raw = &payload[field];
            if raw["__typename"] != if pull { "PullRequest" } else { "Issue" }
                || raw["id"] != subject_node
                || r::big_id(&raw["fullDatabaseId"])? != lock.subject_id
                || raw["number"].as_u64() != Some(lock.number)
                || raw["repository"]["id"] != repository_node
                || raw["locked"].as_bool() != Some(lock.lock)
                || payload["actor"]["id"] != actor_node
                || raw.get("activeLockReason") != Some(&json!(reason))
            {
                return Err(Failure::protocol());
            }
            let name = r::text(&raw["repository"], "nameWithOwner")?;
            if !super::request::repository_name(name)
                || raw["url"]
                    != format!(
                        "https://github.com/{name}/{}/{number}",
                        if pull { "pull" } else { "issues" },
                        number = lock.number
                    )
            {
                return Err(Failure::protocol());
            }
            let date = json!({"updated_at":raw["updatedAt"]});
            let receipt = json!({"account":identity,"repository":lock.repository,"repository_node_id":repository_node,
                "subject":lock.subject,"subject_id":lock.subject_id,"subject_node_id":subject_node,"number":lock.number,
                "locked":lock.lock,"reason":lock.reason,"active_lock_reason":raw["activeLockReason"],"url":raw["url"],"updated_at":r::date(&date,"updated_at")?,
                "actor":{"external_id":identity.external_id,"login":r::nonempty(&payload["actor"],"login")?},"actor_node_id":actor_node,
                "provider_response":{"protocol":"github_graphql_discussion_lock","mutation":mutation,"status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.update()
    }
}

impl Client {
    pub(super) async fn post_lock(
        &self,
        proof: &super::writes::Dispatched<'_>,
        node: &str,
        lock: bool,
        reason: Option<&str>,
        token: &[u8],
    ) -> super::Result<super::transport::MutationResponse> {
        let node = node.to_owned();

        let reason = reason.map(str::to_owned);
        let token = zeroize::Zeroizing::new(token.to_vec());
        self.provider_request(
            move |http| {
                if !super::request::node_id(&node) || !lock && reason.is_some() {
                    return Err(Failure::new(400, "invalid_native_write"));
                }
                let mut input = serde_json::Map::new();
                input.insert("lockableId".into(), serde_json::json!(node));
                if let Some(reason) = reason {
                    input.insert("lockReason".into(), serde_json::json!(reason));
                }
                Ok((
                    super::transport::graphql(
                        http,
                        &token,
                        if lock { LOCK } else { UNLOCK },
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
