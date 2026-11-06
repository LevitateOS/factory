use super::reads::Reader;
use super::request::commit;
use super::{records as r, Failure, Result};
use serde_json::{json, Value};
use std::collections::HashSet;

const QUERY: &str = "query($pull:ID!,$limit:Int!,$before:String){node(id:$pull){... on PullRequest{id number headRefOid commits(last:$limit,before:$before){nodes{commit{oid messageHeadline authoredDate author{name}}} pageInfo{hasPreviousPage startCursor}}}}}";

impl Reader<'_> {
    pub(super) async fn pull_commits(&self, q: &Value) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let head = r::text(q, "head")?;
        let limit = q["limit"].as_u64().ok_or_else(Failure::invalid)?;
        let before = r::text(q, "before")?;
        let original = self.raw_subject(true, number).await?;
        let pull_id = r::id(&original["id"])?;
        let pull_node = r::nonempty(&original, "node_id")?;
        if original["head"]["sha"] != head {
            return Err(Failure::new(409, "github_commits_changed"));
        }
        let token = zeroize::Zeroizing::new(self.token.to_vec());
        let variables = json!({"pull":pull_node,"limit":limit,"before":if before.is_empty(){None}else{Some(before)}});
        let answer = self
            .client
            .provider_request(
                move |http| {
                    Ok((
                        super::transport::graphql(http, &token, QUERY, variables)?,
                        4 * 1024 * 1024,
                    ))
                },
                true,
            )
            .await?
            .value;
        let node = &answer["data"]["node"];
        if node.is_null() {
            return Err(Failure::new(404, "github_not_visible"));
        }
        if node["id"] != pull_node || node["number"] != number || node["headRefOid"] != head {
            return Err(Failure::new(409, "github_commits_changed"));
        }
        let connection = &node["commits"];
        let nodes = connection["nodes"]
            .as_array()
            .ok_or_else(Failure::protocol)?;
        if nodes.len() > limit as usize {
            return Err(Failure::protocol());
        }
        let info = &connection["pageInfo"];
        let more = info["hasPreviousPage"]
            .as_bool()
            .ok_or_else(Failure::protocol)?;
        let cursor = if more {
            let value = r::nonempty(info, "startCursor")?;
            if nodes.is_empty()
                || value.len() > 1024
                || value.chars().any(char::is_control)
                || value == before
            {
                return Err(Failure::protocol());
            }
            value
        } else {
            ""
        };
        let mut seen = HashSet::new();
        let mut items = Vec::with_capacity(nodes.len());
        for item in nodes.iter().rev() {
            let entry = &item["commit"];
            let oid = r::text(entry, "oid")?;
            let summary = r::text(entry, "messageHeadline")?;
            let author = entry["author"]["name"].as_str().unwrap_or("");
            if !commit(oid)
                || !seen.insert(oid)
                || summary.len() > 65536
                || summary.contains('\0')
                || author.len() > 1024
                || author.contains('\0')
            {
                return Err(Failure::protocol());
            }
            items.push(json!({"commit":oid,"summary":summary,"author":author,"authored_at":r::date(entry,"authoredDate")?}));
        }
        let current = self.raw_subject(true, number).await?;
        if r::id(&current["id"])? != pull_id || current["head"]["sha"] != head {
            return Err(Failure::new(409, "github_commits_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"pull":{"kind":"pull","external_id":pull_id,"number":number},"head":head,"items":items,"next_cursor":cursor}),
        )
    }
}
