//! Decode provider-local evidence once. Nullable provider facts stay nullable;
//! this module neither maps canonical identities nor infers product permissions.
use super::request::commit;
use super::{Failure, Result};
use serde_json::{json, Value};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

pub(super) fn id(v: &Value) -> Result<String> {
    v.as_i64()
        .filter(|id| *id > 0)
        .map(|id| id.to_string())
        .ok_or_else(Failure::protocol)
}
pub(super) fn big_id(v: &Value) -> Result<String> {
    v.as_str()
        .filter(|id| super::request::remote_id(id))
        .map(str::to_owned)
        .or_else(|| id(v).ok())
        .ok_or_else(Failure::protocol)
}
pub(super) fn text<'a>(v: &'a Value, key: &str) -> Result<&'a str> {
    v[key].as_str().ok_or_else(Failure::protocol)
}
pub(super) fn nonempty<'a>(v: &'a Value, key: &str) -> Result<&'a str> {
    text(v, key).and_then(|s| {
        if s.is_empty() {
            Err(Failure::protocol())
        } else {
            Ok(s)
        }
    })
}
pub(super) fn string(v: &Value, key: &str) -> Result<Value> {
    let v = &v[key];
    if v.is_null() || v.is_string() {
        Ok(v.clone())
    } else {
        Err(Failure::protocol())
    }
}
pub(super) fn boolean(v: &Value, key: &str) -> Result<Value> {
    let v = &v[key];
    if v.is_null() || v.is_boolean() {
        Ok(v.clone())
    } else {
        Err(Failure::protocol())
    }
}
pub(super) fn integer(v: &Value, key: &str, min: i64) -> Result<Value> {
    let v = &v[key];
    if v.is_null() || v.as_i64().is_some_and(|n| n >= min) {
        Ok(v.clone())
    } else {
        Err(Failure::protocol())
    }
}
pub(super) fn date(v: &Value, key: &str) -> Result<Value> {
    if v[key].is_null() {
        return Ok(Value::Null);
    }
    let s = text(v, key)?;
    OffsetDateTime::parse(s, &Rfc3339).map_err(|_| Failure::protocol())?;
    Ok(v[key].clone())
}
fn full_text(v: &Value, key: &str) -> Result<Value> {
    Ok(Value::String(string(v, key)?.as_str().unwrap_or("").into()))
}
pub(super) fn actor(v: &Value) -> Result<Value> {
    if v.is_null() {
        return Ok(Value::Null);
    }
    Ok(json!({"external_id":id(&v["id"])?,"login":nonempty(v,"login")?}))
}
fn actors(v: &Value) -> Result<Value> {
    if v.is_null() {
        return Ok(Value::Null);
    }
    v.as_array()
        .ok_or_else(Failure::protocol)?
        .iter()
        .map(|v| {
            if v.is_null() {
                return Err(Failure::protocol());
            }
            actor(v)
        })
        .collect::<Result<Vec<_>>>()
        .map(Value::Array)
}
fn labels(v: &Value) -> Result<Value> {
    if v.is_null() {
        return Ok(Value::Null);
    }
    v.as_array().ok_or_else(Failure::protocol)?.iter().map(|v| Ok(json!({"external_id":id(&v["id"])?,"name":nonempty(v,"name")?,"color":full_text(v,"color")?}))).collect::<Result<Vec<_>>>().map(Value::Array)
}
pub(super) fn repository(v: &Value) -> Result<Value> {
    id(&v["owner"]["id"])?;
    Ok(
        json!({"external_id":id(&v["id"])?,"name":nonempty(v,"full_name")?,"private":boolean(v,"private")?,"default_branch":full_text(v,"default_branch")?,"push":boolean(&v["permissions"],"push")?,"admin":boolean(&v["permissions"],"admin")?,"merge_methods":{"merge":boolean(v,"allow_merge_commit")?,"squash":boolean(v,"allow_squash_merge")?,"rebase":boolean(v,"allow_rebase_merge")?}}),
    )
}
pub(super) fn issue(v: &Value) -> Result<Value> {
    Ok(
        json!({"external_id":id(&v["id"])?,"number":v["number"],"title":text(v,"title")?,"body":full_text(v,"body")?,"state":text(v,"state")?,
        "locked":boolean(v,"locked")?,"author":actor(&v["user"])?,"assignees":actors(&v["assignees"])?,"labels":labels(&v["labels"])?,
        "created_at":date(v,"created_at")?,"updated_at":date(v,"updated_at")?,"closed_at":date(v,"closed_at")?}),
    )
}
fn branch(v: &Value) -> Result<Value> {
    if v.is_null() {
        return Ok(Value::Null);
    }
    let sha = string(v, "sha")?;
    if sha.as_str().is_some_and(|s| !commit(s)) {
        return Err(Failure::protocol());
    }
    let repo = if v["repo"].is_null() {
        Value::Null
    } else {
        json!({"external_id":id(&v["repo"]["id"])?,"name":nonempty(&v["repo"],"full_name")?})
    };
    Ok(json!({"repository":repo,"name":full_text(v,"ref")?,"commit":sha}))
}
pub(super) fn pull(v: &Value) -> Result<Value> {
    let mut record = issue(v)?;
    let merged = boolean(v, "merged")?;
    let merged_at = date(v, "merged_at")?;
    if merged == true || !merged_at.is_null() {
        if v["state"] != "closed" || merged == false {
            return Err(Failure::protocol());
        }
        record["state"] = json!("merged");
    }
    for (key, value) in [
        ("draft", boolean(v, "draft")?),
        ("base", branch(&v["base"])?),
        ("head", branch(&v["head"])?),
        ("merged", merged),
        ("merged_at", merged_at),
        ("mergeable", boolean(v, "mergeable")?),
        ("mergeable_state", string(v, "mergeable_state")?),
        ("rebaseable", boolean(v, "rebaseable")?),
    ] {
        record[key] = value;
    }
    Ok(record)
}
pub(super) fn comment(v: &Value) -> Result<Value> {
    Ok(
        json!({"external_id":id(&v["id"])?,"author":actor(&v["user"])?,"body":full_text(v,"body")?,
        "created_at":date(v,"created_at")?,"updated_at":date(v,"updated_at")?,"minimized_reason":string(&v["minimized"],"reason")?}),
    )
}
pub(super) fn review(v: &Value) -> Result<Value> {
    let provider = full_text(v, "state")?;
    let state = match provider.as_str() {
        Some("PENDING") => "pending",
        Some("APPROVED") => "approved",
        Some("CHANGES_REQUESTED") => "changes_requested",
        Some("COMMENTED") => "commented",
        Some("DISMISSED") => "dismissed",
        _ => "unknown",
    };
    let commit_id = string(v, "commit_id")?;
    if commit_id.as_str().is_some_and(|s| !commit(s))
        || state == "pending" && !v["submitted_at"].is_null()
    {
        return Err(Failure::protocol());
    }
    Ok(
        json!({"external_id":id(&v["id"])?,"author":actor(&v["user"])?,"body":full_text(v,"body")?,"state":state,"provider_state":provider,"commit":commit_id,"submitted_at":date(v,"submitted_at")?}),
    )
}
pub(super) fn review_comment(v: &Value) -> Result<Value> {
    let mut record = json!({"external_id":id(&v["id"])?,"author":actor(&v["user"])?,"created_at":date(v,"created_at")?,"updated_at":date(v,"updated_at")?});
    for (raw, key) in [
        ("pull_request_review_id", "review_id"),
        ("in_reply_to_id", "reply_to"),
    ] {
        record[key] = if v[raw].is_null() {
            Value::Null
        } else {
            json!(id(&v[raw])?)
        };
    }
    for key in [
        "body",
        "path",
        "diff_hunk",
        "side",
        "start_side",
        "subject_type",
        "author_association",
        "html_url",
    ] {
        record[key] = string(v, key)?;
    }
    for (raw, key) in [
        ("commit_id", "commit"),
        ("original_commit_id", "original_commit"),
    ] {
        let value = string(v, raw)?;
        if value.as_str().is_some_and(|s| !commit(s)) {
            return Err(Failure::protocol());
        }
        record[key] = value;
    }
    for key in ["line", "start_line", "original_line", "original_start_line"] {
        record[key] = integer(v, key, 1)?;
    }
    for key in ["position", "original_position"] {
        record[key] = integer(v, key, 0)?;
    }
    Ok(record)
}
pub(super) fn check_state(status: &Value) -> &'static str {
    match status.as_str() {
        Some("queued" | "waiting" | "requested" | "pending") => "pending",
        Some("in_progress") => "running",
        Some("completed") => "completed",
        _ => "unknown",
    }
}
pub(super) fn check_conclusion(conclusion: &Value) -> &str {
    match conclusion.as_str() {
        Some(
            s @ ("success" | "failure" | "neutral" | "skipped" | "cancelled" | "timed_out"
            | "action_required" | "stale"),
        ) => s,
        _ => "unknown",
    }
}
pub(super) fn check(v: &Value, head: &str) -> Result<Value> {
    let status = string(v, "status")?;
    let conclusion = string(v, "conclusion")?;
    let state = check_state(&status);
    let result = check_conclusion(&conclusion);
    let app = if v["app"].is_null() {
        Value::Null
    } else {
        json!({"external_id":id(&v["app"]["id"])?,"slug":string(&v["app"],"slug")?,"name":string(&v["app"],"name")?})
    };
    let output = if v["output"].is_null() {
        Value::Null
    } else {
        let o = &v["output"];
        json!({"title":string(o,"title")?,"summary":string(o,"summary")?,"text":string(o,"text")?,"annotations_count":integer(o,"annotations_count",0)?})
    };
    Ok(
        json!({"external_id":id(&v["id"])?,"name":nonempty(v,"name")?,"head":head,"provider_status":status,"provider_conclusion":conclusion,"state":state,"conclusion":result,
        "details_url":string(v,"details_url")?,"started_at":date(v,"started_at")?,"completed_at":date(v,"completed_at")?,"app":app,"output":output}),
    )
}
pub(super) fn status(v: &Value, head: &str) -> Result<Value> {
    let provider = string(v, "state")?;
    let state = match provider.as_str() {
        Some(s @ ("pending" | "success" | "failure" | "error")) => s,
        _ => "unknown",
    };
    let (check_state, conclusion) = match state {
        "pending" => ("pending", "unknown"),
        "success" => ("completed", "success"),
        "failure" | "error" => ("completed", "failure"),
        _ => ("unknown", "unknown"),
    };
    Ok(
        json!({"external_id":id(&v["id"])?,"head":head,"context":string(v,"context")?,"provider_state":provider,"state":state,"check_state":check_state,"conclusion":conclusion,
        "description":string(v,"description")?,"target_url":string(v,"target_url")?,"creator":actor(&v["creator"])?,"created_at":date(v,"created_at")?,"updated_at":date(v,"updated_at")?}),
    )
}
pub(super) fn file(v: &Value) -> Result<Value> {
    let name = text(v, "filename")?;
    if !path(name) {
        return Err(Failure::protocol());
    }
    let additions = integer(v, "additions", 0)?;
    let deletions = integer(v, "deletions", 0)?;
    let (change, old, new) = match text(v, "status")? {
        "added" => ("added", "", name),
        "removed" => ("deleted", name, ""),
        "modified" => ("modified", name, name),
        change @ ("renamed" | "copied") => {
            let old = text(v, "previous_filename")?;
            if !path(old) {
                return Err(Failure::protocol());
            }
            (change, old, name)
        }
        _ => ("unknown", "", name),
    };
    let (state, patch) = patch(v, additions.as_u64(), deletions.as_u64())?;
    Ok(
        json!({"old_path":old,"new_path":new,"change":change,"patch_state":state,"patch":patch,"additions":additions,"deletions":deletions}),
    )
}
fn path(s: &str) -> bool {
    !s.is_empty() && s.len() <= 4096 && !s.contains('\0')
}
fn patch(
    v: &Value,
    additions: Option<u64>,
    deletions: Option<u64>,
) -> Result<(&'static str, Value)> {
    let raw = string(v, "patch")?;
    let Some(raw) = raw.as_str().filter(|s| !s.is_empty()) else {
        return Ok(("unavailable", Value::Null));
    };
    if raw.len() > 16384 {
        let mut end = 16384;
        while !raw.is_char_boundary(end) {
            end -= 1;
        }
        return Ok(("truncated", json!(&raw[..end])));
    }
    Ok(super::patch::classify(raw, additions, deletions))
}

pub(super) fn graphql_continuation(connection: &Value) -> Result<Option<String>> {
    let info = &connection["pageInfo"];
    let more = info["hasNextPage"]
        .as_bool()
        .ok_or_else(Failure::protocol)?;
    let cursor = match info.get("endCursor") {
        Some(Value::Null) => None,
        Some(Value::String(s))
            if !s.is_empty() && s.len() <= 4096 && !s.chars().any(char::is_control) =>
        {
            Some(s.clone())
        }
        _ => return Err(Failure::protocol()),
    };
    if more && cursor.is_none() {
        return Err(Failure::protocol());
    }
    Ok(if more { cursor } else { None })
}
