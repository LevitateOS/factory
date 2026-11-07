//! One bounded issue-dependency effect. The set check is a preflight, not GitHub
//! atomic graph support. Product ordering/recovery remain in the Go journal.
use super::{
    reads::Reader,
    records as r,
    request::{node_id, remote_id},
    Client, Failure, Result, WriteOutcome,
};
use serde::{Deserialize, Serialize};
use serde_json::json;
use std::{collections::BTreeSet, time::Instant};

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct IssueDependency {
    pub repository: String,
    pub issue_id: String,
    pub number: u32,
    pub blocking_id: String,
    pub blocking_number: u32,
    pub expected: Vec<String>,
    pub remove: bool,
}
impl IssueDependency {
    pub fn validate(&self) -> Result<()> {
        let set: BTreeSet<_> = self.expected.iter().collect();
        if !remote_id(&self.repository)
            || !remote_id(&self.issue_id)
            || !remote_id(&self.blocking_id)
            || self.issue_id == self.blocking_id
            || self.number == 0
            || self.blocking_number == 0
            || self.number == self.blocking_number
            || self.expected.len() > 50
            || set.len() != self.expected.len()
            || self
                .expected
                .iter()
                .any(|id| !remote_id(id) || id == &self.issue_id)
            || self.expected.contains(&self.blocking_id) != self.remove
        {
            return Err(Failure::new(400, "invalid_native_dependency"));
        }
        Ok(())
    }
}
impl Reader<'_> {
    async fn dependency_set(
        &self,
        number: u32,
        issue_id: &str,
        repository_node: &str,
    ) -> Result<BTreeSet<String>> {
        let query="query FactoryDependencySet($repository:ID!,$number:Int!){node(id:$repository){__typename ... on Repository{id nameWithOwner issue(number:$number){id fullDatabaseId number blockedBy(first:50){totalCount pageInfo{hasNextPage} nodes{databaseId number repository{databaseId}}}}}}}";
        let variables = json!({"repository":repository_node,"number":number});
        let token = zeroize::Zeroizing::new(self.token.to_vec());
        let response = self
            .client
            .provider_request(
                move |http| {
                    Ok((
                        super::transport::graphql(http, &token, query, variables)?,
                        128 * 1024,
                    ))
                },
                true,
            )
            .await?;
        let repo = &response.value["data"]["node"];
        let issue = &repo["issue"];
        if repo["__typename"] != "Repository"
            || repo["id"] != repository_node
            || repo["nameWithOwner"] != self.name
            || issue["number"].as_u64() != Some(number.into())
            || r::big_id(&issue["fullDatabaseId"])? != issue_id
            || !r::text(issue, "id").is_ok_and(node_id)
        {
            return Err(Failure::protocol());
        }
        let deps = &issue["blockedBy"];
        let items = deps["nodes"].as_array().ok_or_else(Failure::protocol)?;
        if deps["pageInfo"]["hasNextPage"] != false
            || deps["totalCount"].as_u64() != Some(items.len() as u64)
            || items.len() > 50
        {
            return Err(Failure::new(409, "github_dependencies_incomplete"));
        }
        let mut ids = BTreeSet::new();
        for item in items {
            let id = r::big_id(&item["databaseId"])?;
            if r::big_id(&item["repository"]["databaseId"])? != self.repository
                || item["number"].as_u64().is_none_or(|n| n == 0)
                || id == issue_id
                || !ids.insert(id)
            {
                return Err(Failure::protocol());
            }
        }
        Ok(ids)
    }
}
impl Client {
    pub(crate) async fn dependency_account(
        &self,
        token: &[u8],
        account: &str,
        edit: &IssueDependency,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        self.run_mutation(deadline,|mutation| async move {
   edit.validate()?;
   let (mut permit,identity)=mutation.admit(self,token,account).await?;
   let reader=Reader::new(self,token,&edit.repository).await?;
   let repository=reader.get("",&[]).await?.value;
   let repository_node=r::text(&repository,"node_id")?;
   if !node_id(repository_node) || r::id(&repository["id"])?!=edit.repository || repository["archived"]!=false || repository["disabled"]!=false {return Err(Failure::new(403,"github_repository_read_only"))}
   let issue=reader.raw_subject(false,edit.number.into()).await?;
   let blocking=reader.raw_subject(false,edit.blocking_number.into()).await?;
   if r::id(&issue["id"])?!=edit.issue_id || r::id(&blocking["id"])?!=edit.blocking_id || !issue["pull_request"].is_null() || !blocking["pull_request"].is_null() || issue["locked"]!=false || issue["state"]!="open" {return Err(Failure::new(409,"github_dependency_subject_changed"))}
   let issue_node=r::text(&issue,"node_id")?;let blocking_node=r::text(&blocking,"node_id")?;
   if !node_id(issue_node) || !node_id(blocking_node) {return Err(Failure::protocol())}
   let current=reader.dependency_set(edit.number,&edit.issue_id,repository_node).await?;
   if current!=edit.expected.iter().cloned().collect(){return Err(Failure::new(409,"github_dependency_set_changed"))}
   let (operation,input)=if edit.remove {("removeBlockedBy","RemoveBlockedByInput")}else{("addBlockedBy","AddBlockedByInput")};
   let query=format!("mutation FactoryDependency($input:{input}!){{{operation}(input:$input){{issue{{id fullDatabaseId number repository{{id nameWithOwner}}}} blockingIssue{{id fullDatabaseId number repository{{id nameWithOwner}}}}}}}}");
   let variables=json!({"input":{"issueId":issue_node,"blockingIssueId":blocking_node}});
   let token=zeroize::Zeroizing::new(token.to_vec());
   permit.dispatch("github_dependency_expired")?;
   let response=self.provider_request(move |http|Ok((super::transport::graphql(http,&token,&query,variables)?,128*1024)),false).await?;
   let payload=&response.value["data"][operation];
   for (key,id,node,number) in [("issue",edit.issue_id.as_str(),issue_node,edit.number),("blockingIssue",edit.blocking_id.as_str(),blocking_node,edit.blocking_number)] {
    let raw=&payload[key];
    if raw["id"]!=node || r::big_id(&raw["fullDatabaseId"])?!=id || raw["number"].as_u64()!=Some(number.into()) || raw["repository"]["id"]!=repository_node || raw["repository"]["nameWithOwner"]!=reader.name{return Err(Failure::protocol())}
   }
   // Preserve the causal response even if a later aggregate verification fails.
   let receipt=json!({"account":identity,"repository":edit.repository,"repository_node_id":repository_node,"issue_id":edit.issue_id,"number":edit.number,"blocking_id":edit.blocking_id,"blocking_number":edit.blocking_number,"removed":edit.remove,"provider_response":{"protocol":"github_graphql_issue_dependency","mutation":operation,"status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
   super::writes::check_receipt(&receipt)?;self.current()?;Ok(receipt)
  }).await.update()
    }
}
