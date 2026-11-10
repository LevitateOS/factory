//! Opt-in diagnostics of the live desktop. All product mutations stay in UI handlers.
use super::{caller, developer_protocol as wire, Desktop};
use http_body_util::{BodyExt, Full, Limited};
use hyper::{
    body::{Bytes, Incoming},
    server::conn::http1,
    service::service_fn,
    Request, Response,
};
use hyper_util::rt::TokioIo;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::convert::Infallible;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, PermissionsExt};
use std::path::PathBuf;
use std::sync::{atomic::Ordering, Arc, Mutex};
use std::time::Duration;
use tauri::{Manager, WebviewWindow};
use tokio::sync::{oneshot, watch, Semaphore};
const ACTION_LIMIT: usize = 256;
fn random_id() -> Result<String, &'static str> {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes).map_err(|_| "random_unavailable")?;
    Ok(wire::hex(&bytes))
}

#[derive(Clone, Copy, PartialEq)]
pub enum Mode {
    Disabled,
    Inspect,
    Control,
}
impl Mode {
    pub fn parse(s: &str) -> Result<Self, &'static str> {
        match s {
            "inspect" => Ok(Self::Inspect),
            "control" => Ok(Self::Control),
            _ => Err("developer mode must be inspect or control"),
        }
    }
    fn name(self) -> &'static str {
        match self {
            Self::Disabled => "disabled",
            Self::Inspect => "inspect",
            Self::Control => "control",
        }
    }
}
struct Pending {
    window: String,
    generation: u64,
    action: bool,
    reply: oneshot::Sender<Value>,
}
struct Action {
    fingerprint: Vec<u8>,
    result: Value,
}
struct Listener {
    stop: watch::Sender<bool>,
    task: tauri::async_runtime::JoinHandle<()>,
}
pub struct Developer {
    mode: Mode,
    session: String,
    release: Value,
    listener: Mutex<Option<Listener>>,
    pending: Mutex<HashMap<String, Pending>>,
    actions: Mutex<HashMap<String, Action>>,
}
impl Developer {
    pub fn new(mode: Mode, payload: &std::path::Path) -> Result<Self, &'static str> {
        let release = std::fs::read(payload.join("bundle.sha256"))
            .ok()
            .filter(|v| v.len() <= 2 << 20)
            .map(|v| json!({"inventory_sha256":wire::hex(&Sha256::digest(v))}))
            .unwrap_or(Value::Null);
        Ok(Self {
            mode,
            session: random_id()?,
            release,
            listener: Mutex::new(None),
            pending: Mutex::new(HashMap::new()),
            actions: Mutex::new(HashMap::new()),
        })
    }
    pub fn start(&self, app: &tauri::AppHandle) -> Result<(), String> {
        if self.mode == Mode::Disabled {
            return Ok(());
        }
        let profile = app.state::<Desktop>().paths.profile.clone();
        wire::private_directory(&profile)?;
        let directory = wire::directory(&profile);
        match std::fs::DirBuilder::new().mode(0o700).create(&directory) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(_) => return Err("developer directory unavailable".into()),
        }
        wire::private_directory(&directory)?;
        let path = wire::socket(&profile);
        // Existing endpoints are never removed to obtain a new session.
        let listener = std::os::unix::net::UnixListener::bind(&path)
            .map_err(|_| "developer endpoint exists or is unavailable")?;
        let metadata =
            std::fs::symlink_metadata(&path).map_err(|_| "developer endpoint unavailable")?;
        let socket = OwnedSocket {
            path: path.clone(),
            device: metadata.dev(),
            inode: metadata.ino(),
        };
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))
            .map_err(|_| "developer permissions unavailable")?;
        listener
            .set_nonblocking(true)
            .map_err(|_| "developer endpoint unavailable")?;
        let (stop, mut retired) = watch::channel(false);
        let app = app.clone();
        let task = tauri::async_runtime::spawn(async move {
            let _socket = socket;
            let Ok(listener) = tokio::net::UnixListener::from_std(listener) else {
                return;
            };
            let mut jobs = tokio::task::JoinSet::new();
            let connections = Arc::new(Semaphore::new(16));
            loop {
                tokio::select! { biased;
                    _ = retired.changed() => break,
                    accepted = listener.accept() => {
                        let Ok((stream, _)) = accepted else { break; };
                        if !stream.peer_cred().is_ok_and(|p| p.uid() == unsafe { libc::geteuid() }) { continue; }
                        let Ok(permit) = connections.clone().try_acquire_owned() else { continue; };
                        let app = app.clone();
                        jobs.spawn(async move {
                            let _permit = permit;
                            let service = service_fn(move |r| serve(r, app.clone()));
                            let _ = tokio::time::timeout(Duration::from_secs(125), http1::Builder::new().keep_alive(false).max_headers(32).max_buf_size(16384).serve_connection(TokioIo::new(stream), service)).await;
                        });
                    }
                    _ = jobs.join_next(), if !jobs.is_empty() => {}
                }
            }
            drop(listener);
            jobs.shutdown().await;
        });
        *self.listener.lock().unwrap() = Some(Listener { stop, task });
        Ok(())
    }
    pub async fn stop(&self, app: &tauri::AppHandle) {
        let listener = self.listener.lock().unwrap().take();
        if let Some(listener) = listener {
            self.pending.lock().unwrap().clear();
            for window in app.webview_windows().values() {
                let _ = window.eval("window.__factoryDeveloper?.stop()");
            }
            listener.stop.send_replace(true);
            let _ = listener.task.await;
        }
    }
    fn available(&self, desktop: &Desktop) -> bool {
        self.mode != Mode::Disabled && !desktop.quitting.load(Ordering::SeqCst)
    }
}
struct OwnedSocket {
    path: PathBuf,
    device: u64,
    inode: u64,
}
impl Drop for OwnedSocket {
    fn drop(&mut self) {
        if std::fs::symlink_metadata(&self.path)
            .is_ok_and(|m| m.dev() == self.device && m.ino() == self.inode)
        {
            let _ = std::fs::remove_file(&self.path);
        }
    }
}

#[tauri::command]
pub fn developer_context(window: WebviewWindow, app: tauri::AppHandle) -> Result<Value, String> {
    caller(&window)?;
    let desktop = app.state::<Desktop>();
    let dev = &desktop.developer;
    if !dev.available(&desktop) {
        return Ok(json!({"mode":"disabled"}));
    }
    Ok(
        json!({"session":dev.session,"mode":dev.mode.name(),"window":window.label(),"generation":desktop.generation.load(Ordering::SeqCst),"binding":desktop.product.developer_binding(window.label())}),
    )
}

#[tauri::command]
pub fn developer_reply(
    window: WebviewWindow,
    app: tauri::AppHandle,
    nonce: String,
    result: Value,
) -> Result<(), String> {
    caller(&window)?;
    let desktop = app.state::<Desktop>();
    if !desktop.developer.available(&desktop) {
        return Err("developer session retired".into());
    }
    if serde_json::to_vec(&result)
        .map_err(|_| "invalid developer reply")?
        .len()
        > wire::RESPONSE_LIMIT - 2048
    {
        return Err("developer reply too large".into());
    }
    let mut pending = desktop.developer.pending.lock().unwrap();
    let row = pending
        .get(&nonce)
        .filter(|p| p.window == window.label())
        .ok_or("developer request retired")?;
    let current = row.generation == desktop.generation.load(Ordering::SeqCst);
    let action = row.action;
    let row = pending.remove(&nonce).unwrap();
    let _ = row.reply.send(if current {
        result
    } else {
        wire::error(if action { "outcome_unknown" } else { "retired" })
    });
    Ok(())
}

async fn serve(
    request: Request<Incoming>,
    app: tauri::AppHandle,
) -> Result<Response<Full<Bytes>>, Infallible> {
    let invalid = request.method() != "POST"
        || request.uri().path() != "/dev/v1/call"
        || request.uri().query().is_some()
        || request
            .headers()
            .keys()
            .any(|h| h == "origin" || h.as_str().starts_with("sec-fetch-"));
    let result = if invalid {
        wire::error("invalid_request")
    } else {
        match tokio::time::timeout(
            Duration::from_secs(2),
            Limited::new(request.into_body(), wire::REQUEST_LIMIT).collect(),
        )
        .await
        {
            Ok(Ok(body)) => match wire::Request::decode(&body.to_bytes()) {
                Ok(request) => match request.validate() {
                    Ok(()) => dispatch(app, request).await,
                    Err(e) => wire::error(e),
                },
                Err(e) => wire::error(e),
            },
            _ => wire::error("request_too_large_or_incomplete"),
        }
    };
    let body = serde_json::to_vec(&result).unwrap_or_default();
    let body = if body.len() > wire::RESPONSE_LIMIT {
        serde_json::to_vec(&wire::error("response_too_large")).unwrap()
    } else {
        body
    };
    Ok(Response::builder()
        .status(200)
        .header("Content-Type", "application/json")
        .header("Cache-Control", "no-store")
        .body(Full::new(Bytes::from(body)))
        .unwrap())
}

async fn dispatch(app: tauri::AppHandle, request: wire::Request) -> Value {
    let desktop = app.state::<Desktop>();
    let dev = &desktop.developer;
    if !dev.available(&desktop) {
        return wire::error("retired");
    }
    if matches!(request.operation, wire::Operation::Status) {
        let status = desktop.status.lock().unwrap().clone();
        let windows: Vec<_> = app.webview_windows().values().map(|w| json!({"label":w.label(),"visible":w.is_visible().unwrap_or(false),"minimized":w.is_minimized().unwrap_or(false)})).collect();
        return json!({"ok":true,"version":1,"provenance":"native_status","session":dev.session,"mode":dev.mode.name(),"release":dev.release,"runtime":status,"windows":windows,"capabilities":["tree","click","fill","select","check","wait"],"action_capacity":ACTION_LIMIT});
    }
    if request.is_action() && dev.mode != Mode::Control {
        return wire::error("control_mode_required");
    }
    let windows = app.webview_windows();
    let label = if let Some(label) = &request.window {
        label.clone()
    } else if windows.len() == 1 {
        windows.keys().next().unwrap().clone()
    } else {
        return wire::error("window_required");
    };
    let Some(window) = windows.get(&label) else {
        return wire::error("not_found");
    };
    if caller(window).is_err() {
        return wire::error("retired");
    }
    // A scope from an earlier process cannot acquire a replacement session.
    if let Some(scope) = &request.scope {
        if !serde_json::from_str::<Value>(scope)
            .is_ok_and(|v| v["session"] == dev.session && v["window"] == label)
        {
            return wire::error("stale_scope");
        }
    }
    let fingerprint = serde_json::to_vec(&request).unwrap();
    if let Some(action) = &request.action {
        let mut actions = dev.actions.lock().unwrap();
        if let Some(prior) = actions.get(action) {
            return if prior.fingerprint == fingerprint {
                prior.result.clone()
            } else {
                wire::error("action_conflict")
            };
        }
        if actions.len() >= ACTION_LIMIT {
            return wire::error("action_capacity_exhausted");
        }
        // Reserve before dispatch. Lost replies and client disconnects never free an ID.
        actions.insert(
            action.clone(),
            Action {
                fingerprint,
                result: wire::error("outcome_unknown"),
            },
        );
    }
    let result = dispatch_window(&app, window, &request).await;
    if let Some(action) = &request.action {
        if let Some(prior) = dev.actions.lock().unwrap().get_mut(action) {
            prior.result = result.clone();
        }
    }
    result
}

async fn dispatch_window(
    app: &tauri::AppHandle,
    window: &WebviewWindow,
    request: &wire::Request,
) -> Value {
    let desktop = app.state::<Desktop>();
    let dev = &desktop.developer;
    if request.is_action()
        && (!window.is_visible().unwrap_or(false) || window.is_minimized().unwrap_or(true))
    {
        return wire::error("window_not_visible");
    }
    let nonce = match random_id() {
        Ok(n) => n,
        Err(e) => return wire::error(e),
    };
    let generation = desktop.generation.load(Ordering::SeqCst);
    let (tx, rx) = oneshot::channel();
    {
        let mut pending = dev.pending.lock().unwrap();
        let rows: Vec<_> = pending
            .values()
            .filter(|p| p.window == window.label())
            .collect();
        if rows.len() >= 8 || (request.is_action() && rows.iter().any(|p| p.action)) {
            return wire::error("busy");
        }
        pending.insert(
            nonce.clone(),
            Pending {
                window: window.label().into(),
                generation,
                action: request.is_action(),
                reply: tx,
            },
        );
    }
    let _pending = PendingGuard {
        app: app.clone(),
        window: window.clone(),
        nonce: nonce.clone(),
    };
    let script = format!(
        "window.__factoryDeveloper?.dispatch({})",
        json!({"nonce":nonce,"request":request,"deadline_unix_ms":std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_millis() + u128::from(request.timeout_ms())})
    );
    let result = if window.eval(script).is_err() {
        wire::error("unavailable")
    } else {
        match tokio::time::timeout(Duration::from_millis(request.timeout_ms()), rx).await {
            Ok(Ok(result)) => result,
            _ => wire::error(if request.is_action() {
                "outcome_unknown"
            } else {
                "deadline"
            }),
        }
    };
    json!({"ok":result["ok"] == true,"error":result.get("error"),"session":dev.session,"window":window.label(),"action":request.action,"provenance":"ui_dom","observed_unix_ms":std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_millis(),"result":result})
}
struct PendingGuard {
    app: tauri::AppHandle,
    window: WebviewWindow,
    nonce: String,
}
impl Drop for PendingGuard {
    fn drop(&mut self) {
        self.app
            .state::<Desktop>()
            .developer
            .pending
            .lock()
            .unwrap()
            .remove(&self.nonce);
        let _ = self.window.eval(format!(
            "window.__factoryDeveloper?.cancel({})",
            json!(self.nonce)
        ));
    }
}
