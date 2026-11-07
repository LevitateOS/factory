//! Exact native task-control receipts. A request is synced before effects; a
//! missing reply remains uncertain and never authorizes replaying the effect.
use super::*;
use soda_execution_protocol::workspace::{Command as TaskCommand, Reply, MAX_COMMAND, MAX_REPLY};
const MAX_BYTES: u64 = 64 * 1024 * 1024;
const MAX_FILES: usize = 512;

fn read_file(path: &Path, limit: usize) -> io::Result<Vec<u8>> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != 0
        || meta.mode() & 0o7777 != 0o600
        || meta.len() > limit as u64
    {
        return Err(denied("task receipt metadata invalid"));
    }
    let mut bytes = Vec::new();
    file.take(limit as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != meta.len() {
        return Err(denied("task receipt changed"));
    }
    Ok(bytes)
}
fn write(path: &Path, bytes: &[u8]) -> io::Result<()> {
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    file.write_all(bytes)?;
    file.sync_all()?;
    File::open(
        path.parent()
            .ok_or_else(|| denied("task receipt parent missing"))?,
    )?
    .sync_all()
}
pub(super) fn capture(directory: &Path, manifest: &str, invocation: &str) -> io::Result<()> {
    let root = directory.join("task-receipts");
    DirBuilder::new().mode(0o700).create(&root)?;
    write(
        &root.join("binding"),
        &serde_json::to_vec(
            &serde_json::json!({"version":1,"manifest_digest":manifest,"invocation":invocation}),
        )?,
    )?;
    File::open(directory)?.sync_all()
}
pub(super) fn verify(directory: &Path, manifest: &str, invocation: &str) -> io::Result<()> {
    let root = directory.join("task-receipts");
    private_dir(&root, 0o700)?;
    let binding: serde_json::Value =
        serde_json::from_slice(&read_file(&root.join("binding"), 1024)?)?;
    if binding
        != serde_json::json!({"version":1,"manifest_digest":manifest,"invocation":invocation})
    {
        return Err(denied("task receipt binding differs"));
    }
    Ok(())
}
fn capacity(root: &Path, extra: usize) -> io::Result<()> {
    let mut bytes = extra as u64;
    let mut count = 0;
    for entry in fs::read_dir(root)? {
        let entry = entry?;
        let meta = entry.path().symlink_metadata()?;
        if !meta.is_file() || meta.uid() != 0 || meta.mode() & 0o7777 != 0o600 {
            return Err(denied("task receipt inventory invalid"));
        }
        bytes = bytes
            .checked_add(meta.len())
            .ok_or_else(|| denied("task receipt size overflow"))?;
        count += 1;
    }
    if count >= MAX_FILES || bytes > MAX_BYTES {
        return Err(denied("task receipt storage exhausted"));
    }
    Ok(())
}
pub(super) fn read(directory: &Path, command: &TaskCommand) -> io::Result<Option<Vec<u8>>> {
    if !command.retains_receipt() {
        return Err(denied("task command has no retained receipt"));
    }
    let root = directory.join("task-receipts");
    private_dir(&root, 0o700)?;
    let key = command.receipt_key()?;
    let request = match read_file(&root.join(format!("{key}.request")), MAX_COMMAND) {
        Ok(bytes) => bytes,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e),
    };
    if request != serde_json::to_vec(command)? {
        return Err(denied("task receipt command differs"));
    }
    // Publication is atomic. A leftover partial reply must not become evidence.
    match root.join(format!("{key}.pending")).symlink_metadata() {
        Ok(_) => return Err(denied("task reply publication incomplete")),
        Err(e) if e.kind() == io::ErrorKind::NotFound => (),
        Err(e) => return Err(e),
    }
    match read_file(&root.join(format!("{key}.receipt")), MAX_REPLY) {
        Ok(bytes) => {
            let _: Reply = serde_json::from_slice(&bytes)?;
            Ok(Some(bytes))
        }
        Err(e) if e.kind() == io::ErrorKind::NotFound => {
            Err(denied("task outcome remains uncertain"))
        }
        Err(e) => Err(e),
    }
}
pub(super) fn begin(directory: &Path, command: &TaskCommand) -> io::Result<()> {
    let root = directory.join("task-receipts");
    private_dir(&root, 0o700)?;
    let bytes = serde_json::to_vec(command)?;
    capacity(&root, bytes.len())?;
    write(
        &root.join(format!("{}.request", command.receipt_key()?)),
        &bytes,
    )
}
pub(super) fn complete(directory: &Path, command: &TaskCommand, reply: &[u8]) -> io::Result<()> {
    if reply.is_empty() || reply.len() > MAX_REPLY {
        return Err(denied("task receipt size invalid"));
    }
    let _: Reply = serde_json::from_slice(reply)?;
    let root = directory.join("task-receipts");
    private_dir(&root, 0o700)?;
    capacity(&root, reply.len())?;
    let key = command.receipt_key()?;
    let pending = root.join(format!("{key}.pending"));
    let receipt = root.join(format!("{key}.receipt"));
    if receipt.try_exists()? {
        return Err(denied("task receipt already exists"));
    }
    write(&pending, reply)?;
    fs::rename(pending, receipt)?;
    File::open(root)?.sync_all()
}
pub(super) fn cleanup(directory: &Path) -> io::Result<()> {
    let root = directory.join("task-receipts");
    match root.symlink_metadata() {
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e),
        Ok(_) => private_dir(&root, 0o700)?,
    }
    for entry in fs::read_dir(&root)? {
        let entry = entry?;
        let name = entry.file_name();
        let name = name
            .to_str()
            .ok_or_else(|| denied("task receipt name invalid"))?;
        let valid = name == "binding"
            || name.split_once('.').is_some_and(|(key, suffix)| {
                id(key, 64) && matches!(suffix, "request" | "receipt" | "pending")
            });
        let meta = entry.path().symlink_metadata()?;
        if !valid || !meta.is_file() || meta.uid() != 0 || meta.mode() & 0o7777 != 0o600 {
            return Err(denied("task receipt cleanup inventory invalid"));
        }
        fs::remove_file(entry.path())?;
    }
    fs::remove_dir(root)
}
