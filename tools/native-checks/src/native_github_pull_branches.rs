//! Existing native observer's declared, no-forwarding branch-pair checks.
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::{
    credentials::Token,
    github_provider::{Owner, PullBranchRead},
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
    let token = || Token::new(b"factory-declared-pull-branches".to_vec());
    let request = || PullBranchRead {
        repository: "123".into(),
        head_repository: "124".into(),
        base_branch: "main".into(),
        head_branch: "topic".into(),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "same-repository",
        "fork-pair",
        "cross-same-branch",
        "unrelated-network",
        "unknown-source",
        "unknown-metadata",
        "unicode-branch",
        "current-names",
        "big-repository",
        "sha256",
        "read-only",
    ] {
        mode(name)?;
        let mut q = request();
        if name == "same-repository" {
            q.head_repository = q.repository.clone();
        }
        if name == "unicode-branch" {
            q.head_branch = "topic/π/茶".into();
        }
        if name == "cross-same-branch" {
            q.head_branch = q.base_branch.clone();
        }
        if name == "big-repository" {
            q.head_repository = "5000000000".into();
        }
        let v = client
            .observe_pull_branches(token()?, "321", &q)
            .await
            .map_err(|f| format!("branch positive {name}: {} {}", f.status, f.code))?;
        let p = &v["branches"];
        ensure(
            v["kind"] == "pull_branches"
                && v["account"]["external_id"] == "321"
                && p["base"]["repository"]["external_id"] == q.repository
                && p["head"]["repository"]["external_id"] == q.head_repository
                && p["base"]["branch"]["name"] == q.base_branch
                && p["head"]["branch"]["name"] == q.head_branch,
            "branch pair lost immutable scope/names",
        )?;
        match name {
            "unrelated-network" => ensure(
                p["head"]["repository"]["source_repository"]["external_id"] == "999",
                "different network silently replaced",
            )?,
            "unknown-source" => ensure(
                p["head"]["repository"]["source_repository"].is_null()
                    && p["head"]["repository"]["fork"] == true,
                "unknown source became an inferred fork root",
            )?,
            "unknown-metadata" => ensure(
                p["head"]["repository"]["push"].is_null()
                    && p["head"]["repository"]["fork"].is_null()
                    && p["head"]["repository"]["disabled"].is_null(),
                "unknown facts became false",
            )?,
            "current-names" => ensure(
                p["head"]["repository"]["owner"]["login"] == "renamed"
                    && p["head"]["repository"]["name"] == "renamed/head",
                "current labels lost",
            )?,
            "sha256" => ensure(
                p["head"]["branch"]["commit"]
                    .as_str()
                    .is_some_and(|s| s.len() == 64),
                "commit format truncated",
            )?,
            "read-only" => ensure(
                p["base"]["repository"]["archived"] == true
                    && p["head"]["repository"]["disabled"] == true
                    && p["head"]["repository"]["push"] == false,
                "read-only facts invented permission",
            )?,
            _ => (),
        }
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&v)?)?;
        cases.push(name);
    }
    for (name, status) in [
        ("identity-change", 409),
        ("identity-denied", 401),
        ("base-missing", 404),
        ("head-missing", 404),
        ("base-slot-changed", 409),
        ("head-slot-changed", 409),
        ("missing-base-node", 502),
        ("missing-head-node", 502),
        ("missing-owner-node", 502),
        ("wrong-owner-type", 502),
        ("malformed-related", 502),
        ("invalid-metadata", 502),
        ("empty-owner-login", 502),
        ("graph-base-missing", 404),
        ("graph-head-missing", 404),
        ("wrong-node-type", 409),
        ("wrong-repository", 409),
        ("wrong-name", 409),
        ("wrong-owner", 409),
        ("wrong-owner-login", 409),
        ("missing-branch", 404),
        ("omitted-branch", 502),
        ("wrong-branch", 409),
        ("wrong-prefix", 409),
        ("wrong-ref-repository", 409),
        ("missing-ref-node", 502),
        ("no-target", 409),
        ("omitted-target", 502),
        ("non-commit", 502),
        ("bad-commit", 502),
        ("wrong-commit-repository", 502),
        ("changed-base-tip", 409),
        ("changed-head-tip", 409),
        ("changed-ref-node", 409),
        ("changed-repo-on-recheck", 409),
        ("changed-owner-on-recheck", 409),
        ("changed-source-on-recheck", 409),
        ("different-repository-same-node", 409),
        ("same-repository-node-conflict", 409),
        ("same-repository-metadata-conflict", 409),
        ("related-identity-conflict", 409),
        ("malformed", 502),
        ("partial-errors", 502),
        ("denied", 403),
        ("limited", 429),
        ("validation", 400),
        ("redirect", 409),
        ("oversized", 413),
        ("deadline", 504),
    ] {
        mode(name)?;
        let start = Instant::now();
        let mut q = request();
        if name.starts_with("same-repository") {
            q.head_repository = q.repository.clone();
        }
        let f = client
            .observe_pull_branches(token()?, "321", &q)
            .await
            .err()
            .ok_or_else(|| format!("branch refusal accepted {name}"))?;
        ensure(
            f.status == status,
            &format!("branch refusal {name}: {} {}", f.status, f.code),
        )?;
        if name == "validation" {
            ensure(
                f.code == "github_query_rejected",
                "branch query misclassified as mutation",
            )?;
        }
        ensure(
            start.elapsed() < Duration::from_secs(10),
            "branch operation exceeded whole deadline",
        )?;
        cases.push(name);
    }
    mode("fork-pair")?;
    for i in 0..12 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.head_repository = "0".into(),
            2 => q.base_branch.clear(),
            3 => q.head_branch = "../bad".into(),
            4 => q.base_branch = "main..bad".into(),
            5 => q.head_branch = ".hidden".into(),
            6 => q.head_branch = "topic.lock".into(),
            7 => q.head_branch = "x".repeat(502),
            8 => q.head_branch = "topic\nbad".into(),
            9 => {
                q.head_repository = q.repository.clone();
                q.head_branch = q.base_branch.clone();
            }
            10 => q.head_branch = "bad space".into(),
            _ => q.head_branch = "trailing.".into(),
        }
        let before = fs::read(io.join("gets"))?;
        let f = client
            .observe_pull_branches(token()?, "321", &q)
            .await
            .err()
            .ok_or("invalid branch selector accepted")?;
        ensure(
            f.status == 400 && fs::read(io.join("gets"))? == before,
            "invalid branch selector reached provider",
        )?;
    }
    mode("retirement")?;
    let q = request();
    let t = token()?;
    let pending = async { Ok::<_, &str>(client.observe_pull_branches(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    let start = Instant::now();
    owner.stop()?;
    ensure(
        pending.await?.is_err() && start.elapsed() < Duration::from_secs(2),
        "branch observation outlived owner",
    )?;
    ensure(
        client
            .observe_pull_branches(token()?, "321", &request())
            .await
            .is_err(),
        "retired branch owner accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "branch producer sent undeclared request",
    )?;
    fs::write(
        root.join("pull-branches-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":12,"owner_retirement":true,"real_github":false,"mutations":0,"atomic_tip_binding_qualified":false,"canonical_consumer_connected":false}),
        )?,
    )?;
    println!("native pull branch observations passed {} declared cases, 12 invalid descriptors and retirement; no mutations",cases.len());
    Ok(())
}

fn creation_request(name: &str) -> soda_desktop_runtime::github_provider::PullCreate {
    use soda_desktop_runtime::github_provider::{PullBranchPin, PullCreate};
    let mut q = PullCreate {
        repository: "123".into(),
        head_repository: "124".into(),
        base_branch: "main".into(),
        head_branch: "topic".into(),
        base_pin: PullBranchPin {
            repository_node_id: "R_123".into(),
            owner_node_id: "O_10".into(),
            reference_node_id: "REF_base".into(),
            commit: "a".repeat(40),
        },
        head_pin: PullBranchPin {
            repository_node_id: "R_124".into(),
            owner_node_id: "O_10".into(),
            reference_node_id: "REF_head".into(),
            commit: "b".repeat(40),
        },
        title: "Exact human PR π".into(),
        body: "Original writing 🍵\n[link](relative.md)\n".repeat(256),
        allow_maintainer_edit: false,
    };
    match name {
        "same-repository" | "maintainer-same-repository" => {
            q.head_repository = q.repository.clone();
            q.head_pin.repository_node_id = q.base_pin.repository_node_id.clone();
        }
        "cross-same-branch" => q.head_branch = q.base_branch.clone(),
        "maintainer-edit" => {
            q.allow_maintainer_edit = true;
            q.head_pin.owner_node_id = "U_321".into();
        }
        "maintainer-other-owner" => {
            q.allow_maintainer_edit = true;
            q.head_pin.owner_node_id = "U_322".into();
        }
        "unicode-branch" => q.head_branch = "topic/π/茶".into(),
        "empty-body" => q.body.clear(),
        "max-unicode" => {
            q.title = "t".repeat(255);
            q.body = "🍵".repeat(16384);
        }
        "max-control" => q.body = "\u{0001}".repeat(65536),
        "big-ids" => {
            q.head_repository = "5000000000".into();
            q.head_pin.repository_node_id = "R_5000000000".into();
        }
        "sha256" => {
            q.base_pin.commit = "a".repeat(64);
            q.head_pin.commit = "b".repeat(64);
        }
        "no-difference" => q.head_pin.commit = q.base_pin.commit.clone(),
        "receipt-oversized" => {
            q.base_pin.repository_node_id = "R".repeat(1024);
            q.head_pin.repository_node_id = "S".repeat(1024);
            q.base_pin.owner_node_id = "O".repeat(1024);
            q.head_pin.owner_node_id = q.base_pin.owner_node_id.clone();
            q.base_pin.reference_node_id = "B".repeat(1024);
            q.head_pin.reference_node_id = "H".repeat(1024);
        }
        _ => (),
    }
    if matches!(
        name,
        "maintainer-organization" | "maintainer-same-repository"
    ) {
        q.allow_maintainer_edit = true;
    }
    q
}

pub(super) async fn creation(profile: &Path) -> Result<()> {
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::CreateOutcome;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        for marker in ["entered", "release", "cancelled"] {
            let _ = fs::remove_file(io.join(marker));
        }
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let hash = |s: &str| {
        Sha256::digest(s.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>()
    };
    let token = || Token::new(b"factory-declared-pull-create".to_vec());
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let mut client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "same-repository",
        "fork-pair",
        "cross-same-branch",
        "maintainer-edit",
        "current-names",
        "unicode-branch",
        "empty-body",
        "max-unicode",
        "max-control",
        "big-ids",
        "sha256",
        "no-request-id",
    ] {
        mode(name)?;
        let q = creation_request(name);
        let before = writes()?;
        let CreateOutcome::Created(r) = client.create_pull(token()?, "321", &q).await else {
            return Err(format!("pull creation positive refused {name}").into());
        };
        ensure(
            r["account"]["external_id"] == "321"
                && r["repository"] == q.repository
                && r["head_repository"] == q.head_repository
                && r["base"]["pin"]["commit"] == q.base_pin.commit
                && r["head"]["pin"]["commit"] == q.head_pin.commit
                && r["pull"]["author"]["external_id"] == "321"
                && r["pull"]["title_sha256"] == hash(&q.title)
                && r["pull"]["body_sha256"] == hash(&q.body)
                && r["pull"]["allow_maintainer_edit"] == q.allow_maintainer_edit
                && r["pull"]["draft"] == false
                && r["branch_condition"] == "preflight_and_causal_reply"
                && r["provider_response"]["protocol"] == "github_graphql_create_pull"
                && r["provider_response"]["status"] == 200
                && r["pull"].get("body").is_none()
                && r["pull"].get("title").is_none()
                && serde_json::to_vec(&r)?.len() <= 4096
                && writes()? == before + 1,
            "pull causal receipt lost writing/scope or repeated mutation",
        )?;
        if name == "big-ids" {
            ensure(
                r["pull"]["external_id"] == "5000000000",
                "pull BigInt identity truncated",
            )?;
        }
        if name == "no-request-id" {
            ensure(
                r["provider_response"]["request_id"].is_null(),
                "missing request ID invented",
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
        ("base-slot-changed", 409),
        ("head-slot-changed", 409),
        ("changed-saved-base-tip", 409),
        ("changed-saved-head-tip", 409),
        ("changed-reference", 409),
        ("changed-owner", 409),
        ("changed-repository-node", 409),
        ("changed-after-read", 409),
        ("read-only-base", 403),
        ("read-only-head", 403),
        ("unknown-read-only", 403),
        ("head-no-write", 403),
        ("head-write-unknown", 403),
        ("unknown-network", 409),
        ("unknown-fork", 409),
        ("unrelated-network", 409),
        ("no-difference", 409),
        ("maintainer-other-owner", 403),
        ("maintainer-organization", 403),
        ("maintainer-same-repository", 403),
        ("owner-qualifier-mismatch", 409),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let q = creation_request(name);
        let before = writes()?;
        let CreateOutcome::NotSent(f) = client.create_pull(token()?, "321", &q).await else {
            return Err(format!("pull preflight dispatched {name}").into());
        };
        ensure(
            f.status == status && writes()? == before,
            &format!("pull preflight {name}: {} {}", f.status, f.code),
        )?;
        if name == "no-difference" {
            ensure(
                f.code == "github_pull_no_difference",
                "no difference became creation attribution",
            )?;
        }
        cases.push(name);
    }
    mode("fork-pair")?;
    for i in 0..18 {
        let mut q = creation_request("fork-pair");
        match i {
            0 => q.repository = "0123".into(),
            1 => q.head_repository = "0".into(),
            2 => q.base_branch.clear(),
            3 => q.head_branch = "../bad".into(),
            4 => q.title.clear(),
            5 => q.title = " t".into(),
            6 => q.title = "x".repeat(256),
            7 => q.body = "x".repeat(65537),
            8 => q.body = "\0".into(),
            9 => q.base_pin.repository_node_id.clear(),
            10 => q.head_pin.owner_node_id = "bad\n".into(),
            11 => q.head_pin.reference_node_id = q.base_pin.reference_node_id.clone(),
            12 => q.base_pin.commit = "bad".into(),
            13 => q.head_pin.repository_node_id = q.base_pin.repository_node_id.clone(),
            14 => {
                q.head_repository = q.repository.clone();
                q.head_pin.repository_node_id = q.base_pin.repository_node_id.clone();
                q.head_branch = q.base_branch.clone();
            }
            15 => q.head_branch = "x".repeat(502),
            16 => q.title = "\0".into(),
            _ => q.head_branch = "refs/heads/topic".into(),
        };
        let before = fs::read(io.join("gets"))?;
        let CreateOutcome::NotSent(f) = client.create_pull(token()?, "321", &q).await else {
            return Err("invalid pull descriptor dispatched".into());
        };
        ensure(
            f.status == if i == 17 { 409 } else { 400 } && fs::read(io.join("gets"))? == before,
            "invalid pull descriptor reached provider",
        )?;
    }
    for (name, status) in [
        ("denied", 403),
        ("limited", 429),
        ("validation", 400),
        ("server-error", 503),
        ("redirect", 409),
        ("malformed", 502),
        ("partial-errors", 502),
        ("oversized", 413),
        ("wrong-pull-node", 502),
        ("wrong-pull-id", 502),
        ("wrong-number", 502),
        ("wrong-repository", 502),
        ("wrong-base-repository", 502),
        ("wrong-head-repository", 502),
        ("wrong-base-owner", 502),
        ("wrong-head-owner", 502),
        ("wrong-base-tip", 502),
        ("wrong-head-tip", 502),
        ("wrong-base-reference", 502),
        ("wrong-head-reference", 502),
        ("wrong-base-name", 502),
        ("wrong-head-name", 502),
        ("wrong-prefix", 502),
        ("wrong-reference-repository", 502),
        ("missing-reference", 502),
        ("wrong-author", 502),
        ("missing-author", 502),
        ("bot-author", 502),
        ("wrong-title", 502),
        ("wrong-body", 502),
        ("wrong-state", 502),
        ("wrong-draft", 502),
        ("wrong-maintainer-choice", 502),
        ("wrong-url", 502),
        ("wrong-date", 502),
        ("oversized-request-id", 502),
        ("duplicate-request-id", 502),
        ("receipt-oversized", 413),
        ("drop-reply", 503),
        ("deadline", 504),
    ] {
        mode(name)?;
        let q = creation_request(name);
        let before = writes()?;
        let start = Instant::now();
        let CreateOutcome::Indeterminate(f) = client.create_pull(token()?, "321", &q).await else {
            return Err(format!("pull uncertainty became success/absence {name}").into());
        };
        ensure(
            f.status == status
                && writes()? == before + 1
                && start.elapsed() < Duration::from_secs(10),
            &format!("pull uncertainty {name}: {} {}", f.status, f.code),
        )?;
        cases.push(name);
        if name == "drop-reply" {
            let observed = client
                .observe_pull_branches(
                    token()?,
                    "321",
                    &PullBranchRead {
                        repository: q.repository.clone(),
                        head_repository: q.head_repository.clone(),
                        base_branch: q.base_branch.clone(),
                        head_branch: q.head_branch.clone(),
                    },
                )
                .await
                .map_err(|f| f.code)?;
            ensure(
                observed["kind"] == "pull_branches"
                    && observed.get("receipt").is_none()
                    && writes()? == before + 1,
                "matching branches attributed missing creation reply",
            )?;
        }
    }
    mode("serialize")?;
    let mut a = creation_request("serialize");
    a.title = "first creation".into();
    let mut b = a.clone();
    b.title = "second creation".into();
    let t = token()?;
    let clone = client.clone();
    let first_client = client.clone();
    let first = async move { Ok::<_, &str>(first_client.create_pull(t, "321", &a).await) };
    tokio::pin!(first);
    super::native_github_transport::entered(first.as_mut(), &io).await?;
    let before = writes()?;
    let t = token()?;
    let second = async { clone.create_pull(t, "321", &b).await };
    tokio::pin!(second);
    ensure(
        tokio::time::timeout(Duration::from_millis(1200), second.as_mut())
            .await
            .is_err()
            && writes()? == before,
        "clone bypassed global mutation serialization",
    )?;
    fs::write(io.join("release"), b"")?;
    let (a, b) = tokio::join!(first, second);
    ensure(
        matches!(a?, CreateOutcome::Created(_))
            && matches!(b, CreateOutcome::Created(_))
            && writes()? == before + 1
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "pull clone serialization/pacing failed",
    )?;
    for (name, attempted) in [("pause-preflight", false), ("pause-write", true)] {
        mode(name)?;
        let q = creation_request(name);
        let t = token()?;
        let flight = client.clone();
        let pending = async move { Ok::<_, &str>(flight.create_pull(t, "321", &q).await) };
        tokio::pin!(pending);
        super::native_github_transport::entered(pending.as_mut(), &io).await?;
        let start = Instant::now();
        owner.stop()?;
        let outcome = pending.await?;
        ensure(
            (if attempted {
                matches!(outcome, CreateOutcome::Indeterminate(_))
            } else {
                matches!(outcome, CreateOutcome::NotSent(_))
            }) && start.elapsed() < Duration::from_secs(2),
            "pull retirement lost send boundary",
        )?;
        if !attempted {
            owner = tokio::task::spawn_blocking(Owner::start).await??;
            client = owner.client();
        }
    }
    let retired_gets = fs::read(io.join("gets"))?;
    let retired_writes = writes()?;
    ensure(
        matches!(
            client
                .create_pull(token()?, "321", &creation_request("fork-pair"))
                .await,
            CreateOutcome::NotSent(_)
        ) && fs::read(io.join("gets"))? == retired_gets
            && writes()? == retired_writes
            && !io.join("unexpected-request").exists(),
        "retired pull owner accepted work or sent undeclared request",
    )?;
    fs::write(
        root.join("pull-create-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":18,"clone_serialization":true,"mutation_pacing":true,"pre_post_send_retirement":true,"real_github":false,"atomic_tip_binding_qualified":false,"product_consumer_connected":false,"maintainer_live_delegation_qualified":false}),
        )?,
    )?;
    println!("native pull creation passed {} declared cases, 18 invalid descriptors, serialization/pacing and retirement",cases.len());
    Ok(())
}
