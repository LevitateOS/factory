#![cfg(target_os = "linux")]
//! Stages bounded, credential-free Git bundle bytes and asks fixed unprivileged
//! units to prepare reviewer checkouts. This is not agent execution admission.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, RawFd};
use std::os::unix::fs::{DirBuilderExt, FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

const SOCKET: &str = "/run/soda-factory/broker.sock";
const STATE: &str = "/run/soda-factory-broker";
const MAX_REQUEST: usize = 256;
const MAX_BUNDLE: usize = 512 * 1024;
const MAX_RUNS: usize = 64;

#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Request {
    StageBundle {
        version: u8,
        run: String,
        length: usize,
        sha256: String,
    },
    RetireBundle {
        version: u8,
        run: String,
        sha256: String,
    },
    PrepareReviewer {
        version: u8,
        run: String,
        sha256: String,
        candidate: String,
    },
}

#[derive(Serialize)]
struct Staged<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    sha256: &'a str,
}

#[derive(Serialize)]
struct Retired<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    removed: bool,
}

#[derive(Serialize)]
struct Prepared<'a> {
    kind: &'static str,
    version: u8,
    run: &'a str,
    sha256: &'a str,
    candidate: &'a str,
}

fn denied(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn id(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn private_dir(path: &Path, mode: u32) -> io::Result<()> {
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.file_type().is_symlink()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != mode
    {
        return Err(denied("broker state permissions invalid"));
    }
    Ok(())
}

fn socket(fd: RawFd) -> io::Result<()> {
    if unsafe { libc::geteuid() } != 0 || unsafe { libc::getuid() } != 0 {
        return Err(denied("broker requires root-owned service"));
    }
    if unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1 {
        return Err(denied("broker can gain privileges"));
    }
    let parent = Path::new(SOCKET).parent().expect("fixed socket path");
    let parent_meta = parent.symlink_metadata()?;
    if !parent_meta.is_dir() || parent_meta.uid() != 0 || parent_meta.mode() & 0o7777 != 0o755 {
        return Err(denied("broker socket parent invalid"));
    }
    let socket_meta = Path::new(SOCKET).symlink_metadata()?;
    if !socket_meta.file_type().is_socket() || socket_meta.mode() & 0o7777 != 0o600 {
        return Err(denied("broker socket permissions invalid"));
    }
    let mut kind = 0;
    let mut kind_len = std::mem::size_of_val(&kind) as libc::socklen_t;
    let mut peer = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut peer_len = std::mem::size_of_val(&peer) as libc::socklen_t;
    let timeout = libc::timeval {
        tv_sec: 3,
        tv_usec: 0,
    };
    // SAFETY: all option pointers refer to initialized stack values.
    let valid = unsafe {
        libc::getsockopt(
            fd,
            libc::SOL_SOCKET,
            libc::SO_TYPE,
            (&mut kind as *mut i32).cast(),
            &mut kind_len,
        ) == 0
            && libc::getsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_PEERCRED,
                (&mut peer as *mut libc::ucred).cast(),
                &mut peer_len,
            ) == 0
            && libc::setsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_RCVTIMEO,
                (&timeout as *const libc::timeval).cast(),
                std::mem::size_of_val(&timeout) as libc::socklen_t,
            ) == 0
            && libc::setsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_SNDTIMEO,
                (&timeout as *const libc::timeval).cast(),
                std::mem::size_of_val(&timeout) as libc::socklen_t,
            ) == 0
    };
    if !valid
        || kind_len as usize != std::mem::size_of_val(&kind)
        || peer_len as usize != std::mem::size_of_val(&peer)
        || kind != libc::SOCK_STREAM
        || peer.pid <= 0
        || peer.uid == 0
        || peer.uid != socket_meta.uid()
    {
        return Err(denied("broker peer admission failed"));
    }
    Ok(())
}

fn state() -> io::Result<PathBuf> {
    if std::env::var_os("RUNTIME_DIRECTORY").as_deref() != Some(std::ffi::OsStr::new(STATE)) {
        return Err(denied("broker runtime directory missing"));
    }
    let state = PathBuf::from(STATE);
    private_dir(&state, 0o700)?;
    let runs = state.join("runs");
    if !runs.exists() {
        match DirBuilder::new().mode(0o700).create(&runs) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
    }
    private_dir(&runs, 0o700)?;
    let preparations = state.join("preparations");
    if !preparations.exists() {
        match DirBuilder::new().mode(0o700).create(&preparations) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
    }
    private_dir(&preparations, 0o700)?;
    Ok(runs)
}

fn preparations() -> io::Result<PathBuf> {
    let directory = Path::new(STATE).join("preparations");
    private_dir(&directory, 0o700)?;
    Ok(directory)
}

fn verified_bundle(directory: &Path, digest: &str) -> io::Result<bool> {
    let bundle_path = directory.join("bundle");
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(bundle_path)
    {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o444
        || !(1..=MAX_BUNDLE as u64).contains(&metadata.len())
    {
        return Err(denied("staged bundle metadata invalid"));
    }
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    file.take(MAX_BUNDLE as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() || hash(&bytes) != digest {
        return Err(denied("staged bundle digest mismatch"));
    }
    Ok(true)
}

fn manifest(candidate: &str, digest: &str) -> String {
    format!("FACTORY_CANDIDATE={candidate}\nFACTORY_BUNDLE_SHA256={digest}\n")
}

fn read_manifest(path: &Path) -> io::Result<Option<String>> {
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)
    {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || metadata.mode() & 0o7777 != 0o400
        || metadata.len() > 192
    {
        return Err(denied("preparation manifest invalid"));
    }
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    file.take(193).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != metadata.len() {
        return Err(denied("preparation manifest changed"));
    }
    Ok(String::from_utf8(bytes).ok())
}

fn manifest_identity(value: &str) -> Option<(&str, &str)> {
    let (candidate, digest) = value
        .strip_prefix("FACTORY_CANDIDATE=")?
        .split_once("\nFACTORY_BUNDLE_SHA256=")?;
    let digest = digest.strip_suffix('\n')?;
    (id(candidate, 40) && id(digest, 64)).then_some((candidate, digest))
}

fn manager(action: &str, unit: &str, run: &str) -> io::Result<()> {
    let unit = format!("soda-factory-{unit}@{run}.service");
    let status = Command::new("/usr/bin/systemctl")
        .env_clear()
        .args(["--system", "--no-ask-password", action, &unit])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()?;
    if !status.success() {
        return Err(denied("fixed preparation service failed"));
    }
    Ok(())
}

fn preparation_run(preparations: &Path, run: &str) -> io::Result<Option<PathBuf>> {
    let directory = preparations.join(run);
    match directory.symlink_metadata() {
        Ok(_) => private_dir(&directory, 0o700).map(|()| Some(directory)),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error),
    }
}

fn clean_preparation(preparations: &Path, run: &str) -> io::Result<()> {
    let Some(directory) = preparation_run(preparations, run)? else {
        return Ok(());
    };
    manager("stop", "preparer", run)?;
    manager("start", "preparation-cleaner", run)?;
    if fs::read_dir(&directory)?.next().is_some() {
        return Err(denied("preparation cleanup incomplete"));
    }
    fs::remove_dir(&directory)?;
    File::open(preparations)?.sync_all()
}

fn prepared_output(directory: &Path) -> io::Result<Option<u32>> {
    let output = directory.join("output");
    let metadata = match output.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let mode = metadata.mode() & 0o7777;
    if !metadata.is_dir() || metadata.uid() != 0 || !matches!(mode, 0o700 | 0o777 | 0o555) {
        return Err(denied("preparation output invalid"));
    }
    Ok(Some(mode))
}

fn prepared_checkout(directory: &Path) -> io::Result<()> {
    let metadata = directory.join("output/repo").symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() == 0 || metadata.mode() & 0o7777 != 0o755 {
        return Err(denied("prepared reviewer checkout invalid"));
    }
    Ok(())
}

fn prepare(run: &str, digest: &str, candidate: &str) -> io::Result<()> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("reviewer source bundle missing"));
    }
    let preparations = preparations()?;
    let expected = manifest(candidate, digest);
    if let Some(directory) = preparation_run(&preparations, run)? {
        let prior = read_manifest(&directory.join("manifest.env"))?;
        if let Some((old_candidate, old_digest)) = prior.as_deref().and_then(manifest_identity) {
            if old_candidate != candidate || old_digest != digest {
                return Err(denied("run already prepared for another candidate"));
            }
        }
        if prepared_output(&directory)? == Some(0o555) {
            if prior.as_deref() != Some(expected.as_str()) {
                return Err(denied("completed preparation manifest invalid"));
            }
            prepared_checkout(&directory)?;
            return Ok(());
        }
        clean_preparation(&preparations, run)?;
    }
    let directory = preparations.join(run);
    DirBuilder::new().mode(0o700).create(&directory)?;
    let output = directory.join("output");
    DirBuilder::new().mode(0o700).create(&output)?;
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o400)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join("manifest.env"))?;
    file.write_all(expected.as_bytes())?;
    file.sync_all()?;
    fs::set_permissions(&output, fs::Permissions::from_mode(0o777))?;
    File::open(&directory)?.sync_all()?;
    let result = (|| {
        manager("start", "preparer", run)?;
        prepared_checkout(&directory)?;
        private_dir(&output, 0o777)?;
        fs::set_permissions(&output, fs::Permissions::from_mode(0o555))?;
        File::open(&directory)?.sync_all()
    })();
    if result.is_err() {
        clean_preparation(&preparations, run)?;
    }
    result
}

fn clean_retiring(directory: &Path, digest: &str) -> io::Result<()> {
    let metadata = directory.symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.uid() != 0
        || !matches!(metadata.mode() & 0o7777, 0o555 | 0o700)
    {
        return Err(denied("retiring run directory invalid"));
    }
    let has_bundle = verified_bundle(directory, digest)?;
    fs::set_permissions(directory, fs::Permissions::from_mode(0o700))?;
    if has_bundle {
        fs::remove_file(directory.join("bundle"))?;
    }
    fs::remove_dir(directory)
}

fn retire(run: &str, digest: &str) -> io::Result<bool> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    let preparations = preparations()?;
    let retiring = runs.join(format!(".retiring-{run}-{digest}"));
    for entry in fs::read_dir(&runs)? {
        let entry = entry?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if retiring_run(&name) == Some(run) {
            if entry.path() != retiring {
                return Err(denied("run retirement digest differs"));
            }
            match runs.join(run).symlink_metadata() {
                Ok(_) => return Err(denied("run retirement state conflicts")),
                Err(error) if error.kind() == io::ErrorKind::NotFound => (),
                Err(error) => return Err(error),
            }
            if preparation_run(&preparations, run)?.is_some() {
                return Err(denied("retiring run has preparation state"));
            }
            clean_retiring(&retiring, digest)?;
            File::open(&runs)?.sync_all()?;
            return Ok(true);
        }
    }
    let directory = runs.join(run);
    let metadata = match directory.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            if preparation_run(&preparations, run)?.is_some() {
                return Err(denied("orphan preparation cannot retire"));
            }
            return Ok(false);
        }
        Err(error) => return Err(error),
    };
    if !metadata.is_dir() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o555 {
        return Err(denied("staged run directory invalid"));
    }
    if !verified_bundle(&directory, digest)? {
        return Err(denied("staged bundle missing"));
    }
    clean_preparation(&preparations, run)?;
    fs::rename(directory, &retiring)?;
    File::open(&runs)?.sync_all()?;
    clean_retiring(&retiring, digest)?;
    File::open(&runs)?.sync_all()?;
    Ok(true)
}

fn lock(runs: &Path) -> io::Result<File> {
    let path = runs.join(".lock");
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let metadata = file.metadata()?;
    if !metadata.is_file() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o600 {
        return Err(denied("broker lock invalid"));
    }
    if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX) } != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(file)
}

fn hash(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn retiring_run(name: &str) -> Option<&str> {
    let (run, digest) = name.strip_prefix(".retiring-")?.split_once('-')?;
    (id(run, 32) && id(digest, 64)).then_some(run)
}

fn stage(run: &str, bytes: &[u8], digest: &str) -> io::Result<()> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    for entry in fs::read_dir(&runs)? {
        let name = entry?.file_name();
        if retiring_run(&name.to_string_lossy()) == Some(run) {
            return Err(denied("run retirement pending"));
        }
    }
    let final_dir = runs.join(run);
    if final_dir.exists() {
        private_dir(&final_dir, 0o555)?;
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(final_dir.join("bundle"))?;
        let metadata = file.metadata()?;
        if !metadata.is_file()
            || metadata.uid() != 0
            || metadata.mode() & 0o7777 != 0o444
            || metadata.len() != bytes.len() as u64
        {
            return Err(denied("staged bundle metadata invalid"));
        }
        let mut bundle = Vec::with_capacity(bytes.len());
        file.take(bytes.len() as u64 + 1).read_to_end(&mut bundle)?;
        if bundle.len() == bytes.len() && hash(&bundle) == digest {
            return Ok(());
        }
        return Err(denied("run identity already staged with different bytes"));
    }
    if preparation_run(&preparations()?, run)?.is_some() {
        return Err(denied("orphan preparation prevents staging"));
    }
    let mut count = 0;
    for entry in fs::read_dir(&runs)? {
        let entry = entry?;
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if id(&name, 32) {
            count += 1;
        } else if let Some(retiring) = retiring_run(&name) {
            if retiring == run {
                return Err(denied("run retirement pending"));
            }
            count += 1;
        } else if name.len() == 39
            && name.starts_with('.')
            && name.ends_with(".stage")
            && id(&name[1..33], 32)
        {
            fs::remove_dir_all(entry.path())?;
        }
    }
    if count >= MAX_RUNS {
        return Err(denied("broker run staging capacity reached"));
    }
    let temp = runs.join(format!(".{run}.stage"));
    DirBuilder::new().mode(0o700).create(&temp)?;
    let result = (|| {
        let bundle_path = temp.join("bundle");
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(bundle_path)?;
        file.write_all(bytes)?;
        file.sync_all()?;
        file.set_permissions(fs::Permissions::from_mode(0o444))?;
        fs::set_permissions(&temp, fs::Permissions::from_mode(0o555))?;
        File::open(&temp)?.sync_all()?;
        fs::rename(&temp, &final_dir)?;
        File::open(&runs)?.sync_all()?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::set_permissions(&temp, fs::Permissions::from_mode(0o700));
        let _ = fs::remove_dir_all(&temp);
    }
    result
}

fn serve() -> io::Result<()> {
    socket(0)?;
    let mut input = io::stdin().lock();
    let mut header = [0u8; 4];
    input.read_exact(&mut header)?;
    let length = u32::from_le_bytes(header) as usize;
    if length == 0 || length > MAX_REQUEST {
        return Err(denied("broker request bound"));
    }
    let mut request = vec![0; length];
    input.read_exact(&mut request)?;
    let request: Request =
        serde_json::from_slice(&request).map_err(|_| denied("broker request shape invalid"))?;
    let response = match request {
        Request::StageBundle {
            version,
            run,
            length,
            sha256,
        } => {
            if version != 1
                || !id(&run, 32)
                || !id(&sha256, 64)
                || !(1..=MAX_BUNDLE).contains(&length)
            {
                return Err(denied("broker bundle declaration invalid"));
            }
            let mut bundle = vec![0; length];
            input.read_exact(&mut bundle)?;
            if hash(&bundle) != sha256 {
                return Err(denied("broker bundle digest mismatch"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            stage(&run, &bundle, &sha256)?;
            serde_json::to_vec(&Staged {
                kind: "staged",
                version: 1,
                run: &run,
                sha256: &sha256,
            })?
        }
        Request::RetireBundle {
            version,
            run,
            sha256,
        } => {
            if version != 1 || !id(&run, 32) || !id(&sha256, 64) {
                return Err(denied("broker retirement declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            let removed = retire(&run, &sha256)?;
            serde_json::to_vec(&Retired {
                kind: "retired",
                version: 1,
                run: &run,
                removed,
            })?
        }
        Request::PrepareReviewer {
            version,
            run,
            sha256,
            candidate,
        } => {
            if version != 1 || !id(&run, 32) || !id(&sha256, 64) || !id(&candidate, 40) {
                return Err(denied("broker reviewer preparation declaration invalid"));
            }
            unsafe { libc::shutdown(0, libc::SHUT_RD) };
            prepare(&run, &sha256, &candidate)?;
            serde_json::to_vec(&Prepared {
                kind: "prepared",
                version: 1,
                run: &run,
                sha256: &sha256,
                candidate: &candidate,
            })?
        }
    };
    let mut output = io::stdout().lock();
    output.write_all(&(response.len() as u32).to_le_bytes())?;
    output.write_all(&response)?;
    output.flush()
}

fn main() {
    if let Err(error) = serve() {
        eprintln!("Factory broker refused request: {error}");
        std::process::exit(1);
    }
}
