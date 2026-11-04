//! Fixed, desktop-owned client for the privileged run staging broker.
//! A relay identity is usable only after an exact authenticated broker reply.
use serde::{de::DeserializeOwned, Deserialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::io::{self, Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{FileTypeExt, MetadataExt};
use std::os::unix::net::UnixStream;
use std::path::Path;
use std::time::Duration;

const SOCKET: &str = "/run/soda-factory/broker.sock";
const MAX_FRAME: usize = 256;
const MAX_BUNDLE: usize = 512 * 1024;
const ATTEST_TIME: Duration = Duration::from_secs(20);
const PREPARE_TIME: Duration = Duration::from_secs(90);
const AGENT_TIME: Duration = Duration::from_secs(12);

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn id(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn peer_is_root(stream: &UnixStream) -> bool {
    let mut peer = libc::ucred {
        pid: 0,
        uid: u32::MAX,
        gid: 0,
    };
    let mut length = std::mem::size_of_val(&peer) as libc::socklen_t;
    let valid = unsafe {
        libc::getsockopt(
            stream.as_raw_fd(),
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&mut peer as *mut libc::ucred).cast(),
            &mut length,
        ) == 0
    };
    valid && length as usize == std::mem::size_of_val(&peer) && peer.uid == 0 && peer.pid > 0
}

fn connect(timeout: Duration) -> io::Result<UnixStream> {
    let uid = unsafe { libc::geteuid() };
    if uid == 0 || unsafe { libc::getuid() } != uid {
        return Err(refused("broker desktop identity invalid"));
    }
    let parent = Path::new(SOCKET)
        .parent()
        .expect("fixed broker socket path");
    let directory = parent.symlink_metadata()?;
    if !directory.is_dir() || directory.uid() != 0 || directory.mode() & 0o7777 != 0o755 {
        return Err(refused("broker socket directory invalid"));
    }
    let socket = Path::new(SOCKET).symlink_metadata()?;
    if !socket.file_type().is_socket() || socket.uid() != uid || socket.mode() & 0o7777 != 0o600 {
        return Err(refused("broker socket identity invalid"));
    }
    let stream = UnixStream::connect(SOCKET)?;
    stream.set_read_timeout(Some(timeout))?;
    stream.set_write_timeout(Some(timeout))?;
    if !peer_is_root(&stream) {
        return Err(refused("broker peer identity invalid"));
    }
    let connected = Path::new(SOCKET).symlink_metadata()?;
    if !connected.file_type().is_socket()
        || connected.uid() != uid
        || connected.dev() != socket.dev()
        || connected.ino() != socket.ino()
    {
        return Err(refused("broker socket changed during connection"));
    }
    Ok(stream)
}

fn exchange<T: DeserializeOwned>(
    request: Value,
    bundle: Option<&[u8]>,
    timeout: Duration,
) -> io::Result<T> {
    let bytes = serde_json::to_vec(&request).map_err(|_| refused("broker request invalid"))?;
    if bytes.is_empty() || bytes.len() > MAX_FRAME {
        return Err(refused("broker request bound exceeded"));
    }
    let mut stream = connect(timeout)?;
    stream.write_all(&(bytes.len() as u32).to_le_bytes())?;
    stream.write_all(&bytes)?;
    if let Some(bundle) = bundle {
        stream.write_all(bundle)?;
    }
    let mut length = [0u8; 4];
    stream.read_exact(&mut length)?;
    let length = u32::from_le_bytes(length) as usize;
    if length == 0 || length > MAX_FRAME {
        return Err(refused("broker reply bound invalid"));
    }
    let mut reply = vec![0u8; length];
    stream.read_exact(&mut reply)?;
    serde_json::from_slice(&reply).map_err(|_| refused("broker reply shape invalid"))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Staged {
    kind: String,
    version: u8,
    run: String,
    sha256: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Prepared {
    kind: String,
    version: u8,
    run: String,
    sha256: String,
    candidate: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Attested {
    kind: String,
    version: u8,
    run: String,
    candidate: String,
    relay_uid: u32,
    execution_available: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AgentArmed {
    kind: String,
    version: u8,
    run: String,
    candidate: String,
    role: String,
    execution_available: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AgentAttested {
    kind: String,
    version: u8,
    run: String,
    candidate: String,
    role: String,
    worker_uid: u32,
    peer_uid: u32,
    readonly_source: bool,
    execution_available: bool,
}

/// Fixed declaration to the broker; neither value grants execution.
#[derive(Clone, Copy)]
pub enum AgentRole {
    Reviewer,
    Coder,
}

impl AgentRole {
    fn as_str(self) -> &'static str {
        match self {
            Self::Reviewer => "reviewer",
            Self::Coder => "coder",
        }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Retired {
    kind: String,
    version: u8,
    run: String,
    removed: bool,
}

/// Exact broker-issued run identity. Fields cannot be set by an agent or a
/// browser, and an owner consumes the value when activating its socket.
pub struct RelayAttestation {
    run: String,
    candidate: String,
    sha256: String,
    relay_uid: u32,
}

impl RelayAttestation {
    pub fn run(&self) -> &str {
        &self.run
    }
    pub fn candidate(&self) -> &str {
        &self.candidate
    }
    pub fn sha256(&self) -> &str {
        &self.sha256
    }
    pub fn relay_uid(&self) -> u32 {
        self.relay_uid
    }
}

/// All inputs except the bounded bundle are IDs from trusted native run
/// admission. No request selects a path, command, unit property or credential.
pub struct Broker;
impl Broker {
    pub fn stage_bundle(run: &str, bundle: &[u8]) -> io::Result<String> {
        if !id(run, 32) || bundle.is_empty() || bundle.len() > MAX_BUNDLE {
            return Err(refused("broker bundle declaration invalid"));
        }
        let sha256: String = Sha256::digest(bundle)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        let reply: Staged = exchange(
            json!({"kind":"stage_bundle","version":1,"run":run,"length":bundle.len(),"sha256":sha256}),
            Some(bundle),
            PREPARE_TIME,
        )?;
        if reply.kind != "staged"
            || reply.version != 1
            || reply.run != run
            || reply.sha256 != sha256
        {
            return Err(refused("broker staged identity invalid"));
        }
        Ok(sha256)
    }

    pub fn prepare_reviewer(run: &str, sha256: &str, candidate: &str) -> io::Result<()> {
        if !id(run, 32) || !id(sha256, 64) || !id(candidate, 40) {
            return Err(refused("broker preparation declaration invalid"));
        }
        let reply: Prepared = exchange(
            json!({"kind":"prepare_reviewer","version":1,"run":run,"sha256":sha256,"candidate":candidate}),
            None,
            PREPARE_TIME,
        )?;
        if reply.kind != "prepared"
            || reply.version != 1
            || reply.run != run
            || reply.sha256 != sha256
            || reply.candidate != candidate
        {
            return Err(refused("broker prepared identity invalid"));
        }
        Ok(())
    }

    pub fn attest_provider_relay(
        run: &str,
        sha256: &str,
        candidate: &str,
    ) -> io::Result<RelayAttestation> {
        if !id(run, 32) || !id(sha256, 64) || !id(candidate, 40) {
            return Err(refused("broker relay declaration invalid"));
        }
        let reply: Attested = exchange(
            json!({"kind":"attest_provider_relay","version":1,"run":run,"sha256":sha256,"candidate":candidate}),
            None,
            ATTEST_TIME,
        )?;
        let uid = unsafe { libc::geteuid() };
        if reply.kind != "provider_relay_attested"
            || reply.version != 1
            || reply.run != run
            || reply.candidate != candidate
            || reply.relay_uid == 0
            || reply.relay_uid == uid
            || reply.execution_available
        {
            return Err(refused("broker relay identity invalid"));
        }
        Ok(RelayAttestation {
            run: run.into(),
            candidate: candidate.into(),
            sha256: sha256.into(),
            relay_uid: reply.relay_uid,
        })
    }

    /// Arm only the exact run socket and immutable role declaration. The
    /// service behind this socket currently accepts attestation, not launch.
    pub fn arm_agent(run: &str, sha256: &str, candidate: &str, role: AgentRole) -> io::Result<()> {
        if !id(run, 32) || !id(sha256, 64) || !id(candidate, 40) {
            return Err(refused("broker agent declaration invalid"));
        }
        let role = role.as_str();
        let reply: AgentArmed = exchange(
            json!({"kind":"arm_agent","version":1,"run":run,"sha256":sha256,"candidate":candidate,"role":role}),
            None,
            ATTEST_TIME,
        )?;
        if reply.kind != "agent_armed"
            || reply.version != 1
            || reply.run != run
            || reply.candidate != candidate
            || reply.role != role
            || reply.execution_available
        {
            return Err(refused("broker agent identity invalid"));
        }
        Ok(())
    }

    /// Check the separately activated worker's exact identity and mounted
    /// source. This reply is evidence only; no agent command is accepted.
    pub fn attest_agent(run: &str, candidate: &str, role: AgentRole) -> io::Result<u32> {
        if !id(run, 32) || !id(candidate, 40) {
            return Err(refused("agent attestation declaration invalid"));
        }
        let uid = unsafe { libc::geteuid() };
        if uid == 0 || unsafe { libc::getuid() } != uid {
            return Err(refused("agent desktop identity invalid"));
        }
        let path = format!("/run/soda-factory/agents/{run}.sock");
        let parent = Path::new(&path)
            .parent()
            .expect("fixed agent socket parent");
        let directory = parent.symlink_metadata()?;
        let socket = Path::new(&path).symlink_metadata()?;
        if !directory.is_dir()
            || directory.uid() != 0
            || directory.mode() & 0o7777 != 0o755
            || !socket.file_type().is_socket()
            || socket.uid() != uid
            || socket.mode() & 0o7777 != 0o600
        {
            return Err(refused("agent socket identity invalid"));
        }
        let mut stream = UnixStream::connect(&path)?;
        stream.set_read_timeout(Some(AGENT_TIME))?;
        stream.set_write_timeout(Some(AGENT_TIME))?;
        // The listening socket belongs to systemd. On this connection its
        // kernel peer is the root manager, not the later activated worker.
        if !peer_is_root(&stream) {
            return Err(refused("agent socket peer identity invalid"));
        }
        let connected = Path::new(&path).symlink_metadata()?;
        if !connected.file_type().is_socket()
            || connected.uid() != uid
            || connected.dev() != socket.dev()
            || connected.ino() != socket.ino()
        {
            return Err(refused("agent socket changed during connection"));
        }
        let request = serde_json::to_vec(&json!({"kind":"attest_agent","version":1,"run":run}))?;
        stream.write_all(&(request.len() as u32).to_le_bytes())?;
        stream.write_all(&request)?;
        let mut length = [0u8; 4];
        stream.read_exact(&mut length)?;
        let length = u32::from_le_bytes(length) as usize;
        if length == 0 || length > MAX_FRAME {
            return Err(refused("agent reply bound invalid"));
        }
        let mut bytes = vec![0u8; length];
        stream.read_exact(&mut bytes)?;
        let reply: AgentAttested =
            serde_json::from_slice(&bytes).map_err(|_| refused("agent reply shape invalid"))?;
        if reply.kind != "agent_attested"
            || reply.version != 1
            || reply.run != run
            || reply.candidate != candidate
            || reply.role != role.as_str()
            || reply.worker_uid == 0
            || reply.worker_uid == uid
            || reply.peer_uid != uid
            || !reply.readonly_source
            || reply.execution_available
        {
            return Err(refused("agent attested identity invalid"));
        }
        Ok(reply.worker_uid)
    }

    /// Stop the exact relay and prepared checkout through the broker after the
    /// local owner is stopped. A failed retirement must be retried with the
    /// same digest; a different digest is never authority to clean this run.
    pub fn retire_bundle(run: &str, sha256: &str) -> io::Result<bool> {
        if !id(run, 32) || !id(sha256, 64) {
            return Err(refused("broker retirement declaration invalid"));
        }
        let reply: Retired = exchange(
            json!({"kind":"retire_bundle","version":1,"run":run,"sha256":sha256}),
            None,
            PREPARE_TIME,
        )?;
        if reply.kind != "retired" || reply.version != 1 || reply.run != run {
            return Err(refused("broker retired identity invalid"));
        }
        Ok(reply.removed)
    }
}
