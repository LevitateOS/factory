//! Private task-control request/receipt custody. This records native effects;
//! Go remains the sole product task/workflow and checkpoint admission owner.
use super::ReceiptScope;
use crate::private_fs::{private_dir, private_file, publish_bytes, read_bytes};
use sha2::{Digest, Sha256};
use soda_execution_protocol::workspace::{Command, Event, Parent, Reply, MAX_COMMAND, MAX_REPLY};
use std::fs::{DirBuilder, File};
use std::io::{self, Write};
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};

#[derive(Clone)]
pub(super) struct Receipts {
    root: PathBuf,
    manifest_digest: String,
    parent: Parent,
}
fn denied() -> io::Error {
    io::Error::other("native task receipt unavailable or differs")
}
impl Receipts {
    fn path(profile: &Path, scope: &ReceiptScope<'_>) -> io::Result<PathBuf> {
        super::receipt::profile(profile, scope.profile)?;
        Ok(profile.join("native-task-receipts").join(scope.run))
    }
    fn binding(scope: &ReceiptScope<'_>, manifest: &[u8], parent: &Parent) -> io::Result<Vec<u8>> {
        if manifest.is_empty()
            || manifest.len() > soda_execution_protocol::MAX_TASK_MANIFEST
            || !parent.valid()
        {
            return Err(denied());
        }
        let manifest_sha256: String = Sha256::digest(manifest)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        Ok(serde_json::to_vec(
            &serde_json::json!({"kind":"factory_task_receipts","version":2,
            "scope":scope.fingerprint(),"manifest_sha256":manifest_sha256,"parent":parent}),
        )?)
    }
    pub fn create(
        profile: &Path,
        scope: &ReceiptScope<'_>,
        manifest: &[u8],
        parent: &Parent,
    ) -> io::Result<Self> {
        let binding = Self::binding(scope, manifest, parent)?;
        let root = Self::path(profile, scope)?;
        private_dir(root.parent().ok_or_else(denied)?).map_err(|_| denied())?;
        DirBuilder::new().mode(0o700).create(&root)?;
        File::open(root.parent().ok_or_else(denied)?)?.sync_all()?;
        publish_bytes(&root.join("binding"), &binding)?;
        publish_bytes(&root.join("manifest"), manifest)?;
        Ok(Self {
            root,
            manifest_digest: Sha256::digest(manifest)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect(),
            parent: parent.clone(),
        })
    }
    pub fn open(
        profile: &Path,
        scope: &ReceiptScope<'_>,
        manifest: &[u8],
        parent: &Parent,
    ) -> io::Result<Self> {
        let binding = Self::binding(scope, manifest, parent)?;
        let root = Self::path(profile, scope)?;
        super::muse::private(root.parent().ok_or_else(denied)?)?;
        super::muse::private(&root)?;
        let result = Self {
            root,
            manifest_digest: Sha256::digest(manifest)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect(),
            parent: parent.clone(),
        };
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
        let lock = private_file(&self.root.join("receipts.lock"), false).map_err(|_| denied())?;
        lock.lock()?;
        if !self.request(command)? {
            return Err(denied());
        }
        let bytes = serde_json::to_vec(reply)?;
        if bytes.len() > MAX_REPLY {
            return Err(denied());
        }
        if reply.events.iter().any(|e| {
            e.invocation != self.parent.invocation || e.plan_digest != self.manifest_digest
        }) {
            return Err(denied());
        }
        if let Some(saved) = self.reply(command)? {
            return if serde_json::to_vec(&saved)? == bytes {
                Ok(())
            } else {
                Err(denied())
            };
        }
        let existing = self.read_events()?;
        let prefix = existing.len().min(reply.events.len());
        for i in 0..prefix {
            if existing[i].digest()? != reply.events[i].digest()? {
                return Err(denied());
            }
        }
        if reply.events.len() > existing.len() {
            let mut journal =
                private_file(&self.root.join("events"), false).map_err(|_| denied())?;
            for (i, event) in reply.events.iter().enumerate().skip(existing.len()) {
                let expected_sequence = if i == 0 {
                    String::new()
                } else {
                    reply.events[i - 1].digest()?
                };
                if i >= 512
                    || event.sequence != i as i64 + 1
                    || event.prev_digest != expected_sequence
                {
                    return Err(denied());
                }
                journal.write_all(&serde_json::to_vec(event)?)?;
                journal.write_all(b"\n")?;
            }
            journal.sync_all()?;
        }
        publish_bytes(
            &self
                .root
                .join(format!("{}.receipt", command.receipt_key()?)),
            &bytes,
        )
    }
    fn read_events(&self) -> io::Result<Vec<Event>> {
        let bytes = match read_bytes(&self.root.join("events"), 8 * 1024 * 1024) {
            Ok(bytes) => bytes,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(Vec::new()),
            Err(e) => return Err(e),
        };
        if !bytes.ends_with(b"\n") {
            return Err(denied());
        }
        let mut events: Vec<Event> = Vec::new();
        for line in bytes.split(|b| *b == b'\n').filter(|s| !s.is_empty()) {
            let event: Event = serde_json::from_slice(line)?;
            let previous = events
                .last()
                .map(Event::digest)
                .transpose()?
                .unwrap_or_default();
            if events.len() >= 512
                || event.sequence != events.len() as i64 + 1
                || event.prev_digest != previous
            {
                return Err(denied());
            }
            events.push(event);
        }
        Ok(events)
    }
    pub fn events(&self) -> io::Result<Vec<Event>> {
        let lock = private_file(&self.root.join("receipts.lock"), false).map_err(|_| denied())?;
        lock.lock()?;
        let events = self.read_events()?;
        if events.iter().any(|e| {
            e.invocation != self.parent.invocation || e.plan_digest != self.manifest_digest
        }) {
            return Err(denied());
        }
        Ok(events)
    }
    pub fn final_receipt(&self) -> io::Result<Option<Reply>> {
        let events = self.events()?;
        let Some(event) = events
            .iter()
            .rev()
            .find(|event| event.kind == "task_committed")
        else {
            return Ok(None);
        };
        let head = event
            .detail
            .split_whitespace()
            .find_map(|field| field.strip_prefix("commit="))
            .ok_or_else(denied)?;
        if !super::hex(head, 40) {
            return Err(denied());
        }
        self.reply(&Command::Final {
            expected_head: head.into(),
        })
    }
    pub fn pending(&self) -> io::Result<Vec<Command>> {
        let mut commands = Vec::new();
        for entry in std::fs::read_dir(&self.root)? {
            let name = entry?.file_name();
            let Some(key) = name.to_str().and_then(|s| s.strip_suffix(".request")) else {
                continue;
            };
            if !super::hex(key, 64) || commands.len() >= 256 {
                return Err(denied());
            }
            let command: Command =
                serde_json::from_slice(&self.read(&format!("{key}.request"), MAX_COMMAND)?)?;
            if !command.retains_receipt() || command.receipt_key()? != key {
                return Err(denied());
            }
            if self.reply(&command)?.is_none() {
                commands.push(command);
            }
        }
        Ok(commands)
    }
}
