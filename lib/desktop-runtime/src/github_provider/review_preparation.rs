//! Complete bounded pending-review discovery for the authenticated account.
//! These current facts grant no review permission or atomic head/body condition.
use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id},
    Client, Failure, Result,
};
use serde_json::{json, Value};
use std::collections::HashSet;

const LIST:&str="query FactoryPendingReviews($pull:ID!,$after:String){node(id:$pull){__typename ... on PullRequest{id fullDatabaseId number repository{id} headRefOid reviews(first:50,after:$after,states:[PENDING]){totalCount pageInfo{hasNextPage endCursor} nodes{id state author{__typename ... on User{id}} pullRequest{id} repository{id}}}}}}";
const DETAIL:&str="query FactoryPendingReview($review:ID!){node(id:$review){__typename ... on PullRequestReview{id fullDatabaseId state body author{__typename ... on User{id login}} pullRequest{id headRefOid repository{id}} repository{id} commit{oid repository{id}} comments{totalCount} createdAt updatedAt submittedAt}}}";

pub(super) fn admit_verdict(event: &str, preparation: &Value) -> Result<()> {
    if matches!(event, "approve" | "request_changes")
        && preparation["viewer_is_author"].as_bool() != Some(false)
    {
        return Err(Failure::new(403, "github_review_verdict_unavailable"));
    }
    Ok(())
}
pub(super) enum PreparationQuery {
    List {
        pull: String,
        after: Option<String>,
    },
    Detail {
        review: String,
    },
    Comments {
        review: String,
        after: Option<String>,
    },
}
impl PreparationQuery {
    pub(super) fn wire(&self) -> (&'static str, Value) {
        match self {
            Self::List { pull, after } => (LIST, json!({"pull":pull,"after":after})),
            Self::Detail { review } => (DETAIL, json!({"review":review})),
            Self::Comments { review, after } => (COMMENTS, json!({"review":review,"after":after})),
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

// Two full bodies per page fit the fixed 1-MiB response bound even when 64 KiB
// of control characters need JSON escaping. Aggregate bytes/count and the
// caller's existing whole deadline also bound the complete traversal.
const COMMENTS:&str="query FactoryPendingReviewComments($review:ID!,$after:String){node(id:$review){__typename ... on PullRequestReview{id state author{__typename ... on User{id}} pullRequest{id repository{id} headRefOid} repository{id} comments(first:2,after:$after){totalCount pageInfo{hasNextPage endCursor} nodes{id fullDatabaseId state body author{__typename login ... on Node{id}} pullRequestReview{id} pullRequest{id} repository{id} commit{oid} originalCommit{oid} createdAt updatedAt}}}}}";

struct CommentScope<'a> {
    exclude: Option<&'a str>,
    review: &'a str,
    pull: &'a str,
    repo: &'a str,
    actor: &'a str,
    head: Option<&'a str>,
    pending: bool,
    expected_count: u64,
}
impl Reader<'_> {
    pub(super) async fn pending_review_snapshot(
        &self,
        number: u64,
        actor: &str,
        account: &str,
    ) -> Result<Value> {
        let before = self.review_preparation(number, actor, account).await?;
        self.complete_pending_snapshot(number, actor, account, before)
            .await
    }

    pub(super) async fn complete_pending_snapshot(
        &self,
        number: u64,
        actor: &str,
        account: &str,
        mut before: Value,
    ) -> Result<Value> {
        if before["pending"].is_null() {
            before["comments_snapshot"] = Value::Null;
            before["pending_sha256"] = Value::Null;
            return Ok(before);
        }
        let review = r::text(&before["pending"], "node_id")?;
        let pull = r::text(&before["pull"], "node_id")?;
        let repo = r::text(&before, "repository_node_id")?;
        let snapshot = self
            .review_comment_snapshot(CommentScope {
                exclude: None,
                review,
                pull,
                repo,
                actor,
                head: Some(r::text(&before["pull"], "head")?),
                pending: true,
                expected_count: before["pending"]["comments_count"]
                    .as_u64()
                    .ok_or_else(Failure::protocol)?,
            })
            .await?;
        let current = self.review_preparation(number, actor, account).await?;
        for key in [
            "repository",
            "repository_node_id",
            "actor_node_id",
            "viewer_is_author",
            "archived",
            "disabled",
            "pull",
            "pending",
        ] {
            if current[key] != before[key] {
                return Err(Failure::new(409, "github_pending_review_changed"));
            }
        }
        before["pending_sha256"] = json!(super::transport::sha256(
            &serde_json::to_vec(&before["pending"]).map_err(|_| Failure::protocol())?
        ));
        before["comments_snapshot"] = snapshot;
        Ok(before)
    }

    async fn review_comment_snapshot(&self, scope: CommentScope<'_>) -> Result<Value> {
        let CommentScope {
            exclude,
            review,
            pull,
            repo,
            actor,
            head,
            pending,
            expected_count,
        } = scope;
        let mut after = None;
        let mut cursors = HashSet::new();
        let mut ids = HashSet::new();
        let mut numeric = HashSet::new();
        let mut items = Vec::new();
        let mut total = None;
        let mut bytes = 0usize;
        loop {
            let value = self
                .client
                .review_preparation_query(
                    PreparationQuery::Comments {
                        review: review.into(),
                        after: after.clone(),
                    },
                    self.token,
                )
                .await?;
            let raw = &value["data"]["node"];
            if raw.is_null() {
                return Err(Failure::new(409, "github_pending_review_changed"));
            }
            if raw["__typename"] != "PullRequestReview"
                || raw["id"] != review
                || !if pending {
                    raw["state"] == "PENDING"
                } else {
                    matches!(
                        raw["state"].as_str(),
                        Some("COMMENTED" | "APPROVED" | "CHANGES_REQUESTED" | "DISMISSED")
                    )
                }
                || raw["author"]["__typename"] != "User"
                || raw["author"]["id"] != actor
                || raw["pullRequest"]["id"] != pull
                || raw["pullRequest"]["repository"]["id"] != repo
                || raw["repository"]["id"] != repo
                || head.is_some_and(|head| raw["pullRequest"]["headRefOid"] != head)
            {
                return Err(Failure::new(409, "github_pending_review_changed"));
            }
            let connection = &raw["comments"];
            let count = connection["totalCount"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            if count > 1000 {
                return Err(Failure::new(413, "github_pending_comments_too_large"));
            }
            if total.replace(count).is_some_and(|old| old != count) || count != expected_count {
                return Err(Failure::new(409, "github_pending_comments_changed"));
            }
            let nodes = connection["nodes"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            if nodes.len() > 2 {
                return Err(Failure::protocol());
            }
            for comment in nodes {
                let node = opaque(comment, "id")?;
                if !ids.insert(node.to_owned())
                    || comment["state"] != if pending { "PENDING" } else { "SUBMITTED" }
                    || comment["pullRequestReview"]["id"] != review
                    || comment["pullRequest"]["id"] != pull
                    || comment["repository"]["id"] != repo
                {
                    return Err(Failure::protocol());
                }
                let id = if comment["fullDatabaseId"].is_null() {
                    Value::Null
                } else {
                    let id = r::big_id(&comment["fullDatabaseId"])?;
                    if !numeric.insert(id.clone()) {
                        return Err(Failure::protocol());
                    }
                    json!(id)
                };
                let body = r::text(comment, "body")?;
                if body.len() > 65536 || body.contains('\0') {
                    return Err(Failure::new(413, "github_pending_comment_body_too_large"));
                }
                let author = if comment["author"].is_null() {
                    Value::Null
                } else {
                    let raw = &comment["author"];
                    let kind = r::nonempty(raw, "__typename")?;
                    let login = r::nonempty(raw, "login")?;
                    if kind.len() > 128 || login.len() > 256 || login.chars().any(char::is_control)
                    {
                        return Err(Failure::protocol());
                    }
                    let node = if raw["id"].is_null() {
                        Value::Null
                    } else {
                        json!(opaque(raw, "id")?)
                    };
                    json!({"kind":kind,"node_id":node,"login":login})
                };
                let oid = |key: &str| -> Result<Value> {
                    if comment[key].is_null() {
                        return Ok(Value::Null);
                    }
                    let oid = r::text(&comment[key], "oid")?;
                    if !commit(oid) {
                        return Err(Failure::protocol());
                    }
                    Ok(json!(oid))
                };
                let dates =
                    json!({"created_at":comment["createdAt"],"updated_at":comment["updatedAt"]});
                let item = json!({"node_id":node,"external_id":id,"body":body,"author":author,"commit":oid("commit")?,"original_commit":oid("originalCommit")?,"created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?});
                bytes = bytes
                    .checked_add(
                        serde_json::to_vec(&item)
                            .map_err(|_| Failure::protocol())?
                            .len(),
                    )
                    .ok_or_else(Failure::protocol)?;
                if bytes > 8 * 1024 * 1024 {
                    return Err(Failure::new(413, "github_pending_comments_too_large"));
                }
                items.push(item);
            }
            let next = r::graphql_continuation(connection)?;
            if next.is_none() {
                if items.len() as u64 != count {
                    return Err(Failure::new(409, "github_pending_comments_changed"));
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
        items.sort_by(|a, b| a["node_id"].as_str().cmp(&b["node_id"].as_str()));
        let mut writing = Vec::new();
        for item in &items {
            writing.push(json!({"node_id":item["node_id"],"body_sha256":super::transport::sha256(r::text(item,"body")?.as_bytes())}));
        }
        let mut result = json!({"count":items.len(),"sha256":super::transport::sha256(&serde_json::to_vec(&items).map_err(|_|Failure::protocol())?),"identity_writing_sha256":super::transport::sha256(&serde_json::to_vec(&writing).map_err(|_|Failure::protocol())?),"complete":true,"condition":"bounded_observation"});
        if let Some(node) = exclude {
            let member = items.iter().find(|item| item["node_id"] == node);
            let remaining: Vec<_> = writing
                .iter()
                .filter(|item| item["node_id"] != node)
                .collect();
            result["excluding"] = json!({"node_id":node,"found":member.is_some(),"remaining_count":remaining.len(),"identity_writing_sha256":super::transport::sha256(&serde_json::to_vec(&remaining).map_err(|_|Failure::protocol())?),"member":member.map(|item|json!({"node_id":item["node_id"],"external_id":item["external_id"],"body_sha256":super::transport::sha256(item["body"].as_str().unwrap_or_default().as_bytes()),"author":item["author"],"commit":item["commit"],"original_commit":item["original_commit"],"created_at":item["created_at"],"updated_at":item["updated_at"]}))});
        }
        Ok(result)
    }

    // A current bounded observation only. The product must authenticate its
    // saved causal receipt before using this selector for completion; this read
    // never attributes a matching current comment to a lost mutation reply.
    pub(super) async fn inline_preservation(
        &self,
        number: u64,
        review: &str,
        comment: &str,
        actor: &str,
        account: &str,
    ) -> Result<Value> {
        let mut before = self.review_preparation(number, actor, account).await?;
        if before["pending"]["node_id"] != review {
            return Err(Failure::new(409, "github_inline_pending_review_changed"));
        }
        let snapshot = self
            .review_comment_snapshot(CommentScope {
                exclude: Some(comment),
                review,
                pull: r::text(&before["pull"], "node_id")?,
                repo: r::text(&before, "repository_node_id")?,
                actor,
                head: Some(r::text(&before["pull"], "head")?),
                pending: true,
                expected_count: before["pending"]["comments_count"]
                    .as_u64()
                    .ok_or_else(Failure::protocol)?,
            })
            .await?;
        let current = self.review_preparation(number, actor, account).await?;
        if current != before {
            return Err(Failure::new(409, "github_inline_preservation_changed"));
        }
        before["comments_snapshot"] = snapshot;
        before["condition"] = json!("bounded_observation");
        Ok(before)
    }

    pub(super) async fn review_publication(
        &self,
        number: u64,
        review_id: &str,
        review_node: &str,
        actor: &str,
        account: &str,
    ) -> Result<Value> {
        let subject = self.raw_subject(true, number).await?;
        let pull = opaque(&subject, "node_id")?;
        let repository = self.get("", &[]).await?.value;
        if r::id(&repository["id"])? != self.repository {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let repo = opaque(&repository, "node_id")?;
        let raw = self
            .client
            .review_preparation_query(
                PreparationQuery::Detail {
                    review: review_node.into(),
                },
                self.token,
            )
            .await?;
        let before = publication_detail(
            &raw["data"]["node"],
            review_id,
            review_node,
            pull,
            repo,
            actor,
        )?;
        let snapshot = self
            .review_comment_snapshot(CommentScope {
                exclude: None,
                review: review_node,
                pull,
                repo,
                actor,
                head: None,
                pending: false,
                expected_count: before["comments_count"]
                    .as_u64()
                    .ok_or_else(Failure::protocol)?,
            })
            .await?;
        let raw = self
            .client
            .review_preparation_query(
                PreparationQuery::Detail {
                    review: review_node.into(),
                },
                self.token,
            )
            .await?;
        let current = publication_detail(
            &raw["data"]["node"],
            review_id,
            review_node,
            pull,
            repo,
            actor,
        )?;
        if before != current {
            return Err(Failure::new(409, "github_review_publication_changed"));
        }
        let current_subject = self.raw_subject(true, number).await?;
        if current_subject["node_id"] != pull || current_subject["id"] != subject["id"] {
            return Err(Failure::new(409, "github_review_inputs_changed"));
        }
        self.recheck().await?;
        let identity = self.client.identity(self.token).await?;
        if identity.external_id != account || identity.node_id.as_deref() != Some(actor) {
            return Err(Failure::new(409, "github_account_changed"));
        }
        Ok(
            json!({"repository":self.repository,"name":self.name,"repository_node_id":repo,"actor_node_id":actor,"pull":{"external_id":r::id(&subject["id"])?,"node_id":pull,"number":number},"review":before,"comments_snapshot":snapshot,"observation_only":true}),
        )
    }
    pub(super) async fn review_preparation(
        &self,
        number: u64,
        actor: &str,
        account: &str,
    ) -> Result<Value> {
        if !node_id(actor) {
            return Err(Failure::protocol());
        }
        let (before, repository) =
            tokio::try_join!(self.raw_subject(true, number), self.get("", &[]))?;
        let repository = repository.value;
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
        let (current, (), identity) = tokio::try_join!(
            self.raw_subject(true, number),
            self.recheck(),
            self.client.identity(self.token)
        )?;
        for key in [
            "id", "node_id", "number", "state", "locked", "draft", "merged", "base", "head",
        ] {
            if current[key] != before[key] {
                return Err(Failure::new(409, "github_review_inputs_changed"));
            }
        }

        if identity.external_id != account || identity.node_id.as_deref() != Some(actor) {
            return Err(Failure::new(409, "github_account_changed"));
        }
        let author = r::actor(&before["user"])?;
        let viewer_is_author = if author.is_null() {
            Value::Null
        } else {
            json!(r::text(&author, "external_id")? == account)
        };
        Ok(
            json!({"repository":self.repository,"name":self.name,"repository_node_id":repo,"archived":r::boolean(&repository,"archived")?,"disabled":r::boolean(&repository,"disabled")?,"pull":{"external_id":external,"node_id":pull,"number":number,"state":before["state"],"locked":r::boolean(&before,"locked")?,"draft":r::boolean(&before,"draft")?,"merged":r::boolean(&before,"merged")?,"head":head},"pending":pending,"visible_pending_count":count,"actor_node_id":actor,"viewer_is_author":viewer_is_author}),
        )
    }
}

fn publication_detail(
    raw: &Value,
    id: &str,
    node: &str,
    pull: &str,
    repo: &str,
    actor: &str,
) -> Result<Value> {
    if raw.is_null() {
        return Err(Failure::new(404, "github_not_visible"));
    }
    if raw["__typename"] != "PullRequestReview"
        || raw["id"] != node
        || r::big_id(&raw["fullDatabaseId"])? != id
        || raw["author"]["__typename"] != "User"
        || raw["author"]["id"] != actor
        || raw["pullRequest"]["id"] != pull
        || raw["pullRequest"]["repository"]["id"] != repo
        || raw["repository"]["id"] != repo
        || !matches!(
            raw["state"].as_str(),
            Some("COMMENTED" | "APPROVED" | "CHANGES_REQUESTED" | "DISMISSED")
        )
    {
        return Err(Failure::new(409, "github_review_publication_changed"));
    }
    let body = r::text(raw, "body")?;
    if body.len() > 65536 || body.contains('\0') {
        return Err(Failure::new(413, "github_review_body_too_large"));
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
    let count = raw["comments"]["totalCount"]
        .as_u64()
        .ok_or_else(Failure::protocol)?;
    if count > 1000 {
        return Err(Failure::new(413, "github_pending_comments_too_large"));
    }
    let dates = json!({"created_at":raw["createdAt"],"updated_at":raw["updatedAt"],"submitted_at":raw["submittedAt"]});
    let submitted = r::date(&dates, "submitted_at")?;
    if submitted.is_null() {
        return Err(Failure::protocol());
    }
    Ok(
        json!({"external_id":id,"node_id":node,"state":r::text(raw,"state")?.to_ascii_lowercase(),"body_sha256":super::transport::sha256(body.as_bytes()),"commit":oid,"comments_count":count,"created_at":r::date(&dates,"created_at")?,"updated_at":r::date(&dates,"updated_at")?,"submitted_at":submitted}),
    )
}

impl Client {
    pub(super) async fn review_preparation_query(
        &self,
        query: super::review_preparation::PreparationQuery,
        token: &[u8],
    ) -> super::Result<serde_json::Value> {
        let token = zeroize::Zeroizing::new(token.to_vec());
        Ok(self
            .provider_request(
                move |http| {
                    let (query, variables) = query.wire();
                    Ok((
                        super::transport::graphql(http, &token, query, variables)?,
                        1024 * 1024,
                    ))
                },
                None,
            )
            .await?
            .value)
    }
}
