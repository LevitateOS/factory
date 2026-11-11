//! Current branch facts for an explicitly selected PR base/head pair. Rechecks
//! detect observed changes; they are neither atomic tip conditions nor fork-use,
//! maintainer-edit, creation or execution permission.
use super::{
    reads::Reader,
    records as r,
    request::{branch_name, commit, node_id, remote_id},
    Client, Failure, Read, Result,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::HashMap;

pub(super) const QUERY:&str="query FactoryPullBranches($base:ID!,$head:ID!,$baseRef:String!,$headRef:String!){base:node(id:$base){__typename ... on Repository{id nameWithOwner owner{id login} branch:ref(qualifiedName:$baseRef){id name prefix repository{id} target{__typename oid repository{id}}}}} head:node(id:$head){__typename ... on Repository{id nameWithOwner owner{id login} branch:ref(qualifiedName:$headRef){id name prefix repository{id} target{__typename oid repository{id}}}}}}";

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullBranchRead {
    pub repository: String,
    pub head_repository: String,
    pub base_branch: String,
    pub head_branch: String,
}
impl PullBranchRead {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.head_repository)
            || !branch_name(&self.base_branch)
            || !branch_name(&self.head_branch)
            || self.repository == self.head_repository && self.base_branch == self.head_branch
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}
impl Client {
    pub async fn observe_pull_branches(
        &self,
        token: Token,
        expected: &str,
        q: &PullBranchRead,
    ) -> Result<Value> {
        self.observe(
            token,
            expected,
            &Read::PullBranches {
                repository: q.repository.clone(),
                head_repository: q.head_repository.clone(),
                base_branch: q.base_branch.clone(),
                head_branch: q.head_branch.clone(),
            },
        )
        .await
    }
}
impl Reader<'_> {
    pub(super) async fn pull_branches(&self, q: &PullBranchRead) -> Result<Value> {
        let head = Reader::new(self.client, self.token, &q.head_repository).await?;
        let base_repo = self.branch_repository().await?;
        let head_repo = head.branch_repository().await?;
        if q.repository == q.head_repository && base_repo != head_repo {
            return Err(Failure::new(409, "github_branch_pair_changed"));
        }
        let mut ids = HashMap::new();
        let mut nodes = HashMap::new();
        for repository in [&base_repo, &head_repo] {
            for fact in [
                repository,
                &repository["parent_repository"],
                &repository["source_repository"],
            ]
            .into_iter()
            .filter(|v| !v.is_null())
            {
                let id = r::text(fact, "external_id")?;
                let node = r::text(fact, "node_id")?;
                if ids.insert(id, node).is_some_and(|old| old != node)
                    || nodes.insert(node, id).is_some_and(|old| old != id)
                {
                    return Err(Failure::new(409, "github_branch_repository_changed"));
                }
            }
        }
        let observe = async || -> Result<Value> {
            let value = self
                .client
                .pull_branch_query(
                    &base_repo,
                    &head_repo,
                    &q.base_branch,
                    &q.head_branch,
                    self.token,
                )
                .await?;
            Ok(
                json!({"base":{"repository":base_repo,"branch":project(&value["data"]["base"],&base_repo,&q.base_branch)?},"head":{"repository":head_repo,"branch":project(&value["data"]["head"],&head_repo,&q.head_branch)?}}),
            )
        };
        let before = observe().await?;
        if self.branch_repository().await? != base_repo
            || head.branch_repository().await? != head_repo
        {
            return Err(Failure::new(409, "github_branch_pair_changed"));
        }
        if observe().await? != before {
            return Err(Failure::new(409, "github_branch_pair_changed"));
        }
        self.client.current()?;
        Ok(before)
    }
    async fn branch_repository(&self) -> Result<Value> {
        let raw = self.get("", &[]).await?.value;
        if r::id(&raw["id"])? != self.repository || r::text(&raw, "full_name")? != self.name {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let owner = &raw["owner"];
        if !matches!(owner["type"].as_str(), Some("User" | "Organization")) {
            return Err(Failure::protocol());
        }
        let related = |key: &str| -> Result<Value> {
            let value = &raw[key];
            if value.is_null() {
                return Ok(Value::Null);
            }
            Ok(json!({"external_id":r::id(&value["id"])?,"node_id":opaque(value,"node_id")?}))
        };
        Ok(
            json!({"external_id":self.repository,"node_id":opaque(&raw,"node_id")?,"name":self.name,
            "owner":{"external_id":r::id(&owner["id"])?,"node_id":opaque(owner,"node_id")?,"login":label(owner,"login",256)?,"type":owner["type"]},
            "private":r::boolean(&raw,"private")?,"archived":r::boolean(&raw,"archived")?,"disabled":r::boolean(&raw,"disabled")?,"fork":r::boolean(&raw,"fork")?,"push":r::boolean(&raw["permissions"],"push")?,
            "parent_repository":related("parent")?,"source_repository":related("source")?}),
        )
    }
}
fn opaque<'a>(raw: &'a Value, key: &str) -> Result<&'a str> {
    let value = r::text(raw, key)?;
    if !node_id(value) {
        return Err(Failure::protocol());
    }
    Ok(value)
}
fn label<'a>(raw: &'a Value, key: &str, bound: usize) -> Result<&'a str> {
    let value = r::nonempty(raw, key)?;
    if value.len() > bound || value.chars().any(char::is_control) {
        return Err(Failure::protocol());
    }
    Ok(value)
}
fn project(raw: &Value, repository: &Value, branch: &str) -> Result<Value> {
    if raw.is_null() {
        return Err(Failure::new(404, "github_repository_not_visible"));
    }
    if raw["__typename"] != "Repository"
        || raw["id"] != repository["node_id"]
        || raw["nameWithOwner"] != repository["name"]
        || raw["owner"]["id"] != repository["owner"]["node_id"]
        || raw["owner"]["login"] != repository["owner"]["login"]
    {
        return Err(Failure::new(409, "github_branch_repository_changed"));
    }
    let reference = match raw.get("branch") {
        Some(Value::Null) => return Err(Failure::new(404, "github_branch_not_visible")),
        Some(value) => value,
        None => return Err(Failure::protocol()),
    };
    if reference["name"] != branch
        || reference["prefix"] != "refs/heads/"
        || reference["repository"]["id"] != repository["node_id"]
    {
        return Err(Failure::new(409, "github_branch_scope_changed"));
    }
    let target = reference.get("target").ok_or_else(Failure::protocol)?;
    if target.is_null() {
        return Err(Failure::new(409, "github_branch_target_unobserved"));
    }
    if target["__typename"] != "Commit"
        || target["repository"]["id"] != repository["node_id"]
        || !r::text(target, "oid").is_ok_and(commit)
    {
        return Err(Failure::protocol());
    }
    Ok(json!({"name":branch,"node_id":opaque(reference,"id")?,"commit":target["oid"]}))
}

impl Client {
    pub(super) async fn pull_branch_query(
        &self,
        base: &serde_json::Value,
        head: &serde_json::Value,
        base_branch: &str,
        head_branch: &str,
        token: &[u8],
    ) -> super::Result<serde_json::Value> {
        let base: String = super::records::text(base, "node_id")?.into();
        let head: String = super::records::text(head, "node_id")?.into();
        let base_ref: String = format!("refs/heads/{base_branch}");
        let head_ref: String = format!("refs/heads/{head_branch}");
        let token: zeroize::Zeroizing<Vec<u8>> = zeroize::Zeroizing::new(token.to_vec());
        Ok(self.provider_request(move |http| Ok({
            if !super::request::node_id(&base)
                || !super::request::node_id(&head)
                || !base_ref
                    .strip_prefix("refs/heads/")
                    .is_some_and(super::request::branch_name)
                || !head_ref
                    .strip_prefix("refs/heads/")
                    .is_some_and(super::request::branch_name)
            {
                return Err(Failure::invalid());
            }
            (
                super::transport::graphql(
                    http,
                    &token,
                    QUERY,
                    serde_json::json!({"base":base,"head":head,"baseRef":base_ref,"headRef":head_ref}),
                )?,
                1024 * 1024,
            )
        }), None).await?.value)
    }
}
