//! Transfer the credential-free prepared snapshot, then check its actual use.
use super::{
    refused, Executable, Invocation, Limits, Processes, StopReason, PRIVATE, PRIVATE_HOME,
    PRIVATE_TMP, SOURCE,
};
use soda_execution_protocol::{ApprovedFiles, MAX_SETUP_FRAME, MAX_SETUP_OUTPUT};
use std::collections::BTreeMap;
use std::ffi::OsString;
use std::fs::{self, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{symlink, MetadataExt, OpenOptionsExt, PermissionsExt};
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
    let root = source.symlink_metadata()?;
    if !root.is_dir() || root.uid() == 0 || !super::readonly(source)? {
        return Err(refused("worker prepared snapshot is not read-only"));
    }
    let canonical = source.canonicalize()?;
    let mut pending = vec![(source.to_path_buf(), target.to_path_buf(), 0usize)];
    let mut total = 0u64;
    let mut entries = 0;
    while let Some((from, to, depth)) = pending.pop() {
        if entries > 20000 || depth > 64 {
            return Err(refused("worker environment inventory exceeds bounds"));
        }
        for entry in fs::read_dir(from)? {
            entries += 1;
            if entries > 20000 {
                return Err(refused("worker environment inventory exceeds bounds"));
            }
            let entry = entry?;
            let from = entry.path();
            let to = to.join(entry.file_name());
            let meta = from.symlink_metadata()?;
            if meta.is_dir() {
                fs::create_dir(&to)?;
                fs::set_permissions(&to, fs::Permissions::from_mode(0o700))?;
                pending.push((from, to, depth + 1));
            } else if meta.is_file() {
                total = total
                    .checked_add(meta.len())
                    .ok_or_else(|| refused("worker environment size invalid"))?;
                if total > 64 * 1024 * 1024 {
                    return Err(refused("worker environment byte bound exceeded"));
                }
                let file = OpenOptions::new()
                    .read(true)
                    .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                    .open(&from)?;
                let mut output = OpenOptions::new()
                    .write(true)
                    .create_new(true)
                    .mode(0o600 | (meta.mode() & 0o111))
                    .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                    .open(&to)?;
                if io::copy(&mut file.take(meta.len() + 1), &mut output)? != meta.len() {
                    return Err(refused("worker environment file changed"));
                }
                output.flush()?;
            } else if meta.file_type().is_symlink() {
                let link = fs::read_link(&from)?;
                if link.is_absolute() || !from.canonicalize()?.starts_with(&canonical) {
                    return Err(refused("worker environment link escapes snapshot"));
                }
                symlink(link, to)?;
            } else {
                return Err(refused("worker environment has special file"));
            }
        }
    }
    Ok(())
}
pub(super) fn check(processes: &mut Processes, workspace: &Path) -> io::Result<()> {
    copy(
        Path::new("/run/factory-prepared/home"),
        Path::new(PRIVATE_HOME),
    )?;
    let executable = Executable::capture(Path::new("/usr/bin/bash"))
        .map_err(|_| refused("worker check interpreter unavailable"))?;
    let environment = BTreeMap::from([
        ("PATH", "/usr/bin:/bin"),
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
    let duration = Duration::from_secs(10);
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
                memory_bytes: 256 * 1024 * 1024,
                processes: 32,
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
