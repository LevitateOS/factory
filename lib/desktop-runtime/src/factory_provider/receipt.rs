//! Read a retained native run observation without relaunching its worker.
//! The receipt is evidence for the Go ledger, never execution admission or
//! proof that the broker, credential and worker lifetimes are retired.
use super::broker::{AgentProcess, AgentRun};
use super::Role;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::fs::{self, OpenOptions};
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::Path;

const MAX_HEADER: usize = 2048;
const MAX_OUTPUT: usize = 16 * 1024 * 1024;
const MAX_BUNDLE: usize = 4 * 1024 * 1024;
const MAX_FILE: u64 = 4 + MAX_HEADER as u64 + MAX_OUTPUT as u64 + MAX_BUNDLE as u64;

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn boot_id(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
}

fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn private_dir(path: &Path) -> io::Result<()> {
    let metadata = path.symlink_metadata()?;
    if !path.is_absolute()
        || !metadata.is_dir()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
        || fs::canonicalize(path)? != path
    {
        return Err(refused("native receipt directory identity invalid"));
    }
    Ok(())
}

fn profile(profile_dir: &Path, expected: &str) -> io::Result<()> {
    private_dir(profile_dir)?;
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(profile_dir.join("profile.id"))?;
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
        || metadata.len() != 32
    {
        return Err(refused("native receipt profile identity invalid"));
    }
    let mut bytes = [0u8; 32];
    (&file).read_exact(&mut bytes)?;
    if bytes != expected.as_bytes() {
        return Err(refused("native receipt profile differs"));
    }
    Ok(())
}

/// Exact identity retained in the Go ledger before the native attempt.
/// Construct from that trusted record, never from a browser request.
pub struct ReceiptScope<'a> {
    run: &'a str,
    profile: &'a str,
    source_sha256: &'a str,
    candidate: &'a str,
    role: Role,
    tool_sha256: &'a str,
    prompt_sha256: &'a str,
}

impl<'a> ReceiptScope<'a> {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        run: &'a str,
        profile: &'a str,
        source_sha256: &'a str,
        candidate: &'a str,
        role: Role,
        tool_sha256: &'a str,
        prompt_sha256: &'a str,
    ) -> io::Result<Self> {
        if !hex(run, 32)
            || !hex(profile, 32)
            || !hex(source_sha256, 64)
            || !hex(candidate, 40)
            || !hex(tool_sha256, 64)
            || !hex(prompt_sha256, 64)
        {
            return Err(refused("native receipt scope invalid"));
        }
        Ok(Self {
            run,
            profile,
            source_sha256,
            candidate,
            role,
            tool_sha256,
            prompt_sha256,
        })
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Header {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    source_sha256: String,
    candidate: String,
    role: String,
    tool_sha256: String,
    prompt_sha256: String,
    desktop_uid: u32,
    worker_uid: u32,
    invocation: String,
    system_instance: String,
    start_identity: String,
    namespace_pid: u32,
    started_unix_ms: u64,
    reason: String,
    exit_code: Option<i32>,
    exit_signal: Option<i32>,
    output_complete: bool,
    output_length: usize,
    output_sha256: String,
    candidate_state: String,
    result_candidate: Option<String>,
    bundle_length: usize,
    bundle_sha256: Option<String>,
}

fn valid(header: &Header, scope: &ReceiptScope<'_>) -> bool {
    let role = match scope.role {
        Role::Reviewer => "reviewer",
        Role::Coder => "coder",
    };
    let candidate = if role == "reviewer" {
        header.candidate_state == "not_applicable"
            && header.result_candidate.is_none()
            && header.bundle_length == 0
            && header.bundle_sha256.is_none()
    } else if header.candidate_state == "captured" {
        header
            .result_candidate
            .as_deref()
            .is_some_and(|commit| hex(commit, 40) && commit != header.candidate)
            && (1..=MAX_BUNDLE).contains(&header.bundle_length)
            && header
                .bundle_sha256
                .as_deref()
                .is_some_and(|digest| hex(digest, 64))
    } else {
        matches!(
            header.candidate_state.as_str(),
            "unchanged" | "dirty" | "unavailable"
        ) && header.result_candidate.is_none()
            && header.bundle_length == 0
            && header.bundle_sha256.is_none()
    };
    header.kind == "native_agent_receipt"
        && header.version == 2
        && header.run == scope.run
        && header.profile == scope.profile
        && header.source_sha256 == scope.source_sha256
        && header.candidate == scope.candidate
        && header.role == role
        && header.tool_sha256 == scope.tool_sha256
        && header.prompt_sha256 == scope.prompt_sha256
        && header.desktop_uid == unsafe { libc::geteuid() }
        && header.worker_uid != 0
        && header.worker_uid != header.desktop_uid
        && hex(&header.invocation, 32)
        && boot_id(&header.system_instance)
        && matches!(header.start_identity.parse::<u64>(), Ok(value) if value > 0)
        && header.namespace_pid != 0
        && header.started_unix_ms != 0
        && matches!(
            header.reason.as_str(),
            "exited"
                | "cancelled"
                | "deadline"
                | "output_limit"
                | "output_unavailable"
                | "input_unavailable"
                | "observation_unavailable"
                | "unconfirmed"
        )
        && header
            .exit_code
            .is_none_or(|code| (0..=255).contains(&code))
        && header
            .exit_signal
            .is_none_or(|signal| (1..=64).contains(&signal))
        && !(header.exit_code.is_some() && header.exit_signal.is_some())
        && (header.reason != "exited" || header.exit_code.is_some() || header.exit_signal.is_some())
        && header.output_length <= MAX_OUTPUT
        && hex(&header.output_sha256, 64)
        && candidate
}

/// Read a durable exact receipt after restart. `None` means no completed
/// receipt exists; an interrupted `.pending` file is an uncertain outcome.
/// This does not inspect or claim the broker's retirement state.
pub fn read_retained(profile_dir: &Path, scope: &ReceiptScope<'_>) -> io::Result<Option<AgentRun>> {
    profile(profile_dir, scope.profile)?;
    let receipts = profile_dir.join("native-run-receipts");
    match receipts.symlink_metadata() {
        Ok(_) => private_dir(&receipts)?,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    }
    let pending = receipts.join(format!("{}.pending", scope.run));
    let committed = receipts.join(format!("{}.receipt", scope.run));
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(committed)
    {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            return match pending.symlink_metadata() {
                Ok(_) => Err(refused("native receipt write outcome unconfirmed")),
                Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
                Err(error) => Err(error),
            };
        }
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o7777 != 0o600
        || !(4..=MAX_FILE).contains(&metadata.len())
    {
        return Err(refused("native receipt file identity or bound invalid"));
    }
    let mut file = file;
    let mut length = [0u8; 4];
    file.read_exact(&mut length)?;
    let length = u32::from_le_bytes(length) as usize;
    if length == 0 || length > MAX_HEADER {
        return Err(refused("native receipt header bound invalid"));
    }
    let mut bytes = vec![0u8; length];
    file.read_exact(&mut bytes)?;
    let header: Header = serde_json::from_slice(&bytes)
        .map_err(|_| refused("native receipt header shape invalid"))?;
    if !valid(&header, scope)
        || metadata.len()
            != 4 + length as u64 + header.output_length as u64 + header.bundle_length as u64
    {
        return Err(refused("native receipt identity or size invalid"));
    }
    let mut output = vec![0u8; header.output_length];
    let mut bundle = vec![0u8; header.bundle_length];
    file.read_exact(&mut output)?;
    file.read_exact(&mut bundle)?;
    if digest(&output) != header.output_sha256
        || header
            .bundle_sha256
            .as_deref()
            .is_some_and(|value| digest(&bundle) != value)
    {
        return Err(refused("native receipt content digest invalid"));
    }
    Ok(Some(AgentRun {
        worker_uid: header.worker_uid,
        process: AgentProcess {
            invocation: header.invocation,
            system_instance: header.system_instance,
            start_identity: header.start_identity,
            namespace_pid: header.namespace_pid,
            started_unix_ms: header.started_unix_ms,
        },
        reason: header.reason,
        exit_code: header.exit_code,
        exit_signal: header.exit_signal,
        output_complete: header.output_complete,
        output,
        candidate_state: header.candidate_state,
        result_candidate: header.result_candidate,
        candidate_bundle: bundle,
        bundle_sha256: header.bundle_sha256,
    }))
}
