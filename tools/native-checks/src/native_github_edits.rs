//! Real producer acceptance in the existing native integration executable.
//! Run only against an owned no-forwarding peer with literal dummy grants.
use super::{ensure, prepare, Result};
use serde_json::json;
use sha2::{Digest, Sha256};
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::github_provider::{DiscussionEdit, Owner, WriteOutcome};
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::Path,
    time::{Duration, Instant},
};
fn sha(s: &str) -> String {
    Sha256::digest(s.as_bytes())
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
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
    let token = || Token::new(b"factory-declared-edit".to_vec());
    let edit = || DiscussionEdit {
        repository: "123".into(),
        subject: "issue".into(),
        subject_id: "700".into(),
        number: 7,
        title: Some("Exact new title π".into()),
        body: Some("Original human writing 🍵\n[link](relative.md)".repeat(256)),
        original_title: "Owned fixture subject".into(),
        original_body_sha256: sha("Original description π\n"),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "issue-full",
        "pull-full",
        "title-only",
        "body-only",
        "empty-body",
        "max-unicode",
        "max-control",
        "big-id",
        "repository-slot-reused",
        "no-request-id",
        "null-original-body",
        "padded-title",
    ] {
        mode(name)?;
        let mut q = edit();
        match name {
            "pull-full" => q.subject = "pull".into(),
            "title-only" => q.body = None,
            "body-only" => q.title = None,
            "empty-body" => q.body = Some("".into()),
            "max-unicode" => {
                q.title = Some("t".repeat(255));
                q.body = Some("🍵".repeat(16384));
            }
            "max-control" => q.body = Some("\u{0001}".repeat(65536)),
            "big-id" => q.subject_id = "5000000000".into(),
            "null-original-body" => {
                q.original_body_sha256 = sha("");
                q.body = None;
            }
            "padded-title" => q.title = Some(" exact padded writing ".into()),
            _ => (),
        }
        let before = posts(&io)?;
        let outcome = client.edit_discussion(token()?, "321", &q).await;
        let WriteOutcome::Applied(r) = outcome else {
            return Err(format!("edit positive refused: {name}").into());
        };
        let title = q.title.as_deref().unwrap_or(&q.original_title);
        let body = q
            .body
            .as_deref()
            .unwrap_or(if name == "null-original-body" {
                ""
            } else {
                "Original description π\n"
            });
        ensure(
            r["repository"] == q.repository
                && r["subject"] == q.subject
                && r["subject_id"] == q.subject_id
                && r["number"] == 7
                && r["account"]["external_id"] == "321"
                && r["edited_title"] == q.title.is_some()
                && r["edited_body"] == q.body.is_some()
                && r["title_sha256"] == sha(title)
                && r["body_sha256"] == sha(body)
                && r["provider_response"]["protocol"] == "github_graphql_discussion_edit"
                && r["provider_response"]["status"] == 200
                && serde_json::to_vec(&r)?.len() <= 4096
                && posts(&io)? == before + 1,
            "edit receipt lost exact partial writing/binding/bounds",
        )?;
        ensure(
            r.get("title").is_none() && r.get("body").is_none(),
            "receipt duplicated complete writing",
        )?;
        if name == "no-request-id" {
            ensure(
                r["provider_response"]["request_id"].is_null(),
                "invented edit request ID",
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
        ("stale-title", 409),
        ("stale-body", 409),
        ("missing-body", 502),
        ("malformed-body", 502),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let before = posts(&io)?;
        let WriteOutcome::NotSent(f) = client.edit_discussion(token()?, "321", &edit()).await
        else {
            return Err(format!("edit preflight did not refuse: {name}").into());
        };
        ensure(
            f.status == status && posts(&io)? == before,
            "edit preflight dispatched or lost refusal class",
        )?;
        cases.push(name);
    }
    mode("positive")?;
    for i in 0..13 {
        let mut q = edit();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.subject_id = "0700".into(),
            2 => q.subject = "discussion".into(),
            3 => q.number = 0,
            4 => q.number = u64::MAX,
            5 => {
                q.title = None;
                q.body = None;
            }
            6 => q.title = Some("x".repeat(256)),
            7 => q.title = Some(" \n ".into()),
            8 => q.body = Some("\0".into()),
            9 => q.body = Some("x".repeat(65537)),
            10 => q.original_title = "x".repeat(65537),
            11 => q.original_body_sha256 = "A".repeat(64),
            _ => q.original_body_sha256 = "not-a-digest".into(),
        }
        let before = fs::read_to_string(io.join("gets"))?;
        ensure(
            matches!(client.edit_discussion(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.status==400)
                && fs::read_to_string(io.join("gets"))? == before,
            "invalid edit descriptor reached provider",
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
        ("wrong-title", 502),
        ("wrong-body", 502),
        ("unselected-body-changed", 502),
        ("unselected-title-changed", 502),
        ("wrong-url", 502),
        ("missing-full-id", 502),
        ("partial-errors", 502),
        ("graphql-errors", 502),
        ("oversized", 413),
        ("oversized-request-id", 502),
        ("drop-reply", 503),
        ("deadline", 504),
    ] {
        mode(name)?;
        let before = posts(&io)?;
        let mut q = edit();
        if name == "unselected-body-changed" {
            q.body = None;
        }
        if name == "unselected-title-changed" {
            q.title = None;
        }
        let WriteOutcome::Indeterminate(f) = client.edit_discussion(token()?, "321", &q).await
        else {
            return Err(
                format!("edit post-dispatch error became success or absence: {name}").into(),
            );
        };
        ensure(
            f.status == status && posts(&io)? == before + 1,
            "uncertain edit retried or lost class",
        )?;
        cases.push(name);
    }
    mode("serialize")?;
    let a = edit();
    let b = edit();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.edit_discussion(token()?, "321", &a),
        clone.edit_discussion(token()?, "321", &b)
    );
    ensure(
        matches!(first, WriteOutcome::Applied(_))
            && matches!(second, WriteOutcome::Applied(_))
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "edit serialization/pacing failed",
    )?;
    mode("pause-post")?;
    let q = edit();
    let t = token()?;
    let pending = async { Ok::<_, &str>(client.edit_discussion(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    owner.stop()?;
    ensure(
        matches!(pending.await?, WriteOutcome::Indeterminate(_))
            && started.elapsed() < Duration::from_secs(2),
        "edit retirement lost uncertainty or blocked",
    )?;
    ensure(
        matches!(
            client.edit_discussion(token()?, "321", &edit()).await,
            WriteOutcome::NotSent(_)
        ),
        "retired edit owner accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "edit fixture received unexpected request",
    )?;
    fs::write(
        root.join("edit-producer-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_descriptors":13,"serialization":true,"in_flight_retirement":"indeterminate","actual_provider":false,"product_consumer_connected":false,"atomic_content_precondition":false}),
        )?,
    )?;
    println!("PASS: production Rust issue/PR edits preserve field presence and exact writing with immutable targeting, stale-base preflight, bounded causal receipt, no retry, deadlines, serialization and retirement; dummy-only HTTPS, no provider CAS or actual provider/product qualification");
    Ok(())
}
