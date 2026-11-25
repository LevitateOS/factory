//! The GNU `timeout -k GRACE DURATION COMMAND...` subset Factory units use.
//! macOS ships no timeout tool. The command runs in this process's own group;
//! on expiry the group receives SIGTERM, then SIGKILL after the grace period,
//! and the exit status is 124 as with GNU timeout.
#[cfg(not(target_os = "macos"))]
fn main() -> std::process::ExitCode {
    // Linux units call the system GNU timeout directly; this keeps one interface.
    use std::os::unix::process::CommandExt;
    let error = std::process::Command::new("/usr/bin/timeout").args(std::env::args_os().skip(1)).exec();
    eprintln!("soda-factory-timeout: {error}");
    std::process::ExitCode::from(126)
}

#[cfg(target_os = "macos")]
use std::process::{Command, ExitCode};
#[cfg(target_os = "macos")]
use std::time::{Duration, Instant};

#[cfg(target_os = "macos")]
fn duration(value: &str) -> Option<Duration> {
    let digits = value.strip_suffix('s').unwrap_or(value);
    if digits.is_empty() || digits.len() > 6 || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    digits.parse().ok().map(Duration::from_secs)
}

#[cfg(target_os = "macos")]
fn group_except_self(signal: i32) {
    let own = unsafe { libc::getpid() };
    let mut pids = vec![0i32; 1024];
    let bytes = (pids.len() * std::mem::size_of::<i32>()) as i32;
    // PROC_PGRP_ONLY lists members of this process group.
    let read = unsafe { libc::proc_listpids(2, own as u32, pids.as_mut_ptr().cast(), bytes) };
    if read <= 0 {
        return;
    }
    for pid in &pids[..read as usize / std::mem::size_of::<i32>()] {
        if *pid > 1 && *pid != own {
            unsafe { libc::kill(*pid, signal) };
        }
    }
}

#[cfg(target_os = "macos")]
fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let (grace, limit, command) = match args.as_slice() {
        [flag, grace, limit, command @ ..] if flag == "-k" && !command.is_empty() => {
            match (duration(grace), duration(limit)) {
                (Some(grace), Some(limit)) => (grace, limit, command),
                _ => return ExitCode::from(125),
            }
        }
        _ => {
            eprintln!("usage: soda-factory-timeout -k GRACE DURATION COMMAND [ARG]...");
            return ExitCode::from(125);
        }
    };
    if unsafe { libc::setpgid(0, 0) } != 0 && unsafe { libc::getpgrp() != libc::getpid() } {
        return ExitCode::from(125);
    }
    let mut child = match Command::new(&command[0]).args(&command[1..]).spawn() {
        Ok(child) => child,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return ExitCode::from(127),
        Err(_) => return ExitCode::from(126),
    };
    let deadline = Instant::now() + limit;
    let mut killed_at = None;
    loop {
        match child.try_wait() {
            Ok(Some(status)) if killed_at.is_none() => {
                use std::os::unix::process::ExitStatusExt;
                let code = status
                    .code()
                    .or_else(|| status.signal().map(|signal| 128 + signal))
                    .unwrap_or(125);
                return ExitCode::from(code.clamp(0, 255) as u8);
            }
            Ok(Some(_)) => {
                group_except_self(libc::SIGKILL);
                return ExitCode::from(124);
            }
            Ok(None) => (),
            Err(_) => return ExitCode::from(125),
        }
        let now = Instant::now();
        match killed_at {
            None if now >= deadline => {
                group_except_self(libc::SIGTERM);
                killed_at = Some(now);
            }
            Some(at) if now >= at + grace => {
                group_except_self(libc::SIGKILL);
                let _ = child.wait();
                return ExitCode::from(124);
            }
            _ => (),
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}
