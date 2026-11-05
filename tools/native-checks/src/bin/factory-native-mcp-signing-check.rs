//! Real private-file/restart check for native MCP key custody. Uses only a
//! disposable profile and no credential, model or worker process.
use hmac::{Hmac, KeyInit, Mac};
use sha2::Sha256;
use soda_desktop_runtime::mcp_signing::Signer;
use std::fs::{self, DirBuilder, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, PermissionsExt};
use std::path::PathBuf;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let root = PathBuf::from(std::env::args().nth(1).ok_or("scratch root required")?);
    let mut random = [0u8; 8];
    getrandom::fill(&mut random)?;
    let name: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    let profile = root.join(name);
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
    println!("PASS: native MCP private key, scoped derivation, restart and invalid-file refusal");
    Ok(())
}
