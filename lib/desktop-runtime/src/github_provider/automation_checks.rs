//! Finite paired review/CI observations for an owned same-repository PR. GitHub facts
//! stay here; Go owns required-check policy, assessment and canonical revisions.
use super::{
    reads::{page_items, Reader},
    records as r,
    request::{branch_name, commit},
    Failure, Result,
};
use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

fn incomplete() -> Failure {
    Failure::new(409, "github_automation_checks_incomplete")
}
fn check_state(v: &Value) -> &'static str {
    if v["status"] != "completed" {
        return "pending";
    }
    match v["conclusion"].as_str() {
        Some("success") => "success",
        Some("failure" | "timed_out" | "action_required" | "stale" | "startup_failure") => {
            "failure"
        }
        Some("cancelled") => "cancelled",
        Some("skipped") => "skipped",
        Some("neutral") => "warning",
        _ => "unknown",
    }
}
// Every latest provider with the same context must pass. Never let a green
// check hide a red commit status (or another app's check) with that name.
fn severity(state: &str) -> usize {
    [
        "success",
        "pending",
        "unknown",
        "warning",
        "skipped",
        "cancelled",
        "failure",
        "error",
    ]
    .iter()
    .position(|s| *s == state)
    .unwrap_or(2)
}
impl Reader<'_> {
    pub(super) async fn automation_checks(&self, number: u64) -> Result<Value> {
        let before = self.automation_check_snapshot(number).await?;
        self.recheck().await?;
        if self.automation_check_snapshot(number).await? != before {
            return Err(Failure::new(409, "github_automation_checks_changed"));
        }
        Ok(before)
    }
    async fn automation_branch_tip(&self, name: &str) -> Result<String> {
        if !branch_name(name) {
            return Err(Failure::protocol());
        }
        let mut url = url::Url::parse("https://api.github.com/").map_err(|_| Failure::invalid())?;
        url.path_segments_mut()
            .map_err(|_| Failure::invalid())?
            .push(name);
        let branch = self
            .get(
                &format!("/branches/{}", url.path().trim_start_matches('/')),
                &[],
            )
            .await?
            .value;
        let oid = r::nonempty(&branch["commit"], "sha")?;
        if branch["name"] != name || !commit(oid) {
            return Err(Failure::protocol());
        }
        Ok(oid.into())
    }
    pub(super) async fn automation_review(&self, number: u64) -> Result<Value> {
        let before = self.automation_review_snapshot(number).await?;
        self.recheck().await?;
        if self.automation_review_snapshot(number).await? != before {
            return Err(Failure::new(409, "github_automation_review_changed"));
        }
        Ok(before)
    }
    async fn automation_review_snapshot(&self, number: u64) -> Result<Value> {
        let pull = self.raw_subject(true, number).await?;
        let head = r::nonempty(&pull["head"], "sha")?;
        let base = r::nonempty(&pull["base"], "sha")?;
        let head_ref = r::nonempty(&pull["head"], "ref")?;
        let base_ref = r::nonempty(&pull["base"], "ref")?;
        if pull["state"] != "open"
            || pull["merged"] != false
            || pull["draft"] != false
            || r::id(&pull["head"]["repo"]["id"])? != self.repository
            || r::id(&pull["base"]["repo"]["id"])? != self.repository
            || !commit(head)
            || !commit(base)
            || !branch_name(head_ref)
            || !branch_name(base_ref)
            || head_ref == base_ref
        {
            return Err(Failure::new(409, "github_automation_pull_unavailable"));
        }
        let issue = self.raw_subject(false, number).await?;
        if issue["pull_request"]["url"] != self.url(&format!("/pulls/{number}")) {
            return Err(Failure::protocol());
        }
        let head_tip = self.automation_branch_tip(head_ref).await?;
        let base_tip = self.automation_branch_tip(base_ref).await?;
        if head_tip != head || base_tip != base {
            return Err(Failure::new(409, "github_automation_checks_changed"));
        }
        Ok(
            json!({"repository":self.repository,"number":number,"pull_id":r::id(&pull["id"])?,"issue_id":r::id(&issue["id"])?,"author":r::actor(&pull["user"])?,"head_ref":format!("refs/heads/{head_ref}"),"base_ref":format!("refs/heads/{base_ref}"),"head_tip":head_tip,"base_tip":base_tip}),
        )
    }
    pub(super) async fn latest_check_sources(&self, head: &str) -> Result<Vec<Value>> {
        let checks_path = format!("/commits/{head}/check-runs");
        let statuses_path = format!("/commits/{head}/statuses");
        let check_query = [
            ("per_page", "100".into()),
            ("page", "1".into()),
            ("filter", "latest".into()),
        ];
        let status_query = [("per_page", "100".into()), ("page", "1".into())];
        let (checks, statuses) = tokio::try_join!(
            self.get(&checks_path, &check_query),
            self.get(&statuses_path, &status_query)
        )?;
        let count = checks.value["total_count"]
            .as_u64()
            .ok_or_else(Failure::protocol)?;
        let mut check_page = checks;
        check_page.value = check_page.value["check_runs"].take();
        let check_items = page_items(&check_page, 1, 100, true)?;
        if check_page.next != 0 || count != check_items.len() as u64 {
            return Err(incomplete());
        }
        let status_items = page_items(&statuses, 1, 100, true)?;
        if statuses.next != 0 {
            return Err(incomplete());
        }
        let mut sources = Vec::new();
        let mut check_names = BTreeSet::new();
        let mut ids = BTreeSet::new();
        for item in check_items {
            let id = r::id(&item["id"])?;
            let context = r::nonempty(item, "name")?;
            let app = r::id(&item["app"]["id"])?;
            if !ids.insert(id.clone())
                || item["head_sha"] != head
                || item["url"] != self.url(&format!("/check-runs/{id}"))
            {
                return Err(Failure::protocol());
            }
            if !check_names.insert((context.to_owned(), app.clone())) {
                return Err(incomplete());
            }
            sources.push(json!({"kind":"check","id":id,"context":context,"app":app,"state":check_state(item),"provider_status":r::string(item,"status")?,"provider_conclusion":r::string(item,"conclusion")?}));
        }
        let mut latest = BTreeMap::new();
        ids.clear();
        for item in status_items {
            let id = r::id(&item["id"])?;
            let context = r::nonempty(item, "context")?;
            if !ids.insert(id.clone()) || item["url"] != self.url(&format!("/statuses/{head}")) {
                return Err(Failure::protocol());
            }
            let created = OffsetDateTime::parse(r::nonempty(item, "created_at")?, &Rfc3339)
                .map_err(|_| Failure::protocol())?;
            let order = (
                created.unix_timestamp_nanos(),
                id.parse::<i64>().map_err(|_| Failure::protocol())?,
            );
            let state = match item["state"].as_str() {
                Some(s @ ("pending" | "success" | "failure" | "error")) => s,
                _ => "unknown",
            };
            let source = json!({"kind":"status","id":id,"context":context,"state":state});
            if latest
                .get(context)
                .is_none_or(|(previous, _)| order > *previous)
            {
                latest.insert(context.to_owned(), (order, source));
            }
        }
        sources.extend(latest.into_values().map(|(_, source)| source));
        sources.sort_by_key(|s| {
            (
                s["kind"].as_str().unwrap_or("").to_owned(),
                s["id"].as_str().unwrap_or("").to_owned(),
            )
        });
        Ok(sources)
    }
    pub(super) async fn automation_check_snapshot(&self, number: u64) -> Result<Value> {
        let mut snapshot = self.automation_review_snapshot(number).await?;
        let head = r::text(&snapshot, "head_tip")?;
        let sources = self.latest_check_sources(head).await?;
        let mut contexts = BTreeMap::<String, String>::new();
        for source in &sources {
            let context = r::nonempty(source, "context")?;
            let state = r::nonempty(source, "state")?;
            if context.len() > 256 || context.chars().any(char::is_control) {
                return Err(incomplete());
            }
            let entry = contexts
                .entry(context.into())
                .or_insert_with(|| state.into());
            if severity(state) > severity(entry) {
                *entry = state.into();
            }
        }
        if contexts.len() > 50 {
            return Err(incomplete());
        }
        let checks: Vec<_> = contexts
            .into_iter()
            .map(|(context, state)| json!({"context":context,"state":state}))
            .collect();
        snapshot["checks"] = json!(checks);
        snapshot["sources"] = json!(sources);
        snapshot
            .as_object_mut()
            .ok_or_else(Failure::protocol)?
            .remove("author");
        Ok(snapshot)
    }
}
