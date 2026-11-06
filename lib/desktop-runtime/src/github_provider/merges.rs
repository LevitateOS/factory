use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id, remote_id},
    Client, Failure, Result, WriteOutcome,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::time::Instant;

const MERGE: &str = "mutation FactoryMergePull($input:MergePullRequestInput!){mergePullRequest(input:$input){actor{__typename ... on User{id}} pullRequest{id fullDatabaseId number repository{id} headRefOid merged mergedAt mergeCommit{oid messageHeadline messageBody repository{id}}}}}";

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PullMerge {
    pub repository: String,
    pub repository_node_id: String,
    pub pull_id: String,
    pub pull_node_id: String,
    pub number: u32,
    pub head: String,
    pub base: String,
    pub method: String,
    pub message: String,
}
impl PullMerge {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !remote_id(&self.pull_id)
            || !node_id(&self.repository_node_id)
            || !node_id(&self.pull_node_id)
            || self.number == 0
            || !commit(&self.head)
            || !commit(&self.base)
            || !matches!(self.method.as_str(), "merge" | "squash" | "rebase")
            || self.message.len() > 65536
            || self.message.contains('\0')
            || if self.method == "rebase" {
                !self.message.is_empty()
            } else {
                self.message.trim().is_empty()
            }
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
    fn message_parts(&self) -> (&str, &str) {
        match self.message.split_once('\n') {
            Some((headline, body)) => (headline, body.strip_prefix('\n').unwrap_or(body)),
            None => (&self.message, ""),
        }
    }
    fn input(&self) -> Value {
        let mut input = json!({"pullRequestId":self.pull_node_id,"expectedHeadOid":self.head,"mergeMethod":self.method.to_ascii_uppercase()});
        if self.method != "rebase" {
            let (headline, body) = self.message_parts();
            input["commitHeadline"] = json!(headline);
            input["commitBody"] = json!(body);
        }
        input
    }
}
impl Reader<'_> {
    pub(super) async fn merge_preparation(&self, number: u32) -> Result<Value> {
        let observe = async || -> Result<Value> {
            let repository = self.get("", &[]).await?.value;
            let pull = self.raw_subject(true, number.into()).await?;
            if r::id(&repository["id"])? != self.repository
                || repository["full_name"] != self.name
                || r::id(&pull["base"]["repo"]["id"])? != self.repository
                || pull["number"].as_u64() != Some(number.into())
                || !r::text(&repository, "node_id").is_ok_and(node_id)
                || !r::text(&pull, "node_id").is_ok_and(node_id)
                || !r::text(&pull["head"], "sha").is_ok_and(commit)
                || !r::text(&pull["base"], "sha").is_ok_and(commit)
            {
                return Err(Failure::protocol());
            }
            Ok(
                json!({"repository":self.repository,"name":self.name,"repository_node_id":repository["node_id"],
                "pull_id":r::id(&pull["id"])?,"pull_node_id":pull["node_id"],"number":number,
                "head":pull["head"]["sha"],"base":pull["base"]["sha"],"state":r::text(&pull,"state")?,
                "draft":r::boolean(&pull,"draft")?,"merged":r::boolean(&pull,"merged")?,
                "mergeable":r::boolean(&pull,"mergeable")?,"mergeable_state":r::string(&pull,"mergeable_state")?,
                "archived":r::boolean(&repository,"archived")?,"disabled":r::boolean(&repository,"disabled")?,
                "push":r::boolean(&repository["permissions"],"push")?,
                "methods":{"merge":r::boolean(&repository,"allow_merge_commit")?,"squash":r::boolean(&repository,"allow_squash_merge")?,"rebase":r::boolean(&repository,"allow_rebase_merge")?},
                "head_condition":"expected_head_oid","base_condition":"bounded_preflight"}),
            )
        };
        let facts = observe().await?;
        if observe().await? != facts {
            return Err(Failure::new(409, "github_merge_inputs_changed"));
        }
        Ok(facts)
    }
}
impl Client {
    pub async fn merge_pull(&self, token: Token, expected: &str, q: &PullMerge) -> WriteOutcome {
        self.merge_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn merge_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &PullMerge,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        self.run_mutation(deadline, |mutation| async move {
            q.validate()?;
            if !remote_id(expected) { return Err(Failure::new(400,"invalid_native_write")); }
            let (mut permit, identity) = mutation.admit(self, token, expected).await?;
            let actor = identity.node_id.as_deref().ok_or_else(Failure::protocol)?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let facts = reader.merge_preparation(q.number).await?;
            if facts["repository_node_id"] != q.repository_node_id || facts["pull_id"] != q.pull_id
                || facts["pull_node_id"] != q.pull_node_id || facts["head"] != q.head || facts["base"] != q.base
            { return Err(Failure::new(409,"github_merge_inputs_changed")); }
            if facts["state"] != "open" || facts["draft"] != false || facts["merged"] != false
                || facts["mergeable"] != true || !matches!(facts["mergeable_state"].as_str(), Some("clean" | "unstable"))
                || facts["archived"] != false || facts["disabled"] != false || facts["push"] != true
                || facts["methods"][&q.method] != true
            { return Err(Failure::new(403,"github_merge_unavailable")); }
            permit.dispatch("github_merge_expired")?;
            let input = q.input();
            let token = zeroize::Zeroizing::new(token.to_vec());
            let response = self.provider_request(move |http| Ok((super::transport::graphql(http,&token,MERGE,json!({"input":input}))?,1024*1024)),false).await?;
            let payload = &response.value["data"]["mergePullRequest"];
            let pull = &payload["pullRequest"];
            let merged = &pull["mergeCommit"];
            if payload["actor"]["__typename"] != "User" || payload["actor"]["id"] != actor
                || pull["id"] != q.pull_node_id || r::big_id(&pull["fullDatabaseId"])? != q.pull_id
                || pull["number"].as_u64() != Some(q.number.into()) || pull["repository"]["id"] != q.repository_node_id
                || pull["headRefOid"] != q.head || pull["merged"] != true
                || merged["repository"]["id"] != q.repository_node_id || !r::text(merged,"oid").is_ok_and(commit)
            { return Err(Failure::protocol()); }
            let dates = json!({"merged_at":pull["mergedAt"]});
            let merged_at = r::date(&dates,"merged_at")?;
            if merged_at.is_null() { return Err(Failure::protocol()); }
            let (headline, body) = q.message_parts();
            let writing_matches = q.method == "rebase" || r::text(merged,"messageHeadline")? == headline && r::text(merged,"messageBody")? == body;
            let receipt = json!({"account":identity,"repository":q.repository,"repository_node_id":q.repository_node_id,
                "pull_id":q.pull_id,"pull_node_id":q.pull_node_id,"number":q.number,"head":q.head,"base":q.base,
                "method":q.method,"message_sha256":super::transport::sha256(q.message.as_bytes()),"writing_matches":writing_matches,
                "head_condition":"expected_head_oid","base_condition":"bounded_preflight",
                "commit":merged["oid"],"merged_at":merged_at,
                "provider_response":{"protocol":"github_graphql_merge_pull","status":200,"request_id":response.request_id,"response_sha256":response.response_sha256}});
            super::writes::check_receipt(&receipt)?;
            self.current()?;
            Ok(receipt)
        }).await.update()
    }
}
