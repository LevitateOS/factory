//! Real native provider/journal integration observers, never Rust unit tests.
//! Launch only with dummy grants and an owned no-forwarding local HTTPS fixture.
use super::{ensure, prepare, Result};
use serde_json::json;
use sha2::{Digest, Sha256};
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::github_provider::{CreateOutcome, IssueCreate, Owner};
use std::os::unix::fs::PermissionsExt;
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
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
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
