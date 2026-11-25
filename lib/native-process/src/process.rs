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
#[cfg(target_os = "macos")]
pub(super) fn system_instance() -> Result<String> {
    let mut value = [0u8; 64];
    let mut length = value.len();
    if unsafe {
        libc::sysctlbyname(
            c"kern.bootsessionuuid".as_ptr(),
            value.as_mut_ptr().cast(),
            &mut length,
            std::ptr::null_mut(),
            0,
        )
    } != 0
    {
        return Err(Error::Scope);
    }
    let value = std::str::from_utf8(&value[..length])
        .map_err(|_| Error::Scope)?
        .trim_end_matches('\0')
        .to_ascii_lowercase();
    if value.len() != 36 || !value.bytes().all(|b| b.is_ascii_hexdigit() || b == b'-') {
        return Err(Error::Scope);
    }
    Ok(value)
}
#[cfg(target_os = "macos")]
use super::scope::start_identity;
#[cfg(target_os = "macos")]
extern "C" {
    fn ptsname_r(fd: libc::c_int, buffer: *mut libc::c_char, length: libc::size_t) -> libc::c_int;
}
#[cfg(target_os = "macos")]
const TIOCSCTTY: libc::c_ulong = 0x2000_7461;
#[cfg(target_os = "linux")]
use libc::{ptsname_r, TIOCSCTTY};
#[cfg(target_os = "linux")]
pub(super) fn system_instance() -> Result<String> {
    let value =
        std::fs::read_to_string("/proc/sys/kernel/random/boot_id").map_err(|_| Error::Scope)?;
    let value = value.trim();
    if value.len() != 36 || !value.bytes().all(|b| b.is_ascii_hexdigit() || b == b'-') {
        return Err(Error::Scope);
    }
    Ok(value.into())
}
#[cfg(target_os = "linux")]
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
        || unsafe { ptsname_r(master.as_raw_fd(), name.as_mut_ptr(), name.len()) } != 0
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
/// A kernel handle bound to this exact child before it can be reaped: a pidfd
/// on Linux, a kqueue exit registration on macOS.
#[cfg(target_os = "linux")]
fn exit_handle(pid: u32) -> Option<File> {
    let descriptor = unsafe { libc::syscall(libc::SYS_pidfd_open, pid, 0u32) };
    (descriptor >= 0).then(|| unsafe { File::from_raw_fd(descriptor as i32) })
}
#[cfg(target_os = "macos")]
fn exit_handle(pid: u32) -> Option<File> {
    let queue = unsafe { libc::kqueue() };
    if queue < 0 {
        return None;
    }
    let queue = unsafe { File::from_raw_fd(queue) };
    if unsafe { libc::fcntl(queue.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) } != 0 {
        return None;
    }
    let mut change: libc::kevent = unsafe { std::mem::zeroed() };
    change.ident = pid as usize;
    change.filter = libc::EVFILT_PROC;
    change.flags = libc::EV_ADD | libc::EV_RECEIPT;
    change.fflags = libc::NOTE_EXIT;
    let mut receipt: libc::kevent = unsafe { std::mem::zeroed() };
    let received = unsafe {
        libc::kevent(queue.as_raw_fd(), &change, 1, &mut receipt, 1, std::ptr::null())
    };
    (received == 1 && (receipt.flags & libc::EV_ERROR == 0 || receipt.data == 0)).then_some(queue)
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
        #[cfg(target_os = "linux")]
        let group = scope.procs.as_raw_fd();
        #[cfg(target_os = "macos")]
        let tty = {
            let meta = std::os::unix::fs::MetadataExt::rdev(&slave.metadata().map_err(|_| Error::Spawn)?);
            u32::try_from(meta).map_err(|_| Error::Spawn)?
        };
        let directory = input.directory.as_raw_fd();
        let terminal_fd = if input.stdin.is_some() { 1 } else { 0 };
        let mut command = input.executable.command()?;
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
                // macOS has no cgroup to enter; the owner tracks this
                // session from launch and its watchdog signals the group.
                soda_parent_death::arm(parent, libc::SIGKILL, true)?;
                #[cfg(target_os = "linux")]
                if libc::write(group, b"0".as_ptr().cast(), 1) != 1 {
                    return Err(std::io::Error::last_os_error());
                }
                if libc::getppid() != parent
                    || libc::setsid() < 0
                    || libc::ioctl(terminal_fd, TIOCSCTTY as _, 0) < 0
                    || libc::fchdir(directory) != 0
                {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let child = command.spawn().map_err(|_| Error::Spawn)?;
        drop(command);
        #[cfg(target_os = "macos")]
        return Ok((child, master, tty));
        #[cfg(target_os = "linux")]
        Ok((child, master, 0u32))
    })();
    let (mut child, master, _tty) = match launched {
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
    #[cfg(target_os = "macos")]
    let identity = identity.and_then(|identity| scope.adopt(child.id(), _tty).map(|_| identity));
    // Capture before exposing readiness or reaping the child. Even a child
    // which has already exited remains this exact kernel process identity.
    let pidfd_ok = match exit_handle(child.id()) {
        Some(descriptor) => {
            *shared
                .pidfd
                .lock()
                .unwrap_or_else(|error| error.into_inner()) = Some(descriptor);
            true
        }
        None => false,
    };
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
    let mut length = 0usize;
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
                let keep = n.min(input.limits.output_bytes.saturating_sub(length));
                shared
                    .captured
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .extend_from_slice(&buffer[..keep]);
                length += keep;
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
        #[cfg(target_os = "macos")]
        if stopping.is_none() && scope.enforce().is_err() {
            observation_ok = false;
            reason.get_or_insert(StopReason::ObservationUnavailable);
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
            state.output_bytes = length;
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
    // Live readers keep the same bytes until the owner drops this process.
    let bytes = shared.captured.lock().unwrap_or_else(|e| e.into_inner()).clone();
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
