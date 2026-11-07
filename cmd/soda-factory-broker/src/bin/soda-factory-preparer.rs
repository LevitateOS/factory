#![cfg(target_os = "linux")]
//! Unprivileged, fixed-path reviewer checkout preparation. The privileged
//! broker must select both mounts and the exact run/candidate/digest arguments.
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::{Component, Path, PathBuf};
use std::process::{Command, Stdio};
#[path = "../prepared_candidate.rs"]
mod prepared_candidate;
#[path = "../prepared_context.rs"]
mod prepared_context;
#[path = "../prepared_setup.rs"]
mod prepared_setup;

const SOURCE: &str = "/run/factory-source";
const BUNDLE: &str = "/run/factory-source/bundle";
const OUTPUT: &str = "/run/factory-output";
const CHECKOUT: &str = "/run/factory-output/repo";
const MAX_BUNDLE: u64 = 512 * 1024;
const MAX_CHECKOUT_BYTES: u64 = soda_execution_protocol::MAX_PREPARED_TREE_BYTES;
const MAX_TREE_LIST_BYTES: u64 = 8 * 1024 * 1024;
const MAX_ATTRIBUTE_LIST_BYTES: u64 = 48 * 1024 * 1024;
const MAX_ENTRIES: usize = soda_execution_protocol::MAX_PREPARED_TREE_ENTRIES;
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
    let mut command = Command::new("/usr/bin/timeout");
    command
        .env_clear()
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_LFS_SKIP_SMUDGE", "1")
        .env("GIT_ATTR_NOSYSTEM", "1")
        .args(["-k", "1s", "20s", "/usr/bin/git"])
        .args([
            "-c",
            "core.autocrlf=false",
            "-c",
            "core.eol=lf",
            "-c",
            "core.attributesFile=/dev/null",
        ])
        .args(args)
        .stderr(Stdio::null());
    command
}

struct TreeEntry {
    size: u64,
    seen: u8,
    ident: bool,
    crlf: bool,
    encoding: bool,
}

fn attribute_bound(entries: &mut BTreeMap<Vec<u8>, TreeEntry>) -> io::Result<()> {
    let input_path = Path::new(CHECKOUT).join(".git/factory-attribute-paths");
    let result = (|| {
        let mut input = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&input_path)?;
        for path in entries.keys() {
            input.write_all(path)?;
            input.write_all(&[0])?;
        }
        input.flush()?;
        drop(input);
        let input = File::open(&input_path)?;
        let mut query = git(&[
            "-C",
            CHECKOUT,
            "check-attr",
            "--source=HEAD",
            "-z",
            "--stdin",
            "ident",
            "eol",
            "working-tree-encoding",
            "crlf",
        ])
        .stdin(Stdio::from(input))
        .stdout(Stdio::piped())
        .spawn()?;
        let mut output = Vec::new();
        let read = query
            .stdout
            .take()
            .ok_or_else(|| refused("preparer attribute query unavailable"))?
            .take(MAX_ATTRIBUTE_LIST_BYTES + 1)
            .read_to_end(&mut output);
        if read.is_err() || output.len() as u64 > MAX_ATTRIBUTE_LIST_BYTES {
            let _ = query.kill();
            let _ = query.wait();
            return Err(refused("reviewer attribute listing bound exceeded"));
        }
        if !query.wait()?.success() || (!output.is_empty() && !output.ends_with(&[0])) {
            return Err(refused("reviewer attribute listing invalid"));
        }
        let mut fields = output.split(|byte| *byte == 0);
        while let Some(path) = fields.next() {
            if path.is_empty() {
                if fields.next().is_some() {
                    return Err(refused("reviewer attribute record invalid"));
                }
                break;
            }
            let name = fields
                .next()
                .ok_or_else(|| refused("reviewer attribute record invalid"))?;
            let value = fields
                .next()
                .ok_or_else(|| refused("reviewer attribute record invalid"))?;
            let entry = entries
                .get_mut(path)
                .ok_or_else(|| refused("reviewer attribute path invalid"))?;
            let bit = match name {
                b"ident" => 1,
                b"eol" => 2,
                b"working-tree-encoding" => 4,
                b"crlf" => 8,
                _ => return Err(refused("reviewer attribute name invalid")),
            };
            if entry.seen & bit != 0 {
                return Err(refused("reviewer attribute repeated"));
            }
            entry.seen |= bit;
            let active = value != b"unspecified" && value != b"unset";
            match bit {
                1 => entry.ident = active,
                2 => {
                    if active && value != b"crlf" && value != b"lf" {
                        return Err(refused("reviewer line ending attribute invalid"));
                    }
                    entry.crlf = value == b"crlf";
                }
                4 if active => {
                    const ENCODINGS: &[&str] = &[
                        "UTF-16",
                        "UTF-16LE",
                        "UTF-16BE",
                        "UTF-16LE-BOM",
                        "UTF-16BE-BOM",
                        "UTF-32",
                        "UTF-32LE",
                        "UTF-32BE",
                    ];
                    if !ENCODINGS
                        .iter()
                        .any(|encoding| value.eq_ignore_ascii_case(encoding.as_bytes()))
                    {
                        return Err(refused("reviewer worktree encoding unsupported"));
                    }
                    entry.encoding = true;
                }
                8 => entry.crlf |= active,
                _ => (),
            }
        }
        let mut total = 0u64;
        for entry in entries.values() {
            if entry.seen != 0b1111 {
                return Err(refused("reviewer attribute record missing"));
            }
            let factor = (if entry.ident { 16u64 } else { 1 })
                * (if entry.crlf { 2 } else { 1 })
                * (if entry.encoding { 4 } else { 1 });
            let size = entry
                .size
                .checked_mul(factor)
                .and_then(|size| size.checked_add(if entry.encoding { 4 } else { 0 }))
                .ok_or_else(|| refused("reviewer expanded tree byte bound exceeded"))?;
            total = total
                .checked_add(size)
                .ok_or_else(|| refused("reviewer expanded tree byte bound exceeded"))?;
            if total > MAX_CHECKOUT_BYTES {
                return Err(refused("reviewer expanded tree byte bound exceeded"));
            }
        }
        Ok(())
    })();
    let removed = fs::remove_file(&input_path);
    result?;
    removed
}

fn preflight_tree() -> io::Result<()> {
    let mut listing = git(&["-C", CHECKOUT, "ls-tree", "-r", "-l", "-z", "HEAD"])
        .stdout(Stdio::piped())
        .spawn()?;
    let mut output = Vec::new();
    let read = listing
        .stdout
        .take()
        .ok_or_else(|| refused("preparer tree listing unavailable"))?
        .take(MAX_TREE_LIST_BYTES + 1)
        .read_to_end(&mut output);
    if read.is_err() || output.len() as u64 > MAX_TREE_LIST_BYTES {
        let _ = listing.kill();
        let _ = listing.wait();
        return Err(refused("reviewer tree listing bound exceeded"));
    }
    if !listing.wait()?.success() || (!output.is_empty() && !output.ends_with(&[0])) {
        return Err(refused("reviewer tree listing invalid"));
    }
    let mut total = 0u64;
    let mut entries = 0usize;
    let mut paths = BTreeMap::new();
    let mut directories = BTreeSet::new();
    let records = if output.is_empty() {
        &output[..]
    } else {
        &output[..output.len() - 1]
    };
    for record in records.split(|byte| *byte == 0) {
        if record.is_empty() {
            if output.is_empty() {
                break;
            }
            return Err(refused("reviewer tree record empty"));
        }
        entries += 1;
        if entries > MAX_ENTRIES {
            return Err(refused("reviewer tree entry bound exceeded"));
        }
        let separator = record
            .iter()
            .position(|byte| *byte == b'\t')
            .ok_or_else(|| refused("reviewer tree record invalid"))?;
        let (header, rest) = record.split_at(separator);
        let path = &rest[1..];
        let fields: Vec<&[u8]> = header
            .split(|byte| byte.is_ascii_whitespace())
            .filter(|field| !field.is_empty())
            .collect();
        if fields.len() != 4
            || !matches!(fields[0], b"100644" | b"100755" | b"120000")
            || fields[1] != b"blob"
            || fields[2].len() != 40
            || !fields[2].iter().all(u8::is_ascii_hexdigit)
            || path.is_empty()
            || path.len() > 4096
        {
            return Err(refused("reviewer tree entry invalid"));
        }
        let mut depth = 0usize;
        for component in path.split(|byte| *byte == b'/') {
            depth += 1;
            if component.is_empty() || matches!(component, b"." | b".." | b".git") {
                return Err(refused("reviewer tree path invalid"));
            }
        }
        if depth > MAX_DEPTH {
            return Err(refused("reviewer tree depth exceeded"));
        }
        for (index, byte) in path.iter().enumerate() {
            if *byte == b'/' {
                directories.insert(path[..index].to_vec());
                if directories.len() + entries > MAX_ENTRIES {
                    return Err(refused("reviewer tree namespace bound exceeded"));
                }
            }
        }
        let size = std::str::from_utf8(fields[3])
            .ok()
            .and_then(|value| value.parse::<u64>().ok())
            .ok_or_else(|| refused("reviewer tree object size invalid"))?;
        total = total
            .checked_add(size)
            .ok_or_else(|| refused("reviewer tree byte bound exceeded"))?;
        if total > MAX_CHECKOUT_BYTES {
            return Err(refused("reviewer tree byte bound exceeded"));
        }
        if paths
            .insert(
                path.to_vec(),
                TreeEntry {
                    size,
                    seen: 0,
                    ident: false,
                    crlf: false,
                    encoding: false,
                },
            )
            .is_some()
        {
            return Err(refused("reviewer tree path repeated"));
        }
    }
    attribute_bound(&mut paths)
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
    let mut filesystem = std::mem::MaybeUninit::<libc::statfs>::uninit();
    if unsafe { libc::statfs(c"/run/factory-output".as_ptr(), filesystem.as_mut_ptr()) } != 0
        || unsafe { filesystem.assume_init() }.f_type != libc::TMPFS_MAGIC
    {
        return Err(refused("preparer output filesystem is not tmpfs"));
    }
    if fs::read_dir(OUTPUT)?.next().is_some() {
        return Err(refused("preparer output is not empty"));
    }
    let captured = prepared_setup::captured(&args[2])?;
    if captured.is_some() {
        for directory in ["repo", "home"] {
            let target = Path::new(OUTPUT).join(directory);
            fs::create_dir(&target)?;
            fs::set_permissions(&target, fs::Permissions::from_mode(0o700))?;
            soda_native_process::copy_snapshot(
                &Path::new("/run/factory-prepared").join(directory),
                &target,
                soda_execution_protocol::MAX_PREPARED_TREE_BYTES,
                soda_execution_protocol::MAX_PREPARED_TREE_ENTRIES,
            )?;
        }
    } else {
        let clone = git(&[
            "clone",
            "--quiet",
            "--no-local",
            "--no-checkout",
            "--",
            BUNDLE,
            CHECKOUT,
        ])
        .stdout(Stdio::null())
        .status()?;
        if !clone.success() {
            return Err(refused("reviewer bundle clone failed"));
        }
    }
    let mut head = git(&["-C", CHECKOUT, "rev-parse", "--verify", "HEAD^{commit}"])
        .stdout(Stdio::piped())
        .spawn()?;
    let mut output = Vec::with_capacity(42);
    let read = head
        .stdout
        .take()
        .ok_or_else(|| refused("preparer Git output unavailable"))?
        .take(42)
        .read_to_end(&mut output);
    if read.is_err() || output.len() > 41 {
        let _ = head.kill();
        let _ = head.wait();
        return Err(refused("preparer Git output invalid"));
    }
    let status = head.wait()?;
    if !status.success() || output != format!("{}\n", args[2]).as_bytes() {
        return Err(refused("reviewer checkout HEAD differs"));
    }
    if captured.is_none() {
        preflight_tree()?;
        let checkout = git(&["-C", CHECKOUT, "reset", "--hard", "--quiet", "HEAD"])
            .stdout(Stdio::null())
            .status()?;
        if !checkout.success() {
            return Err(refused("reviewer tree checkout failed"));
        }
        let clean = git(&["-C", CHECKOUT, "diff", "--quiet", "HEAD", "--"])
            .stdout(Stdio::null())
            .status()?;
        if !clean.success() {
            return Err(refused("reviewer checkout is dirty"));
        }
    }
    check_tree(Path::new(CHECKOUT))?;
    prepared_setup::run(&args[2], captured.as_ref())?;
    check_tree(Path::new(CHECKOUT))?;
    println!("reviewer checkout prepared for run {}", args[1]);
    Ok(())
}

fn main() {
    let result = if std::env::args().nth(1).as_deref() == Some("context") {
        prepared_context::run()
    } else {
        prepare()
    };
    if let Err(error) = result {
        eprintln!("Factory reviewer preparation refused: {error}");
        std::process::exit(1);
    }
}
