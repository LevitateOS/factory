//! Current immutable diff selection and own pending-review facts. These are
//! bounded observations, not publication permission or historical thread anchors.
use super::{reads::Reader, records as r, request::commit, Failure, Result};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::HashSet;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct InlineAnchor {
    pub base: String,
    pub head: String,
    pub path: String,
    pub side: String,
    pub scope: String,
    pub start_line: u64,
    pub end_line: u64,
}
fn path(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 4096
        && !value.contains('\0')
        && !value.starts_with('/')
        && value.split('/').all(|p| !matches!(p, "" | "." | ".."))
}
impl InlineAnchor {
    pub fn validate(&self) -> Result<()> {
        if !commit(&self.base)
            || !commit(&self.head)
            || !path(&self.path)
            || !matches!(self.side.as_str(), "old" | "new")
            || !(self.scope == "file" && self.start_line == 0 && self.end_line == 0
                || self.scope == "lines"
                    && self.start_line > 0
                    && self.end_line >= self.start_line
                    && self.end_line <= 9_007_199_254_740_991)
        {
            return Err(Failure::invalid());
        }
        Ok(())
    }
}
impl Reader<'_> {
    pub(super) async fn inline_preparation(
        &self,
        number: u64,
        anchor: &InlineAnchor,
        actor: &str,
        account: &str,
    ) -> Result<Value> {
        anchor.validate()?;
        let mut before = self.pending_review_snapshot(number, actor, account).await?;
        if before["pull"]["head"] != anchor.head {
            return Err(Failure::new(409, "github_inline_selection_changed"));
        }
        let mut page = 1u32;
        let mut total = None;
        let mut scope = None;
        let mut names = HashSet::new();
        let mut bytes = 0usize;
        let mut selected = None;
        loop {
            let files=self.diff(&json!({"kind":"pull_files","number":number,"base":anchor.base,"head":anchor.head,"page":page,"limit":10})).await?;
            let diff = &files["diff"];
            let count = files["total"]
                .as_u64()
                .filter(|n| *n <= 3000)
                .ok_or_else(Failure::protocol)?;
            if total.replace(count).is_some_and(|old| old != count)
                || scope.as_ref().is_some_and(|old| old != diff)
                || diff["base"] != anchor.base
                || diff["head"] != anchor.head
                || diff["pull"]["external_id"] != before["pull"]["external_id"]
                || diff["pull"]["number"].as_u64() != Some(number)
            {
                return Err(Failure::new(409, "github_inline_selection_changed"));
            }
            scope = Some(diff.clone());
            for file in files["items"].as_array().ok_or_else(Failure::protocol)? {
                let old = r::text(file, "old_path")?;
                let new = r::text(file, "new_path")?;
                if !old.is_empty() && !path(old)
                    || !new.is_empty() && !path(new)
                    || old.is_empty() && new.is_empty()
                {
                    return Err(Failure::protocol());
                }
                let identity = if new.is_empty() { old } else { new };
                if !names.insert(identity.to_owned()) {
                    return Err(Failure::new(409, "github_inline_catalogue_changed"));
                }
                bytes = bytes
                    .checked_add(
                        serde_json::to_vec(file)
                            .map_err(|_| Failure::protocol())?
                            .len(),
                    )
                    .ok_or_else(Failure::protocol)?;
                if bytes > 8 * 1024 * 1024 {
                    return Err(Failure::new(413, "github_inline_catalogue_too_large"));
                }
                if (if anchor.side == "old" { old } else { new }) == anchor.path {
                    if selected.replace(file.clone()).is_some() {
                        return Err(Failure::new(409, "github_inline_selection_ambiguous"));
                    }
                }
            }
            let next = files["next_page"].as_u64().ok_or_else(Failure::protocol)?;
            if next == 0 {
                break;
            }
            if next != u64::from(page) + 1 || next > 300 {
                return Err(Failure::protocol());
            }
            page = next as u32;
        }
        if Some(names.len() as u64) != total {
            return Err(Failure::new(409, "github_inline_catalogue_changed"));
        }
        let file =
            selected.ok_or_else(|| Failure::new(409, "github_inline_selection_unavailable"))?;
        if !matches!(
            file["change"].as_str(),
            Some("added" | "deleted" | "modified" | "renamed" | "copied")
        ) {
            return Err(Failure::new(409, "github_inline_selection_unavailable"));
        }
        let writing = selection(&file, anchor)?;
        let after = self.pending_review_snapshot(number, actor, account).await?;
        if after != before {
            return Err(Failure::new(409, "github_inline_preparation_changed"));
        }
        let current_scope = self
            .diff(
                &json!({"kind":"pull_diff","number":number,"base":anchor.base,"head":anchor.head}),
            )
            .await?;
        if scope.as_ref() != Some(&current_scope) {
            return Err(Failure::new(409, "github_inline_selection_changed"));
        }
        before["selection"] = serde_json::to_value(anchor).map_err(|_| Failure::protocol())?;
        before["diff"] = json!({"scope":scope,"file":file,"file_sha256":sha(&file)?,"selection":writing,"catalogue":{"count":names.len(),"complete":true,"condition":"bounded_observation"}});
        before["condition"] = json!("bounded_observation");
        before["selection_sha256"] = json!(super::transport::sha256(
            &serde_json::to_vec(anchor).map_err(|_| Failure::protocol())?
        ));
        before["sha256"] = json!(sha(&before)?);
        Ok(before)
    }
}
fn sha(value: &Value) -> Result<String> {
    Ok(super::transport::sha256(
        &serde_json::to_vec(value).map_err(|_| Failure::protocol())?,
    ))
}
fn selection(file: &Value, anchor: &InlineAnchor) -> Result<Value> {
    if anchor.scope == "file" {
        return Ok(
            json!({"scope":"file","count":0,"sha256":sha(&json!({"scope":"file","old_path":file["old_path"],"new_path":file["new_path"],"change":file["change"]}))?,"source":"provider_file_metadata"}),
        );
    }
    if file["patch_state"] != "complete" {
        return Err(Failure::new(409, "github_inline_patch_unavailable"));
    }
    let raw = r::text(file, "patch")?;
    let mut syntax = format!("--- before\n+++ after\n{raw}");
    if !syntax.ends_with('\n') {
        syntax.push('\n')
    }
    let patch = diffy::Patch::from_str(&syntax).map_err(|_| Failure::protocol())?;
    let mut rows = Vec::new();
    let mut old_end = None;
    let mut new_end = None;
    for hunk in patch.hunks() {
        let old = hunk.old_range();
        let new = hunk.new_range();
        if old_end.is_some_and(|end| old.start() < end)
            || new_end.is_some_and(|end| new.start() < end)
        {
            return Err(Failure::protocol());
        }
        old_end = Some(
            old.start()
                .checked_add(old.len())
                .ok_or_else(Failure::protocol)?,
        );
        new_end = Some(
            new.start()
                .checked_add(new.len())
                .ok_or_else(Failure::protocol)?,
        );
        let mut old = old.start() as u64;
        let mut new = new.start() as u64;
        for line in hunk.lines() {
            let (kind, text, left, right) = match line {
                diffy::Line::Context(text) => ("context", *text, Some(old), Some(new)),
                diffy::Line::Delete(text) => ("deleted", *text, Some(old), None),
                diffy::Line::Insert(text) => ("added", *text, None, Some(new)),
            };
            let selected = if anchor.side == "old" { left } else { right };
            if let Some(number) =
                selected.filter(|n| *n >= anchor.start_line && *n <= anchor.end_line)
            {
                rows.push(json!({"line":number,"kind":kind,"old_line":left,"new_line":right,"text_sha256":super::transport::sha256(text.as_bytes())}))
            }
            if left.is_some() {
                old = old.checked_add(1).ok_or_else(Failure::protocol)?
            }
            if right.is_some() {
                new = new.checked_add(1).ok_or_else(Failure::protocol)?
            }
        }
    }
    let count = anchor.end_line - anchor.start_line + 1;
    if rows.len() as u64 != count
        || rows
            .iter()
            .enumerate()
            .any(|(i, row)| row["line"].as_u64() != Some(anchor.start_line + i as u64))
    {
        return Err(Failure::new(409, "github_inline_selection_unavailable"));
    }
    Ok(
        json!({"scope":"lines","count":count,"sha256":sha(&json!(rows))?,"source":"complete_provider_patch"}),
    )
}
