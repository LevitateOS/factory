//! Real isolated Secret Service + native webview acceptance, opt-in only.
use super::*;
use soda_desktop_runtime::credentials::{Account, CredentialState, Error, Provider, Token};
use std::io::Read;
use std::time::Duration;

pub fn start_github_writes(app: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        if let Err(error) = github_writes(&app).await {
            eprintln!("Native GitHub UI write check failed: {error}");
            app.exit(1);
        }
    });
}
async fn github_writes(app: &tauri::AppHandle) -> Result<(), String> {
    use soda_desktop_runtime::github_provider::Owner;
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("owned fixture root missing")?;
    let fixture: serde_json::Value = serde_json::from_slice(
        &std::fs::read(root.join("github-write-fixture.json"))
            .map_err(|_| "owned write fixture missing")?,
    )
    .map_err(|_| "invalid owned write fixture")?;
    ensure(
        std::fs::read_to_string(root.join("connected-fixture-repository"))
            .map_err(|_| "owned fixture marker missing")?
            == fixture["repository"]
                .as_str()
                .ok_or("fixture repository missing")?,
        "owned fixture mismatch",
    )?;
    ensure(
        fixture["name"]
            .as_str()
            .is_some_and(|s| s.split('/').count() == 2)
            && if fixture["issue_only"] == true {
                fixture["issue_number"].as_u64().is_some_and(|n| n > 0)
            } else {
                fixture["number"].as_u64().is_some_and(|n| n > 0)
            },
        "owned write fixture missing",
    )?;
    let store = state.credentials()?;
    let accounts = store.status().await.map_err(|e| e.to_string())?;
    let factory_oauth = fixture["oauth_source"] == "factory_device";
    if factory_oauth {
        ensure(
            app.state::<Desktop>().check.scenario.consent_requested,
            "Factory OAuth check requires the consent observer",
        )?;
        let view = serde_json::to_value(state.signin.view()).map_err(|_| "sign-in status unavailable")?;
        if accounts.is_empty() {
            ensure(view["phase"] == "idle", "owned sign-in already started")?;
            start_signin(app.clone(), check::SignInMode::Consent);
            return Ok(());
        }
        ensure(
            (view["phase"] == "complete" || fixture["resume_pending"] == true)
                && accounts.len() == 1 && accounts[0].grant.is_some(),
            "fresh Factory OAuth grant not observed",
        )?;
    }
    let connection = if !accounts.is_empty() {
        ensure(
            (factory_oauth || fixture["resume_pending"] == true || fixture["pull_read_only"] == true)
                && accounts.len() == 1,
            "unexpected owned check account",
        )?;
        accounts[0].connection.clone()
    } else {
        let mut authorization = std::process::Command::new("gh")
            .args(["auth", "token", "--hostname", "github.com"])
            .output()
            .map_err(|_| "authorized gh grant unavailable")?;
        ensure(
            authorization.status.success(),
            "authorized gh grant unavailable",
        )?;
        while authorization
            .stdout
            .last()
            .is_some_and(u8::is_ascii_whitespace)
        {
            authorization.stdout.pop();
        }
        let token = Token::new(authorization.stdout).map_err(|e| e.to_string())?;
        let mut owner = tokio::task::spawn_blocking(Owner::start)
            .await
            .map_err(|_| "native provider unavailable")?
            .map_err(|e| e.to_string())?;
        let (account, token) = owner
            .client()
            .verify(token)
            .await
            .map_err(|e| e.to_string())?;
        tokio::task::spawn_blocking(move || owner.stop())
            .await
            .map_err(|_| "native provider did not join")?
            .map_err(|e| e.to_string())?;
        store
            .store(account, token)
            .await
            .map_err(|e| e.to_string())?
            .connection
    };
    if fixture["mode"] == "other_actor" {
        ensure(fixture["expected_account"].as_str().is_some(), "other-actor fixture account missing")?;
    }
    if let Some(expected) = fixture["expected_account"].as_str() {
        let accounts = store.status().await.map_err(|e| e.to_string())?;
        ensure(
            accounts.len() == 1 && accounts[0].account == expected,
            "owned check signed into the wrong GitHub account",
        )?;
        println!("Owned GitHub check account: {} ({})", accounts[0].label.as_deref().unwrap_or("unlabelled"), accounts[0].account);
    }
    if fixture["mode"] == "inspect" {
        return Ok(());
    }
    let script = include_str!("github_writes_check.js")
        .replace(
            "__CONNECTION__",
            &serde_json::to_string(&connection).unwrap(),
        )
        .replace("__FIXTURE__", &serde_json::to_string(&fixture).unwrap());
    app.get_webview_window("main")
        .ok_or("main window missing")?
        .eval(&script)
        .map_err(|_| "native write observer unavailable".into())
}
pub fn finish_github_writes(app: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        let result = async {
            let state = app.state::<Desktop>();
            let store = state.credentials()?;
            let accounts = store.status().await.map_err(|e| e.to_string())?;
            ensure(accounts.len() == 1, "unexpected owned write-check accounts")?;
            let root = state.paths.profile.parent().ok_or("owned fixture root missing")?;
            let fixture: serde_json::Value = serde_json::from_slice(
                &std::fs::read(root.join("github-write-fixture.json"))
                    .map_err(|_| "owned fixture missing")?,
            ).map_err(|_| "invalid fixture")?;
            let retained = fixture["mode"] == "other_actor" && fixture["review_stage"] == "draft"
                || (fixture["mode"] == "reviewer_requests" || fixture["issue_navigation"] == true)
                    && fixture["retain_account"] == true;
            if !retained {
                store.withdraw(&accounts[0].connection).await.map_err(|e| e.to_string())?;
            }
            if fixture["mode"] == "placement" {
                write_consent_evidence(&app, "github-ui-write-result.json", &serde_json::json!({
                    "real_github_admin_observation": true,
                    "production_ui_ipc_go_pg_native": true,
                    "placement_cas_generation_retirement_readback": true,
                    "native_workspace_enrollment_readback": true,
                    "automation_started": false,
                    "owned_account_removed": true,
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["mode"] == "merge_only" {
                write_consent_evidence(&app, "github-ui-write-result.json", &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "merge_method": fixture["merge_method"],
                    "multiline_message": fixture["merge_method"] == "squash",
                    "message_free_rebase": fixture["merge_method"] == "rebase",
                    "committed_event_and_normal_readback": true,
                    "owned_account_removed": true,
                    "oauth_source": "user_authorized_gh_grant",
                    "factory_device_scope_qualified": false,
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["mode"] == "branch_rule" {
                write_consent_evidence(&app, "github-ui-write-result.json", &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "protected_branch_merge_refused_before_capture": true,
                    "owned_account_removed": true,
                    "oauth_source": "user_authorized_gh_grant",
                    "installed_qualified": false,
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["mode"] == "other_actor" || fixture["mode"] == "reviewer_requests" {
                let oauth = fixture["oauth_source"] == "factory_device";
                let evidence_name = if fixture["mode"] == "other_actor" {
                    if retained { "github-draft-result.json" } else { "github-verdict-result.json" }
                } else { "github-ui-write-result.json" };
                write_consent_evidence(&app, evidence_name, &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "reviewer_username_lookup_request_remove_request": fixture["mode"] == "reviewer_requests" && fixture["reviewer_kind"] != "team",
                    "reviewer_team_slug_lookup_request_remove_request": fixture["mode"] == "reviewer_requests" && fixture["reviewer_kind"] == "team" && fixture["expect_unavailable"] != true,
                    "unsupported_team_request_refused_before_capture": fixture["expect_unavailable"] == true,
                    "uncertain_reviewer_command_visible": fixture["expect_saved_reviewer"] == true,
                    "own_pending_summary_file_readback": fixture["mode"] == "other_actor",
                    "other_actor_approve_request_changes": fixture["review_stage"] == "verdicts",
                    "pending_comment_publication": fixture["review_stage"] == "publication",
                    "account": accounts[0].account,
                    "owned_account_removed": !retained,
                    "owned_account_retained_for_cross_account_read": retained,
                    "oauth_source": if oauth { "factory_device" } else { "user_authorized_gh_grant" },
                    "factory_device_write_scope_observed": oauth,
                    "granted_scope": accounts[0].grant.as_ref().map(|grant| &grant.scope),
                    "factory_device_scope_qualified": false,
                    "cross_account_privacy_qualified": false,
                    "notification_delivery_qualified": false,
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["outdated_thread"] == true {
                write_consent_evidence(&app, "github-ui-write-result.json", &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "outdated_discussion_and_file_fallback": true,
                    "provider_writes": false,
                    "owned_account_removed": true,
                    "oauth_source": "user_authorized_gh_grant",
                    "factory_device_scope_qualified": false,
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["issue_navigation"] == true {
                let evidence_name = if retained { "github-issue-draft-result.json" } else { "github-ui-write-result.json" };
                write_consent_evidence(&app, evidence_name, &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "feed_issue_comment_navigation": true,
                    "factory_detail_back_deep_link": true,
                    "unsubmitted_issue_draft_retained": true,
                    "unsent_draft_after_desktop_restart": fixture["resume_pending"] == true,
                    "issue_closed_and_reopened": fixture["issue_state"] == true,
                    "provider_writes": fixture["issue_state"] == true,
                    "owned_account_removed": !retained,
                    "oauth_source": "user_authorized_gh_grant",
                    "factory_device_scope_qualified": false,
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["diff_edge"] == true {
                write_consent_evidence(&app, "github-ui-write-result.json", &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "large_file_pagination": true,
                    "renamed_and_partial_patches": true,
                    "provider_writes": false,
                    "owned_account_removed": true,
                    "oauth_source": "user_authorized_gh_grant",
                    "factory_device_scope_qualified": false,
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["checks_read_only"] == true {
                write_consent_evidence(&app, "github-ui-write-result.json", &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "populated_pr_jobs_rendered": true,
                    "provider_writes": false,
                    "owned_account_removed": true,
                    "oauth_source": "user_authorized_gh_grant",
                }))?;
                return Ok::<(), String>(());
            }
            if fixture["pull_read_only"] == true {
                write_consent_evidence(&app, "github-ui-write-result.json", &serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "pull_list_detail": true,
                    "conversation_commits_files_reviews_threads_checks": true,
                    "provider_writes": false,
                    "owned_account_removed": true,
                    "oauth_source": "user_authorized_gh_grant",
                    "factory_device_scope_qualified": false,
                }))?;
                return Ok::<(), String>(());
            }
            let reviews = fixture["mode"] != "thread_resolution";
            let mut evidence = if fixture["issue_only"] == true {
                serde_json::json!({
                    "real_github": true,
                    "webkit": true,
                    "production_ui_ipc_go_pg_native": true,
                    "issue_list_navigation": true,
                    "issue_title_body_edit_readback": true,
                    "issue_comment_readback": true,
                    "owned_account_removed": true,
                    "oauth_source": "user_authorized_gh_grant",
                    "factory_device_scope_qualified": false,
                })
            } else { serde_json::json!({
                "real_github": true,
                "webkit": true,
                "production_ui_ipc_go_pg_native": true,
                "inline_draft_created": reviews && (fixture["resume_pending"] != true || fixture["append_pending"] == true),
                "summary_first_created": reviews && fixture["summary_first"] == true && fixture["resume_pending"] != true,
                "pending_inline_readback": reviews,
                "pending_summary_edit_clear": reviews && fixture["summary_first"] == true && (fixture["resume_pending"] != true || fixture["edit_summary"] == true),
                "inline_only_local_summary": reviews && (fixture["summary_first"] != true || (fixture["resume_pending"] == true && fixture["edit_summary"] != true)),
                "submit_pending_review": reviews,
                "immediate_range": reviews,
                "ordinary_merge": reviews,
                "merged_thread_resolve_reopen": !reviews,
                "readback": true,
                "owned_account_removed": true,
                "oauth_source": "user_authorized_gh_grant",
                "factory_device_scope_qualified": false,
            }) };
            if fixture["oauth_source"] == "factory_device" {
                evidence["oauth_source"] = serde_json::json!("factory_device");
                evidence["factory_device_write_scope_observed"] = serde_json::json!(true);
                evidence["granted_scope"] = serde_json::json!(accounts[0].grant.as_ref().ok_or("OAuth grant metadata missing")?.scope);
            }
            write_consent_evidence(&app, "github-ui-write-result.json", &evidence)?;
            Ok::<(), String>(())
        }.await;
        match result {
            Ok(()) => check::github_writes_done(app),
            Err(error) => {
                eprintln!("Native write-check cleanup failed: {error}");
                app.exit(1);
            }
        }
    });
}

/// Publish only the same public prompt already rendered by the production UI.
/// The device code and grants never enter the observer's files or JS arguments.
pub fn publish_consent_prompt(app: &tauri::AppHandle) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let view = serde_json::to_value(state.signin.view()).map_err(|_| "prompt unavailable")?;
    ensure(view["phase"] == "waiting", "sign-in is not waiting")?;
    write_consent_evidence(app, "user-code.json", &view["prompt"])
}
fn write_consent_evidence(
    app: &tauri::AppHandle,
    name: &str,
    value: &serde_json::Value,
) -> Result<(), String> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let mut file = std::fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(root.join(name))
        .map_err(|_| "owned consent evidence unavailable")?;
    file.write_all(&serde_json::to_vec(value).map_err(|_| "evidence encoding failed")?)
        .map_err(|_| "evidence write failed".into())
}
pub fn start_consent_checks(app: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        match consent_checks(&app).await {
            Ok(()) => check::consent_done(&app),
            Err(error) => {
                eprintln!("Native approved sign-in check failed: {error}");
                app.exit(1);
            }
        }
    });
}
async fn consent_read(
    app: &tauri::AppHandle,
    connection: &str,
    query: soda_desktop_runtime::github::Read,
) -> Result<serde_json::Value, String> {
    let reply = native_github_read(app, connection, query).await?;
    ensure(reply.status == 200, "authenticated native read refused")?;
    Ok(reply.body)
}
async fn native_github_read(
    app: &tauri::AppHandle,
    connection: &str,
    query: soda_desktop_runtime::github::Read,
) -> Result<soda_desktop_runtime::github::Reply, String> {
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    let cap = store
        .capability(connection)
        .await
        .map_err(|e| e.to_string())?;
    let read = state
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .ok_or("runtime missing")?
        .github_read(&store, &cap, query);
    read.await.map_err(|e| e.to_string())
}

fn start_binding_checks(app: tauri::AppHandle, retained: bool) {
    tauri::async_runtime::spawn(async move {
        if let Err(error) = binding_checks(&app, retained).await {
            eprintln!("Native canonical binding check failed: {error}");
            app.exit(1);
        }
    });
}
async fn binding_checks(app: &tauri::AppHandle, retained: bool) -> Result<(), String> {
    use soda_desktop_runtime::github::Read as GitHubRead;
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let io = root.join("io");
    let accounts = state
        .credentials()?
        .status()
        .await
        .map_err(|e| e.to_string())?;
    ensure(accounts.len() == 1, "canonical check account missing")?;
    let connection = &accounts[0].connection;
    let profile = state
        .status
        .lock()
        .unwrap()
        .profile
        .clone()
        .ok_or("profile missing")?;
    let binding = consent_read(app, connection, GitHubRead::Binding).await?["binding"].clone();
    ensure(
        binding["profile"]["id"] == profile
            && binding["actor"]["external_id"] == "123"
            && binding["connection"]["id"] == *connection
            && binding["actor"]["id"] != "123",
        "native canonical binding scope missing",
    )?;
    if !retained {
        // Use an actually verified declared token in a second owned credential
        // profile. That valid credential must still not bind this runtime's DB.
        let store = state.credentials()?;
        let cap = store
            .capability(connection)
            .await
            .map_err(|e| e.to_string())?;
        let token = store
            .with_token(&cap, |bytes| Token::new(bytes.to_vec()))
            .await
            .map_err(|e| e.to_string())?
            .map_err(|e| e.to_string())?;
        let verify = state
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .ok_or("runtime missing")?
            .github_verify(token);
        let (remote, token) = verify.await.map_err(|e| e.to_string())?;
        let other_dir = state.paths.profile.join("other-canonical-profile");
        prepare_profile(&other_dir)?;
        let other_profile = String::from_utf8(fixture()).map_err(|_| "fixture profile invalid")?;
        let other =
            Credentials::open(&other_dir, &other_profile[..32]).map_err(|e| e.to_string())?;
        let saved = other
            .store(remote, token)
            .await
            .map_err(|e| e.to_string())?;
        let other_cap = other
            .capability(&saved.connection)
            .await
            .map_err(|e| e.to_string())?;
        for query in [
            GitHubRead::Binding,
            GitHubRead::CanonicalIssues {
                repository: "1".into(),
                page: 1,
                limit: 2,
                state: "open".into(),
            },
            GitHubRead::CanonicalIssue {
                repository: "1".into(),
                number: 7,
            },
        ] {
            let read = state
                .runtime
                .lock()
                .unwrap()
                .as_ref()
                .ok_or("runtime missing")?
                .github_read(&other, &other_cap, query);
            ensure(
                matches!(
                    read.await,
                    Err(soda_desktop_runtime::github::Error::Invalid)
                ),
                "foreign credential profile reached canonical operation",
            )?;
        }
        other
            .withdraw(&saved.connection)
            .await
            .map_err(|e| e.to_string())?;
    }
    let first = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    let page = &first["repositories"];
    ensure(
        page["items"].as_array().is_some_and(|v| v.len() == 1)
            && page["items"][0]["external_id"] == "9007199254740993"
            && page["items"][0]["id"] != page["items"][0]["external_id"]
            && page["next"] == "2"
            && page["complete"] == false,
        "canonical first repository page invalid",
    )?;
    if retained {
        let proof: serde_json::Value = serde_json::from_slice(
            &std::fs::read(root.join("canonical-binding.json"))
                .map_err(|_| "binding proof missing")?,
        )
        .map_err(|_| "binding proof invalid")?;
        ensure(
            binding == proof["binding"] && page["items"][0] == proof["repository"],
            "runtime restart changed canonical account/repository",
        )?;
        issue_checks(app, connection, &binding, &page["items"][0], true).await?;
        if app.state::<Desktop>().check.scenario.issue_read {
            issue_read_journey(app, connection).await?;
        }
        return write_consent_evidence(
            app,
            "canonical-binding-retained.json",
            &serde_json::json!({"retained_mapping":true}),
        );
    }
    let second = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 2, limit: 1 },
    )
    .await?;
    ensure(
        second["repositories"]["next"] == ""
            && second["repositories"]["complete"] == true
            && second["repositories"]["items"][0]["external_id"] == "9007199254740995",
        "canonical next page invalid",
    )?;
    let repeat = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    ensure(
        repeat["binding"] == binding
            && repeat["repositories"]["observation"]["revision"] == page["observation"]["revision"],
        "reconnect forked canonical account/catalogue",
    )?;
    std::fs::write(io.join("mode"), "binding-rename").map_err(|_| "fixture mode failed")?;
    let renamed = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    ensure(
        renamed["binding"]["actor"]["id"] == binding["actor"]["id"]
            && renamed["binding"]["actor"]["login"] == "renamed-native"
            && renamed["repositories"]["items"][0]["id"] == page["items"][0]["id"]
            && renamed["repositories"]["items"][0]["name"] == "owner/renamed"
            && renamed["repositories"]["observation"]["revision"]
                != page["observation"]["revision"],
        "rename changed identity or retained stale page revision",
    )?;
    for (mode, expected) in [
        ("identity-denied", 401),
        ("binding-wrong-account", 409),
        ("binding-catalog-denied", 403),
        ("binding-bad-page", 502),
        ("binding-live-refusal", 401),
    ] {
        std::fs::write(io.join("mode"), mode).map_err(|_| "fixture mode failed")?;
        let reply = native_github_read(
            app,
            connection,
            GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
        )
        .await?;
        ensure(
            reply.status == expected
                && reply.body.get("binding").is_none()
                && reply.body.get("repositories").is_none(),
            "failed canonical read became cached inventory",
        )?;
        if mode == "binding-live-refusal" {
            let reply = native_github_read(app, connection, GitHubRead::Binding).await?;
            ensure(
                reply.status == 401 && reply.body.get("binding").is_none(),
                "declared provider refusal admitted canonical account",
            )?;
        }
    }
    for name in ["entered", "cancelled"] {
        let _ = std::fs::remove_file(io.join(name));
    }
    std::fs::write(io.join("mode"), "binding-pause").map_err(|_| "fixture mode failed")?;
    let task_app = app.clone();
    let task_connection = connection.clone();
    let task = tauri::async_runtime::spawn(async move {
        native_github_read(
            &task_app,
            &task_connection,
            GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
        )
        .await
    });
    for _ in 0..100 {
        if io.join("entered").is_file() {
            break;
        }
        tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(20)))
            .await
            .map_err(|_| "pause observer stopped")?;
    }
    ensure(
        io.join("entered").is_file(),
        "canonical native read did not enter provider",
    )?;
    task.abort();
    let _ = task.await;
    for _ in 0..100 {
        if io.join("cancelled").is_file() {
            break;
        }
        tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(20)))
            .await
            .map_err(|_| "cancel observer stopped")?;
    }
    ensure(
        io.join("cancelled").is_file(),
        "native canonical cancellation did not reach provider",
    )?;
    std::fs::write(io.join("mode"), "grant").map_err(|_| "fixture mode failed")?;
    let restored = consent_read(
        app,
        connection,
        GitHubRead::CanonicalRepositories { page: 1, limit: 1 },
    )
    .await?;
    ensure(
        restored["binding"] == binding && restored["repositories"]["items"][0] == page["items"][0],
        "refusal/cancellation rebound native mappings",
    )?;
    issue_checks(app, connection, &binding, &page["items"][0], false).await?;
    write_consent_evidence(
        app,
        "canonical-binding.json",
        &serde_json::json!({
            "binding":binding,"repository":page["items"][0],"declared_positive_provider":true,
            "actual_github_refusals":false,"declared_provider_refusals":2,"paging":true,"rename_reconnect":true,"cancellation":true
        }),
    )
}
async fn issue_read_journey(app: &tauri::AppHandle, connection: &str) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    std::fs::write(root.join("io/mode"), "issues-ui")
        .map_err(|_| "issue UI fixture unavailable")?;
    let script = r#"(async()=>{
      const invoke=window.__TAURI__.core.invoke;
      const assert=(ok,message)=>{if(!ok)throw Error(message);};
      const until=async(check,label)=>{const end=Date.now()+20000;for(;;){if(await check())return;assert(Date.now()<end,'issue read deadline: '+label);await new Promise(r=>setTimeout(r,25));}};
      const app=document.querySelector('soda-factory-app');
      const nav=label=>{const button=[...app.querySelectorAll('nav[aria-label="Factory"] button')].find(b=>b.textContent.trim()===label);assert(button&&!button.disabled,'navigation unavailable: '+label);button.click();};
      const report=stage=>invoke('report',{stage,error:null});
      const mode=value=>report('signin-mode-'+value);
      const marker=stage=>until(async()=>{try{await report(stage);return true;}catch{return false;}},stage);
      const detail=()=>app.querySelector('soda-factory-issue');
      const route=()=>new URLSearchParams(location.hash.slice(1));
      const link=(parent,label)=>[...parent.querySelectorAll('a')].find(a=>a.textContent===label);
      const scrollTarget=async(id)=>{await until(()=>{const node=detail()?.querySelector('[id="'+id+'"]');if(!node)return false;const box=node.getBoundingClientRect();return box.top>=-2&&box.top<innerHeight;},'scrolled '+id);};
      try{
        await until(()=>[...document.querySelector('#factory-account').options].some(o=>o.value===__CONNECTION__),'saved account');
        const accounts=document.querySelector('#factory-account');accounts.value=__CONNECTION__;accounts.dispatchEvent(new Event('change',{bubbles:true}));
        const repositories=document.querySelector('#factory-repository');
        await until(()=>!repositories.disabled&&[...repositories.options].some(o=>o.value==='9007199254740993'),'repository catalogue');
        repositories.value='9007199254740993';repositories.dispatchEvent(new Event('change',{bubbles:true}));
        await until(()=>document.querySelector('#connection-status').textContent==='Connected to owner/repo.','repository selection');
        await until(()=>app.querySelector('soda-factory-feed'),'shared feed');
        nav('Issues');await until(()=>app.querySelector('.soda-issue-title:not([disabled])'),'issue list');
        app.querySelector('.soda-issue-title').click();
        await until(()=>detail()?.querySelector('#issuecomment-81'),'detail and ordinary conversation');
        const current=detail(), comment=current.querySelector('#issuecomment-81');
        assert(current.context.issue==='7'&&route().get('view')==='issue','canonical issue route differs');
        const description=current.querySelector('article[aria-label="Issue description"]');
        assert(description.textContent.includes('Read heading')&&comment.textContent.includes('Reply heading'),'shared Markdown not rendered');
        assert(!current.querySelector('script,iframe,img,[onclick],[onerror]')&&!current.querySelector('a[href^="javascript:"]'),'unsafe Markdown DOM survived');
        assert(link(description,'Attachment')&&link(description,'File').href==='https://github.com/owner/repo/blob/main/README.md','attachment/default-branch links differ');
        assert(app.querySelector('soda-factory-work-journey button'),'Factory facts missing after primary read');
        const heading=current.querySelector('article[aria-label="Issue description"] h1');
        link(description,'Heading').click();await until(()=>route().get('fragment')===heading.id,'heading route');
        await scrollTarget(heading.id);
        assert(detail()===current&&current.querySelector('#issuecomment-81')===comment,'fragment navigation remounted the issue');
        link(description,'Comment').click();await until(()=>route().get('fragment')==='issuecomment-81','comment route');
        await scrollTarget('issuecomment-81');
        history.back();await until(()=>route().get('fragment')===heading.id,'Back to heading');await scrollTarget(heading.id);
        history.forward();await until(()=>route().get('fragment')==='issuecomment-81','Forward to comment');
        const next=[...current.querySelectorAll('nav[aria-label="Conversation pages"] button')].find(b=>b.textContent.includes('Next'));
        assert(next&&!next.disabled,'conversation continuation unavailable');next.click();
        await until(()=>current.querySelector('#issuecomment-101'),'next conversation page');
        assert(!current.querySelector('#issuecomment-81'),'conversation pages mixed');
        [...current.querySelectorAll('nav[aria-label="Conversation pages"] button')].find(b=>b.textContent.includes('Previous')).click();
        await until(()=>current.querySelector('#issuecomment-81'),'previous conversation page');
        link(current.querySelector('article[aria-label="Issue description"]'),'Other issue').click();
        await until(()=>detail()?.context?.issue==='10'&&detail()?.textContent.includes('No comments on this page.'),'internal issue link');
        history.back();await until(()=>detail()?.context?.issue==='7'&&detail()?.querySelector('#issuecomment-81'),'Back to issue');
        await scrollTarget('issuecomment-81');
        const deep=new URLSearchParams(location.hash.slice(1));deep.set('fragment',heading.id);location.hash=deep.toString();
        await until(()=>route().get('fragment')===heading.id,'deep link fragment');await scrollTarget(heading.id);
        nav('Work');await until(()=>app.querySelector('soda-factory-work-overview'),'Factory work view');
        history.back();await until(()=>detail()?.querySelector('#issuecomment-81'),'Back from Factory work');await scrollTarget(heading.id);
        await mode('issues-ui-denied');
        [...detail().querySelectorAll('button')].find(b=>b.textContent.includes('Refresh issue')).click();
        await until(()=>detail()?.textContent.includes('This issue is not available to this account.'),'access refusal');
        assert(!detail().querySelector('article')&&!app.querySelector('soda-factory-work-journey button'),'access loss retained issue or Factory facts');
        await mode('issues-ui');
        [...detail().querySelectorAll('button')].find(b=>b.textContent.includes('Refresh issue')).click();
        await until(()=>detail()?.querySelector('#issuecomment-81'),'restored read');
        await mode('issues-ui-pause');
        [...detail().querySelectorAll('button')].find(b=>b.textContent.includes('Refresh issue')).click();
        await marker('signin-entered');document.querySelector('#connection-disconnect').click();
        await marker('signin-upstream-cancelled');
        await until(()=>app.textContent.includes('Choose an account to open Factory.'),'disconnected shared app');
        assert(!detail()&&!app.querySelector('soda-factory-work-journey'),'retired view retained issue facts');
        await mode('grant');await report('signin-issue-read-complete');
      }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
    })()"#.replace("__CONNECTION__", &serde_json::to_string(connection).map_err(|_| "connection encoding failed")?);
    app.get_webview_window("main")
        .ok_or("main window missing")?
        .eval(&script)
        .map_err(|_| "issue UI observer unavailable")?;
    barrier(app, "issue-read-ready", "issue-read-complete").await
}

async fn issue_checks(
    app: &tauri::AppHandle,
    connection: &str,
    binding: &serde_json::Value,
    repo: &serde_json::Value,
    retained: bool,
) -> Result<(), String> {
    use soda_desktop_runtime::github::Read as GitHubRead;
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let io = root.join("io");
    let repository = repo["id"]
        .as_str()
        .ok_or("canonical repository missing")?
        .to_owned();
    let query = |page, state: &str| GitHubRead::CanonicalIssues {
        repository: repository.clone(),
        page,
        limit: 2,
        state: state.into(),
    };
    let first = consent_read(app, connection, query(1, "open")).await?;
    let item = &first["issues"]["items"][0];
    let expected = format!(
        "# Context\n\n<script>never run</script>\n\n{}",
        "Full Markdown survives.\n".repeat(4096)
    );
    ensure(
        first["repository"] == repository
            && first["issues"]["items"]
                .as_array()
                .is_some_and(|a| a.len() == 1)
            && first["issues"]["next"] == "2"
            && first["issues"]["complete"] == false
            && item["ref"]["repository"] == repository
            && item["ref"]["number"] == "7"
            && item["ref"]["external_id"] == "9007199254741001"
            && item["body"] == expected
            && item["author"]["external_id"] == "9007199254740997"
            && item["author"]["id"] != item["author"]["external_id"]
            && item["author"]["host"] == binding["host"]["id"]
            && item["assignees"][0]["id"] == binding["actor"]["id"]
            && item["labels"][0]["external_id"] == "9007199254740999"
            && item["created_at"] == "2026-11-02T09:00:00Z"
            && item["capabilities"].is_null(),
        "canonical issue content/participants/page scope invalid",
    )?;
    if retained {
        let proof: serde_json::Value = serde_json::from_slice(
            &std::fs::read(root.join("canonical-issues.json"))
                .map_err(|_| "issue proof missing")?,
        )
        .map_err(|_| "issue proof invalid")?;
        ensure(
            item["ref"] == proof["issue"] && item["author"] == proof["author"],
            "restart lost canonical issue/author mapping",
        )?;
        return write_consent_evidence(
            app,
            "canonical-issues-retained.json",
            &serde_json::json!({"retained_issue_scope":true,"retained_participant":true}),
        );
    }
    let empty = consent_read(app, connection, query(2, "open")).await?;
    ensure(
        empty["issues"]["items"]
            .as_array()
            .is_some_and(|a| a.is_empty())
            && empty["issues"]["next"] == "3"
            && empty["issues"]["complete"] == false,
        "PR filtering invented end-of-list",
    )?;
    let last = consent_read(app, connection, query(3, "open")).await?;
    let unknown = &last["issues"]["items"][0];
    ensure(
        last["issues"]["next"] == ""
            && last["issues"]["complete"] == true
            && unknown["ref"]["number"] == "10"
            && [
                "author",
                "assignees",
                "labels",
                "locked",
                "created_at",
                "updated_at",
                "closed_at",
                "capabilities",
            ]
            .iter()
            .all(|key| unknown[*key].is_null()),
        "canonical issue invented unknown metadata",
    )?;
    let closed = consent_read(app, connection, query(1, "closed")).await?;
    ensure(
        closed["issues"]["items"][0]["state"] == "closed",
        "closed issue filter lost",
    )?;
    let all = consent_read(app, connection, query(1, "all")).await?;
    ensure(
        all["issues"]["items"]
            .as_array()
            .is_some_and(|a| a.len() == 2)
            && all["issues"]["items"][0]["state"] == "closed"
            && all["issues"]["items"][1]["state"] == "open",
        "all issue filter lost",
    )?;
    let detail = consent_read(
        app,
        connection,
        GitHubRead::CanonicalIssue {
            repository: repository.clone(),
            number: 7,
        },
    )
    .await?;
    ensure(
        detail["issues"]["items"][0]["state"] == "closed"
            && detail["issues"]["items"][0]["locked"] == true
            && detail["issues"]["items"][0]["body"] == expected
            && detail["issues"]["next"] == "",
        "any-state issue detail lost content/lock facts",
    )?;
    for number in [8, 404] {
        let reply = native_github_read(
            app,
            connection,
            GitHubRead::CanonicalIssue {
                repository: repository.clone(),
                number,
            },
        )
        .await?;
        ensure(
            reply.status == 404 && reply.body.get("issues").is_none(),
            "PR wrapper/missing issue became ordinary issue detail",
        )?;
    }
    let reply = native_github_read(
        app,
        connection,
        GitHubRead::CanonicalIssues {
            repository: repo["external_id"].as_str().unwrap().into(),
            page: 1,
            limit: 2,
            state: "open".into(),
        },
    )
    .await?;
    ensure(
        reply.status == 404 && reply.body.get("issues").is_none(),
        "remote repository number bypassed canonical lookup",
    )?;
    let foreign: serde_json::Value = serde_json::from_slice(
        &std::fs::read(io.join("foreign-repository.json"))
            .map_err(|_| "foreign fixture mapping missing")?,
    )
    .map_err(|_| "foreign fixture mapping invalid")?;
    let reply = native_github_read(
        app,
        connection,
        GitHubRead::CanonicalIssues {
            repository: foreign["id"]
                .as_str()
                .ok_or("foreign repository ID missing")?
                .into(),
            page: 1,
            limit: 2,
            state: "open".into(),
        },
    )
    .await?;
    ensure(
        reply.status == 404 && reply.body.get("issues").is_none(),
        "foreign provider repository reached issue source",
    )?;
    std::fs::write(io.join("mode"), "issues-rename").map_err(|_| "fixture mode failed")?;
    let renamed = consent_read(app, connection, query(1, "open")).await?;
    ensure(
        renamed["issues"]["items"][0]["ref"] == item["ref"]
            && renamed["issues"]["items"][0]["author"]["id"] == item["author"]["id"]
            && renamed["issues"]["items"][0]["author"]["login"] == "renamed-author"
            && renamed["issues"]["items"][0]["observation"]["revision"]
                != item["observation"]["revision"],
        "repository/participant rename changed canonical issue identity",
    )?;
    for (mode, status) in [
        ("issues-denied", 403),
        ("issues-final-denied", 404),
        ("issues-name-reused", 404),
        ("issues-live-refusal", 401),
    ] {
        std::fs::write(io.join("mode"), mode).map_err(|_| "fixture mode failed")?;
        let reply = native_github_read(app, connection, query(1, "open")).await?;
        ensure(
            reply.status == status && reply.body.get("issues").is_none(),
            "failed canonical issue read released cached records",
        )?;
        if mode == "issues-live-refusal" {
            let reply = native_github_read(
                app,
                connection,
                GitHubRead::CanonicalIssue {
                    repository: repository.clone(),
                    number: 7,
                },
            )
            .await?;
            ensure(
                reply.status == 401 && reply.body.get("issues").is_none(),
                "declared provider refusal admitted canonical issue detail",
            )?;
        }
    }
    for name in ["entered", "cancelled"] {
        let _ = std::fs::remove_file(io.join(name));
    }
    std::fs::write(io.join("mode"), "issues-pause").map_err(|_| "fixture mode failed")?;
    let task_app = app.clone();
    let task_connection = connection.to_owned();
    let task_repository = repository.clone();
    let task = tauri::async_runtime::spawn(async move {
        native_github_read(
            &task_app,
            &task_connection,
            GitHubRead::CanonicalIssues {
                repository: task_repository,
                page: 1,
                limit: 2,
                state: "open".into(),
            },
        )
        .await
    });
    for _ in 0..100 {
        if io.join("entered").is_file() {
            break;
        }
        tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(20)))
            .await
            .map_err(|_| "issue pause observer stopped")?;
    }
    ensure(
        io.join("entered").is_file(),
        "canonical issue read never entered provider",
    )?;
    task.abort();
    let _ = task.await;
    for _ in 0..100 {
        if io.join("cancelled").is_file() {
            break;
        }
        tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(20)))
            .await
            .map_err(|_| "issue cancel observer stopped")?;
    }
    ensure(
        io.join("cancelled").is_file(),
        "canonical issue cancellation did not reach provider",
    )?;
    std::fs::write(io.join("mode"), "grant").map_err(|_| "fixture mode failed")?;
    let restored = consent_read(app, connection, query(1, "open")).await?;
    ensure(
        restored["issues"]["items"][0]["ref"] == item["ref"]
            && restored["issues"]["items"][0]["author"] == item["author"],
        "refusal/rename/cancel rebound issue participant",
    )?;
    write_consent_evidence(
        app,
        "canonical-issues.json",
        &serde_json::json!({
            "issue":item["ref"],"author":item["author"],"full_markdown_bytes":expected.len(),
            "declared_positive_provider":true,"actual_github_refusals":false,"declared_provider_refusals":2,"filtered_empty_continuation":true,
            "nullable_facts":true,"unknown_mutation_capabilities":true,"rename_stable":true,"cancellation":true
        }),
    )
}
async fn consent_checks(app: &tauri::AppHandle) -> Result<(), String> {
    if app.state::<Desktop>().check.scenario.revocation_cleanup {
        return revocation_cleanup(app).await;
    }
    use soda_desktop_runtime::github::Read as GitHubRead;
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    let accounts = store.status().await.map_err(|e| e.to_string())?;
    ensure(accounts.len() == 1, "approved account not uniquely stored")?;
    let account = &accounts[0];
    ensure(
        account.provider == Provider::Github
            && account.origin == "https://github.com"
            && matches!(account.state, CredentialState::Stored)
            && account.label.is_some(),
        "approved account metadata incomplete",
    )?;
    let grant = account
        .grant
        .as_ref()
        .ok_or("complete OAuth grant not stored")?;
    let namespace = state
        .status
        .lock()
        .unwrap()
        .profile
        .clone()
        .ok_or("profile missing")?;
    let reopened =
        Credentials::open(&state.paths.profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        reopened
            .status()
            .await
            .map_err(|e| e.to_string())?
            .iter()
            .any(|a| {
                a.connection == account.connection
                    && a.account == account.account
                    && a.grant.is_some()
            }),
        "encrypted account reload failed",
    )?;
    let identity = consent_read(app, &account.connection, GitHubRead::Identity).await?;
    ensure(
        identity["account"]["external_id"] == account.account,
        "remote identity mismatch",
    )?;
    workflow_evidence_check(app, &account.connection).await?;
    product_bridge_check(app, &account.connection).await?;
    let canonical_evidence: serde_json::Value = serde_json::from_slice(
        &std::fs::read(state.paths.profile.parent().ok_or("fixture parent missing")?
            .join("canonical-workflow-evidence.json"))
            .map_err(|_| "canonical evidence missing")?,
    ).map_err(|_| "canonical evidence invalid")?;
    let canonical_protection_read = canonical_evidence["actual_canonical_protection_read"]
        .as_bool().ok_or("canonical protection evidence missing")?;
    let mut page = 1;
    let mut pages = 0;
    let mut private = None;
    loop {
        let response = consent_read(
            app,
            &account.connection,
            GitHubRead::Repositories { page, limit: 10 },
        )
        .await?;
        let items = response["items"]
            .as_array()
            .ok_or("repository page missing")?;
        ensure(items.len() <= 10, "repository page unbounded")?;
        pages += 1;
        for repo in items {
            if repo["private"] == true {
                private = repo["external_id"].as_str().map(str::to_owned);
            }
        }
        let next = response["next_page"].as_u64().ok_or("pagination missing")?;
        if next == 0 || pages >= 10 || private.is_some() && pages >= 2 {
            break;
        }
        ensure(
            next > u64::from(page) && next <= u64::from(u32::MAX),
            "invalid next page",
        )?;
        page = next as u32;
    }
    let mut private_read = false;
    if let Some(repository) = private {
        let result = consent_read(
            app,
            &account.connection,
            GitHubRead::Repository { repository },
        )
        .await?;
        ensure(
            result["repository"]["private"] == true,
            "private repository detail mismatch",
        )?;
        private_read = true;
    }
    let refresh_available = grant.refresh_available;
    if refresh_available {
        let cap = store
            .capability(&account.connection)
            .await
            .map_err(|e| e.to_string())?;
        let refresh = state
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .ok_or("runtime missing")?
            .github_refresh(&store, &cap);
        let rotated = refresh.await.map_err(|e| e.to_string())?;
        ensure(
            rotated.connection == account.connection && rotated.account == account.account,
            "provider refresh changed identity",
        )?;
        ensure(
            store.with_token(&cap, |_| ()).await.is_err(),
            "old local revision survived refresh",
        )?;
        let identity = consent_read(app, &account.connection, GitHubRead::Identity).await?;
        ensure(
            identity["account"]["external_id"] == account.account,
            "post-refresh identity mismatch",
        )?;
    }
    let revocation = app.state::<Desktop>().check.scenario.revocation;
    if revocation {
        provider_revocation_check(app, &account.connection, refresh_available).await?;
    }
    let cap = store
        .capability(&account.connection)
        .await
        .map_err(|e| e.to_string())?;
    let retain = app.state::<Desktop>().check.scenario.retain_account;
    if !retain {
        store
            .withdraw(&account.connection)
            .await
            .map_err(|e| e.to_string())?;
        ensure(
            store.status().await.map_err(|e| e.to_string())?.is_empty(),
            "owned test account not removed",
        )?;
        ensure(
            store.with_token(&cap, |_| ()).await.is_err(),
            "withdrawn local capability survived",
        )?;
    }
    write_consent_evidence(
        app,
        "consent-result.json",
        &serde_json::json!({
            "approved_native_signin":true, "verified_identity":true, "encrypted_grant_reload":true,
            "repository_pages":pages, "private_repository_read":private_read,
            "refresh_issued":refresh_available, "real_refresh_exercised":refresh_available,
            "owned_local_account_removed":!retain, "owned_account_retained_for_followup":retain, "remote_revocation_exercised":revocation,
            "canonical_factory_binding":true, "native_product_bridge":true,
            "connected_shared_ui":true,"canonical_job_read":true,
            "canonical_job_continuation":true,"canonical_protection_read":canonical_protection_read
        }),
    )
}

// Only withdraw the owned failed-check account. Never reopen or replay its
// uncertain refresh pair, and never turn cleanup into a provider-pass result.
async fn revocation_cleanup(app: &tauri::AppHandle) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let ready: serde_json::Value = serde_json::from_slice(
        &std::fs::read(root.join("provider-revocation-ready.json"))
            .map_err(|_| "revocation ready evidence missing")?,
    )
    .map_err(|_| "revocation ready evidence invalid")?;
    ensure(
        ready["positive_native_identity"] == true
            && ready["positive_canonical_repository"] == true
            && state
                .paths
                .profile
                .join("provider-revocation-approved")
                .is_file()
            && state
                .paths
                .profile
                .join("provider-revocation-ui-refused")
                .is_file(),
        "owned failed revocation-check evidence missing",
    )?;
    let store = state.credentials()?;
    let accounts = store.status().await.map_err(|e| e.to_string())?;
    ensure(accounts.len() == 1, "cleanup account not uniquely stored")?;
    let account = &accounts[0];
    ensure(
        account.provider == Provider::Github
            && account.origin == "https://github.com"
            && matches!(account.state, CredentialState::RefreshPending),
        "cleanup requires the fenced GitHub fixture account",
    )?;
    let cap = store
        .capability(&account.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store.with_token(&cap, |_| ()).await.is_err(),
        "pending refresh admitted access",
    )?;
    store
        .withdraw(&account.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store.status().await.map_err(|e| e.to_string())?.is_empty(),
        "cleanup left an account",
    )?;
    ensure(
        store.with_token(&cap, |_| ()).await.is_err(),
        "withdrawn capability survived cleanup",
    )?;
    write_consent_evidence(
        app,
        "provider-revocation-cleanup.json",
        &serde_json::json!({
            "owned_pending_account_removed":true,"pending_access_refused":true,
            "withdrawn_capability_refused":true,"refresh_replayed":false,
            "provider_refresh_outcome_unverified":true,"full_provider_gate_complete":false
        }),
    )
}

async fn revocation_product(
    app: &tauri::AppHandle,
    connection: &str,
    path: &str,
) -> Result<Reply, String> {
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    let cap = store
        .capability(connection)
        .await
        .map_err(|e| e.to_string())?;
    let request = state
        .runtime
        .lock()
        .unwrap()
        .as_ref()
        .ok_or("runtime missing")?
        .admit_product(store, cap, Some("724712".into()))?;
    let response = request.request("GET", path, &[]).await?;
    Ok(Reply {
        status: response.status,
        body: serde_json::from_slice(&response.body).map_err(|_| "invalid product reply")?,
    })
}

// The owner revokes the application grant in GitHub settings. This observer
// never accepts an app secret or substitutes local withdrawal for remote proof.
async fn provider_revocation_check(
    app: &tauri::AppHandle,
    connection: &str,
    refresh_available: bool,
) -> Result<(), String> {
    use soda_desktop_runtime::github::{Error as GitHubError, Read};
    consent_read(app, connection, Read::Identity).await?;
    let catalog =
        revocation_product(app, connection, "/api/factory/collaboration/repositories").await?;
    ensure(
        catalog.status == 200
            && catalog.body["items"]
                .as_array()
                .is_some_and(|v| v.len() == 1)
            && catalog.body["items"][0]["external_id"] == "724712",
        "pre-revocation canonical repository unavailable",
    )?;
    let repository = catalog.body["items"][0]["id"]
        .as_str()
        .ok_or("canonical revocation repository missing")?
        .to_owned();
    let review = format!(
        "https://github.com/settings/connections/applications/{}",
        soda_desktop_runtime::github_provider::OAUTH_CLIENT_ID
    );
    write_consent_evidence(
        app,
        "provider-revocation-ready.json",
        &serde_json::json!({"positive_native_identity":true,"positive_canonical_repository":true,"review_url":review,"manual_app_grant_revocation_pending":true,"affects_all_tokens_for_this_app_and_user":true}),
    )?;
    barrier(
        app,
        "provider-revocation-ready",
        "provider-revocation-approved",
    )
    .await?;
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    ensure(
        store
            .status()
            .await
            .map_err(|e| e.to_string())?
            .iter()
            .any(|a| a.connection == connection && matches!(a.state, CredentialState::Stored)),
        "grant was removed locally before remote observation",
    )?;
    let refused = native_github_read(app, connection, Read::Identity).await?;
    ensure(
        refused.status == 401 && refused.body.get("account").is_none(),
        "GitHub did not refuse revoked access",
    )?;
    for path in [
        "/api/factory/identity".to_owned(),
        "/api/factory/collaboration/repositories".to_owned(),
        format!(
            "/api/factory/collaboration/repositories/{repository}/pulls/165146/checks/jobs?limit=1"
        ),
    ] {
        let refused = revocation_product(app, connection, &path).await?;
        ensure(
            (400..=599).contains(&refused.status)
                && refused.body.get("actor").is_none()
                && refused.body.get("items").is_none()
                && refused.body.get("checks").is_none(),
            "revoked grant released canonical cached facts",
        )?;
    }
    let script=r#"(async()=>{const invoke=window.__TAURI__.core.invoke;try{const session=await invoke('factory_prepare_connection',{connection:__CONNECTION__,repository:'724712'});let refused=false;try{await invoke('factory_connect',{session});}catch{refused=true;}if(!refused)throw Error('revoked account connected through WebKit');await invoke('factory_disconnect');await invoke('report',{stage:'signin-revocation-product-refused',error:null});}catch(error){await invoke('report',{stage:'failed',error:String(error)});}})()"#.replace("__CONNECTION__",&serde_json::to_string(connection).map_err(|_|"connection encoding failed")?);
    app.get_webview_window("main")
        .ok_or("main window missing")?
        .eval(&script)
        .map_err(|_| "revocation UI unavailable")?;
    barrier(
        app,
        "provider-revocation-ui-ready",
        "provider-revocation-ui-refused",
    )
    .await?;
    write_consent_evidence(
        app,
        "provider-revocation-access-result.json",
        &serde_json::json!({
            "manual_grant_revocation_confirmed":true,"actual_identity_401":true,
            "canonical_reads_refused":true,"actual_webkit_connect_refused":true,
            "refresh_outcome_pending":refresh_available,"local_removal_pending":true
        }),
    )?;
    let mut refresh_refusal = None;
    if refresh_available {
        let cap = store
            .capability(connection)
            .await
            .map_err(|e| e.to_string())?;
        let refresh = state
            .runtime
            .lock()
            .unwrap()
            .as_ref()
            .ok_or("runtime missing")?
            .github_refresh(&store, &cap);
        let outcome = refresh.await;
        let class = match &outcome {
            Err(GitHubError::Provider(401)) => "sign_in_required",
            Err(GitHubError::RefreshClientRejected) => "client_credentials_rejected",
            Err(GitHubError::Provider(_)) => "provider_unconfirmed",
            Err(GitHubError::Credential(_)) => "custody_refused",
            Err(GitHubError::Unavailable) => "unavailable",
            Err(GitHubError::Invalid | GitHubError::Protocol) => "invalid_or_protocol",
            Ok(_) => "unexpected_success",
        };
        write_consent_evidence(
            app,
            "provider-revocation-refresh-result.json",
            &serde_json::json!({
                "safe_outcome_class":class,"refresh_replayed":false,
                "revoked_refresh_token_error":class=="sign_in_required",
                "client_credentials_rejected":class=="client_credentials_rejected"
            }),
        )?;
        ensure(
            matches!(
                outcome,
                Err(GitHubError::Provider(401) | GitHubError::RefreshClientRejected)
            ),
            "GitHub revoked-refresh outcome was unconfirmed",
        )?;
        refresh_refusal = Some(class);
        ensure(
            store
                .status()
                .await
                .map_err(|e| e.to_string())?
                .iter()
                .any(|a| {
                    a.connection == connection && matches!(a.state, CredentialState::RefreshPending)
                }),
            "failed revoked refresh lost its durable fence",
        )?;
    }
    write_consent_evidence(
        app,
        "provider-revocation-result.json",
        &serde_json::json!({"positive_native_and_canonical_before":true,"manual_grant_revocation_confirmed":true,"actual_identity_401":true,"canonical_reads_refused":true,"actual_webkit_connect_refused":true,"refresh_issued":refresh_available,"post_revocation_refresh_refused":refresh_available,"refresh_refusal_class":refresh_refusal,"local_removal_remains_separate":true}),
    )
}

// Extend the existing real native journey. These are authenticated public
// provider reads, not a substitute for the later canonical Go consumer.
async fn workflow_evidence_check(app: &tauri::AppHandle, connection: &str) -> Result<(), String> {
    use soda_desktop_runtime::github::Read as Query;
    let repository = consent_read(
        app,
        connection,
        Query::FindRepository {
            name: "cli/cli".into(),
        },
    )
    .await?;
    let repository = repository["repository"]["external_id"]
        .as_str()
        .ok_or("workflow repository identity missing")?
        .to_owned();
    let mut observed_jobs = 0usize;
    let mut pages = 0usize;
    for page in 1..=3 {
        let runs = consent_read(
            app,
            connection,
            Query::WorkflowRuns {
                repository: repository.clone(),
                head: String::new(),
                page,
                limit: 2,
            },
        )
        .await?;
        let runs = &runs["workflow_runs"];
        ensure(
            runs["repository"] == repository,
            "workflow repository changed",
        )?;
        let items = runs["items"].as_array().ok_or("workflow page missing")?;
        ensure(items.len() <= 2, "workflow page exceeded request")?;
        pages += 1;
        for run in items {
            let id = run["external_id"]
                .as_str()
                .ok_or("workflow run id missing")?;
            let attempt = run["attempt"].as_str().ok_or("workflow attempt missing")?;
            let head = run["head"].as_str().ok_or("workflow head missing")?;
            let result = native_github_read(
                app,
                connection,
                Query::WorkflowRunJobs {
                    repository: repository.clone(),
                    run_id: id.into(),
                    attempt: attempt.into(),
                    head: head.into(),
                    page: 1,
                    limit: 2,
                },
            )
            .await?;
            // An actively changing run is an observed refusal; inspect another
            // real run rather than combining mutable metadata with its jobs.
            if result.status == 409 {
                continue;
            }
            if result.status != 200 {
                return Err(format!(
                    "workflow jobs unavailable: {} {}",
                    result.status,
                    result.body["error"]
                        .as_str()
                        .unwrap_or("invalid_native_reply")
                ));
            }
            let result = &result.body["jobs"];
            ensure(
                result["run"]["external_id"] == id
                    && result["run"]["attempt"] == attempt
                    && result["run"]["head"] == head,
                "workflow job run/attempt/head changed",
            )?;
            let jobs = result["items"].as_array().ok_or("job page missing")?;
            ensure(jobs.len() <= 2, "job page exceeded request")?;
            for job in jobs {
                ensure(
                    job["run_id"] == id && job["attempt"] == attempt && job["head"] == head,
                    "job escaped captured run/attempt/head",
                )?;
            }
            observed_jobs += jobs.len();
            let changed = native_github_read(
                app,
                connection,
                Query::WorkflowRunJobs {
                    repository: repository.clone(),
                    run_id: id.into(),
                    attempt: attempt.into(),
                    head: "1".repeat(40),
                    page: 1,
                    limit: 2,
                },
            )
            .await?;
            ensure(changed.status == 409, "changed workflow head admitted")?;
            if observed_jobs > 0 {
                break;
            }
        }
        if observed_jobs > 0 || runs["next_page"] == 0 {
            break;
        }
    }
    ensure(observed_jobs > 0, "no actual workflow job observed")?;
    let protection = consent_read(
        app,
        connection,
        Query::PullProtection {
            repository: "724712".into(),
            number: 165146,
            head: String::new(),
        },
    )
    .await?;
    let facts = &protection["protection"];
    ensure(
        facts["repository"] == "724712"
            && facts["pull"]["number"] == 165146
            && facts["head"].is_string()
            && facts["base"].is_string()
            && facts["branch"].is_string(),
        "protection lost pull/head/base binding",
    )?;
    let changed = native_github_read(
        app,
        connection,
        Query::PullProtection {
            repository: "724712".into(),
            number: 165146,
            head: "1".repeat(40),
        },
    )
    .await?;
    ensure(changed.status == 409, "changed protection head admitted")?;
    write_consent_evidence(
        app,
        "native-workflow-evidence.json",
        &serde_json::json!({
            "actual_authenticated_reads":true,"workflow_pages":pages,"jobs_observed":observed_jobs,
            "exact_run_attempt_head":true,"changed_head_refused":true,"pull_base_branch_bound":true,
            "classic_visible":!facts["classic"].is_null(),"active_rules_visible":!facts["rules"].is_null(),
            "canonical_consumer_qualified":false,"merge_gate_qualified":false
        }),
    )?;
    println!("PASS: actual native workflow/job and branch-rule reads retain run/attempt/head/base scope; changed heads refuse; canonical consumer remains separate");
    Ok(())
}

async fn product_bridge_check(app: &tauri::AppHandle, connection: &str) -> Result<(), String> {
    // Exercise the production commands through real WebKit IPC. Provider reads
    // are real and read-only; these ports do not mount a second product view.
    let script = r#"(async()=>{
      const invoke=window.__TAURI__.core.invoke, assert=(ok,m)=>{if(!ok)throw Error(m)};
      const connect=async(connection,repository)=>{const session=await invoke('factory_prepare_connection',{connection,repository});return invoke('factory_connect',{session});};
      const request=(session,path)=>new Promise((resolve,reject)=>{
        const channel=new window.__TAURI__.core.Channel();
        channel.onmessage=value=>value.error?reject(Error(value.error)):resolve(value.reply);
        invoke('factory_product_request',{session,request:{kind:'read',path},channel}).catch(reject);
      });
      try {
        const account=await connect(__CONNECTION__,null);
        assert(account.repository===null&&account.identity.actor&&account.identity.host,'account connection missing');
        const first=await invoke('factory_repositories',{session:account.session,page:1});
        assert(first.status===200&&Array.isArray(first.body.items),'native repository discovery unavailable');
        if(first.body.next_page) assert((await invoke('factory_repositories',{session:account.session,page:first.body.next_page})).status===200,'next discovery page unavailable');
        const located=await invoke('factory_find_repository',{session:account.session,name:'cli/cli'});
        assert(located.status===200&&located.body.repository.external_id,'job repository lookup unavailable');
        const jobScope=await connect(__CONNECTION__,located.body.repository.external_id);
        let jobObserved=false,jobPaged=false;
        for(const number of [14927,14926,14942]){
          const target='/api/factory/collaboration/repositories/'+jobScope.repository.id+'/pulls/'+number+'/checks/jobs';
          const reply=await request(jobScope.session,target+'?limit=1');
          if(reply.status===409)continue;
          assert(reply.status===200,'canonical jobs unavailable: '+reply.status);
          const body=reply.body;assert(body.kind==='job'&&body.pull.repository===jobScope.repository.id&&body.pull.number===String(number)&&body.commit&&body.status_gate==='unknown','canonical job scope/gate differs');
          assert(body.checks.items.length<=1&&body.checks.complete===(body.checks.next===''),'canonical job page differs');
          for(const job of body.checks.items){assert(job.kind==='job'&&job.commit===body.commit&&job.repository===jobScope.repository.id&&job.required===null&&job.job.run_id&&job.job.attempt&&job.job.job_id&&job.job.workflow_commit===''&&job.job.need_approval===null,'canonical job invented authority or lost identity');}
          assert((await request(jobScope.session,target+'?limit=1&head='+'1'.repeat(40))).status===409,'changed canonical job head admitted');
          assert((await request(jobScope.session,target+'?limit=1&head='+body.commit+'&cursor=invalid')).status===400,'invalid job cursor admitted');
          if(body.checks.items.length){
            jobObserved=true;
            if(body.checks.next){
              const next=await request(jobScope.session,target+'?limit=1&head='+body.commit+'&cursor='+encodeURIComponent(body.checks.next));
              assert(next.status===200&&next.body.kind==='job'&&next.body.commit===body.commit,'job continuation unavailable or changed head');
              assert(next.body.checks.next!==body.checks.next,'job continuation did not advance');
              if(next.body.checks.items.length)assert(next.body.checks.items[0].external_id!==body.checks.items[0].external_id,'job continuation repeated the same job');
              jobPaged=true;
            }
            if(jobPaged)break;
          }
        }
        assert(jobObserved,'no actual canonical workflow job observed');
        assert(jobPaged,'no actual canonical job continuation observed');
        const selected=await connect(__CONNECTION__,'724712');
        assert(selected.session!==account.session&&selected.identity.actor===account.identity.actor&&selected.identity.host===account.identity.host,'connection switch changed account identity');
        assert(selected.repository.external_id==='724712'&&selected.repository.host===selected.identity.host,'canonical selected repository missing');
        let refused=false;try{await invoke('factory_repositories',{session:account.session,page:1})}catch{refused=true}assert(refused,'stale selection admitted discovery');
        const root='/api/factory/collaboration/repositories/'+selected.repository.id;
        const identity=await request(selected.session,'/api/factory/identity');
        assert(identity.status===200&&identity.body.actor===selected.identity.actor,'bound product identity mismatch');
        assert((await request(selected.session,'/api/factory/feed?scope=project&repositories='+selected.repository.id+'&limit=1')).status===200,'bound feed unavailable');
        assert((await request(selected.session,root+'/issues?state=all&limit=1')).status===200,'bound issue reads unavailable');
        assert((await request(selected.session,root+'/pulls/165146')).status===200,'bound PR reads unavailable');
        const statuses=await request(selected.session,root+'/pulls/165146/checks?limit=1');
        const changedProtection=__SESSION_LIFECYCLE__&&statuses.status===409&&statuses.body.error?.code==='check_changed';
        if(!changedProtection){
        assert(statuses.status===200,'canonical protection unavailable: '+statuses.status);
        const policy=statuses.body.protection;
        assert(statuses.body.kind==='status'&&statuses.body.status_gate==='unknown'&&policy&&policy.repository===selected.repository.id&&policy.branch&&policy.observation.state==='available','canonical protection scope differs');
        assert(typeof policy.classic_visible==='boolean'&&typeof policy.rules_visible==='boolean'&&typeof policy.requirements_complete==='boolean','protection visibility lost');
        if(!policy.classic_visible||!policy.rules_visible)assert(policy.requirements_complete===false&&policy.required_contexts===null&&policy.required_approvals===null,'unavailable protection invented complete requirements');
        for(const required of policy.required_checks??[])assert(required.context&&['any','app','unknown'].includes(required.source)&&(required.source==='app'?/^[1-9][0-9]*$/.test(required.app):!required.app),'required check lost app binding');
        for(const status of statuses.body.checks.items)assert(status.required===null,'status read invented requiredness');
        }
        assert((await request(selected.session,root+'/pulls/165146/checks?limit=1&head='+'1'.repeat(40))).status===409,'changed canonical protection head admitted');
        await invoke('report',{stage:changedProtection?'signin-evidence-changed':'signin-evidence-observed',error:null});
        const jobs=await request(selected.session,root+'/pulls/165146/checks/jobs?limit=1');
        assert(jobs.status===200&&jobs.body.kind==='job'&&jobs.body.status_gate==='unknown','job read invented a CI gate');
        let completions=0;const channel=new window.__TAURI__.core.Channel();channel.onmessage=()=>completions++;
        const pending=await invoke('factory_product_request',{session:selected.session,request:{kind:'read',path:root+'/pulls/165146/threads?limit=1'},channel});
        await invoke('factory_product_cancel',{session:selected.session,request:pending});
        await new Promise(r=>setTimeout(r,100));assert(completions===0,'cancelled native request delivered a reply');
        await invoke('factory_disconnect');
        refused=false;try{await invoke('factory_repositories',{session:selected.session,page:1})}catch{refused=true}assert(refused,'disconnected selection admitted discovery');
        const reconnected=await connect(__CONNECTION__,'724712');
        assert(reconnected.identity.actor===selected.identity.actor&&reconnected.repository.id===selected.repository.id,'reconnect changed canonical mappings');
        await invoke('factory_disconnect');
        // Now drive the real selectors and shared components, rather than a
        // declared frontend or direct native calls standing in for composition.
        const until=async(check,label)=>{const deadline=Date.now()+40000;for(;;){if(await check())return;if(Date.now()>deadline)throw Error('Connected UI deadline: '+label);await new Promise(r=>setTimeout(r,25));}};
        const accountSelect=document.querySelector('#factory-account');
        await until(()=>[...accountSelect.options].some(o=>o.value===__CONNECTION__),'account option');
        accountSelect.value=__CONNECTION__;accountSelect.dispatchEvent(new Event('change',{bubbles:true}));
        await until(()=>!document.querySelector('#repository-name').disabled,'account context');
        await until(()=>document.querySelector('soda-intent')?.shadowRoot?.querySelector('textarea:not([disabled])'),'shared request editor');
        const outcome='Native connection keeps this request draft.';
        const editor=document.querySelector('soda-intent').shadowRoot.querySelector('textarea:not([disabled])');
        editor.value=outcome;editor.dispatchEvent(new Event('input',{bubbles:true,composed:true}));
        await until(()=>Object.keys(localStorage).some(k=>k.startsWith('soda-intent-draft:')&&localStorage.getItem(k).includes(outcome)),'request draft retained');
        const openRepository=async name=>{
          const input=document.querySelector('#repository-name');input.value=name;
          document.querySelector('#repository-find').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));
          await until(()=>document.querySelector('#connection-status').textContent==='Connected to '+name+'.','selected '+name);
        };
        await openRepository('rust-lang/rust');
        await until(()=>document.querySelector('soda-factory-feed'),'shared feed');
        const nav=label=>{const button=[...document.querySelectorAll('soda-factory-app nav[aria-label="Factory"] button')].find(b=>b.textContent.trim()===label);assert(button&&!button.disabled,'shared navigation '+label);button.click();};
        nav('Issues');
        await until(()=>document.querySelector('.soda-issue-title:not([disabled])'),'actual shared issues');
        document.querySelector('.soda-issue-title').click();
        await until(()=>document.querySelector('soda-factory-issue .soda-issue-detail h2')?.textContent!=='Issue'&&document.querySelector('soda-factory-issue .soda-issue-detail h2'),'actual shared issue detail');
        nav('New issue');
        await until(()=>document.querySelector('soda-factory-issue-create input:not([disabled])'),'shared creation draft');
        const title='Unsubmitted native issue draft';
        const writing=document.querySelector('soda-factory-issue-create input');writing.value=title;writing.dispatchEvent(new Event('input',{bubbles:true}));
        await until(()=>Object.keys(localStorage).some(k=>k.includes('issue')&&localStorage.getItem(k).includes(title)),'issue draft retained');
        await openRepository('rust-lang/cargo');
        nav('New issue');await until(()=>document.querySelector('soda-factory-issue-create input:not([disabled])'),'second repository draft');
        assert(document.querySelector('soda-factory-issue-create input').value!==title,'repository draft leaked');
        await openRepository('rust-lang/rust');nav('New issue');
        await until(()=>document.querySelector('soda-factory-issue-create input')?.value===title,'original repository draft restored');
        if(__SESSION_LIFECYCLE__){
          const repositorySelect=document.querySelector('#factory-repository');
          const selectedRepository=repositorySelect.value;
          const visibility=[];
          const observeVisibility=()=>visibility.push(document.hidden);
          document.addEventListener('visibilitychange',observeVisibility);
          await invoke('report',{stage:'signin-session-minimize',error:null});
          await until(async()=>{try{await invoke('report',{stage:'signin-session-minimize-restored',error:null});return true;}catch{return false;}},'native minimize/restore');
          visibility.length=0;
          await invoke('report',{stage:'signin-session-hide',error:null});
          await until(()=>!document.hidden&&visibility.includes(true)&&visibility.at(-1)===false,'native hide/restore');
          document.removeEventListener('visibilitychange',observeVisibility);
          await until(()=>document.querySelector('#connection-status').textContent==='Connected to rust-lang/rust.','restored native selection');
          assert(accountSelect.value===__CONNECTION__&&repositorySelect.value===selectedRepository,'hide/restore lost selection');
          nav('New issue');await until(()=>document.querySelector('soda-factory-issue-create input')?.value===title,'hide/restore draft');
          for(const cycle of [1,2]){
            await invoke('report',{stage:'signin-session-unavailable-'+cycle,error:null});
            await until(()=>accountSelect.disabled&&document.querySelector('#connection-reconnect').disabled&&document.querySelector('soda-factory-app').textContent.includes('Choose an account to open Factory.'),'unavailable account retires product');
            assert(accountSelect.value===__CONNECTION__&&repositorySelect.value===selectedRepository,'unknown status erased selection');
            assert(document.querySelector('#github-accounts button').disabled,'unknown status left stale account action enabled');
            if(cycle===2)document.querySelector('#connection-disconnect').click();
            await invoke('report',{stage:'signin-session-retired-'+cycle,error:null});
            await until(()=>!accountSelect.disabled,'current account status restored');
            if(cycle===1){
              await until(()=>document.querySelector('#connection-status').textContent==='Connected to rust-lang/rust.','status recovery reconnect');
              nav('New issue');await until(()=>document.querySelector('soda-factory-issue-create input')?.value===title,'status recovery draft');
            }else{
              assert(accountSelect.value===''&&document.querySelector('soda-factory-app').textContent.includes('Choose an account to open Factory.'),'explicit disconnect reconnected on recovery');
            }
          }
          await invoke('report',{stage:'signin-session-complete',error:null});
        }
        document.querySelector('#connection-disconnect').click();
        await until(()=>document.querySelector('soda-factory-app').textContent.includes('Choose an account to open Factory.'),'disconnected app');
        accountSelect.value=__CONNECTION__;accountSelect.dispatchEvent(new Event('change',{bubbles:true}));
        await until(()=>!document.querySelector('#repository-name').disabled,'account reconnect');
        nav('New request');
        await until(()=>document.querySelector('soda-intent')?.shadowRoot?.querySelector('textarea')?.value===outcome,'request draft restored');
        document.querySelector('#connection-disconnect').click();
        await invoke('report',{stage:'signin-product-complete',error:null});
      }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
    })()"#
    .replace(
        "__SESSION_LIFECYCLE__",
        if app.state::<Desktop>().paths.profile.parent().is_some_and(|root| {
            root.join("session-lifecycle-check").is_file()
        }) { "true" } else { "false" },
    )
    .replace("__CONNECTION__", &serde_json::to_string(connection).map_err(|_| "connection encoding failed")?);
    app.get_webview_window("main")
        .ok_or("main window missing")?
        .eval(&script)
        .map_err(|_| "product IPC observer unavailable")?;
    barrier(app, "product-bridge-ready", "product-bridge-complete").await
}

pub fn signin_control(app: &tauri::AppHandle, stage: &str) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let root = state
        .paths
        .profile
        .parent()
        .ok_or("fixture parent missing")?;
    let io = root.join("io");
    if let Some(mode) = stage.strip_prefix("signin-mode-") {
        let issue_mode = app.state::<Desktop>().check.scenario.issue_read
            && matches!(mode, "issues-ui" | "issues-ui-denied" | "issues-ui-pause");
        if !issue_mode
            && !matches!(
                mode,
                "grant" | "pause-wait" | "denied" | "expired" | "identity-denied"
            )
        {
            return Err("unknown declared sign-in mode".into());
        }
        for name in ["entered", "cancelled", "release"] {
            match std::fs::remove_file(io.join(name)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(_) => return Err("fixture marker cleanup failed".into()),
            }
        }
        return std::fs::write(io.join("mode"), mode).map_err(|_| "fixture mode failed".into());
    }
    if stage.starts_with("signin-session-") {
        ensure(
            root.join("session-lifecycle-check").is_file(),
            "session lifecycle driver unavailable",
        )?;
        match stage {
            "signin-session-minimize" | "signin-session-hide" => {
                let window = app
                    .get_webview_window("main")
                    .ok_or("main window missing")?;
                let minimize = stage == "signin-session-minimize";
                if minimize {
                    window.minimize().map_err(|_| "session minimize failed")?;
                } else {
                    window.hide().map_err(|_| "session hide failed")?;
                }
                let app = app.clone();
                tauri::async_runtime::spawn(async move {
                    if minimize {
                        let mut observed = false;
                        for _ in 0..80 {
                            if window.is_minimized().unwrap_or(false) {
                                observed = true;
                                break;
                            }
                            tokio::time::sleep(Duration::from_millis(25)).await;
                        }
                        if !observed {
                            eprintln!("Native minimization was not observed");
                            app.exit(1);
                            return;
                        }
                    }
                    tokio::time::sleep(Duration::from_millis(500)).await;
                    if !app.state::<Desktop>().quitting.load(Ordering::SeqCst) {
                        if open(&app, "main").is_err() {
                            app.exit(1);
                            return;
                        }
                        if minimize {
                            for _ in 0..80 {
                                if window.is_minimized().ok() == Some(false) {
                                    if write_consent_evidence(&app, "signin-session-minimize-restored", &serde_json::json!({"native_window_minimization_observed":true})).is_err() {
                                        app.exit(1);
                                    }
                                    return;
                                }
                                tokio::time::sleep(Duration::from_millis(25)).await;
                            }
                            eprintln!("Native restoration was not observed");
                            app.exit(1);
                        }
                    }
                });
                return Ok(());
            }
            "signin-session-minimize-restored" => {
                return ensure(root.join(stage).is_file(), "native minimize/restore still pending");
            }
            "signin-session-unavailable-1"
            | "signin-session-retired-1"
            | "signin-session-unavailable-2"
            | "signin-session-retired-2" => {
                return write_consent_evidence(app, stage, &serde_json::json!({"ready":true}));
            }
            "signin-session-complete" => {
                return write_consent_evidence(
                    app,
                    "session-lifecycle-result.json",
                    &serde_json::json!({
                        "actual_native_webkit_hide_restore":true,
                        "actual_native_window_minimize_restore":true,
                        "actual_keyring_service_outage_recovery":true,
                        "unknown_status_holds_product_and_preserves_selection":true,
                        "same_repository_draft_restored":true,
                        "explicit_disconnect_prevents_reconnect":true,
                        "historical_crash_cause_known":false,
                        "installed_gate_qualified":false
                    }),
                )
            }
            _ => return Err("unknown native session control".into()),
        }
    }
    match stage {
        "signin-issue-read-complete" => {
            ensure(
                app.state::<Desktop>().check.scenario.issue_read,
                "issue observer disabled",
            )?;
            write_consent_evidence(
                app,
                "issue-read-result.json",
                &serde_json::json!({
                    "actual_native_webkit":true,"declared_https_provider":true,
                    "shared_list_detail_conversation":true,"markdown_sanitized":true,
                    "internal_heading_comment_and_issue_navigation":true,"fragment_retains_view":true,
                    "history_and_deep_link":true,"conversation_paging":true,
                    "factory_facts_retired_on_denial":true,"pending_read_cancelled_on_disconnect":true,
                    "actual_github_consent_claimed":false,"installed_gate_qualified":false
                }),
            )?;
            std::fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(state.paths.profile.join("issue-read-complete"))
                .map(|_| ())
                .map_err(|_| "issue read evidence marker failed".into())
        }
        "signin-revocation-product-refused" => std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(state.paths.profile.join("provider-revocation-ui-refused"))
            .map(|_| ())
            .map_err(|_| "revocation UI evidence unavailable".into()),
        "signin-evidence-observed" | "signin-evidence-changed" => write_consent_evidence(
            app,
            "canonical-workflow-evidence.json",
            &serde_json::json!({
                "actual_canonical_job_read":true,"actual_canonical_job_continuation":true,
                "changed_job_head_refused":true,"invalid_job_cursor_refused":true,
                "actual_canonical_protection_read":stage == "signin-evidence-observed",
                "changed_protection_unavailable":stage == "signin-evidence-changed",
                "unavailable_sources_stay_unknown":true,
                "changed_protection_head_refused":true,"merge_gate_qualified":false
            }),
        ),
        "signin-binding-start" | "signin-binding-retained-start" => {
            start_binding_checks(app.clone(), stage == "signin-binding-retained-start");
            Ok(())
        }
        "signin-binding-done" => ensure(
            root.join("canonical-binding.json").is_file(),
            "canonical binding still pending",
        ),
        "signin-binding-retained-done" => ensure(
            root.join("canonical-binding-retained.json").is_file(),
            "retained canonical binding still pending",
        ),
        "signin-entered" => ensure(
            io.join("entered").is_file(),
            "declared transport not entered",
        ),
        "signin-upstream-cancelled" => ensure(
            io.join("cancelled").is_file(),
            "native cancellation not observed upstream",
        ),
        "signin-browser-opened" => ensure(
            std::fs::read_to_string(root.join("browser-url"))
                .ok()
                .as_deref()
                == Some("https://github.com/login/device\n"),
            "fixed browser handoff not observed",
        ),
        "signin-close-secondary" => app
            .webview_windows()
            .into_values()
            .find(|window| window.label() != "main")
            .ok_or_else(|| {
                format!(
                    "secondary window missing; labels={:?}",
                    app.webview_windows().keys().collect::<Vec<_>>()
                )
            })?
            .close()
            .map_err(|_| "secondary closure failed".into()),
        "signin-reopen" => {
            let window = app
                .get_webview_window("main")
                .ok_or("main window missing")?;
            window.show().map_err(|_| "main window show failed")?;
            window.set_focus().map_err(|_| "main focus failed".into())
        }
        "signin-crash" => {
            let pid = state
                .runtime
                .lock()
                .unwrap()
                .as_ref()
                .ok_or("fixture runtime missing")?
                .health()?
                .backend_process;
            let status = std::process::Command::new("/usr/bin/kill")
                .args(["-KILL", &pid.to_string()])
                .status()
                .map_err(|_| "owned backend crash failed")?;
            ensure(status.success(), "owned backend crash refused")
        }
        _ => Err("unknown native sign-in control".into()),
    }
}
pub fn start_signin(app: tauri::AppHandle, mode: check::SignInMode) {
    let Some(window) = app.get_webview_window("main") else {
        app.exit(1);
        return;
    };
    let _ = window.show();
    let _ = window.set_focus();
    let script=r#"(async()=>{
      const live=__LIVE__, consent=__CONSENT__, invoke=window.__TAURI__.core.invoke;
      const assert=(ok,message)=>{if(!ok)throw Error(message);};
      const report=stage=>invoke('report',{stage,error:null});
      const view=()=>invoke('github_sign_in_status');
      const until=async check=>{for(let i=0;i<300;i++){document.querySelector('#refresh').click();if(await check())return;await new Promise(r=>setTimeout(r,50));}throw Error('sign-in observation deadline');};
      const control=async stage=>{
        if(stage==='signin-binding-retained-done'&&__ISSUE_READ__){
          const deadline=Date.now()+120000;
          for(;;){try{await report(stage);return;}catch{}assert(Date.now()<deadline,'issue journey deadline');await new Promise(r=>setTimeout(r,50));}
        }
        await until(async()=>{try{await report(stage);return true;}catch{return false;}});
      };
      const mode=value=>report('signin-mode-'+value);
      const begin=async()=>{await until(()=>!document.querySelector('#github-sign-in').disabled);document.querySelector('#github-sign-in').click();await until(async()=>{const s=await view();return s.phase==='waiting'&&!document.querySelector('#github-prompt').hidden;});return view();};
      const cancel=async()=>{document.querySelector('#github-cancel').click();await until(async()=>(await view()).phase==='cancelled');};
      try{
        if(__WRITE_JOURNEY__){
          await begin();await report('signin-consent-prompt');
          const deadline=Date.now()+950000;
          for(;;){
            document.querySelector('#refresh').click();const state=await view();
            if(state.phase==='complete')break;
            assert(state.phase==='waiting'||state.phase==='verifying'||state.phase==='saving','Factory sign-in failed: '+String(state.error));
            assert(Date.now()<deadline,'Factory consent deadline');
            await new Promise(resolve=>setTimeout(resolve,250));
          }
          await report('signin-consent-complete');return;
        }
        if(consent&&(await invoke('credential_status')).length===1){await report('signin-consent-complete');return;}
        if(!live)await mode('pause-wait');
        const first=await begin();
        assert(Object.keys(first).sort().join(',')==='attempt,error,phase,prompt','sign-in state exposes extra data');
        assert(Object.keys(first.prompt).sort().join(',')==='expires_at,user_code,verification_uri','prompt exposes native authority');
        assert(first.prompt.verification_uri==='https://github.com/login/device'&&document.querySelector('#github-user-code').textContent===first.prompt.user_code,'real native prompt not rendered');
        assert(!/device_code|access_token|refresh_token/.test(JSON.stringify(first)),'prompt leaked a secret field');
        let refused=false;try{await invoke('github_sign_in');}catch{refused=true;}assert(refused,'duplicate native sign-in replaced the active attempt');
        refused=false;try{await invoke('plugin:opener|open_url',{url:'file:///etc/passwd'});}catch{refused=true;}assert(refused,'generic browser opener authority admitted');
        if(!consent){document.querySelector('#github-open').click();if(!live)await control('signin-browser-opened');}
        await report('signin-close-secondary');assert((await view()).phase==='waiting','closing another window cancelled owner sign-in');
        if(!live)await control('signin-entered');
        if(consent){
          await report('signin-consent-prompt');
          const deadline=Date.now()+950000;
          for(;;){
            document.querySelector('#refresh').click();const s=await view();
            if(s.phase==='complete')break;
            assert(s.phase!=='failed'&&s.phase!=='cancelled'&&s.phase!=='idle','approved native sign-in failed: '+String(s.error));
            assert(Date.now()<deadline,'browser consent deadline');
            await new Promise(r=>setTimeout(r,250));
          }
          await until(()=>document.querySelector('#github-accounts').children.length===1);
          const accounts=await invoke('credential_status');assert(accounts.length===1,'approved account unavailable');
          const session=await invoke('factory_prepare_connection',{connection:accounts[0].connection,repository:null});
          let refused=false;try{await invoke('factory_product_request',{session,request:{kind:'read',path:'/api/factory/identity'},channel:new window.__TAURI__.core.Channel()});}catch(error){refused=String(error).includes('Factory account is not connected');}
          assert(refused,'OAuth login silently admitted Factory work');await invoke('factory_disconnect');
          await report('signin-consent-complete');return;
        }
        await cancel();if(!live)await control('signin-upstream-cancelled');
        assert((await invoke('credential_status')).length===0,'unapproved/cancelled sign-in saved credentials');
        if(live){await report('signin-done');return;}

        await mode('pause-wait');const second=await begin();await control('signin-entered');
        const stale=await invoke('github_sign_in_cancel',{attempt:first.attempt});assert(stale.attempt===second.attempt&&stale.phase==='waiting','stale cancellation retired newer sign-in');
        await invoke('close_window');await until(async()=>(await view()).phase==='cancelled');await control('signin-upstream-cancelled');await report('signin-reopen');
        for(const value of ['denied','expired','identity-denied']){
          await mode(value);await until(()=>!document.querySelector('#github-sign-in').disabled);document.querySelector('#github-sign-in').click();
          await until(async()=>(await view()).phase==='failed');
          assert((await invoke('credential_status')).length===0,'failed/unverified login saved credentials');
          await until(()=>document.querySelector('#github-status').textContent.includes((value==='denied'?'declined':value==='expired'?'expired':'unavailable')));
        }
        await mode('grant');await until(()=>!document.querySelector('#github-sign-in').disabled);document.querySelector('#github-sign-in').click();
        await until(async()=>(await view()).phase==='complete');
        const saved=await invoke('credential_status');assert(saved.length===1&&saved[0].account==='123'&&saved[0].label==='declared-native'&&saved[0].grant.refresh_available,'verified complete account was not stored');
        assert(!/factory-declared-(access|refresh)/.test(JSON.stringify(saved)),'saved account metadata leaked a token');
        await until(()=>document.querySelector('#github-accounts').textContent.includes('declared-native'));
        assert((await invoke('factory_query',{query:{kind:'identity'}})).status===503,'OAuth login silently admitted Factory work');
        await report('signin-binding-start');await control('signin-binding-done');
        await report('signin-crash');await until(async()=>(await invoke('runtime_status')).phase==='failed');
        await invoke('runtime_retry');await until(async()=>(await invoke('runtime_status')).phase==='ready');
        const retained=await invoke('credential_status');assert(retained.length===1&&retained[0].connection===saved[0].connection&&retained[0].label==='declared-native','runtime restart lost saved account/label');
        await report('signin-binding-retained-start');await control('signin-binding-retained-done');
        await until(()=>document.querySelector('#github-accounts').textContent.includes('declared-native'));
        document.querySelector('#github-accounts button').click();await until(async()=>(await invoke('credential_status')).length===0);
        await until(()=>document.querySelector('#github-accounts').children.length===0);

        await mode('pause-wait');await begin();await control('signin-entered');await report('signin-crash');
        await until(async()=>(await invoke('runtime_status')).phase==='failed');
        assert((await view()).phase==='idle','failed runtime retained sign-in authority');
        await invoke('runtime_retry');await until(async()=>(await invoke('runtime_status')).phase==='ready');
        assert((await invoke('credential_status')).length===0,'retired sign-in saved credentials after restart');
        await report('signin-done');
      }catch(error){await invoke('report',{stage:'failed',error:String(error)});}
    })()"#.replace("__LIVE__",if mode != check::SignInMode::Declared {"true"} else {"false"})
    .replace("__WRITE_JOURNEY__",if app.state::<Desktop>().check.scenario.github_writes {"true"} else {"false"})
    .replace("__CONSENT__",if mode == check::SignInMode::Consent {"true"} else {"false"})
    .replace("__ISSUE_READ__",if app.state::<Desktop>().check.scenario.issue_read {"true"} else {"false"});
    if mode == check::SignInMode::Consent {
        tauri::async_runtime::spawn(async move {
            let ready = match app.state::<Desktop>().credentials() {
                Ok(store) => store.storage_ready().await.is_ok(),
                Err(_) => false,
            };
            if !ready {
                eprintln!("Native approved sign-in check refused: default keyring unavailable");
                app.exit(1);
                return;
            }
            if window.eval(&script).is_err() {
                app.exit(1);
            }
        });
    } else {
        let _ = window.eval(&script);
    }
}

pub fn start(app: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        if let Err(error) = run(&app).await {
            eprintln!("Native credential check failed: {error}");
            app.exit(1);
        } else {
            check::credentials_done(&app);
        }
    });
}
fn fixture() -> Vec<u8> {
    let mut random = [0u8; 32];
    std::fs::File::open("/dev/urandom")
        .unwrap()
        .read_exact(&mut random)
        .unwrap();
    random
        .iter()
        .flat_map(|b| format!("{b:02x}").into_bytes())
        .collect()
}
fn ensure(value: bool, label: &'static str) -> Result<(), String> {
    if value {
        Ok(())
    } else {
        Err(label.into())
    }
}
async fn barrier(app: &tauri::AppHandle, ready: &str, done: &str) -> Result<(), String> {
    let profile = app.state::<Desktop>().paths.profile.clone();
    std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(profile.join(ready))
        .map_err(|_| "credential check marker failed")?;
    println!("Credential check barrier: {ready}");
    let done = profile.join(done);
    let seconds = if ready == "provider-revocation-ready" {
        600
    } else if ready == "product-bridge-ready" {
        180
    } else if ready == "issue-read-ready" {
        120
    } else {
        15
    };
    tauri::async_runtime::spawn_blocking(move || {
        let until = std::time::Instant::now() + Duration::from_secs(seconds);
        while !done.is_file() {
            if std::time::Instant::now() >= until {
                return Err("credential check driver timed out".into());
            }
            std::thread::sleep(Duration::from_millis(25));
        }
        Ok(())
    })
    .await
    .map_err(|_| "credential driver stopped")?
}
async fn run(app: &tauri::AppHandle) -> Result<(), String> {
    let state = app.state::<Desktop>();
    let store = state.credentials()?;
    let profile = state.paths.profile.clone();
    let namespace = state
        .status
        .lock()
        .unwrap()
        .profile
        .clone()
        .ok_or("profile missing")?;
    let account = || {
        Account::new(
            Provider::Github,
            "https://github.com",
            "native-fixture:984321234",
        )
        .unwrap()
    };
    let first = fixture();
    let second = fixture();
    let retained = fixture();
    let saved = store
        .store(account(), Token::new(first.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    let cap = store
        .capability(&saved.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store
            .with_token(&cap, |bytes| bytes == first)
            .await
            .map_err(|e| e.to_string())?,
        "native token round trip failed",
    )?;
    let reopened = Credentials::open(&profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        reopened.with_token(&cap, |_| ()).await == Err(Error::Retired),
        "capability escaped owner",
    )?;
    let other_dir = profile.join("other-credential-profile");
    prepare_profile(&other_dir)?;
    let other = Credentials::open(&other_dir, "0123456789abcdef0123456789abcdef")
        .map_err(|e| e.to_string())?;
    ensure(
        other.status().await.map_err(|e| e.to_string())?.is_empty(),
        "profile namespace leaked",
    )?;
    let updated = store
        .store(account(), Token::new(second.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        updated.connection == saved.connection,
        "reauthentication duplicated connection",
    )?;
    ensure(
        store.with_token(&cap, |_| ()).await == Err(Error::Retired),
        "reauthentication preserved old capability",
    )?;
    let cap = store
        .capability(&updated.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store
            .with_token(&cap, |bytes| bytes == second)
            .await
            .map_err(|e| e.to_string())?,
        "replacement token missing",
    )?;
    let script = format!(
        r#"(async()=>{{const invoke=window.__TAURI__.core.invoke;try{{
      const status=await invoke('credential_status');if(status.length!==1||Object.keys(status[0]).sort().join(',')!=='account,connection,origin,provider,state'||status[0].state!=='stored')throw Error('credential metadata boundary failed');
      for(const command of ['credential_token','credential_store']){{let refused=false;try{{await invoke(command,{{connection:{id:?},token:'must-not-be-accepted'}})}}catch{{refused=true;}}if(!refused)throw Error('webview gained token access');}}
      const session=await invoke('factory_prepare_connection',{{connection:status[0].connection,repository:null}});
      let completions=0;const channel=new window.__TAURI__.core.Channel();channel.onmessage=()=>completions++;
      let refused=false;try{{await invoke('factory_product_request',{{session,request:{{kind:'read',path:'/api/factory/identity'}},channel}})}}catch(error){{refused=String(error).includes('Factory account is not connected');}}
      if(!refused)throw Error('stored credential alone admitted product work');
      const before=await invoke('runtime_status');await invoke('credential_withdraw',{{connection:{id:?}}});
      if((await invoke('credential_status')).length!==0||(await invoke('runtime_status')).generation<=before.generation)throw Error('withdrawal did not retire browser binding');
      refused=false;try{{await invoke('factory_product_request',{{session,request:{{kind:'read',path:'/api/factory/identity'}},channel}})}}catch(error){{refused=String(error).includes('Factory connection retired');}}
      if(!refused)throw Error('withdrawn credential retained product admission');
      await new Promise(resolve=>setTimeout(resolve,50));if(completions!==0)throw Error('withdrawn credential delivered product completion');
      await invoke('report',{{stage:'credential-withdrawn',error:null}});
    }}catch(error){{await invoke('report',{{stage:'failed',error:String(error)}});}}}})()"#,
        id = updated.connection
    );
    app.get_webview_window("main")
        .ok_or("main view missing")?
        .eval(&script)
        .map_err(|_| "credential UI check unavailable")?;
    // The webview writes this acknowledgement only through the native report hook.
    barrier(app, "credential-ui-ready", "credential-ui-withdrawn").await?;
    ensure(
        store.with_token(&cap, |_| ()).await == Err(Error::Withdrawn),
        "withdrawn token remained usable",
    )?;
    let reopened = Credentials::open(&profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        matches!(
            reopened.capability(&updated.connection).await,
            Err(Error::Withdrawn)
        ),
        "withdrawal was not durable",
    )?;
    println!("Credential native/UI isolation, rotation and withdrawal verified");
    let saved = store
        .store(account(), Token::new(second.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    let second_account = Account::new(Provider::Github, "https://github.com", "984321235").unwrap();
    let saved_retained = store
        .store(second_account, Token::new(retained.clone()).unwrap())
        .await
        .map_err(|e| e.to_string())?;
    let retained_cap = store
        .capability(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    barrier(app, "credential-lock-ready", "credential-locked").await?;
    let statuses = store.status().await.map_err(|e| e.to_string())?;
    ensure(
        statuses.len() == 2
            && statuses
                .iter()
                .all(|s| matches!(s.state, CredentialState::Locked)),
        "locked keyring appeared ready",
    )?;
    ensure(
        matches!(
            store.capability(&saved.connection).await,
            Err(Error::Locked)
        ),
        "locked keyring issued capability",
    )?;
    ensure(
        store.with_token(&retained_cap, |_| ()).await == Err(Error::Locked),
        "locked keyring exposed token",
    )?;
    ensure(
        store.withdraw(&saved.connection).await == Err(Error::Locked),
        "locked withdrawal was reported successful",
    )?;
    let reopened = Credentials::open(&profile, &namespace).map_err(|e| e.to_string())?;
    ensure(
        matches!(
            reopened.capability(&saved.connection).await,
            Err(Error::Withdrawn)
        ),
        "locked withdrawal did not persist denial",
    )?;
    barrier(app, "credential-stop-ready", "credential-service-stopped").await?;
    ensure(
        matches!(store.status().await, Err(Error::Unavailable)),
        "missing service was reported connected",
    )?;
    ensure(
        matches!(
            reopened.capability(&saved.connection).await,
            Err(Error::Withdrawn)
        ),
        "service loss restored withdrawn capability",
    )?;
    barrier(
        app,
        "credential-restart-ready",
        "credential-service-restored",
    )
    .await?;
    ensure(
        store
            .with_token(&retained_cap, |bytes| bytes == retained)
            .await
            .map_err(|e| e.to_string())?,
        "OS keyring restart lost token",
    )?;
    let check_plaintext = |path: &PathBuf| -> Result<(), String> {
        let data = std::fs::read(path).map_err(|_| "encrypted keyring fixture unavailable")?;
        for secret in [&first, &second, &retained] {
            if data.windows(secret.len()).any(|w| w == secret.as_slice()) {
                return Err("keyring persisted plaintext token".into());
            }
        }
        Ok(())
    };
    let data_dir =
        std::env::var_os("XDG_DATA_HOME").ok_or("isolated credential data root missing")?;
    check_plaintext(&PathBuf::from(data_dir).join("keyrings/login.keyring"))?;
    let expiring = store
        .capability(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    tauri::async_runtime::spawn_blocking(|| std::thread::sleep(Duration::from_millis(30_100)))
        .await
        .map_err(|_| "capability expiry observation stopped")?;
    ensure(
        store.with_token(&expiring, |_| ()).await == Err(Error::Retired),
        "expired token capability remained usable",
    )?;
    let retiring = store
        .capability(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    store
        .withdraw(&saved.connection)
        .await
        .map_err(|e| e.to_string())?;
    store
        .withdraw(&saved_retained.connection)
        .await
        .map_err(|e| e.to_string())?;
    ensure(
        store.status().await.map_err(|e| e.to_string())?.is_empty(),
        "credential fixture cleanup failed",
    )?;
    store.retire();
    ensure(
        store.with_token(&retiring, |_| ()).await == Err(Error::Retired),
        "retired owner admitted token",
    )?;
    Ok(())
}
