//! Opt-in native acceptance hook; absent from the normal application build.
use super::*;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;
#[derive(Clone, Copy, PartialEq)]
pub enum SignInMode {
    Declared,
    Live,
    Consent,
}
/// Parsed once by the check boundary. Flags remain independent because the
/// existing journeys compose observers (for example writes with consent).
#[derive(Default)]
pub struct Options {
    scenario: Scenario,
}
#[derive(Clone, Copy, Default)]
pub(super) struct Scenario {
    pub enabled: bool,
    pub startup_error: bool,
    pub muse_login: bool,
    pub owner_policy: bool,
    pub crash: bool,
    pub preconsent: bool,
    pub resume: bool,
    pub credentials: bool,
    pub control: bool,
    pub signin: Option<SignInMode>,
    pub github_writes: bool,
    pub issue_read: bool,
    pub retain_account: bool,
    pub revocation: bool,
    pub revocation_cleanup: bool,
    // The existing OAuth observer requires this explicit flag even if another
    // sign-in mode appears later. Mode selection itself retains last-flag wins.
    pub consent_requested: bool,
}
impl Options {
    /// Consume only a scenario flag; main still owns production path options
    /// and takes their values directly from its original argument iterator.
    pub fn consume(&mut self, arg: &std::ffi::OsStr) -> bool {
        let scenario = &mut self.scenario;
        match arg.to_str() {
            Some("--check-github-writes") => scenario.github_writes = true,
            Some("--check-issue-read") => {
                scenario.issue_read = true;
                return true;
            }
            Some("--check-provider-revocation-cleanup") => {
                scenario.revocation_cleanup = true;
                return true;
            }
            Some("--check-provider-revocation") => {
                scenario.revocation = true;
                return true;
            }
            Some("--check-retain-account") => {
                scenario.retain_account = true;
                return true;
            }
            Some("--check-control") => scenario.control = true,
            Some("--check-muse-login") => scenario.muse_login = true,
            Some("--check-owner-policy") => scenario.owner_policy = true,
            Some("--check-signin") => scenario.signin = Some(SignInMode::Declared),
            Some("--check-signin-live") => scenario.signin = Some(SignInMode::Live),
            Some("--check-signin-consent") => {
                scenario.signin = Some(SignInMode::Consent);
                scenario.consent_requested = true;
            }
            Some("--check") => {}
            Some("--check-startup-error") => scenario.startup_error = true,
            Some("--check-crash") => scenario.crash = true,
            Some("--check-preconsent") => scenario.preconsent = true,
            Some("--check-resume") => scenario.resume = true,
            Some("--check-credentials") => scenario.credentials = true,
            _ => return false,
        }
        scenario.enabled = true;
        true
    }
    pub fn finish(self) -> Result<Check, &'static str> {
        let scenario = self.scenario;
        if scenario.owner_policy && (scenario.signin.is_some() || scenario.muse_login || scenario.credentials || scenario.control || scenario.preconsent || scenario.startup_error || scenario.crash || scenario.resume || scenario.github_writes || scenario.issue_read || scenario.retain_account || scenario.revocation || scenario.revocation_cleanup) {
            return Err("owner policy requires its own native-check invocation");
        }
        if scenario.muse_login && (scenario.signin.is_some() || scenario.credentials || scenario.control || scenario.preconsent || scenario.startup_error || scenario.crash || scenario.resume || scenario.github_writes || scenario.issue_read) {
            return Err("Muse login requires its own native-check invocation");
        }
        if scenario.retain_account && scenario.signin != Some(SignInMode::Consent) {
            return Err("retained check account requires the consent check");
        }
        if scenario.issue_read && scenario.signin != Some(SignInMode::Declared) {
            return Err("issue read check requires the declared native sign-in journey");
        }
        if scenario.revocation
            && (scenario.signin != Some(SignInMode::Consent) || scenario.retain_account)
        {
            return Err("provider revocation requires a consent check with local withdrawal");
        }
        if scenario.revocation_cleanup
            && (scenario.signin != Some(SignInMode::Consent)
                || scenario.retain_account
                || scenario.revocation)
        {
            return Err("revocation cleanup requires a separate consent-check invocation");
        }
        Ok(Check {
            scenario,
            stage: AtomicUsize::new(0),
            instance: Mutex::new(None),
        })
    }
}
impl Scenario {
    fn timeout(self) -> Duration {
        Duration::from_secs(if self.github_writes {
            600
        } else if self.signin == Some(SignInMode::Consent) || self.muse_login {
            1000
        } else if self.issue_read || self.owner_policy {
            180
        } else if self.signin.is_some() || self.control {
            90
        } else {
            45
        })
    }
    fn consent_result(self) -> &'static str {
        if self.revocation_cleanup {
            "PASS: owned pending account cleanup; provider refresh outcome remains unverified"
        } else {
            "PASS: approved native sign-in, encrypted grant reload and authenticated provider reads; see safe consent-result.json for private/refresh availability"
        }
    }
}

pub fn owner_policy_done(app: &tauri::AppHandle) {
    app.state::<Desktop>().check.stage.store(2, Ordering::SeqCst);
    println!("PASS: native shared owner policy/bootstrap, explicit role/judgment choices, allowance/CAS/withdrawal and model availability; declared read-only collaboration, no model or installed qualification");
    quit(app.clone());
}
pub struct Check {
    pub(super) scenario: Scenario,
    stage: AtomicUsize,
    instance: Mutex<Option<String>>,
}
pub fn timeout(app: &tauri::AppHandle) {
    if !app.state::<Desktop>().check.scenario.enabled {
        return;
    }
    let app = app.clone();
    std::thread::spawn(move || {
        std::thread::sleep(app.state::<Desktop>().check.scenario.timeout());
        if app.state::<Desktop>().check.stage.load(Ordering::SeqCst) != 2 {
            eprintln!("Native desktop acceptance timed out");
            app.exit(1);
        }
    });
}
pub fn consent_done(app: &tauri::AppHandle) {
    app.state::<Desktop>()
        .check
        .stage
        .store(2, Ordering::SeqCst);
    println!("{}", app.state::<Desktop>().check.scenario.consent_result());
    quit(app.clone());
}
pub fn muse_login_done(app: &tauri::AppHandle) {
    app.state::<Desktop>().check.stage.store(2, Ordering::SeqCst);
    println!("PASS: actual WebKit/native CLI initial Muse login, public browser handoff, owner close/cancellation and stale-attempt refusal; private CLI login retained, no model or installed qualification");
    quit(app.clone());
}
pub fn github_writes_done(app: tauri::AppHandle) {
    app.state::<Desktop>()
        .check
        .stage
        .store(2, Ordering::SeqCst);
    println!("PASS: owned GitHub write journey through actual WebKit and production UI/IPC/Go/PostgreSQL/native custody; see github-ui-write-result.json for exact scope and owned account disposition");
    quit(app);
}
fn control_root(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    app.state::<Desktop>()
        .paths
        .profile
        .parent()
        .map(PathBuf::from)
        .ok_or("control fixture root missing".into())
}
fn control_mode(app: &tauri::AppHandle, mode: &str) -> Result<(), String> {
    if !matches!(mode, "pause" | "release") {
        return Err("invalid control fixture mode".into());
    }
    let io = control_root(app)?.join("io");
    if mode == "pause" {
        let _ = std::fs::remove_file(io.join("cancelled"));
        for entry in std::fs::read_dir(io.join("entered"))
            .map_err(|_| "control entry markers unavailable")?
        {
            let entry = entry.map_err(|_| "control entry marker unavailable")?;
            if !entry
                .file_type()
                .map_err(|_| "control marker type unavailable")?
                .is_file()
                || entry
                    .file_name()
                    .to_str()
                    .is_none_or(|s| !s.bytes().all(|b| b.is_ascii_digit() || b == b'-'))
            {
                return Err("unexpected control fixture entry".into());
            }
            std::fs::remove_file(entry.path()).map_err(|_| "control marker cleanup failed")?;
        }
    }
    std::fs::write(io.join("mode"), mode).map_err(|_| "control fixture mode failed".into())
}
fn control_entered(app: &tauri::AppHandle, count: usize) -> bool {
    std::fs::read_dir(control_root(app).unwrap().join("io/entered"))
        .is_ok_and(|v| v.count() >= count)
}
fn control(app: &tauri::AppHandle, stage: &str) -> Result<(), String> {
    if let Some(mode) = stage.strip_prefix("control-mode-") {
        return control_mode(app, mode);
    }
    match stage {
        "control-entered" if control_entered(app, 1) => Ok(()),
        "control-cancelled" if control_root(app)?.join("io/cancelled").is_file() => Ok(()),
        "control-pool-done" if control_root(app)?.join("control-pool.json").is_file() => Ok(()),
        "control-reopen" => app
            .get_webview_window("main")
            .ok_or("main window missing")?
            .show()
            .map_err(|_| "control window show failed".into()),
        "control-pool-start" => {
            let app = app.clone();
            tauri::async_runtime::spawn(async move {
                if let Err(error) = control_pool(&app).await {
                    eprintln!("Native control check failed: {error}");
                    app.exit(1);
                }
            });
            Ok(())
        }
        "control-done" => {
            app.state::<Desktop>()
                .check
                .stage
                .store(2, Ordering::SeqCst);
            println!("PASS: native product/control independence, bounded requests, cancellation, joined retirement and child crash; Quit with a pending actual shell query");
            quit(app.clone());
            Ok(())
        }
        _ => Err("native control observation pending".into()),
    }
}
async fn control_wait(app: &tauri::AppHandle, count: usize) -> Result<(), String> {
    for _ in 0..150 {
        if control_entered(app, count) {
            return Ok(());
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    Err("native product did not enter fixture".into())
}
async fn control_pool(app: &tauri::AppHandle) -> Result<(), String> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    use std::time::Instant;
    let paths = app.state::<Desktop>().paths.clone();
    let root = control_root(app)?;
    let profile = root.join("aux-profile");
    control_mode(app, "release")?;
    let start_paths = paths.clone();
    let start_profile = profile.clone();
    let owner = tauri::async_runtime::spawn_blocking(move || {
        NativeSupervisor::start(&start_paths.payload, &start_paths.backend, &start_profile)
    })
    .await
    .map_err(|_| "control supervisor start failed")??;
    // Native-only acceptance reads this owned fixture key without exporting it
    // to the webview or evidence. Test all instance-header admission shapes.
    let ready = owner.health()?;
    let mut key =
        std::fs::read(profile.join("backend.key")).map_err(|_| "fixture IPC key unavailable")?;
    for headers in [
        vec![],
        vec![("X-Soda-Native-Instance", "00000000000000000000000000000000")],
        vec![
            ("X-Soda-Native-Instance", ready.instance.as_str()),
            ("X-Soda-Native-Instance", ready.instance.as_str()),
        ],
    ] {
        let reply = soda_unix_http::request_authenticated_async(
            &profile.join("backend.sock"),
            "GET",
            "/api/factory/identity",
            "soda-native",
            &[],
            std::str::from_utf8(&key).map_err(|_| "fixture IPC key invalid")?,
            &headers,
            Duration::from_secs(2),
            soda_unix_http::Limits {
                header_bytes: 8192,
                body_bytes: 4096,
            },
        )
        .await
        .map_err(|_| "native product instance probe failed")?;
        if reply.status != 401 {
            key.fill(0);
            return Err("native product accepted missing/wrong/duplicate instance".into());
        }
    }
    key.fill(0);
    if owner.product("GET", "/api/factory/identity", &[])?.status != 503 {
        return Err("blocking native product compatibility changed authority".into());
    }
    control_mode(app, "pause")?;
    let mut requests = Vec::new();
    for _ in 0..16 {
        requests.push(owner.product_request("GET", "/api/factory/identity", &[]));
    }
    control_wait(app, 16).await?;
    let refused = Instant::now();
    if owner
        .product_request("GET", "/api/factory/identity", &[])
        .await
        .is_ok()
        || refused.elapsed() > Duration::from_secs(1)
    {
        return Err("native product admission was not bounded".into());
    }
    let health = Instant::now();
    owner.health()?;
    let health_ms = health.elapsed().as_millis();
    if health.elapsed() > Duration::from_secs(2) {
        return Err("pending product requests blocked readiness".into());
    }
    drop(requests.pop());
    for _ in 0..150 {
        if root.join("io/cancelled").is_file() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    if !root.join("io/cancelled").is_file() {
        return Err("dropping an unpolled request did not cancel provider I/O".into());
    }
    requests.push(owner.product_request("GET", "/api/factory/identity", &[]));
    control_wait(app, 17).await?;
    let (stopped, stop_ms) = tauri::async_runtime::spawn_blocking(move || {
        let t = Instant::now();
        (owner.stop(), t.elapsed().as_millis())
    })
    .await
    .map_err(|_| "control stop task failed")?;
    stopped?;
    if stop_ms > 3000 {
        return Err("product I/O delayed native stop".into());
    }
    for request in requests {
        if request.await.is_ok() {
            return Err("retired native product reply escaped".into());
        }
    }
    if profile.join("data/postmaster.pid").is_file() {
        return Err("joined stop retained PostgreSQL".into());
    }
    control_mode(app, "release")?;
    let start_profile = profile.clone();
    let owner = tauri::async_runtime::spawn_blocking(move || {
        NativeSupervisor::start(&paths.payload, &paths.backend, &start_profile)
    })
    .await
    .map_err(|_| "control restart failed")??;
    let ready = owner.health()?;
    control_mode(app, "pause")?;
    let pending = owner.product_request("GET", "/api/factory/identity", &[]);
    control_wait(app, 1).await?;
    if !std::process::Command::new("/usr/bin/kill")
        .args(["-KILL", &ready.backend_process.to_string()])
        .status()
        .map_err(|_| "owned control crash failed")?
        .success()
    {
        return Err("owned control crash refused".into());
    }
    let crash = Instant::now();
    let mut failed = false;
    for _ in 0..100 {
        if owner.health().is_err() {
            failed = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    if !failed || pending.await.is_ok() {
        return Err("pending product masked native child failure".into());
    }
    if tauri::async_runtime::spawn_blocking(move || owner.stop())
        .await
        .map_err(|_| "crashed control stop failed")?
        .is_ok()
    {
        return Err("crashed native child reported clean retirement".into());
    }
    if profile.join("data/postmaster.pid").is_file() {
        return Err("child crash retained PostgreSQL".into());
    }
    control_mode(app, "release")?;
    let value = serde_json::json!({"bounded_requests":16,"unpolled_drop_cancelled":true,"slot_reclaimed":true,"health_ms":health_ms,"stop_ms":stop_ms,"retired_replies_refused":true,"child_failure_observed":true,"crash_retirement_ms":crash.elapsed().as_millis(),"owned_pg_stopped":true});
    std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(root.join("control-pool.json"))
        .map_err(|_| "control proof unavailable")?
        .write_all(&serde_json::to_vec(&value).unwrap())
        .map_err(|_| "control proof failed".into())
}
fn start_control(app: tauri::AppHandle) {
    let Some(window) = app.get_webview_window("main") else {
        app.exit(1);
        return;
    };
    let _=window.eval(r#"(async()=>{
      const invoke=window.__TAURI__.core.invoke,report=stage=>invoke('report',{stage,error:null});
      const assert=(ok,message)=>{if(!ok)throw Error(message);};
      const until=async check=>{const deadline=Date.now()+15000;for(;;){if(await check())return;if(Date.now()>deadline)throw Error('native control observation deadline');await new Promise(r=>setTimeout(r,25));}};
      const observe=stage=>until(async()=>{try{await report(stage);return true;}catch{return false;}});
      try{
        await report('control-mode-pause');
        const pending=invoke('factory_query',{query:{kind:'identity'}});await observe('control-entered');
        const t=performance.now(),s=await invoke('runtime_status');assert(s.phase==='ready'&&performance.now()-t<2000,'pending shell query blocked native status');
        await report('control-mode-release');assert((await pending).status===503,'fixture query changed unavailable authority');
        let replies=0;const channel=new window.__TAURI__.core.Channel();channel.onmessage=()=>replies++;
        await report('control-mode-pause');const stream=await invoke('factory_stream',{run:'a'.repeat(32),channel});await observe('control-entered');
        await invoke('cancel_stream',{id:stream});await observe('control-cancelled');assert(replies===0,'cancelled stream published a reply');
        await report('control-mode-pause');await invoke('factory_stream',{run:'b'.repeat(32),channel});await observe('control-entered');
        await invoke('close_window');await observe('control-cancelled');await report('control-reopen');assert(replies===0,'closed window received a stream reply');
        await report('control-pool-start');await observe('control-pool-done');
        await report('control-mode-pause');void invoke('factory_query',{query:{kind:'identity'}}).catch(()=>{});await observe('control-entered');
        await report('control-done');
      }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
    })()"#);
}
pub fn loaded(window: &tauri::Webview<tauri::Wry>) {
    if window.label() != "main" || !window.state::<Desktop>().check.scenario.enabled {
        return;
    }
    println!("Native desktop page loaded");
    if window.state::<Desktop>().check.scenario.startup_error {
        if window
            .state::<Desktop>()
            .paths
            .profile
            .parent()
            .is_some_and(|root| root.join("execution-retry-check").is_file())
        {
            let _ = window.eval(r#"(async()=>{
                  const invoke=window.__TAURI__.core.invoke;
                  const until=async check=>{for(let i=0;i<800;i++){const value=await check();if(value)return value;await new Promise(r=>setTimeout(r,50));}throw Error('execution retry deadline');};
                  try {
                    const first=await until(async()=>{const s=await invoke('runtime_status');return s.phase==='ready'&&s.execution_error&&!document.querySelector('#retry').hidden&&s;});
                    if(!document.querySelector('#runtime-status').textContent.includes('Agent execution is unavailable'))throw Error('execution failure was not visible');
                    if(!document.querySelector('soda-factory-app').textContent.includes('Choose an account'))throw Error('execution failure removed browsing UI');
                    await invoke('report',{stage:'execution-retry-release',error:null});
                    await until(async()=>{try{await invoke('report',{stage:'execution-retry-released',error:null});return true;}catch{return false;}});
                    document.querySelector('#retry').click();
                    const current=await until(async()=>{const s=await invoke('runtime_status');return s.generation>first.generation&&s.phase==='ready'&&s.instance!==first.instance&&s.execution_error!==first.execution_error&&s;});
                    await until(()=>document.querySelector('#retry').hidden===!current.execution_error);
                    if(current.execution_error&&!document.querySelector('#runtime-status').textContent.includes('Agent execution is unavailable'))throw Error('remaining missing adapter was hidden');
                    await invoke('report',{stage:'execution-retry-complete',error:null});
                  }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
                })()"#);
            return;
        }
        let _ = window.eval(r#"(async()=>{
          const invoke=window.__TAURI__.core.invoke;
          try {
            for(let i=0;i<400;i++){
              const s=await invoke('runtime_status');
              if(s.phase==='ready')throw Error('invalid runtime became ready');
              if(s.phase==='failed'&&!document.querySelector('#retry').hidden){
                if(!document.querySelector('#runtime-status').textContent.includes('could not start'))throw Error('startup failure was not visible');
                let refused=false;try{await invoke('factory_query',{query:{kind:'identity'}})}catch{refused=true;}if(!refused)throw Error('failed runtime admitted a query');
                await invoke('report',{stage:'startup-error',error:null});return;
              }
              await new Promise(r=>setTimeout(r,50));
            }
            throw Error('failed startup did not reach the shared application');
          }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
        })()"#);
        return;
    }
    let _=window.eval(r#"(async()=>{
      const invoke=window.__TAURI__.core.invoke;
      const wait=async()=>{for(let i=0;i<400;i++){const s=await invoke('runtime_status');if(s.phase==='ready')return s;if(s.phase==='failed')throw Error('native startup failed');await new Promise(r=>setTimeout(r,50));}throw Error('startup timeout');};
      try {
        const s=await wait();await document.fonts.ready;
        const app=document.querySelector('soda-factory-app');if(!app||!app.innerText.includes('Choose an account'))throw Error('shared disconnected application missing');
        if(Object.keys(s).sort().join(',')!=='execution_error,generation,instance,phase,profile')throw Error('native status leaked extra fields');
        app.scheme='light';await app.updateComplete;const light=getComputedStyle(app.querySelector('.soda-factory-root')).backgroundColor;
        app.scheme='dark';await app.updateComplete;const dark=getComputedStyle(app.querySelector('.soda-factory-root')).backgroundColor;
        if(light!=='rgb(255, 255, 255)'||dark!=='rgb(16, 16, 16)')throw Error('native themes failed');
        let refused=false,completions=0;
        for(const [request,expected] of [
          [{kind:'read',path:'/api/factory/identity'},'Factory connection retired'],
          [{kind:'write',path:'/api/factory/intents',body:{}},'Factory connection retired'],
          [{kind:'subscription',body:{}},'Factory connection retired'],
          [{kind:'read',path:'https://github.com'},'unsupported Factory product operation'],
          [{kind:'read',path:'/api/native/collaboration/observe'},'unsupported Factory product operation'],
          [{kind:'read',path:'/api/factory/../healthz'},'unsupported Factory product operation'],
          [{kind:'write',path:'/api/factory/intents',body:{objective:'x'.repeat(66000)}},'Factory product body exceeds the native limit']
        ]){
          const channel=new window.__TAURI__.core.Channel();channel.onmessage=()=>completions++;
          refused=false;try{await invoke('factory_product_request',{session:'missing',request,channel})}catch(error){refused=String(error).includes(expected);}if(!refused)throw Error('product operation missed its expected refusal: '+expected);
        }
        await new Promise(r=>setTimeout(r,50));if(completions!==0)throw Error('unadmitted product operation delivered a completion');
        refused=false;try{const session=await invoke('factory_prepare_connection',{connection:'0'.repeat(32),repository:null});await invoke('factory_connect',{session})}catch{refused=true;}if(!refused)throw Error('missing credential admitted connection');
        await invoke('factory_disconnect');
        await invoke('open_window');await invoke('report',{stage:'mounted',error:null});
      }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
    })()"#);
}
#[tauri::command]
pub fn report(
    window: WebviewWindow,
    app: tauri::AppHandle,
    stage: String,
    error: Option<String>,
) -> Result<(), String> {
    caller(&window)?;
    let state = app.state::<Desktop>();
    if !state.check.scenario.enabled || window.label() != "main" {
        return Err("native acceptance is disabled".into());
    }
    if let Some(error) = error {
        eprintln!("Native desktop check failed: {error}");
        app.exit(1);
        return Ok(());
    }
    if state.check.scenario.startup_error && stage.starts_with("execution-retry-") {
        let root = state
            .paths
            .profile
            .parent()
            .ok_or("fixture parent missing")?;
        if !root.join("execution-retry-check").is_file() {
            return Err("execution retry driver unavailable".into());
        }
        match stage.as_str() {
            "execution-retry-release" => {
                return std::fs::OpenOptions::new()
                    .write(true)
                    .create_new(true)
                    .open(root.join("execution-retry-release"))
                    .map(|_| ())
                    .map_err(|_| "execution retry release marker failed".into());
            }
            "execution-retry-released" => {
                return if root.join("execution-retry-released").is_file() {
                    Ok(())
                } else {
                    Err("execution lock is still held".into())
                };
            }
            "execution-retry-complete" => {
                {
                    let runtime = state.runtime.lock().unwrap();
                    let owner = runtime.as_ref().ok_or("retried runtime missing")?;
                    tauri::async_runtime::block_on(owner.start_execution_coordinator())?;
                }
                state.check.stage.store(2, Ordering::SeqCst);
                println!("PASS: actual native execution-unavailable UI, production retry generation, successful coordinator start and Quit; missing worker isolation remains unavailable");
                quit(app);
                return Ok(());
            }
            _ => return Err("unknown execution retry stage".into()),
        }
    }
    if state.check.scenario.owner_policy && stage == "owner-policy-complete" {
        crate::check_credentials::finish_owner_policy(app);
        return Ok(());
    }
    if state.check.scenario.github_writes
        && stage == "github-writes-complete"
    {
        crate::check_credentials::finish_github_writes(app);
        return Ok(());
    }
    if state.check.scenario.control && stage.starts_with("control-") {
        return control(&app, &stage);
    }
    if state.check.scenario.muse_login && stage.starts_with("muse-signin-") {
        return crate::check_credentials::muse_signin_control(&app, &stage);
    }
    if state.check.scenario.signin.is_some() && stage == "signin-done" {
        state.check.stage.store(2, Ordering::SeqCst);
        println!("PASS: actual Linux Tauri/WebKit desktop sign-in, native prompt/metadata custody and cancellation; declared positive authorization is separate from real human consent");
        quit(app);
        return Ok(());
    }
    if state.check.scenario.preconsent && stage == "preconsent-stable" {
        if state.check.stage.swap(2, Ordering::SeqCst) != 1
            || app.webview_windows().len() < 2
            || state
                .runtime
                .lock()
                .unwrap()
                .as_ref()
                .and_then(|owner| owner.health().ok())
                .is_none()
        {
            app.exit(1);
            return Ok(());
        }
        println!("PASS: current Tauri/WebKit shared UI and native runtime remained available before consent");
        quit(app);
        return Ok(());
    }
    if state.check.scenario.signin == Some(SignInMode::Consent) && stage == "signin-consent-prompt" {
        return crate::check_credentials::publish_consent_prompt(&app);
    }
    if state.check.scenario.signin == Some(SignInMode::Consent) && stage == "signin-consent-complete" {
        if state.check.scenario.github_writes {
            crate::check_credentials::start_github_writes(app);
            return Ok(());
        }
        crate::check_credentials::start_consent_checks(app);
        return Ok(());
    }
    if state.check.scenario.signin == Some(SignInMode::Consent) && stage == "signin-product-complete" {
        std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(state.paths.profile.join("product-bridge-complete"))
            .map_err(|_| "product IPC acknowledgement failed")?;
        return Ok(());
    }
    if state.check.scenario.signin.is_some() && stage.starts_with("signin-") {
        if state.check.scenario.signin != Some(SignInMode::Declared) && stage.starts_with("signin-mode-") {
            return Err("declared modes unavailable in live sign-in check".into());
        }
        return crate::check_credentials::signin_control(&app, &stage);
    }
    if state.check.scenario.credentials && stage == "credential-withdrawn" {
        std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(state.paths.profile.join("credential-ui-withdrawn"))
            .map_err(|_| "credential UI acknowledgement failed")?;
        return Ok(());
    }
    if stage == "startup-error" && state.check.scenario.startup_error {
        if state.runtime.lock().unwrap().is_some() {
            return Err("failed startup retained a runtime".into());
        }
        state.check.stage.store(2, Ordering::SeqCst);
        println!("PASS: native startup error is visible and product admission is closed");
        quit(app);
        return Ok(());
    }
    if stage == "resumed" && state.check.scenario.resume && state.check.stage.load(Ordering::SeqCst) == 1 {
        state.check.stage.store(2, Ordering::SeqCst);
        println!(
            "PASS: native shared UI, private bridge and original PG/Go instance after OS wake"
        );
        quit(app);
        return Ok(());
    }
    if stage != "mounted" || state.check.stage.swap(1, Ordering::SeqCst) != 0 {
        return Err("unexpected native acceptance stage".into());
    }
    let status = state.status.lock().unwrap().clone();
    *state.check.instance.lock().unwrap() = status.instance;
    if app.webview_windows().len() < 2 {
        app.exit(1);
        return Ok(());
    }
    println!(
        "Native desktop mounted: shared UI, private bridge refusals, stream and multiple windows"
    );
    if state.check.scenario.github_writes {
        crate::check_credentials::start_github_writes(app);
        return Ok(());
    }
    if state.check.scenario.owner_policy {
        crate::check_credentials::start_owner_policy(app);
        return Ok(());
    }
    if state.check.scenario.muse_login {
        crate::check_credentials::start_muse_signin(app);
        return Ok(());
    }
    if state.check.scenario.credentials {
        crate::check_credentials::start(app);
        return Ok(());
    }
    if state.check.scenario.control {
        start_control(app);
        return Ok(());
    }
    if let Some(mode) = state.check.scenario.signin {
        crate::check_credentials::start_signin(app, mode);
        return Ok(());
    }
    if state.check.scenario.resume {
        return observe_resume(app);
    }
    if state.check.scenario.crash {
        println!("Native crash ready: {}", std::process::id());
        return Ok(());
    }
    if state.check.scenario.preconsent {
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_secs(20));
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.eval(r#"window.__TAURI__.core.invoke('report',{stage:'preconsent-stable',error:null})"#);
            }
        });
        return Ok(());
    }
    for window in app.webview_windows().values() {
        let _ = window.close();
    }
    std::thread::spawn(move || {
        std::thread::sleep(Duration::from_millis(300));
        if app
            .webview_windows()
            .values()
            .any(|window| window.is_visible().unwrap_or(true))
        {
            eprintln!("Native window closure did not hide all views");
            app.exit(1);
            return;
        }
        let state = app.state::<Desktop>();
        if state
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .and_then(|owner| owner.health().ok())
            .is_none()
        {
            eprintln!("Window close stopped native work");
            app.exit(1);
            return;
        }
        let child = std::process::Command::new(std::env::current_exe().unwrap())
            .arg("--profile")
            .arg(&state.paths.profile)
            .spawn();
        match child {
            Ok(mut child) => {
                let until = std::time::Instant::now() + Duration::from_secs(8);
                loop {
                    if let Ok(Some(status)) = child.try_wait() {
                        if !status.success() {
                            app.exit(1);
                        }
                        break;
                    }
                    if std::time::Instant::now() > until {
                        let _ = child.kill();
                        let _ = child.wait();
                        app.exit(1);
                        break;
                    }
                    std::thread::sleep(Duration::from_millis(25));
                }
            }
            Err(_) => app.exit(1),
        }
    });
    Ok(())
}
pub fn credentials_done(app: &tauri::AppHandle) {
    app.state::<Desktop>()
        .check
        .stage
        .store(2, Ordering::SeqCst);
    println!("PASS: real OS credentials, native-only token capability, UI withdrawal, locked/unavailable errors and retained keyring restart");
    quit(app.clone());
}
fn observe_resume(app: tauri::AppHandle) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let before = state
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .ok_or("runtime missing before suspend")?
        .health()?;
    let status = serde_json::to_string(&*state.status.lock().unwrap())
        .map_err(|_| "pre-suspend status unavailable")?;
    let marker = state.paths.profile.join("wake-complete");
    std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(state.paths.profile.join("wake-ready"))
        .map_err(|_| "resume check requires a fresh profile")?;
    println!("Native desktop ready for guest OS suspend");
    std::thread::spawn(move || {
        let until = std::time::Instant::now() + Duration::from_secs(20);
        while !marker.is_file() {
            if std::time::Instant::now() >= until {
                eprintln!("OS wake observation timed out");
                app.exit(1);
                return;
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        let after = app
            .state::<Desktop>()
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .and_then(|owner| owner.health().ok());
        if after != Some(before) {
            eprintln!("OS wake replaced or lost the native runtime");
            app.exit(1);
            return;
        }
        let script = format!(
            r#"(async()=>{{
            const invoke=window.__TAURI__.core.invoke;
            try{{
              const before={status},after=await invoke('runtime_status');
              if(['phase','profile','instance','generation'].some(k=>before[k]!==after[k]))throw Error('wake retired the original binding');
              const app=document.querySelector('soda-factory-app');
              if(!app||!app.innerText.includes('Choose an account')||getComputedStyle(app.querySelector('.soda-factory-root')).backgroundColor!=='rgb(16, 16, 16)')throw Error('wake lost shared UI state');
              if((await invoke('factory_query',{{query:{{kind:'identity'}}}})).status!==503)throw Error('wake changed account admission');
              await invoke('report',{{stage:'resumed',error:null}});
            }}catch(error){{await invoke('report',{{stage:'failed',error:String(error)}});}}
          }})()"#
        );
        if let Some(window) = app.get_webview_window("main") {
            if window.eval(&script).is_ok() {
                return;
            }
        }
        app.exit(1);
    });
    Ok(())
}
pub fn activated(app: &tauri::AppHandle) {
    let state = app.state::<Desktop>();
    if !state.check.scenario.enabled || state.check.stage.load(Ordering::SeqCst) != 1 {
        return;
    }
    let same = state
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .and_then(|owner| owner.health().ok())
        .is_some_and(|ready| Some(ready.instance) == *state.check.instance.lock().unwrap());
    if !same
        || !app
            .get_webview_window("main")
            .is_some_and(|window| window.is_visible().unwrap_or(false))
    {
        eprintln!("Activation changed runtime or failed to reopen window");
        app.exit(1);
        return;
    }
    state.check.stage.store(2, Ordering::SeqCst);
    println!("PASS: native production desktop readiness, offline shared UI/themes, bounded query/command/stream bridge, close/reopen, second-launch activation and Quit");
    quit(app.clone());
}
