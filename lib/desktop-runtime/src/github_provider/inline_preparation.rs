//! Current immutable diff selection and own pending-review facts. These are
//! bounded observations, not publication permission or historical thread anchors.
use super::{reads::Reader, records as r, Failure, Result};
use serde_json::{json, Value};
use std::collections::HashSet;

pub use crate::provider_contract::InlineAnchor;

use crate::provider_contract::inline_path as path;

pub(super) struct InlineCapture(Value);
impl InlineCapture {
    fn seal(mut value: Value) -> Result<Self> {
        value["sha256"] = json!(sha(&value)?);
        Ok(Self(value))
    }
    pub(super) fn value(&self) -> &Value { &self.0 }
    pub(super) fn into_value(self) -> Value { self.0 }
    pub(super) fn verify(&self, repository: &str, pull: &str, node: &str, digest: &str) -> Result<()> {
        if self.0["repository_node_id"] != repository || self.0["pull"]["external_id"] != pull
            || self.0["pull"]["node_id"] != node || self.0["sha256"] != digest {
            return Err(Failure::new(409,"github_inline_preparation_changed"));
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
    ) -> Result<InlineCapture> {
        anchor.validate()?;
        let mut page = 1u32;
        let captured = self.open_diff(&json!({"kind":"pull_files","number":number,"base":anchor.base,"head":anchor.head})).await?;
        let scope = &captured.scope;
        let total = captured.total.ok_or_else(Failure::protocol)?;
        let pending = self.review_preparation(number, actor, account).await?;
        if pending["pending"]["comments_count"].as_u64().is_some_and(|count| count >= 1000) {
            return Err(Failure::new(413, "github_inline_pending_capacity"));
        }
        let remaining = total.div_ceil(10).max(1) as u32 + self.pending_completion_sends(&pending)? + 2;
        if let Some(work) = crate::provider_contract::operation::Operation::current() {
            if !work.can_finish(remaining) {
                return Err(Failure::new(429, "native_operation_budget"));
            }
        }
        let mut names = HashSet::new();
        let mut bytes = 0usize;
        let mut selected = None;
        loop {
            let files = self.diff_files(&captured, page, 10).await?;
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
        if names.len() as u64 != total {
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
        let mut before = if pending["pending"].is_null() {
            let current = self.review_preparation(number, actor, account).await?;
            Self::same_pending(&pending, &current)?;
            self.complete_pending_snapshot(number, actor, account, current).await?
        } else {
            self.complete_pending_snapshot(number, actor, account, pending).await?
        };
        if before["pending"]["comments_count"].as_u64().is_some_and(|n| n >= 1000) {
            return Err(Failure::new(413, "github_inline_pending_capacity"));
        }
        self.close_diff(&captured).await?;
        if before["pull"]["head"] != anchor.head || scope["pull"]["external_id"] != before["pull"]["external_id"] {
            return Err(Failure::new(409, "github_inline_selection_changed"));
        }
        before["selection"] = serde_json::to_value(anchor).map_err(|_| Failure::protocol())?;
        before["diff"] = json!({"scope":scope,"file":file,"file_sha256":sha(&file)?,"selection":writing,"catalogue":{"count":names.len(),"complete":true,"condition":"bounded_observation"}});
        before["condition"] = json!("bounded_observation");
        before["selection_sha256"] = json!(super::transport::sha256(
            &serde_json::to_vec(anchor).map_err(|_| Failure::protocol())?
        ));
        InlineCapture::seal(before)
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
