//! Real private-file/restart check for native MCP key custody. Uses only a
//! disposable profile and no credential, model or worker process.
use hmac::{Hmac, KeyInit, Mac};
use sha2::Sha256;
use soda_desktop_runtime::mcp_signing::Signer;
use soda_desktop_runtime::supervisor::NativeSupervisor;
use soda_unix_http::{request_authenticated_async, Limits, Response};
use std::fs::{self, DirBuilder, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::time::Duration;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.first().is_some_and(|arg| arg == "exec") {
        let fixture = fs::read("/proc/self/exe")?;
        if fixture.ends_with(b"\0factory-tool-pause-probe") {
            fs::write("probe-entered", std::process::id().to_string())?;
            std::thread::sleep(Duration::from_secs(120));
            return Err("declared stalled protocol probe".into());
        }
        if fixture.ends_with(b"\0factory-tool-invalid-flags") {
            return Err("declared incompatible CLI flags".into());
        }
        if fixture.ends_with(b"\0factory-tool-invalid-json") && !args.iter().any(|arg| arg == "--help") {
            println!("{{\"schema_version\":1,\"payload_type\":\"run.terminal.failed\"}}");
            return Ok(());
        }
        return muse_fixture(&args);
    }
    if args.first().is_some_and(|arg| arg == "tool-capture") {
        use sha2::Digest;
        let captured = fs::read("/proc/self/exe")?;
        let captured_sha: String = Sha256::digest(&captured).iter().map(|byte| format!("{byte:02x}")).collect();
        println!("{captured_sha}");
        std::io::stdout().flush()?;
        let mut input = libc::pollfd { fd: 0, events: libc::POLLIN, revents: 0 };
        if unsafe { libc::poll(&mut input, 1, 30000) } != 1 {
            return Err("captured tool release missing".into());
        }
        if fs::read("/proc/self/exe")? != captured {
            return Err("active captured executable changed".into());
        }
        println!("{captured_sha}");
        return Ok(());
    }
    if args
        .first()
        .is_some_and(|arg| arg == "muse-fixture-descendant")
    {
        std::thread::sleep(Duration::from_secs(120));
        return Ok(());
    }
    let root = PathBuf::from(args.first().ok_or("scratch root required")?);
    if args.len() == 2 && args[1] == "tool-races" {
        return tokio::runtime::Builder::new_current_thread().enable_all().build()?
            .block_on(tool_races(&root));
    }
    if args.len() == 3 && args[2] == "workspace" {
        return workspace_check(&root, Path::new(&args[1]));
    }
    if args.len() == 2 && args[1] == "workspace-readback" {
        return workspace_receipt_readback(&root);
    }
    if args.len() == 5 && args[1] == "workspace-retire" {
        soda_desktop_runtime::factory_provider::Broker::retire_bundle(&args[2], &args[3], &args[4])?;
        println!("PASS: exact owned workspace fixture root retired");
        return Ok(());
    }
    if args.len() == 2 && args[1] == "task-manifest" {
        return task_manifest_bindings(&root);
    }
    if args.len() == 5 && args[4] == "schema" {
        let profile = root.join("schema-profile");
        for restart in 0..2 {
            let supervisor = NativeSupervisor::start(Path::new(&args[1]), Path::new(&args[2]), &profile)?;
            let ready = supervisor.health()?;
            if !Command::new(&args[3])
                .arg(&profile).arg(&ready.instance).arg("check-native-schema").arg(if restart == 0 { "fresh" } else { "restart" })
                .status()?.success() {
                return Err("native schema composition check failed".into());
            }
            supervisor.stop()?;
        }
        println!("PASS: actual standalone startup/restart omits application hosting and retains product storage; retained shared migration preserves rows and hosting verification");
        return Ok(());
    }
    if args.len() == 5 && args[4] == "judgment-live" {
        let profile=root.join("judgment-profile");
        for phase in ["start","restart"] {
            let supervisor=NativeSupervisor::start(Path::new(&args[1]),Path::new(&args[2]),&profile)?;
            let ready=supervisor.health()?;
            if phase=="start" && !Command::new(&args[3]).arg(&profile).arg(&ready.instance).arg("check-owned-tool").arg("discovery").status()?.success() { return Err("official judgment tool unavailable".into()); }
            let checked=Command::new(&args[3]).arg(&profile).arg(&ready.instance).arg("check-live-judgment").arg(phase).status()?;
            supervisor.stop()?;
            if !checked.success() { return Err("live judgment check failed; original command retained, no inference replay".into()); }
        }
        println!("PASS: one live official-tool judgment through production private callback, exact-source accounting and retained readback across supervisor restart; repository authority declared, no GitHub writes or worker gate proof");
        return Ok(());
    }
    if args.len() == 3 && matches!(args[2].as_str(), "muse-connector" | "muse-login") {
        return muse_connector(&root, Path::new(&args[1]), args[2] == "muse-login");
    }
    if args.len() == 2 && matches!(args[1].as_str(),"judgment-connector"|"judgment-fixture") {
        return judgment_connector(&root,args[1]=="judgment-fixture");
    }
    if args.len() == 2 && args[1] == "preparation" {
        return preparation_check(&root);
    }
    if args.len() == 2 && args[1] == "preparation-parallel" {
        return parallel_preparations(&root);
    }
    if args.len() == 5 && args[4] == "preparation-managed" {
        return managed_preparations(
            &root,
            Path::new(&args[1]),
            Path::new(&args[2]),
            Path::new(&args[3]),
        );
    }
    if args.len() == 5 && matches!(args[4].as_str(), "tool" | "tool-discovery" | "tool-update" | "tool-cadence") {
        let started = std::time::Instant::now();
        let supervisor = NativeSupervisor::start(
            Path::new(&args[1]),
            Path::new(&args[2]),
            &root.join("tool-profile"),
        )?;
        let ready = supervisor.health()?;
        let check = |mode: &str| -> Result<(), Box<dyn std::error::Error>> {
            if !Command::new(&args[3])
                .arg(root.join("tool-profile"))
                .arg(&ready.instance)
                .arg("check-owned-tool")
                .arg(mode)
                .status()?
                .success()
            {
                return Err("native tool callback check failed".into());
            }
            Ok(())
        };
        if args[4] == "tool" {
            if soda_desktop_runtime::factory_provider::Tools::discover(&root).is_err() {
                check("unavailable")?;
            }
            tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()?
                .block_on(supervisor.select_execution_tools(Path::new(&std::env::var(
                    "SODA_NATIVE_CHECK_TOOL_DIRECTORY",
                )?)))?;
        }
        check(if args[4] == "tool-update" || args[4] == "tool-cadence" {
            "update"
        } else if args[4] == "tool-discovery" {
            "discovery"
        } else {
            "current"
        })?;
        if args[4] == "tool-cadence" {
            fs::write(root.join("mode"), "v2")?;
            while !root.join("cadence-entered").is_file() {
                if started.elapsed() > Duration::from_secs(330) {
                    return Err("native five-minute release refresh did not run".into());
                }
                std::thread::sleep(Duration::from_millis(100));
            }
            if started.elapsed() < Duration::from_secs(290) {
                return Err("cadence observation was not the five-minute refresh".into());
            }
            check("update")?;
            if !fs::read_dir(root.join("tool-profile/native-tools/muse"))?.filter_map(Result::ok)
                .any(|entry| entry.file_name().to_string_lossy().starts_with("muse-bin-fixture-v2-"))
            {
                return Err("background refresh did not select the changed release".into());
            }
            println!("PASS: actual joined native refresh owner detected a declared changed release at its five-minute tick without a foreground inspection triggering the refresh; Go/native readback selects the new verified ELF");
        }
        supervisor.stop()?;
        if args[4] == "tool-update" {
            let cache = root.join("tool-profile/native-tools/muse");
            let files = fs::read_dir(&cache)?.collect::<Result<Vec<_>, _>>()?;
            if files.len() != 1
                || !files[0]
                    .file_name()
                    .to_string_lossy()
                    .starts_with("muse-bin-")
            {
                return Err(
                    "tool update did not publish one artifact and retire its pending file".into(),
                );
            }
            let before = files[0].metadata()?;
            if before.mode() & 0o777 != 0o500 {
                return Err("cached tool permissions differ".into());
            }
            let restarted = NativeSupervisor::start(
                Path::new(&args[1]),
                Path::new(&args[2]),
                &root.join("tool-profile"),
            )?;
            let current = restarted.health()?;
            if !Command::new(&args[3])
                .arg(root.join("tool-profile"))
                .arg(&current.instance)
                .arg("check-owned-tool")
                .arg("update")
                .status()?
                .success()
            {
                return Err("cached tool was not reused after restart".into());
            }
            restarted.stop()?;
            let after = files[0].path().symlink_metadata()?;
            if before.dev() != after.dev()
                || before.ino() != after.ino()
                || before.len() != after.len()
            {
                return Err("restart replaced the verified cached tool".into());
            }
            println!("PASS: official public artifact fetched into private Factory cache, exact Go callback/hash/permissions, no pending file, retained inode after restart; no CLI, login or model execution");
        }
        return Ok(());
    }
    if args.len() == 5 && args[4] == "execution" {
        return tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?
            .block_on(managed_execution(
                &root,
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
    }
    if args.len() == 5 && args[4] == "execution-muse" {
        return tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?
            .block_on(managed_muse_execution(
                &root,
                Path::new(&args[1]),
                Path::new(&args[2]),
                Path::new(&args[3]),
            ));
    }
    if args.len() == 5 && matches!(args[4].as_str(), "recover-muse" | "recover-admitted") {
        let profile = root.join("execution-profile");
        let supervisor =
            NativeSupervisor::start(Path::new(&args[1]), Path::new(&args[2]), &profile)?;
        let ready = supervisor.health()?;
        let fixture = profile.join("admitted-native-fixture.json");
        let captured: serde_json::Value = serde_json::from_slice(&fs::read(&fixture)?)?;
        if args[4] == "recover-muse"
            && captured["Tasks"] == true
            && captured["ParentRun"]
                .as_str()
                .is_some_and(|s| !s.is_empty())
        {
            tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()?
                .block_on(supervisor.start_execution_coordinator())?;
            for operation in ["check-admitted-native", "settled-admitted-native"] {
                if !Command::new(&args[3])
                    .arg(&profile)
                    .arg(&ready.instance)
                    .arg(operation)
                    .arg(&fixture)
                    .status()?
                    .success()
                {
                    return Err("retained task result or settlement unavailable".into());
                }
            }
            supervisor.stop()?;
            println!("PASS: original completed admitted task run retained across restart and canonically settled without task or inference replay");
            return Ok(());
        }
        if !Command::new(&args[3])
            .arg(&profile)
            .arg(&ready.instance)
            .arg("reconcile-retained-native")
            .arg(profile.join("admitted-native-fixture.json"))
            .status()?
            .success()
        {
            return Err("original retained execution reconciliation failed".into());
        }
        supervisor.stop()?;
        return Ok(());
    }
    if args.len() == 3 || args.len() == 4 {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?;
        runtime.block_on(managed(
            &root,
            Path::new(&args[1]),
            Path::new(&args[2]),
            args.get(3).map(Path::new),
        ))?;
    } else if args.len() != 1 {
        return Err(
            "expected scratch root, optional PostgreSQL payload, Go backend and client".into(),
        );
    }
    let mut random = [0u8; 8];
    getrandom::fill(&mut random)?;
    let name: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    let profile = root.join(&name);
    DirBuilder::new().mode(0o700).create(&profile)?;
    let profile = fs::canonicalize(profile)?;
    let id = "0123456789abcdef0123456789abcdef";
    let digest = "a".repeat(64);
    let other = "b".repeat(64);
    let signer = Signer::open(&profile)?;
    let first = signer.derive(id, &digest)?;
    assert_eq!(first.len(), 64);
    let mut reference = Hmac::<Sha256>::new_from_slice(&fs::read(profile.join("native-mcp.key"))?)?;
    reference.update(format!("{id}\n{digest}").as_bytes());
    let expected: String = reference
        .finalize()
        .into_bytes()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    assert_eq!(first, expected);
    assert_ne!(first, signer.derive(id, &other)?);
    assert!(signer.derive("invalid", &digest).is_err());
    assert!(signer.derive(id, &"A".repeat(64)).is_err());
    drop(signer);
    let reopened = Signer::open(&profile)?;
    assert_eq!(first, reopened.derive(id, &digest)?);
    drop(reopened);
    let path = profile.join("native-mcp.key");
    let metadata = fs::symlink_metadata(&path)?;
    assert!(metadata.is_file());
    assert_eq!(metadata.len(), 32);
    assert_eq!(metadata.uid(), unsafe { libc::geteuid() });
    assert_eq!(metadata.permissions().mode() & 0o077, 0);
    OpenOptions::new()
        .write(true)
        .truncate(true)
        .open(path)?
        .flush()?;
    assert!(Signer::open(&profile).is_err());
    let interrupted = root.join(format!("mcp-pending-{name}"));
    DirBuilder::new().mode(0o700).create(&interrupted)?;
    let interrupted = fs::canonicalize(interrupted)?;
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(interrupted.join("native-mcp.key.pending"))?
        .write_all(b"interrupted first write")?;
    let recovered = Signer::open(&interrupted)?;
    let scoped = recovered.derive(id, &digest)?;
    assert!(!interrupted.join("native-mcp.key.pending").exists());
    assert!(interrupted.join("native-mcp.key.identity").is_file());
    drop(recovered);
    assert_eq!(Signer::open(&interrupted)?.derive(id, &digest)?, scoped);
    let legacy = root.join(format!("mcp-before-marker-{name}"));
    DirBuilder::new().mode(0o700).create(&legacy)?;
    let legacy = fs::canonicalize(legacy)?;
    let mut retained_key = [0u8; 32];
    getrandom::fill(&mut retained_key)?;
    let key_path = legacy.join("native-mcp.key");
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&key_path)?;
    file.write_all(&retained_key)?;
    file.sync_all()?;
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(legacy.join("native-mcp.key.identity.pending"))?
        .write_all(b"interrupted marker write")?;
    let adopted = Signer::open(&legacy)?;
    assert!(legacy.join("native-mcp.key.identity").is_file());
    assert!(!legacy.join("native-mcp.key.identity.pending").exists());
    let adopted_secret = adopted.derive(id, &digest)?;
    let mut reference = Hmac::<Sha256>::new_from_slice(&retained_key)?;
    reference.update(format!("{id}\n{digest}").as_bytes());
    let expected: String = reference
        .finalize()
        .into_bytes()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    retained_key.fill(0);
    assert_eq!(adopted_secret, expected);
    drop(adopted);
    assert_eq!(Signer::open(&legacy)?.derive(id, &digest)?, adopted_secret);
    fs::write(&key_path, [0u8; 32])?;
    assert!(Signer::open(&legacy).is_err());
    fs::remove_file(&key_path)?;
    assert!(Signer::open(&legacy).is_err());
    println!("PASS: native MCP private key, scoped derivation, restart, interrupted creation, pre-marker adoption, changed-key and missing-key refusal");
    Ok(())
}

fn muse_fixture(args: &[String]) -> Result<(), Box<dyn std::error::Error>> {
    let option = |name: &str| -> Result<&str, Box<dyn std::error::Error>> {
        args.iter()
            .position(|arg| arg == name)
            .and_then(|i| args.get(i + 1))
            .map(String::as_str)
            .ok_or_else(|| format!("missing fixed CLI option {name}").into())
    };
    for flag in [
        "--disable-shell",
        "--disable-write",
        "--disable-web-tools",
        "--no-session-log",
        "--disable-request-user-input",
        "--no-foreign-personal-context",
    ] {
        if !args.iter().any(|arg| arg == flag) {
            return Err(format!("missing fixed CLI flag {flag}").into());
        }
    }
    if args.iter().any(|arg| arg == "--help") && option("--model")? == "factory-protocol-probe" {
        return Ok(());
    }
    if option("--provider")? == "echo" {
        if fs::read_to_string(option("--prompt-file")?)? != "Factory native protocol probe"
            || std::env::var_os("META_API_KEY").is_some()
            || std::env::var_os("SODA_FACTORY_RUN_AUTH").is_some()
        {
            return Err("declared offline protocol probe differs".into());
        }
        println!("{}", serde_json::json!({"schema_version":1,"payload_type":"run.terminal.completed","payload":{"terminal":"completed","text":"echo: Factory native protocol probe"}}));
        return Ok(());
    }
    if option("--preset")? != "empty"
        || option("--approval-mode")? != "never"
        || !matches!(option("--max-model-steps")?, "1" | "3")
        || option("--model")? != "muse-spark-1.3-contributor"
        || option("--reasoning-effort")? != "low"
        || option("--contributor-mode")? != "on"
        || !args.iter().any(|arg| arg == "run.toolset=[]")
        || !args.iter().any(|arg| arg == "hooks={}")
        || !args.iter().any(|arg| arg == "managed_hooks_path=null")
        || std::env::var_os("META_API_KEY").is_some()
        || std::env::var_os("SODA_FACTORY_RUN_AUTH").is_some()
        || args
            .iter()
            .any(|arg| matches!(arg.as_str(), "--api-key-stdin" | "--base-url"))
    {
        return Err("fixed Muse CLI source contract differs".into());
    }
    let prompt = fs::read_to_string(option("--prompt-file")?)?;
    if prompt == "wait" {
        let mut child = Command::new("/proc/self/exe")
            .arg("muse-fixture-descendant")
            .spawn()?;
        fs::write("child.pid", child.id().to_string())?;
        println!("fixture_wait_ready");
        std::io::stdout().flush()?;
        let _ = child.wait()?;
    } else if prompt.starts_with("Make one Factory product judgment") {
        let input:serde_json::Value=serde_json::from_str(prompt.split_once('\n').ok_or("judgment input absent")?.1)?;
        let options=input["options"].as_object().ok_or("judgment options absent")?;
        let selected=if options.contains_key("ready_to_merge") {"ready_to_merge"}else if options.contains_key("discussion") {"discussion"}else{options.keys().next().ok_or("judgment alternatives absent")?};
        let probabilities:serde_json::Map<String,serde_json::Value>=options.keys().map(|key|(key.clone(),serde_json::json!(if key==selected{1}else{0}))).collect();
        let answer=serde_json::json!({"selected":selected,"confidence":1,"probabilities":probabilities});
        println!("{}",serde_json::json!({"schema_version":1,"payload_type":"run.terminal.completed","payload":{"terminal":"completed","text":answer.to_string()}}));
    } else if prompt.starts_with("Answer the independent Factory consultation") {
        let input:serde_json::Value=serde_json::from_str(prompt.split_once('\n').ok_or("consultation input absent")?.1)?;
        let kind=input["type"].as_str().ok_or("consultation type absent")?;
        let mut answers=serde_json::Map::new();
        for (id,question) in input["questions"].as_object().ok_or("consultation questions absent")? {
            let answer=match kind {
                "noul"=>serde_json::json!({"type":"noul","noul":0.8}),
                "score"=>serde_json::json!({"type":"score","score":0.75,"confidence":0.9,"legend":{"0":question["criteria"][0],"1":question["criteria"][1]},"probabilities":{"0":0.25,"1":0.75}}),
                "choice"=>serde_json::json!({"type":"choice","choice":"contribute","confidence":0.9,"probabilities":{"contribute":0.95,"no_contribution":0.05}}),
                _=>return Err("unknown consultation type".into()),
            };
            answers.insert(id.clone(),answer);
        }
        let answer=serde_json::json!({"answers":answers});
        println!("{}",serde_json::json!({"schema_version":1,"payload_type":"run.terminal.completed","payload":{"terminal":"completed","text":answer.to_string()}}));
    } else if prompt == "judgment" {
        println!("{{\"schema_version\":1,\"payload_type\":\"run.terminal.completed\",\"payload\":{{\"terminal\":\"completed\",\"text\":\"{{\\\"selected\\\":\\\"new_issue\\\",\\\"confidence\\\":1,\\\"probabilities\\\":{{\\\"new_issue\\\":1,\\\"discussion\\\":0}}}}\"}}}}");
    } else if prompt == "finish" {
        println!("{{\"fixture\":\"ok\"}}");
    } else {
        return Err("unknown connector fixture prompt".into());
    }
    Ok(())
}

fn judgment_connector(root: &Path, prepared: bool) -> Result<(), Box<dyn std::error::Error>> {
    use sha2::Digest;
    use soda_desktop_runtime::factory_provider::{
        JudgmentCalls, JudgmentRequest, MuseTool, ToolSelection,
    };
    use soda_execution_protocol::HarnessProfile;
    use std::sync::{atomic::AtomicBool, Arc};
    let fixture=if prepared {Some(serde_json::from_slice::<serde_json::Value>(&fs::read(root.join("judgment-fixture.json"))?)?)}else{None};
    let profile = if let Some(fixture)=&fixture {fixture["capture"]["profile"].as_str().ok_or("fixture profile absent")?.into()}else{"a".repeat(32)};
    let connection: String = sha2::Sha256::digest(format!("factory_muse_login\n{profile}"))
        .iter()
        .take(16)
        .map(|b| format!("{b:02x}"))
        .collect();
    let executable = std::env::current_exe()?;
    let sha256: String = sha2::Sha256::digest(fs::read(&executable)?)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    let tool = ToolSelection {
        tool: MuseTool {
            harness: "muse",
            version: "fixture".into(),
            sha256: sha256.clone(),
            executable,
        },
        channel: "declared-fixture",
        checked_at_unix: 1,
    };
    let mut calls = JudgmentCalls::default();
    let alive = Arc::new(AtomicBool::new(true));
    let mut results = Vec::new();
    let prompts=if let Some(fixture)=&fixture {vec![fixture["prompt"].as_str().ok_or("fixture prompt absent")?]}else{vec!["judgment","wait"]};
    for (index, prompt) in prompts.into_iter().enumerate() {
        let id = format!("{:032x}", index + 1);
        let digest = "b".repeat(64);
        let mut request = JudgmentRequest {
            id: id.clone(),
            digest: digest.clone(),
            connection: connection.clone(),
            tool_version: "fixture".into(),
            tool_sha256: sha256.clone(),
            prompt_sha256: sha2::Sha256::digest(prompt.as_bytes())
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect(),
            harness: HarnessProfile {
                model: "muse-spark-1.3-contributor".into(),
                effort: "low".into(),
                contributor: true,
            },
            deadline_unix_ms: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)?
                .as_millis() as u64
                + 10_000,
            prompt: prompt.into(),
        };
        if let Some(fixture)=&fixture {
            request.id=fixture["capture"]["id"].as_str().ok_or("fixture id absent")?.into();
            request.digest=fixture["digest"].as_str().ok_or("fixture digest absent")?.into();
            request.deadline_unix_ms=fixture["capture"]["deadline_unix_ms"].as_u64().ok_or("fixture deadline absent")?;
        }
        let id=request.id.clone();let digest=request.digest.clone();
        calls.begin(root, &profile, request.clone(), tool.clone(), alive.clone())?;
        let until = std::time::Instant::now() + Duration::from_secs(20);
        let result = loop {
            let result = calls.inspect(root, &id, &digest, index == 1)?;
            if result["state"] == "complete" {
                break result;
            }
            if std::time::Instant::now() >= until {
                return Err("native judgment did not retire".into());
            }
            std::thread::sleep(Duration::from_millis(25));
        };
        let process = &result["result"]["process"];
        if process["tree_retired"] != true
            || process["output_complete"] != true
            || process["run"] != id
            || process["prompt_sha256"] != request.prompt_sha256
        {
            return Err("native judgment lost actual process retirement or prompt binding".into());
        }
        if index == 0 {
            let output: serde_json::Value = serde_json::from_str(
                result["result"]["output"].as_str().ok_or("native judgment output absent")?,
            )?;
            let valid = if let Some(fixture) = &fixture {
                if fixture["capture"]["output_kind"] == "consultation" {
                    output["answers"].is_object()
                } else {
                    output["selected"].as_str().is_some_and(|selected| fixture["options"].get(selected).is_some())
                }
            } else {
                output["selected"] == "new_issue"
            };
            if !valid {
                return Err("native judgment lost its terminal answer".into());
            }
        }
        if index == 1 && process["reason"] != "cancelled" {
            return Err("native judgment cancellation was not observed".into());
        }
        let repeated = calls.begin(root, &profile, request, tool.clone(), alive.clone())?;
        if repeated != result {
            return Err("native judgment replay differed or started again".into());
        }
        results.push(result);
    }
    calls.retire()?;
    let mut restarted = JudgmentCalls::default();
    if restarted.inspect(root, results[0]["result"]["id"].as_str().ok_or("result id absent")?,results[0]["result"]["digest"].as_str().ok_or("result digest absent")?, false)? != results[0] {
        return Err("native judgment restart lost its original receipt".into());
    }
    fs::write(
        root.join("judgment-result.json"),
        serde_json::to_vec_pretty(
            &serde_json::json!({"result":"pass","finite":true,"cancelled":!prepared,"replay":true,"restart":true,"provider":"declared local CLI, no model or authentication","results":results}),
        )?,
    )?;
    println!("PASS: actual native judgment process, terminal text, {}tree retirement, same-call replay and retained restart; executable/provider facts declared, no model or credentials", if prepared {""}else{"cancellation/"});
    Ok(())
}

fn muse_connector(
    root: &Path,
    helper: &Path,
    live: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    use sha2::Digest;
    use soda_desktop_runtime::factory_provider::{MuseConnector, Tools};
    use soda_execution_protocol::{HarnessProfile, MuseInvocation};
    let (executable, sha256) = if live {
        let selection = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?
            .block_on(Tools::discover(root)?.inspect())?;
        (selection.tool.executable, selection.tool.sha256)
    } else {
        let executable = std::env::current_exe()?;
        let sha256 = sha2::Sha256::digest(fs::read(&executable)?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        // This dummy value belongs only to this check process. The launched
        // connector must exclude it rather than choosing another source.
        std::env::set_var("META_API_KEY", "fixture-not-a-provider-credential");
        (executable, sha256)
    };
    let modes: &[&str] = if live {
        &["login"]
    } else {
        &["finish", "cancel"]
    };
    for mode in modes {
        let mut random = [0u8; 16];
        getrandom::fill(&mut random)?;
        let run: String = random.iter().map(|b| format!("{b:02x}")).collect();
        let mut connector = MuseConnector::start(
            root,
            helper,
            MuseInvocation {
                run,
                service: String::new(),
                executable: executable.to_string_lossy().into_owned(),
                executable_sha256: sha256.clone(),
                prompt: if live {
                    "Reply with exactly OK. Do not use any tools."
                } else if *mode == "cancel" {
                    "wait"
                } else {
                    "finish"
                }
                .into(),
                harness: HarnessProfile {
                    model: "muse-spark-1.3-contributor".into(),
                    effort: "low".into(),
                    contributor: true,
                },
                duration_ms: if live { 30_000 } else { 10_000 },
                deadline_unix_ms: 0,
                max_model_steps: if live { 1 } else { 3 },
                output_bytes: 1024 * 1024,
                mcp: None,
            },
        )?;
        let directory = connector.directory().to_path_buf();
        let until = std::time::Instant::now() + Duration::from_secs(if live { 40 } else { 12 });
        if *mode == "cancel" {
            while !directory.join("work/child.pid").exists() {
                if std::time::Instant::now() >= until {
                    return Err("connector child fixture not observed".into());
                }
                connector.observe(Duration::from_millis(20))?;
                if connector.terminal().is_some() {
                    return Err("connector fixture exited before cancellation".into());
                }
            }
            if !connector
                .inspect(Duration::from_secs(1))?
                .is_some_and(|state| state.sequence > 0 && state.live == Some(true))
            {
                return Err("connector fresh process liveness was not observed".into());
            }
            connector.cancel()?;
        }
        while connector.terminal().is_none() {
            if std::time::Instant::now() >= until {
                return Err("connector result timed out".into());
            }
            connector.observe(Duration::from_millis(100))?;
        }
        let (result, output) = connector.finish()?;
        if !result.tree_retired
            || !result.output_complete
            || result.uid != unsafe { libc::geteuid() }
            || result.reason.as_deref()
                != Some(if *mode == "cancel" {
                    "cancelled"
                } else {
                    "exited"
                })
            || *mode != "cancel" && result.exit_code != Some(0)
        {
            return Err("connector native result differs".into());
        }
        fs::write(directory.join("check-output.log"), &output)?;
        let output = String::from_utf8(output)?;
        if live {
            let events: Vec<serde_json::Value> = output
                .lines()
                .filter_map(|line| serde_json::from_str(line).ok())
                .collect();
            if !events.iter().any(|event| {
                event["payload_type"] == "run.terminal.completed"
                    && event["payload"]["terminal"] == "completed"
                    && event["payload"]["text"] == "OK"
            }) || !events.iter().any(|event| {
                event["payload_type"] == "run.usage"
                    && event["payload"]["calls"] == 1
                    && event["payload"]["completed_calls"] == 1
                    && event["payload"]["failed_calls"] == 0
                    && event["payload"]["cancelled_calls"] == 0
            }) || !events.iter().any(|event| {
                event["payload_type"] == "run.model.configured"
                    && event["payload"]["provider_id"] == "meta"
                    && event["payload"]["model_id"] == "muse-spark-1.3-contributor"
            }) {
                return Err("saved-login connector has no completed model terminal".into());
            }
        } else if *mode == "finish" && !output.contains("\"fixture\":\"ok\"") {
            return Err("connector fixture contract failed".into());
        }
        let binding: serde_json::Value =
            serde_json::from_slice(&fs::read(directory.join("binding.json"))?)?;
        let unit = binding["unit"].as_str().ok_or("connector unit missing")?;
        let state = Command::new("/usr/bin/systemctl")
            .args(["--user", "show", unit, "--property=MainPID", "--value"])
            .output()?;
        if !String::from_utf8_lossy(&state.stdout).trim().is_empty()
            && String::from_utf8_lossy(&state.stdout).trim() != "0"
        {
            return Err("connector user service still has a process".into());
        }
        println!("PASS: {mode}: fixed CLI source, exact process/output receipt, tree retired and service MainPID zero ({})",directory.display());
    }
    Ok(())
}

fn managed_preparations(
    root: &Path,
    payload: &Path,
    backend: &Path,
    client: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    use soda_desktop_runtime::factory_provider::Broker;
    let mut roots = Vec::new();
    let modes = if std::env::var("SODA_NATIVE_CHECK_PUBLIC_SETUP").as_deref() == Ok("1") {
        ["ready", "network-off"]
    } else if std::env::var("SODA_NATIVE_CHECK_SETUP_BUDGET").as_deref() == Ok("1") {
        ["ready", "time-limit"]
    } else if std::env::var("SODA_NATIVE_CHECK_NODE_SETUP").as_deref() == Ok("1") {
        ["ready", "output-limit"]
    } else {
        ["ready", "pending"]
    };
    for mode in modes {
        let profile = root.join(format!("preparation-{mode}"));
        let supervisor = NativeSupervisor::start(payload, backend, &profile)?;
        let ready = supervisor.health()?;
        if !Command::new(client)
            .arg(&profile)
            .arg(&ready.instance)
            .arg("check-native-preparation")
            .arg(mode)
            .status()?
            .success()
        {
            return Err("Go native preparation check failed".into());
        }
        let entries: Vec<_> =
            fs::read_dir(profile.join("native-preparations"))?.collect::<Result<_, _>>()?;
        if entries.is_empty() || mode == "pending" && entries.len() != 1 {
            return Err("native preparation identity missing".into());
        }
        let run = entries[0]
            .file_name()
            .to_str()
            .ok_or("native preparation name invalid")?
            .to_owned();
        for entry in &entries {
            roots.push(
                entry
                    .file_name()
                    .to_str()
                    .ok_or("native preparation name invalid")?
                    .to_owned(),
            );
        }
        fs::write(root.join("execution-runs.ids"), roots.join("\n"))?;
        if mode == "pending" {
            let until = std::time::Instant::now() + Duration::from_secs(5);
            loop {
                let state = Command::new("systemctl")
                    .args([
                        "show",
                        &format!("soda-factory-preparer@{run}.service"),
                        "--property=MainPID",
                        "--value",
                    ])
                    .output()?;
                if state.status.success()
                    && String::from_utf8_lossy(&state.stdout)
                        .trim()
                        .parse::<u32>()
                        .is_ok_and(|pid| pid > 0)
                {
                    break;
                }
                if std::time::Instant::now() >= until {
                    return Err("pending native preparer process was not observed".into());
                }
                std::thread::sleep(Duration::from_millis(20));
            }
            println!("Observed live pending preparer; testing supervisor Quit");
        }
        supervisor.stop()?;
        if Broker::next_profile_run(&ready.profile)?.is_some() {
            return Err("native supervisor left preparation roots".into());
        }
        if entries
            .iter()
            .any(|entry| !entry.path().join("retired.json").is_file())
        {
            return Err("native preparation retirement acknowledgement missing".into());
        }
    }
    if modes.contains(&"network-off") {
        println!("PASS: approved public HTTPS setup installs a cold declared npm dependency; proxy refuses loopback/plain HTTP, absent approval keeps setup offline; exact preparation/root retirement confirmed");
    } else if modes.contains(&"pending") {
        println!("PASS: supervisor Quit cancels and joins an actually running setup before child shutdown, retaining inputs and scoped retirement evidence");
    } else if modes.contains(&"time-limit") {
        println!("PASS: approved setup time budget exceeds the old entry/service deadlines; shorter budget refuses readiness; exact preparation resources retired");
    } else {
        println!("PASS: isolated Node artifact build and bounded diagnostic refusal retain exact scoped retirement evidence");
    }
    Ok(())
}

fn parallel_preparations(root: &Path) -> Result<(), Box<dyn std::error::Error>> {
    use sha2::Digest;
    use soda_desktop_runtime::factory_provider::{AgentCancellation, Broker, Role};
    use soda_execution_protocol::ApprovedFiles;
    use std::collections::BTreeMap;
    use std::time::Instant;
    let bundle = fs::read(std::env::var("SODA_NATIVE_CHECK_BUNDLE")?)?;
    let candidate = std::env::var("SODA_NATIVE_CHECK_COMMIT")?;
    let digest: String = Sha256::digest(&bundle)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    let mut identities = Vec::new();
    for _ in 0..4 {
        let mut bytes = [0u8; 16];
        getrandom::fill(&mut bytes)?;
        identities.push(
            bytes
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect::<String>(),
        );
    }
    let profile = &identities[0];
    let runs = &identities[1..];
    fs::write(root.join("execution-runs.ids"), runs.join("\n"))?;
    let mut slow = Vec::new();
    let live = |run: &str| -> Result<bool, Box<dyn std::error::Error>> {
        let output = Command::new("systemctl")
            .args([
                "show",
                &format!("soda-factory-preparer@{run}.service"),
                "-p",
                "MainPID",
                "--value",
            ])
            .output()?;
        Ok(output.status.success()
            && String::from_utf8_lossy(&output.stdout)
                .trim()
                .parse::<u32>()
                .is_ok_and(|pid| pid > 0))
    };
    for run in &runs[..2] {
        let cancellation = AgentCancellation::default();
        let signal = cancellation.clone();
        let run = run.clone();
        let profile = profile.clone();
        let bundle = bundle.clone();
        let candidate = candidate.clone();
        let thread = std::thread::spawn(move || {
            let approved = ApprovedFiles {
                files: BTreeMap::from([
                    ("setup.sh".into(), b"sleep 20\n".to_vec()),
                    ("check.sh".into(), b"printf checked\n".to_vec()),
                    (
                        "setup-limits.json".into(),
                        br#"{"setup_seconds":25,"check_seconds":1}"#.to_vec(),
                    ),
                ]),
                tools: Vec::new(),
            };
            Broker::prepare_source(
                &run,
                &profile,
                Role::Coder,
                &bundle,
                &approved,
                &candidate,
                signal,
            )
        });
        slow.push((cancellation, thread));
    }
    let until = Instant::now() + Duration::from_secs(6);
    while !live(&runs[0])? || !live(&runs[1])? {
        if Instant::now() >= until {
            return Err("both pending preparers were not live concurrently".into());
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    println!("Observed two live preparers with separate exact-run identities");
    let (cancel, thread) = slow.pop().ok_or("second preparer missing")?;
    let started = Instant::now();
    cancel.cancel();
    let retired = Broker::retire_unlaunched(&runs[1], profile, &digest)?;
    if thread
        .join()
        .map_err(|_| "cancelled preparation panicked")?
        .is_ok()
        || !retired
        || started.elapsed() > Duration::from_secs(5)
        || !live(&runs[0])?
    {
        return Err("independent preparation cancellation did not retire promptly while another remained live".into());
    }
    let approved = ApprovedFiles {
        files: BTreeMap::from([
            ("setup.sh".into(), b"printf prepared\n".to_vec()),
            ("check.sh".into(), b"printf checked\n".to_vec()),
        ]),
        tools: Vec::new(),
    };
    let started = Instant::now();
    let receipt = Broker::prepare_source(
        &runs[2],
        profile,
        Role::Coder,
        &bundle,
        &approved,
        &candidate,
        AgentCancellation::default(),
    )?;
    if receipt.setup_exit != Some(0)
        || receipt.check_exit != Some(0)
        || started.elapsed() > Duration::from_secs(5)
        || !live(&runs[0])?
    {
        return Err(
            "independent preparation did not complete while the first remained live".into(),
        );
    }
    if !Broker::retire_unlaunched(&runs[2], profile, &digest)? {
        return Err("fast preparation retirement unconfirmed".into());
    }
    let (cancel, thread) = slow.pop().ok_or("first preparer missing")?;
    cancel.cancel();
    if thread
        .join()
        .map_err(|_| "first preparation panicked")?
        .is_ok()
        || !Broker::retire_unlaunched(&runs[0], profile, &digest)?
        || Broker::next_profile_run(profile)?.is_some()
    {
        return Err("parallel preparation final retirement unconfirmed".into());
    }
    println!("PASS: overlapping live preparations; cancellation and an independent setup complete promptly; all exact staged roots retired");
    Ok(())
}

fn workspace_parent(tool: &str) -> soda_execution_protocol::workspace::Parent {
    soda_execution_protocol::workspace::Parent {
        invocation: "native-workspace-fixture".into(),
        system_instance: "fixture-system-instance".into(),
        start_identity: "1".into(),
        pid: 1,
        executable_sha256: tool.into(),
    }
}
fn workspace_receipt_readback(root: &Path) -> Result<(), Box<dyn std::error::Error>> {
    use soda_desktop_runtime::factory_provider::{Broker, ReceiptScope, Role};
    use soda_execution_protocol::workspace::Command as WorkspaceCommand;
    let binding: serde_json::Value = serde_json::from_slice(&fs::read(root.join("binding.json"))?)?;
    let field = |key: &str| {
        binding[key]
            .as_str()
            .ok_or("workspace receipt binding missing")
    };
    let scope = ReceiptScope::new(
        field("run")?,
        field("profile")?,
        field("source_sha256")?,
        field("candidate")?,
        Role::Coder,
        field("tool")?,
        field("prompt")?,
    )?;
    let command: WorkspaceCommand =
        serde_json::from_slice(&fs::read(root.join("checkpoint.request.json"))?)?;
    let manifest = fs::read(root.join("workspace.manifest"))?;
    let result = Broker::retained_task_receipt(
        &root.join("native-profile"),
        &scope,
        &manifest,
        &workspace_parent(field("tool")?),
        &command,
    );
    if field("mode")? == "cancel" {
        let request = root
            .join("native-profile/native-task-receipts")
            .join(field("run")?)
            .join(format!("{}.request", command.receipt_key()?));
        if result.is_ok() || !request.is_file() {
            return Err(
                "cancelled task lost its pending request or gained a fabricated result".into(),
            );
        }
        println!("PASS: new native process preserves the cancelled checkpoint's pending request; no result or effect replay");
        return Ok(());
    }
    let first = result?;
    if serde_json::to_vec(&first)? != fs::read(root.join("checkpoint.result.json"))? {
        return Err("retained checkpoint result changed".into());
    }
    let command: WorkspaceCommand =
        serde_json::from_slice(&fs::read(root.join("final.request.json"))?)?;
    let final_result = Broker::retained_task_receipt(
        &root.join("native-profile"),
        &scope,
        &manifest,
        &workspace_parent(field("tool")?),
        &command,
    )?;
    if serde_json::to_vec(&final_result)? != fs::read(root.join("result.json"))?
        || final_result.bundle != fs::read(root.join("candidate.bundle"))?
    {
        return Err("retained final checkpoint or candidate changed".into());
    }
    println!("PASS: new native process reads exact task/final receipts, journal and candidate after root retirement; no worker launch");
    Ok(())
}

fn workspace_check(root: &Path, helper: &Path) -> Result<(), Box<dyn std::error::Error>> {
    use sha2::Digest;
    use soda_desktop_runtime::factory_provider::{AgentCancellation, Broker, ReceiptScope, Role};
    use soda_execution_protocol::workspace::{Command as WorkspaceCommand, Reply};
    let source_path = std::env::var("SODA_NATIVE_CHECK_BUNDLE")?;
    let bundle = fs::read(&source_path)?;
    let candidate = std::env::var("SODA_NATIVE_CHECK_COMMIT")?;
    let hash = |bytes: &[u8]| -> String {
        Sha256::digest(bytes)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    };
    let source_sha = hash(&bundle);
    let tool = hash(b"native workspace fixture, no model");
    let prompt = hash(b"sequential workspace integration");
    let mut runs = Vec::new();
    for mode in ["ready", "failed", "ownership", "dependency", "cancel"] {
        let scratch = root.join(mode);
        DirBuilder::new().mode(0o700).create(&scratch)?;
        let mut random = [0; 16];
        getrandom::fill(&mut random)?;
        let run: String = random.iter().map(|b| format!("{b:02x}")).collect();
        getrandom::fill(&mut random)?;
        let profile: String = random.iter().map(|b| format!("{b:02x}")).collect();
        runs.push(run.clone());
        fs::write(root.join("execution-runs.ids"), runs.join("\n"))?;
        fs::write(
            scratch.join("binding.json"),
            serde_json::to_vec(
                &serde_json::json!({"run":run,"profile":profile,"source_sha256":source_sha,
                    "candidate":candidate,"tool":tool,"prompt":prompt,"mode":mode}),
            )?,
        )?;
        if !Command::new(helper)
            .args([
                "workspace-manifest",
                scratch.to_str().ok_or("scratch encoding")?,
                &run,
                &candidate,
                mode,
            ])
            .status()?
            .success()
        {
            return Err("Go workspace manifest failed".into());
        }
        let manifest = fs::read(scratch.join("workspace.manifest"))?;
        let native_profile = scratch.join("native-profile");
        DirBuilder::new().mode(0o700).create(&native_profile)?;
        let mut profile_file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(native_profile.join("profile.id"))?;
        profile_file.write_all(profile.as_bytes())?;
        profile_file.sync_all()?;
        let result = (|| -> Result<(), Box<dyn std::error::Error>> {
            Broker::stage_bundle(&run, &profile, Role::Coder, &tool, &prompt, &bundle)?;
            Broker::prepare_reviewer(&run, &profile, &source_sha, &candidate)?;
            let scope = ReceiptScope::new(
                &run,
                &profile,
                &source_sha,
                &candidate,
                Role::Coder,
                &tool,
                &prompt,
            )?;
            let cancel = AgentCancellation::default();
            let mut workspace =
                Broker::prepare_task_workspace(&native_profile, &scope, &manifest, cancel.clone())?;
            if workspace
                .command(&WorkspaceCommand::Start {
                    task: "first".into(),
                })
                .is_ok()
            {
                return Err("task admitted before parent observation".into());
            }
            workspace.bind_parent(&workspace_parent(&tool))?;
            if mode == "dependency" {
                if workspace
                    .command(&WorkspaceCommand::Start {
                        task: "second".into(),
                    })
                    .is_ok()
                {
                    return Err("unmet task dependency accepted".into());
                }
                return Ok(());
            }
            workspace.command(&WorkspaceCommand::Start {
                task: "first".into(),
            })?;
            if mode == "ownership" {
                if workspace
                    .command(&WorkspaceCommand::Edit {
                        task: "first".into(),
                        operation_id: "unowned".into(),
                        path: "README.md".into(),
                        offset: 0,
                        expected: String::new(),
                        replacement: "unowned".into(),
                    })
                    .is_ok()
                {
                    return Err("unowned edit accepted".into());
                }
                return Ok(());
            }
            workspace.command(&WorkspaceCommand::Edit {
                task: "first".into(),
                operation_id: "first-edit".into(),
                path: "native-task.txt".into(),
                offset: 0,
                expected: String::new(),
                replacement: "first\n".into(),
            })?;
            workspace.command(&WorkspaceCommand::MakeDirectory {
                operation_id: "mkdir".into(),
                task: "first".into(),
                path: "native-task-dir".into(),
            })?;
            workspace.command(&WorkspaceCommand::MakeDirectory {
                operation_id: "existing-directory".into(),
                task: "first".into(),
                path: "native-task-dir".into(),
            })?;
            for (name, content) in [("kept.txt", "kept\n"), ("to-remove", "discard\n")] {
                workspace.command(&WorkspaceCommand::Edit {
                    operation_id: name.into(),
                    task: "first".into(),
                    path: format!("native-task-dir/{name}"),
                    offset: 0,
                    expected: String::new(),
                    replacement: content.into(),
                })?;
            }
            let file = workspace.command(&WorkspaceCommand::Read {
                task: "first".into(),
                path: "native-task-dir/to-remove".into(),
                offset: 0,
                limit: 1024,
            })?;
            workspace.command(&WorkspaceCommand::Remove {
                operation_id: "remove".into(),
                task: "first".into(),
                path: "native-task-dir/to-remove".into(),
                expected_digest: file.file_digest,
            })?;
            let read = workspace.command(&WorkspaceCommand::Read {
                task: "first".into(),
                path: "native-task.txt".into(),
                offset: 0,
                limit: 1024,
            })?;
            if read.content.as_deref() != Some("first\n") {
                return Err("workspace edit readback differs".into());
            }
            let released = workspace.command(&WorkspaceCommand::Release {
                task: "first".into(),
            })?;
            let cancellation = if mode == "cancel" {
                Some(std::thread::spawn(move || {
                    std::thread::sleep(Duration::from_secs(1));
                    cancel.cancel();
                }))
            } else {
                None
            };
            let began = std::time::Instant::now();
            let checkpoint_command = WorkspaceCommand::Checkpoint {
                task: "first".into(),
                expected_head: candidate.clone(),
                output_digest: released.output_digest,
            };
            fs::write(
                scratch.join("checkpoint.request.json"),
                serde_json::to_vec(&checkpoint_command)?,
            )?;
            let checkpoint = workspace.command(&checkpoint_command);
            if let Some(thread) = cancellation {
                thread.join().map_err(|_| "cancellation thread failed")?;
                if checkpoint.is_ok() || began.elapsed() > Duration::from_secs(6) {
                    return Err("workspace cancellation did not interrupt checkpoint".into());
                }
                return Ok(());
            }
            let first = checkpoint?;
            fs::write(
                scratch.join("checkpoint.result.json"),
                serde_json::to_vec(&first)?,
            )?;
            if mode == "failed" {
                if first.status != "failed"
                    || first.head != candidate
                    || first.checks.len() != 1
                    || first.checks[0].passed
                {
                    return Err("failed check advanced checkpoint".into());
                }
                workspace.command(&WorkspaceCommand::Close)?;
                return Ok(());
            }
            if first.status != "committed"
                || first.head == candidate
                || first.checks.len() != 1
                || !first.checks[0].passed
            {
                return Err("first task checkpoint incomplete".into());
            }
            let replay = workspace.command(&checkpoint_command)?;
            if serde_json::to_vec(&replay)? != serde_json::to_vec(&first)? {
                return Err("checkpoint retry changed its result".into());
            }
            // Withhold only this disposable profile's completed local copy to
            // exercise broker readback. This does not qualify a killed publisher
            // or an actual lost transport reply.
            let receipt_path = native_profile
                .join("native-task-receipts")
                .join(&run)
                .join(format!("{}.receipt", checkpoint_command.receipt_key()?));
            fs::rename(&receipt_path, scratch.join("withheld-checkpoint.receipt"))?;
            let recovered = Broker::retained_task_receipt(
                &native_profile,
                &scope,
                &manifest,
                &workspace_parent(&tool),
                &checkpoint_command,
            )?;
            if serde_json::to_vec(&recovered)?
                != fs::read(scratch.join("withheld-checkpoint.receipt"))?
            {
                return Err("broker readback changed the committed checkpoint".into());
            }
            if Broker::retained_task_receipt(
                &native_profile,
                &scope,
                &manifest,
                &soda_execution_protocol::workspace::Parent {
                    pid: 2,
                    ..workspace_parent(&tool)
                },
                &checkpoint_command,
            )
            .is_ok()
            {
                return Err("foreign checkpoint invocation accepted".into());
            }
            workspace.command(&WorkspaceCommand::Start {
                task: "second".into(),
            })?;
            workspace.command(&WorkspaceCommand::Edit {
                task: "second".into(),
                operation_id: "second-edit".into(),
                path: "native-task.txt".into(),
                offset: 0,
                expected: "first".into(),
                replacement: "second".into(),
            })?;
            let released = workspace.command(&WorkspaceCommand::Release {
                task: "second".into(),
            })?;
            let second = workspace.command(&WorkspaceCommand::Checkpoint {
                task: "second".into(),
                expected_head: first.head.clone(),
                output_digest: released.output_digest,
            })?;
            if second.status != "committed" || second.head == first.head {
                return Err("second task checkpoint incomplete".into());
            }
            let final_command = WorkspaceCommand::Final {
                expected_head: second.head.clone(),
            };
            fs::write(
                scratch.join("final.request.json"),
                serde_json::to_vec(&final_command)?,
            )?;
            let final_result: Reply = workspace.command(&final_command)?;
            if final_result.status != "committed"
                || final_result.head != second.head
                || final_result.bundle.is_empty()
                || final_result.checks.len() != 1
                || !final_result.checks[0].passed
            {
                return Err("final task candidate incomplete".into());
            }
            fs::write(
                scratch.join("workspace.events.json"),
                serde_json::to_vec(&final_result.events)?,
            )?;
            fs::write(scratch.join("candidate.bundle"), &final_result.bundle)?;
            fs::write(
                scratch.join("result.json"),
                serde_json::to_vec(&final_result)?,
            )?;
            if !Command::new(helper)
                .arg("workspace-events")
                .arg(&scratch)
                .status()?
                .success()
            {
                return Err("Go workspace journal import failed".into());
            }
            let export = scratch.join("export");
            let git = |args: &[&std::ffi::OsStr]| -> Result<(), Box<dyn std::error::Error>> {
                if !Command::new("/usr/bin/git")
                    .env_clear()
                    .env("PATH", "/usr/bin:/bin")
                    .env("TMPDIR", &scratch)
                    .env("GIT_CONFIG_NOSYSTEM", "1")
                    .env("GIT_CONFIG_GLOBAL", "/dev/null")
                    .args(args)
                    .status()?
                    .success()
                {
                    return Err("workspace candidate Git verification failed".into());
                }
                Ok(())
            };
            use std::ffi::OsStr;
            git(&[
                OsStr::new("clone"),
                OsStr::new("--quiet"),
                OsStr::new(&source_path),
                export.as_os_str(),
            ])?;
            git(&[
                OsStr::new("-C"),
                export.as_os_str(),
                OsStr::new("fetch"),
                OsStr::new("--quiet"),
                scratch.join("candidate.bundle").as_os_str(),
                OsStr::new("HEAD"),
            ])?;
            git(&[
                OsStr::new("-C"),
                export.as_os_str(),
                OsStr::new("checkout"),
                OsStr::new("--quiet"),
                OsStr::new(&second.head),
            ])?;
            if fs::read(export.join("native-task.txt"))? != b"second\n" {
                return Err("exported task edits differ".into());
            }
            workspace.command(&WorkspaceCommand::Close)?;
            Ok(())
        })();
        let retired = Broker::retire_bundle(&run, &profile, &source_sha);
        result?;
        if !retired? || Broker::next_profile_run(&profile)?.is_some() {
            return Err("workspace root retirement incomplete".into());
        }
        if matches!(mode, "ready" | "cancel")
            && !Command::new(std::env::current_exe()?)
                .arg(&scratch)
                .arg("workspace-readback")
                .status()?
                .success()
        {
            return Err("new-process task receipt readback failed".into());
        }
        println!("PASS: isolated sequential workspace {mode}; exact root retirement, no model or provider request");
    }
    Ok(())
}

fn preparation_check(root: &Path) -> Result<(), Box<dyn std::error::Error>> {
    use sha2::Digest;
    use soda_desktop_runtime::factory_provider::{AgentCancellation, Broker, Role};
    use std::collections::BTreeMap;
    let bundle = fs::read(std::env::var("SODA_NATIVE_CHECK_BUNDLE")?)?;
    let candidate = std::env::var("SODA_NATIVE_CHECK_COMMIT")?;
    let digest: String = Sha256::digest(&bundle)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    let mut random = [0u8; 16];
    getrandom::fill(&mut random)?;
    let profile: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    let mut runs = Vec::new();
    for mode in ["ready", "failed", "missing"] {
        getrandom::fill(&mut random)?;
        let run: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
        runs.push(run.clone());
        fs::write(root.join("execution-runs.ids"), runs.join("\n"))?;
        let approved = soda_execution_protocol::ApprovedFiles {
            files: BTreeMap::from([
                (
                    "setup.sh".into(),
                    b"printf prepared > prepared-marker\nprintf setup-complete\\n\n".to_vec(),
                ),
                (
                    "check.sh".into(),
                    if mode == "failed" {
                        b"test -f prepared-marker\nprintf check-failed\\n\nexit 17\n".to_vec()
                    } else {
                        b"test -f prepared-marker\nprintf check-complete\\n\n".to_vec()
                    },
                ),
            ]),
            tools: if mode == "missing" {
                vec!["factory-fixture-missing".into()]
            } else {
                vec!["git".into(), "bash".into()]
            },
        };
        let result = Broker::prepare_source(
            &run,
            &profile,
            Role::Coder,
            &bundle,
            &approved,
            &candidate,
            AgentCancellation::default(),
        );
        let retire = Broker::retire_unlaunched(&run, &profile, &digest);
        let receipt = result?;
        if !retire? {
            return Err("native preparation root retirement unconfirmed".into());
        }
        match mode {
            "ready"
                if receipt.setup_exit == Some(0)
                    && receipt.check_exit == Some(0)
                    && receipt.missing.is_empty()
                    && receipt.tools.len() == 2
                    && receipt.output.contains("check-complete") =>
            {
                ()
            }
            "failed" if receipt.setup_exit == Some(0) && receipt.check_exit == Some(17) => (),
            "missing"
                if receipt.setup_exit.is_none()
                    && receipt.check_exit.is_none()
                    && receipt.missing == ["factory-fixture-missing"] =>
            {
                ()
            }
            _ => return Err(format!("native setup result differs for {mode}").into()),
        }
        println!("PASS: isolated native preparation {mode}: actual setup/check exits, source and approved-file digests, exact root retirement");
    }
    if Broker::next_profile_run(&profile)?.is_some() {
        return Err("native preparation left a staged root".into());
    }
    Ok(())
}

async fn managed_muse_execution(
    root: &Path,
    payload: &Path,
    backend: &Path,
    client: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    use soda_desktop_runtime::factory_provider::{Broker, ExecutionOutcome, LaunchIntent};
    let profile = root.join("execution-profile");
    let supervisor = NativeSupervisor::start(payload, backend, &profile)?;
    let ready = supervisor.health()?;
    let command = |operation: &str, argument: &str| -> Result<(), Box<dyn std::error::Error>> {
        if !Command::new(client)
            .env("SODA_NATIVE_KEEP_PREPARATION", "1")
            .arg(&profile)
            .arg(&ready.instance)
            .arg(operation)
            .arg(argument)
            .status()?
            .success()
        {
            return Err(format!("CLI source fixture {operation} failed").into());
        }
        Ok(())
    };
    let live = std::env::var("SODA_NATIVE_CHECK_MUSE_LIVE").as_deref() == Ok("1");
    let withdraw = std::env::var("SODA_NATIVE_CHECK_MUSE_WITHDRAW").as_deref() == Ok("1");
    let tasks = std::env::var("SODA_NATIVE_CHECK_TASKS").as_deref() == Ok("1");
    if tasks && withdraw {
        return Err("task integration and selected-source withdrawal are separate checks".into());
    }
    if withdraw && live {
        return Err("withdrawal check uses only the local CLI fixture".into());
    }
    if live {
        command("check-owned-tool", "discovery")?;
    }
    command("check-native-preparation", "ready")?;
    let preparations: Vec<_> =
        fs::read_dir(profile.join("native-preparations"))?.collect::<Result<_, _>>()?;
    if preparations.len() != 1 {
        return Err("expected one actual native preparation".into());
    }
    let preparation: serde_json::Value =
        serde_json::from_slice(&fs::read(preparations[0].path().join("request.json"))?)?;
    let mut random = [0u8; 16];
    getrandom::fill(&mut random)?;
    let run: String = random.iter().map(|b| format!("{b:02x}")).collect();
    fs::write(root.join("execution-runs.ids"), &run)?;
    let fixture = profile.join("admitted-native-fixture.json");
    fs::write(
        &fixture,
        serde_json::to_vec(
            &serde_json::json!({"Run":run,"MuseLogin":true,"Live":live,"Withdraw":withdraw,"Tasks":tasks,
        "Preparation":preparation["input"]["preparation"]["id"],"Executable":client.canonicalize()?}),
        )?,
    )?;
    command(
        "launch-admitted-native",
        fixture.to_str().ok_or("invalid fixture path")?,
    )?;
    if withdraw {
        command(
            "withdraw-admitted-native",
            fixture.to_str().ok_or("invalid fixture path")?,
        )?;
        supervisor.stop()?;
        if Broker::next_profile_run(&ready.profile)?.is_some() {
            return Err("withdrawn CLI source left an owned broker root".into());
        }
        return Ok(());
    }
    let intent = LaunchIntent::from_go(&fs::read_to_string(
        profile
            .join("native-run-intents")
            .join(format!("{run}.json")),
    )?)?;
    let captured: serde_json::Value = serde_json::from_slice(&fs::read(&fixture)?)?;
    let until = std::time::UNIX_EPOCH
        + Duration::from_secs(
            captured["DeadlineUnix"]
                .as_u64()
                .ok_or("CLI deadline missing")?
                + 5,
        );
    loop {
        match supervisor.execution_outcome(&intent)? {
            ExecutionOutcome::Pending if std::time::SystemTime::now() < until => {
                tokio::time::sleep(Duration::from_millis(20)).await
            }
            ExecutionOutcome::MuseComplete(result)
                if result.root_retired
                    && result.process.output_complete
                    && result.process.exit_code == Some(0) =>
            {
                break
            }
            ExecutionOutcome::MuseComplete(_) => {
                command(
                    "observe-admitted-native",
                    fixture.to_str().ok_or("invalid fixture path")?,
                )?;
                supervisor.stop()?;
                return Err("CLI source execution failed; exact terminal facts retained".into());
            }
            _ => return Err("CLI source execution has no complete retired result".into()),
        }
    }
    command(
        "check-admitted-native",
        fixture.to_str().ok_or("invalid fixture path")?,
    )?;
    command(
        "acknowledge-native",
        fixture.to_str().ok_or("invalid fixture path")?,
    )?;
    if !matches!(
        supervisor.execution_outcome(&intent)?,
        ExecutionOutcome::MuseComplete(_)
    ) {
        return Err("native entry was discarded before canonical accounting".into());
    }
    supervisor.start_execution_coordinator().await?;
    command(
        "settled-admitted-native",
        fixture.to_str().ok_or("invalid fixture path")?,
    )?;
    if supervisor.execution_outcome(&intent).is_ok() {
        return Err("accounted native entry still occupies delivery capacity".into());
    }
    if tasks {
        command(
            "launch-admitted-tasks",
            fixture.to_str().ok_or("invalid fixture path")?,
        )?;
        let child: serde_json::Value = serde_json::from_slice(&fs::read(&fixture)?)?;
        let child_run = child["Run"].as_str().ok_or("implementation run missing")?;
        fs::write(
            root.join("execution-runs.ids"),
            format!("{run}\n{child_run}"),
        )?;
        command(
            "check-admitted-native",
            fixture.to_str().ok_or("invalid fixture path")?,
        )?;
        command(
            "settled-admitted-native",
            fixture.to_str().ok_or("invalid fixture path")?,
        )?;
    }
    command(
        "check-admitted-native",
        fixture.to_str().ok_or("invalid fixture path")?,
    )?;
    supervisor.stop()?;
    let restarted = NativeSupervisor::start(payload, backend, &profile)?;
    let current = restarted.health()?;
    if current.instance == ready.instance {
        return Err("native restart reused its instance".into());
    }
    for operation in ["check-admitted-native", "settled-admitted-native"] {
        if !Command::new(client)
            .arg(&profile)
            .arg(&current.instance)
            .arg(operation)
            .arg(&fixture)
            .status()?
            .success()
        {
            return Err("retained CLI result/accounting read after restart failed".into());
        }
    }
    restarted.stop()?;
    println!("PASS: acknowledgement waits for accounting, frees the native entry, and preserves final output/accounting across restart without inference replay");
    if Broker::next_profile_run(&ready.profile)?.is_some() {
        return Err("CLI source left an owned broker root".into());
    }
    println!("PASS: CLI-managed source: actual PostgreSQL admission, Go launcher, native registry, scoped MCP artifact/result, coordinator settlement and process/root retirement; live model={live}, sequential tasks={tasks}; installed qualification remains open");
    Ok(())
}

// Uses the established isolated local ELF/keyring fixture. The Go row is a
// declared intent, not a dispatch admission or a connected provider proof.
async fn managed_execution(
    root: &Path,
    payload: &Path,
    backend: &Path,
    client: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    use base64::Engine;
    use sha2::Digest;
    use soda_desktop_runtime::credentials::{Account, Credentials, Provider, Token};
    use soda_desktop_runtime::factory_provider::{
        read_retained, AttemptPhase, Broker, ExecutionOutcome, LaunchIntent, ReceiptScope, Role,
    };
    use std::sync::Arc;
    let digest = |bytes: &[u8]| -> String {
        Sha256::digest(bytes)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    };
    let source = fs::read(std::env::var("SODA_NATIVE_CHECK_BUNDLE")?)?;
    let commit = std::env::var("SODA_NATIVE_CHECK_COMMIT")?;
    let prompt_storage = vec![b'p'; 64 * 1024];
    let prompt = prompt_storage.as_slice();
    let profile = root.join("execution-profile");
    let supervisor = NativeSupervisor::start(payload, backend, &profile)?;
    let ready = supervisor.health()?;
    let credentials = Arc::new(Credentials::open(&profile, &ready.profile)?);
    credentials.storage_ready().await?;
    let account = credentials
        .store(
            Account::new(
                Provider::Muse,
                "https://api.meta.ai",
                "owned-local-execution-fixture",
            )?,
            Token::new(b"dummy-local-fixture-only".to_vec())?,
        )
        .await?;
    supervisor.enroll_execution_account(
        credentials.clone(),
        credentials.capability(&account.connection).await?,
    )?;
    let command = |operation: &str, argument: &str| -> Result<(), Box<dyn std::error::Error>> {
        if !Command::new(client)
            .env("SODA_NATIVE_KEEP_PREPARATION", "1")
            .arg(&profile)
            .arg(&ready.instance)
            .arg(operation)
            .arg(argument)
            .status()?
            .success()
        {
            return Err(format!("owned execution fixture {operation}/{argument} failed").into());
        }
        Ok(())
    };
    let candidate_check = std::env::var("SODA_NATIVE_CHECK_CANDIDATE").as_deref() == Ok("1");
    let source_preparation = {
        command("check-native-preparation", "ready")?;
        let role =
            std::env::var("SODA_NATIVE_PREPARATION_ROLE").unwrap_or_else(|_| "soda-coder".into());
        let mut selected = Vec::new();
        for entry in fs::read_dir(profile.join("native-preparations"))? {
            let request: serde_json::Value =
                serde_json::from_slice(&fs::read(entry?.path().join("request.json"))?)?;
            if request["input"]["preparation"]["role"].as_str() == Some(role.as_str()) {
                selected.push(request);
            }
        }
        if selected.len() != 1 {
            return Err("candidate source preparation missing".into());
        }
        selected.remove(0)
    };
    if std::env::var("SODA_NATIVE_CHECK_ADMITTED").as_deref() == Ok("1") {
        let mut random = [0u8; 16];
        getrandom::fill(&mut random)?;
        let run: String = random.iter().map(|b| format!("{b:02x}")).collect();
        fs::write(root.join("execution-runs.ids"), &run)?;
        let fixture = profile.join("admitted-native-fixture.json");
        fs::write(
            &fixture,
            serde_json::to_vec(&serde_json::json!({
                "Run":run,"Connection":account.connection,
                "Preparation":source_preparation["input"]["preparation"]["id"],
                "Executable":std::env::var("SODA_NATIVE_CHECK_FINITE")?
            }))?,
        )?;
        command(
            "launch-admitted-native",
            fixture.to_str().ok_or("fixture path invalid")?,
        )?;
        let intent = LaunchIntent::from_go(&fs::read_to_string(
            profile
                .join("native-run-intents")
                .join(format!("{run}.json")),
        )?)?;
        let admitted: serde_json::Value = serde_json::from_slice(&fs::read(&fixture)?)?;
        let expires = std::time::UNIX_EPOCH
            + Duration::from_secs(
                admitted["DeadlineUnix"]
                    .as_u64()
                    .ok_or("captured run deadline missing")?,
            );
        let deadline = std::time::Instant::now()
            + expires
                .duration_since(std::time::SystemTime::now())
                .unwrap_or_default()
            + Duration::from_secs(5);
        loop {
            match supervisor.execution_outcome(&intent)? {
                ExecutionOutcome::Pending if std::time::Instant::now() < deadline => {
                    tokio::time::sleep(Duration::from_millis(20)).await
                }
                ExecutionOutcome::Complete(result)
                    if result.exit_code == Some(0) && result.output_complete =>
                {
                    break
                }
                _ => {
                    return Err(
                        "admitted native execution did not finish with a complete actual receipt"
                            .into(),
                    )
                }
            }
        }
        command(
            "check-admitted-native",
            fixture.to_str().ok_or("fixture path invalid")?,
        )?;
        supervisor.stop()?;
        let restarted = NativeSupervisor::start(payload, backend, &profile)?;
        let current = restarted.health()?;
        if current.instance == ready.instance {
            return Err("native restart reused the original instance".into());
        }
        for operation in ["check-admitted-native", "settled-admitted-native"] {
            if operation == "settled-admitted-native" {
                restarted.start_execution_coordinator().await?;
            }
            if !Command::new(client)
                .arg(&profile)
                .arg(&current.instance)
                .arg(operation)
                .arg(&fixture)
                .status()?
                .success()
            {
                return Err("retained admitted execution after restart failed".into());
            }
        }
        restarted.stop()?;
        credentials.withdraw(&account.connection).await?;
        Broker::retire_bundle(&run, &ready.profile, &digest(&source))?;
        println!("PASS: a new native instance reads exact retired run output and settles its original admitted row without relaunch or live adoption");
        return Ok(());
    }
    let mut quit_ticket = None;
    let mut runs = Vec::new();
    let modes: &[&str] = if candidate_check {
        &["finite"]
    } else {
        &["finite", "cancel", "quit"]
    };
    for &mode in modes {
        let executable = PathBuf::from(std::env::var(if mode == "finite" {
            "SODA_NATIVE_CHECK_FINITE"
        } else {
            "SODA_NATIVE_CHECK_IDLE"
        })?);
        let tool = digest(&fs::read(&executable)?);
        let mut random = [0u8; 16];
        getrandom::fill(&mut random)?;
        let run: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
        runs.push(run.clone());
        fs::write(root.join("execution-runs.ids"), runs.join("\n"))?;
        let fixture = profile.join("native-recovery-fixture.json");
        let project = {
            source_preparation["input"]["preparation"]["project"]
                .as_str()
                .ok_or("preparation workspace missing")?
        };
        let worktree = {
            source_preparation["native"]["worktree_id"]
                .as_str()
                .ok_or("preparation worktree missing")?
        };
        let role = source_preparation["input"]["preparation"]["role"]
            .as_str()
            .ok_or("preparation role missing")?;
        let input = serde_json::json!({"Connection":account.connection,"Prompt":std::str::from_utf8(prompt)?,"Run":{
            "id":run,"project_id":project,"role":role,"input_sha":commit,
            "model":"muse-spark-1.3-contributor","harness":"muse-fixture","native":{
                "tool":{"harness":"muse","version":"fixture","sha256":tool,"executable":executable},
                "bundle_sha256":digest(&source),"prompt_sha256":digest(prompt),"retirement":"not_started","sealed":false,
                "provenance":{"trust_mode":"isolated_system_worker","profile":ready.profile,"instance_id":ready.instance,
                    "worktree_id":worktree,"broker_run":run,"process":null,"custody":{"state":"not_acquired","acquisition_id":"","connection":"","generation":"0"}}
            }
        }});
        fs::write(&fixture, serde_json::to_vec(&input)?)?;
        command(
            "create-native-intent",
            fixture.to_str().ok_or("fixture path invalid")?,
        )?;
        let intent = LaunchIntent::from_go(&fs::read_to_string(
            profile.join("native-recovery-intent.json"),
        )?)?;
        if candidate_check {
            command("check-native-sponsorship", &account.connection)?;
        }
        command("check-owned-execution", "launch_refused")?;
        task_manifest_bindings(&profile)?;
        if supervisor.execution_ticket(&intent).is_ok() {
            return Err("unadmitted Go launch reached native effects".into());
        }
        // Native producer admission is declared here. The public Go launch
        // guard above must refuse these unplaced fixture rows.
        let scope_digest = "a".repeat(64);
        let secret = Signer::open(&profile)?.derive(intent.run(), &scope_digest)?;
        let body = serde_json::to_vec(
            &serde_json::json!({"operation":"launch","intent":intent.raw(),
            "prompt":base64::engine::general_purpose::STANDARD.encode(prompt),"capability":intent.run(),
            "scope_digest":scope_digest,"secret":secret,"tools":["factory.context"]}),
        )?;
        let bearer = fs::read_to_string(profile.join("backend.key"))?;
        if candidate_check {
            let mut wrong: serde_json::Value = serde_json::from_str(intent.raw())?;
            let actual = wrong["native"]["credential_sha256"]
                .as_str()
                .ok_or("credential binding missing")?;
            wrong["native"]["credential_sha256"] = serde_json::json!(if actual == "0".repeat(64) {
                "1".repeat(64)
            } else {
                "0".repeat(64)
            });
            let mut rejected: serde_json::Value = serde_json::from_slice(&body)?;
            rejected["intent"] = serde_json::json!(serde_json::to_string(&wrong)?);
            let response = request_authenticated_async(
                &profile.join("host.sock"),
                "POST",
                "/api/native/execution",
                "soda-native",
                &serde_json::to_vec(&rejected)?,
                &bearer,
                &[("X-Soda-Native-Instance", ready.instance.as_str())],
                Duration::from_secs(5),
                Limits {
                    header_bytes: 4096,
                    body_bytes: 512 * 1024,
                },
            )
            .await
            .map_err(|_| "credential-bound launch refusal unavailable")?;
            if response.status != 503 || supervisor.execution_ticket(&intent).is_ok() {
                return Err("native launch used a different sponsored credential".into());
            }
        }
        let response = request_authenticated_async(
            &profile.join("host.sock"),
            "POST",
            "/api/native/execution",
            "soda-native",
            &body,
            &bearer,
            &[("X-Soda-Native-Instance", ready.instance.as_str())],
            Duration::from_secs(5),
            Limits {
                header_bytes: 4096,
                body_bytes: 512 * 1024,
            },
        )
        .await
        .map_err(|error| format!("native launch fixture transport: {error:?}"))?;
        if response.status != 200
            || serde_json::from_slice::<serde_json::Value>(&response.body)?["accepted"] != true
        {
            return Err("declared native launch callback did not accept the owned job".into());
        }
        let ticket = supervisor.execution_ticket(&intent)?;
        let start_seconds =
            if std::env::var("SODA_NATIVE_CHECK_WORKER_BUDGET").as_deref() == Ok("1") {
                45
            } else {
                12
            };
        let deadline = std::time::Instant::now() + Duration::from_secs(start_seconds);
        loop {
            match ticket.observe().phase {
                AttemptPhase::Executing => break,
                AttemptPhase::Starting if std::time::Instant::now() < deadline => {
                    tokio::time::sleep(Duration::from_millis(20)).await
                }
                _ => return Err("owned execution did not start".into()),
            }
        }
        command("check-owned-execution", "running")?;
        let before = ticket.inspect(Duration::from_secs(2))?.started.process;
        let replay = request_authenticated_async(
            &profile.join("host.sock"),
            "POST",
            "/api/native/execution",
            "soda-native",
            &body,
            &bearer,
            &[("X-Soda-Native-Instance", ready.instance.as_str())],
            Duration::from_secs(5),
            Limits {
                header_bytes: 4096,
                body_bytes: 512 * 1024,
            },
        )
        .await
        .map_err(|error| format!("native launch replay: {error:?}"))?;
        if replay.status != 200
            || supervisor
                .execution_ticket(&intent)?
                .inspect(Duration::from_secs(2))?
                .started
                .process
                != before
        {
            return Err("native launch replay replaced the worker process".into());
        }
        println!("PASS: native launch callback admits a retained 64 KiB prompt and replay preserves the actual worker process");
        if mode == "quit" {
            quit_ticket = Some((ticket, run.clone(), tool.clone()));
            break;
        }
        if mode == "cancel" {
            command("check-owned-execution", "cancel")?;
        }
        loop {
            match supervisor.execution_outcome(&intent)? {
                ExecutionOutcome::Pending if std::time::Instant::now() < deadline => {
                    tokio::time::sleep(Duration::from_millis(20)).await
                }
                ExecutionOutcome::Complete(result) if mode == "finite" => {
                    if candidate_check {
                        if result.candidate_state != "captured"
                            || result.result_candidate.is_none()
                            || result.candidate_bundle.is_empty()
                        {
                            return Err("native coder did not capture a real Git candidate".into());
                        }
                        fs::write(
                            profile.join("native-candidate-fixture.json"),
                            serde_json::to_vec(&serde_json::json!({
                                "candidate":result.result_candidate,"bundle":result.candidate_bundle
                            }))?,
                        )?;
                        fs::copy(
                            profile.join("native-recovery-canonical.id"),
                            profile.join("native-candidate-canonical.id"),
                        )?;
                    }
                    break;
                }
                ExecutionOutcome::Complete(result) if mode == "cancel" => {
                    if result.reason != "cancelled" || !result.output_complete {
                        return Err("cancelled worker lacks its exact terminal receipt".into());
                    }
                    break;
                }
                _ => return Err("owned execution result differs".into()),
            }
        }
        command(
            "check-owned-execution",
            if mode == "finite" {
                "complete"
            } else {
                "stopped"
            },
        )?;
        if mode == "finite" && candidate_check {
            command("check-owned-execution", "export")?;
            let destination = root.join("recovered-work");
            DirBuilder::new().mode(0o700).create(&destination)?;
            let target = supervisor.register_recovery_checkout(&destination)?;
            fs::write(profile.join("native-recovery-target.id"), target)?;
            supervisor.start_execution_coordinator().await?;
            supervisor.start_execution_coordinator().await?;
            command("check-owned-execution", "coordinator")?;
            command("check-owned-execution", "recover_started")?;
            if fs::read(destination.join("checkout/native-candidate.txt"))?
                != b"Actual isolated coder candidate\n"
            {
                return Err("recovered checkout lost the actual candidate".into());
            }
            if destination.join("checkout/.factory-build").exists()
                || destination.join("checkout/factory-env").exists()
            {
                return Err("recovery copied the worker environment".into());
            }
            fs::write(
                destination.join("checkout/README.md"),
                b"Human edit retained on recovery retry\n",
            )?;
            command("check-owned-execution", "recover_replay")?;
            if fs::read(destination.join("checkout/README.md"))?
                != b"Human edit retained on recovery retry\n"
            {
                return Err("recovery retry overwrote human work".into());
            }
            let occupied = root.join("occupied-recovery");
            DirBuilder::new().mode(0o700).create(&occupied)?;
            fs::write(occupied.join("human.txt"), b"Retain existing work\n")?;
            if supervisor.register_recovery_checkout(&occupied).is_ok()
                || fs::read(occupied.join("human.txt"))? != b"Retain existing work\n"
            {
                return Err("native target registration changed occupied work".into());
            }
        }
    }
    if candidate_check {
        supervisor.stop()?;
    } else {
        supervisor.stop()?;
        let (ticket, run, tool) = quit_ticket.ok_or("quit fixture missing")?;
        assert!(ticket.inspect(Duration::from_secs(1)).is_err());
        assert_eq!(ticket.observe().phase, AttemptPhase::Retired);
        let source_digest = digest(&source);
        let prompt_digest = digest(prompt);
        let scope = ReceiptScope::new(
            &run,
            &ready.profile,
            &source_digest,
            &commit,
            Role::Reviewer,
            &tool,
            &prompt_digest,
        )?;
        let result = read_retained(&profile, &scope)?.ok_or("Quit terminal receipt missing")?;
        if result.reason != "cancelled"
            || !result.output_complete
            || ticket.observe().started.map(|started| started.process) != Some(result.process)
        {
            return Err("Quit lost the exact cancelled process receipt".into());
        }
        assert!(ticket.custody()?.sealed);
        assert_eq!(
            ticket.custody()?.state,
            soda_desktop_runtime::factory_provider::CustodyState::Released
        );
    }
    credentials.withdraw(&account.connection).await?;
    assert!(!profile.join("host.sock").exists());
    // Fixture cleanup is separate from outcome verification: uncertain staging
    // remains intact until the check explicitly removes its exact owned roots.
    for run in runs {
        Broker::retire_bundle(&run, &ready.profile, &digest(&source))?;
    }
    if candidate_check {
        let restarted = NativeSupervisor::start(payload, backend, &profile)?;
        let health = restarted.health()?;
        if !Command::new(client)
            .arg(&profile)
            .arg(&health.instance)
            .arg("check-owned-execution")
            .arg("export")
            .status()?
            .success()
        {
            return Err("native candidate export after restart failed".into());
        }
        if !Command::new(client)
            .arg(&profile)
            .arg(&health.instance)
            .arg("check-owned-execution")
            .arg("recover_replay")
            .status()?
            .success()
        {
            return Err("native work recovery replay after restart failed".into());
        }
        if fs::read(root.join("recovered-work/checkout/README.md"))?
            != b"Human edit retained on recovery retry\n"
        {
            return Err("restarted recovery changed human work".into());
        }
        if !Command::new(client)
            .arg(&profile)
            .arg(&health.instance)
            .arg("withdraw-native-sponsorship")
            .arg(&account.connection)
            .status()?
            .success()
        {
            return Err("native sponsorship withdrawal after key removal failed".into());
        }
        restarted.stop()?;
        println!("PASS: exact candidate bundle remains exportable after native restart without the execution registry");
    }
    if !candidate_check {
        println!("PASS: actual supervisor owns finite/cancel/Quit executions; Go observes stopped retirement; cancellation and Quit retain exact terminal process receipts and release custody before child dependencies stop");
    }
    Ok(())
}

async fn sign(
    socket: &Path,
    bearer: &str,
    instance: &str,
    id: &str,
    digest: &str,
) -> Result<Response, Box<dyn std::error::Error>> {
    let body = serde_json::to_vec(&serde_json::json!({"capability_id":id,"scope_digest":digest}))?;
    Ok(request_authenticated_async(
        socket,
        "POST",
        "/api/native/mcp-sign",
        "soda-native",
        &body,
        bearer,
        &[("x-soda-native-instance", instance)],
        Duration::from_secs(5),
        Limits {
            header_bytes: 4096,
            body_bytes: 256,
        },
    )
    .await
    .map_err(|error| format!("native signing transport: {error:?}"))?)
}

async fn managed(
    root: &Path,
    payload: &Path,
    backend: &Path,
    go_client: Option<&Path>,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut random = [0u8; 8];
    getrandom::fill(&mut random)?;
    let name: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    let profile = root.join(format!("managed-mcp-{name}"));
    let id = "0123456789abcdef0123456789abcdef";
    let digest = "a".repeat(64);
    let mut first = None;
    for restart in 0..2 {
        let supervisor = NativeSupervisor::start(payload, backend, &profile)?;
        let ready = supervisor.health()?;
        let bearer = fs::read_to_string(profile.join("backend.key"))?;
        let socket = profile.join("host.sock");
        let response = sign(&socket, &bearer, &ready.instance, id, &digest).await?;
        if let Some(go_client) = go_client {
            assert!(Command::new(go_client)
                .arg(&profile)
                .arg(&ready.instance)
                .status()?
                .success());
        }
        let recovery = if go_client.is_some() {
            let broker_run = "c7".repeat(16);
            let recovered = supervisor.native_recovery(&broker_run).await?;
            assert_eq!(
                recovered.run(),
                fs::read_to_string(profile.join("check-recovery-run.id"))?
            );
            assert_ne!(recovered.run(), recovered.broker_run());
            assert_eq!(recovered.workspace(), "p123456789012345678901234");
            assert_eq!(recovered.broker_run(), broker_run);
            assert!(recovered.recorded_placement().is_none());
            let evidence = recovered.read_evidence()?;
            assert!(
                evidence.intent.is_none()
                    && evidence.receipt.is_none()
                    && evidence.custody.is_none()
            );
            assert!(supervisor.native_recovery("invalid").await.is_err());
            assert!(supervisor.native_recovery(&"fa".repeat(16)).await.is_err());
            assert!(supervisor
                .product(
                    "GET",
                    &format!("/api/native/execution/recovery/{broker_run}"),
                    &[]
                )
                .is_err());
            Some(recovered)
        } else {
            None
        };
        assert_eq!(response.status, 200);
        let value: serde_json::Value = serde_json::from_slice(&response.body)?;
        let secret = value["secret"].as_str().ok_or("missing scoped secret")?;
        let mut reference =
            Hmac::<Sha256>::new_from_slice(&fs::read(profile.join("native-mcp.key"))?)?;
        reference.update(format!("{id}\n{digest}").as_bytes());
        let expected: String = reference
            .finalize()
            .into_bytes()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        assert_eq!(secret, expected);
        if restart == 0 {
            first = Some(secret.to_owned());
            assert_eq!(
                sign(&socket, "0", &ready.instance, id, &digest)
                    .await?
                    .status,
                401
            );
            assert_eq!(
                sign(
                    &socket,
                    &bearer,
                    "00000000000000000000000000000000",
                    id,
                    &digest
                )
                .await?
                .status,
                401
            );
            assert_eq!(
                sign(&socket, &bearer, &ready.instance, "invalid", &digest)
                    .await?
                    .status,
                400
            );
        } else {
            assert_eq!(first.as_deref(), Some(secret));
        }
        supervisor.stop()?;
        if let Some(recovery) = recovery {
            assert!(recovery.read_evidence().is_err());
        }
        assert!(!socket.exists());
        if let Some(go_client) = go_client {
            assert!(Command::new(go_client)
                .arg(&profile)
                .arg(&ready.instance)
                .arg("retired")
                .status()?
                .success());
        }
    }
    println!("PASS: managed native MCP callback, peer channel, denial, retirement and restart");
    if go_client.is_some() {
        println!("PASS: Rust recovery reader retains canonical/native scope across restart, leaves missing local evidence unknown, refuses invalid/missing selectors and generic product access, and fences evidence after retirement");
    }
    Ok(())
}

// Actual cross-language retained-file readback, not task execution or admission.
fn task_manifest_bindings(profile: &Path) -> Result<(), Box<dyn std::error::Error>> {
    use base64::Engine;
    use soda_desktop_runtime::factory_provider::{read_intent, ReceiptScope, Role};
    let bindings: Vec<serde_json::Value> =
        serde_json::from_slice(&fs::read(profile.join("task-manifest-bindings.json"))?)?;
    let root = profile.join("native-run-intents");
    fs::create_dir_all(&root)?;
    fs::set_permissions(&root, fs::Permissions::from_mode(0o700))?;
    for fixture in bindings {
        let raw = fixture["intent"].as_str().ok_or("binding intent missing")?;
        let wire: serde_json::Value = serde_json::from_str(raw)?;
        let field =
            |value: &serde_json::Value, key: &str| -> Result<String, Box<dyn std::error::Error>> {
                Ok(value[key]
                    .as_str()
                    .ok_or("binding selector missing")?
                    .to_owned())
            };
        let run = field(&wire, "run")?;
        let profile_id = field(&wire["native"]["workspace"], "profile")?;
        let source = field(&wire["native"], "bundle_sha256")?;
        let candidate = field(&wire, "source_commit")?;
        let tool = field(&wire["native"]["tool"], "sha256")?;
        let prompt = field(&wire["native"], "prompt_sha256")?;
        let scope = ReceiptScope::new(
            &run,
            &profile_id,
            &source,
            &candidate,
            Role::Coder,
            &tool,
            &prompt,
        )?;
        let manifest = base64::engine::general_purpose::STANDARD.decode(
            fixture["manifest"]
                .as_str()
                .ok_or("binding manifest missing")?,
        )?;
        let retained = root.join(format!("{run}.json"));
        let material = root.join(format!("{run}.manifest"));
        let pending = root.join(format!("{run}.manifest.pending"));
        for (path, bytes) in [
            (&retained, raw.as_bytes()),
            (&material, manifest.as_slice()),
        ] {
            let mut file = OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(path)?;
            file.write_all(bytes)?;
            file.sync_all()?;
        }
        if read_intent(profile, &scope)?.is_none() {
            return Err("exact task binding not read".into());
        }
        let mut changed = manifest.clone();
        changed.push(b' ');
        fs::write(&material, changed)?;
        if read_intent(profile, &scope).is_ok() {
            return Err("altered manifest was accepted".into());
        }
        fs::write(&material, &manifest)?;
        fs::write(&pending, b"incomplete")?;
        if read_intent(profile, &scope).is_ok() {
            return Err("pending manifest was accepted".into());
        }
        fs::remove_file(pending)?;
        if read_intent(profile, &scope)?.is_none() {
            return Err("exact manifest readback failed".into());
        }
    }
    println!("PASS: exact Go task intent/manifest readback for token and CLI sources; altered bytes and a pending manifest marker refused; no task or provider admitted");
    Ok(())
}

async fn tool_races(root: &Path) -> Result<(), Box<dyn std::error::Error>> {
    use soda_desktop_runtime::factory_provider::Tools;
    use std::io::{BufRead, BufReader};
    use std::process::Stdio;
    let tools = Tools::new(None, root)?;
    let mode = |value: &str| fs::write(root.join("mode"), value);
    mode("interrupt")?;
    let mut pending = Box::pin(tools.inspect());
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    while !root.join("download-entered").is_file() {
        tokio::select! {
            result = &mut pending => return Err(format!("interrupted download completed early: {:?}", result.err()).into()),
            _ = tokio::time::sleep(Duration::from_millis(10)) => (),
        }
        if std::time::Instant::now() > deadline {
            return Err("tool download barrier missing".into());
        }
    }
    drop(pending);
    let cache = root.join("native-tools/muse");
    if fs::read_dir(&cache)?.next().is_some() || tools.status()?.state == "ready" {
        return Err("cancelled download published a tool or retained staging bytes".into());
    }
    mode("change")?;
    if tools.inspect().await.is_ok() || tools.status()?.state != "unavailable" {
        return Err("changed release was published as current".into());
    }
    mode("v1")?;
    let first = tools.inspect().await?;
    let first_bytes = fs::read(&first.tool.executable)?;
    let mut child = Command::new(&first.tool.executable)
        .arg("tool-capture").stdin(Stdio::piped()).stdout(Stdio::piped())
        .stderr(Stdio::null()).spawn()?;
    let result = async {
        let mut output = BufReader::new(child.stdout.take().ok_or("capture output missing")?);
        let mut initial = String::new();
        output.read_line(&mut initial)?;
        if initial.trim() != first.tool.sha256 {
            return Err("active tool did not capture the selected bytes".into());
        }
        mode("v2")?;
        let second = tools.refresh().await?;
        if first.tool.sha256 == second.tool.sha256
            || first.tool.executable == second.tool.executable
            || fs::read(&first.tool.executable)? != first_bytes
            || tools.status()?.selected_version.as_deref() != Some(&second.tool.version)
            || tools.status()?.state != "ready"
        {
            return Err("new selection replaced active bytes or retained stale status".into());
        }
        child.stdin.take().ok_or("capture release missing")?.write_all(b"x")?;
        let mut retained = String::new();
        output.read_line(&mut retained)?;
        if retained != initial || !child.wait()?.success() {
            return Err("active tool changed after selecting the new release".into());
        }
        let next = Command::new(&second.tool.executable).arg("tool-capture").output()?;
        if !next.status.success()
            || std::str::from_utf8(&next.stdout)?.lines().next() != Some(second.tool.sha256.as_str())
        {
            return Err("new run did not capture the new verified tool".into());
        }
        for invalid in ["bad-flags", "bad-json"] {
            mode(invalid)?;
            if tools.refresh().await.is_ok() || tools.status()?.state != "unavailable"
                || tools.status()?.selected_version.as_deref() != Some(second.tool.version.as_str())
            {
                return Err("incompatible CLI became a ready selection".into());
            }
            mode("v2")?;
            if tools.inspect().await?.tool.sha256 != second.tool.sha256 {
                return Err("failed interface probe replaced the prior verified tool".into());
            }
        }
        mode("pause-probe")?;
        let mut pending = Box::pin(tools.refresh());
        let deadline = std::time::Instant::now() + Duration::from_secs(30);
        let (probe, pid) = loop {
            if let Some(found) = fs::read_dir(&cache)?.filter_map(Result::ok).find_map(|entry| {
                let path = entry.path();
                fs::read_to_string(path.join("probe-entered")).ok().map(|pid| (path, pid))
            }) { break found; }
            tokio::select! {
                result = &mut pending => return Err(format!("protocol probe completed before cancellation: {:?}", result.err()).into()),
                _ = tokio::time::sleep(Duration::from_millis(10)) => (),
            }
            if std::time::Instant::now() > deadline { return Err("protocol cancellation barrier missing".into()); }
        };
        drop(pending);
        mode("v2")?;
        if tools.inspect().await?.tool.sha256 != second.tool.sha256 { return Err("cancelled probe replaced the current selection".into()); }
        let deadline = std::time::Instant::now() + Duration::from_secs(2);
        while probe.exists() {
            if std::time::Instant::now() > deadline { return Err("cancelled protocol probe was not joined/cleaned".into()); }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        if Path::new("/proc").join(pid).exists() || tools.status()?.state != "ready" {
            return Err("cancelled protocol child or stale failure survived recovery".into());
        }
        if fs::read_dir(&cache)?.filter_map(Result::ok).any(|entry| entry.file_name().to_string_lossy().starts_with('.')) {
            return Err("tool selection left a pending artifact".into());
        }
        Ok::<(), Box<dyn std::error::Error>>(())
    }.await;
    let _ = child.kill();
    let retirement = child.wait();
    result?;
    retirement?;
    println!("PASS: native tool owner cancels an interrupted download without publication/staging bytes, refuses a release changed during acquisition, recovers current status, preserves active captured ELF bytes and selects the new verified ELF for the next run; incompatible flags/terminal protocol stay unavailable and retain the prior selection; declared no-forwarding releases, no provider/model/login");
    Ok(())
}
