//! Production native reader against an owned no-forwarding declared peer.
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::{
    credentials::Token,
    github_provider::{Owner, ThreadRead},
};
use std::{
    fs,
    path::Path,
    time::{Duration, Instant},
};

pub(super) async fn producer(profile: &Path) -> Result<()> {
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let token = || Token::new(b"factory-declared-thread".to_vec());
    let request = || ThreadRead {
        repository: "123".into(),
        number: 7,
        page: 1,
        limit: 1,
        thread_id: String::new(),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "positive",
        "page-two",
        "detail",
        "detail-page-two",
        "nullable",
        "empty",
        "no-anchor",
        "big-id",
    ] {
        mode(name)?;
        let mut q = request();
        if name == "page-two" {
            q.page = 2;
        }
        if name.starts_with("detail") {
            q.thread_id = "PRRT_declared1".into();
            q.limit = 2;
        }
        if name == "detail-page-two" {
            q.page = 2;
        }
        let value = client
            .observe_review_threads(token()?, "321", &q)
            .await
            .map_err(|e| format!("thread positive {name}: {} {}", e.status, e.code))?;
        ensure(
            value["kind"] == "pull_review_threads"
                && value["account"]["external_id"] == "321"
                && value["threads"]["repository"] == "123"
                && value["threads"]["pull"]["external_id"] == "700",
            "thread read lost binding",
        )?;
        let items = value["threads"]["items"]
            .as_array()
            .ok_or("thread items absent")?;
        if name == "empty" {
            ensure(
                items.is_empty() && value["threads"]["next_page"] == 0,
                "empty invented threads",
            )?;
        } else {
            ensure(items.len() == 1, "thread window ignored")?;
            let item = &items[0];
            let second = name == "page-two";
            ensure(
                item["external_id"]
                    == if second {
                        "PRRT_declared2"
                    } else {
                        "PRRT_declared1"
                    }
                    && item["resolution"] == if second { "resolved" } else { "unresolved" }
                    && item["outdated"] == second
                    && item["viewer_can_resolve"] == !second
                    && item["viewer_can_unresolve"] == second,
                "thread identity/state/action facts changed",
            )?;
            let comments = item["comments"]["items"]
                .as_array()
                .ok_or("comments absent")?;
            ensure(
                !comments.is_empty() && comments[0]["body"] == "exact **Markdown**\n\nfixture",
                "comment content changed",
            )?;
            if name == "nullable" {
                ensure(
                    comments[0]["author"].is_null() && comments[0]["review_id"].is_null(),
                    "unknown links invented",
                )?;
            } else {
                ensure(
                    comments[0]["author"]["external_id"] == "321"
                        && comments[0]["review_id"] == "800",
                    "comment links lost",
                )?;
            }
            if name == "no-anchor" {
                ensure(
                    item["recorded_location"].is_null(),
                    "invented current-head coordinates",
                )?;
            } else {
                ensure(
                    item["recorded_location"]
                        == json!({"commit":"a".repeat(40),"path":"src/fixture.rs","side":"new","line":9}),
                    "recorded coordinates changed",
                )?;
            }
            if name == "detail" {
                ensure(
                    comments.len() == 2 && item["comments"]["next_page"] == 2,
                    "comment first page lost",
                )?;
            }
            if name == "detail-page-two" {
                ensure(
                    comments.len() == 1
                        && comments[0]["external_id"] == "903"
                        && item["comments"]["next_page"] == 0,
                    "comment offset lost",
                )?;
            }
            if name == "big-id" {
                ensure(
                    comments[0]["external_id"] == "5000000000",
                    "BigInt comment truncated",
                )?;
            }
            if name == "positive" {
                ensure(value["threads"]["next_page"] == 2, "thread next page lost")?;
            }
        }
        fs::write(
            root.join(format!("{name}.json")),
            serde_json::to_vec(&value)?,
        )?;
        cases.push(name);
    }
    for (name, status) in [
        ("identity-change", 409),
        ("identity-denied", 401),
        ("wrong-thread-pull", 409),
        ("wrong-thread-repository", 409),
        ("wrong-parent", 409),
        ("duplicate-thread", 502),
        ("duplicate-comment", 502),
        ("count-change", 409),
        ("count-mismatch", 409),
        ("cursor-loop", 502),
        ("empty-continuation", 502),
        ("missing-cursor", 502),
        ("unknown-state", 502),
        ("unknown-viewer", 502),
        ("wrong-comment-scope", 502),
        ("bad-date", 502),
        ("bad-comment-node", 502),
        ("comment-count", 502),
        ("partial-errors", 502),
        ("missing-thread", 404),
        ("wrong-type", 502),
        ("denied", 403),
        ("validation", 400),
        ("redirect", 409),
        ("too-many-threads", 413),
        ("oversized", 413),
        ("changed-recheck", 409),
        ("deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if matches!(name, "missing-thread" | "wrong-type") {
            q.thread_id = "PRRT_declared1".into();
        }
        let start = Instant::now();
        let failure = client
            .observe_review_threads(token()?, "321", &q)
            .await
            .err()
            .ok_or_else(|| format!("accepted thread refusal {name}"))?;
        ensure(
            failure.status == status,
            &format!("thread refusal {name}: {} {}", failure.status, failure.code),
        )?;
        if name == "deadline" {
            ensure(
                start.elapsed() < Duration::from_secs(10),
                "thread deadline unbounded",
            )?;
        }
        cases.push(name);
    }
    mode("positive")?;
    for i in 0..8 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.number = 0,
            2 => q.page = 0,
            3 => q.page = 1_000_001,
            4 => q.limit = 21,
            5 => q.thread_id = "901".into(),
            6 => q.thread_id = "x".repeat(1025),
            _ => q.thread_id = "PRRT_\n".into(),
        }
        let before = fs::read(io.join("gets"))?;
        let f = client
            .observe_review_threads(token()?, "321", &q)
            .await
            .err()
            .ok_or("accepted invalid descriptor")?;
        ensure(
            f.status == 400 && fs::read(io.join("gets"))? == before,
            "invalid/legacy descriptor reached provider",
        )?;
    }
    mode("retirement")?;
    let flight = {
        let client = client.clone();
        let q = request();
        tokio::spawn(async move {
            client
                .observe_review_threads(token().unwrap(), "321", &q)
                .await
        })
    };
    let end = Instant::now() + Duration::from_secs(3);
    while !io.join("entered").exists() {
        ensure(Instant::now() < end, "thread retirement never entered")?;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    let start = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        flight.await?.is_err() && start.elapsed() < Duration::from_secs(3),
        "thread read outlived owner",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "reader sent an undeclared request",
    )?;
    fs::write(
        root.join("thread-producer-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":8,"retirement":true,"real_github":false,"canonical_cutover":false}),
        )?,
    )?;
    println!("native GraphQL thread producer passed {} declared cases, 8 invalid descriptors and retirement",cases.len());
    Ok(())
}

pub(super) async fn product(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    use super::native_github_fixture::{call, set_mode};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
    };
    use std::sync::Arc;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    set_mode(&io, "product-positive")?;
    let mut runtime = NativeSupervisor::start(payload, production, profile)?;
    let mut store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "thread check needs empty owned custody",
    )?;
    let saved = store
        .store(
            Account::new(Provider::Github, "https://github.com", "321")?
                .with_label("declared-reader")?,
            Token::new(b"factory-declared-thread".to_vec())?,
        )
        .await?;
    let connection = saved.connection;
    let catalogue = call(
        &runtime,
        &store,
        &connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "thread canonical catalogue refused")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let target = format!("/api/factory/collaboration/repositories/{repository}/pulls/7/threads");
    let first = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{target}?limit=1"),
        &[],
    )
    .await?;
    ensure(
        first.0 == 200,
        &format!("thread first page refused: {} {}", first.0, first.1),
    )?;
    let first_item = &first.1["items"][0];
    ensure(
        first_item["external_id"] == "PRRT_declared1"
            && first_item["pull"]["repository"] == repository
            && first_item["resolution"] == "unresolved"
            && first_item["outdated"] == false
            && first_item["capabilities"].is_null()
            && first_item["anchor"].is_null()
            && first_item["current_anchor"].is_null()
            && first_item["recorded_location"]["line"] == 9
            && first_item["comments"]["items"]
                .as_array()
                .is_some_and(|v| v.len() == 10)
            && first_item["comments"]["items"][0]["comment"]["author"]["id"]
                .as_str()
                .is_some(),
        "canonical thread facts/actor mapping lost",
    )?;
    let next = first.1["next"]
        .as_str()
        .filter(|s| !s.is_empty())
        .ok_or("thread cursor absent")?;
    let second = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{target}?limit=1&cursor={next}"),
        &[],
    )
    .await?;
    ensure(
        second.0 == 200
            && second.1["items"][0]["external_id"] == "PRRT_declared2"
            && second.1["items"][0]["resolution"] == "resolved"
            && second.1["items"][0]["outdated"] == true
            && second.1["complete"] == true,
        "canonical thread continuation changed identity/state",
    )?;
    let comment_next = first_item["comments"]["next"]
        .as_str()
        .filter(|s| !s.is_empty())
        .ok_or("preview comment cursor absent")?;
    let continued = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{target}/PRRT_declared1/comments?limit=20&cursor={comment_next}"),
        &[],
    )
    .await?;
    ensure(
        continued.0 == 200
            && continued.1["items"]
                .as_array()
                .is_some_and(|v| v.len() == 2)
            && continued.1["items"][0]["comment"]["external_id"] == "911"
            && continued.1["complete"] == true,
        "preview continuation changed comment page size",
    )?;
    let detail = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{target}/PRRT_declared1"),
        &[],
    )
    .await?;
    ensure(
        detail.0 == 200
            && detail.1["external_id"] == "PRRT_declared1"
            && detail.1["comments"]["items"]
                .as_array()
                .is_some_and(|v| v.len() == 10),
        "canonical thread detail refused",
    )?;
    let mut cases = vec![
        "catalogue",
        "thread-pages",
        "comment-preview-continuation",
        "detail",
        "canonical-actor",
        "unknown-anchor",
    ];
    // Captured REST cursors refuse even when all resource/page fields otherwise match.
    let legacy_cursor = base64::Engine::encode(
        &base64::engine::general_purpose::URL_SAFE_NO_PAD,
        serde_json::to_vec(
            &json!({"kind":"pull_threads","repository":"123","number":7,"page":2,"limit":1}),
        )?,
    );
    for suffix in [
        format!("?limit=1&cursor={legacy_cursor}"),
        "/901".into(),
        format!("/PRRT_declared2/comments?limit=20&cursor={comment_next}"),
    ] {
        let before = fs::read(io.join("queries"))?;
        ensure(
            call(
                &runtime,
                &store,
                &connection,
                "GET",
                &format!("{target}{suffix}"),
                &[],
            )
            .await?
            .0 == 400
                && fs::read(io.join("queries"))? == before,
            "legacy/numeric/other-thread cursor reached provider",
        )?;
    }
    cases.push("legacy-and-mismatched-cursor-refusal");
    for name in ["nullable", "no-anchor", "big-id", "empty"] {
        set_mode(&io, &format!("product-{name}"))?;
        let result = call(&runtime, &store, &connection, "GET", &target, &[]).await?;
        ensure(
            result.0 == 200,
            &format!("canonical thread {name} refused: {} {}", result.0, result.1),
        )?;
        let item = &result.1["items"][0];
        match name {
            "nullable" => ensure(
                item["comments"]["items"][0]["review_id"].is_null()
                    && item["comments"]["items"][0]["comment"]["author"].is_null(),
                "canonical consumer invented unknown links",
            )?,
            "no-anchor" => ensure(
                item["recorded_location"].is_null() && item["anchor"].is_null(),
                "canonical consumer invented anchor",
            )?,
            "big-id" => ensure(
                item["comments"]["items"][0]["comment"]["external_id"] == "5000000000",
                "canonical BigInt truncated",
            )?,
            _ => ensure(
                result.1["items"].as_array().is_some_and(Vec::is_empty),
                "canonical empty catalogue invented thread",
            )?,
        }
        cases.push(name);
    }
    for (name, status) in [
        ("wrong-thread-pull", 409),
        ("wrong-thread-repository", 409),
        ("count-change", 409),
        ("partial-errors", 502),
        ("too-many-threads", 413),
    ] {
        set_mode(&io, &format!("product-{name}"))?;
        let result = call(&runtime, &store, &connection, "GET", &target, &[]).await?;
        ensure(
            result.0 == status,
            &format!("canonical refusal {name}: {} {}", result.0, result.1),
        )?;
        cases.push(name);
    }
    set_mode(&io, "product-positive")?;
    store.retire();
    runtime.stop()?;
    runtime = NativeSupervisor::start(payload, production, profile)?;
    store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    let restarted = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{target}?limit=1&cursor={next}"),
        &[],
    )
    .await?;
    ensure(
        restarted.0 == 200 && restarted.1["items"][0]["external_id"] == "PRRT_declared2",
        "canonical identity/cursor failed retained restart",
    )?;
    cases.push("retained-restart");
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "thread check left owned account",
    )?;
    store.retire();
    runtime.stop()?;
    ensure(
        !io.join("unexpected-request").exists(),
        "canonical reader sent undeclared request",
    )?;
    fs::write(
        root.join("thread-product-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"native_go_postgresql":true,"legacy_cursor_refused":true,"real_github":false,"capabilities_enabled":false,"owned_grant_removed":true}),
        )?,
    )?;
    fs::write(
        root.join("thread-first-page.json"),
        serde_json::to_vec(&first.1)?,
    )?;
    println!(
        "native GraphQL thread canonical consumer passed {} declared cases and retained restart",
        cases.len()
    );
    Ok(())
}

pub(super) async fn resolution(profile: &Path) -> Result<()> {
    use soda_desktop_runtime::github_provider::{
        OriginalThreadAnchor, ThreadResolution, WriteOutcome,
    };
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let posts = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("posts"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let token = || Token::new(b"factory-declared-thread-effect".to_vec());
    let request = || ThreadResolution {
        repository: "123".into(),
        pull_id: "700".into(),
        number: 7,
        thread_id: "PRRT_declared1".into(),
        resolved: true,
        original_anchor: None,
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let mut client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "resolve",
        "unresolve",
        "outdated",
        "big-id",
        "no-request-id",
    ] {
        mode(name)?;
        let mut q = request();
        q.resolved = name != "unresolve";
        if name == "big-id" {
            q.pull_id = "5000000000".into();
        }
        let before = posts()?;
        let WriteOutcome::Applied(r) = client.set_thread_resolution(token()?, "321", &q).await
        else {
            return Err(format!("thread effect positive refused {name}").into());
        };
        ensure(
            r["account"]["external_id"] == "321"
                && r["repository"] == "123"
                && r["pull_id"] == q.pull_id
                && r["number"] == 7
                && r["thread_id"] == q.thread_id
                && r["resolved"] == q.resolved
                && r["original_anchor"].is_null()
                && r["provider_response"]["status"] == 200
                && r["provider_response"]["protocol"] == "github_graphql_thread_resolution"
                && r["provider_response"]["response_sha256"]
                    .as_str()
                    .is_some_and(|s| s.len() == 64)
                && serde_json::to_vec(&r)?.len() <= 4096
                && posts()? == before + 1,
            "thread causal receipt lost scope/state/bounds or retried",
        )?;
        if q.resolved {
            ensure(
                r["resolved_by"]["external_id"] == "321"
                    && r["resolved_by"]["node_id"] == "U_declared321",
                "resolver identity lost",
            )?;
        } else {
            ensure(r["resolved_by"].is_null(), "unresolve invented an actor")?;
        }
        if name == "outdated" {
            ensure(r["outdated"] == true, "outdated state dropped")?;
        }
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
        ("missing-actor-node", 502),
        ("repository-change", 409),
        ("missing-repository-node", 502),
        ("subject-change", 409),
        ("missing-pull-node", 502),
        ("locked", 403),
        ("lock-unknown", 502),
        ("archived", 403),
        ("disabled", 403),
        ("readonly-unknown", 403),
        ("preflight-wrong-pull", 409),
        ("preflight-wrong-repository", 409),
        ("preflight-type", 502),
        ("preflight-partial-errors", 502),
        ("permission-denied", 403),
        ("permission-unknown", 502),
        ("already-resolved", 409),
        ("already-unresolved", 409),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if name == "already-unresolved" {
            q.resolved = false;
        }
        let before = posts()?;
        let WriteOutcome::NotSent(f) = client.set_thread_resolution(token()?, "321", &q).await
        else {
            return Err(format!("thread preflight dispatched {name}").into());
        };
        ensure(
            f.status == status && posts()? == before,
            &format!("thread preflight {name}: {} {}", f.status, f.code),
        )?;
        if name.starts_with("already-") {
            ensure(
                f.code == "github_thread_already_desired",
                "no-op acquired a receipt",
            )?;
        }
        cases.push(name);
    }
    mode("resolve")?;
    for i in 0..8 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.pull_id = "0700".into(),
            2 => q.number = 0,
            3 => q.number = u64::MAX,
            4 => q.thread_id = "901".into(),
            5 => q.thread_id.clear(),
            6 => q.thread_id = "T".repeat(1025),
            _ => q.thread_id = "PRRT_\n".into(),
        }
        let before = fs::read(io.join("gets"))?;
        ensure(
            matches!(client.set_thread_resolution(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.status==400)
                && fs::read(io.join("gets"))? == before,
            "invalid thread effect reached provider",
        )?;
    }
    let mut q = request();
    q.original_anchor = Some(OriginalThreadAnchor {
        base: "a".repeat(40),
        head: "b".repeat(40),
        path: "src/fixture.rs".into(),
        side: "new".into(),
        scope: "lines".into(),
        start_line: 9,
        end_line: 9,
    });
    let before = fs::read(io.join("gets"))?;
    ensure(
        matches!(client.set_thread_resolution(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.status==409 && f.code=="github_original_anchor_unobserved")
            && fs::read(io.join("gets"))? == before,
        "unproved original pair silently discarded or sent",
    )?;
    for (name, status) in [
        ("denied", 403),
        ("limited", 429),
        ("validation", 400),
        ("server-error", 503),
        ("redirect", 409),
        ("malformed", 502),
        ("wrong-thread", 502),
        ("wrong-pull", 502),
        ("wrong-repository", 502),
        ("wrong-thread-repo", 502),
        ("wrong-number", 502),
        ("wrong-full-id", 502),
        ("wrong-resolved", 502),
        ("unknown-outdated", 502),
        ("wrong-resolver", 502),
        ("missing-resolver", 502),
        ("wrong-url", 502),
        ("partial-errors", 502),
        ("graphql-errors", 502),
        ("oversized", 413),
        ("oversized-request-id", 502),
        ("duplicate-request-id", 502),
        ("receipt-oversized", 413),
        ("drop-reply", 503),
        ("deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if name == "receipt-oversized" {
            q.thread_id = "T".repeat(1024);
        }
        let before = posts()?;
        let start = Instant::now();
        let WriteOutcome::Indeterminate(f) =
            client.set_thread_resolution(token()?, "321", &q).await
        else {
            return Err(
                format!("thread after-dispatch failure attributed success/absence {name}").into(),
            );
        };
        ensure(
            f.status == status && posts()? == before + 1,
            &format!("thread uncertainty {name}: {} {}", f.status, f.code),
        )?;
        ensure(
            start.elapsed() < Duration::from_secs(10),
            "thread effect exceeded whole deadline",
        )?;
        cases.push(name);
        if name == "drop-reply" {
            mode("after-drop")?;
            ensure(
                matches!(client.set_thread_resolution(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.code=="github_thread_already_desired")
                    && posts()? == before + 1,
                "matching state attributed the lost reply or retried",
            )?;
        }
    }
    mode("missing-unresolve-resolver")?;
    let mut q = request();
    q.resolved = false;
    ensure(
        matches!(client.set_thread_resolution(token()?,"321",&q).await,WriteOutcome::Indeterminate(f)if f.status==502),
        "absent resolver was treated as known null",
    )?;
    cases.push("missing-unresolve-resolver");
    mode("serialize")?;
    let a = request();
    let b = request();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.set_thread_resolution(token()?, "321", &a),
        clone.set_thread_resolution(token()?, "321", &b)
    );
    ensure(
        matches!(first, WriteOutcome::Applied(_))
            && matches!(second, WriteOutcome::Applied(_))
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "thread mutation pacing/serialization failed",
    )?;
    for (name, attempted) in [("pause-preflight", false), ("pause-post", true)] {
        for marker in ["entered", "release", "cancelled"] {
            let _ = fs::remove_file(io.join(marker));
        }
        mode(name)?;
        let q = request();
        let t = token()?;
        let flight_client = client.clone();
        let pending =
            async move { Ok::<_, &str>(flight_client.set_thread_resolution(t, "321", &q).await) };
        tokio::pin!(pending);
        super::native_github_fixture::entered(pending.as_mut(), &io).await?;
        let start = Instant::now();
        owner.stop()?;
        let result = pending.await?;
        ensure(
            if attempted {
                matches!(result, WriteOutcome::Indeterminate(_))
            } else {
                matches!(result, WriteOutcome::NotSent(_))
            },
            "retirement lost dispatch boundary",
        )?;
        ensure(
            start.elapsed() < Duration::from_secs(2),
            "thread owner retirement blocked",
        )?;
        if !attempted {
            owner = tokio::task::spawn_blocking(Owner::start).await??;
            client = owner.client();
        }
    }
    ensure(
        matches!(
            client
                .set_thread_resolution(token()?, "321", &request())
                .await,
            WriteOutcome::NotSent(_)
        ),
        "retired owner accepted a thread mutation",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "thread effect fixture saw an undeclared request",
    )?;
    fs::write(
        root.join("thread-resolution-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":8,"unproved_original_anchor_refused":true,"serialization":true,"preflight_retirement":"not_sent","post_retirement":"indeterminate","real_github":false,"native_callback_connected":false}),
        )?,
    )?;
    println!("native thread resolve/unresolve passed {} declared cases, 8 invalid descriptors, unproved anchor refusal, pacing and both retirement boundaries",cases.len());
    Ok(())
}

pub(super) async fn resolution_product(
    production: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    use super::native_github_fixture::{call, completed, set_mode, uncertain};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
        ProductRequest,
    };
    use std::sync::Arc;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let posts = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("posts"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let envelope = |path: &str,
                    id: &str,
                    thread: &str,
                    resolved: bool,
                    anchor: serde_json::Value|
     -> Result<Vec<u8>> {
        let q: ProductRequest = serde_json::from_value(
            json!({"kind":"write","path":path,"body":{"command_id":id,"thread_id":thread,"resolved":resolved,"anchor":anchor}}),
        )?;
        Ok(q.request()?.2)
    };
    set_mode(&io, "positive")?;
    let mut runtime = NativeSupervisor::start(payload, production, profile)?;
    let mut store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "thread product needs empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-reader"))
    };
    let mut connection = store
        .store(
            account()?,
            Token::new(b"factory-declared-thread-effect".to_vec())?,
        )
        .await?
        .connection;
    let catalogue = call(
        &runtime,
        &store,
        &connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "thread product catalogue refused")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let base = format!("/api/factory/collaboration/repositories/{repository}/pulls/7");
    let submit = format!("{base}/thread-resolutions");
    let command = |id: &str| format!("{submit}/commands/{id}");
    let selection_target = format!("{base}/threads/PRRT_declared1/selection");
    let selection = call(&runtime, &store, &connection, "GET", &selection_target, &[]).await?;
    ensure(
        selection.0 == 200
            && selection.1["thread_id"] == "PRRT_declared1"
            && selection.1["resolved"] == false
            && selection.1["original_anchor"].is_null()
            && selection.1["resolve"]["state"] == "allowed"
            && selection.1["unresolve"]["state"] == "denied"
            && posts()? == 0,
        "thread selection invented context/state/authority or wrote",
    )?;
    let mut cases = vec!["safe-selection".to_owned()];
    for (i, resolved) in [true, false].into_iter().enumerate() {
        let id = format!("{:032x}", 100 + i);
        let body = envelope(
            &submit,
            &id,
            "PRRT_declared1",
            resolved,
            serde_json::Value::Null,
        )?;
        let before = posts()?;
        let reply = call(&runtime, &store, &connection, "POST", &submit, &body).await?;
        completed(&reply, "committed")?;
        let read = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{base}/threads/PRRT_declared1"),
            &[],
        )
        .await?;
        ensure(
            read.0 == 200
                && read.1["resolution"] == if resolved { "resolved" } else { "unresolved" }
                && read.1["anchor"].is_null(),
            "thread normal readback lost effect/context",
        )?;
        let selected = call(&runtime, &store, &connection, "GET", &selection_target, &[]).await?;
        ensure(
            selected.0 == 200 && selected.1["resolved"] == resolved
                && selected.1["resolve"]["state"] == if resolved { "denied" } else { "allowed" }
                && selected.1["unresolve"]["state"] == if resolved { "allowed" } else { "denied" },
            "selection lost current state or provider action facts",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &body).await?,
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
        for (thread, state, anchor) in [
            ("PRRT_declared1", !resolved, serde_json::Value::Null),
            ("PRRT_other", resolved, serde_json::Value::Null),
            (
                "PRRT_declared1",
                resolved,
                json!({"base":"a".repeat(40),"head":"b".repeat(40),"path":"src/fixture.rs","side":"new","scope":"lines","start_line":9,"end_line":9}),
            ),
        ] {
            ensure(
                call(
                    &runtime,
                    &store,
                    &connection,
                    "POST",
                    &submit,
                    &envelope(&submit, &id, thread, state, anchor)?,
                )
                .await?
                .0 == 409,
                "changed thread/state/context adopted command",
            )?;
        }
        ensure(
            posts()? == before + 1,
            "thread replay/lookup/cancel/conflict repeated POST",
        )?;
        cases.push(format!("positive-{i}"));
    }
    let no_op_id = "22222222222222222222222222222222";
    let no_op = envelope(
        &submit,
        no_op_id,
        "PRRT_declared1",
        false,
        serde_json::Value::Null,
    )?;
    let before = posts()?;
    completed(
        &call(&runtime, &store, &connection, "POST", &submit, &no_op).await?,
        "not_committed",
    )?;
    completed(
        &call(&runtime, &store, &connection, "POST", &submit, &no_op).await?,
        "not_committed",
    )?;
    ensure(posts()? == before, "already desired state sent a mutation")?;
    cases.push("known-no-send-no-op".into());
    let full = json!({"base":"a".repeat(40),"head":"b".repeat(40),"path":"p".repeat(4096),"side":"new","scope":"lines","start_line":9,"end_line":9});
    ensure(
        call(
            &runtime,
            &store,
            &connection,
            "POST",
            &submit,
            &envelope(
                &submit,
                "23232323232323232323232323232323",
                "PRRT_declared1",
                true,
                full,
            )?,
        )
        .await?
        .0 == 409
            && posts()? == before,
        "unproved full anchor was discarded or sent",
    )?;
    cases.push("full-original-anchor-refusal".into());
    let pre_cancel_id = "24242424242424242424242424242424";
    // Establish a saved command through a preflight pause after capture/reserve;
    // the separate SQL driver covers cancellation winning before reservation.
    set_mode(&io, "concurrent")?;
    let concurrent_id = "33333333333333333333333333333333";
    let concurrent = envelope(
        &submit,
        concurrent_id,
        "PRRT_declared1",
        true,
        serde_json::Value::Null,
    )?;
    let before = posts()?;
    let (a, b) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &concurrent),
        call(&runtime, &store, &connection, "POST", &submit, &concurrent)
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
            &command(concurrent_id),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        posts()? == before + 1,
        "concurrent thread command dispatched twice",
    )?;
    cases.push("concurrent-identical-command".into());
    let wrong = format!(
        "/api/factory/collaboration/repositories/{}/pulls/7/thread-resolutions",
        repository.parse::<i64>()? + 1
    );
    ensure(
        call(
            &runtime,
            &store,
            &connection,
            "POST",
            &wrong,
            &envelope(
                &wrong,
                "99999999999999999999999999999999",
                "PRRT_declared1",
                true,
                serde_json::Value::Null,
            )?,
        )
        .await?
        .0 == 403
            && posts()? == before + 1,
        "thread write expanded selected repository",
    )?;
    cases.push("other-repository-refusal".into());
    set_mode(&io, "drop-reply")?;
    let lost_id = "44444444444444444444444444444444";
    let lost = envelope(
        &submit,
        lost_id,
        "PRRT_declared1",
        true,
        serde_json::Value::Null,
    )?;
    let before = posts()?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    set_mode(&io, "observe-after-loss")?;
    let read = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{base}/threads/PRRT_declared1"),
        &[],
    )
    .await?;
    ensure(
        read.0 == 200 && read.1["resolution"] == "resolved",
        "fixture lost reply did not apply desired state",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        posts()? == before + 1,
        "matching state attributed lost thread reply or resent",
    )?;
    cases.push("lost-reply-no-state-attribution".into());
    for (name, id, effect) in [
        (
            "pause-post",
            "55555555555555555555555555555555",
            "committed",
        ),
        ("pause-preflight", pre_cancel_id, "not_committed"),
    ] {
        set_mode(&io, name)?;
        let body = envelope(&submit, id, "PRRT_declared1", true, serde_json::Value::Null)?;
        let before = posts()?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let pending = request.request("POST", &submit, &body);
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
            "racing cancellation claimed rollback",
        )?;
        fs::write(io.join("release"), b"")?;
        let response = pending.await?;
        let reply = (response.status, serde_json::from_slice(&response.body)?);
        completed(&reply, effect)?;
        ensure(
            reply.1["cancellation"] == "indeterminate"
                && posts()? == before + u64::from(name == "pause-post"),
            "late thread result lost cancellation uncertainty or retried",
        )?;
        cases.push(name.into());
    }
    set_mode(&io, "pause-withdrawal")?;
    let withdrawn_id = "77777777777777777777777777777777";
    let withdrawn = envelope(
        &submit,
        withdrawn_id,
        "PRRT_declared1",
        true,
        serde_json::Value::Null,
    )?;
    let before = posts()?;
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
        .store(
            account()?,
            Token::new(b"factory-declared-thread-effect".to_vec())?,
        )
        .await?
        .connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "withdrawn generation returned thread result",
    )?;
    set_mode(&io, "observe-after-withdrawal")?;
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
        posts()? == before + 1,
        "replacement custody repeated uncertain thread write",
    )?;
    cases.push("withdrawal-replacement".into());
    set_mode(&io, "pause-stop")?;
    let stopped_id = "88888888888888888888888888888888";
    let stopped = envelope(
        &submit,
        stopped_id,
        "PRRT_declared1",
        true,
        serde_json::Value::Null,
    )?;
    let before = posts()?;
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
        "stopped native runtime returned a thread result",
    )?;
    store.retire();
    runtime = NativeSupervisor::start(payload, production, profile)?;
    store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    set_mode(&io, "observe-after-stop")?;
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
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(&format!("{:032x}", 100)),
            &[],
        )
        .await?,
        "committed",
    )?;
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(no_op_id),
            &[],
        )
        .await?,
        "not_committed",
    )?;
    ensure(
        posts()? == before + 1,
        "restart repeated thread dispatch or lost terminal state",
    )?;
    cases.push("retained-restart".into());
    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?,
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
        "another account read thread capture",
    )?;
    ensure(
        call(&runtime, &store, &other.connection, "POST", &submit, &lost)
            .await?
            .0
            == 409
            && posts()? == before + 1,
        "another account adopted thread capture",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "thread product left owned grant",
    )?;
    store.retire();
    runtime.stop()?;
    ensure(
        !io.join("unexpected-request").exists(),
        "thread product fixture saw undeclared request",
    )?;
    fs::write(
        root.join("thread-resolution-product-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"provider_posts":posts()?,"native_go_postgresql":true,"real_github":false,"known_original_context_supported":false,"shared_write_capabilities_enabled":false,"owned_accounts_removed":true}),
        )?,
    )?;
    println!(
        "composed native thread resolution passed {} declared cases with {} dummy POSTs",
        cases.len(),
        posts()?
    );
    Ok(())
}
