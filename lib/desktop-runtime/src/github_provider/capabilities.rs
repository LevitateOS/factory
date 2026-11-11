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
        let repository = self.get("", &[]).await?.value;
        let repo = r::text(&repository, "node_id")?;
        if r::id(&repository["id"])? != self.repository || !node_id(repo) {
            return Err(Failure::protocol());
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
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"archived":r::boolean(raw,"isArchived")?,"disabled":r::boolean(raw,"isDisabled")?,"issues_enabled":r::boolean(raw,"hasIssuesEnabled")?,"can_create_issues":r::boolean(raw,"viewerCanCreateIssues")?,"writer":matches!(raw["viewerPermission"].as_str(),Some("WRITE"|"MAINTAIN"|"ADMIN")),"merge":r::boolean(raw,"mergeCommitAllowed")?,"squash":r::boolean(raw,"squashMergeAllowed")?,"rebase":r::boolean(raw,"rebaseMergeAllowed")?,"subject":target}),
        )
    }
}
