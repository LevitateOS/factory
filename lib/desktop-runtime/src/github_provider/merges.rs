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
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub controller_grant_id:String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source_connection:String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub head_ref:String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub base_ref:String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub host_revision:Option<i64>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub checks:Vec<Value>,
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
            || !matches!(self.method.as_str(), "merge" | "squash" | "rebase" | "fast-forward-only")
            || self.message.len() > 65536
            || self.message.contains('\0')
            || if matches!(self.method.as_str(),"rebase"|"fast-forward-only") {
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
    pub(super) async fn merge_completion(&self, number: u64) -> Result<Value> {
        let observe = async || -> Result<Value> {
            let pull = self.raw_subject(true, number).await?;
            let issue = self.raw_subject(false, number).await?;
            let head = r::nonempty(&pull["head"], "sha")?;
            let base = r::nonempty(&pull["base"], "sha")?;
            // REST 2026-03-10 omits merge_commit_sha. Bind the GraphQL result
            // to the independently read numeric PR and repository node IDs.
            let query="query FactoryMergedCommit($pull:ID!){node(id:$pull){__typename ... on PullRequest{id fullDatabaseId number repository{id} headRefOid merged mergeCommit{oid repository{id}}}}}";
            let variables = json!({"pull":r::text(&pull,"node_id")?});
            let token = zeroize::Zeroizing::new(self.token.to_vec());
            let graph = self
                .client
                .provider_request(
                    move |http| {
                        Ok((
                            super::transport::graphql(http, &token, query, variables)?,
                            1024 * 1024,
                        ))
                    },
                    None,
                )
                .await?
                .value;
            let result = &graph["data"]["node"];
            if result["__typename"] != "PullRequest"
                || result["id"] != pull["node_id"]
                || r::big_id(&result["fullDatabaseId"])? != r::id(&pull["id"])?
                || result["number"] != pull["number"]
                || result["repository"]["id"] != pull["base"]["repo"]["node_id"]
                || result["headRefOid"] != head
                || result["merged"] != true
                || result["mergeCommit"]["repository"]["id"] != result["repository"]["id"]
            {
                return Err(Failure::protocol());
            }
            let merged = r::nonempty(&result["mergeCommit"], "oid")?;
            let branch = r::nonempty(&pull["base"], "ref")?;
            if pull["merged"] != true
                || pull["state"] != "closed"
                || r::id(&pull["base"]["repo"]["id"])? != self.repository
                || r::id(&pull["head"]["repo"]["id"])? != self.repository
                || !commit(head)
                || !commit(base)
                || !commit(merged)
                || !super::request::branch_name(branch)
                || issue["pull_request"]["url"] != self.url(&format!("/pulls/{number}"))
            {
                return Err(Failure::new(409, "github_merge_completion_pending"));
            }
            let mut url =
                url::Url::parse("https://api.github.com/").map_err(|_| Failure::invalid())?;
            url.path_segments_mut()
                .map_err(|_| Failure::invalid())?
                .push(branch);
            let observed = self
                .get(
                    &format!("/branches/{}", url.path().trim_start_matches('/')),
                    &[],
                )
                .await?
                .value;
            let tip = r::nonempty(&observed["commit"], "sha")?;
            if observed["name"] != branch || !commit(tip) {
                return Err(Failure::protocol());
            }
            let contains = if tip == merged {
                true
            } else {
                let comparison = self
                    .get(&format!("/compare/{merged}...{tip}"), &[])
                    .await?
                    .value;
                comparison["base_commit"]["sha"] == merged
                    && comparison["merge_base_commit"]["sha"] == merged
                    && matches!(comparison["status"].as_str(), Some("ahead" | "identical"))
            };
            Ok(
                json!({"repository":self.repository,"number":number,"pull_id":r::id(&pull["id"])?,
                "issue_id":r::id(&issue["id"])?,"head":head,"head_ref":format!("refs/heads/{}",r::nonempty(&pull["head"],"ref")?),
                "base_ref":format!("refs/heads/{branch}"),"commit":merged,"base_tip":tip,"base_contains":contains,
                "merger":r::actor(&pull["merged_by"])?,"merged_at":r::date(&pull,"merged_at")?,
                "closed_at":r::date(&issue,"closed_at")?,"issue_closed":issue["state"]=="closed"}),
            )
        };
        let before = observe().await?;
        self.recheck().await?;
        if observe().await? != before {
            return Err(Failure::new(409, "github_merge_completion_changed"));
        }
        Ok(before)
    }
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
            if !q.controller_grant_id.is_empty() || !q.source_connection.is_empty() || !q.head_ref.is_empty() || !q.base_ref.is_empty() || q.host_revision.is_some() || !q.checks.is_empty() || q.method=="fast-forward-only" {return Err(Failure::invalid());}
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
            let proof = permit.dispatch("github_merge_expired")?;
            let input = q.input();
            let token = zeroize::Zeroizing::new(token.to_vec());
            let response = self.provider_request(move |http| Ok((super::transport::graphql(http,&token,MERGE,json!({"input":input}))?,1024*1024)),Some(&proof)).await?;
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
