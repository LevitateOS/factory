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
