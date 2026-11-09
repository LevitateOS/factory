#[cfg(feature = "native-check")]
mod bridge;
mod product;
use product::{
    factory_connect, factory_disconnect, factory_find_repository, factory_open_link,
    factory_prepare_connection, factory_product_cancel, factory_product_request,
    factory_repositories,
};
mod signin;
use signin::{github_sign_in, github_sign_in_cancel, github_sign_in_open, github_sign_in_status};
mod fountain_signin;
mod muse_signin;
use fountain_signin::{
    fountain_controller, fountain_enrollment, fountain_refresh, fountain_sign_in,
    fountain_sign_in_cancel, fountain_sign_in_open, fountain_sign_in_status,
};
use muse_signin::{
    muse_sign_in, muse_sign_in_cancel, muse_sign_in_open, muse_sign_in_status, muse_tools_refresh,
};
#[cfg(feature = "native-check")]
mod check;
#[cfg(feature = "native-check")]
mod check_credentials;
#[cfg(feature = "native-check")]
use bridge::{cancel_stream, factory_command, factory_query, factory_stream};
use serde::Serialize;
use serde_json::Value;
use soda_desktop_runtime::credentials::{AccountStatus, Credentials};
use soda_desktop_runtime::supervisor::{prepare_profile, NativeSupervisor};
#[cfg(feature = "native-check")]
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
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
    execution_error: Option<String>,
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
    #[cfg(feature = "native-check")]
    streams: Mutex<HashMap<String, bridge::Observation>>,
    signin: signin::SignIn,
    fountain_signin: signin::SignIn,
    muse_signin: muse_signin::MuseSignIn,
    product: product::Connections,
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
        let product_tasks = state.retire_credentials();
        #[cfg(feature = "native-check")]
        state.cancel_streams();
        {
            let mut status = state.status.lock().unwrap();
            status.phase = "starting".into();
            status.execution_error = None;
            status.instance = None;
            status.generation = generation;
        }
        std::thread::spawn(move || {
            tauri::async_runtime::block_on(async move {
                for task in product_tasks {
                    let _ = task.await;
                }
            });
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
                Ok(owner) => match tauri::async_runtime::block_on(async {
                    if let Err(error) = owner.start_execution_coordinator().await {
                        eprintln!("native execution startup: {error}");
                    }
                    owner.health()
                }) {
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
                            execution_error: owner.execution_error(),
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
    fn retire_credentials(&self) -> Vec<tauri::async_runtime::JoinHandle<()>> {
        let mut tasks = self.product.retire(None);
        if let Some(task) = self.muse_signin.retire() {
            tasks.push(task);
        }
        if let Some(store) = self.credentials.lock().unwrap().take() {
            store.retire();
        }
        if let Some(task) = self.signin.retire() {
            tasks.push(task);
        }
        if let Some(task) = self.fountain_signin.retire() {
            tasks.push(task);
        }
        tasks
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
    #[cfg(feature = "native-check")]
    async fn call(&self, method: &str, path: &str, body: Value) -> Result<Reply, String> {
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
        let request = {
            let runtime = self.runtime.lock().unwrap();
            if epoch != self.generation.load(Ordering::SeqCst)
                || self.starting.load(Ordering::SeqCst)
                || self.quitting.load(Ordering::SeqCst)
            {
                return Err("Factory binding retired".into());
            }
            runtime
                .as_ref()
                .ok_or("Factory is unavailable")?
                .product_request(method, path, &bytes)
        };
        let response = request.await?;
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
fn join_streams(tasks: Vec<tauri::async_runtime::JoinHandle<()>>) {
    if !tasks.is_empty() {
        tauri::async_runtime::spawn(async move {
            for task in tasks {
                let _ = task.await;
            }
        });
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
            let failed = {
                let runtime = state.runtime.lock().unwrap();
                runtime
                    .as_ref()
                    .is_some_and(|owner| owner.health().is_err())
            };
            if failed {
                state.generation.fetch_add(1, Ordering::SeqCst);
                #[cfg(feature = "native-check")]
                state.cancel_streams();
                let tasks = state.retire_credentials();
                tauri::async_runtime::block_on(async move {
                    for task in tasks {
                        let _ = task.await;
                    }
                });
                let mut status = state.status.lock().unwrap();
                status.phase = "failed".into();
                status.instance = None;
                status.generation = state.generation.load(Ordering::SeqCst);
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
    {
        let status = state.status.lock().unwrap();
        if status.phase != "failed"
            && !(status.phase == "ready" && status.execution_error.is_some())
        {
            return Err("Factory is not awaiting restart".into());
        }
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
    state.fountain_signin.cancel(None, None).await;
    let signin_task = state.signin.retire();
    let fountain_task = state.fountain_signin.retire();
    let generation = state.generation.fetch_add(1, Ordering::SeqCst) + 1;
    #[cfg(feature = "native-check")]
    state.cancel_streams();
    let mut tasks = state.product.retire(None);
    tasks.extend(signin_task);
    tasks.extend(fountain_task);
    if let Some(task) = state.muse_signin.retire() {
        tasks.push(task);
    }
    state.status.lock().unwrap().generation = generation;
    for task in tasks {
        let _ = task.await;
    }
    store.withdraw(&connection).await.map_err(|e| e.to_string())
}
fn open(app: &tauri::AppHandle, label: &str) -> Result<(), String> {
    if let Some(window) = app.get_webview_window(label) {
        // GTK can leave a mapped iconified window hidden after deiconify.
        // Clear its iconified state before remapping through the normal opener.
        if window.is_minimized().unwrap_or(false) {
            window.hide().map_err(|_| "window unavailable")?;
            window.unminimize().map_err(|_| "window unavailable")?;
        }
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
    #[cfg(feature = "native-check")]
    let streams = app.state::<Desktop>().take_streams(None);
    let product_tasks = app.state::<Desktop>().retire_credentials();
    app.state::<Desktop>().status.lock().unwrap().phase = "stopping".into();
    tauri::async_runtime::spawn(async move {
        #[cfg(feature = "native-check")]
        for stream in streams {
            let _ = stream.await;
        }
        for task in product_tasks {
            let _ = task.await;
        }
        let runtime_app = app.clone();
        let result = tauri::async_runtime::spawn_blocking(move || {
            runtime_app
                .state::<Desktop>()
                .runtime
                .lock()
                .unwrap()
                .take()
                .map(|owner| owner.stop())
        })
        .await;
        let code = if result.map_or(true, |r| r.is_some_and(|r| r.is_err())) {
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
    let mut checks = check::Options::default();
    let mut args = std::env::args_os().skip(1);
    while let Some(arg) = args.next() {
        #[cfg(feature = "native-check")]
        if checks.consume(&arg) {
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
    #[cfg(feature = "native-check")]
    let check = checks.finish()?;
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
            execution_error: None,
        }),
        starting: AtomicBool::new(false),
        quitting: AtomicBool::new(false),
        generation: AtomicU64::new(0),
        windows: AtomicU64::new(1),
        #[cfg(feature = "native-check")]
        streams: Mutex::new(HashMap::new()),
        signin: signin::SignIn::new(),
        fountain_signin: signin::SignIn::new(),
        muse_signin: muse_signin::MuseSignIn::new(),
        product: product::Connections::new(),
        #[cfg(feature = "native-check")]
        check,
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
        fountain_sign_in,
        fountain_sign_in_status,
        fountain_sign_in_cancel,
        fountain_sign_in_open,
        fountain_refresh,
        fountain_enrollment,
        fountain_controller,
        muse_sign_in,
        muse_sign_in_status,
        muse_tools_refresh,
        muse_sign_in_cancel,
        muse_sign_in_open,
        factory_query,
        factory_connect,
        factory_prepare_connection,
        factory_find_repository,
        factory_open_link,
        factory_disconnect,
        factory_repositories,
        factory_product_request,
        factory_product_cancel,
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
        fountain_sign_in,
        fountain_sign_in_status,
        fountain_sign_in_cancel,
        fountain_sign_in_open,
        fountain_refresh,
        fountain_enrollment,
        fountain_controller,
        muse_sign_in,
        muse_sign_in_status,
        muse_tools_refresh,
        muse_sign_in_cancel,
        muse_sign_in_open,
        factory_connect,
        factory_prepare_connection,
        factory_find_repository,
        factory_open_link,
        factory_disconnect,
        factory_repositories,
        factory_product_request,
        factory_product_cancel,
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
                    app.state::<Desktop>()
                        .muse_signin
                        .cancel(None, Some(&label))
                        .await;
                    app.state::<Desktop>()
                        .fountain_signin
                        .cancel(None, Some(&label))
                        .await;
                });
                let state = window.state::<Desktop>();
                join_streams(state.product.retire(Some(window.label())));
                #[cfg(feature = "native-check")]
                join_streams(state.take_streams(Some(window.label())));
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
