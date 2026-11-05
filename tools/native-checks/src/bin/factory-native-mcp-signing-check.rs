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
    Ok(())
}
