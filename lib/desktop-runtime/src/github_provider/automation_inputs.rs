//! Complete bounded issue inputs for acceptance and dispatch. Provider history
//! IDs stay opaque here; Go owns their canonical registry and revision numbers.
use super::{reads::Reader, records as r, Client, Failure, Result};
use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet};
use zeroize::Zeroizing;

macro_rules! issue_fields { () => { r#"id databaseId number title body createdAt closedAt closed locked lastEditedAt
author{__typename login ... on User{databaseId} ... on Bot{databaseId}}
userContentEdits(first:1){totalCount}
timelineItems(first:100,itemTypes:[RENAMED_TITLE_EVENT,CLOSED_EVENT,REOPENED_EVENT,BLOCKED_BY_ADDED_EVENT,BLOCKED_BY_REMOVED_EVENT]){pageInfo{hasNextPage} nodes{
__typename ... on RenamedTitleEvent{id currentTitle} ... on ClosedEvent{id} ... on ReopenedEvent{id}
... on BlockedByAddedEvent{id blockingIssue{databaseId}} ... on BlockedByRemovedEvent{id blockingIssue{databaseId}}}}
blockedBy(first:50){totalCount pageInfo{hasNextPage} nodes{databaseId number repository{databaseId}}}"# }; }
const QUERY: &str = concat!(r#"query FactoryIssueInputs($owner:String!,$name:String!,$number:Int!,$ref:String!,$comments:[ID!]!){
repository(owner:$owner,name:$name){databaseId nameWithOwner
issue(number:$number){"#, issue_fields!(), r#"}
ref(qualifiedName:$ref){name prefix target{__typename oid}}}
nodes(ids:$comments){__typename ... on IssueComment{id databaseId body lastEditedAt isMinimized issue{id} userContentEdits(first:1){totalCount}}}}
"#);
const INVENTORY_QUERY: &str=concat!(r#"query FactoryCompletionIssues($owner:String!,$name:String!,$ref:String!,$after:String,$limit:Int!){repository(owner:$owner,name:$name){databaseId nameWithOwner issues(first:$limit,after:$after,states:OPEN,orderBy:{field:CREATED_AT,direction:ASC}){pageInfo{hasNextPage endCursor} nodes{"#,issue_fields!(),r#"}} ref(qualifiedName:$ref){name prefix target{__typename oid}}}}"#);

fn opaque(value: &Value, field: &str) -> Result<String> {
    r::text(value, field).and_then(|s| {
        if super::request::node_id(s) {
            Ok(s.into())
        } else {
            Err(Failure::protocol())
        }
    })
}
fn count(value: &Value) -> Result<u64> {
    value
        .as_u64()
        .filter(|n| *n <= i32::MAX as u64)
        .ok_or_else(Failure::protocol)
}
fn complete_nodes(value: &Value, max: usize) -> Result<&Vec<Value>> {
    if value["pageInfo"]["hasNextPage"] != false {
        return Err(Failure::new(409, "github_issue_inputs_incomplete"));
    }
    value["nodes"]
        .as_array()
        .filter(|v| v.len() <= max)
        .ok_or_else(Failure::protocol)
}

impl Reader<'_> {
    pub(super) async fn automation_inputs(
        &self,
        number: u32,
        comments: &[String],
        target: &str,
    ) -> Result<Value> {
        let mut comment_nodes = Vec::with_capacity(comments.len());
        for id in comments {
            let value = self
                .get(&format!("/issues/comments/{id}"), &[])
                .await?
                .value;
            if r::id(&value["id"])? != *id {
                return Err(Failure::protocol());
            }
            comment_nodes.push(opaque(&value, "node_id")?);
        }
        let (owner, name) = self.name.split_once('/').ok_or_else(Failure::protocol)?;
        let variables = json!({"owner":owner,"name":name,"number":number,"ref":target,"comments":comment_nodes});
        let before = project(
            &self
                .client
                .issue_inputs_query(self.token, variables.clone())
                .await?,
            self.repository,
            number,
            comments,
            target,
        )?;
        self.recheck().await?;
        let after = project(
            &self
                .client
                .issue_inputs_query(self.token, variables)
                .await?,
            self.repository,
            number,
            comments,
            target,
        )?;
        if before != after {
            return Err(Failure::new(409, "github_issue_inputs_changed"));
        }
        Ok(before)
    }
}
impl Client {
    async fn issue_inputs_query(&self, token: &[u8], variables: Value) -> Result<Value> {
        self.issue_inputs_document(token,QUERY,variables,2*1024*1024).await
    }
    async fn issue_inputs_document(&self,token:&[u8],query:&'static str,variables:Value,limit:usize)->Result<Value> {
        let token = Zeroizing::new(token.to_vec());
        Ok(self
            .provider_request(
                move |http| {
                    Ok((
                        super::transport::graphql(http, &token, query, variables)?,
                        limit,
                    ))
                },
                None,
            )
            .await?
            .value)
    }
}

fn project(
    value: &Value,
    repository: &str,
    number: u32,
    selected: &[String],
    target: &str,
) -> Result<Value> {
    if value.get("errors").is_some_and(|errors| !errors.is_null()) {
        return Err(Failure::protocol());
    }
    let repo=&value["data"]["repository"];
    project_input(repo,&repo["issue"],&value["data"]["nodes"],repository,number,selected,target)
}

fn project_input(repo:&Value,issue:&Value,nodes:&Value,repository:&str,number:u32,selected:&[String],target:&str)->Result<Value> {

    if r::big_id(&repo["databaseId"])? != repository {
        return Err(Failure::protocol());
    }
    if issue["number"].as_u64() != Some(u64::from(number)) {
        return Err(Failure::protocol());
    }
    let issue_id = r::big_id(&issue["databaseId"])?;
    let issue_node = opaque(issue, "id")?;
    let version = count(&issue["userContentEdits"]["totalCount"])?;
    let title = r::text(issue, "title")?;
    let body = r::text(issue, "body")?;
    if title.is_empty()
        || title.len() > 4096
        || body.len() > 65536
        || !issue["closed"].is_boolean()
        || !issue["locked"].is_boolean()
    {
        return Err(Failure::protocol());
    }
    r::date(issue, "createdAt")?;
    r::date(issue, "closedAt")?;
    r::date(issue, "lastEditedAt")?;
    if issue["createdAt"].is_null()
        || issue["closed"].as_bool() != Some(!issue["closedAt"].is_null())
        || issue["lastEditedAt"].is_null() != (version == 0)
    {
        return Err(Failure::new(409, "github_issue_edit_history_incomplete"));
    }
    let author = json!({"external_id":r::big_id(&issue["author"]["databaseId"])?, "login":r::nonempty(&issue["author"], "login")?});
    let mut title_occurrence = "original".to_owned();
    let mut lifecycle_occurrence = "original".to_owned();
    let mut closing_occurrence = String::new();
    let mut last_title = None;
    let mut edges = BTreeMap::new();
    let mut seen = BTreeSet::new();
    for event in complete_nodes(&issue["timelineItems"], 100)? {
        let id = opaque(event, "id")?;
        if !seen.insert(id.clone()) {
            return Err(Failure::protocol());
        }
        match r::text(event, "__typename")? {
            "RenamedTitleEvent" => {
                title_occurrence = id;
                last_title = Some(r::text(event, "currentTitle")?);
            }
            "ClosedEvent" => {
                lifecycle_occurrence = id.clone();
                closing_occurrence = id;
            }
            "ReopenedEvent" => {
                lifecycle_occurrence = id;
                closing_occurrence.clear();
            }
            "BlockedByAddedEvent" => {
                edges.insert(r::big_id(&event["blockingIssue"]["databaseId"])?, id);
            }
            "BlockedByRemovedEvent" => {
                edges.remove(&r::big_id(&event["blockingIssue"]["databaseId"])?);
            }
            _ => return Err(Failure::protocol()),
        }
    }
    if last_title.is_some_and(|last| last != title)
        || issue["closed"].as_bool() != Some(!closing_occurrence.is_empty())
    {
        return Err(Failure::new(409, "github_issue_history_incomplete"));
    }
    let blocked = complete_nodes(&issue["blockedBy"], 50)?;
    if count(&issue["blockedBy"]["totalCount"])? != blocked.len() as u64
        || blocked.len() != edges.len()
    {
        return Err(Failure::new(409, "github_issue_dependencies_incomplete"));
    }
    let mut dependencies = Vec::with_capacity(blocked.len());
    let mut edge_seen = BTreeSet::new();
    for endpoint in blocked {
        let id = r::big_id(&endpoint["databaseId"])?;
        let occurrence = edges
            .get(&id)
            .ok_or_else(|| Failure::new(409, "github_issue_dependencies_incomplete"))?;
        if !edge_seen.insert(id.clone()) {
            return Err(Failure::protocol());
        }
        dependencies.push(json!({"occurrence":occurrence,"depends_on":id,"repository":r::big_id(&endpoint["repository"]["databaseId"])?,"number":count(&endpoint["number"])?}));
    }
    dependencies.sort_by_key(|v| v["depends_on"].as_str().unwrap_or("").to_owned());
    let nodes = nodes
        .as_array()
        .filter(|v| v.len() == selected.len())
        .ok_or_else(Failure::protocol)?;
    let mut comments = Vec::with_capacity(nodes.len());
    for (id, comment) in selected.iter().zip(nodes) {
        if comment["__typename"] != "IssueComment"
            || r::big_id(&comment["databaseId"])? != *id
            || comment["issue"]["id"].as_str() != Some(&issue_node)
            || comment["isMinimized"] != false
        {
            return Err(Failure::new(409, "github_issue_comment_unavailable"));
        }
        let content = r::text(comment, "body")?;
        let version = count(&comment["userContentEdits"]["totalCount"])?;
        r::date(comment, "lastEditedAt")?;
        if content.len() > 65536 || comment["lastEditedAt"].is_null() != (version == 0) {
            return Err(Failure::new(409, "github_issue_edit_history_incomplete"));
        }
        comments.push(json!({"id":id,"body":content,"version":version}));
    }
    let reference=project_reference(repo,target)?;
    let projected = json!({"issue":{"id":issue_id,"number":number,"title":title,"body":body,"version":version,"title_occurrence":title_occurrence,
        "lifecycle_occurrence":lifecycle_occurrence,"closing_occurrence":closing_occurrence,"author":author,"created_at":issue["createdAt"],"closed_at":issue["closedAt"],"closed":issue["closed"],"locked":issue["locked"]},
        "dependencies":dependencies,"comments":comments,"tip":reference["target"]["oid"],"tip_ref":target});
    if serde_json::to_vec(&projected)
        .map_err(|_| Failure::protocol())?
        .len()
        > 256 * 1024
    {
        return Err(Failure::new(409, "github_issue_inputs_incomplete"));
    }
    Ok(projected)
}

impl Reader<'_> {
    pub(super) async fn completion_issues(&self,after:&str,target:&str,limit:u32)->Result<Value> {
        let (owner,name)=self.name.split_once('/').ok_or_else(Failure::protocol)?;
        let variables=json!({"owner":owner,"name":name,"ref":target,"after":if after.is_empty(){Value::Null}else{json!(after)},"limit":limit});
        let observe=|| async {
            let raw=self.client.issue_inputs_document(self.token,INVENTORY_QUERY,variables.clone(),8*1024*1024).await?;
            project_inventory(&raw,self.repository,target,limit,after)
        };
        if crate::provider_contract::operation::Operation::current().is_some_and(|work| !work.can_finish(3)) {
            return Err(Failure::new(429,"native_operation_budget"));
        }
        let first=observe().await?;
        self.recheck().await?;
        let confirmed=observe().await?;
        if first!=confirmed {return Err(Failure::new(409,"github_inventory_changed"));}
        Ok(first)
    }
}

fn project_inventory(raw:&Value,repository:&str,target:&str,limit:u32,after:&str)->Result<Value> {
    if raw.get("errors").is_some_and(|e|!e.is_null()) {return Err(Failure::protocol());}
    let repo=&raw["data"]["repository"];
    if r::big_id(&repo["databaseId"])?!=repository {return Err(Failure::protocol());}
    let reference=project_reference(repo,target)?;
    let connection=&repo["issues"];
    let nodes=connection["nodes"].as_array().filter(|n|n.len()<=limit as usize).ok_or_else(Failure::protocol)?;
    let next=r::graphql_continuation(connection)?.unwrap_or_default();
    if !next.is_empty() && (nodes.is_empty() || next==after) {return Err(Failure::protocol());}
    let mut items=Vec::with_capacity(nodes.len());
    let mut seen=BTreeSet::new();let mut numbers=BTreeSet::new();
    for node in nodes {
        let number=node["number"].as_u64().filter(|n|*n>0 && *n<=i32::MAX as u64).ok_or_else(Failure::protocol)?;
        if !numbers.insert(number) {return Err(Failure::protocol());}
        let projected=project_input(repo,node,&json!([]),repository,number as u32,&[],target)?;
        if projected["issue"]["closed"]!=false || !seen.insert(projected["issue"]["id"].clone().as_str().ok_or_else(Failure::protocol)?.to_owned()) {return Err(Failure::protocol());}
        items.push(projected);
    }
    Ok(json!({"repository":repository,"target_ref":target,"tip":reference["target"]["oid"],"items":items,"next_cursor":next,"complete":next.is_empty()}))
}

fn project_reference<'a>(repo:&'a Value,target:&str)->Result<&'a Value> {
    let reference = &repo["ref"];
    if format!(
        "{}{}",
        r::text(reference, "prefix")?,
        r::text(reference, "name")?
    ) != target
        || reference["target"]["__typename"] != "Commit"
        || !r::text(&reference["target"], "oid").is_ok_and(super::request::commit)
    {
        return Err(Failure::protocol());
    }
    Ok(reference)
}
