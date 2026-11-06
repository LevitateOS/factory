use std::error::Error;
use std::fs::{self, DirBuilder, File, OpenOptions};
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
