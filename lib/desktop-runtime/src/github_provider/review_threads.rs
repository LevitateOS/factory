//! The defining GraphQL thread reader. No historical REST root-comment ID is
//! translated into a thread or granted mutation authority.
use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id, remote_id},
    Client, Failure, Result,
};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{collections::HashSet, time::Duration};

const FIELDS:&str="id isResolved isOutdated viewerCanResolve viewerCanUnresolve path diffSide pullRequest{id fullDatabaseId number repository{id}} repository{id} root:comments(first:1){nodes{originalCommit{oid} originalLine path replyTo{id}}} comments(first:$limit,skip:$skip){totalCount pageInfo{hasNextPage endCursor} nodes{id fullDatabaseId body createdAt updatedAt author{login ... on User{databaseId} ... on Bot{databaseId}} pullRequestReview{fullDatabaseId} pullRequest{id} repository{id}}}";

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ThreadRead {
    pub repository: String,
    pub number: u64,
    pub page: u32,
    pub limit: u32,
    pub thread_id: String,
}
impl ThreadRead {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.page == 0
            || self.page > 1_000_000
            || self.limit == 0
            || self.limit > 20
            || !self.thread_id.is_empty()
                && (!node_id(&self.thread_id) || self.thread_id.bytes().all(|b| b.is_ascii_digit()))
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}
// A finite native-only query selector; no caller-supplied GraphQL is accepted.
pub(super) enum ThreadQuery {
    Catalogue {
        pull: String,
        after: Option<String>,
    },
    Detail {
        thread: String,
        skip: u32,
        limit: u32,
    },
}
impl ThreadQuery {
    pub(super) fn wire(&self) -> (String, Value) {
        match self {
            Self::Catalogue { pull, after } => (
                "query FactoryReviewThreads($pull:ID!,$after:String,$limit:Int!,$skip:Int!){node(id:$pull){__typename ... on PullRequest{id fullDatabaseId number repository{id} reviewThreads(first:50,after:$after){totalCount pageInfo{hasNextPage endCursor} nodes{FIELDS}}}}}".replace("FIELDS", FIELDS),
                json!({"pull":pull,"after":after,"limit":10,"skip":0}),
            ),
            Self::Detail { thread, skip, limit } => (
                "query FactoryReviewThread($thread:ID!,$limit:Int!,$skip:Int!){node(id:$thread){__typename ... on PullRequestReviewThread{FIELDS}}}".replace("FIELDS", FIELDS),
                json!({"thread":thread,"limit":limit,"skip":skip}),
            ),
        }
    }
}

impl Client {
    /// Authenticated observation producer, with no canonical IDs or SQL access.
    pub async fn observe_review_threads(
        &self,
        token: Token,
        expected: &str,
        q: &ThreadRead,
    ) -> Result<Value> {
        q.validate()?;
        if !remote_id(expected) {
            return Err(Failure::invalid());
        }
        self.scope(async {
            tokio::time::timeout(Duration::from_secs(8), async {
                let identity = self.identity(token.as_bytes()).await?;
                if identity.external_id != expected {
                    return Err(Failure::new(409, "github_account_changed"));
                }
                let reader = Reader::new(self, token.as_bytes(), &q.repository).await?;
                let observed = reader.graph_threads(q).await?;
                self.current()?;
                Ok(json!({"kind":"pull_review_threads","account":identity,"threads":observed}))
            })
            .await
            .unwrap_or_else(|_| Err(Failure::new(504, "github_deadline")))
        })
        .await?
    }
}
impl Reader<'_> {
    pub(super) async fn graph_threads(&self, q: &ThreadRead) -> Result<Value> {
        let pull = self.raw_subject(true, q.number).await?;
        let pull_id = r::id(&pull["id"])?;
        let pull_node = r::text(&pull, "node_id")?;
        if !node_id(pull_node) {
            return Err(Failure::protocol());
        }
        let repository = self.get("", &[]).await?.value;
        if r::id(&repository["id"])? != q.repository {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let repository_node = r::text(&repository, "node_id")?;
        if !node_id(repository_node) {
            return Err(Failure::protocol());
        }
        let mut projected = Vec::new();
        let next;
        if !q.thread_id.is_empty() {
            let skip = (q.page - 1)
                .checked_mul(q.limit)
                .ok_or_else(Failure::invalid)?;
            let value = self
                .client
                .thread_query(
                    ThreadQuery::Detail {
                        thread: q.thread_id.clone(),
                        skip,
                        limit: q.limit,
                    },
                    self.token,
                )
                .await?;
            let thread = &value["data"]["node"];
            if thread.is_null() {
                return Err(Failure::new(404, "github_not_visible"));
            }
            if thread["__typename"] != "PullRequestReviewThread" || thread["id"] != q.thread_id {
                return Err(Failure::protocol());
            }
            projected.push(project(
                thread,
                pull_node,
                &pull_id,
                q.number,
                repository_node,
                q.page,
                q.limit,
            )?);
            next = 0;
        } else {
            let mut after = None;
            let mut cursors = HashSet::new();
            let mut ids = HashSet::new();
            let mut all = Vec::new();
            let mut total = None;
            let mut bytes = 0;
            let mut comments = 0;
            loop {
                let value = self
                    .client
                    .thread_query(
                        ThreadQuery::Catalogue {
                            pull: pull_node.into(),
                            after: after.clone(),
                        },
                        self.token,
                    )
                    .await?;
                let observed = &value["data"]["node"];
                if observed.is_null() {
                    return Err(Failure::new(404, "github_not_visible"));
                }
                if observed["__typename"] != "PullRequest"
                    || observed["id"] != pull_node
                    || r::big_id(&observed["fullDatabaseId"])? != pull_id
                    || observed["number"].as_u64() != Some(q.number)
                    || observed["repository"]["id"] != repository_node
                {
                    return Err(Failure::new(409, "github_threads_changed"));
                }
                let connection = &observed["reviewThreads"];
                let count = connection["totalCount"]
                    .as_u64()
                    .ok_or_else(Failure::protocol)?;
                if count > 1000 {
                    return Err(Failure::new(413, "github_threads_too_large"));
                }
                if total
                    .replace(count)
                    .is_some_and(|previous| previous != count)
                {
                    return Err(Failure::new(409, "github_threads_changed"));
                }
                let nodes = connection["nodes"]
                    .as_array()
                    .ok_or_else(Failure::protocol)?;
                if nodes.len() > 50 {
                    return Err(Failure::protocol());
                }
                for thread in nodes {
                    let id = r::text(thread, "id")?;
                    if !ids.insert(id.to_owned()) {
                        return Err(Failure::protocol());
                    }
                    let item = project(
                        thread,
                        pull_node,
                        &pull_id,
                        q.number,
                        repository_node,
                        1,
                        10,
                    )?;
                    comments += item["comments"]["items"]
                        .as_array()
                        .ok_or_else(Failure::protocol)?
                        .len();
                    bytes += serde_json::to_vec(&item)
                        .map_err(|_| Failure::protocol())?
                        .len();
                    if all.len() >= 1000 || comments > 1000 || bytes > 8 * 1024 * 1024 {
                        return Err(Failure::new(413, "github_threads_too_large"));
                    }
                    all.push(item);
                }
                let following = r::graphql_continuation(connection)?;
                if following.is_none() {
                    if all.len() as u64 != count {
                        return Err(Failure::new(409, "github_threads_changed"));
                    }
                    break;
                }
                if nodes.is_empty()
                    || ids.len() >= 1000
                    || !cursors.insert(following.clone().ok_or_else(Failure::protocol)?)
                {
                    return Err(Failure::protocol());
                }
                after = following;
            }
            let start = (q.page as usize - 1)
                .checked_mul(q.limit as usize)
                .ok_or_else(Failure::invalid)?;
            if start >= all.len() && !(q.page == 1 && all.is_empty()) {
                return Err(Failure::invalid());
            }
            let end = all.len().min(start + q.limit as usize);
            next = if end < all.len() { q.page + 1 } else { 0 };
            projected.extend(all.into_iter().skip(start).take(end - start));
        }
        let current = self.raw_subject(true, q.number).await?;
        if r::id(&current["id"])? != pull_id || current["node_id"] != pull_node {
            return Err(Failure::new(409, "github_threads_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":q.repository,"name":self.name,"pull":{"kind":"pull","external_id":pull_id,"number":q.number},"items":projected,"next_page":next}),
        )
    }
}

fn project(
    thread: &Value,
    pull: &str,
    pull_id: &str,
    number: u64,
    repository: &str,
    page: u32,
    limit: u32,
) -> Result<Value> {
    let id = r::text(thread, "id")?;
    if !node_id(id)
        || id.bytes().all(|b| b.is_ascii_digit())
        || thread["pullRequest"]["id"] != pull
        || r::big_id(&thread["pullRequest"]["fullDatabaseId"])? != pull_id
        || thread["pullRequest"]["number"].as_u64() != Some(number)
        || thread["pullRequest"]["repository"]["id"] != repository
        || thread["repository"]["id"] != repository
    {
        return Err(Failure::new(409, "github_thread_scope_changed"));
    }
    let resolved = thread["isResolved"]
        .as_bool()
        .ok_or_else(Failure::protocol)?;
    let outdated = thread["isOutdated"]
        .as_bool()
        .ok_or_else(Failure::protocol)?;
    let can_resolve = thread["viewerCanResolve"]
        .as_bool()
        .ok_or_else(Failure::protocol)?;
    let can_unresolve = thread["viewerCanUnresolve"]
        .as_bool()
        .ok_or_else(Failure::protocol)?;
    let connection = &thread["comments"];
    let nodes = connection["nodes"]
        .as_array()
        .ok_or_else(Failure::protocol)?;
    let total = connection["totalCount"]
        .as_u64()
        .ok_or_else(Failure::protocol)?;
    let start = (page as u64 - 1) * limit as u64;
    if nodes.len() > limit as usize
        || start >= total && !(page == 1 && total == 0)
        || start + nodes.len() as u64 > total
    {
        return Err(Failure::protocol());
    }
    let more = r::graphql_continuation(connection)?.is_some();
    if more != (start + (nodes.len() as u64) < total) || more && nodes.is_empty() {
        return Err(Failure::protocol());
    }
    let mut seen = HashSet::new();
    let mut items = Vec::new();
    for comment in nodes {
        let comment_id = r::big_id(&comment["fullDatabaseId"])?;
        if !seen.insert(comment_id.clone())
            || !r::text(comment, "id").is_ok_and(node_id)
            || comment["pullRequest"]["id"] != pull
            || comment["repository"]["id"] != repository
        {
            return Err(Failure::protocol());
        }
        let author = if comment["author"].is_null() || comment["author"]["databaseId"].is_null() {
            Value::Null
        } else {
            json!({"external_id":r::id(&comment["author"]["databaseId"])?,"login":r::nonempty(&comment["author"],"login")?})
        };
        let review = if comment["pullRequestReview"].is_null() {
            Value::Null
        } else {
            json!(r::big_id(&comment["pullRequestReview"]["fullDatabaseId"])?)
        };
        let dates = json!({"created_at":comment["createdAt"],"updated_at":comment["updatedAt"]});
        items.push(json!({"external_id":comment_id,"review_id":review,"author":author,"body":r::text(comment,"body")?,
            "created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?}));
    }
    // These are native stored coordinates, never a proven two-commit diff pair.
    let roots = thread["root"]["nodes"]
        .as_array()
        .ok_or_else(Failure::protocol)?;
    if roots.len() > 1 {
        return Err(Failure::protocol());
    }
    let root = roots.first().unwrap_or(&Value::Null);
    let location = match (
        root["originalCommit"]["oid"].as_str(),
        root["originalLine"].as_u64(),
        root["path"].as_str(),
        thread["diffSide"].as_str(),
    ) {
        (Some(oid), Some(line), Some(path), Some(side @ ("LEFT" | "RIGHT")))
            if commit(oid)
                && line > 0
                && line <= 9_007_199_254_740_991
                && !path.is_empty()
                && path.len() <= 4096
                && !path.contains('\0')
                && root["replyTo"].is_null()
                && thread["path"] == path =>
        {
            json!({"commit":oid,"path":path,"side":if side=="LEFT"{"old"}else{"new"},"line":line})
        }
        _ => Value::Null,
    };
    Ok(
        json!({"external_id":id,"recorded_location":location,"resolution":if resolved{"resolved"}else{"unresolved"},"outdated":outdated,
        "viewer_can_resolve":can_resolve,"viewer_can_unresolve":can_unresolve,"comments":{"items":items,"next_page":if more{page+1}else{0}}}),
    )
}
