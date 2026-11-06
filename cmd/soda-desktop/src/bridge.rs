//! Opt-in readiness/control fixture bridge; absent from the product build.
use super::{caller, join_streams, Desktop, Reply};
use serde::Deserialize;
use serde_json::Value;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::time::Duration;
use tauri::{Manager, WebviewWindow};

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

pub(super) struct Observation {
    window: String,
    active: Arc<AtomicBool>,
    task: tauri::async_runtime::JoinHandle<()>,
}

impl Desktop {
    pub(super) fn cancel_streams(&self) {
        join_streams(self.take_streams(None));
    }
    pub(super) fn take_streams(
        &self,
        window: Option<&str>,
    ) -> Vec<tauri::async_runtime::JoinHandle<()>> {
        let mut streams = self.streams.lock().unwrap();
        let ids: Vec<_> = streams
            .iter()
            .filter(|(_, s)| window.is_none_or(|w| s.window == w))
            .map(|(id, _)| id.clone())
            .collect();
        let mut tasks = Vec::with_capacity(ids.len());
        for id in ids {
            if let Some(stream) = streams.remove(&id) {
                stream.active.store(false, Ordering::SeqCst);
                stream.task.abort();
                tasks.push(stream.task);
            }
        }
        tasks
    }
}

#[tauri::command]
pub(super) async fn factory_query(
    window: WebviewWindow,
    app: tauri::AppHandle,
    query: Query,
) -> Result<Reply, String> {
    caller(&window)?;
    let path = query.target()?;
    app.state::<Desktop>().call("GET", &path, Value::Null).await
}
#[tauri::command]
pub(super) async fn factory_command(
    window: WebviewWindow,
    app: tauri::AppHandle,
    command: Mutation,
) -> Result<Reply, String> {
    caller(&window)?;
    let (path, body) = command.request()?;
    app.state::<Desktop>().call("POST", &path, body).await
}
#[tauri::command]
pub(super) fn factory_stream(
    window: WebviewWindow,
    app: tauri::AppHandle,
    run: String,
    channel: tauri::ipc::Channel<Reply>,
) -> Result<String, String> {
    caller(&window)?;
    run_id(&run)?;
    let state = app.state::<Desktop>();
    let mut streams = state.streams.lock().unwrap();
    if streams.len() >= 4 {
        return Err("native observation limit reached".into());
    }
    let id = state.windows.fetch_add(1, Ordering::SeqCst).to_string();
    let active = Arc::new(AtomicBool::new(true));
    let epoch = state.generation.load(Ordering::SeqCst);
    let observed = id.clone();
    let stream_app = app.clone();
    let stream_active = active.clone();
    let task = tauri::async_runtime::spawn(async move {
        loop {
            let state = stream_app.state::<Desktop>();
            if !stream_active.load(Ordering::SeqCst)
                || epoch != state.generation.load(Ordering::SeqCst)
                || state.quitting.load(Ordering::SeqCst)
            {
                break;
            }
            match state
                .call("GET", &format!("/api/factory/runs/{run}"), Value::Null)
                .await
            {
                Ok(reply) => {
                    let stop = reply.status >= 400;
                    if !stream_active.load(Ordering::SeqCst)
                        || epoch != state.generation.load(Ordering::SeqCst)
                        || channel.send(reply).is_err()
                        || stop
                    {
                        break;
                    }
                }
                Err(_) => break,
            }
            tokio::time::sleep(Duration::from_secs(1)).await;
        }
        stream_app
            .state::<Desktop>()
            .streams
            .lock()
            .unwrap()
            .remove(&observed);
    });
    streams.insert(
        id.clone(),
        Observation {
            window: window.label().into(),
            active,
            task,
        },
    );
    Ok(id)
}
#[tauri::command]
pub(super) fn cancel_stream(
    window: WebviewWindow,
    state: tauri::State<'_, Desktop>,
    id: String,
) -> Result<(), String> {
    caller(&window)?;
    let mut streams = state.streams.lock().unwrap();
    if let Some(stream) = streams.get(&id) {
        if stream.window != window.label() {
            return Err("observation belongs to another window".into());
        }
    }
    if let Some(stream) = streams.remove(&id) {
        stream.active.store(false, Ordering::SeqCst);
        stream.task.abort();
        join_streams(vec![stream.task]);
    }
    Ok(())
}
