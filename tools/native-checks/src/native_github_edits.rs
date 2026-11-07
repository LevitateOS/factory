//! Real producer acceptance in the existing native integration executable.
//! Run only against an owned no-forwarding peer with literal dummy grants.
use super::native_github_fixture::{call, completed, posts, set_mode, uncertain};
use super::{ensure, prepare, Result};
use serde_json::json;
use sha2::{Digest, Sha256};
use soda_desktop_runtime::credentials::Token;
use soda_desktop_runtime::credentials::{Account, Credentials, Provider};
use soda_desktop_runtime::github_provider::{DiscussionEdit, Owner, WriteOutcome};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use soda_desktop_runtime::ProductRequest;
use std::sync::Arc;
use std::{
    fs,
    os::{
        fd::AsRawFd,
        unix::fs::{OpenOptionsExt, PermissionsExt},
    },
    path::Path,
    time::{Duration, Instant},
};
fn sha(s: &str) -> String {
    Sha256::digest(s.as_bytes())
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
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
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
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

fn expected(read: &serde_json::Value) -> Result<serde_json::Value> {
    Ok(
        json!({"external_id":read["ref"]["external_id"].as_str().ok_or("edit base identity absent")?,"title":read["title"].as_str().ok_or("edit base title absent")?,"body_digest":sha(read["body"].as_str().ok_or("edit base body absent")?)}),
    )
}
fn envelope(
    path: &str,
    id: &str,
    title: Option<&str>,
    body: Option<&str>,
    base: Option<&serde_json::Value>,
) -> Result<Vec<u8>> {
    let mut input = json!({"command_id":id});
    if let Some(title) = title {
        input["title"] = json!(title);
    }
    if let Some(body) = body {
        input["body"] = json!(body);
    }
    if let Some(base) = base {
        input["expected"] = base.clone();
    }
    let q: ProductRequest =
        serde_json::from_value(json!({"kind":"write","path":path,"body":input}))?;
    Ok(q.request()?.2)
}
pub(super) async fn product(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
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
        "edit product requires empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let saved = store
        .store(account()?, Token::new(b"factory-declared-edit".to_vec())?)
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
    ensure(catalogue.0 == 200, "edit canonical catalogue failed")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let root_path = format!("/api/factory/collaboration/repositories/{repository}");
    if std::env::var("SODA_NATIVE_CHECK_READ_CAPABILITIES").as_deref() == Ok("1") {
        let coordinator_refusal =
            std::env::var("SODA_NATIVE_CHECK_COORDINATOR_REFUSAL").as_deref() == Ok("1");
        if coordinator_refusal {
            let lock = fs::OpenOptions::new()
                .read(true)
                .write(true)
                .create(true)
                .mode(0o600)
                .open(profile.join("coordinator.lock"))?;
            ensure(
                unsafe { libc::flock(lock.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0,
                "owned coordinator check lock unavailable",
            )?;
            ensure(
                runtime.start_execution_coordinator().await.is_err(),
                "competing controller appeared admitted",
            )?;
            ensure(
                runtime.health().is_ok() && runtime.execution_error().is_some(),
                "coordinator refusal displaced product health",
            )?;
        }
        if std::env::var("SODA_NATIVE_CHECK_NO_EXECUTION").as_deref() == Ok("1") {
            ensure(
                runtime.execution_error().is_some(),
                "missing worker setup appeared execution-ready",
            )?;
            runtime.start_execution_coordinator().await?;
            ensure(
                runtime.health().is_ok() && runtime.execution_error().is_some(),
                "execution readiness displaced product health",
            )?;
        }
        if runtime.execution_error().is_some() {
            let ready = runtime.health()?;
            let bearer = fs::read_to_string(profile.join("backend.key"))?;
            let reply = soda_unix_http::request_authenticated_async(
                &profile.join("host.sock"),
                "POST",
                "/api/native/preparation",
                "soda-native",
                br#"{"operation":"prepare","request":{}}"#,
                &bearer,
                &[("X-Soda-Native-Instance", ready.instance.as_str())],
                Duration::from_secs(5),
                soda_unix_http::Limits {
                    header_bytes: 4096,
                    body_bytes: 4096,
                },
            )
            .await
            .map_err(|_| "owned unavailable preparation request failed")?;
            let body: serde_json::Value = serde_json::from_slice(&reply.body)?;
            ensure(
                reply.status == 503 && body["error"] == "native_execution_unavailable",
                "unavailable native preparation reached its effect owner",
            )?;
        }
        for mode in ["positive", "own", "locked", "archived"] {
            set_mode(&io, mode)?;
            let facts = call(
                &runtime,
                &store,
                &connection,
                "GET",
                &format!("{root_path}/pulls/7/capabilities"),
                &[],
            )
            .await?;
            ensure(facts.0 == 200, "read-role capabilities unavailable")?;
            let actions = &facts.1["actions"];
            for action in ["pull.comment", "pull.review", "pull.review.comment"] {
                let expected = if mode == "locked" || mode == "archived" {
                    "denied"
                } else {
                    "allowed"
                };
                ensure(
                    actions[action]["state"] == expected,
                    "read-role comment/review projection differs",
                )?;
            }
            for action in ["pull.review.approve", "pull.review.request_changes"] {
                let expected = if mode == "positive" {
                    "allowed"
                } else {
                    "denied"
                };
                ensure(
                    actions[action]["state"] == expected,
                    "author/lock/archive review restriction differs",
                )?;
            }
            ensure(
                actions["pull.merge"]["state"] != "allowed",
                "read-role review granted merge capability",
            )?;
        }
        set_mode(&io, "positive")?;
        ensure(
            posts(&io)? == 0 && !io.join("unexpected-request").exists(),
            "capability read performed a write or unknown request",
        )?;
        store.withdraw(&connection).await?;
        ensure(
            store.status().await?.is_empty(),
            "capability check left owned grants",
        )?;
        store.retire();
        let execution_unavailable = runtime.execution_error().is_some();
        runtime.stop()?;
        fs::write(
            root.join("read-capabilities-result.json"),
            serde_json::to_vec_pretty(&json!({
                "read_role_comments_reviews":true,"self_approval_refused":true,"locked_archived_refused":true,
                "merge_unavailable":true,"provider_mutations":0,"real_github":false,
                "coordinator_refusal":coordinator_refusal,"execution_unavailable":execution_unavailable
            }))?,
        )?;
        println!("PASS: native Rust/Go/PG read-role comment/review capabilities; self-approval, lock/archive and merge restrictions retained; declared peer, no writes");
        return Ok(());
    }
    if std::env::var("SODA_NATIVE_CHECK_LOCAL_RESOURCES").as_deref() == Ok("1") {
        let settings_path = format!("/api/repositories/{repository}/factory");
        let before = call(&runtime, &store, &connection, "GET", &settings_path, &[]).await?;
        ensure(before.0 == 200, "local settings read unavailable")?;
        ensure(
            before.1["policy"].is_null()
                && before.1["policy_options"]["target_branch"] == "refs/heads/main"
                && before.1["policy_options"]["merge_methods"] == json!(["squash", "rebase"]),
            "initial policy options invented a disabled or unsupported merge method",
        )?;
        let binding = &before.1["native_bindings"][0];
        let actor = json!({"native_profile":binding["native_profile"],
            "native_connection":binding["native_connection"],"token_id":binding["token_id"],"actor_id":binding["actor_id"]});
        let role = json!({"account":"declared-coder","harness":"muse","model":"muse-spark-1.3-contributor",
            "profile":{"effort":"high","contributor":false}});
        let mut reviewer = role.clone();
        reviewer["account"] = json!("declared-reviewer");
        let mut initial_role = role.clone();
        initial_role
            .as_object_mut()
            .ok_or("initial role object missing")?
            .remove("account");
        let changes = [
            json!({"kind":"capacity","body":{"expected_revision":0,"max_concurrent_runs":2}}),
            json!({"kind":"repository_setting","repository":repository,"setting":"placement",
                "body":{"expected_revision":"","state":"active"}}),
            json!({"kind":"repository_setting","repository":repository,"setting":"operator-grant",
                "body":{"expected_revision":0,"active":true}}),
            json!({"kind":"repository_setting","repository":repository,"setting":"environment-grant",
                "body":{"expected_revision":0,"active":true,"native":true}}),
            json!({"kind":"repository_setting","repository":repository,"setting":"policy",
                "body":{"expected_revision":0,"enabled":false,"paused":false,"target_branch":"refs/heads/main",
                    "roles":{"soda-coder":initial_role,"soda-reviewer":initial_role},"required_checks":["fixture/check"],
                    "merge_method":"squash","publish_actor":actor,"create_actor":actor,
                    "review_actor":actor,"merge_actor":actor}}),
        ];
        for change in changes {
            let request: ProductRequest = serde_json::from_value(change)?;
            let (method, target, body) = request.request()?;
            let reply = call(&runtime, &store, &connection, method, &target, &body).await?;
            ensure(
                reply.0 == 200,
                &format!(
                    "local resource change {target} refused: {} {}",
                    reply.0, reply.1["error"]["code"]
                ),
            )?;
        }
        let initial = call(&runtime, &store, &connection, "GET", &settings_path, &[]).await?;
        ensure(
            initial.0 == 200
                && initial.1["policy"]["revision"] == 1
                && initial.1["policy"]["enabled"] == false
                && initial.1["policy"]["roles"]["soda-coder"]["account"].is_null()
                && initial.1["policy"]["roles"]["soda-reviewer"]["account"].is_null()
                && initial.1["policy"]["judgments"].is_null()
                && initial.1["policy"]["merge_method"] == "squash",
            "disabled initial policy invented spending choices",
        )?;
        let update: ProductRequest = serde_json::from_value(
            json!({"kind":"repository_setting","repository":repository,"setting":"policy",
			"body":{"expected_revision":1,"enabled":false,"paused":false,"target_branch":"refs/heads/main",
				"roles":{"soda-coder":role,"soda-reviewer":reviewer},"required_checks":["fixture/check"],
				"merge_method":"rebase","publish_actor":actor,"create_actor":actor,"review_actor":actor,"merge_actor":actor}}),
        )?;
        let (method, target, body) = update.request()?;
        ensure(
            call(&runtime, &store, &connection, method, &target, &body)
                .await?
                .0
                == 200,
            "role update after initial policy refused",
        )?;
        let after = call(&runtime, &store, &connection, "GET", &settings_path, &[]).await?;
        ensure(
            after.0 == 200
                && after.1["capacity"]["max_concurrent_runs"] == 2
                && after.1["operator_grant"]["active"] == true
                && after.1["environment_grant"]["active"] == true
                && after.1["policy"]["enabled"] == false
                && after.1["policy"]["roles"]["soda-coder"]["account"] == "declared-coder"
                && after.1["policy"]["roles"]["soda-reviewer"]["account"] == "declared-reviewer",
            "local resource readback changed permission, limit or disabled automation",
        )?;
        let source = &after.1["spending_sources"][0];
        let source_id = source["connection"]
            .as_str()
            .ok_or("Muse source reference absent")?;
        ensure(
            source["label"] == "This Muse login"
                && source["native"]["authentication"] == "muse_login"
                && source["native"]["credential_sha256"].is_null()
                && source["execution_available"] == false,
            "Muse login source invented credential/account or execution proof",
        )?;
        let funding: ProductRequest = serde_json::from_value(
            json!({"kind":"sponsorship","repository":repository,"connection":source_id,
            "body":{"expected_revision":0,"roles":["soda-coder","soda-reviewer","factory-judgment"],"allowance_minutes":30,"active":true}}),
        )?;
        let (method, target, body) = funding.request()?;
        let granted = call(&runtime, &store, &connection, method, &target, &body).await?;
        ensure(
            granted.0 == 200,
            &format!("Muse source allowance failed: {} {}", granted.0, granted.1),
        )?;
        let funded = call(&runtime, &store, &connection, "GET", &settings_path, &[]).await?;
        let saved = &funded.1["sponsorships"][0];
        ensure(
            saved["connection"] == source_id
                && saved["native"]["authentication"] == "muse_login"
                && saved["native"]["credential_sha256"].is_null()
                && saved["allowance_minutes"] == 30
                && saved["active"] == true,
            "Muse source allowance lost CLI-managed authentication",
        )?;
        let mut selected_role = role.clone();
        selected_role["account"] = json!(source_id);
        let mut judgment = selected_role.clone();
        judgment["model"] = json!("declared-judgment-model");
        let selected = json!({"expected_revision":2,"enabled":false,"paused":false,"target_branch":"refs/heads/main",
            "roles":{"soda-coder":selected_role,"soda-reviewer":selected_role},"judgments":judgment,"required_checks":["fixture/check"],"merge_method":"rebase",
            "publish_actor":actor,"create_actor":actor,"review_actor":actor,"merge_actor":actor});
        ensure(
            call(
                &runtime,
                &store,
                &connection,
                "PUT",
                &format!("{settings_path}/policy"),
                &serde_json::to_vec(&selected)?,
            )
            .await?
            .0 == 200,
            "explicit per-role Muse login selection refused",
        )?;
        let selected = call(&runtime, &store, &connection, "GET", &settings_path, &[]).await?;
        ensure(
            selected.1["policy"]["roles"]["soda-coder"]["account"] == source_id
                && selected.1["policy"]["roles"]["soda-reviewer"]["account"] == source_id
                && selected.1["policy"]["judgments"]["account"] == source_id
                && selected.1["policy"]["judgments"]["model"] == "declared-judgment-model"
                && selected.1["sponsorships"][0]["roles"]
                    .as_array().is_some_and(|roles| roles.contains(&json!("factory-judgment")))
                && selected.1["policy"]["enabled"] == false,
            "saved Muse role choice changed source or enabled automation",
        )?;
        ensure(
            call(&runtime, &store, &connection, "PUT", &target, &body)
                .await?
                .0
                == 412,
            "stale Muse source allowance overwrote current funding",
        )?;
        let withdrawal = json!({"expected_revision":saved["revision"],"roles":["soda-coder","soda-reviewer","factory-judgment"],"allowance_minutes":30,"active":false});
        ensure(
            call(
                &runtime,
                &store,
                &connection,
                "PUT",
                &target,
                &serde_json::to_vec(&withdrawal)?,
            )
            .await?
            .0 == 200,
            "Muse source withdrawal required a new login",
        )?;
        let withdrawn = call(&runtime, &store, &connection, "GET", &settings_path, &[]).await?;
        ensure(
            withdrawn.1["sponsorships"][0]["active"] == false
                && withdrawn.1["policy"]["judgments"]["account"] == source_id
                && withdrawn.1["policy"]["judgments"]["model"] == "declared-judgment-model",
            "Muse source withdrawal was not retained or replaced the judgment selection",
        )?;
        println!("PASS: explicit CLI-managed Muse source allowance, per-role and judgment selection, CAS/readback and withdrawal without a Muse credential or billing identity; judgment execution remains unavailable");
        let access = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{settings_path}/access"),
            &[],
        )
        .await?;
        ensure(
            access.0 == 200
                && access.1["viewer"]["local_owner"] == true
                && access.1["viewer"]["owner_or_admin"] == false
                && access.1["viewer"]["code_write"] == false
                && access.1["editable"]["policy"]["editable"] == true
                && access.1["editable"]["operator_grant"]["editable"] == true
                && access.1["editable"]["environment_grant"]["editable"] == true,
            "native local-owner settings projection unavailable or invented provider permission",
        )?;
        let denied = call(&runtime, &store, &connection, "POST", "/api/factory/intents", &serde_json::to_vec(&json!({
            "id":format!("d{}","1".repeat(24)),"intent":format!("d{}","2".repeat(24)),
            "repository":repository,"objective":"Do not infer repository write from local ownership",
            "state":"draft","max_issues":1,"ideas":[],"tasks":[]
        }))?).await?;
        ensure(
            denied.0 == 403,
            "local resource ownership became repository write permission",
        )?;
        fs::write(
            root.join("local-resource-result.json"),
            serde_json::to_vec(&json!({
                "local_owner_controls":true,"run_limit":2,"repository_run_permission":true,
                "workspace_permission":true,"policy_enabled":false,"provider_write_refused":true,"access_readback":true,"initial_policy_without_spending":true,"enabled_merge_options":true,
            "provider_permissions":"declared push=false and no admin observation; no real provider effects"
            }))?,
        )?;
        println!("PASS: native local-owner capacity/placement/repository/workspace/policy setup and readback without repository admin; provider write stays refused and automation disabled");
        store.withdraw(&connection).await?;
        ensure(
            store.status().await?.is_empty(),
            "local settings check left owned grants",
        )?;
        store.retire();
        runtime.stop()?;
        return Ok(());
    }
    let mut cases = Vec::new();
    for (i, (kind, title, body)) in [
        (
            "issues",
            Some("Exact issue title π".to_owned()),
            Some("Exact human body 🍵\n[link](relative.md)".to_owned()),
        ),
        (
            "pulls",
            Some("Exact PR title".into()),
            Some("Exact PR description".into()),
        ),
        ("issues", Some("Title-only edit".into()), None),
        ("issues", None, Some("".into())),
        ("issues", Some("t".repeat(255)), Some("🍵".repeat(16384))),
        (
            "pulls",
            Some("Maximum escaped body".into()),
            Some("\u{0001}".repeat(65536)),
        ),
    ]
    .into_iter()
    .enumerate()
    {
        let subject = format!("{root_path}/{kind}/7");
        let submit = format!("{subject}/edits");
        let id = format!("{:032x}", 100 + i);
        let command = format!("{submit}/commands/{id}");
        let read = call(&runtime, &store, &connection, "GET", &subject, &[]).await?;
        ensure(read.0 == 200, "edit normal original read failed")?;
        let base = expected(&read.1)?;
        let bytes = envelope(&submit, &id, title.as_deref(), body.as_deref(), Some(&base))?;
        let before = posts(&io)?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "committed",
        )?;
        let current = call(&runtime, &store, &connection, "GET", &subject, &[]).await?;
        ensure(
            current.0 == 200
                && current.1["ref"]["external_id"] == read.1["ref"]["external_id"]
                && current.1["title"].as_str()
                    == Some(
                        title
                            .as_deref()
                            .unwrap_or(read.1["title"].as_str().ok_or("original title absent")?),
                    )
                && current.1["body"].as_str()
                    == Some(
                        body.as_deref()
                            .unwrap_or(read.1["body"].as_str().ok_or("original body absent")?),
                    ),
            "edit readback changed requested or omitted fields",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "committed",
        )?;
        completed(
            &call(&runtime, &store, &connection, "GET", &command, &[]).await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{command}/cancel"),
                b"{}",
            )
            .await?,
            "committed",
        )?;
        let conflict = envelope(
            &submit,
            &id,
            Some("Different retry intent"),
            body.as_deref(),
            Some(&base),
        )?;
        ensure(
            call(&runtime, &store, &connection, "POST", &submit, &conflict)
                .await?
                .0
                == 409
                && posts(&io)? == before + 1,
            "edit replay/conflict/cancel repeated mutation",
        )?;
        cases.push(format!("positive-{i}"));
    }
    let subject = format!("{root_path}/issues/7");
    let submit = format!("{subject}/edits");
    let command = |id: &str| format!("{submit}/commands/{id}");
    let read = call(&runtime, &store, &connection, "GET", &subject, &[]).await?;
    let mut stale = expected(&read.1)?;
    stale["title"] = json!("Stale client title");
    let stale_id = "11111111111111111111111111111111";
    let stale_bytes = envelope(
        &submit,
        stale_id,
        Some("Must not replace stale content"),
        None,
        Some(&stale),
    )?;
    let before = posts(&io)?;
    completed(
        &call(&runtime, &store, &connection, "POST", &submit, &stale_bytes).await?,
        "not_committed",
    )?;
    completed(
        &call(&runtime, &store, &connection, "POST", &submit, &stale_bytes).await?,
        "not_committed",
    )?;
    ensure(
        posts(&io)? == before,
        "stale expected content reached provider",
    )?;
    cases.push("stale-client-base".into());
    for name in ["stale-after-prepare"] {
        let read = call(&runtime, &store, &connection, "GET", &subject, &[]).await?;
        let base = expected(&read.1)?;
        let bytes = envelope(
            &submit,
            "22222222222222222222222222222222",
            None,
            Some("Must not overwrite changed base"),
            Some(&base),
        )?;
        set_mode(&io, name)?;
        let before = posts(&io)?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "not_committed",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "not_committed",
        )?;
        ensure(
            posts(&io)? == before,
            "changed provider preflight reached mutation",
        )?;
        cases.push(name.into());
        set_mode(&io, "positive")?;
    }
    let read = call(&runtime, &store, &connection, "GET", &subject, &[]).await?;
    let base = expected(&read.1)?;
    let concurrent = "33333333333333333333333333333333";
    let bytes = envelope(
        &submit,
        concurrent,
        Some("Concurrent edit"),
        Some("One mutation"),
        Some(&base),
    )?;
    let before = posts(&io)?;
    let (a, b) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &bytes),
        call(&runtime, &store, &connection, "POST", &submit, &bytes)
    );
    for r in [a?, b?] {
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
            &command(concurrent),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        posts(&io)? == before + 1,
        "concurrent exact edits sent twice",
    )?;
    cases.push("concurrent-identical-command".into());
    let wrong = format!(
        "/api/factory/collaboration/repositories/{}/issues/7/edits",
        repository.parse::<i64>()? + 1
    );
    ensure(
        call(
            &runtime,
            &store,
            &connection,
            "POST",
            &wrong,
            &envelope(
                &wrong,
                "99999999999999999999999999999999",
                Some("Wrong scope"),
                None,
                None,
            )?,
        )
        .await?
        .0 == 403
            && posts(&io)? == before + 1,
        "edit expanded selected repository",
    )?;
    cases.push("other-repository-refusal".into());
    // Matching current writing does not attribute a lost mutation to this command.
    let lost_id = "44444444444444444444444444444444";
    let lost = envelope(
        &submit,
        lost_id,
        Some("Owned lost-reply edit"),
        Some("Readable writing is not attribution"),
        None,
    )?;
    set_mode(&io, "drop-reply")?;
    let before = posts(&io)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    set_mode(&io, "positive")?;
    let read = call(&runtime, &store, &connection, "GET", &subject, &[]).await?;
    ensure(
        read.0 == 200 && read.1["title"] == "Owned lost-reply edit",
        "lost edit fixture did not retain actual readable mutation",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "matching writing fabricated attribution or authorized retry",
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
        let read = call(&runtime, &store, &connection, "GET", &subject, &[]).await?;
        let base = expected(&read.1)?;
        let bytes = envelope(
            &submit,
            id,
            Some("Cancellation-race writing"),
            None,
            Some(&base),
        )?;
        set_mode(&io, name)?;
        let before = posts(&io)?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let pending = request.request("POST", &submit, &bytes);
        tokio::pin!(pending);
        super::native_github_fixture::entered(pending.as_mut(), &io).await?;
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
            "racing edit cancellation invented rollback",
        )?;
        fs::write(io.join("release"), b"")?;
        let reply = pending.await?;
        let result = (reply.status, serde_json::from_slice(&reply.body)?);
        completed(&result, effect)?;
        ensure(
            result.1["cancellation"] == "indeterminate"
                && posts(&io)? == before + u64::from(name == "pause-post"),
            "late edit result erased cancellation or repeated POST",
        )?;
        cases.push(name.into());
        set_mode(&io, "positive")?;
    }
    set_mode(&io, "pause-post")?;
    let withdrawn_id = "77777777777777777777777777777777";
    let withdrawn = envelope(
        &submit,
        withdrawn_id,
        Some("Withdrawn-generation writing"),
        None,
        None,
    )?;
    let before = posts(&io)?;
    let request = runtime.admit_product(
        store.clone(),
        store.capability(&connection).await?,
        Some("123".into()),
    )?;
    let pending = request.request("POST", &submit, &withdrawn);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    store.withdraw(&connection).await?;
    connection = store
        .store(account()?, Token::new(b"factory-declared-edit".to_vec())?)
        .await?
        .connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "retired generation returned edit success",
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
        "replacement custody replayed uncertain edit",
    )?;
    cases.push("withdrawal-replacement".into());
    set_mode(&io, "pause-post")?;
    let stopped_id = "88888888888888888888888888888888";
    let stopped = envelope(
        &submit,
        stopped_id,
        None,
        Some("Retained uncertain edit"),
        None,
    )?;
    let before = posts(&io)?;
    let request = runtime.admit_product(
        store.clone(),
        store.capability(&connection).await?,
        Some("123".into()),
    )?;
    let pending = request.request("POST", &submit, &stopped);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        pending.await.is_err(),
        "stopped runtime returned edit result",
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
    for (i, kind) in ["issues", "pulls", "issues", "issues", "issues", "pulls"]
        .into_iter()
        .enumerate()
    {
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "GET",
                &format!("{root_path}/{kind}/7/edits/commands/{:032x}", 100 + i),
                &[],
            )
            .await?,
            "committed",
        )?;
    }
    ensure(posts(&io)? == before + 1, "restart replayed captured edit")?;
    cases.push("retained-restart".into());
    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?
                .with_label("declared-other")?,
            Token::new(b"factory-declared-other".to_vec())?,
        )
        .await?;
    ensure(
        call(
            &runtime,
            &store,
            &other.connection,
            "GET",
            &command(lost_id),
            &[],
        )
        .await?
        .0 == 404,
        "another account read edit capture",
    )?;
    ensure(
        call(&runtime, &store, &other.connection, "POST", &submit, &lost)
            .await?
            .0
            == 409
            && posts(&io)? == before + 1,
        "another account adopted edit capture",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "edit product fixture received unexpected request",
    )?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "edit product left owned grants",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("edit-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_provider":false,"native_product_consumer":true,"provider_posts":posts(&io)?,"capabilities_enabled":false,"retained_restart":true,"maximum_writing_bytes":65536,"atomic_content_precondition":false}),
        )?,
    )?;
    println!("PASS: composed native/Rust/Go/PG edits preserve exact partial writing, original-content checks, causal identity, one-shot replay, uncertain replies, cancel races, withdrawal, other-account refusal and restart; dummy-only peer, no provider CAS or actual GitHub/installed qualification");
    Ok(())
}
