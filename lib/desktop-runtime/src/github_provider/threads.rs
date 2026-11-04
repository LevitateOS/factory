//! REST review reply groups use their actual root comment identity. These are
//! not claims about GraphQL resolution or proven diff placement. Collect the
//! complete bounded reply forest before paging; never silently lose a reply.
use super::reads::Reader;
use super::{Failure, Result};
use serde_json::{json, Value};
use std::collections::{HashMap, HashSet};

impl Reader<'_> {
    pub(super) async fn threads(&self, query: &Value) -> Result<Value> {
        let number = query["number"].as_u64().ok_or_else(Failure::invalid)?;
        let initial = self.subject(true, number).await?;
        let mut records = Vec::new();
        let mut bytes = 0;
        let mut page = 1;
        loop {
            let observed = self
                .reviews(
                    &json!({"kind":"pull_review_comments","number":number,"page":page,"limit":50}),
                    true,
                )
                .await?;
            if observed["pull"] != initial {
                return Err(Failure::new(409, "github_threads_changed"));
            }
            let items = observed["items"].as_array().ok_or_else(Failure::protocol)?;
            for item in items {
                bytes += serde_json::to_vec(item)
                    .map_err(|_| Failure::protocol())?
                    .len();
                if records.len() >= 1000 || bytes > 8 * 1024 * 1024 {
                    return Err(Failure::new(413, "github_threads_too_large"));
                }
                records.push(item.clone());
            }
            let next = observed["next_page"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            if next == 0 {
                break;
            }
            if page >= 20 {
                return Err(Failure::new(413, "github_threads_too_large"));
            }
            page = next;
        }
        let mut by_id = HashMap::new();
        let mut roots = Vec::new();
        for (index, record) in records.iter().enumerate() {
            let id = record["external_id"]
                .as_str()
                .ok_or_else(Failure::protocol)?;
            if by_id.insert(id, index).is_some() {
                return Err(Failure::protocol());
            }
            if record["reply_to"].is_null() {
                roots.push(id);
            }
        }
        let mut groups: HashMap<&str, Vec<&Value>> = roots.iter().map(|id| (*id, vec![])).collect();
        for record in &records {
            let mut id = record["external_id"]
                .as_str()
                .ok_or_else(Failure::protocol)?;
            let mut seen = HashSet::new();
            loop {
                if !seen.insert(id) {
                    return Err(Failure::protocol());
                }
                let index = by_id.get(id).ok_or_else(Failure::protocol)?;
                let parent = &records[*index]["reply_to"];
                if parent.is_null() {
                    break;
                }
                id = parent.as_str().ok_or_else(Failure::protocol)?;
            }
            groups
                .get_mut(id)
                .ok_or_else(Failure::protocol)?
                .push(record);
        }
        let selected = query["thread_id"].as_str().ok_or_else(Failure::invalid)?;
        let page = query["page"].as_u64().ok_or_else(Failure::invalid)?;
        let limit = query["limit"].as_u64().ok_or_else(Failure::invalid)?;
        let mut items = Vec::new();
        let next;
        if selected.is_empty() {
            let (start, end, more) = window(roots.len(), page, limit)?;
            for id in &roots[start..end] {
                let all = &groups[id];
                let comments = &all[..all.len().min(10)];
                items.push(group(
                    id,
                    &records[by_id[id]],
                    comments,
                    if all.len() > 10 { 2 } else { 0 },
                ));
            }
            next = more;
        } else {
            let all = groups
                .get(selected)
                .ok_or_else(|| Failure::new(404, "github_not_visible"))?;
            let (start, end, more) = window(all.len(), page, limit)?;
            items.push(group(
                selected,
                &records[by_id[selected]],
                &all[start..end],
                more,
            ));
            next = 0;
        }
        if self.subject(true, number).await? != initial {
            return Err(Failure::new(409, "github_threads_changed"));
        }
        self.recheck().await?;
        Ok(
            json!({"repository":self.repository,"name":self.name,"pull":initial,"items":items,"next_page":next}),
        )
    }
}
fn window(total: usize, page: u64, limit: u64) -> Result<(usize, usize, u64)> {
    let start = (page - 1).checked_mul(limit).ok_or_else(Failure::invalid)? as usize;
    if start >= total && !(page == 1 && total == 0) {
        return Err(Failure::invalid());
    }
    let end = total.min(start + limit as usize);
    Ok((start, end, if end < total { page + 1 } else { 0 }))
}
fn group(id: &str, root: &Value, comments: &[&Value], next: u64) -> Value {
    // Native stored coordinates are not an immutable two-commit diff anchor.
    let location = match (
        root["commit"].as_str(),
        root["path"].as_str(),
        root["side"].as_str(),
        root["line"].as_u64(),
    ) {
        (Some(commit), Some(path), Some(side @ ("LEFT" | "RIGHT")), Some(line))
            if line > 0
                && line <= 9_007_199_254_740_991
                && !path.is_empty()
                && path.len() <= 4096 =>
        {
            json!({"commit":commit,"path":path,"side":if side == "LEFT" {"old"} else {"new"},"line":line})
        }
        _ => Value::Null,
    };
    json!({"external_id":id,"recorded_location":location,"resolution":"unknown","outdated":null,"comments":{"items":comments,"next_page":next}})
}
