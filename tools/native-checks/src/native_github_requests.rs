//! GitHub request counts per product step, against a loopback stand-in for
//! api.github.com. The real native connector, Go backend and PostgreSQL run;
//! only the provider is controlled. Each step prints the requests it caused and
//! fails when it sends more than the step needs.
use super::{ensure, prepare, Result};
use serde_json::{json, Value};
use soda_desktop_runtime::credentials::{Account, Credentials, Provider, Token};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

const NAME: &str = "owner/repo";

fn user() -> Value {
    json!({"id":321,"login":"declared-writer","node_id":"U_declared"})
}
fn repository() -> Value {
    json!({"id":123,"name":"repo","full_name":NAME,"private":false,"archived":false,"disabled":false,"default_branch":"main","node_id":"R_123","owner":{"id":321,"login":"owner"},
        "permissions":{"admin":true,"push":true,"pull":true},"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":true})
}
fn issue(number: u64) -> Value {
    json!({"id":7000+number,"number":number,"node_id":format!("I_{number}"),"repository_url":format!("https://api.github.com/repos/{NAME}"),"title":"Existing issue","body":"","state":"open","locked":false,"user":user(),
        "assignees":[],"labels":[],"created_at":"2026-11-01T00:00:00Z","updated_at":"2026-11-01T00:00:00Z","closed_at":null})
}
fn comment() -> Value {
    json!({"id":9001,"user":user(),"body":"Reply","created_at":"2026-11-01T00:00:00Z","updated_at":"2026-11-01T00:00:00Z"})
}

/// The provider's answer to one request, by method and path alone.
fn answer(method: &str, path: &str, body: &[u8]) -> (u16, Value) {
    let repo = format!("/repos/{NAME}");
    match (method, path) {
        ("GET", "/user") => (200, user()),
        ("GET", "/user/repos") | ("GET", "/installation/repositories") => (200, json!([repository()])),
        ("GET", "/repositories/123") => (200, repository()),
        ("GET", p) if p == repo => (200, repository()),
        ("GET", p) if p == format!("{repo}/issues") => (200, json!([issue(7)])),
        ("GET", p) if p == format!("{repo}/issues/7") => (200, issue(7)),
        ("GET", p) if p == format!("{repo}/issues/7/comments") => (200, json!([])),
        ("POST", p) if p == format!("{repo}/issues/7/comments") => (201, comment()),
        ("POST", p) if p == format!("{repo}/issues") => (201, issue(8)),
        ("POST", "/graphql") => graphql(body),
        _ => (404, json!({"message":"Not Found"})),
    }
}

#[derive(Default)]
struct Seen(Vec<String>);

async fn serve(listener: tokio::net::TcpListener, seen: Arc<Mutex<Seen>>) {
    loop {
        let Ok((mut socket, _)) = listener.accept().await else { return };
        let seen = seen.clone();
        tokio::spawn(async move {
            loop {
                let mut request = Vec::new();
                let mut buf = [0u8; 8192];
                while !request.windows(4).any(|w| w == b"\r\n\r\n") && request.len() < 256 * 1024 {
                    match socket.read(&mut buf).await {
                        Ok(0) | Err(_) => return,
                        Ok(n) => request.extend_from_slice(&buf[..n]),
                    }
                }
                let split = request.windows(4).position(|w| w == b"\r\n\r\n").unwrap_or(request.len()) + 4;
                let head = String::from_utf8_lossy(&request[..split.min(request.len())]).into_owned();
                let length = head
                    .lines()
                    .find_map(|l| l.split_once(':').filter(|(k, _)| k.eq_ignore_ascii_case("content-length")).and_then(|(_, v)| v.trim().parse::<usize>().ok()))
                    .unwrap_or(0);
                let mut body = request[split.min(request.len())..].to_vec();
                while body.len() < length {
                    match socket.read(&mut buf).await {
                        Ok(0) | Err(_) => return,
                        Ok(n) => body.extend_from_slice(&buf[..n]),
                    }
                }
                let mut parts = head.lines().next().unwrap_or("").split(' ');
                let (method, target) = (parts.next().unwrap_or("").to_owned(), parts.next().unwrap_or("").to_owned());
                let path = target.split('?').next().unwrap_or("").to_owned();
                seen.lock().unwrap().0.push(format!("{method} {target}"));
                let (status, value) = answer(&method, &path, &body);
                let text = value.to_string();
                let response = format!(
                    "HTTP/1.1 {status} Controlled\r\ncontent-type: application/json; charset=utf-8\r\nx-ratelimit-remaining: 4999\r\nx-ratelimit-limit: 5000\r\nx-ratelimit-reset: 4102444800\r\nx-oauth-scopes: repo\r\ncontent-length: {}\r\n\r\n{text}",
                    text.len()
                );
                if socket.write_all(response.as_bytes()).await.is_err() {
                    return;
                }
            }
        });
    }
}

pub(super) async fn run(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    prepare(profile)?;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
    let origin = format!("http://127.0.0.1:{}/", listener.local_addr()?.port());
    ensure(soda_desktop_runtime::github_provider::control_api(&origin), "controlled GitHub origin refused")?;
    let seen = Arc::new(Mutex::new(Seen::default()));
    tokio::spawn(serve(listener, seen.clone()));
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let result = steps(&runtime, profile, &seen).await;
    let stopped = runtime.stop();
    result?;
    stopped?;
    Ok(())
}

async fn steps(runtime: &NativeSupervisor, _profile: &Path, seen: &Arc<Mutex<Seen>>) -> Result<()> {
    let store = Arc::new(Credentials::open(_profile, &runtime.health()?.profile)?);
    ensure(store.status().await?.is_empty(), "request check requires empty native custody")?;
    let account = Account::new(Provider::Github, "https://github.com", "321")?.with_label("declared-writer")?;
    let connection = store.store(account, Token::new(b"factory-declared-requests".to_vec())?).await?.connection;
    let mut failures = Vec::new();
    let mut step = async |name: &str, most: usize, method: &str, target: &str, body: Value, settle: u64| -> Result<Value> {
        let before = seen.lock().unwrap().0.len();
        let bytes = if body.is_null() { Vec::new() } else { serde_json::to_vec(&body)? };
        let admitted = runtime.admit_product(store.clone(), store.capability(&connection).await?, Some("123".into()))?;
        let raw = admitted.request(method, target, &bytes).await?;
        let reply = (raw.status, serde_json::from_slice::<Value>(&raw.body).unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&raw.body).into_owned())));
        tokio::time::sleep(Duration::from_millis(settle)).await;
        let sent = seen.lock().unwrap().0[before..].to_vec();
        println!("{name}: HTTP {} · {} GitHub requests (most {most})", reply.0, sent.len());
        for line in &sent {
            println!("    {line}");
        }
        let shown = reply.1.to_string();
        println!("    reply {}", &shown[..shown.len().min(300)]);
        if sent.len() > most {
            failures.push(format!("{name} sent {} > {most}", sent.len()));
        }
        Ok(reply.1)
    };
    let catalogue = step("catalogue", 2, "GET", "/api/factory/collaboration/repositories", Value::Null, 0).await?;
    let repository = catalogue["items"][0]["id"].as_str().ok_or("canonical repository absent")?.to_owned();
    step("feed read", 0, "GET", &format!("/api/factory/feed?repositories={repository}&limit=20"), Value::Null, 0).await?;
    step("issues", 1, "GET", &format!("/api/factory/collaboration/repositories/{repository}/issues?state=open&limit=20"), Value::Null, 0).await?;
    step("issue", 1, "GET", &format!("/api/factory/collaboration/repositories/{repository}/issues/7"), Value::Null, 0).await?;
    step(
        "comment",
        2,
        "POST",
        &format!("/api/factory/collaboration/repositories/{repository}/issues/7/comments"),
        json!({"command_id":format!("{:032x}", 1),"body":"Reply"}),
        0,
    )
    .await?;
    step(
        "feed post",
        1,
        "POST",
        "/api/factory/feed/posts",
        json!({"repository":repository,"body":"The settings page forgets my theme after a restart.","idempotency_key":"request-check-1"}),
        3000,
    )
    .await?;
    step("feed read after post", 0, "GET", &format!("/api/factory/feed?repositories={repository}&limit=20"), Value::Null, 0).await?;
    if failures.is_empty() {
        println!("PASS: every step stayed within its GitHub request count");
        Ok(())
    } else {
        Err(failures.join("; ").into())
    }
}

/// GraphQL writes Factory sends, answered as GitHub would.
fn graphql(body: &[u8]) -> (u16, Value) {
    let request: Value = serde_json::from_slice(body).unwrap_or(Value::Null);
    let query = request["query"].as_str().unwrap_or("");
    let variables = &request["variables"];
    if query.contains("addComment") {
        let url = format!("https://github.com/{NAME}/issues/7");
        return (
            200,
            json!({"data":{"addComment":{"subject":{"__typename":"Issue","id":variables["subject"],"url":url},
                "commentEdge":{"node":{"id":"IC_9001","fullDatabaseId":"9001","body":variables["body"],"createdAt":"2026-11-01T00:00:00Z",
                "updatedAt":"2026-11-01T00:00:00Z","url":format!("{url}#issuecomment-9001"),"minimizedReason":null,
                "author":{"login":"declared-writer","id":"U_declared"}}}}}}),
        );
    }
    (200, json!({"errors":[{"message":"unscripted query"}]}))
}
