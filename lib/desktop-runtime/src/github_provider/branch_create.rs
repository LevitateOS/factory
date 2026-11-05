//! One new branch at an exact immutable commit. No update, force, deletion or
//! compensation. The product owner must commit its one-shot fence before send.
use super::{
    reads::Reader,
    records as r,
    request::{branch_name, commit, node_id, remote_id},
    Client, CreateOutcome, Failure, Result,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::time::{Duration, Instant};

pub(super) const QUERY:&str="query FactoryBranchCreation($repository:ID!,$name:String!,$oid:GitObjectID!){node(id:$repository){__typename ... on Repository{id nameWithOwner branch:ref(qualifiedName:$name){id name prefix repository{id} target{__typename oid repository{id}}} commit:object(oid:$oid){__typename oid repository{id}}}}}";
pub(super) const CREATE:&str="mutation FactoryCreateBranch($input:CreateRefInput!){createRef(input:$input){ref{id name prefix repository{id} target{__typename oid repository{id}}}}}";

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BranchCreate {
    pub repository: String,
    pub repository_node_id: String,
    /// An unqualified literal branch name; the transport always supplies refs/heads/.
    pub name: String,
    pub commit: String,
}
impl BranchCreate {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !node_id(&self.repository_node_id)
            || !branch_name(&self.name)
            || !commit(&self.commit)
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}
impl Reader<'_> {
    async fn branch_create_repository(&self) -> Result<Value> {
        let raw = self.get("", &[]).await?.value;
        if r::id(&raw["id"])? != self.repository || raw["full_name"] != self.name {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let node = r::text(&raw, "node_id")?;
        if !node_id(node) {
            return Err(Failure::protocol());
        }
        Ok(
            json!({"repository":self.repository,"name":self.name,"repository_node_id":node,"archived":r::boolean(&raw,"archived")?,"disabled":r::boolean(&raw,"disabled")?,"push":r::boolean(&raw["permissions"],"push")?}),
        )
    }
    pub(super) async fn branch_creation(
        &self,
        name: &str,
        oid: &str,
        account: &str,
        actor: &str,
    ) -> Result<Value> {
        let before = self.branch_create_repository().await?;
        let repository = r::text(&before, "repository_node_id")?;
        let observe = async || -> Result<Value> {
            let v = self
                .client
                .branch_creation_query(repository, name, oid, self.token)
                .await?;
            let raw = &v["data"]["node"];
            if raw.is_null() {
                return Err(Failure::new(404, "github_repository_not_visible"));
            }
            if raw["__typename"] != "Repository"
                || raw["id"] != repository
                || raw["nameWithOwner"] != self.name
            {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            if raw.get("commit").is_none() {
                return Err(Failure::protocol());
            }
            if raw["commit"].is_null() {
                return Err(Failure::new(404, "github_branch_commit_not_visible"));
            }
            if raw["commit"]["__typename"] != "Commit"
                || raw["commit"]["oid"] != oid
                || raw["commit"]["repository"]["id"] != repository
            {
                return Err(Failure::protocol());
            }
            let branch = match raw.get("branch") {
                Some(Value::Null) => Value::Null,
                Some(raw) => project_branch(raw, repository, name, None)?,
                None => return Err(Failure::protocol()),
            };
            Ok(branch)
        };
        let branch = observe().await?;
        if self.branch_create_repository().await? != before || observe().await? != branch {
            return Err(Failure::new(409, "github_branch_creation_inputs_changed"));
        }
        // Numeric lookup independently detects a name that was transferred/reused.
        if self.client.repository(self.repository, self.token).await?["name"] != self.name {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let current = self.client.identity(self.token).await?;
        if current.external_id != account || current.node_id.as_deref() != Some(actor) {
            return Err(Failure::new(409, "github_account_changed"));
        }
        let mut result = before;
        result["requested_branch"] = json!(name);
        result["requested_commit"] = json!(oid);
        result["branch"] = branch;
        result["condition"] = json!("bounded_observation");
        Ok(result)
    }
}
fn project_branch(raw: &Value, repository: &str, name: &str, oid: Option<&str>) -> Result<Value> {
    let id = r::text(raw, "id")?;
    if !node_id(id)
        || raw["name"] != name
        || raw["prefix"] != "refs/heads/"
        || raw["repository"]["id"] != repository
        || raw["target"]["__typename"] != "Commit"
        || !r::text(&raw["target"], "oid").is_ok_and(commit)
        || raw["target"]["repository"]["id"] != repository
        || oid.is_some_and(|oid| raw["target"]["oid"] != oid)
    {
        return Err(Failure::protocol());
    }
    Ok(json!({"name":name,"node_id":id,"commit":raw["target"]["oid"]}))
}
impl Client {
    pub async fn create_branch(
        &self,
        token: Token,
        expected: &str,
        q: &BranchCreate,
    ) -> CreateOutcome {
        self.branch_create_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn branch_create_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &BranchCreate,
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
            self.current()?;
            let mut next = self.mutations.lock().await;
            self.delay(next.saturating_duration_since(Instant::now()))
                .await?;
            let identity = self.identity(token).await?;
            if identity.external_id != expected {
                return Err(Failure::new(409, "github_account_changed"));
            }
            let actor = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let observed = reader
                .branch_creation(&q.name, &q.commit, expected, actor)
                .await?;
            if observed["repository_node_id"] != q.repository_node_id {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            if !observed["branch"].is_null() {
                return Err(Failure::new(409, "github_branch_exists"));
            }
            if observed["archived"].as_bool() != Some(false)
                || observed["disabled"].as_bool() != Some(false)
                || observed["push"].as_bool() != Some(true)
            {
                return Err(Failure::new(403, "github_branch_write_unavailable"));
            }
            self.current()?;
            if Instant::now() >= until {
                return Err(Failure::new(409, "github_branch_creation_expired"));
            }
            *next = Instant::now() + Duration::from_secs(1);
            attempted = true;
            let response = self.post_branch(q, token).await?;
            let branch = project_branch(
                &response.value["data"]["createRef"]["ref"],
                &q.repository_node_id,
                &q.name,
                Some(&q.commit),
            )?;
            let receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":q.repository_node_id,"branch":branch,"creation_condition":"new_ref_at_exact_commit","provider_response":{"protocol":"github_graphql_create_ref","status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
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
