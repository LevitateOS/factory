//! Complete, current native discussion/check observations. No read grants a
//! review vote, thread mutation, execution or merge capability.
use super::product::{actor, date, id, text};
use super::{invalid, Client, Result};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};

fn remote(value: &Value) -> Result<&str> {
    value.as_str().filter(|s| s.parse::<i64>().is_ok_and(|n| n > 0 && n.to_string() == *s)).ok_or_else(invalid)
}
fn page(items: &[Value], index: u32, limit: u32) -> Result<(Vec<Value>, u32)> {
    if index == 0 || limit == 0 { return Err(invalid()); }
    let start = (index as usize - 1).checked_mul(limit as usize).ok_or_else(invalid)?;
    if start >= items.len() && !(index == 1 && items.is_empty()) { return Err(invalid()); }
    let end = start.saturating_add(limit as usize).min(items.len());
    Ok((items[start..end].to_vec(), if end < items.len() { index + 1 } else { 0 }))
}
fn rows<'a>(set: &'a Value, issue: &Value) -> Result<&'a [Value]> {
    let values = match &set["items"] { Value::Null => &[][..], Value::Array(items) if items.len() <= 50 => items.as_slice(), _ => return Err(invalid()) };
    if set["issue_id"] != *issue || set["complete"] != true || set["total"].as_u64() != Some(values.len() as u64) { return Err(invalid()); }
    Ok(values)
}

impl Client {
    pub(super) async fn visible_required_checks(&self, repository: &str, number: u32, head: &str, base: &str) -> Result<Value> {
        let pull = self.product_pull(number,repository).await?;
        if pull["head"]["commit"] != head || pull["base"]["commit"] != base { return Err(invalid()); }
        let branch = text(&pull["base"],"name",512)?;
        if self.product_tip(&format!("refs/heads/{branch}")).await? != base || self.product_pull(number,repository).await? != pull { return Err(invalid()); }
        // The native conditional merge gate owns policy admission. Its scalar
        // readiness does not enumerate GitHub-style branch/ruleset requirements.
        Ok(json!({"protection":{"repository":repository,"name":format!("{}/{}",self.owner,self.repository),"pull":{"kind":"pull","external_id":pull["external_id"],"number":number},"head":head,"base":base,"branch":branch,"branch_commit":base,"protected":null,"classic":null,"rules":null,
            "summary":{"protected":null,"requirements_complete":false,"classic_visible":false,"rules_visible":false}},"evaluation_commit":head,"status_gate":"unknown","missing_required":[],"invalid_required":[]}))
    }
    pub(super) async fn delivery_opinions(&self, repository: &str, number: u32) -> Result<Value> {
        let before = self.product_pull(number, repository).await?;
        let selection = json!({"families":["pull","reviews"],"pull_number":number.to_string(),"limit":50});
        let (revision, snapshot) = self.snapshot(selection.clone()).await?;
        let pull = &snapshot["pull"];
        if pull["visible"] != true || pull["complete"] != true || pull["id"] != before["external_id"] || pull["head_tip"] != before["head"]["commit"] { return Err(invalid()); }
        let mut items = Vec::new();
        let mut seen = BTreeSet::new();
        let mut veto = false;
        // Native official/stale/dismissed state is host-owned. A COMMENT never
        // supersedes an effective vote. Match the native merge gate's veto.
        for row in rows(&snapshot["reviews"], &pull["issue_id"])? {
            let id = remote(&row["id"])?;
            if row["visible"] != true || row["complete"] != true || !seen.insert(id) { return Err(invalid()); }
            if row["stale"] == true || row["dismissed"] == true { continue; }
            let state = match row["type"].as_str() {
                Some("APPROVED") => "approved", Some("REQUEST_CHANGES") => { veto = true; "changes_requested" },
                Some("COMMENT") => "commented", Some("PENDING" | "REQUEST_REVIEW") => continue, _ => return Err(invalid()),
            };
            items.push(json!({"id":id,"state":state}));
        }
        let (after_revision, after) = self.snapshot(selection).await?;
        if revision != after_revision || snapshot != after || self.product_pull(number, repository).await? != before { return Err(invalid()); }
        // Required-review policies remain governed by host merge eligibility;
        // this observation does not manufacture an approval decision.
        Ok(json!({"repository":repository,"pull_id":before["external_id"],"number":number,"head":before["head"]["commit"],"base":before["base"]["commit"],"decision":if veto {json!("CHANGES_REQUESTED")} else {Value::Null},"items":items}))
    }

    pub(super) async fn visible_checks(&self, repository: &str, number: u32, index: u32, limit: u32, head: &str, runs: bool) -> Result<Value> {
        let pull = self.product_pull(number, repository).await?;
        let exact = pull["head"]["commit"].as_str().ok_or_else(invalid)?;
        let observed = if runs { json!({"sources":[]}) } else { self.current_checks(exact).await? };
        if (!head.is_empty() && head != exact) || pull["head"]["commit"] != exact { return Err(invalid()); }
        let mut items = Vec::new();
        // Fountain Actions publish native commit statuses. There is no distinct
        // GitHub check-run family; the shared reader continues to statuses.
        if !runs {
            for source in observed["sources"].as_array().ok_or_else(invalid)? {
                let state = text(source,"state",64)?;
                let (check, conclusion) = match state {
                    "pending" => ("pending", "unknown"), "success" => ("completed", "success"),
                    "failure" | "error" => ("completed", "failure"), "warning" => ("completed", "neutral"), _ => ("unknown", "unknown"),
                };
                let creator = source["creator"].as_str().ok_or_else(invalid)?;
                let context = text(source,"context",256)?;
                let context = if creator == "-2" { format!("actions:{context}") } else { format!("actor:{creator}:{context}") };
                items.push(json!({"external_id":remote(&source["id"])?,"head":exact,"context":context,"provider_state":state,"state":state,"check_state":check,"conclusion":conclusion,
                    "creator":null,"description":null,"target_url":null,"created_at":null,"updated_at":null}));
            }
        }
        let (selected, next) = page(&items,index,limit)?;
        if self.product_pull(number,repository).await? != pull { return Err(invalid()); }
        Ok(json!({"repository":repository,"name":format!("{}/{}",self.owner,self.repository),"pull":{"kind":"pull","external_id":pull["external_id"],"number":number},"head":exact,"base":pull["base"]["commit"],"branch":pull["base"]["name"],"items":selected,"next_page":next,"total":items.len()}))
    }

    pub(super) async fn discussion_threads(&self, repository: &str, number: u32, index: u32, limit: u32, thread: &str, cursor: &str) -> Result<Value> {
        let pull = self.product_pull(number,repository).await?;
        use crate::provider_contract::thread_cursor;
        let scope = |root: &str, size: u32| thread_cursor::scope(&json!({"host":self.origin(),"account":self.capability.account().account,
            "repository":repository,"pull":pull["external_id"],"head":pull["head"],"base":pull["base"],"root":root,"limit":size})).map_err(|_| invalid());
        let bound = scope(thread, limit)?;
        let continuation = thread_cursor::decode(cursor,&bound,index).map_err(|_| super::Failure {status:409,code:"native_thread_cursor_changed"})?;
        if !continuation.after.is_empty() { return Err(invalid()); }
        let mut selection = json!({"families":["pull","review_threads"],"pull_number":number.to_string(),"limit":50});
        let mut native = Vec::new();
        let mut captures = Vec::new();
        let mut seen = BTreeSet::new();
        let mut revision = None;
        loop {
            let (current,snapshot) = self.snapshot(selection.clone()).await?;
            let p = &snapshot["pull"];
            let set = &snapshot["review_threads"];
            if revision.is_some_and(|r| r != current) || p["visible"] != true || p["complete"] != true || p["id"] != pull["external_id"] || p["head_tip"] != pull["head"]["commit"] || set["issue_id"] != p["issue_id"] || set["pull_number"] != number.to_string() { return Err(invalid()); }
            revision = Some(current);
            let values = match &set["items"] { Value::Null => &[][..], Value::Array(items) if items.len()<=50 => items.as_slice(), _ => return Err(invalid()) };
            for value in values {
                if value["visible"] != true || value["complete"] != true || value["issue_id"] != p["issue_id"] || !seen.insert(remote(&value["root_id"])?.to_owned()) || native.len()>=256 { return Err(invalid()); }
                native.push(value.clone());
            }
            let cursor = set["next_cursor"].as_str().unwrap_or("").to_owned();
            let empty = values.is_empty();
            captures.push((selection.clone(),snapshot));
            if cursor.is_empty() {
                if captures.last().ok_or_else(invalid)?.1["review_threads"]["complete"] != true { return Err(invalid()); }
                break;
            }
            if empty || selection["cursor"] == cursor || !seen.contains(&cursor) { return Err(invalid()); }
            selection["cursor"] = json!(cursor);
        }
        let total = native.len() as u64;
        if continuation.total.is_some_and(|previous| previous != total) { return Err(invalid()); }
        let (native, next_page) = if thread.is_empty() { page(&native,index,limit)? } else {
            let selected: Vec<_> = native.into_iter().filter(|value| format!("fountain:thread:{repository}:{}",value["root_id"].as_str().unwrap_or("")) == thread).collect();
            if selected.len()!=1 { return Err(invalid()); } (selected,0)
        };
        let mut selected_ids = BTreeSet::new();
        for item in &native {
            let members = item["members"].as_array().ok_or_else(invalid)?;
            let (selected, _) = page(members,if thread.is_empty(){1}else{index},if thread.is_empty(){10}else{limit})?;
            for member in selected { selected_ids.insert(remote(&member["id"])?.to_owned()); }
        }
        let mut selected_comments = BTreeMap::new();
        let ids: Vec<_> = selected_ids.iter().cloned().collect();
        for batch in ids.chunks(50) {
            let selection = json!({"families":["pull","comments"],"pull_number":number.to_string(),"comment_ids":batch,"limit":50});
            let (current,snapshot) = self.snapshot(selection.clone()).await?;
            if Some(current) != revision || snapshot["pull"]["id"] != pull["external_id"] || snapshot["pull"]["head_tip"] != pull["head"]["commit"] { return Err(invalid()); }
            let observed = rows(&snapshot["comments"], &snapshot["pull"]["issue_id"])?;
            if observed.len() != batch.len() { return Err(invalid()); }
            for comment in observed {
                let id = remote(&comment["id"])?;
                if !batch.iter().any(|key| key==id) || comment["visible"]!=true || comment["complete"]!=true || selected_comments.insert(id.to_owned(),comment.clone()).is_some() { return Err(invalid()); }
                remote(&comment["review_id"])?;
            }
            captures.push((selection,snapshot));
        }
        if crate::provider_contract::operation::Operation::current().is_some_and(|work| !work.can_finish(selected_ids.len() as u32 + captures.len() as u32 * 3 + 2)) {
            return Err(super::Failure {status:429,code:"native_operation_budget"});
        }
        let mut comments_by_id = BTreeMap::new();
        for (id, observed) in &selected_comments {
            let review = remote(&observed["review_id"])?;
            let (value, _):(Value, _) = self.request_url(reqwest::Method::GET,
                self.product_url(&["pulls",&number.to_string(),"reviews",review,"comments",id])?,None,2*1024*1024).await?;
            if super::product::id(&value["id"])? != *id || super::product::id(&value["pull_request_review_id"])? != review { return Err(invalid()); }
            comments_by_id.insert(id.clone(),value);
        }
        let mut result = Vec::new();
        let mut member_count = 0;
        for native in &native {
            let root = remote(&native["root_id"])?;
            let identity = format!("fountain:thread:{repository}:{root}");
            if !thread.is_empty() && thread != identity { continue; }
            remote(&native["review_id"])?;
            let members = native["members"].as_array().ok_or_else(invalid)?;
            if members.is_empty() || native["member_total"].as_u64() != Some(members.len() as u64) || members[0]["id"] != root { return Err(invalid()); }
            member_count += members.len();
            if member_count > 256 { return Err(invalid()); }
            let mut comments = Vec::new();
            let (members, comments_next) = page(members,if thread.is_empty(){1}else{index},if thread.is_empty(){10}else{limit})?;
            for member in &members {
                let key = remote(&member["id"])?;
                let raw = comments_by_id.get(key).ok_or_else(invalid)?;
                let observed = selected_comments.get(key).ok_or_else(invalid)?;
                let review = id(&raw["pull_request_review_id"])?;
                let body = text(raw,"body",128*1024)?;
                let digest:String = Sha256::digest(body.as_bytes()).iter().map(|b|format!("{b:02x}")).collect();
                if member["visible"] != true || member["complete"] != true || member["issue_id"] != native["issue_id"] || member["content_digest"] != digest || observed["content_digest"] != digest || observed["review_id"] != review { return Err(invalid()); }
                comments.push(json!({"external_id":key,"review_id":review,"body":body,"author":actor(&raw["user"])?,"created_at":date(raw,"created_at")?,"updated_at":date(raw,"updated_at")?}));
            }
            let comments_cursor = thread_cursor::encode(&scope(&identity,if thread.is_empty(){10}else{limit})?,comments_next,"",None,0).map_err(|_|invalid())?;
            let original = native["original_commit"].as_str().filter(|s| super::hex(s,40));
            let line = native["line"].as_i64().unwrap_or(0);
            let path = native["path"].as_str().unwrap_or("");
            let side = native["side"].as_str().unwrap_or("");
            result.push(json!({"external_id":identity,"recorded_location":{"commit":original,"path":path,"side":side,"line":line},"resolution":if native["resolved"]==true{"resolved"}else{"unresolved"},"outdated":native["outdated"].as_bool().unwrap_or(false),
                "viewer_can_resolve":false,"viewer_can_unresolve":false,"comments":{"items":comments,"next_page":comments_next,"next_cursor":comments_cursor}}));
        }
        for (selection,prior) in captures {
            let (current,after) = self.snapshot(selection).await?;
            if Some(current) != revision || after != prior { return Err(invalid()); }
        }
        if self.product_pull(number,repository).await? != pull { return Err(invalid()); }
        let items = result;
        let next = next_page;
        let next_cursor = thread_cursor::encode(&bound,next,"",Some(total),0).map_err(|_|invalid())?;
        Ok(json!({"repository":repository,"name":format!("{}/{}",self.owner,self.repository),"pull":{"kind":"pull","external_id":pull["external_id"],"number":number},"items":items,"next_page":next,"next_cursor":next_cursor}))
    }
}
