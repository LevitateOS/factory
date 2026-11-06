//! Execute only the approved, root-owned entrypoints inside preparer@.service.
//! No provider account, desktop home, network grant or arbitrary command enters.
use super::{git, refused, CHECKOUT, OUTPUT, SOURCE};
use soda_execution_protocol::{ApprovedFiles, SetupReceipt, MAX_SETUP_FRAME, MAX_SETUP_OUTPUT};
use std::collections::BTreeMap;
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::process::{CommandExt, ExitStatusExt};
use std::path::Path;
use std::process::{Command, Stdio};

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
        return Err(refused("approved setup file metadata invalid"));
    }
    let mut bytes = Vec::new();
    file.take(limit as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != meta.len() {
        return Err(refused("approved setup file changed"));
    }
    Ok(bytes)
}
fn entry(name: &str, output: &File) -> io::Result<i32> {
    let mut command = Command::new("/usr/bin/timeout");
    command
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .env("LANG", "C.UTF-8")
        .env("HOME", format!("{OUTPUT}/home"))
        .env("TMPDIR", format!("{OUTPUT}/tmp"))
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_TERMINAL_PROMPT", "0")
        .current_dir(CHECKOUT)
        .args([
            "-k",
            "1s",
            "10s",
            "/usr/bin/bash",
            "--noprofile",
            "--norc",
            "-e",
            "-u",
        ])
        .arg(format!("{SOURCE}/approved/{name}"))
        .stdin(Stdio::null())
        .stdout(Stdio::from(output.try_clone()?))
        .stderr(Stdio::from(output.try_clone()?));
    // Apply the display-output ceiling to the script and its descendants, not
    // to Git's preceding source import. The unit bounds memory/tree lifetime.
    unsafe {
        command.pre_exec(|| {
            let limit = libc::rlimit {
                rlim_cur: MAX_SETUP_OUTPUT as libc::rlim_t,
                rlim_max: MAX_SETUP_OUTPUT as libc::rlim_t,
            };
            if libc::setrlimit(libc::RLIMIT_FSIZE, &limit) != 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let status = command.status()?;
    Ok(status
        .code()
        .unwrap_or_else(|| 128 + status.signal().unwrap_or(0)))
}
pub(super) fn run(candidate: &str) -> io::Result<()> {
    let input = Path::new(SOURCE).join("approved.json");
    match input.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
        Ok(_) => (),
    }
    let bytes = root_file(&input, MAX_SETUP_FRAME)?;
    let approved: ApprovedFiles =
        serde_json::from_slice(&bytes).map_err(|_| refused("approved setup shape invalid"))?;
    if !approved.valid()
        || root_file(&Path::new(SOURCE).join("owner.setup_sha256"), 65)?
            != format!("{}\n", approved.digest()).as_bytes()
    {
        return Err(refused("approved setup digest differs"));
    }
    for (name, expected) in &approved.files {
        if root_file(&Path::new(SOURCE).join("approved").join(name), 32768)? != *expected {
            return Err(refused("approved entrypoint differs"));
        }
    }
    if unsafe { libc::prctl(libc::PR_SET_DUMPABLE, 0, 0, 0, 0) } != 0 {
        return Err(io::Error::last_os_error());
    }
    for directory in ["home", "tmp"] {
        let path = Path::new(OUTPUT).join(directory);
        fs::create_dir(&path)?;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    }
    let mut tools = BTreeMap::new();
    let mut missing = Vec::new();
    for name in &approved.tools {
        let path = Path::new("/usr/bin").join(name);
        match path.canonicalize() {
            Ok(path)
                if path.starts_with("/usr")
                    && path
                        .metadata()
                        .is_ok_and(|meta| meta.is_file() && meta.mode() & 0o111 != 0) =>
            {
                tools.insert(name.clone(), path.to_string_lossy().into_owned());
            }
            _ => missing.push(name.clone()),
        }
    }
    let output_path = Path::new(OUTPUT).join("setup-output");
    let mut output = OpenOptions::new()
        .read(true)
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(&output_path)?;
    let mut receipt = SetupReceipt {
        worker_uid: unsafe { libc::geteuid() },
        setup_sha256: approved.digest(),
        source_commit: candidate.into(),
        setup_exit: None,
        check_exit: None,
        tools,
        missing,
        output: String::new(),
    };
    if receipt.missing.is_empty() {
        let setup = entry("setup.sh", &output)?;
        receipt.setup_exit = Some(setup);
        if setup == 0 {
            receipt.check_exit = Some(entry("check.sh", &output)?);
        }
    }
    output.flush()?;
    drop(output);
    let mut bytes = Vec::new();
    File::open(output_path)?
        .take(MAX_SETUP_OUTPUT as u64 + 1)
        .read_to_end(&mut bytes)?;
    if bytes.len() > MAX_SETUP_OUTPUT {
        return Err(refused("approved setup output exceeds bound"));
    }
    receipt.output =
        String::from_utf8(bytes).map_err(|_| refused("approved setup output is not text"))?;
    // The broker has no DAC override capability. Publish completed helper
    // directories for its metadata walk inside the root-private staging area.
    let mut pending = vec![
        (Path::new(CHECKOUT).to_path_buf(), 0usize),
        (Path::new(OUTPUT).join("home"), 0usize),
        (Path::new(OUTPUT).join("tmp"), 0usize),
    ];
    let mut entries = 0;
    while let Some((path, depth)) = pending.pop() {
        entries += 1;
        if entries > 20000 || depth > 64 {
            return Err(refused("setup private output exceeds bounds"));
        }
        let meta = path.symlink_metadata()?;
        if meta.is_dir() {
            if meta.uid() != unsafe { libc::geteuid() } {
                return Err(refused("setup output directory owner differs"));
            }
            fs::set_permissions(&path, fs::Permissions::from_mode(0o755))?;
            for entry in fs::read_dir(path)? {
                pending.push((entry?.path(), depth + 1));
            }
        } else if meta.is_file() {
            if meta.uid() != unsafe { libc::geteuid() } {
                return Err(refused("setup output file owner differs"));
            }
            fs::set_permissions(
                &path,
                fs::Permissions::from_mode((meta.mode() & 0o777) | 0o444),
            )?;
        } else if !meta.file_type().is_symlink() {
            return Err(refused("setup output has special files"));
        }
    }
    if !git(&[
        "-C",
        CHECKOUT,
        "-c",
        "core.fsmonitor=false",
        "-c",
        "core.hooksPath=/dev/null",
        "diff",
        "--no-ext-diff",
        "--no-textconv",
        "--quiet",
        "HEAD",
        "--",
    ])
    .stdout(Stdio::null())
    .status()?
    .success()
    {
        return Err(refused("approved setup changed protected source"));
    }
    let mut proof = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o644)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(Path::new(OUTPUT).join("setup-receipt.json"))?;
    proof.write_all(&serde_json::to_vec(&receipt)?)?;
    proof.sync_all()?;
    File::open(OUTPUT)?.sync_all()
}
