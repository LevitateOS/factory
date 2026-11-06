//! Capture a committed or bounded dirty coder result from a retired agent tree.
//! The caller supplies its bounded Git process owner and a worker-private path.
use sha2::{Digest, Sha256};
use std::ffi::OsStr;
use std::fs::{self, OpenOptions};
use std::io::{self, Read};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Component, Path};
use std::time::Duration;

pub(crate) use soda_execution_protocol::MAX_CANDIDATE_BUNDLE as MAX_BUNDLE;
const MAX_DIRTY_BYTES: u64 = 32 * 1024 * 1024;
const MAX_STATUS: usize = 4096;
const MAX_DIRTY_PATHS: usize = 512;

pub(crate) enum Candidate {
    NotApplicable,
    Unchanged,
    Dirty,
    Captured {
        commit: String,
        bundle: Vec<u8>,
        sha256: String,
    },
}

#[derive(PartialEq, Eq)]
pub(crate) struct RepositoryIdentity {
    work_device: u64,
    work_inode: u64,
    git_device: u64,
    git_inode: u64,
    config_sha256: [u8; 32],
    exclude_sha256: Option<[u8; 32]>,
    attributes_sha256: Option<[u8; 32]>,
    alternates_sha256: Option<[u8; 32]>,
}

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn oid(bytes: &[u8]) -> io::Result<String> {
    let value = bytes
        .strip_suffix(b"\r\n")
        .or_else(|| bytes.strip_suffix(b"\n"))
        .ok_or_else(|| refused("candidate commit line invalid"))?;
    if value.len() != 40 || !value.iter().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(refused("candidate commit identity invalid"));
    }
    std::str::from_utf8(value)
        .map(str::to_owned)
        .map_err(|_| refused("candidate commit encoding invalid"))
}

fn bounded_dirty(workspace: &Path, status: &[u8]) -> io::Result<()> {
    if status.is_empty() || status.len() > MAX_STATUS || !status.ends_with(&[0]) {
        return Err(refused("candidate dirty inventory bound invalid"));
    }
    let canonical_workspace = fs::canonicalize(workspace)?;
    if canonical_workspace != workspace {
        return Err(refused("candidate workspace path changed"));
    }
    let mut entries = status[..status.len() - 1].split(|byte| *byte == 0);
    let mut total = 0u64;
    let mut count = 0usize;
    while let Some(entry) = entries.next() {
        if entry.len() < 4 || entry[2] != b' ' {
            return Err(refused("candidate dirty inventory invalid"));
        }
        count += 1;
        if count > MAX_DIRTY_PATHS {
            return Err(refused("candidate dirty path bound exceeded"));
        }
        let relative = Path::new(OsStr::from_bytes(&entry[3..]));
        if !relative
            .components()
            .all(|component| matches!(component, Component::Normal(_)))
        {
            return Err(refused("candidate dirty path invalid"));
        }
        let metadata = match workspace.join(relative).symlink_metadata() {
            Ok(metadata) => metadata,
            Err(error)
                if error.kind() == io::ErrorKind::NotFound
                    && (entry[0] == b'D' || entry[1] == b'D') =>
            {
                if entry[0] == b'R' || entry[1] == b'R' || entry[0] == b'C' || entry[1] == b'C' {
                    entries
                        .next()
                        .ok_or_else(|| refused("candidate rename source missing"))?;
                }
                continue;
            }
            Err(error) => return Err(error),
        };
        let parent = workspace.join(
            relative
                .parent()
                .ok_or_else(|| refused("candidate dirty parent missing"))?,
        );
        // Git reports paths below ordinary directories too. Never follow a
        // parent symlink outside the captured workspace.
        if !fs::canonicalize(parent)?.starts_with(&canonical_workspace) {
            return Err(refused("candidate dirty path escaped workspace"));
        }
        if metadata.uid() != unsafe { libc::geteuid() }
            || !(metadata.is_file() || metadata.file_type().is_symlink())
        {
            return Err(refused("candidate dirty file identity invalid"));
        }
        let bytes = metadata.len().max(metadata.blocks().saturating_mul(512));
        total = total
            .checked_add(bytes)
            .ok_or_else(|| refused("candidate dirty size overflow"))?;
        if total > MAX_DIRTY_BYTES {
            return Err(refused("candidate dirty size bound exceeded"));
        }
        if entry[0] == b'R' || entry[1] == b'R' || entry[0] == b'C' || entry[1] == b'C' {
            entries
                .next()
                .ok_or_else(|| refused("candidate rename source missing"))?;
        }
    }
    Ok(())
}

fn private_digest(path: &Path, owner: u32, required: bool) -> io::Result<Option<[u8; 32]>> {
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)
    {
        Ok(file) => file,
        Err(error) if !required && error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != owner
        || metadata.mode() & 0o077 != 0
        || metadata.len() > 64 * 1024
    {
        return Err(refused("candidate Git metadata invalid"));
    }
    let mut bytes = Vec::new();
    file.take(64 * 1024 + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() {
        return Err(refused("candidate Git metadata changed"));
    }
    Ok(Some(Sha256::digest(&bytes).into()))
}

/// Capture the repository path and local Git configuration before the model
/// process can edit them. After tree retirement the same identity is required
/// before any Git command reads the workspace again.
pub(crate) fn repository_identity(workspace: &Path) -> io::Result<RepositoryIdentity> {
    let work = workspace.symlink_metadata()?;
    let git = workspace.join(".git").symlink_metadata()?;
    if !work.is_dir()
        || !git.is_dir()
        || work.uid() != unsafe { libc::geteuid() }
        || git.uid() != work.uid()
        || work.mode() & 0o077 != 0
        || git.mode() & 0o077 != 0
        || work.dev() != git.dev()
    {
        return Err(refused("candidate repository identity invalid"));
    }
    let config_sha256 = private_digest(&workspace.join(".git/config"), work.uid(), true)?
        .ok_or_else(|| refused("candidate Git configuration missing"))?;
    Ok(RepositoryIdentity {
        work_device: work.dev(),
        work_inode: work.ino(),
        git_device: git.dev(),
        git_inode: git.ino(),
        config_sha256,
        exclude_sha256: private_digest(&workspace.join(".git/info/exclude"), work.uid(), false)?,
        attributes_sha256: private_digest(
            &workspace.join(".git/info/attributes"),
            work.uid(),
            false,
        )?,
        alternates_sha256: private_digest(
            &workspace.join(".git/objects/info/alternates"),
            work.uid(),
            false,
        )?,
    })
}

/// The worker must have joined the agent child and all descendants first.
/// Dirty edits are checkpointed only after their bounded inventory is checked.
/// This is an internal native snapshot, not a product publication decision.
pub(crate) fn capture<F>(
    workspace: &Path,
    base: &str,
    destination: &Path,
    identity: &RepositoryIdentity,
    mut git: F,
) -> io::Result<Candidate>
where
    F: FnMut(&[&str], Duration) -> io::Result<Vec<u8>>,
{
    if base.len() != 40 || !base.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(refused("candidate base invalid"));
    }
    if &repository_identity(workspace)? != identity {
        return Err(refused("candidate repository changed during execution"));
    }
    let work = workspace
        .to_str()
        .ok_or_else(|| refused("candidate workspace encoding invalid"))?;
    let target = destination
        .to_str()
        .ok_or_else(|| refused("candidate bundle path encoding invalid"))?;
    if destination.symlink_metadata().is_ok() {
        return Err(refused("candidate bundle path already exists"));
    }
    let mut commit = oid(&git(
        &["-C", work, "rev-parse", "HEAD"],
        Duration::from_secs(3),
    )?)?;
    let status = git(
        &[
            "-C",
            work,
            "status",
            "--porcelain=v1",
            "-z",
            "--untracked-files=all",
        ],
        Duration::from_secs(5),
    )?;
    if !status.is_empty() {
        bounded_dirty(workspace, &status)?;
        git(
            &[
                "-C",
                work,
                "-c",
                "core.hooksPath=/dev/null",
                "add",
                "-A",
                "--",
                ".",
            ],
            Duration::from_secs(20),
        )?;
        let tree = oid(&git(&["-C", work, "write-tree"], Duration::from_secs(5))?)?;
        let checkpoint = oid(&git(
            &[
                "-C",
                work,
                "-c",
                "user.name=Factory Native Checkpoint",
                "-c",
                "user.email=factory@invalid.local",
                "-c",
                "commit.gpgsign=false",
                "-c",
                "core.hooksPath=/dev/null",
                "commit-tree",
                &tree,
                "-p",
                &commit,
                "-m",
                "Factory native checkpoint",
            ],
            Duration::from_secs(5),
        )?)?;
        git(
            &[
                "-C",
                work,
                "-c",
                "core.hooksPath=/dev/null",
                "update-ref",
                "HEAD",
                &checkpoint,
                &commit,
            ],
            Duration::from_secs(5),
        )?;
        commit = checkpoint;
        if !git(
            &[
                "-C",
                work,
                "status",
                "--porcelain=v1",
                "-z",
                "--untracked-files=all",
            ],
            Duration::from_secs(5),
        )?
        .is_empty()
        {
            return Ok(Candidate::Dirty);
        }
    }
    if commit == base {
        return Ok(Candidate::Unchanged);
    }
    git(
        &["-C", work, "merge-base", "--is-ancestor", base, &commit],
        Duration::from_secs(3),
    )?;
    let result: io::Result<Candidate> = (|| {
        git(
            &[
                "-C",
                work,
                "bundle",
                "create",
                target,
                "HEAD",
                &format!("^{base}"),
            ],
            Duration::from_secs(20),
        )?;
        git(
            &["-C", work, "bundle", "verify", target],
            Duration::from_secs(5),
        )?;
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(destination)?;
        let metadata = file.metadata()?;
        if !metadata.is_file()
            || metadata.uid() != unsafe { libc::geteuid() }
            || metadata.mode() & 0o077 != 0
            || !(1..=MAX_BUNDLE as u64).contains(&metadata.len())
        {
            return Err(refused("candidate bundle metadata or size invalid"));
        }
        let mut bytes = Vec::with_capacity(metadata.len() as usize);
        file.take(MAX_BUNDLE as u64 + 1).read_to_end(&mut bytes)?;
        if bytes.len() as u64 != metadata.len() {
            return Err(refused("candidate bundle changed during read"));
        }
        let sha256: String = Sha256::digest(&bytes)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        Ok(Candidate::Captured {
            commit,
            bundle: bytes,
            sha256,
        })
    })();
    let removed = fs::remove_file(destination);
    match (result, removed) {
        (Ok(candidate), Ok(())) => Ok(candidate),
        (Err(error), _) => Err(error),
        (_, Err(error)) => Err(error),
    }
}
