//! Real native supervisor/keyring/Go with the production Rust GitHub connector.
//! Actual-provider negatives and dummy-only declared HTTPS stay separate.
use super::native_github_fixture::entered;
use super::{ensure, fixture, prepare, Result};
use soda_desktop_runtime::credentials::{
    Account, CredentialState, Credentials, Error as CredentialError, Provider, Token,
};
use soda_desktop_runtime::github::{Error, Read};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, Instant};

pub(super) async fn run_assembly_budget_declared(
    production: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    use super::native_github_fixture::{call, set_mode};
    use serde_json::json;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    set_mode(&io, "assembly-positive")?;
    let attempts = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("attempts"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    ensure(attempts()? == 0, "unselected startup contacted provider")?;
    let store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "assembly fixture requires empty custody",
    )?;
    let saved = store
        .store(
            Account::new(Provider::Github, "https://github.com", "321")?,
            Token::new(b"factory-declared-thread".to_vec())?,
        )
        .await?;
    let catalogue = call(
        &runtime,
        &store,
        &saved.connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "assembly catalogue refused")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?;
    let mut stages = Vec::new();
    for target in [
        format!("/api/repositories/{repository}/factory"),
        format!("/api/repositories/{repository}/factory/access"),
        format!("/api/repositories/{repository}/factory/issues/progress?issues=1,2"),
        format!("/api/factory/feed?repositories={repository}"),
    ] {
        let before = attempts()?;
        let reply = call(&runtime, &store, &saved.connection, "GET", &target, &[]).await?;
        ensure(
            reply.0 == 200,
            &format!(
                "assembly navigation refused {target}: {} {}",
                reply.0, reply.1
            ),
        )?;
        stages.push(json!({"path":target,"attempts":attempts()? - before}));
    }
    let navigation = attempts()?;
    ensure(
        navigation <= 60,
        "startup/navigation exceeded declared request bound",
    )?;
    tokio::time::sleep(Duration::from_secs(21)).await;
    ensure(
        attempts()? == navigation,
        "unfunded idle runtime polled provider",
    )?;
    for scenario in [
        "assembly-positive",
        "assembly-fork",
        "assembly-head-change",
        "assembly-withdrawn",
    ] {
        set_mode(&io, scenario)?;
        let before = attempts()?;
        let result = runtime
            .github_read(
                &store,
                &store.capability(&saved.connection).await?,
                Read::MergeReadiness {
                    repository: "123".into(),
                    number: 7,
                    head: "a".repeat(40),
                    base: "b".repeat(40),
                },
            )
            .await?;
        let count = attempts()? - before;
        ensure(
            count <= 64,
            "merge compound read exceeded one native operation",
        )?;
        if matches!(scenario, "assembly-positive" | "assembly-fork") {
            ensure(
                result.status == 200,
                &format!(
                    "merge compound refused {scenario}: {} {}",
                    result.status, result.body
                ),
            )?;
            ensure(
                result.body["payload"]["preparation"]["head"] == "a".repeat(40)
                    || result.body["merge_readiness"]["preparation"]["head"] == "a".repeat(40),
                "merge compound lost exact head",
            )?;
        } else {
            ensure(
                matches!(result.status, 403 | 409),
                "changed/withdrawn merge exposed material",
            )?;
        }
        stages.push(json!({"phase":scenario,"attempts":count,"status":result.status}));
    }
    store.withdraw(&saved.connection).await?;
    runtime.stop()?;
    ensure(
        !io.join("unexpected-request").exists(),
        "assembly peer saw undeclared request",
    )?;
    fs::write(
        root.join("assembly-budget.json"),
        serde_json::to_vec_pretty(
            &json!({"stages":stages,"navigation_attempts":navigation,"idle_seconds":21,"idle_attempts":0,"declared_no_forwarding":true,"policy_or_funding_granted":false,"live_merge_qualified":false}),
        )?,
    )?;
    println!("PASS: native/keyring/Go/PG startup/navigation, idle and paired merge-readiness physical-send transcript; no live provider, inference or effect");
    Ok(())
}

// The exact production budget and private-file owner are compiled into this
// existing check. Synthetic monotonic seconds exercise admission without a
// keyring, network request, provider response or wall-clock expiry wait.
pub(super) fn run_budget_declared(profile: &Path) -> Result<()> {
    use super::traffic_budget::Budget;
    const NOW: u64 = 1_800_000_000;
    ensure(
        profile.is_absolute()
            && profile.starts_with("/home")
            && !profile.exists()
            && profile.parent().is_some_and(Path::is_dir),
        "fresh owned /home budget profile required",
    )?;
    super::private_fs::private_dir(profile)?;
    let budget = Budget::memory();
    for attempt in 0..600 {
        // Responses deliberately remain unobserved: failed/unknown attempts
        // must consume the same pre-send capacity as successful responses.
        ensure(
            budget.admit(NOW, attempt % 2 == 0)?.is_none(),
            "local wire ceiling refused one of its first 600 attempts",
        )?;
    }
    budget.observe(
        NOW,
        true,
        Some(5000),
        Some(4999),
        Some(NOW + 3600),
        None,
        false,
    )?;
    let held = budget
        .admit(NOW, false)?
        .ok_or("601st wire attempt admitted")?;
    let snapshot = budget.snapshot(NOW)?;
    ensure(
        snapshot.ceiling == 600
            && snapshot.used == 600
            && snapshot.reset == NOW + 3600
            && held.reset == snapshot.reset
            && snapshot.suppressed == 1,
        "wire ceiling did not retain every attempt or its exact next slot",
    )?;

    let rolling = Budget::memory();
    for now in [NOW, NOW + 10] {
        for _ in 0..300 {
            ensure(rolling.admit(now, true)?.is_none(), "rolling setup refused")?;
        }
    }
    let partial = rolling.snapshot(NOW + 3600)?;
    ensure(
        partial.used == 300 && partial.reset == NOW + 3610,
        "rolling expiry reset all attempts or retained expired attempts",
    )?;
    for _ in 0..300 {
        ensure(
            rolling.admit(NOW + 3600, true)?.is_none(),
            "rolling expiry did not release only the elapsed capacity",
        )?;
    }
    ensure(
        rolling.admit(NOW + 3600, true)?.is_some() && rolling.snapshot(NOW + 3610)?.used == 300,
        "rolling window exceeded its ceiling or used a fixed-hour reset",
    )?;

    let reserve = Budget::memory();
    reserve.observe(
        NOW,
        true,
        Some(5000),
        Some(1001),
        Some(NOW + 90),
        None,
        false,
    )?;
    ensure(
        reserve.admit(NOW, true)?.is_none(),
        "reserve held above 20 percent",
    )?;
    reserve.observe(
        NOW + 1,
        true,
        Some(5000),
        Some(1000),
        Some(NOW + 90),
        None,
        false,
    )?;
    ensure(
        reserve
            .admit(NOW + 1, true)?
            .is_some_and(|hold| hold.reset == NOW + 90)
            && reserve.admit(NOW + 1, false)?.is_none()
            && reserve.snapshot(NOW + 89)?.provider_reset == NOW + 90
            && reserve.admit(NOW + 90, true)?.is_none()
            && reserve.snapshot(NOW + 90)?.provider_reset == 0,
        "authenticated provider reserve threshold, scope or expiry differed",
    )?;
    reserve.observe(NOW + 91, true, None, None, None, Some(12), true)?;
    ensure(
        reserve
            .admit(NOW + 91, true)?
            .is_some_and(|hold| hold.reset == NOW + 103)
            && reserve.admit(NOW + 91, false)?.is_none()
            && reserve.admit(NOW + 103, true)?.is_none(),
        "provider Retry-After hold did not preserve its scope and deadline",
    )?;
    let unauthenticated = Budget::memory();
    let exhausted = Budget::memory();
    exhausted.observe(NOW, true, None, Some(0), Some(NOW + 30), None, false)?;
    ensure(
        exhausted.admit(NOW, true)?.is_some_and(|hold| hold.reset == NOW + 30),
        "known exhausted provider quota did not hold without its limit header",
    )?;
    unauthenticated.observe(
        NOW,
        false,
        Some(5000),
        Some(0),
        Some(NOW + 90),
        Some(12),
        true,
    )?;
    ensure(
        unauthenticated.admit(NOW, true)?.is_none(),
        "unauthenticated response acquired authenticated account authority",
    )?;

    let concurrent = profile.join("concurrent");
    super::private_fs::private_dir(&concurrent)?;
    // The full ceiling is exercised above in memory. Seed only this declared
    // private history so the competing-owner proof needs two durable debits.
    super::private_fs::write_bytes(
        &concurrent.join("github-wire-budget.json"),
        &serde_json::to_vec(&serde_json::json!({
            "last": NOW, "attempts": vec![NOW; 598], "provider_until": 0
        }))?,
    )?;
    fn available<T>(
        until: Instant,
        mut read: impl FnMut() -> std::io::Result<T>,
    ) -> std::io::Result<T> {
        loop {
            match read() {
                Err(error)
                    if error.kind() == std::io::ErrorKind::WouldBlock && Instant::now() < until =>
                {
                    std::thread::yield_now();
                }
                result => return result,
            }
        }
    }
    let until = Instant::now() + Duration::from_secs(15);
    let (admitted, suppressed) = std::thread::scope(|scope| -> std::io::Result<(usize, u64)> {
        let owners: Vec<_> = (0..2)
            .map(|_| {
                let concurrent = &concurrent;
                scope.spawn(move || -> std::io::Result<(usize, u64)> {
                    let owner = available(until, || Budget::retained(concurrent))?;
                    let mut admitted = 0;
                    for attempt in 0..2 {
                        admitted += usize::from(
                            available(until, || owner.admit(NOW, attempt % 2 == 0))?.is_none(),
                        );
                    }
                    Ok((
                        admitted,
                        available(until, || owner.snapshot(NOW))?.suppressed,
                    ))
                })
            })
            .collect();
        owners.into_iter().try_fold((0, 0), |sum, owner| {
            owner
                .join()
                .map_err(|_| std::io::Error::other("budget owner panicked"))?
                .map(|count| (sum.0 + count.0, sum.1 + count.1))
        })
    })?;
    let reopened = Budget::retained(&concurrent)?;
    let snapshot = reopened.snapshot(NOW)?;
    ensure(
        admitted == 2 && snapshot.used == 600 && suppressed == 2,
        "independent profile owners or reopen multiplied the wire allowance",
    )?;
    // There is no token/account parameter in the profile journal: reopening
    // as a replacement credential owner cannot reset the captured attempts.
    ensure(
        reopened.admit(NOW, true)?.is_some()
            && Budget::retained(&concurrent)?.snapshot(NOW)?.used == 600
            && reopened.admit(NOW + 3600, true)?.is_none(),
        "profile restart reset capacity or rolling expiry remained fenced",
    )?;
    let interrupted = profile.join("interrupted");
    super::private_fs::private_dir(&interrupted)?;
    let original = Budget::retained(&interrupted)?;
    ensure(
        original.admit(NOW, true)?.is_none(),
        "interrupted setup refused",
    )?;
    original.observe(
        NOW,
        true,
        Some(5000),
        Some(1000),
        Some(NOW + 30),
        None,
        false,
    )?;
    let journal = interrupted.join("github-wire-budget.json");
    super::private_fs::write_bytes(
        &interrupted.join("github-wire-budget.json.pending"),
        &super::private_fs::read_bytes(&journal, 24 * 1024)?,
    )?;
    fs::remove_file(&journal)?;
    let recovered = Budget::retained(&interrupted)?;
    let recovered_state = recovered.snapshot(NOW)?;
    ensure(
        recovered_state.used == 1
            && recovered_state.provider_reset == NOW + 30
            && recovered.admit(NOW, true)?.is_some()
            && !interrupted.join("github-wire-budget.json.pending").exists(),
        "interrupted journal replacement lost its original debit or provider hold",
    )?;
    fs::remove_file(&journal)?;
    ensure(
        Budget::retained(&interrupted).is_err(),
        "initialized profile with a missing journal received fresh authority",
    )?;
    let failed_hold = profile.join("failed-hold");
    super::private_fs::private_dir(&failed_hold)?;
    let owner = Budget::retained(&failed_hold)?;
    ensure(
        owner.admit(NOW, true)?.is_none(),
        "hold-failure setup refused",
    )?;
    let pending = failed_hold.join("github-wire-budget.json.pending");
    super::private_fs::write_bytes(&pending, b"{not-json")?;
    ensure(
        owner
            .observe(NOW, true, None, None, None, Some(30), true)
            .is_err()
            && owner.admit(NOW, true).is_err(),
        "failed provider-hold persistence retained send authority",
    )?;
    // Remove only this injected fault: the original owner must remain fenced
    // even when its unchanged canonical journal becomes readable again.
    fs::remove_file(&pending)?;
    let fenced = owner.snapshot(NOW)?;
    ensure(
        !fenced.available && fenced.used == 1 && owner.admit(NOW, true).is_err(),
        "persistence failure did not retain the owner's unavailable status",
    )?;
    let malformed = profile.join("malformed");
    super::private_fs::private_dir(&malformed)?;
    super::private_fs::write_bytes(&malformed.join("github-wire-budget.json"), b"{not-json")?;
    ensure(
        Budget::retained(&malformed).is_err(),
        "malformed retained budget silently reset wire authority",
    )?;
    println!("PASS: exact production GitHub budget admits 600 total attempts, holds the 601st, counts unobserved attempts, expires individual rolling slots, enforces authenticated 20-percent/Retry-After holds, and preserves one private profile ceiling across competing owners/reopen/interrupted replacement; failed hold persistence fences the owner, and missing/malformed journals refuse. Cross-owner history starts from 598 declared debits. Synthetic time and responses; no keyring, network, provider, model or mutation effects.");
    Ok(())
}

// Bounded provider producer check. This uses only dummy credentials and the
// launcher's process-local, no-forwarding HTTPS fixture. No product capability,
// durable reconciliation or actual GitHub mutation qualification is implied.
pub(super) async fn run_comment_declared(profile: &Path) -> Result<()> {
    use serde_json::json;
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::{Comment, CommentOutcome, Owner};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent missing")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let mode = |value: &str| -> Result<()> {
        fs::write(io.join("mode"), value)?;
        Ok(())
    };
    let token = || Token::new(b"factory-declared-comment".to_vec());
    let comment = || Comment {
        repository: "123".into(),
        subject: "issue".into(),
        subject_id: "700".into(),
        number: 7,
        body: "Human comment π 🍵\n[link](relative.md)\n".repeat(512),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "positive",
        "pull",
        "max-unicode",
        "max-control",
        "big-comment-id",
        "repository-slot-reused",
    ] {
        mode(name)?;
        let mut c = comment();
        if name == "pull" {
            c.subject = "pull".into();
        }
        if name == "max-unicode" {
            c.body = "🍵".repeat(16384);
        }
        if name == "max-control" {
            c.body = "\u{0001}".repeat(65536);
        }
        let CommentOutcome::Created(value) = client.create_comment(token()?, "321", &c).await
        else {
            return Err("declared comment creation refused".into());
        };
        ensure(
            value["comment"]["external_id"]
                == if name == "big-comment-id" {
                    "5000000000"
                } else {
                    "900"
                }
                && value["comment"].get("body").is_none()
                && value["comment"]["body_sha256"]
                    == Sha256::digest(c.body.as_bytes())
                        .iter()
                        .map(|byte| format!("{byte:02x}"))
                        .collect::<String>()
                && value["comment"]["author"]["external_id"] == "321"
                && value["repository"] == "123"
                && value["subject_id"] == "700"
                && value["subject"] == c.subject,
            "declared comment lost response scope or human writing",
        )?;
        ensure(
            value["provider_response"]["status"] == 200
                && value["provider_response"]["protocol"] == "github_graphql_add_comment"
                && value["provider_response"]["request_id"] == "DECLARED:COMMENT:201"
                && value["provider_response"]["response_sha256"]
                    .as_str()
                    .is_some_and(|s| s.len() == 64)
                && serde_json::to_vec(&value)?.len() <= 4096,
            "native comment lost bounded causal POST evidence",
        )?;
        fs::write(
            root.join(format!("{name}-receipt.json")),
            serde_json::to_vec(&value)?,
        )?;
        cases.push(name);
    }
    for (name, status) in [
        ("identity-change", 409),
        ("identity-denied", 401),
        ("subject-change", 409),
        ("subject-kind", 409),
        ("locked", 403),
        ("lock-unknown", 403),
        ("repository-change", 409),
        ("archived", 403),
        ("disabled", 403),
        ("readonly-unknown", 403),
        ("missing-actor-node", 502),
        ("missing-subject-node", 502),
    ] {
        mode(name)?;
        let before = fs::read_to_string(io.join("posts"))?;
        let CommentOutcome::NotSent(failure) =
            client.create_comment(token()?, "321", &comment()).await
        else {
            return Err("declared comment preflight did not refuse before dispatch".into());
        };
        ensure(
            failure.status == status && fs::read_to_string(io.join("posts"))? == before,
            "declared preflight failure dispatched or lost status",
        )?;
        cases.push(name);
    }
    mode("positive")?;
    for i in 0..8 {
        let mut c = comment();
        match i {
            0 => c.repository = "0123".into(),
            1 => c.subject_id = "0700".into(),
            2 => c.subject = "review".into(),
            3 => c.number = 0,
            4 => c.body = " ".into(),
            5 => c.body = "\0".into(),
            6 => c.body = "x".repeat(65537),
            _ => c.number = u64::MAX,
        }
        let before = fs::read_to_string(io.join("gets"))?;
        ensure(
            matches!(client.create_comment(token()?,"321",&c).await, CommentOutcome::NotSent(f) if f.status==400),
            "invalid semantic comment accepted",
        )?;
        ensure(
            fs::read_to_string(io.join("gets"))? == before,
            "invalid comment reached network",
        )?;
    }
    for (name, status) in [
        ("denied", 403),
        ("limited", 429),
        ("validation", 400),
        ("server-error", 503),
        ("redirect", 409),
        ("unexpected-status", 503),
        ("malformed", 502),
        ("wrong-url", 502),
        ("wrong-parent", 502),
        ("wrong-author", 502),
        ("wrong-body", 502),
        ("oversized", 413),
        ("receipt-oversized", 413),
        ("request-id-oversized", 502),
        ("request-id-duplicate", 502),
        ("graphql-errors", 502),
        ("partial-errors", 502),
        ("wrong-subject-type", 502),
        ("missing-full-database-id", 502),
        ("drop-reply", 503),
        ("deadline", 504),
    ] {
        mode(name)?;
        let before = fs::read_to_string(io.join("posts"))?.parse::<u64>()?;
        let CommentOutcome::Indeterminate(failure) =
            client.create_comment(token()?, "321", &comment()).await
        else {
            return Err("post-dispatch failure became a safe retry or success".into());
        };
        ensure(
            failure.status == status,
            "declared uncertain comment lost failure class",
        )?;
        ensure(
            fs::read_to_string(io.join("posts"))?.parse::<u64>()? == before + 1,
            "comment POST retried or redirected",
        )?;
        cases.push(name);
    }
    mode("serialize")?;
    let a = comment();
    let b = comment();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.create_comment(token()?, "321", &a),
        clone.create_comment(token()?, "321", &b)
    );
    ensure(
        matches!(first, CommentOutcome::Created(_)) && matches!(second, CommentOutcome::Created(_)),
        "serialized comment calls failed",
    )?;
    ensure(
        !io.join("overlap").exists() && !io.join("too-fast").exists(),
        "comment writes overlap or ignore pacing",
    )?;
    mode("pause-post")?;
    let c = comment();
    let pending_token = token()?;
    let pending = async { Ok::<_, &str>(client.create_comment(pending_token, "321", &c).await) };
    tokio::pin!(pending);
    entered(pending.as_mut(), &io).await?;
    let start = Instant::now();
    owner.stop()?;
    ensure(
        matches!(pending.await?, CommentOutcome::Indeterminate(_))
            && start.elapsed() < Duration::from_secs(2),
        "owner retirement lost uncertain in-flight effect or blocked",
    )?;
    ensure(
        matches!(
            client.create_comment(token()?, "321", &comment()).await,
            CommentOutcome::NotSent(_)
        ),
        "retired owner admitted a comment",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "comment fixture observed an unexpected request",
    )?;
    fs::write(
        root.join("comment-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_descriptors":8,"serialization":true,"in_flight_retirement":"indeterminate","actual_provider":false,"product_capability_enabled":false}),
        )?,
    )?;
    println!("PASS: production Rust comment producer preserves writing/scope, rejects preflight conflicts, serializes/paces one-shot POSTs, fences post-dispatch failures and retires its in-flight transport; dummy-only HTTPS, no actual GitHub write or product admission qualification");
    Ok(())
}

// Runs the existing native/keyring/Go/PG integration against an explicitly
// declared HTTPS provider fixture. The launcher supplies process-local trust
// and a loopback-only proxy. Never use a retained account or real grant here.
pub(super) async fn run_evidence_declared(
    production: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    use serde_json::{json, Value};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent missing")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let mode = |value: &str| -> Result<()> {
        fs::write(io.join("mode"), value)?;
        Ok(())
    };
    mode("positive-start")?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "declared evidence requires empty credential fixture",
    )?;
    let saved = store
        .store(
            account(),
            Token::new(b"factory-declared-evidence".to_vec())?,
        )
        .await?;
    let query = || Read::PullProtection {
        repository: "123".into(),
        number: 7,
        head: "a".repeat(40),
    };
    let mut cases = Vec::new();
    for name in [
        "positive",
        "empty",
        "hidden-classic",
        "hidden-rules",
        "legacy",
        "unknown-rule",
        "unknown-parameter",
    ] {
        mode(name)?;
        let cap = store.capability(&saved.connection).await?;
        let reply = runtime.github_read(&store, &cap, query()).await?;
        ensure(
            reply.status == 200,
            &format!("declared {name} protection refused: {}", reply.status),
        )?;
        let p = &reply.body["protection"]["summary"];
        ensure(
            reply.body["protection"]["head"] == "a".repeat(40)
                && reply.body["protection"]["base"] == "b".repeat(40)
                && reply.body["protection"]["branch"] == "main",
            "declared protection lost scope",
        )?;
        match name {
            "positive" => ensure(
                p["requirements_complete"] == true
                    && p["required_approvals"] == 3
                    && p["require_up_to_date"] == true
                    && p["require_code_owner_reviews"] == true
                    && p["require_last_push_approval"] == true
                    && p["require_signed"] == true
                    && p["required_contexts"].is_null()
                    && p["required_checks"].as_array().is_some_and(|v| {
                        v.len() == 3
                            && v.iter().any(|c| c["context"] == "build" && c["app"] == "7")
                            && v.iter().any(|c| c["context"] == "build" && c["app"] == "9")
                            && v.iter()
                                .any(|c| c["context"] == "lint" && c["source"] == "any")
                    }),
                "declared classic/rules lost app or combined constraints",
            )?,
            "empty" => ensure(
                p["requirements_complete"] == true
                    && p["status_checks"] == false
                    && p["required_contexts"] == json!([])
                    && p["required_approvals"] == 0,
                "declared empty requirements differ",
            )?,
            "hidden-classic" | "hidden-rules" => ensure(
                p["requirements_complete"] == false
                    && p["required_approvals"].is_null()
                    && p["required_contexts"].is_null(),
                "declared hidden source became complete or zero",
            )?,
            "legacy" => ensure(
                p["requirements_complete"] == false
                    && p["required_checks"].as_array().is_some_and(|v| {
                        v.iter()
                            .any(|c| c["context"] == "build" && c["source"] == "unknown")
                    }),
                "legacy context invented an app binding",
            )?,
            _ => ensure(
                p["requirements_complete"] == false
                    && p["unknown_rule_kinds"]
                        .as_array()
                        .is_some_and(|v| !v.is_empty()),
                "unrepresented rule or parameter became complete",
            )?,
        }
        cases.push(name);
    }
    for (name, status) in [
        ("classic-change", 409),
        ("rules-change", 409),
        ("branch-change", 409),
        ("head-change", 409),
        ("base-change", 409),
        ("identity-change", 409),
        ("repository-change", 404),
        ("revoked", 401),
        ("oversized", 413),
        ("limited", 429),
    ] {
        mode(name)?;
        let cap = store.capability(&saved.connection).await?;
        let reply = runtime.github_read(&store, &cap, query()).await?;
        ensure(
            reply.status == status && reply.body.get("protection").is_none(),
            &format!("declared {name} did not refuse: {}", reply.status),
        )?;
        cases.push(name);
        if name == "limited" {
            let before = fs::read_to_string(io.join("gets"))?;
            mode("positive")?;
            let cap = store.capability(&saved.connection).await?;
            let held = runtime.github_read(&store, &cap, query()).await?;
            ensure(held.status == 429 && fs::read_to_string(io.join("gets"))? == before,
                "rate-limited read retried HTTPS before its reset")?;
        }
    }
    // The following independent provider scenarios begin in a new native
    // owner. A retained login does not renew a rate hold or any write command.
    runtime.stop()?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    // The same facts must survive the real canonical consumer, not merely the
    // defining provider decoder. Each request admits fresh native custody.
    mode("positive-canonical")?;
    let request = runtime.admit_product(
        store.clone(),
        store.capability(&saved.connection).await?,
        Some("123".into()),
    )?;
    let catalog = request
        .request("GET", "/api/factory/collaboration/repositories", &[])
        .await?;
    let catalog: Value = serde_json::from_slice(&catalog.body)?;
    let repository = catalog["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository missing")?;
    let target =
        format!("/api/factory/collaboration/repositories/{repository}/pulls/7/checks?limit=1");
    let mut status_cursor = String::new();
    for name in [
        "positive-product",
        "empty-product",
        "hidden-classic-product",
        "unknown-rule-product",
    ] {
        mode(name)?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&saved.connection).await?,
            Some("123".into()),
        )?;
        let response = request.request("GET", &target, &[]).await?;
        ensure(
            response.status == 200,
            &format!("declared canonical {name} refused: {}", response.status),
        )?;
        let body: Value = serde_json::from_slice(&response.body)?;
        let p = &body["protection"];
        ensure(
            body["pull"]["repository"] == repository
                && body["commit"] == "a".repeat(40)
                && body["status_gate"] == if name == "empty-product" { "not_required" } else { "unknown" }
                && p["repository"] == repository
                && p["branch"] == "main"
                && p["observation"]["state"] == "available",
            "canonical protection lost identity or invented gate",
        )?;
        if name == "positive-product" {
            ensure(
                p["requirements_complete"] == true
                    && p["required_contexts"].is_null()
                    && p["required_checks"]
                        .as_array()
                        .is_some_and(|v| v.len() == 3)
                    && body["checks"]["items"][0]["source_app"] == "7"
                    && body["checks"]["items"][0]["required"].is_null(),
                "canonical app-qualified requirements changed",
            )?;
            status_cursor = body["checks"]["next"]
                .as_str()
                .filter(|s| !s.is_empty())
                .ok_or("status continuation missing")?
                .into();
        }
        if name == "empty-product" {
            ensure(
                p["requirements_complete"] == true && p["required_approvals"] == 0,
                "canonical empty policy changed",
            )?;
        }
        if name == "hidden-classic-product" || name == "unknown-rule-product" {
            ensure(
                p["requirements_complete"] == false && p["required_contexts"].is_null(),
                "canonical partial policy became complete",
            )?;
        }
        cases.push(name);
    }
    for name in [
        "classic-change-product",
        "rules-change-product",
        "branch-change-product",
        "head-change-product",
        "base-change-product",
        "base-change-between-product",
        "base-change-empty-product",
        "identity-change-product",
        "repository-change-product",
        "revoked-product",
        "oversized-product",
    ] {
        mode(name)?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&saved.connection).await?,
            Some("123".into()),
        )?;
        if let Ok(response) = request.request("GET", &target, &[]).await {
            let body: Value = serde_json::from_slice(&response.body)?;
            ensure(
                response.status != 200 && body.get("protection").is_none(),
                &format!("canonical {name} released partial/cached protection"),
            )?;
        }
        cases.push(name);
    }
    for name in ["cursor-base-change-product", "cursor-branch-change-product"] {
        mode(name)?;
        let target = format!("{target}&head={}&cursor={}", "a".repeat(40), status_cursor);
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&saved.connection).await?,
            Some("123".into()),
        )?;
        let response = request.request("GET", &target, &[]).await?;
        ensure(
            response.status == 409,
            "status continuation ignored changed base or branch",
        )?;
        cases.push(name);
    }
    let jobs_target =
        format!("/api/factory/collaboration/repositories/{repository}/pulls/7/checks/jobs?limit=1");
    mode("jobs-normal-product")?;
    let (status, first) =
        declared_product(&runtime, &store, &saved.connection, &jobs_target).await?;
    ensure(
        status == 200
            && first["kind"] == "job"
            && first["commit"] == "a".repeat(40)
            && first["status_gate"] == "unknown"
            && first["checks"]["items"][0]["job"]["run_id"] == "100"
            && first["checks"]["items"][0]["job"]["attempt"] == "1"
            && first["checks"]["items"][0]["required"].is_null(),
        "canonical declared workflow job lost scope or invented authority",
    )?;
    let cursor = first["checks"]["next"]
        .as_str()
        .filter(|s| !s.is_empty())
        .ok_or("job continuation missing")?;
    let continued = format!("{jobs_target}&head={}&cursor={cursor}", "a".repeat(40));
    let (status, next) = declared_product(&runtime, &store, &saved.connection, &continued).await?;
    ensure(
        status == 200
            && next["checks"]["complete"] == true
            && next["checks"]["items"][0]["external_id"]
                != first["checks"]["items"][0]["external_id"],
        "job continuation did not progress to completion",
    )?;
    cases.push("jobs-normal-product");
    let mut missed = Vec::new();
    for name in [
        "jobs-job-count-change-product",
        "jobs-repeat-product",
        "jobs-base-change-product",
        "jobs-branch-change-product",
        "jobs-run-count-change-product",
        "jobs-run-replaced-product",
        "jobs-attempt-change-product",
    ] {
        mode(name)?;
        let (status, body) =
            declared_product(&runtime, &store, &saved.connection, &continued).await?;
        if status != 409 || body.get("checks").is_some() {
            missed.push(name);
        } else {
            cases.push(name);
        }
    }
    {
        use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
        let original: Value = serde_json::from_slice(&URL_SAFE_NO_PAD.decode(cursor)?)?;
        for (field, value) in [
            ("repository", json!("124")),
            ("number", json!(8)),
            ("head", json!("c".repeat(40))),
            ("limit", json!(2)),
        ] {
            mode("jobs-cursor-product")?;
            let mut wrong = original.clone();
            wrong[field] = value;
            let wrong = URL_SAFE_NO_PAD.encode(serde_json::to_vec(&wrong)?);
            let (status, body) = declared_product(
                &runtime,
                &store,
                &saved.connection,
                &format!("{jobs_target}&head={}&cursor={wrong}", "a".repeat(40)),
            )
            .await?;
            ensure(
                status == 400 && body.get("checks").is_none(),
                "job cursor escaped captured scope",
            )?;
        }
        cases.push("jobs-cursor-scope-product");
    }
    for name in [
        "jobs-empty-product",
        "jobs-no-jobs-product",
        "jobs-empty-first-product",
    ] {
        mode(name)?;
        let (status, body) =
            declared_product(&runtime, &store, &saved.connection, &jobs_target).await?;
        ensure(
            status == 200
                && body["checks"]["items"] == json!([])
                && body["status_gate"] == "unknown",
            "empty workflow observation invented a job or gate",
        )?;
        if name == "jobs-empty-first-product" {
            let cursor = body["checks"]["next"]
                .as_str()
                .filter(|s| !s.is_empty())
                .ok_or("empty job run lost successor")?;
            let target = format!("{jobs_target}&head={}&cursor={cursor}", "a".repeat(40));
            let (status, next) =
                declared_product(&runtime, &store, &saved.connection, &target).await?;
            ensure(
                status == 200
                    && next["checks"]["complete"] == true
                    && next["checks"]["items"][0]["job"]["run_id"] == "101",
                "empty job run did not continue to another run",
            )?;
            mode("jobs-multi-reordered-product")?;
            let (status, next) =
                declared_product(&runtime, &store, &saved.connection, &target).await?;
            if status != 409 || next.get("checks").is_some() {
                missed.push("jobs-multi-reordered-product");
            } else {
                cases.push("jobs-multi-reordered-product");
            }
        } else {
            ensure(
                body["checks"]["complete"] == true && body["checks"]["next"] == "",
                "empty workflow result did not end",
            )?;
        }
        cases.push(name);
    }
    for (name, expected) in [
        ("jobs-attempt-during-product", 409),
        ("jobs-list-head-product", 502),
        ("jobs-bad-run-product", 502),
        ("jobs-bad-head-product", 502),
        ("jobs-bad-attempt-product", 502),
        ("jobs-bad-url-product", 502),
        ("jobs-truncated-product", 502),
        ("jobs-empty-inconsistent-product", 502),
        ("jobs-cap-product", 413),
        ("revoked-product", 0),
        ("identity-change-product", 0),
        ("repository-change-product", 0),
        ("limited-product", 0),
    ] {
        mode(name)?;
        let response = declared_product(&runtime, &store, &saved.connection, &jobs_target).await;
        if let Ok((status, body)) = response {
            ensure(
                (status == expected || expected == 0 && (400..=599).contains(&status))
                    && body.get("checks").is_none(),
                &format!("declared job refusal {name} differs: {status}"),
            )?;
            if name == "limited-product" {
                ensure(status == 429 && body["error"]["code"] == "provider_rate_limited",
                    "canonical binding hid the provider rate limit")?;
                let before = fs::read_to_string(io.join("gets"))?;
                mode("positive-canonical")?;
                let (held, _) = declared_product(&runtime, &store, &saved.connection, &jobs_target).await?;
                ensure(held == 429 && fs::read_to_string(io.join("gets"))? == before,
                    "canonical rate-limited read retried HTTPS")?;
            }
        }
        cases.push(name);
    }
    fs::write(
        root.join("job-refusals.json"),
        serde_json::to_vec(&json!({"declared_provider_responses":true,"missed_refusals":missed}))?,
    )?;
    ensure(
        missed.is_empty(),
        "declared workflow job consistency refusals missing; see private job-refusals.json",
    )?;
    runtime.stop()?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let retired = runtime.admit_product(
        store.clone(),
        store.capability(&saved.connection).await?,
        Some("123".into()),
    )?;
    let retired_jobs = runtime.admit_product(
        store.clone(),
        store.capability(&saved.connection).await?,
        Some("123".into()),
    )?;
    mode("jobs-pause-product")?;
    let pending = runtime.admit_product(
        store.clone(),
        store.capability(&saved.connection).await?,
        Some("123".into()),
    )?;
    let mut pending = Box::pin(pending.request("GET", &jobs_target, &[]));
    entered(pending.as_mut(), &io).await?;
    store.withdraw(&saved.connection).await?;
    mark(&io.join("release"))?;
    if let Ok(response) = pending.await {
        let body: Value = serde_json::from_slice(&response.body)?;
        ensure(
            response.status != 200 && body.get("checks").is_none(),
            "withdrawn pending job read released observations",
        )?;
    }
    ensure(
        retired_jobs
            .request("GET", &jobs_target, &[])
            .await
            .is_err(),
        "withdrawn declared capability admitted jobs",
    )?;
    cases.push("jobs-withdraw-pending-product");
    ensure(
        retired.request("GET", &target, &[]).await.is_err(),
        "withdrawn declared capability admitted canonical protection",
    )?;
    ensure(
        store.status().await?.is_empty(),
        "declared account remained after withdrawal",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("evidence-result.json"),
        serde_json::to_vec(
            &json!({"declared_provider_responses":true,"real_native_keyring_go_pg":true,"cases":cases,"local_withdrawal":true,"live_github_qualification":false,"merge_gate_qualified":false}),
        )?,
    )?;
    println!("PASS: declared HTTPS protection facts/refusals cross production native decoder, canonical Go reads and private keyring/PG; no live provider or merge gate qualified");
    Ok(())
}

async fn declared_product(
    runtime: &NativeSupervisor,
    store: &Arc<Credentials>,
    connection: &str,
    target: &str,
) -> Result<(u16, serde_json::Value)> {
    let request = runtime.admit_product(
        store.clone(),
        store.capability(connection).await?,
        Some("123".into()),
    )?;
    let response = request.request("GET", target, &[]).await?;
    Ok((response.status, serde_json::from_slice(&response.body)?))
}

fn account() -> Account {
    Account::new(Provider::Github, "https://github.com", "1").unwrap()
}
fn invalid() -> Result<Vec<u8>> {
    let mut value = b"factory-check-invalid-".to_vec();
    value.extend(fixture()?);
    Ok(value)
}
pub(super) async fn run_refresh_refusal(
    production: &Path,
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
    let invalid = invalid()?;
    let saved = store
        .store_grant(account(), super::grant(&invalid, &invalid, 120, 180)?)
        .await?;
    let cap = store.capability(&saved.connection).await?;
    let actual = runtime.github_refresh(&store, &cap).await;
    let refusal_class = match actual {
        Err(Error::RefreshClientRejected) => "client_credentials_rejected",
        Err(Error::Provider(401)) => "sign_in_required",
        _ => {
            return Err("actual GitHub invalid refresh outcome was not a confirmed refusal".into())
        }
    };
    ensure(
        matches!(
            store.status().await?[0].state,
            CredentialState::RefreshPending
        ),
        "actual refused refresh remained usable",
    )?;
    ensure(
        matches!(
            runtime.github_refresh(&store, &cap).await,
            Err(Error::Credential(CredentialError::RefreshPending))
        ),
        "actual refused refresh implicitly replayed",
    )?;
    store.withdraw(&saved.connection).await?;
    store.retire();
    runtime.stop()?;
    fs::write(
        profile
            .parent()
            .ok_or("fixture parent missing")?
            .join("refresh-refusal-result.json"),
        serde_json::to_vec(&serde_json::json!({
            "generated_invalid_fixture":true,"owned_grant_used":false,
            "safe_refusal_class":refusal_class,"pending_custody_retained":true,
            "second_request_refused_before_network":true,"local_account_removed":true,
            "actual_revoked_refresh_token_proved":false
        }))?,
    )?;
    println!("PASS: actual GitHub refused a generated invalid refresh fixture ({refusal_class}); Rust connector/custody retained the pending fence, refused replay and withdrew the fixture; no real grant or revoked-token classification claimed");
    Ok(())
}

// Declared HTTPS only: the same production Rust owner, without a Go HTTP
// observer or real grants. The standalone refusal mode handles actual GitHub.
pub(super) async fn run_refresh_declared(
    production: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let store = Credentials::open(profile, &runtime.health()?.profile)?;
    let observed_account = || Account::new(Provider::Github, "https://github.com", "123").unwrap();
    let fixture_grant = || {
        super::grant(
            b"factory-declared-access",
            b"factory-declared-refresh",
            120,
            180,
        )
    };
    let mode = |value: &str| -> Result<()> {
        fs::write(io.join("mode"), value)?;
        for name in ["entered", "release", "cancelled", "calls", "verified"] {
            match fs::remove_file(io.join(name)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.into()),
            }
        }
        Ok(())
    };
    for value in ["success", "success-again"] {
        mode(value)?;
        let saved = store
            .store_grant(observed_account(), fixture_grant()?)
            .await?;
        let cap = store.capability(&saved.connection).await?;
        let rotated = runtime.github_refresh(&store, &cap).await?;
        let info = rotated.grant.as_ref().ok_or("refresh metadata missing")?;
        ensure(
            rotated.connection == saved.connection
                && rotated.account == "123"
                && info.expires_at.is_some()
                && info.refresh_expires_at.is_some()
                && info.refresh_available
                && info.scope == "repo,read:user",
            "refresh lost account/expiry/scope",
        )?;
        let current = store.capability(&saved.connection).await?;
        ensure(
            store
                .with_token(&current, |bytes| bytes == b"factory-refreshed-access")
                .await?
                && store
                    .with_refresh_operation(&current, async |bytes| {
                        bytes == b"factory-refreshed-refresh"
                    })
                    .await?,
            "refreshed complete pair was not stored",
        )?;
        ensure(
            store.with_token(&cap, |_| ()).await == Err(CredentialError::Retired)
                && fs::read_to_string(io.join("calls"))? == "1"
                && io.join("verified").exists(),
            "refresh replayed or skipped identity/revision validation",
        )?;
        let reopened = Credentials::open(profile, &runtime.health()?.profile)?;
        let loaded = reopened.capability(&saved.connection).await?;
        ensure(
            reopened
                .with_token(&loaded, |bytes| bytes == b"factory-refreshed-access")
                .await?
                && reopened
                    .with_refresh_operation(&loaded, async |bytes| {
                        bytes == b"factory-refreshed-refresh"
                    })
                    .await?
                && reopened.status().await?[0]
                    .grant
                    .as_ref()
                    .is_some_and(|value| {
                        value.expires_at == info.expires_at
                            && value.refresh_expires_at == info.refresh_expires_at
                    }),
            "rotated grant did not survive encrypted reload",
        )?;
        reopened.retire();
    }
    println!("PASS: declared JSON refresh crosses production Rust HTTPS/keyring, verifies replacement identity, preserves expiry/scope/pair, rotates revision and dispatches once without client secret/scope parameters");
    for (value, status) in [
        ("bad-refresh", 401),
        ("client-rejected", 403),
        ("unknown-error", 502),
        ("form", 502),
        ("malformed", 502),
        ("missing-refresh", 502),
        ("same-refresh", 502),
        ("invalid-scope", 502),
        ("invalid-expiry", 502),
        ("oversized", 413),
        ("redirect", 502),
        ("redirect-grant", 502),
        ("http-error-grant", 502),
        ("http-error-known", 401),
        ("identity-denied", 401),
        ("wrong-account", 0),
    ] {
        mode(value)?;
        let saved = store
            .store_grant(observed_account(), fixture_grant()?)
            .await?;
        let cap = store.capability(&saved.connection).await?;
        let result = runtime.github_refresh(&store, &cap).await;
        ensure(
            if status == 0 {
                matches!(result, Err(Error::Credential(CredentialError::Retired)))
            } else if value == "client-rejected" {
                matches!(result, Err(Error::RefreshClientRejected))
            } else {
                matches!(result,Err(Error::Provider(code)) if code==status)
            },
            &format!("declared {value} refresh/identity refusal was lost"),
        )?;
        ensure(
            matches!(
                store.status().await?[0].state,
                CredentialState::RefreshPending
            ) && store.with_token(&cap, |_| ()).await == Err(CredentialError::RefreshPending)
                && matches!(
                    runtime.github_refresh(&store, &cap).await,
                    Err(Error::Credential(CredentialError::RefreshPending))
                )
                && fs::read_to_string(io.join("calls"))? == "1",
            "refused refresh exposed old credentials or replayed",
        )?;
        if !matches!(value, "identity-denied" | "wrong-account") {
            ensure(
                !io.join("verified").exists(),
                "refused exchange reached identity verification",
            )?;
        }
    }
    mode("pause-refresh")?;
    let saved = store
        .store_grant(observed_account(), fixture_grant()?)
        .await?;
    let cap = store.capability(&saved.connection).await?;
    let mut future = Box::pin(runtime.github_refresh(&store, &cap));
    entered(future.as_mut(), &io).await?;
    drop(future);
    cancelled(&io).await?;
    ensure(
        matches!(
            store.status().await?[0].state,
            CredentialState::RefreshPending
        ),
        "cancelled network refresh lost uncertainty",
    )?;
    println!("PASS: declared provider/identity/malformed/redirect refusals retain uncertainty; dropping the caller cancels production Rust OAuth I/O");

    mode("pause-identity")?;
    let saved = store
        .store_grant(observed_account(), fixture_grant()?)
        .await?;
    let cap = store.capability(&saved.connection).await?;
    let mut future = Box::pin(runtime.github_refresh(&store, &cap));
    entered(future.as_mut(), &io).await?;
    store.withdraw(&saved.connection).await?;
    mark(&io.join("release"))?;
    ensure(
        matches!(
            future.await,
            Err(Error::Credential(CredentialError::Withdrawn))
        ) && store.status().await?.is_empty(),
        "refreshed identity resurrected withdrawn credentials",
    )?;

    mode("pause-refresh")?;
    let saved = store
        .store_grant(observed_account(), fixture_grant()?)
        .await?;
    let cap = store.capability(&saved.connection).await?;
    let mut future = Box::pin(runtime.github_refresh(&store, &cap));
    entered(future.as_mut(), &io).await?;
    let newer = store
        .store_grant(
            observed_account(),
            super::grant(b"factory-newer-access", b"factory-newer-refresh", 120, 180)?,
        )
        .await?;
    mark(&io.join("release"))?;
    ensure(
        matches!(
            future.await,
            Err(Error::Credential(CredentialError::Retired))
        ),
        "stale native refresh replaced a newer sign-in",
    )?;
    let current = store.capability(&newer.connection).await?;
    ensure(
        store
            .with_token(&current, |bytes| bytes == b"factory-newer-access")
            .await?,
        "newer credential damaged by late native refresh",
    )?;

    mode("pause-refresh")?;
    let saved = store
        .store_grant(observed_account(), fixture_grant()?)
        .await?;
    let cap = store.capability(&saved.connection).await?;
    let mut future = Box::pin(runtime.github_refresh(&store, &cap));
    entered(future.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        matches!(future.await, Err(Error::Unavailable)),
        "retired native instance released refresh",
    )?;
    cancelled(&io).await?;
    store.withdraw(&saved.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "declared refresh fixture account not removed",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "declared OAuth followed a redirect or released an unexpected request",
    )?;
    store.retire();
    fs::write(
        profile
            .parent()
            .ok_or("fixture parent missing")?
            .join("refresh-declared-result.json"),
        serde_json::to_vec(&serde_json::json!({
            "declared_https_only":true,"production_rust_connector":true,"real_grant_used":false,
            "complete_rotation_and_reload":true,"one_exchange_per_rotation":true,
            "provider_and_identity_refusals":true,"malformed_or_incomplete_refusals":true,
            "non_success_grant_refused_before_identity":true,"redirect_not_followed":true,
            "caller_cancelled_upstream":true,"withdrawal_and_replacement_preserved":true,
            "retirement_joined":true,"owned_local_account_removed":true
        }))?,
    )?;
    println!("PASS: pending Rust identity/token I/O cannot publish after withdrawal/replacement; native stop joins upstream refresh, pending custody remains fenced and owned PG/backend stop; declared evidence only");
    Ok(())
}
/// Declared transport only; proves that full grants compose across the native
/// device/verifier/custody boundaries without dropping refresh or expiration.
pub(super) async fn run_grant(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    fs::write(io.join("mode"), b"grant")?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
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
    fs::write(
        profile
            .parent()
            .ok_or("fixture parent missing")?
            .join("grant-declared-result.json"),
        serde_json::to_vec(&serde_json::json!({
            "declared_https_only":true,"production_rust_connector":true,"real_grant_used":false,
            "device_grant_identity_verified":true,"encrypted_complete_grant_reloaded":true,
            "owned_local_account_removed":true
        }))?,
    )?;
    println!("PASS: declared device grant crosses production Rust HTTPS, identity verification, complete encrypted storage and native reopen without losing refresh/expiry; owned PG/backend stop; no real GitHub consent claimed");
    Ok(())
}

/// Device authorization producer only. Controlled transport grants are labelled
/// explicitly; no human consent, real token or successful login is asserted.
pub(super) async fn run_device_pending(
    production: &Path,
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
    ensure(
        store.status().await?.is_empty(),
        "unapproved device flow saved an account",
    )?;
    store.retire();
    fs::write(
        profile
            .parent()
            .ok_or("fixture parent missing")?
            .join("device-pending-result.json"),
        serde_json::to_vec(&serde_json::json!({
            "actual_github_challenge":true,"unapproved_wait_cancelled":true,
            "retired_start_refused":true,"custody_empty":true,"consent_or_grant_claimed":false
        }))?,
    )?;
    Ok(())
}

pub(super) async fn run_device_declared(
    production: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let store = Credentials::open(profile, &runtime.health()?.profile)?;
    ensure(
        store.status().await?.is_empty(),
        "declared device fixture not empty",
    )?;
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
            fs::write(
                profile
                    .parent()
                    .ok_or("fixture parent missing")?
                    .join("device-backoff-observation.json"),
                serde_json::to_vec(&serde_json::json!({
                    "elapsed_ms":before.elapsed().as_millis(),"polls":fs::read_to_string(io.join("calls"))?.parse::<u64>()?
                }))?,
            )?;
            ensure(
                before.elapsed() >= Duration::from_secs(14)
                    && fs::read_to_string(io.join("calls"))? == "3",
                "polling ignored slow_down or long native deadline",
            )?;
        }
        let (_account, _token) = runtime.github_verify(grant.access).await?;
    }
    println!("PASS: declared Rust HTTPS device grants preserve expiring/refresh and long-lived facts; fresh identity verifies; pending/slow_down respects >12-second polling schedule");
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
    for (mode, status) in [("bad-uri", 502), ("oversized", 413), ("redirect", 502)] {
        set_mode(mode)?;
        ensure(
            matches!(
                runtime.github_device_start().await,
                Err(Error::Provider(code)) if code == status
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
    ensure(
        !io.join("calls").exists() && !io.join("unexpected-request").exists(),
        "retired device operation sent an unexpected request",
    )?;
    store.retire();
    fs::write(
        profile
            .parent()
            .ok_or("fixture parent missing")?
            .join("device-declared-result.json"),
        serde_json::to_vec(&serde_json::json!({
            "declared_https_only":true,"production_rust_connector":true,"real_grant_used":false,
            "expiring_and_long_lived_preserved":true,"pending_and_slowdown_observed":true,
            "denial_expiry_deadline_and_malformed_refused":true,"start_and_wait_cancelled_upstream":true,
            "retired_authorization_refused_before_network":true,"custody_empty":true
        }))?,
    )?;
    println!("PASS: declared Rust HTTPS denial, expiry, local deadline, invalid token/URI/body/redirect, in-flight start/wait cancellation and retired authorization refuse; actual keyring stays empty and owned PG/backend stop");
    Ok(())
}
async fn cancelled(io: &Path) -> Result<()> {
    let until = Instant::now() + Duration::from_secs(4);
    while !io.join("cancelled").exists() {
        ensure(
            Instant::now() < until,
            "cancellation did not reach provider network request",
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
    payload: &Path,
    profile: &Path,
    declared: bool,
) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    if declared {
        fs::write(io.join("mode"), "verify")?;
    }
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
    if !declared {
        println!("PASS: actual GitHub refuses the generated invalid bootstrap fixture; native custody stays empty, Factory remains unbound and retired verification refuses");
        fs::write(
            profile
                .parent()
                .ok_or("fixture parent missing")?
                .join("verify-refusal-result.json"),
            serde_json::to_vec(
                &serde_json::json!({"actual_github_401":true,"generated_invalid_fixture":true,"custody_empty":true,"factory_unbound":true,"retired_verification_refused":true}),
            )?,
        )?;
        return Ok(());
    }

    fs::write(io.join("calls"), "0")?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
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
        "declared unpersisted token did not traverse production Rust HTTPS",
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
            "bootstrap cancellation did not close Rust HTTPS request",
        )?;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    ensure(
        store.status().await?.is_empty(),
        "cancelled verification saved an account",
    )?;
    println!("PASS: dropping unpersisted verification cancels production Rust HTTPS promptly, with no OS account written");
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
    cancelled(&io).await?;
    ensure(
        !io.join("unexpected-request").exists(),
        "verification released an unexpected request",
    )?;
    store.retire();
    fs::write(
        profile
            .parent()
            .ok_or("fixture parent missing")?
            .join("verify-declared-result.json"),
        serde_json::to_vec(
            &serde_json::json!({"declared_https_only":true,"production_rust_connector":true,"refusal_before_custody":true,"native_restart_rotated_instance":true,"caller_cancelled_upstream":true,"retirement_joined":true,"custody_empty":true,"real_grant_used":false}),
        )?,
    )?;
    println!("PASS: declared Rust HTTPS bootstrap refusal, restart, cancellation and joined retirement preserve empty real custody and unbound Factory");
    Ok(())
}
pub(super) async fn run(
    production: &Path,
    payload: &Path,
    profile: &Path,
    declared: bool,
) -> Result<()> {
    prepare(profile)?;
    let io = profile.parent().ok_or("fixture parent missing")?.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    if declared {
        fs::write(io.join("mode"), "transport")?;
    }
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
    if !declared {
        store.withdraw(&saved.connection).await?;
        ensure(
            store.status().await?.is_empty(),
            "actual transport fixture account not removed",
        )?;
        store.retire();
        fs::write(
            profile
                .parent()
                .ok_or("fixture parent missing")?
                .join("transport-refusal-result.json"),
            serde_json::to_vec(
                &serde_json::json!({"actual_github_401":true,"generated_invalid_fixture":true,"factory_unbound":true,"retired_read_refused":true,"owned_local_account_removed":true}),
            )?,
        )?;
        println!("PASS: actual GitHub refuses the native invalid fixture, Factory remains unbound, retired read refuses and owned account is removed");
        return Ok(());
    }
    store.retire();

    fs::write(io.join("calls"), "0")?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
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
        "native catalogue fell back to public data or lost declared credential scope",
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
        "pending Rust reply survived OS credential replacement",
    )?;
    println!("PASS: pending production Rust HTTPS result refuses after independent real-keyring credential replacement");

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
        "pending Rust reply survived OS credential withdrawal",
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
            "caller cancellation did not close Rust HTTPS promptly",
        )?;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    println!("PASS: dropping native read cancels the production Rust HTTPS request promptly");

    pause(&io)?;
    let mut request = Box::pin(runtime.github_read(&store, &cap, Read::Identity));
    entered(request.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        matches!(request.await, Err(Error::Unavailable)),
        "retiring native runtime released a provider result",
    )?;
    cancelled(&io).await?;
    store.withdraw(&saved.connection).await?;
    store.withdraw(&fountain.connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "native transport fixture credential cleanup failed",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "transport released an unexpected request",
    )?;
    store.retire();
    other.retire();
    fs::write(
        profile
            .parent()
            .ok_or("fixture parent missing")?
            .join("transport-declared-result.json"),
        serde_json::to_vec(
            &serde_json::json!({"declared_https_only":true,"production_rust_connector":true,"real_grant_used":false,"native_restart_rotated_instance":true,"invalid_and_foreign_selectors_refused_before_network":true,"replacement_and_withdrawal_preserved":true,"caller_cancelled_upstream":true,"retirement_joined":true,"owned_local_accounts_removed":true}),
        )?,
    )?;
    println!("PASS: declared production Rust HTTPS scopes/refusals, real-keyring replacement/withdrawal, cancellation and joined native retirement; owned accounts and Go/PG cleaned up");
    Ok(())
}
