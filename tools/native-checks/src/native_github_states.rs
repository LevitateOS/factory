//! Existing real native integration executable; dummy-only owned HTTPS peer.
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::github_provider::{DiscussionState, Owner, StateOutcome};
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
