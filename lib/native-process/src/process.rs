use super::{scope::Scope, Error, Executable, Exit, Limits, Result, Shared, StopReason};
use std::collections::BTreeMap;
use std::ffi::OsString;
use std::fs::{File, OpenOptions};
use std::io::Write;
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::fs::OpenOptionsExt;
use std::os::unix::process::{CommandExt, ExitStatusExt};
use std::path::Path;
use std::process::Stdio;
use std::sync::atomic::Ordering;
use std::sync::{mpsc::SyncSender, Arc};
use std::time::{Duration, Instant, SystemTime};
use zeroize::Zeroizing;
pub(super) struct Prepared {
    pub executable: Executable,
    pub directory: File,
    pub stdin: Option<Zeroizing<Vec<u8>>>,
    pub arguments: Vec<OsString>,
    pub environment: BTreeMap<OsString, OsString>,
    pub limits: Limits,
    pub columns: u16,
    pub rows: u16,
}
pub(super) fn directory(path: &Path) -> Result<File> {
    OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)
        .map_err(|_| Error::Storage)
}
pub(super) fn system_instance() -> Result<String> {
    let value =
        std::fs::read_to_string("/proc/sys/kernel/random/boot_id").map_err(|_| Error::Scope)?;
    let value = value.trim();
    if value.len() != 36 || !value.bytes().all(|b| b.is_ascii_hexdigit() || b == b'-') {
        return Err(Error::Scope);
    }
    Ok(value.into())
}
fn start_identity(pid: u32) -> Result<String> {
    let value = std::fs::read_to_string(format!("/proc/{pid}/stat")).map_err(|_| Error::Spawn)?;
    let (_, fields) = value.rsplit_once(") ").ok_or(Error::Spawn)?;
    let ticks = fields.split_whitespace().nth(19).ok_or(Error::Spawn)?;
    if ticks.is_empty() || !ticks.bytes().all(|b| b.is_ascii_digit()) {
        return Err(Error::Spawn);
    }
    Ok(ticks.into())
}
fn terminal(rows: u16, columns: u16) -> Result<(File, File)> {
    // CLOEXEC must be atomic: another monitor can fork concurrently. openpty
    // followed by fcntl would let that child inherit an unrelated terminal.
    let master = unsafe { libc::posix_openpt(libc::O_RDWR | libc::O_NOCTTY | libc::O_CLOEXEC) };
    if master < 0 {
        return Err(Error::Spawn);
    }
    // SAFETY: posix_openpt returned a fresh, exclusively owned descriptor.
    let master = unsafe { File::from_raw_fd(master) };
    let mut name = [0 as libc::c_char; 4096];
    if unsafe { libc::grantpt(master.as_raw_fd()) } != 0
        || unsafe { libc::unlockpt(master.as_raw_fd()) } != 0
        || unsafe { libc::ptsname_r(master.as_raw_fd(), name.as_mut_ptr(), name.len()) } != 0
    {
        return Err(Error::Spawn);
    }
    let slave = unsafe {
        libc::open(
            name.as_ptr(),
            libc::O_RDWR | libc::O_NOCTTY | libc::O_CLOEXEC,
        )
    };
    if slave < 0 {
        return Err(Error::Spawn);
    }
    // SAFETY: open returned a fresh, exclusively owned descriptor.
    let slave = unsafe { File::from_raw_fd(slave) };
    let size = libc::winsize {
        ws_row: rows,
        ws_col: columns,
        ws_xpixel: 0,
        ws_ypixel: 0,
    };
    if unsafe { libc::ioctl(slave.as_raw_fd(), libc::TIOCSWINSZ, &size) } != 0 {
        return Err(Error::Spawn);
    }
    let flags = unsafe { libc::fcntl(master.as_raw_fd(), libc::F_GETFL) };
    if flags < 0
        || unsafe { libc::fcntl(master.as_raw_fd(), libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0
    {
        return Err(Error::Spawn);
    }
    Ok((master, slave))
}
pub(super) fn run(
    mut input: Prepared,
    mut scope: Scope,
    mut output: File,
    shared: Arc<Shared>,
    deadline: Instant,
    ready: SyncSender<Result<()>>,
) {
    let launched = (|| {
        let (master, slave) = terminal(input.rows, input.columns)?;
        let parent = unsafe { libc::getpid() };
        let group = scope.procs.as_raw_fd();
        let directory = input.directory.as_raw_fd();
        let terminal_fd = if input.stdin.is_some() { 1 } else { 0 };
        let mut command = input.executable.command();
        command
            .args(&input.arguments)
            .env_clear()
            .env("PATH", "/usr/bin:/bin")
            .env("LANG", "C.UTF-8")
            .env("TERM", "xterm-256color")
            .envs(&input.environment);
        if input.stdin.is_some() {
            command.stdin(Stdio::piped());
        } else {
            command.stdin(Stdio::from(slave.try_clone().map_err(|_| Error::Spawn)?));
        }
        command
            .stdout(Stdio::from(slave.try_clone().map_err(|_| Error::Spawn)?))
            .stderr(Stdio::from(slave));
        // Only async-signal-safe syscalls between fork and exec; all descriptors
        // and bytes were prepared in the parent. Enter the kernel scope BEFORE
        // executable code can fork. setsid descendants stay in that scope.
        unsafe {
            command.pre_exec(move || {
                if libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL) != 0
                    || libc::getppid() != parent
                    || libc::write(group, b"0".as_ptr().cast(), 1) != 1
                    || libc::setsid() < 0
                    || libc::ioctl(terminal_fd, libc::TIOCSCTTY, 0) < 0
                    || libc::fchdir(directory) != 0
                {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let child = command.spawn().map_err(|_| Error::Spawn)?;
        drop(command);
        Ok((child, master))
    })();
    let (mut child, master) = match launched {
        Ok(value) => value,
        Err(error) => {
            let retired = scope.remove().is_ok();
            let mut state = shared.observation.lock().unwrap_or_else(|e| e.into_inner());
            state.tree_retired = retired;
            state.finished = true;
            shared.done.notify_all();
            let _ = ready.send(Err(error));
            return;
        }
    };
    let identity = start_identity(child.id());
    // Capture before exposing readiness or reaping the child. Even a child
    // which has already exited remains this exact kernel process identity.
    let descriptor = unsafe { libc::syscall(libc::SYS_pidfd_open, child.id(), 0u32) };
    let pidfd_ok = descriptor >= 0;
    if pidfd_ok {
        let descriptor = unsafe { File::from_raw_fd(descriptor as i32) };
        *shared
            .pidfd
            .lock()
            .unwrap_or_else(|error| error.into_inner()) = Some(descriptor);
    }
    let input_ok = if let Some(bytes) = input.stdin.take() {
        child
            .stdin
            .take()
            .ok_or(Error::Spawn)
            .and_then(|mut pipe| pipe.write_all(&bytes).map_err(|_| Error::Spawn))
            .is_ok()
    } else {
        true
    };
    let started_at = SystemTime::now();
    let started_clock = Instant::now();
    {
        let mut state = shared.observation.lock().unwrap_or_else(|e| e.into_inner());
        state.pid = child.id();
        state.started_at = started_at;
        if let Ok(identity) = &identity {
            state.start_identity = identity.clone();
        }
    }
    if identity.is_err() || !pidfd_ok {
        shared.cancelled.store(true, Ordering::SeqCst);
    }
    let _ = ready.send(identity.map(|_| ()).and(if input_ok && pidfd_ok {
        Ok(())
    } else {
        Err(Error::Spawn)
    }));
    let mut bytes = Vec::new();
    let mut buffer = [0u8; 16384];
    let mut eof = false;
    let mut exit = None;
    let mut reason = (!input_ok).then_some(StopReason::InputUnavailable);
    let mut stopping = None;
    let mut observation_ok = input_ok;
    let mut child_unobservable = false;
    let mut tree_retired = false;
    loop {
        if reason.is_none() {
            reason = if shared.cancelled.load(Ordering::SeqCst) {
                Some(StopReason::Cancelled)
            } else if Instant::now() >= deadline {
                Some(StopReason::Deadline)
            } else {
                None
            };
        }
        // Read at most 64KiB per pass so continuous output cannot starve the
        // deadline, child monitoring, cancellation or tree cleanup.
        for _ in 0..4 {
            if eof {
                break;
            }
            let n =
                unsafe { libc::read(master.as_raw_fd(), buffer.as_mut_ptr().cast(), buffer.len()) };
            if n > 0 {
                let n = n as usize;
                let keep = n.min(input.limits.output_bytes.saturating_sub(bytes.len()));
                bytes.extend_from_slice(&buffer[..keep]);
                if keep < n {
                    reason.get_or_insert(StopReason::OutputLimit);
                    break;
                }
            } else if n == 0 {
                eof = true;
            } else {
                let error = std::io::Error::last_os_error();
                match error.raw_os_error() {
                    Some(libc::EIO) => eof = true,
                    Some(libc::EAGAIN) => break,
                    Some(libc::EINTR) => continue,
                    _ => {
                        observation_ok = false;
                        reason.get_or_insert(StopReason::OutputUnavailable);
                        eof = true;
                    }
                }
            }
        }
        if exit.is_none() {
            match child.try_wait() {
                Ok(Some(status)) => {
                    exit = Some(Exit {
                        code: status.code(),
                        signal: status.signal(),
                    });
                    reason.get_or_insert(StopReason::Exited);
                }
                Ok(None) => (),
                Err(_) => {
                    observation_ok = false;
                    child_unobservable = true;
                    reason.get_or_insert(StopReason::ObservationUnavailable);
                }
            }
        }
        if reason.is_some() && stopping.is_none() {
            observation_ok &= scope.kill().is_ok();
            stopping = Some(Instant::now());
        }
        match scope.populated() {
            Ok(false) if exit.is_some() && eof => {
                tree_retired = scope.remove().is_ok();
                break;
            }
            Ok(_) => (),
            Err(_) => {
                observation_ok = false;
                reason.get_or_insert(StopReason::ObservationUnavailable);
            }
        }
        if stopping.is_some_and(|at| at.elapsed() > Duration::from_secs(3))
            && (exit.is_some() || child_unobservable)
        {
            // Preserve uncertainty instead of inferring empty tree/output from
            // the direct child's exit. Reopen fences any retained kernel scope.
            break;
        }
        {
            let mut state = shared.observation.lock().unwrap_or_else(|e| e.into_inner());
            state.exit = exit;
            state.output_bytes = bytes.len();
            state.reason = reason;
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    // Anchor elapsed time to launch, independent of wall-clock corrections or
    // later output persistence and product reconciliation.
    let stopped_at = tree_retired
        .then(|| started_at.checked_add(started_clock.elapsed()))
        .flatten();
    // Slow artifact I/O starts only after the bounded child/tree control pass.
    // The owner still joins this work; no output write holds a live child owner.
    let output_complete = output
        .write_all(&bytes)
        .and_then(|_| output.sync_all())
        .is_ok()
        && eof
        && observation_ok
        && reason != Some(StopReason::OutputLimit);
    let mut state = shared.observation.lock().unwrap_or_else(|e| e.into_inner());
    state.exit = exit;
    state.reason = reason;
    state.tree_retired = tree_retired;
    state.stopped_at = stopped_at;
    state.output_bytes = bytes.len();
    state.output_complete = output_complete;
    state.finished = true;
    shared.done.notify_all();
}
