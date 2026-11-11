//! Required-check readiness is distinct from merge eligibility and Factory's
//! stricter selected-check policy. Provider source binding stays in Rust.
use super::{reads::Reader, records as r, Failure, Result};
use serde_json::{json, Value};
use std::collections::BTreeSet;

fn state(source: &Value) -> Result<&str> {
    if source["eligible"] == false { return Ok("unknown"); }
    if source["kind"] == "check" {
        if source["provider_status"] != "completed" {
            return Ok("pending");
        }
        return Ok(match source["provider_conclusion"].as_str() {
            Some("success" | "neutral" | "skipped") => "success",
            Some(
                "failure" | "timed_out" | "action_required" | "stale" | "startup_failure"
                | "cancelled",
            ) => "failure",
            _ => "unknown",
        });
    }
    r::text(source, "state")
}
fn rank(state: &str) -> usize {
    match state {
        "success" => 0,
        "pending" => 1,
        "unknown" => 2,
        "failure" => 3,
        "error" => 4,
        _ => 2,
    }
}

impl Reader<'_> {
    pub(super) async fn required_checks(&self, q: &Value) -> Result<Value> {
        let number = q["number"].as_u64().ok_or_else(Failure::invalid)?;
        let head = r::text(q, "head")?;
        let base = r::text(q, "base")?;
        let before = self.check_evaluation(number).await?;
        if before["head"] != head || before["base"] != base {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        let request = json!({"number":number,"head":head});
        let ((evaluation, sources), protection) = tokio::try_join!(
            async {
                match self.evaluation_sources(&before).await {
                    Ok((evaluation, sources)) => Ok((evaluation, Some(sources))),
                    Err(failure) if failure.code == "github_automation_checks_incomplete" => Ok((head.into(), None)),
                    Err(failure) => Err(failure),
                }
            },
            self.protection(&request)
        )?;
        // Policy pairs its own sources and PR/branch binding. Compare CI again
        // after that paired observation instead of repeating the complete policy read.
        let (after, latest) = tokio::join!(self.check_evaluation(number), async {
            if sources.is_some() {
                self.evaluation_sources(&before).await
            } else {
                Ok((evaluation.clone(), Vec::new()))
            }
        });
        if let Some(sources) = &sources {
            if latest? != (evaluation.clone(), sources.clone()) {
                return Err(Failure::new(409, "github_checks_changed"));
            }
        }
        if after? != before {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        if protection["repository"] != self.repository
            || protection["pull"] != before["pull"]
            || protection["pull"]["number"] != number
            || protection["head"] != head
            || protection["base"] != base
            || protection["branch_commit"] != base
            || protection["branch"] != before["branch"]
        {
            return Err(Failure::new(409, "github_checks_changed"));
        }
        let summary = &protection["summary"];
        let mut gate = "unknown";
        let mut missing = BTreeSet::new();
        let mut invalid = BTreeSet::new();
        if summary["requirements_complete"] == true && sources.is_some() {
            let requirements = summary["required_checks"]
                .as_array()
                .ok_or_else(Failure::protocol)?;
            let sources = sources.as_ref().ok_or_else(Failure::protocol)?;
            gate = if requirements.is_empty() {
                "not_required"
            } else {
                "success"
            };
            for required in requirements {
                let context = r::text(required, "context")?;
                let candidates: Vec<_> =
                    sources.iter().filter(|s| s["context"] == context).collect();
                let mut result = "success";
                if candidates.is_empty() {
                    missing.insert(context.to_owned());
                    result = "pending";
                }
                match required["source"].as_str() {
                    Some("any") => {}
                    Some("app") => {
                        let app = r::text(required, "app")?;
                        if !candidates
                            .iter()
                            .any(|s| s["kind"] == "check" && s["app"] == app)
                        {
                            // Commit statuses do not expose an integration ID.
                            // A matching name cannot prove their required app.
                            invalid.insert(context.to_owned());
                            result = "unknown";
                        }
                    }
                    _ => {
                        invalid.insert(context.to_owned());
                        result = "unknown";
                    }
                }
                for candidate in candidates {
                    let observed = state(candidate)?;
                    if rank(observed) > rank(result) {
                        result = observed;
                    }
                }
                if rank(result) > rank(gate) {
                    gate = result;
                }
            }
        }
        self.recheck().await?;
        Ok(
            json!({"protection":protection,"evaluation_commit":evaluation,"status_gate":gate,"missing_required":missing,"invalid_required":invalid}),
        )
    }
}
