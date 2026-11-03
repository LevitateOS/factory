mod bridge;
mod signin;
use signin::{github_sign_in, github_sign_in_cancel, github_sign_in_open, github_sign_in_status};
#[cfg(feature = "native-check")]
mod check;
#[cfg(feature = "native-check")]
mod check_credentials;
use bridge::{Mutation, Query};
use serde::Serialize;
use serde_json::Value;
use soda_desktop_runtime::credentials::{AccountStatus, Credentials};
use soda_desktop_runtime::supervisor::{prepare_profile, NativeSupervisor};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::{Manager, RunEvent, WebviewUrl, WebviewWindow, WebviewWindowBuilder, WindowEvent};

#[derive(Clone)]
struct Paths {
    profile: PathBuf,
    payload: PathBuf,
    backend: PathBuf,
}
#[derive(Clone, Serialize)]
struct Status {
    phase: String,
    profile: Option<String>,
    instance: Option<String>,
    generation: u64,
}
#[derive(Clone, Serialize)]
struct Reply {
    status: u16,
    body: Value,
}
struct Desktop {
    paths: Paths,
    runtime: Mutex<Option<NativeSupervisor>>,
    credentials: Mutex<Option<Arc<Credentials>>>,
    status: Mutex<Status>,
    starting: AtomicBool,
    quitting: AtomicBool,
    generation: AtomicU64,
    windows: AtomicU64,
    streams: Mutex<HashMap<String, (String, Arc<AtomicBool>)>>,
    signin: signin::SignIn,
    #[cfg(feature = "native-check")]
    check: check::Check,
}
impl Desktop {
    fn start(app: tauri::AppHandle) {
        let state = app.state::<Desktop>();
        if state.quitting.load(Ordering::SeqCst) || state.starting.swap(true, Ordering::SeqCst) {
            return;
        }
        let generation = state.generation.fetch_add(1, Ordering::SeqCst) + 1;
        state.retire_credentials();
        state.cancel_streams();
        {
            let mut status = state.status.lock().unwrap();
            status.phase = "starting".into();
            status.instance = None;
            status.generation = generation;
        }
        std::thread::spawn(move || {
            let state = app.state::<Desktop>();
            let mut runtime = state.runtime.lock().unwrap();
            if let Some(old) = runtime.take() {
                let _ = old.stop();
            }
            match NativeSupervisor::start(
                &state.paths.payload,
                &state.paths.backend,
                &state.paths.profile,
            ) {
                Ok(owner) => match owner.health() {
                    Ok(ready) => {
                        match Credentials::open(&state.paths.profile, &ready.profile) {
                            Ok(store) => *state.credentials.lock().unwrap() = Some(Arc::new(store)),
                            Err(error) => eprintln!("native credentials: {error}"),
                        }
                        *state.status.lock().unwrap() = Status {
                            phase: "ready".into(),
                            profile: Some(ready.profile),
                            instance: Some(ready.instance),
                            generation: state.generation.load(Ordering::SeqCst),
                        };
                        *runtime = Some(owner);
                    }
                    Err(error) => {
                        eprintln!("native startup: {error}");
                        state.status.lock().unwrap().phase = "failed".into();
                    }
                },
                Err(error) => {
                    eprintln!("native startup: {error}");
                    state.status.lock().unwrap().phase = "failed".into();
                }
            }
            state.starting.store(false, Ordering::SeqCst);
        });
    }
    fn cancel_streams(&self) {
        for (_, (_, active)) in self.streams.lock().unwrap().drain() {
            active.store(false, Ordering::SeqCst);
        }
    }
    fn retire_credentials(&self) {
        if let Some(store) = self.credentials.lock().unwrap().take() {
            store.retire();
        }
        self.signin.retire();
    }
    fn credentials(&self) -> Result<Arc<Credentials>, String> {
        if self.starting.load(Ordering::SeqCst)
            || self.quitting.load(Ordering::SeqCst)
            || self.status.lock().unwrap().phase != "ready"
        {
            return Err("native account connection is unavailable".into());
        }
        self.credentials
            .lock()
            .unwrap()
            .clone()
            .ok_or("native credential storage is unavailable".into())
    }
    fn call(&self, method: &str, path: &str, body: Value) -> Result<Reply, String> {
        if self.quitting.load(Ordering::SeqCst) || self.starting.load(Ordering::SeqCst) {
            return Err("Factory is unavailable".into());
        }
        let bytes = if method == "GET" {
            vec![]
        } else {
            serde_json::to_vec(&body).map_err(|_| "invalid request")?
        };
        if bytes.len() > 65536 {
            return Err("request body exceeds the native limit".into());
        }
        let epoch = self.generation.load(Ordering::SeqCst);
        let runtime = self.runtime.lock().unwrap();
        if epoch != self.generation.load(Ordering::SeqCst)
            || self.starting.load(Ordering::SeqCst)
            || self.quitting.load(Ordering::SeqCst)
        {
            return Err("Factory binding retired".into());
        }
        let response = runtime
            .as_ref()
            .ok_or("Factory is unavailable")?
            .product(method, path, &bytes)?;
        if epoch != self.generation.load(Ordering::SeqCst) || self.quitting.load(Ordering::SeqCst) {
            return Err("Factory binding retired".into());
        }
        let body = if response.body.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&response.body).map_err(|_| "invalid native response")?
        };
        Ok(Reply {
            status: response.status,
            body,
        })
    }
}
fn caller(window: &WebviewWindow) -> Result<(), String> {
    let url = window.url().map_err(|_| "unavailable window")?;
    if url.scheme() != "tauri"
        || url.host_str() != Some("localhost")
        || !matches!(url.path(), "" | "/" | "/index.html")
    {
        return Err("local application window required".into());
    }
    Ok(())
}
#[tauri::command]
async fn runtime_status(window: WebviewWindow, app: tauri::AppHandle) -> Result<Status, String> {
    caller(&window)?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<Desktop>();
        if !state.starting.load(Ordering::SeqCst)
            && !state.quitting.load(Ordering::SeqCst)
            && state.status.lock().unwrap().phase == "ready"
        {
            if let Some(owner) = state.runtime.lock().unwrap().as_ref() {
                if owner.health().is_err() {
                    state.generation.fetch_add(1, Ordering::SeqCst);
                    state.cancel_streams();
                    state.retire_credentials();
                    let mut status = state.status.lock().unwrap();
                    status.phase = "failed".into();
                    status.instance = None;
                    status.generation = state.generation.load(Ordering::SeqCst);
                }
            }
        }
        let status = state.status.lock().unwrap().clone();
        Ok(status)
    })
    .await
    .map_err(|_| "native status unavailable")?
}
#[tauri::command]
fn runtime_retry(
    window: WebviewWindow,
    app: tauri::AppHandle,
    state: tauri::State<'_, Desktop>,
) -> Result<(), String> {
    caller(&window)?;
    if state.status.lock().unwrap().phase != "failed" {
        return Err("Factory is not awaiting restart".into());
    }
    Desktop::start(app);
    Ok(())
}
#[tauri::command]
async fn credential_status(
    window: WebviewWindow,
    app: tauri::AppHandle,
) -> Result<Vec<AccountStatus>, String> {
    caller(&window)?;
    app.state::<Desktop>()
        .credentials()?
        .status()
        .await
        .map_err(|e| e.to_string())
}
#[tauri::command]
async fn credential_withdraw(
    window: WebviewWindow,
    app: tauri::AppHandle,
    connection: String,
) -> Result<(), String> {
    caller(&window)?;
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    state.signin.cancel(None, None).await;
    state.signin.retire();
    let generation = state.generation.fetch_add(1, Ordering::SeqCst) + 1;
    state.cancel_streams();
    state.status.lock().unwrap().generation = generation;
    store.withdraw(&connection).await.map_err(|e| e.to_string())
}
#[tauri::command]
async fn factory_query(
    window: WebviewWindow,
    app: tauri::AppHandle,
    query: Query,
) -> Result<Reply, String> {
    caller(&window)?;
    let path = query.target()?;
    tauri::async_runtime::spawn_blocking(move || {
        app.state::<Desktop>().call("GET", &path, Value::Null)
    })
    .await
    .map_err(|_| "native query unavailable")?
}
#[tauri::command]
async fn factory_command(
    window: WebviewWindow,
    app: tauri::AppHandle,
    command: Mutation,
) -> Result<Reply, String> {
    caller(&window)?;
    let (path, body) = command.request()?;
    tauri::async_runtime::spawn_blocking(move || app.state::<Desktop>().call("POST", &path, body))
        .await
        .map_err(|_| "native command unavailable")?
}
#[tauri::command]
fn factory_stream(
    window: WebviewWindow,
    app: tauri::AppHandle,
    run: String,
    channel: tauri::ipc::Channel<Reply>,
) -> Result<String, String> {
    caller(&window)?;
    bridge::run_id(&run)?;
    let state = app.state::<Desktop>();
    let mut streams = state.streams.lock().unwrap();
    if streams.len() >= 4 {
        return Err("native observation limit reached".into());
    }
    let id = state.windows.fetch_add(1, Ordering::SeqCst).to_string();
    let active = Arc::new(AtomicBool::new(true));
    streams.insert(id.clone(), (window.label().into(), active.clone()));
    let epoch = state.generation.load(Ordering::SeqCst);
    drop(streams);
    let observed = id.clone();
    std::thread::spawn(move || {
        loop {
            let state = app.state::<Desktop>();
            if !active.load(Ordering::SeqCst)
                || epoch != state.generation.load(Ordering::SeqCst)
                || state.quitting.load(Ordering::SeqCst)
            {
                break;
            }
            match state.call("GET", &format!("/api/factory/runs/{run}"), Value::Null) {
                Ok(reply) => {
                    let stop = reply.status >= 400;
                    if !active.load(Ordering::SeqCst)
                        || epoch != state.generation.load(Ordering::SeqCst)
                        || channel.send(reply).is_err()
                        || stop
                    {
                        break;
                    }
                }
                Err(_) => break,
            }
            std::thread::sleep(Duration::from_secs(1));
        }
        app.state::<Desktop>()
            .streams
            .lock()
            .unwrap()
            .remove(&observed);
    });
    Ok(id)
}
#[tauri::command]
fn cancel_stream(
    window: WebviewWindow,
    state: tauri::State<'_, Desktop>,
    id: String,
) -> Result<(), String> {
    caller(&window)?;
    let mut streams = state.streams.lock().unwrap();
    if let Some((owner, active)) = streams.get(&id) {
        if owner != window.label() {
            return Err("observation belongs to another window".into());
        }
        active.store(false, Ordering::SeqCst);
    }
    streams.remove(&id);
    Ok(())
}
fn open(app: &tauri::AppHandle, label: &str) -> Result<(), String> {
    if let Some(window) = app.get_webview_window(label) {
        window.show().map_err(|_| "window unavailable")?;
        let _ = window.unminimize();
        let _ = window.set_focus();
        return Ok(());
    }
    let profile = app.state::<Desktop>().paths.profile.clone();
    WebviewWindowBuilder::new(app, label, WebviewUrl::App("index.html".into()))
        .title("Factory")
        .inner_size(1180.0, 900.0)
        .data_directory(profile.join("webview"))
        .on_navigation(|url| {
            #[cfg(feature = "native-check")]
            eprintln!(
                "Native navigation: scheme={} host={:?} path={}",
                url.scheme(),
                url.host_str(),
                url.path()
            );
            url.scheme() == "tauri"
                && url.host_str() == Some("localhost")
                && matches!(url.path(), "" | "/" | "/index.html")
        })
        .on_new_window(|_, _| tauri::webview::NewWindowResponse::Deny)
        .build()
        .map_err(|_| "window could not open")?;
    Ok(())
}
#[tauri::command]
fn open_window(
    window: WebviewWindow,
    app: tauri::AppHandle,
    state: tauri::State<'_, Desktop>,
) -> Result<(), String> {
    caller(&window)?;
    let label = format!("view-{}", state.windows.fetch_add(1, Ordering::SeqCst));
    open(&app, &label)
}
#[tauri::command]
fn close_window(window: WebviewWindow) -> Result<(), String> {
    caller(&window)?;
    window.close().map_err(|_| "window could not close".into())
}
fn quit(app: tauri::AppHandle) {
    if app.state::<Desktop>().quitting.swap(true, Ordering::SeqCst) {
        return;
    }
    app.state::<Desktop>()
        .generation
        .fetch_add(1, Ordering::SeqCst);
    app.state::<Desktop>().cancel_streams();
    app.state::<Desktop>().retire_credentials();
    app.state::<Desktop>().status.lock().unwrap().phase = "stopping".into();
    std::thread::spawn(move || {
        let result = app
            .state::<Desktop>()
            .runtime
            .lock()
            .unwrap()
            .take()
            .map(|owner| owner.stop());
        let code = if result.is_some_and(|result| result.is_err()) {
            1
        } else {
            0
        };
        app.exit(code);
    });
}
#[tauri::command]
fn quit_factory(window: WebviewWindow, app: tauri::AppHandle) -> Result<(), String> {
    caller(&window)?;
    quit(app);
    Ok(())
}
fn main() {
    if let Err(error) = run() {
        eprintln!("Factory: {error}");
        std::process::exit(1);
    }
}
fn run() -> Result<(), Box<dyn std::error::Error>> {
    let exe = std::env::current_exe()?;
    let parent = exe.parent().ok_or("missing application directory")?;
    let data = std::env::var_os("XDG_DATA_HOME")
        .map(PathBuf::from)
        .filter(|path| path.is_absolute())
        .or_else(|| std::env::var_os("HOME").map(|path| PathBuf::from(path).join(".local/share")))
        .ok_or("missing native data directory")?;
    let mut paths = Paths {
        profile: data.join("soda-factory"),
        payload: parent.join("factory-runtime/postgresql"),
        backend: parent.join("factory-runtime/soda-factory-server"),
    };
    #[cfg(feature = "native-check")]
    let mut checking = false;
    #[cfg(feature = "native-check")]
    let mut startup_error = false;
    #[cfg(feature = "native-check")]
    let mut crash_check = false;
    #[cfg(feature = "native-check")]
    let mut resume_check = false;
    #[cfg(feature = "native-check")]
    let mut credential_check = false;
    #[cfg(feature = "native-check")]
    let mut signin_check = None;
    let mut args = std::env::args_os().skip(1);
    while let Some(arg) = args.next() {
        #[cfg(feature = "native-check")]
        if arg == "--check-signin"
            || arg == "--check-signin-live"
            || arg == "--check-signin-consent"
        {
            checking = true;
            signin_check = Some(if arg == "--check-signin-consent" {
                check::SignInMode::Consent
            } else if arg == "--check-signin-live" {
                check::SignInMode::Live
            } else {
                check::SignInMode::Declared
            });
            continue;
        }
        #[cfg(feature = "native-check")]
        if arg == "--check" {
            checking = true;
            continue;
        }
        #[cfg(feature = "native-check")]
        if arg == "--check-startup-error" {
            checking = true;
            startup_error = true;
            continue;
        }
        #[cfg(feature = "native-check")]
        if arg == "--check-crash" {
            checking = true;
            crash_check = true;
            continue;
        }
        #[cfg(feature = "native-check")]
        if arg == "--check-resume" {
            checking = true;
            resume_check = true;
            continue;
        }
        #[cfg(feature = "native-check")]
        if arg == "--check-credentials" {
            checking = true;
            credential_check = true;
            continue;
        }
        let value = PathBuf::from(args.next().ok_or("native path option requires a value")?);
        if !value.is_absolute() {
            return Err("native paths must be absolute".into());
        }
        match arg.to_str() {
            Some("--profile") => paths.profile = value,
            Some("--postgres") => paths.payload = value,
            Some("--backend") => paths.backend = value,
            _ => return Err("unsupported native application option".into()),
        }
    }
    prepare_profile(&paths.profile)?;
    let bus = format!(
        "dev.soda.Factory.p{}",
        paths
            .profile
            .as_os_str()
            .as_encoded_bytes()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>()
    );
    let state = Desktop {
        paths,
        runtime: Mutex::new(None),
        credentials: Mutex::new(None),
        status: Mutex::new(Status {
            phase: "starting".into(),
            profile: None,
            instance: None,
            generation: 0,
        }),
        starting: AtomicBool::new(false),
        quitting: AtomicBool::new(false),
        generation: AtomicU64::new(0),
        windows: AtomicU64::new(1),
        streams: Mutex::new(HashMap::new()),
        signin: signin::SignIn::new(),
        #[cfg(feature = "native-check")]
        check: check::Check::new(
            checking,
            startup_error,
            crash_check,
            resume_check,
            credential_check,
            signin_check,
        ),
    };
    let builder = tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(
            tauri_plugin_single_instance::Builder::new()
                .dbus_id(bus)
                .callback(|app, _, _| {
                    let _ = open(app, "main");
                    #[cfg(feature = "native-check")]
                    check::activated(app);
                })
                .build(),
        )
        .manage(state);
    #[cfg(feature = "native-check")]
    let builder = builder.invoke_handler(tauri::generate_handler![
        runtime_status,
        runtime_retry,
        credential_status,
        credential_withdraw,
        github_sign_in,
        github_sign_in_status,
        github_sign_in_cancel,
        github_sign_in_open,
        factory_query,
        factory_command,
        factory_stream,
        cancel_stream,
        open_window,
        close_window,
        quit_factory,
        check::report
    ]);
    #[cfg(not(feature = "native-check"))]
    let builder = builder.invoke_handler(tauri::generate_handler![
        runtime_status,
        runtime_retry,
        credential_status,
        credential_withdraw,
        github_sign_in,
        github_sign_in_status,
        github_sign_in_cancel,
        github_sign_in_open,
        factory_query,
        factory_command,
        factory_stream,
        cancel_stream,
        open_window,
        close_window,
        quit_factory
    ]);
    let app = builder
        .setup(|app| {
            open(app.handle(), "main").map_err(std::io::Error::other)?;
            Desktop::start(app.handle().clone());
            #[cfg(feature = "native-check")]
            check::timeout(app.handle());
            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
                let app = window.app_handle().clone();
                let label = window.label().to_string();
                tauri::async_runtime::spawn(async move {
                    app.state::<Desktop>()
                        .signin
                        .cancel(None, Some(&label))
                        .await;
                });
                let state = window.state::<Desktop>();
                let mut streams = state.streams.lock().unwrap();
                streams.retain(|_, (owner, active)| {
                    if owner == window.label() {
                        active.store(false, Ordering::SeqCst);
                        false
                    } else {
                        true
                    }
                });
            }
        })
        .on_page_load(|_window, _payload| {
            #[cfg(feature = "native-check")]
            if _payload.event() == tauri::webview::PageLoadEvent::Finished {
                check::loaded(_window);
            }
        })
        .build(tauri::generate_context!())?;
    app.run(|app, event| {
        if let RunEvent::ExitRequested { code, api, .. } = event {
            if code.is_none() {
                api.prevent_exit();
                quit(app.clone());
            }
        }
    });
    Ok(())
}
