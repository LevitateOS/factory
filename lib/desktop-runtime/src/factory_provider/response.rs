//! Validate a complete provider SSE response before any of its bytes reach an
//! isolated CLI. A model reply cannot add tools that the run did not offer.
use serde_json::Value;
use std::collections::{BTreeMap, BTreeSet};

const MAX_EVENTS: usize = 65_536;

fn bind(calls: &mut BTreeMap<String, String>, id: &str, name: &str) -> bool {
    if id.is_empty() || id.len() > 256 {
        return false;
    }
    match calls.get(id) {
        Some(prior) => prior == name,
        None => {
            calls.insert(id.to_owned(), name.to_owned());
            true
        }
    }
}

fn permitted(
    value: &Value,
    offered: &BTreeSet<String>,
    calls: &mut BTreeMap<String, String>,
) -> bool {
    match value {
        Value::Array(items) => items.iter().all(|item| permitted(item, offered, calls)),
        Value::Object(fields) => {
            if let Some(kind) = fields.get("type").and_then(Value::as_str) {
                if kind == "function_call" {
                    let Some(name) = fields.get("name").and_then(Value::as_str) else {
                        return false;
                    };
                    if !offered.contains(name)
                        || fields
                            .get("namespace")
                            .is_some_and(|value| value.as_str() != Some("muse"))
                    {
                        return false;
                    }
                    let Some(id) = fields.get("id").and_then(Value::as_str) else {
                        return false;
                    };
                    if !bind(calls, id, name) {
                        return false;
                    }
                } else if kind.starts_with("response.function_call_arguments.")
                    && fields.get("name").is_some_and(|value| {
                        value.as_str().is_none_or(|name| {
                            !offered.contains(name)
                                || fields
                                    .get("item_id")
                                    .and_then(Value::as_str)
                                    .is_none_or(|id| !bind(calls, id, name))
                        })
                    })
                {
                    return false;
                } else if kind.contains("tool")
                    || kind.contains("mcp")
                    || kind.contains("web_search")
                    || kind.contains("file_search")
                    || kind.contains("code_interpreter")
                    || kind.contains("computer_call")
                    || kind.contains("shell_call")
                    || kind.ends_with("_call")
                {
                    return false;
                }
            }
            fields
                .values()
                .all(|value| permitted(value, offered, calls))
        }
        _ => true,
    }
}

pub(super) fn admitted_sse(bytes: &[u8], offered: &BTreeSet<String>) -> bool {
    let Ok(text) = std::str::from_utf8(bytes) else {
        return false;
    };
    let normalized = text.replace("\r\n", "\n");
    if !normalized.ends_with("\n\n") {
        return false;
    }
    let mut terminal = false;
    let mut done = false;
    let mut count = 0usize;
    let mut calls = BTreeMap::new();
    let mut arguments = BTreeSet::new();
    for frame in normalized.split_terminator("\n\n") {
        if frame.is_empty() {
            continue;
        }
        count += 1;
        if count > MAX_EVENTS || done {
            return false;
        }
        let mut event = None;
        let mut data = String::new();
        for line in frame.split('\n') {
            if line.starts_with(':') {
                continue;
            }
            if let Some(value) = line.strip_prefix("event:") {
                if event.replace(value.trim()).is_some() {
                    return false;
                }
            } else if let Some(value) = line.strip_prefix("data:") {
                if !data.is_empty() {
                    data.push('\n');
                }
                data.push_str(value.strip_prefix(' ').unwrap_or(value));
            } else {
                return false;
            }
        }
        if data == "[DONE]" {
            if !terminal || event.is_some() {
                return false;
            }
            done = true;
            continue;
        }
        if data.is_empty() || terminal {
            return false;
        }
        let Ok(value) = serde_json::from_str::<Value>(&data) else {
            return false;
        };
        let Some(kind) = value.get("type").and_then(Value::as_str) else {
            return false;
        };
        if !kind.starts_with("response.") || event.is_some_and(|event| event != kind) {
            return false;
        }
        if !permitted(&value, offered, &mut calls) {
            return false;
        }
        if kind.starts_with("response.function_call_arguments.") {
            let Some(id) = value.get("item_id").and_then(Value::as_str) else {
                return false;
            };
            if id.is_empty() || id.len() > 256 {
                return false;
            }
            arguments.insert(id.to_owned());
        }
        terminal = matches!(
            kind,
            "response.completed" | "response.failed" | "response.incomplete"
        );
    }
    terminal
        && calls.len() <= 1
        && arguments.len() <= 1
        && arguments.iter().all(|id| calls.contains_key(id))
}
