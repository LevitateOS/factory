//! Authenticated reviewer observations. Complete bounded traversal is not an
//! atomic membership snapshot, write permission or attribution of a prior effect.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, Failure, Result,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::{HashMap, HashSet};

const REQUESTS:&str="query FactoryReviewRequests($pull:ID!,$after:String){node(id:$pull){__typename ... on PullRequest{id fullDatabaseId number repository{id} reviewRequests(first:50,after:$after){totalCount pageInfo{hasNextPage endCursor} nodes{id asCodeOwner pullRequest{id} requestedReviewer{__typename ... on Node{id} ... on User{databaseId login} ... on Bot{databaseId login} ... on Team{databaseId name slug organization{id login}}}}}}}}";
const TEAM:&str="query FactoryReviewTeam($target:ID!){node(id:$target){__typename ... on Team{id databaseId name slug organization{id login}}}}";

#[derive(Clone, Deserialize, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum ReviewerTarget {
    User { external_id: String },
    Team { external_id: String },
}
impl ReviewerTarget {
    pub(super) fn validate(&self) -> Result<()> {
        let valid = match self {
            Self::User { external_id } => remote_id(external_id),
            Self::Team { external_id } => {
                node_id(external_id)
                    && external_id.len() <= 512
                    && !external_id.bytes().all(|b| b.is_ascii_digit())
            }
        };
        if !valid {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewerRead {
    pub repository: String,
    pub number: u64,
    pub target: Option<ReviewerTarget>,
}
impl ReviewerRead {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository) || self.number == 0 || self.number > i64::MAX as u64 {
            return Err(Failure::invalid());
        }
        if let Some(target) = &self.target {
            target.validate()?;
        }
        Ok(())
    }
}
pub(super) enum ReviewerQuery {
    Requests { pull: String, after: Option<String> },
    Team { node: String },
}
impl ReviewerQuery {
    pub(super) fn wire(&self) -> (&'static str, Value) {
        match self {
            Self::Requests { pull, after } => (REQUESTS, json!({"pull":pull,"after":after})),
            Self::Team { node } => (TEAM, json!({"target":node})),
        }
    }
}
impl Client {
    pub async fn observe_review_requests(
        &self,
        token: Token,
        expected: &str,
        q: &ReviewerRead,
    ) -> Result<Value> {
        self.observe(
            token,
            expected,
            &super::Read::PullReviewRequests {
                repository: q.repository.clone(),
                number: q.number,
                target: q.target.clone(),
            },
        )
        .await
    }
}
impl Reader<'_> {
    pub(super) async fn review_requests(&self, q: &ReviewerRead) -> Result<Value> {
        let pull = self.raw_subject(true, q.number).await?;
        let pull_id = r::id(&pull["id"])?;
        let pull_node = opaque(&pull, "node_id", 1024)?;
        let repository = self.get("", &[]).await?.value;
        if r::id(&repository["id"])? != q.repository {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let repo_node = opaque(&repository, "node_id", 1024)?;
        let owner_node = opaque(&repository["owner"], "node_id", 1024)?;
        let mut after = None;
        let mut cursors = HashSet::new();
        let mut ids = HashSet::new();
        let mut targets = HashMap::new();
        let mut actor_ids = HashMap::new();
        let mut items = Vec::new();
        let mut total = None;
        let mut bytes = 0;
        let mut visible = true;
        loop {
            let value = self
                .client
                .reviewer_query(
                    ReviewerQuery::Requests {
                        pull: pull_node.into(),
                        after: after.clone(),
                    },
                    self.token,
                )
                .await?;
            let raw = &value["data"]["node"];
            if raw.is_null() {
                return Err(Failure::new(404, "github_not_visible"));
            }
            if raw["__typename"] != "PullRequest"
                || raw["id"] != pull_node
                || r::big_id(&raw["fullDatabaseId"])? != pull_id
                || raw["number"].as_u64() != Some(q.number)
                || raw["repository"]["id"] != repo_node
            {
                return Err(Failure::new(409, "github_review_requests_changed"));
            }
            let connection = &raw["reviewRequests"];
            let count = connection["totalCount"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            if count > 1000 {
                return Err(Failure::new(413, "github_review_requests_too_large"));
            }
            if total.replace(count).is_some_and(|old| old != count) {
                return Err(Failure::new(409, "github_review_requests_changed"));
            }
            let nodes = connection["nodes"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            if nodes.len() > 50 {
                return Err(Failure::protocol());
            }
            for request in nodes {
                let id = opaque(request, "id", 1024)?;
                if !ids.insert(id.to_owned()) || request["pullRequest"]["id"] != pull_node {
                    return Err(Failure::protocol());
                }
                let code_owner = request["asCodeOwner"]
                    .as_bool()
                    .ok_or_else(Failure::protocol)?;
                let reviewer = match request.get("requestedReviewer") {
                    Some(Value::Null) => Value::Null,
                    Some(raw) => project(raw, owner_node)?,
                    None => return Err(Failure::protocol()),
                };
                if let Some(node) = reviewer["node_id"].as_str() {
                    if matches!(reviewer["kind"].as_str(), Some("user" | "bot")) {
                        if let Some(id) = reviewer["external_id"].as_str() {
                            if actor_ids.insert(id.to_owned(), node.to_owned()).is_some() {
                                return Err(Failure::protocol());
                            }
                        }
                    }
                    if targets.insert(node.to_owned(), reviewer.clone()).is_some() {
                        return Err(Failure::protocol());
                    }
                } else {
                    visible = false;
                }
                let item = json!({"request_id":id,"as_code_owner":code_owner,"reviewer":reviewer});
                bytes += serde_json::to_vec(&item)
                    .map_err(|_| Failure::protocol())?
                    .len();
                if items.len() >= 1000 || bytes > 8 * 1024 * 1024 {
                    return Err(Failure::new(413, "github_review_requests_too_large"));
                }
                items.push(item);
            }
            let next = r::graphql_continuation(connection)?;
            if next.is_none() {
                if items.len() as u64 != count {
                    return Err(Failure::new(409, "github_review_requests_changed"));
                }
                break;
            }
            if nodes.is_empty()
                || ids.len() >= 1000
                || !cursors.insert(next.clone().ok_or_else(Failure::protocol)?)
            {
                return Err(Failure::protocol());
            }
            after = next;
        }
        let selected = match &q.target {
            None => Value::Null,
            Some(ReviewerTarget::User { external_id }) => {
                let raw = self
                    .client
                    .get(&format!("/user/{external_id}"), &[], self.token)
                    .await?
                    .value;
                if r::id(&raw["id"])? != *external_id {
                    return Err(Failure::new(409, "github_reviewer_changed"));
                }
                if raw["type"] != "User" {
                    return Err(Failure::new(409, "github_reviewer_kind_changed"));
                }
                json!({"kind":"user","external_id":external_id,"node_id":opaque(&raw,"node_id",1024)?,"login":text(&raw,"login",256)?})
            }
            Some(ReviewerTarget::Team { external_id }) => {
                let value = self
                    .client
                    .reviewer_query(
                        ReviewerQuery::Team {
                            node: external_id.clone(),
                        },
                        self.token,
                    )
                    .await?;
                let raw = &value["data"]["node"];
                if raw.is_null() {
                    return Err(Failure::new(404, "github_not_visible"));
                }
                if raw["__typename"] != "Team" {
                    return Err(Failure::new(409, "github_reviewer_kind_changed"));
                }
                if raw["id"] != *external_id {
                    return Err(Failure::new(409, "github_reviewer_changed"));
                }
                project(raw, owner_node)?
            }
        };
        let requested = selected["node_id"]
            .as_str()
            .map(|node| -> Result<Option<bool>> {
                if let Some(observed) = targets.get(node) {
                    if observed["kind"] != selected["kind"]
                        || selected["kind"] == "user"
                            && !observed["external_id"].is_null()
                            && observed["external_id"] != selected["external_id"]
                    {
                        return Err(Failure::new(409, "github_reviewer_changed"));
                    }
                    Ok(Some(true))
                } else if visible {
                    Ok(Some(false))
                } else {
                    Ok(None)
                }
            })
            .transpose()?
            .flatten();
        let current = self.raw_subject(true, q.number).await?;
        if r::id(&current["id"])? != pull_id || current["node_id"] != pull_node {
            return Err(Failure::new(409, "github_review_requests_changed"));
        }
        let current = self.get("", &[]).await?.value;
        if r::id(&current["id"])? != q.repository
            || current["node_id"] != repo_node
            || current["owner"]["node_id"] != owner_node
        {
            return Err(Failure::new(409, "github_review_requests_changed"));
        }
        self.client.current()?;
        Ok(
            json!({"repository":q.repository,"name":self.name,"repository_node_id":repo_node,"owner_node_id":owner_node,
            "pull":{"kind":"pull","external_id":pull_id,"node_id":pull_node,"number":q.number},"items":items,"all_targets_visible":visible,"selected_target":selected,"requested":requested}),
        )
    }
}
fn opaque<'a>(v: &'a Value, key: &str, bound: usize) -> Result<&'a str> {
    let s = r::text(v, key)?;
    if !node_id(s) || s.len() > bound {
        return Err(Failure::protocol());
    }
    Ok(s)
}
fn text<'a>(v: &'a Value, key: &str, bound: usize) -> Result<&'a str> {
    let s = r::nonempty(v, key)?;
    if s.len() > bound || s.chars().any(char::is_control) {
        return Err(Failure::protocol());
    }
    Ok(s)
}
fn optional_id(v: &Value, key: &str) -> Result<Value> {
    match v.get(key) {
        Some(Value::Null) => Ok(Value::Null),
        Some(id) => Ok(json!(r::id(id)?)),
        None => Err(Failure::protocol()),
    }
}
fn project(v: &Value, owner: &str) -> Result<Value> {
    let kind = text(v, "__typename", 128)?;
    match kind {
        "User" | "Bot" => Ok(
            json!({"kind":if kind=="User"{"user"}else{"bot"},"node_id":opaque(v,"id",1024)?,"external_id":optional_id(v,"databaseId")?,"login":text(v,"login",256)?}),
        ),
        "Team" => {
            let node = opaque(v, "id", 512)?;
            let org = &v["organization"];
            if opaque(org, "id", 1024)? != owner {
                return Err(Failure::new(409, "github_reviewer_scope_changed"));
            }
            Ok(
                json!({"kind":"team","external_id":node,"node_id":node,"database_id":optional_id(v,"databaseId")?,"name":text(v,"name",512)?,"slug":text(v,"slug",512)?,"organization":{"node_id":owner,"login":text(org,"login",256)?}}),
            )
        }
        _ => {
            if !kind.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_') {
                return Err(Failure::protocol());
            }
            let node = match v.get("id") {
                None | Some(Value::Null) => None,
                Some(_) => Some(opaque(v, "id", 1024)?),
            };
            Ok(json!({"kind":"other","type":kind,"node_id":node}))
        }
    }
}
