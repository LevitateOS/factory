//! Composed human comments through the actual native credential/effect owner,
//! shared Go HTTP/service/journal and PostgreSQL. Dummy-only HTTPS, no forwarding.
use super::{ensure, prepare, Result};
use serde_json::{json, Value};
use soda_desktop_runtime::credentials::{Account, Credentials, Provider, Token};
use soda_desktop_runtime::supervisor::NativeSupervisor;
use soda_desktop_runtime::ProductRequest;
use std::os::unix::fs::PermissionsExt;
use std::{fs, path::Path, sync::Arc};

async fn call(
    runtime: &NativeSupervisor,
    store: &Arc<Credentials>,
    connection: &str,
    method: &str,
    target: &str,
    body: &[u8],
) -> Result<(u16, Value)> {
    let request = runtime.admit_product(
        store.clone(),
        store.capability(connection).await?,
        Some("123".into()),
    )?;
    let reply = request.request(method, target, body).await?;
    Ok((reply.status, serde_json::from_slice(&reply.body)?))
}
fn envelope(path: &str, id: &str, body: &str) -> Result<Vec<u8>> {
    let request: ProductRequest = serde_json::from_value(
        json!({"kind":"write","path":path,"body":{"command_id":id,"body":body}}),
    )?;
    let (_, _, bytes) = request.request()?;
    Ok(bytes)
}
fn posts(io: &Path) -> Result<u64> {
    match fs::read_to_string(io.join("posts")) {
        Ok(s) => Ok(s.parse()?),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(0),
        Err(e) => Err(e.into()),
    }
}
fn mode(io: &Path, name: &str) -> Result<()> {
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
fn completed(reply: &(u16, Value), effect: &str) -> Result<()> {
    ensure(
        reply.0 == 200 && reply.1["effect"] == effect && reply.1["finished"] == true,
        "composed comment did not retain its complete outcome",
    )
}
fn uncertain(reply: &(u16, Value)) -> Result<()> {
    ensure(
        matches!(reply.0, 200 | 202)
            && reply.1["effect"] == "indeterminate"
            && reply.1["finished"] == false,
        "uncertain comment became complete or safe to repeat",
    )
}

pub(super) async fn run(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    mode(&io, "positive")?;
    let mut runtime = NativeSupervisor::start(payload, production, profile)?;
    let mut store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "composed comment check requires empty native custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let saved = store
        .store(
            account()?,
            Token::new(b"factory-declared-comment".to_vec())?,
        )
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
    ensure(
        catalogue.0 == 200,
        "composed comment canonical catalogue refused",
    )?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let issue = format!("/api/factory/collaboration/repositories/{repository}/issues/7/comments");
    let pull = format!("/api/factory/collaboration/repositories/{repository}/pulls/8/comments");
    let mut cases = Vec::new();
    for (i, (path, body)) in [
        (
            &issue,
            "Owned ordinary human writing π\n[link](relative.md)".to_owned(),
        ),
        (&pull, "Owned PR conversation".to_owned()),
        (&issue, "🍵".repeat(16384)),
        (&issue, "\u{0001}".repeat(65536)),
    ]
    .into_iter()
    .enumerate()
    {
        let id = format!("{:032x}", 100 + i);
        let bytes = envelope(path, &id, &body)?;
        let before = posts(&io)?;
        completed(
            &call(&runtime, &store, &connection, "POST", path, &bytes).await?,
            "committed",
        )?;
        ensure(
            posts(&io)? == before + 1,
            "composed comment did not send exactly one POST",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", path, &bytes).await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "GET",
                &format!("{path}/commands/{id}"),
                &[],
            )
            .await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{path}/commands/{id}/cancel"),
                b"{}",
            )
            .await?,
            "committed",
        )?;
        ensure(
            posts(&io)? == before + 1,
            "lookup/replay/cancel duplicated a committed comment",
        )?;
        let conflict = call(
            &runtime,
            &store,
            &connection,
            "POST",
            path,
            &envelope(path, &id, "changed human writing")?,
        )
        .await?;
        ensure(
            conflict.0 == 409 && posts(&io)? == before + 1,
            "changed command writing was accepted or dispatched",
        )?;
        let read = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{path}?limit=50"),
            &[],
        )
        .await?;
        ensure(
            read.0 == 200
                && read.1["items"]
                    .as_array()
                    .is_some_and(|items| items.iter().any(|item| item["comment"]["body"] == body)),
            "real composed discussion readback lost writing",
        )?;
        cases.push(format!("positive-{i}"));
    }
    for path in [&issue, &pull] {
        let id = "22222222222222222222222222222222";
        let reply = call(
            &runtime,
            &store,
            &connection,
            "POST",
            path,
            &envelope(path, id, &"x".repeat(65537))?,
        )
        .await?;
        ensure(reply.0 == 400, "composed Go writing bound weakened")?;
    }
    let concurrent_id = "33333333333333333333333333333333";
    let concurrent = envelope(
        &issue,
        concurrent_id,
        "Owned simultaneous identical command",
    )?;
    let before = posts(&io)?;
    let (first, second) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &issue, &concurrent),
        call(&runtime, &store, &connection, "POST", &issue, &concurrent)
    );
    for reply in [first?, second?] {
        if reply.1["finished"] == true {
            completed(&reply, "committed")?;
        } else {
            uncertain(&reply)?;
        }
    }
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{issue}/commands/{concurrent_id}"),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        posts(&io)? == before + 1,
        "concurrent product submissions duplicated a POST",
    )?;
    cases.push("concurrent-identical-command".into());
    let other_repository = repository.parse::<i64>()? + 1;
    let other_path =
        format!("/api/factory/collaboration/repositories/{other_repository}/issues/7/comments");
    let refused = call(
        &runtime,
        &store,
        &connection,
        "POST",
        &other_path,
        &envelope(
            &other_path,
            "99999999999999999999999999999999",
            "Owned wrong repository scope",
        )?,
    )
    .await?;
    ensure(
        refused.0 == 403 && posts(&io)? == before + 1,
        "request expanded its admitted canonical repository scope",
    )?;
    cases.push("other-repository-refusal".into());
    let lost_id = "44444444444444444444444444444444";
    let lost = envelope(
        &issue,
        lost_id,
        "Owned response lost after provider creation",
    )?;
    mode(&io, "drop-reply")?;
    let before = posts(&io)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &issue, &lost).await?)?;
    mode(&io, "positive")?;
    uncertain(&call(&runtime, &store, &connection, "POST", &issue, &lost).await?)?;
    uncertain(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{issue}/commands/{lost_id}"),
            &[],
        )
        .await?,
    )?;
    let read = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{issue}?limit=50"),
        &[],
    )
    .await?;
    ensure(
        read.0 == 200
            && read.1["items"].as_array().is_some_and(|v| {
                v.iter()
                    .any(|x| x["comment"]["body"] == "Owned response lost after provider creation")
            }),
        "lost-reply fixture did not actually create its observed comment",
    )?;
    uncertain(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{issue}/commands/{lost_id}"),
            &[],
        )
        .await?,
    )?;
    ensure(
        posts(&io)? == before + 1,
        "lost response or matching readback authorized a duplicate POST",
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
        mode(&io, name)?;
        let before = posts(&io)?;
        let bytes = envelope(&issue, id, "Owned concurrent cancellation")?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let pending = request.request("POST", &issue, &bytes);
        tokio::pin!(pending);
        super::native_github_transport::entered(pending.as_mut(), &io).await?;
        let cancelled = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &format!("{issue}/commands/{id}/cancel"),
            b"{}",
        )
        .await?;
        uncertain(&cancelled)?;
        ensure(
            cancelled.1["cancellation"] == "indeterminate",
            "in-flight cancel fabricated acknowledgment",
        )?;
        fs::write(io.join("release"), b"")?;
        let reply = pending.await?;
        let result = (reply.status, serde_json::from_slice::<Value>(&reply.body)?);
        completed(&result, effect)?;
        ensure(
            result.1["cancellation"] == "indeterminate",
            "late primary receipt erased cancellation uncertainty",
        )?;
        ensure(
            posts(&io)? == before + u64::from(effect == "committed"),
            "preflight cancellation sent a POST or committed race duplicated",
        )?;
        cases.push(name.into());
    }
    // A replaced OS revision cannot deliver the old operation's successful reply.
    mode(&io, "pause-post")?;
    let withdrawn_id = "77777777777777777777777777777777";
    let withdrawn = envelope(&issue, withdrawn_id, "Owned withdrawal while reply pending")?;
    let before = posts(&io)?;
    let request = runtime.admit_product(
        store.clone(),
        store.capability(&connection).await?,
        Some("123".into()),
    )?;
    let pending = request.request("POST", &issue, &withdrawn);
    tokio::pin!(pending);
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    store.withdraw(&connection).await?;
    let saved = store
        .store(
            account()?,
            Token::new(b"factory-declared-comment".to_vec())?,
        )
        .await?;
    connection = saved.connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "withdrawn/replaced native capability delivered an old comment result",
    )?;
    mode(&io, "positive")?;
    uncertain(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{issue}/commands/{withdrawn_id}"),
            &[],
        )
        .await?,
    )?;
    uncertain(&call(&runtime, &store, &connection, "POST", &issue, &withdrawn).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "reauthentication replayed an uncertain old comment",
    )?;
    cases.push("withdrawal-replacement-fences-old-result".into());

    mode(&io, "pause-post")?;
    let stopped_id = "88888888888888888888888888888888";
    let stopped = envelope(&issue, stopped_id, "Owned runtime stops after dispatch")?;
    let before = posts(&io)?;
    let request = runtime.admit_product(
        store.clone(),
        store.capability(&connection).await?,
        Some("123".into()),
    )?;
    let pending = request.request("POST", &issue, &stopped);
    tokio::pin!(pending);
    super::native_github_transport::entered(pending.as_mut(), &io).await?;
    runtime.stop()?;
    ensure(
        pending.await.is_err(),
        "retired runtime delivered an in-flight comment",
    )?;
    store.retire();
    runtime = NativeSupervisor::start(payload, production, profile)?;
    store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    mode(&io, "positive")?;
    uncertain(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{issue}/commands/{stopped_id}"),
            &[],
        )
        .await?,
    )?;
    uncertain(&call(&runtime, &store, &connection, "POST", &issue, &stopped).await?)?;
    uncertain(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{issue}/commands/{lost_id}"),
            &[],
        )
        .await?,
    )?;
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{issue}/commands/{:032x}", 100),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        posts(&io)? == before + 1,
        "retained restart repeated a captured uncertain POST",
    )?;
    cases.push("retained-runtime-restart".into());

    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?
                .with_label("declared-other")?,
            Token::new(b"factory-declared-other".to_vec())?,
        )
        .await?;
    let hidden = call(
        &runtime,
        &store,
        &other.connection,
        "GET",
        &format!("{issue}/commands/{lost_id}"),
        &[],
    )
    .await?;
    ensure(
        hidden.0 == 404,
        "another account read a retained comment command",
    )?;
    let conflict = call(&runtime, &store, &other.connection, "POST", &issue, &lost).await?;
    ensure(
        conflict.0 == 409 && posts(&io)? == before + 1,
        "another account adopted or dispatched the retained command",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "composed comment fixture saw an unexpected request",
    )?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "owned composed comment grants remained",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("comment-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_provider":false,"native_product_consumer":true,"capabilities_enabled":false,"provider_posts":posts(&io)?,"retained_restart":true,"maximum_writing_bytes":65536}),
        )?,
    )?;
    println!("PASS: composed native/Rust/Go/PG human comments preserve exact writing, causal receipts, one-shot replay, lost-reply uncertainty, concurrent cancellation, withdrawn generations, other-account refusal and retained restart; dummy-only provider, no actual GitHub or installed gate qualification");
    Ok(())
}
