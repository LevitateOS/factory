//! Single-target provider effects. Removal's name selector is checked before
//! and after dispatch; it is not an atomic immutable-target/repository condition.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, Failure, ReviewerRead, ReviewerTarget, WriteOutcome,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{
    collections::HashMap,
    time::{Duration, Instant},
};

pub(super) const REQUEST:&str="mutation FactoryRequestReviewer($input:RequestReviewsInput!){requestReviews(input:$input){actor{login ... on User{id} ... on Bot{id}} pullRequest{id fullDatabaseId number url repository{id nameWithOwner}}}}";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewRequestChange {
    pub repository: String,
    pub pull_id: String,
    pub number: u64,
    pub target: ReviewerTarget,
    pub requested: bool,
}
impl ReviewRequestChange {
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || !remote_id(&self.pull_id)
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.target.validate().is_err()
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}
impl Client {
    pub async fn set_review_request(
        &self,
        token: Token,
        expected: &str,
        q: &ReviewRequestChange,
    ) -> WriteOutcome {
        self.review_request_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn review_request_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &ReviewRequestChange,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        let mut attempted = false;
        let until = deadline
            .unwrap_or_else(|| Instant::now() + Duration::from_secs(8))
            .min(Instant::now() + Duration::from_secs(8));
        let work = async {
            q.validate()?;
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
            let account_node = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let pull = reader.raw_subject(true, q.number).await?;
            if r::id(&pull["id"])? != q.pull_id {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            let pull_node = r::text(&pull, "node_id")?;
            if !node_id(pull_node) {
                return Err(Failure::protocol());
            }
            match pull["locked"].as_bool() {
                Some(false) => {}
                Some(true) => return Err(Failure::new(403, "github_discussion_locked")),
                None => return Err(Failure::protocol()),
            }
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != q.repository {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            let repo_node = r::text(&repository, "node_id")?;
            if !node_id(repo_node) {
                return Err(Failure::protocol());
            }
            if repository["archived"].as_bool() != Some(false)
                || repository["disabled"].as_bool() != Some(false)
            {
                return Err(Failure::new(403, "github_repository_read_only"));
            }
            let read = ReviewerRead {
                repository: q.repository.clone(),
                number: q.number,
                target: Some(q.target.clone()),
            };
            let before = reader.review_requests(&read).await?;
            if before["pull"]["external_id"] != q.pull_id
                || before["pull"]["node_id"] != pull_node
                || before["repository_node_id"] != repo_node
            {
                return Err(Failure::new(409, "github_review_requests_changed"));
            }
            let Some(requested) = before["requested"].as_bool() else {
                return Err(Failure::new(409, "github_review_request_unobserved"));
            };
            if requested == q.requested {
                return Err(Failure::new(409, "github_reviewer_already_desired"));
            }
            if q.requested
                && before["items"]
                    .as_array()
                    .ok_or_else(Failure::protocol)?
                    .len()
                    >= 1000
            {
                return Err(Failure::new(413, "github_review_requests_too_large"));
            }
            let target = &before["selected_target"];
            let target_node = r::text(target, "node_id")?;
            let team = matches!(q.target, ReviewerTarget::Team { .. });
            let selector = r::text(target, if team { "slug" } else { "login" })?;
            self.current()?;
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_review_request_expired"));
            }
            *next = Instant::now() + Duration::from_secs(1);
            attempted = true;
            let response = if q.requested {
                self.post_review_request(pull_node, target_node, team, token)
                    .await?
            } else {
                self.remove_review_request(&reader.name, q.number, selector, team, token)
                    .await?
            };
            let actor = if q.requested {
                let payload = &response.value["data"]["requestReviews"];
                let raw = &payload["pullRequest"];
                if raw["id"] != pull_node
                    || r::big_id(&raw["fullDatabaseId"])? != q.pull_id
                    || raw["number"].as_u64() != Some(q.number)
                    || raw["repository"]["id"] != repo_node
                    || payload["actor"]["id"] != account_node
                {
                    return Err(Failure::protocol());
                }
                let name = r::text(&raw["repository"], "nameWithOwner")?;
                if !super::request::repository_name(name)
                    || raw["url"] != format!("https://github.com/{name}/pull/{}", q.number)
                {
                    return Err(Failure::protocol());
                }
                json!({"external_id":expected,"node_id":account_node,"login":r::nonempty(&payload["actor"],"login")?})
            } else {
                let raw = &response.value;
                if r::id(&raw["id"])? != q.pull_id
                    || raw["node_id"] != pull_node
                    || raw["number"].as_u64() != Some(q.number)
                    || r::id(&raw["base"]["repo"]["id"])? != q.repository
                    || raw["base"]["repo"]["node_id"] != repo_node
                    || raw["url"] != reader.url(&format!("/pulls/{}", q.number))
                {
                    return Err(Failure::protocol());
                }
                Value::Null // REST returns no authenticated mutation actor.
            };
            // Readback can only qualify a causal reply already received above.
            // It never recovers an attribution after a missing/failed reply.
            let after = reader.review_requests(&read).await?;
            if after["pull"] != before["pull"]
                || after["repository_node_id"] != before["repository_node_id"]
                || after["owner_node_id"] != before["owner_node_id"]
                || after["selected_target"]["node_id"] != target_node
                || after["selected_target"]["kind"] != target["kind"]
                || after["selected_target"]["external_id"] != target["external_id"]
                || after["requested"].as_bool() != Some(q.requested)
            {
                return Err(Failure::new(409, "github_review_request_readback_changed"));
            }
            if !q.requested
                && after["selected_target"][if team { "slug" } else { "login" }] != selector
            {
                return Err(Failure::new(409, "github_review_request_name_changed"));
            }
            let remaining = after["items"].as_array().ok_or_else(Failure::protocol)?;
            let by_id: HashMap<_, _> = remaining
                .iter()
                .map(|i| (i["request_id"].as_str().unwrap_or(""), i))
                .collect();
            let mut unrelated = 0;
            for request in before["items"].as_array().ok_or_else(Failure::protocol)? {
                if request["reviewer"]["node_id"] == target_node {
                    continue;
                }
                unrelated += 1;
                let Some(observed) = by_id.get(r::text(request, "request_id")?) else {
                    return Err(Failure::new(
                        409,
                        "github_unrelated_review_requests_changed",
                    ));
                };
                if request["as_code_owner"] != observed["as_code_owner"]
                    || !request["reviewer"].is_null()
                        && (request["reviewer"]["node_id"] != observed["reviewer"]["node_id"]
                            || request["reviewer"]["kind"] != observed["reviewer"]["kind"])
                {
                    return Err(Failure::new(
                        409,
                        "github_unrelated_review_requests_changed",
                    ));
                }
            }
            let receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":repo_node,"pull_id":q.pull_id,"pull_node_id":pull_node,"number":q.number,
                "target":{"kind":target["kind"],"external_id":target["external_id"],"node_id":target_node},"requested":q.requested,"actor":actor,
                "target_selector":if q.requested{"node_id"}else{"name_preflight_and_recheck"},"selector_sha256":super::transport::sha256(selector.as_bytes()),"unrelated_requests_preserved":unrelated,
                "before_sha256":super::transport::sha256(&serde_json::to_vec(&before).map_err(|_|Failure::protocol())?),"after_sha256":super::transport::sha256(&serde_json::to_vec(&after).map_err(|_|Failure::protocol())?),
                "provider_response":{"protocol":if q.requested{"github_graphql_request_review"}else{"github_rest_remove_review_request"},"method":if q.requested{"POST"}else{"DELETE"},"status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
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
