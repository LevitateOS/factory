#![cfg(target_os = "linux")]
//! Unprivileged, fixed-path reviewer checkout preparation. The privileged
//! broker must select both mounts and the exact run/candidate/digest arguments.
use sha2::{Digest, Sha256};
use std::fs::{self, OpenOptions};
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Component, Path, PathBuf};
use std::process::{Command, Stdio};

const SOURCE: &str = "/run/factory-source";
const BUNDLE: &str = "/run/factory-source/bundle";
const OUTPUT: &str = "/run/factory-output";
const CHECKOUT: &str = "/run/factory-output/repo";
const MAX_BUNDLE: u64 = 512 * 1024;
const MAX_CHECKOUT_BYTES: u64 = 64 * 1024 * 1024;
const MAX_ENTRIES: usize = 20_000;
const MAX_DEPTH: usize = 64;

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn private_process() -> io::Result<()> {
    let uid = unsafe { libc::geteuid() };
    if uid == 0
        || unsafe { libc::getuid() } != uid
        || unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1
        || std::env::var_os("DBUS_SESSION_BUS_ADDRESS").is_some()
        || std::env::var_os("XDG_RUNTIME_DIR").is_some()
    {
        return Err(refused("preparer service identity invalid"));
    }
    Ok(())
}

fn directory(path: &Path, owner: u32, mode: u32) -> io::Result<()> {
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != owner || metadata.mode() & 0o7777 != mode {
        return Err(refused("preparer mount directory invalid"));
    }
    Ok(())
}

fn bundle(expected: &str) -> io::Result<()> {
    directory(Path::new(SOURCE), 0, 0o555)?;
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(BUNDLE)?;
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || !(1..=MAX_BUNDLE).contains(&metadata.len())
    {
        return Err(refused("preparer source bundle invalid"));
    }
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    file.take(MAX_BUNDLE + 1).read_to_end(&mut bytes)?;
    let digest: String = Sha256::digest(&bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    if bytes.len() as u64 != metadata.len() || digest != expected {
        return Err(refused("preparer source digest mismatch"));
    }
    Ok(())
}

fn git(args: &[&str]) -> Command {
    let mut command = Command::new("/usr/bin/git");
    command
        .env_clear()
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_LFS_SKIP_SMUDGE", "1")
        .args(args)
        .stderr(Stdio::null());
    command
}

fn contained_symlink(root: &Path, path: &Path, target: &Path) -> io::Result<()> {
    let parent = path
        .parent()
        .and_then(|parent| parent.strip_prefix(root).ok())
        .ok_or_else(|| refused("reviewer symlink parent invalid"))?;
    let mut depth = parent
        .components()
        .filter(|component| matches!(component, Component::Normal(_)))
        .count();
    for component in target.components() {
        match component {
            Component::Normal(_) => depth += 1,
            Component::CurDir => (),
            Component::ParentDir if depth > 0 => depth -= 1,
            Component::ParentDir | Component::RootDir | Component::Prefix(_) => {
                return Err(refused("reviewer symlink escapes checkout"));
            }
        }
    }
    Ok(())
}

fn check_tree(root: &Path) -> io::Result<()> {
    let canonical_root = fs::canonicalize(root)?;
    let mut total = 0u64;
    let mut entries = 0usize;
    let mut pending = vec![(root.to_path_buf(), 0usize)];
    while let Some((directory, depth)) = pending.pop() {
        if depth > MAX_DEPTH {
            return Err(refused("reviewer checkout depth exceeded"));
        }
        for entry in fs::read_dir(&directory)? {
            let entry = entry?;
            entries += 1;
            if entries > MAX_ENTRIES {
                return Err(refused("reviewer checkout entry bound exceeded"));
            }
            let path: PathBuf = entry.path();
            let metadata = path.symlink_metadata()?;
            if metadata.is_dir() {
                pending.push((path, depth + 1));
            } else if metadata.is_file() {
                total = total.saturating_add(metadata.len());
            } else if metadata.file_type().is_symlink() {
                let target = fs::read_link(&path)?;
                if target.as_os_str().len() > 4096 {
                    return Err(refused("reviewer checkout symlink bound exceeded"));
                }
                contained_symlink(root, &path, &target)?;
                // A lexically contained target can still leave the checkout
                // through another symlink in its path. Resolve the complete
                // chain while the private preparer owns the frozen input.
                // Dangling links are refused because their destination cannot
                // be proven to stay inside the reviewer mount.
                let resolved = fs::canonicalize(&path)?;
                if !resolved.starts_with(&canonical_root) {
                    return Err(refused("reviewer symlink resolves outside checkout"));
                }
                if !fs::metadata(&resolved)?.is_file() {
                    return Err(refused("reviewer directory symlink is not admitted"));
                }
                total = total.saturating_add(target.as_os_str().len() as u64);
            } else {
                return Err(refused("reviewer checkout has special file"));
            }
            if total > MAX_CHECKOUT_BYTES {
                return Err(refused("reviewer checkout byte bound exceeded"));
            }
        }
    }
    Ok(())
}

fn prepare() -> io::Result<()> {
    let args: Vec<String> = std::env::args_os()
        .map(|value| {
            value
                .into_string()
                .map_err(|_| refused("preparer argument encoding invalid"))
        })
        .collect::<io::Result<_>>()?;
    if args.len() != 4 || !hex(&args[1], 32) || !hex(&args[2], 40) || !hex(&args[3], 64) {
        return Err(refused("preparer run, candidate or digest invalid"));
    }
    private_process()?;
    bundle(&args[3])?;
    directory(Path::new(OUTPUT), 0, 0o777)?;
    if fs::read_dir(OUTPUT)?.next().is_some() {
        return Err(refused("preparer output is not empty"));
    }
    let clone = git(&["clone", "--quiet", "--no-local", "--", BUNDLE, CHECKOUT])
        .stdout(Stdio::null())
        .status()?;
    if !clone.success() {
        return Err(refused("reviewer bundle clone failed"));
    }
    let head = git(&["-C", CHECKOUT, "rev-parse", "--verify", "HEAD^{commit}"]).output()?;
    if !head.status.success() || head.stdout != format!("{}\n", args[2]).as_bytes() {
        return Err(refused("reviewer checkout HEAD differs"));
    }
    let clean = git(&["-C", CHECKOUT, "diff", "--quiet", "HEAD", "--"])
        .stdout(Stdio::null())
        .status()?;
    if !clean.success() {
        return Err(refused("reviewer checkout is dirty"));
    }
    check_tree(Path::new(CHECKOUT))?;
    println!("reviewer checkout prepared for run {}", args[1]);
    Ok(())
}

fn main() {
    if let Err(error) = prepare() {
        eprintln!("Factory reviewer preparation refused: {error}");
        std::process::exit(1);
    }
}
