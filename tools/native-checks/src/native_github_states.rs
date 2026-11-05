//! Existing real native integration executable; dummy-only owned HTTPS peer.
use super::native_github_issues::{call, completed, set_mode, uncertain};
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::credentials::{Account, Credentials, Provider};
use soda_desktop_runtime::github_provider::{DiscussionState, Owner, StateOutcome};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use soda_desktop_runtime::ProductRequest;
use std::sync::Arc;
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::Path,
    time::{Duration, Instant},
};

fn posts(io: &Path) -> Result<u64> {
    match fs::read_to_string(io.join("posts")) {
        Ok(s) => Ok(s.parse()?),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(0),
        Err(e) => Err(e.into()),
    }
}
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
    let token = || Token::new(b"factory-declared-state".to_vec());
    let state = || DiscussionState {
        repository: "123".into(),
        subject: "issue".into(),
        subject_id: "700".into(),
        number: 7,
        close: true,
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "issue-close",
        "issue-reopen",
        "pull-close",
        "pull-reopen",
        "big-id",
        "repository-slot-reused",
        "no-request-id",
    ] {
        mode(name)?;
        let mut q = state();
        q.close = !name.ends_with("reopen");
        if name.starts_with("pull") {
            q.subject = "pull".into();
        }
        if name == "big-id" {
            q.subject_id = "5000000000".into();
        }
        let before = posts(&io)?;
        let StateOutcome::Applied(r) = client.set_discussion_state(token()?, "321", &q).await
        else {
            return Err(format!("state positive refused: {name}").into());
        };
        ensure(
            r["repository"] == q.repository
                && r["subject"] == q.subject
                && r["subject_id"] == q.subject_id
                && r["number"] == 7
                && r["close"] == q.close
                && r["account"]["external_id"] == "321"
                && r["state"] == if q.close { "CLOSED" } else { "OPEN" }
                && r["provider_response"]["status"] == 200
                && r["provider_response"]["protocol"] == "github_graphql_discussion_state"
                && posts(&io)? == before + 1
                && serde_json::to_vec(&r)?.len() <= 4096,
            "state causal receipt lost binding/bounds",
        )?;
        if name == "no-request-id" {
            ensure(
                r["provider_response"]["request_id"].is_null(),
                "invented request id",
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
        ("repository-change", 409),
        ("subject-change", 409),
        ("subject-kind", 409),
        ("missing-subject-node", 502),
        ("missing-repository-node", 502),
        ("archived", 403),
        ("disabled", 403),
        ("readonly-unknown", 403),
        ("unknown-state", 502),
        ("already-current", 409),
        ("merged-pull", 409),
        ("unknown-merged", 409),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let mut q = state();
        if name.contains("merged") {
            q.subject = "pull".into();
        }
        let before = posts(&io)?;
        let outcome = client.set_discussion_state(token()?, "321", &q).await;
        let StateOutcome::NotSent(f) = outcome else {
            return Err(format!("state preflight did not refuse: {name}").into());
        };
        ensure(
            f.status == status && posts(&io)? == before,
            "state preflight dispatched or lost refusal",
        )?;
        cases.push(name);
    }
    mode("positive")?;
    for i in 0..7 {
        let mut q = state();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.subject_id = "0700".into(),
            2 => q.subject = "discussion".into(),
            3 => q.number = 0,
            4 => q.number = u64::MAX,
            5 => q.repository = "-1".into(),
            _ => q.subject_id = "".into(),
        };
        let before = fs::read_to_string(io.join("gets"))?;
        ensure(
            matches!(client.set_discussion_state(token()?,"321",&q).await,StateOutcome::NotSent(f) if f.status==400)
                && fs::read_to_string(io.join("gets"))? == before,
            "invalid state reached provider",
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
        ("wrong-state", 502),
        ("wrong-url", 502),
        ("merged-reply", 502),
        ("missing-full-id", 502),
        ("partial-errors", 502),
        ("graphql-errors", 502),
        ("oversized", 413),
        ("oversized-request-id", 502),
        ("drop-reply", 503),
        ("deadline", 504),
    ] {
        mode(name)?;
        let mut q = state();
        if name == "merged-reply" {
            q.subject = "pull".into();
        }
        let before = posts(&io)?;
        let StateOutcome::Indeterminate(f) = client.set_discussion_state(token()?, "321", &q).await
        else {
            return Err(format!("state after dispatch became success or absence: {name}").into());
        };
        ensure(
            f.status == status && posts(&io)? == before + 1,
            "state uncertain effect retried or lost class",
        )?;
        cases.push(name);
    }
    mode("serialize")?;
    let a = state();
    let b = state();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.set_discussion_state(token()?, "321", &a),
        clone.set_discussion_state(token()?, "321", &b)
    );
    ensure(
        matches!(first, StateOutcome::Applied(_))
            && matches!(second, StateOutcome::Applied(_))
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "state serialization/pacing failed",
    )?;
    mode("pause-post")?;
    let q = state();
    let t = token()?;
    let pending = async { Ok::<_, &str>(client.set_discussion_state(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    owner.stop()?;
    ensure(
        matches!(pending.await?, StateOutcome::Indeterminate(_))
            && started.elapsed() < Duration::from_secs(2),
        "state retirement lost uncertainty or blocked",
    )?;
    ensure(
        matches!(
            client.set_discussion_state(token()?, "321", &state()).await,
            StateOutcome::NotSent(_)
        ),
        "retired state owner accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "state fixture received unexpected request",
    )?;
    fs::write(
        root.join("state-producer-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_descriptors":7,"serialization":true,"in_flight_retirement":"indeterminate","actual_provider":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("PASS: production Rust close/reopen binds immutable subject/repository and causal state receipt; no retry, merged refusal, preflight/post deadlines, serialization and retirement pass; dummy-only HTTPS, no live provider or product gate qualification");
    Ok(())
}

fn envelope(path: &str, id: &str, close: bool) -> Result<Vec<u8>> {
    let q: ProductRequest = serde_json::from_value(
        json!({"kind":"write","path":path,"body":{"command_id":id,"close":close}}),
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
        "state product requires empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let saved = store
        .store(account()?, Token::new(b"factory-declared-state".to_vec())?)
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
    ensure(catalogue.0 == 200, "state canonical catalogue failed")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let base = format!("/api/factory/collaboration/repositories/{repository}");
    let mut cases = Vec::new();
    for (i, (kind, close)) in [
        ("issues", true),
        ("issues", false),
        ("pulls", true),
        ("pulls", false),
    ]
    .into_iter()
    .enumerate()
    {
        let submit = format!("{base}/{kind}/7/state");
        let id = format!("{:032x}", 100 + i);
        let command = format!("{submit}/commands/{id}");
        let bytes = envelope(&submit, &id, close)?;
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
            read.0 == 200 && read.1["state"] == if close { "closed" } else { "open" },
            "state normal readback did not match requested effect",
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
        ensure(
            call(
                &runtime,
                &store,
                &connection,
                "POST",
                &submit,
                &envelope(&submit, &id, !close)?,
            )
            .await?
            .0 == 409
                && posts(&io)? == before + 1,
            "state replay/cancel/conflict duplicated effect",
        )?;
        cases.push(format!("{kind}-{}", if close { "close" } else { "reopen" }));
    }
    let submit = format!("{base}/issues/7/state");
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
        "concurrent state commands repeated POST",
    )?;
    cases.push("concurrent-identical-command".into());
    let wrong = format!(
        "/api/factory/collaboration/repositories/{}/issues/7/state",
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
        "state request expanded selected scope",
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
        read.0 == 200 && read.1["state"] == "open",
        "lost state reply fixture did not retain readable remote effect",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "state readback fabricated attribution or retried",
    )?;
    cases.push("lost-reply-current-state-remains-uncertain".into());
    for (name, close, effect, id) in [
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
        let bytes = envelope(&submit, id, close)?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let pending = request.request("POST", &submit, &bytes);
        tokio::pin!(pending);
        super::native_github_transport::entered(pending.as_mut(), &io).await?;
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
            "late known state erased cancellation uncertainty",
        )?;
        ensure(
            posts(&io)? == before + u64::from(name == "pause-post"),
            "cancel race repeated state POST",
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
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    store.withdraw(&connection).await?;
    connection = store
        .store(account()?, Token::new(b"factory-declared-state".to_vec())?)
        .await?
        .connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "withdrawn generation returned a state result",
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
        "replacement custody repeated uncertain state mutation",
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
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        pending.await.is_err(),
        "stopped runtime returned a state reply",
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
    for (i, kind) in ["issues", "issues", "pulls", "pulls"]
        .into_iter()
        .enumerate()
    {
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "GET",
                &format!("{base}/{kind}/7/state/commands/{:032x}", 100 + i),
                &[],
            )
            .await?,
            "committed",
        )?;
    }
    ensure(
        posts(&io)? == before + 1,
        "retained restart repeated state mutation",
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
        "another account read state capture",
    )?;
    ensure(
        call(&runtime, &store, &other.connection, "POST", &submit, &lost)
            .await?
            .0
            == 409
            && posts(&io)? == before + 1,
        "another account adopted state command",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "state product fixture saw unexpected request",
    )?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "state product left owned grant",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("state-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_provider":false,"native_product_consumer":true,"provider_posts":posts(&io)?,"capabilities_enabled":false,"retained_restart":true}),
        )?,
    )?;
    println!("PASS: composed native/Rust/Go/PG close/reopen retains causal identity, normal state readback, one-shot replay, ambiguous reply, cancellation races, withdrawal, other-account refusal and restart; dummy-only provider, no actual GitHub or installed qualification");
    Ok(())
}
