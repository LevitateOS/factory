//! Complete bounded pending-review discovery for the authenticated account.
//! These current facts grant no review permission or atomic head/body condition.
use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id},
    Failure, Result,
};
use serde_json::{json, Value};
use std::collections::HashSet;

const LIST:&str="query FactoryPendingReviews($pull:ID!,$after:String){node(id:$pull){__typename ... on PullRequest{id fullDatabaseId number repository{id} headRefOid reviews(first:50,after:$after,states:[PENDING]){totalCount pageInfo{hasNextPage endCursor} nodes{id state author{__typename ... on User{id}} pullRequest{id} repository{id}}}}}}";
const DETAIL:&str="query FactoryPendingReview($review:ID!){node(id:$review){__typename ... on PullRequestReview{id fullDatabaseId state body author{__typename ... on User{id login}} pullRequest{id headRefOid repository{id}} repository{id} commit{oid repository{id}} comments{totalCount} createdAt updatedAt submittedAt}}}";
pub(super) enum PreparationQuery {
    List { pull: String, after: Option<String> },
    Detail { review: String },
}
impl PreparationQuery {
    pub(super) fn wire(&self) -> (&'static str, Value) {
        match self {
            Self::List { pull, after } => (LIST, json!({"pull":pull,"after":after})),
            Self::Detail { review } => (DETAIL, json!({"review":review})),
        }
    }
}
fn opaque<'a>(raw: &'a Value, key: &str) -> Result<&'a str> {
    let v = r::text(raw, key)?;
    if !node_id(v) {
        return Err(Failure::protocol());
    }
    Ok(v)
}
impl Reader<'_> {
    pub(super) async fn review_preparation(
        &self,
        number: u64,
        actor: &str,
        account: &str,
    ) -> Result<Value> {
        if !node_id(actor) {
            return Err(Failure::protocol());
        }
        let before = self.raw_subject(true, number).await?;
        let repository = self.get("", &[]).await?.value;
        let repo = opaque(&repository, "node_id")?;
        let pull = opaque(&before, "node_id")?;
        let external = r::id(&before["id"])?;
        let head = r::text(&before["head"], "sha")?;
        if !commit(head)
            || r::id(&repository["id"])? != self.repository
            || r::id(&before["base"]["repo"]["id"])? != self.repository
        {
            return Err(Failure::protocol());
        }
        let mut after = None;
        let mut cursors = HashSet::new();
        let mut ids = HashSet::new();
        let mut count = None;
        let mut selected = None;
        loop {
            let value = self
                .client
                .review_preparation_query(
                    PreparationQuery::List {
                        pull: pull.into(),
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
                || raw["id"] != pull
                || r::big_id(&raw["fullDatabaseId"])? != external
                || raw["number"].as_u64() != Some(number)
                || raw["repository"]["id"] != repo
                || raw["headRefOid"] != head
            {
                return Err(Failure::new(409, "github_review_inputs_changed"));
            }
            let connection = &raw["reviews"];
            let total = connection["totalCount"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            if total > 1000 {
                return Err(Failure::new(413, "github_pending_reviews_too_large"));
            }
            if count.replace(total).is_some_and(|old| old != total) {
                return Err(Failure::new(409, "github_pending_reviews_changed"));
            }
            let nodes = connection["nodes"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            if nodes.len() > 50 {
                return Err(Failure::protocol());
            }
            for review in nodes {
                let id = opaque(review, "id")?;
                if !ids.insert(id.to_owned())
                    || review["state"] != "PENDING"
                    || review["pullRequest"]["id"] != pull
                    || review["repository"]["id"] != repo
                    || review["author"]["__typename"] != "User"
                {
                    return Err(Failure::protocol());
                }
                let author = opaque(&review["author"], "id")?;
                if author == actor && selected.replace(id.to_owned()).is_some() {
                    return Err(Failure::new(409, "github_pending_reviews_ambiguous"));
                }
            }
            let next = r::graphql_continuation(connection)?;
            if next.is_none() {
                if ids.len() as u64 != total {
                    return Err(Failure::new(409, "github_pending_reviews_changed"));
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
        let pending = if let Some(node) = selected {
            let value = self
                .client
                .review_preparation_query(
                    PreparationQuery::Detail {
                        review: node.clone(),
                    },
                    self.token,
                )
                .await?;
            let raw = &value["data"]["node"];
            if raw.is_null() {
                return Err(Failure::new(409, "github_pending_review_changed"));
            }
            if raw["__typename"] != "PullRequestReview"
                || raw["id"] != node
                || raw["state"] != "PENDING"
                || raw["author"]["__typename"] != "User"
                || raw["author"]["id"] != actor
                || raw["pullRequest"]["id"] != pull
                || raw["pullRequest"]["headRefOid"] != head
                || raw["pullRequest"]["repository"]["id"] != repo
                || raw["repository"]["id"] != repo
                || !raw["submittedAt"].is_null()
            {
                return Err(Failure::new(409, "github_pending_review_changed"));
            }
            let body = r::text(raw, "body")?;
            if body.len() > 65536 || body.contains('\0') {
                return Err(Failure::new(413, "github_pending_review_body_too_large"));
            }
            let oid = if raw["commit"].is_null() {
                Value::Null
            } else {
                let oid = r::text(&raw["commit"], "oid")?;
                if !commit(oid) || raw["commit"]["repository"]["id"] != repo {
                    return Err(Failure::protocol());
                }
                json!(oid)
            };
            let id = if raw["fullDatabaseId"].is_null() {
                Value::Null
            } else {
                json!(r::big_id(&raw["fullDatabaseId"])?)
            };
            let comments = raw["comments"]["totalCount"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            if comments > i64::MAX as u64 {
                return Err(Failure::protocol());
            }
            let dates = json!({"created_at":raw["createdAt"],"updated_at":raw["updatedAt"]});
            json!({"external_id":id,"node_id":node,"state":"pending","body":body,"commit":oid,"comments_count":comments,"created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?})
        } else {
            Value::Null
        };
        let current = self.raw_subject(true, number).await?;
        for key in [
            "id", "node_id", "number", "state", "locked", "draft", "merged", "base", "head",
        ] {
            if current[key] != before[key] {
                return Err(Failure::new(409, "github_review_inputs_changed"));
            }
        }
        self.recheck().await?;
        let identity = self.client.identity(self.token).await?;
        if identity.external_id != account || identity.node_id.as_deref() != Some(actor) {
            return Err(Failure::new(409, "github_account_changed"));
        }
        Ok(
            json!({"repository":self.repository,"name":self.name,"repository_node_id":repo,"archived":r::boolean(&repository,"archived")?,"disabled":r::boolean(&repository,"disabled")?,"pull":{"external_id":external,"node_id":pull,"number":number,"state":before["state"],"locked":r::boolean(&before,"locked")?,"draft":r::boolean(&before,"draft")?,"merged":r::boolean(&before,"merged")?,"head":head},"pending":pending,"visible_pending_count":count,"actor_node_id":actor}),
        )
    }
}
