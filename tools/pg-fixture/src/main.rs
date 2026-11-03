//! Ephemeral PostgreSQL 18 for Soda/Forgejo tests (A10 runtime fixture).
//! Same pinned image and role==database shape as the appliance cluster, with
//! random per-run passwords that never appear in argv, stdout or logs.
//!
//! ```sh
//! eval "$(soda-pg-fixture start)"   # prints KEY=VALUE assignments
//! # ... run tests against $SODA_PG_HOST:$SODA_PG_PORT ...
//! soda-pg-fixture stop "$SODA_PG_CONTAINER"
//! ```
//!
//! Start output (passwords exposed as file paths only):
//!   SODA_PG_CONTAINER  container name for stop
//!   SODA_PG_HOST       127.0.0.1
//!   SODA_PG_PORT       mapped loopback port
//!   SODA_PG_DATABASES  space-separated databases created (default "forgejo soda")
//!   SODA_PG_DIR        secret dir holding <role>.passwd (mode 0600); caller removes it
//!   SODA_PG_SUPER_PASSWORD_FILE  postgres superuser password file
//!
//! SODA_PG_FIXTURE_DIR reuses a caller-owned dir instead of mktemp.
//! SODA_PG_DATABASES overrides the role/database list (names: [a-z0-9_]+).
//! Exit 3 when the container engine or image is unavailable: callers skip.
//!
//! Rust port of scripts/pg-fixture.sh. Std-only so the tree builds and tests
//! without vendoring (`cargo build`). Behavior, exit codes and
//! output lines match the shell original exactly.

use rustix::fd::OwnedFd;
use rustix::fs::{
    fchmod, fstat, ftruncate, mkdirat, open, openat, openat2, FileType, Mode, OFlags, ResolveFlags,
};
use std::collections::HashMap;
use std::env;
use std::fs::{self, File};
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::{Component, Path, PathBuf};
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

// Must match Image= in system/host/services/soda-postgres.container.
const IMAGE: &str =
    "docker.io/library/postgres:18@sha256:979bccfde35445db618c65107d27d08c6c21339d8dc3ad4c830c6633947be0e4";
const READY_TIMEOUT: Duration = Duration::from_secs(90);

fn main() {
    std::process::exit(run());
}

fn run() -> i32 {
    // `command -v podman` first: a missing engine is skip (3), like the script.
    if !have_podman() {
        eprintln!("podman unavailable");
        return 3;
    }
    let argv: Vec<String> = env::args().collect();
    match argv.get(1).map(String::as_str) {
        Some("start") => cmd_start(),
        Some("stop") => {
            let name = argv.get(2).map(String::as_str).unwrap_or("");
            if name.is_empty() {
                eprintln!("container name required");
                return 2;
            }
            // Remove the container and its anonymous fixture volumes.
            match Command::new("podman")
                .args(["rm", "-f", "-v", name])
                .stdout(Stdio::null())
                .status()
            {
                Ok(status) => status.code().unwrap_or(1),
                Err(_) => 1,
            }
        }
        _ => {
            let prog = argv
                .first()
                .map(String::as_str)
                .unwrap_or("soda-pg-fixture");
            eprintln!("usage: {prog} {{start|stop <container>}}");
            2
        }
    }
}

fn is_executable(path: &Path) -> bool {
    fs::metadata(path).is_ok_and(|info| info.is_file() && info.permissions().mode() & 0o111 != 0)
}

fn have_podman() -> bool {
    env::var_os("PATH")
        .is_some_and(|paths| env::split_paths(&paths).any(|dir| is_executable(&dir.join("podman"))))
}

/// Shell `case "$db" in ''|*[!a-z0-9_]*|'pg_'*)` refusal, inverted.
fn valid_db_name(db: &str) -> bool {
    !db.is_empty()
        && db
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'_')
        && !db.starts_with("pg_")
}

fn read_urandom(buf: &mut [u8]) -> Result<(), String> {
    getrandom::fill(buf).map_err(|e| format!("/dev/urandom: {e}"))
}

/// `mktemp -d "${TMPDIR:-/tmp}/soda-pg-fixture.XXXXXX"` (mode 0700), or the
/// caller-owned SODA_PG_FIXTURE_DIR created like `mkdir -p`.
struct SecretDir {
    path: PathBuf,
    fd: OwnedFd,
    auto_owner: Option<tempfile::TempDir>,
}

impl SecretDir {
    fn handoff(mut self) -> PathBuf {
        if let Some(owner) = self.auto_owner.take() {
            owner.keep()
        } else {
            self.path
        }
    }
}

fn secret_dir() -> Result<SecretDir, String> {
    if let Ok(dir) = env::var("SODA_PG_FIXTURE_DIR") {
        let path = PathBuf::from(&dir);
        if path.as_os_str().is_empty() {
            return Err("fixture directory path is empty".to_owned());
        }
        let fd = open_secret_dir(&path, true)?;
        return Ok(SecretDir {
            path,
            fd,
            auto_owner: None,
        });
    }
    let base = env::var("TMPDIR").unwrap_or_else(|_| "/tmp".to_string());
    let owner = tempfile::Builder::new()
        .prefix("soda-pg-fixture.")
        .permissions(fs::Permissions::from_mode(0o700))
        .tempdir_in(&base)
        .map_err(|e| format!("{base}: {e}"))?;
    let path = owner.path().to_path_buf();
    let fd = open_secret_dir(&path, false)?;
    Ok(SecretDir {
        path,
        fd,
        auto_owner: Some(owner),
    })
}

fn split_absolute_parts(path: &Path) -> Result<Vec<std::ffi::OsString>, String> {
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        env::current_dir()
            .map_err(|e| format!("cannot resolve fixture directory: {e}"))?
            .join(path)
    };
    let mut parts = Vec::new();
    for component in absolute.components() {
        match component {
            Component::RootDir | Component::CurDir => {}
            Component::Normal(name) => parts.push(name.to_os_string()),
            Component::ParentDir => {
                return Err("fixture directory must not contain parent components".to_owned())
            }
            Component::Prefix(_) => return Err("invalid fixture directory path".to_owned()),
        }
    }
    if parts.is_empty() {
        return Err("fixture directory must be a private owned directory".to_owned());
    }
    Ok(parts)
}

fn walk_components(
    root: OwnedFd,
    parts: &[std::ffi::OsString],
    create_missing: bool,
) -> Result<OwnedFd, String> {
    let resolve = ResolveFlags::BENEATH | ResolveFlags::NO_SYMLINKS | ResolveFlags::NO_MAGICLINKS;
    let mut current = root;
    for component in parts {
        let next = match openat2(
            &current,
            component,
            OFlags::PATH | OFlags::DIRECTORY | OFlags::CLOEXEC,
            Mode::empty(),
            resolve,
        ) {
            Ok(fd) => fd,
            Err(e) if create_missing && e == rustix::io::Errno::NOENT => {
                match mkdir_private_at(&current, component) {
                    Ok(()) => {}
                    Err(create_error) if create_error == rustix::io::Errno::EXIST => {}
                    Err(create_error) => {
                        return Err(format!(
                            "cannot create fixture directory component: {create_error}"
                        ));
                    }
                };
                openat2(
                    &current,
                    component,
                    OFlags::PATH | OFlags::DIRECTORY | OFlags::CLOEXEC,
                    Mode::empty(),
                    resolve,
                )
                .map_err(|e| format!("cannot open fixture directory component: {e}"))?
            }
            Err(e) => return Err(format!("cannot open fixture directory component: {e}")),
        };
        current = next;
    }
    Ok(current)
}

fn check_private_dir(current: &OwnedFd) -> Result<(), String> {
    let stat = fstat(current).map_err(|e| format!("cannot inspect fixture directory: {e}"))?;
    let mode = stat.st_mode as u32;
    if stat.st_uid != unsafe { libc::geteuid() } as u32
        || FileType::from_raw_mode(stat.st_mode) != FileType::Directory
        || mode & 0o077 != 0
        || mode & 0o700 != 0o700
    {
        return Err(
            "fixture directory must be current-user-owned and private (mode 0700)".to_owned(),
        );
    }
    Ok(())
}

/// Open or create a directory by walking from `/` through directory FDs only.
/// Every component is resolved beneath the held root with all symlinks denied.
fn open_secret_dir(path: &Path, create_missing: bool) -> Result<OwnedFd, String> {
    let parts = split_absolute_parts(path)?;
    let root = open(
        "/",
        OFlags::PATH | OFlags::DIRECTORY | OFlags::CLOEXEC,
        Mode::empty(),
    )
    .map_err(|e| format!("cannot open fixture path root: {e}"))?;
    let current = walk_components(root, &parts, create_missing)?;
    check_private_dir(&current)?;
    Ok(current)
}

// Ambient umask can only restrict this private creation. Admission fails
// closed if the resulting directory is unusable; never change process umask.
fn mkdir_private_at(parent: &OwnedFd, name: &std::ffi::OsStr) -> rustix::io::Result<()> {
    mkdirat(parent, name, Mode::from_raw_mode(0o700))
}

/// `head -c 33 /dev/urandom | od -An -tx1 | tr -d ' \n'`: 66 lowercase hex
/// chars, no trailing newline, then `chmod 600`.
fn write_password(dir: &SecretDir, role: &str) -> Result<String, String> {
    let mut bytes = [0u8; 33];
    read_urandom(&mut bytes)?;
    let hex: String = bytes.iter().map(|b| format!("{b:02x}")).collect();
    let name = format!("{role}.passwd");
    let fd = openat(
        &dir.fd,
        name.as_str(),
        OFlags::RDWR | OFlags::CREATE | OFlags::CLOEXEC | OFlags::NOFOLLOW | OFlags::NONBLOCK,
        Mode::from_raw_mode(0o600),
    )
    .map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    let stat = fstat(&fd).map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    let mode = stat.st_mode as u32;
    if FileType::from_raw_mode(stat.st_mode) != FileType::RegularFile
        || stat.st_uid != unsafe { libc::geteuid() } as u32
        || stat.st_nlink != 1
        || mode & 0o077 != 0
    {
        return Err(format!(
            "{}/{}: refusing non-private password file",
            dir.path.display(),
            name
        ));
    }
    fchmod(&fd, Mode::from_raw_mode(0o600))
        .map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    ftruncate(&fd, 0).map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    let mut file = File::from(fd);
    file.write_all(hex.as_bytes())
        .map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    file.sync_all()
        .map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    file.seek(SeekFrom::Start(0))
        .map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    let mut readback = Vec::with_capacity(67);
    file.take(67)
        .read_to_end(&mut readback)
        .map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))?;
    if readback.len() != 66 || readback.as_slice() != hex.as_bytes() {
        return Err(format!(
            "{}/{}: invalid password file contents",
            dir.path.display(),
            name
        ));
    }
    String::from_utf8(readback).map_err(|e| format!("{}/{}: {e}", dir.path.display(), name))
}

fn role_sql(db: &str, password: &str) -> String {
    format!("CREATE ROLE \"{db}\" LOGIN PASSWORD '{password}'; CREATE DATABASE \"{db}\" OWNER \"{db}\";")
}

/// First line of `podman port`, minus `${port##*:}`.
fn first_port(output: &str) -> String {
    let line = output.lines().next().unwrap_or("");
    match line.rfind(':') {
        Some(i) => line[i + 1..].to_string(),
        None => line.to_string(),
    }
}

/// The EXIT trap after a successful `podman run`: best-effort container and anonymous-volume cleanup.
fn cleanup(name: &str) {
    let _ = Command::new("podman")
        .args(["rm", "-f", "-v", name])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status();
}

fn read_database_list() -> Result<(String, Vec<String>), i32> {
    // Unset -> default; explicitly empty -> bare cluster (superuser only).
    let databases = env::var("SODA_PG_DATABASES").unwrap_or_else(|_| "forgejo soda".to_string());
    let mut list = Vec::new();
    for db in databases.split_whitespace() {
        if !valid_db_name(db) {
            eprintln!("refusing database name: {db}");
            return Err(2);
        }
        list.push(db.to_owned());
    }
    Ok((databases, list))
}

fn ensure_image() -> Result<(), i32> {
    let have_image = Command::new("podman")
        .args(["image", "exists", IMAGE])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|s| s.success());
    if have_image {
        return Ok(());
    }
    let pulled = Command::new("podman")
        .args(["pull", IMAGE])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|s| s.success());
    if !pulled {
        eprintln!("postgres fixture image unavailable");
        return Err(3);
    }
    Ok(())
}

fn write_role_passwords(dir: &SecretDir, roles: &[&str]) -> Result<HashMap<String, String>, i32> {
    let mut passwords = HashMap::new();
    for role in roles {
        match write_password(dir, role) {
            Ok(password) => {
                passwords.insert((*role).to_owned(), password);
            }
            Err(e) => {
                eprintln!("{e}");
                return Err(1);
            }
        }
    }
    Ok(passwords)
}

fn run_postgres(dir: &SecretDir, name: &str) -> Result<(), i32> {
    let super_secret = format!(
        "{}/postgres.passwd:/run/secrets/soda-pg-super:ro,z",
        dir.path.display()
    );
    match Command::new("podman")
        .args([
            "run",
            "-d",
            "--rm",
            "--name",
            name,
            "--pull=never",
            "-p",
            "127.0.0.1::5432",
            "-v",
            &super_secret,
            "-e",
            "POSTGRES_PASSWORD_FILE=/run/secrets/soda-pg-super",
            IMAGE,
        ])
        .stdout(Stdio::null())
        .status()
    {
        Ok(status) if status.success() => Ok(()),
        // `set -e`: the failing command's status is the script's status.
        // The EXIT trap is not armed yet, so no cleanup here.
        Ok(status) => Err(status.code().unwrap_or(1)),
        Err(e) => {
            eprintln!("podman run: {e}");
            Err(1)
        }
    }
}

// First start runs initdb behind a temporary socket-only server that answers
// pg_isready and then restarts; only the final server listens on TCP.
fn wait_ready(name: &str, deadline: Instant) -> Result<(), i32> {
    loop {
        let ready = Command::new("podman")
            .args([
                "exec",
                "-u",
                "postgres",
                name,
                "pg_isready",
                "-q",
                "-h",
                "127.0.0.1",
            ])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .is_ok_and(|s| s.success());
        if ready {
            return Ok(());
        }
        if Instant::now() >= deadline {
            eprintln!("postgres fixture never became ready");
            cleanup(name);
            return Err(1);
        }
        thread::sleep(Duration::from_secs(1));
    }
}

fn load_databases(
    name: &str,
    list: &[String],
    passwords: &HashMap<String, String>,
) -> Result<(), i32> {
    let mut sql = String::new();
    for db in list {
        let Some(password) = passwords.get(db.as_str()) else {
            eprintln!("password generation did not return a value for {db}");
            cleanup(name);
            return Err(1);
        };
        sql.push_str(&role_sql(db, password));
    }
    let mut child = match Command::new("podman")
        .args([
            "exec",
            "-i",
            "-u",
            "postgres",
            name,
            "psql",
            "-v",
            "ON_ERROR_STOP=1",
            "-f",
            "-",
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .spawn()
    {
        Ok(child) => child,
        Err(e) => {
            eprintln!("{e}");
            cleanup(name);
            return Err(1);
        }
    };
    // A heredoc appends one trailing newline.
    let input = format!("{sql}\n");
    let wrote = child
        .stdin
        .take()
        .is_some_and(|mut stdin| stdin.write_all(input.as_bytes()).is_ok());
    match (wrote, child.wait()) {
        (true, Ok(status)) if status.success() => Ok(()),
        (_, Ok(status)) => {
            cleanup(name);
            Err(status.code().filter(|code| *code != 0).unwrap_or(1))
        }
        (_, Err(e)) => {
            eprintln!("{e}");
            cleanup(name);
            Err(1)
        }
    }
}

fn query_port(name: &str) -> Result<String, i32> {
    // Trap disarmed: from here failures exit without cleanup, like the script.
    match Command::new("podman").args(["port", name, "5432"]).output() {
        Ok(output) if output.status.success() => {
            Ok(first_port(&String::from_utf8_lossy(&output.stdout)))
        }
        Ok(output) => {
            cleanup(name);
            Err(output.status.code().unwrap_or(1))
        }
        Err(e) => {
            eprintln!("{e}");
            cleanup(name);
            Err(1)
        }
    }
}

fn write_handoff(dir: &SecretDir, name: &str, port: &str, databases: &str) -> Result<(), i32> {
    // Keep ownership until the complete handoff is written. A closed stdout
    // must not leave auto-created secrets behind with no caller receiving the
    // path needed to remove them.
    let output = format!(
        "SODA_PG_CONTAINER={name}\nSODA_PG_HOST=127.0.0.1\nSODA_PG_PORT={port}\nSODA_PG_DATABASES=\"{databases}\"\nSODA_PG_DIR={}\nSODA_PG_SUPER_PASSWORD_FILE={}/postgres.passwd\n",
        dir.path.display(),
        dir.path.display()
    );
    if let Err(e) = std::io::stdout().write_all(output.as_bytes()) {
        eprintln!("cannot write fixture handoff: {e}");
        cleanup(name);
        return Err(1);
    }
    Ok(())
}

fn cmd_start() -> i32 {
    let (databases, list) = match read_database_list() {
        Ok((databases, list)) => (databases, list),
        Err(code) => return code,
    };
    if let Err(code) = ensure_image() {
        return code;
    }
    let dir = match secret_dir() {
        Ok(dir) => dir,
        Err(e) => {
            eprintln!("{e}");
            return 1;
        }
    };
    let mut roles = vec!["postgres"];
    roles.extend(list.iter().map(String::as_str));
    let passwords = match write_role_passwords(&dir, &roles) {
        Ok(passwords) => passwords,
        Err(code) => return code,
    };

    let mut rand = [0u8; 2];
    if let Err(e) = read_urandom(&mut rand) {
        eprintln!("{e}");
        return 1;
    }
    // `soda-pg-test-$$-$RANDOM`.
    let random = u16::from_ne_bytes(rand) % 32768;
    let name = format!("soda-pg-test-{}-{random}", std::process::id());
    if let Err(code) = run_postgres(&dir, &name) {
        return code;
    }
    if let Err(code) = wait_ready(&name, Instant::now() + READY_TIMEOUT) {
        return code;
    }
    if let Err(code) = load_databases(&name, &list, &passwords) {
        return code;
    }
    let port = match query_port(&name) {
        Ok(port) => port,
        Err(code) => return code,
    };
    if write_handoff(&dir, &name, &port, &databases).is_err() {
        return 1;
    }
    let _caller_owned_dir = dir.handoff();
    0
}
