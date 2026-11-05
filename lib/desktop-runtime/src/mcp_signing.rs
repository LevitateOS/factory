//! Native custody for the desktop worker MCP root key. Product capability
//! records and current-run admission remain in Go; only one scoped derivation
//! result may cross the native callback.
use crate::database::{private_dir, private_file};
use hmac::{Hmac, KeyInit, Mac};
use sha2::Sha256;
use std::fs;
use std::io::{Read, Write};
use std::path::Path;
use zeroize::Zeroizing;

type Result<T> = std::result::Result<T, String>;

pub struct Signer {
    key: Zeroizing<[u8; 32]>,
}

impl Signer {
    /// Open the exclusive desktop profile's persistent root key. A partial or
    /// invalid existing file is an error, never a reason to rotate silently.
    /// The profile owner must hold its exclusive lock before calling this.
    pub fn open(profile: &Path) -> Result<Self> {
        private_dir(profile).map_err(|_| "native MCP profile unavailable")?;
        let path = profile.join("native-mcp.key");
        match private_file(&path, true) {
            Ok(mut file) => {
                let mut key = Zeroizing::new([0u8; 32]);
                getrandom::fill(&mut *key).map_err(|_| "native MCP key generation unavailable")?;
                file.write_all(&*key)
                    .and_then(|_| file.sync_all())
                    .map_err(|_| "native MCP key write unavailable")?;
                fs::File::open(profile)
                    .and_then(|directory| directory.sync_all())
                    .map_err(|_| "native MCP key durability unavailable")?;
                Ok(Self { key })
            }
            Err(error)
                if error
                    .downcast_ref::<std::io::Error>()
                    .is_some_and(|error| error.kind() == std::io::ErrorKind::AlreadyExists) =>
            {
                let mut file =
                    private_file(&path, false).map_err(|_| "native MCP key unavailable")?;
                let mut key = Zeroizing::new([0u8; 32]);
                file.read_exact(&mut *key)
                    .map_err(|_| "native MCP key invalid")?;
                let mut extra = [0u8; 1];
                if file
                    .read(&mut extra)
                    .map_err(|_| "native MCP key invalid")?
                    != 0
                {
                    return Err("native MCP key invalid".into());
                }
                Ok(Self { key })
            }
            Err(_) => Err("native MCP key unavailable".into()),
        }
    }

    /// Derive exactly the Go capability protocol's HMAC over a canonical
    /// capability ID and immutable scope digest. Caller admission and expiry
    /// remain separate product checks; the root key never leaves this owner.
    pub fn derive(&self, id: &str, scope_digest: &str) -> Result<String> {
        if !valid_id(id) || !valid_digest(scope_digest) {
            return Err("native MCP scope invalid".into());
        }
        let mut mac =
            Hmac::<Sha256>::new_from_slice(&*self.key).map_err(|_| "native MCP key invalid")?;
        mac.update(id.as_bytes());
        mac.update(b"\n");
        mac.update(scope_digest.as_bytes());
        let result = mac.finalize().into_bytes();
        Ok(result.iter().map(|byte| format!("{byte:02x}")).collect())
    }
}

fn valid_id(id: &str) -> bool {
    id.len() == 32
        && id
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn valid_digest(digest: &str) -> bool {
    digest.len() == 64
        && digest
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}
