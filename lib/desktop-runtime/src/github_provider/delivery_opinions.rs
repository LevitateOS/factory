//! Provider-defined current review decisions. Historical REST review ordering
//! cannot identify the effective opinion or its current writer permission.
use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id},
    Failure, Result,
};
use serde_json::{json, Value};
use std::collections::HashSet;

const QUERY: &str = "query FactoryDeliveryOpinions($pull:ID!,$after:String){node(id:$pull){... on PullRequest{id fullDatabaseId number repository{id} headRefOid baseRefOid reviewDecision latestOpinionatedReviews(first:100,after:$after,writersOnly:true){totalCount pageInfo{hasNextPage endCursor} nodes{fullDatabaseId state}}}}}";

impl Reader<'_> {
    pub(super) async fn delivery_opinions(&self, number: u64) -> Result<Value> {
        let pull = self.raw_subject(true, number).await?;
        let pull_id = r::id(&pull["id"])?;
        let pull_node = r::nonempty(&pull, "node_id")?.to_owned();
        let repository = self.get("", &[]).await?.value;
        let repository_node = r::nonempty(&repository, "node_id")?;
        let head = r::nonempty(&pull["head"], "sha")?;
        let base = r::nonempty(&pull["base"], "sha")?;
        if !node_id(&pull_node)
            || !node_id(repository_node)
            || r::id(&repository["id"])? != self.repository
            || !commit(head)
            || !commit(base)
        {
            return Err(Failure::protocol());
        }
        let mut after: Option<String> = None;
        let mut cursors = HashSet::new();
        let mut ids = HashSet::new();
        let mut items = Vec::new();
        let mut decision: Option<Value> = None;
        let mut total: Option<u64> = None;
        loop {
            let token = zeroize::Zeroizing::new(self.token.to_vec());
            let variables = json!({"pull":pull_node,"after":after});
            let response = self
                .client
                .provider_request(
                    move |http| {
                        Ok((
                            super::transport::graphql(http, &token, QUERY, variables.clone())?,
                            1024 * 1024,
                        ))
                    },
                    None,
                )
                .await?;
            let observed = &response.value["data"]["node"];
            if observed["id"] != pull_node
                || r::big_id(&observed["fullDatabaseId"])? != pull_id
                || observed["number"].as_u64() != Some(number)
                || observed["repository"]["id"] != repository_node
                || observed["headRefOid"] != head
                || observed["baseRefOid"] != base
            {
                return Err(Failure::new(409, "github_delivery_opinions_changed"));
            }
            let current = &observed["reviewDecision"];
            if !current.is_null()
                && !matches!(
                    current.as_str(),
                    Some("APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED")
                )
            {
                return Err(Failure::protocol());
            }
            if decision.as_ref().is_some_and(|d| d != current) {
                return Err(Failure::new(409, "github_delivery_opinions_changed"));
            }
            decision = Some(current.clone());
            let connection = &observed["latestOpinionatedReviews"];
            let count = connection["totalCount"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            if count > 256 {
                return Err(Failure::new(413, "github_delivery_opinions_too_large"));
            }
            if total.is_some_and(|t| t != count) {
                return Err(Failure::new(409, "github_delivery_opinions_changed"));
            }
            total = Some(count);
            let nodes = connection["nodes"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            if nodes.len() > 100 || items.len() + nodes.len() > count as usize {
                return Err(Failure::protocol());
            }
            for review in nodes {
                let id = r::big_id(&review["fullDatabaseId"])?;
                if !ids.insert(id.clone()) {
                    return Err(Failure::new(409, "github_delivery_opinions_changed"));
                }
                let state = match r::text(review, "state")? {
                    "APPROVED" => "approved",
                    "CHANGES_REQUESTED" => "changes_requested",
                    "COMMENTED" => "commented",
                    "DISMISSED" => "dismissed",
                    _ => return Err(Failure::protocol()),
                };
                items.push(json!({"id":id,"state":state}));
            }
            after = r::graphql_continuation(connection)?;
            if let Some(cursor) = &after {
                if nodes.is_empty() || !cursors.insert(cursor.clone()) {
                    return Err(Failure::protocol());
                }
            } else {
                if items.len() != count as usize {
                    return Err(Failure::new(409, "github_delivery_opinions_changed"));
                }
                break;
            }
        }
        let current = self.raw_subject(true, number).await?;
        if current["id"] != pull["id"]
            || current["node_id"] != pull_node
            || current["head"]["sha"] != head
            || current["base"]["sha"] != base
        {
            return Err(Failure::new(409, "github_delivery_opinions_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"pull_id":pull_id,"number":number,"head":head,"base":base,"decision":decision.unwrap_or(Value::Null),"items":items}),
        )
    }
}
