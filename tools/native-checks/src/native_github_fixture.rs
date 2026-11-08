use super::{ensure, Result};
use soda_desktop_runtime::credentials::Credentials;
use soda_desktop_runtime::supervisor::NativeSupervisor;
use std::fs;
use std::future::Future;
use std::path::Path;
use std::pin::Pin;
use std::sync::Arc;
use std::task::Poll;
use std::time::{Duration, Instant};

pub(super) async fn call(
    runtime: &NativeSupervisor,
    store: &Arc<Credentials>,
    connection: &str,
    method: &str,
    target: &str,
    body: &[u8],
) -> Result<(u16, serde_json::Value)> {
    let request = runtime.admit_product(
        store.clone(),
        store.capability(connection).await?,
        Some("123".into()),
    )?;
    let reply = request.request(method, target, body).await?;
    Ok((reply.status, serde_json::from_slice(&reply.body)?))
}

pub(super) fn set_mode(io: &Path, name: &str) -> Result<()> {
    if name.starts_with("pause-") {
        for marker in ["entered", "release", "cancelled"] {
            match fs::remove_file(io.join(marker)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.into()),
            }
        }
    }
    fs::write(io.join("mode"), name)?;
    Ok(())
}

pub(super) fn completed(reply: &(u16, serde_json::Value), effect: &str) -> Result<()> {
    ensure(
        reply.0 == 200 && reply.1["effect"] == effect && reply.1["finished"] == true,
        "composed human command did not retain complete primary",
    )
}

pub(super) fn uncertain(reply: &(u16, serde_json::Value)) -> Result<()> {
    ensure(
        matches!(reply.0, 200 | 202)
            && reply.1["effect"] == "indeterminate"
            && reply.1["finished"] == false
            && ["issue", "pull", "branch", "review"]
                .iter()
                .all(|field| reply.1[*field].is_null()),
        "unknown human command became complete, attributable or safe to retry",
    )
}

pub(super) fn posts(io: &Path) -> Result<u64> {
    match fs::read_to_string(io.join("posts")) {
        Ok(s) => Ok(s.parse()?),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(0),
        Err(e) => Err(e.into()),
    }
}

pub(super) async fn entered<T, E: std::fmt::Display>(
    future: Pin<&mut impl Future<Output = std::result::Result<T, E>>>,
    io: &Path,
) -> Result<()> {
    let mut future = future;
    let until = Instant::now() + Duration::from_secs(4);
    loop {
        let ready = std::future::poll_fn(|cx| Poll::Ready(future.as_mut().poll(cx))).await;
        if let Poll::Ready(result) = ready {
            return Err(match result {
                Ok(_) => "RPC completed before transport barrier".into(),
                Err(e) => format!("RPC failed before transport barrier: {e}"),
            }
            .into());
        }
        if io.join("entered").exists() {
            return Ok(());
        }
        ensure(
            Instant::now() < until,
            "provider transport did not reach barrier",
        )?;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

// Exercise the production private callback wire; never accepts a foreign endpoint.
pub(super) async fn callback(
    runtime: &NativeSupervisor,
    profile: &Path,
    route: &str,
    body: serde_json::Value,
) -> Result<(u16, serde_json::Value)> {
    let health = runtime.health()?;
    let bearer = fs::read_to_string(profile.join("backend.key"))?;
    let response = soda_unix_http::request_authenticated_async(
        &profile.join("host.sock"),
        "POST",
        route,
        "soda-native",
        &serde_json::to_vec(&body)?,
        &bearer,
        &[("X-Soda-Native-Instance", health.instance.as_str())],
        Duration::from_secs(5),
        soda_unix_http::Limits {
            header_bytes: 4096,
            body_bytes: 512 * 1024,
        },
    )
    .await
    .map_err(|error| format!("private callback transport: {error:?}"))?;
    Ok((response.status, serde_json::from_slice(&response.body)?))
}
