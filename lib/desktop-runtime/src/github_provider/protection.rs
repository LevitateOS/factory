//! Decode provider policy here, beside the authenticated reads. This is a
//! partial requirements observation, never an actor permission or merge gate.
use super::{Failure, Result};
use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet};

fn text(value: &Value) -> Result<&str> {
    value
        .as_str()
        .filter(|s| !s.is_empty() && s.len() <= 1024 && !s.chars().any(char::is_control))
        .ok_or_else(Failure::protocol)
}
fn flag(value: &Value) -> Result<Option<bool>> {
    if value.is_null() {
        Ok(None)
    } else {
        value.as_bool().map(Some).ok_or_else(Failure::protocol)
    }
}
fn count(value: &Value) -> Result<Option<i64>> {
    if value.is_null() {
        Ok(None)
    } else {
        value
            .as_i64()
            .filter(|n| (0..=9007199254740991).contains(n))
            .map(Some)
            .ok_or_else(Failure::protocol)
    }
}
// A positive requirement survives a missing source. A negative requires all
// sources to be known; missing provider fields are not disabled requirements.
fn combine(a: Option<bool>, b: Option<bool>, visible: bool) -> Option<bool> {
    if a == Some(true) || b == Some(true) {
        Some(true)
    } else if visible && a == Some(false) && b == Some(false) {
        Some(false)
    } else {
        None
    }
}

pub(super) fn summarize(
    protected: &Value,
    classic: Option<&Value>,
    rules: Option<&[Value]>,
) -> Result<Value> {
    let visible = classic.is_some() && rules.is_some();
    let mut checks = BTreeMap::new();
    let mut unknown = BTreeSet::new();
    let mut add = |context: &Value, app: &Value, absent_any: bool| -> Result<()> {
        let context = text(context)?;
        let (source, app) = if app.is_null() {
            (if absent_any { "any" } else { "unknown" }, String::new())
        } else {
            match app.as_i64() {
                Some(-1) => ("any", String::new()),
                Some(n) if n > 0 => ("app", n.to_string()),
                _ => return Err(Failure::protocol()),
            }
        };
        checks.insert(
            (context.to_owned(), source, app.clone()),
            json!({"context":context,"source":source,"app":app}),
        );
        if checks.len() > 1000 {
            return Err(Failure::new(413, "github_requirements_too_large"));
        }
        Ok(())
    };
    let mut classic_strict = None;
    let mut classic_approvals = None;
    let mut classic_dismiss = None;
    let mut classic_owners = None;
    let mut classic_last = None;
    if let Some(c) = classic {
        let status = &c["required_status_checks"];
        classic_strict = if status.is_null() {
            Some(false)
        } else {
            flag(&status["strict"])?
        };
        if !status.is_null() {
            let typed = status["checks"].as_array();
            if let Some(typed) = typed {
                for check in typed {
                    add(&check["context"], &check["app_id"], false)?;
                }
            }
            for context in status["contexts"]
                .as_array()
                .ok_or_else(Failure::protocol)?
            {
                if !typed.is_some_and(|items| items.iter().any(|c| c["context"] == *context)) {
                    add(context, &Value::Null, false)?;
                }
            }
        }
        let review = &c["required_pull_request_reviews"];
        if review.is_null() {
            classic_approvals = Some(0);
            classic_dismiss = Some(false);
            classic_owners = Some(false);
            classic_last = Some(false);
        } else {
            classic_approvals = count(&review["required_approving_review_count"])?;
            classic_dismiss = flag(&review["dismiss_stale_reviews"])?;
            classic_owners = flag(&review["require_code_owner_reviews"])?;
            classic_last = flag(&review["require_last_push_approval"])?;
        }
        // Restrictions and bypass actors are deliberately not interpreted as
        // merge authority. Surface additional classic policies as unrepresented.
        if let Some(kinds) = c["unrepresented"].as_array() {
            for kind in kinds {
                unknown.insert(text(kind)?.to_owned());
            }
        }
    }
    let mut strict = Some(false);
    let mut approvals = Some(0);
    let mut dismiss = Some(false);
    let mut owners = Some(false);
    let mut last = Some(false);
    let mut signed = Some(false);
    let mut linear = Some(false);
    let mut threads = Some(false);
    if let Some(rules) = rules {
        for rule in rules {
            let kind = text(&rule["type"])?;
            let p = &rule["parameters"];
            match kind {
                "required_status_checks" => {
                    let required = p["required_status_checks"]
                        .as_array()
                        .filter(|v| v.len() <= 1000)
                        .ok_or_else(Failure::protocol)?;
                    for check in required {
                        add(&check["context"], &check["integration_id"], true)?;
                    }
                    // GitHub applies strictness only with a required check.
                    let value = if required.is_empty() {
                        Some(false)
                    } else {
                        flag(&p["strict_required_status_checks_policy"])?
                    };
                    strict = combine(strict, value, true);
                }
                "pull_request" => {
                    approvals = match (approvals, count(&p["required_approving_review_count"])?) {
                        (Some(a), Some(b)) => Some(a.max(b)),
                        _ => None,
                    };
                    dismiss = combine(dismiss, flag(&p["dismiss_stale_reviews_on_push"])?, true);
                    owners = combine(owners, flag(&p["require_code_owner_review"])?, true);
                    last = combine(last, flag(&p["require_last_push_approval"])?, true);
                    threads = combine(
                        threads,
                        flag(&p["required_review_thread_resolution"])?,
                        true,
                    );
                    // New parameters (e.g. review teams) must remain visible as
                    // unknown policy rather than being silently dropped.
                    if let Some(params) = p.as_object() {
                        if params.keys().any(|k| {
                            !matches!(
                                k.as_str(),
                                "required_approving_review_count"
                                    | "dismiss_stale_reviews_on_push"
                                    | "require_code_owner_review"
                                    | "require_last_push_approval"
                                    | "required_review_thread_resolution"
                                    | "allowed_merge_methods"
                            )
                        }) {
                            unknown.insert("pull_request_parameters".into());
                        }
                        if params.contains_key("allowed_merge_methods") {
                            unknown.insert("allowed_merge_methods".into());
                        }
                    }
                }
                "required_signatures" => signed = Some(true),
                "required_linear_history" => linear = Some(true),
                _ => {
                    unknown.insert(kind.to_owned());
                }
            }
        }
    }
    drop(add);
    if unknown.len() > 100 {
        return Err(Failure::new(413, "github_rules_too_large"));
    }
    let bindings_known = checks.keys().all(|(_, source, _)| *source != "unknown");
    let complete = visible && bindings_known && unknown.is_empty();
    let contexts = if complete && checks.keys().all(|(_, source, _)| *source == "any") {
        json!(checks
            .keys()
            .map(|(context, _, _)| context)
            .collect::<BTreeSet<_>>())
    } else {
        Value::Null
    };
    let classic_field = |key| {
        classic
            .map(|c| flag(&c[key]))
            .transpose()
            .map(Option::flatten)
    };
    let required_approvals = if visible {
        match (classic_approvals, approvals) {
            (Some(a), Some(b)) => Some(a.max(b)),
            _ => None,
        }
    } else {
        None
    };
    Ok(
        json!({"protected":flag(protected)?,"status_checks":if !checks.is_empty(){Some(true)}else if complete{Some(false)}else{None},
        "required_contexts":contexts,"required_checks":checks.into_values().collect::<Vec<_>>(),
        "requirements_complete":complete,"classic_visible":classic.is_some(),"rules_visible":rules.is_some(),"unknown_rule_kinds":unknown,
        "required_approvals":required_approvals,
        "require_up_to_date":combine(classic_strict,strict,visible),
        "dismiss_stale":combine(classic_dismiss,dismiss,visible),
        "require_code_owner_reviews":combine(classic_owners,owners,visible),
        "require_last_push_approval":combine(classic_last,last,visible),
        "require_signed":combine(classic_field("required_signatures")?,signed,visible),
        "require_linear_history":combine(classic_field("required_linear_history")?,linear,visible),
        "require_resolved_threads":combine(classic_field("required_conversation_resolution")?,threads,visible),
        "apply_to_admins":if visible && rules.is_some_and(|r|r.is_empty()){classic_field("enforce_admins")?}else{None},
        "block_rejected":null,"block_review_requests":null,"ignore_stale":null,"protected_files":"","unprotected_files":""}),
    )
}
