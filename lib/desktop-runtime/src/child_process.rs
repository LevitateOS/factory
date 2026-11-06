use std::os::unix::process::CommandExt;
use std::process::Command;

// Linux parent-death ownership is tied to the spawning thread. The supervisor
// keeps a dedicated owner thread alive until its children have stopped.
pub(crate) fn parent_death(command: &mut Command, signal: i32) {
    let parent = unsafe { libc::getpid() };
    unsafe {
        command.pre_exec(move || {
            if libc::prctl(libc::PR_SET_PDEATHSIG, signal) != 0 {
                return Err(std::io::Error::last_os_error());
            }
            if libc::getppid() != parent {
                return Err(std::io::Error::from_raw_os_error(libc::ESRCH));
            }
            Ok(())
        });
    }
}
