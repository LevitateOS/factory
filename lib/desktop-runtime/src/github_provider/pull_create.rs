//! One PR creation attempt over saved branch pins. Preflight and a matching
//! causal reply are not an atomic tip/repository condition or remote deduplication.
//! The Go command owner must reserve its durable one-shot claim before each call.
use super::{
    reads::Reader,
    records as r,
    request::{branch_name, commit, node_id, remote_id, repository_name},
    Client, CreateOutcome, Failure, PullBranchRead,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::time::{Duration, Instant};

pub(super) const CREATE:&str="mutation FactoryCreatePull($input:CreatePullRequestInput!){createPullRequest(input:$input){pullRequest{id fullDatabaseId number title body url state isDraft maintainerCanModify createdAt updatedAt repository{id} author{__typename login ... on User{id}} baseRefName baseRefOid headRefName headRefOid baseRepository{id nameWithOwner owner{id login}} headRepository{id nameWithOwner owner{id login}} baseRef{id name prefix repository{id}} headRef{id name prefix repository{id}}}}}";

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullBranchPin {
    pub repository_node_id: String,
    pub owner_node_id: String,
    pub reference_node_id: String,
    pub commit: String,
}
impl PullBranchPin {
    fn valid(&self) -> bool {
        node_id(&self.repository_node_id)
            && node_id(&self.owner_node_id)
            && node_id(&self.reference_node_id)
            && commit(&self.commit)
    }
    fn matches(&self, side: &Value) -> bool {
        side["repository"]["node_id"] == self.repository_node_id
            && side["repository"]["owner"]["node_id"] == self.owner_node_id
            && side["branch"]["node_id"] == self.reference_node_id
            && side["branch"]["commit"] == self.commit
    }
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullCreate {
    pub repository: String,
    pub head_repository: String,
    pub base_branch: String,
    pub head_branch: String,
    pub base_pin: PullBranchPin,
    pub head_pin: PullBranchPin,
    pub title: String,
    pub body: String,
    pub allow_maintainer_edit: bool,
}
impl PullCreate {
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || !remote_id(&self.head_repository)
            || !branch_name(&self.base_branch)
            || !branch_name(&self.head_branch)
            || self.repository == self.head_repository && self.base_branch == self.head_branch
            || !self.base_pin.valid()
            || !self.head_pin.valid()
            || self.base_pin.reference_node_id == self.head_pin.reference_node_id
            || (self.repository == self.head_repository)
                != (self.base_pin.repository_node_id == self.head_pin.repository_node_id)
            || self.repository == self.head_repository
                && self.base_pin.owner_node_id != self.head_pin.owner_node_id
            || self.title.is_empty()
            || self.title.trim() != self.title
            || self.title.len() > 255
            || self.title.contains('\0')
            || self.body.len() > 65536
            || self.body.contains('\0')
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        // These literal branch names can be ambiguous with the provider's ref
        // qualification rules. Never silently reinterpret a saved selector.
        if self.base_branch.starts_with("refs/") || self.head_branch.starts_with("refs/") {
            return Err(Failure::new(
                409,
                "github_pull_branch_qualification_unsupported",
            ));
        }
        Ok(())
    }
}
impl Client {
    pub async fn create_pull(&self, token: Token, expected: &str, q: &PullCreate) -> CreateOutcome {
        self.pull_create_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn pull_create_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &PullCreate,
        deadline: Option<Instant>,
    ) -> CreateOutcome {
        let mut attempted = false;
        let until = deadline
            .unwrap_or_else(|| Instant::now() + Duration::from_secs(8))
            .min(Instant::now() + Duration::from_secs(8));
        let work = async {
            q.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_pull_creation_expired"));
            }
            self.current()?;
            let mut next = self.mutations.lock().await;
            self.delay(next.saturating_duration_since(Instant::now()))
                .await?;
            let identity = self.identity(token).await?;
            if identity.external_id != expected {
                return Err(Failure::new(409, "github_account_changed"));
            }
            let actor_node = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let observed = reader
                .pull_branches(&PullBranchRead {
                    repository: q.repository.clone(),
                    head_repository: q.head_repository.clone(),
                    base_branch: q.base_branch.clone(),
                    head_branch: q.head_branch.clone(),
                })
                .await?;
            let base = &observed["base"];
            let head = &observed["head"];
            if !q.base_pin.matches(base) || !q.head_pin.matches(head) {
                return Err(Failure::new(409, "github_pull_inputs_changed"));
            }
            for side in [base, head] {
                if side["repository"]["archived"].as_bool() != Some(false)
                    || side["repository"]["disabled"].as_bool() != Some(false)
                {
                    return Err(Failure::new(403, "github_repository_read_only"));
                }
            }
            if head["repository"]["push"].as_bool() != Some(true) {
                return Err(Failure::new(403, "github_pull_head_write_unavailable"));
            }
            if q.repository != q.head_repository
                && network_root(&base["repository"])? != network_root(&head["repository"])?
            {
                return Err(Failure::new(409, "github_pull_network_changed"));
            }
            if q.base_pin.commit == q.head_pin.commit {
                return Err(Failure::new(409, "github_pull_no_difference"));
            }
            if q.allow_maintainer_edit
                && (q.repository == q.head_repository
                    || head["repository"]["fork"] != true
                    || head["repository"]["owner"]["type"] != "User"
                    || head["repository"]["owner"]["external_id"] != expected
                    || head["repository"]["owner"]["node_id"] != actor_node)
            {
                return Err(Failure::new(403, "github_maintainer_edit_unavailable"));
            }
            let head_owner = r::text(&head["repository"]["owner"], "login")?;
            let head_name = r::text(&head["repository"], "name")?;
            if !head_name
                .split_once('/')
                .is_some_and(|(owner, _)| owner.eq_ignore_ascii_case(head_owner))
            {
                return Err(Failure::new(409, "github_pull_head_qualifier_changed"));
            }
            let head_ref = if q.repository == q.head_repository {
                q.head_branch.clone()
            } else {
                format!("{head_owner}:{}", q.head_branch)
            };
            self.current()?;
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_pull_creation_expired"));
            }
            *next = Instant::now() + Duration::from_secs(1);
            attempted = true;
            let response = self.post_pull(q, &head_ref, token).await?;
            let raw = &response.value["data"]["createPullRequest"]["pullRequest"];
            let id = r::big_id(&raw["fullDatabaseId"])?;
            let number = raw["number"]
                .as_u64()
                .filter(|n| *n > 0 && *n <= i64::MAX as u64)
                .ok_or_else(Failure::protocol)?;
            if !r::text(raw, "id").is_ok_and(node_id)
                || raw["repository"]["id"] != q.base_pin.repository_node_id
                || raw["author"]["__typename"] != "User"
                || raw["author"]["id"] != actor_node
                || raw["title"] != q.title
                || raw["body"] != q.body
                || raw["state"] != "OPEN"
                || raw["isDraft"] != false
                || raw["maintainerCanModify"].as_bool() != Some(q.allow_maintainer_edit)
            {
                return Err(Failure::protocol());
            }
            verify_side(raw, "base", &q.base_branch, &q.base_pin)?;
            verify_side(raw, "head", &q.head_branch, &q.head_pin)?;
            let name = r::text(&raw["baseRepository"], "nameWithOwner")?;
            if !repository_name(name)
                || raw["url"] != format!("https://github.com/{name}/pull/{number}")
            {
                return Err(Failure::protocol());
            }
            let dates = json!({"created_at":raw["createdAt"],"updated_at":raw["updatedAt"]});
            let receipt = json!({"account":identity,"repository":q.repository,"head_repository":q.head_repository,
                "base":{"branch":q.base_branch,"pin":q.base_pin},"head":{"branch":q.head_branch,"pin":q.head_pin},"branch_condition":"preflight_and_causal_reply",
                "pull":{"external_id":id,"node_id":raw["id"],"number":number,"url":raw["url"],"author":{"external_id":expected,"node_id":actor_node,"login":r::nonempty(&raw["author"],"login")?},"state":"open","draft":false,"allow_maintainer_edit":q.allow_maintainer_edit,
                    "title_sha256":super::transport::sha256(q.title.as_bytes()),"body_sha256":super::transport::sha256(q.body.as_bytes()),"created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?},
                "provider_response":{"protocol":"github_graphql_create_pull","status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
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
            .and_then(|v| v);
        match result {
            Ok(v) => CreateOutcome::Created(v),
            Err(f) if attempted => CreateOutcome::Indeterminate(f),
            Err(f) => CreateOutcome::NotSent(f),
        }
    }
}
fn network_root(repository: &Value) -> Result<Value, Failure> {
    match repository["fork"].as_bool() {
        Some(false) => {
            Ok(json!({"external_id":repository["external_id"],"node_id":repository["node_id"]}))
        }
        Some(true) if repository["source_repository"].is_object() => {
            Ok(repository["source_repository"].clone())
        }
        _ => Err(Failure::new(409, "github_pull_network_unobserved")),
    }
}
fn verify_side(raw: &Value, side: &str, branch: &str, pin: &PullBranchPin) -> Result<(), Failure> {
    let repository = &raw[format!("{side}Repository")];
    let reference = &raw[format!("{side}Ref")];
    if repository["id"] != pin.repository_node_id
        || repository["owner"]["id"] != pin.owner_node_id
        || raw[format!("{side}RefName")] != branch
        || raw[format!("{side}RefOid")] != pin.commit
        || reference["id"] != pin.reference_node_id
        || reference["name"] != branch
        || reference["prefix"] != "refs/heads/"
        || reference["repository"]["id"] != pin.repository_node_id
    {
        return Err(Failure::protocol());
    }
    let name = r::text(repository, "nameWithOwner")?;
    if !repository_name(name)
        || !name.split_once('/').is_some_and(|(owner, _)| {
            repository["owner"]["login"]
                .as_str()
                .is_some_and(|login| owner.eq_ignore_ascii_case(login))
        })
    {
        return Err(Failure::protocol());
    }
    Ok(())
}
