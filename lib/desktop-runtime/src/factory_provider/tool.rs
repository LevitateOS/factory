//! The Muse already installed on this computer, and the identity of the exact
//! executable a run will use. Factory never downloads, updates or signs in to
//! Muse: the user's own installer and login own that. Go and the webview cannot
//! choose a path. Discovery never executes a launcher or reads its login.
use serde::Serialize;
use sha2::{Digest, Sha256};
use soda_execution_protocol::MAX_EXECUTABLE;
use std::fs::{self, OpenOptions};
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

fn refused() -> io::Error {
    io::Error::other("installed Muse executable unavailable")
}
const NOT_INSTALLED: &str = "Muse is not installed on this computer. Install it, then run muse login in a terminal.";

#[derive(Clone, Serialize)]
pub struct ToolSelection {
    pub tool: Tool,
    pub channel: &'static str,
    pub checked_at_unix: u64,
}
#[derive(Clone, Serialize)]
pub struct Tool {
    pub harness: &'static str,
    pub version: String,
    pub sha256: String,
    pub executable: PathBuf,
}
#[derive(Clone, Serialize)]
pub struct ToolStatus {
    pub state: &'static str,
    pub upstream_version: Option<String>,
    pub selected_version: Option<String>,
    pub checked_at_unix: Option<u64>,
    pub origin: Option<&'static str>,
    pub error: Option<&'static str>,
}

pub struct Tools {
    directory: Option<PathBuf>,
    status: Mutex<ToolStatus>,
    selected: tokio::sync::Mutex<Option<(Instant, ToolSelection, std::fs::Metadata)>>,
}
struct SelectionCheck<'a> {
    status: &'a Mutex<ToolStatus>,
    finished: bool,
}
impl Drop for SelectionCheck<'_> {
    fn drop(&mut self) {
        if !self.finished {
            if let Ok(mut status) = self.status.lock() {
                status.state = "unavailable";
                status.error = Some("Muse check was interrupted. Check again before starting new work.");
            }
        }
    }
}
impl Tools {
    pub const REFRESH: Duration = Duration::from_secs(300);
    pub fn discover(profile: &Path) -> io::Result<Self> {
        let mut directories: Vec<PathBuf> = std::env::var_os("PATH")
            .map(|paths| {
                std::env::split_paths(&paths)
                    .filter(|path| path.is_absolute())
                    .collect()
            })
            .unwrap_or_default();
        if let Some(home) = std::env::var_os("HOME") {
            let directory = PathBuf::from(home).join(".local/bin");
            if directory.is_absolute() && !directories.contains(&directory) {
                directories.push(directory);
            }
        }
        for directory in directories {
            let launcher = directory.join("muse");
            if !launcher
                .metadata()
                .is_ok_and(|meta| meta.is_file() && meta.mode() & 0o111 != 0)
            {
                continue;
            }
            // The official launcher keeps muse-bin-VERSION and .muse-version
            // beside itself. Neither the shell script nor a reported version
            // is run; the executable's bytes are hashed instead.
            let launcher = launcher.canonicalize()?;
            return Self::new(Some(launcher.parent().ok_or_else(refused)?), profile);
        }
        Self::new(None, profile)
    }
    pub fn new(directory: Option<&Path>, profile: &Path) -> io::Result<Self> {
        if profile.canonicalize()? != profile {
            return Err(refused());
        }
        let owner = profile.symlink_metadata()?;
        if !owner.is_dir() || owner.uid() != unsafe { libc::geteuid() } || owner.mode() & 0o077 != 0
        {
            return Err(refused());
        }
        let directory = directory
            .map(|directory| -> io::Result<PathBuf> {
                let directory = directory.canonicalize()?;
                let meta = directory.symlink_metadata()?;
                if !meta.is_dir()
                    || meta.mode() & 0o022 != 0
                    || meta.uid() != 0 && meta.uid() != unsafe { libc::geteuid() }
                {
                    return Err(refused());
                }
                Ok(directory)
            })
            .transpose()?;
        let (state, error) = match directory {
            Some(_) => ("checking", None),
            None => ("unavailable", Some(NOT_INSTALLED)),
        };
        Ok(Self {
            directory,
            status: Mutex::new(ToolStatus {
                state,
                upstream_version: None,
                selected_version: None,
                checked_at_unix: None,
                origin: None,
                error,
            }),
            selected: tokio::sync::Mutex::new(None),
        })
    }
    /// The executable already selected and verified, without checking again.
    pub async fn current(&self) -> Option<ToolSelection> {
        self.selected.lock().await.as_ref().map(|(_, selection, _)| selection.clone())
    }
    pub async fn inspect(&self) -> io::Result<ToolSelection> {
        self.select().await
    }
    /// Look again, picking up a Muse the user's installer updated.
    pub async fn refresh(&self) -> io::Result<ToolSelection> {
        self.select().await
    }
    pub async fn refresh_chosen(&self) -> io::Result<Option<ToolSelection>> {
        if self.directory.is_none() {
            return Ok(None);
        }
        self.refresh().await.map(Some)
    }
    pub fn status(&self) -> io::Result<ToolStatus> {
        self.status
            .lock()
            .map(|status| status.clone())
            .map_err(|_| refused())
    }
    async fn select(&self) -> io::Result<ToolSelection> {
        let mut selected = self.selected.lock().await;
        let mut check = SelectionCheck { status: &self.status, finished: false };
        let result = self.select_inner(&mut selected).await;
        if let Err(error) = &result {
            eprintln!("installed Muse check: {error}");
            if let Ok(mut status) = self.status.lock() {
                status.state = "unavailable";
                status.error = Some(if self.directory.is_none() {
                    NOT_INSTALLED
                } else {
                    "The installed Muse could not be read. Reinstall Muse, then check again."
                });
            }
        }
        check.finished = true;
        result
    }
    async fn select_inner(
        &self,
        selected: &mut Option<(Instant, ToolSelection, std::fs::Metadata)>,
    ) -> io::Result<ToolSelection> {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| refused())?
            .as_secs();
        let directory = self.directory.as_ref().ok_or_else(|| io::Error::other(NOT_INSTALLED))?;
        let version = installed_version(directory)?;
        let path = directory.join(format!("muse-bin-{version}"));
        // The same file needs no second hash: its inode, size, mode and times
        // are unchanged since the bytes were read.
        let unchanged = selected.as_mut().filter(|(_, selection, identity)| {
            selection.tool.executable == path
                && path.symlink_metadata().is_ok_and(|current| same_file(identity, &current))
        });
        if let Some((_, selection, _)) = unchanged {
            selection.checked_at_unix = now;
            let mut status = self.status.lock().map_err(|_| refused())?;
            status.state = "ready";
            status.checked_at_unix = Some(now);
            status.error = None;
            return Ok(selection.clone());
        }
        {
            let mut status = self.status.lock().map_err(|_| refused())?;
            status.state = "checking";
            status.error = None;
        }
        let checked = Instant::now();
        let (executable, identity, sha256) =
            tokio::task::spawn_blocking(move || verify(&path))
                .await
                .map_err(|_| refused())??;
        let selection = ToolSelection {
            tool: Tool { harness: "muse", version, sha256, executable },
            channel: "installed",
            checked_at_unix: now,
        };
        {
            let mut status = self.status.lock().map_err(|_| refused())?;
            status.state = "ready";
            status.selected_version = Some(selection.tool.version.clone());
            status.checked_at_unix = Some(now);
            status.origin = Some("installed");
            status.error = None;
        }
        *selected = Some((checked, selection.clone(), identity));
        Ok(selection)
    }
}
/// The version the user's launcher runs, as it records it beside itself.
fn installed_version(directory: &Path) -> io::Result<String> {
    let mut version = String::new();
    OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join(".muse-version"))?
        .take(64)
        .read_to_string(&mut version)?;
    let version = version.trim();
    if version.is_empty()
        || version.len() > 32
        || !version
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'-' | b'_'))
    {
        return Err(io::Error::other("installed Muse version unreadable"));
    }
    Ok(version.to_owned())
}
fn same_file(a: &std::fs::Metadata, b: &std::fs::Metadata) -> bool {
    b.is_file()
        && a.dev() == b.dev()
        && a.ino() == b.ino()
        && a.len() == b.len()
        && a.mode() == b.mode()
        && a.uid() == b.uid()
        && a.mtime() == b.mtime()
        && a.mtime_nsec() == b.mtime_nsec()
        && a.ctime() == b.ctime()
        && a.ctime_nsec() == b.ctime_nsec()
}
/// The host's own executable format: ELF on Linux; thin 64-bit or universal
/// Mach-O on macOS.
fn native_executable(magic: &[u8]) -> bool {
    if cfg!(target_os = "macos") {
        matches!(magic, [0xcf, 0xfa, 0xed, 0xfe] | [0xca, 0xfe, 0xba, 0xbe])
    } else {
        magic == b"\x7fELF"
    }
}
/// A native executable owned by the user or root that nobody else can
/// rewrite, and the SHA-256 of the exact bytes read.
fn verify(path: &Path) -> io::Result<(PathBuf, std::fs::Metadata, String)> {
    let mut file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let before = file.metadata()?;
    if !before.is_file()
        || !(4..=MAX_EXECUTABLE).contains(&before.len())
        || before.mode() & 0o111 == 0
        || before.mode() & 0o022 != 0
        || before.uid() != 0 && before.uid() != unsafe { libc::geteuid() }
    {
        return Err(io::Error::other("installed Muse executable is not a private native executable"));
    }
    let mut bytes = vec![0u8; 1 << 20];
    let mut hash = Sha256::new();
    let mut read = 0u64;
    loop {
        let count = file.read(&mut bytes)?;
        if count == 0 {
            break;
        }
        if read == 0 && (count < 4 || !native_executable(&bytes[..4])) {
            return Err(io::Error::other("installed Muse executable is not native"));
        }
        read += count as u64;
        if read > before.len() {
            return Err(refused());
        }
        hash.update(&bytes[..count]);
    }
    let after = file.metadata()?;
    if read != before.len() || !same_file(&before, &after) || !same_file(&after, &fs::symlink_metadata(path)?) {
        return Err(io::Error::other("installed Muse executable changed while it was read"));
    }
    let sha256 = hash.finalize().iter().map(|byte| format!("{byte:02x}")).collect();
    Ok((path.to_path_buf(), after, sha256))
}
