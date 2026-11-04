use serde::Deserialize;
use serde_json::Value;

/// Shared product HTTP operations, never provider requests or native URLs.
/// Product policy and resource authorization remain in Go. Native custody and
/// selected-resource scope are supplied independently of this untrusted path.
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum ProductRequest {
    Read { path: String },
    Write { path: String, body: Value },
    Subscription { body: Value },
}
impl ProductRequest {
    pub fn request(self) -> Result<(&'static str, String, Vec<u8>), String> {
        let (method, path, body) = match self {
            Self::Read { path } => ("GET", path, None),
            Self::Write { path, body } => ("POST", path, Some(body)),
            Self::Subscription { body } => {
                ("PUT", "/api/factory/feed/subscription".into(), Some(body))
            }
        };
        let pathname = path.split('?').next().unwrap_or("");
        if path.len() > 2048
            || !path.is_ascii()
            || path.bytes().any(|b| b.is_ascii_control() || b == b'#')
            || !(pathname.starts_with("/api/factory/")
                || pathname.starts_with("/api/repositories/"))
            || pathname.split('/').skip(1).any(|part| {
                part.is_empty()
                    || !part
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_'))
            })
            || body.as_ref().is_some_and(|b| !b.is_object())
        {
            return Err("unsupported Factory product operation".into());
        }
        let body = body
            .map(|b| serde_json::to_vec(&b))
            .transpose()
            .map_err(|_| "invalid Factory product body")?
            .unwrap_or_default();
        if body.len() > 65536 {
            return Err("Factory product body exceeds the native limit".into());
        }
        Ok((method, path, body))
    }
}

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
#[cfg(feature = "native-check")]
pub enum Query {
    Identity,
    Feed,
    Request { intent: String },
    Run { run: String },
}
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
#[cfg(feature = "native-check")]
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
#[cfg(feature = "native-check")]
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
#[cfg(feature = "native-check")]
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
#[cfg(feature = "native-check")]
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
#[cfg(feature = "native-check")]
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
