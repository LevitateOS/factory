//! Existing real host acceptance: actual native pages and production shared UI.
//! Fixture writes use ordinary local REST; the reader logs in through the UI.
use super::fountain_read_issues::{read, Seed};
use super::{checked, private_file, require, Browser, OwnedChild, Result};
use serde_json::{json, Value};
use std::fs;
use std::path::PathBuf;
use std::process::Command;
use std::thread;
use std::time::{Duration, Instant};

pub(super) fn run(b: &mut Browser, generation: &str, api: &str) -> Result<Value> {
    let module = PathBuf::from(
        std::env::var_os("FACTORY_CHECK_BROWSER_MODULE")
            .ok_or("explicit owned Playwright module required for --issues-browser")?,
    );
    require(
        module.is_absolute() && module.starts_with("/home") && module.is_file(),
        "owned browser module unavailable",
    )?;
    let root = b.root.clone();
    let native = "/api/v1/repos/factory-check/read-one-renamed";
    let issue = Seed { browser: b }.json(
        "POST",
        &format!("{native}/issues"),
        Some(json!({"title":"Rendered native read","body":"Preparing owned read fixture"})),
        201,
    )?;
    let number = issue["number"]
        .as_i64()
        .filter(|n| *n > 0)
        .ok_or("browser fixture issue number missing")?;
    let comment = Seed { browser: b }.json(
        "POST",
        &format!("{native}/issues/{number}/comments"),
        Some(json!({"body":"# Native comment\n\nAn actual ordinary conversation record."})),
        201,
    )?;
    let comment_id = comment["id"]
        .as_i64()
        .filter(|n| *n > 0)
        .ok_or("browser fixture comment id missing")?;
    let body=format!("# Native heading\n[Heading](#native-heading) [Comment](#issuecomment-{comment_id}) [File](README.md) [Branch](src/branch/main/README.md) [Bad](javascript:alert%281%29) ![Attachment](assets/map.png)\n\n<script>never run</script>\n\n{}", "Native read description.\n\n".repeat(80));
    Seed { browser: b }.json(
        "PATCH",
        &format!("{native}/issues/{number}"),
        Some(json!({"body":body})),
        201,
    )?;
    let password = String::from_utf8(checked(
        Command::new("openssl").args(["rand", "-hex", "20"]),
        "browser fixture password unavailable",
    )?)?
    .trim()
    .to_owned();
    let username = "factory-read-browser";
    Seed {browser:b}.json("POST", "/api/v1/admin/users", Some(json!({"username":username,"email":"factory-read-browser@example.invalid","password":password,"must_change_password":false,"send_notify":false})), 201)?;
    Seed { browser: b }.json(
        "PUT",
        &format!("{native}/collaborators/{username}"),
        Some(json!({"permission":"read"})),
        204,
    )?;
    let catalogue = read(
        b,
        &format!("{api}collaboration/repositories?limit=50"),
        generation,
        200,
    )?;
    let repository = catalogue["items"]
        .as_array()
        .ok_or("browser catalogue missing")?
        .iter()
        .find(|row| row["name"] == "factory-check/read-one-renamed")
        .and_then(|row| row["id"].as_str())
        .ok_or("browser canonical repository missing")?;
    private_file(&root.join("browser-password"), password.as_bytes())?;
    private_file(
        &root.join("browser-input.json"),
        &serde_json::to_vec(
            &json!({"module":module,"origin":b.origin,"username":username,"number":number.to_string(),"comment":comment_id.to_string(),"canonical_repository":repository}),
        )?,
    )?;
    private_file(
        &root.join("issue-browser.mjs"),
        include_bytes!("fountain_issue_browser.mjs"),
    )?;
    let mut browser = OwnedChild::spawn(
        Command::new("bun")
            .arg(root.join("issue-browser.mjs"))
            .arg(&root),
        &root.join("issue-browser.log"),
    )?;
    let until = Instant::now() + Duration::from_secs(120);
    loop {
        require(
            browser.child.try_wait()?.is_none(),
            "native issue browser stopped before revocation barrier; inspect private stage/log",
        )?;
        if root.join("issue-browser-read-ready").is_file() {
            break;
        }
        require(
            Instant::now() < until,
            "native issue browser readiness deadline",
        )?;
        thread::sleep(Duration::from_millis(25));
    }
    Seed { browser: b }.json(
        "PUT",
        &format!("{native}/collaborators/{username}"),
        Some(json!({"permission":"write"})),
        204,
    )?;
    private_file(&root.join("issue-browser-write-ready"), b"")?;
    loop {
        require(
            browser.child.try_wait()?.is_none(),
            "native issue browser stopped before write readback; inspect private stage/log",
        )?;
        if root.join("issue-browser-ready").is_file() {
            break;
        }
        require(Instant::now() < until, "native issue browser write deadline")?;
        thread::sleep(Duration::from_millis(25));
    }
    Seed { browser: b }.json(
        "DELETE",
        &format!("{native}/collaborators/{username}"),
        None,
        204,
    )?;
    private_file(&root.join("issue-browser-revoked"), b"")?;
    loop {
        if let Some(status) = browser.child.try_wait()? {
            require(
                status.success(),
                "native issue browser failed; inspect private stage/log",
            )?;
            break;
        }
        require(
            Instant::now() < until,
            "native issue browser finish deadline",
        )?;
        thread::sleep(Duration::from_millis(25));
    }
    let result: Value = serde_json::from_slice(&fs::read(root.join("issue-browser-result.json"))?)?;
    require(
        result["result"] == "pass" && result["read_only_before_grant"] == true
            && result["shared_comment_and_edit_readback"] == true
            && result["access_revocation_retired_facts"] == true,
        "native issue browser proof incomplete",
    )?;
    println!("PASS: actual Fountain shared issue UI, reader denial, comment/edit readback and access revocation");
    Ok(result)
}
