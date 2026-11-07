//! Real native provider/journal integration observers, never Rust unit tests.
//! Launch only with dummy grants and an owned no-forwarding local HTTPS fixture.
use super::native_github_fixture::{call, completed, posts, set_mode, uncertain};
use super::{ensure, prepare, Result};
use serde_json::json;
use sha2::{Digest, Sha256};
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::credentials::{Account, Credentials, Provider};
use soda_desktop_runtime::github_provider::{CreateOutcome, IssueCreate, Owner};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use soda_desktop_runtime::ProductRequest;
use std::os::unix::fs::PermissionsExt;
use std::sync::Arc;
use std::{
    fs,
    path::Path,
    time::{Duration, Instant},
};

fn sha(value: &str) -> String {
    Sha256::digest(value.as_bytes())
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
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
    let token = || Token::new(b"factory-declared-issue".to_vec());
    let issue = || IssueCreate {
        repository: "123".into(),
        title: "Exact human issue π".into(),
        body: "Original writing 🍵\n[link](relative.md)\n".repeat(256),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "positive",
        "empty-body",
        "max-unicode",
        "max-control",
        "big-issue-id",
        "repository-slot-reused",
        "no-request-id",
    ] {
        mode(name)?;
        let mut q = issue();
        match name {
            "empty-body" => q.body.clear(),
            "max-unicode" => {
                q.body = "🍵".repeat(16384);
                q.title = "t".repeat(255)
            }
            "max-control" => q.body = "\u{0001}".repeat(65536),
            _ => (),
        };
        let before = posts(&io)?;
        let CreateOutcome::Created(value) = client.create_issue(token()?, "321", &q).await else {
            return Err("declared issue creation refused".into());
        };
        ensure(
            value["repository"] == "123"
                && value["repository_node_id"] == "R_declared123"
                && value["account"]["external_id"] == "321"
                && value["issue"]["number"] == 17
                && value["issue"]["external_id"]
                    == if name == "big-issue-id" {
                        "5000000000"
                    } else {
                        "1700"
                    }
                && value["issue"]["author"]["external_id"] == "321"
                && value["issue"]["title_sha256"] == sha(&q.title)
                && value["issue"]["body_sha256"] == sha(&q.body)
                && value["issue"].get("body").is_none()
                && value["issue"].get("title").is_none()
                && value["provider_response"]["protocol"] == "github_graphql_create_issue"
                && value["provider_response"]["status"] == 200
                && serde_json::to_vec(&value)?.len() <= 4096,
            "issue causal receipt lost identity/writing/bounds",
        )?;
        ensure(
            posts(&io)? == before + 1,
            "successful issue issued more than one POST",
        )?;
        if name == "no-request-id" {
            ensure(
                value["provider_response"]["request_id"].is_null(),
                "missing provider request ID was invented",
            )?;
        }
        fs::write(
            root.join(format!("{name}-receipt.json")),
            serde_json::to_vec(&value)?,
        )?;
        cases.push(name);
    }
    for (name, status) in [
        ("identity-change", 409),
        ("identity-denied", 401),
        ("missing-actor-node", 502),
        ("repository-change", 409),
        ("missing-repository-node", 502),
        ("archived", 403),
        ("disabled", 403),
        ("readonly-unknown", 403),
        ("issues-disabled", 403),
        ("issues-unknown", 403),
    ] {
        mode(name)?;
        let before = posts(&io)?;
        let CreateOutcome::NotSent(f) = client.create_issue(token()?, "321", &issue()).await else {
            return Err("issue preflight did not refuse before dispatch".into());
        };
        ensure(
            f.status == status && posts(&io)? == before,
            "issue preflight failure dispatched or lost status",
        )?;
        cases.push(name);
    }
    mode("positive")?;
    for i in 0..8 {
        let mut q = issue();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.title.clear(),
            2 => q.title = " padded ".into(),
            3 => q.title = "x".repeat(256),
            4 => q.title = "\0".into(),
            5 => q.body = "x".repeat(65537),
            6 => q.body = "\0".into(),
            _ => q.repository = "-1".into(),
        };
        let before = fs::read_to_string(io.join("gets"))?;
        ensure(
            matches!(client.create_issue(token()?,"321",&q).await,CreateOutcome::NotSent(f)if f.status==400)
                && fs::read_to_string(io.join("gets"))? == before,
            "invalid issue request reached provider",
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
        ("wrong-repository", 502),
        ("wrong-author", 502),
        ("wrong-title", 502),
        ("wrong-body", 502),
        ("wrong-url", 502),
        ("invalid-number", 502),
        ("missing-full-database-id", 502),
        ("partial-errors", 502),
        ("graphql-errors", 502),
        ("oversized", 413),
        ("receipt-oversized", 413),
        ("drop-reply", 503),
        ("deadline", 504),
    ] {
        mode(name)?;
        let before = posts(&io)?;
        let CreateOutcome::Indeterminate(f) = client.create_issue(token()?, "321", &issue()).await
        else {
            return Err("post-dispatch issue failure became success or safe absence".into());
        };
        ensure(
            f.status == status && posts(&io)? == before + 1,
            "uncertain issue lost refusal class or repeated POST",
        )?;
        cases.push(name);
    }
    mode("serialize")?;
    let a = issue();
    let b = issue();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.create_issue(token()?, "321", &a),
        clone.create_issue(token()?, "321", &b)
    );
    ensure(
        matches!(first, CreateOutcome::Created(_))
            && matches!(second, CreateOutcome::Created(_))
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "issue writes failed serialization/pacing",
    )?;
    mode("pause-post")?;
    let q = issue();
    let t = token()?;
    let pending = async { Ok::<_, &str>(client.create_issue(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    owner.stop()?;
    ensure(
        matches!(pending.await?, CreateOutcome::Indeterminate(_))
            && started.elapsed() < Duration::from_secs(2),
        "issue owner retirement lost uncertainty or blocked",
    )?;
    ensure(
        matches!(
            client.create_issue(token()?, "321", &issue()).await,
            CreateOutcome::NotSent(_)
        ),
        "retired owner accepted issue creation",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "issue fixture saw an unexpected request",
    )?;
    fs::write(
        root.join("issue-producer-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_descriptors":8,"serialization":true,"in_flight_retirement":"indeterminate","actual_provider":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("PASS: production Rust issue creation binds the immutable repository, unchanged title/body and causal bounded receipt; preflight/no-retry uncertainty/serialization/retirement pass; dummy-only HTTPS, no actual provider or product admission qualification");
    Ok(())
}

fn envelope(path: &str, id: &str, title: &str, body: &str) -> Result<Vec<u8>> {
    let request: ProductRequest = serde_json::from_value(
        json!({"kind":"write","path":path,"body":{"command_id":id,"title":title,"body":body}}),
    )?;
    let (_, _, bytes) = request.request()?;
    Ok(bytes)
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
        "issue product requires empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let saved = store
        .store(account()?, Token::new(b"factory-declared-issue".to_vec())?)
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
    ensure(
        catalogue.0 == 200,
        "issue product canonical catalogue failed",
    )?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let base = format!("/api/factory/collaboration/repositories/{repository}");
    if std::env::var("SODA_NATIVE_CHECK_REQUEST_CAPTURE").as_deref() == Ok("1") {
        let original = "Capture this original request without running a model.";
        let body = serde_json::to_vec(
            &json!({"repository":repository,"body":original,"idempotency_key":"native-request-capture"}),
        )?;
        let saved = call(
            &runtime,
            &store,
            &connection,
            "POST",
            "/api/factory/feed/posts",
            &body,
        )
        .await?;
        ensure(
            matches!(saved.0, 200 | 202),
            &format!("native request submit refused: {} {}", saved.0, saved.1),
        )?;
        let post = saved.1["post_id"]
            .as_str()
            .ok_or("native request post absent")?
            .to_owned();
        let helper = std::env::var("SODA_NATIVE_CHECK_HELPER")?;
        ensure(
            std::process::Command::new(helper)
                .arg(profile)
                .arg(runtime.health()?.instance)
                .arg("declare-request-route")
                .arg(&post)
                .status()?
                .success(),
            "declared routing prerequisite failed",
        )?;
        runtime.start_execution_coordinator().await?;
        let path = format!("/api/factory/feed/posts/{post}");
        let deadline = Instant::now() + Duration::from_secs(60);
        let settled = loop {
            let read = call(&runtime, &store, &connection, "GET", &path, &[]).await?;
            ensure(
                read.0 == 200 && read.1["body"] == original,
                "native request writing changed",
            )?;
            if read.1["effect"] == "verified" && read.1["issue_maker"]["state"] == "waiting" {
                break read.1;
            }
            ensure(
                read.1["attempts"].as_i64() == Some(0),
                &format!("declared native request creation failed: {}", read.1),
            )?;
            ensure(
                Instant::now() < deadline,
                &format!("native request creation did not settle: {}", read.1),
            )?;
            tokio::time::sleep(Duration::from_millis(500)).await;
        };
        ensure(
            settled["issue"].as_i64().is_some_and(|n| n > 0)
                && settled["issue_maker"]["detail"]
                    .as_str()
                    .is_some_and(|s| s.contains("AI refinement is unavailable")),
            "unsupported refinement appeared executable or mislabeled authentication",
        )?;
        ensure(
            posts(&io)? == 1,
            "native request did not create exactly one issue",
        )?;
        let replay = call(
            &runtime,
            &store,
            &connection,
            "POST",
            "/api/factory/feed/posts",
            &body,
        )
        .await?;
        ensure(
            matches!(replay.0, 200 | 202) && replay.1["post_id"] == post && posts(&io)? == 1,
            "native request replay duplicated its effect",
        )?;
        runtime.stop()?;
        runtime = NativeSupervisor::start(payload, production, profile)?;
        store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
        let retained = call(&runtime, &store, &connection, "GET", &path, &[]).await?;
        ensure(
            retained.0 == 200
                && retained.1["issue"] == settled["issue"]
                && retained.1["effect"] == "verified"
                && retained.1["body"] == original
                && posts(&io)? == 1,
            "native request restart lost its exact issue or replayed creation",
        )?;
        store.withdraw(&connection).await?;
        ensure(
            call(
                &runtime,
                &store,
                &connection,
                "POST",
                "/api/factory/feed/posts",
                &body,
            )
            .await
            .is_err()
                && posts(&io)? == 1,
            "withdrawn request account admitted another effect",
        )?;
        runtime.stop()?;
        fs::write(
            root.join("request-capture-result.json"),
            serde_json::to_vec_pretty(
                &json!({"native_post":post,"issue":settled["issue"],"created_once":true,"replay":true,"restart":true,"withdrawal":true,"refinement":"unavailable_no_model","provider_facts":"declared","routing":"declared"}),
            )?,
        )?;
        println!("PASS: native request captures its admitted account/source and creates exactly one issue through the real journal/connector; replay, restart and withdrawal preserve it; unsupported refinement waits without model spending. Provider facts and routing are declared; no connected or installed gate closed.");
        return Ok(());
    }
    let submit = format!("{base}/issues");
    let command = |id: &str| format!("{base}/issue-creation/commands/{id}");
    let mut cases = Vec::new();
    for (i, (title, body)) in [
        ("Original human issue π".to_owned(), "".to_owned()),
        (
            "Ordinary description".into(),
            "Exact writing 🍵\n[link](relative.md)".into(),
        ),
        ("t".repeat(255), "🍵".repeat(16384)),
        ("Control-escaped writing".into(), "\u{0001}".repeat(65536)),
    ]
    .into_iter()
    .enumerate()
    {
        let id = format!("{:032x}", 100 + i);
        let bytes = envelope(&submit, &id, &title, &body)?;
        let before = posts(&io)?;
        let result = call(&runtime, &store, &connection, "POST", &submit, &bytes).await?;
        completed(&result, "committed")?;
        ensure(
            result.1["issue"]["repository"] == repository && result.1["issue"]["kind"] == "issue",
            "creation lost its currently readable canonical destination",
        )?;
        let number = result.1["issue"]["number"]
            .as_str()
            .ok_or("created issue number absent")?
            .parse::<i64>()?;
        let external = result.1["issue"]["external_id"].clone();
        let read = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{base}/issues/{number}"),
            &[],
        )
        .await?;
        ensure(
            read.0 == 200
                && read.1["ref"]["external_id"] == external
                && read.1["title"] == title
                && read.1["body"] == body,
            "created normal issue readback changed original title/body",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "committed",
        )?;
        completed(
            &call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{}/cancel", command(&id)),
                b"{}",
            )
            .await?,
            "committed",
        )?;
        let conflict = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &submit,
            &envelope(&submit, &id, "changed title", &body)?,
        )
        .await?;
        ensure(
            conflict.0 == 409 && posts(&io)? == before + 1,
            "replay/lookup/cancel/title conflict duplicated issue creation",
        )?;
        cases.push(format!("positive-{i}"));
    }
    let before = posts(&io)?;
    let concurrent_id = "33333333333333333333333333333333";
    let bytes = envelope(
        &submit,
        concurrent_id,
        "Concurrent exact intent",
        "One issue",
    )?;
    let (first, second) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &bytes),
        call(&runtime, &store, &connection, "POST", &submit, &bytes)
    );
    for r in [first?, second?] {
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
            &command(concurrent_id),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        posts(&io)? == before + 1,
        "simultaneous issue submissions repeated the POST",
    )?;
    cases.push("concurrent-identical-command".into());
    let other = repository.parse::<i64>()? + 1;
    let other_path = format!("/api/factory/collaboration/repositories/{other}/issues");
    let refused = call(
        &runtime,
        &store,
        &connection,
        "POST",
        &other_path,
        &envelope(
            &other_path,
            "99999999999999999999999999999999",
            "Wrong scope",
            "",
        )?,
    )
    .await?;
    ensure(
        refused.0 == 403 && posts(&io)? == before + 1,
        "issue request expanded native repository scope",
    )?;
    cases.push("other-repository-refusal".into());
    for (title, body) in [
        (" padded ".to_owned(), "".to_owned()),
        ("x".repeat(256), "".into()),
        ("valid".into(), "x".repeat(65537)),
    ] {
        let r = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &submit,
            &envelope(&submit, "22222222222222222222222222222222", &title, &body)?,
        )
        .await?;
        ensure(r.0 == 400, "Go writing bounds were weakened")?;
    }
    // A committed primary survives failed or changed normal destination reads.
    set_mode(&io, "hide-created")?;
    let hidden_id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    let hidden = envelope(
        &submit,
        hidden_id,
        "Owned hidden destination",
        "Original body",
    )?;
    let before = posts(&io)?;
    let r = call(&runtime, &store, &connection, "POST", &submit, &hidden).await?;
    completed(&r, "committed")?;
    ensure(
        r.1["issue"].is_null(),
        "hidden created issue became readable",
    )?;
    set_mode(&io, "destination-change")?;
    let r = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &command(hidden_id),
        &[],
    )
    .await?;
    completed(&r, "committed")?;
    ensure(
        r.1["issue"].is_null(),
        "unrelated destination identity was adopted",
    )?;
    set_mode(&io, "positive")?;
    let r = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &command(hidden_id),
        &[],
    )
    .await?;
    completed(&r, "committed")?;
    ensure(
        !r.1["issue"].is_null() && posts(&io)? == before + 1,
        "destination restoration erased effect or repeated creation",
    )?;
    cases.push("committed-primary-destination-retirement".into());
    let lost_id = "44444444444444444444444444444444";
    let lost = envelope(
        &submit,
        lost_id,
        "Owned lost creation reply",
        "Observed matching writing is not attribution",
    )?;
    set_mode(&io, "drop-reply")?;
    let before = posts(&io)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    set_mode(&io, "positive")?;
    let read = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{submit}?limit=50"),
        &[],
    )
    .await?;
    ensure(
        read.0 == 200
            && read.1["items"]
                .as_array()
                .is_some_and(|v| v.iter().any(|x| x["title"] == "Owned lost creation reply")),
        "lost-reply fixture did not really create a readable issue",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "matching issue search/replay manufactured creation attribution",
    )?;
    cases.push("lost-reply-readback-remains-uncertain".into());
    for (name, effect, id) in [
        (
            "pause-post",
            "committed",
            "55555555555555555555555555555555",
        ),
        (
            "pause-preflight",
            "not_committed",
            "66666666666666666666666666666666",
        ),
    ] {
        set_mode(&io, name)?;
        let before = posts(&io)?;
        let bytes = envelope(
            &submit,
            id,
            "Owned cancellation race",
            "Exact original body",
        )?;
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
            "cancel invented a no-effect acknowledgment",
        )?;
        fs::write(io.join("release"), b"")?;
        let reply = pending.await?;
        let r = (
            reply.status,
            serde_json::from_slice::<serde_json::Value>(&reply.body)?,
        );
        completed(&r, effect)?;
        ensure(
            r.1["cancellation"] == "indeterminate"
                && posts(&io)? == before + u64::from(effect == "committed"),
            "late primary evidence/cancellation lost or duplicate dispatched",
        )?;
        cases.push(name.into());
    }
    set_mode(&io, "pause-post")?;
    let withdrawn_id = "77777777777777777777777777777777";
    let withdrawn = envelope(&submit, withdrawn_id, "Owned withdrawal race", "")?;
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
        .store(account()?, Token::new(b"factory-declared-issue".to_vec())?)
        .await?
        .connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "withdrawn/replaced generation delivered an old creation result",
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
        "reconsent replayed an unknown issue",
    )?;
    cases.push("withdrawal-replacement".into());
    set_mode(&io, "pause-post")?;
    let stopped_id = "88888888888888888888888888888888";
    let stopped = envelope(&submit, stopped_id, "Owned restart after dispatch", "")?;
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
        "stopped runtime delivered an in-flight issue",
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
    for i in 0..4 {
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "GET",
                &command(&format!("{:032x}", 100 + i)),
                &[],
            )
            .await?,
            "committed",
        )?;
    }
    ensure(
        posts(&io)? == before + 1,
        "retained restart repeated issue creation",
    )?;
    cases.push("retained-restart".into());
    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?
                .with_label("declared-other")?,
            Token::new(b"factory-declared-other".to_vec())?,
        )
        .await?;
    let hidden = call(
        &runtime,
        &store,
        &other.connection,
        "GET",
        &command(lost_id),
        &[],
    )
    .await?;
    ensure(hidden.0 == 404, "other account read the captured issue")?;
    let conflict = call(&runtime, &store, &other.connection, "POST", &submit, &lost).await?;
    ensure(
        conflict.0 == 409 && posts(&io)? == before + 1,
        "other account adopted or dispatched a captured issue",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "issue product fixture saw an unexpected request",
    )?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "issue product left owned grants",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("issue-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_provider":false,"native_product_consumer":true,"capabilities_enabled":false,"provider_posts":posts(&io)?,"retained_restart":true,"maximum_writing_bytes":65536}),
        )?,
    )?;
    println!("PASS: composed native/Rust/Go/PG issue creation retains exact writing, causal identity, readable destination, one-shot replay, ambiguous replies, cancel races, withdrawal, other-account refusal and restart; dummy-only provider, no actual GitHub or installed gate qualification");
    Ok(())
}
