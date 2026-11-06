//! Combine a bounded incremental result with protected source in private tmpfs.
use super::{hex, prepared_context::query_at, refused};
use sha2::{Digest, Sha256};
use soda_execution_protocol::{PreparedSourceResult, MAX_STAGE_BUNDLE};
use std::fs::{self, OpenOptions};
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};

const PRIVATE: &str = "/run/factory-context-private";
const REPO: &str = "/run/factory-context-private/repo";
const DELTA: &str = "/run/factory-context-candidate.bundle";
const BUNDLE: &str = "/run/factory-context-private/source.bundle";

pub(super) fn run(
    base: &str,
    candidate: &str,
    delta_length: usize,
    delta_sha256: &str,
) -> io::Result<PreparedSourceResult> {
    if !hex(base, 40)
        || !hex(candidate, 40)
        || !hex(delta_sha256, 64)
        || !(1..=MAX_STAGE_BUNDLE).contains(&delta_length)
    {
        return Err(refused("candidate import input invalid"));
    }
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(DELTA)?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != 0
        || meta.mode() & 0o7777 != 0o444
        || meta.len() != delta_length as u64
    {
        return Err(refused("candidate import metadata invalid"));
    }
    let mut bytes = Vec::new();
    file.take(MAX_STAGE_BUNDLE as u64 + 1)
        .read_to_end(&mut bytes)?;
    let hash = |bytes: &[u8]| {
        Sha256::digest(bytes)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>()
    };
    if bytes.len() != delta_length || hash(&bytes) != delta_sha256 {
        return Err(refused("candidate import digest differs"));
    }
    drop(bytes);
    let mut filesystem = std::mem::MaybeUninit::<libc::statfs>::uninit();
    if unsafe {
        libc::statfs(
            c"/run/factory-context-private".as_ptr(),
            filesystem.as_mut_ptr(),
        )
    } != 0
        || unsafe { filesystem.assume_init() }.f_type != libc::TMPFS_MAGIC
        || fs::read_dir(PRIVATE)?.next().is_some()
    {
        return Err(refused("candidate import private filesystem invalid"));
    }
    query_at(
        PRIVATE,
        &[
            "clone",
            "--quiet",
            "--no-local",
            "--no-checkout",
            "--",
            "/run/factory-context-base.bundle",
            REPO,
        ],
        0,
    )?;
    if query_at(REPO, &["rev-parse", "--verify", "HEAD^{commit}"], 41)?
        != format!("{base}\n").as_bytes()
    {
        return Err(refused("candidate base differs"));
    }
    query_at(
        REPO,
        &[
            "fetch",
            "--quiet",
            "--no-tags",
            "--no-write-fetch-head",
            DELTA,
            "HEAD:refs/heads/factory-candidate",
        ],
        0,
    )?;
    if query_at(
        REPO,
        &[
            "rev-parse",
            "--verify",
            "refs/heads/factory-candidate^{commit}",
        ],
        41,
    )? != format!("{candidate}\n").as_bytes()
    {
        return Err(refused("candidate imported head differs"));
    }
    query_at(REPO, &["merge-base", "--is-ancestor", base, candidate], 0)?;
    query_at(
        REPO,
        &["symbolic-ref", "HEAD", "refs/heads/factory-candidate"],
        0,
    )?;
    query_at(REPO, &["bundle", "create", BUNDLE, "HEAD"], 0)?;
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(BUNDLE)?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != unsafe { libc::geteuid() }
        || !(1..=MAX_STAGE_BUNDLE as u64).contains(&meta.len())
    {
        return Err(refused("complete candidate source exceeds bounds"));
    }
    let mut bundle = Vec::new();
    file.take(MAX_STAGE_BUNDLE as u64 + 1)
        .read_to_end(&mut bundle)?;
    if bundle.len() as u64 != meta.len() {
        return Err(refused("complete candidate source changed"));
    }
    Ok(PreparedSourceResult::Candidate {
        source_commit: candidate.into(),
        sha256: hash(&bundle),
        bundle,
    })
}
