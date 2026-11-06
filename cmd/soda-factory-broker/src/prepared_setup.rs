//! Execute only the approved, root-owned entrypoints inside preparer@.service.
//! No provider account, desktop home, network grant or arbitrary command enters.
use super::{git, refused, CHECKOUT, MAX_CHECKOUT_BYTES, OUTPUT, SOURCE};
use soda_execution_protocol::{ApprovedFiles, SetupReceipt, MAX_SETUP_FRAME, MAX_SETUP_OUTPUT};
use std::collections::BTreeMap;
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Seek, SeekFrom, Write};
use std::os::fd::OwnedFd;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::net::UnixStream;
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
fn entry(name: &str, seconds: u32, output: &File) -> io::Result<(i32, bool)> {
    let (mut reader, writer) = UnixStream::pair()?;
    let mut retained = output.try_clone()?;
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
        .args(["-k", "1s"])
        .arg(format!("{seconds}s"))
        .args(["/usr/bin/bash", "--noprofile", "--norc", "-e", "-u"])
        .arg(format!("{SOURCE}/approved/{name}"))
        .stdin(Stdio::null())
        .stdout(Stdio::from(OwnedFd::from(writer.try_clone()?)))
        .stderr(Stdio::from(OwnedFd::from(writer)));
    // Build artifacts use the existing checkout envelope. Captured diagnostics
    // have their own smaller ceiling; they must not limit compiler output files.
    unsafe {
        command.pre_exec(|| {
            let limit = libc::rlimit {
                rlim_cur: MAX_CHECKOUT_BYTES as libc::rlim_t,
                rlim_max: MAX_CHECKOUT_BYTES as libc::rlim_t,
            };
            if libc::setrlimit(libc::RLIMIT_FSIZE, &limit) != 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let mut child = command.spawn()?;
    // Command retains its configured descriptors across spawn. Close those copies
    // so the collector can observe EOF when the script's process tree exits.
    drop(command);
    let capture = std::thread::spawn(move || -> io::Result<bool> {
        let mut used = retained.metadata()?.len() as usize;
        let mut bytes = [0u8; 8192];
        loop {
            let count = match reader.read(&mut bytes) {
                Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
                result => result?,
            };
            if count == 0 {
                retained.flush()?;
                return Ok(false);
            }
            let available = MAX_SETUP_OUTPUT.saturating_sub(used);
            retained.write_all(&bytes[..count.min(available)])?;
            if count > available {
                let marker = b"\n[Factory: setup diagnostic output limit exceeded]\n";
                let prefix = (MAX_SETUP_OUTPUT - marker.len()) as u64;
                retained.set_len(prefix)?;
                retained.seek(SeekFrom::Start(prefix))?;
                retained.write_all(marker)?;
                retained.flush()?;
                return Ok(true);
            }
            used += count;
        }
    });
    let status = child.wait();
    let truncated = capture
        .join()
        .map_err(|_| io::Error::other("setup output collector failed"))??;
    let status = status?;
    Ok((
        status
            .code()
            .unwrap_or_else(|| 128 + status.signal().unwrap_or(0)),
        truncated,
    ))
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
    let limits = approved
        .limits()
        .ok_or_else(|| refused("approved setup time limits invalid"))?;
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
        output_truncated: false,
    };
    if receipt.missing.is_empty() {
        let (setup, truncated) = entry("setup.sh", limits.setup_seconds, &output)?;
        receipt.setup_exit = Some(setup);
        receipt.output_truncated = truncated;
        if setup == 0 && !truncated {
            let (check, truncated) = entry("check.sh", limits.check_seconds, &output)?;
            receipt.check_exit = Some(check);
            receipt.output_truncated = truncated;
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
