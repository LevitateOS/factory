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

// These endpoints provide an exact count. Missing/truncated page data or a
// skipped continuation must not become a falsely complete workflow/job read.
fn counted_page(
    response: &super::transport::Response,
    page: u32,
    limit: u32,
    count: u64,
) -> Result<&Vec<Value>> {
    let items = page_items(response, page, limit, true)?;
    let start = u64::from(page - 1) * u64::from(limit);
    let expected = count.saturating_sub(start).min(u64::from(limit));
    let next = if start + (items.len() as u64) < count {
        page + 1
    } else {
        0
    };
    if items.len() as u64 != expected || response.next != next {
        return Err(Failure::protocol());
    }
    Ok(items)
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
        self.workflow_runs_page(q, true).await
    }
    async fn workflow_runs_page(&self, q: &Value, recheck: bool) -> Result<Value> {
        let (page, limit) = paging(q);
        let head = r::text(q, "head")?;
        let mut params = vec![("page", page.to_string()), ("per_page", limit.to_string())];
        if !head.is_empty() {
            params.push(("head_sha", head.into()));
        }
        let mut response = self.get("/actions/runs", &params).await?;
        let count = total(&response.value)?;
        // GitHub caps search-filtered workflow runs at 1,000. Refuse a larger
        // observed total rather than presenting the truncated search as all runs.
        if !head.is_empty() && count > 1000 {
            return Err(Failure::new(413, "github_workflows_too_large"));
        }
        response.value = response.value["workflow_runs"].take();
        let mut items = Vec::new();
        let mut seen = HashSet::new();
        for value in counted_page(&response, page, limit, count)? {
            let run = self.workflow(value)?;
            unique(&mut seen, r::text(&run, "external_id")?)?;
            if !head.is_empty() && run["head"] != head {
                return Err(Failure::protocol());
            }
            items.push(run);
        }
        if recheck {
            self.recheck().await?;
        }
        Ok(
            json!({"repository":self.repository,"name":self.name,"head":head,"items":items,"next_page":response.next,"total":count}),
        )
    }

    pub(super) async fn workflow_jobs(&self, q: &Value) -> Result<Value> {
        self.workflow_jobs_page(q, true).await
    }
    async fn workflow_jobs_page(&self, q: &Value, recheck: bool) -> Result<Value> {
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
        for value in counted_page(&response, page, limit, count)? {
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
        if recheck {
            self.recheck().await?;
        }
        Ok(
            json!({"repository":self.repository,"name":self.name,"run":before,"items":items,"next_page":response.next,"total":count}),
        )
    }

    pub(super) async fn pull_workflow_jobs(&self, q: &Value) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let before = self.protection_binding(&self.raw_subject(true, number).await?)?;
        if q["head"] != "" && q["head"] != before["head"] {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        let runs = self
            .workflow_runs_page(
                &json!({"page":q["run_page"],"limit":1,"head":before["head"]}),
                false,
            )
            .await?;
        let items = runs["items"].as_array().ok_or_else(Failure::protocol)?;
        let jobs = if let Some(run) = items.first() {
            if q["run_id"] != ""
                && (q["run_id"] != run["external_id"] || q["attempt"] != run["attempt"])
            {
                return Err(Failure::new(409, "github_checks_changed"));
            }
            Some(self.workflow_jobs_page(&json!({"run_id":run["external_id"],"attempt":run["attempt"],"head":before["head"],"page":q["page"],"limit":q["limit"]}),false).await?)
        } else {
            None
        };
        if self.protection_binding(&self.raw_subject(true, number).await?)? != before {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"pull":before["pull"],"head":before["head"],"base":before["base"],"branch":before["branch"],"runs":runs,"jobs":jobs}),
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

    pub(super) fn protection_binding(&self, raw: &Value) -> Result<Value> {
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

    async fn protection_sources(
        &self,
        branch_target: &str,
        encoded: &str,
    ) -> Result<(Option<Value>, Option<Vec<Value>>)> {
        let classic_path = format!("{branch_target}/protection");
        let rules_path = format!("/rules/branches/{encoded}");
        let rule_query = [("page", "1".into()), ("per_page", "100".into())];
        let (classic, rules) = tokio::try_join!(
            self.optional_protection(&classic_path, &[]),
            self.optional_protection(&rules_path, &rule_query,)
        )?;
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
        Ok((classic, rules))
    }

    pub(super) async fn protection(&self, q: &Value) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let pull = self.raw_subject(true, number).await?;
        let before = self.protection_binding(&pull)?;
        if q["head"] != "" && q["head"] != before["head"] {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        let captured = self.protection_snapshot_for_pull(&pull).await?;
        let pull = self.raw_subject(true, number).await?;
        if self.protection_snapshot_for_pull(&pull).await? != captured {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        self.recheck().await?;
        Ok(captured)
    }

    pub(super) async fn protection_snapshot_for_pull(&self, pull: &Value) -> Result<Value> {
        let before = self.protection_binding(pull)?;
        // Encode this as one path segment; branch slashes must not select a
        // different endpoint or introduce query parameters.
        let encoded = super::reads::branch_segment(r::text(&before, "branch")?)?;
        let branch_target = format!("/branches/{encoded}");
        let (branch, (classic, rules)) = tokio::try_join!(
            self.get(&branch_target, &[]),
            self.protection_sources(&branch_target, &encoded)
        )?;
        let captured_branch = branch_identity(&branch.value, r::text(&before, "branch")?)?;
        let summary = super::protection::summarize(
            &captured_branch["protected"],
            classic.as_ref(),
            rules.as_deref(),
        )?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"pull":before["pull"],"head":before["head"],"base":before["base"],"branch":before["branch"],
            "branch_commit":captured_branch["commit"],"protected":captured_branch["protected"],"classic":classic,"rules":rules,"summary":summary}),
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
    let mut unrepresented = Vec::new();
    if !value["restrictions"].is_null() {
        unrepresented.push("classic_restrictions");
    }
    if !value["required_pull_request_reviews"]["dismissal_restrictions"].is_null() {
        unrepresented.push("classic_dismissal_restrictions");
    }
    if !value["required_pull_request_reviews"]["bypass_pull_request_allowances"].is_null() {
        unrepresented.push("classic_review_bypass");
    }
    for key in ["lock_branch", "block_creations"] {
        if value[key]["enabled"] == true {
            unrepresented.push(key);
        }
    }
    let mut result = json!({"required_status_checks":status,"required_pull_request_reviews":reviews,"unrepresented":unrepresented});
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
