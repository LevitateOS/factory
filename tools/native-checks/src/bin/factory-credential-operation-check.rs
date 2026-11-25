//! Native credential/provider integration checks. Declared modes use dummy
//! grants; connected modes require explicitly supplied owned credentials.
use soda_desktop_runtime::credentials::{
    Account, CredentialState, Credentials, Error, Grant, Provider, Token, TokenCapability,
};
use std::collections::HashMap;
use std::error::Error as StdError;
use std::fs;
use std::io::Read;
use std::os::fd::AsRawFd;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::sync::oneshot;

type Result<T> = std::result::Result<T, Box<dyn StdError>>;
#[allow(dead_code)]
#[path = "../../../../lib/desktop-runtime/src/private_fs.rs"]
mod private_fs;
#[path = "../../../../lib/desktop-runtime/src/github_provider/traffic_budget.rs"]
mod traffic_budget;
#[path = "../native_github_comments.rs"]
mod native_github_comments;
#[path = "../native_github_edits.rs"]
mod native_github_edits;
#[path = "../native_github_fixture.rs"]
mod native_github_fixture;
#[path = "../native_github_issues.rs"]
mod native_github_issues;
#[path = "../native_github_locks.rs"]
mod native_github_locks;
#[path = "../native_github_pull_branches.rs"]
mod native_github_pull_branches;
#[path = "../native_github_review_requests.rs"]
mod native_github_review_requests;
#[path = "../native_github_states.rs"]
mod native_github_states;
#[path = "../native_github_threads.rs"]
mod native_github_threads;
#[path = "../native_github_transport.rs"]
mod native_github_transport;
#[path = "../native_typesafe.rs"]
mod native_typesafe;
fn ensure(ok: bool, message: &str) -> Result<()> {
    if ok {
        Ok(())
    } else {
        Err(message.into())
    }
}
fn fixture() -> Result<Vec<u8>> {
    let mut bytes = [0; 32];
    fs::File::open("/dev/urandom")?.read_exact(&mut bytes)?;
    Ok(bytes
        .iter()
        .flat_map(|b| format!("{b:02x}").into_bytes())
        .collect())
}
fn account() -> Account {
    Account::new(
        Provider::Github,
        "https://github.com",
        "native-operation-fixture",
    )
    .unwrap()
}
struct Dropped(Arc<AtomicBool>);
impl Drop for Dropped {
    fn drop(&mut self) {
        self.0.store(true, Ordering::SeqCst);
    }
}
struct Flight {
    task: tokio::task::JoinHandle<std::result::Result<bool, Error>>,
    started: oneshot::Receiver<bool>,
    finish: oneshot::Sender<()>,
    dropped: Arc<AtomicBool>,
}
fn launch(
    store: Arc<Credentials>,
    cap: Arc<TokenCapability>,
    expected: Vec<u8>,
    refresh: bool,
) -> Flight {
    let (started, start) = oneshot::channel();
    let (finish, done) = oneshot::channel();
    let dropped = Arc::new(AtomicBool::new(false));
    let marker = dropped.clone();
    let task = tokio::spawn(async move {
        let operation = async move |bytes: &[u8]| {
            let _dropped = Dropped(marker);
            let correct = bytes == expected;
            let _ = started.send(correct);
            let _ = done.await;
            correct
        };
        if refresh {
            store.with_refresh_operation(&cap, operation).await
        } else {
            store.with_token_operation(&cap, operation).await
        }
    });
    Flight {
        task,
        started: start,
        finish,
        dropped,
    }
}
fn launch_stream(
    store: Arc<Credentials>,
    cap: Arc<TokenCapability>,
    expected: Vec<u8>,
    duration: Duration,
) -> Flight {
    let (started, start) = oneshot::channel();
    let (finish, done) = oneshot::channel();
    let dropped = Arc::new(AtomicBool::new(false));
    let marker = dropped.clone();
    let task = tokio::spawn(async move {
        store
            .with_token_stream(&cap, duration, async |bytes| {
                let _dropped = Dropped(marker);
                let correct = bytes == expected;
                let _ = started.send(correct);
                let _ = done.await;
                correct
            })
            .await
    });
    Flight {
        task,
        started: start,
        finish,
        dropped,
    }
}
fn prepare(profile: &Path) -> Result<()> {
    let parent = profile.parent().ok_or("private fixture parent required")?;
    // Linux runs against an isolated session keyring daemon under /home. macOS
    // has no per-session daemon to isolate: fixture items live in the login
    // Keychain under the fixed fixture namespace, which no real profile uses,
    // and each mode refuses a namespace that already holds accounts.
    let owned = cfg!(target_os = "macos") || profile.starts_with("/home");
    ensure(
        profile.is_absolute() && owned && !profile.exists() && parent.is_dir(),
        "fresh owned profile required",
    )?;
    ensure(
        unsafe { libc::geteuid() } != 0,
        "keyring observer requires an ordinary user",
    )?;
    #[cfg(target_os = "linux")]
    for (key, value) in [
        (
            "DBUS_SESSION_BUS_ADDRESS",
            format!("unix:path={}/xdg/bus", parent.display()),
        ),
        ("XDG_DATA_HOME", parent.join("data").display().to_string()),
        (
            "GNOME_KEYRING_CONTROL",
            parent.join("control").display().to_string(),
        ),
    ] {
        ensure(
            std::env::var(key).ok().as_deref() == Some(&value),
            "isolated keyring environment required",
        )?;
    }
    fs::create_dir(profile)?;
    fs::set_permissions(profile, fs::Permissions::from_mode(0o700))?;
    Ok(())
}
async fn run(profile: &Path, restart_keyring: bool) -> Result<()> {
    prepare(profile)?;
    let parent = profile.parent().ok_or("fixture parent missing")?;
    let namespace = "123456789abcdef0123456789abcdef0";
    let mut store = Arc::new(Credentials::open(profile, namespace)?);
    ensure(
        store.status().await?.is_empty(),
        "fixture profile already contains accounts",
    )?;
    let first = fixture()?;
    let second = fixture()?;
    let third = fixture()?;
    let saved = store.store(account(), Token::new(first.clone())?).await?;
    let cap = Arc::new(store.capability(&saved.connection).await?);
    ensure(
        store
            .with_token_operation(&cap, async |bytes| bytes == first)
            .await?
            && store.with_token(&cap, |_| true).await?,
        "healthy native operation failed",
    )?;
    if restart_keyring {
        for _ in 0..256 {
            let current = store.capability(&saved.connection).await?;
            ensure(
                store.status().await?.len() == 1
                    && store.with_token(&current, |bytes| bytes == first).await?,
                "repeated current keyring read failed",
            )?;
        }
        fs::write(parent.join("transport-ready"), b"ready\n")?;
        let until = Instant::now() + Duration::from_secs(20);
        while !parent.join("transport-restarted").exists() {
            ensure(Instant::now() < until, "owned keyring restart driver timed out")?;
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
        let current = store.capability(&saved.connection).await?;
        ensure(
            store.with_token(&current, |bytes| bytes == first).await?,
            "retained credential did not recover after service owner changed",
        )?;
        println!("PASS: 256 fresh status/capability/secret reads and retained credential recovery after real keyring daemon restart");
    }
    let other = Credentials::open(profile, namespace)?;
    let called = AtomicBool::new(false);
    ensure(
        other
            .with_token_operation(&cap, async |_| {
                called.store(true, Ordering::SeqCst);
            })
            .await
            == Err(Error::Retired)
            && !called.load(Ordering::SeqCst),
        "capability escaped native owner",
    )?;

    let flight = launch(store.clone(), cap.clone(), first.clone(), false);
    let stream = launch_stream(store.clone(), cap, first.clone(), Duration::from_secs(10));
    ensure(
        flight.started.await? && stream.started.await?,
        "native operation received wrong token",
    )?;
    store.withdraw(&saved.connection).await?;
    flight
        .finish
        .send(())
        .map_err(|_| "withdrawn callback stopped early")?;
    ensure(
        flight.task.await? == Err(Error::Withdrawn) && flight.dropped.load(Ordering::SeqCst),
        "withdrawal released a stale native result",
    )?;
    ensure(
        tokio::time::timeout(Duration::from_secs(2), stream.task).await?? == Err(Error::Retired)
            && stream.dropped.load(Ordering::SeqCst),
        "local withdrawal did not promptly drop streaming token use",
    )?;
    drop(stream.finish);
    println!("PASS: real keyring withdrawal completes while native I/O is pending and promptly drops a streaming callback");

    let saved = store.store(account(), Token::new(first.clone())?).await?;
    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap.clone(), first.clone(), false);
    let stream = launch_stream(store.clone(), cap, first.clone(), Duration::from_secs(10));
    ensure(
        flight.started.await? && stream.started.await?,
        "rotation operation received wrong token",
    )?;
    // A separate owner forces the post-I/O check to observe actual OS metadata,
    // rather than merely a flag in this Credentials instance.
    let updated = other.store(account(), Token::new(second.clone())?).await?;
    ensure(
        updated.connection == saved.connection,
        "rotation changed connection identity",
    )?;
    flight
        .finish
        .send(())
        .map_err(|_| "rotated callback stopped early")?;
    ensure(
        flight.task.await? == Err(Error::Retired) && flight.dropped.load(Ordering::SeqCst),
        "OS credential revision change released a stale result",
    )?;
    let stream_result = tokio::time::timeout(Duration::from_secs(5), stream.task).await??;
    ensure(
        matches!(stream_result, Err(Error::Retired | Error::Withdrawn))
            && stream.dropped.load(Ordering::SeqCst),
        "independent replacement did not stop streaming token use",
    )?;
    drop(stream.finish);
    println!("PASS: independently reopened keyring owner rotates the real credential during I/O; old streaming use and result refused");

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap.clone(), second.clone(), false);
    let stream = launch_stream(store.clone(), cap, second.clone(), Duration::from_secs(10));
    ensure(
        flight.started.await? && stream.started.await?,
        "retirement operation received wrong token",
    )?;
    store.retire();
    flight
        .finish
        .send(())
        .map_err(|_| "retired callback stopped early")?;
    ensure(
        flight.task.await? == Err(Error::Retired),
        "native owner retirement released a result",
    )?;
    ensure(
        tokio::time::timeout(Duration::from_secs(2), stream.task).await?? == Err(Error::Retired)
            && stream.dropped.load(Ordering::SeqCst),
        "owner retirement retained streaming token use",
    )?;
    drop(stream.finish);
    store = Arc::new(Credentials::open(profile, namespace)?);

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap, second.clone(), false);
    ensure(
        flight.started.await?,
        "cancellation operation received wrong token",
    )?;
    flight.task.abort();
    ensure(
        flight.task.await.is_err_and(|e| e.is_cancelled()) && flight.dropped.load(Ordering::SeqCst),
        "caller cancellation retained native callback",
    )?;
    drop(flight.finish);

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let started = Instant::now();
    let flight = launch(store.clone(), cap, second.clone(), false);
    ensure(
        flight.started.await?,
        "deadline operation received wrong token",
    )?;
    ensure(
        flight.task.await? == Err(Error::Deadline)
            && flight.dropped.load(Ordering::SeqCst)
            && started.elapsed() >= Duration::from_secs(7)
            && started.elapsed() < Duration::from_secs(10),
        "finite operation deadline did not retire its native callback",
    )?;
    drop(flight.finish);
    println!("PASS: caller cancellation and actual eight-second operation deadline drop pending native work");

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let stream = launch_stream(
        store.clone(),
        cap.clone(),
        second.clone(),
        Duration::from_secs(1),
    );
    ensure(
        stream.started.await?,
        "stream deadline received wrong token",
    )?;
    ensure(
        stream.task.await? == Err(Error::Deadline) && stream.dropped.load(Ordering::SeqCst),
        "finite stream deadline retained token use",
    )?;
    drop(stream.finish);
    let called = AtomicBool::new(false);
    for invalid in [Duration::ZERO, Duration::from_secs(4 * 60 * 60 + 1)] {
        ensure(
            store
                .with_token_stream(&cap, invalid, async |_| {
                    called.store(true, Ordering::SeqCst);
                })
                .await
                == Err(Error::Invalid),
            "invalid stream lifetime admitted token use",
        )?;
    }
    ensure(
        !called.load(Ordering::SeqCst),
        "invalid stream invoked callback",
    )?;
    println!("PASS: finite streaming deadline drops token use; zero and over-four-hour lifetimes refuse before callback");

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let lease_started = Instant::now();
    tokio::time::sleep(Duration::from_secs(28)).await;
    let flight = launch(store.clone(), cap.clone(), second.clone(), false);
    let stream = launch_stream(
        store.clone(),
        cap.clone(),
        second.clone(),
        Duration::from_secs(8),
    );
    ensure(
        flight.started.await? && stream.started.await?,
        "expiry operation received wrong token",
    )?;
    ensure(
        flight.task.await? == Err(Error::Retired)
            && flight.dropped.load(Ordering::SeqCst)
            && lease_started.elapsed() >= Duration::from_secs(29),
        "real capability expiry released a result",
    )?;
    drop(flight.finish);
    tokio::time::sleep(Duration::from_secs(1)).await;
    stream
        .finish
        .send(())
        .map_err(|_| "stream ended before renewal")?;
    ensure(
        stream.task.await? == Ok(true)
            && stream.dropped.load(Ordering::SeqCst)
            && lease_started.elapsed() >= Duration::from_secs(30),
        "current streaming credential did not renew beyond the initial lease",
    )?;
    println!("PASS: actual thirty-second admission lease expires during short I/O while a current, rechecked streaming call continues past that lease");

    // Refused capabilities never invoke the callback or retry work. A fresh
    // explicitly acquired capability can observe the current OS credential.
    let called = AtomicBool::new(false);
    ensure(
        store
            .with_token_operation(&cap, async |_| {
                called.store(true, Ordering::SeqCst);
            })
            .await
            == Err(Error::Retired)
            && !called.load(Ordering::SeqCst),
        "expired operation invoked callback",
    )?;
    let updated = store.store(account(), Token::new(third.clone())?).await?;
    let cap = store.capability(&updated.connection).await?;
    ensure(
        store
            .with_token_operation(&cap, async |bytes| bytes == third)
            .await?,
        "fresh credential failed after refused operation",
    )?;
    // The isolated GNOME keyring stores files this check can scan. The macOS
    // login Keychain encrypts items itself and is not scanned here.
    #[cfg(target_os = "linux")]
    let mut keyrings = 0;
    #[cfg(target_os = "linux")]
    for entry in fs::read_dir(parent.join("data/keyrings"))? {
        let path = entry?.path();
        if path.extension().is_some_and(|s| s == "keyring") {
            let encrypted = fs::read(path)?;
            for token in [&first, &second, &third] {
                ensure(
                    !encrypted.windows(token.len()).any(|part| part == token),
                    "fixture token found in plaintext keyring",
                )?;
            }
            keyrings += 1;
        }
    }
    #[cfg(target_os = "linux")]
    ensure(keyrings > 0, "real encrypted keyring file missing")?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "fixture credential cleanup failed",
    )?;
    println!("PASS: asynchronous native credential borrowing, current OS rechecks, expiry/cancellation and encrypted keyring cleanup; no provider login qualified");
    Ok(())
}
fn epoch() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs()
}
fn grant(
    access: &[u8],
    refresh: &[u8],
    access_seconds: u64,
    refresh_seconds: u64,
) -> Result<Grant> {
    Ok(Grant {
        client_id: None,
        access: Token::new(access.to_vec())?,
        refresh: Some(Token::new(refresh.to_vec())?),
        expires_at: Some(epoch() + access_seconds),
        refresh_expires_at: Some(epoch() + refresh_seconds),
        scope: "repo,read:user".into(),
    })
}
async fn after(expiry: u64) {
    while epoch() <= expiry {
        tokio::time::sleep(Duration::from_millis(25)).await;
    }
}
async fn run_grant_custody(profile: &Path) -> Result<()> {
    prepare(profile)?;
    let namespace = "123456789abcdef0123456789abcdef0";
    let store = Arc::new(Credentials::open(profile, namespace)?);
    let access = fixture()?;
    let refresh = fixture()?;
    let replacement = fixture()?;
    let raw = store.store(account(), Token::new(access.clone())?).await?;
    let raw_cap = store.capability(&raw.connection).await?;
    let service =
        secret_service::SecretService::connect(secret_service::EncryptionType::Dh).await?;
    let mut found = service
        .search_items(HashMap::from([
            ("application", "dev.soda.factory"),
            ("profile", namespace),
            ("connection", raw.connection.as_str()),
        ]))
        .await?;
    let item = found.unlocked.pop().ok_or("fixture item missing")?;
    // Actual retained raw-token items predate the format attribute. Removing it
    // in the isolated service qualifies the real decoder rather than a new item.
    let mut raw_attrs = item.get_attributes().await?;
    raw_attrs.remove("format");
    item.set_attributes(
        raw_attrs
            .iter()
            .map(|(key, value)| (key.as_str(), value.as_str()))
            .collect(),
    )
    .await?;
    ensure(
        store.with_token(&raw_cap, |bytes| bytes == access).await?,
        "raw token read failed",
    )?;
    ensure(
        matches!(
            store.with_refresh_operation(&raw_cap, async |_| ()).await,
            Err(Error::Missing)
        ),
        "raw token invented refresh authority",
    )?;
    let saved = store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    ensure(
        saved.connection == raw.connection
            && saved
                .grant
                .as_ref()
                .is_some_and(|g| g.scope == "repo,read:user" && g.refresh_available),
        "grant lost stable connection/metadata",
    )?;
    ensure(
        matches!(
            store.with_token(&raw_cap, |_| ()).await,
            Err(Error::Retired)
        ),
        "grant upgrade retained old raw-token revision",
    )?;
    let reopened = Credentials::open(profile, namespace)?;
    let cap = reopened.capability(&saved.connection).await?;
    ensure(
        reopened
            .with_token_operation(&cap, async |bytes| bytes == access)
            .await?
            && reopened
                .with_refresh_operation(&cap, async |bytes| bytes == refresh)
                .await?,
        "complete grant did not survive native custody reopen",
    )?;
    let statuses = reopened.status().await?;
    let visible = serde_json::to_string(&statuses)?;
    for token in [&access, &refresh, &replacement] {
        ensure(
            !visible.contains(std::str::from_utf8(token)?),
            "credential status exposed a secret",
        )?;
    }
    ensure(
        statuses.len() == 1
            && statuses[0].grant.as_ref().is_some_and(|g| {
                g.expires_at == saved.grant.as_ref().unwrap().expires_at
                    && g.refresh_expires_at == saved.grant.as_ref().unwrap().refresh_expires_at
            }),
        "retained expiry metadata changed",
    )?;
    println!("PASS: raw-token credential upgrades in place to a complete encrypted grant; native owner reopen preserves access/refresh/expiry/scope, status contains only non-secret facts");

    let attrs = item.get_attributes().await?;
    let mut damaged = attrs.clone();
    damaged.remove("format");
    item.set_attributes(
        damaged
            .iter()
            .map(|(key, value)| (key.as_str(), value.as_str()))
            .collect(),
    )
    .await?;
    ensure(
        reopened.with_token(&cap, |_| ()).await == Err(Error::Invalid),
        "damaged grant was forwarded as a raw token",
    )?;
    damaged = attrs.clone();
    damaged.insert("revision".into(), "ffffffffffffffffffffffffffffffff".into());
    item.set_attributes(
        damaged
            .iter()
            .map(|(key, value)| (key.as_str(), value.as_str()))
            .collect(),
    )
    .await?;
    let damaged_cap = reopened.capability(&saved.connection).await?;
    ensure(
        reopened.with_token(&damaged_cap, |_| ()).await == Err(Error::Retired),
        "mismatched grant payload/metadata revision admitted",
    )?;
    item.set_attributes(
        attrs
            .iter()
            .map(|(key, value)| (key.as_str(), value.as_str()))
            .collect(),
    )
    .await?;
    ensure(
        reopened.with_token(&cap, |bytes| bytes == access).await?,
        "restored fixture grant failed",
    )?;
    println!("PASS: real missing-format raw token remains readable; damaged grant format/revision refuses without exposing the serialized grant as access authority");

    let cap = store.capability(&saved.connection).await?;
    let invalid = Grant {
        client_id: None,
        access: Token::new(access.clone())?,
        refresh: None,
        expires_at: Some(epoch() + 120),
        refresh_expires_at: None,
        scope: "repo".into(),
    };
    ensure(
        matches!(
            store.store_grant(account(), invalid).await,
            Err(Error::Invalid)
        ) && store.with_token(&cap, |_| true).await?,
        "invalid grant damaged the current saved account",
    )?;
    let expiring = grant(&access, &refresh, 3, 120)?;
    let access_expiry = expiring.expires_at.unwrap();
    store.store_grant(account(), expiring).await?;
    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap.clone(), access.clone(), false);
    ensure(
        flight.started.await?,
        "expiring access did not reach native operation",
    )?;
    after(access_expiry).await;
    flight
        .finish
        .send(())
        .map_err(|_| "expired operation ended early")?;
    ensure(
        flight.task.await? == Err(Error::Expired) && flight.dropped.load(Ordering::SeqCst),
        "access expiry released stale I/O",
    )?;
    let called = AtomicBool::new(false);
    ensure(
        store
            .with_token_operation(&cap, async |_| {
                called.store(true, Ordering::SeqCst);
            })
            .await
            == Err(Error::Expired)
            && !called.load(Ordering::SeqCst),
        "expired access invoked native callback",
    )?;
    ensure(
        store
            .with_refresh_operation(&cap, async |bytes| bytes == refresh)
            .await?,
        "access expiry suppressed independent valid refresh",
    )?;
    ensure(
        matches!(store.status().await?[0].state, CredentialState::Expired),
        "expired access is shown as stored/usable",
    )?;
    println!("PASS: actual access-token expiry during I/O refuses its result and future callbacks; still-valid refresh remains available; expired status is truthful");

    let expiring = grant(&access, &refresh, 120, 3)?;
    let refresh_expiry = expiring.refresh_expires_at.unwrap();
    store.store_grant(account(), expiring).await?;
    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap.clone(), refresh.clone(), true);
    ensure(
        flight.started.await?,
        "expiring refresh did not reach native operation",
    )?;
    after(refresh_expiry).await;
    flight
        .finish
        .send(())
        .map_err(|_| "expired refresh ended early")?;
    ensure(
        flight.task.await? == Err(Error::Expired) && flight.dropped.load(Ordering::SeqCst),
        "refresh expiry released stale I/O",
    )?;
    ensure(
        store.with_refresh_operation(&cap, async |_| ()).await == Err(Error::Expired),
        "expired refresh invoked another operation",
    )?;
    ensure(
        store.with_token(&cap, |bytes| bytes == access).await?
            && !store.status().await?[0]
                .grant
                .as_ref()
                .unwrap()
                .refresh_available,
        "refresh expiry damaged valid access or lied about refresh",
    )?;

    store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap, refresh.clone(), true);
    ensure(
        flight.started.await?,
        "refresh withdrawal operation did not start",
    )?;
    store.withdraw(&saved.connection).await?;
    flight
        .finish
        .send(())
        .map_err(|_| "withdrawn refresh ended early")?;
    ensure(
        flight.task.await? == Err(Error::Withdrawn),
        "withdrawal released stale refresh I/O",
    )?;

    let saved = store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap, refresh.clone(), true);
    ensure(
        flight.started.await?,
        "refresh rotation operation did not start",
    )?;
    let rotated = reopened
        .store_grant(account(), grant(&replacement, &replacement, 120, 180)?)
        .await?;
    flight
        .finish
        .send(())
        .map_err(|_| "rotated refresh ended early")?;
    ensure(
        flight.task.await? == Err(Error::Retired),
        "independent keyring replacement released stale refresh I/O",
    )?;
    let cap = Arc::new(store.capability(&rotated.connection).await?);
    let flight = launch(store.clone(), cap, replacement.clone(), true);
    ensure(
        flight.started.await?,
        "refresh cancellation operation did not start",
    )?;
    flight.task.abort();
    ensure(
        flight.task.await.is_err_and(|e| e.is_cancelled()) && flight.dropped.load(Ordering::SeqCst),
        "cancelled refresh retained native work",
    )?;
    drop(flight.finish);
    println!("PASS: independent refresh expiry, concurrent withdrawal/replacement and caller cancellation refuse stale native results");

    let parent = profile.parent().ok_or("fixture parent missing")?;
    let mut encrypted_files = 0;
    for entry in fs::read_dir(parent.join("data/keyrings"))? {
        let path = entry?.path();
        if path.extension().is_some_and(|s| s == "keyring") {
            let encrypted = fs::read(path)?;
            for token in [&access, &refresh, &replacement] {
                ensure(
                    !encrypted.windows(token.len()).any(|part| part == token),
                    "grant secret found in plaintext keyring",
                )?;
            }
            encrypted_files += 1;
        }
    }
    ensure(encrypted_files > 0, "encrypted keyring file missing")?;
    store.withdraw(&rotated.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "grant credential cleanup failed",
    )?;
    println!("PASS: actual encrypted Linux keyring contains no plaintext access/refresh fixtures; withdrawal removes complete grant; no provider consent/login claimed");
    Ok(())
}
async fn run_rotation_probe(profile: &Path, connection: &str) -> Result<()> {
    let store = Credentials::open(profile, "123456789abcdef0123456789abcdef0")?;
    let cap = store.capability(connection).await?;
    let called = AtomicBool::new(false);
    ensure(
        store.with_token(&cap, |_| ()).await == Err(Error::RefreshPending),
        "reopened process reused uncertain access",
    )?;
    ensure(
        matches!(
            store
                .rotate_grant(&cap, async |_| {
                    called.store(true, Ordering::SeqCst);
                    Err(Error::Unavailable)
                })
                .await,
            Err(Error::RefreshPending)
        ) && !called.load(Ordering::SeqCst),
        "reopened process replayed uncertain refresh",
    )?;
    println!(
        "PASS: separate native process refuses retained uncertain access/refresh without dispatch"
    );
    Ok(())
}
async fn run_rotation_writer(profile: &Path, connection: &str) -> Result<()> {
    let store = Credentials::open(profile, "123456789abcdef0123456789abcdef0")?;
    fs::write(profile.join("rotation-writer-entered"), b"")?;
    let token = fixture()?;
    let saved = store
        .store_grant(account(), grant(&token, &token, 120, 180)?)
        .await?;
    ensure(
        saved.connection == connection,
        "serialized writer changed connection",
    )?;
    println!("PASS: separate credential writer resumes after the OS mutation lease is released");
    Ok(())
}
async fn run_rotation_crash(profile: &Path, connection: &str) -> Result<()> {
    let store = Credentials::open(profile, "123456789abcdef0123456789abcdef0")?;
    let cap = store.capability(connection).await?;
    store
        .rotate_grant(&cap, async |_| {
            fs::write(profile.join("rotation-crash-entered"), b"").map_err(|_| Error::Storage)?;
            std::future::pending::<()>().await;
            Err(Error::Unavailable)
        })
        .await?;
    Err("crash fixture returned unexpectedly".into())
}
async fn run_rotation(profile: &Path) -> Result<()> {
    prepare(profile)?;
    let namespace = "123456789abcdef0123456789abcdef0";
    let store = Arc::new(Credentials::open(profile, namespace)?);
    let other = Credentials::open(profile, namespace)?;
    let access = fixture()?;
    let refresh = fixture()?;
    let next = fixture()?;
    let saved = store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    let lock = fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(profile.join("credential-mutation.lock"))?;
    ensure(
        unsafe { libc::flock(lock.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0,
        "fixture mutation lease failed",
    )?;
    let mut child = std::process::Command::new(std::env::current_exe()?)
        .arg("--rotation-writer")
        .arg(profile)
        .arg(&saved.connection)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()?;
    let until = Instant::now() + Duration::from_secs(2);
    while !profile.join("rotation-writer-entered").exists() {
        ensure(
            child.try_wait()?.is_none() && Instant::now() < until,
            "writer did not enter mutation lease check",
        )?;
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    tokio::time::sleep(Duration::from_millis(100)).await;
    ensure(
        child.try_wait()?.is_none(),
        "independent writer bypassed an owned OS mutation lease",
    )?;
    drop(lock);
    let output = child.wait_with_output()?;
    ensure(
        output.status.success(),
        "writer failed after releasing mutation lease",
    )?;
    print!("{}", String::from_utf8(output.stdout)?);
    store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    let cap = store.capability(&saved.connection).await?;
    let replacement = grant(&next, &next, 120, 180)?;
    let rotated = store
        .rotate_grant(&cap, async |bytes| {
            if bytes != refresh {
                return Err(Error::Invalid);
            }
            Ok((account(), replacement))
        })
        .await?;
    ensure(
        rotated.connection == saved.connection
            && !profile
                .join("refreshing-credentials")
                .join(&saved.connection)
                .exists(),
        "rotation changed identity or left a successful intent",
    )?;
    ensure(
        store.with_token(&cap, |_| ()).await == Err(Error::Retired),
        "rotation retained the old revision",
    )?;
    let cap = other.capability(&saved.connection).await?;
    ensure(
        other.with_token(&cap, |bytes| bytes == next).await?
            && other
                .with_refresh_operation(&cap, async |bytes| bytes == next)
                .await?,
        "rotation dropped a complete replacement secret",
    )?;
    println!("PASS: complete conditional grant rotation preserves connection, advances revision and clears its durable intent only after publication");

    let mut child = std::process::Command::new(std::env::current_exe()?)
        .arg("--rotation-crash")
        .arg(profile)
        .arg(&saved.connection)
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()?;
    let until = Instant::now() + Duration::from_secs(2);
    while !profile.join("rotation-crash-entered").exists() {
        ensure(
            child.try_wait()?.is_none() && Instant::now() < until,
            "crash fixture never dispatched after reservation",
        )?;
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    child.kill()?;
    ensure(!child.wait()?.success(), "crash fixture did not terminate")?;
    let result = std::process::Command::new(std::env::current_exe()?)
        .arg("--rotation-probe")
        .arg(profile)
        .arg(&saved.connection)
        .output()?;
    ensure(
        result.status.success(),
        "killed owner left replayable refresh",
    )?;
    print!("{}", String::from_utf8(result.stdout)?);
    println!("PASS: actual native credential process dies after dispatch; another process refuses its durable uncertain revision");
    store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;

    let cap = store.capability(&saved.connection).await?;
    ensure(
        matches!(
            store
                .rotate_grant(&cap, async |_| Err(Error::Unavailable))
                .await,
            Err(Error::Unavailable)
        ),
        "declared lost reply was accepted",
    )?;
    ensure(
        matches!(
            store.status().await?[0].state,
            CredentialState::RefreshPending
        ),
        "lost reply was shown as usable",
    )?;
    let result = std::process::Command::new(std::env::current_exe()?)
        .arg("--rotation-probe")
        .arg(profile)
        .arg(&saved.connection)
        .output()?;
    ensure(
        result.status.success(),
        "separate process replayed retained uncertain refresh",
    )?;
    print!("{}", String::from_utf8(result.stdout)?);
    store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    let incomplete = fs::OpenOptions::new().write(true).create_new(true).open(
        profile
            .join("refreshing-credentials")
            .join(&saved.connection),
    )?;
    incomplete.set_permissions(fs::Permissions::from_mode(0o600))?;
    incomplete.sync_all()?;
    let cap = other.capability(&saved.connection).await?;
    ensure(
        other.with_token(&cap, |_| ()).await == Err(Error::RefreshPending),
        "incomplete durable reservation admitted access",
    )?;
    store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let (entered, start) = oneshot::channel();
    let (release, finish) = oneshot::channel();
    let worker = store.clone();
    let pending_cap = cap.clone();
    let replacement = grant(&next, &next, 120, 180)?;
    let task = tokio::spawn(async move {
        worker
            .rotate_grant(&pending_cap, async |_| {
                let _ = entered.send(());
                let _ = finish.await;
                Ok((account(), replacement))
            })
            .await
    });
    start.await?;
    ensure(
        profile
            .join("refreshing-credentials")
            .join(&saved.connection)
            .is_file(),
        "callback preceded durable reservation",
    )?;
    let other_cap = other.capability(&saved.connection).await?;
    let called = AtomicBool::new(false);
    ensure(
        matches!(
            other
                .rotate_grant(&other_cap, async |_| {
                    called.store(true, Ordering::SeqCst);
                    Err(Error::Unavailable)
                })
                .await,
            Err(Error::RefreshPending)
        ) && !called.load(Ordering::SeqCst),
        "concurrent owner dispatched a duplicate refresh",
    )?;
    let newer = other
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    release
        .send(())
        .map_err(|_| "old refresh stopped before reply")?;
    ensure(
        matches!(task.await?, Err(Error::Retired)),
        "stale refresh overwrote reauthentication",
    )?;
    let current = other.capability(&newer.connection).await?;
    ensure(
        other.with_token(&current, |bytes| bytes == access).await?,
        "stale refresh damaged newer credentials",
    )?;
    println!("PASS: reservation is durable before I/O; a second owner cannot dispatch; independent reauthentication proceeds during I/O and survives the stale reply");

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let (entered, start) = oneshot::channel();
    let (release, finish) = oneshot::channel();
    let worker = store.clone();
    let replacement = grant(&next, &next, 120, 180)?;
    let task = tokio::spawn(async move {
        worker
            .rotate_grant(&cap, async |_| {
                let _ = entered.send(());
                let _ = finish.await;
                Ok((account(), replacement))
            })
            .await
    });
    start.await?;
    other.withdraw(&saved.connection).await?;
    release
        .send(())
        .map_err(|_| "withdrawn refresh stopped early")?;
    ensure(
        matches!(task.await?, Err(Error::Withdrawn)) && other.status().await?.is_empty(),
        "refresh resurrected a withdrawn/deleted credential",
    )?;
    let saved = store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let (entered, start) = oneshot::channel();
    let (_release, finish) = oneshot::channel::<()>();
    let dropped = Arc::new(AtomicBool::new(false));
    let marker = dropped.clone();
    let worker = store.clone();
    let task = tokio::spawn(async move {
        worker
            .rotate_grant(&cap, async |_| {
                let _marker = Dropped(marker);
                let _ = entered.send(());
                let _ = finish.await;
                Err(Error::Unavailable)
            })
            .await
    });
    start.await?;
    task.abort();
    ensure(
        task.await.is_err_and(|e| e.is_cancelled()) && dropped.load(Ordering::SeqCst),
        "cancelled rotation retained callback",
    )?;
    let reopened = Credentials::open(profile, namespace)?;
    let cap = reopened.capability(&saved.connection).await?;
    ensure(
        reopened.with_refresh_operation(&cap, async |_| ()).await == Err(Error::RefreshPending),
        "cancelled rotation replayed refresh after reopen",
    )?;
    store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;

    let cap = store.capability(&saved.connection).await?;
    let wrong = Account::new(
        Provider::Github,
        "https://github.com",
        "different-remote-account",
    )?;
    let replacement = grant(&next, &next, 120, 180)?;
    ensure(
        matches!(
            store
                .rotate_grant(&cap, async |_| Ok((wrong, replacement)))
                .await,
            Err(Error::Retired)
        ),
        "rotation changed verified account",
    )?;
    store
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    println!("PASS: withdrawal cannot be resurrected, cancelled I/O drops, retained uncertainty cannot replay, and a different verified account cannot replace the captured identity");

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let (entered, start) = oneshot::channel();
    let (release, finish) = oneshot::channel();
    let worker = store.clone();
    let replacement = grant(&next, &next, 120, 180)?;
    let task = tokio::spawn(async move {
        worker
            .rotate_grant(&cap, async |_| {
                let _ = entered.send(());
                let _ = finish.await;
                Ok((account(), replacement))
            })
            .await
    });
    start.await?;
    store.retire();
    release
        .send(())
        .map_err(|_| "retired rotation ended early")?;
    ensure(
        matches!(task.await?, Err(Error::Retired)),
        "retired owner published rotation",
    )?;
    reopened
        .store_grant(account(), grant(&access, &refresh, 120, 180)?)
        .await?;
    let cap = reopened.capability(&saved.connection).await?;
    let before = Instant::now();
    ensure(
        matches!(
            reopened
                .rotate_grant(&cap, async |_| {
                    std::future::pending::<()>().await;
                    Err(Error::Unavailable)
                })
                .await,
            Err(Error::Deadline)
        ) && before.elapsed() >= Duration::from_secs(7)
            && before.elapsed() < Duration::from_secs(10),
        "rotation has no actual bounded operation deadline",
    )?;
    ensure(
        matches!(
            reopened.status().await?[0].state,
            CredentialState::RefreshPending
        ),
        "timed-out rotation lost its durable uncertainty",
    )?;
    reopened.withdraw(&saved.connection).await?;
    ensure(
        reopened.status().await?.is_empty(),
        "rotation fixture cleanup failed",
    )?;
    println!("PASS: retired owner refuses publication, actual eight-second deadline leaves durable uncertainty, and complete fixture withdrawal succeeds; no real provider refresh/login claimed");
    Ok(())
}
async fn fountain_watch_declared(client:Arc<soda_desktop_runtime::fountain_provider::Client>,enrolled:soda_desktop_runtime::fountain_provider::Enrollment,io:&Path)->Result<()> {
 use soda_desktop_runtime::{factory_provider::{GrantWatches,AgentCancellation},fountain_provider::OperationGrant};
 fs::create_dir_all(io)?;
 let now=SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs() as i64;
 let grant=OperationGrant{id:"123456789abcdef0123456789abcdef2".into(),enrollment_id:enrolled.id,runtime_id:enrolled.runtime_id,installation_id:"fixture-installation".into(),generation:"1".into(),actor:enrolled.actor,repository:enrolled.repository,operations:vec!["execution.launch".into()],not_after:now+60,active:true};
 fs::write(io.join("watch-grant.json"),serde_json::to_vec(&grant)?)?;fs::write(io.join("mode"),"watch")?;
 let grant=client.operation_grant(&grant.id).await.map_err(|e|e.code)?;
 let count=||->Result<u32>{Ok(fs::read_to_string(io.join("watch-sends"))?.parse()?)};
 let watches=GrantWatches::default();let alive=Arc::new(AtomicBool::new(true));
 let a=AgentCancellation::default();let b=AgentCancellation::default();let short=AgentCancellation::default();
 let before=count()?;
 let one=watches.observe_execution(client.clone(),grant.clone(),now+30,a.clone(),alive.clone())?;
 let two=watches.observe_execution(client.clone(),grant.clone(),now+30,b.clone(),alive.clone())?;
 let until=Instant::now()+Duration::from_secs(2);
 while count()?==before {ensure(Instant::now()<until,"watch did not start")?;tokio::time::sleep(Duration::from_millis(10)).await;}
 tokio::time::sleep(Duration::from_millis(1100)).await;
 ensure(count()?==before+2,"matching leases multiplied grant streams")?;
 let mut other=grant.clone();other.id="123456789abcdef0123456789abcdef3".into();
 let c=AgentCancellation::default();let foreign=watches.observe_execution(client.clone(),other,now+30,c.clone(),alive.clone())?;
 let until=Instant::now()+Duration::from_secs(2);
 while count()?==before+2 {ensure(Instant::now()<until,"distinct grant did not start its own watch")?;tokio::time::sleep(Duration::from_millis(10)).await;}
 drop(foreign);ensure(!a.cancelled()&&!b.cancelled(),"detaching different grant cancelled siblings")?;
 let deadline=SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs() as i64+1;
 let short_lease=watches.observe_execution(client.clone(),grant.clone(),deadline,short.clone(),alive.clone())?;
 let until=Instant::now()+Duration::from_secs(2);
 while !short.cancelled(){ensure(Instant::now()<until,"individual deadline did not cancel")?;tokio::time::sleep(Duration::from_millis(10)).await;}
 ensure(!a.cancelled()&&!b.cancelled(),"short lease deadline cancelled survivors")?;drop(short_lease);
 fs::write(io.join("mode"),"watch-withdraw")?;
 let until=Instant::now()+Duration::from_secs(3);
 while !a.cancelled()||!b.cancelled(){ensure(Instant::now()<until,"withdrawal did not cancel every member")?;tokio::time::sleep(Duration::from_millis(10)).await;}
 drop(one);drop(two);let final_count=count()?;tokio::time::sleep(Duration::from_millis(1100)).await;ensure(count()?==final_count,"last detach left live grant traffic")?;
 fs::write(io.join("mode"),"enrollment")?;
 println!("PASS: two same-grant leases share exactly2 polls over1.1s; distinct grants separate, per-lease deadline preserves siblings, withdrawal cancels all and last detach joins traffic; no claims or launch");
 Ok(())
}

async fn fountain_enrollment(profile: &Path, origin: &str, application: &str) -> Result<()> {
    use soda_desktop_runtime::{fountain_provider::Client, mcp_signing::Signer};
    prepare(profile)?;
    let lock = fs::OpenOptions::new().create_new(true).write(true).open(profile.join("fixture.lock"))?;
    ensure(unsafe { libc::flock(lock.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0, "fixture profile lock failed")?;
    let runtime = "123456789abcdef0123456789abcdef0";
    let command = "123456789abcdef0123456789abcdef1";
    let store = Arc::new(Credentials::open(profile, runtime)?);
    let mut token = Vec::new();
    std::io::stdin().take(65537).read_to_end(&mut token)?;
    ensure(!token.is_empty() && token.len() <= 65536, "bounded fixture token required")?;
    let saved = store.store(Account::new(Provider::Fountain, origin, "2")?, Token::new(token)?).await?;
    let result = async {
        let signer = Signer::open(profile)?;
        let client = Client::new(store.clone(), store.capability(&saved.connection).await?, &signer, runtime, application, "user2", "repo1", command).map_err(|e| e.code)?;
        let enrolled = client.enroll().await.map_err(|e| e.code)?;
        ensure(enrolled.active && enrolled.runtime_bound && enrolled.actor.username == "user2", "bound Fountain enrollment missing")?;
        if std::env::var_os("FACTORY_DECLARED_FOUNTAIN_WATCH").is_some(){
            return fountain_watch_declared(Arc::new(client),enrolled, &profile.parent().ok_or("fixture parent missing")?.join("io")).await;
        }
        if std::env::var("FACTORY_DECLARED_FOUNTAIN_READS").as_deref() == Ok("1") {
            let io = profile.parent().ok_or("fixture parent missing")?.join("io");
            fs::create_dir(&io)?;
            use soda_desktop_runtime::provider_contract::Read;
            for (mode, kind, count) in [
                ("merge", "merge_preparation", 11), ("checks", "automation_checks", 9),
                ("empty-runs", "pull_check_runs", 4), ("conflict", "merge_preparation", 15),
                ("revision-change", "merge_preparation", 0), ("head-change", "merge_preparation", 0),
                ("permission-change", "merge_preparation", 0), ("withdrawal", "merge_preparation", 0),
                ("veto", "merge_preparation", 11), ("pending", "merge_preparation", 0),
                ("retry-exhaustion", "merge_preparation", 0), ("slow-body", "merge_preparation", 0),
                ("threads-small", "pull_threads", 17), ("threads-many", "pull_threads", 17),
                ("threads-body-change", "pull_threads", 0), ("threads-revision-change", "pull_threads", 0),
            ] {
                fs::write(io.join("mode"), mode)?;
                let query = match kind {
                    "pull_threads" => Read::PullThreads { repository: enrolled.repository.id.clone(), number:7, page:1,limit:1,thread_id:String::new(),cursor:String::new() },
                    "automation_checks" => Read::AutomationChecks { repository: enrolled.repository.id.clone(), number: 7 },
                    "pull_check_runs" => Read::PullCheckRuns { repository: enrolled.repository.id.clone(), number: 7, head: String::new(), page: 1, limit: 10 },
                    _ => Read::MergePreparation { repository: enrolled.repository.id.clone(), number: 7 },
                };
                let started = std::time::Instant::now();
                let result = client.product_read(&enrolled.repository.id, &query).await;
                if mode == "slow-body" { ensure(result.is_err() && started.elapsed() < Duration::from_millis(8500), "Fountain body outlived operation deadline")?; }
                let calls: u32 = fs::read_to_string(io.join("count"))?.parse()?;
                if count == 0 {
                    ensure(result.is_err(), "changed Fountain facts escaped paired observation")?;
                    ensure(calls <= if kind=="pull_threads"{17}else{11}, "refusal exceeded complete read bound")?;
                } else {
                    let value = result.map_err(|error| error.code)?;
                    ensure(calls == count, "Fountain compound read amplified wire calls")?;
                    if kind == "pull_threads" { ensure(value["threads"]["items"].as_array().is_some_and(|items| items.len()==1) && value["threads"]["items"][0]["comments"]["items"][0]["body"] == "Preserved human thread body", "selected Fountain thread body or window changed")?; }
                    if mode == "veto" { ensure(value["preparation"]["provider_eligible"] == false, "human veto disappeared")?; }
                    if mode == "merge" { ensure(value["preparation"]["provider_eligible"] == true && value["preparation"]["checks"][0]["state"] == "success", "merge union lost check/eligibility")?; }
                }
            }
            println!("PASS: declared Fountain merge11/checks9/empty-runs4 sends, four conflicts add4; closing revision/head/permission/withdrawal, veto/pending and shared retry exhaustion checked");
            fs::write(io.join("mode"), "enrollment")?;
        }
        let repository = client.product_read(&enrolled.repository.id,&soda_desktop_runtime::provider_contract::Read::Repository{repository:enrolled.repository.id.clone()}).await.map_err(|e| e.code)?;
        ensure(repository["repository"]["external_id"] == enrolled.repository.id, "Fountain repository changed")?;
        drop(client);
        drop(signer);
        let signer = Signer::open(profile)?;
        let client = Client::new(store.clone(), store.capability(&saved.connection).await?, &signer, runtime, application, "user2", "repo1", command).map_err(|e| e.code)?;
        ensure(client.enroll().await.map_err(|e|e.code)?.active, "reopened native proof cannot replay enrollment")?;
        ensure(client.status().await.map_err(|e|e.code)?.active, "Fountain enrollment readback missing")?;
        ensure(!client.withdraw().await.map_err(|e|e.code)?.active, "Fountain withdrawal missing")?;
        ensure(!client.enroll().await.map_err(|e|e.code)?.active && !client.status().await.map_err(|e|e.code)?.active, "withdrawal replay reactivated enrollment")?;
        ensure(matches!(client.product_read(&enrolled.repository.id,&soda_desktop_runtime::provider_contract::Read::Repository{repository:enrolled.repository.id.clone()}).await, Err(error) if matches!(error.status,400|403)), "withdrawn repository read was admitted")?;
        Ok::<_, Box<dyn StdError>>(())
    }.await;
    let withdrawn = store.withdraw(&saved.connection).await;
    store.retire();
    result?;
    withdrawn?;
    if std::env::var_os("FACTORY_DECLARED_FOUNTAIN_WATCH").is_some() {
        println!("Fountain native shared-watch check passed; owned fixture credential withdrawn");
    } else { println!("Fountain native TLS enrollment/read/reopen/replay/withdrawal passed; owned fixture credential withdrawn"); }
    Ok(())
}

async fn controller_worker(profile: &Path, credentials: Arc<Credentials>, client: Arc<soda_desktop_runtime::fountain_provider::Client>, origin: &str, connection: &str, grant: &soda_desktop_runtime::fountain_provider::OperationGrant) -> Result<Option<soda_desktop_runtime::fountain_provider::ExecutionClaimRequest>> {
    use soda_desktop_runtime::{supervisor::NativeSupervisor, factory_provider::{LaunchIntent,ExecutionOutcome}};
    use std::os::unix::fs::OpenOptionsExt;
    use std::io::Write;
    use std::process::Command;
    let payload=std::env::var("FACTORY_FOUNTAIN_CONTROLLER_PAYLOAD")?;
    let backend=std::env::var("FACTORY_FOUNTAIN_CONTROLLER_BACKEND")?;
    let check=std::env::var("FACTORY_FOUNTAIN_CONTROLLER_GO_CHECK")?;
    let finite=std::env::var("FACTORY_FOUNTAIN_CONTROLLER_FINITE")?;
    let mut identity=fs::OpenOptions::new().create_new(true).write(true).mode(0o600).open(profile.join("profile.id"))?;
    identity.write_all(grant.runtime_id.as_bytes())?;identity.sync_all()?;drop(identity);
    let supervisor=NativeSupervisor::start(Path::new(&payload),Path::new(&backend),profile)?;
    let ready=supervisor.health()?;
    ensure(ready.profile==grant.runtime_id,"native controller profile changed")?;
    supervisor.enroll_credentials(credentials.clone())?;
    supervisor.observe_fountain_controller(client.clone()).await?;
    let product_request = |method:&'static str,path:String,body:Vec<u8>| {
        let supervisor=&supervisor;
        let credentials=credentials.clone();
        let client=client.clone();
        let repository=grant.repository.id.clone();
        async move {
            let capability=credentials.capability(connection).await?;
            let request=supervisor.admit_fountain_product(credentials,capability,repository,client)?;
            let reply=request.request(method,&path,&body).await?;
            if reply.status!=200 {
                let failure:serde_json::Value=serde_json::from_slice(&reply.body)?;
                return Err(format!("connected Fountain shared product read {path}: HTTP {}, code={}",reply.status,failure["error"]["code"].as_str().unwrap_or("unavailable")).into());
            }
            Ok::<serde_json::Value,Box<dyn std::error::Error>>(serde_json::from_slice(&reply.body)?)
        }
    };
    let product_read = |path:String| product_request("GET",path,Vec::new());
    let identity=product_read("/api/factory/identity".into()).await?;
    ensure(identity["host"].as_str().is_some_and(|id|id.len()==32)&&identity["actor"].as_str().is_some(),"native Fountain canonical identity missing")?;
    let catalog=product_read("/api/factory/collaboration/repositories".into()).await?;
    let items=catalog["items"].as_array().ok_or("native Fountain catalog absent")?;
    ensure(catalog["complete"]==true&&items.len()==1&&items[0]["external_id"]==grant.repository.id&&items[0]["host"]==identity["host"],"native Fountain catalog scope changed")?;
    let repository=items[0]["id"].as_str().ok_or("canonical Fountain repository absent")?;
    let configuration=product_read(format!("/api/repositories/{repository}/factory")).await?;
    ensure(configuration["native_bindings"].as_array().is_some_and(|refs|refs.len()==6&&refs.iter().all(|r|r["controller_grant_id"].is_null()))&&configuration["policy_options"]["controller_grant_required"]==true&&configuration["policy_options"]["native_setup"]==true,"Fountain policy references lost their explicit host-grant prerequisite")?;
    let setup=product_read(format!("/api/repositories/{repository}/factory/setup")).await?;
    ensure(setup["available"]==false,"Fountain read enrollment exposed preparation without captured host authority")?;
    let placement_path=format!("/api/repositories/{repository}/factory/placement");
    let placement=product_read(placement_path.clone()).await?;
    ensure(placement["can_select"]==false,"Fountain connection offered standalone placement authority")?;
    ensure(placement["can_configure_local"]==true,"host placement disabled independent local resource consent")?;
    let request=supervisor.admit_fountain_product(credentials.clone(),credentials.capability(connection).await?,grant.repository.id.clone(),client.clone())?;
    let refused=request.request("PUT",&placement_path,br#"{"expected_revision":"","state":"active"}"#).await?;
    ensure(refused.status==503,"Fountain standalone placement write was not refused before changing host observation")?;
    let binding=serde_json::json!({"actor_id":identity["actor"],"token_id":"0","native_profile":ready.profile,"native_connection":connection,"controller_grant_id":grant.id});
    let mut policy=serde_json::json!({"expected_revision":0,"enabled":false,"paused":false,"target_branch":format!("refs/heads/{}",grant.repository.default_branch),"merge_method":"merge","required_checks":["fixture/unavailable"],
        "roles":{"soda-coder":{"harness":"muse","model":"fixture-unavailable"},"soda-reviewer":{"harness":"muse","model":"fixture-unavailable"}},"publish_actor":binding,"create_actor":binding,"review_actor":binding,"merge_actor":binding,"execution_actor":binding});
    if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_MERGE").is_some(){policy["merge_method"]=serde_json::json!("fast-forward-only");policy["required_checks"]=serde_json::json!(["actions:Native CI / exact-candidate (pull_request)"]);}
    let request=supervisor.admit_fountain_product(credentials.clone(),credentials.capability(connection).await?,grant.repository.id.clone(),client.clone())?;
    let refused=request.request("PUT",&format!("/api/repositories/{repository}/factory/policy"),&serde_json::to_vec(&policy)?).await?;
    ensure(refused.status!=200&&product_read(format!("/api/repositories/{repository}/factory")).await?["policy"].is_null(),"limited host grant was enlarged into publication policy")?;
    let placement=client.placement().await.map_err(|e|e.code)?;
    let full=client.authorize_operations(profile,&soda_desktop_runtime::fountain_provider::OperationAuthorization{
        command_id:"123456789abcdef0123456789abcdefa".into(),expected_placement:placement.revision,generation:placement.generation,
        operations:vec!["execution.launch".into(),"git.ref.publish".into(),"pull_request.create".into(),"pull_request.review.submit".into(),"pull_request.merge".into()],not_after:epoch() as i64+if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_MERGE").is_some(){600}else{90}}).await.map_err(|e|e.code)?;
    for slot in ["publish_actor","create_actor","review_actor","merge_actor"] { policy[slot]["controller_grant_id"]=serde_json::json!(full.id); }
    let request=supervisor.admit_fountain_product(credentials.clone(),credentials.capability(connection).await?,grant.repository.id.clone(),client.clone())?;
    let saved=request.request("PUT",&format!("/api/repositories/{repository}/factory/policy"),&serde_json::to_vec(&policy)?).await?;
    if saved.status!=200 { return Err(format!("actual Fountain policy save HTTP {}: {}",saved.status,String::from_utf8_lossy(&saved.body)).into()); }
    let saved=product_read(format!("/api/repositories/{repository}/factory")).await?;
    ensure(saved["policy"]["execution_actor"]["controller_grant_id"]==grant.id&&saved["policy"]["publish_actor"]["controller_grant_id"]==full.id&&saved["policy"]["enabled"]==false,"Fountain policy did not retain the explicit original host grants")?;
    println!("PASS: actual shared Fountain policy refuses a limited host grant and captures a fresh explicitly selected full operation grant with automation disabled; no funding or execution inferred");
    let base=format!("/api/factory/collaboration/repositories/{repository}");
    let first=product_read(format!("{base}/issues?limit=1&state=all")).await?;
    ensure(first["items"].as_array().is_some_and(|items|items.len()==1),"native Fountain first issue page absent")?;
    let cursor=first["next"].as_str().filter(|v|!v.is_empty()).ok_or("native Fountain next issue page absent")?;
    let cursor:String=url::form_urlencoded::byte_serialize(cursor.as_bytes()).collect();
    let second=product_read(format!("{base}/issues?limit=1&state=all&cursor={cursor}")).await?;
    ensure(second["items"].as_array().is_some_and(|items|items.len()==1)&&second["items"][0]["ref"]!=first["items"][0]["ref"],"native Fountain pagination duplicated its first issue")?;
    let number=first["items"][0]["ref"]["number"].as_str().ok_or("native Fountain issue number absent")?;
    let issue=product_read(format!("{base}/issues/{number}")).await?;
    ensure(issue["ref"]==first["items"][0]["ref"],"native Fountain issue detail changed identity")?;
    product_read(format!("{base}/issues/{number}/comments?limit=1")).await?;
    let pulls=product_read(format!("{base}/pulls?limit=1&state=all")).await?;
    ensure(pulls["items"].as_array().is_some_and(|items|items.len()==1),"native Fountain pull page absent")?;
    let pull_number=pulls["items"][0]["ref"]["number"].as_str().ok_or("native Fountain pull number absent")?;
    let pull=product_read(format!("{base}/pulls/{pull_number}")).await?;
    ensure(pull["ref"]==pulls["items"][0]["ref"]&&pull["base"]==pulls["items"][0]["base"]&&pull["head"]==pulls["items"][0]["head"],"native Fountain pull detail changed repository/head/base identity")?;
    product_read(format!("{base}/pulls/{pull_number}/comments?limit=1")).await?;
    if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_INPUTS").is_some() {
        use soda_desktop_runtime::github_provider::Read;
        let fixture:serde_json::Value=serde_json::from_slice(&fs::read(profile.parent().ok_or("fixture root absent")?.join("inputs-fixture.json"))?)?;
        let number=u32::try_from(fixture["number"].as_u64().ok_or("snapshot fixture number absent")?)?;
        let comment=fixture["comment"].as_str().ok_or("snapshot fixture comment absent")?.to_owned();
        let query=Read::AutomationInputs{repository:grant.repository.id.clone(),number,comments:vec![comment.clone()],target_ref:format!("refs/heads/{}",grant.repository.default_branch)};
        let observed=client.product_read(&grant.repository.id,&query).await.map_err(|e|e.code)?;
        let input=&observed["inputs"];
        ensure(input["issue"]["id"]==fixture["id"]&&input["issue"]["version"]==0&&input["issue"]["title_occurrence"]=="original"&&input["issue"]["lifecycle_occurrence"]=="original"&&input["issue"]["body"]=="Preserve this exact admitted objective.\n","native snapshot changed original creation/body/version")?;
        ensure(input["comments"][0]["id"]==comment&&input["comments"][0]["body"]=="Selected native context.\n"&&input["comments"][0]["version"]==0,"native selected comment changed exact text/version")?;
        ensure(input["dependencies"].as_array().is_some_and(|edges|edges.len()==1)&&input["dependencies"][0]["depends_on"]==fixture["dependency"]&&input["dependencies"][0]["number"]==fixture["dependency_number"],"native dependency occurrence lost its scoped target locator")?;
        let permission=client.product_read(&grant.repository.id,&Read::IssueCreatorPermission{repository:grant.repository.id.clone(),number,creator:grant.actor.id.clone()}).await.map_err(|e|e.code)?;
        ensure(permission["capabilities"]["issue_id"]==fixture["id"]&&permission["capabilities"]["write"]==true,"native creator permission was not currently verified")?;
        product_read(format!("/api/repositories/{repository}/factory/issues/{number}/acceptance-preview?comment_id={comment}")).await?;
        ensure(client.product_read(&grant.repository.id,&Read::IssueCreatorPermission{repository:grant.repository.id.clone(),number,creator:"999999".into()}).await.is_err(),"another creator was admitted to native intake")?;
        println!("PASS: actual enrolled public native snapshots and shared Go acceptance preview bind first-created issue history, exact selected comment/version, dependency occurrence/locator, target ref and current creator permission; another creator is refused, no inference or dispatch");
    }
    println!("PASS: actual enrolled Fountain shared Go product identity, exact canonical repository, two native authenticated issue pages, detail and comments; read enrollment grants no provider writes or execution");
    // Use the host's current code-read enrollment, not a declared local source
    // or a private appliance callback. Setup/funding remain explicit fixtures.
    let source=client.source("",&[],&[]).await.map_err(|e|e.code)?;
    ensure(source.repository==grant.repository.id,"native Fountain source repository changed")?;
    let target=format!("refs/heads/{}",grant.repository.default_branch);
    use soda_desktop_runtime::github_provider::Read;
    use base64::Engine;
    use sha2::{Digest,Sha256};
    let tip=client.product_read(&grant.repository.id,&Read::TargetTip{repository:grant.repository.id.clone(),target_ref:target.clone()}).await.map_err(|e|e.code)?;
    ensure(tip["publication"]["repository"]==grant.repository.id&&tip["publication"]["tip"]==source.commit,"native Fountain target/source binding changed")?;
    let setup=client.product_read(&grant.repository.id,&Read::SetupSource{repository:grant.repository.id.clone(),commit:source.commit.clone(),target_ref:target.clone(),paths:vec!["README.md".into()]}).await.map_err(|e|e.code)?;
    ensure(setup["publication"]["commit"]==source.commit&&setup["publication"]["target_ref"]==target&&setup["publication"]["files"].is_object()&&setup["publication"]["source_files"].is_object(),"native Fountain approved setup source scope changed")?;
    let captured=client.product_read(&grant.repository.id,&Read::SourceBundle{repository:grant.repository.id.clone(),base:String::new(),expected_base:String::new(),commit:source.commit.clone(),target_ref:target.clone(),number:None,head_repository:String::new()}).await.map_err(|e|e.code)?;
    let bytes=base64::engine::general_purpose::STANDARD.decode(captured["publication"]["bundle"].as_str().ok_or("native Fountain source bundle absent")?)?;
    let bundle_digest:String=Sha256::digest(&bytes).iter().map(|b|format!("{b:02x}")).collect();
    ensure(!bytes.is_empty()&&captured["publication"]["sha256"]==bundle_digest&&captured["publication"]["commit"]==source.commit&&captured["publication"]["target"]==target,"native Fountain source digest/head/target changed")?;
    ensure(client.product_read(&grant.repository.id,&Read::SetupSource{repository:grant.repository.id.clone(),commit:"0".repeat(40),target_ref:target,paths:vec![]}).await.is_err(),"native Fountain stale source passed setup selection")?;
    println!("PASS: actual enrolled Fountain target tip, bounded optional setup/source files, exact digest-bound native source bundle and stale source refusal; no preparation authority inferred");
    let bundle=profile.join("fountain-source.bundle");
    let mut capture=fs::OpenOptions::new().create_new(true).write(true).mode(0o600).open(&bundle)?;
    capture.write_all(&source.bundle)?;capture.sync_all()?;drop(capture);
    let source_commit=source.commit.clone();
    fs::write(profile.join("controller-fixture.json"),serde_json::to_vec(&serde_json::json!({"Origin":origin,"Connection":connection,"Actor":grant.actor.id,"Repository":grant.repository.id,"Grant":grant.id}))?)?;
    let command=|operation:&str, argument:&str| {
        let (check,profile,instance,operation,argument,bundle,source_commit)=(check.clone(),profile.to_path_buf(),ready.instance.clone(),operation.to_owned(),argument.to_owned(),bundle.clone(),source_commit.clone());
        async move {
            let status=tokio::task::spawn_blocking(move || Command::new(&check).env("SODA_NATIVE_CHECK_CONTROLLER","1").env("SODA_NATIVE_KEEP_PREPARATION","1").env("SODA_NATIVE_CHECK_BUNDLE",bundle).env("SODA_NATIVE_CHECK_COMMIT",source_commit).arg(profile).arg(instance).arg(operation).arg(argument).status()).await??;
            ensure(status.success(),"connected controller native Go action failed")
        }
    };
    let public_setup=std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_PRODUCT_SETUP").is_some();
    if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_PUBLICATION").is_some() {
        let parent=profile.parent().ok_or("native fixture root missing")?;
        let publication:serde_json::Value=serde_json::from_slice(&fs::read(parent.join("publication-fixture.json"))?)?;
        let issue:serde_json::Value=serde_json::from_slice(&fs::read(parent.join("inputs-fixture.json"))?)?;
        let path=profile.join("publication-inputs.json");
        let mut input=serde_json::json!({"Repository":repository,"Issue":issue["number"].to_string(),"Connection":connection,"Publication":publication});
        if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_MERGE").is_some(){input["Checks"]=serde_json::json!({"method":"fast-forward-only","merge_checks":["actions:Native CI / exact-candidate (pull_request)"]});}
        fs::write(&path,serde_json::to_vec(&input)?)?;
        let operation=if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_PULL").is_some(){"check-background-publication"}else{"check-background-publication-branch"};
        command(operation,path.to_str().ok_or("publication fixture path invalid")?).await?;
        if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_REVIEW").is_some() {
            let state:serde_json::Value=serde_json::from_slice(&fs::read(profile.join("native-publication.json"))?)?;
            let number=state["Review"]["pr_number"].as_u64().ok_or("original review PR number absent")?;
            let list=product_read(format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews?limit=50")).await?;
            let items=list["items"].as_array().ok_or("shared review list absent")?;
            let original=items.iter().find(|item|item["body"]==state["Review"]["body"]).ok_or("shared original review absent")?;
            ensure(original["state"]=="commented"&&original["commit"]==state["Review"]["head_oid"],"shared Go review list changed native COMMENT/body/candidate")?;
            let id=original["external_id"].as_str().ok_or("shared review identity absent")?;
            let detail=product_read(format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews/{id}")).await?;
            ensure(detail["external_id"]==id&&detail["body"]==state["Review"]["body"]&&detail["commit"]==state["Review"]["head_oid"]&&detail["state"]=="commented","shared review detail did not preserve its original native text")?;
            println!("PASS: actual public Fountain review list/detail through shared Go presentation API retain COMMENT, exact candidate, author and verbatim original body");
            if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_MERGE").is_some() {
                let checks=product_read(format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/checks?limit=50")).await?;
                let rows=checks["checks"]["items"].as_array().ok_or("shared native check items absent")?;
                ensure(rows.iter().any(|row|row["name"]=="actions:Native CI / exact-candidate (pull_request)"&&row["conclusion"]=="success"&&row["commit"]==state["Review"]["head_oid"]),"shared native Actions check missing or bound to another candidate")?;
                ensure(checks["status_gate"]=="unknown"&&checks["protection"]["requirements_complete"]==false,"visible CI claimed complete policy authority")?;
                let threads=product_read(format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads?limit=20")).await?;
                let items=threads["items"].as_array().ok_or("shared native threads absent")?;
                ensure(items.len()==1&&items[0]["resolution"]=="unresolved","native thread catalogue lost its original unresolved conversation")?;
                let thread=items[0]["external_id"].as_str().ok_or("native thread identity absent")?;
                let members=product_read(format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/comments?limit=1")).await?;
                let next=members["next"].as_str().filter(|v|!v.is_empty()).ok_or("native member cursor absent")?;
                let next=url::form_urlencoded::Serializer::new(String::new()).append_pair("cursor",next).finish();
                let rest=product_read(format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/comments?limit=1&{next}")).await?;
                ensure(members["items"][0]["comment"]["body"]=="First recorded native observation.\n"&&rest["items"][0]["comment"]["body"]=="Second recorded native observation.\n"&&rest["complete"]==true,"native thread member pagination changed exact recorded text")?;
                println!("PASS: actual shared Go CI/status and nonempty native conversation/member pagination retain exact candidate and verbatim text; unknown policy and unavailable resolution stay explicit");
            }
        }
    }
    let lost_preparation=std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_PREPARATION_LOST").is_some();
    if public_setup {
        product_request("PUT",format!("/api/repositories/{repository}/factory/environment-grant"),serde_json::to_vec(&serde_json::json!({"native":true,"active":true,"expected_revision":0}))?).await?;
        let setup_path=format!("/api/repositories/{repository}/factory/setup");
        let preview=product_read(setup_path.clone()).await?;
        ensure(preview["available"]==true&&preview["source"]==source.commit&&preview["source_inputs"].as_object().is_some_and(|paths|!paths.is_empty()),"actual Fountain setup preview did not bind selected source inputs")?;
        let mut random=[0u8;16];getrandom::fill(&mut random)?;
        let command_id:String=random.iter().map(|b|format!("{b:02x}")).collect();
        let approval=serde_json::json!({"command_id":command_id,"source":preview["source"],"target":preview["target"],"source_inputs":preview["source_inputs"],"requirement":preview["requirement"],"approval":preview["approval"],"environment_revision":preview["environment_revision"],"tools":["git","bash"],
            "files":{"setup.sh":"mkdir -p .factory-build\nprintf 'prepared dependency\\n' > .factory-build/dependency\nprintf 'prepared HOME\\n' > \"$HOME/factory-env\"\nchmod 600 \"$HOME/factory-env\" .factory-build/dependency\n",
            "check.sh":"test \"$(cat .factory-build/dependency)\" = 'prepared dependency'\ntest \"$(cat \"$HOME/factory-env\")\" = 'prepared HOME'\nprintf 'checked-by-native\\n'\n"}});
        product_request("PUT",setup_path.clone(),serde_json::to_vec(&approval)?).await?;
        let until=Instant::now()+Duration::from_secs(35);
        loop {
            let view=product_read(setup_path.clone()).await?;
            let states=view["preparations"].as_array().ok_or("public setup preparations missing")?;
            if states.len()==2&&if lost_preparation { states.iter().filter(|p|p["state"]["phase"]=="interrupted").count()==1&&states.iter().filter(|p|p["state"]["ready"]==true).count()==1 } else {states.iter().all(|p|p["current"]==true&&p["state"]["ready"]==true)} {break;}
            ensure(Instant::now()<until,"public Fountain role preparation did not become ready")?;
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
        // Exact approval recovery returns its original receipt; it cannot run
        // either role's setup again or allocate another host claim.
        product_request("PUT",setup_path,serde_json::to_vec(&approval)?).await?;
        if !lost_preparation { println!("PASS: actual enrolled Fountain public product workspace consent, exact source/recipe approval, two host-claimed privileged role preparations and exact approval replay without another constructor"); }
    } else {
        command("check-native-preparation","ready").await?;
    }
    let preparations=fs::read_dir(profile.join("native-preparations"))?.collect::<std::result::Result<Vec<_>,_>>()?;
    ensure(preparations.len()==if public_setup{2}else{1},"controller preparation missing or duplicated")?;
    let preparation=preparations.iter().find(|entry| if lost_preparation {entry.path().join("controller-not-started.json").is_file()} else {fs::read(entry.path().join("request.json")).ok().and_then(|raw|serde_json::from_slice::<serde_json::Value>(&raw).ok()).is_some_and(|request|request["input"]["preparation"]["role"]=="soda-coder")}).ok_or("selected preparation absent")?;
    let prepared:serde_json::Value=serde_json::from_slice(&fs::read(preparation.path().join("request.json"))?)?;
    let fixture=profile.join("admitted-native-fixture.json");
    let withdraw = std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_WITHDRAW").is_some();
    let mut worker_claim=None;
    if lost_preparation {
        fs::write(&fixture,serde_json::to_vec(&serde_json::json!({"Preparation":prepared["input"]["preparation"]["id"]}))?)?;
        let no_workers=match fs::read_dir(profile.join("native-run-intents")) {
            Ok(mut entries)=>entries.next().is_none(),
            Err(error) if error.kind()==std::io::ErrorKind::NotFound=>true,
            Err(error)=>return Err(error.into()),
        };
        ensure(no_workers,"lost preparation reply dispatched a worker")?;
    } else {
    let mut random=[0u8;16];getrandom::fill(&mut random)?;
    let run:String=random.iter().map(|b|format!("{b:02x}")).collect();
    fs::write(&fixture,serde_json::to_vec(&serde_json::json!({"Run":run,"MuseLogin":true,"Withdraw":withdraw,"Preparation":prepared["input"]["preparation"]["id"],"Executable":finite}))?)?;
    command("launch-admitted-native",fixture.to_str().ok_or("fixture path invalid")?).await?;
    let intent=LaunchIntent::from_go(&fs::read_to_string(profile.join("native-run-intents").join(format!("{run}.json")))?)?;
    if withdraw {
        let until=Instant::now()+Duration::from_secs(12);
        loop {
            let services=profile.join("native-muse-runs");
            if services.is_dir() && fs::read_dir(&services)?.any(|entry| entry.is_ok_and(|entry| entry.path().join("work/native-fixture-ready").is_file())) {break;}
            ensure(Instant::now()<until,"controlled worker did not reach actual MCP artifact publication")?;
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        let placement=client.placement().await.map_err(|e|e.code)?;
        let mut random=[0u8;16];getrandom::fill(&mut random)?;
        let command_id=random.iter().map(|b|format!("{b:02x}")).collect();
        let request=soda_desktop_runtime::fountain_provider::Selection{command_id,expected_revision:placement.revision,action:"withdraw".into(),not_after:epoch() as i64+120};
        let withdrawn=client.select_runtime(profile,&request).await.map_err(|e|e.code)?;
        ensure(withdrawn.placement.state=="retiring","actual worker controller withdrawal failed")?;
    }
    let until=Instant::now()+Duration::from_secs(65);
    loop {
        match supervisor.execution_outcome(&intent)? {
            ExecutionOutcome::Pending if Instant::now()<until => tokio::time::sleep(Duration::from_millis(20)).await,
            ExecutionOutcome::MuseComplete(result) if result.root_retired && result.process.output_complete &&
                (if withdraw {result.process.reason==Some(soda_execution_protocol::StopReason::Cancelled) && result.process.tree_retired} else {result.process.exit_code==Some(0)}) => break,
            _ => return Err("host-controlled native worker did not retire successfully".into()),
        }
    }
    if withdraw {
        command("observe-admitted-native",fixture.to_str().ok_or("fixture path invalid")?).await?;
        command("reconcile-retained-native",fixture.to_str().ok_or("fixture path invalid")?).await?;
    } else {
        command("check-admitted-native",fixture.to_str().ok_or("fixture path invalid")?).await?;
        // The shell's artifact path: exact file bytes and media type, not JSON,
        // including a binary reply above the ordinary 2 MiB response ceiling.
        let published:serde_json::Value=serde_json::from_slice(&fs::read(profile.join("native-worker-artifacts.json"))?)?;
        let large:Vec<u8>=(0..2*1024*1024+4099usize).map(|i|((i*31+i/251)&0xff) as u8).collect();
        for (field,media,expected) in [("text","text/plain",b"actual admitted native worker artifact\n".to_vec()),("binary","application/octet-stream",large)] {
            let id=published[field].as_str().filter(|id|id.len()==32).ok_or("published artifact identity absent")?;
            let path=format!("/api/factory/artifacts/{id}?repository={repository}");
            ensure(soda_desktop_runtime::product_artifact_read("GET",&path),"artifact read was not recognized as a byte reply")?;
            let request=supervisor.admit_fountain_product(credentials.clone(),credentials.capability(connection).await?,grant.repository.id.clone(),client.clone())?;
            let reply=request.request("GET",&path,&[]).await?;
            ensure(reply.status==200&&reply.content_type.as_deref()==Some(media)&&reply.body==expected,&format!("native artifact read changed {field} status, media type or bytes"))?;
        }
        println!("PASS: native custody reads the worker text artifact and a {}-byte binary artifact through the shared Go route with exact status, media type and bytes",2*1024*1024+4099);
        command("acknowledge-native",fixture.to_str().ok_or("fixture path invalid")?).await?;
        supervisor.start_execution_coordinator().await?;
        command("settled-admitted-native",fixture.to_str().ok_or("fixture path invalid")?).await?;
    }
    let wire:serde_json::Value=serde_json::from_str(intent.raw())?;
    let admitted:serde_json::Value=serde_json::from_slice(&fs::read(&fixture)?)?;
    worker_claim=Some(soda_desktop_runtime::fountain_provider::ExecutionClaimRequest {
        command_id:run,
        manifest_digest:wire["controller"]["manifest_digest"].as_str().ok_or("controller capture missing")?.into(),
        not_after:admitted["DeadlineUnix"].as_i64().ok_or("worker deadline missing")?.min(grant.not_after),
    });
    }
    if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_MERGE").is_some(){
        command("check-background-publication-merge",profile.join("publication-inputs.json").to_str().ok_or("publication input path invalid")?).await?;
    }
    tokio::task::spawn_blocking(move||supervisor.stop()).await?.map_err(|error|format!("native controller stop: {error}"))?;
    let recovered_credentials=Arc::new(Credentials::open(profile,&ready.profile)?);
    let (history,original_grant)=soda_desktop_runtime::fountain_provider::captured_operation_grant(recovered_credentials.clone(),profile,&ready.profile,connection,&grant.id).await.map_err(|e|e.code)?;
    for preparation in &preparations {
    let prepared:serde_json::Value=serde_json::from_slice(&fs::read(preparation.path().join("request.json"))?)?;
    let preparation_claim=soda_desktop_runtime::fountain_provider::ExecutionClaimRequest{
        command_id:preparation.file_name().into_string().map_err(|_|"preparation key invalid")?,manifest_digest:Sha256::digest(serde_json::to_vec(&prepared)?).iter().map(|b|format!("{b:02x}")).collect(),
        not_after:prepared["controller"]["not_after"].as_i64().ok_or("preparation grant deadline absent")?};
    let retired=history.execution_claim(&original_grant,&preparation_claim).await.map_err(|e|e.code)?;
    ensure(retired.settled().is_some_and(|(state,digest)|state==if preparation.path().join("controller-not-started.json").is_file(){"not_started"}else{"retired"}&&digest.len()==64),"physical preparation retirement did not settle its original host claim")?;
    }
    let reopened=NativeSupervisor::start(Path::new(&payload),Path::new(&backend),profile)?;
    let reopened_ready=reopened.health()?;
    ensure(reopened_ready.instance!=ready.instance,"preparation recovery reused the retired native instance")?;
    reopened.enroll_credentials(recovered_credentials.clone())?;
    let check_status=tokio::task::spawn_blocking({let profile=profile.to_path_buf();let check=check.clone();move||Command::new(check).arg(profile).arg(reopened_ready.instance).arg("check-native-preparation").arg("retained").status()}).await??;
    ensure(check_status.success(),"retained preparation recovery failed")?;
    tokio::task::spawn_blocking(move||reopened.stop()).await?.map_err(|error|format!("reopened native controller stop: {error}"))?;
    recovered_credentials.retire();
    println!("PASS: actual privileged preparation binds its original complete request to one host claim; normal shutdown confirms physical and host retirement, reopen adopts that receipt and refuses the old constructor without replay");
    if lost_preparation {println!("PASS: lost original preparation claim reply admits no constructor or worker; exact approval replay sends no second claim, definitive no-start settles the original host journal and reopen preserves the refusal");}
    else {
        if withdraw {println!("PASS: actual central placement withdrawal cancels the live native worker after MCP artifact publication; exact process/root retirement and original-source accounting retained without replay");}
        println!("PASS: actual Fountain code-read enrollment/source bundle surrounds native preparation, original host claim, Go admission, finite local worker/MCP and physical retirement; no inference or full delivery claimed");
    }
    Ok(worker_claim)
}

async fn fountain_controller(profile: &Path, origin: &str, application: &str) -> Result<()> {
    use soda_desktop_runtime::{fountain_oauth, fountain_provider::{Client,Selection,OperationAuthorization,ExecutionClaimRequest,ExecutionRetirement},mcp_signing::Signer};
    prepare(profile)?;
    let lock=fs::OpenOptions::new().create_new(true).write(true).open(profile.join("fixture.lock"))?;
    ensure(unsafe{libc::flock(lock.as_raw_fd(),libc::LOCK_EX|libc::LOCK_NB)}==0,"controller profile lock failed")?;
    let runtime="123456789abcdef0123456789abcdef0";
    let mut enrollment=String::new();
    let mut store=Arc::new(Credentials::open(profile,runtime)?);
    let mut token=Vec::new();std::io::stdin().take(65537).read_to_end(&mut token)?;
    ensure(!token.is_empty()&&token.len()<=65536,"bounded fixture OAuth token required")?;
    let mut parts=token.splitn(2,|b|*b==b'\n');
    let read_token=parts.next().ok_or("read token missing")?.to_vec();
    let control_token=parts.next().ok_or("control token missing")?.to_vec();token.fill(0);
    let read_oauth=fountain_oauth::Client::configured(origin).await?;
    let read_saved=store.store_grant(Account::new(Provider::Fountain,origin,"2")?,Grant{access:Token::new(read_token)?,refresh:None,expires_at:None,refresh_expires_at:None,scope:"read:repository".into(),client_id:Some(read_oauth.application().into())}).await?;
    let original=store.capability(&read_saved.connection).await?;
    let saved=store.store_grant(Account::new(Provider::Fountain,origin,"2")?,Grant{access:Token::new(control_token)?,refresh:None,expires_at:None,refresh_expires_at:None,scope:"write:repository write:issue".into(),client_id:Some(application.into())}).await?;
    ensure(saved.connection!=read_saved.connection,"control sign-in replaced the original read connection")?;
    store.with_token(&original,|bytes|!bytes.is_empty()).await?;
    let mut reconnected_connection=None;
    let result=async {
        let consent=fountain_oauth::Client::configured_control(origin).await?;
        ensure(consent.application()==application,"configured control application changed")?;
        let pending=consent.begin().await?;
        let url=url::Url::parse(&pending.prompt().authorization_uri)?;
        ensure(url.query_pairs().any(|(key,value)|key=="scope"&&value=="write:repository write:issue"),"explicit native control scope missing")?;
        drop(pending); // This check uses the actual host-exchanged fixture token.
        let signer=Signer::open(profile)?;
        let client=soda_desktop_runtime::fountain_provider::retained_client(store.clone(),store.capability(&saved.connection).await?,profile,runtime,"user2","repo1").await.map_err(|e|e.code)?;
        let enrolled=client.enroll().await.map_err(|e|e.code)?;
        ensure(enrolled.active,"control enrollment missing")?;
        enrollment=enrolled.id;
        let initial=client.placement().await.map_err(|e|e.code)?;
        ensure(initial.state=="inactive","fixture controller was not retired")?;
        let request=Selection{command_id:"123456789abcdef0123456789abcdef3".into(),expected_revision:initial.revision,action:"activate".into(),not_after:epoch() as i64+120};
        ensure(matches!(client.select_runtime(profile,&request).await,Err(error)if error.status==503),"lost initial controller reply was not exercised")?;
        drop(client);drop(signer);store.retire();
        store=Arc::new(Credentials::open(profile,runtime)?);
        let signer=Signer::open(profile)?;
        let client=Arc::new(Client::new(store.clone(),store.capability(&saved.connection).await?,&signer,runtime,application,"user2","repo1",&enrollment).map_err(|e|e.code)?);
        let receipt=client.select_runtime(profile,&request).await.map_err(|e|e.code)?;
        ensure(receipt.placement.state=="active","reopened controller command not adopted")?;
        let authorization=OperationAuthorization{command_id:"123456789abcdef0123456789abcdef4".into(),expected_placement:receipt.placement.revision.clone(),generation:receipt.placement.generation.clone(),operations:vec!["issue.comment.publish".into(),"execution.launch".into()],not_after:epoch() as i64+if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_MERGE").is_some(){600}else{90}};
        let grant=client.authorize_operations(profile,&authorization).await.map_err(|e|e.code)?;
        ensure(grant.active&&grant.allows_current("execution.launch")&&grant.allows_current("issue.comment.publish")&&!grant.allows_current("git.ref.publish"),"native per-operation authority missing or enlarged")?;
        let claim_request=ExecutionClaimRequest{command_id:"123456789abcdef0123456789abcdef8".into(),manifest_digest:"8".repeat(64),not_after:grant.not_after};
        let claimed=client.claim_execution(profile,&grant,&claim_request).await.map_err(|e|e.code)?;
        ensure(claimed.acquired(),"first native claim did not acquire exactly one dispatch")?;
        let observed=client.claim_execution(profile,&grant,&claim_request).await.map_err(|e|e.code)?;
        ensure(!observed.acquired()&&observed.manifest_digest==claimed.manifest_digest,"claim replay acquired another launch")?;
        let mut changed_claim=claim_request.clone();changed_claim.manifest_digest="9".repeat(64);
        ensure(matches!(client.claim_execution(profile,&grant,&changed_claim).await,Err(e)if e.status==400),"changed native claim capture was accepted")?;
        let no_start=ExecutionRetirement{manifest_digest:claim_request.manifest_digest.clone(),state:"not_started".into(),receipt_digest:"a".repeat(64)};
        ensure(client.retire_execution(profile,&grant,&claim_request,&no_start).await.map_err(|e|e.code)?.settled().is_some_and(|(state,_)|state=="not_started"),"known fixture no-start did not retire claim")?;
        let lost_claim=ExecutionClaimRequest{command_id:"123456789abcdef0123456789abcdef9".into(),manifest_digest:"9".repeat(64),not_after:grant.not_after};
        let claim_worker=client.clone();let claim_profile=profile.to_path_buf();let captured_grant=grant.clone();let captured_request=lost_claim.clone();
        let first_claim=tokio::spawn(async move{claim_worker.claim_execution(&claim_profile,&captured_grant,&captured_request).await});
        let root=profile.parent().ok_or("native root missing")?;
        let until=Instant::now()+Duration::from_secs(3);
        while !root.join("claim-request-paused").is_file()&&Instant::now()<until{tokio::time::sleep(Duration::from_millis(10)).await;}
        let claim_paused=root.join("claim-request-paused").is_file();
        let concurrent=client.claim_execution(profile,&grant,&lost_claim).await;
        let release=fs::write(root.join("claim-request-release"),b"release original claim");
        let original_claim=first_claim.await?;release?;
        ensure(claim_paused&&matches!(concurrent,Err(e)if e.status==404),"lookup absence did not fence concurrent claim")?;
        ensure(!original_claim.map_err(|e|e.code)?.acquired(),"lost reserve reply authorized a launch from lookup")?;
        drop(client);drop(signer);store.retire();store=Arc::new(Credentials::open(profile,runtime)?);
        let signer=Signer::open(profile)?;
        ensure(soda_desktop_runtime::fountain_provider::captured_operation_grant(store.clone(),profile,runtime,&"0".repeat(32),&grant.id).await.is_err(),"another native connection adopted the captured controller")?;
        let (client,captured_grant)=soda_desktop_runtime::fountain_provider::captured_operation_grant(store.clone(),profile,runtime,&saved.connection,&grant.id).await.map_err(|e|e.code)?;
        let client=Arc::new(client);
        ensure(captured_grant.id==grant.id&&captured_grant.not_after==grant.not_after&&captured_grant.runtime_id==grant.runtime_id,"native reopen changed the captured controller scope")?;
        if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_PAYLOAD").is_some() {
            if let Some(worker)=controller_worker(profile,store.clone(),client.clone(),origin,&saved.connection,&grant).await? {
            let retired=client.execution_claim(&grant,&worker).await.map_err(|e|e.code)?;
            ensure(retired.settled().is_some_and(|(state,digest)|state=="retired"&&digest.len()==64),"native physical retirement did not settle the exact host claim")?;
            }
        }
        ensure(!client.claim_execution(profile,&grant,&lost_claim).await.map_err(|e|e.code)?.acquired(),"reopen acquired an old dispatch")?;
        let lost_no_start=ExecutionRetirement{manifest_digest:lost_claim.manifest_digest.clone(),state:"not_started".into(),receipt_digest:"b".repeat(64)};
        client.retire_execution(profile,&grant,&lost_claim,&lost_no_start).await.map_err(|e|e.code)?;
        ensure(!client.retire_execution(profile,&grant,&lost_claim,&lost_no_start).await.map_err(|e|e.code)?.acquired(),"retirement replay acquired dispatch")?;
        let placement=client.placement().await.map_err(|e|e.code)?;
        let placement=if placement.state=="retiring" && std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_WITHDRAW").is_some() {placement} else {
            let withdrawal=Selection{command_id:"123456789abcdef0123456789abcdef5".into(),expected_revision:placement.revision,action:"withdraw".into(),not_after:epoch() as i64+120};
            client.select_runtime(profile,&withdrawal).await.map_err(|e|e.code)?.placement
        };
        ensure(!client.authorize_operations(profile,&authorization).await.map_err(|e|e.code)?.active,"old grant replay renewed withdrawal")?;
        let retirement=Selection{command_id:"123456789abcdef0123456789abcdef6".into(),expected_revision:placement.revision,action:"retire".into(),not_after:epoch() as i64+120};
        ensure(client.select_runtime(profile,&retirement).await.map_err(|e|e.code)?.placement.state=="inactive","native controller did not retire")?;
        if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_PUBLICATION").is_some() {
            let entries=fs::read_dir(profile.join("fountain-operations"))?;
            let mut recovered=0;
            for entry in entries {
                let entry=entry?;
                if !entry.file_name().to_string_lossy().ends_with(".publication.json"){continue;}
                let capture:serde_json::Value=serde_json::from_slice(&fs::read(entry.path())?)?;
                let operation=capture["command"].as_str().ok_or("publication command absent")?;
                let digest=capture["digest"].as_str().ok_or("publication digest absent")?;
                let deadline=capture["not_after"].as_u64().ok_or("publication deadline absent")?;
                let before=fs::read(entry.path())?;
                match client.recover_publication(profile,operation,digest,deadline).await.map_err(|e|e.code)? {
                    Some(soda_desktop_runtime::fountain_provider::PublicationOutcome::Ref(soda_desktop_runtime::github_provider::WriteOutcome::Applied(receipt)))=>ensure(receipt["new"]==capture["branch"]["candidate"]&&receipt["host_operation"]["grant_id"]==capture["branch"]["controller_grant_id"],"withdrawn publication journal changed its original receipt")?,
                    Some(soda_desktop_runtime::fountain_provider::PublicationOutcome::Pull(soda_desktop_runtime::github_provider::WriteOutcome::Applied(receipt)))=>ensure(receipt["head"]["pin"]==capture["pull"]["head_pin"]&&receipt["base"]["pin"]==capture["pull"]["base_pin"]&&receipt["host_operation"]["grant_id"]==capture["pull"]["controller_grant_id"],"withdrawn PR journal changed its exact creation pins")?,
                    Some(soda_desktop_runtime::fountain_provider::PublicationOutcome::Review(soda_desktop_runtime::github_provider::WriteOutcome::Applied(receipt)))=>ensure(receipt["head"]==capture["review"]["head"]&&receipt["base"]==capture["review"]["base"]&&receipt["review"]["state"]=="commented"&&receipt["host_operation"]["grant_id"]==capture["review"]["controller_grant_id"],"withdrawn review changed its original candidate or vote")?,
                    Some(soda_desktop_runtime::fountain_provider::PublicationOutcome::Merge(soda_desktop_runtime::github_provider::WriteOutcome::Applied(receipt)))=>ensure(receipt["commit"]==capture["merge"]["head"]&&receipt["base"]==capture["merge"]["base"]&&receipt["host_operation"]["grant_id"]==capture["merge"]["controller_grant_id"],"withdrawn merge changed its original candidate/result or base")?,
                    _=>return Err("withdrawn publication did not recover its original committed receipt".into())
                }
                ensure(fs::read(entry.path())?==before,"publication recovery renewed original authority")?;
                recovered+=1;
            }
            ensure(recovered==if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_MERGE").is_some(){4}else if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_REVIEW").is_some(){3}else if std::env::var_os("FACTORY_FOUNTAIN_CONTROLLER_PULL").is_some(){2}else{1},"original publication capture missing or duplicated")?;
            println!("PASS: native restart and central withdrawal preserve the exact original conditional publication host receipt and captured deadline without registration or receive replay");
        }
        let replacement=store.with_token(&store.capability(&saved.connection).await?,|bytes|Token::new(bytes.to_vec())).await??;
        store.withdraw(&saved.connection).await?;
        let rebound=store.store_grant(Account::new(Provider::Fountain,origin,"2")?,Grant{access:replacement,refresh:None,expires_at:None,refresh_expires_at:None,scope:"write:repository write:issue".into(),client_id:Some(application.into())}).await?;
        reconnected_connection=Some(rebound.connection.clone());
        ensure(rebound.connection!=saved.connection,"withdrawn source connection was reused")?;
        let rebound_client=Client::new(store.clone(),store.capability(&rebound.connection).await?,&signer,runtime,application,"user2","repo1",&enrollment).map_err(|e|e.code)?;
        let original_grant=rebound_client.operation_grant(&grant.id).await.map_err(|e|e.code)?;
        ensure(!original_grant.active,"retired controller grant regained authority")?;
        let old_claim=rebound_client.execution_claim(&original_grant,&lost_claim).await.map_err(|e|e.code)?;
        ensure(old_claim.settled().is_some_and(|(_,digest)|digest==lost_no_start.receipt_digest),"reconnected lookup changed claim retirement or authority")?;
        Ok::<_,Box<dyn StdError>>(())
    }.await;
    if let Some(connection)=reconnected_connection {store.withdraw(&connection).await?;}
    let withdrawn=store.withdraw(&saved.connection).await;
    let original_preserved=store.with_token(&store.capability(&read_saved.connection).await?,|bytes|!bytes.is_empty()).await?;
    let read_withdrawn=store.withdraw(&read_saved.connection).await;
    store.retire();result?;withdrawn?;read_withdrawn?;ensure(original_preserved,"read credential was not preserved")?;
    println!("PASS: actual TLS control discovery and explicit consent URL, native-held fixture OAuth/profile proof, saved-before-send command, lost initial reply/reopen/exact replay, finite grant, original claim lookup after lost reply and reopen, controller withdrawal/retirement and reconnected read-only claim history; connector-only unless finite-worker evidence is printed above; no paid inference");
    Ok(())
}

async fn fountain_login(profile:&Path,origin:&str,application:&str,backend:&Path,payload:&Path)->Result<()> {
    use soda_desktop_runtime::{fountain_oauth, fountain_provider, supervisor::NativeSupervisor};
    prepare(profile)?;
    let runtime=NativeSupervisor::start(payload,backend,profile)?;
    let ready=runtime.health()?;
    let store=Arc::new(Credentials::open(profile,&ready.profile)?);
    let oauth=fountain_oauth::Client::configured(origin).await?;
    ensure(oauth.application()==application,"configured public application differs")?;
    // Migrate the actual old Secret Service formats in this disposable profile.
    // Missing historical client metadata never implies permission to refresh.
    let legacy_bytes=fixture()?;
    let legacy_account=Account::new(Provider::Fountain,origin,"2")?;
    let raw=store.store(legacy_account.clone(),Token::new(legacy_bytes.clone())?).await?;
    let raw_cap=store.capability(&raw.connection).await?;
    ensure(matches!(oauth.refresh(&store,&raw_cap).await,Err(fountain_oauth::Error::Reauthorize)),"legacy raw token invented OAuth refresh authority")?;
    let legacy=store.store_grant(legacy_account,grant(&legacy_bytes,&legacy_bytes,120,180)?).await?;
    let legacy_cap=store.capability(&legacy.connection).await?;
    ensure(store.with_token(&legacy_cap,|bytes|bytes==legacy_bytes).await?,"legacy v1 grant lost access")?;
    ensure(matches!(oauth.refresh(&store,&legacy_cap).await,Err(fountain_oauth::Error::Reauthorize)),"legacy v1 grant inferred a public client")?;
    let authorization=oauth.clone().begin().await?;
    fs::write(profile.parent().ok_or("parent missing")?.join("fountain-authorize-uri"),authorization.prompt().authorization_uri.as_bytes())?;
    let(account,grant)=authorization.wait().await?;
    let saved=store.store_grant(account,grant).await?;
    ensure(saved.connection==raw.connection&&saved.connection==legacy.connection,"explicit sign-in changed the legacy connection identity")?;
    ensure(matches!(store.with_token(&raw_cap,|_|()).await,Err(Error::Retired))&&matches!(store.with_token(&legacy_cap,|_|()).await,Err(Error::Retired)),"old credential revision survived migration")?;
    ensure(saved.provider==Provider::Fountain&&saved.account=="2"&&saved.grant.as_ref().and_then(|g|g.client_id.as_deref())==Some(application),"real verified Fountain identity/client capture missing")?;
    let result=async {
        let client=fountain_provider::retained_client(store.clone(),store.capability(&saved.connection).await?,profile,&ready.profile,"user2","repo1").await.map_err(|e|e.code)?;
        let enrolled=if std::env::var("SODA_FOUNTAIN_LOST_ENROLLMENT").as_deref()==Ok("1") {
            ensure(client.enroll().await.is_err(),"fixture did not lose the original enrollment reply")?;
            drop(client);
            let recovered=fountain_provider::retained_client(store.clone(),store.capability(&saved.connection).await?,profile,&ready.profile,"user2","repo1").await.map_err(|e|e.code)?;
            // Lookup the retained command before any repeated write.
            recovered.status().await.map_err(|e|e.code)?
        } else {client.enroll().await.map_err(|e|e.code)?};
        let client=fountain_provider::retained_client(store.clone(),store.capability(&saved.connection).await?,profile,&ready.profile,"user2","repo1").await.map_err(|e|e.code)?;
        ensure(enrolled.active&&enrolled.runtime_bound&&enrolled.runtime_id==ready.profile,"real profile proof missing")?;
        ensure(client.product_read(&enrolled.repository.id,&soda_desktop_runtime::provider_contract::Read::Repository{repository:enrolled.repository.id.clone()}).await.map_err(|e|e.code)?["repository"]["external_id"]==enrolled.repository.id,"real enrolled repository mismatch")?;
        drop(client);
        let cap=store.capability(&saved.connection).await?;
        if std::env::var("SODA_FOUNTAIN_REFRESH_RACE").as_deref()==Ok("1") {
            let expiry=saved.grant.as_ref().and_then(|g|g.expires_at).ok_or("expiry missing")?;
            while std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH)?.as_secs()<expiry {tokio::time::sleep(Duration::from_millis(25)).await;}
            ensure(matches!(store.with_token(&cap,|_|()).await,Err(Error::Expired)),"expired provider access remained usable")?;
        }
        let refreshed=oauth.refresh(&store,&cap).await?;
        ensure(refreshed.connection==saved.connection&&refreshed.grant.as_ref().and_then(|g|g.client_id.as_deref())==Some(application),"provider refresh rebound client or account")?;
        ensure(matches!(store.with_token(&cap,|_|()).await,Err(Error::Retired)),"old refresh revision retained access")?;
        let reopened=Arc::new(Credentials::open(profile,&ready.profile)?);
        let status=reopened.status().await?;
        ensure(status.len()==1&&status[0].grant.as_ref().and_then(|g|g.client_id.as_deref())==Some(application),"reopened v2 client binding missing")?;
        let client=fountain_provider::retained_client(reopened.clone(),reopened.capability(&saved.connection).await?,profile,&ready.profile,"user2","repo1").await.map_err(|e|e.code)?;
        ensure(client.enroll().await.map_err(|e|e.code)?.id==enrolled.id,"refresh/reopen lost original enrollment command")?;
        ensure(!client.withdraw().await.map_err(|e|e.code)?.active,"enrollment withdrawal failed")?;
        ensure(!client.enroll().await.map_err(|e|e.code)?.active,"withdrawn enrollment reactivated")?;
        ensure(matches!(client.product_read(&enrolled.repository.id,&soda_desktop_runtime::provider_contract::Read::Repository{repository:enrolled.repository.id.clone()}).await,Err(e)if matches!(e.status,400|403)),"withdrawn read admitted")?;
        if std::env::var("SODA_FOUNTAIN_REFRESH_RACE").as_deref()==Ok("1") {
            let cap=reopened.capability(&saved.connection).await?;
            let worker=reopened.clone(); let oauth=oauth.clone();
            let task=tokio::spawn(async move {oauth.refresh(&worker,&cap).await});
            let root=profile.parent().ok_or("parent missing")?;
            let deadline=Instant::now()+Duration::from_secs(5);
            while !root.join("refresh-entered").is_file() {
                ensure(Instant::now()<deadline,"actual refresh request did not enter host")?;
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
            let cap=reopened.capability(&saved.connection).await?;
            ensure(matches!(reopened.with_token(&cap,|_|()).await,Err(Error::RefreshPending)),"in-flight refresh allowed ordinary access")?;
            reopened.withdraw(&saved.connection).await?;
            fs::write(root.join("refresh-release"),b"release")?;
            ensure(matches!(task.await?,Err(fountain_oauth::Error::Credential(Error::Withdrawn))),"actual host reply resurrected withdrawn credential")?;
            ensure(reopened.status().await?.is_empty(),"withdrawn refresh published a credential")?;
        }
        Ok::<_,Box<dyn StdError>>(())
    }.await;
    let withdrawal=store.withdraw(&saved.connection).await;
    store.retire();
    let stopped=runtime.stop();
    result?;withdrawal?;stopped?;
    println!("PASS: actual configured-host PKCE/code exchange, verified account, client-bound encrypted grant, native profile enrollment, provider refresh, old-revision refusal, reopen and irreversible enrollment withdrawal; owned login withdrawn and runtime joined");
    Ok(())
}

async fn run_selected(args: &[std::ffi::OsString]) -> Result<()> {
    if args.first().and_then(|arg| arg.to_str()) == Some("--typesafe") {
        ensure(args.len() == 2, "TypeSafe custody requires a new profile")?;
        return native_typesafe::run(Path::new(&args[1])).await;
    }
    if args.first().and_then(|arg|arg.to_str()) == Some("--fountain-controller-declared") {
        ensure(args.len()==4,"Fountain controller requires new profile, TLS origin and control application")?;
        return fountain_controller(Path::new(&args[1]),args[2].to_str().ok_or("origin missing")?,args[3].to_str().ok_or("application missing")?).await;
    }
    if args.first().and_then(|arg|arg.to_str()).is_some_and(|v|matches!(v,"--github-write-retention-connected"|"--github-nosend-retention-connected")) {
        ensure(args.len()==5,"retention requires backend, PostgreSQL payload, new profile and exact fixture commit")?;
        return native_github_pull_branches::connected_retention(Path::new(&args[1]),Path::new(&args[2]),Path::new(&args[3]),args[4].to_str().ok_or("commit missing")?,args[0]=="--github-nosend-retention-connected").await;
    }
    if args.first().and_then(|arg|arg.to_str()) == Some("--fountain-desktop-login") {
        ensure(args.len()==6,"Fountain login requires new profile, configured TLS origin, application ID, backend and PostgreSQL payload")?;
        return fountain_login(Path::new(&args[1]),args[2].to_str().ok_or("origin missing")?,args[3].to_str().ok_or("application missing")?,Path::new(&args[4]),Path::new(&args[5])).await;
    }
    if args.first().and_then(|arg|arg.to_str()) == Some("--fountain-desktop-declared") {
        ensure(args.len() == 4, "Fountain mode requires new profile, TLS origin and public application ID")?;
        return fountain_enrollment(Path::new(&args[1]), args[2].to_str().ok_or("origin invalid")?, args[3].to_str().ok_or("application invalid")?).await;
    }
    match (args.first().and_then(|arg| arg.to_str()), args.len()) {
        (Some("--github-assembly-budget-declared"), 4) => {
            native_github_transport::run_assembly_budget_declared(Path::new(&args[1]), Path::new(&args[2]), Path::new(&args[3])).await
        }
        (Some("--native-publication-history"),6) => {
            let runtime=soda_desktop_runtime::supervisor::NativeSupervisor::start(Path::new(&args[2]),Path::new(&args[1]),Path::new(&args[3]))?;
            let instance=runtime.health()?.instance;
            let status=tokio::process::Command::new(&args[4]).arg(&args[3]).arg(&instance).arg("check-background-publication-retained").arg(&args[5]).status().await?;
            runtime.stop()?;
            ensure(status.success(),"retained native publication history/upgrade check failed")
        }
        (Some("--github-automation-acceptance"), n) if (8..=10).contains(&n) => {
            use serde_json::json;
            use soda_desktop_runtime::supervisor::NativeSupervisor;
            let field = |n: usize| {
                args[n]
                    .to_str()
                    .ok_or("invalid acceptance fixture argument")
            };
            let profile = Path::new(&args[3]);
            let resume_merge=args.len()==10 && field(9)?=="merge-resume";
            if resume_merge {
                ensure(profile.starts_with("/home") && profile.join("profile.id").is_file(),"retained private merge profile required")?;
            } else { prepare(profile)?; }
            let mut secret = Vec::new();
            std::io::stdin().take(65537).read_to_end(&mut secret)?;
            if secret.last() == Some(&b'\n') {
                secret.pop();
            }
            let device = secret == b"device";
            let token = if device { None } else { Some(Token::new(secret)?) };
            let production = Path::new(&args[1]);
            let payload = Path::new(&args[2]);
            let mut runtime = Some(NativeSupervisor::start(payload, production, profile)?);
            let ready = runtime.as_ref().expect("owned runtime").health()?;
            let store = Arc::new(Credentials::open(profile, &ready.profile)?);
            ensure(
                store.status().await?.is_empty(),
                "acceptance fixture keyring profile is not empty",
            )?;
            let saved = if device {
                let authorization = runtime.as_ref().expect("owned runtime").github_device_start().await?;
                println!("DEVICE: {} {}", authorization.prompt().verification_uri, authorization.prompt().user_code);
                let grant = authorization.wait().await?;
                let (account, grant) = runtime.as_ref().expect("owned runtime").github_verify_grant(grant).await?;
                store.store_grant(account, grant).await?
            } else { store.store(
                    Account::new(Provider::Github, "https://github.com", field(4)?)?,
                    token.expect("supplied token"),
                )
                .await? };
            let outcome: Result<()>=async {
                ensure(saved.account == field(4)?, "connected account differs from authorized fixture")?;
                let request=runtime.as_ref().expect("owned runtime").admit_product(store.clone(),store.capability(&saved.connection).await?,Some(field(5)?.into()))?;
                let reply=request.request("GET","/api/factory/collaboration/repositories?limit=1",&[]).await?;
                ensure(reply.status==200,"native repository catalogue unavailable")?;
                let catalog:serde_json::Value=serde_json::from_slice(&reply.body)?;
                let repository=catalog["items"][0]["id"].as_str().ok_or("canonical repository missing")?;
                if args.len()==10 && field(9)?=="merge-permission" {
                    use soda_desktop_runtime::github_provider::{Owner, Read, PullMerge, WriteOutcome};
                    let root=profile.parent().ok_or("profile parent missing")?;
                    let case:serde_json::Value=serde_json::from_slice(&fs::read(&args[8])?)?;
                    let number=case["number"].as_u64().ok_or("permission PR missing")? as u32;
                    let mut owner=tokio::task::spawn_blocking(Owner::start).await??;
                    let client=owner.client();
                    let capability=store.capability(&saved.connection).await?;
                    let observed=store.with_token_operation(&capability,async |bytes| {
                        client.observe(Token::new(bytes.to_vec()).expect("owned token"),field(4).expect("actor"),&Read::MergePreparation{repository:field(5).expect("repository").into(),number}).await
                    }).await??;
                    let facts=&observed["preparation"];
                    ensure(facts["push"]==true && facts["state"]=="open" && facts["methods"]["merge"]==true,"writer preparation unavailable")?;
                    let merge=PullMerge{host_revision:None,checks:Vec::new(),controller_grant_id:String::new(),source_connection:String::new(),head_ref:String::new(),base_ref:String::new(),repository:field(5)?.into(),repository_node_id:facts["repository_node_id"].as_str().ok_or("repository node missing")?.into(),pull_id:facts["pull_id"].as_str().ok_or("pull missing")?.into(),pull_node_id:facts["pull_node_id"].as_str().ok_or("pull node missing")?.into(),number,head:facts["head"].as_str().ok_or("head missing")?.into(),base:facts["base"].as_str().ok_or("base missing")?.into(),method:"merge".into(),message:"Q11 permission-race fixture".into()};
                    fs::write(root.join("writer-preparation.json"),serde_json::to_vec(&observed)?)?;
                    fs::write(root.join("permission.ready"),b"downgrade fixture team before continue")?;
                    tokio::time::timeout(Duration::from_secs(300),async {while !root.join("permission.continue").exists(){tokio::time::sleep(Duration::from_millis(100)).await;}}).await?;
                    let capability=store.capability(&saved.connection).await?;
                    let current=store.with_token_operation(&capability,async |bytes| {
                        client.observe(Token::new(bytes.to_vec()).expect("owned token"),field(4).expect("actor"),&Read::MergePreparation{repository:field(5).expect("repository").into(),number}).await
                    }).await??;
                    ensure(current["preparation"]["push"]==false,"fixture permission did not change; no merge attempted")?;
                    fs::write(root.join("reader-preparation.json"),serde_json::to_vec(&current)?)?;
                    let denied=store.with_token_operation(&capability,async |bytes| {
                        client.merge_pull(Token::new(bytes.to_vec()).expect("owned token"),field(4).expect("actor"),&merge).await
                    }).await?;
                    ensure(matches!(denied,WriteOutcome::NotSent(ref failure) if failure.status==403 && failure.code=="github_merge_unavailable"),"permission loss did not prevent native dispatch")?;
                    let request=runtime.as_ref().expect("owned runtime").admit_product(store.clone(),store.capability(&saved.connection).await?,Some(field(5)?.into()))?;
                    let reply=request.request("GET",&format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/capabilities"),&[]).await?;
                    let body:serde_json::Value=serde_json::from_slice(&reply.body)?;
                    ensure(reply.status==200 && body["actions"]["pull.merge"]["state"].as_str().is_some_and(|state| state!="allowed"),"canonical permission loss was hidden")?;
                    fs::write(root.join("reader-capabilities.json"),&reply.body)?;
                    owner.stop()?;
                    println!("PASS: actual writer preparation followed by provider role loss refuses native merge before dispatch; canonical capabilities deny merge; original intent retained");
                    return Ok(());
                }
                if args.len()==10 && field(9)?=="creation-discovery" {
                    let background=profile.parent().ok_or("profile parent missing")?.join("background.json");
                    fs::write(&background,serde_json::to_vec(&json!({"Repository":repository,"Issue":field(6)?,"Connection":saved.connection}))?)?;
                    let check=|action:&str,instance:&str| {
                        let mut command=tokio::process::Command::new(&args[7]);
                        command.arg(profile).arg(instance).arg(action).arg(&background);
                        async move {ensure(command.status().await?.success(),"connected native creation check failed")}
                    };
                    check("check-background-creation",&ready.instance).await?;
                    runtime.take().expect("owned runtime").stop()?;
                    runtime=Some(NativeSupervisor::start(payload,production,profile)?);
                    let request=runtime.as_ref().expect("owned runtime").admit_product(store.clone(),store.capability(&saved.connection).await?,Some(field(5)?.into()))?;
                    ensure(request.request("GET","/api/factory/collaboration/repositories?limit=1",&[]).await?.status==200,"retained creation account unavailable")?;
                    let instance=runtime.as_ref().expect("owned runtime").health()?.instance;
                    check("check-background-creation-retained",&instance).await?;
                    store.withdraw(&saved.connection).await?;
                    check("check-background-creation-withdrawn",&instance).await?;
                    println!("PASS: live native GitHub original-issue discovery, current writer permission, automatic initial acceptance, retained restart replay and local grant withdrawal; gh grant/local policy fixture, no Factory OAuth, model or full intake pipeline");
                    return Ok(());
                }
                if args.len()==10 && field(9)?=="checks-view" {
                    let cases:Vec<serde_json::Value>=serde_json::from_slice(&fs::read(&args[8])?)?;
                    ensure(!cases.is_empty() && cases.len()<=10,"bounded nonempty check cases required")?;
                    for case in cases {
                        let number=case["number"].as_u64().ok_or("check view PR missing")?;
                        let request=runtime.as_ref().expect("owned runtime").admit_product(store.clone(),store.capability(&saved.connection).await?,Some(field(5)?.into()))?;
                        let reply=request.request("GET",&format!("/api/factory/collaboration/repositories/{repository}/pulls/{number}/checks?limit=1"),&[]).await?;
                        let body:serde_json::Value=serde_json::from_slice(&reply.body)?;
                        fs::write(profile.parent().ok_or("profile parent missing")?.join(format!("checks-{number}.json")),&reply.body)?;
                        ensure(reply.status==200 && body["status_gate"]==case["gate"] && body["protection"]["requirements_complete"]==case["complete"],"connected canonical required-check gate differs")?;
                        ensure(body["missing_required"]==case["missing"] && body["invalid_required"]==case["invalid"],"connected canonical check requirements differ")?;
                        if !case["evaluation_commit"].is_null() {
                            ensure(body["evaluation_commit"]==case["evaluation_commit"],"required-check evaluation commit differs")?;
                        }
                        if !case["target"].is_null() {
                            let background=profile.parent().ok_or("profile parent missing")?.join("background.json");
                            fs::write(&background,serde_json::to_vec(&json!({"Repository":repository,"Issue":field(6)?,"Connection":saved.connection,"Checks":case["target"],"ExpectedChecks":case["expected_checks"],"EvaluationCommit":case["evaluation_commit"]}))?)?;
                            for action in ["check-background-inputs","check-background-ci"] {
                                ensure(tokio::process::Command::new(&args[7]).arg(profile).arg(&ready.instance).arg(action).arg(&background).status().await?.success(),"connected native CI assessment failed")?;
                            }
                        }
                    }
                    println!("PASS: actual GitHub required-check gates through canonical Go/native product reads; CI readiness grants no merge eligibility");
                    return Ok(());
                }

                let background=profile.parent().ok_or("profile parent missing")?.join("background.json");
                let base=format!("/api/repositories/{repository}/factory/issues/{}",field(6)?);
                let (body,accepted,mut fixture)=if resume_merge {
                    let mut fixture:serde_json::Value=serde_json::from_slice(&fs::read(&background)?)?;
                    fixture["Connection"]=json!(saved.connection);
                    (Vec::new(),serde_json::Value::Null,fixture)
                } else {
                let request=runtime.as_ref().expect("owned runtime").admit_product(store.clone(),store.capability(&saved.connection).await?,Some(field(5)?.into()))?;
                let preview=request.request("GET",&format!("{base}/acceptance-preview"),&[]).await?;
                fs::write(profile.parent().ok_or("profile parent missing")?.join("preview.json"),&preview.body)?;
                ensure(preview.status==200,"connected acceptance preview unavailable")?;
                let view:serde_json::Value=serde_json::from_slice(&preview.body)?;
                ensure(view["dependencies"]==json!([]),"acceptance fixture needs its actual empty dependency set")?;
                let mut random=[0u8;12];getrandom::fill(&mut random)?;
                let id=format!("d{}",random.iter().map(|b|format!("{b:02x}")).collect::<String>());
                let decision=json!({"decision_id":id,"title_digest":view["title_digest"],"title_occurrence":view["title_occurrence"],"content_digest":view["body_digest"],"content_version":view["content_version"],"native_revision":view["native_revision"]});
                let body=serde_json::to_vec(&decision)?;
                let request=runtime.as_ref().expect("owned runtime").admit_product(store.clone(),store.capability(&saved.connection).await?,Some(field(5)?.into()))?;
                let admitted=request.request("POST",&format!("{base}/acceptances"),&body).await?;
                fs::write(profile.parent().ok_or("profile parent missing")?.join("acceptance.json"),&admitted.body)?;
                ensure(admitted.status==201,"connected native acceptance admission unavailable")?;
                let accepted:serde_json::Value=serde_json::from_slice(&admitted.body)?;
                ensure(accepted["decision_id"]==id && accepted["head"]==id,"native acceptance head differs")?;
                    (body,accepted,json!({"Repository":repository,"Issue":field(6)?,"Connection":saved.connection,"Decision":id}))
                };
                let delivery_check=args.len()==10 && field(9)?=="delivery-material";
                let correction_check=args.len()==10 && field(9)?=="correction";
                let merge_check=args.len()==10 && matches!(field(9)?,"merge"|"merge-resume");
                let publication_check=args.len()==10 && field(9)?=="publication";
                let recovery_check=args.len()==10 && field(9)?=="review-recovery";
                let review_check=args.len()==10 && (field(9)?=="review" || recovery_check);
                if args.len()>=9 { fixture[if publication_check {"Publication"} else if correction_check {"Correction"} else {"Checks"}]=serde_json::from_slice(&fs::read(&args[8])?)?; }
                fs::write(&background,serde_json::to_vec(&fixture)?)?;
                let check = |action: &str, instance: &str| {
                    let mut command=tokio::process::Command::new(&args[7]);
                    command.arg(profile).arg(instance).arg(action).arg(&background);
                    async move { ensure(command.status().await?.success(),"native background product check failed") }
                };
                check("check-background-inputs",&ready.instance).await?;
                if delivery_check {
                    check("check-background-delivery-material",&ready.instance).await?;
                    println!("PASS: connected native delivery discussion/opinions, complete bounded traversal and explicit unknown facts; local policy declared, no GitHub write, model or merge");
                    return Ok(());
                }
                if resume_merge {check("check-background-merge-resume",&ready.instance).await?;}
                else if merge_check {check("check-background-merge",&ready.instance).await?;}
                else if correction_check {check("check-background-correction",&ready.instance).await?;}
                else if publication_check {check("check-background-publication",&ready.instance).await?;}
                else if recovery_check {check("check-background-review-recovery-prepare",&ready.instance).await?;}
                else if review_check {check("check-background-review",&ready.instance).await?;}
                else {
                    if args.len()==9 { check("check-background-ci",&ready.instance).await?; }
                    runtime.as_ref().expect("owned runtime").start_execution_coordinator().await?;
                    check("check-background-readiness",&ready.instance).await?;
                }
                runtime.take().expect("owned runtime").stop()?;
                runtime=Some(NativeSupervisor::start(payload,production,profile)?);
                let request=runtime.as_ref().expect("owned runtime").admit_product(store.clone(),store.capability(&saved.connection).await?,Some(field(5)?.into()))?;
                if !merge_check {
                    let replay=request.request("POST",&format!("{base}/acceptances"),&body).await?;
                    ensure(replay.status==201 && serde_json::from_slice::<serde_json::Value>(&replay.body)?==accepted,"native acceptance changed after retained restart replay")?;
                }
                let restarted=runtime.as_ref().expect("owned runtime").health()?;
                if recovery_check {check("check-background-review-recovery",&restarted.instance).await?;}
                check("check-background-restarted",&restarted.instance).await?;
                if publication_check {check("check-background-publication-retained",&restarted.instance).await?;}
                if correction_check {check("check-background-correction-retained",&restarted.instance).await?;}
                if merge_check {check("check-background-merge-retained",&restarted.instance).await?;}
                if review_check {check("check-background-review-retained",&restarted.instance).await?;}
                store.withdraw(&saved.connection).await?;
                check("check-background-withdrawn",&restarted.instance).await?;
                if merge_check {check("check-background-merge-retained",&restarted.instance).await?;println!("PASS: actual native merge/result/completion, command replay and retained restart/withdrawal; local workflow inputs declared, no model/full coordinator");}
                else if correction_check {check("check-background-correction-retained",&restarted.instance).await?;println!("PASS: actual native correction ref update/replay and retained restart/withdrawal; original workflow/run inputs declared, no model or merge");}
                else if publication_check {check("check-background-publication-retained",&restarted.instance).await?;println!("PASS: actual native Git pack/branch and PR publication, command replay, retained restart and credential withdrawal; run/authority inputs declared, no model or merge");}
                else if recovery_check {check("check-background-review-recovery-retained",&restarted.instance).await?;println!("PASS: connected native coordinator review recovery across restart/withdrawal; generic absence, expired and unknown sends remain held; one COMMENT review, no model or execution");}
                else if review_check {check("check-background-review-retained",&restarted.instance).await?;println!("PASS: connected native review fixture/restart/withdrawal; one labeled COMMENT review, no model or execution");}
                else {println!("PASS: connected native approval, background evidence/readiness without a view, retained restart replay and exact account withdrawal; no GitHub write or execution");}
                Ok(())
            }.await;
            let removed = store.withdraw(&saved.connection).await;
            let stopped = runtime.take().map(NativeSupervisor::stop).transpose();
            removed?;
            stopped?;
            outcome
        }
        (Some("--github-automation-inputs" | "--github-merge-completion" | "--github-source-bundle" | "--github-completion-issues"), n) if n >= 6 => {
            use std::io::Write;
            use std::os::unix::fs::OpenOptionsExt;
            let field = |n: usize| {
                args[n]
                    .to_str()
                    .ok_or("invalid automation fixture argument")
            };
            let mut secret = Vec::new();
            std::io::stdin().take(65537).read_to_end(&mut secret)?;
            if secret.last() == Some(&b'\n') {
                secret.pop();
            }
            let token = Token::new(secret)?;
            let mut owner =
                tokio::task::spawn_blocking(soda_desktop_runtime::github_provider::Owner::start)
                    .await??;
            let completion=args[0]=="--github-merge-completion";
            let source=args[0]=="--github-source-bundle";
            let batch=args[0]=="--github-completion-issues";
            let query=if batch { soda_desktop_runtime::github_provider::Read::CompletionIssues {repository:field(2)?.into(),target_ref:field(4)?.into(),limit:field(3)?.parse()?,cursor:if args.len()>6 {field(6)?.into()}else{String::new()}} } else if source { soda_desktop_runtime::github_provider::Read::SourceBundle {repository:field(2)?.into(),base:field(3)?.into(),expected_base:String::new(),commit:field(4)?.into(),target_ref:field(6)?.into(),number:None,head_repository:String::new()} }
            else if completion { soda_desktop_runtime::github_provider::Read::MergeCompletion {repository:field(2)?.into(),number:field(3)?.parse()?} } else {
                soda_desktop_runtime::github_provider::Read::AutomationInputs {repository:field(2)?.into(),number:field(3)?.parse()?,target_ref:field(4)?.into(),comments:args[6..].iter().map(|s|s.to_str().map(str::to_owned).ok_or("invalid selected comment")).collect::<std::result::Result<_,_>>()?}
            };
            let observed = owner
                .client()
                .observe(
                    token,
                    field(1)?,
                    &query,
                )
                .await;
            owner.stop()?;
            let observed = observed.map_err(|failure| {
                format!(
                    "native automation read: {} {}",
                    failure.status, failure.code
                )
            })?;
            let mut file = fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(Path::new(&args[5]))?;
            file.write_all(&serde_json::to_vec(&observed)?)?;
            file.sync_all()?;
            if source { println!("PASS: actual authenticated native source bundle for the exact observed branch tip; no write or product admission"); }
            else if completion { println!("PASS: actual native merged PR/base completion read; no write or product admission"); }
            else { println!("PASS: actual native GitHub bounded issue history, selected comments, dependencies and branch snapshot; no write or product admission"); }
            Ok(())
        }
        (Some("--github-budget-declared"), 2) => {
            native_github_transport::run_budget_declared(Path::new(&args[1]))
        }
        (Some("--github-evidence-declared"), 4) => {
            native_github_transport::run_evidence_declared(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-refresh-refusal"), 4) => {
            native_github_transport::run_refresh_refusal(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-refresh-declared"), 4) => {
            native_github_transport::run_refresh_declared(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-refresh"), _) => {
            Err("historical Go refresh observer retired; use --github-refresh-refusal or --github-refresh-declared with production backend, PostgreSQL payload and fresh profile".into())
        }
        (Some("--grant-rotation"), 2) => {
            run_rotation(Path::new(&args[1])).await
        }
        (Some("--rotation-probe"), 3) => {
            let connection = args[2].to_str().ok_or("invalid connection")?;
            run_rotation_probe(Path::new(&args[1]), connection).await
        }
        (Some("--rotation-writer"), 3) => {
            let connection = args[2].to_str().ok_or("invalid connection")?;
            run_rotation_writer(Path::new(&args[1]), connection).await
        }
        (Some("--rotation-crash"), 3) => {
            let connection = args[2].to_str().ok_or("invalid connection")?;
            run_rotation_crash(Path::new(&args[1]), connection).await
        }
        (Some("--grant-custody"), 2) => {
            run_grant_custody(Path::new(&args[1])).await
        }
        (Some("--credential-transport"), 2) => {
            run(Path::new(&args[1]), cfg!(target_os = "linux")).await
        }
        (Some("--github-grant-declared"), 4) => {
            native_github_transport::run_grant(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-grant"), _) => {
            Err("historical Go grant observer retired; use --github-grant-declared with production backend, PostgreSQL payload and fresh profile".into())
        }
        (Some("--github-device-pending"), 4) => {
            native_github_transport::run_device_pending(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-device-declared"), 4) => {
            native_github_transport::run_device_declared(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-device"), _) => {
            Err("historical Go device observer retired; use --github-device-pending or --github-device-declared with production backend, PostgreSQL payload and fresh profile".into())
        }
        (Some("--github-comment-declared"), 2) => {
            native_github_transport::run_comment_declared(Path::new(&args[1])).await
        }
        (Some("--github-issue-declared"), 2) => {
            native_github_issues::producer(Path::new(&args[1])).await
        }
        (Some("--github-state-declared"), 2) => {
            native_github_states::producer(Path::new(&args[1])).await
        }
        (Some("--github-edit-declared"), 2) => {
            native_github_edits::producer(Path::new(&args[1])).await
        }
        (Some("--github-lock-declared"), 2) => {
            native_github_locks::producer(Path::new(&args[1])).await
        }
        (Some("--github-threads-declared"), 2) => {
            native_github_threads::producer(Path::new(&args[1])).await
        }
        (Some("--github-reviewer-read-declared"), 2) => {
            native_github_review_requests::producer(Path::new(&args[1])).await
        }
        (Some("--github-review-preparation-declared"), 2) => {
            native_github_review_requests::preparation(Path::new(&args[1])).await
        }
        (Some("--github-inline-expiry-product-declared"), 5) => {
            native_github_review_requests::expiry(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
                false,
                true,
            )
            .await
        }
        (Some("--github-inline-product-declared"), 5) => {
            native_github_review_requests::inline_product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
            )
            .await
        }
        (Some("--github-inline-preservation-declared"), 2) => {
            native_github_review_requests::inline_preservation(Path::new(&args[1])).await
        }
        (Some("--github-inline-draft-declared"), 2) => {
            native_github_review_requests::inline_draft(Path::new(&args[1])).await
        }
        (Some("--github-inline-preparation-port-declared"), 4) => {
            native_github_review_requests::inline_preparation_port(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-inline-preparation-declared"), 2) => {
            native_github_review_requests::inline_preparation(Path::new(&args[1])).await
        }
        (Some("--github-pending-review-snapshot-declared"), 2) => {
            native_github_review_requests::pending_snapshot(Path::new(&args[1])).await
        }
        (Some("--github-review-publication-declared"), 2) => {
            native_github_review_requests::publication(Path::new(&args[1])).await
        }
        (Some("--github-pending-review-submit-declared"), 2) => {
            native_github_review_requests::pending_submit(Path::new(&args[1])).await
        }
        (Some("--github-review-create-declared"), 2) => {
            native_github_review_requests::creation(Path::new(&args[1])).await
        }
        (Some("--github-review-create-product-declared"), 4) => {
            native_github_review_requests::new_product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-pending-review-product-declared"), 5) => {
            native_github_review_requests::pending_product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
            )
            .await
        }
        (
            Some(
                "--github-review-expiry-product-declared"
                | "--github-pending-review-expiry-product-declared",
            ),
            5,
        ) => {
            native_github_review_requests::expiry(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
                args[0] == "--github-pending-review-expiry-product-declared",
                false,
            )
            .await
        }
        (Some("--github-pull-branches-declared"), 2) => {
            native_github_pull_branches::producer(Path::new(&args[1])).await
        }
        (Some("--github-branch-create-declared"), 2) => {
            native_github_pull_branches::branch_creation(Path::new(&args[1])).await
        }
        (Some("--github-branch-expiry-product-declared"), 5) => {
            native_github_pull_branches::branch_expiry(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
            )
            .await
        }
        (Some("--github-merges-connected" | "--github-review-writes-connected" | "--github-inlines-connected" | "--github-general-writes-connected"), 5) => {
            native_github_pull_branches::connected_writes(Path::new(&args[1]), Path::new(&args[2]), Path::new(&args[3]), Path::new(&args[4]), match args[0].to_str() {Some("--github-review-writes-connected")=>native_github_pull_branches::ConnectedWriteCase::Reviews,Some("--github-inlines-connected")=>native_github_pull_branches::ConnectedWriteCase::Inlines,Some("--github-general-writes-connected")=>native_github_pull_branches::ConnectedWriteCase::General,_=>native_github_pull_branches::ConnectedWriteCase::Merges}).await
        }
        (Some("--github-branch-product-declared"), 5) => {
            native_github_pull_branches::branch_product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
            )
            .await
        }
        (Some("--github-pull-create-declared"), 2) => {
            native_github_pull_branches::creation(Path::new(&args[1])).await
        }
        (Some("--github-pull-create-product-declared"), 4) => {
            native_github_pull_branches::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-reviewer-write-declared"), 2) => {
            native_github_review_requests::effects(Path::new(&args[1])).await
        }
        (Some("--github-reviewer-product-declared"), 4) => {
            native_github_review_requests::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-thread-resolution-declared"), 2) => {
            native_github_threads::resolution(Path::new(&args[1])).await
        }
        (Some("--github-thread-resolution-product-declared"), 4) => {
            native_github_threads::resolution_product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-threads-product-declared"), 4) => {
            native_github_threads::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-lock-product-declared"), 4) => {
            native_github_locks::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-edit-product-declared"), 4) => {
            native_github_edits::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-state-product-declared"), 4) => {
            native_github_states::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-issue-product-declared"), 4) => {
            native_github_issues::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-comment-envelope-declared"), 4) => {
            Err("the comment envelope producer observer is retired; use --github-comment-product-declared for the composed writer in an owned no-forwarding fixture".into())
        }
        (Some("--github-comment-product-declared"), 4) => {
            native_github_comments::run(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-transport-refusal" | "--github-transport-declared"), 4) => {
            native_github_transport::run(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                args[0] == "--github-transport-declared",
            )
            .await
        }
        (Some("--github-verify-refusal" | "--github-verify-declared"), 4) => {
            native_github_transport::run_verify(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                args[0] == "--github-verify-declared",
            )
            .await
        }
        (Some("--github-verify" | "--github-transport"), _) => {
            Err("historical Go verifier/transport observers retired; use their -refusal or -declared replacement with production backend, PostgreSQL payload and fresh profile".into())
        }
        (Some("--github-issues"), 4) => {
            native_github_transport::run_records(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                false,
            )
            .await
        }
        (Some("--github-discussion"), 4) => {
            native_github_transport::run_discussion(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-reviews"), 4) => {
            native_github_transport::run_reviews(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            )
            .await
        }
        (Some("--github-pulls"), 4) => {
            native_github_transport::run_records(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                true,
            )
            .await
        }
        (Some("--github-diff"), 4) => {
            native_github_transport::run_diff(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                None,
            )
            .await
        }
        (Some("--github-diff-pins"), 5) => {
            let saved: serde_json::Value = serde_json::from_slice(&fs::read(&args[4])?)?;
            ensure(
                saved["result"] == "pass" && saved["phase"] == "discovery",
                "actual discovery evidence required",
            )?;
            let base = saved["diff"]["base"]
                .as_str()
                .ok_or("discovery base missing")?;
            let head = saved["diff"]["head"]
                .as_str()
                .ok_or("discovery head missing")?;
            ensure(
                !base.is_empty() && !head.is_empty(),
                "saved diff pair required",
            )?;
            native_github_transport::run_diff(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Some((base, head)),
            )
            .await
        }
        (Some("--github-review-comments"), 5) => {
            let saved: serde_json::Value = serde_json::from_slice(&fs::read(&args[4])?)?;
            ensure(
                saved["result"] == "pass" && saved["phase"] == "list",
                "actual review-comment page required",
            )?;
            let repository = saved["page"]["repository"]
                .as_str()
                .ok_or("review-comment repository missing")?;
            let number = saved["page"]["pull"]["number"]
                .as_u64()
                .and_then(|n| u32::try_from(n).ok())
                .ok_or("review-comment PR missing")?;
            let id = saved["page"]["items"][0]["external_id"]
                .as_str()
                .ok_or("actual comment ID missing")?;
            native_github_transport::run_review_comments(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                repository,
                number,
                id,
            )
            .await
        }
        (Some("--github-commit-statuses"), 5) => {
            let saved: serde_json::Value = serde_json::from_slice(&fs::read(&args[4])?)?;
            ensure(
                saved["result"] == "pass" && saved["phase"] == "discover",
                "actual status discovery required",
            )?;
            let repository = saved["page"]["repository"]
                .as_str()
                .ok_or("status repository missing")?;
            let number = saved["page"]["pull"]["number"]
                .as_u64()
                .and_then(|n| u32::try_from(n).ok())
                .ok_or("status PR missing")?;
            let head = saved["page"]["head"]
                .as_str()
                .ok_or("status head missing")?;
            native_github_transport::run_commit_statuses(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                repository,
                number,
                head,
            )
            .await
        }
        (Some("--github-check-runs"), 5) => {
            let saved: serde_json::Value = serde_json::from_slice(&fs::read(&args[4])?)?;
            ensure(
                saved["result"] == "pass" && saved["phase"] == "discover",
                "actual check discovery evidence required",
            )?;
            let head = saved["page"]["head"]
                .as_str()
                .ok_or("check discovery head missing")?;
            ensure(!head.is_empty(), "actual full head required")?;
            native_github_transport::run_check_runs(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                head,
            )
            .await
        }
        (Some("--github-files"), 5) => {
            let saved: serde_json::Value = serde_json::from_slice(&fs::read(&args[4])?)?;
            ensure(
                saved["result"] == "pass" && saved["phase"] == "discovery",
                "actual file discovery evidence required",
            )?;
            let base = saved["page"]["diff"]["base"]
                .as_str()
                .ok_or("file discovery base missing")?;
            let head = saved["page"]["diff"]["head"]
                .as_str()
                .ok_or("file discovery head missing")?;
            ensure(
                !base.is_empty() && !head.is_empty(),
                "saved file diff pair required",
            )?;
            native_github_transport::run_files(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                base,
                head,
            )
            .await
        }
        _ => {
            ensure(args.len() == 1, "usage: factory-credential-operation-check NEW_PROFILE | --github-transport|--github-verify PRODUCTION_BACKEND OBSERVED_BACKEND PG_PAYLOAD NEW_PROFILE | --github-issues|--github-discussion|--github-pulls|--github-reviews|--github-diff BACKEND PG_PAYLOAD NEW_PROFILE | --github-diff-pins|--github-files|--github-check-runs|--github-commit-statuses|--github-review-comments BACKEND PG_PAYLOAD NEW_PROFILE SAVED_DISCOVERY_JSON (isolated real keyring driver only)")?;
            run(Path::new(&args[0]), false).await
        }
    }
}

fn main() {
    let result = (|| -> Result<()> {
        let args: Vec<_> = std::env::args_os().skip(1).collect();
        // Blocking owner retirement (including error-path Drop) must leave
        // the fixture's keyring transport executor available until it joins.
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .enable_all()
            .build()?;
        runtime.block_on(run_selected(&args))
    })();
    if let Err(error) = result {
        eprintln!("Native credential operation check: {error}");
        std::process::exit(1);
    }
}
