//! Conservative allocated-block bound for all retained preparation trees.
//! Completed trees are frozen. Active trees reserve their full service memory
//! ceiling and are never traversed while their preparer can mutate them.
use std::fs;
use std::io;
use std::os::unix::fs::MetadataExt;
use std::path::Path;

pub(crate) const MAX_RETAINED_BYTES: u64 = 4 * 1024 * 1024 * 1024;
// Keep aligned with preparer@.service MemoryMax on the /run tmpfs.
pub(crate) const MAX_ACTIVE_BYTES: u64 = 1024 * 1024 * 1024;
const MAX_ENTRIES: usize = 1_000_000;
const MAX_DEPTH: usize = 128;

fn denied(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

/// Refuse an additional worst-case charge before launching a preparer, and
/// recheck the actual allocated blocks after it stops. Hard links are charged
/// once per directory entry, which deliberately overcounts shared blocks.
/// Returns active reservations, also needed for the shared /run free-space gate.
pub(crate) fn capacity(root: &Path, additional: u64) -> io::Result<u64> {
    let parent = root.symlink_metadata()?;
    if !parent.is_dir() {
        return Err(denied("preparation storage root invalid"));
    }
    let device = parent.dev();
    let mut total = 0u64;
    let mut reserved = 0u64;
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
        let active = if depth == 1 && metadata.is_dir() {
            match path.join("output").symlink_metadata() {
                Ok(output) => {
                    if !output.is_dir() || !matches!(output.mode() & 0o7777, 0o555 | 0o777) {
                        return Err(denied("preparation output state invalid"));
                    }
                    output.mode() & 0o7777 == 0o777
                }
                Err(error) if error.kind() == io::ErrorKind::NotFound => false,
                Err(error) => return Err(error),
            }
        } else {
            false
        };
        let allocated = if active {
            reserved = reserved
                .checked_add(MAX_ACTIVE_BYTES)
                .ok_or_else(|| denied("preparation reservation overflow"))?;
            MAX_ACTIVE_BYTES
        } else {
            metadata
                .blocks()
                .checked_mul(512)
                .ok_or_else(|| denied("preparation storage size invalid"))?
        };
        total = total
            .checked_add(allocated)
            .ok_or_else(|| denied("preparation storage size invalid"))?;
        if total
            .checked_add(additional)
            .is_none_or(|needed| needed > MAX_RETAINED_BYTES)
        {
            return Err(denied("retained preparation storage bound exceeded"));
        }
        if metadata.is_dir() && !active {
            for entry in fs::read_dir(path)? {
                if stack.len() >= MAX_ENTRIES - entries {
                    return Err(denied("preparation storage entry bound exceeded"));
                }
                stack.push((entry?.path(), depth + 1));
            }
        }
    }
    Ok(reserved)
}
