//! Real isolated Secret Service + native webview acceptance, opt-in only.
use super::*;
use soda_desktop_runtime::credentials::{Account, CredentialState, Error, Provider, Token};
use std::io::Read;

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
