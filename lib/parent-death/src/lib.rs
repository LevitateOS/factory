//! Parent-death delivery shared by the desktop runtime and the native process
//! owner, so both rely on one defining implementation per host.
#![cfg(unix)]
use std::io;
use std::os::unix::process::CommandExt;
use std::process::Command;

/// Delivers `signal` to the child when this process dies, including abrupt
/// death. The child refuses to exec if the watch cannot be armed or its parent
/// is already gone.
pub fn parent_death(command: &mut Command, signal: i32) {
    let parent = unsafe { libc::getpid() };
    unsafe {
        command.pre_exec(move || {
            arm(parent, signal, false)?;
            if libc::getppid() != parent {
                return Err(io::Error::from_raw_os_error(libc::ESRCH));
            }
            Ok(())
        });
    }
}

/// Arm parent death from inside a caller's own `pre_exec`. With `group`, macOS
/// also signals the child's process group, which the caller must make it lead
/// before exec; Linux callers own their tree through a cgroup instead.
///
/// # Safety
/// Call only between fork and exec, with `parent` read before the fork.
// Linux parent-death ownership is tied to the spawning thread. The supervisor
// keeps a dedicated owner thread alive until its children have stopped.
#[cfg(target_os = "linux")]
pub unsafe fn arm(_parent: libc::pid_t, signal: i32, _group: bool) -> io::Result<()> {
    if libc::prctl(libc::PR_SET_PDEATHSIG, signal) != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

// macOS has no parent-death signal. A double-forked watchdog, detached from
// both processes, registers kqueue exit notes for the parent and the child
// before the child may exec. It signals the child if the parent exits first
// and leaves when the child exits. Double-forking keeps the exec'd program
// from ever observing an unknown child of its own. Only async-signal-safe
// system calls run between fork and exec.
#[cfg(target_os = "macos")]
pub unsafe fn arm(parent: libc::pid_t, signal: i32, group: bool) -> io::Result<()> {
    let child = libc::getpid();
    let mut ready = [0; 2];
    if libc::pipe(ready.as_mut_ptr()) != 0 {
        return Err(io::Error::last_os_error());
    }
    let middle = libc::fork();
    if middle < 0 {
        let error = io::Error::last_os_error();
        libc::close(ready[0]);
        libc::close(ready[1]);
        return Err(error);
    }
    if middle == 0 {
        libc::close(ready[0]);
        if libc::fork() == 0 {
            watchdog(parent, child, signal, group, ready[1]);
        }
        libc::_exit(0);
    }
    libc::close(ready[1]);
    let mut status = 0;
    while libc::waitpid(middle, &mut status, 0) < 0 && errno() == libc::EINTR {}
    let mut byte = 0u8;
    let read = loop {
        let read = libc::read(ready[0], (&mut byte as *mut u8).cast(), 1);
        if read >= 0 || errno() != libc::EINTR {
            break read;
        }
    };
    libc::close(ready[0]);
    if read != 1 {
        return Err(io::Error::from_raw_os_error(libc::ECHILD));
    }
    Ok(())
}

#[cfg(target_os = "macos")]
unsafe fn watchdog(parent: libc::pid_t, child: libc::pid_t, signal: i32, group: bool, ready: i32) -> ! {
    libc::setsid();
    for ignored in [
        libc::SIGHUP,
        libc::SIGINT,
        libc::SIGQUIT,
        libc::SIGTERM,
        libc::SIGPIPE,
        libc::SIGTSTP,
        libc::SIGTTIN,
        libc::SIGTTOU,
    ] {
        libc::signal(ignored, libc::SIG_IGN);
    }
    let queue = libc::kqueue();
    if queue < 0 {
        libc::_exit(1);
    }
    let mut changes: [libc::kevent; 2] = std::mem::zeroed();
    for (change, pid) in changes.iter_mut().zip([child, parent]) {
        change.ident = pid as usize;
        change.filter = libc::EVFILT_PROC;
        change.flags = libc::EV_ADD | libc::EV_RECEIPT;
        change.fflags = libc::NOTE_EXIT;
    }
    let mut receipts: [libc::kevent; 2] = std::mem::zeroed();
    let received = libc::kevent(
        queue,
        changes.as_ptr(),
        2,
        receipts.as_mut_ptr(),
        2,
        std::ptr::null(),
    );
    // Without both registrations the child gets no readiness byte and refuses
    // to exec, matching a failed or late Linux parent-death arm.
    if received != 2
        || receipts
            .iter()
            .any(|receipt| receipt.flags & libc::EV_ERROR != 0 && receipt.data != 0)
    {
        libc::_exit(1);
    }
    // Hold no inherited descriptor: an open output pipe or exec-status pipe
    // would delay its reader's EOF until the child exits.
    for fd in 0..libc::getdtablesize() {
        if fd != queue && fd != ready {
            libc::close(fd);
        }
    }
    let byte = 1u8;
    if libc::write(ready, (&byte as *const u8).cast(), 1) != 1 {
        libc::_exit(1);
    }
    libc::close(ready);
    loop {
        let mut events: [libc::kevent; 2] = std::mem::zeroed();
        let received = libc::kevent(
            queue,
            std::ptr::null(),
            0,
            events.as_mut_ptr(),
            2,
            std::ptr::null(),
        );
        if received < 0 {
            if errno() == libc::EINTR {
                continue;
            }
            // An unobservable parent is treated as a dead one.
            if group {
                libc::kill(-child, signal);
            }
            libc::kill(child, signal);
            libc::_exit(1);
        }
        let events = &events[..received as usize];
        if events.iter().any(|event| event.ident == child as usize) {
            libc::_exit(0);
        }
        if events.iter().any(|event| event.ident == parent as usize) {
            if group {
                libc::kill(-child, signal);
            }
            libc::kill(child, signal);
            libc::_exit(0);
        }
    }
}

#[cfg(target_os = "macos")]
fn errno() -> i32 {
    unsafe { *libc::__error() }
}
