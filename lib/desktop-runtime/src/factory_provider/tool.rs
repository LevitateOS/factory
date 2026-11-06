//! Official stable selection and actual installed ELF identity. The native host
//! supplies the install directory; Go and the webview cannot choose a path.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use soda_execution_protocol::MAX_EXECUTABLE;
use std::collections::BTreeMap;
use std::fs::OpenOptions;
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

const CHANNEL: &str = "https://api.meta.ai/muse-code/channels/muse-stable";
const REFRESH: Duration = Duration::from_secs(300);
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
    checksum: String,
    size: u64,
}

pub struct Tools {
    directory: PathBuf,
    client: reqwest::Client,
    selected: tokio::sync::Mutex<Option<(Instant, ToolSelection, std::fs::Metadata)>>,
}
impl Tools {
    pub fn new(directory: &Path) -> io::Result<Self> {
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
    pub async fn inspect(&self) -> io::Result<ToolSelection> {
        let mut selected = self.selected.lock().await;
        if let Some((checked, selection, identity)) = selected.as_ref() {
            if checked.elapsed() < REFRESH {
                let current = selection.tool.executable.symlink_metadata()?;
                if !same_file(identity, &current) {
                    return Err(refused());
                }
                return Ok(selection.clone());
            }
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
        // Construct the sole official manifest target. Never follow a URL from
        // metadata into another host, redirect or credential-bearing request.
        let manifest_url = format!("https://lookaside.facebook.com/lookaside/muse/download/?channel=muse&version={}&file=manifest.json",channel.version);
        if channel.manifest_url != manifest_url {
            return Err(refused());
        }
        let manifest: Manifest = self.metadata(&manifest_url).await?;
        let platform = match std::env::consts::ARCH {
            "x86_64" => "x86_linux",
            "aarch64" => "aarch64_linux",
            _ => return Err(refused()),
        };
        let artifact = manifest.artifacts.get(platform).ok_or_else(refused)?;
        if manifest.version != channel.version
            || manifest.checksum_algorithm != "sha256"
            || !super::hex(&artifact.checksum, 64)
            || !(4..=MAX_EXECUTABLE).contains(&artifact.size)
        {
            return Err(refused());
        }
        let path = self.directory.join(format!("muse-bin-{}", channel.version));
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
            None => tokio::task::spawn_blocking(move || verify(&path, size, &expected))
                .await
                .map_err(|_| refused())??,
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
        *selected = Some((checked, selection.clone(), identity));
        Ok(selection)
    }
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
