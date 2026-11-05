//! Real native producer acceptance; only an owned no-forwarding dummy peer.
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::github_provider::{DiscussionLock, Owner, WriteOutcome};
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
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
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
