//! Owned PostgreSQL child on a private, authenticated Unix socket. Never adopts
//! or stops another server, overwrites a cluster, or opens a TCP listener.
use crate::child_process::parent_death;
use crate::private_fs::{private_dir, private_file};
use std::error::Error;
use std::fs::{self, File};
use std::io::Write;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, ExitStatus, Stdio};
use std::thread;
use std::time::{Duration, Instant};

type Result<T> = std::result::Result<T, Box<dyn Error>>;
const TIMEOUT: Duration = Duration::from_secs(30);

pub struct NativePostgres {
    child: Option<Child>,
    profile: PathBuf,
    connection: String,
    _lock: File,
}

impl NativePostgres {
    /// The payload contains bin/{postgres,initdb,pg_isready}, lib and share from
    /// one native PostgreSQL build. The profile is private to the current user.
    pub fn start(payload: &Path, profile: &Path) -> Result<Self> {
        private_dir(profile)?;
        let profile = fs::canonicalize(profile)?;
        let lock = private_file(&profile.join("database.lock"), false)?;
        lock.try_lock()?;
        let socket = profile.join("socket");
        private_dir(&socket)?;
        private_dir(&profile.join("tmp"))?;
        if socket.as_os_str().len() > 80 {
            return Err("native database profile path is too long for its Unix socket".into());
        }
        let payload = fs::canonicalize(payload)?;
        let log = private_file(&profile.join("postgres.log"), false)?;
        let version = command(&payload, &profile, "postgres")
            .arg("--version")
            .output()?;
        if !version.status.success() {
            return Err("native postgres version check failed".into());
        }
        let version = String::from_utf8(version.stdout)?;
        let major = version
            .split_whitespace()
            .last()
            .and_then(|v| v.split('.').next())
            .filter(|v| !v.is_empty() && v.bytes().all(|b| b.is_ascii_digit()))
            .ok_or("invalid native postgres version")?;
        let data = profile.join("data");
        let password_file = profile.join("database.password");
        if data.try_exists()? {
            private_dir(&data)?;
            if fs::read_to_string(data.join("PG_VERSION"))?.trim() != major {
                return Err("native PostgreSQL major version differs from retained cluster; upgrade required".into());
            }
        } else {
            let mut random = [0; 32];
            getrandom::fill(&mut random)?;
            let password: String = random.iter().map(|b| format!("{b:02x}")).collect();
            let mut secret = private_file(&password_file, true)?;
            secret.write_all(password.as_bytes())?;
            secret.sync_all()?;
            let mut init = command(&payload, &profile, "initdb");
            init.arg("-D")
                .arg(&data)
                .arg("--username=factory")
                .arg("--auth-local=scram-sha-256")
                .arg("--auth-host=reject")
                .arg("--locale-provider=builtin")
                .arg("--builtin-locale=C.UTF-8")
                .arg("--encoding=UTF8")
                .arg("--set=dynamic_shared_memory_type=mmap")
                .arg("--pwfile")
                .arg(&password_file)
                .stdout(log.try_clone()?)
                .stderr(log.try_clone()?);
            let mut init = init.spawn()?;
            if !wait(&mut init, TIMEOUT)?.success() {
                return Err(format!(
                    "initdb failed; inspect {}",
                    profile.join("postgres.log").display()
                )
                .into());
            }
        }
        let secret = private_file(&password_file, false)?;
        let password = fs::read_to_string(&password_file)?;
        drop(secret);
        if password.len() != 64 || !password.bytes().all(|b| b.is_ascii_hexdigit()) {
            return Err("invalid native database password file".into());
        }
        let mut uri = url::Url::parse("postgresql:///postgres")?;
        uri.query_pairs_mut()
            .append_pair("host", socket.to_str().ok_or("non-UTF8 database profile")?)
            .append_pair("user", "factory")
            .append_pair("password", &password)
            .append_pair("port", "5432")
            .append_pair("sslmode", "disable");
        let socket_setting = format!(
            "unix_socket_directories=\"{}\"",
            socket.display().to_string().replace('"', "\"\"")
        );
        let child = command(&payload, &profile, "postgres")
            .arg("-D")
            .arg(&data)
            .args([
                "-c",
                "listen_addresses=",
                "-c",
                &socket_setting,
                "-c",
                "unix_socket_permissions=0700",
                "-c",
                "max_connections=32",
                "-c",
                "shared_buffers=16MB",
                "-c",
                "dynamic_shared_memory_type=mmap",
                "-c",
                "logging_collector=off",
                "-c",
                "log_destination=stderr",
                "-c",
                "huge_pages=off",
            ])
            .stdout(log.try_clone()?)
            .stderr(log)
            .spawn()?;
        let mut database = Self {
            child: Some(child),
            profile,
            connection: uri.into(),
            _lock: lock,
        };
        let deadline = Instant::now() + TIMEOUT;
        loop {
            if let Some(status) = database.child.as_mut().unwrap().try_wait()? {
                return Err(format!(
                    "native PostgreSQL exited {status}; inspect {}/postgres.log",
                    database.profile.display()
                )
                .into());
            }
            let mut ready = command(&payload, &database.profile, "pg_isready")
                .arg("-h")
                .arg(&socket)
                .args(["-U", "factory", "-d", "postgres", "-t", "1"])
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn()?;
            if wait(&mut ready, Duration::from_secs(2))?.success() {
                return Ok(database);
            }
            if Instant::now() >= deadline {
                return Err("native PostgreSQL startup timed out".into());
            }
            thread::sleep(Duration::from_millis(50));
        }
    }

    /// Secret-bearing URI for the backend; never send to a webview or log it.
    pub fn connection_uri(&self) -> &str {
        &self.connection
    }

    pub(crate) fn profile(&self) -> &Path {
        &self.profile
    }

    /// Owned process only. Used by the native crash/recovery integration driver.
    pub fn process_id(&self) -> u32 {
        self.child.as_ref().unwrap().id()
    }

    /// Observation of the owned child, not inference from a PID/socket file.
    pub fn check_running(&mut self) -> Result<()> {
        let child = self.child.as_mut().ok_or("native PostgreSQL is stopped")?;
        if child.try_wait()?.is_some() {
            return Err("owned PostgreSQL exited".into());
        }
        Ok(())
    }
    pub fn stop(mut self) -> Result<()> {
        self.stop_inner()
    }

    fn stop_inner(&mut self) -> Result<()> {
        let Some(mut child) = self.child.take() else {
            return Ok(());
        };
        if let Some(status) = child.try_wait()? {
            return Err(format!("owned PostgreSQL exited before shutdown: {status}").into());
        }
        // Fast shutdown aborts live connections, checkpoints, and exits cleanly.
        if unsafe { libc::kill(child.id() as i32, libc::SIGINT) } != 0 {
            let error = std::io::Error::last_os_error();
            let _ = child.kill();
            let _ = child.wait();
            return Err(error.into());
        }
        if !wait(&mut child, TIMEOUT)?.success() {
            return Err("native PostgreSQL did not stop cleanly".into());
        }
        Ok(())
    }
}

impl Drop for NativePostgres {
    fn drop(&mut self) {
        if let Err(error) = self.stop_inner() {
            eprintln!("native database retirement: {error}");
        }
    }
}

fn command(payload: &Path, profile: &Path, name: &str) -> Command {
    let mut command = Command::new(payload.join("bin").join(name));
    command
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .env("LANG", "C.UTF-8")
        // Native payloads are relocated out of their build prefix. Do not
        // accidentally load libpq from a developer's staging installation.
        .env("LD_LIBRARY_PATH", payload.join("lib"))
        .env("TMPDIR", profile.join("tmp"))
        .stdin(Stdio::null())
        .process_group(0);
    parent_death(&mut command, libc::SIGINT);
    command
}

fn wait(child: &mut Child, timeout: Duration) -> Result<ExitStatus> {
    let deadline = Instant::now() + timeout;
    loop {
        if let Some(status) = child.try_wait()? {
            return Ok(status);
        }
        if Instant::now() >= deadline {
            // Every command owns its process group; no caller-selected PID.
            unsafe {
                libc::kill(-(child.id() as i32), libc::SIGKILL);
            }
            let _ = child.wait();
            return Err("native PostgreSQL command timed out and was killed".into());
        }
        thread::sleep(Duration::from_millis(25));
    }
}
