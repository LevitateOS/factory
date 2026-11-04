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
use tauri::{Manager, WebviewWindow};
use tokio::sync::watch;

#[derive(Clone, Serialize)]
pub(super) struct Binding {
    session: String,
    generation: u64,
    identity: Value,
    repository: Option<Value>,
    account_label: String,
}
struct Selection {
    id: String,
    window: String,
    connection: String,
    repository: Option<String>,
    generation: u64,
    cancelled: watch::Sender<bool>,
    binding: Mutex<Option<Binding>>,
    connecting: AtomicBool,
}
struct Job {
    window: String,
    selection: String,
    active: Arc<AtomicBool>,
    task: tauri::async_runtime::JoinHandle<()>,
}
pub(super) struct Connections {
    selections: Mutex<HashMap<String, Arc<Selection>>>,
    jobs: Mutex<HashMap<String, Job>>,
    next: AtomicU64,
}
impl Connections {
    pub fn new() -> Self {
        Self {
            selections: Mutex::new(HashMap::new()),
            jobs: Mutex::new(HashMap::new()),
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
        || selection.generation != desktop.generation.load(Ordering::SeqCst)
        || desktop.starting.load(Ordering::SeqCst)
        || desktop.quitting.load(Ordering::SeqCst)
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
    current(desktop, selection)?;
    let credentials = desktop.credentials()?;
    let capability = credentials
        .capability(&selection.connection)
        .await
        .map_err(|_| "Factory account is unavailable")?;
    current(desktop, selection)?;
    let request = {
        let runtime = desktop.runtime.lock().unwrap();
        current(desktop, selection)?;
        runtime
            .as_ref()
            .ok_or("Factory is unavailable")?
            .admit_product(credentials, capability, selection.repository.clone())?
    };
    let response = request.request(method, path, body).await?;
    current(desktop, selection)?;
    Ok(Reply {
        status: response.status,
        body: if response.body.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&response.body).map_err(|_| "invalid native product response")?
        },
    })
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
) -> Result<String, String> {
    product_caller(&window)?;
    if !registry(&connection)
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
            generation: desktop.generation.load(Ordering::SeqCst),
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
        if reply.status != 200
            || !canonical(&reply.body["actor"])
            || !reply.body["host"].as_str().is_some_and(registry)
        {
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

#[derive(Serialize)]
pub(super) struct Completion {
    reply: Option<Reply>,
    error: Option<&'static str>,
}
#[tauri::command]
pub(super) fn factory_product_request(
    window: WebviewWindow,
    app: tauri::AppHandle,
    session: String,
    request: super::bridge::ProductRequest,
    channel: tauri::ipc::Channel<Completion>,
) -> Result<String, String> {
    product_caller(&window)?;
    let (method, path, body) = request.request()?;
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
        let result = tokio::select! {biased; _ = cancelled(&selection) => Err("Factory connection retired".into()), result = call(&desktop, &selection, method, &path, &body) => result};
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
            let completion = match result {
                Ok(reply) => Completion {
                    reply: Some(reply),
                    error: None,
                },
                Err(_) => Completion {
                    reply: None,
                    error: Some(
                        "Factory request is unavailable. Reconnect your account and try again.",
                    ),
                },
            };
            let _ = channel.send(completion);
        }
        jobs.remove(&task_id);
    });
    jobs.insert(
        id.clone(),
        Job {
            window: window.label().into(),
            selection: session,
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
