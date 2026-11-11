//! Desktop-owned authorization lifecycle. Only safe view state crosses IPC.
use super::{caller, Desktop};
use serde::Serialize;
use soda_desktop_runtime::credentials::{Credentials, Error as CredentialError};
use soda_desktop_runtime::github::Error;
use std::sync::{Arc, Mutex};
use tauri::{Manager, WebviewWindow};
use tauri_plugin_opener::OpenerExt;

#[derive(Clone, Serialize)]
pub(super) struct Prompt {
    user_code: String,
    verification_uri: String,
    expires_at: u64,
}
impl Prompt {
    pub(super) fn browser(uri: String, expires_at: u64) -> Self {
        Self {
            user_code: String::new(),
            verification_uri: uri,
            expires_at,
        }
    }
    pub(super) fn uri(&self) -> &str {
        &self.verification_uri
    }
}
#[derive(Clone, Serialize)]
pub(super) struct View {
    attempt: u64,
    phase: &'static str,
    prompt: Option<Prompt>,
    error: Option<String>,
}
/// Lifecycle: Requesting -> Waiting -> Verifying -> Saving -> Complete, or
/// Failed/Cancelled from any active phase. A refresh goes Requesting ->
/// Verifying -> Complete. Prompt and error exist only in their own phase.
pub(super) enum Phase {
    Idle,
    Requesting,
    Waiting(Prompt),
    Verifying,
    Saving,
    Complete,
    Failed(String),
    Cancelled,
}
impl Phase {
    fn active(&self) -> bool {
        matches!(
            self,
            Phase::Requesting | Phase::Waiting(_) | Phase::Verifying | Phase::Saving
        )
    }
    fn name(&self) -> &'static str {
        match self {
            Phase::Idle => "idle",
            Phase::Requesting => "requesting",
            Phase::Waiting(_) => "waiting",
            Phase::Verifying => "verifying",
            Phase::Saving => "saving",
            Phase::Complete => "complete",
            Phase::Failed(_) => "failed",
            Phase::Cancelled => "cancelled",
        }
    }
}
struct State {
    attempt: u64,
    phase: Phase,
    owner: String,
    task: Option<tauri::async_runtime::JoinHandle<()>>,
}
impl State {
    fn view(&self) -> View {
        View {
            attempt: self.attempt,
            phase: self.phase.name(),
            prompt: match &self.phase {
                Phase::Waiting(prompt) => Some(prompt.clone()),
                _ => None,
            },
            error: match &self.phase {
                Phase::Failed(error) => Some(error.clone()),
                _ => None,
            },
        }
    }
    // The only phase transition: attempt and owner must match (when given) and
    // the current phase must be active.
    fn advance(&mut self, attempt: Option<u64>, owner: Option<&str>, next: Phase) -> bool {
        if attempt.is_some_and(|id| id != self.attempt)
            || owner.is_some_and(|owner| owner != self.owner)
            || !self.phase.active()
        {
            return false;
        }
        self.phase = next;
        true
    }
}
pub(super) struct SignIn {
    state: Mutex<State>,
    // Storage and explicit cancellation have one linearization point. A cancel
    // during saving waits for that bounded write; a completed login wins cleanly.
    pub(super) saving: tauri::async_runtime::Mutex<()>,
}
impl SignIn {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(State {
                attempt: 0,
                phase: Phase::Idle,
                owner: String::new(),
                task: None,
            }),
            saving: tauri::async_runtime::Mutex::new(()),
        }
    }
    pub fn view(&self) -> View {
        self.state.lock().unwrap().view()
    }
    pub fn retire(&self) -> Option<tauri::async_runtime::JoinHandle<()>> {
        let mut state = self.state.lock().unwrap();
        let task = state.task.take();
        if let Some(task) = &task {
            task.abort();
        }
        state.phase = Phase::Idle;
        task
    }
    pub(super) fn start(
        &self,
        owner: &str,
        spawn: impl FnOnce(u64) -> tauri::async_runtime::JoinHandle<()>,
    ) -> Result<View, String> {
        let mut state = self.state.lock().unwrap();
        if state.phase.active() {
            return Err("Sign-in is already in progress.".into());
        }
        state.attempt = state
            .attempt
            .checked_add(1)
            .ok_or("Restart Factory before signing in again")?;
        state.phase = Phase::Requesting;
        state.owner = owner.into();
        state.task = Some(spawn(state.attempt));
        Ok(state.view())
    }
    /// Worker-side transition: refused when the runtime changed (epoch,
    /// not ready) or the attempt is no longer the active one.
    pub(super) fn update(
        &self,
        app: &tauri::AppHandle,
        attempt: u64,
        epoch: u64,
        next: Phase,
    ) -> bool {
        let desktop = app.state::<Desktop>();
        if !desktop.live(epoch) {
            return false;
        }
        self.state
            .lock()
            .unwrap()
            .advance(Some(attempt), None, next)
    }
    pub(super) fn failed(&self, attempt: u64, message: String) {
        self.state
            .lock()
            .unwrap()
            .advance(Some(attempt), None, Phase::Failed(message));
    }
    pub(super) fn browser_prompt(&self, attempt: u64, owner: &str) -> Result<Prompt, String> {
        let state = self.state.lock().unwrap();
        match &state.phase {
            Phase::Waiting(prompt) if state.attempt == attempt && state.owner == owner => {
                Ok(prompt.clone())
            }
            _ => Err("Sign-in is no longer waiting.".into()),
        }
    }
    pub async fn cancel(&self, attempt: Option<u64>, owner: Option<&str>) -> View {
        let _saving = self.saving.lock().await;
        let task = {
            let mut state = self.state.lock().unwrap();
            if !state.advance(attempt, owner, Phase::Cancelled) {
                return state.view();
            }
            state.task.take()
        };
        if let Some(task) = task {
            task.abort();
            let _ = task.await;
        }
        self.view()
    }
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
    next: Phase,
) -> Result<(), Error> {
    if app.state::<Desktop>().signin.update(app, attempt, epoch, next) {
        Ok(())
    } else {
        Err(Error::Unavailable)
    }
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
pub(super) async fn github_refresh(
    window: WebviewWindow,
    app: tauri::AppHandle,
    connection: String,
) -> Result<View,String> {
    caller(&window)?;
    let desktop=app.state::<Desktop>();
    let store=desktop.credentials()?;
    let cap=store.capability(&connection).await.map_err(|e|e.to_string())?;
    if cap.account().provider!=soda_desktop_runtime::credentials::Provider::Github {
        return Err("Choose a GitHub account.".into());
    }
    let epoch=desktop.generation();
    let worker=app.clone();
    let _saving=desktop.signin.saving.lock().await;
    desktop.signin.start(window.label(),move |attempt|tauri::async_runtime::spawn(async move {
        let result=async {
            update(&worker,attempt,epoch,Phase::Verifying)?;
            let owner=worker.state::<Desktop>();
            let refresh={
                let runtime=owner.runtime.lock().unwrap();
                runtime.as_ref().ok_or(Error::Unavailable)?.github_refresh(&store,&cap)
            };
            refresh.await?;
            update(&worker,attempt,epoch,Phase::Complete)
        }.await;
        if let Err(error)=result { worker.state::<Desktop>().signin.failed(attempt,message(error)); }
    }))
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
    let epoch = desktop.generation();
    let start = desktop
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .ok_or("Factory is unavailable")?
        .github_device_start();
    let worker = app.clone();
    desktop.signin.start(window.label(), move |attempt| {
        tauri::async_runtime::spawn(async move {
            let result = async {
                let authorization = start.await?;
                let verifier = authorization.verifier();
                let prompt = authorization.prompt();
                update(
                    &worker,
                    attempt,
                    epoch,
                    Phase::Waiting(Prompt {
                        user_code: prompt.user_code.clone(),
                        verification_uri: prompt.verification_uri.clone(),
                        expires_at: prompt.expires_at,
                    }),
                )?;
                let grant = authorization.wait().await?;
                update(&worker, attempt, epoch, Phase::Verifying)?;
                let (account, grant) = verifier.verify(grant).await?;
                let owner = worker.state::<Desktop>();
                let _saving = owner.signin.saving.lock().await;
                update(&worker, attempt, epoch, Phase::Saving)?;
                store
                    .store_grant(account, grant)
                    .await
                    .map_err(Error::Credential)?;
                update(&worker, attempt, epoch, Phase::Complete)
            }
            .await;
            if let Err(error) = result {
                worker.state::<Desktop>().signin.failed(attempt, message(error));
            }
        })
    })
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
        .cancel(Some(attempt), Some(window.label()))
        .await)
}
#[tauri::command]
pub(super) async fn github_sign_in_open(
    window: WebviewWindow,
    app: tauri::AppHandle,
    attempt: u64,
) -> Result<(), String> {
    caller(&window)?;
    app.state::<Desktop>()
        .signin
        .browser_prompt(attempt, window.label())
        .map_err(|_| "Start GitHub sign-in to open the verification page.")?;
    tauri::async_runtime::spawn_blocking(move || {
        app.opener()
            .open_url("https://github.com/login/device", None::<&str>)
    })
    .await
    .map_err(|_| "Your browser could not open. Visit github.com/login/device.")?
    .map_err(|_| "Your browser could not open. Visit github.com/login/device.".into())
}
