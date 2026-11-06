//! Real native producer acceptance; only an owned no-forwarding dummy peer.
use super::native_github_fixture::{call, completed, posts, set_mode, uncertain};
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::credentials::{Account, Credentials, Provider};
use soda_desktop_runtime::github_provider::{DiscussionLock, Owner, WriteOutcome};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use soda_desktop_runtime::ProductRequest;
use std::sync::Arc;
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::Path,
    time::{Duration, Instant},
};
pub(super) async fn producer(profile: &Path) -> Result<()> {
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let token = || Token::new(b"factory-declared-lock".to_vec());
    let lock = || DiscussionLock {
        repository: "123".into(),
        subject: "issue".into(),
        subject_id: "700".into(),
        number: 7,
        lock: true,
        reason: "spam".into(),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for (name, reason, active) in [
        ("issue-lock", "spam", Some("SPAM")),
        ("issue-unlock", "", None),
        ("pull-lock", "spam", Some("SPAM")),
        ("pull-unlock", "", None),
        ("off-topic", "off-topic", Some("OFF_TOPIC")),
        ("resolved", "resolved", Some("RESOLVED")),
        ("spam", "spam", Some("SPAM")),
        ("too-heated", "too heated", Some("TOO_HEATED")),
        ("blank-reason", "", None),
        ("big-id", "spam", Some("SPAM")),
        ("repository-slot-reused", "spam", Some("SPAM")),
        ("no-request-id", "spam", Some("SPAM")),
    ] {
        mode(name)?;
        let mut q = lock();
        q.reason = reason.into();
        q.lock = !name.ends_with("unlock");
        if name.starts_with("pull") {
            q.subject = "pull".into();
        }
        if name == "big-id" {
            q.subject_id = "5000000000".into();
        }
        let before = posts(&io)?;
        let WriteOutcome::Applied(r) = client.set_discussion_lock(token()?, "321", &q).await else {
            return Err(format!("lock positive refused: {name}").into());
        };
        ensure(
            r["account"]["external_id"] == "321"
                && r["actor"]["external_id"] == "321"
                && r["actor_node_id"] == "U_declared321"
                && r["repository"] == q.repository
                && r["subject"] == q.subject
                && r["subject_id"] == q.subject_id
                && r["number"] == 7
                && r["locked"] == q.lock
                && r["reason"] == q.reason
                && r["active_lock_reason"] == json!(active)
                && r["provider_response"]["status"] == 200
                && r["provider_response"]["protocol"] == "github_graphql_discussion_lock"
                && serde_json::to_vec(&r)?.len() <= 4096
                && posts(&io)? == before + 1,
            "lock causal receipt lost scope/actor/desired state/reason/bounds",
        )?;
        if name == "no-request-id" {
            ensure(
                r["provider_response"]["request_id"].is_null(),
                "lock invented request id",
            )?;
        }
        fs::write(
            root.join(format!("{name}-receipt.json")),
            serde_json::to_vec(&r)?,
        )?;
        cases.push(name);
    }
    for (name, status) in [
        ("identity-change", 409),
        ("identity-denied", 401),
        ("missing-actor-node", 502),
        ("repository-change", 409),
        ("subject-change", 409),
        ("subject-kind", 409),
        ("missing-subject-node", 502),
        ("missing-repository-node", 502),
        ("archived", 403),
        ("disabled", 403),
        ("readonly-unknown", 403),
        ("lock-unknown", 502),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let before = posts(&io)?;
        let WriteOutcome::NotSent(f) = client.set_discussion_lock(token()?, "321", &lock()).await
        else {
            return Err(format!("lock preflight did not refuse: {name}").into());
        };
        ensure(
            f.status == status && posts(&io)? == before,
            "lock preflight dispatched or lost refusal class",
        )?;
        cases.push(name);
    }
    mode("positive")?;
    for i in 0..8 {
        let mut q = lock();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.subject_id = "0700".into(),
            2 => q.subject = "discussion".into(),
            3 => q.number = 0,
            4 => q.number = u64::MAX,
            5 => q.reason = "\0".into(),
            6 => q.reason = "x".repeat(65537),
            _ => q.lock = false,
        }
        let before = fs::read_to_string(io.join("gets"))?;
        ensure(
            matches!(client.set_discussion_lock(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.status==400)
                && fs::read_to_string(io.join("gets"))? == before,
            "invalid lock descriptor reached provider",
        )?;
    }
    for reason in [
        "Human prose".to_owned(),
        "SPAM".into(),
        " spam ".into(),
        "\u{0001}".repeat(65536),
    ] {
        let mut q = lock();
        q.reason = reason;
        let before = fs::read_to_string(io.join("gets"))?;
        ensure(
            matches!(client.set_discussion_lock(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.status==400 && f.code=="github_lock_reason_unsupported")
                && fs::read_to_string(io.join("gets"))? == before,
            "unsupported reason was normalized or sent",
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
        ("wrong-subject", 502),
        ("wrong-id", 502),
        ("wrong-repository", 502),
        ("wrong-number", 502),
        ("wrong-kind", 502),
        ("wrong-locked", 502),
        ("wrong-reason", 502),
        ("missing-reason", 502),
        ("missing-actor", 502),
        ("wrong-actor", 502),
        ("wrong-url", 502),
        ("missing-full-id", 502),
        ("partial-errors", 502),
        ("graphql-errors", 502),
        ("oversized", 413),
        ("oversized-request-id", 502),
        ("receipt-oversized", 413),
        ("drop-reply", 503),
        ("deadline", 504),
    ] {
        mode(name)?;
        let before = posts(&io)?;
        let WriteOutcome::Indeterminate(f) =
            client.set_discussion_lock(token()?, "321", &lock()).await
        else {
            return Err(
                format!("lock after-dispatch failure became success/absence: {name}").into(),
            );
        };
        ensure(
            f.status == status && posts(&io)? == before + 1,
            "uncertain lock retried or lost class",
        )?;
        cases.push(name);
    }
    // A missing nullable reason must not be read as known null on unlock either.
    mode("missing-unlock-reason")?;
    let mut q = lock();
    q.lock = false;
    q.reason.clear();
    let before = posts(&io)?;
    ensure(
        matches!(client.set_discussion_lock(token()?,"321",&q).await,WriteOutcome::Indeterminate(f)if f.status==502)
            && posts(&io)? == before + 1,
        "absent unlock reason became known cleared reason",
    )?;
    cases.push("missing-unlock-reason");
    mode("serialize")?;
    let a = lock();
    let b = lock();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.set_discussion_lock(token()?, "321", &a),
        clone.set_discussion_lock(token()?, "321", &b)
    );
    ensure(
        matches!(first, WriteOutcome::Applied(_))
            && matches!(second, WriteOutcome::Applied(_))
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "lock serialization/pacing failed",
    )?;
    mode("pause-post")?;
    let q = lock();
    let t = token()?;
    let pending = async { Ok::<_, &str>(client.set_discussion_lock(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    owner.stop()?;
    ensure(
        matches!(pending.await?, WriteOutcome::Indeterminate(_))
            && started.elapsed() < Duration::from_secs(2),
        "lock retirement blocked or lost uncertainty",
    )?;
    ensure(
        matches!(
            client.set_discussion_lock(token()?, "321", &lock()).await,
            WriteOutcome::NotSent(_)
        ),
        "retired lock owner accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "lock fixture received unexpected request",
    )?;
    fs::write(
        root.join("lock-producer-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_descriptors":8,"unsupported_reasons":4,"serialization":true,"in_flight_retirement":"indeterminate","actual_provider":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("PASS: production Rust issue/PR lock/unlock binds immutable scope, exact supported reason and causal actor/state receipt; unsupported reasons send nothing; no retry, deadlines, serialization and retirement pass; dummy-only HTTPS, no actual provider/product qualification");
    Ok(())
}

fn reason_envelope(path: &str, id: &str, lock: bool, reason: &str) -> Result<Vec<u8>> {
    let q: ProductRequest = serde_json::from_value(
        json!({"kind":"write","path":path,"body":{"command_id":id,"lock":lock,"reason":reason}}),
    )?;
    Ok(q.request()?.2)
}
fn envelope(path: &str, id: &str, lock: bool) -> Result<Vec<u8>> {
    let q: ProductRequest = serde_json::from_value(
        json!({"kind":"write","path":path,"body":{"command_id":id,"lock":lock,"reason":if lock{"spam"}else{""}}}),
    )?;
    Ok(q.request()?.2)
}
pub(super) async fn product(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    set_mode(&io, "positive")?;
    let mut runtime = NativeSupervisor::start(payload, production, profile)?;
    let mut store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "lock product requires empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let saved = store
        .store(account()?, Token::new(b"factory-declared-lock".to_vec())?)
        .await?;
    let mut connection = saved.connection;
    let catalogue = call(
        &runtime,
        &store,
        &connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "lock canonical catalogue failed")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let base = format!("/api/factory/collaboration/repositories/{repository}");
    let mut cases = Vec::new();
    for (i, (kind, locked, reason)) in [
        ("issues", true, "spam"),
        ("issues", false, ""),
        ("pulls", true, "off-topic"),
        ("pulls", false, ""),
        ("issues", true, "resolved"),
        ("issues", false, ""),
        ("pulls", true, "too heated"),
        ("pulls", false, ""),
        ("issues", true, ""),
        ("issues", false, ""),
    ]
    .into_iter()
    .enumerate()
    {
        let submit = format!("{base}/{kind}/7/locks");
        let id = format!("{:032x}", 100 + i);
        let command = format!("{submit}/commands/{id}");
        let bytes = reason_envelope(&submit, &id, locked, reason)?;
        let before = posts(&io)?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "committed",
        )?;
        let read = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{base}/{kind}/7"),
            &[],
        )
        .await?;
        ensure(
            read.0 == 200 && read.1["locked"].as_bool() == Some(locked),
            "lock normal readback lost requested state",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "committed",
        )?;
        completed(
            &call(&runtime, &store, &connection, "GET", &command, &[]).await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{command}/cancel"),
                b"{}",
            )
            .await?,
            "committed",
        )?;
        let conflict = reason_envelope(
            &submit,
            &id,
            locked,
            if locked { "Different reason" } else { "" },
        )?;
        if locked {
            ensure(
                call(&runtime, &store, &connection, "POST", &submit, &conflict)
                    .await?
                    .0
                    == 409,
                "changed lock reason adopted captured command",
            )?;
        }
        ensure(
            posts(&io)? == before + 1,
            "lock replay/cancel/conflict repeated POST",
        )?;
        cases.push(format!("positive-{i}"));
    }
    set_mode(&io, "unknown-read-lock")?;
    let unknown = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{base}/pulls/7"),
        &[],
    )
    .await?;
    ensure(
        unknown.0 == 200 && unknown.1.get("locked") == Some(&serde_json::Value::Null),
        "unobserved PR lock was omitted or made known",
    )?;
    cases.push("pull-lock-unobserved".into());
    set_mode(&io, "positive")?;
    for (i, reason) in [
        "Human prose".to_owned(),
        "SPAM".into(),
        " spam ".into(),
        "\u{0001}".repeat(65536),
    ]
    .into_iter()
    .enumerate()
    {
        let submit = format!("{base}/issues/7/locks");
        let id = format!("{:032x}", 200 + i);
        let bytes = reason_envelope(&submit, &id, true, &reason)?;
        let before = posts(&io)?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "not_committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "GET",
                &format!("{submit}/commands/{id}"),
                &[],
            )
            .await?,
            "not_committed",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "not_committed",
        )?;
        ensure(
            posts(&io)? == before,
            "unsupported lock reason reached a mutation",
        )?;
        cases.push(format!("unsupported-{i}"));
    }
    let submit = format!("{base}/issues/7/locks");
    let command = |id: &str| format!("{submit}/commands/{id}");
    let concurrent = "33333333333333333333333333333333";
    let bytes = envelope(&submit, concurrent, true)?;
    let before = posts(&io)?;
    let (a, b) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &bytes),
        call(&runtime, &store, &connection, "POST", &submit, &bytes)
    );
    for r in [a?, b?] {
        if r.1["finished"] == true {
            completed(&r, "committed")?
        } else {
            uncertain(&r)?
        }
    }
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(concurrent),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        posts(&io)? == before + 1,
        "concurrent lock commands repeated POST",
    )?;
    cases.push("concurrent-identical-command".into());
    let wrong = format!(
        "/api/factory/collaboration/repositories/{}/issues/7/locks",
        repository.parse::<i64>()? + 1
    );
    ensure(
        call(
            &runtime,
            &store,
            &connection,
            "POST",
            &wrong,
            &envelope(&wrong, "99999999999999999999999999999999", true)?,
        )
        .await?
        .0 == 403
            && posts(&io)? == before + 1,
        "lock request expanded selected scope",
    )?;
    cases.push("other-repository-refusal".into());
    // Current state is not evidence that this command performed the transition.
    let lost_id = "44444444444444444444444444444444";
    let lost = envelope(&submit, lost_id, false)?;
    set_mode(&io, "drop-reply")?;
    let before = posts(&io)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    set_mode(&io, "positive")?;
    let read = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{base}/issues/7"),
        &[],
    )
    .await?;
    ensure(
        read.0 == 200 && read.1["locked"] == false,
        "lost lock reply fixture did not retain readable remote effect",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "lock readback fabricated attribution or retried",
    )?;
    cases.push("lost-reply-current-lock-remains-uncertain".into());
    for (name, lock, effect, id) in [
        (
            "pause-post",
            true,
            "committed",
            "55555555555555555555555555555555",
        ),
        (
            "pause-preflight",
            false,
            "not_committed",
            "66666666666666666666666666666666",
        ),
    ] {
        set_mode(&io, name)?;
        let before = posts(&io)?;
        let bytes = envelope(&submit, id, lock)?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let pending = request.request("POST", &submit, &bytes);
        tokio::pin!(pending);
        super::native_github_fixture::entered(pending.as_mut(), &io).await?;
        let cancelled = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &format!("{}/cancel", command(id)),
            b"{}",
        )
        .await?;
        uncertain(&cancelled)?;
        ensure(
            cancelled.1["cancellation"] == "indeterminate",
            "racing cancellation claimed complete rollback",
        )?;
        fs::write(io.join("release"), b"")?;
        let reply = pending.await?;
        let result = (reply.status, serde_json::from_slice(&reply.body)?);
        completed(&result, effect)?;
        ensure(
            result.1["cancellation"] == "indeterminate",
            "late known lock erased cancellation uncertainty",
        )?;
        ensure(
            posts(&io)? == before + u64::from(name == "pause-post"),
            "cancel race repeated lock POST",
        )?;
        cases.push(name.into());
    }
    set_mode(&io, "pause-post")?;
    let withdrawn_id = "77777777777777777777777777777777";
    let withdrawn = envelope(&submit, withdrawn_id, true)?;
    let before = posts(&io)?;
    let request = runtime.admit_product(
        store.clone(),
        store.capability(&connection).await?,
        Some("123".into()),
    )?;
    let pending = request.request("POST", &submit, &withdrawn);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    store.withdraw(&connection).await?;
    connection = store
        .store(account()?, Token::new(b"factory-declared-lock".to_vec())?)
        .await?
        .connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "withdrawn generation returned a lock result",
    )?;
    set_mode(&io, "positive")?;
    uncertain(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(withdrawn_id),
            &[],
        )
        .await?,
    )?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &withdrawn).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "replacement custody repeated uncertain lock mutation",
    )?;
    cases.push("withdrawal-replacement".into());
    set_mode(&io, "pause-post")?;
    let stopped_id = "88888888888888888888888888888888";
    let stopped = envelope(&submit, stopped_id, false)?;
    let before = posts(&io)?;
    let request = runtime.admit_product(
        store.clone(),
        store.capability(&connection).await?,
        Some("123".into()),
    )?;
    let pending = request.request("POST", &submit, &stopped);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        pending.await.is_err(),
        "stopped runtime returned a lock reply",
    )?;
    store.retire();
    runtime = NativeSupervisor::start(payload, production, profile)?;
    store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    set_mode(&io, "positive")?;
    uncertain(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(stopped_id),
            &[],
        )
        .await?,
    )?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &stopped).await?)?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    for (i, kind) in [
        "issues", "issues", "pulls", "pulls", "issues", "issues", "pulls", "pulls", "issues",
        "issues",
    ]
    .into_iter()
    .enumerate()
    {
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "GET",
                &format!("{base}/{kind}/7/locks/commands/{:032x}", 100 + i),
                &[],
            )
            .await?,
            "committed",
        )?;
    }
    ensure(
        posts(&io)? == before + 1,
        "retained restart repeated lock mutation",
    )?;
    cases.push("retained-restart".into());
    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?
                .with_label("declared-other")?,
            Token::new(b"factory-declared-other".to_vec())?,
        )
        .await?;
    ensure(
        call(
            &runtime,
            &store,
            &other.connection,
            "GET",
            &command(lost_id),
            &[],
        )
        .await?
        .0 == 404,
        "another account read lock capture",
    )?;
    ensure(
        call(&runtime, &store, &other.connection, "POST", &submit, &lost)
            .await?
            .0
            == 409
            && posts(&io)? == before + 1,
        "another account adopted lock command",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "lock product fixture saw unexpected request",
    )?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "lock product left owned grant",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("lock-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_provider":false,"native_product_consumer":true,"provider_posts":posts(&io)?,"capabilities_enabled":false,"retained_restart":true}),
        )?,
    )?;
    println!("PASS: composed native/Rust/Go/PG lock/unlock retains exact supported/unsupported reasons, causal actor/state receipt, normal readback, one-shot replay, ambiguous replies, cancellation races, withdrawal, another-account refusal and restart; dummy-only provider, no actual GitHub or installed qualification");
    Ok(())
}
