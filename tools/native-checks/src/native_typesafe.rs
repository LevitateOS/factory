//! JEV key custody against controlled TypeSafe replies. A loopback stand-in
//! answers each request from a script, records what it was sent and fails the
//! check if Factory sends more requests than the script allows.
use super::{ensure, prepare, Result};
use soda_desktop_runtime::credentials::{Credentials, Provider, Token};
use soda_desktop_runtime::jev::{Error, Jev, State};
use std::collections::VecDeque;
use std::path::Path;
use std::sync::{Arc, Mutex};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

#[derive(Clone, Copy)]
enum Reply {
    Models,
    Status(u16),
    /// Accept, then close without answering.
    Drop,
}

#[derive(Default)]
struct Stand {
    script: VecDeque<Reply>,
    /// (method and path, Authorization header) for every request received.
    seen: Vec<(String, String)>,
    unscripted: usize,
}

const MODELS: &str = r#"{"models":[{"name":"jev-latest","description":"Latest Jev","release_date":"2026-11-01"},{"name":"jev-1.14.0","description":"Jev 1.14","release_date":"2026-11-01"},{"name":"jev-1.13.0","description":"Jev 1.13","release_date":"2026-09-15"}]}"#;

async fn serve(listener: tokio::net::TcpListener, stand: Arc<Mutex<Stand>>) {
    loop {
        let Ok((mut socket, _)) = listener.accept().await else { return };
        let stand = stand.clone();
        tokio::spawn(async move {
            let mut request = Vec::new();
            let mut buf = [0u8; 4096];
            while !request.windows(4).any(|w| w == b"\r\n\r\n") && request.len() < 64 * 1024 {
                match socket.read(&mut buf).await {
                    Ok(0) | Err(_) => return,
                    Ok(n) => request.extend_from_slice(&buf[..n]),
                }
            }
            let text = String::from_utf8_lossy(&request).into_owned();
            let line = text.lines().next().unwrap_or("").rsplit_once(' ').map(|(l, _)| l.to_owned()).unwrap_or_default();
            let auth = text
                .lines()
                .find_map(|l| l.split_once(':').filter(|(k, _)| k.eq_ignore_ascii_case("authorization")).map(|(_, v)| v.trim().to_owned()))
                .unwrap_or_default();
            let reply = {
                let mut stand = stand.lock().unwrap();
                stand.seen.push((line, auth));
                let next = stand.script.pop_front();
                if next.is_none() {
                    stand.unscripted += 1;
                }
                next.unwrap_or(Reply::Status(599))
            };
            let (status, body) = match reply {
                Reply::Drop => return,
                Reply::Models => (200, MODELS.to_owned()),
                Reply::Status(code) => (code, r#"{"detail":"controlled reply"}"#.to_owned()),
            };
            let response = format!(
                "HTTP/1.1 {status} Controlled\r\ncontent-type: application/json\r\ncontent-length: {}\r\nconnection: close\r\n\r\n{body}",
                body.len()
            );
            let _ = socket.write_all(response.as_bytes()).await;
            let _ = socket.shutdown().await;
        });
    }
}

fn key(tag: &str) -> String {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes).expect("random fixture key");
    format!("ts-{tag}-{}", bytes.iter().map(|b| format!("{b:02x}")).collect::<String>())
}

/// Every TypeSafe outcome the Accounts card can show, with an exact request
/// count per step, against a fresh profile in the fixture keyring namespace.
pub async fn run(profile: &Path) -> Result<()> {
    prepare(profile)?;
    let store = Credentials::open(profile, "123456789abcdef0123456789abcdef0")?;
    ensure(store.status().await?.is_empty(), "fixture profile already contains accounts")?;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
    let base = format!("http://127.0.0.1:{}", listener.local_addr()?.port());
    let stand = Arc::new(Mutex::new(Stand::default()));
    let server = tokio::spawn(serve(listener, stand.clone()));
    let jev = Jev::controlled(profile, &base)?;
    let (a, b) = (key("a"), key("b"));
    let token = |k: &str| Token::new(k.as_bytes().to_vec());
    let step = |replies: &[Reply]| {
        let mut s = stand.lock().unwrap();
        ensure(s.script.is_empty(), "an earlier scripted reply was not used")?;
        s.script.extend(replies.iter().copied());
        Ok::<usize, Box<dyn std::error::Error>>(s.seen.len())
    };
    let sent_since = |before: usize| stand.lock().unwrap().seen[before..].to_vec();
    let bearer = |k: &str| format!("Bearer {k}");

    ensure(jev.status(&store).await?.state == State::None, "fresh profile reports a JEV key")?;

    // Too short to be a key: refused locally, nothing sent.
    let before = step(&[])?;
    ensure(matches!(jev.connect(&store, token("short-key")?).await, Err(Error::Invalid)), "short key was not refused")?;
    ensure(sent_since(before).is_empty(), "short key reached TypeSafe")?;

    // 401 on connect: one request with that key, nothing stored.
    let before = step(&[Reply::Status(401)])?;
    ensure(matches!(jev.connect(&store, token(&a)?).await, Err(Error::NotAccepted)), "401 did not refuse the key")?;
    let sent = sent_since(before);
    ensure(sent.len() == 1 && sent[0] == ("GET /v1/models".into(), bearer(&a)), "connect check was not one GET /v1/models with the key")?;
    ensure(jev.status(&store).await?.state == State::None, "a refused key was stored")?;
    ensure(store.status().await?.is_empty(), "a refused key reached the keyring")?;

    // Offline and unexpected statuses on connect: one request each, nothing stored.
    let before = step(&[Reply::Drop])?;
    ensure(matches!(jev.connect(&store, token(&a)?).await, Err(Error::Offline)), "a dropped connection was not offline")?;
    ensure(sent_since(before).len() == 1, "offline connect retried")?;
    let before = step(&[Reply::Status(529)])?;
    ensure(matches!(jev.connect(&store, token(&a)?).await, Err(Error::Unexpected(529))), "529 on connect was not unexpected")?;
    ensure(sent_since(before).len() == 1 && store.status().await?.is_empty(), "529 on connect retried or stored")?;

    // 200: stored with the models TypeSafe listed.
    let before = step(&[Reply::Models])?;
    let status = jev.connect(&store, token(&a)?).await?;
    ensure(sent_since(before).len() == 1, "connect sent more than one request")?;
    ensure(
        status.state == State::Connected
            && status.last4.as_deref() == Some(&a[a.len() - 4..])
            && status.models.len() == 3
            && status.default_model == "jev-latest"
            && status.added_at > 0
            && status.checked_at > 0,
        "connected status is incomplete",
    )?;
    let saved = store.status().await?;
    ensure(saved.len() == 1 && saved[0].provider == Provider::Typesafe, "the key is not one TypeSafe credential")?;

    // Model choice is local.
    let before = step(&[])?;
    ensure(jev.set_model(&store, "jev-1.13.0").await?.default_model == "jev-1.13.0", "pinned model not kept")?;
    ensure(matches!(jev.set_model(&store, "not-a-model").await, Err(Error::Invalid)), "unknown model accepted")?;
    ensure(sent_since(before).is_empty(), "choosing a model contacted TypeSafe")?;

    // Replacing with a refused key keeps the saved one.
    let before = step(&[Reply::Status(401)])?;
    ensure(matches!(jev.connect(&store, token(&b)?).await, Err(Error::NotAccepted)), "refused replacement was accepted")?;
    ensure(sent_since(before) == vec![("GET /v1/models".into(), bearer(&b))], "replacement check was not one request with the new key")?;
    let kept = jev.status(&store).await?;
    ensure(kept.state == State::Connected && kept.last4.as_deref() == Some(&a[a.len() - 4..]), "refused replacement changed the saved key")?;
    let before = step(&[Reply::Models])?;
    jev.check(&store).await?;
    ensure(sent_since(before) == vec![("GET /v1/models".into(), bearer(&a))], "Check now did not send the kept key once")?;

    // An accepted replacement takes over and keeps an offered pinned model.
    let before = step(&[Reply::Models])?;
    let replaced = jev.connect(&store, token(&b)?).await?;
    ensure(sent_since(before).len() == 1, "replacement sent more than one request")?;
    ensure(
        replaced.last4.as_deref() == Some(&b[b.len() - 4..]) && replaced.default_model == "jev-1.13.0",
        "replacement lost the new key or the pinned model",
    )?;
    ensure(store.status().await?.len() == 1, "replacement left two TypeSafe credentials")?;

    // Check now: 401 marks it refused (not an error), 200 clears it.
    let before = step(&[Reply::Status(401)])?;
    let refused = jev.check(&store).await?;
    ensure(refused.state == State::Refused && refused.refused_at.is_some(), "401 on Check now did not mark the key refused")?;
    ensure(sent_since(before) == vec![("GET /v1/models".into(), bearer(&b))], "Check now did not send the saved key once")?;
    ensure(jev.status(&store).await?.state == State::Refused, "refusal was not kept")?;
    let before = step(&[Reply::Models])?;
    ensure(jev.check(&store).await?.state == State::Connected, "a passing check did not clear the refusal")?;
    ensure(sent_since(before).len() == 1, "passing check sent more than one request")?;

    // Offline and busy on Check now: one request each, status unchanged.
    let before = step(&[Reply::Drop])?;
    ensure(matches!(jev.check(&store).await, Err(Error::Offline)), "offline check was not offline")?;
    let before_busy = step(&[Reply::Status(429)])?;
    ensure(before_busy == before + 1, "offline check retried")?;
    ensure(matches!(jev.check(&store).await, Err(Error::Unexpected(429))), "429 on check was not reported")?;
    ensure(sent_since(before_busy).len() == 1, "429 on check retried")?;
    ensure(jev.status(&store).await?.state == State::Connected, "offline or busy changed the key state")?;

    // The keys appear nowhere in the profile directory.
    for entry in std::fs::read_dir(profile)? {
        let path = entry?.path();
        if path.is_file() {
            let bytes = std::fs::read(&path)?;
            for k in [&a, &b] {
                ensure(!bytes.windows(k.len()).any(|w| w == k.as_bytes()), "a TypeSafe key was written to the profile directory")?;
            }
        }
    }

    // Remove deletes the credential and the record, without contacting TypeSafe.
    let before = step(&[])?;
    ensure(jev.remove(&store).await?.state == State::None, "remove left a JEV key")?;
    ensure(sent_since(before).is_empty(), "remove contacted TypeSafe")?;
    ensure(store.status().await?.is_empty(), "remove left a credential")?;
    ensure(
        std::fs::read_dir(profile)?.filter_map(|e| e.ok()).all(|e| !e.file_name().to_string_lossy().starts_with("jev-")),
        "remove left the JEV record",
    )?;

    let total = {
        let s = stand.lock().unwrap();
        ensure(s.script.is_empty() && s.unscripted == 0, "requests did not match the script")?;
        s.seen.len()
    };
    server.abort();
    println!("{{\"result\":\"pass\",\"check\":\"typesafe-key-custody\",\"typesafe_requests\":{total}}}");
    Ok(())
}
