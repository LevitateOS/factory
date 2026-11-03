//! Actual local Git/PR/review fixtures, read through installed human admission.
use super::fountain_read_issues::{available, curl_string, login, read, Seed};
use super::{checked, form, private_file, require, Browser, Result};
use base64::{engine::general_purpose::STANDARD, Engine};
use serde_json::{json, Value};
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::process::Command;
use std::thread;
use std::time::{Duration, Instant};

fn native_id(value: &Value) -> Result<String> {
    Ok(value["id"].as_i64().ok_or("native ID missing")?.to_string())
}

// Follow actual cursors, checking every bounded page and duplicate identity.
pub(super) fn pages(
    b: &mut Browser,
    path: &str,
    generation: &str,
    key: &str,
) -> Result<Vec<Value>> {
    let separator = if path.contains('?') { '&' } else { '?' };
    let path = format!("{path}{separator}limit=1");
    let mut next = String::new();
    let mut items = vec![];
    for _ in 0..12 {
        let suffix = if next.is_empty() {
            String::new()
        } else {
            format!("&{}", form(&[("cursor", &next)]))
        };
        let page = read(b, &format!("{path}{suffix}"), generation, 200)?;
        available(&page)?;
        let rows = page["items"].as_array().ok_or("page items missing")?;
        require(rows.len() <= 1, "native page exceeded requested limit")?;
        for row in rows {
            require(
                !row[key].is_null() && !items.iter().any(|prior: &Value| prior[key] == row[key]),
                "missing or repeated paged resource identity",
            )?;
            items.push(row.clone());
        }
        if page["complete"] == true {
            require(page["next"] == "", "complete page retained continuation")?;
            return Ok(items);
        }
        let cursor = page["next"].as_str().ok_or("page cursor missing")?;
        require(
            !cursor.is_empty() && cursor != next,
            "cursor did not advance",
        )?;
        next = cursor.to_owned();
    }
    Err("native paging did not finish".into())
}

fn file(
    b: &mut Browser,
    repo: &str,
    name: &str,
    branch: &str,
    new: bool,
    bytes: &[u8],
) -> Result<Value> {
    let mut payload = json!({"branch":branch,"content":STANDARD.encode(bytes),"message":"local PR qualification"});
    if new {
        payload["new_branch"] = json!("feature");
    }
    Seed { browser: b }.json(
        "POST",
        &format!("{repo}/contents/{name}"),
        Some(payload),
        201,
    )
}

pub(super) fn run(b: &mut Browser, generation: &str, password: &str, api: &str) -> Result<Value> {
    private_file(
        &b.root.join("seed.curl"),
        format!("user = \"factory-check:{}\"\n", curl_string(password)).as_bytes(),
    )?;
    let repo = "/api/v1/repos/factory-check/pull-code";
    let native = Seed { browser: b }.json("POST", "/api/v1/user/repos", Some(json!({"name":"pull-code","private":true,"auto_init":true,"default_branch":"main","readme":"Default"})), 201)?;
    // A preceding ordinary issue deliberately separates display number from PR ID.
    Seed { browser: b }.json(
        "POST",
        &format!("{repo}/issues"),
        Some(json!({"title":"Ordinary issue, not a PR","body":"typed identity fixture"})),
        201,
    )?;
    let alpha = file(
        b,
        repo,
        "alpha.txt",
        "main",
        true,
        b"alpha original\nsecond line\n",
    )?;
    file(
        b,
        repo,
        "beta.txt",
        "feature",
        false,
        b"beta original\nsecond line\n",
    )?;
    file(
        b,
        repo,
        "blob.bin",
        "feature",
        false,
        b"\0binary\0fixture\xff",
    )?;
    let body = format!("complete native pull body\n{}", "界".repeat(5000));
    let pull = Seed { browser: b }.json(
        "POST",
        &format!("{repo}/pulls"),
        Some(json!({"base":"main","head":"feature","title":"Native code review","body":body})),
        201,
    )?;
    require(
        pull["number"] == 2 && pull["id"] != pull["number"],
        "native PR/issue identity fixture did not separate",
    )?;
    let pull_native = native_id(&pull)?;
    let head = pull["head"]["sha"]
        .as_str()
        .ok_or("native PR head missing")?
        .to_owned();
    let base = pull["merge_base"]
        .as_str()
        .ok_or("native merge base missing")?
        .to_owned();
    for (branch, title, closed) in [
        ("draft-code", "WIP: Native draft", false),
        ("closed-code", "Native closed", true),
    ] {
        Seed { browser: b }.json("POST", &format!("{repo}/contents/{branch}.txt"), Some(json!({"branch":"main","new_branch":branch,"content":STANDARD.encode(b"other native branch\n")})), 201)?;
        let other = Seed { browser: b }.json(
            "POST",
            &format!("{repo}/pulls"),
            Some(json!({"base":"main","head":branch,"title":title})),
            201,
        )?;
        if closed {
            let number = other["number"].as_i64().ok_or("closed PR number missing")?;
            Seed { browser: b }.json(
                "PATCH",
                &format!("{repo}/pulls/{number}"),
                Some(json!({"state":"closed"})),
                201,
            )?;
        }
    }
    let repos = pages(
        b,
        &format!("{api}collaboration/repositories?query=pull-code"),
        generation,
        "id",
    )?;
    require(
        repos.len() == 1 && repos[0]["external_id"] == native_id(&native)?,
        "native PR repository mapping lost",
    )?;
    let id = repos[0]["id"]
        .as_str()
        .ok_or("canonical repository missing")?;
    let repository = format!("{api}collaboration/repositories/{id}");
    let prefix = format!("{repository}/pulls/2");
    let detail = read(b, &prefix, generation, 200)?;
    available(&detail)?;
    require(
        detail["ref"]["kind"] == "pull"
            && detail["ref"]["external_id"] == pull_native
            && detail["ref"]["number"] == "2"
            && detail["ref"]["repository"] == id
            && detail["body"] == body
            && detail["draft"] == false,
        "native typed PR/full body lost",
    )?;
    require(
        detail["head"]["repository"]["id"] == id
            && detail["head"]["name"] == "feature"
            && detail["base"]["name"] == "main"
            && detail["head"]["commit"].is_null()
            && detail["base"]["commit"].is_null(),
        "PR metadata invented commit observations",
    )?;
    let all = pages(
        b,
        &format!("{repository}/pulls?state=all"),
        generation,
        "ref",
    )?;
    require(
        all.len() == 3
            && all.iter().any(|p| p["draft"] == true)
            && all.iter().any(|p| p["state"] == "closed"),
        "PR all/draft/closed paging lost",
    )?;
    require(
        pages(b, &format!("{repository}/pulls"), generation, "ref")?.len() == 2
            && pages(
                b,
                &format!("{repository}/pulls?state=closed"),
                generation,
                "ref",
            )?
            .len()
                == 1,
        "native open/closed filters lost",
    )?;

    let first_files = read(b, &format!("{prefix}/files?limit=1"), generation, 200)?;
    require(
        first_files["diff"]["base"] == base
            && first_files["diff"]["head"] == head
            && first_files["diff"]["pull"] == detail["ref"]
            && first_files["files"]["complete"] == false,
        "actual Git pair or file continuation lost",
    )?;
    let mut files = first_files["files"]["items"]
        .as_array()
        .ok_or("file items missing")?
        .clone();
    let pair = form(&[("base", &base), ("head", &head)]);
    let original_cursor = first_files["files"]["next"]
        .as_str()
        .ok_or("file cursor missing")?
        .to_owned();
    let mut cursor = original_cursor.clone();
    for _ in 0..5 {
        let page = read(
            b,
            &format!(
                "{prefix}/files?{pair}&limit=1&{}",
                form(&[("cursor", &cursor)])
            ),
            generation,
            200,
        )?;
        require(
            page["diff"] == first_files["diff"],
            "file pair changed across continuation",
        )?;
        available(&page["files"])?;
        let rows = page["files"]["items"]
            .as_array()
            .ok_or("file continuation items missing")?;
        require(
            rows.len() == 1 && !files.iter().any(|p| p["new_path"] == rows[0]["new_path"]),
            "file page duplicated or exceeded limit",
        )?;
        files.extend_from_slice(rows);
        if page["files"]["complete"] == true {
            break;
        }
        let next = page["files"]["next"]
            .as_str()
            .ok_or("file continuation missing")?;
        require(next != cursor, "file cursor did not progress")?;
        cursor = next.to_owned();
    }
    require(
        files.len() == 3
            && files.iter().all(|f| f["diff"] == first_files["diff"])
            && files.iter().any(|f| {
                f["new_path"] == "blob.bin" && f["patch_state"] == "binary" && f["patch"].is_null()
            })
            && files.iter().any(|f| {
                f["new_path"] == "alpha.txt"
                    && f["patch_state"] == "complete"
                    && f["patch"]
                        .as_str()
                        .is_some_and(|v| v.contains("+alpha original"))
            }),
        "native text/binary file evidence lost",
    )?;

    let review_body = format!("complete native review\n{}", "評".repeat(4500));
    let inline_body = format!("complete native inline\n{}", "話".repeat(4500));
    let review = Seed { browser: b }.json("POST", &format!("{repo}/pulls/2/reviews"), Some(json!({"event":"COMMENT","body":review_body,"commit_id":head,"comments":[{"path":"alpha.txt","body":inline_body,"new_position":1},{"path":"beta.txt","body":"beta inline","new_position":1}]})), 200)?;
    let review_id = native_id(&review)?;
    let reply = Seed { browser: b }.json(
        "POST",
        &format!("{repo}/pulls/2/reviews/{review_id}/comments"),
        Some(json!({"path":"alpha.txt","body":"actual native inline reply","new_position":1})),
        200,
    )?;
    let pending = Seed { browser: b }.json("POST", &format!("{repo}/pulls/2/reviews"), Some(json!({"event":"PENDING","body":"private pending review","commit_id":head,"comments":[{"path":"beta.txt","body":"private pending inline","new_position":2}]})), 200)?;
    let pending_id = native_id(&pending)?;
    Seed { browser: b }.json(
        "POST",
        &format!("{repo}/issues/2/comments"),
        Some(json!({"body":"ordinary PR discussion, distinct from review"})),
        201,
    )?;
    let reviews = pages(b, &format!("{prefix}/reviews"), generation, "external_id")?;
    require(
        reviews.len() == 2
            && reviews
                .iter()
                .any(|r| r["state"] == "pending" && r["submitted_at"].is_null()),
        "author pending review visibility lost",
    )?;
    let submitted = read(b, &format!("{prefix}/reviews/{review_id}"), generation, 200)?;
    require(
        submitted["body"] == review_body
            && submitted["state"] == "commented"
            && submitted["commit"] == head
            && submitted["pull"] == detail["ref"]
            && !submitted["submitted_at"].is_null(),
        "complete submitted review facts lost",
    )?;
    let threads = pages(b, &format!("{prefix}/threads"), generation, "external_id")?;
    require(
        threads.len() == 3,
        "native submitted/pending thread pages lost",
    )?;
    let alpha_thread = threads
        .iter()
        .find(|t| t["anchor"]["new_path"] == "alpha.txt")
        .ok_or("alpha original anchor missing")?;
    let thread_id = alpha_thread["external_id"]
        .as_str()
        .ok_or("thread ID missing")?;
    let thread_path = format!("{prefix}/threads/{thread_id}");
    let original_anchor = alpha_thread["anchor"].clone();
    require(
        original_anchor["diff"] == first_files["diff"]
            && original_anchor["side"] == "new"
            && original_anchor["scope"] == "lines"
            && original_anchor["lines"] == json!({"start":1,"end":1})
            && alpha_thread["resolution"] == "unresolved"
            && alpha_thread["outdated"] == false
            && alpha_thread["recorded_location"]["path"] == "alpha.txt",
        "native immutable anchor/placement lost",
    )?;
    let thread_detail = read(b, &thread_path, generation, 200)?;
    require(
        thread_detail["anchor"] == original_anchor
            && thread_detail["comments"]["items"][0]["comment"]["body"] == inline_body,
        "full inline body/detail lost",
    )?;
    let comments = pages(b, &format!("{thread_path}/comments"), generation, "comment")?;
    require(
        comments.len() == 2
            && comments[1]["comment"]["external_id"] == native_id(&reply)?
            && comments[1]["comment"]["body"] == "actual native inline reply"
            && comments.iter().all(|c| c["review_id"] == review_id),
        "native thread reply paging lost",
    )?;
    let discussion = pages(b, &format!("{prefix}/comments"), generation, "comment")?;
    require(
        discussion.len() == 1
            && discussion[0]["comment"]["body"] == "ordinary PR discussion, distinct from review"
            && discussion[0]["pull"] == detail["ref"],
        "ordinary PR discussion mixed with review comments",
    )?;
    let caps = read(b, &format!("{prefix}/capabilities"), generation, 200)?;
    require(
        caps["pull"] == detail["ref"]
            && caps["issue"].is_null()
            && caps["head"] == head
            && caps["actions"]["pull.edit"]["state"] == "allowed"
            && caps["actions"]["pull.review.approve"]["state"] == "denied"
            && caps["actions"]["pull.review.comment"]["state"] == "allowed",
        "native author capability scope lost",
    )?;

    let reader_password = String::from_utf8(checked(
        Command::new("openssl").args(["rand", "-hex", "20"]),
        "PR reader password generation failed",
    )?)?
    .trim()
    .to_owned();
    Seed { browser: b }.json("POST", "/api/v1/admin/users", Some(json!({"username":"pull-reader","email":"pull-reader@example.invalid","password":reader_password,"must_change_password":false,"send_notify":false})), 201)?;
    let reader_root = b.root.join("pull-reader");
    fs::create_dir(&reader_root)?;
    fs::set_permissions(&reader_root, fs::Permissions::from_mode(0o700))?;
    fs::copy(b.root.join("cert.pem"), reader_root.join("cert.pem"))?;
    private_file(&reader_root.join("cookies"), b"")?;
    private_file(
        &reader_root.join("seed.curl"),
        format!("user = \"pull-reader:{}\"\n", curl_string(&reader_password)).as_bytes(),
    )?;
    let mut reader = Browser {
        root: reader_root,
        origin: b.origin.clone(),
        count: 0,
    };
    let reader_generation = login(&mut reader, "pull-reader", &reader_password)?;
    read(&mut reader, &prefix, &reader_generation, 404)?;
    Seed { browser: b }.json(
        "PUT",
        &format!("{repo}/collaborators/pull-reader"),
        Some(json!({"permission":"read"})),
        204,
    )?;
    require(
        read(&mut reader, &prefix, &reader_generation, 200)?["body"] == body,
        "private reader PR body changed",
    )?;
    let visible_reviews = pages(
        &mut reader,
        &format!("{prefix}/reviews"),
        &reader_generation,
        "external_id",
    )?;
    require(
        visible_reviews.len() == 1 && visible_reviews[0]["external_id"] == review_id,
        "private pending review leaked into reader list",
    )?;
    read(
        &mut reader,
        &format!("{prefix}/reviews/{pending_id}"),
        &reader_generation,
        404,
    )?;
    let visible_threads = pages(
        &mut reader,
        &format!("{prefix}/threads"),
        &reader_generation,
        "external_id",
    )?;
    require(
        visible_threads.len() == 2,
        "private pending thread leaked into reader list",
    )?;
    let private_thread = threads
        .iter()
        .find(|t| t["comments"]["items"][0]["review_id"] == pending_id)
        .ok_or("private pending thread missing")?;
    let private_id = private_thread["external_id"]
        .as_str()
        .ok_or("private thread ID missing")?;
    read(
        &mut reader,
        &format!("{prefix}/threads/{private_id}"),
        &reader_generation,
        404,
    )?;
    let reader_caps = read(
        &mut reader,
        &format!("{prefix}/capabilities"),
        &reader_generation,
        200,
    )?;
    require(
        reader_caps["actions"]["pull.edit"]["state"] == "denied"
            && reader_caps["actions"]["pull.review.approve"]["state"] == "allowed"
            && reader_caps["actions"]["pull.comment"]["state"] == "allowed",
        "native reader/author capability distinction lost",
    )?;
    let approval = Seed {
        browser: &mut reader,
    }
    .json(
        "POST",
        &format!("{repo}/pulls/2/reviews"),
        Some(json!({"event":"APPROVED","body":"actual non-author approval","commit_id":head})),
        200,
    )?;
    let approval_id = native_id(&approval)?;
    require(
        read(
            b,
            &format!("{prefix}/reviews/{approval_id}"),
            generation,
            200,
        )?["state"]
            == "approved",
        "actual reader approval was not normalized",
    )?;

    // Replace the anchored line through the native file API and wait for its
    // asynchronous hidden-PR-ref update, never supplying a fabricated Git ref.
    let moved = Seed { browser: b }.json("PUT", &format!("{repo}/contents/alpha.txt"), Some(json!({"branch":"feature","sha":alpha["content"]["sha"],"content":STANDARD.encode(b"replacement line\nsecond line\n"),"message":"replace reviewed line"})), 200)?;
    let moved_head = moved["commit"]["sha"]
        .as_str()
        .ok_or("updated native commit missing")?;
    require(moved_head != head, "head update did not move")?;
    let until = Instant::now() + Duration::from_secs(20);
    loop {
        let current = read(b, &format!("{prefix}/files?limit=1"), generation, 200)?;
        if current["diff"]["head"] == moved_head {
            break;
        }
        require(
            Instant::now() < until,
            "native hidden PR head did not advance",
        )?;
        thread::sleep(Duration::from_millis(100));
    }
    read(
        b,
        &format!(
            "{prefix}/files?{pair}&limit=1&{}",
            form(&[("cursor", &original_cursor)])
        ),
        generation,
        409,
    )?;
    let outdated = read(b, &thread_path, generation, 200)?;
    require(
        outdated["anchor"] == original_anchor
            && outdated["comments"]["items"][0]["anchor"] == original_anchor
            && outdated["outdated"] == true
            && outdated["current_anchor"].is_null(),
        "changed head overwrote original review anchor or invented relocation",
    )?;
    require(
        read(b, &format!("{prefix}/reviews/{review_id}"), generation, 200)?["commit"] == head,
        "historical review adopted current head",
    )?;
    for path in [
        format!("{prefix}?actor=1"),
        format!("{prefix}/files?limit=11"),
        format!("{prefix}/reviews/{review_id}?limit=1"),
        format!("{prefix}/threads?actor=1"),
    ] {
        read(b, &path, generation, 400)?;
    }
    Seed { browser: b }.json(
        "DELETE",
        &format!("{repo}/collaborators/pull-reader"),
        None,
        204,
    )?;
    require(
        pages(
            &mut reader,
            &format!("{api}collaboration/repositories?query=pull-code"),
            &reader_generation,
            "id",
        )?
        .is_empty(),
        "revoked PR repository stayed discoverable",
    )?;
    for suffix in [
        "",
        "/files",
        "/reviews",
        "/threads",
        "/comments",
        "/capabilities",
    ] {
        read(
            &mut reader,
            &format!("{prefix}{suffix}"),
            &reader_generation,
            404,
        )?;
    }
    println!("PASS: real private native PR metadata/files/reviews/threads/discussion, immutable anchors and changed-head refusal, pending privacy, non-author approval and access revocation");
    Ok(
        json!({"result":"pass","pulls":3,"files":3,"reviews":3,"threads":3,"full_body_runes":body.chars().count(),"pending_privacy":true,"changed_head":true,"access_revocation":true}),
    )
}
