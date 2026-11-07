//! Official stable selection and actual installed ELF identity. The native host
//! discovers the installed CLI or supplies its directory; Go and the webview
//! cannot choose a path. Discovery never executes a launcher or reads its login.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use soda_execution_protocol::MAX_EXECUTABLE;
use std::collections::BTreeMap;
use std::fs::{self, DirBuilder, OpenOptions};
use std::io::{self, Read};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::io::AsyncWriteExt;

const CHANNEL: &str = "https://api.meta.ai/muse-code/channels/muse-stable";
fn refused() -> io::Error {
    io::Error::other("current official native Muse executable unavailable")
}

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
#[derive(Deserialize)]
struct Channel {
    channel: String,
    version: String,
    manifest_url: String,
    state: String,
}
#[derive(Deserialize)]
struct Manifest {
    version: String,
    checksum_algorithm: String,
    artifacts: BTreeMap<String, Artifact>,
}
#[derive(Deserialize)]
struct Artifact {
    url: String,
    checksum: String,
    size: u64,
}

pub struct Tools {
    directory: PathBuf,
    cache: PathBuf,
    status: Mutex<ToolStatus>,
    client: reqwest::Client,
    selected: tokio::sync::Mutex<Option<(Instant, ToolSelection, std::fs::Metadata)>>,
}
impl Tools {
    pub(crate) const REFRESH: Duration = Duration::from_secs(300);
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
            // Official launchers keep muse-bin-VERSION beside the launcher.
            // Resolve symlinks, then use the existing channel/manifest/ELF checks;
            // neither a shell script nor a reported version is executable proof.
            let launcher = launcher.canonicalize()?;
            return Self::new(launcher.parent().ok_or_else(refused)?, profile);
        }
        Err(refused())
    }
    pub fn new(directory: &Path, profile: &Path) -> io::Result<Self> {
        if profile.canonicalize()? != profile {
            return Err(refused());
        }
        let owner = profile.symlink_metadata()?;
        if !owner.is_dir() || owner.uid() != unsafe { libc::geteuid() } || owner.mode() & 0o077 != 0
        {
            return Err(refused());
        }
        let directory = directory.canonicalize()?;
        let meta = directory.symlink_metadata()?;
        if !meta.is_dir()
            || meta.mode() & 0o022 != 0
            || !matches!(meta.uid(), 0) && meta.uid() != unsafe { libc::geteuid() }
        {
            return Err(refused());
        }
        Ok(Self {
            directory,
            cache: profile.join("native-tools").join("muse"),
            status: Mutex::new(ToolStatus {
                state: "checking",
                upstream_version: None,
                selected_version: None,
                checked_at_unix: None,
                origin: None,
                error: None,
            }),
            client: reqwest::Client::builder()
                .https_only(true)
                .user_agent("muse-code/factory-native")
                .redirect(reqwest::redirect::Policy::none())
                .retry(reqwest::retry::never())
                .connect_timeout(Duration::from_secs(2))
                .timeout(Duration::from_secs(3))
                .build()
                .map_err(|_| refused())?,
            selected: tokio::sync::Mutex::new(None),
        })
    }
    async fn metadata<T: serde::de::DeserializeOwned>(&self, url: &str) -> io::Result<T> {
        let mut response = self.client.get(url).send().await.map_err(|error| {
            io::Error::other(format!(
                "native Muse release request: {}",
                error.without_url()
            ))
        })?;
        if !response.status().is_success() {
            let target = response
                .headers()
                .get(reqwest::header::LOCATION)
                .and_then(|value| value.to_str().ok())
                .and_then(|value| url::Url::parse(value).ok())
                .and_then(|value| value.host_str().map(str::to_owned));
            return Err(io::Error::other(format!(
                "native Muse release {url} HTTP {}, redirect host {:?}",
                response.status().as_u16(),
                target
            )));
        }
        let mut body = Vec::new();
        while let Some(chunk) = response.chunk().await.map_err(|_| refused())? {
            if body.len() + chunk.len() > 32768 {
                return Err(refused());
            }
            body.extend_from_slice(&chunk);
        }
        serde_json::from_slice(&body).map_err(|_| refused())
    }
    // Install only into Factory's private cache. Never rewrite the user's Muse
    // launcher, login, current binary or an executable retained by another run.
    async fn acquire(
        &self,
        version: &str,
        artifact: &Artifact,
    ) -> io::Result<(PathBuf, fs::Metadata)> {
        private_directory(self.cache.parent().ok_or_else(refused)?)?;
        private_directory(&self.cache)?;
        let path = self
            .cache
            .join(format!("muse-bin-{version}-{}", artifact.checksum));
        match path.symlink_metadata() {
            Ok(_) => {
                let size = artifact.size;
                let expected = artifact.checksum.clone();
                return tokio::task::spawn_blocking(move || verify(&path, size, &expected))
                    .await
                    .map_err(|_| refused())?;
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => (),
            Err(error) => return Err(error),
        }
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).map_err(|_| refused())?;
        let pending_path = self.cache.join(format!(
            ".download-{}",
            random
                .iter()
                .map(|v| format!("{v:02x}"))
                .collect::<String>()
        ));
        let file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&pending_path)?;
        let pending = Pending(pending_path);
        self.status.lock().map_err(|_| refused())?.state = "downloading";
        let mut file = tokio::fs::File::from_std(file);
        let mut response = self
            .client
            .get(&artifact.url)
            .timeout(Duration::from_secs(120))
            .send()
            .await
            .map_err(|error| {
                io::Error::other(format!("native Muse download: {}", error.without_url()))
            })?;
        if !response.status().is_success()
            || response
                .content_length()
                .is_some_and(|size| size != artifact.size)
        {
            return Err(io::Error::other(format!(
                "native Muse artifact unavailable: HTTP {}",
                response.status().as_u16()
            )));
        }
        let mut length = 0u64;
        let mut hash = Sha256::new();
        while let Some(bytes) = response.chunk().await.map_err(|_| refused())? {
            length = length.checked_add(bytes.len() as u64).ok_or_else(refused)?;
            if length > artifact.size {
                return Err(refused());
            }
            hash.update(&bytes);
            file.write_all(&bytes).await?;
        }
        let observed: String = hash
            .finalize()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        if length != artifact.size || observed != artifact.checksum {
            return Err(refused());
        }
        file.sync_all().await?;
        drop(file);
        fs::set_permissions(&pending.0, fs::Permissions::from_mode(0o500))?;
        match fs::hard_link(&pending.0, &path) {
            Ok(()) => fs::File::open(&self.cache)?.sync_all()?,
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
        // Removing the staging link changes ctime. Capture the published file's
        // immutable identity only after that final filesystem mutation.
        drop(pending);
        fs::File::open(&self.cache)?.sync_all()?;
        let size = artifact.size;
        let expected = artifact.checksum.clone();
        tokio::task::spawn_blocking(move || verify(&path, size, &expected))
            .await
            .map_err(|_| refused())?
    }
    pub async fn inspect(&self) -> io::Result<ToolSelection> {
        self.select(false).await
    }
    pub(crate) async fn refresh(&self) -> io::Result<ToolSelection> {
        self.select(true).await
    }
    pub fn status(&self) -> io::Result<ToolStatus> {
        self.status
            .lock()
            .map(|status| status.clone())
            .map_err(|_| refused())
    }
    async fn select(&self, refresh: bool) -> io::Result<ToolSelection> {
        let result = self.select_inner(refresh).await;
        if result.is_err() {
            if let Ok(mut status) = self.status.lock() {
                status.state = "unavailable";
                status.error =
                    Some("Muse could not be verified or updated. New runs are unavailable.");
            }
        }
        result
    }
    async fn select_inner(&self, refresh: bool) -> io::Result<ToolSelection> {
        let mut selected = self.selected.lock().await;
        if let Some((checked, selection, identity)) = selected.as_ref() {
            if !refresh
                && checked.elapsed() < Self::REFRESH
                && self.status.lock().map_err(|_| refused())?.state == "ready"
            {
                let current = selection.tool.executable.symlink_metadata()?;
                if !same_file(identity, &current) {
                    return Err(refused());
                }
                return Ok(selection.clone());
            }
        }
        {
            let mut status = self.status.lock().map_err(|_| refused())?;
            status.state = "checking";
            status.error = None;
        }
        let checked = Instant::now();
        let checked_at_unix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| refused())?
            .as_secs();
        let channel: Channel = self.metadata(CHANNEL).await?;
        if channel.channel != "muse-stable"
            || channel.state != "public"
            || channel.version.is_empty()
            || channel.version.len() > 32
            || !channel
                .version
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'-' | b'_'))
        {
            return Err(refused());
        }
        self.status.lock().map_err(|_| refused())?.upstream_version = Some(channel.version.clone());
        // Construct the sole official manifest target. Never follow a URL from
        // metadata into another host, redirect or credential-bearing request.
        let manifest_url = format!("https://lookaside.facebook.com/lookaside/muse/download/?channel=muse&version={}&file=manifest.json",channel.version);
        if channel.manifest_url != manifest_url {
            return Err(refused());
        }
        let manifest: Manifest = self.metadata(&manifest_url).await?;
        let (platform, file) = match std::env::consts::ARCH {
            "x86_64" => ("x86_linux", "muse-x86-linux"),
            "aarch64" => ("aarch64_linux", "muse-aarch64-linux"),
            _ => return Err(refused()),
        };
        let artifact = manifest.artifacts.get(platform).ok_or_else(refused)?;
        if manifest.version != channel.version
            || manifest.checksum_algorithm != "sha256"
            || !super::hex(&artifact.checksum, 64)
            || !(4..=MAX_EXECUTABLE).contains(&artifact.size)
            || artifact.url != format!("https://lookaside.facebook.com/lookaside/muse/download/?channel=muse&version={}&file={file}", channel.version)
        {
            return Err(refused());
        }
        let installed = self.directory.join(format!("muse-bin-{}", channel.version));
        let path = selected
            .as_ref()
            .map(|(_, selection, _)| selection.tool.executable.clone())
            .filter(|_| {
                selected
                    .as_ref()
                    .is_some_and(|(_, selection, _)| selection.tool.version == channel.version)
            })
            .unwrap_or(installed.clone());
        let expected = artifact.checksum.clone();
        let size = artifact.size;
        let unchanged = selected.as_ref().and_then(|(_, selection, identity)| {
            let current = path.symlink_metadata().ok()?;
            (selection.tool.version == channel.version
                && selection.tool.sha256 == expected
                && same_file(identity, &current))
            .then(|| (path.clone(), current))
        });
        let (executable, identity) = match unchanged {
            Some(verified) => verified,
            None => {
                let verified = tokio::task::spawn_blocking(move || verify(&path, size, &expected))
                    .await
                    .map_err(|_| refused())?;
                match verified {
                    Ok(verified) => verified,
                    Err(_) => self.acquire(&channel.version, artifact).await?,
                }
            }
        };
        let selection = ToolSelection {
            tool: Tool {
                harness: "muse",
                version: channel.version,
                sha256: artifact.checksum.clone(),
                executable,
            },
            channel: "muse-stable",
            checked_at_unix,
        };
        {
            let mut status = self.status.lock().map_err(|_| refused())?;
            status.state = "ready";
            status.selected_version = Some(selection.tool.version.clone());
            status.checked_at_unix = Some(checked_at_unix);
            status.origin = Some(if selection.tool.executable.starts_with(&self.cache) {
                "factory_cache"
            } else {
                "installed"
            });
            status.error = None;
        }
        *selected = Some((checked, selection.clone(), identity));
        Ok(selection)
    }
}
struct Pending(PathBuf);
impl Drop for Pending {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}
fn private_directory(path: &Path) -> io::Result<()> {
    match DirBuilder::new().mode(0o700).create(path) {
        Ok(()) => (),
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
        Err(error) => return Err(error),
    }
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
        || path.canonicalize()? != path
    {
        return Err(refused());
    }
    Ok(())
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
fn verify(path: &Path, size: u64, expected: &str) -> io::Result<(PathBuf, std::fs::Metadata)> {
    let mut file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let before = file.metadata()?;
    if !before.is_file()
        || before.len() != size
        || before.mode() & 0o111 == 0
        || before.mode() & 0o022 != 0
    {
        return Err(refused());
    }
    let mut bytes = [0u8; 65536];
    let mut hash = Sha256::new();
    let mut read = 0;
    loop {
        let count = file.read(&mut bytes)?;
        if count == 0 {
            break;
        }
        if read == 0 && (count < 4 || &bytes[..4] != b"\x7fELF") {
            return Err(refused());
        }
        read += count as u64;
        if read > size {
            return Err(refused());
        }
        hash.update(&bytes[..count]);
    }
    let after = file.metadata()?;
    let observed: String = hash
        .finalize()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    if read != size || observed != expected || !same_file(&before, &after) {
        return Err(refused());
    }
    if !same_file(&after, &path.symlink_metadata()?) {
        return Err(refused());
    }
    Ok((path.to_path_buf(), after))
}
