//! Desktop-owned authorization lifecycle. Only safe view state crosses IPC.
use super::{caller, Desktop};
use serde::Serialize;
use soda_desktop_runtime::credentials::{Credentials, Error as CredentialError};
use soda_desktop_runtime::github::Error;
use std::sync::atomic::Ordering;
use std::sync::{Arc, Mutex};
use tauri::{Manager, WebviewWindow};
use tauri_plugin_opener::OpenerExt;

#[derive(Clone, Serialize)]
pub(super) struct Prompt {
    user_code: String,
    verification_uri: String,
    expires_at: u64,
}
#[derive(Clone, Serialize)]
pub(super) struct View {
    attempt: u64,
    phase: String,
    prompt: Option<Prompt>,
    error: Option<String>,
}
impl Default for View {
    fn default() -> Self {
        Self {
            attempt: 0,
            phase: "idle".into(),
            prompt: None,
            error: None,
        }
    }
}
struct State {
    view: View,
    owner: String,
    task: Option<tauri::async_runtime::JoinHandle<()>>,
}
pub(super) struct SignIn {
    state: Mutex<State>,
    // Storage and explicit cancellation have one linearization point. A cancel
    // during saving waits for that bounded write; a completed login wins cleanly.
    saving: tauri::async_runtime::Mutex<()>,
}
impl SignIn {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(State {
                view: View::default(),
                owner: String::new(),
                task: None,
            }),
            saving: tauri::async_runtime::Mutex::new(()),
        }
    }
    pub fn view(&self) -> View {
        self.state.lock().unwrap().view.clone()
    }
    pub fn retire(&self) {
        let mut state = self.state.lock().unwrap();
        if let Some(task) = state.task.take() {
            task.abort();
        }
        state.view.phase = "idle".into();
        state.view.prompt = None;
        state.view.error = None;
    }
    pub async fn cancel(&self, attempt: Option<u64>, owner: Option<&str>) -> View {
        let _saving = self.saving.lock().await;
        let task = {
            let mut state = self.state.lock().unwrap();
            if attempt.is_some_and(|id| id != state.view.attempt)
                || owner.is_some_and(|owner| owner != state.owner)
                || !active(&state.view.phase)
            {
                return state.view.clone();
            }
            state.view.phase = "cancelled".into();
            state.view.prompt = None;
            state.view.error = None;
            state.task.take()
        };
        if let Some(task) = task {
            task.abort();
            let _ = task.await;
        }
        self.view()
    }
}
fn active(phase: &str) -> bool {
    matches!(phase, "requesting" | "waiting" | "verifying" | "saving")
}
fn message(error: Error) -> String {
    match error {
        Error::Credential(CredentialError::Locked) => "Unlock your desktop keyring and try again.",
        Error::Credential(_) => {
            "Your account could not be saved. Check your desktop keyring and try again."
        }
        Error::Provider(403) => "GitHub sign-in was declined. You can try again.",
        Error::Provider(408) => "This sign-in code expired. Start again to get a new code.",
        Error::Protocol => "GitHub sign-in could not be verified. Try again.",
        _ => "GitHub sign-in is unavailable. Try again when connected.",
    }
    .into()
}
fn update(
    app: &tauri::AppHandle,
    attempt: u64,
    epoch: u64,
    phase: &str,
    prompt: Option<Prompt>,
) -> Result<(), Error> {
    let desktop = app.state::<Desktop>();
    if epoch != desktop.generation.load(Ordering::SeqCst)
        || desktop.quitting.load(Ordering::SeqCst)
        || desktop.starting.load(Ordering::SeqCst)
    {
        return Err(Error::Unavailable);
    }
    let mut state = desktop.signin.state.lock().unwrap();
    if state.view.attempt != attempt || !active(&state.view.phase) {
        return Err(Error::Unavailable);
    }
    state.view.phase = phase.into();
    state.view.prompt = prompt;
    Ok(())
}

#[tauri::command]
pub(super) fn github_sign_in_status(
    window: WebviewWindow,
    app: tauri::AppHandle,
) -> Result<View, String> {
    caller(&window)?;
    Ok(app.state::<Desktop>().signin.view())
}
#[tauri::command]
pub(super) async fn github_sign_in(
    window: WebviewWindow,
    app: tauri::AppHandle,
) -> Result<View, String> {
    caller(&window)?;
    let desktop = app.state::<Desktop>();
    let store: Arc<Credentials> = desktop.credentials()?;
    let _saving = desktop.signin.saving.lock().await;
    let epoch = desktop.generation.load(Ordering::SeqCst);
    let start = desktop
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .ok_or("Factory is unavailable")?
        .github_device_start();
    let mut state = desktop.signin.state.lock().unwrap();
    if active(&state.view.phase) {
        return Err("GitHub sign-in is already in progress.".into());
    }
    let attempt = state
        .view
        .attempt
        .checked_add(1)
        .ok_or("Restart Factory before signing in again")?;
    state.view = View {
        attempt,
        phase: "requesting".into(),
        prompt: None,
        error: None,
    };
    state.owner = window.label().into();
    let worker = app.clone();
    state.task = Some(tauri::async_runtime::spawn(async move {
        let result = async {
            let authorization = start.await?;
            let verifier = authorization.verifier();
            let prompt = authorization.prompt();
            update(
                &worker,
                attempt,
                epoch,
                "waiting",
                Some(Prompt {
                    user_code: prompt.user_code.clone(),
                    verification_uri: prompt.verification_uri.clone(),
                    expires_at: prompt.expires_at,
                }),
            )?;
            let grant = authorization.wait().await?;
            update(&worker, attempt, epoch, "verifying", None)?;
            let (account, grant) = verifier.verify(grant).await?;
            let owner = worker.state::<Desktop>();
            let _saving = owner.signin.saving.lock().await;
            update(&worker, attempt, epoch, "saving", None)?;
            store
                .store_grant(account, grant)
                .await
                .map_err(Error::Credential)?;
            update(&worker, attempt, epoch, "complete", None)
        }
        .await;
        if let Err(error) = result {
            let owner = worker.state::<Desktop>();
            let mut state = owner.signin.state.lock().unwrap();
            if state.view.attempt == attempt && active(&state.view.phase) {
                state.view.phase = "failed".into();
                state.view.prompt = None;
                state.view.error = Some(message(error));
            }
        }
    }));
    Ok(state.view.clone())
}
#[tauri::command]
pub(super) async fn github_sign_in_cancel(
    window: WebviewWindow,
    app: tauri::AppHandle,
    attempt: u64,
) -> Result<View, String> {
    caller(&window)?;
    Ok(app
        .state::<Desktop>()
        .signin
        .cancel(Some(attempt), None)
        .await)
}
#[tauri::command]
pub(super) async fn github_sign_in_open(
    window: WebviewWindow,
    app: tauri::AppHandle,
    attempt: u64,
) -> Result<(), String> {
    caller(&window)?;
    let view = app.state::<Desktop>().signin.view();
    if view.attempt != attempt || view.phase != "waiting" {
        return Err("Start GitHub sign-in to open the verification page.".into());
    }
    tauri::async_runtime::spawn_blocking(move || {
        app.opener()
            .open_url("https://github.com/login/device", None::<&str>)
    })
    .await
    .map_err(|_| "Your browser could not open. Visit github.com/login/device.")?
    .map_err(|_| "Your browser could not open. Visit github.com/login/device.".into())
}
