//! Real isolated Linux keyring observation for asynchronous native token use.
//! No provider login, network credential, webview or application admission.
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
#[path = "../native_github_comments.rs"]
mod native_github_comments;
#[path = "../native_github_issues.rs"]
mod native_github_issues;
#[path = "../native_github_states.rs"]
mod native_github_states;
#[path = "../native_github_transport.rs"]
mod native_github_transport;
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
    ensure(
        profile.is_absolute()
            && profile.starts_with("/home")
            && !profile.exists()
            && parent.is_dir(),
        "fresh owned /home profile required",
    )?;
    ensure(
        unsafe { libc::geteuid() } != 0,
        "keyring observer requires an ordinary user",
    )?;
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
async fn run(profile: &Path) -> Result<()> {
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
    let data = parent.join("data/keyrings");
    let mut keyrings = 0;
    for entry in fs::read_dir(data)? {
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
fn main() {
    let result = (|| -> Result<()> {
        let args: Vec<_> = std::env::args_os().skip(1).collect();
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?;
        if args.len() == 4 && args[0] == "--github-evidence-declared" {
            return runtime.block_on(native_github_transport::run_evidence_declared(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4 && args[0] == "--github-refresh-refusal" {
            return runtime.block_on(native_github_transport::run_refresh_refusal(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4 && args[0] == "--github-refresh-declared" {
            return runtime.block_on(native_github_transport::run_refresh_declared(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.first().is_some_and(|arg| arg == "--github-refresh") {
            return Err("historical Go refresh observer retired; use --github-refresh-refusal or --github-refresh-declared with production backend, PostgreSQL payload and fresh profile".into());
        }
        if args.len() == 2 && args[0] == "--grant-rotation" {
            return runtime.block_on(run_rotation(Path::new(&args[1])));
        }
        if args.len() == 3 && args[0] == "--rotation-probe" {
            let connection = args[2].to_str().ok_or("invalid connection")?;
            return runtime.block_on(run_rotation_probe(Path::new(&args[1]), connection));
        }
        if args.len() == 3 && args[0] == "--rotation-writer" {
            let connection = args[2].to_str().ok_or("invalid connection")?;
            return runtime.block_on(run_rotation_writer(Path::new(&args[1]), connection));
        }
        if args.len() == 3 && args[0] == "--rotation-crash" {
            let connection = args[2].to_str().ok_or("invalid connection")?;
            return runtime.block_on(run_rotation_crash(Path::new(&args[1]), connection));
        }
        if args.len() == 2 && args[0] == "--grant-custody" {
            return runtime.block_on(run_grant_custody(Path::new(&args[1])));
        }
        if args.len() == 4 && args[0] == "--github-grant-declared" {
            return runtime.block_on(native_github_transport::run_grant(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.first().is_some_and(|arg| arg == "--github-grant") {
            return Err("historical Go grant observer retired; use --github-grant-declared with production backend, PostgreSQL payload and fresh profile".into());
        }
        if args.len() == 4 && args[0] == "--github-device-pending" {
            return runtime.block_on(native_github_transport::run_device_pending(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4 && args[0] == "--github-device-declared" {
            return runtime.block_on(native_github_transport::run_device_declared(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.first().is_some_and(|arg| arg == "--github-device") {
            return Err("historical Go device observer retired; use --github-device-pending or --github-device-declared with production backend, PostgreSQL payload and fresh profile".into());
        }
        if args.len() == 2 && args[0] == "--github-comment-declared" {
            return runtime.block_on(native_github_transport::run_comment_declared(Path::new(
                &args[1],
            )));
        }
        if args.len() == 2 && args[0] == "--github-issue-declared" {
            return runtime.block_on(native_github_issues::producer(Path::new(&args[1])));
        }
        if args.len() == 2 && args[0] == "--github-state-declared" {
            return runtime.block_on(native_github_states::producer(Path::new(&args[1])));
        }
        if args.len() == 4 && args[0] == "--github-state-product-declared" {
            return runtime.block_on(native_github_states::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4 && args[0] == "--github-issue-product-declared" {
            return runtime.block_on(native_github_issues::product(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4 && args[0] == "--github-comment-envelope-declared" {
            return Err("the comment envelope producer observer is retired; use --github-comment-product-declared for the composed writer in an owned no-forwarding fixture".into());
        }
        if args.len() == 4 && args[0] == "--github-comment-product-declared" {
            return runtime.block_on(native_github_comments::run(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4
            && (args[0] == "--github-transport-refusal" || args[0] == "--github-transport-declared")
        {
            return runtime.block_on(native_github_transport::run(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                args[0] == "--github-transport-declared",
            ));
        }
        if args.len() == 4
            && (args[0] == "--github-verify-refusal" || args[0] == "--github-verify-declared")
        {
            return runtime.block_on(native_github_transport::run_verify(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                args[0] == "--github-verify-declared",
            ));
        }
        if args
            .first()
            .is_some_and(|arg| arg == "--github-verify" || arg == "--github-transport")
        {
            return Err("historical Go verifier/transport observers retired; use their -refusal or -declared replacement with production backend, PostgreSQL payload and fresh profile".into());
        }
        if args.len() == 4 && args[0] == "--github-issues" {
            return runtime.block_on(native_github_transport::run_records(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                false,
            ));
        }
        if args.len() == 4 && args[0] == "--github-discussion" {
            return runtime.block_on(native_github_transport::run_discussion(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4 && args[0] == "--github-reviews" {
            return runtime.block_on(native_github_transport::run_reviews(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
        }
        if args.len() == 4 && args[0] == "--github-pulls" {
            return runtime.block_on(native_github_transport::run_records(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                true,
            ));
        }
        if args.len() == 4 && args[0] == "--github-diff" {
            return runtime.block_on(native_github_transport::run_diff(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                None,
            ));
        }
        if args.len() == 5 && args[0] == "--github-diff-pins" {
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
            return runtime.block_on(native_github_transport::run_diff(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Some((base, head)),
            ));
        }
        if args.len() == 5 && args[0] == "--github-review-comments" {
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
            return runtime.block_on(native_github_transport::run_review_comments(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                repository,
                number,
                id,
            ));
        }
        if args.len() == 5 && args[0] == "--github-commit-statuses" {
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
            return runtime.block_on(native_github_transport::run_commit_statuses(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                repository,
                number,
                head,
            ));
        }
        if args.len() == 5 && args[0] == "--github-check-runs" {
            let saved: serde_json::Value = serde_json::from_slice(&fs::read(&args[4])?)?;
            ensure(
                saved["result"] == "pass" && saved["phase"] == "discover",
                "actual check discovery evidence required",
            )?;
            let head = saved["page"]["head"]
                .as_str()
                .ok_or("check discovery head missing")?;
            ensure(!head.is_empty(), "actual full head required")?;
            return runtime.block_on(native_github_transport::run_check_runs(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                head,
            ));
        }
        if args.len() == 5 && args[0] == "--github-files" {
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
            return runtime.block_on(native_github_transport::run_files(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                base,
                head,
            ));
        }
        ensure(args.len() == 1, "usage: factory-credential-operation-check NEW_PROFILE | --github-transport|--github-verify PRODUCTION_BACKEND OBSERVED_BACKEND PG_PAYLOAD NEW_PROFILE | --github-issues|--github-discussion|--github-pulls|--github-reviews|--github-diff BACKEND PG_PAYLOAD NEW_PROFILE | --github-diff-pins|--github-files|--github-check-runs|--github-commit-statuses|--github-review-comments BACKEND PG_PAYLOAD NEW_PROFILE SAVED_DISCOVERY_JSON (isolated real keyring driver only)")?;
        runtime.block_on(run(Path::new(&args[0])))
    })();
    if let Err(error) = result {
        eprintln!("Native credential operation check: {error}");
        std::process::exit(1);
    }
}
