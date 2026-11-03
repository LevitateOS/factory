//! Real isolated Secret Service + native webview acceptance, opt-in only.
use super::*;
use soda_desktop_runtime::credentials::{Account, CredentialState, Error, Provider, Token};
use std::io::Read;

/// Publish only the same public prompt already rendered by the production UI.
/// The device code and grants never enter the observer's files or JS arguments.
pub fn publish_consent_prompt(app: &tauri::AppHandle) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let view = serde_json::to_value(state.signin.view()).map_err(|_| "prompt unavailable")?;
    ensure(view["phase"] == "waiting", "sign-in is not waiting")?;
    write_consent_evidence(app, "user-code.json", &view["prompt"])
}
fn write_consent_evidence(
    app: &tauri::AppHandle,
    name: &str,
    value: &serde_json::Value,
) -> Result<(), String> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let mut file = std::fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(root.join(name))
        .map_err(|_| "owned consent evidence unavailable")?;
    file.write_all(&serde_json::to_vec(value).map_err(|_| "evidence encoding failed")?)
        .map_err(|_| "evidence write failed".into())
}
pub fn start_consent_checks(app: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        match consent_checks(&app).await {
            Ok(()) => check::consent_done(&app),
            Err(error) => {
                eprintln!("Native approved sign-in check failed: {error}");
                app.exit(1);
            }
        }
    });
}
async fn consent_read(
    app: &tauri::AppHandle,
    connection: &str,
    query: soda_desktop_runtime::github::Read,
) -> Result<serde_json::Value, String> {
    let reply = native_github_read(app, connection, query).await?;
    ensure(reply.status == 200, "authenticated native read refused")?;
    Ok(reply.body)
}
async fn native_github_read(
    app: &tauri::AppHandle,
    connection: &str,
    query: soda_desktop_runtime::github::Read,
) -> Result<soda_desktop_runtime::github::Reply, String> {
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    let cap = store
        .capability(connection)
        .await
        .map_err(|e| e.to_string())?;
    let read = state
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .ok_or("runtime missing")?
        .github_read(&store, &cap, query);
    read.await.map_err(|e| e.to_string())
}

fn start_binding_checks(app: tauri::AppHandle, retained: bool) {
    tauri::async_runtime::spawn(async move {
        if let Err(error) = binding_checks(&app, retained).await {
            eprintln!("Native canonical binding check failed: {error}");
            app.exit(1);
        }
    });
}
async fn binding_checks(app: &tauri::AppHandle, retained: bool) -> Result<(), String> {
    use soda_desktop_runtime::github::Read as GitHubRead;
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let io = root.join("io");
    let accounts = state
        .credentials()?
        .status()
        .await
        .map_err(|e| e.to_string())?;
    ensure(accounts.len() == 1, "canonical check account missing")?;
    let connection = &accounts[0].connection;
    let profile = state
        .status
        .lock()
        .unwrap()
        .profile
        .clone()
        .ok_or("profile missing")?;
    let binding = consent_read(app, connection, GitHubRead::Binding).await?["binding"].clone();
    ensure(
        binding["profile"]["id"] == profile
            && binding["actor"]["external_id"] == "123"
            && binding["connection"]["id"] == *connection
            && binding["actor"]["id"] != "123",
        "native canonical binding scope missing",
    )?;
    if !retained {
        // Use an actually verified declared token in a second owned credential
        // profile. That valid credential must still not bind this runtime's DB.
        let store = state.credentials()?;
        let cap = store
            .capability(connection)
            .await
            .map_err(|e| e.to_string())?;
        let token = store
            .with_token(&cap, |bytes| Token::new(bytes.to_vec()))
            .await
            .map_err(|e| e.to_string())?
            .map_err(|e| e.to_string())?;
        let verify = state
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .ok_or("runtime missing")?
            .github_verify(token);
        let (remote, token) = verify.await.map_err(|e| e.to_string())?;
        let other_dir = state.paths.profile.join("other-canonical-profile");
        prepare_profile(&other_dir)?;
        let other_profile = String::from_utf8(fixture()).map_err(|_| "fixture profile invalid")?;
        let other =
            Credentials::open(&other_dir, &other_profile[..32]).map_err(|e| e.to_string())?;
        let saved = other
            .store(remote, token)
            .await
            .map_err(|e| e.to_string())?;
        let other_cap = other
            .capability(&saved.connection)
            .await
            .map_err(|e| e.to_string())?;
        let read = state
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .ok_or("runtime missing")?
            .github_read(&other, &other_cap, GitHubRead::Binding);
        ensure(
            matches!(
                read.await,
                Err(soda_desktop_runtime::github::Error::Invalid)
            ),
            "foreign credential profile bound runtime ledger",
        )?;
        other
            .withdraw(&saved.connection)
            .await
            .map_err(|e| e.to_string())?;
    }
    let first = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    let page = &first["repositories"];
    ensure(
        page["items"].as_array().is_some_and(|v| v.len() == 1)
            && page["items"][0]["external_id"] == "9007199254740993"
            && page["items"][0]["id"] != page["items"][0]["external_id"]
            && page["next"] == "2"
            && page["complete"] == false,
        "canonical first repository page invalid",
    )?;
    if retained {
        let proof: serde_json::Value = serde_json::from_slice(
            &std::fs::read(root.join("canonical-binding.json"))
                .map_err(|_| "binding proof missing")?,
        )
        .map_err(|_| "binding proof invalid")?;
        ensure(
            binding == proof["binding"] && page["items"][0] == proof["repository"],
            "runtime restart changed canonical account/repository",
        )?;
        return write_consent_evidence(
            app,
            "canonical-binding-retained.json",
            &serde_json::json!({"retained_mapping":true}),
        );
    }
    let second = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 2, limit: 1 },
    )
    .await?;
    ensure(
        second["repositories"]["next"] == ""
            && second["repositories"]["complete"] == true
            && second["repositories"]["items"][0]["external_id"] == "9007199254740995",
        "canonical next page invalid",
    )?;
    let repeat = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    ensure(
        repeat["binding"] == binding
            && repeat["repositories"]["observation"]["revision"] == page["observation"]["revision"],
        "reconnect forked canonical account/catalogue",
    )?;
    std::fs::write(io.join("mode"), "binding-rename").map_err(|_| "fixture mode failed")?;
    let renamed = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    ensure(
        renamed["binding"]["actor"]["id"] == binding["actor"]["id"]
            && renamed["binding"]["actor"]["login"] == "renamed-native"
            && renamed["repositories"]["items"][0]["id"] == page["items"][0]["id"]
            && renamed["repositories"]["items"][0]["name"] == "owner/renamed"
            && renamed["repositories"]["observation"]["revision"]
                != page["observation"]["revision"],
        "rename changed identity or retained stale page revision",
    )?;
    for (mode, expected) in [
        ("identity-denied", 401),
        ("binding-wrong-account", 409),
        ("binding-catalog-denied", 403),
        ("binding-bad-page", 503),
        ("binding-live-refusal", 401),
    ] {
        std::fs::write(io.join("mode"), mode).map_err(|_| "fixture mode failed")?;
        let reply = native_github_read(
            app,
            connection,
            GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
        )
        .await?;
        ensure(
            reply.status == expected
                && reply.body.get("binding").is_none()
                && reply.body.get("repositories").is_none(),
            "failed canonical read became cached inventory",
        )?;
        if mode == "binding-live-refusal" {
            let reply = native_github_read(app, connection, GitHubRead::Binding).await?;
            ensure(
                reply.status == 401 && reply.body.get("binding").is_none(),
                "real GitHub refusal admitted canonical account",
            )?;
        }
    }
    for name in ["entered", "cancelled"] {
        let _ = std::fs::remove_file(io.join(name));
    }
    std::fs::write(io.join("mode"), "binding-pause").map_err(|_| "fixture mode failed")?;
    let task_app = app.clone();
    let task_connection = connection.clone();
    let task = tauri::async_runtime::spawn(async move {
        native_github_read(
            &task_app,
            &task_connection,
            GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
        )
        .await
    });
    for _ in 0..100 {
        if io.join("entered").is_file() {
            break;
        }
        tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(20)))
            .await
            .map_err(|_| "pause observer stopped")?;
    }
    ensure(
        io.join("entered").is_file(),
        "canonical native read did not enter provider",
    )?;
    task.abort();
    let _ = task.await;
    for _ in 0..100 {
        if io.join("cancelled").is_file() {
            break;
        }
        tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(20)))
            .await
            .map_err(|_| "cancel observer stopped")?;
    }
    ensure(
        io.join("cancelled").is_file(),
        "native canonical cancellation did not reach provider",
    )?;
    std::fs::write(io.join("mode"), "grant").map_err(|_| "fixture mode failed")?;
    let restored = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    ensure(
        restored["binding"] == binding && restored["repositories"]["items"][0] == page["items"][0],
        "refusal/cancellation rebound native mappings",
    )?;
    write_consent_evidence(
        app,
        "canonical-binding.json",
        &serde_json::json!({
            "binding":binding,"repository":page["items"][0],"declared_positive_provider":true,
            "actual_github_refusals":2,"paging":true,"rename_reconnect":true,"cancellation":true
        }),
    )
}
async fn consent_checks(app: &tauri::AppHandle) -> Result<(), String> {
    use soda_desktop_runtime::github::Read as GitHubRead;
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    let accounts = store.status().await.map_err(|e| e.to_string())?;
    ensure(accounts.len() == 1, "approved account not uniquely stored")?;
    let account = &accounts[0];
    ensure(
        account.provider == Provider::Github
            && account.origin == "https://github.com"
            && matches!(account.state, CredentialState::Stored)
            && account.label.is_some(),
        "approved account metadata incomplete",
    )?;
    let grant = account
        .grant
        .as_ref()
        .ok_or("complete OAuth grant not stored")?;
    let namespace = state
        .status
        .lock()
        .unwrap()
        .profile
        .clone()
        .ok_or("profile missing")?;
    let reopened =
        Credentials::open(&state.paths.profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        reopened
            .status()
            .await
            .map_err(|e| e.to_string())?
            .iter()
            .any(|a| {
                a.connection == account.connection
                    && a.account == account.account
                    && a.grant.is_some()
            }),
        "encrypted account reload failed",
    )?;
    let identity = consent_read(app, &account.connection, GitHubRead::Identity).await?;
    ensure(
        identity["account"]["external_id"] == account.account,
        "remote identity mismatch",
    )?;
    let mut page = 1;
    let mut pages = 0;
    let mut private = None;
    loop {
        let response = consent_read(
            app,
            &account.connection,
            GitHubRead::Repositories { page, limit: 10 },
        )
        .await?;
        let items = response["items"]
            .as_array()
            .ok_or("repository page missing")?;
        ensure(items.len() <= 10, "repository page unbounded")?;
        pages += 1;
        for repo in items {
            if repo["private"] == true {
                private = repo["external_id"].as_str().map(str::to_owned);
            }
        }
        let next = response["next_page"].as_u64().ok_or("pagination missing")?;
        if next == 0 || pages >= 10 || private.is_some() && pages >= 2 {
            break;
        }
        ensure(
            next > u64::from(page) && next <= u64::from(u32::MAX),
            "invalid next page",
        )?;
        page = next as u32;
    }
    let mut private_read = false;
    if let Some(repository) = private {
        let result = consent_read(
            app,
            &account.connection,
            GitHubRead::Repository { repository },
        )
        .await?;
        ensure(
            result["repository"]["private"] == true,
            "private repository detail mismatch",
        )?;
        private_read = true;
    }
    let refresh_available = grant.refresh_available;
    if refresh_available {
        let cap = store
            .capability(&account.connection)
            .await
            .map_err(|e| e.to_string())?;
        let refresh = state
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .ok_or("runtime missing")?
            .github_refresh(&store, &cap);
        let rotated = refresh.await.map_err(|e| e.to_string())?;
        ensure(
            rotated.connection == account.connection && rotated.account == account.account,
            "provider refresh changed identity",
        )?;
        ensure(
            store.with_token(&cap, |_| ()).await.is_err(),
            "old local revision survived refresh",
        )?;
        let identity = consent_read(app, &account.connection, GitHubRead::Identity).await?;
        ensure(
            identity["account"]["external_id"] == account.account,
            "post-refresh identity mismatch",
        )?;
    }
    let cap = store
        .capability(&account.connection)
        .await
        .map_err(|e| e.to_string())?;
    store
        .withdraw(&account.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store.status().await.map_err(|e| e.to_string())?.is_empty(),
        "owned test account not removed",
    )?;
    ensure(
        store.with_token(&cap, |_| ()).await.is_err(),
        "withdrawn local capability survived",
    )?;
    write_consent_evidence(
        app,
        "consent-result.json",
        &serde_json::json!({
            "approved_native_signin":true, "verified_identity":true, "encrypted_grant_reload":true,
            "repository_pages":pages, "private_repository_read":private_read,
            "refresh_issued":refresh_available, "real_refresh_exercised":refresh_available,
            "owned_local_account_removed":true, "remote_revocation_exercised":false,
            "canonical_factory_binding":false
        }),
    )
}

pub fn signin_control(app: &tauri::AppHandle, stage: &str) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let io = root.join("io");
    if let Some(mode) = stage.strip_prefix("signin-mode-") {
        if !matches!(
            mode,
            "grant" | "pause-wait" | "denied" | "expired" | "identity-denied"
        ) {
            return Err("unknown declared sign-in mode".into());
        }
        for name in ["entered", "cancelled", "release"] {
            match std::fs::remove_file(io.join(name)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(_) => return Err("fixture marker cleanup failed".into()),
            }
        }
        return std::fs::write(io.join("mode"), mode).map_err(|_| "fixture mode failed".into());
    }
    match stage {
        "signin-binding-start" | "signin-binding-retained-start" => {
            start_binding_checks(app.clone(), stage == "signin-binding-retained-start");
            Ok(())
        }
        "signin-binding-done" => ensure(
            root.join("canonical-binding.json").is_file(),
            "canonical binding still pending",
        ),
        "signin-binding-retained-done" => ensure(
            root.join("canonical-binding-retained.json").is_file(),
            "retained canonical binding still pending",
        ),
        "signin-entered" => ensure(
            io.join("entered").is_file(),
            "declared transport not entered",
        ),
        "signin-upstream-cancelled" => ensure(
            io.join("cancelled").is_file(),
            "native cancellation not observed upstream",
        ),
        "signin-browser-opened" => ensure(
            std::fs::read_to_string(root.join("browser-url"))
                .ok()
                .as_deref()
                == Some("https://github.com/login/device\n"),
            "fixed browser handoff not observed",
        ),
        "signin-close-secondary" => app
            .webview_windows()
            .into_values()
            .find(|window| window.label() != "main")
            .ok_or_else(|| {
                format!(
                    "secondary window missing; labels={:?}",
                    app.webview_windows().keys().collect::<Vec<_>>()
                )
            })?
            .close()
            .map_err(|_| "secondary closure failed".into()),
        "signin-reopen" => {
            let window = app
                .get_webview_window("main")
                .ok_or("main window missing")?;
            window.show().map_err(|_| "main window show failed")?;
            window.set_focus().map_err(|_| "main focus failed".into())
        }
        "signin-crash" => {
            let pid = state
                .runtime
                .lock()
                .unwrap()
                .as_ref()
                .ok_or("fixture runtime missing")?
                .health()?
                .backend_process;
            let status = std::process::Command::new("/usr/bin/kill")
                .args(["-KILL", &pid.to_string()])
                .status()
                .map_err(|_| "owned backend crash failed")?;
            ensure(status.success(), "owned backend crash refused")
        }
        _ => Err("unknown native sign-in control".into()),
    }
}
pub fn start_signin(app: tauri::AppHandle, mode: check::SignInMode) {
    let Some(window) = app.get_webview_window("main") else {
        app.exit(1);
        return;
    };
    let _ = window.show();
    let _ = window.set_focus();
    let script=r#"(async()=>{
      const live=__LIVE__, consent=__CONSENT__, invoke=window.__TAURI__.core.invoke;
      const assert=(ok,message)=>{if(!ok)throw Error(message);};
      const report=stage=>invoke('report',{stage,error:null});
      const view=()=>invoke('github_sign_in_status');
      const until=async check=>{for(let i=0;i<300;i++){document.querySelector('#refresh').click();if(await check())return;await new Promise(r=>setTimeout(r,50));}throw Error('sign-in observation deadline');};
      const control=async stage=>{await until(async()=>{try{await report(stage);return true;}catch{return false;}});};
      const mode=value=>report('signin-mode-'+value);
      const begin=async()=>{await until(()=>!document.querySelector('#github-sign-in').disabled);document.querySelector('#github-sign-in').click();await until(async()=>{const s=await view();return s.phase==='waiting'&&!document.querySelector('#github-prompt').hidden;});return view();};
      const cancel=async()=>{document.querySelector('#github-cancel').click();await until(async()=>(await view()).phase==='cancelled');};
      try{
        if(!live)await mode('pause-wait');
        const first=await begin();
        assert(Object.keys(first).sort().join(',')==='attempt,error,phase,prompt','sign-in state exposes extra data');
        assert(Object.keys(first.prompt).sort().join(',')==='expires_at,user_code,verification_uri','prompt exposes native authority');
        assert(first.prompt.verification_uri==='https://github.com/login/device'&&document.querySelector('#github-user-code').textContent===first.prompt.user_code,'real native prompt not rendered');
        assert(!/device_code|access_token|refresh_token/.test(JSON.stringify(first)),'prompt leaked a secret field');
        let refused=false;try{await invoke('github_sign_in');}catch{refused=true;}assert(refused,'duplicate native sign-in replaced the active attempt');
        refused=false;try{await invoke('plugin:opener|open_url',{url:'file:///etc/passwd'});}catch{refused=true;}assert(refused,'generic browser opener authority admitted');
        document.querySelector('#github-open').click();await control('signin-browser-opened');
        await report('signin-close-secondary');assert((await view()).phase==='waiting','closing another window cancelled owner sign-in');
        if(!live)await control('signin-entered');
        if(consent){
          await report('signin-consent-prompt');
          const deadline=Date.now()+950000;
          for(;;){
            document.querySelector('#refresh').click();const s=await view();
            if(s.phase==='complete')break;
            assert(s.phase!=='failed'&&s.phase!=='cancelled'&&s.phase!=='idle','approved native sign-in failed');
            assert(Date.now()<deadline,'browser consent deadline');
            await new Promise(r=>setTimeout(r,250));
          }
          await until(()=>document.querySelector('#github-accounts').children.length===1);
          assert((await invoke('factory_query',{query:{kind:'identity'}})).status===503,'OAuth login silently admitted Factory work');
          await report('signin-consent-complete');return;
        }
        await cancel();if(!live)await control('signin-upstream-cancelled');
        assert((await invoke('credential_status')).length===0,'unapproved/cancelled sign-in saved credentials');
        if(live){await report('signin-done');return;}

        await mode('pause-wait');const second=await begin();await control('signin-entered');
        const stale=await invoke('github_sign_in_cancel',{attempt:first.attempt});assert(stale.attempt===second.attempt&&stale.phase==='waiting','stale cancellation retired newer sign-in');
        await invoke('close_window');await until(async()=>(await view()).phase==='cancelled');await control('signin-upstream-cancelled');await report('signin-reopen');
        for(const value of ['denied','expired','identity-denied']){
          await mode(value);await until(()=>!document.querySelector('#github-sign-in').disabled);document.querySelector('#github-sign-in').click();
          await until(async()=>(await view()).phase==='failed');
          assert((await invoke('credential_status')).length===0,'failed/unverified login saved credentials');
          await until(()=>document.querySelector('#github-status').textContent.includes((value==='denied'?'declined':value==='expired'?'expired':'unavailable')));
        }
        await mode('grant');await until(()=>!document.querySelector('#github-sign-in').disabled);document.querySelector('#github-sign-in').click();
        await until(async()=>(await view()).phase==='complete');
        const saved=await invoke('credential_status');assert(saved.length===1&&saved[0].account==='123'&&saved[0].label==='declared-native'&&saved[0].grant.refresh_available,'verified complete account was not stored');
        assert(!/factory-declared-(access|refresh)/.test(JSON.stringify(saved)),'saved account metadata leaked a token');
        await until(()=>document.querySelector('#github-accounts').textContent.includes('declared-native'));
        assert((await invoke('factory_query',{query:{kind:'identity'}})).status===503,'OAuth login silently admitted Factory work');
        await report('signin-binding-start');await control('signin-binding-done');
        await report('signin-crash');await until(async()=>(await invoke('runtime_status')).phase==='failed');
        await invoke('runtime_retry');await until(async()=>(await invoke('runtime_status')).phase==='ready');
        const retained=await invoke('credential_status');assert(retained.length===1&&retained[0].connection===saved[0].connection&&retained[0].label==='declared-native','runtime restart lost saved account/label');
        await report('signin-binding-retained-start');await control('signin-binding-retained-done');
        await until(()=>document.querySelector('#github-accounts').textContent.includes('declared-native'));
        document.querySelector('#github-accounts button').click();await until(async()=>(await invoke('credential_status')).length===0);
        await until(()=>document.querySelector('#github-accounts').children.length===0);

        await mode('pause-wait');await begin();await control('signin-entered');await report('signin-crash');
        await until(async()=>(await invoke('runtime_status')).phase==='failed');
        assert((await view()).phase==='idle','failed runtime retained sign-in authority');
        await invoke('runtime_retry');await until(async()=>(await invoke('runtime_status')).phase==='ready');
        assert((await invoke('credential_status')).length===0,'retired sign-in saved credentials after restart');
        await report('signin-done');
      }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
    })()"#.replace("__LIVE__",if mode != check::SignInMode::Declared {"true"} else {"false"})
    .replace("__CONSENT__",if mode == check::SignInMode::Consent {"true"} else {"false"});
    let _ = window.eval(&script);
}

pub fn start(app: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        if let Err(error) = run(&app).await {
            eprintln!("Native credential check failed: {error}");
            app.exit(1);
        } else {
            check::credentials_done(&app);
        }
    });
}
fn fixture() -> Vec<u8> {
    let mut random = [0u8; 32];
    std::fs::File::open("/dev/urandom")
        .unwrap()
        .read_exact(&mut random)
        .unwrap();
    random
        .iter()
        .flat_map(|b| format!("{b:02x}").into_bytes())
        .collect()
}
fn ensure(value: bool, label: &'static str) -> Result<(), String> {
    if value {
        Ok(())
    } else {
        Err(label.into())
    }
}
async fn barrier(app: &tauri::AppHandle, ready: &str, done: &str) -> Result<(), String> {
    let profile = app.state::<Desktop>().paths.profile.clone();
    std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(profile.join(ready))
        .map_err(|_| "credential check marker failed")?;
    println!("Credential check barrier: {ready}");
    let done = profile.join(done);
    tauri::async_runtime::spawn_blocking(move || {
        let until = std::time::Instant::now() + Duration::from_secs(15);
        while !done.is_file() {
            if std::time::Instant::now() >= until {
                return Err("credential check driver timed out".into());
            }
            std::thread::sleep(Duration::from_millis(25));
        }
        Ok(())
    })
    .await
    .map_err(|_| "credential driver stopped")?
}
async fn run(app: &tauri::AppHandle) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    let profile = state.paths.profile.clone();
    let namespace = state
        .status
        .lock()
        .unwrap()
        .profile
        .clone()
        .ok_or("profile missing")?;
    let account = || {
        Account::new(
            Provider::Github,
            "https://github.com",
            "native-fixture:984321234",
        )
        .unwrap()
    };
    let first = fixture();
    let second = fixture();
    let retained = fixture();
    let saved = store
        .store(account(), Token::new(first.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    let cap = store
        .capability(&saved.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store
            .with_token(&cap, |bytes| bytes == first)
            .await
            .map_err(|e| e.to_string())?,
        "native token round trip failed",
    )?;
    let reopened = Credentials::open(&profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        reopened.with_token(&cap, |_| ()).await == Err(Error::Retired),
        "capability escaped owner",
    )?;
    let other_dir = profile.join("other-credential-profile");
    prepare_profile(&other_dir)?;
    let other = Credentials::open(&other_dir, "0123456789abcdef0123456789abcdef")
        .map_err(|e| e.to_string())?;
    ensure(
        other.status().await.map_err(|e| e.to_string())?.is_empty(),
        "profile namespace leaked",
    )?;
    let updated = store
        .store(account(), Token::new(second.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        updated.connection == saved.connection,
        "reauthentication duplicated connection",
    )?;
    ensure(
        store.with_token(&cap, |_| ()).await == Err(Error::Retired),
        "reauthentication preserved old capability",
    )?;
    let cap = store
        .capability(&updated.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store
            .with_token(&cap, |bytes| bytes == second)
            .await
            .map_err(|e| e.to_string())?,
        "replacement token missing",
    )?;
    let script = format!(
        r#"(async()=>{{const invoke=window.__TAURI__.core.invoke;try{{
      const status=await invoke('credential_status');if(status.length!==1||Object.keys(status[0]).sort().join(',')!=='account,connection,origin,provider,state'||status[0].state!=='stored')throw Error('credential metadata boundary failed');
      for(const command of ['credential_token','credential_store']){{let refused=false;try{{await invoke(command,{{connection:{id:?},token:'must-not-be-accepted'}})}}catch{{refused=true;}}if(!refused)throw Error('webview gained token access');}}
      const before=await invoke('runtime_status');await invoke('credential_withdraw',{{connection:{id:?}}});
      if((await invoke('credential_status')).length!==0||(await invoke('runtime_status')).generation<=before.generation)throw Error('withdrawal did not retire browser binding');
      await invoke('report',{{stage:'credential-withdrawn',error:null}});
    }}catch(error){{await invoke('report',{{stage:'failed',error:String(error)}});}}}})()"#,
        id = updated.connection
    );
    app.get_webview_window("main")
        .ok_or("main view missing")?
        .eval(&script)
        .map_err(|_| "credential UI check unavailable")?;
    // The webview writes this acknowledgement only through the native report hook.
    barrier(app, "credential-ui-ready", "credential-ui-withdrawn").await?;
    ensure(
        store.with_token(&cap, |_| ()).await == Err(Error::Withdrawn),
        "withdrawn token remained usable",
    )?;
    let reopened = Credentials::open(&profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        matches!(
            reopened.capability(&updated.connection).await,
            Err(Error::Withdrawn)
        ),
        "withdrawal was not durable",
    )?;
    println!("Credential native/UI isolation, rotation and withdrawal verified");
    let saved = store
        .store(account(), Token::new(second.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    let second_account = Account::new(Provider::Github, "https://github.com", "984321235").unwrap();
    let saved_retained = store
        .store(second_account, Token::new(retained.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    let retained_cap = store
        .capability(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    barrier(app, "credential-lock-ready", "credential-locked").await?;
    let statuses = store.status().await.map_err(|e| e.to_string())?;
    ensure(
        statuses.len() == 2
            && statuses
                .iter()
                .all(|s| matches!(s.state, CredentialState::Locked)),
        "locked keyring appeared ready",
    )?;
    ensure(
        matches!(
            store.capability(&saved.connection).await,
            Err(Error::Locked)
        ),
        "locked keyring issued capability",
    )?;
    ensure(
        store.with_token(&retained_cap, |_| ()).await == Err(Error::Locked),
        "locked keyring exposed token",
    )?;
    ensure(
        store.withdraw(&saved.connection).await == Err(Error::Locked),
        "locked withdrawal was reported successful",
    )?;
    let reopened = Credentials::open(&profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        matches!(
            reopened.capability(&saved.connection).await,
            Err(Error::Withdrawn)
        ),
        "locked withdrawal did not persist denial",
    )?;
    barrier(app, "credential-stop-ready", "credential-service-stopped").await?;
    ensure(
        matches!(store.status().await, Err(Error::Unavailable)),
        "missing service was reported connected",
    )?;
    ensure(
        matches!(
            reopened.capability(&saved.connection).await,
            Err(Error::Withdrawn)
        ),
        "service loss restored withdrawn capability",
    )?;
    barrier(
        app,
        "credential-restart-ready",
        "credential-service-restored",
    )
    .await?;
    ensure(
        store
            .with_token(&retained_cap, |bytes| bytes == retained)
            .await
            .map_err(|e| e.to_string())?,
        "OS keyring restart lost token",
    )?;
    let check_plaintext = |path: &PathBuf| -> Result<(), String> {
        let data = std::fs::read(path).map_err(|_| "encrypted keyring fixture unavailable")?;
        for secret in [&first, &second, &retained] {
            if data.windows(secret.len()).any(|w| w == secret.as_slice()) {
                return Err("keyring persisted plaintext token".into());
            }
        }
        Ok(())
    };
    let data_dir =
        std::env::var_os("XDG_DATA_HOME").ok_or("isolated credential data root missing")?;
    check_plaintext(&PathBuf::from(data_dir).join("keyrings/login.keyring"))?;
    let expiring = store
        .capability(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(30_100)))
        .await
        .map_err(|_| "capability expiry observation stopped")?;
    ensure(
        store.with_token(&expiring, |_| ()).await == Err(Error::Retired),
        "expired token capability remained usable",
    )?;
    let retiring = store
        .capability(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    store
        .withdraw(&saved.connection)
        .await
        .map_err(|e| e.to_string())?;
    store
        .withdraw(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store.status().await.map_err(|e| e.to_string())?.is_empty(),
        "credential fixture cleanup failed",
    )?;
    store.retire();
    ensure(
        store.with_token(&retiring, |_| ()).await == Err(Error::Retired),
        "retired owner admitted token",
    )?;
    Ok(())
}
