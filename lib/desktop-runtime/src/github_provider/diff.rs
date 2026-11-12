use super::reads::{paging, unique, Reader};
use super::request::commit;
use super::{records as r, Failure, Result};
use serde_json::{json, Value};
use std::collections::HashSet;

pub(super) struct DiffScope {
    number: u64,
    before: Value,
    pub(super) scope: Value,
    pub(super) total: Option<u64>,
}
impl Reader<'_> {
    pub(super) async fn merge_base(&self, base: &str, head: &str) -> Result<String> {
        if !commit(base) || !commit(head) {
            return Err(Failure::invalid());
        }
        let comparison = self
            .get(
                &format!("/compare/{base}...{head}"),
                &[("page", "2".into()), ("per_page", "1".into())],
            )
            .await?
            .value;
        let merge_base = r::text(&comparison["merge_base_commit"], "sha")?;
        if comparison["base_commit"]["sha"] != base || !commit(merge_base) {
            return Err(Failure::protocol());
        }
        Ok(merge_base.into())
    }

    pub(super) async fn diff(&self, q: &Value) -> Result<Value> {
        let captured = self.open_diff(q).await?;
        let result = if q["kind"] == "pull_files" {
            let (page, limit) = paging(q);
            self.diff_files(&captured, page, limit).await?
        } else {
            captured.scope.clone()
        };
        self.close_diff(&captured).await?;
        Ok(result)
    }
    pub(super) async fn open_diff(&self, q: &Value) -> Result<DiffScope> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let pull = self.raw_subject(true, number).await?;
        let before = self.diff_state(&pull)?;
        if q["head"] != "" && q["head"] != before["head"] {
            return Err(Failure::new(409, "github_diff_changed"));
        }
        let merge_base = self
            .merge_base(r::text(&before, "base")?, r::text(&before, "head")?)
            .await?;
        if q["base"] != "" && q["base"] != merge_base {
            return Err(Failure::new(409, "github_diff_changed"));
        }
        let scope = json!({"repository":self.repository,"name":self.name,"pull":{"kind":"pull","external_id":before["pull"],"number":number},"base":merge_base,"head":before["head"],"pull_base":before["base"],"base_branch":before["branch"]});
        let total = if q["kind"] == "pull_files" {
            let total = pull["changed_files"]
                .as_u64()
                .ok_or_else(Failure::unavailable)?;
            if total > 3000 {
                return Err(Failure::new(413, "github_files_too_large"));
            }
            Some(total)
        } else {
            None
        };
        Ok(DiffScope {
            number,
            before,
            scope,
            total,
        })
    }
    pub(super) async fn diff_files(
        &self,
        captured: &DiffScope,
        page: u32,
        limit: u32,
    ) -> Result<Value> {
        if page == 0 || limit == 0 || limit > 100 {
            return Err(Failure::invalid());
        }
        let number = captured.number;
        let total = captured.total.ok_or_else(Failure::invalid)?;
        let offset = (page as u64 - 1) * limit as u64;
        if offset >= total && !(page == 1 && total == 0) {
            return Err(Failure::invalid());
        }
        let response = self
            .get(
                &format!("/pulls/{number}/files"),
                &[("page", page.to_string()), ("per_page", limit.to_string())],
            )
            .await?;
        let want = (limit as u64).min(total - offset);
        let next = if offset + want < total { page + 1 } else { 0 };
        let items = response.value.as_array().ok_or_else(Failure::protocol)?;
        if items.len() as u64 != want
            || response.next != next
            || response.last != 0 && response.last as u64 != total.div_ceil(limit as u64)
        {
            return Err(Failure::protocol());
        }
        let mut seen = HashSet::new();
        let mut projected = Vec::new();
        for item in items {
            unique(&mut seen, r::text(item, "filename")?)?;
            projected.push(r::file(item)?);
        }
        Ok(
            json!({"diff":captured.scope,"items":projected,"page":page,"limit":limit,"total":total,"next_page":next}),
        )
    }
    pub(super) async fn close_diff(&self, captured: &DiffScope) -> Result<()> {
        let after = self.raw_subject(true, captured.number).await?;
        if self.diff_state(&after)? != captured.before
            || captured
                .total
                .is_some_and(|total| after["changed_files"].as_u64() != Some(total))
        {
            return Err(Failure::new(409, "github_diff_changed"));
        }
        self.recheck().await
    }
    fn diff_state(&self, pull: &Value) -> Result<Value> {
        let base = r::text(&pull["base"], "sha")?;
        let head = r::text(&pull["head"], "sha")?;
        let branch = r::nonempty(&pull["base"], "ref")?;
        if !commit(base) || !commit(head) {
            return Err(Failure::unavailable());
        }
        Ok(
            json!({"repository":self.repository,"pull":r::id(&pull["id"])?,"base":base,"head":head,"branch":branch}),
        )
    }
}
