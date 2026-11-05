//! Native provider facts. Run/attempt, branch and ruleset identities are kept
//! separately; none of these observations is a product merge/admission verdict.
use super::reads::{page_items, paging, unique, Reader};
use super::request::commit;
use super::{records as r, Failure, Result};
use serde_json::{json, Value};
use std::collections::HashSet;

fn total(value: &Value) -> Result<u64> {
    value["total_count"]
        .as_u64()
        .filter(|n| *n <= i64::MAX as u64)
        .ok_or_else(Failure::protocol)
}

impl Reader<'_> {
    fn workflow(&self, value: &Value) -> Result<Value> {
        let id = r::id(&value["id"])?;
        let attempt = r::id(&value["run_attempt"])?;
        let head = r::nonempty(value, "head_sha")?;
        if r::id(&value["repository"]["id"])? != self.repository || !commit(head) {
            return Err(Failure::new(502, "invalid_github_workflow_scope"));
        }
        if value["url"] != self.url(&format!("/actions/runs/{id}")) {
            return Err(Failure::new(502, "invalid_github_workflow_url"));
        }
        if value["jobs_url"] != self.url(&format!("/actions/runs/{id}/jobs"))
            && value["jobs_url"] != self.url(&format!("/actions/runs/{id}/attempts/{attempt}/jobs"))
        {
            return Err(Failure::new(502, "invalid_github_workflow_jobs_url"));
        }
        let status = r::string(value, "status")?;
        let conclusion = r::string(value, "conclusion")?;
        Ok(json!({
            "external_id":id,"run_number":r::id(&value["run_number"])?,
            "attempt":attempt,"workflow_id":r::id(&value["workflow_id"])?,
            "name":r::string(value,"name")?,"path":r::nonempty(value,"path")?,
            "head":head,"head_branch":r::string(value,"head_branch")?,
            "head_repository":if value["head_repository"].is_null(){Value::Null}else{json!({"external_id":r::id(&value["head_repository"]["id"])?,"name":r::nonempty(&value["head_repository"],"full_name")?})},
            "event":r::nonempty(value,"event")?,"provider_status":status,"provider_conclusion":conclusion,
            "state":r::check_state(&status),"conclusion":r::check_conclusion(&conclusion),
            "created_at":r::date(value,"created_at")?,"updated_at":r::date(value,"updated_at")?,
            "started_at":r::date(value,"run_started_at")?,"actor":r::actor(&value["actor"])?,
            "triggering_actor":r::actor(&value["triggering_actor"])?,"details_url":r::string(value,"html_url")?
        }))
    }

    pub(super) async fn workflow_runs(&self, q: &Value) -> Result<Value> {
        let (page, limit) = paging(q);
        let head = r::text(q, "head")?;
        let mut params = vec![("page", page.to_string()), ("per_page", limit.to_string())];
        if !head.is_empty() {
            params.push(("head_sha", head.into()));
        }
        let mut response = self.get("/actions/runs", &params).await?;
        let count = total(&response.value)?;
        response.value = response.value["workflow_runs"].take();
        let mut items = Vec::new();
        let mut seen = HashSet::new();
        for value in page_items(&response, page, limit, true)? {
            let run = self.workflow(value)?;
            unique(&mut seen, r::text(&run, "external_id")?)?;
            if !head.is_empty() && run["head"] != head {
                return Err(Failure::protocol());
            }
            items.push(run);
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"head":head,"items":items,"next_page":response.next,"total":count}),
        )
    }

    pub(super) async fn workflow_jobs(&self, q: &Value) -> Result<Value> {
        let run = r::text(q, "run_id")?;
        let attempt = r::text(q, "attempt")?;
        let head = r::text(q, "head")?;
        let target = format!("/actions/runs/{run}/attempts/{attempt}");
        let before = self
            .workflow(&self.get(&target, &[]).await?.value)
            .map_err(|error| {
                if error.code == "invalid_github_response" {
                    Failure::new(502, "invalid_github_workflow_attempt")
                } else {
                    error
                }
            })?;
        if before["external_id"] != run || before["attempt"] != attempt || before["head"] != head {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        let (page, limit) = paging(q);
        let mut response = self
            .get(
                &format!("{target}/jobs"),
                &[("page", page.to_string()), ("per_page", limit.to_string())],
            )
            .await?;
        let count = total(&response.value)?;
        response.value = response.value["jobs"].take();
        let mut items = Vec::new();
        let mut seen = HashSet::new();
        for value in page_items(&response, page, limit, true)? {
            let id = r::id(&value["id"])?;
            unique(&mut seen, &id)?;
            if r::id(&value["run_id"])? != run
                || value["head_sha"] != head
                || value["run_url"] != self.url(&format!("/actions/runs/{run}"))
                || value["url"] != self.url(&format!("/actions/jobs/{id}"))
                || !value["run_attempt"].is_null() && r::id(&value["run_attempt"])? != attempt
            {
                return Err(Failure::new(502, "invalid_github_job_binding"));
            }
            let status = r::string(value, "status")?;
            let conclusion = r::string(value, "conclusion")?;
            items.push(json!({"external_id":id,"name":r::nonempty(value,"name")?,"head":head,
                "run_id":run,"attempt":attempt,"provider_status":status,"provider_conclusion":conclusion,
                "state":r::check_state(&status),"conclusion":r::check_conclusion(&conclusion),
                "started_at":r::date(value,"started_at")?,"completed_at":r::date(value,"completed_at")?,
                "details_url":r::string(value,"html_url")?,"check_run_url":r::string(value,"check_run_url")?}));
        }
        // Refuse a changed snapshot instead of combining another attempt or
        // mutable run's metadata with these jobs. Callers can retry explicitly.
        if self.workflow(&self.get(&target, &[]).await?.value)? != before {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"run":before,"items":items,"next_page":response.next,"total":count}),
        )
    }

    async fn optional_protection(
        &self,
        target: &str,
        query: &[(&str, String)],
    ) -> Result<Option<super::transport::Response>> {
        match self.get(target, query).await {
            Ok(value) => Ok(Some(value)),
            // A 404 may conceal a rule, not prove that no rule exists.
            Err(error)
                if matches!(error.status, 403 | 404)
                    && matches!(error.code, "github_forbidden" | "github_not_visible") =>
            {
                Ok(None)
            }
            Err(error) => Err(error),
        }
    }

    fn protection_binding(&self, raw: &Value) -> Result<Value> {
        let head = r::nonempty(&raw["head"], "sha")?;
        let base = r::nonempty(&raw["base"], "sha")?;
        let branch = r::nonempty(&raw["base"], "ref")?;
        if !commit(head)
            || !commit(base)
            || matches!(branch, "." | "..")
            || branch.len() > 4096
            || branch.chars().any(char::is_control)
            || r::id(&raw["base"]["repo"]["id"])? != self.repository
        {
            return Err(Failure::protocol());
        }
        Ok(
            json!({"pull":{"kind":"pull","external_id":r::id(&raw["id"])?,"number":raw["number"]},"head":head,"base":base,"branch":branch}),
        )
    }

    pub(super) async fn protection(&self, q: &Value) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let before = self.protection_binding(&self.raw_subject(true, number).await?)?;
        if q["head"] != "" && q["head"] != before["head"] {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        // Encode this as one path segment; branch slashes must not select a
        // different endpoint or introduce query parameters.
        let mut url = url::Url::parse("https://api.github.com/").map_err(|_| Failure::invalid())?;
        url.path_segments_mut()
            .map_err(|_| Failure::invalid())?
            .push(r::text(&before, "branch")?);
        let encoded = url.path().trim_start_matches('/');
        let branch_target = format!("/branches/{encoded}");
        let branch = self.get(&branch_target, &[]).await?.value;
        let captured_branch = branch_identity(&branch, r::text(&before, "branch")?)?;
        let classic = self
            .optional_protection(&format!("{branch_target}/protection"), &[])
            .await?;
        let rules = self
            .optional_protection(
                &format!("/rules/branches/{encoded}"),
                &[("page", "1".into()), ("per_page", "100".into())],
            )
            .await?;
        let classic = classic
            .map(|response| classic_protection(&response.value))
            .transpose()?;
        let rules = rules.map(|response| {
            if response.next!=0 {return Err(Failure::new(413,"github_rules_too_large"));}
            let values = response.value.as_array().filter(|v|v.len()<=100).ok_or_else(Failure::protocol)?;
            values.iter().map(|value| {
                if !(value["parameters"].is_null() || value["parameters"].is_object()) {return Err(Failure::protocol());}
                Ok(json!({"type":r::nonempty(value,"type")?,"ruleset_id":r::id(&value["ruleset_id"])?,
                    "source":r::string(value,"ruleset_source")?,"source_type":r::string(value,"ruleset_source_type")?,
                    "parameters":value["parameters"]}))
            }).collect::<Result<Vec<_>>>()
        }).transpose()?;
        if self.protection_binding(&self.raw_subject(true, number).await?)? != before
            || captured_branch
                != branch_identity(
                    &self.get(&branch_target, &[]).await?.value,
                    r::text(&before, "branch")?,
                )?
        {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"pull":before["pull"],"head":before["head"],"base":before["base"],"branch":before["branch"],
            "branch_commit":captured_branch["commit"],"protected":captured_branch["protected"],"classic":classic,"rules":rules}),
        )
    }
}

fn branch_identity(value: &Value, name: &str) -> Result<Value> {
    let sha = r::nonempty(&value["commit"], "sha")?;
    if value["name"] != name || !commit(sha) {
        return Err(Failure::protocol());
    }
    Ok(json!({"commit":sha,"protected":r::boolean(value,"protected")?}))
}
fn classic_protection(value: &Value) -> Result<Value> {
    let status = &value["required_status_checks"];
    let status = if status.is_null() {
        Value::Null
    } else {
        if !status.is_object() {
            return Err(Failure::protocol());
        }
        let contexts = status["contexts"]
            .as_array()
            .filter(|v| v.len() <= 1000)
            .ok_or_else(Failure::protocol)?;
        if contexts
            .iter()
            .any(|v| v.as_str().is_none_or(str::is_empty))
        {
            return Err(Failure::protocol());
        }
        let checks = if status["checks"].is_null() {
            Value::Null
        } else {
            Value::Array(status["checks"].as_array().filter(|v|v.len()<=1000).ok_or_else(Failure::protocol)?.iter().map(|check|Ok(json!({"context":r::nonempty(check,"context")?,"app_id":r::integer(check,"app_id",-1)?}))).collect::<Result<_>>()?)
        };
        json!({"strict":r::boolean(status,"strict")?,"contexts":contexts,"checks":checks})
    };
    let reviews = &value["required_pull_request_reviews"];
    let reviews = if reviews.is_null() {
        Value::Null
    } else {
        if !reviews.is_object() {
            return Err(Failure::protocol());
        }
        json!({"required_approving_review_count":r::integer(reviews,"required_approving_review_count",0)?,
            "dismiss_stale_reviews":r::boolean(reviews,"dismiss_stale_reviews")?,"require_code_owner_reviews":r::boolean(reviews,"require_code_owner_reviews")?,"require_last_push_approval":r::boolean(reviews,"require_last_push_approval")?})
    };
    let mut result =
        json!({"required_status_checks":status,"required_pull_request_reviews":reviews});
    for key in [
        "enforce_admins",
        "required_signatures",
        "required_linear_history",
        "required_conversation_resolution",
        "allow_force_pushes",
        "allow_deletions",
        "lock_branch",
        "block_creations",
    ] {
        let item = &value[key];
        if !(item.is_null() || item.is_object()) {
            return Err(Failure::protocol());
        }
        result[key] = r::boolean(item, "enabled")?;
    }
    Ok(result)
}
