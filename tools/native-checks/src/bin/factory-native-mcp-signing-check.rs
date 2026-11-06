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
    let root = PathBuf::from(args.first().ok_or("scratch root required")?);
    if args.len() == 2 && args[1] == "preparation" {
        return preparation_check(&root);
    }
    if args.len() == 5 && args[4] == "preparation-managed" {
        return managed_preparations(
            &root,
            Path::new(&args[1]),
            Path::new(&args[2]),
            Path::new(&args[3]),
        );
    }
    if args.len() == 5 && args[4] == "tool" {
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
        check("unavailable")?;
        let selection_started = std::time::Instant::now();
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?
            .block_on(supervisor.select_execution_tools(Path::new(&std::env::var(
                "SODA_NATIVE_CHECK_TOOL_DIRECTORY",
            )?)))?;
        println!(
            "Native official ELF selection: {} ms",
            selection_started.elapsed().as_millis()
        );
        check("current")?;
        supervisor.stop()?;
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

fn managed_preparations(
    root: &Path,
    payload: &Path,
    backend: &Path,
    client: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    use soda_desktop_runtime::factory_provider::Broker;
    let mut roots = Vec::new();
    for mode in ["ready", "pending"] {
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
    println!("PASS: supervisor Quit cancels and joins an actually running setup before child shutdown, retaining inputs and scoped retirement evidence");
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

// Uses the established isolated local ELF/keyring fixture. The Go row is a
// declared intent, not a dispatch admission or a connected provider proof.
async fn managed_execution(
    root: &Path,
    payload: &Path,
    backend: &Path,
    client: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    use sha2::Digest;
    use soda_desktop_runtime::credentials::{Account, Credentials, Provider, Token};
    use soda_desktop_runtime::factory_provider::{
        AttemptPhase, Broker, Budget, ExecutionOutcome, ExecutionStart, LaunchIntent, Policy, Role,
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
    let prompt = b"Observe one finite local fixture process.\n";
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
        let entries: Vec<_> =
            fs::read_dir(profile.join("native-preparations"))?.collect::<Result<_, _>>()?;
        if entries.len() != 1 {
            return Err("candidate source preparation missing".into());
        }
        serde_json::from_slice::<serde_json::Value>(&fs::read(
            entries[0].path().join("request.json"),
        )?)?
    };
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
        let policy = Policy::new(
            &run,
            &ready.profile,
            &digest(&source),
            &commit,
            &account.connection,
            if role == "soda-coder" {
                Role::Coder
            } else {
                Role::Reviewer
            },
            Duration::from_secs(60),
            Budget::new(2, 256 * 1024, 8192)?,
        )?;
        let mcp = supervisor
            .worker_mcp(
                &intent,
                &["factory.context".into()],
                intent.run(),
                &"a".repeat(64),
            )
            .await?;
        let ticket = supervisor.launch_admitted_execution(ExecutionStart {
            intent: intent.clone(),
            policy,
            credentials: credentials.clone(),
            capability: credentials.capability(&account.connection).await?,
            bundle: source.clone(),
            prompt: prompt.to_vec(),
            executable,
            mcp,
        })?;
        let deadline = std::time::Instant::now() + Duration::from_secs(12);
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
        if mode == "quit" {
            quit_ticket = Some(ticket);
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
                ExecutionOutcome::Uncertain if mode == "cancel" => break,
                _ => return Err("owned execution result differs".into()),
            }
        }
        command(
            "check-owned-execution",
            if mode == "finite" {
                "complete"
            } else {
                "uncertain"
            },
        )?;
        if mode == "finite" && candidate_check {
            command("check-owned-execution", "export")?;
        }
    }
    if candidate_check {
        supervisor.stop()?;
    } else {
        // Cancelled local fixtures do not yield a terminal receipt. Quit joins
        // them and reports the remaining reconciliation.
        assert!(supervisor.stop().is_err());
        let ticket = quit_ticket.ok_or("quit fixture missing")?;
        assert!(ticket.inspect(Duration::from_secs(1)).is_err());
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
        restarted.stop()?;
        println!("PASS: exact candidate bundle remains exportable after native restart without the execution registry");
    }
    if !candidate_check {
        println!("PASS: actual supervisor owns finite/cancel/Quit executions; Go callbacks record exact native facts; cancelled results remain uncertain; shutdown joins workers before child dependencies stop");
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
