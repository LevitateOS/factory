//! Fixed host locations shared by the desktop, the privileged broker and its
//! isolated units. Linux uses systemd runtime directories and per-unit bind
//! mounts. macOS has neither: the launchd broker uses `/private/var/run`, and
//! each unit host exports the unit's real paths through a root-owned view
//! directory named by `FACTORY_UNIT_VIEW`, confined by a Seatbelt profile.
use std::path::{Path, PathBuf};

#[cfg(not(target_os = "macos"))]
mod fixed {
    pub const SOCKETS: &str = "/run/soda-factory";
    pub const BROKER_STATE: &str = "/run/soda-factory-broker";
    pub const AGENT_RUNTIME: &str = "/run/soda-factory-agent";
    pub const RELAY_RUNTIME: &str = "/run/soda-factory-relay";
    pub const REVIEWER_RUNTIME: &str = "/run/soda-factory-reviewer";
    pub const WORKER_RUNTIME: &str = "/run/soda-factory-worker";
    pub const LIBEXEC: &str = "/usr/libexec";
    pub const BASH: &str = "/usr/bin/bash";
    pub const TIMEOUT: &str = "/usr/bin/timeout";
    /// Broker state directories are reached only through bind mounts.
    pub const TRAVERSE_MODE: u32 = 0o700;
}
#[cfg(target_os = "macos")]
mod fixed {
    pub const SOCKETS: &str = "/private/var/run/soda-factory";
    pub const BROKER_STATE: &str = "/private/var/run/soda-factory-broker";
    pub const AGENT_RUNTIME: &str = "/private/var/run/soda-factory-agent";
    pub const RELAY_RUNTIME: &str = "/private/var/run/soda-factory-relay";
    pub const REVIEWER_RUNTIME: &str = "/private/var/run/soda-factory-reviewer";
    pub const WORKER_RUNTIME: &str = "/private/var/run/soda-factory-worker";
    /// Root-owned on every ancestor; `/usr/libexec` is SIP-protected.
    pub const LIBEXEC: &str = "/Library/PrivilegedHelperTools/soda-factory";
    pub const BASH: &str = "/bin/bash";
    pub const TIMEOUT: &str = "/Library/PrivilegedHelperTools/soda-factory/soda-factory-timeout";
    /// Pool users traverse real broker paths: no listing, exact random run
    /// names only, and each unit's Seatbelt profile admits only its own run.
    pub const TRAVERSE_MODE: u32 = 0o711;
}
pub use fixed::*;

pub fn broker_socket() -> PathBuf {
    PathBuf::from(SOCKETS).join("broker.sock")
}
pub fn agent_socket(run: &str) -> PathBuf {
    PathBuf::from(SOCKETS).join("agents").join(format!("{run}.sock"))
}

/// Directory holding the desktop's per-run provider owner sockets. Linux uses
/// the user's systemd runtime directory. macOS has none a pool user may reach,
/// so the installer creates a desktop-owned directory under the broker
/// sockets, and each relay's Seatbelt profile admits only its own socket.
pub fn provider_sockets(desktop_uid: u32) -> PathBuf {
    if cfg!(target_os = "macos") {
        PathBuf::from(SOCKETS).join("providers").join(desktop_uid.to_string())
    } else {
        PathBuf::from(format!("/run/user/{desktop_uid}/soda-factory-provider"))
    }
}

/// Mode of the desktop-owned provider socket directory.
pub const PROVIDER_SOCKETS_MODE: u32 = if cfg!(target_os = "macos") { 0o711 } else { 0o700 };

/// The system Git a unit runs. macOS `/usr/bin/git` is an xcrun shim that
/// reads per-user developer settings, so units use the real binary.
pub fn git() -> &'static str {
    if cfg!(target_os = "macos") {
        [
            "/Library/Developer/CommandLineTools/usr/bin/git",
            "/Applications/Xcode.app/Contents/Developer/usr/bin/git",
        ]
        .into_iter()
        .find(|path| Path::new(path).is_file())
        .unwrap_or("/Library/Developer/CommandLineTools/usr/bin/git")
    } else {
        "/usr/bin/git"
    }
}

/// macOS environment naming a unit's root-owned view directory.
pub const UNIT_VIEW: &str = "FACTORY_UNIT_VIEW";
/// macOS environment carrying the run's provider relay loopback port.
pub const RELAY_PORT: &str = "FACTORY_RELAY_PORT";
/// Linux relays own a private network namespace and a fixed port.
pub const LINUX_RELAY_PORT: u16 = 37377;

/// A path a unit sees at `/run/NAME`: a bind mount on Linux, an entry of the
/// unit host's view directory on macOS. The value lives for the process.
/// A macOS unit started without a valid view resolves to a path that cannot
/// exist, so its own identity checks refuse service.
pub fn unit(name: &str) -> &'static str {
    use std::collections::BTreeMap;
    use std::sync::Mutex;
    static PATHS: Mutex<BTreeMap<String, &'static str>> = Mutex::new(BTreeMap::new());
    let mut paths = PATHS.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
    if let Some(path) = paths.get(name) {
        return path;
    }
    let path = if cfg!(target_os = "macos") {
        let root = std::env::var_os(UNIT_VIEW)
            .map(PathBuf::from)
            .filter(|view| view.is_absolute() && view.starts_with(BROKER_STATE))
            .unwrap_or_else(|| PathBuf::from("/nonexistent/factory-unit-view"));
        // View entries are links to real broker paths; units check the
        // real inode, as Linux units check the bind-mounted one.
        let path = root.join(name);
        std::fs::canonicalize(&path).unwrap_or(path).to_string_lossy().into_owned()
    } else {
        format!("/run/{name}")
    };
    let path: &'static str = Box::leak(path.into_boxed_str());
    paths.insert(name.to_owned(), path);
    path
}

/// The relay address the unit's clients use.
pub fn relay_origin() -> String {
    let port = if cfg!(target_os = "macos") {
        std::env::var(RELAY_PORT)
            .ok()
            .and_then(|value| value.parse::<u16>().ok())
            .filter(|port| *port >= 1024)
            .unwrap_or(0)
    } else {
        LINUX_RELAY_PORT
    };
    format!("http://127.0.0.1:{port}")
}

/// Seatbelt attestation a macOS unit makes about itself in place of Linux
/// `NoNewPrivileges` and read-only bind mounts.
#[cfg(target_os = "macos")]
pub mod confinement {
    use std::ffi::{c_char, c_int, CString};
    use std::path::Path;
    const SANDBOX_FILTER_NONE: c_int = 0;
    const SANDBOX_FILTER_PATH: c_int = 1;
    extern "C" {
        fn sandbox_check(pid: libc::pid_t, operation: *const c_char, filter: c_int, ...) -> c_int;
    }
    /// The calling process runs under a Seatbelt profile.
    pub fn confined() -> bool {
        unsafe { sandbox_check(libc::getpid(), std::ptr::null(), SANDBOX_FILTER_NONE) == 1 }
    }
    fn denied(operation: &str, path: &Path) -> bool {
        let (Ok(operation), Ok(path)) = (
            CString::new(operation),
            CString::new(path.as_os_str().as_encoded_bytes()),
        ) else {
            return false;
        };
        unsafe {
            sandbox_check(
                libc::getpid(),
                operation.as_ptr(),
                SANDBOX_FILTER_PATH,
                path.as_ptr(),
            ) == 1
        }
    }
    /// The profile refuses every write form at `path`.
    pub fn write_denied(path: &Path) -> bool {
        let real = std::fs::canonicalize(path).unwrap_or_else(|_| path.to_path_buf());
        confined()
            && ["file-write-data", "file-write-create", "file-write-unlink", "file-write-mode"]
                .into_iter()
                .all(|operation| denied(operation, path) && denied(operation, &real))
    }
    /// The profile refuses reading `path` or its metadata.
    pub fn read_denied(path: &Path) -> bool {
        confined() && denied("file-read-data", path) && denied("file-read-metadata", path)
    }
}

/// The unit cannot gain privileges: systemd `NoNewPrivileges` on Linux, the
/// unit host's Seatbelt profile (which also refuses set-ID programs) on macOS.
pub fn unit_confined() -> bool {
    #[cfg(target_os = "linux")]
    return unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } == 1;
    #[cfg(target_os = "macos")]
    return confinement::confined();
    #[allow(unreachable_code)]
    false
}

/// Unit storage that does not outlive the host's boot: tmpfs on Linux; on
/// macOS the broker's `/private/var/run` state, which also holds every unit
/// scratch volume mount.
pub fn runtime_filesystem(path: &Path) -> bool {
    #[cfg(target_os = "linux")]
    {
        let Ok(path) = std::ffi::CString::new(path.as_os_str().as_encoded_bytes()) else {
            return false;
        };
        let mut filesystem = std::mem::MaybeUninit::<libc::statfs>::uninit();
        return unsafe { libc::statfs(path.as_ptr(), filesystem.as_mut_ptr()) } == 0
            && unsafe { filesystem.assume_init() }.f_type == libc::TMPFS_MAGIC;
    }
    #[cfg(not(target_os = "linux"))]
    std::fs::canonicalize(path).is_ok_and(|path| path.starts_with(BROKER_STATE))
}

/// Kernel-attested process and user of a connected Unix stream's peer.
pub fn peer_credentials(fd: std::os::fd::RawFd) -> Option<(i32, u32)> {
    #[cfg(target_os = "linux")]
    {
        let mut peer = libc::ucred { pid: 0, uid: 0, gid: 0 };
        let mut length = std::mem::size_of_val(&peer) as libc::socklen_t;
        let read = unsafe {
            libc::getsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_PEERCRED,
                (&mut peer as *mut libc::ucred).cast(),
                &mut length,
            )
        };
        return (read == 0 && length as usize == std::mem::size_of_val(&peer))
            .then_some((peer.pid, peer.uid));
    }
    #[cfg(target_os = "macos")]
    {
        let mut uid = 0;
        let mut gid = 0;
        let mut pid: libc::pid_t = 0;
        let mut length = std::mem::size_of_val(&pid) as libc::socklen_t;
        let read = unsafe {
            libc::getpeereid(fd, &mut uid, &mut gid) == 0
                && libc::getsockopt(
                    fd,
                    libc::SOL_LOCAL,
                    libc::LOCAL_PEERPID,
                    (&mut pid as *mut libc::pid_t).cast(),
                    &mut length,
                ) == 0
        };
        return (read && length as usize == std::mem::size_of_val(&pid)).then_some((pid, uid));
    }
    #[allow(unreachable_code)]
    None
}

/// The unit cannot write `path`: a read-only bind mount on Linux, a Seatbelt
/// read-only grant on macOS.
pub fn read_only(path: &Path) -> bool {
    #[cfg(target_os = "linux")]
    {
        let Ok(path) = std::ffi::CString::new(path.as_os_str().as_encoded_bytes()) else {
            return false;
        };
        let mut stat = std::mem::MaybeUninit::<libc::statvfs>::uninit();
        return unsafe { libc::statvfs(path.as_ptr(), stat.as_mut_ptr()) } == 0
            && unsafe { stat.assume_init() }.f_flag & libc::ST_RDONLY as u64 != 0;
    }
    #[cfg(target_os = "macos")]
    return confinement::write_denied(path);
    #[allow(unreachable_code)]
    false
}

/// Shared scratch locations a unit must not reach: `InaccessiblePaths` on
/// Linux, Seatbelt denials on macOS.
#[cfg(target_os = "linux")]
pub const SHARED_SCRATCH: &[&str] = &["/tmp", "/var/tmp", "/dev/shm", "/run/lock"];
#[cfg(target_os = "macos")]
pub const SHARED_SCRATCH: &[&str] = &["/private/tmp", "/private/var/tmp", "/private/var/folders"];
pub fn inaccessible(path: &Path) -> bool {
    #[cfg(target_os = "linux")]
    {
        use std::os::unix::fs::MetadataExt;
        return path
            .symlink_metadata()
            .is_ok_and(|metadata| metadata.is_dir() && metadata.uid() == 0 && metadata.mode() & 0o7777 == 0);
    }
    #[cfg(target_os = "macos")]
    return confinement::read_denied(path) && confinement::write_denied(path);
    #[allow(unreachable_code)]
    false
}

/// Installed broker service definitions. A definition without its socket
/// means an installed broker that is not running, not an absent install.
#[cfg(target_os = "linux")]
pub const BROKER_SERVICE_DEFINITIONS: &[&str] = &[
    "/etc/systemd/system/soda-factory-broker.socket",
    "/run/systemd/system/soda-factory-broker.socket",
    "/usr/lib/systemd/system/soda-factory-broker.socket",
];
#[cfg(target_os = "macos")]
pub const BROKER_SERVICE_DEFINITIONS: &[&str] = &["/Library/LaunchDaemons/soda.factory.broker.plist"];
/// The launchd job label of the macOS broker.
pub const BROKER_LABEL: &str = "soda.factory.broker";
