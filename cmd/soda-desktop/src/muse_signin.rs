//! Window/profile ownership for the CLI's existing login handoff.
use super::{caller, Desktop};
use serde::Serialize;
use soda_desktop_runtime::factory_provider::{MuseLogin, MuseLoginView, ToolStatus, Tools};
use std::sync::{Arc, Mutex};
use tauri::{Manager, WebviewWindow};
use tauri_plugin_opener::OpenerExt;

struct State {
    attempt: u64,
    epoch: u64,
    owner: String,
    view: MuseLoginView,
    preparing: Option<tauri::async_runtime::JoinHandle<()>>,
    login: Option<MuseLogin>,
}
pub(super) struct MuseSignIn {
    state: Mutex<State>,
    transition: tauri::async_runtime::Mutex<()>,
}
#[derive(Serialize)]
pub(super) struct View {
    attempt: u64,
    available: bool,
    can_control: bool,
    checking: bool,
    tools: Option<ToolStatus>,
    #[serde(flatten)]
    view: MuseLoginView,
}
fn active(view: &MuseLoginView) -> bool { matches!(view.phase, "requesting" | "waiting") }
fn tools(app: &tauri::AppHandle) -> Result<Arc<Tools>, String> {
    let state = app.state::<Desktop>();
    if state.ready_epoch().is_none() { return Err("Muse sign-in is unavailable until Factory is ready".into()); }
    let tools = state.runtime.lock().unwrap().as_ref().ok_or("native runtime unavailable")?.execution_tools();
    tools
}
impl MuseSignIn {
    pub fn new() -> Self {
        Self { state: Mutex::new(State { attempt: 0, epoch: 0, owner: String::new(), view: Default::default(), preparing: None, login: None }), transition: tauri::async_runtime::Mutex::new(()) }
    }
    fn view(&self, label: &str, epoch: u64, available: bool, tools: Option<ToolStatus>) -> View {
        let state = self.state.lock().unwrap();
        View { attempt: state.attempt, available: available && state.preparing.is_none(), can_control: state.owner == label && state.epoch == epoch, checking: state.preparing.is_some(), tools,
            view: if state.epoch != epoch { Default::default() } else { state.login.as_ref().map(MuseLogin::view).unwrap_or_else(|| state.view.clone()) } }
    }
    pub fn retire(&self) -> Option<tauri::async_runtime::JoinHandle<()>> {
        let (task, login) = {
            let mut state = self.state.lock().unwrap();
            state.attempt += 1;
            state.view = Default::default();
            state.owner.clear();
            (state.preparing.take(), state.login.take())
        };
        if let Some(task) = &task { task.abort(); }
        drop(login);
        task
    }
    pub async fn cancel(&self, attempt: Option<u64>, owner: Option<&str>) {
        let _transition = self.transition.lock().await;
        let (task, login) = {
            let mut state = self.state.lock().unwrap();
            if attempt.is_some_and(|attempt| attempt != state.attempt)
                || owner.is_some_and(|owner| owner != state.owner) { return; }
            state.view = state.login.as_ref().map(MuseLogin::view).unwrap_or_else(|| state.view.clone());
            if active(&state.view) { state.view = MuseLoginView { phase: "cancelled", prompt: None, error: None }; }
            (state.preparing.take(), state.login.take())
        };
        if let Some(task) = task { task.abort(); let _ = task.await; }
        drop(login);
    }
}
pub(super) fn status(app: &tauri::AppHandle, label: &str) -> View {
    let tool_status = tools(app).ok().and_then(|tools| tools.status().ok());
    let available = MuseLogin::available() && tool_status.as_ref().is_some_and(|status| {
        status.state == "ready" && status.checked_at_unix.is_some_and(|checked| {
            std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).is_ok_and(|now| now.as_secs().saturating_sub(checked) < Tools::REFRESH.as_secs())
        })
    });
    app.state::<Desktop>().muse_signin.view(label, app.state::<Desktop>().generation(), available, tool_status)
}
#[tauri::command]
pub(super) async fn muse_tools_refresh(window: WebviewWindow, app: tauri::AppHandle) -> Result<View, String> {
    caller(&window)?;
    let epoch = app.state::<Desktop>().generation();
    let native_tools = tools(&app)?;
    let state = app.state::<Desktop>();
    let _transition = state.muse_signin.transition.lock().await;
    let mut auth = state.muse_signin.state.lock().unwrap();
    if !state.live(epoch) {
        return Err("Muse update owner is no longer current".into());
    }
    let view = auth.login.as_ref().map(MuseLogin::view).unwrap_or_else(|| auth.view.clone());
    if active(&view) || auth.preparing.is_some() {
        return Err("A Muse check or sign-in is already in progress".into());
    }
    auth.attempt += 1;
    let attempt = auth.attempt;
    auth.epoch = epoch;
    auth.owner = window.label().to_owned();
    let worker = app.clone();
    auth.preparing = Some(tauri::async_runtime::spawn(async move {
        let _ = native_tools.refresh().await;
        let state = worker.state::<Desktop>();
        let mut auth = state.muse_signin.state.lock().unwrap();
        if auth.attempt == attempt && auth.epoch == epoch {
            auth.preparing = None;
        }
    }));
    drop(auth);
    Ok(status(&app, window.label()))
}
#[tauri::command]
pub(super) async fn muse_sign_in_status(window: WebviewWindow, app: tauri::AppHandle) -> Result<View, String> {
    caller(&window)?;
    let label = window.label().to_owned();
    tauri::async_runtime::spawn_blocking(move || status(&app, &label)).await.map_err(|_| "Muse status unavailable".into())
}
#[tauri::command]
pub(super) async fn muse_sign_in(window: WebviewWindow, app: tauri::AppHandle) -> Result<View, String> {
    caller(&window)?;
    let epoch = app.state::<Desktop>().generation();
    let native_tools = tools(&app)?;
    if !status(&app, window.label()).available { return Err("Muse sign-in is unavailable until its current release is verified".into()); }
    let state = app.state::<Desktop>();
    let _transition = state.muse_signin.transition.lock().await;
    let label = window.label().to_owned();
    let mut auth = state.muse_signin.state.lock().unwrap();
    if !state.live(epoch) {
        return Err("Muse sign-in owner is no longer current".into());
    }
    let view = auth.login.as_ref().map(MuseLogin::view).unwrap_or_else(|| auth.view.clone());
    if active(&view) || auth.preparing.is_some() { return Err("A Muse check or sign-in is already in progress".into()); }
    drop(auth.login.take());
    auth.attempt += 1;
    let attempt = auth.attempt;
    auth.epoch = epoch;
    auth.owner = label.clone();
    auth.view = MuseLoginView { phase: "requesting", prompt: None, error: None };
    let worker = app.clone();
    let profile = state.paths.profile.clone();
    auth.preparing = Some(tauri::async_runtime::spawn(async move {
        let selection = native_tools.inspect().await;
        let state = worker.state::<Desktop>();
        let mut auth = state.muse_signin.state.lock().unwrap();
        if auth.attempt != attempt || auth.epoch != epoch || auth.owner != label
            || !state.live(epoch)
        { return; }
        let result = selection.and_then(|selection| MuseLogin::start(&profile, selection));
        match result {
            Ok(login) => auth.login = Some(login),
            Err(_) => auth.view = MuseLoginView { phase: "failed", prompt: None, error: Some("Muse sign-in is unavailable. Verify Muse and try again.") },
        }
        auth.preparing = None;
    }));
    drop(auth);
    Ok(status(&app, window.label()))
}
#[tauri::command]
pub(super) async fn muse_sign_in_cancel(window: WebviewWindow, app: tauri::AppHandle, attempt: u64) -> Result<View, String> {
    caller(&window)?;
    app.state::<Desktop>().muse_signin.cancel(Some(attempt), Some(window.label())).await;
    Ok(status(&app, window.label()))
}
#[tauri::command]
pub(super) fn muse_sign_in_open(window: WebviewWindow, app: tauri::AppHandle, attempt: u64) -> Result<(), String> {
    caller(&window)?;
    let view = status(&app, window.label());
    if view.attempt != attempt || !view.can_control || view.view.phase != "waiting" { return Err("Muse sign-in prompt is no longer current".into()); }
    let prompt = view.view.prompt.ok_or("Muse sign-in prompt unavailable")?;
    app.opener().open_url(prompt.verification_uri, None::<&str>).map_err(|_| "Muse sign-in page could not open".into())
}
