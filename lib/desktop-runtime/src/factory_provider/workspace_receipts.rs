//! Private task-control request/receipt custody. This records native effects;
//! Go remains the sole product task/workflow and checkpoint admission owner.
use super::ReceiptScope;
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use sha2::{Digest, Sha256};
use soda_execution_protocol::workspace::{Command, Reply, MAX_COMMAND, MAX_REPLY};
use std::fs::{DirBuilder, File};
use std::io;
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};

pub(super) struct Receipts {
    root: PathBuf,
}
fn denied() -> io::Error {
    io::Error::other("native task receipt unavailable or differs")
}
impl Receipts {
    fn path(profile: &Path, scope: &ReceiptScope<'_>) -> io::Result<PathBuf> {
        super::receipt::profile(profile, scope.profile)?;
        Ok(profile.join("native-task-receipts").join(scope.run))
    }
    fn binding(scope: &ReceiptScope<'_>, manifest: &[u8], invocation: &str) -> io::Result<Vec<u8>> {
        if manifest.is_empty()
            || manifest.len() > soda_execution_protocol::MAX_TASK_MANIFEST
            || invocation.is_empty()
            || invocation.len() > 128
            || invocation.chars().any(char::is_control)
        {
            return Err(denied());
        }
        let manifest_sha256: String = Sha256::digest(manifest)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        Ok(serde_json::to_vec(
            &serde_json::json!({"kind":"factory_task_receipts","version":1,
            "scope":scope.fingerprint(),"manifest_sha256":manifest_sha256,"invocation":invocation}),
        )?)
    }
    pub fn create(
        profile: &Path,
        scope: &ReceiptScope<'_>,
        manifest: &[u8],
        invocation: &str,
    ) -> io::Result<Self> {
        let binding = Self::binding(scope, manifest, invocation)?;
        let root = Self::path(profile, scope)?;
        private_dir(root.parent().ok_or_else(denied)?).map_err(|_| denied())?;
        DirBuilder::new().mode(0o700).create(&root)?;
        File::open(root.parent().ok_or_else(denied)?)?.sync_all()?;
        publish_bytes(&root.join("binding"), &binding)?;
        publish_bytes(&root.join("manifest"), manifest)?;
        Ok(Self { root })
    }
    pub fn open(
        profile: &Path,
        scope: &ReceiptScope<'_>,
        manifest: &[u8],
        invocation: &str,
    ) -> io::Result<Self> {
        let binding = Self::binding(scope, manifest, invocation)?;
        let root = Self::path(profile, scope)?;
        super::muse::private(root.parent().ok_or_else(denied)?)?;
        super::muse::private(&root)?;
        let result = Self { root };
        if result.read("binding", 1024)? != binding
            || result.read("manifest", soda_execution_protocol::MAX_TASK_MANIFEST)? != manifest
        {
            return Err(denied());
        }
        Ok(result)
    }
    fn read(&self, name: &str, limit: usize) -> io::Result<Vec<u8>> {
        match self.root.join(format!("{name}.pending")).symlink_metadata() {
            Ok(_) => return Err(denied()),
            Err(e) if e.kind() == io::ErrorKind::NotFound => (),
            Err(e) => return Err(e),
        }
        read_bytes(&self.root.join(name), limit)
    }
    pub fn request(&self, command: &Command) -> io::Result<bool> {
        let name = format!("{}.request", command.receipt_key()?);
        match self.read(&name, MAX_COMMAND) {
            Ok(bytes) if bytes == serde_json::to_vec(command)? => Ok(true),
            Ok(_) => Err(denied()),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(false),
            Err(e) => Err(e),
        }
    }
    pub fn begin(&self, command: &Command) -> io::Result<()> {
        if !command.retains_receipt() {
            return Err(denied());
        }
        publish_bytes(
            &self
                .root
                .join(format!("{}.request", command.receipt_key()?)),
            &serde_json::to_vec(command)?,
        )
    }
    pub fn reply(&self, command: &Command) -> io::Result<Option<Reply>> {
        if !self.request(command)? {
            return Ok(None);
        }
        let name = format!("{}.receipt", command.receipt_key()?);
        match self.read(&name, MAX_REPLY) {
            Ok(bytes) => serde_json::from_slice(&bytes)
                .map(Some)
                .map_err(io::Error::other),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(e),
        }
    }
    pub fn complete(&self, command: &Command, reply: &Reply) -> io::Result<()> {
        if !self.request(command)? {
            return Err(denied());
        }
        let bytes = serde_json::to_vec(reply)?;
        if bytes.len() > MAX_REPLY {
            return Err(denied());
        }
        publish_bytes(
            &self
                .root
                .join(format!("{}.receipt", command.receipt_key()?)),
            &bytes,
        )
    }
}
