use super::{
    reads::Reader,
    records as r,
    request::{commit, node_id},
    Failure, Result,
};
use serde_json::{json, Value};

impl Reader<'_> {
    pub(super) async fn write_capabilities(
        &self,
        number: Option<u32>,
        pull: bool,
    ) -> Result<Value> {
        let repository = self.raw_repository.clone();
        let projected = self.write_capabilities_for_repository(&repository, number, pull).await?;
        Ok(projected)
    }
    pub(super) async fn write_capabilities_for_repository(
        &self,
        repository: &Value,
        number: Option<u32>,
        pull: bool,
    ) -> Result<Value> {
        let repo = r::text(&repository, "node_id")?;
        if r::id(&repository["id"])? != self.repository || !node_id(repo) {
            return Err(Failure::protocol());
        }
        // Repository capabilities are all in the repository GitHub already
        // returned; only a subject's viewer capabilities need a query.
        if number.is_none() {
            return rest_repository_capabilities(self.repository, &self.name, repository);
        }
        let subject=match number{
            Some(_) if pull=>"subject:pullRequest(number:$number){id fullDatabaseId number state locked isDraft merged headRefOid baseRefOid viewerCanUpdate viewerCanClose viewerCanReopen viewerDidAuthor mergeStateStatus reviews(first:1,states:[PENDING]){totalCount nodes{body viewerDidAuthor}}}",
            Some(_)=>"subject:issue(number:$number){id fullDatabaseId number state locked viewerCanUpdate viewerCanClose viewerCanReopen viewerDidAuthor}",
            None=>"",
        };
        let query=format!("query FactoryWriteCapabilities($repository:ID!{}){{node(id:$repository){{__typename ... on Repository{{id nameWithOwner isArchived isDisabled hasIssuesEnabled viewerCanCreateIssues viewerPermission mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed {subject}}}}}}}",if number.is_some(){",$number:Int!"}else{""});
        let mut variables = json!({"repository":repo});
        if let Some(number) = number {
            variables["number"] = json!(number);
        }
        let token = zeroize::Zeroizing::new(self.token.to_vec());
        let value = self
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
            .await?
            .value;
        let raw = &value["data"]["node"];
        if raw["__typename"] != "Repository"
            || raw["id"] != repo
            || raw["nameWithOwner"] != self.name
        {
            return Err(Failure::new(409, "github_repository_changed"));
        }
        let target = if let Some(number) = number {
            let target = &raw["subject"];
            if target.is_null() {
                return Err(Failure::new(404, "github_not_visible"));
            }
            if !r::text(target, "id").is_ok_and(node_id)
                || target["number"].as_u64() != Some(number.into())
            {
                return Err(Failure::protocol());
            }
            if pull
                && (!r::text(target, "headRefOid").is_ok_and(commit)
                    || !r::text(target, "baseRefOid").is_ok_and(commit))
            {
                return Err(Failure::protocol());
            }
            // GitHub refuses summary edits on inline-only reviews. Empty text
            // cannot establish that a summary record exists, even after a clear.
            let draft_review = if pull {
                let reviews = &target["reviews"];
                match reviews["totalCount"].as_u64() {
                    Some(0) if reviews["nodes"].as_array().is_some_and(Vec::is_empty) => json!(true),
                    Some(1) if reviews["nodes"].as_array().is_some_and(|n| n.len() == 1)
                        && reviews["nodes"][0]["viewerDidAuthor"] == true => {
                            json!(!r::text(&reviews["nodes"][0], "body")?.is_empty())
                        }
                    _ => Value::Null,
                }
            } else { Value::Null };
            json!({"draft_review":draft_review,"inline_publish":if pull { target["reviews"]["totalCount"].as_u64().map(|n| n == 0) } else { None },"kind":if pull{"pull"}else{"issue"},"external_id":r::big_id(&target["fullDatabaseId"])?,"number":number,"state":r::text(target,"state")?.to_ascii_lowercase(),"locked":r::boolean(target,"locked")?,"can_update":r::boolean(target,"viewerCanUpdate")?,"can_close":r::boolean(target,"viewerCanClose")?,"can_reopen":r::boolean(target,"viewerCanReopen")?,"own":r::boolean(target,"viewerDidAuthor")?,"draft":target["isDraft"],"merged":target["merged"],"head":target["headRefOid"],"base":target["baseRefOid"],"merge_ready":target["mergeStateStatus"]=="CLEAN"})
        } else {
            Value::Null
        };
        Ok(
            json!({"repository":self.repository,"name":self.name,"archived":r::boolean(raw,"isArchived")?,"disabled":r::boolean(raw,"isDisabled")?,"issues_enabled":r::boolean(raw,"hasIssuesEnabled")?,"can_create_issues":r::boolean(raw,"viewerCanCreateIssues")?,"writer":matches!(raw["viewerPermission"].as_str(),Some("WRITE"|"MAINTAIN"|"ADMIN")),"merge":r::boolean(raw,"mergeCommitAllowed")?,"squash":r::boolean(raw,"squashMergeAllowed")?,"rebase":r::boolean(raw,"rebaseMergeAllowed")?,"subject":target}),
        )
    }
}

fn rest_repository_capabilities(id: &str, name: &str, repository: &Value) -> Result<Value> {
    let archived = r::boolean(repository, "archived")?;
    let issues = r::boolean(repository, "has_issues")?;
    Ok(json!({"repository":id,"name":name,"archived":archived,"disabled":r::boolean(repository,"disabled")?,
        "issues_enabled":issues,"can_create_issues":issues == true && archived == false,
        "writer":repository["permissions"]["push"] == true,"merge":r::boolean(repository,"allow_merge_commit")?,
        "squash":r::boolean(repository,"allow_squash_merge")?,"rebase":r::boolean(repository,"allow_rebase_merge")?,"subject":Value::Null}))
}

impl Reader<'_> {
    /// A pull's capabilities from the REST pull and repository already read.
    /// Pending-review facts are not in them and stay unobserved.
    pub(super) async fn pull_capabilities_from_rest(&self, pull: &Value) -> Result<Value> {
        let account = self.client.identity(self.token).await?;
        let mut projected = rest_repository_capabilities(self.repository, &self.name, &self.raw_repository)?;
        let head = r::text(&pull["head"], "sha")?;
        let base = r::text(&pull["base"], "sha")?;
        if !commit(head) || !commit(base) {
            return Err(Failure::protocol());
        }
        let own = r::id(&pull["user"]["id"])? == account.external_id;
        let writer = projected["writer"] == true;
        projected["subject"] = json!({"draft_review":Value::Null,"inline_publish":Value::Null,"kind":"pull",
            "external_id":r::id(&pull["id"])?,"number":pull["number"],"state":r::text(pull,"state")?,
            "locked":r::boolean(pull,"locked")?,"can_update":writer || own,"can_close":writer || own,"can_reopen":writer || own,
            "own":own,"draft":r::boolean(pull,"draft")?,"merged":r::boolean(pull,"merged")?,"head":head,"base":base,
            "merge_ready":pull["mergeable_state"] == "clean"});
        Ok(projected)
    }
}
