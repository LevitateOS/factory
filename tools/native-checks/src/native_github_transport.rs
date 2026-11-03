//! Real native supervisor/keyring/Go/GitHub transport. Generated invalid tokens
//! qualify credential forwarding/refusal, never a successful OAuth grant.
use super::{ensure, fixture, prepare, Result};
use soda_desktop_runtime::credentials::{
    Account, Credentials, Error as CredentialError, Provider, Token,
};
use soda_desktop_runtime::github::{Error, Read};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use std::fs;
use std::future::Future;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::pin::Pin;
use std::task::Poll;
use std::time::{Duration, Instant};

fn account() -> Account {
    Account::new(Provider::Github, "https://github.com", "1").unwrap()
}
fn invalid() -> Result<Vec<u8>> {
    let mut value = b"factory-check-invalid-".to_vec();
    value.extend(fixture()?);
    Ok(value)
}
/// Declared transport only; proves that full grants compose across the native
/// device/verifier/custody boundaries without dropping refresh or expiration.
pub(super) async fn run_grant(observed: &Path, payload: &Path, profile: &Path) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    fs::write(io.join("mode"), b"grant")?;
    let runtime = NativeSupervisor::start(payload, observed, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    let grant = runtime.github_device_start().await?.wait().await?;
    let expected_expiry = grant.expires_at;
    let expected_refresh_expiry = grant.refresh_expires_at;
    let (account, grant) = runtime.github_verify_grant(grant).await?;
    let saved = store.store_grant(account, grant).await?;
    let info = saved
        .grant
        .as_ref()
        .ok_or("stored grant metadata missing")?;
    ensure(
        saved.account == "123"
            && info.expires_at == expected_expiry
            && info.refresh_expires_at == expected_refresh_expiry
            && info.refresh_available,
        "verified grant lost identity or expiry on storage",
    )?;
    let reopened = Credentials::open(profile, &ready.profile)?;
    let cap = reopened.capability(&saved.connection).await?;
    ensure(
        reopened
            .with_token(&cap, |bytes| bytes == b"factory-declared-access")
            .await?
            && reopened
                .with_refresh_operation(&cap, async |bytes| bytes == b"factory-declared-refresh")
                .await?,
        "device/verifier/store/reopen lost a declared grant secret",
    )?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "declared grant cleanup failed",
    )?;
    store.retire();
    reopened.retire();
    runtime.stop()?;
    println!("PASS: declared upstream device grant crosses private Go/Rust transport, identity verification, full encrypted storage and native reopen without losing refresh/expiry; real private PG/backend stop; actual GitHub consent/login remains unqualified");
    Ok(())
}

/// Device authorization producer only. Controlled transport grants are labelled
/// explicitly; no human consent, real token or successful login is asserted.
pub(super) async fn run_device(
    production: &Path,
    observed: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "device profile is not empty",
    )?;
    let authorization = runtime.github_device_start().await?;
    ensure(
        authorization.prompt().verification_uri == "https://github.com/login/device"
            && authorization.prompt().user_code.len() == 9,
        "actual GitHub challenge is invalid",
    )?;
    ensure(
        tokio::time::timeout(Duration::from_secs(7), authorization.wait())
            .await
            .is_err(),
        "unapproved GitHub device challenge did not remain pending",
    )?;
    let stale = runtime.github_device_start();
    runtime.stop()?;
    ensure(
        matches!(stale.await, Err(Error::Unavailable)),
        "retired device start survived",
    )?;
    println!("PASS: actual GitHub device challenge with owned public client ID; unapproved polling stays pending and cancels; retired start refuses; no consent/token/login claimed");

    let runtime = NativeSupervisor::start(payload, observed, profile)?;
    let set_mode = |mode: &str| -> Result<()> {
        fs::write(io.join("mode"), mode)?;
        for name in ["entered", "cancelled", "calls"] {
            match fs::remove_file(io.join(name)) {
                Ok(()) => (),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
                Err(error) => return Err(error.into()),
            }
        }
        Ok(())
    };
    for mode in ["grant", "long-lived", "backoff"] {
        set_mode(mode)?;
        let authorization = runtime.github_device_start().await?;
        let prompt = serde_json::to_string(authorization.prompt())?;
        ensure(
            !prompt.contains("device_code") && !prompt.contains("token"),
            "prompt exposes a native field",
        )?;
        let before = Instant::now();
        let grant = authorization.wait().await?;
        ensure(grant.scope == "repo,read:user", "declared grant lost scope")?;
        if mode == "long-lived" {
            ensure(
                grant.expires_at.is_none()
                    && grant.refresh.is_none()
                    && grant.refresh_expires_at.is_none(),
                "invented expiry or refresh",
            )?;
        } else {
            ensure(
                grant.expires_at.is_some()
                    && grant.refresh.is_some()
                    && grant.refresh_expires_at.is_some(),
                "lost expiry/refresh data",
            )?;
        }
        if mode == "backoff" {
            ensure(
                before.elapsed() >= Duration::from_secs(12)
                    && fs::read_to_string(io.join("calls"))? == "3",
                "polling ignored slow_down or long native deadline",
            )?;
        }
        let (_account, _token) = runtime.github_verify(grant.access).await?;
    }
    println!("PASS: declared transport grants preserve expiring/refresh and long-lived facts; native identity verifier composes; upstream slow_down takes >12 seconds across the normal 10-second server deadline");
    for (mode, status) in [
        ("denied", 403),
        ("expired", 408),
        ("deadline", 408),
        ("invalid-token", 502),
    ] {
        set_mode(mode)?;
        let authorization = runtime.github_device_start().await?;
        ensure(
            matches!(authorization.wait().await, Err(Error::Provider(code)) if code == status),
            "device refusal/expiry was not preserved",
        )?;
    }
    for mode in ["bad-uri", "oversized", "redirect"] {
        set_mode(mode)?;
        ensure(
            matches!(
                runtime.github_device_start().await,
                Err(Error::Provider(502))
            ),
            "invalid upstream challenge was accepted",
        )?;
    }
    set_mode("pause-start")?;
    let mut start = Box::pin(runtime.github_device_start());
    entered(start.as_mut(), &io).await?;
    drop(start);
    cancelled(&io).await?;
    set_mode("pause-wait")?;
    let authorization = runtime.github_device_start().await?;
    let mut wait = Box::pin(authorization.wait());
    entered(wait.as_mut(), &io).await?;
    drop(wait);
    cancelled(&io).await?;
    set_mode("grant")?;
    let authorization = runtime.github_device_start().await?;
    runtime.stop()?;
    ensure(
        matches!(authorization.wait().await, Err(Error::Unavailable)),
        "captured authorization survived instance retirement",
    )?;
    ensure(
        store.status().await?.is_empty(),
        "authorization producer saved a credential",
    )?;
    store.retire();
    println!("PASS: declared transport denial, expiry, local deadline, invalid token/URI/body/redirect, in-flight start/wait cancellation and retired authorization refuse; isolated OS custody remains empty; private native PG/backend stop cleanly");
    Ok(())
}
async fn cancelled(io: &Path) -> Result<()> {
    let until = Instant::now() + Duration::from_secs(4);
    while !io.join("cancelled").exists() {
        ensure(
            Instant::now() < until,
            "device cancellation did not reach Go network request",
        )?;
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    Ok(())
}
fn mark(path: &Path) -> Result<()> {
    fs::write(path, b"")?;
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    Ok(())
}
fn pause(io: &Path) -> Result<()> {
    for name in ["pause", "entered", "release", "cancelled", "status"] {
        match fs::remove_file(io.join(name)) {
            Ok(()) => (),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
            Err(e) => return Err(e.into()),
        }
    }
    mark(&io.join("pause"))
}
async fn entered<T>(
    future: Pin<&mut impl Future<Output = std::result::Result<T, Error>>>,
    io: &Path,
) -> Result<()> {
    let mut future = future;
    let until = Instant::now() + Duration::from_secs(4);
    loop {
        let ready = std::future::poll_fn(|cx| Poll::Ready(future.as_mut().poll(cx))).await;
        if let Poll::Ready(result) = ready {
            return Err(match result {
                Ok(_) => "RPC completed before transport barrier".into(),
                Err(e) => format!("RPC failed before transport barrier: {e}"),
            }
            .into());
        }
        if io.join("entered").exists() {
            return Ok(());
        }
        ensure(
            Instant::now() < until,
            "actual Go transport did not reach barrier",
        )?;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}
fn calls(io: &Path) -> Result<u64> {
    Ok(fs::read_to_string(io.join("calls"))?.parse()?)
}
pub(super) async fn run_check_runs(
    production: &Path,
    payload: &Path,
    profile: &Path,
    head: &str,
) -> Result<()> {
    run_status_pages(
        production,
        payload,
        profile,
        ("10270722", 4493, head),
        false,
    )
    .await
}
pub(super) async fn run_commit_statuses(
    production: &Path,
    payload: &Path,
    profile: &Path,
    repository: &str,
    number: u32,
    head: &str,
) -> Result<()> {
    run_status_pages(
        production,
        payload,
        profile,
        (repository, number, head),
        true,
    )
    .await
}
async fn run_status_pages(
    production: &Path,
    payload: &Path,
    profile: &Path,
    scope: (&str, u32, &str),
    statuses: bool,
) -> Result<()> {
    let (repository, number, head) = scope;
    let field = if statuses { "statuses" } else { "checks" };
    let query = |repository: &str, number: u32, page: u32, limit: u32, head: &str| {
        let repository = repository.into();
        let head = head.into();
        if statuses {
            Read::PullCommitStatuses {
                repository,
                number,
                page,
                limit,
                head,
            }
        } else {
            Read::PullCheckRuns {
                repository,
                number,
                page,
                limit,
                head,
            }
        }
    };
    prepare(profile)?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "status-page fixture is not empty",
    )?;
    // Generated invalid fixture only, never a successful product login.
    let saved = store.store(account(), Token::new(invalid()?)?).await?;
    let cap = store.capability(&saved.connection).await?;
    for (page, head) in [(1, ""), (2, head)] {
        let reply = runtime
            .github_read(&store, &cap, query(repository, number, page, 1, head))
            .await?;
        ensure(
            reply.status == 401
                && reply.body["error"] == "github_sign_in_required"
                && reply.body.get(field).is_none(),
            "native checks bypassed real GitHub authentication",
        )?;
    }
    let invalid_repository = format!("0{repository}");
    for (repository, number, page, limit, head) in [
        (invalid_repository.as_str(), number, 1, 1, ""),
        (repository, 0, 1, 1, ""),
        (repository, number, 0, 1, ""),
        (repository, number, 1, 51, ""),
        (repository, number, 2, 1, ""),
        (repository, number, 1, 1, "branch"),
    ] {
        ensure(
            matches!(
                runtime
                    .github_read(&store, &cap, query(repository, number, page, limit, head))
                    .await,
                Err(Error::Invalid)
            ),
            "invalid native check descriptor was accepted",
        )?;
    }
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "check read admitted Factory work",
    )?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "status-page fixture cleanup failed",
    )?;
    store.retire();
    runtime.stop()?;
    println!("PASS: production native discovered/pinned {field} pages receive actual GitHub 401; invalid descriptors refuse; Factory remains unbound and real keyring/PG/backend clean up; no authenticated read qualified");
    Ok(())
}
pub(super) async fn run_reviews(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    run_review_pages(
        production,
        payload,
        profile,
        ("10270722", 4493, "5035997116"),
        false,
    )
    .await
}
pub(super) async fn run_review_comments(
    production: &Path,
    payload: &Path,
    profile: &Path,
    repository: &str,
    number: u32,
    id: &str,
) -> Result<()> {
    run_review_pages(production, payload, profile, (repository, number, id), true).await
}
async fn run_review_pages(
    production: &Path,
    payload: &Path,
    profile: &Path,
    scope: (&str, u32, &str),
    inline: bool,
) -> Result<()> {
    let (repository, number, id) = scope;
    let field = if inline { "review_comments" } else { "reviews" };
    let list = |page, limit| {
        if inline {
            Read::PullReviewComments {
                repository: repository.into(),
                number,
                page,
                limit,
            }
        } else {
            Read::PullReviews {
                repository: repository.into(),
                number,
                page,
                limit,
            }
        }
    };
    let detail = |repository: &str, number, id: &str| {
        if inline {
            Read::PullReviewComment {
                repository: repository.into(),
                number,
                comment_id: id.into(),
            }
        } else {
            Read::PullReview {
                repository: repository.into(),
                number,
                review_id: id.into(),
            }
        }
    };
    prepare(profile)?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "review fixture is not empty",
    )?;
    // Generated invalid fixture only; never a verified product account.
    let saved = store.store(account(), Token::new(invalid()?)?).await?;
    let cap = store.capability(&saved.connection).await?;
    for query in [list(1, 1), detail(repository, number, id)] {
        let reply = runtime.github_read(&store, &cap, query).await?;
        ensure(
            reply.status == 401
                && reply.body["error"] == "github_sign_in_required"
                && reply.body.get(field).is_none(),
            "native reviews bypassed actual GitHub authentication",
        )?;
    }
    let invalid_repository = format!("0{repository}");
    let invalid_id = format!("0{id}");
    for query in [
        list(0, 1),
        list(1, 51),
        detail(&invalid_repository, number, id),
        detail(repository, 0, id),
        detail(repository, number, "0"),
        detail(repository, number, &invalid_id),
        detail(repository, number, "9223372036854775808"),
    ] {
        ensure(
            matches!(
                runtime.github_read(&store, &cap, query).await,
                Err(Error::Invalid)
            ),
            "invalid native review descriptor was accepted",
        )?;
    }
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "review read admitted Factory work",
    )?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "review fixture cleanup failed",
    )?;
    store.retire();
    runtime.stop()?;
    println!("PASS: production native {field} list/detail receive actual GitHub 401; invalid descriptors refuse; Factory remains unbound and real keyring/PG/backend clean up; no authenticated review access qualified");
    Ok(())
}
pub(super) async fn run_files(
    production: &Path,
    payload: &Path,
    profile: &Path,
    base: &str,
    head: &str,
) -> Result<()> {
    prepare(profile)?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "file fixture is not empty",
    )?;
    // Generated invalid fixture only; no verified product account or OAuth grant.
    let saved = store.store(account(), Token::new(invalid()?)?).await?;
    let cap = store.capability(&saved.connection).await?;
    for (page, base, head) in [(1, "", ""), (2, base, head)] {
        let reply = runtime
            .github_read(
                &store,
                &cap,
                Read::PullFiles {
                    repository: "10270722".into(),
                    number: 2125,
                    page,
                    limit: 1,
                    base: base.into(),
                    head: head.into(),
                },
            )
            .await?;
        ensure(
            reply.status == 401
                && reply.body["error"] == "github_sign_in_required"
                && reply.body.get("files").is_none(),
            "native files bypassed actual GitHub authentication",
        )?;
    }
    for (repository, number, page, limit, base, head) in [
        ("010270722", 2125, 1, 1, "", ""),
        ("10270722", 0, 1, 1, "", ""),
        ("10270722", 2125, 0, 1, "", ""),
        ("10270722", 2125, 3001, 1, "", ""),
        ("10270722", 2125, 1, 0, "", ""),
        ("10270722", 2125, 1, 11, "", ""),
        ("10270722", 2125, 2, 1, "", ""),
        ("10270722", 2125, 1, 1, base, ""),
        ("10270722", 2125, 1, 1, "branch", head),
    ] {
        ensure(
            matches!(
                runtime
                    .github_read(
                        &store,
                        &cap,
                        Read::PullFiles {
                            repository: repository.into(),
                            number,
                            page,
                            limit,
                            base: base.into(),
                            head: head.into(),
                        }
                    )
                    .await,
                Err(Error::Invalid)
            ),
            "invalid native file descriptor was accepted",
        )?;
    }
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "file read admitted Factory work",
    )?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "file fixture cleanup failed",
    )?;
    store.retire();
    runtime.stop()?;
    println!("PASS: production native discovered/pinned file descriptors receive actual GitHub 401; invalid selectors refuse; Factory stays unbound and real OS credential/PG/backend clean up; authenticated file access remains unqualified");
    Ok(())
}
pub(super) async fn run_diff(
    production: &Path,
    payload: &Path,
    profile: &Path,
    pin: Option<(&str, &str)>,
) -> Result<()> {
    prepare(profile)?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "diff fixture is not empty",
    )?;
    // Generated invalid fixture, never a verified product login.
    let saved = store.store(account(), Token::new(invalid()?)?).await?;
    let cap = store.capability(&saved.connection).await?;
    let query = Read::PullDiff {
        repository: "10270722".into(),
        number: 2125,
        base: pin.map_or("", |(base, _)| base).into(),
        head: pin.map_or("", |(_, head)| head).into(),
    };
    let reply = runtime.github_read(&store, &cap, query).await?;
    ensure(
        reply.status == 401
            && reply.body["error"] == "github_sign_in_required"
            && reply.body.get("diff").is_none(),
        "native diff bypassed actual GitHub authentication",
    )?;
    if pin.is_none() {
        for query in [
            Read::PullDiff {
                repository: "10270722".into(),
                number: 2125,
                base: "a".repeat(40),
                head: "".into(),
            },
            Read::PullDiff {
                repository: "10270722".into(),
                number: 2125,
                base: "a".repeat(40),
                head: "x".repeat(40),
            },
            Read::PullDiff {
                repository: "010270722".into(),
                number: 2125,
                base: "".into(),
                head: "".into(),
            },
            Read::PullDiff {
                repository: "10270722".into(),
                number: 0,
                base: "".into(),
                head: "".into(),
            },
        ] {
            ensure(
                matches!(
                    runtime.github_read(&store, &cap, query).await,
                    Err(Error::Invalid)
                ),
                "invalid native diff descriptor was accepted",
            )?;
        }
    }
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "diff scope admitted Factory work",
    )?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "diff fixture cleanup failed",
    )?;
    store.retire();
    runtime.stop()?;
    println!("PASS: production native {} diff scope receives actual GitHub 401; Factory remains unbound and real OS credential/PG/backend clean up; no authenticated diff qualification", if pin.is_some() { "pinned" } else { "discovered" });
    Ok(())
}
pub(super) async fn run_discussion(
    production: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    prepare(profile)?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "discussion fixture is not empty",
    )?;
    // Generated invalid fixture, never a successfully verified account.
    let saved = store.store(account(), Token::new(invalid()?)?).await?;
    let cap = store.capability(&saved.connection).await?;
    for query in [
        Read::IssueComments {
            repository: "23096959".into(),
            number: 43651,
            page: 1,
            limit: 1,
        },
        Read::PullComments {
            repository: "10270722".into(),
            number: 2125,
            page: 1,
            limit: 1,
        },
    ] {
        let reply = runtime.github_read(&store, &cap, query).await?;
        ensure(
            reply.status == 401
                && reply.body["error"] == "github_sign_in_required"
                && reply.body.get("discussion").is_none(),
            "native conversation bypassed actual GitHub authentication",
        )?;
    }
    for query in [
        Read::IssueComments {
            repository: "23096959".into(),
            number: 43651,
            page: 0,
            limit: 1,
        },
        Read::PullComments {
            repository: "10270722".into(),
            number: 2125,
            page: 1,
            limit: 51,
        },
        Read::IssueComments {
            repository: "23096959".into(),
            number: 0,
            page: 1,
            limit: 1,
        },
        Read::PullComments {
            repository: "010270722".into(),
            number: 2125,
            page: 1,
            limit: 1,
        },
    ] {
        ensure(
            matches!(
                runtime.github_read(&store, &cap, query).await,
                Err(Error::Invalid)
            ),
            "invalid native conversation descriptor was accepted",
        )?;
    }
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "conversation read admitted Factory work",
    )?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "discussion fixture cleanup failed",
    )?;
    store.retire();
    runtime.stop()?;
    println!("PASS: production native issue/PR ordinary conversations receive actual GitHub 401 without comment data; invalid descriptors refuse, Factory stays unbound and real keyring/PG/backend clean up; authenticated conversation access remains unqualified");
    Ok(())
}
pub(super) async fn run_records(
    production: &Path,
    payload: &Path,
    profile: &Path,
    pulls: bool,
) -> Result<()> {
    prepare(profile)?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let ready = runtime.health()?;
    let store = Credentials::open(profile, &ready.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "record fixture is not empty",
    )?;
    // Generated invalid fixture only; never a successfully verified login.
    let saved = store.store(account(), Token::new(invalid()?)?).await?;
    let cap = store.capability(&saved.connection).await?;
    let queries = if pulls {
        [
            Read::Pulls {
                repository: "10270722".into(),
                page: 1,
                limit: 1,
                state: "all".into(),
            },
            Read::Pull {
                repository: "10270722".into(),
                number: 2125,
            },
        ]
    } else {
        [
            Read::Issues {
                repository: "10270722".into(),
                page: 1,
                limit: 1,
                state: "all".into(),
            },
            Read::Issue {
                repository: "10270722".into(),
                number: 1,
            },
        ]
    };
    for query in queries {
        let reply = runtime.github_read(&store, &cap, query).await?;
        ensure(
            reply.status == 401
                && reply.body["error"] == "github_sign_in_required"
                && reply
                    .body
                    .get(if pulls { "pulls" } else { "issues" })
                    .is_none(),
            "native record selector bypassed actual GitHub authentication",
        )?;
    }
    let invalid = if pulls {
        [
            Read::Pulls {
                repository: "10270722".into(),
                page: 1,
                limit: 51,
                state: "all".into(),
            },
            Read::Pulls {
                repository: "10270722".into(),
                page: 1,
                limit: 1,
                state: "merged".into(),
            },
            Read::Pull {
                repository: "010270722".into(),
                number: 2125,
            },
            Read::Pull {
                repository: "10270722".into(),
                number: 0,
            },
        ]
    } else {
        [
            Read::Issues {
                repository: "10270722".into(),
                page: 1,
                limit: 51,
                state: "all".into(),
            },
            Read::Issues {
                repository: "10270722".into(),
                page: 1,
                limit: 1,
                state: "merged".into(),
            },
            Read::Issue {
                repository: "010270722".into(),
                number: 1,
            },
            Read::Issue {
                repository: "10270722".into(),
                number: 0,
            },
        ]
    };
    for query in invalid {
        ensure(
            matches!(
                runtime.github_read(&store, &cap, query).await,
                Err(Error::Invalid)
            ),
            "invalid native record descriptor was accepted",
        )?;
    }
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "native record read admitted Factory work",
    )?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "record fixture cleanup failed",
    )?;
    store.retire();
    runtime.stop()?;
    println!("PASS: production native {} list/detail receive actual GitHub authentication refusal; invalid descriptors refuse, Factory remains unbound and real keyring/PG/backend cleanly retire; no authenticated read qualification", if pulls { "PR" } else { "issue" });
    Ok(())
}
/// Bootstrap-only observation. Never creates a guessed account or saved token.
pub(super) async fn run_verify(
    production: &Path,
    observed: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let original = runtime.health()?;
    let store = Credentials::open(profile, &original.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "bootstrap profile is not empty",
    )?;
    ensure(
        matches!(
            runtime.github_verify(Token::new(invalid()?)?).await,
            Err(Error::Provider(401))
        ),
        "production bootstrap did not return actual GitHub refusal",
    )?;
    ensure(
        store.status().await?.is_empty(),
        "failed verification saved an account",
    )?;
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "bootstrap admitted unbound Factory work",
    )?;
    let stale = runtime.github_verify(Token::new(invalid()?)?);
    runtime.stop()?;
    ensure(
        matches!(stale.await, Err(Error::Unavailable)),
        "retired bootstrap channel remained usable",
    )?;
    store.retire();
    println!("PASS: production native bootstrap receives actual GitHub 401 before any account exists; OS custody stays empty and Factory remains unbound");

    let runtime = NativeSupervisor::start(payload, observed, profile)?;
    let current = runtime.health()?;
    ensure(
        current.profile == original.profile && current.instance != original.instance,
        "bootstrap restart did not rotate native instance",
    )?;
    let store = Credentials::open(profile, &current.profile)?;
    ensure(
        matches!(
            runtime.github_verify(Token::new(invalid()?)?).await,
            Err(Error::Provider(401))
        ) && calls(&io)? == 1
            && fs::read_to_string(io.join("status"))? == "401",
        "unpersisted token did not traverse actual Go SDK/GitHub path",
    )?;
    ensure(
        store.status().await?.is_empty(),
        "observed refusal saved an account",
    )?;
    pause(&io)?;
    let mut request = Box::pin(runtime.github_verify(Token::new(invalid()?)?));
    entered(request.as_mut(), &io).await?;
    drop(request);
    let until = Instant::now() + Duration::from_millis(1500);
    while !io.join("cancelled").exists() {
        ensure(
            Instant::now() < until,
            "bootstrap cancellation did not close real Go request",
        )?;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    ensure(
        store.status().await?.is_empty(),
        "cancelled verification saved an account",
    )?;
    println!("PASS: cancelling unpersisted token verification cancels actual Go request promptly, with no OS account written");
    pause(&io)?;
    let mut request = Box::pin(runtime.github_verify(Token::new(invalid()?)?));
    entered(request.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        matches!(request.await, Err(Error::Unavailable)),
        "retiring runtime released bootstrap result",
    )?;
    ensure(
        store.status().await?.is_empty(),
        "retired verification saved an account",
    )?;
    store.retire();
    println!("PASS: unpersisted token/native PG/backend retirement; successful OAuth verification/storage remains unqualified");
    Ok(())
}
pub(super) async fn run(
    production: &Path,
    observed: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let original = runtime.health()?;
    let store = Credentials::open(profile, &original.profile)?;
    let token = invalid()?;
    let saved = store.store(account(), Token::new(token.clone())?).await?;
    let cap = store.capability(&saved.connection).await?;
    let response = runtime.github_read(&store, &cap, Read::Identity).await?;
    ensure(
        response.status == 401 && response.body["error"] == "github_sign_in_required",
        "production native backend did not preserve actual GitHub authentication refusal",
    )?;
    ensure(
        runtime.product("GET", "/api/factory/identity", &[])?.status == 503,
        "transport silently admitted an unbound Factory account",
    )?;
    let stale = runtime.github_read(&store, &cap, Read::Identity);
    runtime.stop()?;
    ensure(
        matches!(stale.await, Err(Error::Unavailable)),
        "retired runtime channel remained available",
    )?;
    store.retire();
    println!("PASS: production native executable uses real OS credential/Go SDK/GitHub path; invalid credential refused, Factory remains unbound and old channel retires");

    // The observed executable uses the same production RunNative/handler/SDK;
    // its transport observes headers and pauses before forwarding real HTTPS.
    let runtime = NativeSupervisor::start(payload, observed, profile)?;
    let current = runtime.health()?;
    ensure(
        current.profile == original.profile && current.instance != original.instance,
        "native restart did not retain profile/rotate instance",
    )?;
    let store = Credentials::open(profile, &current.profile)?;
    let cap = store.capability(&saved.connection).await?;
    let response = runtime
        .github_read(&store, &cap, Read::Repositories { page: 1, limit: 1 })
        .await?;
    ensure(
        response.status == 401
            && response.body["error"] == "github_sign_in_required"
            && calls(&io)? == 1,
        "native catalogue fell back to public data or lost forwarded credential",
    )?;
    let before = calls(&io)?;
    ensure(
        matches!(
            runtime
                .github_read(&store, &cap, Read::Repositories { page: 1, limit: 51 })
                .await,
            Err(Error::Invalid)
        ) && calls(&io)? == before,
        "invalid native selector reached GitHub",
    )?;
    let fountain = store
        .store(
            Account::new(Provider::Fountain, "https://example.invalid", "fixture")?,
            Token::new(invalid()?)?,
        )
        .await?;
    let wrong = store.capability(&fountain.connection).await?;
    ensure(
        matches!(
            runtime.github_read(&store, &wrong, Read::Identity).await,
            Err(Error::Invalid)
        ) && calls(&io)? == before,
        "Fountain credential forwarded to GitHub",
    )?;

    pause(&io)?;
    let mut request = Box::pin(runtime.github_read(&store, &cap, Read::Identity));
    entered(request.as_mut(), &io).await?;
    let other = Credentials::open(profile, &current.profile)?;
    let rotated = other.store(account(), Token::new(invalid()?)?).await?;
    ensure(
        rotated.connection == saved.connection,
        "real reauthentication changed connection identity",
    )?;
    mark(&io.join("release"))?;
    ensure(
        matches!(
            request.await,
            Err(Error::Credential(CredentialError::Retired))
        ),
        "Go reply survived actual OS credential replacement",
    )?;
    ensure(
        fs::read_to_string(io.join("status"))? == "401",
        "rotation did not forward to actual GitHub",
    )?;
    println!("PASS: actual Go HTTP request carries the native credential; independent keyring rotation refuses its eventual real GitHub reply");

    let cap = store.capability(&saved.connection).await?;
    pause(&io)?;
    let mut request = Box::pin(runtime.github_read(&store, &cap, Read::Identity));
    entered(request.as_mut(), &io).await?;
    store.withdraw(&saved.connection).await?;
    mark(&io.join("release"))?;
    ensure(
        matches!(
            request.await,
            Err(Error::Credential(CredentialError::Withdrawn))
        ),
        "Go reply survived actual OS credential withdrawal",
    )?;
    ensure(
        fs::read_to_string(io.join("status"))? == "401",
        "withdrawal did not observe actual GitHub reply",
    )?;
    let before = calls(&io)?;
    ensure(
        matches!(
            runtime.github_read(&store, &cap, Read::Identity).await,
            Err(Error::Credential(CredentialError::Withdrawn))
        ) && calls(&io)? == before,
        "withdrawn credential started another provider request",
    )?;

    let saved = store.store(account(), Token::new(invalid()?)?).await?;
    let cap = store.capability(&saved.connection).await?;
    pause(&io)?;
    let mut request = Box::pin(runtime.github_read(&store, &cap, Read::Identity));
    entered(request.as_mut(), &io).await?;
    drop(request);
    let until = Instant::now() + Duration::from_millis(1500);
    while !io.join("cancelled").exists() {
        ensure(
            Instant::now() < until,
            "Rust cancellation did not close IPC/cancel actual Go request promptly",
        )?;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    println!("PASS: dropping native request closes Unix HTTP and cancels the actual Go SDK transport before its server deadline");

    pause(&io)?;
    let mut request = Box::pin(runtime.github_read(&store, &cap, Read::Identity));
    entered(request.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        matches!(request.await, Err(Error::Unavailable)),
        "retiring native runtime released a provider result",
    )?;
    store.withdraw(&saved.connection).await?;
    store.withdraw(&fountain.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "native transport fixture credential cleanup failed",
    )?;
    println!("PASS: real native keyring/Rust IPC/Go/GitHub transport, scope/refusal/rotation/withdrawal/cancellation and native PG/backend retirement; no OAuth/private-read qualification");
    Ok(())
}
