//! Existing native executable's production reader/effect acceptance. Use only an
//! isolated, no-forwarding declared peer and its fixed dummy credential.
use super::{ensure, prepare, Result};
use serde_json::json;
use soda_desktop_runtime::{
    credentials::Token,
    github_provider::{Owner, ReviewerRead, ReviewerTarget},
};
use std::{
    fs,
    path::Path,
    time::{Duration, Instant},
};

// The supplied Go observer composes the production admitted port, not a second
// provider reader. It records private facts in this owned dummy-only profile.
pub(super) async fn inline_preparation_port(
    production: &Path,
    payload: &Path,
    profile: &Path,
) -> Result<()> {
    use super::native_github_fixture::call;
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
    };
    use std::{os::unix::fs::PermissionsExt, sync::Arc};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let credentials = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        credentials.status().await?.is_empty(),
        "owned custody not empty",
    )?;
    let connection = credentials
        .store(
            Account::new(Provider::Github, "https://github.com", "321")?,
            Token::new(b"factory-declared-review-preparation".to_vec())?,
        )
        .await?
        .connection;
    let anchor = || json!({"base":"a".repeat(40),"head":"b".repeat(40),"path":"src/tea.md","side":"new","scope":"lines","start_line":2,"end_line":3});
    let mut cases = Vec::new();
    for name in [
        "new-lines",
        "old-lines",
        "new-file",
        "renamed-old",
        "unicode-path",
        "maximum-path",
        "escaped-path",
        "merge-base",
        "no-pending",
        "pending-change",
        "comment-change",
    ] {
        fs::write(io.join("mode"), format!("inline-{name}"))?;
        let mut a = anchor();
        match name {
            "old-lines" => a["side"] = json!("old"),
            "new-file" => {
                a["scope"] = json!("file");
                a["start_line"] = json!(0);
                a["end_line"] = json!(0);
            }
            "renamed-old" => {
                a["side"] = json!("old");
                a["path"] = json!("src/old.md");
            }
            "unicode-path" => a["path"] = json!("src/茶/π.md"),
            "maximum-path" => a["path"] = json!(format!("p/{}", "x".repeat(4094))),
            "escaped-path" => a["path"] = json!(format!("p/{}", "\u{0001}".repeat(4094))),
            _ => (),
        }
        fs::write(
            io.join("selection.json"),
            serde_json::to_vec(&json!({"repository":"123","number":7,"anchor":a}))?,
        )?;
        let reply = call(
            &runtime,
            &credentials,
            &connection,
            "GET",
            "/api/factory/collaboration/repositories",
            &[],
        )
        .await?;
        ensure(
            reply.0 == 503,
            "observer accidentally enabled a product consumer",
        )?;
        let proof: serde_json::Value =
            serde_json::from_slice(&fs::read(io.join("port-proof.json"))?)?;
        ensure(
            proof["ok"] == true
                && proof["identity"]["Connection"]["account"] == "321"
                && proof["identity"]["SelectedRepository"] == "123"
                && proof["preparation"]["selection"] == a
                && proof["preparation"]["repository"] == "123"
                && proof["preparation"]["diff"]["catalogue"]["complete"] == true
                && proof["preparation"]["diff"]["catalogue"]["count"] == 13,
            "Go/native inline facts lost exact selection/account/catalogue",
        )?;
        if name == "pending-change" {
            ensure(
                proof["preparation"]["pending"]["body"]
                    == "Changed pending summary after catalogue",
                "current pending summary was not captured",
            )?;
        }
        if name == "escaped-path" {
            ensure(
                proof["descriptor_bytes"]
                    .as_u64()
                    .is_some_and(|n| n > 8192 && n <= 65536),
                "maximal escaped path did not cross ordinary descriptor bound",
            )?;
        }
        if name == "merge-base" {
            ensure(
                proof["preparation"]["diff"]["scope"]["base"] == "a".repeat(40)
                    && proof["preparation"]["diff"]["scope"]["pull_base"] == "c".repeat(40),
                "composed preparation conflated branch tip and merge base",
            )?;
        }
        cases.push(name);
    }
    for (name, status) in [
        ("invalid-path", 0),
        ("invalid-number", 0),
        ("invalid-repository", 0),
        ("outside-repository", 403),
        ("stale-head", 409),
        ("unavailable-patch", 409),
    ] {
        fs::write(io.join("mode"), format!("inline-{name}"))?;
        let mut input = json!({"repository":"123","number":7,"anchor":anchor()});
        match name {
            "invalid-path" => input["anchor"]["path"] = json!("x".repeat(4097)),
            "invalid-number" => input["number"] = json!(0),
            "invalid-repository" => input["repository"] = json!("0123"),
            "outside-repository" => input["repository"] = json!("124"),
            "stale-head" => input["anchor"]["head"] = json!("c".repeat(40)),
            _ => (),
        }
        fs::write(io.join("selection.json"), serde_json::to_vec(&input)?)?;
        let before = fs::read(io.join("gets"))?;
        call(
            &runtime,
            &credentials,
            &connection,
            "GET",
            "/api/factory/collaboration/repositories",
            &[],
        )
        .await?;
        let proof: serde_json::Value =
            serde_json::from_slice(&fs::read(io.join("port-proof.json"))?)?;
        ensure(
            proof["ok"] == false && proof["status"] == status,
            "Go/native invalid selection lost refusal",
        )?;
        if status == 0 || name == "outside-repository" {
            ensure(
                fs::read(io.join("gets"))? == before,
                "invalid or outside selector reached provider",
            )?;
        }
        cases.push(name);
    }
    // The Go observer sends these declared malformed descriptors only to its
    // own authenticated dummy port; no provider I/O is expected.
    for name in ["ordinary-large", "inline-over-bound", "inline-extra-field"] {
        fs::write(io.join("mode"), format!("inline-{name}"))?;
        fs::write(
            io.join("selection.json"),
            serde_json::to_vec(&json!({"raw_case":name}))?,
        )?;
        let before = fs::read(io.join("gets"))?;
        call(
            &runtime,
            &credentials,
            &connection,
            "GET",
            "/api/factory/collaboration/repositories",
            &[],
        )
        .await?;
        let proof: serde_json::Value =
            serde_json::from_slice(&fs::read(io.join("port-proof.json"))?)?;
        ensure(
            proof["status"] == 400 && fs::read(io.join("gets"))? == before,
            "untyped/oversized descriptor admission widened",
        )?;
        cases.push(name);
    }
    credentials.withdraw(&connection).await?;
    ensure(
        credentials.status().await?.is_empty(),
        "owned dummy account retained",
    )?;
    tokio::task::spawn_blocking(move || runtime.stop()).await??;
    ensure(
        !io.join("unexpected-request").exists()
            && fs::read_to_string(io.join("writes")).unwrap_or_else(|_| "0".into()) == "0",
        "preparation port mutated or sent undeclared provider request",
    )?;
    fs::write(
        root.join("inline-preparation-port-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_go_native_callback":true,"actual_custody":true,"actual_postgresql_startup":true,"maximum_escaped_path":4096,"read_descriptor_limit":65536,"other_reads_remain":8192,"provider_mutations":0,"product_writer_connected":false,"real_github":false,"installed_qualification":false}),
        )?,
    )?;
    println!("PASS: admitted Go/native inline preparation including maximal escaped path, narrow envelope refusal, current custody and joined stop; no product writer or provider mutations");
    Ok(())
}

pub(super) async fn inline_product(
    production: &Path,
    payload: &Path,
    helper: &Path,
    profile: &Path,
) -> Result<()> {
    use super::native_github_fixture::{call, completed};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
        ProductRequest,
    };
    use std::{os::unix::fs::PermissionsExt, process::Command, sync::Arc};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let mode = |name: &str| -> Result<()> {
        for key in ["entered", "release", "cancelled", "verification-mode"] {
            match fs::remove_file(io.join(key)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.into()),
            }
        }
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let journal = |id: &str, stage: &str| -> Result<()> {
        ensure(
            Command::new(helper)
                .arg(profile)
                .arg(id)
                .arg(stage)
                .status()?
                .success(),
            "owned inline journal assertion failed",
        )
    };
    let primary = |r: &(u16, serde_json::Value)| -> Result<()> {
        ensure(
            r.0 == 202
                && r.1["effect"] == "committed"
                && r.1["finished"] == false
                && r.1["completion"] == "needs_intervention",
            "inline primary was lost or verification preceded durable save",
        )
    };
    mode("inline-product-start")?;
    let mut runtime = NativeSupervisor::start(payload, production, profile)?;
    let mut store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(store.status().await?.is_empty(), "owned custody not empty")?;
    let account = || Account::new(Provider::Github, "https://github.com", "321");
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let mut connection = store.store(account()?, token()?).await?.connection;
    let catalogue = call(
        &runtime,
        &store,
        &connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "inline catalogue failed")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let submit =
        format!("/api/factory/collaboration/repositories/{repository}/pulls/7/inline-comments");
    let command = |id: &str| format!("{submit}/commands/{id}");
    let anchor = || json!({"base":"a".repeat(40),"head":"b".repeat(40),"path":"src/tea.md","side":"new","scope":"lines","start_line":2,"end_line":3});
    let envelope = |id: &str, a: serde_json::Value, body: &str, draft: bool| -> Result<Vec<u8>> {
        let request: ProductRequest = serde_json::from_value(
            json!({"kind":"write","path":submit,"body":{"command_id":id,"anchor":a,"body":body,"draft":draft}}),
        )?;
        Ok(request.request()?.2)
    };
    let mut cases = Vec::new();
    let mut last_id = String::new();
    let mut last_body = Vec::new();
    for (i, name) in [
        "inline-product-lines",
        "inline-product-single",
        "inline-product-file",
        "inline-binary-file",
        "inline-renamed-old",
        "inline-unicode-path",
        "inline-maximum-path",
        "inline-escaped-path",
        "inline-draft-null-comment-id",
        "inline-draft-big-id",
        "inline-draft-request-id",
    ]
    .into_iter()
    .enumerate()
    {
        mode(name)?;
        let id = format!("{:032x}", 100 + i);
        let mut a = anchor();
        match name {
            "inline-product-single" => a["start_line"] = json!(3),
            "inline-product-file" | "inline-binary-file" => {
                a["scope"] = json!("file");
                a["start_line"] = json!(0);
                a["end_line"] = json!(0)
            }
            "inline-renamed-old" => {
                a["side"] = json!("old");
                a["path"] = json!("src/old.md")
            }
            "inline-unicode-path" => a["path"] = json!("src/茶/π.md"),
            "inline-maximum-path" => a["path"] = json!(format!("p/{}", "x".repeat(4094))),
            "inline-escaped-path" => a["path"] = json!(format!("p/{}", "\u{0001}".repeat(4094))),
            _ => (),
        }
        let body = if name == "inline-escaped-path" {
            "\u{0001}".repeat(65536)
        } else {
            "Exact owned inline writing 🍵\n[link](relative.md)".into()
        };
        let bytes = envelope(&id, a, &body, true)?;
        let before = writes()?;
        let r = call(&runtime, &store, &connection, "POST", &submit, &bytes).await?;
        primary(&r)?;
        journal(&id, "primary")?;
        let r = call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?;
        completed(&r, "committed")?;
        journal(&id, "complete")?;
        let r = call(&runtime, &store, &connection, "POST", &submit, &bytes).await?;
        completed(&r, "committed")?;
        journal(&id, "retained")?;
        ensure(
            writes()? == before + 1,
            "inline replay duplicated native effect",
        )?;
        let public = serde_json::to_string(&r.1)?;
        ensure(
            !public.contains("sha256")
                && !public.contains("factory-declared")
                && !public.contains("C_new")
                && !public.contains("provider_intent"),
            "inline HTTP leaked private evidence",
        )?;
        cases.push(name.to_owned());
        last_id = id;
        last_body = bytes;
    }
    for (i, verification) in [
        "summary",
        "old-writing",
        "new-writing",
        "new-identity",
        "published-child",
        "missing-new",
        "review-identity",
    ]
    .into_iter()
    .enumerate()
    {
        mode(&format!("inline-product-verify-{verification}"))?;
        let id = format!("{:032x}", 200 + i);
        let bytes = envelope(&id, anchor(), "Exact inline preservation writing", true)?;
        let before = writes()?;
        primary(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
        journal(&id, "primary")?;
        fs::write(io.join("verification-mode"), verification)?;
        let r = call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?;
        ensure(
            r.0 == 200 && r.1["effect"] == "committed" && r.1["finished"] == false,
            "failed inline preservation erased primary or completed",
        )?;
        journal(&id, "unfinished")?;
        fs::remove_file(io.join("verification-mode"))?;
        completed(
            &call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?,
            "committed",
        )?;
        journal(&id, "complete")?;
        ensure(
            writes()? == before + 1,
            "preservation retry sent another effect",
        )?;
        cases.push(format!("verification-{verification}"));
    }
    for (i, name) in [
        "inline-draft-summary-change",
        "inline-draft-count-change",
        "inline-draft-published",
    ]
    .into_iter()
    .enumerate()
    {
        mode(name)?;
        let id = format!("{:032x}", 300 + i);
        let bytes = envelope(&id, anchor(), "Causal discrepancy remains unfinished", true)?;
        let before = writes()?;
        primary(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
        journal(&id, "primary")?;
        let r = call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?;
        ensure(
            r.0 == 200 && r.1["effect"] == "committed" && r.1["finished"] == false,
            "causal discrepancy was erased by current facts",
        )?;
        journal(&id, "unfinished")?;
        ensure(writes()? == before + 1, "causal discrepancy resent")?;
        cases.push(name.into());
    }
    mode("inline-draft-loss")?;
    let lost_id = "dddddddddddddddddddddddddddddddd";
    let lost = envelope(
        lost_id,
        anchor(),
        "Lost inline reply cannot be attributed",
        true,
    )?;
    let before = writes()?;
    let r = call(&runtime, &store, &connection, "POST", &submit, &lost).await?;
    ensure(
        r.0 == 202 && r.1["effect"] == "indeterminate" && r.1["finished"] == false,
        "lost inline reply became known or no-effect",
    )?;
    let queries = fs::read(io.join("queries"))?;
    for action in ["GET", "POST"] {
        let target = if action == "GET" {
            command(lost_id)
        } else {
            submit.clone()
        };
        let body = if action == "GET" { &[][..] } else { &lost };
        let r = call(&runtime, &store, &connection, action, &target, body).await?;
        ensure(
            matches!(r.0, 200 | 202)
                && r.1["effect"] == "indeterminate"
                && r.1["finished"] == false,
            "current matching inline was attributed or replayed",
        )?;
    }
    ensure(
        writes()? == before + 1 && fs::read(io.join("queries"))? == queries,
        "unknown inline path searched matching content or resent",
    )?;
    cases.push("lost-reply-and-no-current-attribution".into());
    for (i, name) in ["inline-draft-pause", "inline-product-late-no-effect"]
        .into_iter()
        .enumerate()
    {
        mode(name)?;
        let id = format!("{:032x}", 400 + i);
        let bytes = envelope(
            &id,
            anchor(),
            "Original inline survives a cancellation race",
            true,
        )?;
        let before = writes()?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let target = submit.clone();
        let flight = tokio::spawn(async move { request.request("POST", &target, &bytes).await });
        let until = Instant::now() + Duration::from_secs(6);
        while !io.join("entered").exists() {
            ensure(
                Instant::now() < until,
                "inline cancellation race did not reach declared phase",
            )?;
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        journal(&id, "reserved")?;
        let cancelled = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &format!("{}/cancel", command(&id)),
            b"{}",
        )
        .await?;
        ensure(
            cancelled.0 == 202
                && cancelled.1["cancellation"] == "indeterminate"
                && cancelled.1["finished"] == false,
            "reserved inline cancellation invented a tombstone",
        )?;
        fs::write(io.join("release"), b"release")?;
        let raw = flight.await??;
        let r = (
            raw.status,
            serde_json::from_slice::<serde_json::Value>(&raw.body)?,
        );
        ensure(
            r.0 == if i == 0 { 202 } else { 200 }
                && r.1["cancellation"] == "indeterminate"
                && r.1["finished"] == (i != 0),
            "late primary erased cancellation uncertainty",
        )?;
        if i == 0 {
            ensure(
                r.1["effect"] == "committed",
                "late causal inline primary lost",
            )?;
            journal(&id, "primary")?;
            let r = call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?;
            ensure(
                r.0 == 200
                    && r.1["effect"] == "committed"
                    && r.1["completion"] == "complete"
                    && r.1["cancellation"] == "indeterminate"
                    && r.1["finished"] == true,
                "late inline preservation erased primary or cancellation",
            )?;
            journal(&id, "cancel-complete")?;
            ensure(
                writes()? == before + 1,
                "cancelled committed inline duplicated",
            )?;
        } else {
            ensure(
                r.1["effect"] == "not_committed"
                    && r.1["completion"] == "complete"
                    && writes()? == before,
                "late known no-send inline was lost or mutated",
            )?;
            journal(&id, "cancel-no-effect")?;
        }
        cases.push(format!("late-primary-after-cancel-{i}"));
    }
    mode("inline-product-parallel")?;
    let parallel_id = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
    let bytes = envelope(
        parallel_id,
        anchor(),
        "One immutable concurrent inline",
        true,
    )?;
    let before = writes()?;
    let (left, right) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &bytes),
        call(&runtime, &store, &connection, "POST", &submit, &bytes)
    );
    let left = left?;
    let right = right?;
    ensure(
        matches!(left.0, 200 | 202) && matches!(right.0, 200 | 202) && writes()? == before + 1,
        "parallel inline lost journal admission or duplicated",
    )?;
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(parallel_id),
            &[],
        )
        .await?,
        "committed",
    )?;
    cases.push("parallel-one-shot".into());
    mode("inline-product-conflict")?;
    let mut changed: serde_json::Value = serde_json::from_slice(&last_body)?;
    changed["body"] = json!("Conflicting writing");
    let before = writes()?;
    ensure(
        call(
            &runtime,
            &store,
            &connection,
            "POST",
            &submit,
            &serde_json::to_vec(&changed)?,
        )
        .await?
        .0 == 409
            && writes()? == before,
        "changed inline reused saved command",
    )?;
    cases.push("conflicting-writing".into());
    mode("inline-product-invalid-http")?;
    for i in 0..6 {
        let mut bad: serde_json::Value = serde_json::from_slice(&last_body)?;
        bad["command_id"] = json!(format!("{:032x}", 500 + i));
        match i {
            0 => bad["body"] = serde_json::Value::Null,
            1 => bad["draft"] = serde_json::Value::Null,
            2 => bad["anchor"]["head"] = serde_json::Value::Null,
            3 => bad["provider_intent"] = json!("not admitted from HTTP"),
            4 => bad["anchor"]["path"] = json!("../outside"),
            _ => bad["anchor"]["start_line"] = json!("2"),
        }
        let before = writes()?;
        ensure(
            call(
                &runtime,
                &store,
                &connection,
                "POST",
                &submit,
                &serde_json::to_vec(&bad)?,
            )
            .await?
            .0 == 400
                && writes()? == before,
            "invalid inline HTTP shape reached an effect",
        )?;
    }
    cases.push("six-invalid-http-shapes".into());
    mode("inline-product-capacity")?;
    let capacity = envelope(
        "cccccccccccccccccccccccccccccccc",
        anchor(),
        "No room for a complete added-member observation",
        true,
    )?;
    let before = writes()?;
    ensure(
        call(&runtime, &store, &connection, "POST", &submit, &capacity)
            .await?
            .0
            == 503
            && writes()? == before,
        "full pending review sent an unobservable added member",
    )?;
    cases.push("existing-thousand-member-capacity".into());
    // Immediate publication now has connected coverage with pending preservation.
    store.withdraw(&connection).await?;
    store.retire();
    runtime.stop()?;
    runtime = NativeSupervisor::start(payload, production, profile)?;
    store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "withdrawal did not survive restart",
    )?;
    connection = store.store(account()?, token()?).await?.connection;
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(&last_id),
            &[],
        )
        .await?,
        "committed",
    )?;
    journal(&last_id, "retained")?;
    cases.push("withdrawal-reconsent-restart".into());
    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?,
            Token::new(b"factory-declared-other".to_vec())?,
        )
        .await?;
    let before = writes()?;
    ensure(
        call(
            &runtime,
            &store,
            &other.connection,
            "GET",
            &command(&last_id),
            &[],
        )
        .await?
        .0 == 404
            && call(
                &runtime,
                &store,
                &other.connection,
                "POST",
                &submit,
                &last_body,
            )
            .await?
            .0 == 409
            && writes()? == before,
        "other account adopted inline capture",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "composed inline sent undeclared provider request",
    )?;
    store.withdraw(&connection).await?;
    ensure(store.status().await?.is_empty(), "owned grants remain")?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("inline-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"provider_mutations":writes()?,"actual_native_callback_custody":true,"actual_go_http_store":true,"causal_primary_before_preservation":true,"original_expiry_qualified":false,"dummy_cancellation_races":true,"actual_provider":false,"capabilities_enabled":false,"installed_qualified":false}),
        )?,
    )?;
    println!("PASS: composed journaled inline draft creation retains exact primary before preservation, one-shot replay/uncertainty, full writing/selection and custody/restart; dummy-only provider");
    Ok(())
}

pub(super) async fn inline_preservation(profile: &Path) -> Result<()> {
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::Read;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), format!("comments-{name}"))?;
        Ok(())
    };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let query = |member: &str| Read::InlinePreservation {
        repository: "123".into(),
        number: 7,
        review_node_id: "REV_owned".into(),
        comment_node_id: member.into(),
    };
    let sha = |s: &[u8]| -> String {
        Sha256::digest(s)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "three",
        "reordered",
        "missing",
        "empty",
        "maximum",
        "max-control",
        "max-unicode",
        "unknown-id",
        "null-author",
        "null-author-node",
        "null-commits",
        "old-original",
        "big-ids",
    ] {
        mode(if name == "missing" { "three" } else { name })?;
        let member = if name == "missing" {
            "PC_absent"
        } else {
            "PC_1"
        };
        let v = client
            .observe(token()?, "321", &query(member))
            .await
            .map_err(|f| format!("preservation {name}: {} {}", f.status, f.code))?;
        let p = &v["preservation"];
        let snapshot = &p["comments_snapshot"];
        let excluding = &snapshot["excluding"];
        let count = if name == "maximum" {
            1000
        } else if name == "empty" {
            0
        } else {
            3
        };
        let found = name != "missing" && count > 0;
        let body = |i: u64| match name {
            "max-control" => "\u{0001}".repeat(65536),
            "max-unicode" => "🍵".repeat(16384),
            "maximum" => "x".into(),
            _ => format!("Exact pending comment {i} 🍵\n[link](relative.md)"),
        };
        let mut expected = Vec::new();
        for i in 0..count {
            if !found || i != 1 {
                expected.push(
                    json!({"node_id":format!("PC_{i}"),"body_sha256":sha(body(i).as_bytes())}),
                );
            }
        }
        expected.sort_by(|a, b| a["node_id"].as_str().cmp(&b["node_id"].as_str()));
        ensure(
            v["kind"] == "inline_preservation"
                && v["account"]["external_id"] == "321"
                && p["repository"] == "123"
                && p["pending"]["node_id"] == "REV_owned"
                && p["condition"] == "bounded_observation"
                && snapshot["complete"] == true
                && snapshot["count"] == count
                && excluding["found"] == found
                && excluding["remaining_count"] == expected.len()
                && excluding["identity_writing_sha256"] == sha(&serde_json::to_vec(&expected)?),
            "current pending exclusion/writing facts differ from independent members",
        )?;
        if found {
            let m = &excluding["member"];
            ensure(
                m["node_id"] == member
                    && m["body_sha256"] == sha(body(1).as_bytes())
                    && m.get("body").is_none(),
                "selected member body digest lost or raw writing exposed",
            )?;
            if name == "unknown-id" {
                ensure(
                    m["external_id"].is_null(),
                    "unknown numeric member ID invented",
                )?;
            }
            if name == "null-author" {
                ensure(m["author"].is_null(), "unknown member author invented")?;
            }
            if name == "null-commits" {
                ensure(
                    m["commit"].is_null() && m["original_commit"].is_null(),
                    "unknown original/member commits invented",
                )?;
            }
            if name == "old-original" {
                ensure(
                    m["original_commit"] == "c".repeat(40),
                    "historical member commit replaced by current head",
                )?;
            }
        } else {
            ensure(
                excluding["member"].is_null(),
                "absent selected member invented",
            )?;
        }
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&v)?)?;
        cases.push(name);
    }
    for (name, status) in [
        ("absent", 409),
        ("root-gone", 409),
        ("wrong-root", 409),
        ("wrong-owner", 409),
        ("submitted", 409),
        ("wrong-pull", 409),
        ("wrong-repo", 409),
        ("wrong-head", 409),
        ("count-change", 409),
        ("count-mismatch", 409),
        ("too-many", 413),
        ("duplicate-node", 502),
        ("duplicate-id", 502),
        ("cursor-cycle", 502),
        ("empty-continuation", 502),
        ("wrong-comment-review", 502),
        ("wrong-comment-pull", 502),
        ("wrong-comment-repo", 502),
        ("published-comment", 502),
        ("pending-changed", 409),
        ("partial-errors", 502),
        ("aggregate-bytes", 413),
        ("denied", 403),
        ("deadline", 504),
    ] {
        mode(name)?;
        match client.observe(token()?, "321", &query("PC_1")).await {
            Err(f) if f.status == status => (),
            Err(f) => {
                return Err(format!(
                    "preservation {name}: expected {status}, got {} {}",
                    f.status, f.code
                )
                .into())
            }
            Ok(_) => return Err(format!("unproved pending preservation {name} accepted").into()),
        };
        cases.push(name);
    }
    mode("three")?;
    for i in 0..5 {
        let mut q = query("PC_1");
        if let Read::InlinePreservation {
            repository,
            number,
            review_node_id,
            comment_node_id,
        } = &mut q
        {
            match i {
                0 => repository.push('x'),
                1 => *number = 0,
                2 => review_node_id.clear(),
                3 => comment_node_id.clear(),
                _ => *comment_node_id = review_node_id.clone(),
            }
        }
        let before = fs::read(io.join("gets"))?;
        ensure(
            matches!(client.observe(token()?,"321",&q).await,Err(f)if f.status==400)
                && fs::read(io.join("gets"))? == before,
            "invalid preservation selector made I/O",
        )?;
    }
    mode("pause")?;
    let task_client = client.clone();
    let q = query("PC_1");
    let task = tokio::spawn(async move {
        task_client
            .observe(
                Token::new(b"factory-declared-review-preparation".to_vec()).unwrap(),
                "321",
                &q,
            )
            .await
    });
    let until = Instant::now() + Duration::from_secs(5);
    while !io.join("entered").exists() {
        ensure(
            Instant::now() < until,
            "preservation retirement did not enter reader",
        )?;
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let stopped = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        stopped.elapsed() < Duration::from_secs(3) && task.await?.is_err(),
        "preservation retirement failed to join",
    )?;
    let before = fs::read(io.join("gets"))?;
    ensure(
        client
            .observe(token()?, "321", &query("PC_1"))
            .await
            .is_err()
            && fs::read(io.join("gets"))? == before,
        "stopped preservation reader made I/O",
    )?;
    ensure(
        !io.join("unexpected-request").exists()
            && fs::read_to_string(io.join("writes")).unwrap_or_else(|_| "0".into()) == "0",
        "preservation reader mutated or sent undeclared request",
    )?;
    fs::write(
        root.join("inline-preservation-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_selectors":5,"maximum_members":1000,"aggregate_bytes":8*1024*1024,"joined_retirement":true,"provider_mutations":0,"current_observation_only":true,"causal_attribution":false,"product_consumer_connected":false,"real_github":false}),
        )?,
    )?;
    println!("PASS: bounded exact own pending/member exclusion, independent remaining-writing digest, nullable/original facts and joined retirement; current facts grant no causal attribution or completed preservation");
    Ok(())
}

pub(super) async fn inline_draft(profile: &Path) -> Result<()> {
    use soda_desktop_runtime::github_provider::{CreateOutcome, InlineAnchor, InlineDraft, Read};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let anchor = || InlineAnchor {
        base: "a".repeat(40),
        head: "b".repeat(40),
        path: "src/tea.md".into(),
        side: "new".into(),
        scope: "lines".into(),
        start_line: 2,
        end_line: 3,
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "inline-draft-lines",
        "inline-draft-single",
        "inline-draft-file",
        "inline-binary-file",
        "inline-renamed-old",
        "inline-unicode-path",
        "inline-maximum-path",
        "inline-escaped-path",
        "inline-draft-max-writing",
        "inline-draft-null-comment-id",
        "inline-draft-big-id",
        "inline-draft-request-id",
        "inline-draft-summary-change",
        "inline-draft-count-change",
        "inline-draft-published",
    ] {
        mode(name)?;
        let mut a = anchor();
        match name {
            "inline-draft-single" => a.start_line = 3,
            "inline-draft-file" | "inline-binary-file" => {
                a.scope = "file".into();
                a.start_line = 0;
                a.end_line = 0;
            }
            "inline-renamed-old" => {
                a.side = "old".into();
                a.path = "src/old.md".into();
            }
            "inline-unicode-path" => a.path = "src/茶/π.md".into(),
            "inline-maximum-path" => a.path = format!("p/{}", "x".repeat(4094)),
            "inline-escaped-path" => a.path = format!("p/{}", "\u{0001}".repeat(4094)),
            _ => (),
        }
        let observed = client
            .observe(
                token()?,
                "321",
                &Read::InlinePreparation {
                    repository: "123".into(),
                    number: 7,
                    anchor: a.clone(),
                },
            )
            .await
            .map_err(|f| format!("draft preparation {name}: {} {}", f.status, f.code))?;
        let p = &observed["preparation"];
        let q = InlineDraft {
            repository: "123".into(),
            repository_node_id: "R_123".into(),
            pull_id: "700".into(),
            pull_node_id: "P_700".into(),
            number: 7,
            anchor: a,
            review_node_id: "REV_owned".into(),
            preparation_sha256: p["sha256"]
                .as_str()
                .ok_or("preparation digest absent")?
                .into(),
            body: if matches!(name, "inline-draft-max-writing" | "inline-escaped-path") {
                "\u{0001}".repeat(65536)
            } else {
                "Exact inline writing 🍵\n[link](relative.md)".into()
            },
        };
        let before = writes()?;
        let receipt = match client.append_pending_thread(token()?, "321", &q).await {
            CreateOutcome::Created(v) => v,
            CreateOutcome::NotSent(f) => {
                return Err(format!("draft {name} not sent: {} {}", f.status, f.code).into())
            }
            CreateOutcome::Indeterminate(f) => {
                return Err(format!("draft {name} uncertain: {} {}", f.status, f.code).into())
            }
        };
        ensure(
            writes()? == before + 1
                && receipt["thread_node_id"] == "T_new"
                && receipt["comment"]["node_id"] == "C_new"
                && receipt["review"]["node_id"] == "REV_owned"
                && receipt["preparation_sha256"] == p["sha256"]
                && receipt["selection_sha256"] == p["selection_sha256"]
                && receipt["expected_pending"]["comments_count"] == 3
                && receipt["expected_pending"]["comments_sha256"]
                    == p["comments_snapshot"]["identity_writing_sha256"]
                && receipt["preservation_verification"] == "required"
                && serde_json::to_vec(&receipt)?.len() <= 4096,
            "causal inline receipt lost exact identity/writing or claimed preservation",
        )?;
        if name == "inline-draft-published" {
            ensure(
                receipt["review"]["state"] == "commented"
                    && receipt["comment"]["state"] == "submitted"
                    && !receipt["comment"]["published_at"].is_null(),
                "unexpected publication erased known creation or invented privacy",
            )?;
        }
        if name == "inline-draft-summary-change" {
            ensure(
                receipt["review"]["body_sha256"] != receipt["expected_pending"]["body_sha256"],
                "concurrent summary discrepancy erased",
            )?;
        }
        if name == "inline-draft-count-change" {
            ensure(
                receipt["review"]["comments_count"] == 9,
                "concurrent child count discrepancy erased",
            )?;
        }
        fs::write(
            root.join(format!("{name}-receipt.json")),
            serde_json::to_vec(&receipt)?,
        )?;
        cases.push(name);
    }
    mode("inline-draft-invalid")?;
    let observed = client
        .observe(
            token()?,
            "321",
            &Read::InlinePreparation {
                repository: "123".into(),
                number: 7,
                anchor: anchor(),
            },
        )
        .await
        .map_err(|_| "declared preparation failed")?;
    let saved = InlineDraft {
        repository: "123".into(),
        repository_node_id: "R_123".into(),
        pull_id: "700".into(),
        pull_node_id: "P_700".into(),
        number: 7,
        anchor: anchor(),
        review_node_id: "REV_owned".into(),
        preparation_sha256: observed["preparation"]["sha256"]
            .as_str()
            .ok_or("digest missing")?
            .into(),
        body: "Exact writing".into(),
    };
    for i in 0..12 {
        let mut q = saved.clone();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.review_node_id.clear(),
            2 => q.body.clear(),
            3 => q.body = " ".into(),
            4 => q.body = "\0".into(),
            5 => q.body = "x".repeat(65537),
            6 => q.anchor.end_line = i32::MAX as u64 + 1,
            7 => q.anchor.path = "../other".into(),
            8 => q.preparation_sha256 = "x".repeat(64),
            9 => q.number = 0,
            10 => q.pull_id = "invalid".into(),
            _ => q.anchor.scope = "other".into(),
        };
        let before = fs::read(io.join("gets"))?;
        ensure(
            matches!(client.append_pending_thread(token()?,"321",&q).await,CreateOutcome::NotSent(f)if f.status==400)
                && fs::read(io.join("gets"))? == before,
            "invalid draft descriptor made provider I/O",
        )?;
    }
    for name in [
        "inline-draft-stale-preparation",
        "inline-draft-wrong-pending",
        "inline-draft-wrong-parent",
        "inline-no-pending",
    ] {
        mode(name)?;
        let mut q = saved.clone();
        let current = client
            .observe(
                token()?,
                "321",
                &Read::InlinePreparation {
                    repository: "123".into(),
                    number: 7,
                    anchor: anchor(),
                },
            )
            .await
            .map_err(|_| "changed-target preparation unavailable")?;
        q.preparation_sha256 = current["preparation"]["sha256"]
            .as_str()
            .ok_or("changed-target digest absent")?
            .into();
        match name {
            "inline-draft-stale-preparation" => q.preparation_sha256 = "0".repeat(64),
            "inline-draft-wrong-pending" => q.review_node_id = "REV_other".into(),
            "inline-draft-wrong-parent" => q.pull_node_id = "P_other".into(),
            _ => (),
        };
        let before = writes()?;
        ensure(
            matches!(client.append_pending_thread(token()?,"321",&q).await,CreateOutcome::NotSent(f)if f.status==409)
                && writes()? == before,
            "changed pending/preparation sent a mutation",
        )?;
        cases.push(name);
    }
    for name in [
        "inline-draft-loss",
        "inline-draft-denied",
        "inline-draft-errors",
        "inline-draft-wrong-thread",
        "inline-draft-wrong-side",
        "inline-draft-wrong-range",
        "inline-draft-wrong-start",
        "inline-draft-wrong-path",
        "inline-draft-wrong-repo",
        "inline-draft-wrong-pull",
        "inline-draft-wrong-review",
        "inline-draft-wrong-body",
        "inline-draft-wrong-author",
        "inline-draft-wrong-commit",
        "inline-draft-wrong-original",
        "inline-draft-reply",
        "inline-draft-extra-comment",
        "inline-draft-pagination",
        "inline-draft-bad-date",
    ] {
        mode(name)?;
        let before = writes()?;
        ensure(
            matches!(
                client.append_pending_thread(token()?, "321", &saved).await,
                CreateOutcome::Indeterminate(_)
            ) && writes()? == before + 1,
            "lost/misbound reply became no-effect or was retried",
        )?;
        cases.push(name);
    }
    mode("comments-maximum")?;
    let before = writes()?;
    ensure(
        matches!(client.append_pending_thread(token()?, "321", &saved).await, CreateOutcome::NotSent(f) if f.status == 413 && f.code == "github_inline_pending_capacity")
            && writes()? == before,
        "native full pending review admitted an unobservable added member",
    )?;
    cases.push("native-thousand-member-capacity");
    mode("inline-draft-parallel")?;
    let start = Instant::now();
    let before = writes()?;
    let left = client.clone();
    let right = client.clone();
    let lq = saved.clone();
    let rq = saved.clone();
    let (l, r) = tokio::join!(
        left.append_pending_thread(token()?, "321", &lq),
        right.append_pending_thread(token()?, "321", &rq)
    );
    ensure(
        matches!(l, CreateOutcome::Created(_))
            && matches!(r, CreateOutcome::Created(_))
            && writes()? == before + 2
            && start.elapsed() >= Duration::from_secs(1),
        "cloned inline handles lost shared mutation pacing",
    )?;
    mode("inline-draft-pause")?;
    let task_client = client.clone();
    let q = saved.clone();
    let task = tokio::spawn(async move {
        task_client
            .append_pending_thread(
                Token::new(b"factory-declared-review-preparation".to_vec()).unwrap(),
                "321",
                &q,
            )
            .await
    });
    let until = Instant::now() + Duration::from_secs(5);
    while !io.join("entered").exists() {
        ensure(
            Instant::now() < until,
            "inline retirement did not reach mutation",
        )?;
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let stopped = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        stopped.elapsed() < Duration::from_secs(3)
            && matches!(task.await?, CreateOutcome::Indeterminate(_)),
        "retirement failed to join in-flight inline effect",
    )?;
    let before = writes()?;
    ensure(
        matches!(
            client.append_pending_thread(token()?, "321", &saved).await,
            CreateOutcome::NotSent(_)
        ) && writes()? == before,
        "stopped inline producer dispatched",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "inline effect sent undeclared provider request",
    )?;
    fs::write(
        root.join("inline-draft-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_descriptors":12,"provider_mutations":writes()?,"joined_retirement":true,"causal_primary_separate_from_preservation":true,"product_consumer_connected":false,"real_github":false,"privacy_qualified":false}),
        )?,
    )?;
    println!("PASS: exact own pending inline causal creation, writing/selection/identity binding, uncertainty and joined retirement; preservation/privacy still require separate verification");
    Ok(())
}

pub(super) async fn inline_preparation(profile: &Path) -> Result<()> {
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::{InlineAnchor, Read};
    let sha = |s: &str| -> String {
        Sha256::digest(s.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    };
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), format!("inline-{name}"))?;
        Ok(())
    };
    let gets = || -> Result<Vec<u8>> { Ok(fs::read(io.join("gets")).unwrap_or_default()) };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let anchor = || InlineAnchor {
        base: "a".repeat(40),
        head: "b".repeat(40),
        path: "src/tea.md".into(),
        side: "new".into(),
        scope: "lines".into(),
        start_line: 2,
        end_line: 3,
    };
    let query = |anchor: InlineAnchor| Read::InlinePreparation {
        repository: "123".into(),
        number: 7,
        anchor,
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "new-lines",
        "old-lines",
        "new-file",
        "old-file",
        "renamed-old",
        "renamed-new",
        "deleted-old",
        "added-new",
        "copied-new",
        "merge-base",
        "no-pending",
        "binary-file",
        "unknown-metadata",
        "unicode-path",
        "maximum-path",
        "escaped-path",
        "no-newline",
        "control-content",
    ] {
        mode(name)?;
        let mut a = anchor();
        if matches!(
            name,
            "old-lines" | "old-file" | "renamed-old" | "deleted-old"
        ) {
            a.side = "old".into()
        }
        if matches!(name, "new-file" | "old-file" | "binary-file") {
            a.scope = "file".into();
            a.start_line = 0;
            a.end_line = 0
        }
        match name {
            "renamed-old" => a.path = "src/old.md".into(),
            "unicode-path" => a.path = "src/茶/π.md".into(),
            "maximum-path" => a.path = format!("p/{}", "x".repeat(4094)),
            "escaped-path" => a.path = format!("p/{}", "\u{0001}".repeat(4094)),
            "no-newline" => {
                a.start_line = 4;
                a.end_line = 4
            }
            _ => (),
        }
        let v = client
            .observe(token()?, "321", &query(a.clone()))
            .await
            .map_err(|f| format!("inline preparation {name}: {} {}", f.status, f.code))?;
        let p = &v["preparation"];
        ensure(
            v["kind"] == "inline_preparation"
                && v["account"]["external_id"] == "321"
                && p["repository"] == "123"
                && p["repository_node_id"] == "R_123"
                && p["pull"]["external_id"] == "700"
                && p["selection"] == serde_json::to_value(&a)?
                && p["diff"]["scope"]["base"] == a.base
                && p["diff"]["scope"]["head"] == a.head
                && p["diff"]["catalogue"]["complete"] == true
                && p["diff"]["catalogue"]["count"] == 13
                && p["condition"] == "bounded_observation",
            "inline preparation lost exact account/selection or complete catalogue",
        )?;
        if name == "merge-base" {
            ensure(
                p["diff"]["scope"]["pull_base"] == "c".repeat(40)
                    && p["selection"]["base"] == "a".repeat(40),
                "branch tip was substituted for immutable merge base",
            )?
        }
        if name == "no-pending" {
            ensure(
                p["pending"].is_null() && p["comments_snapshot"].is_null(),
                "absent pending review became adopted",
            )?
        } else {
            ensure(
                p["pending"]["node_id"] == "REV_owned"
                    && p["comments_snapshot"]["complete"] == true
                    && p["comments_snapshot"]["count"] == 3,
                "current own pending context lost",
            )?
        }
        if name == "unknown-metadata" {
            ensure(
                p["archived"].is_null() && p["pull"]["draft"].is_null(),
                "unknown metadata became permission",
            )?
        }
        let file = &p["diff"]["file"];
        if name == "renamed-old" {
            ensure(
                file["old_path"] == "src/old.md" && file["new_path"] == "src/tea.md",
                "rename path mapping was inferred from one name",
            )?
        }
        if a.scope == "file" {
            ensure(
                p["diff"]["selection"]["scope"] == "file" && p["diff"]["selection"]["count"] == 0,
                "file selection invented line positions",
            )?
        } else {
            // Explicit fixture positions/text are independent of the production parser.
            let rows = if name == "no-newline" {
                json!([{"line":4,"kind":"context","old_line":4,"new_line":4,"text_sha256":sha("four")}])
            } else if name == "added-new" {
                json!([{"line":2,"kind":"added","old_line":null,"new_line":2,"text_sha256":sha("new2\n")},{"line":3,"kind":"added","old_line":null,"new_line":3,"text_sha256":sha("three\n")}])
            } else if name == "deleted-old" {
                json!([{"line":2,"kind":"deleted","old_line":2,"new_line":null,"text_sha256":sha("old2\n")},{"line":3,"kind":"deleted","old_line":3,"new_line":null,"text_sha256":sha("three\n")}])
            } else if a.side == "old" {
                json!([{"line":2,"kind":"deleted","old_line":2,"new_line":null,"text_sha256":sha("old2\n")},{"line":3,"kind":"context","old_line":3,"new_line":3,"text_sha256":sha("three\n")}])
            } else {
                json!([{"line":2,"kind":"added","old_line":null,"new_line":2,"text_sha256":sha(if name=="control-content"{"new2 \u{0001}🍵\n"}else{"new2\n"})},{"line":3,"kind":"context","old_line":3,"new_line":3,"text_sha256":sha("three\n")}])
            };
            ensure(
                p["diff"]["selection"]["sha256"] == sha(&serde_json::to_string(&rows)?)
                    && p["diff"]["selection"]["count"] == a.end_line - a.start_line + 1,
                "inline selection content/position digest differs from independent facts",
            )?;
        }
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&v)?)?;
        cases.push(name.to_owned());
    }
    for (name, status) in [
        ("stale-head", 409),
        ("stale-base", 409),
        ("missing-file", 409),
        ("missing-line", 409),
        ("wrong-side", 409),
        ("gap-range", 409),
        ("truncated-patch", 409),
        ("unavailable-patch", 409),
        ("unknown-change", 409),
        ("unsafe-file-path", 502),
        ("duplicate-file", 409),
        ("ambiguous-copy", 409),
        ("file-count-change", 409),
        ("pagination-link", 502),
        ("pending-change", 409),
        ("comment-change", 409),
        ("base-moves-after-files", 409),
        ("account-change", 409),
        ("query-denied", 403),
        ("too-many-files", 413),
        ("aggregate-bytes", 413),
        ("deadline", 504),
    ] {
        mode(name)?;
        let mut a = anchor();
        match name {
            "stale-head" => a.head = "c".repeat(40),
            "stale-base" => a.base = "c".repeat(40),
            "missing-file" => a.path = "missing.md".into(),
            "missing-line" => {
                a.start_line = 6;
                a.end_line = 6
            }
            "wrong-side" => a.side = "old".into(),
            "gap-range" => {
                a.start_line = 3;
                a.end_line = 10
            }
            "ambiguous-copy" => {
                a.side = "old".into();
                a.path = "src/source.md".into()
            }
            _ => (),
        }
        match client.observe(token()?, "321", &query(a)).await {
            Err(f) if f.status == status => (),
            Err(f) => {
                return Err(format!(
                    "inline {name} expected {status}, got {} {}",
                    f.status, f.code
                )
                .into())
            }
            Ok(_) => return Err(format!("unproved inline selection {name} accepted").into()),
        };
        cases.push(name.into());
    }
    let mut invalid = Vec::new();
    for path in [
        "",
        "/file",
        "../file",
        "a/../file",
        "a//file",
        "a/./file",
        "file/",
        "bad\0path",
    ] {
        let mut a = anchor();
        a.path = path.into();
        invalid.push(a)
    }
    for i in 0..8 {
        let mut a = anchor();
        match i {
            0 => a.base = "invalid".into(),
            1 => a.head = "B".repeat(40),
            2 => a.path = "x".repeat(4097),
            3 => a.side = "unknown".into(),
            4 => a.scope = "unknown".into(),
            5 => a.scope = "file".into(),
            6 => a.start_line = 0,
            _ => a.end_line = 9_007_199_254_740_992,
        };
        invalid.push(a)
    }
    let before = gets()?;
    for a in &invalid {
        ensure(
            client
                .observe(token()?, "321", &query(a.clone()))
                .await
                .is_err()
                && gets()? == before,
            "invalid inline selector made provider I/O",
        )?
    }
    for q in [
        Read::InlinePreparation {
            repository: "0123".into(),
            number: 7,
            anchor: anchor(),
        },
        Read::InlinePreparation {
            repository: "123".into(),
            number: 0,
            anchor: anchor(),
        },
    ] {
        ensure(
            client.observe(token()?, "321", &q).await.is_err() && gets()? == before,
            "invalid inline parent scope made provider I/O",
        )?
    }
    mode("pause")?;
    let q = query(anchor());
    let pending = client.observe(token()?, "321", &q);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    owner.stop()?;
    ensure(
        pending.await.is_err() && started.elapsed() < Duration::from_secs(2),
        "retired inline reader delivered old facts or failed to join",
    )?;
    let before = gets()?;
    ensure(
        client.observe(token()?, "321", &q).await.is_err() && gets()? == before,
        "stopped inline reader performed I/O",
    )?;
    ensure(
        !io.join("writes").exists() && !io.join("unexpected-request").exists(),
        "inline preparation mutated provider or sent undeclared request",
    )?;
    fs::write(
        root.join("inline-preparation-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"invalid_selectors":invalid.len()+2,"provider_mutations":0,"retirement":true,"actual_provider":false,"product_consumer_connected":false,"original_provider_anchor_inferred":false}),
        )?,
    )?;
    println!("PASS: bounded current inline selection/pending preparation, independent diff position/writing digest, catalogue/change/bounds refusal and retirement; no mutations or original-anchor inference");
    Ok(())
}

pub(super) async fn producer(profile: &Path) -> Result<()> {
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let token = || Token::new(b"factory-declared-reviewers".to_vec());
    let request = || ReviewerRead {
        repository: "123".into(),
        number: 7,
        target: Some(ReviewerTarget::User {
            external_id: "456".into(),
        }),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "user",
        "team",
        "no-target",
        "user-not-requested",
        "team-not-requested",
        "hidden-absent",
        "hidden-present",
        "empty",
        "user-id-unknown",
        "team-id-unknown",
        "big-id",
        "user-rename",
        "team-rename",
        "other-kind",
    ] {
        mode(name)?;
        let mut q = request();
        if name.starts_with("team") {
            q.target = Some(ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            });
        }
        if name == "big-id" {
            q.target = Some(ReviewerTarget::User {
                external_id: "5000000000".into(),
            });
        }
        if name == "no-target" {
            q.target = None;
        }
        let value = client
            .observe_review_requests(token()?, "321", &q)
            .await
            .map_err(|f| format!("reviewer positive {name}: {} {}", f.status, f.code))?;
        let snapshot = &value["review_requests"];
        let items = snapshot["items"].as_array().ok_or("request items absent")?;
        ensure(
            value["account"]["external_id"] == "321"
                && snapshot["repository"] == "123"
                && snapshot["pull"]["external_id"]
                    == if name == "big-id" {
                        "5000000000"
                    } else {
                        "700"
                    },
            "reviewer snapshot lost immutable scope",
        )?;
        if name == "no-target" {
            ensure(
                snapshot["selected_target"].is_null() && snapshot["requested"].is_null(),
                "targetless read invented membership",
            )?;
        } else if name == "hidden-absent" {
            ensure(
                snapshot["requested"].is_null() && snapshot["all_targets_visible"] == false,
                "hidden target became absent",
            )?;
        } else if matches!(name, "user-not-requested" | "team-not-requested" | "empty") {
            ensure(
                snapshot["requested"] == false,
                "namespace collision or empty catalogue invented membership",
            )?;
        } else {
            ensure(
                snapshot["requested"] == true,
                "selected target presence lost",
            )?;
        }
        if name == "hidden-present" {
            ensure(
                snapshot["all_targets_visible"] == false
                    && items.iter().any(|i| i["reviewer"].is_null()),
                "hidden request discarded or made known",
            )?;
        }
        if name == "empty" {
            ensure(items.is_empty(), "empty request list invented rows")?;
        }
        if name == "user" || name == "team" {
            let user = items
                .iter()
                .find(|i| i["reviewer"]["kind"] == "user")
                .ok_or("user missing")?;
            let team = items
                .iter()
                .find(|i| i["reviewer"]["kind"] == "team")
                .ok_or("team missing")?;
            ensure(
                user["reviewer"]["external_id"] == "456"
                    && team["reviewer"]["external_id"] == "T_declared456"
                    && team["reviewer"]["database_id"] == "456"
                    && user["as_code_owner"] == true,
                "user/team identities or code-owner fact merged",
            )?;
        }
        if name == "user-id-unknown" || name == "big-id" {
            ensure(
                items[0]["reviewer"]["external_id"].is_null(),
                "unknown numeric identity invented",
            )?;
        }
        if name == "team-id-unknown" {
            ensure(
                snapshot["selected_target"]["database_id"].is_null()
                    && snapshot["selected_target"]["external_id"] == "T_declared456",
                "team identity depended on nullable database id",
            )?;
        }
        if name == "user-rename" {
            ensure(
                snapshot["selected_target"]["login"] == "renamed-user",
                "current user label lost",
            )?;
        }
        if name == "team-rename" {
            ensure(
                snapshot["selected_target"]["slug"] == "renamed-team",
                "current team label lost",
            )?;
        }
        if name == "other-kind" {
            ensure(
                items.iter().any(|i| {
                    i["reviewer"]["kind"] == "other" && i["reviewer"]["type"] == "Mannequin"
                }),
                "non-user/team request silently dropped",
            )?;
        }
        fs::write(
            root.join(format!("{name}.json")),
            serde_json::to_vec(&value)?,
        )?;
        cases.push(name);
    }
    for (name, status) in [
        ("identity-change", 409),
        ("identity-denied", 401),
        ("repository-change", 409),
        ("missing-owner-node", 502),
        ("wrong-parent", 409),
        ("wrong-repository", 409),
        ("missing-pull", 404),
        ("duplicate-request", 502),
        ("duplicate-target", 502),
        ("duplicate-user-id", 502),
        ("wrong-request-pull", 502),
        ("unknown-code-owner", 502),
        ("missing-reviewer", 502),
        ("missing-database-id", 502),
        ("foreign-team", 409),
        ("count-change", 409),
        ("count-mismatch", 409),
        ("cursor-loop", 502),
        ("empty-continuation", 502),
        ("missing-cursor", 502),
        ("too-many", 413),
        ("partial-errors", 502),
        ("malformed", 502),
        ("oversized", 413),
        ("denied", 403),
        ("validation", 400),
        ("redirect", 409),
        ("user-gone", 404),
        ("user-changed", 409),
        ("user-kind", 409),
        ("user-node", 502),
        ("user-login", 502),
        ("team-gone", 404),
        ("team-kind", 409),
        ("team-changed", 409),
        ("team-organization", 409),
        ("team-slug", 502),
        ("target-class-conflict", 409),
        ("target-id-conflict", 409),
        ("changed-pull-recheck", 409),
        ("changed-owner-recheck", 409),
        ("deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if name.starts_with("team-") {
            q.target = Some(ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            });
        }
        let start = Instant::now();
        let f = client
            .observe_review_requests(token()?, "321", &q)
            .await
            .err()
            .ok_or_else(|| format!("reviewer refusal accepted {name}"))?;
        ensure(
            f.status == status,
            &format!("reviewer refusal {name}: {} {}", f.status, f.code),
        )?;
        ensure(
            start.elapsed() < Duration::from_secs(10),
            "reviewer read deadline unbounded",
        )?;
        cases.push(name);
    }
    mode("user")?;
    for i in 0..8 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.number = 0,
            2 => q.number = u64::MAX,
            3 => {
                q.target = Some(ReviewerTarget::User {
                    external_id: "0456".into(),
                })
            }
            4 => {
                q.target = Some(ReviewerTarget::Team {
                    external_id: "456".into(),
                })
            }
            5 => {
                q.target = Some(ReviewerTarget::Team {
                    external_id: "T".repeat(513),
                })
            }
            6 => {
                q.target = Some(ReviewerTarget::Team {
                    external_id: "T_\n".into(),
                })
            }
            _ => {
                q.target = Some(ReviewerTarget::User {
                    external_id: "-1".into(),
                })
            }
        }
        let before = fs::read(io.join("gets"))?;
        ensure(
            client
                .observe_review_requests(token()?, "321", &q)
                .await
                .err()
                .is_some_and(|f| f.status == 400)
                && fs::read(io.join("gets"))? == before,
            "invalid target reached provider",
        )?;
    }
    mode("retirement")?;
    let q = request();
    let t = token()?;
    let pending = async { Ok::<_, &str>(client.observe_review_requests(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let start = Instant::now();
    owner.stop()?;
    ensure(
        pending.await?.is_err() && start.elapsed() < Duration::from_secs(2),
        "reviewer reader outlived owner",
    )?;
    ensure(
        client
            .observe_review_requests(token()?, "321", &request())
            .await
            .is_err(),
        "retired reviewer reader accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "reviewer reader sent undeclared request",
    )?;
    fs::write(
        root.join("reviewer-read-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":8,"owner_retirement":true,"real_github":false,"mutations":0,"canonical_consumer_connected":false}),
        )?,
    )?;
    println!("native reviewer observation passed {} declared cases, 8 invalid descriptors and owner retirement; no mutations",cases.len());
    Ok(())
}

pub(super) async fn effects(profile: &Path) -> Result<()> {
    use soda_desktop_runtime::github_provider::{ReviewRequestChange, WriteOutcome};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let token = || Token::new(b"factory-declared-reviewer-write".to_vec());
    let request = || ReviewRequestChange {
        repository: "123".into(),
        pull_id: "700".into(),
        number: 7,
        target: ReviewerTarget::User {
            external_id: "456".into(),
        },
        requested: true,
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let mut client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "add-user",
        "remove-user",
        "add-team",
        "remove-team",
        "big-id",
        "no-request-id",
        "current-user-name",
        "current-team-slug",
    ] {
        mode(name)?;
        let mut q = request();
        q.requested = !name.starts_with("remove") && !name.starts_with("current-");
        if name.contains("team") {
            q.target = ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            };
        }
        if name == "big-id" {
            q.pull_id = "5000000000".into();
            q.target = ReviewerTarget::User {
                external_id: "5000000000".into(),
            };
        }
        let before = writes()?;
        let WriteOutcome::Applied(r) = client.set_review_request(token()?, "321", &q).await else {
            return Err(format!("reviewer positive refused {name}").into());
        };
        ensure(
            r["account"]["external_id"] == "321"
                && r["repository"] == "123"
                && r["pull_id"] == q.pull_id
                && r["number"] == 7
                && r["requested"] == q.requested
                && r["unrelated_requests_preserved"] == 2
                && r["provider_response"]["status"] == 200
                && r["before_sha256"].as_str().is_some_and(|s| s.len() == 64)
                && r["after_sha256"].as_str().is_some_and(|s| s.len() == 64)
                && serde_json::to_vec(&r)?.len() <= 4096
                && writes()? == before + 1,
            "reviewer causal receipt lost binding/bounds or repeated effect",
        )?;
        if q.requested {
            ensure(
                r["actor"]["external_id"] == "321"
                    && r["target_selector"] == "node_id"
                    && r["provider_response"]["method"] == "POST",
                "addition lost union/actor/node semantics",
            )?;
        } else {
            ensure(
                r["actor"].is_null()
                    && r["target_selector"] == "name_preflight_and_recheck"
                    && r["provider_response"]["method"] == "DELETE",
                "removal invented actor or immutable selector proof",
            )?;
        }
        if name == "no-request-id" {
            ensure(
                r["provider_response"]["request_id"].is_null(),
                "request id invented",
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
        ("missing-actor-node", 502),
        ("repository-change", 409),
        ("missing-repository-node", 502),
        ("subject-change", 409),
        ("missing-pull-node", 502),
        ("locked", 403),
        ("lock-unknown", 502),
        ("archived", 403),
        ("disabled", 403),
        ("readonly-unknown", 403),
        ("target-changed", 409),
        ("team-foreign", 409),
        ("hidden-absence", 409),
        ("already-requested", 409),
        ("already-absent", 409),
        ("maximum-before", 413),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        if name == "team-foreign" {
            q.target = ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            };
        }
        if name == "already-absent" {
            q.requested = false;
        }
        let before = writes()?;
        let WriteOutcome::NotSent(f) = client.set_review_request(token()?, "321", &q).await else {
            return Err(format!("reviewer preflight dispatched {name}").into());
        };
        ensure(
            f.status == status && writes()? == before,
            &format!("reviewer preflight {name}: {} {}", f.status, f.code),
        )?;
        cases.push(name);
    }
    mode("add-user")?;
    for i in 0..8 {
        let mut q = request();
        match i {
            0 => q.repository = "0123".into(),
            1 => q.pull_id = "0700".into(),
            2 => q.number = 0,
            3 => q.number = u64::MAX,
            4 => {
                q.target = ReviewerTarget::User {
                    external_id: "0456".into(),
                }
            }
            5 => {
                q.target = ReviewerTarget::Team {
                    external_id: "456".into(),
                }
            }
            6 => {
                q.target = ReviewerTarget::Team {
                    external_id: "T".repeat(513),
                }
            }
            _ => {
                q.target = ReviewerTarget::Team {
                    external_id: "T_\n".into(),
                }
            }
        }
        let before = fs::read(io.join("gets"))?;
        ensure(
            matches!(client.set_review_request(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.status==400)
                && fs::read(io.join("gets"))? == before,
            "invalid reviewer change reached provider",
        )?;
    }
    for (name, status) in [
        ("denied", 403),
        ("limited", 429),
        ("validation", 400),
        ("server-error", 503),
        ("redirect", 409),
        ("malformed", 502),
        ("partial-errors", 502),
        ("oversized", 413),
        ("wrong-pull", 502),
        ("wrong-repository", 502),
        ("wrong-number", 502),
        ("wrong-actor", 502),
        ("wrong-url", 502),
        ("remove-wrong-id", 502),
        ("remove-wrong-node", 502),
        ("remove-wrong-repository", 502),
        ("remove-wrong-number", 502),
        ("remove-wrong-url", 502),
        ("post-target-absent", 409),
        ("unrelated-dropped", 409),
        ("unrelated-mutated", 409),
        ("code-owner-changed", 409),
        ("post-hidden", 409),
        ("post-read-denied", 403),
        ("post-target-gone", 404),
        ("name-race-user", 409),
        ("name-race-team", 409),
        ("maximum-after", 413),
        ("oversized-request-id", 502),
        ("duplicate-request-id", 502),
        ("receipt-oversized", 413),
        ("drop-reply", 503),
        ("deadline", 504),
        ("deadline-after-reply", 504),
    ] {
        mode(name)?;
        let mut q = request();
        q.requested = !name.starts_with("remove-") && !name.starts_with("name-race");
        if name == "name-race-team" {
            q.target = ReviewerTarget::Team {
                external_id: "T_declared456".into(),
            };
        }
        let before = writes()?;
        let start = Instant::now();
        let WriteOutcome::Indeterminate(f) = client.set_review_request(token()?, "321", &q).await
        else {
            return Err(format!("uncertain reviewer result became success/absence {name}").into());
        };
        ensure(
            f.status == status && writes()? == before + 1,
            &format!("reviewer uncertainty {name}: {} {}", f.status, f.code),
        )?;
        ensure(
            start.elapsed() < Duration::from_secs(10),
            "reviewer effect exceeded whole deadline",
        )?;
        cases.push(name);
        if name == "drop-reply" {
            mode("after-drop")?;
            ensure(
                matches!(client.set_review_request(token()?,"321",&q).await,WriteOutcome::NotSent(f)if f.code=="github_reviewer_already_desired")
                    && writes()? == before + 1,
                "matching request state attributed loss or repeated mutation",
            )?;
        }
    }
    mode("serialize")?;
    let a = request();
    let b = request();
    let clone = client.clone();
    let (first, second) = tokio::join!(
        client.set_review_request(token()?, "321", &a),
        clone.set_review_request(token()?, "321", &b)
    );
    ensure(
        ((matches!(&first, WriteOutcome::Applied(_))
            && matches!(&second,WriteOutcome::NotSent(f)if f.code=="github_reviewer_already_desired"))
            || (matches!(&second, WriteOutcome::Applied(_))
                && matches!(&first,WriteOutcome::NotSent(f)if f.code=="github_reviewer_already_desired")))
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "reviewer clone serialization or mutation pacing failed",
    )?;
    for (name, attempted) in [("pause-preflight", false), ("pause-write", true)] {
        for marker in ["entered", "release", "cancelled"] {
            let _ = fs::remove_file(io.join(marker));
        }
        mode(name)?;
        let q = request();
        let t = token()?;
        let flight = client.clone();
        let pending = async move { Ok::<_, &str>(flight.set_review_request(t, "321", &q).await) };
        tokio::pin!(pending);
        super::native_github_fixture::entered(pending.as_mut(), &io).await?;
        let start = Instant::now();
        owner.stop()?;
        let result = pending.await?;
        ensure(
            if attempted {
                matches!(result, WriteOutcome::Indeterminate(_))
            } else {
                matches!(result, WriteOutcome::NotSent(_))
            } && start.elapsed() < Duration::from_secs(2),
            "reviewer retirement lost dispatch boundary",
        )?;
        if !attempted {
            owner = tokio::task::spawn_blocking(Owner::start).await??;
            client = owner.client();
        }
    }
    ensure(
        matches!(
            client.set_review_request(token()?, "321", &request()).await,
            WriteOutcome::NotSent(_)
        ),
        "retired reviewer writer accepted work",
    )?;
    ensure(
        !io.join("unexpected-request").exists(),
        "reviewer writer sent undeclared request",
    )?;
    fs::write(
        root.join("reviewer-write-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":8,"clone_serialization":true,"owner_retirement":true,"real_github":false,"product_consumer_connected":false,"atomic_name_binding_qualified":false}),
        )?,
    )?;
    println!("native reviewer request/removal passed {} declared cases, 8 invalid descriptors, serialization and retirement",cases.len());
    Ok(())
}

pub(super) async fn product(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    use super::native_github_fixture::{call, completed, set_mode, uncertain};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
        ProductRequest,
    };
    use std::sync::Arc;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let envelope = |path: &str,
                    id: &str,
                    kind: &str,
                    target: &str,
                    requested: bool|
     -> Result<Vec<u8>> {
        let q: ProductRequest = serde_json::from_value(
            json!({"kind":"write","path":path,"body":{"command_id":id,"reviewer_kind":kind,"reviewer_id":target,"requested":requested}}),
        )?;
        Ok(q.request()?.2)
    };
    set_mode(&io, "positive")?;
    let mut runtime = NativeSupervisor::start(payload, production, profile)?;
    let mut store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "reviewer product requires empty owned custody",
    )?;
    let account = || Account::new(Provider::Github, "https://github.com", "321");
    let mut connection = store
        .store(
            account()?,
            Token::new(b"factory-declared-reviewer-write".to_vec())?,
        )
        .await?
        .connection;
    let catalogue = call(
        &runtime,
        &store,
        &connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "reviewer product catalogue refused")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("repository absent")?;
    let submit =
        format!("/api/factory/collaboration/repositories/{repository}/pulls/7/review-requests");
    let command = |id: &str| format!("{submit}/commands/{id}");
    set_mode(&io, "catalogue")?;
    let targets = call(&runtime, &store, &connection, "GET", &submit, &[]).await?;
    fs::write(
        root.join("reviewer-first-catalogue.json"),
        serde_json::to_vec(&targets.1)?,
    )?;
    ensure(
        targets.0 == 200
            && targets.1["pull"]["repository"] == repository
            && targets.1["pull"]["external_id"] == "700"
            && targets.1["all_targets_visible"] == true,
        &format!(
            "canonical reviewer catalogue scope failed: {} {}",
            targets.0, targets.1
        ),
    )?;
    let rows = targets.1["items"]
        .as_array()
        .ok_or("review requests absent")?;
    let user = rows
        .iter()
        .find(|r| r["reviewer"]["kind"] == "user" && r["reviewer"]["external_id"] == "456")
        .and_then(|r| r["reviewer"]["actor"]["id"].as_str())
        .ok_or("observed canonical user absent")?;
    let team = rows
        .iter()
        .find(|r| {
            r["reviewer"]["kind"] == "team" && r["reviewer"]["external_id"] == "T_declared456"
        })
        .and_then(|r| r["reviewer"]["team"]["id"].as_str())
        .ok_or("observed canonical team absent")?;
    let mut cases = vec!["canonical-catalogue"];
    for (kind, name, field, canonical) in [("user", "target-user", "actor", user), ("team", "target-team", "team", team)] {
        let result = call(&runtime,&store,&connection,"GET",&format!("{submit}/lookup?kind={kind}&name={name}"),&[]).await?;
        ensure(result.0 == 200 && result.1[field]["id"] == canonical && result.1["pull"]["external_id"] == "700" && writes()? == 0,
            &format!("canonical {kind} lookup failed: {} {}",result.0,result.1))?;
        cases.push(if kind == "user" { "user-name-lookup" } else { "team-slug-lookup" });
    }
    for suffix in ["?kind=user&name=declared-target&name=other", "?kind=user&name=x&extra=x", "?kind=bot&name=x", "?kind=user&name=..%2Fbad"] {
        ensure(call(&runtime,&store,&connection,"GET",&format!("{submit}/lookup{suffix}"),&[]).await?.0 == 400,
            "invalid reviewer lookup reached provider")?;
    }
    cases.push("invalid-lookup-refused");
    for mode in [
        "catalogue-hidden",
        "catalogue-user-unknown",
        "catalogue-team-unknown",
        "catalogue-other-unknown",
        "catalogue-bot",
        "catalogue-renamed",
        "catalogue-big-id",
    ] {
        set_mode(&io, mode)?;
        let reply = call(&runtime, &store, &connection, "GET", &submit, &[]).await?;
        ensure(
            reply.0 == 200,
            &format!("canonical reviewer shape {mode}: {} {}", reply.0, reply.1),
        )?;
        let rows = reply.1["items"].as_array().ok_or("snapshot rows absent")?;
        let selected = rows
            .iter()
            .find(|r| r["external_id"] == "RR_catalogue_user")
            .ok_or("catalogue user absent")?;
        let selected_team = rows
            .iter()
            .find(|r| r["external_id"] == "RR_catalogue_team")
            .ok_or("catalogue team absent")?;
        ensure(
            selected_team["reviewer"]["team"]["id"] == team,
            "team metadata rekeyed immutable identity",
        )?;
        match mode {
            "catalogue-hidden" => ensure(
                reply.1["all_targets_visible"] == false
                    && rows.iter().any(|r| r["reviewer"].is_null()),
                "hidden reviewer discarded",
            )?,
            "catalogue-user-unknown" | "catalogue-big-id" => ensure(
                selected["reviewer"]["external_id"].is_null()
                    && selected["reviewer"]["actor"].is_null()
                    && selected["reviewer"]["node_id"].is_string(),
                "unknown numeric actor invented or node lost",
            )?,
            "catalogue-other-unknown" => ensure(
                reply.1["all_targets_visible"] == false
                    && rows.iter().any(|r| {
                        r["reviewer"]["kind"] == "other"
                            && r["reviewer"]["node_id"].is_null()
                            && r["reviewer"]["provider_kind"] == "Mannequin"
                    }),
                "unknown other reviewer discarded or made known",
            )?,
            "catalogue-bot" => ensure(
                rows.iter().any(|r| {
                    r["reviewer"]["kind"] == "bot"
                        && r["reviewer"]["actor"]["external_id"] == "777"
                        && r["reviewer"]["team"].is_null()
                }),
                "bot kind/canonical identity lost",
            )?,
            "catalogue-renamed" => ensure(
                selected["reviewer"]["actor"]["id"] == user
                    && selected["reviewer"]["label"] == "renamed-user"
                    && selected_team["reviewer"]["label"] == "Renamed team",
                "rename replaced canonical identity",
            )?,
            _ => (),
        }
        if mode == "catalogue-big-id" {
            ensure(
                reply.1["pull"]["external_id"] == "5000000000",
                "BigInt pull identity truncated",
            )?;
        }
        cases.push(mode);
    }
    for (mode, status) in [
        ("partial-errors", 502),
        ("catalogue-denied", 403),
        ("catalogue-count-change", 409),
        ("catalogue-too-many", 413),
        ("maximum-before", 200),
    ] {
        set_mode(&io, mode)?;
        let reply = call(&runtime, &store, &connection, "GET", &submit, &[]).await?;
        ensure(
            reply.0 == status,
            &format!("canonical read bound/refusal {mode}: {}", reply.0),
        )?;
        if mode == "maximum-before" {
            ensure(
                reply.1["items"].as_array().is_some_and(|r| r.len() == 1000),
                "bounded full reviewer list truncated",
            )?;
        }
        cases.push(mode);
    }
    ensure(
        call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{submit}?limit=1"),
            &[],
        )
        .await?
        .0 == 400
            && writes()? == 0,
        "reviewer catalogue accepted unsupported query or wrote",
    )?;
    cases.push("catalogue-query-refusal");
    for (i, kind, target, requested, mode) in [
        (0, "user", user, true, "add-user"),
        (1, "user", user, false, "remove-user"),
        (2, "team", team, true, "add-team"),
        (3, "team", team, false, "remove-team"),
    ] {
        set_mode(&io, mode)?;
        let id = format!("{:032x}", 100 + i);
        let body = envelope(&submit, &id, kind, target, requested)?;
        let before = writes()?;
        let reply = call(&runtime, &store, &connection, "POST", &submit, &body).await?;
        completed(&reply, "committed")?;
        let read = call(&runtime, &store, &connection, "GET", &submit, &[]).await?;
        ensure(
            read.0 == 200
                && read.1["all_targets_visible"] == true
                && read.1["items"].as_array().is_some_and(|rows| {
                    rows.iter().any(|r| {
                        r["reviewer"]["kind"] == kind
                            && r["reviewer"][if kind == "team" { "team" } else { "actor" }]["id"]
                                == target
                    }) == requested
                }),
            "normal reviewer readback lost selected effect/identity",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &body).await?,
            "committed",
        )?;
        completed(
            &call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{}/cancel", command(&id)),
                b"{}",
            )
            .await?,
            "committed",
        )?;
        for (changed_kind, changed_target, changed_state) in [
            (kind, target, !requested),
            (
                if kind == "user" { "team" } else { "user" },
                if kind == "user" { team } else { user },
                requested,
            ),
        ] {
            ensure(
                call(
                    &runtime,
                    &store,
                    &connection,
                    "POST",
                    &submit,
                    &envelope(&submit, &id, changed_kind, changed_target, changed_state)?,
                )
                .await?
                .0 == 409,
                "changed reviewer intent adopted command",
            )?;
        }
        ensure(
            writes()? == before + 1,
            "replay/lookup/cancel/conflict repeated reviewer effect",
        )?;
        cases.push(mode);
    }
    set_mode(&io, "already-requested")?;
    let noop_id = "22222222222222222222222222222222";
    let noop = envelope(&submit, noop_id, "user", user, true)?;
    let before = writes()?;
    for _ in 0..2 {
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &noop).await?,
            "not_committed",
        )?;
    }
    ensure(writes()? == before, "already requested state wrote")?;
    cases.push("no-op");
    set_mode(&io, "concurrent")?;
    let concurrent_id = "33333333333333333333333333333333";
    let body = envelope(&submit, concurrent_id, "user", user, true)?;
    let before = writes()?;
    let (a, b) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &body),
        call(&runtime, &store, &connection, "POST", &submit, &body)
    );
    for reply in [a?, b?] {
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
            &command(concurrent_id),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        writes()? == before + 1,
        "concurrent reviewer command dispatched twice",
    )?;
    cases.push("concurrent");
    let wrong = format!(
        "/api/factory/collaboration/repositories/{}/pulls/7/review-requests",
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
                "user",
                user,
                true,
            )?,
        )
        .await?
        .0 == 403
            && writes()? == before + 1,
        "reviewer write expanded repository scope",
    )?;
    cases.push("other-repository");
    set_mode(&io, "drop-reply")?;
    let lost_id = "44444444444444444444444444444444";
    let lost = envelope(&submit, lost_id, "user", user, true)?;
    let before = writes()?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    set_mode(&io, "observe-after-loss")?;
    let after_loss = call(&runtime, &store, &connection, "GET", &submit, &[]).await?;
    ensure(
        after_loss.0 == 200
            && after_loss.1["items"]
                .as_array()
                .is_some_and(|r| r.iter().any(|r| r["reviewer"]["actor"]["id"] == user)),
        "lost reply fixture failed ordinary observed state",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        writes()? == before + 1,
        "lost reviewer reply was attributed or resent",
    )?;
    cases.push("lost-reply");
    for (mode, id, effect) in [
        (
            "pause-post",
            "55555555555555555555555555555555",
            "committed",
        ),
        (
            "pause-preflight",
            "66666666666666666666666666666666",
            "not_committed",
        ),
    ] {
        set_mode(&io, mode)?;
        let body = envelope(&submit, id, "user", user, true)?;
        let before = writes()?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let pending = request.request("POST", &submit, &body);
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
            "racing cancellation claimed rollback",
        )?;
        fs::write(io.join("release"), b"")?;
        let response = pending.await?;
        let reply = (response.status, serde_json::from_slice(&response.body)?);
        completed(&reply, effect)?;
        ensure(
            reply.1["cancellation"] == "indeterminate"
                && writes()? == before + u64::from(mode == "pause-post"),
            "late reviewer result lost cancellation uncertainty",
        )?;
        cases.push(mode);
    }
    set_mode(&io, "pause-withdrawal")?;
    let withdrawn_id = "77777777777777777777777777777777";
    let withdrawn = envelope(&submit, withdrawn_id, "user", user, true)?;
    let before = writes()?;
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
        .store(
            account()?,
            Token::new(b"factory-declared-reviewer-write".to_vec())?,
        )
        .await?
        .connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "withdrawn generation returned reviewer result",
    )?;
    set_mode(&io, "observe-after-withdrawal")?;
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
        writes()? == before + 1,
        "replacement custody repeated uncertain reviewer effect",
    )?;
    cases.push("withdrawal-replacement");
    set_mode(&io, "pause-stop")?;
    let stopped_id = "88888888888888888888888888888888";
    let stopped = envelope(&submit, stopped_id, "user", user, true)?;
    let before = writes()?;
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
        "stopped runtime returned reviewer result",
    )?;
    store.retire();
    runtime = NativeSupervisor::start(payload, production, profile)?;
    store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    set_mode(&io, "observe-after-stop")?;
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
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(&format!("{:032x}", 100)),
            &[],
        )
        .await?,
        "committed",
    )?;
    completed(
        &call(&runtime, &store, &connection, "GET", &command(noop_id), &[]).await?,
        "not_committed",
    )?;
    ensure(
        writes()? == before + 1,
        "retained restart repeated reviewer effect",
    )?;
    cases.push("retained-restart");
    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?,
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
        "another account read reviewer capture",
    )?;
    ensure(
        call(&runtime, &store, &other.connection, "POST", &submit, &lost)
            .await?
            .0
            == 409
            && writes()? == before + 1,
        "another account adopted reviewer capture",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account");
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "reviewer product left owned account",
    )?;
    store.retire();
    runtime.stop()?;
    ensure(
        !io.join("unexpected-request").exists(),
        "reviewer product sent undeclared request",
    )?;
    fs::write(
        root.join("reviewer-product-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"provider_mutations":writes()?,"native_go_postgresql":true,"canonical_targets_seeded":false,"declared_authenticated_catalogue":true,"normal_readback":true,"real_github":false,"atomic_name_binding_qualified":false,"shared_write_capabilities_enabled":false,"owned_accounts_removed":true}),
        )?,
    )?;
    println!(
        "composed native reviewer request/removal passed {} declared cases with {} dummy mutations",
        cases.len(),
        writes()?
    );
    Ok(())
}

pub(super) async fn preparation(profile: &Path) -> Result<()> {
    use soda_desktop_runtime::github_provider::Read;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let request = || Read::PullReviewPreparation {
        repository: "123".into(),
        number: 7,
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "absent",
        "pending",
        "other-user",
        "paged",
        "maximum",
        "max-body",
        "old-commit",
        "null-commit",
        "unknown-review-id",
        "unknown-metadata",
        "big-ids",
    ] {
        mode(name)?;
        let v = client
            .observe(token()?, "321", &request())
            .await
            .map_err(|f| format!("preparation {name}: {} {}", f.status, f.code))?;
        let p = &v["preparation"];
        ensure(
            v["kind"] == "pull_review_preparation"
                && v["account"]["external_id"] == "321"
                && p["repository"] == "123"
                && p["pull"]["number"] == 7
                && p["actor_node_id"] == "U_321",
            "pending preparation lost account/resource scope",
        )?;
        if matches!(name, "absent" | "other-user") {
            ensure(
                p["pending"].is_null(),
                "another user's pending review was adopted",
            )?
        } else {
            ensure(
                p["pending"]["node_id"] == "REV_owned" && p["pending"]["state"] == "pending",
                "current pending review lost",
            )?;
        }
        match name {
            "max-body" => ensure(
                p["pending"]["body"]
                    .as_str()
                    .is_some_and(|b| b.len() == 65536),
                "pending full body truncated",
            )?,
            "maximum" => ensure(
                p["visible_pending_count"] == 1000,
                "bounded complete pending traversal lost rows",
            )?,
            "paged" => ensure(
                p["visible_pending_count"] == 51,
                "paged pending traversal lost rows",
            )?,
            "unknown-review-id" => ensure(
                p["pending"]["external_id"].is_null(),
                "unknown numeric review identity invented",
            )?,
            "null-commit" => ensure(
                p["pending"]["commit"].is_null(),
                "unknown pending commit invented",
            )?,
            "old-commit" => ensure(
                p["pending"]["commit"] != p["pull"]["head"],
                "historical pending commit replaced by current head",
            )?,
            "unknown-metadata" => ensure(
                p["archived"].is_null() && p["pull"]["draft"].is_null(),
                "unknown facts became false",
            )?,
            "big-ids" => ensure(
                p["pending"]["external_id"] == "5000000001"
                    && p["pull"]["external_id"] == "5000000000",
                "BigInt identities truncated",
            )?,
            _ => (),
        }
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&v)?)?;
        cases.push(name);
    }
    for (name, status) in [
        ("identity-denied", 401),
        ("identity-change", 409),
        ("identity-alias", 409),
        ("missing-actor-node", 502),
        ("missing-pull-node", 502),
        ("missing-repo-node", 502),
        ("missing-head", 502),
        ("invalid-head", 502),
        ("head-change", 409),
        ("repo-slot-change", 502),
        ("partial-errors", 502),
        ("query-denied", 403),
        ("query-rejected", 400),
        ("query-oversized", 413),
        ("list-hidden", 404),
        ("wrong-pull", 409),
        ("wrong-number", 409),
        ("wrong-repo", 409),
        ("wrong-head", 409),
        ("wrong-full-id", 409),
        ("count-change", 409),
        ("count-mismatch", 409),
        ("too-many", 413),
        ("too-many-page", 502),
        ("duplicate-node", 502),
        ("cursor-cycle", 502),
        ("empty-continuation", 502),
        ("unknown-author", 502),
        ("wrong-review-parent", 502),
        ("wrong-review-repo", 502),
        ("nonpending-list", 502),
        ("two-owned", 409),
        ("detail-gone", 409),
        ("detail-submitted", 409),
        ("detail-wrong-id", 409),
        ("detail-wrong-owner", 409),
        ("detail-wrong-pull", 409),
        ("detail-wrong-repo", 409),
        ("detail-wrong-head", 409),
        ("detail-submitted-date", 409),
        ("detail-null-body", 502),
        ("detail-oversized-body", 413),
        ("detail-invalid-commit", 502),
        ("detail-wrong-commit-repo", 502),
        ("detail-invalid-id", 502),
        ("detail-invalid-count", 502),
        ("detail-invalid-date", 502),
    ] {
        mode(name)?;
        let f = client
            .observe(token()?, "321", &request())
            .await
            .err()
            .ok_or("invalid preparation passed")?;
        ensure(
            f.status == status,
            &format!(
                "preparation refusal {name}: {} {}, expected {status}",
                f.status, f.code
            ),
        )?;
        cases.push(name);
    }
    let before = fs::read(io.join("gets"))?;
    for q in [
        Read::PullReviewPreparation {
            repository: "0".into(),
            number: 7,
        },
        Read::PullReviewPreparation {
            repository: "123".into(),
            number: 0,
        },
    ] {
        ensure(
            client.observe(token()?, "321", &q).await.is_err(),
            "invalid selector accepted",
        )?;
    }
    ensure(
        fs::read(io.join("gets"))? == before,
        "invalid preparation selector reached provider",
    )?;
    mode("pause")?;
    let pending_request = request();
    let pending = client.observe(token()?, "321", &pending_request);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        pending.await.is_err() && started.elapsed() < Duration::from_secs(2),
        "pending preparation owner retirement blocked or delivered",
    )?;
    ensure(
        !io.join("unexpected-request").exists() && !io.join("writes").exists(),
        "preparation sent undeclared/mutating request",
    )?;
    fs::write(
        root.join("review-preparation-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":2,"complete_pending_traversal":true,"mutable_author_filter":false,"retirement":true,"provider_mutations":0,"real_github":false,"review_permission_qualified":false,"atomic_head_body_qualified":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("native review preparation passed {} declared cases, two invalid selectors and retirement; no mutations or live-provider qualification",cases.len());
    Ok(())
}

pub(super) async fn creation(profile: &Path) -> Result<()> {
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::{CreateOutcome, ReviewCreate};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        for key in ["entered", "release", "cancelled"] {
            match fs::remove_file(io.join(key)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.into()),
            }
        }
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let token = || Token::new(b"factory-declared-review-create".to_vec());
    let request = || ReviewCreate {
        base: String::new(),
        repository: "123".into(),
        repository_node_id: "R_123".into(),
        pull_id: "700".into(),
        pull_node_id: "P_700".into(),
        number: 7,
        head: "b".repeat(40),
        event: "comment".into(),
        body: "Exact human review 🍵\n[link](relative.md)".into(),
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    for name in [
        "draft",
        "comment",
        "approve",
        "request_changes",
        "empty-draft",
        "empty-approval",
        "max-unicode",
        "max-control",
        "big-ids",
        "no-request-id",
    ] {
        mode(name)?;
        let mut q = request();
        if matches!(name, "draft" | "approve" | "request_changes") {
            q.event = name.into()
        }
        if name == "empty-draft" {
            q.event = "draft".into();
            q.body.clear()
        }
        if name == "empty-approval" {
            q.event = "approve".into();
            q.body.clear()
        }
        if name == "max-unicode" {
            q.body = "🍵".repeat(16384)
        }
        if name == "max-control" {
            q.body = "\u{0001}".repeat(65536)
        }
        if name == "big-ids" {
            q.pull_id = "5000000000".into()
        }
        let before = writes()?;
        let r = match client.create_review(token()?, "321", &q).await {
            CreateOutcome::Created(r) => r,
            CreateOutcome::NotSent(f) | CreateOutcome::Indeterminate(f) => {
                return Err(format!("review positive {name}: {} {}", f.status, f.code).into())
            }
        };
        ensure(
            r["event"] == q.event
                && r["pull_id"] == q.pull_id
                && r["head"] == q.head
                && r["review"]["body_sha256"]
                    == Sha256::digest(q.body.as_bytes())
                        .iter()
                        .map(|b| format!("{b:02x}"))
                        .collect::<String>()
                && r["review"]["comments_count"] == 0
                && writes()? == before + 1,
            "review lost intent/causal receipt",
        )?;
        ensure(
            (q.event == "draft") == r["review"]["submitted_at"].is_null(),
            "draft/submitted review confused",
        )?;
        cases.push(name);
    }
    for (name, status) in [
        ("existing-pending", 409),
        ("identity-denied", 401),
        ("identity-change", 409),
        ("missing-actor-node", 502),
        ("read-only", 403),
        ("unknown-read-only", 403),
        ("locked", 403),
        ("merged", 403),
        ("closed", 403),
        ("unknown-merged", 403),
        ("saved-head-changed", 409),
        ("saved-pull-changed", 409),
        ("saved-node-changed", 409),
        ("saved-repo-node-changed", 409),
        ("empty-comment", 409),
        ("preflight-deadline", 504),
    ] {
        mode(name)?;
        let mut q = request();
        match name {
            "saved-head-changed" => q.head = "c".repeat(40),
            "saved-pull-changed" => q.pull_id = "701".into(),
            "saved-node-changed" => q.pull_node_id = "P_other".into(),
            "saved-repo-node-changed" => q.repository_node_id = "R_other".into(),
            "empty-comment" => q.body.clear(),
            _ => (),
        }
        let before = writes()?;
        let f = match client.create_review(token()?, "321", &q).await {
            CreateOutcome::NotSent(f) => f,
            _ => return Err(format!("review pre-send {name} not retained").into()),
        };
        ensure(
            f.status == status && writes()? == before,
            &format!("review pre-send {name}: {} {}", f.status, f.code),
        )?;
        cases.push(name);
    }
    for name in [
        "denied",
        "validation",
        "limited",
        "server-error",
        "redirect",
        "malformed",
        "oversized",
        "partial-errors",
        "drop-reply",
        "deadline",
        "wrong-id",
        "wrong-node",
        "wrong-state",
        "wrong-body",
        "wrong-author",
        "wrong-repo",
        "wrong-pull",
        "wrong-pull-id",
        "wrong-number",
        "wrong-head",
        "wrong-parent-repo",
        "wrong-commit",
        "wrong-commit-repo",
        "wrong-comments",
        "wrong-url",
        "wrong-date",
        "wrong-submitted",
        "oversized-request-id",
        "duplicate-request-id",
    ] {
        mode(name)?;
        let before = writes()?;
        ensure(
            matches!(
                client.create_review(token()?, "321", &request()).await,
                CreateOutcome::Indeterminate(_)
            ) && writes()? == before + 1,
            &format!("review post-send {name} lost uncertainty or retried"),
        )?;
        cases.push(name);
    }
    let before = writes()?;
    for i in 0..9 {
        let mut q = request();
        match i {
            0 => q.repository = "0".into(),
            1 => q.pull_id = "0".into(),
            2 => q.pull_node_id.clear(),
            3 => q.repository_node_id.clear(),
            4 => q.number = 0,
            5 => q.head = "invalid".into(),
            6 => q.event = "unsupported".into(),
            7 => q.body = "x".repeat(65537),
            _ => q.body = "\0".into(),
        };
        ensure(
            matches!(
                client.create_review(token()?, "321", &q).await,
                CreateOutcome::NotSent(_)
            ),
            "invalid review descriptor sent",
        )?;
    }
    ensure(
        writes()? == before,
        "invalid review descriptors mutated provider",
    )?;
    mode("serialize")?;
    let q = request();
    let first_token = token()?;
    let first = async { Ok::<_, String>(client.create_review(first_token, "321", &q).await) };
    tokio::pin!(first);
    super::native_github_fixture::entered(first.as_mut(), &io).await?;
    let before = writes()?;
    let clone = client.clone();
    let second = clone.create_review(token()?, "321", &q);
    tokio::pin!(second);
    tokio::select! {result=&mut second=>return Err(format!("review clone completed while prior response paused: {}",matches!(result,CreateOutcome::Created(_))).into()),_=tokio::time::sleep(Duration::from_millis(1200))=>()}
    ensure(
        writes()? == before,
        "review clone sent while predecessor pending",
    )?;
    fs::write(io.join("release"), b"")?;
    ensure(
        matches!(first.await?, CreateOutcome::Created(_))
            && matches!(second.await, CreateOutcome::Created(_)),
        "serialized reviews failed",
    )?;
    mode("pause-post")?;
    let pending_token = token()?;
    let pending = async { Ok::<_, String>(client.create_review(pending_token, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        matches!(pending.await?, CreateOutcome::Indeterminate(_))
            && started.elapsed() < Duration::from_secs(2),
        "review retirement lost uncertainty or blocked",
    )?;
    let before = writes()?;
    ensure(
        matches!(
            client.create_review(token()?, "321", &q).await,
            CreateOutcome::NotSent(_)
        ) && writes()? == before,
        "retired review owner mutated",
    )?;
    ensure(
        !io.join("unexpected-request").exists()
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "review fixture saw undeclared request/overlap/pacing failure",
    )?;
    fs::write(
        root.join("review-create-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":9,"serialized_clones":true,"pacing":true,"retirement":true,"provider_mutations":writes()?,"real_github":false,"existing_pending_submission":false,"draft_body_update":false,"atomic_head_qualified":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("native new-review creation passed {} declared cases, nine invalid descriptors, clone serialization/pacing and retirement",cases.len());
    Ok(())
}

pub(super) async fn new_product(production: &Path, payload: &Path, profile: &Path) -> Result<()> {
    use super::native_github_fixture::{call, completed, set_mode};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
        ProductRequest,
    };
    use std::{os::unix::fs::PermissionsExt, sync::Arc};
    let posts = |io: &Path| -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let uncertain = |r: &(u16, serde_json::Value)| -> Result<()> {
        ensure(
            matches!(r.0, 200 | 202)
                && r.1["effect"] == "indeterminate"
                && r.1["finished"] == false
                && r.1["review"].is_null(),
            "uncertain review became complete or attributable",
        )
    };
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
        "review product requires empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let saved = store
        .store(
            account()?,
            Token::new(b"factory-declared-review-create".to_vec())?,
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
        "review product canonical catalogue failed",
    )?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let base = format!("/api/factory/collaboration/repositories/{repository}");
    let submit = format!("{base}/pulls/7/reviews");
    let command = |id: &str| format!("{submit}/commands/{id}");
    let envelope = |path: &str, id: &str, label: &str, body: &str| -> Result<Vec<u8>> {
        let event = match label {
            "draft" | "comment" | "approve" | "request_changes" => label,
            _ if body.is_empty() => "approve",
            _ => "comment",
        };
        let q: ProductRequest = serde_json::from_value(
            json!({"kind":"write","path":path,"body":{"command_id":id,"event":event,"body":body}}),
        )?;
        Ok(q.request()?.2)
    };
    let mut cases = Vec::new();
    for (i, (title, body)) in [
        ("approve".to_owned(), "".to_owned()),
        (
            "comment".into(),
            "Exact writing 🍵\n[link](relative.md)".into(),
        ),
        ("draft".into(), "🍵".repeat(16384)),
        ("request_changes".into(), "\u{0001}".repeat(65536)),
    ]
    .into_iter()
    .enumerate()
    {
        let id = format!("{:032x}", 100 + i);
        let bytes = envelope(&submit, &id, &title, &body)?;
        set_mode(&io, &format!("positive-{i}"))?;
        let before = posts(&io)?;
        let result = call(&runtime, &store, &connection, "POST", &submit, &bytes).await?;
        completed(&result, "committed")?;
        ensure(
            result.1["review"]["pull"]["repository"] == repository
                && result.1["review"]["pull"]["kind"] == "pull",
            "creation lost its currently readable canonical destination",
        )?;
        let external = result.1["review"]["external_id"]
            .as_str()
            .ok_or("created review ID missing")?
            .to_owned();
        let read = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{submit}/{external}"),
            &[],
        )
        .await?;
        ensure(
            read.0 == 200 && read.1["external_id"] == external && read.1["body"] == body,
            "created normal review readback changed original title/body",
        )?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "committed",
        )?;
        completed(
            &call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{}/cancel", command(&id)),
                b"{}",
            )
            .await?,
            "committed",
        )?;
        let conflict = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &submit,
            &envelope(
                &submit,
                &id,
                if title == "approve" {
                    "comment"
                } else {
                    "approve"
                },
                &body,
            )?,
        )
        .await?;
        ensure(
            conflict.0 == 409 && posts(&io)? == before + 1,
            "replay/lookup/cancel/title conflict duplicated review creation",
        )?;
        cases.push(format!("positive-{i}"));
    }
    set_mode(&io, "positive")?;
    let before = posts(&io)?;
    let concurrent_id = "33333333333333333333333333333333";
    let bytes = envelope(
        &submit,
        concurrent_id,
        "Concurrent exact intent",
        "One review",
    )?;
    let (first, second) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &bytes),
        call(&runtime, &store, &connection, "POST", &submit, &bytes)
    );
    for r in [first?, second?] {
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
            &command(concurrent_id),
            &[],
        )
        .await?,
        "committed",
    )?;
    ensure(
        posts(&io)? == before + 1,
        "simultaneous review submissions repeated the POST",
    )?;
    cases.push("concurrent-identical-command".into());
    let other = repository.parse::<i64>()? + 1;
    let other_path = format!("/api/factory/collaboration/repositories/{other}/pulls/7/reviews");
    let refused = call(
        &runtime,
        &store,
        &connection,
        "POST",
        &other_path,
        &envelope(
            &other_path,
            "99999999999999999999999999999999",
            "Wrong scope",
            "",
        )?,
    )
    .await?;
    ensure(
        refused.0 == 403 && posts(&io)? == before + 1,
        "review request expanded native repository scope",
    )?;
    cases.push("other-repository-refusal".into());
    for body in ["x".repeat(65537), "\0".into()] {
        let r = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &submit,
            &envelope(
                &submit,
                "22222222222222222222222222222222",
                "comment",
                &body,
            )?,
        )
        .await?;
        ensure(r.0 == 400, "review writing bounds weakened")?;
    }
    set_mode(&io, "existing-pending")?;
    let before_pending = posts(&io)?;
    let r = call(
        &runtime,
        &store,
        &connection,
        "POST",
        &submit,
        &envelope(
            &submit,
            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            "draft",
            "Existing pending must be preserved",
        )?,
    )
    .await?;
    ensure(
        r.0 == 503 && posts(&io)? == before_pending,
        "existing pending review adopted or overwritten",
    )?;
    cases.push("existing-pending-unavailable".into());
    // A committed primary survives failed or changed normal destination reads.
    set_mode(&io, "hide-created")?;
    let hidden_id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    let hidden = envelope(
        &submit,
        hidden_id,
        "Owned hidden destination",
        "Original body",
    )?;
    let before = posts(&io)?;
    let r = call(&runtime, &store, &connection, "POST", &submit, &hidden).await?;
    completed(&r, "committed")?;
    ensure(
        r.1["review"].is_null(),
        "hidden created review became readable",
    )?;
    set_mode(&io, "destination-change")?;
    let r = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &command(hidden_id),
        &[],
    )
    .await?;
    completed(&r, "committed")?;
    ensure(
        r.1["review"].is_null(),
        "unrelated destination identity was adopted",
    )?;
    set_mode(&io, "positive")?;
    let r = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &command(hidden_id),
        &[],
    )
    .await?;
    completed(&r, "committed")?;
    ensure(
        !r.1["review"].is_null() && posts(&io)? == before + 1,
        "destination restoration erased effect or repeated creation",
    )?;
    cases.push("committed-primary-destination-retirement".into());
    let lost_id = "44444444444444444444444444444444";
    let lost = envelope(
        &submit,
        lost_id,
        "Owned lost creation reply",
        "Observed matching writing is not attribution",
    )?;
    set_mode(&io, "drop-reply")?;
    let before = posts(&io)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    set_mode(&io, "positive")?;
    let last = fs::read_to_string(io.join("last-review"))?;
    let read = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{submit}/{last}"),
        &[],
    )
    .await?;
    ensure(
        read.0 == 200 && read.1["body"] == "Observed matching writing is not attribution",
        "lost review not normally readable",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        posts(&io)? == before + 1,
        "matching review search/replay manufactured creation attribution",
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
        set_mode(&io, name)?;
        let before = posts(&io)?;
        let bytes = envelope(
            &submit,
            id,
            "Owned cancellation race",
            "Exact original body",
        )?;
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
            "cancel invented a no-effect acknowledgment",
        )?;
        fs::write(io.join("release"), b"")?;
        let reply = pending.await?;
        let r = (
            reply.status,
            serde_json::from_slice::<serde_json::Value>(&reply.body)?,
        );
        completed(&r, effect)?;
        ensure(
            r.1["cancellation"] == "indeterminate"
                && posts(&io)? == before + u64::from(effect == "committed"),
            "late primary evidence/cancellation lost or duplicate dispatched",
        )?;
        cases.push(name.into());
    }
    set_mode(&io, "pause-post")?;
    let withdrawn_id = "77777777777777777777777777777777";
    let withdrawn = envelope(&submit, withdrawn_id, "Owned withdrawal race", "")?;
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
        .store(
            account()?,
            Token::new(b"factory-declared-review-create".to_vec())?,
        )
        .await?
        .connection;
    fs::write(io.join("release"), b"")?;
    ensure(
        pending.await.is_err(),
        "withdrawn/replaced generation delivered an old creation result",
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
        "reconsent replayed an unknown review",
    )?;
    cases.push("withdrawal-replacement".into());
    set_mode(&io, "pause-post")?;
    let stopped_id = "88888888888888888888888888888888";
    let stopped = envelope(&submit, stopped_id, "Owned restart after dispatch", "")?;
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
        "stopped runtime delivered an in-flight review",
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
    for i in 0..4 {
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "GET",
                &command(&format!("{:032x}", 100 + i)),
                &[],
            )
            .await?,
            "committed",
        )?;
    }
    ensure(
        posts(&io)? == before + 1,
        "retained restart repeated review creation",
    )?;
    cases.push("retained-restart".into());
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
        &command(lost_id),
        &[],
    )
    .await?;
    ensure(hidden.0 == 404, "other account read the captured review")?;
    let conflict = call(&runtime, &store, &other.connection, "POST", &submit, &lost).await?;
    ensure(
        conflict.0 == 409 && posts(&io)? == before + 1,
        "other account adopted or dispatched a captured review",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account-refusal".into());
    ensure(
        !io.join("unexpected-request").exists(),
        "review product fixture saw an unexpected request",
    )?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "review product left owned grants",
    )?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("review-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"actual_provider":false,"native_product_consumer":true,"capabilities_enabled":false,"provider_posts":posts(&io)?,"retained_restart":true,"maximum_writing_bytes":65536}),
        )?,
    )?;
    println!("PASS: composed native/Rust/Go/PG review creation retains exact writing, causal identity, readable destination, one-shot replay, ambiguous replies, cancel races, withdrawal, other-account refusal and restart; dummy-only provider, no actual GitHub or installed gate qualification");
    Ok(())
}

// Composed pending submission uses the actual native owner, Go HTTP/journal and
// PostgreSQL. Its external helper only reads this newly created owned journal
// and compares immutable preparation/receipt bytes; no row is seeded or edited.
pub(super) async fn pending_product(
    production: &Path,
    payload: &Path,
    helper: &Path,
    profile: &Path,
) -> Result<()> {
    use super::native_github_fixture::{call, completed, set_mode};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
        ProductRequest,
    };
    use std::{os::unix::fs::PermissionsExt, process::Command, sync::Arc};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::set_permissions(&io, fs::Permissions::from_mode(0o700))?;
    let mode = |name: &str| -> Result<()> {
        for marker in ["entered", "release", "cancelled", "verification-mode"] {
            match fs::remove_file(io.join(marker)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.into()),
            }
        }
        set_mode(&io, name)
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let journal = |id: &str, stage: &str| -> Result<()> {
        ensure(
            Command::new(helper)
                .arg(profile)
                .arg(id)
                .arg(stage)
                .status()?
                .success(),
            "owned journal proof failed",
        )
    };
    let primary = |r: &(u16, serde_json::Value)| -> Result<()> {
        ensure(
            matches!(r.0, 200 | 202) && r.1["effect"] == "committed" && r.1["finished"] == false,
            "causal primary lost or verification claimed before durable save",
        )
    };
    let uncertain = |r: &(u16, serde_json::Value)| -> Result<()> {
        ensure(
            matches!(r.0, 200 | 202)
                && r.1["effect"] == "indeterminate"
                && r.1["finished"] == false
                && r.1["review"].is_null(),
            "lost reply became attributable",
        )
    };
    mode("submit-start")?;
    let mut runtime = NativeSupervisor::start(payload, production, profile)?;
    let mut store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "pending product requires empty owned custody",
    )?;
    let account = || {
        Account::new(Provider::Github, "https://github.com", "321")
            .and_then(|a| a.with_label("declared-writer"))
    };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let mut connection = store.store(account()?, token()?).await?.connection;
    let catalogue = call(
        &runtime,
        &store,
        &connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "pending catalogue failed")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("canonical repository absent")?
        .to_owned();
    let submit = format!("/api/factory/collaboration/repositories/{repository}/pulls/7/reviews");
    let command = |id: &str| format!("{submit}/commands/{id}");
    let envelope = |id: &str, event: &str, body: &str| -> Result<Vec<u8>> {
        let request: ProductRequest = serde_json::from_value(
            json!({"kind":"write","path":submit,"body":{"command_id":id,"event":event,"body":body,"expected_head":"b".repeat(40)}}),
        )?;
        Ok(request.request()?.2)
    };
    let mut cases = Vec::new();
    mode("submit-unchanged-draft")?;
    let unchanged_id = format!("{:032x}", 9800);
    let before = writes()?;
    let unchanged = call(
        &runtime, &store, &connection, "POST", &submit,
        &envelope(&unchanged_id, "draft", "Exact pending writing 🍵\n[link](relative.md)")?,
    ).await?;
    ensure(
        unchanged.0 == 409 && writes()? == before,
        "unchanged draft summary dispatched a provider mutation",
    )?;
    cases.push("unchanged-draft-not-sent".to_owned());
    for (i, (name, event, body)) in [
        ("comment", "comment", String::new()),
        ("approve", "approve", String::new()),
        (
            "changes",
            "request_changes",
            "Exact replacement 🍵\n[link](relative.md)".into(),
        ),
        ("max-control", "comment", "\u{0001}".repeat(65536)),
        ("max-unicode", "approve", "🍵".repeat(16384)),
        ("historical-commit", "comment", String::new()),
        ("unknown-review-id", "approve", String::new()),
        ("big-ids", "comment", String::new()),
    ]
    .into_iter()
    .enumerate()
    {
        mode(&format!("submit-{name}"))?;
        if i == 0 {
            let stale_id = format!("{:032x}", 9900);
            let stale = serde_json::to_vec(&json!({"command_id":stale_id,"event":event,"body":body,"expected_head":"f".repeat(40)}))?;
            let before = writes()?;
            ensure(call(&runtime,&store,&connection,"POST",&submit,&stale).await?.0 == 409
                && call(&runtime,&store,&connection,"GET",&command(&stale_id),&[]).await?.0 == 404
                && writes()? == before, "pending review stale head was captured or dispatched")?;
            cases.push("selected-head-conflict-before-capture".into());
        }
        let id = format!("{:032x}", 100 + i);
        let bytes = envelope(&id, event, &body)?;
        let before = writes()?;
        let r = call(&runtime, &store, &connection, "POST", &submit, &bytes).await?;
        primary(&r)?;
        journal(&id, "primary")?;
        ensure(
            !r.1["review"].is_null(),
            "known primary lost readable canonical destination",
        )?;
        let external = r.1["review"]["external_id"]
            .as_str()
            .ok_or("review destination absent")?;
        let read = call(
            &runtime,
            &store,
            &connection,
            "GET",
            &format!("{submit}/{external}"),
            &[],
        )
        .await?;
        let expected = if body.is_empty() {
            "Exact pending writing 🍵\n[link](relative.md)"
        } else {
            &body
        };
        ensure(
            read.0 == 200 && read.1["body"] == expected,
            "pending submission changed summary writing",
        )?;
        completed(
            &call(&runtime, &store, &connection, "GET", &command(&id), &[]).await?,
            "committed",
        )?;
        journal(&id, "complete")?;
        completed(
            &call(&runtime, &store, &connection, "POST", &submit, &bytes).await?,
            "committed",
        )?;
        completed(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{}/cancel", command(&id)),
                b"{}",
            )
            .await?,
            "committed",
        )?;
        let conflict = call(
            &runtime,
            &store,
            &connection,
            "POST",
            &submit,
            &envelope(&id, event, "Different writing")?,
        )
        .await?;
        ensure(
            conflict.0 == 409 && writes()? == before + 1,
            "pending replay/conflict/cancel repeated dispatch",
        )?;
        cases.push(name.to_owned());
    }
    // All verifier failures retain the exact saved causal primary. Changes to
    // writing or comment identity cannot finish preservation from counts alone.
    mode("submit-verification")?;
    let id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    let bytes = envelope(id, "comment", "")?;
    let before = writes()?;
    primary(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
    journal(id, "primary")?;
    for fault in ["deny", "body", "writing", "identity", "count"] {
        fs::write(io.join("verification-mode"), fault)?;
        primary(&call(&runtime, &store, &connection, "GET", &command(id), &[]).await?)?;
        journal(id, "unfinished")?;
        primary(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
        primary(
            &call(
                &runtime,
                &store,
                &connection,
                "POST",
                &format!("{}/cancel", command(id)),
                b"{}",
            )
            .await?,
        )?;
        ensure(
            writes()? == before + 1,
            "verification failure resent primary",
        )?;
        cases.push(format!("verification-{fault}"));
    }
    // A current head advance or later dismissal does not erase a historical
    // valid primary when exact review/comment writing remains observable.
    fs::write(io.join("verification-mode"), "head-dismissed")?;
    completed(
        &call(&runtime, &store, &connection, "GET", &command(id), &[]).await?,
        "committed",
    )?;
    journal(id, "complete")?;
    cases.push("head-advance-dismissal".into());
    mode("submit-changed-count-reply")?;
    let id = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
    let bytes = envelope(id, "approve", "")?;
    let before = writes()?;
    primary(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
    journal(id, "primary")?;
    fs::write(io.join("verification-mode"), "restore-count")?;
    primary(&call(&runtime, &store, &connection, "GET", &command(id), &[]).await?)?;
    journal(id, "unfinished")?;
    primary(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
    ensure(
        writes()? == before + 1,
        "count mismatch erased historical discrepancy or repeated send",
    )?;
    cases.push("causal-count-mismatch".into());
    mode("submit-drop-reply")?;
    let lost_id = "cccccccccccccccccccccccccccccccc";
    let lost = envelope(lost_id, "comment", "Lost causal reply writing")?;
    let before = writes()?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    let read = call(
        &runtime,
        &store,
        &connection,
        "GET",
        &format!("{submit}/800"),
        &[],
    )
    .await?;
    ensure(
        read.0 == 200 && read.1["body"] == "Lost causal reply writing",
        "lost reply destination not normally readable",
    )?;
    uncertain(&call(&runtime, &store, &connection, "GET", &command(lost_id), &[]).await?)?;
    uncertain(&call(&runtime, &store, &connection, "POST", &submit, &lost).await?)?;
    ensure(
        writes()? == before + 1,
        "current matching review attributed lost reply",
    )?;
    cases.push("lost-reply".into());
    mode("submit-concurrent")?;
    let id = "dddddddddddddddddddddddddddddddd";
    let bytes = envelope(id, "approve", "")?;
    let before = writes()?;
    let (a, b) = tokio::join!(
        call(&runtime, &store, &connection, "POST", &submit, &bytes),
        call(&runtime, &store, &connection, "POST", &submit, &bytes)
    );
    for r in [a?, b?] {
        if r.1["effect"] == "indeterminate" {
            uncertain(&r)?
        } else if r.1["finished"] == true {
            completed(&r, "committed")?
        } else {
            primary(&r)?
        }
    }
    completed(
        &call(&runtime, &store, &connection, "GET", &command(id), &[]).await?,
        "committed",
    )?;
    ensure(
        writes()? == before + 1,
        "simultaneous pending submission repeated mutation",
    )?;
    cases.push("concurrent".into());
    mode("submit-cancel-race")?;
    let id = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
    let bytes = envelope(id, "comment", "Cancelled after sending")?;
    let before = writes()?;
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
        "cancel invented a no-effect acknowledgment",
    )?;
    fs::write(io.join("release"), b"")?;
    let reply = pending.await?;
    let r = (reply.status, serde_json::from_slice(&reply.body)?);
    primary(&r)?;
    ensure(
        r.1["cancellation"] == "indeterminate",
        "late unfinished primary erased cancellation",
    )?;
    journal(id, "primary")?;
    let r = call(&runtime, &store, &connection, "GET", &command(id), &[]).await?;
    completed(&r, "committed")?;
    journal(id, "complete")?;
    ensure(
        r.1["cancellation"] == "indeterminate" && writes()? == before + 1,
        "completion lost cancellation or repeated send",
    )?;
    cases.push("late-primary-cancel-completion".into());
    mode("submit-restart-known")?;
    let known_id = "ffffffffffffffffffffffffffffffff";
    let bytes = envelope(known_id, "comment", "")?;
    primary(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
    journal(known_id, "primary")?;
    runtime.stop()?;
    store.retire();
    runtime = NativeSupervisor::start(payload, production, profile)?;
    store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    completed(
        &call(
            &runtime,
            &store,
            &connection,
            "GET",
            &command(known_id),
            &[],
        )
        .await?,
        "committed",
    )?;
    journal(known_id, "complete")?;
    cases.push("known-primary-restart".into());
    for (name, id, withdraw) in [
        ("submit-withdraw", "11111111111111111111111111111111", true),
        ("submit-stop", "22222222222222222222222222222222", false),
    ] {
        mode(name)?;
        let bytes = envelope(id, "approve", "")?;
        let before = writes()?;
        let request = runtime.admit_product(
            store.clone(),
            store.capability(&connection).await?,
            Some("123".into()),
        )?;
        let pending = request.request("POST", &submit, &bytes);
        tokio::pin!(pending);
        super::native_github_fixture::entered(pending.as_mut(), &io).await?;
        if withdraw {
            store.withdraw(&connection).await?;
            connection = store.store(account()?, token()?).await?.connection;
            fs::write(io.join("release"), b"")?;
            ensure(
                pending.await.is_err(),
                "retired generation delivered old reply",
            )?;
        } else {
            runtime.stop()?;
            fs::write(io.join("release"), b"")?;
            ensure(
                pending.await.is_err(),
                "retired generation delivered old reply",
            )?;
            store.retire();
            runtime = NativeSupervisor::start(payload, production, profile)?;
            store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
        }
        uncertain(&call(&runtime, &store, &connection, "GET", &command(id), &[]).await?)?;
        uncertain(&call(&runtime, &store, &connection, "POST", &submit, &bytes).await?)?;
        ensure(
            writes()? == before + 1,
            "retained unknown pending submission repeated send",
        )?;
        cases.push(name.into());
    }
    let other = store
        .store(
            Account::new(Provider::Github, "https://github.com", "322")?
                .with_label("declared-other")?,
            Token::new(b"factory-declared-other".to_vec())?,
        )
        .await?;
    let before = writes()?;
    let r = call(
        &runtime,
        &store,
        &other.connection,
        "GET",
        &command(lost_id),
        &[],
    )
    .await?;
    ensure(r.0 == 404, "other account read pending capture")?;
    let r = call(&runtime, &store, &other.connection, "POST", &submit, &lost).await?;
    ensure(
        r.0 == 409 && writes()? == before,
        "other account adopted pending capture",
    )?;
    store.withdraw(&other.connection).await?;
    cases.push("other-account".into());
    // Pending-summary edit/clear now has connected coverage with the same review ID.
    ensure(
        !io.join("unexpected-request").exists()
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "unexpected/unserialized/unpaced provider request",
    )?;
    store.withdraw(&connection).await?;
    ensure(store.status().await?.is_empty(), "owned grants remain")?;
    store.retire();
    runtime.stop()?;
    fs::write(
        root.join("pending-review-product-result.json"),
        serde_json::to_vec_pretty(
            &json!({"cases":cases,"native_product_consumer":true,"actual_provider":false,"provider_mutations":writes()?,"immutable_journal_proved":true,"capabilities_enabled":false,"atomic_publication_qualified":false,"original_expiry_qualified":false}),
        )?,
    )?;
    println!("PASS: composed pending review submission persists the exact causal primary before separate writing/identity verification, retains uncertainty/cancellation across loss, custody and restart; dummy-only provider");
    Ok(())
}

// The external helper operates only on this newly created disposable profile.
// Its SQL trigger delays the first outcome transaction after capture commit;
// it never inserts/replaces a capture or changes its preparation/deadline.
pub(super) async fn expiry(
    production: &Path,
    payload: &Path,
    helper: &Path,
    profile: &Path,
    pending: bool,
    inline: bool,
) -> Result<()> {
    use super::native_github_fixture::{call, completed};
    use soda_desktop_runtime::{
        credentials::{Account, Credentials, Provider},
        supervisor::NativeSupervisor,
        ProductRequest,
    };
    use std::{
        process::Command,
        sync::Arc,
        time::{SystemTime, UNIX_EPOCH},
    };
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    fs::write(
        io.join("mode"),
        if inline {
            "inline-product-expiry"
        } else if pending {
            "submit-expiry"
        } else {
            "expiry"
        },
    )?;
    let runtime = NativeSupervisor::start(payload, production, profile)?;
    let store = Arc::new(Credentials::open(profile, &runtime.health()?.profile)?);
    ensure(
        store.status().await?.is_empty(),
        "expiry requires empty owned custody",
    )?;
    let connection = store
        .store(
            Account::new(Provider::Github, "https://github.com", "321")?,
            Token::new(if pending || inline {
                b"factory-declared-review-preparation".to_vec()
            } else {
                b"factory-declared-review-create".to_vec()
            })?,
        )
        .await?
        .connection;
    let catalogue = call(
        &runtime,
        &store,
        &connection,
        "GET",
        "/api/factory/collaboration/repositories",
        &[],
    )
    .await?;
    ensure(catalogue.0 == 200, "expiry catalogue failed")?;
    let repository = catalogue.1["items"][0]["id"]
        .as_str()
        .ok_or("expiry canonical repository absent")?;
    let resource = if inline { "inline-comments" } else { "reviews" };
    let submit = format!("/api/factory/collaboration/repositories/{repository}/pulls/7/{resource}");
    let id = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
    let command = format!("{submit}/commands/{id}");
    let input = if inline {
        json!({"command_id":id,"draft":true,"body":"Exact original retained inline 🍵","anchor":{"base":"a".repeat(40),"head":"b".repeat(40),"path":"src/tea.md","side":"new","scope":"lines","start_line":2,"end_line":3}})
    } else {
        json!({"command_id":id,"event":"comment","body":"Exact original retained review 🍵"})
    };
    let q: ProductRequest =
        serde_json::from_value(json!({"kind":"write","path":submit,"body":input}))?;
    let body = q.request()?.2;
    let run_helper = async |action: &str| -> Result<()> {
        let helper = helper.to_path_buf();
        let profile = profile.to_path_buf();
        let action = action.to_owned();
        let status = tokio::task::spawn_blocking(move || {
            Command::new(helper).arg(action).arg(profile).status()
        })
        .await??;
        ensure(status.success(), "owned expiry SQL helper refused")
    };
    run_helper("install").await?;
    let initial = call(&runtime, &store, &connection, "POST", &submit, &body).await;
    ensure(
        initial.is_err(),
        "delayed first outcome unexpectedly completed",
    )?;
    run_helper("capture").await?;
    run_helper("disarm").await?;
    let deadline = fs::read_to_string(io.join("expiry-deadline"))?.parse::<u64>()?;
    let now = SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs();
    ensure(
        deadline > now && deadline - now <= 300,
        "original expiry deadline was not captured in the future",
    )?;
    let queries = fs::read(io.join("queries"))?;
    let wait = deadline.saturating_add(1).saturating_sub(now);
    fs::write(
        root.join("expiry-wait.json"),
        serde_json::to_vec(
            &json!({"original_not_after":deadline,"wait_seconds":wait,"capture_unclaimed":true,"deadline_rewritten":false}),
        )?,
    )?;
    println!("original {resource} capture is unclaimed; waiting {wait} real seconds until its saved deadline passes");
    tokio::time::sleep(Duration::from_secs(wait)).await;
    let r = call(&runtime, &store, &connection, "POST", &submit, &body).await?;
    ensure(r.0 == 403, "expired original review was admitted")?;
    run_helper("verify").await?;
    ensure(
        fs::read(io.join("queries"))? == queries && !io.join("writes").exists(),
        "expired capture was reprepared or reached a mutation",
    )?;
    let lookup = call(&runtime, &store, &connection, "GET", &command, &[]).await?;
    ensure(
        lookup.0 == 200 && lookup.1["finished"] == false && lookup.1["effect"].is_null(),
        "expired unclaimed lookup invented a provider outcome",
    )?;
    let cancel = call(
        &runtime,
        &store,
        &connection,
        "POST",
        &format!("{command}/cancel"),
        b"{}",
    )
    .await?;
    completed(&cancel, "not_committed")?;
    ensure(
        cancel.1["cancellation"] == "cancelled",
        "expired unclaimed cancellation became uncertain",
    )?;
    run_helper("cancelled").await?;
    store.withdraw(&connection).await?;
    ensure(
        store.status().await?.is_empty(),
        "expiry left owned account",
    )?;
    store.retire();
    runtime.stop()?;
    ensure(
        !io.join("unexpected-request").exists() && !io.join("writes").exists(),
        "expiry sent undeclared or mutating provider request",
    )?;
    fs::write(
        root.join(if inline {
            "inline-expiry-result.json"
        } else {
            "review-expiry-result.json"
        }),
        serde_json::to_vec(
            &json!({"preparation_version":if pending{2}else{1},"actual_product_capture":true,"original_not_after":deadline,"waited_real_seconds":wait,"capture_unclaimed":true,"intent_deadline_unchanged":true,"no_reprepare":true,"provider_mutations":0,"expired_http_status":r.0,"local_cancellation_complete":true,"owned_account_removed":true,"native_go_postgresql":true,"real_github":false}),
        )?,
    )?;
    println!("PASS: real product {resource} capture survived its original five-minute expiry unclaimed; unchanged intent/deadline, no reprepare or mutation, exact local cancellation and clean retirement");
    Ok(())
}

pub(super) async fn pending_snapshot(profile: &Path) -> Result<()> {
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::Read;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let request = || Read::PullPendingReviewSnapshot {
        repository: "123".into(),
        number: 7,
    };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    let mut base_digest = None;
    for name in [
        "absent",
        "empty",
        "three",
        "reordered",
        "maximum",
        "max-unicode",
        "max-control",
        "empty-body",
        "unknown-id",
        "null-author",
        "null-author-node",
        "null-commits",
        "old-original",
        "big-ids",
    ] {
        mode(&format!("comments-{name}"))?;
        let v = client
            .observe(token()?, "321", &request())
            .await
            .map_err(|f| format!("pending comment snapshot {name}: {} {}", f.status, f.code))?;
        let p = &v["preparation"];
        let digest = &p["comments_snapshot"];
        ensure(
            v["kind"] == "pull_pending_review_snapshot"
                && p["repository"] == "123"
                && p["actor_node_id"] == "U_321",
            "snapshot lost current account/resource",
        )?;
        if name == "absent" {
            ensure(
                p["pending"].is_null() && digest.is_null(),
                "absent pending review acquired comment facts",
            )?;
            cases.push(name);
            continue;
        }
        let count = if name == "maximum" {
            1000
        } else if name == "empty" {
            0
        } else {
            3
        };
        let mut expected = Vec::new();
        for i in 0..count {
            let body = match name {
                "max-unicode" => "🍵".repeat(16384),
                "max-control" => "\u{0001}".repeat(65536),
                "empty-body" => String::new(),
                "maximum" => "x".into(),
                _ => format!("Exact pending comment {i} 🍵\n[link](relative.md)"),
            };
            let external = if name == "unknown-id" {
                serde_json::Value::Null
            } else if name == "big-ids" {
                json!((5000000000u64 + i).to_string())
            } else {
                json!((900 + i).to_string())
            };
            let author = match name {
                "null-author" => serde_json::Value::Null,
                "null-author-node" => {
                    json!({"kind":"Mannequin","node_id":null,"login":"unknown-author"})
                }
                _ => json!({"kind":"User","node_id":"U_321","login":"declared-owner"}),
            };
            let commit = if name == "null-commits" {
                serde_json::Value::Null
            } else {
                json!("b".repeat(40))
            };
            let original = if name == "null-commits" {
                serde_json::Value::Null
            } else if name == "old-original" {
                json!("c".repeat(40))
            } else {
                json!("b".repeat(40))
            };
            expected.push(json!({"node_id":format!("PC_{i}"),"external_id":external,"body":body,"author":author,"commit":commit,"original_commit":original,"created_at":"2026-11-05T12:00:00Z","updated_at":"2026-11-05T12:01:00Z"}));
        }
        expected.sort_by(|a, b| a["node_id"].as_str().cmp(&b["node_id"].as_str()));
        let hash = Sha256::digest(serde_json::to_vec(&expected)?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>();
        let pending_hash = Sha256::digest(serde_json::to_vec(&p["pending"])?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>();
        let mut writing = Vec::new();
        for item in &expected {
            writing.push(json!({"node_id":item["node_id"],"body_sha256":Sha256::digest(item["body"].as_str().ok_or("expected writing absent")?.as_bytes()).iter().map(|b|format!("{b:02x}")).collect::<String>()}));
        }
        let writing_hash = Sha256::digest(serde_json::to_vec(&writing)?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>();
        ensure(
            digest["count"] == count
                && digest["complete"] == true
                && digest["condition"] == "bounded_observation"
                && digest["sha256"] == hash
                && digest["identity_writing_sha256"] == writing_hash
                && p["pending_sha256"] == pending_hash
                && p["pending"]["node_id"] == "REV_owned",
            &format!("pending full identity/writing digest differs: {name}"),
        )?;
        if name == "three" {
            base_digest = Some(hash.clone())
        }
        if name == "reordered" {
            ensure(
                base_digest.as_deref() == Some(&hash),
                "page order changed the observed set digest",
            )?
        }
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&v)?)?;
        cases.push(name);
    }
    for (name, status) in [
        ("root-gone", 409),
        ("wrong-root", 409),
        ("wrong-owner", 409),
        ("submitted", 409),
        ("wrong-pull", 409),
        ("wrong-repo", 409),
        ("wrong-head", 409),
        ("count-change", 409),
        ("count-mismatch", 409),
        ("too-many", 413),
        ("too-many-page", 502),
        ("duplicate-node", 502),
        ("duplicate-id", 502),
        ("cursor-cycle", 502),
        ("empty-continuation", 502),
        ("wrong-comment-review", 502),
        ("wrong-comment-pull", 502),
        ("wrong-comment-repo", 502),
        ("published-comment", 502),
        ("missing-node", 502),
        ("invalid-id", 502),
        ("null-body", 502),
        ("large-body", 413),
        ("nul-body", 413),
        ("invalid-author", 502),
        ("invalid-author-node", 502),
        ("invalid-commit", 502),
        ("invalid-date", 502),
        ("aggregate-bytes", 413),
        ("pending-changed", 409),
        ("partial-errors", 502),
        ("denied", 403),
        ("oversized", 413),
        ("deadline", 504),
    ] {
        mode(&format!("comments-{name}"))?;
        let f = client
            .observe(token()?, "321", &request())
            .await
            .err()
            .ok_or("invalid pending comments passed")?;
        ensure(
            f.status == status,
            &format!(
                "pending snapshot {name}: {} {}, expected {status}",
                f.status, f.code
            ),
        )?;
        cases.push(name);
    }
    let before = fs::read(io.join("gets"))?;
    for q in [
        Read::PullPendingReviewSnapshot {
            repository: "0".into(),
            number: 7,
        },
        Read::PullPendingReviewSnapshot {
            repository: "123".into(),
            number: 0,
        },
    ] {
        ensure(
            client.observe(token()?, "321", &q).await.is_err(),
            "invalid snapshot selector accepted",
        )?
    }
    ensure(
        fs::read(io.join("gets"))? == before,
        "invalid snapshot reached provider",
    )?;
    mode("comments-pause")?;
    let q = request();
    let pending = client.observe(token()?, "321", &q);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        pending.await.is_err() && started.elapsed() < Duration::from_secs(2),
        "pending comments retirement blocked or delivered",
    )?;
    ensure(
        !io.join("writes").exists() && !io.join("unexpected-request").exists(),
        "pending snapshot sent mutation/undeclared request",
    )?;
    fs::write(
        root.join("pending-review-snapshot-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":2,"full_writing_identity_digest":true,"reordering_stable":true,"nullable_metadata_preserved":true,"maximum_comments":1000,"aggregate_bytes":8*1024*1024,"retirement":true,"provider_mutations":0,"real_github":false,"atomic_publication_condition":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("pending review comment snapshot passed {} declared cases, two invalid selectors and retirement; no mutations or atomic-publication claim",cases.len());
    Ok(())
}

pub(super) async fn publication(profile: &Path) -> Result<()> {
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::Read;
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let request = || Read::PullReviewPublication {
        repository: "123".into(),
        number: 7,
        review_id: "800".into(),
        review_node_id: "REV_owned".into(),
    };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let hash = |bytes: &[u8]| {
        Sha256::digest(bytes)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>()
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    let mut baseline = None;
    for name in [
        "commented",
        "approved",
        "changes-requested",
        "dismissed",
        "empty",
        "maximum",
        "max-control",
        "old-review-commit",
        "head-moves",
        "changed-dates",
        "changed-writing",
        "unknown-id",
        "null-author",
        "null-commits",
    ] {
        mode(&format!("publication-{name}"))?;
        let v = client
            .observe(token()?, "321", &request())
            .await
            .map_err(|f| format!("publication {name}: {} {}", f.status, f.code))?;
        let p = &v["publication"];
        let snapshot = &p["comments_snapshot"];
        let count = if name == "empty" {
            0
        } else if name == "maximum" {
            1000
        } else {
            3
        };
        let mut expected = Vec::new();
        for i in 0..count {
            let body = if name == "maximum" {
                "x".into()
            } else if name == "max-control" {
                "\u{0001}".repeat(65536)
            } else if name == "changed-writing" && i == 0 {
                "Changed published writing".into()
            } else {
                format!("Exact pending comment {i} 🍵\n[link](relative.md)")
            };
            expected.push(json!({"node_id":format!("PC_{i}"),"body_sha256":hash(body.as_bytes())}));
        }
        expected.sort_by(|a, b| a["node_id"].as_str().cmp(&b["node_id"].as_str()));
        let expected_hash = hash(&serde_json::to_vec(&expected)?);
        ensure(
            v["kind"] == "pull_review_publication"
                && v["account"]["external_id"] == "321"
                && p["repository"] == "123"
                && p["pull"]["external_id"] == "700"
                && p["review"]["external_id"] == "800"
                && p["review"]["node_id"] == "REV_owned"
                && p["observation_only"] == true
                && snapshot["count"] == count
                && snapshot["identity_writing_sha256"] == expected_hash
                && snapshot["complete"] == true,
            "publication lost scope or complete identity/writing facts",
        )?;
        if name == "commented" {
            baseline = Some(expected_hash.clone())
        }
        if name == "changed-dates" {
            ensure(
                baseline.as_deref() == Some(&expected_hash),
                "publication timestamps changed the writing digest",
            )?
        }
        if name == "changed-writing" {
            ensure(
                baseline.as_deref() != Some(&expected_hash),
                "changed writing was hidden",
            )?
        }
        if name == "old-review-commit" {
            ensure(
                p["review"]["commit"] == "c".repeat(40),
                "published review was repinned to current head",
            )?
        }
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&v)?)?;
        cases.push(name);
    }
    for (name, status) in [
        ("detail-gone", 404),
        ("detail-wrong-id", 409),
        ("detail-wrong-owner", 409),
        ("detail-wrong-pull", 409),
        ("detail-wrong-repo", 409),
        ("detail-pending", 409),
        ("detail-no-submission", 502),
        ("detail-large-body", 413),
        ("detail-invalid-commit", 502),
        ("detail-invalid-date", 502),
        ("detail-change", 409),
        ("wrong-root", 409),
        ("wrong-owner", 409),
        ("wrong-pull", 409),
        ("wrong-repo", 409),
        ("pending-comment", 502),
        ("count-change", 409),
        ("duplicate-node", 502),
        ("duplicate-id", 502),
        ("cursor-cycle", 502),
        ("null-body", 502),
        ("large-body", 413),
        ("invalid-date", 502),
        ("denied", 403),
        ("partial-errors", 502),
        ("oversized", 413),
        ("deadline", 504),
    ] {
        mode(&format!("publication-{name}"))?;
        let f = client
            .observe(token()?, "321", &request())
            .await
            .err()
            .ok_or("invalid publication passed")?;
        ensure(
            f.status == status,
            &format!(
                "publication refusal {name}: {} {}, expected {status}",
                f.status, f.code
            ),
        )?;
        cases.push(name);
    }
    let before = fs::read(io.join("gets"))?;
    for i in 0..3 {
        let mut q = request();
        if let Read::PullReviewPublication {
            repository,
            review_id,
            review_node_id,
            ..
        } = &mut q
        {
            match i {
                0 => *repository = "0".into(),
                1 => review_id.clear(),
                _ => review_node_id.clear(),
            }
        }
        ensure(
            client.observe(token()?, "321", &q).await.is_err(),
            "invalid publication selector accepted",
        )?;
    }
    ensure(
        fs::read(io.join("gets"))? == before,
        "invalid publication reached provider",
    )?;
    mode("publication-pause")?;
    let q = request();
    let pending = client.observe(token()?, "321", &q);
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        pending.await.is_err() && started.elapsed() < Duration::from_secs(2),
        "publication retirement blocked or delivered",
    )?;
    ensure(
        !io.join("writes").exists() && !io.join("unexpected-request").exists(),
        "publication reader sent mutation/undeclared request",
    )?;
    fs::write(
        root.join("review-publication-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":3,"identity_writing_digest":true,"publication_dates_distinct_from_writing":true,"historical_commit_preserved":true,"current_head_may_advance":true,"current_observation_only":true,"causal_attribution":false,"retirement":true,"provider_mutations":0,"real_github":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("review publication observer passed {} declared cases, three invalid selectors and retirement; current facts only, no mutations or attribution",cases.len());
    Ok(())
}

pub(super) async fn pending_submit(profile: &Path) -> Result<()> {
    use sha2::{Digest, Sha256};
    use soda_desktop_runtime::github_provider::{PendingReviewSubmit, Read, WriteOutcome};
    prepare(profile)?;
    let root = profile.parent().ok_or("fixture parent absent")?;
    let io = root.join("io");
    fs::create_dir(&io)?;
    let mode = |name: &str| -> Result<()> {
        for key in ["armed", "entered", "release", "cancelled"] {
            match fs::remove_file(io.join(key)) {
                Ok(()) => (),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.into()),
            }
        }
        fs::write(io.join("mode"), name)?;
        Ok(())
    };
    let writes = || -> Result<u64> {
        Ok(fs::read_to_string(io.join("writes"))
            .unwrap_or_else(|_| "0".into())
            .parse()?)
    };
    let token = || Token::new(b"factory-declared-review-preparation".to_vec());
    let hash = |s: &str| {
        Sha256::digest(s.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>()
    };
    let mut owner = tokio::task::spawn_blocking(Owner::start).await??;
    let client = owner.client();
    let mut cases = Vec::new();
    async fn saved(
        client: &soda_desktop_runtime::github_provider::Client,
        io: &Path,
    ) -> Result<PendingReviewSubmit> {
        let v = client
            .observe(
                Token::new(b"factory-declared-review-preparation".to_vec())?,
                "321",
                &Read::PullPendingReviewSnapshot {
                    repository: "123".into(),
                    number: 7,
                },
            )
            .await
            .map_err(|f| format!("pending submission preparation: {} {}", f.status, f.code))?;
        let p = &v["preparation"];
        let text = |v: &serde_json::Value| -> Result<String> {
            Ok(v.as_str().ok_or("native pending pin absent")?.to_owned())
        };
        let q = PendingReviewSubmit {
            repository: "123".into(),
            repository_node_id: text(&p["repository_node_id"])?,
            pull_id: text(&p["pull"]["external_id"])?,
            pull_node_id: text(&p["pull"]["node_id"])?,
            number: 7,
            head: text(&p["pull"]["head"])?,
            review_id: p["pending"]["external_id"].as_str().map(str::to_owned),
            review_node_id: text(&p["pending"]["node_id"])?,
            review_commit: text(&p["pending"]["commit"])?,
            pending_sha256: text(&p["pending_sha256"])?,
            comments_sha256: text(&p["comments_snapshot"]["identity_writing_sha256"])?,
            comments_count: p["comments_snapshot"]["count"]
                .as_u64()
                .ok_or("native pending count absent")?,
            event: "comment".into(),
            body: None,
        };
        fs::write(io.join("armed"), b"")?;
        Ok(q)
    }
    for name in [
        "comment",
        "approve",
        "request_changes",
        "explicit-body",
        "clear-approval",
        "max-unicode",
        "max-control",
        "historical-commit",
        "unknown-review-id",
        "big-ids",
        "changed-count-reply",
        "no-request-id",
    ] {
        mode(&format!("submit-{name}"))?;
        let mut q = saved(&client, &io).await?;
        if name == "approve" || name == "clear-approval" {
            q.event = "approve".into()
        };
        if name == "request_changes" {
            q.event = name.into()
        }
        if name == "explicit-body" {
            q.body = Some("Exact replacement summary 🍵".into())
        };
        if name == "clear-approval" {
            q.body = Some(String::new())
        };
        if name == "max-unicode" {
            q.body = Some("🍵".repeat(16384))
        };
        if name == "max-control" {
            q.body = Some("\u{0001}".repeat(65536))
        }
        let before = writes()?;
        let r = match client.submit_pending_review(token()?, "321", &q).await {
            WriteOutcome::Applied(r) => r,
            WriteOutcome::NotSent(f) | WriteOutcome::Indeterminate(f) => {
                return Err(format!("pending submit {name}: {} {}", f.status, f.code).into())
            }
        };
        let body = q
            .body
            .as_deref()
            .unwrap_or("Exact pending writing 🍵\n[link](relative.md)");
        ensure(
            r["pending"]["node_id"] == q.review_node_id
                && r["pending"]["sha256"] == q.pending_sha256
                && r["review"]["node_id"] == q.review_node_id
                && r["review"]["body_sha256"] == hash(body)
                && r["review"]["commit"] == q.review_commit
                && r["event"] == q.event
                && r["publication_verification"] == "required"
                && writes()? == before + 1,
            "pending causal primary lost identity/body/commit or claimed verification",
        )?;
        if name == "changed-count-reply" {
            ensure(
                r["review"]["comments_count"] == 4 && r["expected_comments"]["count"] == 3,
                "known primary/count difference was discarded",
            )?
        } else {
            let id = r["review"]["external_id"]
                .as_str()
                .ok_or("causal review ID absent")?
                .to_owned();
            let v = client
                .observe(
                    token()?,
                    "321",
                    &Read::PullReviewPublication {
                        repository: q.repository.clone(),
                        number: 7,
                        review_id: id,
                        review_node_id: q.review_node_id.clone(),
                    },
                )
                .await
                .map_err(|f| format!("pending publication {name}: {} {}", f.status, f.code))?;
            ensure(
                v["publication"]["comments_snapshot"]["identity_writing_sha256"]
                    == q.comments_sha256
                    && v["publication"]["review"]["body_sha256"] == hash(body),
                "submitted existing review lost exact comment identities/writing",
            )?;
        }
        fs::write(root.join(format!("{name}.json")), serde_json::to_vec(&r)?)?;
        cases.push(name);
    }
    for name in [
        "missing-pending",
        "read-only",
        "locked",
        "closed",
        "merged",
        "unknown-merged",
        "identity-denied",
        "changed-pending-hash",
        "changed-comments-hash",
        "changed-count",
        "changed-review-node",
        "changed-review-id",
        "changed-review-commit",
        "changed-pull-id",
        "changed-pull-node",
        "changed-repo-node",
        "changed-head",
        "empty-changes",
        "preflight-deadline",
    ] {
        mode(&format!("submit-{name}"))?;
        let mut q = saved(&client, &io).await?;
        match name {
            "changed-pending-hash" => q.pending_sha256 = "0".repeat(64),
            "changed-comments-hash" => q.comments_sha256 = "0".repeat(64),
            "changed-count" => q.comments_count += 1,
            "changed-review-node" => q.review_node_id = "REV_other".into(),
            "changed-review-id" => q.review_id = Some("801".into()),
            "changed-review-commit" => q.review_commit = "d".repeat(40),
            "changed-pull-id" => q.pull_id = "701".into(),
            "changed-pull-node" => q.pull_node_id = "P_other".into(),
            "changed-repo-node" => q.repository_node_id = "R_other".into(),
            "changed-head" => q.head = "d".repeat(40),
            "empty-changes" => {
                q.event = "request_changes".into();
                q.body = Some(String::new())
            }
            _ => (),
        }
        let before = writes()?;
        ensure(
            matches!(
                client.submit_pending_review(token()?, "321", &q).await,
                WriteOutcome::NotSent(_)
            ) && writes()? == before,
            &format!("pending pre-send {name} attempted an effect"),
        )?;
        cases.push(name);
    }
    for name in [
        "denied",
        "validation",
        "limited",
        "server-error",
        "redirect",
        "malformed",
        "oversized",
        "partial-errors",
        "drop-reply",
        "deadline",
        "wrong-node",
        "wrong-id",
        "wrong-state",
        "wrong-body",
        "wrong-author",
        "wrong-repo",
        "wrong-pull",
        "wrong-pull-id",
        "wrong-number",
        "wrong-head",
        "wrong-parent-repo",
        "wrong-commit",
        "wrong-commit-repo",
        "wrong-date",
        "missing-submission",
        "oversized-request-id",
        "duplicate-request-id",
    ] {
        mode(&format!("submit-{name}"))?;
        let q = saved(&client, &io).await?;
        let before = writes()?;
        let outcome = client.submit_pending_review(token()?, "321", &q).await;
        ensure(
            matches!(outcome, WriteOutcome::Indeterminate(_)) && writes()? == before + 1,
            &format!("pending post-send {name} lost uncertainty or retried"),
        )?;
        if name == "drop-reply" {
            let v = client
                .observe(
                    token()?,
                    "321",
                    &Read::PullReviewPublication {
                        repository: "123".into(),
                        number: 7,
                        review_id: "800".into(),
                        review_node_id: q.review_node_id.clone(),
                    },
                )
                .await
                .map_err(|f| format!("lost pending publication: {} {}", f.status, f.code))?;
            ensure(
                v["publication"]["review"]["external_id"] == "800"
                    && matches!(outcome, WriteOutcome::Indeterminate(_)),
                "current publication attributed lost reply",
            )?;
        }
        cases.push(name);
    }
    mode("submit-invalid")?;
    let q = saved(&client, &io).await?;
    let before = fs::read(io.join("gets"))?;
    let w = writes()?;
    for i in 0..15 {
        let mut q = q.clone();
        match i {
            0 => q.repository = "0".into(),
            1 => q.repository_node_id.clear(),
            2 => q.pull_id = "0".into(),
            3 => q.pull_node_id.clear(),
            4 => q.number = 0,
            5 => q.head = "invalid".into(),
            6 => q.review_node_id.clear(),
            7 => q.review_id = Some("0".into()),
            8 => q.review_commit = "invalid".into(),
            9 => q.pending_sha256 = "a".repeat(40),
            10 => q.comments_sha256 = "a".repeat(40),
            11 => q.comments_count = 1001,
            12 => q.event = "draft".into(),
            13 => q.body = Some("x".repeat(65537)),
            _ => q.body = Some("\0".into()),
        };
        ensure(
            matches!(
                client.submit_pending_review(token()?, "321", &q).await,
                WriteOutcome::NotSent(_)
            ),
            "invalid pending descriptor sent",
        )?;
    }
    ensure(
        fs::read(io.join("gets"))? == before && writes()? == w,
        "invalid pending descriptor reached provider",
    )?;
    mode("submit-serialize")?;
    let q = saved(&client, &io).await?;
    let first_token = token()?;
    let first =
        async { Ok::<_, String>(client.submit_pending_review(first_token, "321", &q).await) };
    tokio::pin!(first);
    super::native_github_fixture::entered(first.as_mut(), &io).await?;
    let before = writes()?;
    let clone = client.clone();
    let second = clone.submit_pending_review(token()?, "321", &q);
    tokio::pin!(second);
    tokio::select! {_=&mut second=>return Err("pending clone completed while predecessor paused".into()),_=tokio::time::sleep(Duration::from_millis(1200))=>()}
    ensure(
        writes()? == before,
        "pending clone sent before predecessor returned",
    )?;
    fs::write(io.join("release"), b"")?;
    ensure(
        matches!(first.await?, WriteOutcome::Applied(_))
            && matches!(second.await, WriteOutcome::NotSent(_))
            && writes()? == before,
        "pending serialization/replay duplicated submission",
    )?;
    mode("submit-pause-post")?;
    let q = saved(&client, &io).await?;
    let t = token()?;
    let pending = async { Ok::<_, String>(client.submit_pending_review(t, "321", &q).await) };
    tokio::pin!(pending);
    super::native_github_fixture::entered(pending.as_mut(), &io).await?;
    let started = Instant::now();
    tokio::task::spawn_blocking(move || owner.stop()).await??;
    ensure(
        matches!(pending.await?, WriteOutcome::Indeterminate(_))
            && started.elapsed() < Duration::from_secs(2),
        "pending retirement lost uncertainty or blocked",
    )?;
    let w = writes()?;
    ensure(
        matches!(
            client.submit_pending_review(token()?, "321", &q).await,
            WriteOutcome::NotSent(_)
        ) && writes()? == w,
        "retired pending owner mutated",
    )?;
    ensure(
        !io.join("unexpected-request").exists()
            && !io.join("overlap").exists()
            && !io.join("too-fast").exists(),
        "pending fixture saw undeclared request/overlap/pacing failure",
    )?;
    fs::write(
        root.join("pending-review-submit-result.json"),
        serde_json::to_vec(
            &json!({"cases":cases,"invalid_descriptors":15,"causal_primary_separate_from_verification":true,"same_pending_identity":true,"preserve_or_replace_summary":true,"cloned_serialization":true,"pacing":true,"retirement":true,"provider_mutations":writes()?,"real_github":false,"atomic_publication_condition":false,"product_consumer_connected":false}),
        )?,
    )?;
    println!("pending review submission passed {} declared cases, 15 invalid descriptors, serialization/pacing and retirement; causal primary requires separate verification",cases.len());
    Ok(())
}
