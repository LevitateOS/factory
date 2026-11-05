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

pub(super) async fn branch_creation(profile: &Path) -> Result<()> {
    use soda_desktop_runtime::github_provider::{BranchCreate, CreateOutcome, Read};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        for marker in ["entered", "release", "cancelled"] {
            match fs::remove_file(io.join(marker)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.into()),
            }
        }
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let calls = || -> Result<Vec<u8>> { Ok(fs::read(io.join("calls")).unwrap_or_default()) };
    let token = || Token::new(b"factory-declared-branch-create".to_vec());
    let request = || BranchCreate {
        repository: "123".into(),
        repository_node_id: "R_123".into(),
        name: "topic/new".into(),
        commit: "b".repeat(40),
    };
    let read = |q: &BranchCreate| Read::BranchCreation {
        repository: q.repository.clone(),
        name: q.name.clone(),
        commit: q.commit.clone(),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "positive",
        "unicode",
        "maximum-name",
        "literal-refs",
        "sha256",
        "big-repository",
        "current-name",
    ] {
        mode(name)?;
        let mut q = request();
        match name {
            "unicode" => q.name = "topic/π/茶".into(),
            "maximum-name" => q.name = "n".repeat(501),
            "literal-refs" => q.name = "refs/tags/literal-branch".into(),
            "sha256" => q.commit = "a".repeat(64),
            "big-repository" => q.repository = "5000000000".into(),
            _ => (),
        }
        let p = client
            .observe(token()?, "321", &read(&q))
            .await
            .map_err(|f| format!("branch prepare {name}: {} {}", f.status, f.code))?;
        ensure(
            p["preparation"]["repository_node_id"] == q.repository_node_id
                && p["preparation"]["requested_branch"] == q.name
                && p["preparation"]["requested_commit"] == q.commit
                && p["preparation"]["branch"].is_null(),
            "branch preparation lost exact selector or invented an existing branch",
        )?;
        let before = writes()?;
        let CreateOutcome::Created(r) = client.create_branch(token()?, "321", &q).await else {
            return Err(format!("branch creation {name} refused").into());
        };
        ensure(
            r["repository"] == q.repository
                && r["repository_node_id"] == q.repository_node_id
                && r["account"]["external_id"] == "321"
                && r["branch"]["name"] == q.name
                && r["branch"]["node_id"] == "REF_new"
                && r["branch"]["commit"] == q.commit
                && r["creation_condition"] == "new_ref_at_exact_commit"
                && r["provider_response"]["protocol"] == "github_graphql_create_ref"
                && serde_json::to_vec(&r)?.len() <= 4096
                && writes()? == before + 1,
            "branch causal receipt lost exact identity/commit",
        )?;
        let p = client
            .observe(token()?, "321", &read(&q))
            .await
            .map_err(|f| format!("branch readback {name}: {} {}", f.status, f.code))?;
        ensure(
            p["preparation"]["branch"] == r["branch"],
            "created branch is not normally observable",
        )?;
        match client.create_branch(token()?, "321", &q).await {
            CreateOutcome::NotSent(f) if f.status == 409 => (),
            _ => return Err("existing branch was adopted or rewritten".into()),
        }
        ensure(writes()? == before + 1, "existing branch repeated mutation")?;
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&r)?)?;
        cases.push(name.to_owned());
    }
    for (name, status) in [
        ("identity-change", 409),
        ("identity-denied", 401),
        ("repository-hidden", 404),
        ("repository-slot-changed", 409),
        ("pin-changed", 409),
        ("query-hidden", 404),
        ("query-wrong-type", 409),
        ("query-wrong-repository", 409),
        ("query-wrong-name", 409),
        ("commit-hidden", 404),
        ("commit-omitted", 502),
        ("commit-wrong-type", 502),
        ("commit-wrong-oid", 502),
        ("commit-wrong-repository", 502),
        ("branch-omitted", 502),
        ("branch-exists", 409),
        ("changed-branch", 409),
        ("read-only", 403),
        ("unknown-push", 403),
        ("changed-repository", 409),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if name == "pin-changed" {
            q.repository_node_id = "R_other".into()
        };
        let before = writes()?;
        match client.create_branch(token()?, "321", &q).await {
            CreateOutcome::NotSent(f) if f.status == status => (),
            CreateOutcome::NotSent(f) => {
                return Err(format!(
                    "branch {name} expected {status}, got {} {}",
                    f.status, f.code
                )
                .into())
            }
            _ => {
                return Err(
                    format!("branch preflight {name} reached or attributed a mutation").into(),
                )
            }
        }
        ensure(
            writes()? == before,
            "branch preflight rejection sent a write",
        )?;
        cases.push(name.into());
    }
    let mut invalid = Vec::new();
    for branch in [
        "",
        "/topic",
        "topic/",
        "bad..branch",
        "bad@{branch",
        "bad branch",
        "bad:branch",
        ".hidden",
        "part/.hidden",
        "topic.lock",
        "topic.",
        "bad\nbranch",
    ] {
        let mut q = request();
        q.name = branch.into();
        invalid.push(q)
    }
    for i in 0..4 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.repository_node_id = "".into(),
            2 => q.commit = "b".repeat(39),
            _ => q.name = "n".repeat(502),
        };
        invalid.push(q)
    }
    for q in &invalid {
        let before = calls()?;
        match client.create_branch(token()?, "321", q).await {
            CreateOutcome::NotSent(f) if f.status == 400 => (),
            _ => return Err("invalid branch descriptor performed I/O".into()),
        };
        ensure(calls()? == before, "invalid branch effect performed I/O")?;
        // The repository Node pin belongs only to the write descriptor.
        if !q.repository_node_id.is_empty() {
            ensure(
                client.observe(token()?, "321", &read(q)).await.is_err() && calls()? == before,
                "invalid branch selector accepted or performed I/O",
            )?;
        }
    }
    for name in [
        "wrong-node",
        "wrong-name",
        "wrong-prefix",
        "wrong-repository",
        "null-target",
        "wrong-target-type",
        "wrong-target-oid",
        "wrong-target-repository",
        "null-ref",
        "malformed",
        "partial-errors",
        "denied",
        "drop-reply",
        "deadline",
        "duplicate-request-id",
    ] {
        mode(name)?;
        let q = request();
        let before = writes()?;
        match client.create_branch(token()?, "321", &q).await {
            CreateOutcome::Indeterminate(_) => (),
            _ => {
                return Err(
                    format!("branch post-send {name} was attributed or called no-effect").into(),
                )
            }
        }
        ensure(writes()? == before + 1, "post-send branch failure retried")?;
        if name == "drop-reply" {
            let p = client
                .observe(token()?, "321", &read(&q))
                .await
                .map_err(|_| "lost branch readback failed")?;
            ensure(
                p["preparation"]["branch"]["node_id"] == "REF_new",
                "lost branch not normally readable",
            )?;
        }
        cases.push(name.into());
    }
    mode("serialize")?;
    let q = request();
    let before = writes()?;
    let first_token = token()?;
    let first = async {
        Ok::<_, std::convert::Infallible>(client.create_branch(first_token, "321", &q).await)
    };
    tokio::pin!(first);
    super::native_github_transport::entered(first.as_mut(), &io).await?;
    let clone = client.clone();
    let second = clone.create_branch(token()?, "321", &q);
    tokio::pin!(second);
    tokio::select! {_=&mut second=>return Err("branch clone escaped mutation serialization".into()),_=tokio::time::sleep(Duration::from_millis(1100))=>()};
    fs::write(io.join("release"), b"")?;
    ensure(
        matches!(first.await?, CreateOutcome::Created(_))
            && matches!(second.await, CreateOutcome::NotSent(_))
            && writes()? == before + 1,
        "serialized branch clone repeated/adopted created ref",
    )?;
    cases.push("serialized-clones".into());
    mode("retire")?;
    let q = request();
    let pending_token = token()?;
    let pending = async {
        Ok::<_, std::convert::Infallible>(client.create_branch(pending_token, "321", &q).await)
    };
    tokio::pin!(pending);
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    owner.stop()?;
    ensure(
        matches!(pending.await?, CreateOutcome::Indeterminate(_))
            && started.elapsed() < Duration::from_secs(2),
        "retired branch effect delivered stale reply or failed to join",
    )?;
    let before = calls()?;
    ensure(
        matches!(
            client.create_branch(token()?, "321", &q).await,
            CreateOutcome::NotSent(_)
        ) && calls()? == before,
        "stopped branch owner made provider I/O",
    )?;
    ensure(
        !io.join("unexpected-request").exists()
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "unexpected/unserialized/unpaced branch request",
    )?;
    fs::write(
        root.join("branch-create-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_descriptors":invalid.len(),"provider_mutations":writes()?,"retirement":true,"actual_provider":false,"product_consumer_connected":false,"force_update_or_delete":false}),
        )?,
    )?;
    println!("PASS: bounded native branch preparation/create/readback, exact commit/ref identity, preflight refusal, causal uncertainty, serialized clones and retirement; dummy-only provider");
    Ok(())
}

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

pub(super) async fn product(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    use super::native_github_issues::{call, set_mode};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
        ProductRequest,
    };
    use std::{os::unix::fs::PermissionsExt, sync::Arc};
    let completed = |r: &(u16, serde_json::Value), effect: &str| -> Result<()> {
        ensure(
            r.0 == 200 && r.1["effect"] == effect && r.1["finished"] == true,
            &format!(
                "composed pull primary: expected {effect}, received {} {}",
                r.0, r.1
            ),
        )
    };
    for (method, target, limit) in [
        (
            "POST",
            "/api/factory/collaboration/repositories/1/pulls",
            512 * 1024,
        ),
        (
            "GET",
            "/api/factory/collaboration/repositories/1/pulls",
            65536,
        ),
        (
            "POST",
            "/api/factory/collaboration/repositories/01/pulls",
            65536,
        ),
        (
            "POST",
            "/api/factory/collaboration/repositories/0/pulls",
            65536,
        ),
        (
            "POST",
            "/api/factory/collaboration/repositories/1/pulls?x=1",
            65536,
        ),
        (
            "POST",
            "/api/factory/collaboration/repositories/1/pull-creation/commands/abc/cancel",
            65536,
        ),
        ("POST", "/api/native/mcp-sign", 65536),
    ] {
        ensure(
            soda_desktop_runtime::product_request_body_limit(method, target) == limit,
            "pull route widened an unrelated native envelope",
        )?;
    }
    let posts = |io: &Path| -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let uncertain = |r: &(u16, serde_json::Value)| -> Result<()> {
        ensure(
            matches!(r.0, 200 | 202)
                && r.1["effect"] == "indeterminate"
                && r.1["finished"] == false
                && r.1["pull"].is_null(),
            "unknown pull became complete or attributable",
        )
    };
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
        "pull product requires empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let saved = store
        .store(
            account()?,
            Token::new(b"factory-declared-pull-create".to_vec())?,
        )
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
        "pull product canonical catalogue failed",
    )?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let base = format!("/api/factory/collaboration/repositories/{repository}");
    let first = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{base}/pulls/7"),
        &[],
    )
    .await?;
    ensure(first.0 == 200, "authenticated head reference read failed")?;
    let head = first.1["head"]["repository"]["id"]
        .as_str()
        .ok_or("canonical head repository missing")?;
    let submit = format!("{base}/pulls");
    let envelope = |path: &str, id: &str, title: &str, body: &str| -> Result<Vec<u8>> {
        let q: ProductRequest = serde_json::from_value(
            json!({"kind":"write","path":path,"body":{"command_id":id,"head_repository":head,"base_branch":"main","head_branch":"topic","title":title,"body":body,"allow_maintainer_edit":false}}),
        )?;
        Ok(q.request()?.2)
    };
    let command = |id: &str| format!("{base}/pull-creation/commands/{id}");
    let mut cases = Vec::new();
    for (i, (title, body)) in [
        ("Original human pull π".to_owned(), "".to_owned()),
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
            result.1["pull"]["repository"] == repository && result.1["pull"]["kind"] == "pull",
            "creation lost its currently readable canonical destination",
        )?;
        let number = result.1["pull"]["number"]
            .as_str()
            .ok_or("created pull number absent")?
            .parse::<i64>()?;
        let external = result.1["pull"]["external_id"].clone();
        let read = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{base}/pulls/{number}"),
            &[],
        )
        .await?;
        ensure(
            read.0 == 200
                && read.1["ref"]["external_id"] == external
                && read.1["title"] == title
                && read.1["body"] == body,
            "created normal pull readback changed original title/body",
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
            "replay/lookup/cancel/title conflict duplicated pull creation",
        )?;
        cases.push(format!("positive-{i}"));
    }
    for (i, mode) in [
        "same-repository",
        "cross-same-branch",
        "unicode-branch",
        "maintainer-edit",
    ]
    .into_iter()
    .enumerate()
    {
        set_mode(&io, mode)?;
        let mut v: serde_json::Value = serde_json::from_slice(&envelope(
            &submit,
            &format!("{:032x}", 200 + i),
            "Selected branch pair",
            "Exact original writing",
        )?)?;
        if mode == "same-repository" {
            v["head_repository"] = json!(repository)
        }
        if mode == "cross-same-branch" {
            v["head_branch"] = json!("main")
        }
        if mode == "unicode-branch" {
            v["head_branch"] = json!("topic/π/茶")
        }
        if mode == "maintainer-edit" {
            v["allow_maintainer_edit"] = json!(true)
        }
        let body = serde_json::to_vec(&v)?;
        let before = posts(&io)?;
        let result = call(&runtime, &store, &connection, "POST", &submit, &body).await?;
        completed(&result, "committed")?;
        ensure(
            !result.1["pull"].is_null() && posts(&io)? == before + 1,
            "selected branch/delegation lost destination or duplicated",
        )?;
        for (field, value) in [
            ("head_repository", json!(repository)),
            ("base_branch", json!("different")),
            ("head_branch", json!("different")),
            ("body", json!("different")),
            (
                "allow_maintainer_edit",
                json!(!v["allow_maintainer_edit"].as_bool().unwrap()),
            ),
        ] {
            if v[field] == value {
                continue;
            }
            let mut conflict = v.clone();
            conflict[field] = value;
            let r = call(
                &runtime,
                &store,
                &connection,
                "POST",
                &submit,
                &serde_json::to_vec(&conflict)?,
            )
            .await?;
            let expected = if mode == "cross-same-branch" && field == "head_repository" {
                400
            } else {
                409
            };
            ensure(
                r.0 == expected && posts(&io)? == before + 1,
                &format!(
                    "saved pull input {mode}/{field}: expected {expected}, received {} {}",
                    r.0, r.1
                ),
            )?;
        }
        cases.push(mode.into());
    }
    for (i, mode) in [
        "no-difference",
        "read-only-head",
        "head-no-write",
        "unknown-network",
        "unrelated-network",
    ]
    .into_iter()
    .enumerate()
    {
        set_mode(&io, mode)?;
        let bytes = envelope(
            &submit,
            &format!("{:032x}", 300 + i),
            "Refused before send",
            "",
        )?;
        let before = posts(&io)?;
        let r = call(&runtime, &store, &connection, "POST", &submit, &bytes).await?;
        completed(&r, "not_committed")?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "not_committed",
        )?;
        ensure(
            r.1["pull"].is_null() && posts(&io)? == before,
            "preflight refusal attempted creation or projected destination",
        )?;
        cases.push(mode.into());
    }
    set_mode(&io, "positive")?;
    let before = posts(&io)?;
    let concurrent_id = "33333333333333333333333333333333";
    let bytes = envelope(
        &submit,
        concurrent_id,
        "Concurrent exact intent",
        "One pull",
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
        "simultaneous pull submissions repeated the POST",
    )?;
    cases.push("concurrent-identical-command".into());
    let other = repository.parse::<i64>()? + 1;
    let other_path = format!("/api/factory/collaboration/repositories/{other}/pulls");
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
        "pull request expanded native repository scope",
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
    ensure(r.1["pull"].is_null(), "hidden created pull became readable")?;
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
        r.1["pull"].is_null(),
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
        !r.1["pull"].is_null() && posts(&io)? == before + 1,
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
        "lost-reply fixture did not really create a readable pull",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "matching pull search/replay manufactured creation attribution",
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
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    store.withdraw(&connection).await?;
    connection = store
        .store(
            account()?,
            Token::new(b"factory-declared-pull-create".to_vec())?,
        )
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
        "reconsent replayed an unknown pull",
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
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        pending.await.is_err(),
        "stopped runtime delivered an in-flight pull",
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
        "retained restart repeated pull creation",
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
    ensure(hidden.0 == 404, "other account read the captured pull")?;
    let conflict = call(&runtime, &store, &other.connection, "POST", &submit, &lost).await?;
    ensure(
        conflict.0 == 409 && posts(&io)? == before + 1,
        "other account adopted or dispatched a captured pull",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "pull product fixture saw an unexpected request",
    )?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "pull product left owned grants",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("pull-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_provider":false,"native_product_consumer":true,"capabilities_enabled":false,"head_seeded":false,"head_from_authenticated_normal_pull":true,"provider_posts":posts(&io)?,"retained_restart":true,"maximum_writing_bytes":65536}),
        )?,
    )?;
    println!("PASS: composed native/Rust/Go/PG pull creation retains exact writing, causal identity, readable destination, one-shot replay, ambiguous replies, cancel races, withdrawal, other-account refusal and restart; dummy-only provider, no actual GitHub or installed gate qualification");
    Ok(())
}
