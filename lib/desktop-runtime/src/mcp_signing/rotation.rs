//! Explicit offline MCP maintenance. The original key remains the enrollment
//! seed. Pending rotation fences startup until its original command recovers.
use super::{valid_digest, valid_id, Result, Signer};
use crate::private_fs::{private_dir, publish_bytes, read_bytes, write_bytes};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{fs, os::unix::fs::MetadataExt, path::Path};
use zeroize::{Zeroize, Zeroizing};

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct RootRotation {
    pub command: String,
    pub previous_root: String,
    pub root: String,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct State {
    receipt: RootRotation,
    key: String,
}
impl Drop for State {
    fn drop(&mut self) {
        self.key.zeroize();
    }
}
const CURRENT: &str = "native-mcp-root.json";
const PENDING: &str = "native-mcp-root.pending";
const RECEIPTS: &str = "native-mcp-rotations";

pub(super) fn fingerprint(key: &[u8; 32]) -> String {
    Sha256::digest(key)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
fn exists(path: &Path) -> Result<bool> {
    match fs::symlink_metadata(path) {
        Ok(_) => Ok(true),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(_) => Err("native MCP rotation record unavailable".into()),
    }
}
fn key(state: &State) -> Result<Zeroizing<[u8; 32]>> {
    if !valid_id(&state.receipt.command)
        || !valid_digest(&state.receipt.previous_root)
        || !valid_digest(&state.receipt.root)
        || !valid_digest(&state.key)
    {
        return Err("native MCP rotation record invalid".into());
    }
    let mut key = Zeroizing::new([0; 32]);
    for (index, pair) in state.key.as_bytes().chunks_exact(2).enumerate() {
        key[index] = ((pair[0] as char).to_digit(16).unwrap() * 16
            + (pair[1] as char).to_digit(16).unwrap()) as u8;
    }
    if fingerprint(&key) != state.receipt.root {
        return Err("native MCP rotated key differs".into());
    }
    Ok(key)
}
fn read_state(path: &Path) -> Result<State> {
    let metadata =
        fs::symlink_metadata(path).map_err(|_| "native MCP rotation record unavailable")?;
    if !metadata.is_file() || metadata.nlink() != 1 {
        return Err("native MCP rotation record invalid".into());
    }
    let raw = Zeroizing::new(
        read_bytes(path, 1024).map_err(|_| "native MCP rotation record unavailable")?,
    );
    let state: State =
        serde_json::from_slice(&raw).map_err(|_| "native MCP rotation record invalid")?;
    key(&state)?;
    Ok(state)
}
fn receipt_path(profile: &Path, command: &str) -> std::path::PathBuf {
    profile.join(RECEIPTS).join(format!("{command}.json"))
}
fn read_receipt(profile: &Path, command: &str) -> Result<RootRotation> {
    let raw = read_bytes(&receipt_path(profile, command), 512)
        .map_err(|_| "native MCP rotation receipt unavailable")?;
    let receipt: RootRotation =
        serde_json::from_slice(&raw).map_err(|_| "native MCP rotation receipt invalid")?;
    if receipt.command != command
        || !valid_digest(&receipt.previous_root)
        || !valid_digest(&receipt.root)
    {
        return Err("native MCP rotation receipt invalid".into());
    }
    Ok(receipt)
}
fn save_receipt(profile: &Path, receipt: &RootRotation) -> Result<()> {
    let path = receipt_path(profile, &receipt.command);
    if exists(&path)? {
        if read_receipt(profile, &receipt.command)? != *receipt {
            return Err("native MCP rotation receipt conflicts".into());
        }
        return Ok(());
    }
    let pending = path.with_file_name(format!("{}.json.pending", receipt.command));
    // A partial receipt cannot authorize another rotation. The committed current
    // record proves precisely which nonsecret receipt to finish publishing.
    if exists(&pending)? {
        read_bytes(&pending, 512).map_err(|_| "native MCP rotation receipt staging invalid")?;
        fs::remove_file(&pending).map_err(|_| "native MCP rotation receipt staging unavailable")?;
    }
    publish_bytes(
        &path,
        &serde_json::to_vec(receipt).map_err(|_| "native MCP receipt invalid")?,
    )
    .map_err(|_| "native MCP rotation receipt commit unavailable".into())
}
pub(super) fn current_key(profile: &Path, original: &[u8; 32]) -> Result<Zeroizing<[u8; 32]>> {
    if exists(&profile.join(RECEIPTS))? {
        private_dir(&profile.join(RECEIPTS)).map_err(|_| "native MCP rotation history invalid")?;
    }
    if exists(&profile.join(PENDING))? {
        return Err("native MCP rotation pending; recover the original maintenance command".into());
    }
    if exists(&profile.join(CURRENT))? {
        let state = read_state(&profile.join(CURRENT))?;
        if read_receipt(profile, &state.receipt.command)? != state.receipt {
            return Err("native MCP rotation receipt differs".into());
        }
        return key(&state);
    }
    if exists(&profile.join(RECEIPTS))? {
        private_dir(&profile.join(RECEIPTS)).map_err(|_| "native MCP rotation history invalid")?;
        if fs::read_dir(profile.join(RECEIPTS))
            .map_err(|_| "native MCP rotation history unavailable")?
            .next()
            .is_some()
        {
            return Err("native MCP retained rotated key missing".into());
        }
    }
    Ok(Zeroizing::new(*original))
}

/// Caller holds the exclusive supervisor lock and runs no product/provider
/// owner. `revoke` must durably revoke all staged/active Go capabilities after
/// closing/draining admission. It is an idempotent local transaction: an unknown
/// reply repeats only revocation, never a launch, inference or remote write.
pub fn rotate(
    profile: &Path,
    command: &str,
    expected_root: &str,
    revoke: impl FnOnce() -> Result<()>,
) -> Result<RootRotation> {
    if !valid_id(command) || !valid_digest(expected_root) {
        return Err("native MCP rotation command invalid".into());
    }
    let original = Signer::open_enrollment(profile)?;
    private_dir(&profile.join(RECEIPTS)).map_err(|_| "native MCP rotation history invalid")?;
    if exists(&receipt_path(profile, command))? {
        let receipt = read_receipt(profile, command)?;
        if receipt.previous_root != expected_root {
            return Err("native MCP rotation command conflicts".into());
        }
        current_key(profile, &original.key)?;
        return Ok(receipt);
    }
    let current = if exists(&profile.join(CURRENT))? {
        Some(read_state(&profile.join(CURRENT))?)
    } else {
        None
    };
    if let Some(state) = current.as_ref() {
        if state.receipt.command == command {
            if state.receipt.previous_root != expected_root {
                return Err("native MCP rotation command conflicts".into());
            }
            if exists(&profile.join(PENDING))? {
                return Err("native MCP rotation staging uncertain".into());
            }
            save_receipt(profile, &state.receipt)?;
            return Ok(state.receipt.clone());
        }
        if read_receipt(profile, &state.receipt.command)? != state.receipt {
            return Err("native MCP rotation receipt differs".into());
        }
    }
    let previous = match current.as_ref() {
        Some(state) => state.receipt.root.clone(),
        None => {
            if fs::read_dir(profile.join(RECEIPTS))
                .map_err(|_| "native MCP rotation history unavailable")?
                .next()
                .is_some()
            {
                return Err("native MCP retained rotated key missing".into());
            }
            fingerprint(&original.key)
        }
    };
    if previous != expected_root {
        return Err("native MCP rotation expected root changed".into());
    }
    let pending = profile.join(PENDING);
    let state = if exists(&pending)? {
        let state = read_state(&pending)?;
        if state.receipt.command != command || state.receipt.previous_root != expected_root {
            return Err("native MCP rotation belongs to another command".into());
        }
        state
    } else {
        let mut next = Zeroizing::new([0; 32]);
        getrandom::fill(&mut *next).map_err(|_| "native MCP key generation unavailable")?;
        let state = State {
            receipt: RootRotation {
                command: command.into(),
                previous_root: previous,
                root: fingerprint(&next),
            },
            key: next.iter().map(|byte| format!("{byte:02x}")).collect(),
        };
        let raw = Zeroizing::new(
            serde_json::to_vec(&state).map_err(|_| "native MCP rotation record invalid")?,
        );
        write_bytes(&pending, &raw).map_err(|_| "native MCP rotation staging unavailable")?;
        state
    };
    revoke()?;
    fs::rename(&pending, profile.join(CURRENT))
        .map_err(|_| "native MCP rotation commit unavailable")?;
    fs::File::open(profile)
        .and_then(|directory| directory.sync_all())
        .map_err(|_| "native MCP rotation durability unavailable")?;
    save_receipt(profile, &state.receipt)?;
    Ok(state.receipt.clone())
}
