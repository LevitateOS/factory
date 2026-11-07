//! Transfer the credential-free prepared snapshot, then check its actual use.
use super::{
    refused, Executable, Invocation, Limits, Processes, StopReason, PRIVATE, PRIVATE_HOME,
    PRIVATE_TMP, SOURCE,
};
use soda_execution_protocol::{
    ApprovedFiles, MAX_PREPARED_TREE_BYTES, MAX_PREPARED_TREE_ENTRIES, MAX_SETUP_FRAME,
    MAX_SETUP_OUTPUT,
};
use std::collections::BTreeMap;
use std::ffi::OsString;
use std::fs::OpenOptions;
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::Path;
use std::time::Duration;

fn root_file(path: &Path, limit: usize) -> io::Result<Vec<u8>> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != 0
        || meta.mode() & 0o7777 != 0o444
        || meta.len() > limit as u64
    {
        return Err(refused("worker approved input metadata invalid"));
    }
    let mut bytes = Vec::new();
    file.take(limit as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != meta.len() {
        return Err(refused("worker approved input changed"));
    }
    Ok(bytes)
}
pub(super) fn approved() -> io::Result<Option<ApprovedFiles>> {
    let root = Path::new("/run/factory-captured-inputs");
    if !root.join("owner.setup_sha256").try_exists()? {
        return Ok(None);
    }
    let input: ApprovedFiles =
        serde_json::from_slice(&root_file(&root.join("approved.json"), MAX_SETUP_FRAME)?)
            .map_err(|_| refused("worker approved inputs invalid"))?;
    if !input.valid()
        || root_file(&root.join("owner.setup_sha256"), 65)?
            != format!("{}\n", input.digest()).as_bytes()
    {
        return Err(refused("worker setup digest differs"));
    }
    for (name, expected) in &input.files {
        if root_file(&root.join("approved").join(name), 32768)? != *expected {
            return Err(refused("worker entrypoint differs"));
        }
    }
    Ok(Some(input))
}
pub(super) fn copy(source: &Path, target: &Path) -> io::Result<()> {
    soda_native_process::copy_snapshot(
        source,
        target,
        MAX_PREPARED_TREE_BYTES,
        MAX_PREPARED_TREE_ENTRIES,
    )
}
pub(super) fn check(
    processes: &mut Processes,
    workspace: &Path,
    approved: &ApprovedFiles,
) -> io::Result<()> {
    copy(
        Path::new("/run/factory-prepared/home"),
        Path::new(PRIVATE_HOME),
    )?;
    let executable = Executable::capture(Path::new("/usr/bin/bash"))
        .map_err(|_| refused("worker check interpreter unavailable"))?;
    let tool_path = soda_execution_protocol::setup_path(PRIVATE_HOME);
    let environment = BTreeMap::from([
        ("PATH", tool_path.as_str()),
        ("LANG", "C.UTF-8"),
        ("HOME", PRIVATE_HOME),
        ("TMPDIR", PRIVATE_TMP),
        ("GIT_CONFIG_NOSYSTEM", "1"),
        ("GIT_CONFIG_GLOBAL", "/dev/null"),
        ("GIT_TERMINAL_PROMPT", "0"),
        ("GIT_CONFIG_COUNT", "1"),
        ("GIT_CONFIG_KEY_0", "safe.directory"),
        ("GIT_CONFIG_VALUE_0", SOURCE),
    ])
    .into_iter()
    .map(|(key, value)| (OsString::from(key), OsString::from(value)))
    .collect();
    let duration = Duration::from_secs(
        approved
            .limits()
            .ok_or_else(|| refused("worker setup time limits invalid"))?
            .check_seconds as u64,
    );
    let process = processes
        .launch(Invocation {
            executable: &executable,
            directory: Path::new(PRIVATE),
            arguments: [
                "--noprofile",
                "--norc",
                "-e",
                "-u",
                "-c",
                "cd -- \"$1\"; exec /usr/bin/bash --noprofile --norc -e -u /run/factory-captured-inputs/approved/check.sh",
                "factory-environment-check",
            ]
            .map(OsString::from)
            .into_iter().chain([workspace.as_os_str().to_owned()]).collect(),
            environment,
            limits: Limits {
                duration,
                output_bytes: MAX_SETUP_OUTPUT,
                memory_bytes: 1024 * 1024 * 1024,
                processes: 128,
                cpu_percent: 100,
            },
            columns: 80,
            rows: 24,
        })
        .map_err(|_| refused("worker environment check unavailable"))?;
    let observed = process.wait(duration + Duration::from_secs(2));
    if !observed.finished
        || !observed.tree_retired
        || !observed.output_complete
        || observed.reason != Some(StopReason::Exited)
        || observed.exit.and_then(|exit| exit.code) != Some(0)
    {
        return Err(refused("worker environment check failed"));
    }
    Ok(())
}
