//! JEV (TypeSafe) key commands for the shell. The key crosses IPC once, inward,
//! in `jev_connect`; every reply is the key-free status.
use super::{caller, Desktop};
use soda_desktop_runtime::credentials::{Error as CredentialError, Token};
use soda_desktop_runtime::jev::{Error, Jev, Status};
use tauri::{Manager, WebviewWindow};
use tauri_plugin_opener::OpenerExt;
use zeroize::Zeroizing;

fn message(error: Error) -> String {
    match error {
        Error::NotAccepted => "TypeSafe didn’t accept this key. Check that you copied all of it, or make a new key in the TypeSafe console.".into(),
        Error::Offline => "Factory can’t reach TypeSafe right now. Check your connection and try again.".into(),
        Error::Unexpected(0) => "TypeSafe sent a reply Factory couldn’t read. Try again later.".into(),
        Error::Unexpected(status) => format!("TypeSafe answered with an unexpected error ({status}). Try again later."),
        Error::Invalid => "That doesn’t look like a TypeSafe API key. Paste the whole key from the TypeSafe console.".into(),
        Error::Missing => "No TypeSafe key is saved on this Mac.".into(),
        Error::Credential(CredentialError::Locked) => "Unlock your desktop keyring and try again.".into(),
        Error::Credential(_) | Error::Storage => "The key could not be saved on this Mac. Try again.".into(),
    }
}

fn connector(app: &tauri::AppHandle) -> Result<(std::sync::Arc<soda_desktop_runtime::credentials::Credentials>, Jev), String> {
    let desktop = app.state::<Desktop>();
    let store = desktop.credentials()?;
    let jev = Jev::new(&desktop.paths.profile).map_err(message)?;
    Ok((store, jev))
}

#[tauri::command]
pub(super) async fn jev_status(window: WebviewWindow, app: tauri::AppHandle) -> Result<Status, String> {
    caller(&window)?;
    let (store, jev) = connector(&app)?;
    jev.status(&store).await.map_err(message)
}

#[tauri::command]
pub(super) async fn jev_connect(window: WebviewWindow, app: tauri::AppHandle, key: String) -> Result<Status, String> {
    let key = Zeroizing::new(key);
    caller(&window)?;
    let token = Token::new(key.trim().as_bytes().to_vec()).map_err(|_| message(Error::Invalid))?;
    let (store, jev) = connector(&app)?;
    jev.connect(&store, token).await.map_err(message)
}

#[tauri::command]
pub(super) async fn jev_check(window: WebviewWindow, app: tauri::AppHandle) -> Result<Status, String> {
    caller(&window)?;
    let (store, jev) = connector(&app)?;
    jev.check(&store).await.map_err(message)
}

#[tauri::command]
pub(super) async fn jev_set_model(window: WebviewWindow, app: tauri::AppHandle, model: String) -> Result<Status, String> {
    caller(&window)?;
    let (store, jev) = connector(&app)?;
    jev.set_model(&store, &model).await.map_err(message)
}

#[tauri::command]
pub(super) async fn jev_remove(window: WebviewWindow, app: tauri::AppHandle) -> Result<Status, String> {
    caller(&window)?;
    let (store, jev) = connector(&app)?;
    jev.remove(&store).await.map_err(message)
}

/// Opens one of TypeSafe's fixed console pages in the browser.
#[tauri::command]
pub(super) async fn jev_open_console(window: WebviewWindow, app: tauri::AppHandle, page: String) -> Result<(), String> {
    caller(&window)?;
    let url = match page.as_str() {
        "keys" => "https://console.typesafe.ai/keys",
        "rate_limits" => "https://console.typesafe.ai/rate-limits",
        _ => return Err("Unknown TypeSafe page.".into()),
    };
    tauri::async_runtime::spawn_blocking(move || app.opener().open_url(url, None::<&str>))
        .await
        .map_err(|_| "Your browser could not open. Visit console.typesafe.ai.")?
        .map_err(|_| "Your browser could not open. Visit console.typesafe.ai.".into())
}
