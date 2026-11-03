//! Populated read qualification against the fresh running native provider.
//! Fixture writes use its ordinary local REST API; Factory reads use native
//! human sessions through the installed package, never a seed credential.
use super::{checked, form, private_file, require, Browser, Result};
use serde_json::{json, Value};
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::process::Command;
use std::thread;
use std::time::{Duration, Instant};

pub(super) fn curl_string(value: &str) -> String {
    value.replace('\\', "\\\\").replace('"', "\\\"")
}

pub(super) struct Seed<'a> {
    pub(super) browser: &'a mut Browser,
}
impl Seed<'_> {
    pub(super) fn json(
        &mut self,
        method: &str,
        path: &str,
        payload: Option<Value>,
        expected: u16,
    ) -> Result<Value> {
        let until = Instant::now() + Duration::from_secs(20);
        loop {
            let (status, bytes) = self.request(method, path, payload.as_ref())?;
            // This exact native reservation refusal precedes the mutation.
            // Never replay an uncertain transport error or a generic 503.
            let refused = status == 503
                && serde_json::from_slice::<Value>(&bytes)
                    .ok()
                    .is_some_and(|v| {
                        v["message"]
                            .as_str()
                            .and_then(|s| {
                                s.strip_prefix(
                                    "native mutation reservation is busy: ordinary writer ",
                                )
                            })
                            .and_then(|s| s.strip_suffix(" is fenced"))
                            .is_some_and(|family| {
                                !family.is_empty()
                                    && family.bytes().all(|c| c.is_ascii_lowercase() || c == b'-')
                            })
                    });
            if refused && Instant::now() < until {
                thread::sleep(Duration::from_millis(150));
                continue;
            }
            require(status==expected,&format!("native fixture {method} {path}: HTTP {status}, expected {expected}; private capture retained"))?;
            return if bytes.is_empty() {
                Ok(Value::Null)
            } else {
                Ok(serde_json::from_slice(&bytes)?)
            };
        }
    }
    fn request(
        &mut self,
        method: &str,
        path: &str,
        payload: Option<&Value>,
    ) -> Result<(u16, Vec<u8>)> {
        require(path.starts_with("/api/v1/"), "fixture API path required")?;
        let b = &mut self.browser;
        b.count += 1;
        let body = b.root.join(format!("seed-{}.body", b.count));
        private_file(&body, b"")?;
        let mut command = Command::new("curl");
        command
            .args(["--silent", "--show-error", "--max-time", "20", "--cacert"])
            .arg(b.root.join("cert.pem"))
            .arg("--config")
            .arg(b.root.join("seed.curl"))
            .arg("--header")
            .arg(format!("Origin: {}", b.origin))
            .args([
                "--header",
                "Sec-Fetch-Site: same-origin",
                "--request",
                method,
                "--output",
            ])
            .arg(&body)
            .args(["--write-out", "%{http_code}"]);
        if let Some(payload) = payload {
            let input = b.root.join(format!("seed-{}.json", b.count));
            private_file(&input, &serde_json::to_vec(&payload)?)?;
            command
                .args([
                    "--header",
                    "Content-Type: application/json",
                    "--data-binary",
                ])
                .arg(format!("@{}", input.display()));
        }
        command.arg(format!("{}{}", b.origin, path));
        let status = String::from_utf8(checked(
            &mut command,
            "local native fixture request failed",
        )?)?
        .parse::<u16>()?;
        let bytes = fs::read(&body)?;
        Ok((status, bytes))
    }
}

pub(super) fn read(b: &mut Browser, path: &str, generation: &str, status: u16) -> Result<Value> {
    let response = b.request(path, None, Some(generation))?;
    require(
        response.0 == status,
        &format!("shared read {path}: HTTP {}, expected {status}", response.0),
    )?;
    Ok(serde_json::from_str(&response.1)?)
}
pub(super) fn available(page: &Value) -> Result<()> {
    require(
        page["observation"]["state"] == "available"
            && page["observation"]["revision"]
                .as_str()
                .is_some_and(|v| !v.is_empty()),
        "available provider observation missing",
    )
}
fn refs(b: &mut Browser, api: &str, generation: &str) -> Result<Vec<Value>> {
    let path = format!("{api}collaboration/repositories?query=read-&limit=1");
    let mut next = String::new();
    let mut items = vec![];
    for _ in 0..10 {
        let suffix = if next.is_empty() {
            String::new()
        } else {
            format!("&{}", form(&[("cursor", &next)]))
        };
        let page = read(b, &format!("{path}{suffix}"), generation, 200)?;
        available(&page)?;
        let rows = page["items"].as_array().ok_or("repository items missing")?;
        require(rows.len() <= 1, "repository page exceeded limit")?;
        for row in rows {
            require(
                !items.iter().any(|r: &Value| r["id"] == row["id"]),
                "repository repeated across live pages",
            )?;
            items.push(row.clone());
        }
        if page["complete"] == true {
            return Ok(items);
        }
        let cursor = page["next"].as_str().ok_or("catalogue cursor missing")?;
        require(
            !cursor.is_empty() && cursor != next,
            "catalogue cursor did not advance",
        )?;
        next = cursor.to_owned();
    }
    Err("catalogue paging did not finish".into())
}
pub(super) fn login(b: &mut Browser, name: &str, password: &str) -> Result<String> {
    require(
        b.request("/user/login", None, None)?.0 == 200,
        "reader login page failed",
    )?;
    let payload = form(&[("user_name", name), ("password", password)]);
    require(
        b.request("/user/login", Some(&payload), None)?.0 == 303,
        "reader native login failed",
    )?;
    let page = b.request("/feed", None, None)?;
    require(page.0 == 200, "reader native package page failed")?;
    page.2
        .lines()
        .find_map(|l| {
            l.split_once(':')
                .filter(|(k, _)| k.eq_ignore_ascii_case("X-Extension-Session-Generation"))
                .map(|(_, v)| v.trim().to_owned())
        })
        .ok_or_else(|| "reader generation missing".into())
}

pub(super) fn run(b: &mut Browser, generation: &str, password: &str, api: &str) -> Result<Value> {
    private_file(
        &b.root.join("seed.curl"),
        format!("user = \"factory-check:{}\"\n", curl_string(password)).as_bytes(),
    )?;
    let owner = "factory-check";
    let repo_api = format!("/api/v1/repos/{owner}/read-one");
    let first=Seed{browser:b}.json("POST","/api/v1/user/repos",Some(json!({"name":"read-one","private":true,"auto_init":true,"default_branch":"main","readme":"Default"})),201)?;
    let second=Seed{browser:b}.json("POST","/api/v1/user/repos",Some(json!({"name":"read-two","private":true,"auto_init":true,"default_branch":"main","readme":"Default"})),201)?;
    let native_first = first["id"]
        .as_i64()
        .ok_or("native first repository ID missing")?
        .to_string();
    let native_second = second["id"]
        .as_i64()
        .ok_or("native second repository ID missing")?
        .to_string();
    let label = Seed { browser: b }.json(
        "POST",
        &format!("{repo_api}/labels"),
        Some(json!({"name":"native-label","color":"112233"})),
        201,
    )?;
    let full_body = format!("real native issue body\n{}", "界".repeat(5000));
    let issue=Seed{browser:b}.json("POST",&format!("{repo_api}/issues"),Some(json!({"title":"Complete native issue","body":full_body,"assignees":[owner],"labels":[label["id"]]})),201)?;
    let closed = Seed { browser: b }.json(
        "POST",
        &format!("{repo_api}/issues"),
        Some(json!({"title":"Closed native issue","body":"closed body"})),
        201,
    )?;
    Seed { browser: b }.json(
        "PATCH",
        &format!("{repo_api}/issues/2"),
        Some(json!({"state":"closed"})),
        201,
    )?;
    let other = Seed { browser: b }.json(
        "POST",
        &format!("/api/v1/repos/{owner}/read-two/issues"),
        Some(json!({"title":"Same number, other repository","body":"other repository body"})),
        201,
    )?;
    require(
        issue["number"] == 1 && closed["number"] == 2 && other["number"] == 1,
        "native fixture issue numbering differed",
    )?;
    let comment_body = format!("native ordinary comment\n{}", "話".repeat(4500));
    let comment = Seed { browser: b }.json(
        "POST",
        &format!("{repo_api}/issues/1/comments"),
        Some(json!({"body":comment_body})),
        201,
    )?;
    Seed { browser: b }.json(
        "POST",
        &format!("{repo_api}/issues/1/comments"),
        Some(json!({"body":"second native ordinary comment"})),
        201,
    )?;
    let repos = refs(b, api, generation)?;
    require(
        repos.len() == 2,
        "populated private catalogue did not contain both repositories",
    )?;
    let local = repos
        .iter()
        .find(|r| r["external_id"] == native_first)
        .ok_or("first native repository not mapped")?;
    let local_other = repos
        .iter()
        .find(|r| r["external_id"] == native_second)
        .ok_or("second native repository not mapped")?;
    require(
        local["host"] == local_other["host"] && local["id"] != local_other["id"],
        "repository namespaces crossed",
    )?;
    let local_id = local["id"]
        .as_str()
        .ok_or("canonical repository ID missing")?
        .to_owned();
    let prefix = format!("{api}collaboration/repositories/{local_id}");
    let detail = read(b, &format!("{prefix}/issues/1"), generation, 200)?;
    available(&detail)?;
    require(
        detail["ref"]["kind"] == "issue"
            && detail["ref"]["repository"] == local_id
            && detail["ref"]["number"] == "1"
            && detail["ref"]["external_id"]
                == issue["id"].as_i64().ok_or("issue ID missing")?.to_string()
            && detail["body"] == full_body,
        "full issue body/typed identity lost",
    )?;
    require(
        detail["assignees"].as_array().is_some_and(|v| v.len() == 1)
            && detail["labels"][0]["name"] == "native-label"
            && detail["author"]["external_id"] == "1",
        "native issue participant/label facts lost",
    )?;
    let open = read(b, &format!("{prefix}/issues?limit=1"), generation, 200)?;
    require(
        open["items"].as_array().is_some_and(|v| v.len() == 1)
            && open["items"][0]["state"] == "open"
            && open["complete"] == true,
        "open issue filter lost",
    )?;
    let all = read(
        b,
        &format!("{prefix}/issues?state=all&limit=1"),
        generation,
        200,
    )?;
    require(all["complete"] == false, "all-issue paging missing")?;
    let continuation = form(&[(
        "cursor",
        all["next"].as_str().ok_or("issue cursor missing")?,
    )]);
    let next = read(
        b,
        &format!("{prefix}/issues?state=all&limit=1&{continuation}"),
        generation,
        200,
    )?;
    require(
        next["complete"] == true
            && next["items"][0]["state"] == "closed"
            && next["items"][0]["ref"]["number"] == "2",
        "closed issue continuation lost",
    )?;
    let comments = read(
        b,
        &format!("{prefix}/issues/1/comments?limit=1"),
        generation,
        200,
    )?;
    require(
        comments["complete"] == false
            && comments["items"][0]["comment"]["body"] == comment_body
            && comments["items"][0]["comment"]["external_id"]
                == comment["id"]
                    .as_i64()
                    .ok_or("comment ID missing")?
                    .to_string(),
        "full native comment/paging lost",
    )?;
    let continuation = form(&[(
        "cursor",
        comments["next"].as_str().ok_or("comment cursor missing")?,
    )]);
    let rest = read(
        b,
        &format!("{prefix}/issues/1/comments?limit=1&{continuation}"),
        generation,
        200,
    )?;
    require(
        rest["complete"] == true
            && rest["items"][0]["comment"]["body"] == "second native ordinary comment",
        "comment continuation lost",
    )?;
    let caps = read(
        b,
        &format!("{prefix}/issues/1/capabilities"),
        generation,
        200,
    )?;
    require(
        caps["issue"]["external_id"] == detail["ref"]["external_id"]
            && caps["actions"]["issue.edit"]["state"] == "allowed"
            && caps["pull"].is_null(),
        "native author issue capability scope lost",
    )?;
    let other_id = local_other["id"]
        .as_str()
        .ok_or("other canonical repository ID missing")?;
    let other_detail = read(
        b,
        &format!("{api}collaboration/repositories/{other_id}/issues/1"),
        generation,
        200,
    )?;
    require(
        other_detail["ref"]["external_id"] != detail["ref"]["external_id"]
            && other_detail["body"] == "other repository body",
        "same-number issue crossed repositories",
    )?;
    for path in [
        format!("{prefix}/issues/1?actor=1"),
        format!("{prefix}/issues?limit=51"),
    ] {
        read(b, &path, generation, 400)?;
    }
    let reader_password = String::from_utf8(checked(
        Command::new("openssl").args(["rand", "-hex", "20"]),
        "reader password generation failed",
    )?)?
    .trim()
    .to_owned();
    let reader_user=Seed{browser:b}.json("POST","/api/v1/admin/users",Some(json!({"username":"factory-reader","email":"factory-reader@example.invalid","password":reader_password,"must_change_password":false,"send_notify":false})),201)?;
    let reader_root = b.root.join("reader");
    fs::create_dir(&reader_root)?;
    fs::set_permissions(&reader_root, fs::Permissions::from_mode(0o700))?;
    fs::copy(b.root.join("cert.pem"), reader_root.join("cert.pem"))?;
    private_file(&reader_root.join("cookies"), b"")?;
    let mut reader = Browser {
        root: reader_root,
        origin: b.origin.clone(),
        count: 0,
    };
    let reader_generation = login(&mut reader, "factory-reader", &reader_password)?;
    let reader_identity = read(
        &mut reader,
        &format!("{api}identity"),
        &reader_generation,
        200,
    )?;
    require(
        reader_identity["native_actor"]
            == reader_user["id"]
                .as_i64()
                .ok_or("reader native ID missing")?
                .to_string(),
        "reader native identity substituted",
    )?;
    require(
        refs(&mut reader, api, &reader_generation)?.is_empty(),
        "private catalogue disclosed unshared repositories",
    )?;
    read(
        &mut reader,
        &format!("{prefix}/issues/1"),
        &reader_generation,
        404,
    )?;
    Seed { browser: b }.json(
        "PUT",
        &format!("{repo_api}/collaborators/factory-reader"),
        Some(json!({"permission":"read"})),
        204,
    )?;
    let shared = refs(&mut reader, api, &reader_generation)?;
    require(
        shared.len() == 1 && shared[0]["id"] == local_id,
        "native private collaboration read was not observed",
    )?;
    require(
        read(
            &mut reader,
            &format!("{prefix}/issues/1"),
            &reader_generation,
            200,
        )?["body"]
            == full_body,
        "shared private issue body changed",
    )?;
    let reader_caps = read(
        &mut reader,
        &format!("{prefix}/issues/1/capabilities"),
        &reader_generation,
        200,
    )?;
    require(
        reader_caps["actions"]["issue.edit"]["state"] == "denied"
            && reader_caps["actions"]["issue.comment"]["state"] == "allowed",
        "read access became writer capability",
    )?;
    Seed { browser: b }.json(
        "PATCH",
        &repo_api,
        Some(json!({"name":"read-one-renamed"})),
        200,
    )?;
    let renamed = refs(&mut reader, api, &reader_generation)?;
    require(
        renamed.len() == 1
            && renamed[0]["id"] == local_id
            && renamed[0]["name"] == "factory-check/read-one-renamed",
        "rename rebound native/canonical repository identity",
    )?;
    require(
        read(
            &mut reader,
            &format!("{prefix}/issues/1"),
            &reader_generation,
            200,
        )?["ref"]["external_id"]
            == detail["ref"]["external_id"],
        "rename rebound issue identity",
    )?;
    Seed { browser: b }.json(
        "DELETE",
        "/api/v1/repos/factory-check/read-one-renamed/collaborators/factory-reader",
        None,
        204,
    )?;
    require(
        refs(&mut reader, api, &reader_generation)?.is_empty(),
        "revoked repository remained discoverable",
    )?;
    for path in [
        format!("{prefix}/issues/1"),
        format!("{prefix}/issues/1/comments"),
        format!("{prefix}/issues/1/capabilities"),
    ] {
        read(&mut reader, &path, &reader_generation, 404)?;
    }
    println!("PASS: real private native repository/issue/comment/capability reads, complete bodies, paging, separate reader login, rename and collaboration revocation");
    Ok(
        json!({"result":"pass","repositories":2,"issues":3,"full_body_runes":full_body.chars().count(),"comments":2,"private_reader":true,"rename":true,"access_revocation":true}),
    )
}
