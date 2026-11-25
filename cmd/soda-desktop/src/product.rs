//! Window-owned product connections. Go remains the product/transaction owner;
//! this owner admits native custody and retires each window's pending IPC work.
use super::{caller, Desktop, Reply};
use serde::Serialize;
use serde_json::Value;
use soda_desktop_runtime::github::Read;
use std::collections::HashMap;
use std::sync::{
    atomic::{AtomicBool, AtomicU64, Ordering},
    Arc, Mutex,
};
use tauri::ipc::InvokeResponseBody;
use tauri::{Manager, WebviewWindow};
use tokio::sync::watch;

#[derive(Clone, Serialize)]
pub(super) struct Binding {
    session: String,
    generation: u64,
    identity: Value,
    repository: Option<Value>,
    account_label: String,
    provider: String,
    origin: String,
}
struct Selection {
    id: String,
    window: String,
    connection: String,
    repository: Option<String>,
    fountain_repository: Option<String>,
    generation: u64,
    cancelled: watch::Sender<bool>,
    binding: Mutex<Option<Binding>>,
    connecting: AtomicBool,
}
struct Job {
    window: String,
    selection: String,
    output: bool,
    active: Arc<AtomicBool>,
    task: tauri::async_runtime::JoinHandle<()>,
}
/// The account and repository a user asked a new window to open. It is only a
/// starting choice: the window still selects through the ordinary connection path.
#[derive(Clone, Serialize, serde::Deserialize)]
pub(super) struct WindowTarget {
    connection: String,
    repository: String,
}
pub(super) struct Connections {
    selections: Mutex<HashMap<String, Arc<Selection>>>,
    jobs: Mutex<HashMap<String, Job>>,
    targets: Mutex<HashMap<String, WindowTarget>>,
    next: AtomicU64,
}
impl Connections {
    /// Holds a new window's starting choice until that window takes it once.
    pub(super) fn aim(&self, window: &str, target: WindowTarget) -> Result<(), String> {
        if !registry(&target.connection) || !canonical(&Value::String(target.repository.clone())) {
            return Err("invalid Factory account or repository selection".into());
        }
        self.targets.lock().unwrap().insert(window.into(), target);
        Ok(())
    }
    pub(super) fn developer_binding(&self, window: &str) -> Value {
        self.selections.lock().unwrap().get(window).map(|s| {
            serde_json::json!({"selection":s.id,"connection":s.connection,"repository":s.repository,"binding":*s.binding.lock().unwrap(),"retired":*s.cancelled.borrow()})
        }).unwrap_or(Value::Null)
    }
    pub fn new() -> Self {
        Self {
            selections: Mutex::new(HashMap::new()),
            jobs: Mutex::new(HashMap::new()),
            targets: Mutex::new(HashMap::new()),
            next: AtomicU64::new(1),
        }
    }
    fn id(&self) -> String {
        self.next.fetch_add(1, Ordering::SeqCst).to_string()
    }
    fn selected(&self, window: &str, id: &str) -> Result<Arc<Selection>, String> {
        self.selections
            .lock()
            .unwrap()
            .get(window)
            .filter(|s| s.id == id && !*s.cancelled.borrow())
            .cloned()
            .ok_or("Factory connection retired".into())
    }
    // Mark every selection retired before cancelling tasks. Callers join the
    // returned handles outside locks; no product I/O holds child/lifecycle locks.
    pub fn retire(&self, window: Option<&str>) -> Vec<tauri::async_runtime::JoinHandle<()>> {
        self.targets.lock().unwrap().retain(|name, _| window.is_some_and(|w| w != name));
        let mut selections = self.selections.lock().unwrap();
        selections.retain(|name, selection| {
            if window.is_none_or(|w| w == name) {
                selection.cancelled.send_replace(true);
                false
            } else {
                true
            }
        });
        self.retire_jobs(window)
    }
    fn retire_jobs(&self, window: Option<&str>) -> Vec<tauri::async_runtime::JoinHandle<()>> {
        let mut jobs = self.jobs.lock().unwrap();
        let ids: Vec<_> = jobs
            .iter()
            .filter(|(_, j)| window.is_none_or(|w| w == j.window))
            .map(|(id, _)| id.clone())
            .collect();
        ids.into_iter()
            .filter_map(|id| jobs.remove(&id))
            .map(|j| {
                j.active.store(false, Ordering::SeqCst);
                j.task.abort();
                j.task
            })
            .collect()
    }
}
fn current(desktop: &Desktop, selection: &Selection) -> Result<(), String> {
    if *selection.cancelled.borrow()
        || !desktop.live(selection.generation)
    {
        return Err("Factory connection retired".into());
    }
    Ok(())
}
async fn call(
    desktop: &Desktop,
    selection: &Selection,
    method: &str,
    path: &str,
    body: &[u8],
) -> Result<Reply, String> {
    json(exchange(desktop, selection, method, path, body).await?)
}
fn json(response: soda_unix_http::Response) -> Result<Reply, String> {
    Ok(Reply {
        status: response.status,
        body: if response.body.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&response.body).map_err(|_| "invalid native product response")?
        },
    })
}
async fn exchange(
    desktop: &Desktop,
    selection: &Selection,
    method: &str,
    path: &str,
    body: &[u8],
) -> Result<soda_unix_http::Response, String> {
    current(desktop, selection)?;
    let credentials = desktop.credentials()?;
    let capability = credentials
        .capability(&selection.connection)
        .await
        .map_err(|_| "Factory account is unavailable")?;
    current(desktop, selection)?;
    let fountain = if capability.account().provider == soda_desktop_runtime::credentials::Provider::Fountain {
        let name = selection.fountain_repository.as_deref().ok_or("Choose an enrolled Fountain repository")?;
        let (owner, repository) = name.split_once('/').ok_or("Use owner/repository")?;
        let profile = desktop.status().profile.ok_or("Factory is unavailable")?;
        let client = soda_desktop_runtime::fountain_provider::retained_client(
            credentials.clone(), credentials.renew_capability(&capability).await.map_err(|_|"Factory account changed")?, &desktop.paths.profile, &profile, owner, repository,
        ).await.map_err(|_| "Current Fountain enrollment is unavailable")?;
        Some(Arc::new(client))
    } else {
        if selection.fountain_repository.is_some() { return Err("Factory account changed".into()); }
        None
    };
    let request = {
        let runtime = desktop.runtime.lock().unwrap();
        current(desktop, selection)?;
        let runtime = runtime.as_ref().ok_or("Factory is unavailable")?;
        if let Some(client) = fountain {
            runtime.admit_fountain_product(credentials, capability,
                selection.repository.clone().ok_or("Choose an enrolled Fountain repository")?, client)?
        } else {
            runtime.admit_product(credentials, capability, selection.repository.clone())?
        }
    };
    let response = request.request(method, path, body).await?;
    current(desktop, selection)?;
    Ok(response)
}
async fn cancelled(selection: &Selection) {
    let mut receiver = selection.cancelled.subscribe();
    let _ = receiver.wait_for(|cancelled| *cancelled).await;
}
async fn join(tasks: Vec<tauri::async_runtime::JoinHandle<()>>) {
    for task in tasks {
        let _ = task.await;
    }
}
fn canonical(value: &Value) -> bool {
    value
        .as_str()
        .is_some_and(|s| s.parse::<i64>().is_ok_and(|n| n > 0 && n.to_string() == s))
}
fn registry(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn provider_rate_limit(reply: &Reply) -> Result<(), String> {
    if reply.status == 429 && reply.body["error"]["code"] == "provider_rate_limited" {
        return Err(reply.body["error"]["message"]
            .as_str()
            .filter(|s| !s.is_empty() && s.len() <= 256 && !s.chars().any(char::is_control))
            .unwrap_or("The provider is rate limited. Wait before reconnecting.")
            .to_owned());
    }
    Ok(())
}
fn product_caller(window: &WebviewWindow) -> Result<(), String> {
    caller(window)?;
    if !window.is_visible().map_err(|_| "window is unavailable")? {
        return Err("open application window required".into());
    }
    Ok(())
}

#[tauri::command]
pub(super) async fn factory_prepare_connection(
    window: WebviewWindow,
    app: tauri::AppHandle,
    connection: String,
    repository: Option<String>,
    fountain_repository: Option<String>,
) -> Result<String, String> {
    product_caller(&window)?;
    if !registry(&connection)
        || fountain_repository.as_ref().is_some_and(|name| name.len() > 201 || !name.split_once('/').is_some_and(|(owner,repo)| !owner.is_empty() && !repo.is_empty() && name.bytes().all(|b|b.is_ascii_alphanumeric()||matches!(b,b'/'|b'_'|b'-'|b'.'))))
        || repository
            .as_ref()
            .is_some_and(|r| !canonical(&Value::String(r.clone())))
    {
        return Err("invalid Factory account or repository selection".into());
    }
    let desktop = app.state::<Desktop>();
    desktop.credentials()?;
    // Replacement and cancellation are linearized together. A competing connect
    // cannot publish an older binding while the first request awaits the provider.
    let (selection, retired) = {
        let mut selections = desktop.product.selections.lock().unwrap();
        if let Some(old) = selections.remove(window.label()) {
            old.cancelled.send_replace(true);
        }
        let retired = desktop.product.retire_jobs(Some(window.label()));
        let selection = Arc::new(Selection {
            id: desktop.product.id(),
            window: window.label().into(),
            connection,
            repository,
            fountain_repository,
            generation: desktop.generation(),
            cancelled: watch::channel(false).0,
            binding: Mutex::new(None),
            connecting: AtomicBool::new(false),
        });
        selections.insert(window.label().into(), selection.clone());
        (selection, retired)
    };
    join(retired).await;
    current(&desktop, &selection)?;
    Ok(selection.id.clone())
}

/// The starting choice for this window, given once; the main window has none.
#[tauri::command]
pub(super) fn factory_window_target(window: WebviewWindow, app: tauri::AppHandle) -> Result<Option<WindowTarget>, String> {
    caller(&window)?;
    Ok(app.state::<Desktop>().product.targets.lock().unwrap().remove(window.label()))
}

#[tauri::command]
pub(super) async fn factory_connect(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
) -> Result<Binding, String> {
    product_caller(&window)?;
    let desktop = app.state::<Desktop>();
    let selection = desktop.product.selected(window.label(), &session)?;
    if selection.connecting.swap(true, Ordering::SeqCst) {
        return Err("Factory connection was already requested".into());
    }
    let work = async {
        // Closing can race registration; admit the window again after the
        // selection is installed so closure either refuses or cancels it.
        product_caller(&window)?;
        let reply = call(&desktop, &selection, "GET", "/api/factory/identity", &[]).await?;
        provider_rate_limit(&reply)?;
        if reply.status != 200
            || !canonical(&reply.body["actor"])
            || !reply.body["host"].as_str().is_some_and(registry)
        {
            #[cfg(feature = "native-check")]
            eprintln!(
                "Native check account connection refused: HTTP {}, code={:?}, canonical_actor={}, canonical_host={}",
                reply.status,
                reply.body["error"]["code"].as_str(),
                canonical(&reply.body["actor"]),
                reply.body["host"].as_str().is_some_and(registry)
            );
            return Err("Factory account could not be connected".into());
        }
        let repository = if let Some(remote) = &selection.repository {
            let catalog = call(
                &desktop,
                &selection,
                "GET",
                "/api/factory/collaboration/repositories",
                &[],
            )
            .await?;
            provider_rate_limit(&catalog)?;
            let items = catalog.body["items"]
                .as_array()
                .ok_or("invalid selected repository")?;
            if catalog.status != 200
                || catalog.body["complete"] != true
                || items.len() != 1
                || !canonical(&items[0]["id"])
                || items[0]["host"] != reply.body["host"]
                || items[0]["external_id"] != *remote
            {
                return Err("Factory repository could not be connected".into());
            }
            Some(items[0].clone())
        } else {
            None
        };
        let credentials = desktop.credentials()?;
        let cap = credentials
            .capability(&selection.connection)
            .await
            .map_err(|_| "Factory account is unavailable")?;
        let account_label = cap
            .account()
            .label
            .clone()
            .filter(|s| !s.is_empty() && s.len() <= 256 && !s.chars().any(char::is_control))
            .unwrap_or_else(|| cap.account().account.clone());
        current(&desktop, &selection)?;
        product_caller(&window)?;
        let binding = Binding {
            session: selection.id.clone(),
            generation: selection.generation,
            identity: reply.body,
            repository,
            account_label,
            provider: match cap.account().provider {
                soda_desktop_runtime::credentials::Provider::Github=>"github",
                soda_desktop_runtime::credentials::Provider::Fountain=>"fountain",
                _=>return Err("Unsupported Factory account".into()),
            }.into(),
            origin: cap.account().origin.clone(),
        };
        *selection.binding.lock().unwrap() = Some(binding.clone());
        Ok(binding)
    };
    tokio::select! {biased; _ = cancelled(&selection) => Err("Factory connection retired".into()), result = work => result}
}

#[tauri::command]
pub(super) async fn factory_disconnect(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: Option<String>,
) -> Result<(), String> {
    caller(&window)?;
    let desktop = app.state::<Desktop>();
    let retired = {
        let mut selections = desktop.product.selections.lock().unwrap();
        if session
            .as_ref()
            .is_none_or(|id| selections.get(window.label()).is_some_and(|s| &s.id == id))
        {
            if let Some(old) = selections.remove(window.label()) {
                old.cancelled.send_replace(true);
            }
            desktop.product.retire_jobs(Some(window.label()))
        } else {
            vec![]
        }
    };
    join(retired).await;
    Ok(())
}

#[tauri::command]
pub(super) async fn factory_repositories(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    page: u32,
) -> Result<Reply, String> {
    product_caller(&window)?;
    if page == 0 || page > 1_000_000 {
        return Err("invalid repository page".into());
    }
    let desktop = app.state::<Desktop>();
    let selection = desktop.product.selected(window.label(), &session)?;
    let work = discover(&desktop, &selection, Read::Repositories { page, limit: 20 });
    tokio::select! {biased; _ = cancelled(&selection) => Err("Factory connection retired".into()), result = work => result}
}
async fn discover(desktop: &Desktop, selection: &Selection, query: Read) -> Result<Reply, String> {
    current(desktop, selection)?;
    if selection.binding.lock().unwrap().is_none() {
        return Err("Factory account is not connected".into());
    }
    let credentials = desktop.credentials()?;
    let cap = credentials
        .capability(&selection.connection)
        .await
        .map_err(|_| "Factory account is unavailable")?;
    let read = desktop
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .ok_or("Factory is unavailable")?
        .github_read(&credentials, &cap, query);
    let reply = read
        .await
        .map_err(|_| "Repository discovery is unavailable")?;
    current(desktop, selection)?;
    Ok(Reply {
        status: reply.status,
        body: reply.body,
    })
}
#[tauri::command]
pub(super) async fn factory_find_repository(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    name: String,
) -> Result<Reply, String> {
    product_caller(&window)?;
    let desktop = app.state::<Desktop>();
    let selection = desktop.product.selected(window.label(), &session)?;
    let work = discover(&desktop, &selection, Read::FindRepository { name });
    tokio::select! {biased; _ = cancelled(&selection) => Err("Factory connection retired".into()), result = work => result}
}
/// New project: creates a repository owned by this window's connected GitHub
/// account. The window then connects to it through `factory_prepare_connection`.
#[tauri::command]
pub(super) async fn factory_create_repository(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    name: String,
    description: String,
    private: bool,
) -> Result<Reply, String> {
    product_caller(&window)?;
    let desktop = app.state::<Desktop>();
    let selection = desktop.product.selected(window.label(), &session)?;
    let work = async {
        current(&desktop, &selection)?;
        if selection.binding.lock().unwrap().is_none() {
            return Err("Factory account is not connected".to_string());
        }
        let credentials = desktop.credentials()?;
        let cap = credentials
            .capability(&selection.connection)
            .await
            .map_err(|_| "Factory account is unavailable")?;
        let create = desktop
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .ok_or("Factory is unavailable")?
            .github_create_repository(
                &credentials,
                &cap,
                soda_desktop_runtime::github_provider::RepositoryCreate { name, description, private },
            );
        let reply = create.await.map_err(|_| "Repository creation is unavailable")?;
        current(&desktop, &selection)?;
        Ok(Reply { status: reply.status, body: reply.body })
    };
    tokio::select! {biased; _ = cancelled(&selection) => Err("Factory connection retired".into()), result = work => result}
}
#[tauri::command]
pub(super) async fn factory_open_link(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    url: String,
) -> Result<(), String> {
    use tauri_plugin_opener::OpenerExt;
    product_caller(&window)?;
    let target = tauri::Url::parse(&url).map_err(|_| "invalid web link")?;
    if url.len() > 4096
        || url.chars().any(char::is_control)
        || !matches!(target.scheme(), "https" | "http" | "mailto")
        || !target.username().is_empty()
        || target.password().is_some()
    {
        return Err("unsupported web link".into());
    }
    let selection = app
        .state::<Desktop>()
        .product
        .selected(window.label(), &session)?;
    if selection.binding.lock().unwrap().is_none() {
        return Err("Factory account is not connected".into());
    }
    current(&app.state::<Desktop>(), &selection)?;
    tauri::async_runtime::spawn_blocking(move || {
        product_caller(&window)?;
        current(&app.state::<Desktop>(), &selection)?;
        app.opener()
            .open_url(target.as_str(), None::<&str>)
            .map_err(|_| "web link could not open".to_string())
    })
    .await
    .map_err(|_| "web link is unavailable")?
}

/// Saves an artifact the window already read through its connected selection.
/// The webview cannot follow `blob:` downloads (navigation admits only the
/// app page), so the bytes arrive as a raw IPC body and land in the user's
/// download directory under a sanitized new name; nothing is overwritten.
#[tauri::command]
pub(super) async fn factory_save_artifact(
    window: WebviewWindow,
    app: tauri::AppHandle,
    request: tauri::ipc::Request<'_>,
) -> Result<String, String> {
    product_caller(&window)?;
    let header = |name: &str| {
        request
            .headers()
            .get(name)
            .and_then(|value| value.to_str().ok())
            .ok_or("invalid artifact download")
    };
    let session = header("x-factory-session")?;
    let name = file_name(&percent_decoded(header("x-factory-name")?)?);
    let tauri::ipc::InvokeBody::Raw(bytes) = request.body() else {
        return Err("invalid artifact download".into());
    };
    if bytes.len() > soda_desktop_runtime::PRODUCT_ARTIFACT_BYTES {
        return Err("artifact exceeds the download limit".into());
    }
    let bytes = bytes.clone();
    let desktop = app.state::<Desktop>();
    let selection = desktop.product.selected(window.label(), session)?;
    if selection.binding.lock().unwrap().is_none() {
        return Err("Factory account is not connected".into());
    }
    current(&desktop, &selection)?;
    let directory = app
        .path()
        .download_dir()
        .ok()
        .filter(|path| path.is_absolute() && path.is_dir())
        .ok_or("Downloads folder is unavailable")?;
    tauri::async_runtime::spawn_blocking(move || save(&directory, &name, &bytes))
        .await
        .map_err(|_| "artifact could not be saved")?
}
fn percent_decoded(value: &str) -> Result<String, String> {
    let (raw, mut bytes) = (value.as_bytes(), Vec::with_capacity(value.len()));
    let mut index = 0;
    while index < raw.len() {
        if raw[index] == b'%' {
            let pair = raw.get(index + 1..index + 3).and_then(|pair| std::str::from_utf8(pair).ok());
            bytes.push(pair.and_then(|pair| u8::from_str_radix(pair, 16).ok()).ok_or("invalid artifact name")?);
            index += 3;
        } else {
            bytes.push(raw[index]);
            index += 1;
        }
    }
    String::from_utf8(bytes).map_err(|_| "invalid artifact name".into())
}
/// One path component: no separators, controls or leading dots, bounded length.
fn file_name(raw: &str) -> String {
    let cleaned: String = raw
        .chars()
        .map(|c| if c.is_control() || matches!(c, '/' | '\\') { '_' } else { c })
        .take(120)
        .collect();
    let cleaned = cleaned.trim().trim_start_matches('.').trim();
    if cleaned.is_empty() { "artifact".into() } else { cleaned.into() }
}
fn save(directory: &std::path::Path, name: &str, bytes: &[u8]) -> Result<String, String> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let (stem, extension) = match name.rsplit_once('.') {
        Some((stem, extension)) if !stem.is_empty() && extension.chars().count() <= 16 => (stem, format!(".{extension}")),
        _ => (name, String::new()),
    };
    for attempt in 0..100 {
        let candidate = if attempt == 0 { name.to_owned() } else { format!("{stem} ({attempt}){extension}") };
        // create_new is O_EXCL: an existing file or link is never followed or replaced.
        match std::fs::OpenOptions::new().write(true).create_new(true).mode(0o600).open(directory.join(&candidate)) {
            Ok(mut file) => {
                let written = file.write_all(bytes).and_then(|()| file.sync_all());
                if written.is_err() {
                    let _ = std::fs::remove_file(directory.join(&candidate));
                    return Err("artifact could not be saved".into());
                }
                return Ok(candidate);
            }
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(_) => return Err("artifact could not be saved".into()),
        }
    }
    Err("too many files already use this artifact name".into())
}
#[derive(Serialize)]
struct Completion {
    reply: Option<Reply>,
    error: Option<&'static str>,
}
/// A JSON completion, or an artifact read's bytes as one raw message:
/// a 4-byte big-endian header length, the JSON header
/// `{status, content_type, length}`, then exactly `length` body bytes.
fn completion(result: Result<soda_unix_http::Response, String>, artifact: bool) -> InvokeResponseBody {
    let reply = match result {
        Ok(response) if artifact => {
            let header = serde_json::json!({
                "status": response.status,
                "content_type": response.content_type.as_deref().unwrap_or("application/octet-stream"),
                "length": response.body.len(),
            })
            .to_string();
            let mut frame = Vec::with_capacity(4 + header.len() + response.body.len());
            frame.extend_from_slice(&(header.len() as u32).to_be_bytes());
            frame.extend_from_slice(header.as_bytes());
            frame.extend_from_slice(&response.body);
            return InvokeResponseBody::Raw(frame);
        }
        Ok(response) => json(response).ok(),
        Err(_) => None,
    };
    let completion = Completion {
        error: reply.is_none().then_some("Factory request is unavailable. Reconnect your account and try again."),
        reply,
    };
    InvokeResponseBody::Json(serde_json::to_string(&completion).unwrap_or_else(|_| "null".into()))
}
#[tauri::command]
pub(super) fn factory_product_request(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    request: soda_desktop_runtime::ProductRequest,
    channel: tauri::ipc::Channel<InvokeResponseBody>,
) -> Result<String, String> {
    product_caller(&window)?;
    let (method, path, body) = request.request()?;
    let artifact = soda_desktop_runtime::product_artifact_read(method, &path);
    let desktop = app.state::<Desktop>();
    let selections = desktop.product.selections.lock().unwrap();
    let selection = selections
        .get(window.label())
        .filter(|s| s.id == session)
        .cloned()
        .ok_or("Factory connection retired")?;
    current(&desktop, &selection)?;
    if selection.binding.lock().unwrap().is_none() {
        return Err("Factory account is not connected".into());
    }
    let id = desktop.product.id();
    let active = Arc::new(AtomicBool::new(true));
    let mut jobs = desktop.product.jobs.lock().unwrap();
    if jobs.len() >= 16 {
        return Err("native product request limit reached".into());
    }
    let task_app = app.clone();
    let task_id = id.clone();
    let task_active = active.clone();
    let task = tauri::async_runtime::spawn(async move {
        let desktop = task_app.state::<Desktop>();
        let result = tokio::select! {biased; _ = cancelled(&selection) => Err("Factory connection retired".into()), result = exchange(&desktop, &selection, method, &path, &body) => result};
        let message = completion(result, artifact);
        // Serialize delivery with connection retirement and explicit cancellation.
        // A buffered completion is also scoped by session at the UI consumer.
        let selections = desktop.product.selections.lock().unwrap();
        let mut jobs = desktop.product.jobs.lock().unwrap();
        if selections
            .get(&selection.window)
            .is_some_and(|s| s.id == selection.id)
            && current(&desktop, &selection).is_ok()
            && task_active.load(Ordering::SeqCst)
        {
            let _ = channel.send(message);
        }
        jobs.remove(&task_id);
    });
    jobs.insert(
        id.clone(),
        Job {
            window: window.label().into(),
            selection: session,
            output: false,
            active,
            task,
        },
    );
    Ok(id)
}
#[tauri::command]
pub(super) async fn factory_product_cancel(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    request: String,
) -> Result<(), String> {
    caller(&window)?;
    let retired = {
        let desktop = app.state::<Desktop>();
        let mut jobs = desktop.product.jobs.lock().unwrap();
        if jobs
            .get(&request)
            .is_some_and(|j| j.window != window.label() || j.selection != session)
        {
            return Err("request belongs to another connection".into());
        }
        jobs.remove(&request)
    };
    if let Some(job) = retired {
        job.active.store(false, Ordering::SeqCst);
        job.task.abort();
        let _ = job.task.await;
    }
    Ok(())
}
/// Admit one live-output response for the selected GitHub repository. Go
/// observes the provider once for the binding; later checks stay local.
async fn output(
    desktop: &Desktop,
    selection: &Selection,
    target: &str,
    chunks: tokio::sync::mpsc::Sender<Vec<u8>>,
) -> Result<u16, String> {
    current(desktop, selection)?;
    let credentials = desktop.credentials()?;
    let capability = credentials
        .capability(&selection.connection)
        .await
        .map_err(|_| "Factory account is unavailable")?;
    current(desktop, selection)?;
    if capability.account().provider != soda_desktop_runtime::credentials::Provider::Github
        || selection.fountain_repository.is_some()
    {
        return Err("Live output is unavailable for this account".into());
    }
    let request = {
        let runtime = desktop.runtime.lock().unwrap();
        current(desktop, selection)?;
        let runtime = runtime.as_ref().ok_or("Factory is unavailable")?;
        runtime.admit_product_output(
            credentials,
            capability,
            selection.repository.clone().ok_or("Choose a repository")?,
        )?
    };
    let status = request.output(target, chunks).await?;
    current(desktop, selection)?;
    Ok(status)
}
/// Frames the shared Watch view admits; anything else from Go is dropped.
fn output_frame(line: &[u8]) -> Option<Value> {
    let value: Value = serde_json::from_slice(line).ok()?;
    matches!(value["type"].as_str(), Some("status" | "output" | "closed")).then_some(value)
}
/// One read-only live-output response for this window's selection, delivered
/// as Watch frames and then `{"type":"end","status":N}`. A window has one: a
/// newer request replaces it. Window close, account change and
/// `factory_product_cancel` end it; the run itself is never touched.
#[tauri::command]
pub(super) fn factory_product_output(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    run: String,
    cursor: u64,
    channel: tauri::ipc::Channel<InvokeResponseBody>,
) -> Result<String, String> {
    product_caller(&window)?;
    if run.is_empty()
        || run.len() > 64
        || !run.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-')
        || cursor > 256 << 20
    {
        return Err("invalid Factory run output request".into());
    }
    let target = format!("/api/factory/runs/{run}/output?cursor={cursor}");
    let desktop = app.state::<Desktop>();
    let selection = desktop
        .product
        .selections
        .lock()
        .unwrap()
        .get(window.label())
        .filter(|s| s.id == session)
        .cloned()
        .ok_or("Factory connection retired")?;
    current(&desktop, &selection)?;
    if selection.binding.lock().unwrap().is_none() {
        return Err("Factory account is not connected".into());
    }
    let id = desktop.product.id();
    let active = Arc::new(AtomicBool::new(true));
    let mut jobs = desktop.product.jobs.lock().unwrap();
    let replaced: Vec<_> = jobs
        .iter()
        .filter(|(_, j)| j.output && j.window == window.label())
        .map(|(id, _)| id.clone())
        .collect();
    for job in replaced.into_iter().filter_map(|id| jobs.remove(&id)) {
        job.active.store(false, Ordering::SeqCst);
        job.task.abort();
    }
    if jobs.len() >= 16 {
        return Err("native product request limit reached".into());
    }
    let task_app = app.clone();
    let task_id = id.clone();
    let task_active = active.clone();
    let task = tauri::async_runtime::spawn(async move {
        let desktop = task_app.state::<Desktop>();
        let deliver = |message: Value| {
            // Serialize delivery with retirement, replacement and cancellation.
            let selections = desktop.product.selections.lock().unwrap();
            let _jobs = desktop.product.jobs.lock().unwrap();
            if selections
                .get(&selection.window)
                .is_some_and(|s| s.id == selection.id)
                && current(&desktop, &selection).is_ok()
                && task_active.load(Ordering::SeqCst)
            {
                let _ = channel.send(InvokeResponseBody::Json(message.to_string()));
            }
        };
        let (chunks, mut received) = tokio::sync::mpsc::channel::<Vec<u8>>(64);
        let response = output(&desktop, &selection, &target, chunks);
        let forward = async {
            let mut pending = Vec::new();
            while let Some(chunk) = received.recv().await {
                pending.extend_from_slice(&chunk);
                while let Some(end) = pending.iter().position(|&b| b == b'\n') {
                    let line: Vec<u8> = pending.drain(..=end).collect();
                    if let Some(frame) = output_frame(&line[..end]) {
                        deliver(frame);
                    }
                }
                if pending.len() > 65536 {
                    break;
                }
            }
        };
        let status = tokio::select! {
            biased;
            _ = cancelled(&selection) => None,
            (status, ()) = async { tokio::join!(response, forward) } => status.ok(),
        };
        deliver(serde_json::json!({"type":"end","status":status}));
        desktop.product.jobs.lock().unwrap().remove(&task_id);
    });
    jobs.insert(
        id.clone(),
        Job {
            window: window.label().into(),
            selection: session,
            output: true,
            active,
            task,
        },
    );
    Ok(id)
}
