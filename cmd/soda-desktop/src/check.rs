//! Opt-in native acceptance hook; absent from the normal application build.
use super::*;
use std::sync::atomic::{AtomicUsize, Ordering};
#[derive(Clone, Copy, PartialEq)]
pub enum SignInMode {
    Declared,
    Live,
    Consent,
}
pub struct Check {
    pub enabled: bool,
    startup_error: bool,
    crash: bool,
    resume: bool,
    credentials: bool,
    signin: Option<SignInMode>,
    stage: AtomicUsize,
    instance: Mutex<Option<String>>,
}
impl Check {
    pub fn new(
        enabled: bool,
        startup_error: bool,
        crash: bool,
        resume: bool,
        credentials: bool,
        signin: Option<SignInMode>,
    ) -> Self {
        Self {
            enabled,
            startup_error,
            crash,
            resume,
            credentials,
            signin,
            stage: AtomicUsize::new(0),
            instance: Mutex::new(None),
        }
    }
}
pub fn timeout(app: &tauri::AppHandle) {
    if !app.state::<Desktop>().check.enabled {
        return;
    }
    let app = app.clone();
    std::thread::spawn(move || {
        std::thread::sleep(Duration::from_secs(
            if app.state::<Desktop>().check.signin == Some(SignInMode::Consent) {
                1000
            } else if app.state::<Desktop>().check.signin.is_some() {
                90
            } else {
                45
            },
        ));
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
    println!("PASS: approved native sign-in, encrypted grant reload and authenticated provider reads; see safe consent-result.json for private/refresh availability");
    quit(app.clone());
}
pub fn loaded(window: &tauri::Webview<tauri::Wry>) {
    if window.label() != "main" || !window.state::<Desktop>().check.enabled {
        return;
    }
    println!("Native desktop page loaded");
    if window.state::<Desktop>().check.startup_error {
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
        if(Object.keys(s).sort().join(',')!=='generation,instance,phase,profile')throw Error('native status leaked extra fields');
        app.scheme='light';await app.updateComplete;const light=getComputedStyle(app.querySelector('.soda-factory-root')).backgroundColor;
        app.scheme='dark';await app.updateComplete;const dark=getComputedStyle(app.querySelector('.soda-factory-root')).backgroundColor;
        if(light!=='rgb(255, 255, 255)'||dark!=='rgb(16, 16, 16)')throw Error('native themes failed');
        if((await invoke('factory_query',{query:{kind:'identity'}})).status!==503)throw Error('unbound account appeared connected');
        if((await invoke('factory_command',{command:{kind:'save_request',revision:{}}})).status!==503)throw Error('unbound mutation succeeded');
        let refused=false;try{await invoke('factory_query',{query:{kind:'url',url:'file:///etc/passwd'}})}catch{refused=true;}if(!refused)throw Error('arbitrary target accepted');
        refused=false;try{await invoke('factory_command',{command:{kind:'save_request',revision:{objective:'x'.repeat(66000)}}})}catch{refused=true;}if(!refused)throw Error('oversized command accepted');
        const channel=new window.__TAURI__.core.Channel();const observed=new Promise(resolve=>channel.onmessage=resolve);
        const id=await invoke('factory_stream',{run:'a'.repeat(32),channel});if((await observed).status!==503)throw Error('unbound observation succeeded');await invoke('cancel_stream',{id});
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
    if !state.check.enabled || window.label() != "main" {
        return Err("native acceptance is disabled".into());
    }
    if let Some(error) = error {
        eprintln!("Native desktop check failed: {error}");
        app.exit(1);
        return Ok(());
    }
    if state.check.signin.is_some() && stage == "signin-done" {
        state.check.stage.store(2, Ordering::SeqCst);
        println!("PASS: actual Linux Tauri/WebKit desktop sign-in, native prompt/metadata custody and cancellation; declared positive authorization is separate from real human consent");
        quit(app);
        return Ok(());
    }
    if state.check.signin == Some(SignInMode::Consent) && stage == "signin-consent-prompt" {
        return crate::check_credentials::publish_consent_prompt(&app);
    }
    if state.check.signin == Some(SignInMode::Consent) && stage == "signin-consent-complete" {
        crate::check_credentials::start_consent_checks(app);
        return Ok(());
    }
    if state.check.signin.is_some() && stage.starts_with("signin-") {
        if state.check.signin != Some(SignInMode::Declared) && stage.starts_with("signin-mode-") {
            return Err("declared modes unavailable in live sign-in check".into());
        }
        return crate::check_credentials::signin_control(&app, &stage);
    }
    if state.check.credentials && stage == "credential-withdrawn" {
        std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(state.paths.profile.join("credential-ui-withdrawn"))
            .map_err(|_| "credential UI acknowledgement failed")?;
        return Ok(());
    }
    if stage == "startup-error" && state.check.startup_error {
        if state.runtime.lock().unwrap().is_some() {
            return Err("failed startup retained a runtime".into());
        }
        state.check.stage.store(2, Ordering::SeqCst);
        println!("PASS: native startup error is visible and product admission is closed");
        quit(app);
        return Ok(());
    }
    if stage == "resumed" && state.check.resume && state.check.stage.load(Ordering::SeqCst) == 1 {
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
    if state.check.credentials {
        crate::check_credentials::start(app);
        return Ok(());
    }
    if let Some(mode) = state.check.signin {
        crate::check_credentials::start_signin(app, mode);
        return Ok(());
    }
    if state.check.resume {
        return observe_resume(app);
    }
    if state.check.crash {
        println!("Native crash ready: {}", std::process::id());
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
    if !state.check.enabled || state.check.stage.load(Ordering::SeqCst) != 1 {
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
