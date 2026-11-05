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
