//! Existing native executable's production reader acceptance. Use only an
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
