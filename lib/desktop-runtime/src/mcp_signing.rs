//! Native custody for the desktop worker MCP root key. Product capability
//! records and current-run admission remain in Go; only one scoped derivation
//! result may cross the native callback.
use crate::private_fs::{private_dir, private_file};
use hmac::{Hmac, KeyInit, Mac};
use sha2::{Digest, Sha256};
use std::ffi::CString;
use std::fs::{self, OpenOptions};
use std::io::{Read, Write};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
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
        let pending = profile.join("native-mcp.key.pending");
        match fs::symlink_metadata(&path) {
            Ok(_) => {
                match fs::symlink_metadata(&pending) {
                    Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
                    _ => return Err("native MCP key staging uncertain".into()),
                }
                let key = read_key(&path)?;
                identity(profile, &key)?;
                return Ok(Self { key });
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err("native MCP key unavailable".into()),
        }
        for name in ["native-mcp.key.identity", "native-mcp.key.identity.pending"] {
            match fs::symlink_metadata(profile.join(name)) {
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
                _ => return Err("native MCP retained key missing".into()),
            }
        }
        match fs::symlink_metadata(&pending) {
            Ok(metadata) => {
                if !metadata.is_file()
                    || metadata.uid() != unsafe { libc::geteuid() }
                    || metadata.mode() & 0o077 != 0
                    || metadata.nlink() != 1
                {
                    return Err("native MCP key staging uncertain".into());
                }
                fs::remove_file(&pending).map_err(|_| "native MCP key staging uncertain")?;
                fs::File::open(profile)
                    .and_then(|directory| directory.sync_all())
                    .map_err(|_| "native MCP key staging uncertain")?;
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err("native MCP key staging uncertain".into()),
        }
        let mut file =
            private_file(&pending, true).map_err(|_| "native MCP key staging unavailable")?;
        let mut key = Zeroizing::new([0u8; 32]);
        getrandom::fill(&mut *key).map_err(|_| "native MCP key generation unavailable")?;
        file.write_all(&*key)
            .and_then(|_| file.sync_all())
            .map_err(|_| "native MCP key write unavailable")?;
        let from = CString::new(pending.as_os_str().as_bytes())
            .map_err(|_| "native MCP key staging invalid")?;
        let to = CString::new(path.as_os_str().as_bytes())
            .map_err(|_| "native MCP key staging invalid")?;
        if unsafe {
            libc::renameat2(
                libc::AT_FDCWD,
                from.as_ptr(),
                libc::AT_FDCWD,
                to.as_ptr(),
                libc::RENAME_NOREPLACE,
            )
        } != 0
        {
            return Err("native MCP key commit unavailable".into());
        }
        fs::File::open(profile)
            .and_then(|directory| directory.sync_all())
            .map_err(|_| "native MCP key durability unavailable")?;
        identity(profile, &key)?;
        Ok(Self { key })
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

    /// Verify one presented worker secret without returning the expected
    /// secret to the Go worker HTTP transport. Product state remains Go-owned.
    pub fn verify(&self, id: &str, scope_digest: &str, presented: &str) -> Result<bool> {
        if !valid_id(id) || !valid_digest(scope_digest) {
            return Err("native MCP scope invalid".into());
        }
        if !valid_digest(presented) {
            return Ok(false);
        }
        let mut bytes = Zeroizing::new([0u8; 32]);
        for (index, pair) in presented.as_bytes().chunks_exact(2).enumerate() {
            let high = (pair[0] as char)
                .to_digit(16)
                .ok_or("native MCP secret invalid")?;
            let low = (pair[1] as char)
                .to_digit(16)
                .ok_or("native MCP secret invalid")?;
            bytes[index] = ((high << 4) | low) as u8;
        }
        let mut mac =
            Hmac::<Sha256>::new_from_slice(&*self.key).map_err(|_| "native MCP key invalid")?;
        mac.update(id.as_bytes());
        mac.update(b"\n");
        mac.update(scope_digest.as_bytes());
        Ok(mac.verify_slice(&*bytes).is_ok())
    }
}

// The identity is nonsecret evidence that this profile has used a root key.
// A missing or changed key after this marker commits must not silently rotate.
// Existing pre-marker profiles acquire it only after their exact key is read.
fn identity(profile: &Path, key: &[u8; 32]) -> Result<()> {
    let marker = profile.join("native-mcp.key.identity");
    let pending = profile.join("native-mcp.key.identity.pending");
    let expected: String = Sha256::digest(key)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    match fs::symlink_metadata(&marker) {
        Ok(_) => {
            match fs::symlink_metadata(&pending) {
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
                _ => return Err("native MCP key identity staging uncertain".into()),
            }
            let file = OpenOptions::new()
                .read(true)
                .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                .open(&marker)
                .map_err(|_| "native MCP key identity unavailable")?;
            let metadata = file
                .metadata()
                .map_err(|_| "native MCP key identity unavailable")?;
            if !metadata.is_file()
                || metadata.uid() != unsafe { libc::geteuid() }
                || metadata.mode() & 0o077 != 0
                || metadata.nlink() != 1
                || metadata.len() != 64
            {
                return Err("native MCP key identity invalid".into());
            }
            let mut actual = String::new();
            file.take(65)
                .read_to_string(&mut actual)
                .map_err(|_| "native MCP key identity invalid")?;
            if actual != expected {
                return Err("native MCP key identity differs".into());
            }
            return Ok(());
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
        Err(_) => return Err("native MCP key identity unavailable".into()),
    }
    match fs::symlink_metadata(&pending) {
        Ok(metadata) => {
            if !metadata.is_file()
                || metadata.uid() != unsafe { libc::geteuid() }
                || metadata.mode() & 0o077 != 0
                || metadata.nlink() != 1
            {
                return Err("native MCP key identity staging uncertain".into());
            }
            fs::remove_file(&pending).map_err(|_| "native MCP key identity staging uncertain")?;
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
        Err(_) => return Err("native MCP key identity staging uncertain".into()),
    }
    let mut file =
        private_file(&pending, true).map_err(|_| "native MCP key identity staging unavailable")?;
    file.write_all(expected.as_bytes())
        .and_then(|_| file.sync_all())
        .map_err(|_| "native MCP key identity write unavailable")?;
    let from = CString::new(pending.as_os_str().as_bytes())
        .map_err(|_| "native MCP key identity staging invalid")?;
    let to = CString::new(marker.as_os_str().as_bytes())
        .map_err(|_| "native MCP key identity staging invalid")?;
    if unsafe {
        libc::renameat2(
            libc::AT_FDCWD,
            from.as_ptr(),
            libc::AT_FDCWD,
            to.as_ptr(),
            libc::RENAME_NOREPLACE,
        )
    } != 0
    {
        return Err("native MCP key identity commit unavailable".into());
    }
    fs::File::open(profile)
        .and_then(|directory| directory.sync_all())
        .map_err(|_| "native MCP key identity durability unavailable")?;
    Ok(())
}

fn read_key(path: &Path) -> Result<Zeroizing<[u8; 32]>> {
    let mut file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)
        .map_err(|_| "native MCP key unavailable")?;
    let metadata = file.metadata().map_err(|_| "native MCP key unavailable")?;
    if !metadata.is_file()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
        || metadata.nlink() != 1
        || metadata.len() != 32
    {
        return Err("native MCP key invalid".into());
    }
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
    Ok(key)
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
