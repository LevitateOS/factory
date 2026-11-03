//! Real isolated Linux keyring observation for asynchronous native token use.
//! No provider login, network credential, webview or application admission.
use soda_desktop_runtime::credentials::{
    Account, Credentials, Error, Provider, Token, TokenCapability,
};
use std::error::Error as StdError;
use std::fs;
use std::io::Read;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::oneshot;

type Result<T> = std::result::Result<T, Box<dyn StdError>>;
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
fn launch(store: Arc<Credentials>, cap: Arc<TokenCapability>, expected: Vec<u8>) -> Flight {
    let (started, start) = oneshot::channel();
    let (finish, done) = oneshot::channel();
    let dropped = Arc::new(AtomicBool::new(false));
    let marker = dropped.clone();
    let task = tokio::spawn(async move {
        store
            .with_token_operation(&cap, async move |bytes| {
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

    let flight = launch(store.clone(), cap, first.clone());
    ensure(
        flight.started.await?,
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
    println!("PASS: real keyring withdrawal completes while native I/O is pending and refuses its result");

    let saved = store.store(account(), Token::new(first.clone())?).await?;
    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap.clone(), first.clone());
    ensure(
        flight.started.await?,
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
    println!("PASS: independently reopened keyring owner rotates the real credential during I/O; old result refused");

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap, second.clone());
    ensure(
        flight.started.await?,
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
    store = Arc::new(Credentials::open(profile, namespace)?);

    let cap = Arc::new(store.capability(&saved.connection).await?);
    let flight = launch(store.clone(), cap, second.clone());
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
    let flight = launch(store.clone(), cap, second.clone());
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
    let lease_started = Instant::now();
    tokio::time::sleep(Duration::from_secs(28)).await;
    let flight = launch(store.clone(), cap.clone(), second.clone());
    ensure(
        flight.started.await?,
        "expiry operation received wrong token",
    )?;
    ensure(
        flight.task.await? == Err(Error::Retired)
            && flight.dropped.load(Ordering::SeqCst)
            && lease_started.elapsed() >= Duration::from_secs(29),
        "real capability expiry released a result",
    )?;
    drop(flight.finish);
    println!("PASS: actual thirty-second native lease expires during I/O; no shortened test lease");

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
fn main() {
    let result = (|| -> Result<()> {
        let args: Vec<_> = std::env::args_os().skip(1).collect();
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?;
        if args.len() == 5 && args[0] == "--github-device" {
            return runtime.block_on(native_github_transport::run_device(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
            ));
        }
        if args.len() == 5 && args[0] == "--github-transport" {
            return runtime.block_on(native_github_transport::run(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
            ));
        }
        if args.len() == 5 && args[0] == "--github-verify" {
            return runtime.block_on(native_github_transport::run_verify(
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
                Path::new(&args[4]),
            ));
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
