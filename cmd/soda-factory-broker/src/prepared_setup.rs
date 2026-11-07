//! Execute only the approved, root-owned entrypoints inside preparer@.service.
//! Only reviewed setup may use public HTTPS; no provider account or desktop HOME enters.
use super::{git, refused, CHECKOUT, MAX_CHECKOUT_BYTES, OUTPUT, SOURCE};
use soda_execution_protocol::{
    ApprovedFiles, CapturedSetup, SetupReceipt, MAX_SETUP_FRAME, MAX_SETUP_OUTPUT,
};
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
fn entry(name: &str, seconds: u32, output: &File, network: bool) -> io::Result<(i32, bool)> {
    let (mut reader, writer) = UnixStream::pair()?;
    let mut retained = output.try_clone()?;
    let mut command = Command::new("/usr/bin/timeout");
    command
        .env_clear()
        .env(
            "PATH",
            soda_execution_protocol::setup_path(&format!("{OUTPUT}/home")),
        )
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
    if network {
        for name in ["HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"] {
            command.env(name, "http://127.0.0.1:37377");
        }
        command.env("NO_PROXY", "").env("no_proxy", "");
    }
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
fn resolve_tools(approved: &ApprovedFiles) -> (BTreeMap<String, String>, Vec<String>) {
    let home = Path::new(OUTPUT).join("home");
    let search = soda_execution_protocol::setup_path(&home.to_string_lossy());
    let mut tools = BTreeMap::new();
    let mut missing = Vec::new();
    for name in &approved.tools {
        let resolved = std::env::split_paths(&search).find_map(|directory| {
            let path = directory.join(name).canonicalize().ok()?;
            ((path.starts_with("/usr") || path.starts_with(&home))
                && path
                    .metadata()
                    .is_ok_and(|meta| meta.is_file() && meta.mode() & 0o111 != 0))
            .then_some(path)
        });
        match resolved {
            Some(path) => {
                tools.insert(name.clone(), path.to_string_lossy().into_owned());
            }
            None => missing.push(name.clone()),
        }
    }
    (tools, missing)
}

pub(super) fn captured(candidate: &str) -> io::Result<Option<CapturedSetup>> {
    let path = Path::new(SOURCE).join("owner.prepared.json");
    if !path.try_exists()? {
        return Ok(None);
    }
    let value: CapturedSetup = serde_json::from_slice(&root_file(&path, 2 * 1024 * 1024)?)
        .map_err(|_| refused("captured setup descriptor invalid"))?;
    let approved: ApprovedFiles = serde_json::from_slice(&root_file(
        &Path::new(SOURCE).join("approved.json"),
        MAX_SETUP_FRAME,
    )?)
    .map_err(|_| refused("captured approved inputs invalid"))?;
    if !approved.valid()
        || value.source.candidate != candidate
        || value.receipt.source_commit != candidate
        || value.receipt.setup_sha256 != approved.digest()
        || value.receipt.prepared_source.is_some()
        || value.receipt.setup_exit != Some(0)
        || value.receipt.check_exit != Some(0)
        || value.receipt.output_truncated
        || !value.receipt.missing.is_empty()
    {
        return Err(refused("captured setup is not ready for this source"));
    }
    Ok(Some(value))
}
pub(super) fn run(candidate: &str, captured: Option<&CapturedSetup>) -> io::Result<()> {
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
        if directory == "home" && captured.is_some() {
            continue;
        }
        let path = Path::new(OUTPUT).join(directory);
        fs::create_dir(&path)?;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
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
        prepared_source: captured.map(|value| value.source.clone()),
        worker_uid: unsafe { libc::geteuid() },
        setup_sha256: approved.digest(),
        source_commit: candidate.into(),
        setup_exit: None,
        check_exit: None,
        tools: BTreeMap::new(),
        missing: Vec::new(),
        output: String::new(),
        output_truncated: false,
    };
    let network = captured.is_none() && approved.public_https() == Some(true);
    let (setup, truncated) = if captured.is_some() {
        (0, false)
    } else {
        entry("setup.sh", limits.setup_seconds, &output, network)?
    };
    receipt.setup_exit = Some(setup);
    receipt.output_truncated = truncated;
    (receipt.tools, receipt.missing) = resolve_tools(&approved);
    if setup == 0 && !truncated && receipt.missing.is_empty() {
        let (check, truncated) = entry("check.sh", limits.check_seconds, &output, network)?;
        receipt.check_exit = Some(check);
        receipt.output_truncated = truncated;
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
        if entries > soda_execution_protocol::MAX_PREPARED_TREE_ENTRIES || depth > 64 {
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
        } else if meta.file_type().is_symlink() {
            let link = fs::read_link(&path)?;
            if link.is_absolute() {
                // Package managers may write absolute cache links within HOME.
                // Make those portable before the snapshot changes mount path.
                let home = Path::new(OUTPUT).join("home");
                let parent = path
                    .parent()
                    .and_then(|parent| parent.strip_prefix(&home).ok())
                    .ok_or_else(|| refused("absolute setup link outside private HOME"))?;
                let resolved = path.canonicalize()?;
                let destination = resolved
                    .strip_prefix(&home)
                    .map_err(|_| refused("setup HOME link escapes snapshot"))?;
                let mut relative = std::path::PathBuf::new();
                for _ in parent.components() {
                    relative.push("..");
                }
                relative.push(destination);
                fs::remove_file(&path)?;
                std::os::unix::fs::symlink(relative, &path)?;
            }
        } else {
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
