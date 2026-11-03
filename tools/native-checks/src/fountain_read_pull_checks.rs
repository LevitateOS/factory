//! Installed human reads of real local statuses, protection and Actions jobs.
use super::fountain_read_issues::{available, curl_string, login, read, Seed};
use super::{checked, form, private_file, require, Browser, Result};
use base64::{engine::general_purpose::STANDARD, Engine};
use serde_json::{json, Value};
use std::collections::BTreeSet;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::process::Command;
use std::thread;
use std::time::{Duration, Instant};

const WORKFLOW: &str = "name: Local qualification\non:\n  pull_request:\n    types: [opened, synchronize]\n  workflow_dispatch:\njobs:\n  build:\n    name: Local build\n    runs-on: factory-check-no-runner\n    steps:\n      - run: 'true'\n  lint:\n    name: Local lint\n    runs-on: factory-check-no-runner\n    steps:\n      - run: 'true'\n";

fn status(b: &mut Browser, repo: &str, head: &str, name: &str, state: &str) -> Result<Value> {
    Seed { browser: b }.json("POST", &format!("{repo}/statuses/{head}"), Some(json!({"context":name,"state":state,"description":"real local qualification status","target_url":"https://example.invalid/native-check"})), 201)
}

fn checks(b: &mut Browser, path: &str, generation: &str) -> Result<Value> {
    let first = read(b, &format!("{path}?limit=1"), generation, 200)?;
    available(&first)?;
    let head = first["commit"]
        .as_str()
        .ok_or("observed check head missing")?;
    let mut result = first.clone();
    let mut rows: Vec<Value> = vec![];
    let mut page = first.clone();
    let mut next = String::new();
    for _ in 0..20 {
        require(
            page["commit"] == head
                && page["observation"]["revision"] == first["observation"]["revision"]
                && page["kind"] == first["kind"],
            "check snapshot changed across native pages",
        )?;
        available(&page["checks"])?;
        let items = page["checks"]["items"]
            .as_array()
            .ok_or("check page items missing")?;
        require(items.len() <= 1, "check limit not honored")?;
        for row in items {
            require(
                row["external_id"].as_str().is_some_and(|v| !v.is_empty())
                    && !rows.iter().any(|r| r["external_id"] == row["external_id"])
                    && row["commit"] == head
                    && row["kind"] == page["kind"],
                "check page scope/identity repeated",
            )?;
            rows.push(row.clone());
        }
        if page["checks"]["complete"] == true {
            require(
                page["checks"]["next"] == "",
                "complete checks retained cursor",
            )?;
            result["checks"]["items"] = json!(rows);
            return Ok(result);
        }
        let cursor = page["checks"]["next"]
            .as_str()
            .ok_or("check continuation missing")?;
        require(
            !cursor.is_empty() && cursor != next,
            "check cursor did not advance",
        )?;
        next = cursor.to_owned();
        page = read(
            b,
            &format!(
                "{path}?limit=1&{}",
                form(&[("head", head), ("cursor", &next)])
            ),
            generation,
            200,
        )?;
    }
    Err("check pagination did not finish".into())
}

fn capability(
    b: &mut Browser,
    path: &str,
    generation: &str,
    action: &str,
    state: &str,
    reason: &str,
) -> Result<Value> {
    let until = Instant::now() + Duration::from_secs(20);
    loop {
        let caps = read(b, path, generation, 200)?;
        let fact = &caps["actions"][action];
        if fact["state"] == state && (reason.is_empty() || fact["reason"] == reason) {
            return Ok(caps);
        }
        require(
            Instant::now() < until,
            &format!("native {action} did not become {state}/{reason}; last fact {fact}"),
        )?;
        thread::sleep(Duration::from_millis(150));
    }
}

pub(super) fn run(b: &mut Browser, generation: &str, password: &str, api: &str) -> Result<Value> {
    private_file(
        &b.root.join("seed.curl"),
        format!("user = \"factory-check:{}\"\n", curl_string(password)).as_bytes(),
    )?;
    let repo = "/api/v1/repos/factory-check/pull-checks";
    let native = Seed { browser: b }.json("POST", "/api/v1/user/repos", Some(json!({"name":"pull-checks","private":true,"auto_init":true,"default_branch":"main","readme":"Default"})), 201)?;
    Seed { browser: b }.json("PATCH", repo, Some(json!({"has_actions":true})), 200)?;
    Seed { browser: b }.json("POST", &format!("{repo}/contents/.forgejo/workflows/qualification.yml"), Some(json!({"branch":"main","content":STANDARD.encode(WORKFLOW),"message":"local workflow without a runner"})), 201)?;
    Seed { browser: b }.json("POST", &format!("{repo}/contents/feature.txt"), Some(json!({"branch":"main","new_branch":"feature","content":STANDARD.encode(b"native check fixture\n")})), 201)?;
    let pull = Seed { browser: b }.json(
        "POST",
        &format!("{repo}/pulls"),
        Some(json!({"base":"main","head":"feature","title":"Native checks and protection"})),
        201,
    )?;
    require(pull["number"] == 1, "native PR fixture number differed")?;
    let head = pull["head"]["sha"]
        .as_str()
        .ok_or("native head missing")?
        .to_owned();
    let base = pull["base"]["sha"]
        .as_str()
        .ok_or("native base missing")?
        .to_owned();
    let catalogue = read(
        b,
        &format!("{api}collaboration/repositories?query=pull-checks&limit=1"),
        generation,
        200,
    )?;
    require(
        catalogue["complete"] == true
            && catalogue["items"].as_array().is_some_and(|v| v.len() == 1)
            && catalogue["items"][0]["external_id"]
                == native["id"]
                    .as_i64()
                    .ok_or("native repository missing")?
                    .to_string(),
        "native checks repository mapping missing",
    )?;
    let id = catalogue["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository missing")?;
    let repository = format!("{api}collaboration/repositories/{id}");
    let prefix = format!("{repository}/pulls/1");
    let check_path = format!("{prefix}/checks");
    let jobs_path = format!("{check_path}/jobs");
    let caps_path = format!("{prefix}/capabilities");
    // Automatic native workflows also publish ordinary commit statuses.
    // They remain visible here, distinct from their richer job records below.
    let initial = checks(b, &check_path, generation)?;
    require(
        initial["protection"]["protected"] == false
            && initial["protection"]["required_contexts"] == json!([])
            && initial["status_gate"] == "not_required",
        "observed unprotected status requirements lost",
    )?;
    status(b, repo, &base, "unrelated-base", "failure")?;
    status(b, repo, &head, "ci/build", "pending")?;
    status(b, repo, &head, "lint", "success")?;
    status(b, repo, &head, "optional", "failure")?;
    let unprotected = checks(b, &check_path, generation)?;
    require(
        unprotected["checks"]["items"].as_array().is_some_and(|v| {
            ["ci/build", "lint", "optional"]
                .iter()
                .all(|name| v.iter().any(|s| s["name"] == *name))
                && v.iter().all(|s| {
                    s["required"] == false && s["job"].is_null() && s["name"] != "unrelated-base"
                })
        }),
        "unprotected latest statuses leaked unrelated head or job facts",
    )?;
    let old_cursor = unprotected["checks"]["next"]
        .as_str()
        .ok_or("status cursor missing")?
        .to_owned();
    Seed { browser: b }.json("POST", &format!("{repo}/branch_protections"), Some(json!({"rule_name":"main","enable_push":true,"enable_status_check":true,"status_check_contexts":["ci/*","missing"],"required_approvals":1,"apply_to_admins":true,"block_on_rejected_reviews":true})), 201)?;
    read(
        b,
        &format!(
            "{check_path}?limit=1&{}",
            form(&[("head", &head), ("cursor", &old_cursor)])
        ),
        generation,
        409,
    )?;
    let protected = checks(b, &check_path, generation)?;
    require(
        protected["protection"]["protected"] == true
            && protected["protection"]["required_approvals"] == 1
            && protected["protection"]["apply_to_admins"] == true
            && protected["protection"]["block_rejected"] == true
            && protected["missing_required"] == json!(["missing"])
            && protected["status_gate"] == "pending",
        "native requirements or missing status gate lost",
    )?;
    let protected_rows = protected["checks"]["items"]
        .as_array()
        .ok_or("protected status rows missing")?;
    require(
        protected_rows.iter().any(|s| {
            s["name"] == "ci/build"
                && s["required"] == true
                && s["state"] == "pending"
                && s["conclusion"] == "unknown"
        }) && protected_rows
            .iter()
            .any(|s| s["name"] == "lint" && s["required"] == false && s["conclusion"] == "success"),
        "glob requiredness/latest status normalization lost",
    )?;
    capability(
        b,
        &caps_path,
        generation,
        "pull.merge",
        "denied",
        "branch_protection",
    )?;
    status(b, repo, &head, "missing", "success")?;
    let failed_status = status(b, repo, &head, "ci/build", "failure")?;
    let failed = checks(b, &check_path, generation)?;
    let pending_id = &protected_rows
        .iter()
        .find(|s| s["name"] == "ci/build")
        .ok_or("pending build status missing")?["external_id"];
    require(
        // The ordinary REST status ID is its repository-local Index, whereas
        // the native collaboration record deliberately retains the global ID.
        failed_status["status"] == "failure"
            && failed["status_gate"] == "failure"
            && failed["missing_required"] == json!([])
            && failed["checks"]["items"].as_array().is_some_and(|v| {
                v.iter().filter(|s| s["name"] == "ci/build").count() == 1
                    && v.iter().any(|s| {
                        s["name"] == "ci/build"
                            && s["external_id"] != *pending_id
                            && s["conclusion"] == "failure"
                    })
            }),
        "required failure/latest history collapse lost",
    )?;
    status(b, repo, &head, "ci/build", "success")?;
    let success = checks(b, &check_path, generation)?;
    require(
        success["status_gate"] == "success"
            && success["checks"]["items"].as_array().is_some_and(|v| {
                v.iter().any(|s| {
                    s["name"] == "optional"
                        && s["conclusion"] == "failure"
                        && s["required"] == false
                })
            }),
        "optional failure incorrectly changed required status gate",
    )?;
    // Status success alone cannot satisfy the separate native approval gate.
    capability(
        b,
        &caps_path,
        generation,
        "pull.merge",
        "denied",
        "branch_protection",
    )?;

    // Both automatic PR creation and explicit native dispatch create real jobs.
    let dispatched = Seed { browser: b }.json(
        "POST",
        &format!("{repo}/actions/workflows/qualification.yml/dispatches"),
        Some(json!({"ref":"feature","return_run_info":true})),
        201,
    )?;
    let until = Instant::now() + Duration::from_secs(30);
    let jobs = loop {
        let jobs = checks(b, &jobs_path, generation)?;
        let rows = jobs["checks"]["items"]
            .as_array()
            .ok_or("native jobs missing")?;
        if rows.len() == 4 {
            break jobs;
        }
        require(
            Instant::now() < until,
            &format!(
                "native workflow did not produce four real jobs; observed {}",
                rows.len()
            ),
        )?;
        thread::sleep(Duration::from_millis(200));
    };
    let rows = jobs["checks"]["items"]
        .as_array()
        .ok_or("native job rows missing")?;
    let run_ids: BTreeSet<_> = rows
        .iter()
        .filter_map(|r| r["job"]["run_id"].as_str())
        .collect();
    require(
        run_ids.len() == 2
            && run_ids.contains(
                dispatched["id"]
                    .as_i64()
                    .ok_or("dispatch run ID missing")?
                    .to_string()
                    .as_str(),
            )
            && rows.iter().all(|j| {
                j["required"].is_null()
                    && j["state"] == "pending"
                    && j["conclusion"] == "unknown"
                    && j["provider_state"] == "waiting"
                    && j["job"]["need_approval"] == false
                    && j["job"]["workflow"] == ".forgejo/workflows/qualification.yml"
                    && j["job"]["workflow_commit"] == head
                    && j["job"]["started_at"].is_null()
                    && j["job"]["stopped_at"].is_null()
                    && j["details_url"]
                        .as_str()
                        .is_some_and(|s| s.starts_with(&b.origin))
            }),
        "actual waiting run/job identity, workflow or unknown facts lost",
    )?;
    require(
        rows.iter()
            .any(|j| j["job"]["trigger_event"] == "pull_request")
            && rows
                .iter()
                .any(|j| j["job"]["trigger_event"] == "workflow_dispatch")
            && jobs["protection"].is_null()
            && jobs["status_gate"] == "unknown",
        "job events or separate protection facts conflated",
    )?;
    let jobs_cursor = jobs["checks"]["next"]
        .as_str()
        .ok_or("job continuation missing")?
        .to_owned();
    let cancel_number = dispatched["run_number"]
        .as_i64()
        .ok_or("dispatch run number missing")?
        .to_string();
    require(
        b.request(
            &format!("/factory-check/pull-checks/actions/runs/{cancel_number}/cancel"),
            Some(""),
            None,
        )?
        .0 == 200,
        "native run cancellation failed",
    )?;
    read(
        b,
        &format!(
            "{jobs_path}?limit=1&{}",
            form(&[("head", &head), ("cursor", &jobs_cursor)])
        ),
        generation,
        409,
    )?;
    let cancelled = checks(b, &jobs_path, generation)?;
    require(
        cancelled["checks"]["items"].as_array().is_some_and(|v| {
            v.iter()
                .filter(|j| j["state"] == "completed" && j["conclusion"] == "cancelled")
                .count()
                == 2
                && v.iter().filter(|j| j["state"] == "pending").count() == 2
        }),
        "native cancelled/waiting jobs were conflated",
    )?;
    for path in [
        format!("{check_path}?actor=1"),
        format!("{jobs_path}?limit=51"),
    ] {
        read(b, &path, generation, 400)?;
    }
    read(
        b,
        &format!("{check_path}?{}", form(&[("head", &base)])),
        generation,
        409,
    )?;
    read(
        b,
        &format!(
            "{jobs_path}?limit=1&{}",
            form(&[("head", &head), ("cursor", &old_cursor)])
        ),
        generation,
        409,
    )?;

    let reader_password = String::from_utf8(checked(
        Command::new("openssl").args(["rand", "-hex", "20"]),
        "checks reader password generation failed",
    )?)?
    .trim()
    .to_owned();
    Seed { browser: b }.json("POST", "/api/v1/admin/users", Some(json!({"username":"checks-reader","email":"checks-reader@example.invalid","password":reader_password,"must_change_password":false,"send_notify":false})), 201)?;
    let reader_root = b.root.join("checks-reader");
    fs::create_dir(&reader_root)?;
    fs::set_permissions(&reader_root, fs::Permissions::from_mode(0o700))?;
    fs::copy(b.root.join("cert.pem"), reader_root.join("cert.pem"))?;
    private_file(&reader_root.join("cookies"), b"")?;
    private_file(
        &reader_root.join("seed.curl"),
        format!(
            "user = \"checks-reader:{}\"\n",
            curl_string(&reader_password)
        )
        .as_bytes(),
    )?;
    let mut reader = Browser {
        root: reader_root,
        origin: b.origin.clone(),
        count: 0,
    };
    let reader_generation = login(&mut reader, "checks-reader", &reader_password)?;
    for path in [&check_path, &jobs_path, &caps_path] {
        read(&mut reader, path, &reader_generation, 404)?;
    }
    Seed { browser: b }.json(
        "PUT",
        &format!("{repo}/collaborators/checks-reader"),
        Some(json!({"permission":"read"})),
        204,
    )?;
    require(
        checks(&mut reader, &check_path, &reader_generation)?["status_gate"] == "success"
            && checks(&mut reader, &jobs_path, &reader_generation)?["checks"]["items"]
                .as_array()
                .is_some_and(|v| v.len() == 4),
        "real read collaborator lost statuses/jobs",
    )?;
    capability(
        &mut reader,
        &caps_path,
        &reader_generation,
        "pull.merge",
        "denied",
        "merge_permission_denied",
    )?;
    Seed { browser: b }.json("PATCH", repo, Some(json!({"has_actions":false})), 200)?;
    read(&mut reader, &jobs_path, &reader_generation, 404)?;
    let disabled = capability(
        &mut reader,
        &caps_path,
        &reader_generation,
        "checks.jobs.read",
        "unsupported",
        "",
    )?;
    require(
        disabled["actions"]["checks.read"]["state"] == "allowed"
            && checks(&mut reader, &check_path, &reader_generation)?["status_gate"] == "success",
        "Actions withdrawal also removed independent status reads",
    )?;
    Seed { browser: b }.json("PATCH", repo, Some(json!({"has_actions":true})), 200)?;
    capability(
        &mut reader,
        &caps_path,
        &reader_generation,
        "checks.jobs.read",
        "allowed",
        "",
    )?;

    // Native approval must be official for the protected branch. Grant actual
    // writer access before seeding that review; read capability is not elevated.
    Seed { browser: b }.json(
        "PUT",
        &format!("{repo}/collaborators/checks-reader"),
        Some(json!({"permission":"write"})),
        204,
    )?;
    Seed {
        browser: &mut reader,
    }
    .json(
        "POST",
        &format!("{repo}/pulls/1/reviews"),
        Some(
            json!({"event":"APPROVED","body":"native protected-branch approval","commit_id":head}),
        ),
        200,
    )?;
    capability(b, &caps_path, generation, "pull.merge", "allowed", "")?;

    // Owned local Git fault injection: remove only this fresh fixture's exact
    // hidden head ref, observe unavailable facts, then restore with Git CAS.
    let bare = fs::canonicalize(b.root.join("repositories/factory-check/pull-checks.git"))?;
    require(
        bare.starts_with(&b.root),
        "fault injection escaped owned native profile",
    )?;
    let reference = "refs/pull/1/head";
    checked(
        Command::new("git").arg("--git-dir").arg(&bare).args([
            "update-ref",
            "-d",
            reference,
            &head,
        ]),
        "owned hidden-ref removal failed",
    )?;
    read(b, &check_path, generation, 503)?;
    read(b, &jobs_path, generation, 503)?;
    let missing = read(b, &caps_path, generation, 200)?;
    require(
        missing["head"].is_null()
            && missing["actions"]["checks.read"]["state"] == "unavailable"
            && missing["actions"]["checks.jobs.read"]["state"] == "unavailable"
            && missing["actions"]["pull.comment"]["state"] == "allowed",
        "missing head became empty success or erased known comment permissions",
    )?;
    checked(
        Command::new("git").arg("--git-dir").arg(&bare).args([
            "update-ref",
            reference,
            &head,
            &"0".repeat(head.len()),
        ]),
        "owned hidden-ref restoration failed",
    )?;
    capability(b, &caps_path, generation, "pull.merge", "allowed", "")?;
    // Normal local native merge proves merged metadata; no external repository
    // or Factory publication/action route is written by this fixture.
    Seed { browser: b }.json(
        "POST",
        &format!("{repo}/pulls/1/merge"),
        Some(json!({"Do":"merge","head_commit_id":head,"delete_branch_after_merge":false})),
        200,
    )?;
    let merged = read(b, &prefix, generation, 200)?;
    require(
        merged["state"] == "merged"
            && !merged["merged_at"].is_null()
            && read(
                b,
                &format!("{repository}/pulls?state=closed&limit=1"),
                generation,
                200,
            )?["items"][0]["state"]
                == "merged",
        "actual merged native PR was flattened to closed",
    )?;
    capability(
        b,
        &caps_path,
        generation,
        "pull.merge",
        "denied",
        "pull_merged",
    )?;
    Seed { browser: b }.json(
        "DELETE",
        &format!("{repo}/collaborators/checks-reader"),
        None,
        204,
    )?;
    for path in [&check_path, &jobs_path, &caps_path] {
        read(&mut reader, path, &reader_generation, 404)?;
    }
    println!("PASS: installed native current-head statuses/protection, actual waiting/cancelled Actions jobs, separate unit access, missing-ref refusal, protected approval/merge and access revocation");
    Ok(
        json!({"result":"pass","manual_status_contexts":4,"workflow_runs":2,"jobs":4,"cancelled_jobs":2,"protection":true,"separate_actions_access":true,"missing_head":true,"native_merge":true,"access_revocation":true}),
    )
}
