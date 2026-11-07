//! Headless Linux process/resource primitive. It executes as its own caller UID;
//! callers must supply an isolated worker identity for untrusted workspace
//! commands. A trusted provider connector may use its caller's saved login with
//! a fixed CLI/tool contract; that is not an isolated workspace worker.
//! This crate is deliberately absent from the desktop runtime dependency graph.
mod storage;
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::ffi::OsString;
use std::fs::File;
use std::os::fd::AsRawFd;
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::thread::JoinHandle;
use std::time::{Duration, Instant, SystemTime};
use storage::{private_dir, private_file};
use zeroize::Zeroizing;
mod executable;
mod process;
mod scope;
pub use executable::Executable;

type Result<T> = std::result::Result<T, Error>;
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Error {
    Invalid,
    Executable,
    Delegation,
    Scope,
    Storage,
    Capacity,
    Retired,
    Spawn,
    Observation,
    Retirement,
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Self::Invalid => "invalid native invocation",
            Self::Executable => "native executable capture unavailable",
            Self::Delegation => {
                "native execution requires delegated cgroup-v2 CPU, memory and process controllers"
            }
            Self::Scope => "native process resource scope unavailable",
            Self::Storage => "private native process storage unavailable",
            Self::Capacity => "native process capacity reached",
            Self::Retired => "native process owner retired",
            Self::Spawn => "native process launch unavailable",
            Self::Observation => "native process observation unavailable",
            Self::Retirement => "native process retirement unconfirmed",
        })
    }
}
impl std::error::Error for Error {}

/// Native hard ceilings, distinct from product budgets or dispatch policy.
#[derive(Clone)]
pub struct Limits {
    pub duration: Duration,
    pub output_bytes: usize,
    pub memory_bytes: u64,
    pub processes: u32,
    pub cpu_percent: u32,
}
impl Limits {
    fn validate(&self) -> Result<()> {
        if self.duration.is_zero()
            || self.duration > Duration::from_secs(4 * 60 * 60)
            || self.output_bytes == 0
            || self.output_bytes > 16 * 1024 * 1024
            || self.memory_bytes < 16 * 1024 * 1024
            || self.memory_bytes > 16 * 1024 * 1024 * 1024
            || self.processes == 0
            || self.processes > 128
            || self.cpu_percent == 0
            || self.cpu_percent > 400
        {
            return Err(Error::Invalid);
        }
        Ok(())
    }
}

/// Native-only inputs. No Deserialize/Serialize/Debug: credentials and argv
/// never acquire a generic Go RPC or a browser shell bridge through this type.
pub struct Invocation<'a> {
    pub executable: &'a Executable,
    pub directory: &'a Path,
    pub arguments: Vec<OsString>,
    pub environment: BTreeMap<OsString, OsString>,
    pub limits: Limits,
    pub columns: u16,
    pub rows: u16,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum StopReason {
    Exited,
    Cancelled,
    Deadline,
    OutputLimit,
    OutputUnavailable,
    InputUnavailable,
    ObservationUnavailable,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Exit {
    pub code: Option<i32>,
    pub signal: Option<i32>,
}
/// Independent observations: direct-child exit, empty kernel tree and sealed
/// output are not interchangeable and never imply credential release.
#[derive(Clone, Debug)]
pub struct Observation {
    pub invocation: String,
    pub system_instance: String,
    pub start_identity: String,
    pub pid: u32,
    pub started_at: SystemTime,
    pub stopped_at: Option<SystemTime>,
    pub executable: PathBuf,
    pub executable_sha256: String,
    pub exit: Option<Exit>,
    pub reason: Option<StopReason>,
    pub tree_retired: bool,
    pub output_bytes: usize,
    pub output_complete: bool,
    pub output: PathBuf,
    pub finished: bool,
}
struct Shared {
    observation: Mutex<Observation>,
    pidfd: Mutex<Option<File>>,
    cancelled: AtomicBool,
    done: Condvar,
}
/// Dropping a request's handle does not cancel admitted native work. The owner,
/// not Go progress polling or an HTTP request lifetime, retains the monitor.
#[derive(Clone)]
pub struct Process {
    shared: Arc<Shared>,
}
impl Process {
    /// Query the kernel handle captured at launch. PID reuse cannot turn an
    /// exited child into a live observation. This says nothing about remaining
    /// descendants, sealed output, custody or product admission.
    pub fn is_live(&self) -> Result<bool> {
        let descriptor = self.shared.pidfd.lock().map_err(|_| Error::Observation)?;
        let descriptor = descriptor.as_ref().ok_or(Error::Observation)?;
        let mut poll = libc::pollfd {
            fd: descriptor.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        for _ in 0..8 {
            let ready = unsafe { libc::poll(&mut poll, 1, 0) };
            if ready == 0 {
                return Ok(true);
            }
            if ready > 0 && poll.revents & libc::POLLNVAL == 0 && poll.revents & libc::POLLIN != 0 {
                return Ok(false);
            }
            if ready >= 0
                || std::io::Error::last_os_error().kind() != std::io::ErrorKind::Interrupted
            {
                return Err(Error::Observation);
            }
        }
        Err(Error::Observation)
    }

    pub fn observe(&self) -> Observation {
        self.shared
            .observation
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }
    pub fn cancel(&self) {
        self.shared.cancelled.store(true, Ordering::SeqCst);
    }
    pub fn wait(&self, timeout: Duration) -> Observation {
        let state = self
            .shared
            .observation
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        let (state, _) = self
            .shared
            .done
            .wait_timeout_while(state, timeout, |s| !s.finished)
            .unwrap_or_else(|e| e.into_inner());
        state.clone()
    }
}
struct Worker {
    process: Process,
    thread: JoinHandle<()>,
}
pub struct Processes {
    root: PathBuf,
    scopes: scope::Root,
    workers: Vec<Worker>,
    retired: bool,
    cleanup_uncertain: bool,
    _lock: File,
}
impl Processes {
    /// root is private retained native storage; profile is the configured Rust
    /// profile identity, not a browser-selected canonical product identifier.
    pub fn open(root: &Path, delegated: &Path, profile: &str) -> Result<Self> {
        if !id(profile) {
            return Err(Error::Invalid);
        }
        private_dir(root).map_err(|_| Error::Storage)?;
        let lock =
            private_file(&root.join("process-owner.lock"), false).map_err(|_| Error::Storage)?;
        lock.try_lock().map_err(|_| Error::Capacity)?;
        // A repeated profile label in different storage must never adopt or
        // retire another live owner's kernel scopes. Bind the namespace to the
        // actual exclusively locked native file, stable across profile rename.
        let meta = lock.metadata().map_err(|_| Error::Storage)?;
        let mut hash = Sha256::new();
        hash.update(b"factory-native-process-scope-v1\0");
        hash.update(profile.as_bytes());
        hash.update(meta.dev().to_le_bytes());
        hash.update(meta.ino().to_le_bytes());
        let namespace: String = hash.finalize()[..16]
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        let scopes = scope::Root::open(delegated, &namespace)?;
        Ok(Self {
            root: root.into(),
            scopes,
            workers: Vec::new(),
            retired: false,
            cleanup_uncertain: false,
            _lock: lock,
        })
    }
    pub fn launch(&mut self, input: Invocation<'_>) -> Result<Process> {
        self.launch_inner(input, None)
    }

    /// One bounded native-only stdin payload, sent through a pipe and closed
    /// before normal output monitoring. Callers must not put account secrets
    /// here; an isolated agent receives only a run-scoped placeholder.
    pub fn launch_with_stdin(&mut self, input: Invocation<'_>, stdin: Vec<u8>) -> Result<Process> {
        let stdin = Zeroizing::new(stdin);
        if stdin.is_empty() || stdin.len() > 1024 {
            return Err(Error::Invalid);
        }
        self.launch_inner(input, Some(stdin))
    }

    fn launch_inner(
        &mut self,
        input: Invocation<'_>,
        stdin: Option<Zeroizing<Vec<u8>>>,
    ) -> Result<Process> {
        if self.retired {
            return Err(Error::Retired);
        }
        input.limits.validate()?;
        if input.rows == 0
            || input.rows > 256
            || input.columns == 0
            || input.columns > 512
            || input.arguments.len() > 256
            || input.environment.len() > 128
            || input.arguments.iter().map(|s| s.len()).sum::<usize>() > 256 * 1024
            || input
                .environment
                .iter()
                .map(|(k, v)| k.len() + v.len())
                .sum::<usize>()
                > 64 * 1024
        {
            return Err(Error::Invalid);
        }
        // Reap completed monitor threads before reusing finite native capacity.
        let mut kept = Vec::new();
        for worker in self.workers.drain(..) {
            if worker.process.observe().finished {
                let retired = worker.process.observe().tree_retired;
                if worker.thread.join().is_err() || !retired {
                    self.retired = true;
                    self.cleanup_uncertain = true;
                }
            } else {
                kept.push(worker);
            }
        }
        self.workers = kept;
        if self.retired {
            return Err(Error::Retirement);
        }
        if self.workers.len() >= 8 {
            return Err(Error::Capacity);
        }
        private_dir(input.directory).map_err(|_| Error::Storage)?;
        let directory = process::directory(input.directory)?;
        let executable = input.executable.duplicate()?;
        let invocation = random()?;
        let artifacts = self.root.join(&invocation);
        private_dir(&artifacts).map_err(|_| Error::Storage)?;
        let output_path = artifacts.join("terminal.log");
        let output = private_file(&output_path, true).map_err(|_| Error::Storage)?;
        let scope = self.scopes.create(&invocation, &input.limits)?;
        let shared = Arc::new(Shared {
            pidfd: Mutex::new(None),
            observation: Mutex::new(Observation {
                invocation,
                system_instance: process::system_instance()?,
                start_identity: String::new(),
                pid: 0,
                started_at: SystemTime::now(),
                stopped_at: None,
                executable: executable.source().into(),
                executable_sha256: executable.sha256().into(),
                exit: None,
                reason: None,
                tree_retired: false,
                output_bytes: 0,
                output_complete: false,
                output: output_path,
                finished: false,
            }),
            cancelled: AtomicBool::new(false),
            done: Condvar::new(),
        });
        let process = Process {
            shared: shared.clone(),
        };
        let (ready, started) = std::sync::mpsc::sync_channel(1);
        let deadline = Instant::now() + input.limits.duration;
        let prepared = process::Prepared {
            executable,
            directory,
            stdin,
            arguments: input.arguments,
            environment: input.environment,
            limits: input.limits,
            columns: input.columns,
            rows: input.rows,
        };
        let thread = std::thread::Builder::new()
            .name("factory-native-process".into())
            .spawn(move || {
                process::run(prepared, scope, output, shared, deadline, ready);
            })
            .map_err(|_| {
                self.retired = true;
                self.cleanup_uncertain = true;
                Error::Spawn
            })?;
        match started.recv() {
            Ok(Ok(())) => {
                self.workers.push(Worker {
                    process: process.clone(),
                    thread,
                });
                Ok(process)
            }
            result => {
                process.cancel();
                if thread.join().is_err() || !process.observe().tree_retired {
                    self.retired = true;
                    self.cleanup_uncertain = true;
                }
                Err(match result {
                    Ok(Err(error)) => error,
                    _ => Error::Spawn,
                })
            }
        }
    }
    /// Stop admission, cancel every kernel scope, then JOIN every monitor. No
    /// detached request task outlives the resource owner. Failed kernel cleanup
    /// is returned explicitly and its retained scope is fenced on reopen.
    pub fn retire(&mut self) -> Result<()> {
        self.retired = true;
        for worker in &self.workers {
            worker.process.cancel();
        }
        let mut confirmed = !self.cleanup_uncertain;
        for worker in self.workers.drain(..) {
            let process = worker.process;
            if worker.thread.join().is_err() || !process.observe().tree_retired {
                confirmed = false;
            }
        }
        if confirmed {
            Ok(())
        } else {
            self.cleanup_uncertain = true;
            Err(Error::Retirement)
        }
    }
}
impl Drop for Processes {
    fn drop(&mut self) {
        let _ = self.retire();
    }
}
fn id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn random() -> Result<String> {
    let mut bytes = [0; 16];
    getrandom::fill(&mut bytes).map_err(|_| Error::Storage)?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}
