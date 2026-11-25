use std::error::Error;
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::path::Path;

type Result<T> = std::result::Result<T, Box<dyn Error>>;

pub(crate) fn private_dir(path: &Path) -> Result<()> {
    match DirBuilder::new().mode(0o700).create(path) {
        Ok(()) => (),
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => (),
        Err(error) => return Err(error.into()),
    }
    let metadata = fs::symlink_metadata(path)?;
    if !metadata.is_dir()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
    {
        return Err("native database directory must be owned, private and not a symlink".into());
    }
    if !path.is_absolute() || fs::canonicalize(path)? != path {
        return Err("native database paths must be absolute with no symlink components".into());
    }
    Ok(())
}

pub(crate) fn private_file(path: &Path, fresh: bool) -> Result<File> {
    let mut options = OpenOptions::new();
    options
        .read(true)
        .append(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW);
    if fresh {
        options.create_new(true);
    } else {
        options.create(true);
    }
    let file = options.open(path)?;
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
    {
        return Err("native database file must be owned and private".into());
    }
    Ok(file)
}

pub(crate) fn write_bytes(path: &Path, bytes: &[u8]) -> io::Result<()> {
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
            .ok_or_else(|| io::Error::other("native record parent missing"))?,
    )?
    .sync_all()
}
pub(crate) fn read_bytes(path: &Path, limit: usize) -> io::Result<Vec<u8>> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != unsafe { libc::geteuid() }
        || meta.mode() & 0o077 != 0
        || meta.len() > limit as u64
    {
        return Err(io::Error::other("native record is not private and bounded"));
    }
    let mut bytes = Vec::new();
    file.take(limit as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != meta.len() {
        return Err(io::Error::other("native record changed"));
    }
    Ok(bytes)
}

/// Publish one immutable native record. A pending marker fences interrupted
/// publication; an existing destination is never replaced.
pub(crate) fn publish_bytes(path: &Path, bytes: &[u8]) -> io::Result<()> {
    let parent = path
        .parent()
        .ok_or_else(|| io::Error::other("native record parent missing"))?;
    let name = path
        .file_name()
        .and_then(|s| s.to_str())
        .ok_or_else(|| io::Error::other("native record name invalid"))?;
    let pending = path.with_file_name(format!("{name}.pending"));
    write_bytes(&pending, bytes)?;
    fs::hard_link(&pending, path)?;
    File::open(parent)?.sync_all()?;
    fs::remove_file(pending)?;
    File::open(parent)?.sync_all()
}

/// Publish one immutable run record under caller-named `pending` and
/// `committed` paths in `root`. Unlike `publish_bytes`, `root` is not synced
/// between writing `pending` and linking it.
pub(crate) fn publish_linked(
    root: &Path,
    pending: &Path,
    committed: &Path,
    parts: &[&[u8]],
) -> io::Result<()> {
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(pending)?;
    for part in parts {
        file.write_all(part)?;
    }
    file.sync_all()?;
    fs::hard_link(pending, committed)?;
    File::open(root)?.sync_all()?;
    fs::remove_file(pending)?;
    File::open(root)?.sync_all()
}

/// Renames `from` to `to`, failing with `AlreadyExists` instead of replacing
/// an existing target.
pub(crate) fn rename_noreplace(from: &Path, to: &Path) -> io::Result<()> {
    use std::os::unix::ffi::OsStrExt;
    let invalid = || io::Error::from(io::ErrorKind::InvalidInput);
    let from = std::ffi::CString::new(from.as_os_str().as_bytes()).map_err(|_| invalid())?;
    let to = std::ffi::CString::new(to.as_os_str().as_bytes()).map_err(|_| invalid())?;
    #[cfg(target_os = "linux")]
    let result = unsafe {
        libc::renameat2(
            libc::AT_FDCWD,
            from.as_ptr(),
            libc::AT_FDCWD,
            to.as_ptr(),
            libc::RENAME_NOREPLACE,
        )
    };
    #[cfg(target_os = "macos")]
    let result = unsafe { libc::renamex_np(from.as_ptr(), to.as_ptr(), libc::RENAME_EXCL) };
    if result == 0 {
        Ok(())
    } else {
        Err(io::Error::last_os_error())
    }
}
