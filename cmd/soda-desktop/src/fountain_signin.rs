//! Fountain uses the same owned/cancellable desktop sign-in lifecycle as GitHub.
use super::{
    caller,
    signin::{Phase, Prompt, View},
    Desktop,
};
use soda_desktop_runtime::{
    credentials::{Credentials, Provider},
    fountain_oauth::{Client, Error},
};
use std::sync::Arc;
use tauri::{Manager, WebviewWindow};
use tauri_plugin_opener::OpenerExt;
fn update(
    app: &tauri::AppHandle,
    attempt: u64,
    epoch: u64,
    next: Phase,
) -> Result<(), Error> {
    if app
        .state::<Desktop>()
        .fountain_signin
        .update(app, attempt, epoch, next)
    {
        Ok(())
    } else {
        Err(Error::Unavailable)
    }
}
fn message(error: Error) -> String {
    match error {
        Error::Credential(soda_desktop_runtime::credentials::Error::Locked) => {
            "Unlock your desktop keyring and try again.".into()
        }
        _ => error.to_string(),
    }
}
#[tauri::command]
pub(super) fn fountain_sign_in_status(
    window: WebviewWindow,
    app: tauri::AppHandle,
) -> Result<View, String> {
    caller(&window)?;
    Ok(app.state::<Desktop>().fountain_signin.view())
}
#[tauri::command]
pub(super) async fn fountain_sign_in(
    window: WebviewWindow,
    app: tauri::AppHandle,
    origin: String,
    control: Option<bool>,
    issues_only: Option<bool>,
) -> Result<View, String> {
    caller(&window)?;
    if control.unwrap_or(false) && issues_only.unwrap_or(false) {
        return Err("Choose issue collaboration or automation control access.".into());
    }
    let desktop = app.state::<Desktop>();
    let store: Arc<Credentials> = desktop.credentials()?;
    let _saving = desktop.fountain_signin.saving.lock().await;
    let epoch = desktop.generation();
    let worker = app.clone();
    desktop
        .fountain_signin
        .start(window.label(), move |attempt| {
            tauri::async_runtime::spawn(async move {
                let result = async {
                    let client = if issues_only.unwrap_or(false) {
                        Client::configured_issues(&origin).await?
                    } else if control.unwrap_or(false) {
                        Client::configured_control(&origin).await?
                    } else {
                        Client::configured(&origin).await?
                    };
                    let authorization = client.begin().await?;
                    let prompt = authorization.prompt();
                    update(
                        &worker,
                        attempt,
                        epoch,
                        Phase::Waiting(Prompt::browser(
                            prompt.authorization_uri.clone(),
                            prompt.expires_at,
                        )),
                    )?;
                    let (account, grant) = authorization.wait().await?;
                    update(&worker, attempt, epoch, Phase::Verifying)?;
                    let owner = worker.state::<Desktop>();
                    let _saving = owner.fountain_signin.saving.lock().await;
                    update(&worker, attempt, epoch, Phase::Saving)?;
                    store
                        .store_grant(account, grant)
                        .await
                        .map_err(Error::Credential)?;
                    update(&worker, attempt, epoch, Phase::Complete)
                }
                .await;
                if let Err(error) = result {
                    worker
                        .state::<Desktop>()
                        .fountain_signin
                        .failed(attempt, message(error));
                }
            })
        })
}
#[tauri::command]
pub(super) async fn fountain_sign_in_cancel(
    window: WebviewWindow,
    app: tauri::AppHandle,
    attempt: u64,
) -> Result<View, String> {
    caller(&window)?;
    Ok(app
        .state::<Desktop>()
        .fountain_signin
        .cancel(Some(attempt), Some(window.label()))
        .await)
}
#[tauri::command]
pub(super) async fn fountain_sign_in_open(
    window: WebviewWindow,
    app: tauri::AppHandle,
    attempt: u64,
) -> Result<(), String> {
    caller(&window)?;
    let prompt = app
        .state::<Desktop>()
        .fountain_signin
        .browser_prompt(attempt, window.label())?;
    tauri::async_runtime::spawn_blocking(move || app.opener().open_url(prompt.uri(), None::<&str>))
        .await
        .map_err(|_| "Browser could not open.")?
        .map_err(|_| "Browser could not open.".into())
}
#[tauri::command]
pub(super) async fn fountain_refresh(
    window: WebviewWindow,
    app: tauri::AppHandle,
    connection: String,
) -> Result<View, String> {
    caller(&window)?;
    let desktop = app.state::<Desktop>();
    let store = desktop.credentials()?;
    let cap = store
        .capability(&connection)
        .await
        .map_err(|e| e.to_string())?;
    if cap.account().provider != Provider::Fountain {
        return Err("Choose a Fountain account.".into());
    }
    let epoch = desktop.generation();
    let worker = app.clone();
    let _saving = desktop.fountain_signin.saving.lock().await;
    desktop
        .fountain_signin
        .start(window.label(), move |attempt| {
            tauri::async_runtime::spawn(async move {
                let result = async {
                    update(&worker, attempt, epoch, Phase::Verifying)?;
                    let saved = store
                        .status()
                        .await
                        .map_err(Error::Credential)?
                        .into_iter()
                        .find(|value| value.connection == cap.account().connection)
                        .ok_or(Error::Reauthorize)?;
                    let client = match saved.grant.as_ref().map(|grant| grant.scope.as_str()) {
                        Some("write:issue") => {
                            Client::configured_issues(&cap.account().origin).await?
                        }
                        Some("read:repository" | "read:repository read:issue") => {
                            Client::configured(&cap.account().origin).await?
                        }
                        Some("write:repository" | "write:repository write:issue") => {
                            Client::configured_control(&cap.account().origin).await?
                        }
                        _ => return Err(Error::Reauthorize),
                    };
                    client.refresh(&store, &cap).await?;
                    update(&worker, attempt, epoch, Phase::Complete)
                }
                .await;
                if let Err(error) = result {
                    worker
                        .state::<Desktop>()
                        .fountain_signin
                        .failed(attempt, message(error));
                }
            })
        })
}

#[tauri::command]
pub(super) async fn fountain_enrollment(
    window: WebviewWindow,
    app: tauri::AppHandle,
    connection: String,
    repository: String,
    action: String,
) -> Result<soda_desktop_runtime::fountain_provider::Enrollment, String> {
    caller(&window)?;
    if !matches!(action.as_str(), "enroll" | "status" | "withdraw") {
        return Err("Choose an enrollment action.".into());
    }
    let desktop = app.state::<Desktop>();
    let epoch = desktop.generation();
    let client = repository_client(&app, &connection, &repository).await?;
    let result = match action.as_str() {
        "enroll" => client.enroll().await,
        "status" => client.status().await,
        _ => client.withdraw().await,
    };
    if !desktop.live(epoch) {
        return Err("Fountain account changed; the original enrollment command is kept.".into());
    }
    result.map_err(|_| {
        "Fountain enrollment is unavailable. Retry observes the original command.".into()
    })
}

async fn repository_client(
    app: &tauri::AppHandle,
    connection: &str,
    repository: &str,
) -> Result<soda_desktop_runtime::fountain_provider::Client, String> {
    let (owner, name) = repository.split_once('/').ok_or("Use owner/repository.")?;
    let desktop = app.state::<Desktop>();
    let store = desktop.credentials()?;
    let profile = desktop
        .status()
        .profile
        .ok_or("Factory is unavailable.")?;
    let cap = store
        .capability(&connection)
        .await
        .map_err(|e| e.to_string())?;
    soda_desktop_runtime::fountain_provider::retained_client(
        store,
        cap,
        &desktop.paths.profile,
        &profile,
        owner,
        name,
    )
    .await
    .map_err(|e| {
        if e.code == "fountain_sign_in_again" {
            "Sign in again before enrolling this older saved account.".to_owned()
        } else {
            "Fountain enrollment is unavailable. Its original command is kept.".to_owned()
        }
    })
}

#[tauri::command]
pub(super) async fn fountain_controller(
    window: WebviewWindow,
    app: tauri::AppHandle,
    connection: String,
    repository: String,
    selection: Option<soda_desktop_runtime::fountain_provider::Selection>,
    authorization: Option<soda_desktop_runtime::fountain_provider::OperationAuthorization>,
) -> Result<serde_json::Value, String> {
    caller(&window)?;
    if selection.is_some() && authorization.is_some() {
        return Err("Choose one controller command.".into());
    }
    let desktop = app.state::<Desktop>();
    let epoch = desktop.generation();
    let client = repository_client(&app, &connection, &repository).await?;
    let client = Arc::new(client);
    let value = if let Some(request) = selection {
        serde_json::to_value(
            client
                .select_runtime(&desktop.paths.profile, &request)
                .await
                .map_err(|_| "Controller request is unavailable. Its original command is kept.")?,
        )
    } else if let Some(request) = authorization {
        serde_json::to_value(
            client
                .authorize_operations(&desktop.paths.profile, &request)
                .await
                .map_err(|_| {
                    "Operation authorization is unavailable. Its original command is kept."
                })?,
        )
    } else {
        serde_json::to_value(
            client
                .placement()
                .await
                .map_err(|_| "Controller status is unavailable.")?,
        )
    }
    .map_err(|_| "Controller response could not be verified.")?;
    if !desktop.live(epoch) {
        return Err("Factory changed; the original controller command is kept.".into());
    }
    let observed = desktop.runtime.lock().unwrap().as_ref().ok_or("Factory is unavailable.")?.observe_fountain_controller(client);
    observed.await.map_err(|_| "Central placement could not be recorded. The original controller command is kept.")?;
    if !desktop.live(epoch) { return Err("Factory changed; the original controller command is kept.".into()); }
    Ok(value)
}
