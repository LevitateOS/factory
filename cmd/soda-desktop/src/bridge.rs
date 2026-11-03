use serde::Deserialize;
use serde_json::Value;

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Query {
    Identity,
    Feed,
    Request { intent: String },
    Run { run: String },
}
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Mutation {
    SaveRequest {
        revision: Value,
    },
    Explore {
        intent: String,
        revision: String,
    },
    Plan {
        intent: String,
        revision: String,
        operation: String,
    },
    Generate {
        intent: String,
        revision: String,
    },
}
impl Query {
    pub fn target(&self) -> Result<String, String> {
        Ok(match self {
            Self::Identity => "/api/factory/identity".into(),
            Self::Feed => "/api/factory/feed".into(),
            Self::Request { intent } => {
                decision(intent)?;
                format!("/api/factory/intents/{intent}")
            }
            Self::Run { run } => {
                run_id(run)?;
                format!("/api/factory/runs/{run}")
            }
        })
    }
}
impl Mutation {
    pub fn request(self) -> Result<(String, Value), String> {
        Ok(match self {
            Self::SaveRequest { revision } => {
                if !revision.is_object() {
                    return Err("invalid request body".into());
                }
                ("/api/factory/intents".into(), revision)
            }
            Self::Explore { intent, revision } => {
                decision(&intent)?;
                decision(&revision)?;
                (
                    format!("/api/factory/intents/{intent}/explore"),
                    serde_json::json!({"revision":revision}),
                )
            }
            Self::Plan {
                intent,
                revision,
                operation,
            } => {
                decision(&intent)?;
                decision(&revision)?;
                if !matches!(
                    operation.as_str(),
                    "propose_intent" | "decomposition_options"
                ) {
                    return Err("invalid planning operation".into());
                }
                (
                    format!("/api/factory/intents/{intent}/plan"),
                    serde_json::json!({"revision":revision,"operation":operation}),
                )
            }
            Self::Generate { intent, revision } => {
                decision(&intent)?;
                decision(&revision)?;
                (
                    format!("/api/factory/intents/{intent}/generate"),
                    serde_json::json!({"revision":revision}),
                )
            }
        })
    }
}
fn decision(value: &str) -> Result<(), String> {
    if value.len() == 25
        && value.starts_with('d')
        && value.as_bytes()[1..]
            .iter()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(b))
    {
        Ok(())
    } else {
        Err("invalid request identity".into())
    }
}
pub fn run_id(value: &str) -> Result<(), String> {
    if value.len() == 32
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    {
        Ok(())
    } else {
        Err("invalid run identity".into())
    }
}
