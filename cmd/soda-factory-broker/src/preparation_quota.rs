//! Conservative allocated-block bound for all retained preparation trees.
//! The broker holds its run lock while a preparer runs; completed checkouts are
//! frozen before this walk and worker mounts expose them read-only.
use std::fs;
use std::io;
use std::os::unix::fs::MetadataExt;
use std::path::Path;

pub(crate) const MAX_RETAINED_BYTES: u64 = 512 * 1024 * 1024;
const MAX_ENTRIES: usize = 200_000;
const MAX_DEPTH: usize = 128;

fn denied(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

/// Refuse an additional worst-case charge before launching a preparer, and
/// recheck the actual allocated blocks after it stops. Hard links are charged
/// once per directory entry, which deliberately overcounts shared blocks.
pub(crate) fn capacity(root: &Path, additional: u64) -> io::Result<u64> {
    let parent = root.symlink_metadata()?;
    if !parent.is_dir() {
        return Err(denied("preparation storage root invalid"));
    }
    let device = parent.dev();
    let mut total = 0u64;
    let mut entries = 0usize;
    let mut stack = vec![(root.to_path_buf(), 0usize)];
    while let Some((path, depth)) = stack.pop() {
        if depth > MAX_DEPTH {
            return Err(denied("preparation storage depth exceeded"));
        }
        let metadata = path.symlink_metadata()?;
        if metadata.dev() != device
            || !(metadata.is_dir() || metadata.is_file() || metadata.file_type().is_symlink())
        {
            return Err(denied("preparation storage entry invalid"));
        }
        entries = entries
            .checked_add(1)
            .ok_or_else(|| denied("preparation storage entry bound exceeded"))?;
        if entries > MAX_ENTRIES {
            return Err(denied("preparation storage entry bound exceeded"));
        }
        let allocated = metadata
            .blocks()
            .checked_mul(512)
            .ok_or_else(|| denied("preparation storage size invalid"))?;
        total = total
            .checked_add(allocated)
            .ok_or_else(|| denied("preparation storage size invalid"))?;
        if total
            .checked_add(additional)
            .is_none_or(|needed| needed > MAX_RETAINED_BYTES)
        {
            return Err(denied("retained preparation storage bound exceeded"));
        }
        if metadata.is_dir() {
            for entry in fs::read_dir(path)? {
                if stack.len() >= MAX_ENTRIES - entries {
                    return Err(denied("preparation storage entry bound exceeded"));
                }
                stack.push((entry?.path(), depth + 1));
            }
        }
    }
    Ok(total)
}
