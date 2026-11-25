//! Fixed host locations shared by the desktop, the privileged broker and its
//! isolated units. Linux uses systemd runtime directories and per-unit bind
//! mounts. macOS has neither: the launchd broker uses `/private/var/run`, and
//! each unit host exports the unit's real paths through a root-owned view
//! directory named by `FACTORY_UNIT_VIEW`.
use std::path::PathBuf;

#[cfg(not(target_os = "macos"))]
mod fixed {
    pub const SOCKETS: &str = "/run/soda-factory";
    pub const BROKER_STATE: &str = "/run/soda-factory-broker";
    pub const AGENT_RUNTIME: &str = "/run/soda-factory-agent";
    pub const RELAY_RUNTIME: &str = "/run/soda-factory-relay";
    pub const REVIEWER_RUNTIME: &str = "/run/soda-factory-reviewer";
    pub const WORKER_RUNTIME: &str = "/run/soda-factory-worker";
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

/// macOS environment naming a unit's root-owned view directory.
pub const UNIT_VIEW: &str = "FACTORY_UNIT_VIEW";

/// A path a unit sees at `/run/NAME`: a bind mount on Linux, an entry of the
/// unit host's view directory on macOS. Returns `None` on macOS when the unit
/// was not started by a unit host.
pub fn unit_path(name: &str) -> Option<PathBuf> {
    if cfg!(target_os = "macos") {
        let view = PathBuf::from(std::env::var_os(UNIT_VIEW)?);
        view.is_absolute().then(|| view.join(name))
    } else {
        Some(PathBuf::from("/run").join(name))
    }
}
