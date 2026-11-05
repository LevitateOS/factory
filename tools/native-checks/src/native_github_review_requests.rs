//! Existing native executable's production reader/effect acceptance. Use only an
//! isolated, no-forwarding declared peer and its fixed dummy credential.
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::{
    credentials::Token,
    github_provider::{Owner, ReviewerRead, ReviewerTarget},
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
    let token = || Token::new(b"factory-declared-reviewers".to_vec());
    let request = || ReviewerRead {
        repository: "123".into(),
        number: 7,
        target: Some(ReviewerTarget::User {
            external_id: "456".into(),
        }),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "user",
        "team",
        "no-target",
        "user-not-requested",
        "team-not-requested",
        "hidden-absent",
        "hidden-present",
        "empty",
        "user-id-unknown",
        "team-id-unknown",
        "big-id",
        "user-rename",
        "team-rename",
        "other-kind",
    ] {
        mode(name)?;
        let mut q = request();
        if name.starts_with("team") {
            q.target = Some(ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            });
        }
        if name == "big-id" {
            q.target = Some(ReviewerTarget::User {
                external_id: "5000000000".into(),
            });
        }
        if name == "no-target" {
            q.target = None;
        }
        let value = client
            .observe_review_requests(token()?, "321", &q)
            .await
            .map_err(|f| format!("reviewer positive {name}: {} {}", f.status, f.code))?;
        let snapshot = &value["review_requests"];
        let items = snapshot["items"].as_array().ok_or("request items absent")?;
        ensure(
            value["account"]["external_id"] == "321"
                && snapshot["repository"] == "123"
                && snapshot["pull"]["external_id"]
                    == if name == "big-id" {
                        "5000000000"
                    } else {
                        "700"
                    },
            "reviewer snapshot lost immutable scope",
        )?;
        if name == "no-target" {
            ensure(
                snapshot["selected_target"].is_null() && snapshot["requested"].is_null(),
                "targetless read invented membership",
            )?;
        } else if name == "hidden-absent" {
            ensure(
                snapshot["requested"].is_null() && snapshot["all_targets_visible"] == false,
                "hidden target became absent",
            )?;
        } else if matches!(name, "user-not-requested" | "team-not-requested" | "empty") {
            ensure(
                snapshot["requested"] == false,
                "namespace collision or empty catalogue invented membership",
            )?;
        } else {
            ensure(
                snapshot["requested"] == true,
                "selected target presence lost",
            )?;
        }
        if name == "hidden-present" {
            ensure(
                snapshot["all_targets_visible"] == false
                    && items.iter().any(|i| i["reviewer"].is_null()),
                "hidden request discarded or made known",
            )?;
        }
        if name == "empty" {
            ensure(items.is_empty(), "empty request list invented rows")?;
        }
        if name == "user" || name == "team" {
            let user = items
                .iter()
                .find(|i| i["reviewer"]["kind"] == "user")
                .ok_or("user missing")?;
            let team = items
                .iter()
                .find(|i| i["reviewer"]["kind"] == "team")
                .ok_or("team missing")?;
            ensure(
                user["reviewer"]["external_id"] == "456"
                    && team["reviewer"]["external_id"] == "T_declared456"
                    && team["reviewer"]["database_id"] == "456"
                    && user["as_code_owner"] == true,
                "user/team identities or code-owner fact merged",
            )?;
        }
        if name == "user-id-unknown" || name == "big-id" {
            ensure(
                items[0]["reviewer"]["external_id"].is_null(),
                "unknown numeric identity invented",
            )?;
        }
        if name == "team-id-unknown" {
            ensure(
                snapshot["selected_target"]["database_id"].is_null()
                    && snapshot["selected_target"]["external_id"] == "T_declared456",
                "team identity depended on nullable database id",
            )?;
        }
        if name == "user-rename" {
            ensure(
                snapshot["selected_target"]["login"] == "renamed-user",
                "current user label lost",
            )?;
        }
        if name == "team-rename" {
            ensure(
                snapshot["selected_target"]["slug"] == "renamed-team",
                "current team label lost",
            )?;
        }
        if name == "other-kind" {
            ensure(
                items.iter().any(|i| {
                    i["reviewer"]["kind"] == "other" && i["reviewer"]["type"] == "Mannequin"
                }),
                "non-user/team request silently dropped",
            )?;
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
        ("repository-change", 409),
        ("missing-owner-node", 502),
        ("wrong-parent", 409),
        ("wrong-repository", 409),
        ("missing-pull", 404),
        ("duplicate-request", 502),
        ("duplicate-target", 502),
        ("duplicate-user-id", 502),
        ("wrong-request-pull", 502),
        ("unknown-code-owner", 502),
        ("missing-reviewer", 502),
        ("missing-database-id", 502),
        ("foreign-team", 409),
        ("count-change", 409),
        ("count-mismatch", 409),
        ("cursor-loop", 502),
        ("empty-continuation", 502),
        ("missing-cursor", 502),
        ("too-many", 413),
        ("partial-errors", 502),
        ("malformed", 502),
        ("oversized", 413),
        ("denied", 403),
        ("validation", 400),
        ("redirect", 409),
        ("user-gone", 404),
        ("user-changed", 409),
        ("user-kind", 409),
        ("user-node", 502),
        ("user-login", 502),
        ("team-gone", 404),
        ("team-kind", 409),
        ("team-changed", 409),
        ("team-organization", 409),
        ("team-slug", 502),
        ("target-class-conflict", 409),
        ("target-id-conflict", 409),
        ("changed-pull-recheck", 409),
        ("changed-owner-recheck", 409),
        ("deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if name.starts_with("team-") {
            q.target = Some(ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            });
        }
        let start = Instant::now();
        let f = client
            .observe_review_requests(token()?, "321", &q)
            .await
            .err()
            .ok_or_else(|| format!("reviewer refusal accepted {name}"))?;
        ensure(
            f.status == status,
            &format!("reviewer refusal {name}: {} {}", f.status, f.code),
        )?;
        ensure(
            start.elapsed() < Duration::from_secs(10),
            "reviewer read deadline unbounded",
        )?;
        cases.push(name);
    }
    mode("user")?;
    for i in 0..8 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.number = 0,
            2 => q.number = u64::MAX,
            3 => {
                q.target = Some(ReviewerTarget::User {
                    external_id: "0456".into(),
                })
            }
            4 => {
                q.target = Some(ReviewerTarget::Team {
                    external_id: "456".into(),
                })
            }
            5 => {
                q.target = Some(ReviewerTarget::Team {
                    external_id: "T".repeat(513),
                })
            }
            6 => {
                q.target = Some(ReviewerTarget::Team {
                    external_id: "T_\n".into(),
                })
            }
            _ => {
                q.target = Some(ReviewerTarget::User {
                    external_id: "-1".into(),
                })
            }
        }
        let before = fs::read(io.join("gets"))?;
        ensure(
            client
                .observe_review_requests(token()?, "321", &q)
                .await
                .err()
                .is_some_and(|f| f.status == 400)
                && fs::read(io.join("gets"))? == before,
            "invalid target reached provider",
        )?;
    }
    mode("retirement")?;
    let q = request();
    let t = token()?;
    let pending = async { Ok::<_, &str>(client.observe_review_requests(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    let start = Instant::now();
    owner.stop()?;
    ensure(
        pending.await?.is_err() && start.elapsed() < Duration::from_secs(2),
        "reviewer reader outlived owner",
    )?;
    ensure(
        client
            .observe_review_requests(token()?, "321", &request())
            .await
            .is_err(),
        "retired reviewer reader accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "reviewer reader sent undeclared request",
    )?;
    fs::write(
        root.join("reviewer-read-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":8,"owner_retirement":true,"real_github":false,"mutations":0,"canonical_consumer_connected":false}),
        )?,
    )?;
    println!("native reviewer observation passed {} declared cases, 8 invalid descriptors and owner retirement; no mutations",cases.len());
    Ok(())
}

pub(super) async fn effects(profile: &Path) -> Result<()> {
    use soda_desktop_runtime::github_provider::{ReviewRequestChange, WriteOutcome};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let token = || Token::new(b"factory-declared-reviewer-write".to_vec());
    let request = || ReviewRequestChange {
        repository: "123".into(),
        pull_id: "700".into(),
        number: 7,
        target: ReviewerTarget::User {
            external_id: "456".into(),
        },
        requested: true,
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let mut client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "add-user",
        "remove-user",
        "add-team",
        "remove-team",
        "big-id",
        "no-request-id",
        "current-user-name",
        "current-team-slug",
    ] {
        mode(name)?;
        let mut q = request();
        q.requested = !name.starts_with("remove") && !name.starts_with("current-");
        if name.contains("team") {
            q.target = ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            };
        }
        if name == "big-id" {
            q.pull_id = "5000000000".into();
            q.target = ReviewerTarget::User {
                external_id: "5000000000".into(),
            };
        }
        let before = writes()?;
        let WriteOutcome::Applied(r) = client.set_review_request(token()?, "321", &q).await else {
            return Err(format!("reviewer positive refused {name}").into());
        };
        ensure(
            r["account"]["external_id"] == "321"
                && r["repository"] == "123"
                && r["pull_id"] == q.pull_id
                && r["number"] == 7
                && r["requested"] == q.requested
                && r["unrelated_requests_preserved"] == 2
                && r["provider_response"]["status"] == 200
                && r["before_sha256"].as_str().is_some_and(|s| s.len() == 64)
                && r["after_sha256"].as_str().is_some_and(|s| s.len() == 64)
                && serde_json::to_vec(&r)?.len() <= 4096
                && writes()? == before + 1,
            "reviewer causal receipt lost binding/bounds or repeated effect",
        )?;
        if q.requested {
            ensure(
                r["actor"]["external_id"] == "321"
                    && r["target_selector"] == "node_id"
                    && r["provider_response"]["method"] == "POST",
                "addition lost union/actor/node semantics",
            )?;
        } else {
            ensure(
                r["actor"].is_null()
                    && r["target_selector"] == "name_preflight_and_recheck"
                    && r["provider_response"]["method"] == "DELETE",
                "removal invented actor or immutable selector proof",
            )?;
        }
        if name == "no-request-id" {
            ensure(
                r["provider_response"]["request_id"].is_null(),
                "request id invented",
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
        ("target-changed", 409),
        ("team-foreign", 409),
        ("hidden-absence", 409),
        ("already-requested", 409),
        ("already-absent", 409),
        ("maximum-before", 413),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if name == "team-foreign" {
            q.target = ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            };
        }
        if name == "already-absent" {
            q.requested = false;
        }
        let before = writes()?;
        let WriteOutcome::NotSent(f) = client.set_review_request(token()?, "321", &q).await else {
            return Err(format!("reviewer preflight dispatched {name}").into());
        };
        ensure(
            f.status == status && writes()? == before,
            &format!("reviewer preflight {name}: {} {}", f.status, f.code),
        )?;
        cases.push(name);
    }
    mode("add-user")?;
    for i in 0..8 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.pull_id = "0700".into(),
            2 => q.number = 0,
            3 => q.number = u64::MAX,
            4 => {
                q.target = ReviewerTarget::User {
                    external_id: "0456".into(),
                }
            }
            5 => {
                q.target = ReviewerTarget::Team {
                    external_id: "456".into(),
                }
            }
            6 => {
                q.target = ReviewerTarget::Team {
                    external_id: "T".repeat(513),
                }
            }
            _ => {
                q.target = ReviewerTarget::Team {
                    external_id: "T_\n".into(),
                }
            }
        }
        let before = fs::read(io.join("gets"))?;
        ensure(
            matches!(client.set_review_request(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.status==400)
                && fs::read(io.join("gets"))? == before,
            "invalid reviewer change reached provider",
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
        ("wrong-pull", 502),
        ("wrong-repository", 502),
        ("wrong-number", 502),
        ("wrong-actor", 502),
        ("wrong-url", 502),
        ("remove-wrong-id", 502),
        ("remove-wrong-node", 502),
        ("remove-wrong-repository", 502),
        ("remove-wrong-number", 502),
        ("remove-wrong-url", 502),
        ("post-target-absent", 409),
        ("unrelated-dropped", 409),
        ("unrelated-mutated", 409),
        ("code-owner-changed", 409),
        ("post-hidden", 409),
        ("post-read-denied", 403),
        ("post-target-gone", 404),
        ("name-race-user", 409),
        ("name-race-team", 409),
        ("maximum-after", 413),
        ("oversized-request-id", 502),
        ("duplicate-request-id", 502),
        ("receipt-oversized", 413),
        ("drop-reply", 503),
        ("deadline", 504),
        ("deadline-after-reply", 504),
    ] {
        mode(name)?;
        let mut q = request();
        q.requested = !name.starts_with("remove-") && !name.starts_with("name-race");
        if name == "name-race-team" {
            q.target = ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            };
        }
        let before = writes()?;
        let start = Instant::now();
        let WriteOutcome::Indeterminate(f) = client.set_review_request(token()?, "321", &q).await
        else {
            return Err(format!("uncertain reviewer result became success/absence {name}").into());
        };
        ensure(
            f.status == status && writes()? == before + 1,
            &format!("reviewer uncertainty {name}: {} {}", f.status, f.code),
        )?;
        ensure(
            start.elapsed() < Duration::from_secs(10),
            "reviewer effect exceeded whole deadline",
        )?;
        cases.push(name);
        if name == "drop-reply" {
            mode("after-drop")?;
            ensure(
                matches!(client.set_review_request(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.code=="github_reviewer_already_desired")
                    && writes()? == before + 1,
                "matching request state attributed loss or repeated mutation",
            )?;
        }
    }
    mode("serialize")?;
    let a = request();
    let b = request();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.set_review_request(token()?, "321", &a),
        clone.set_review_request(token()?, "321", &b)
    );
    ensure(
        ((matches!(&first, WriteOutcome::Applied(_))
            && matches!(&second,WriteOutcome::NotSent(f)if f.code=="github_reviewer_already_desired"))
            || (matches!(&second, WriteOutcome::Applied(_))
                && matches!(&first,WriteOutcome::NotSent(f)if f.code=="github_reviewer_already_desired")))
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "reviewer clone serialization or mutation pacing failed",
    )?;
    for (name, attempted) in [("pause-preflight", false), ("pause-write", true)] {
        for marker in ["entered", "release", "cancelled"] {
            let _ = fs::remove_file(io.join(marker));
        }
        mode(name)?;
        let q = request();
        let t = token()?;
        let flight = client.clone();
        let pending = async move { Ok::<_, &str>(flight.set_review_request(t, "321", &q).await) };
        tokio::pin!(pending);
        super::native_github_transport::entered(pending.as_mut(), &io).await?;
        let start = Instant::now();
        owner.stop()?;
        let result = pending.await?;
        ensure(
            if attempted {
                matches!(result, WriteOutcome::Indeterminate(_))
            } else {
                matches!(result, WriteOutcome::NotSent(_))
            } && start.elapsed() < Duration::from_secs(2),
            "reviewer retirement lost dispatch boundary",
        )?;
        if !attempted {
            owner = tokio::task::spawn_blocking(Owner::start).await??;
            client = owner.client();
        }
    }
    ensure(
        matches!(
            client.set_review_request(token()?, "321", &request()).await,
            WriteOutcome::NotSent(_)
        ),
        "retired reviewer writer accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "reviewer writer sent undeclared request",
    )?;
    fs::write(
        root.join("reviewer-write-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":8,"clone_serialization":true,"owner_retirement":true,"real_github":false,"product_consumer_connected":false,"atomic_name_binding_qualified":false}),
        )?,
    )?;
    println!("native reviewer request/removal passed {} declared cases, 8 invalid descriptors, serialization and retirement",cases.len());
    Ok(())
}
