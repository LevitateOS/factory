use super::{id, Error, Limits, Result};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

pub(super) struct Root {
    path: PathBuf,
}
impl Root {
    /// The caller admits a user-delegated cgroup-v2 root. Never enables host or
    /// system controllers, adopts unrelated groups, or falls back to killpg.
    pub fn open(delegated: &Path, profile: &str) -> Result<Self> {
        if !id(profile)
            || !delegated.starts_with("/sys/fs/cgroup")
            || fs::canonicalize(delegated).ok().as_deref() != Some(delegated)
        {
            return Err(Error::Delegation);
        }
        let dir = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(delegated)
            .map_err(|_| Error::Delegation)?;
        let meta = dir.metadata().map_err(|_| Error::Delegation)?;
        let mut stat = std::mem::MaybeUninit::<libc::statfs>::uninit();
        if meta.uid() != unsafe { libc::geteuid() }
            || unsafe { libc::fstatfs(dir.as_raw_fd(), stat.as_mut_ptr()) } != 0
            || unsafe { stat.assume_init() }.f_type != libc::CGROUP2_SUPER_MAGIC
        {
            return Err(Error::Delegation);
        }
        let active = fs::read_to_string(delegated.join("cgroup.subtree_control"))
            .map_err(|_| Error::Delegation)?;
        if ["cpu", "memory", "pids"]
            .iter()
            .any(|c| !active.split_whitespace().any(|v| v == *c))
        {
            return Err(Error::Delegation);
        }
        let path = delegated.join(format!("factory-{profile}"));
        match DirBuilder::new().mode(0o700).create(&path) {
            Ok(()) => (),
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => (),
            Err(_) => return Err(Error::Delegation),
        }
        let meta = fs::symlink_metadata(&path).map_err(|_| Error::Delegation)?;
        if !meta.is_dir() || meta.uid() != unsafe { libc::geteuid() } {
            return Err(Error::Delegation);
        }
        fs::set_permissions(&path, fs::Permissions::from_mode(0o700))
            .map_err(|_| Error::Delegation)?;
        control(&path.join("cgroup.subtree_control"), b"+cpu +memory +pids")?;
        let root = Self { path };
        // The profile's exclusive native lock is held before reaching here.
        // Old kernel scopes are retired before any new launch; no old PID is
        // treated as a live process identity or as proof of a successful exit.
        for entry in fs::read_dir(&root.path).map_err(|_| Error::Retirement)? {
            let entry = entry.map_err(|_| Error::Retirement)?;
            if !entry.file_type().map_err(|_| Error::Retirement)?.is_dir() {
                continue;
            }
            if !entry.file_name().to_str().is_some_and(id) {
                return Err(Error::Retirement);
            }
            let mut old = Scope::open(entry.path())?;
            old.kill()?;
            let deadline = Instant::now() + Duration::from_secs(3);
            while old.populated()? {
                if Instant::now() >= deadline {
                    return Err(Error::Retirement);
                }
                std::thread::sleep(Duration::from_millis(10));
            }
            old.remove()?;
        }
        Ok(root)
    }
    pub fn create(&self, invocation: &str, limits: &Limits) -> Result<Scope> {
        let path = self.path.join(invocation);
        DirBuilder::new()
            .mode(0o700)
            .create(&path)
            .map_err(|_| Error::Scope)?;
        let mut scope = Scope::open(path)?;
        let configured = (|| {
            control(
                &scope.path.join("pids.max"),
                limits.processes.to_string().as_bytes(),
            )?;
            control(
                &scope.path.join("memory.max"),
                limits.memory_bytes.to_string().as_bytes(),
            )?;
            control(&scope.path.join("memory.swap.max"), b"0")?;
            control(&scope.path.join("memory.oom.group"), b"1")?;
            control(
                &scope.path.join("cpu.max"),
                format!("{} 100000", limits.cpu_percent * 1000).as_bytes(),
            )
        })();
        if let Err(error) = configured {
            let _ = scope.remove();
            return Err(error);
        }
        Ok(scope)
    }
}
impl Drop for Root {
    fn drop(&mut self) {
        // Empty native profile scopes have no lifetime beyond their owner.
        // A retained child makes this fail, preserving uncertain cleanup.
        let _ = fs::remove_dir(&self.path);
    }
}
fn control(path: &Path, value: &[u8]) -> Result<()> {
    OpenOptions::new()
        .write(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)
        .and_then(|mut file| file.write_all(value))
        .map_err(|_| Error::Scope)
}
pub(super) struct Scope {
    path: PathBuf,
    pub procs: File,
    killer: File,
    events: File,
    removed: bool,
}
impl Scope {
    fn open(path: PathBuf) -> Result<Self> {
        let procs = OpenOptions::new()
            .write(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(path.join("cgroup.procs"))
            .map_err(|_| Error::Scope)?;
        let killer = OpenOptions::new()
            .write(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(path.join("cgroup.kill"))
            .map_err(|_| Error::Scope)?;
        let events = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(path.join("cgroup.events"))
            .map_err(|_| Error::Scope)?;
        Ok(Self {
            path,
            procs,
            killer,
            events,
            removed: false,
        })
    }
    pub fn kill(&mut self) -> Result<()> {
        self.killer
            .seek(SeekFrom::Start(0))
            .map_err(|_| Error::Retirement)?;
        self.killer.write_all(b"1").map_err(|_| Error::Retirement)
    }
    pub fn populated(&mut self) -> Result<bool> {
        self.events
            .seek(SeekFrom::Start(0))
            .map_err(|_| Error::Retirement)?;
        let mut value = String::new();
        (&mut self.events)
            .take(4096)
            .read_to_string(&mut value)
            .map_err(|_| Error::Retirement)?;
        match value.lines().find_map(|l| l.strip_prefix("populated ")) {
            Some("0") => Ok(false),
            Some("1") => Ok(true),
            _ => Err(Error::Retirement),
        }
    }
    pub fn remove(&mut self) -> Result<()> {
        if self.populated()? {
            return Err(Error::Retirement);
        }
        fs::remove_dir(&self.path).map_err(|_| Error::Retirement)?;
        self.removed = true;
        Ok(())
    }
}
impl Drop for Scope {
    fn drop(&mut self) {
        if !self.removed {
            let _ = self.kill();
        }
    }
}
