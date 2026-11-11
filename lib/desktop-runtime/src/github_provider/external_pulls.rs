//! Current provider facts for external PR intake. Product eligibility and
//! canonical issue association remain with the Go consumer.
use super::{
    reads::Reader,
    records as r,
    request::{branch_name, commit, node_id},
    Failure, Identity, Result,
};
use serde_json::{json, Value};
use std::collections::{HashMap, HashSet};

const FIELDS: &str = "id fullDatabaseId number title state isDraft headRefOid baseRefOid headRefName baseRefName repository{id databaseId} headRepository{id databaseId} baseRepository{id databaseId} author{login ... on Node{id}} closingIssuesReferences(first:100){totalCount pageInfo{hasNextPage endCursor} nodes{id fullDatabaseId number repository{id databaseId}}}";

impl Reader<'_> {
    pub(super) async fn external_pull_candidates(
        &self,
        number: u32,
        cursor: &str,
        actor: &Identity,
    ) -> Result<Value> {
        let actor_node = actor
            .node_id
            .as_deref()
            .filter(|id| node_id(id))
            .ok_or_else(Failure::protocol)?;
        let (owner, name) = self.name.split_once('/').ok_or_else(Failure::protocol)?;
        let (declarations, selection) = if number == 0 {
            (",$after:String", format!("pulls:pullRequests(states:OPEN,first:16,after:$after,orderBy:{{field:CREATED_AT,direction:ASC}}){{totalCount pageInfo{{hasNextPage endCursor}} nodes{{{FIELDS}}}}}"))
        } else {
            (
                ",$number:Int!",
                format!("pull:pullRequest(number:$number){{{FIELDS}}}"),
            )
        };
        let query = format!("query FactoryExternalPullCandidates($owner:String!,$name:String!{declarations}){{viewer{{id login}} repository(owner:$owner,name:$name){{id databaseId nameWithOwner viewerPermission {selection}}}}}");
        let mut variables = json!({"owner":owner,"name":name});
        if number == 0 {
            variables["after"] = if cursor.is_empty() {
                Value::Null
            } else {
                json!(cursor)
            };
        } else {
            variables["number"] = json!(number);
        }
        let confirm_query = query.clone();
        let confirm_variables = variables.clone();
        let token = zeroize::Zeroizing::new(self.token.to_vec());
        let response = self
            .client
            .provider_request(
                move |http| {
                    Ok((
                        super::transport::graphql(http, &token, &query, variables)?,
                        1024 * 1024,
                    ))
                },
                None,
            )
            .await?;
        let data = &response.value["data"];
        if data["viewer"]["id"].as_str() != Some(actor_node)
            || data["viewer"]["login"] != actor.login
        {
            return Err(Failure::new(409, "github_account_changed"));
        }
        let repository = &data["repository"];
        let repository_node = r::text(repository, "id")?;
        if !node_id(repository_node)
            || r::big_id(&repository["databaseId"])? != self.repository
            || repository["nameWithOwner"] != self.name
        {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let writer = permission(&repository["viewerPermission"])?;
        let (pulls, next) = if number == 0 {
            let connection = &repository["pulls"];
            let nodes = connection["nodes"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            let total = connection["totalCount"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            let next = r::graphql_continuation(connection)?;
            if nodes.len() > 16
                || nodes.len() as u64 > total
                || next.is_some() && nodes.is_empty()
                || cursor.is_empty() && next.is_none() && nodes.len() as u64 != total
                || next.as_deref() == Some(cursor)
            {
                return Err(Failure::protocol());
            }
            (nodes.clone(), next)
        } else {
            let pull = &repository["pull"];
            if pull.is_null() {
                return Err(Failure::new(404, "github_not_visible"));
            }
            (vec![pull.clone()], None)
        };
        let mut authors = HashMap::new();
        let mut numbers = HashSet::new();
        let mut ids = HashSet::new();
        let mut items = Vec::with_capacity(pulls.len());
        for pull in pulls {
            let observed_number = pull["number"]
                .as_u64()
                .filter(|n| (1..=i32::MAX as u64).contains(n))
                .ok_or_else(Failure::protocol)?;
            let pull_node = r::text(&pull, "id")?;
            let pull_id = r::big_id(&pull["fullDatabaseId"])?;
            let head = r::text(&pull, "headRefOid")?;
            let base = r::text(&pull, "baseRefOid")?;
            let head_ref = r::text(&pull, "headRefName")?;
            let base_ref = r::text(&pull, "baseRefName")?;
            let state = r::text(&pull, "state")?;
            if !node_id(pull_node)
                || !commit(head)
                || !commit(base)
                || !branch_name(head_ref)
                || !branch_name(base_ref)
                || number != 0 && observed_number != u64::from(number)
                || !matches!(state, "OPEN" | "CLOSED" | "MERGED")
                || number == 0 && state != "OPEN"
                || !numbers.insert(observed_number)
                || !ids.insert(pull_id.clone())
                || pull["repository"]["id"] != repository_node
                || r::big_id(&pull["repository"]["databaseId"])? != self.repository
            {
                return Err(Failure::protocol());
            }
            let head_repository = repository_id(&pull["headRepository"])?;
            let base_repository = repository_id(&pull["baseRepository"])?;
            if base_repository != self.repository || pull["baseRepository"]["id"] != repository_node
            {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            if head_repository == self.repository && pull["headRepository"]["id"] != repository_node
            {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            let (author, author_writer) =
                self.external_author(&pull["author"], &mut authors).await?;
            let (closing_issues, closing_issues_complete) =
                self.external_closing_issues(&pull["closingIssuesReferences"], repository_node)?;
            items.push(json!({"number":observed_number,"external_id":pull_id,"node_id":pull_node,
                "repository":self.repository,"head_repository":head_repository,"base_repository":base_repository,
                "head_tip":head,"base_tip":base,"head_ref":format!("refs/heads/{head_ref}"),"base_ref":format!("refs/heads/{base_ref}"),
                "title":r::text(&pull,"title")?,"state":state.to_ascii_lowercase(),"draft":pull["isDraft"].as_bool().ok_or_else(Failure::protocol)?,
                "author":author,"author_writer":author_writer,"closing_issues":closing_issues,"closing_issues_complete":closing_issues_complete}));
        }
        let token = zeroize::Zeroizing::new(self.token.to_vec());
        let confirmed = self
            .client
            .provider_request(
                move |http| {
                    Ok((
                        super::transport::graphql(http, &token, &confirm_query, confirm_variables)?,
                        1024 * 1024,
                    ))
                },
                None,
            )
            .await?;
        if &confirmed.value["data"] != data {
            return Err(Failure::new(409, "github_pull_candidates_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"repository_node_id":repository_node,"name":self.name,
            "actor":{"external_id":actor.external_id,"login":actor.login},"actor_node_id":actor_node,"writer":writer,
            "items":items,"complete":next.is_none(),"next_cursor":next.unwrap_or_default()}),
        )
    }

    async fn external_author(
        &self,
        author: &Value,
        cache: &mut HashMap<String, (Value, bool)>,
    ) -> Result<(Value, bool)> {
        if author.is_null() {
            return Ok((Value::Null, false));
        }
        let id = r::text(author, "id")?;
        let login = r::text(author, "login")?;
        if !node_id(id)
            || login.is_empty()
            || login.len() > 100
            || !login
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'[' | b']'))
        {
            return Err(Failure::protocol());
        }
        if let Some(observed) = cache.get(id) {
            if observed.0["login"] != login {
                return Err(Failure::new(409, "github_pull_author_changed"));
            }
            return Ok(observed.clone());
        }
        let raw = self
            .get(&format!("/collaborators/{login}/permission"), &[])
            .await?
            .value;
        if raw["user"]["node_id"] != id || raw["user"]["login"] != login {
            return Err(Failure::new(409, "github_pull_author_changed"));
        }
        let writer = match r::text(&raw, "permission")? {
            "admin" | "write" => true,
            "read" | "none" => false,
            _ => return Err(Failure::new(409, "github_creator_permission_unknown")),
        };
        let observed = (r::actor(&raw["user"])?, writer);
        cache.insert(id.to_owned(), observed.clone());
        Ok(observed)
    }

    fn external_closing_issues(
        &self,
        connection: &Value,
        repository_node: &str,
    ) -> Result<(Vec<Value>, bool)> {
        if connection.is_null() {
            return Ok((Vec::new(), false));
        }
        let nodes = connection["nodes"]
            .as_array()
            .ok_or_else(Failure::protocol)?;
        let total = connection["totalCount"]
            .as_u64()
            .ok_or_else(Failure::protocol)?;
        let mut complete =
            r::graphql_continuation(connection)?.is_none() && nodes.len() as u64 == total;
        if nodes.len() > 100 || nodes.len() as u64 > total {
            return Err(Failure::protocol());
        }
        let mut items = Vec::new();
        let mut ids = HashSet::new();
        for issue in nodes {
            let id = r::big_id(&issue["fullDatabaseId"])?;
            let number = issue["number"]
                .as_u64()
                .filter(|n| *n > 0)
                .ok_or_else(Failure::protocol)?;
            if !r::text(issue, "id").is_ok_and(node_id) || !ids.insert(id.clone()) {
                return Err(Failure::protocol());
            }
            if repository_id(&issue["repository"])? != self.repository
                || issue["repository"]["id"] != repository_node
            {
                complete = false;
                continue;
            }
            items.push(json!({"repository":self.repository,"external_id":id,"node_id":issue["id"],"number":number}));
        }
        Ok((items, complete))
    }
}

fn repository_id(repository: &Value) -> Result<String> {
    if repository.is_null() {
        return Ok(String::new());
    }
    if !r::text(repository, "id").is_ok_and(node_id) {
        return Err(Failure::protocol());
    }
    r::big_id(&repository["databaseId"])
}

fn permission(raw: &Value) -> Result<bool> {
    match raw.as_str() {
        Some("ADMIN" | "MAINTAIN" | "WRITE") => Ok(true),
        Some("TRIAGE" | "READ") => Ok(false),
        None if raw.is_null() => Ok(false),
        _ => Err(Failure::protocol()),
    }
}
