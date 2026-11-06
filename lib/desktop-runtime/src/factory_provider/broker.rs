//! Fixed, desktop-owned client for the privileged run staging broker.
//! A relay identity is usable only after an exact authenticated broker reply.
use super::{AgentObserver, Role};
use serde::{de::DeserializeOwned, Deserialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use soda_execution_protocol::{
    AgentExecution as AgentExecuted, AgentObserved, AgentStarted as AgentStartedReply,
    MAX_EXECUTABLE, MAX_EXECUTION_REPLY as MAX_EXEC_REPLY, MAX_OUTPUT, MAX_PROMPT,
    MAX_STAGE_BUNDLE as MAX_BUNDLE, MAX_START_REPLY,
};
use std::collections::BTreeSet;
use std::fs::OpenOptions;
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt};
use std::os::unix::net::UnixStream;
use std::path::Path;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::thread;
use std::time::{Duration, Instant};

const SOCKET: &str = "/run/soda-factory/broker.sock";
const MAX_FRAME: usize = soda_execution_protocol::MAX_BROKER_FRAME;
const ATTEST_TIME: Duration = Duration::from_secs(20);
const PREPARE_TIME: Duration = Duration::from_secs(90);
const AGENT_TIME: Duration = Duration::from_secs(60);

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn id(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn boot_id(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
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

fn connect(timeout: Duration, cancellation: Option<&AgentCancellation>) -> io::Result<UnixStream> {
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
    // A full activation backlog must not hide a blocking connect from the
    // lifecycle cancellation signal. Each AF_UNIX attempt is nonblocking.
    let deadline = Instant::now() + timeout;
    let stream = loop {
        if cancellation.is_some_and(AgentCancellation::cancelled) {
            return Err(refused("broker connection cancelled"));
        }
        if Instant::now() >= deadline {
            return Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "broker connection timed out",
            ));
        }
        let fd = unsafe {
            libc::socket(
                libc::AF_UNIX,
                libc::SOCK_STREAM | libc::SOCK_CLOEXEC | libc::SOCK_NONBLOCK,
                0,
            )
        };
        if fd < 0 {
            return Err(io::Error::last_os_error());
        }
        let stream = unsafe { UnixStream::from_raw_fd(fd) };
        let mut address: libc::sockaddr_un = unsafe { std::mem::zeroed() };
        address.sun_family = libc::AF_UNIX as libc::sa_family_t;
        for (target, byte) in address.sun_path.iter_mut().zip(SOCKET.bytes()) {
            *target = byte as libc::c_char;
        }
        let length = (std::mem::offset_of!(libc::sockaddr_un, sun_path) + SOCKET.len() + 1)
            as libc::socklen_t;
        let result =
            unsafe { libc::connect(fd, (&address as *const libc::sockaddr_un).cast(), length) };
        if result == 0 {
            stream.set_nonblocking(false)?;
            break stream;
        }
        let error = io::Error::last_os_error();
        if error.raw_os_error() == Some(libc::EINPROGRESS) {
            let mut poll = libc::pollfd {
                fd,
                events: libc::POLLOUT,
                revents: 0,
            };
            loop {
                if cancellation.is_some_and(AgentCancellation::cancelled) {
                    return Err(refused("broker connection cancelled"));
                }
                if Instant::now() >= deadline {
                    return Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "broker connection timed out",
                    ));
                }
                let ready = unsafe { libc::poll(&mut poll, 1, 20) };
                if ready < 0 && io::Error::last_os_error().kind() != io::ErrorKind::Interrupted {
                    return Err(io::Error::last_os_error());
                }
                if ready > 0 {
                    break;
                }
            }
            // peer_addr also rejects an unconnected/error descriptor. No
            // readiness event alone is accepted as a connected broker.
            stream.peer_addr()?;
            stream.set_nonblocking(false)?;
            break stream;
        }
        if error.kind() != io::ErrorKind::WouldBlock && error.kind() != io::ErrorKind::Interrupted {
            return Err(error);
        }
        drop(stream);
        thread::sleep(Duration::from_millis(20));
    };
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
    exchange_controlled(request, bundle, timeout, None, MAX_FRAME)
}

fn exchange_controlled<T: DeserializeOwned>(
    request: Value,
    bundle: Option<&[u8]>,
    timeout: Duration,
    cancellation: Option<AgentCancellation>,
    reply_limit: usize,
) -> io::Result<T> {
    let bytes = serde_json::to_vec(&request).map_err(|_| refused("broker request invalid"))?;
    if bytes.is_empty() || bytes.len() > MAX_FRAME {
        return Err(refused("broker request bound exceeded"));
    }
    let mut stream = connect(timeout, cancellation.as_ref())?;
    let _watch = cancellation
        .clone()
        .map(|cancel| AgentWatch::start(&stream, cancel))
        .transpose()?;
    stream.write_all(&(bytes.len() as u32).to_le_bytes())?;
    stream.write_all(&bytes)?;
    if let Some(bundle) = bundle {
        stream.write_all(bundle)?;
    }
    let mut length = [0u8; 4];
    stream.read_exact(&mut length)?;
    let length = u32::from_le_bytes(length) as usize;
    if length == 0 || length > reply_limit {
        return Err(refused("broker reply bound invalid"));
    }
    let mut reply = vec![0u8; length];
    stream.read_exact(&mut reply)?;
    if cancellation
        .as_ref()
        .is_some_and(AgentCancellation::cancelled)
    {
        return Err(refused("broker preparation cancelled"));
    }
    serde_json::from_slice(&reply).map_err(|_| refused("broker reply shape invalid"))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Staged {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    role: String,
    tool_sha256: String,
    prompt_sha256: String,
    sha256: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ProfileRun {
    kind: String,
    version: u8,
    profile: String,
    run: Option<String>,
    sha256: Option<String>,
    launched: bool,
    retiring: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Prepared {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    sha256: String,
    candidate: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Attested {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    role: String,
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
    profile: String,
    candidate: String,
    role: String,
    tool_sha256: String,
    prompt_sha256: String,
    execution_available: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AgentAttested {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    candidate: String,
    role: String,
    tool_sha256: String,
    prompt_sha256: String,
    worker_uid: u32,
    peer_uid: u32,
    readonly_source: bool,
    coder_workspace: bool,
    execution_available: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AgentProcess {
    pub invocation: String,
    pub system_instance: String,
    pub start_identity: String,
    /// PID inside the isolated worker's PID namespace.
    pub namespace_pid: u32,
    pub started_unix_ms: u64,
}

/// Observed after the isolated process starts, before its terminal receipt.
/// This does not prove it remains live after delivery or grant MCP authority.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AgentStarted {
    pub worker_uid: u32,
    pub process: AgentProcess,
}

pub struct AgentRun {
    pub worker_uid: u32,
    pub process: AgentProcess,
    pub reason: String,
    pub exit_code: Option<i32>,
    pub exit_signal: Option<i32>,
    pub output_complete: bool,
    pub output: Vec<u8>,
    pub candidate_state: String,
    pub result_candidate: Option<String>,
    pub candidate_bundle: Vec<u8>,
    pub bundle_sha256: Option<String>,
}

/// A trusted native owner can cancel a blocked broker call from its lifecycle
/// thread. Closing that client connection makes the broker stop the exact agent
/// service; it is not a claim that the worker has already retired.
#[derive(Clone, Default)]
pub struct AgentCancellation(Arc<AtomicBool>);

impl AgentCancellation {
    pub fn cancel(&self) {
        self.0.store(true, Ordering::SeqCst);
    }

    pub fn cancelled(&self) -> bool {
        self.0.load(Ordering::SeqCst)
    }

    pub(super) fn same(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.0, &other.0)
    }
}

struct AgentWatch {
    stream: UnixStream,
    done: Arc<AtomicBool>,
    watcher: Option<thread::JoinHandle<()>>,
}

impl AgentWatch {
    fn start(stream: &UnixStream, cancellation: AgentCancellation) -> io::Result<Self> {
        let owned = stream.try_clone()?;
        let cancel_stream = stream.try_clone()?;
        let done = Arc::new(AtomicBool::new(false));
        let observed = done.clone();
        let watcher = thread::Builder::new()
            .name("factory-agent-cancellation".into())
            .spawn(move || {
                while !observed.load(Ordering::SeqCst) {
                    if cancellation.cancelled() {
                        let _ = cancel_stream.shutdown(std::net::Shutdown::Both);
                        break;
                    }
                    thread::sleep(Duration::from_millis(20));
                }
            })?;
        Ok(Self {
            stream: owned,
            done,
            watcher: Some(watcher),
        })
    }
}

impl Drop for AgentWatch {
    fn drop(&mut self) {
        self.done.store(true, Ordering::SeqCst);
        // Errors and caller retirement must close the broker connection even
        // while the watcher still holds another descriptor for this socket.
        let _ = self.stream.shutdown(std::net::Shutdown::Both);
        if let Some(watcher) = self.watcher.take() {
            let _ = watcher.join();
        }
    }
}

impl Role {
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
    profile: String,
    removed: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Quiesced {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    sha256: String,
}

/// Exact broker-issued run identity. Fields cannot be set by an agent or a
/// browser, and an owner consumes the value when activating its socket.
pub struct RelayAttestation {
    run: String,
    profile: String,
    role: Role,
    candidate: String,
    sha256: String,
    relay_uid: u32,
}

impl RelayAttestation {
    pub fn run(&self) -> &str {
        &self.run
    }
    pub fn profile(&self) -> &str {
        &self.profile
    }
    pub fn role(&self) -> Role {
        self.role
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
    /// A credential-free source/setup job. The caller retains its cancellation
    /// and exact staged identity until retirement; no model execution is armed.
    pub fn prepare_source(
        run: &str,
        profile: &str,
        role: Role,
        bundle: &[u8],
        approved: &soda_execution_protocol::ApprovedFiles,
        candidate: &str,
        cancellation: AgentCancellation,
    ) -> io::Result<soda_execution_protocol::SetupReceipt> {
        if !id(run, 32)
            || !id(profile, 32)
            || !id(candidate, 40)
            || bundle.is_empty()
            || bundle.len() > MAX_BUNDLE
            || !approved.valid()
        {
            return Err(refused("native source preparation invalid"));
        }
        let sha256: String = Sha256::digest(bundle)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        let setup =
            serde_json::to_vec(approved).map_err(|_| refused("native approved setup invalid"))?;
        if setup.len() > soda_execution_protocol::MAX_SETUP_FRAME {
            return Err(refused("native approved setup exceeds bound"));
        }
        let mut payload = Vec::with_capacity(bundle.len() + setup.len());
        payload.extend_from_slice(bundle);
        payload.extend_from_slice(&setup);
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct StagedPreparation {
            kind: String,
            version: u8,
            run: String,
            profile: String,
            sha256: String,
            setup_sha256: String,
        }
        let staged: StagedPreparation = exchange_controlled(
            json!({"kind":"stage_preparation","version":1,"run":run,"profile":profile,
            "role":role.as_str(),"length":bundle.len(),"sha256":sha256,"setup_length":setup.len(),"setup_sha256":approved.digest()}),
            Some(&payload),
            PREPARE_TIME,
            Some(cancellation.clone()),
            MAX_FRAME,
        )?;
        if staged.kind != "preparation_staged"
            || staged.version != 1
            || staged.run != run
            || staged.profile != profile
            || staged.sha256 != sha256
            || staged.setup_sha256 != approved.digest()
        {
            return Err(refused("native source stage differs"));
        }
        Self::preparing(cancellation.clone()).prepare_reviewer(run, profile, &sha256, candidate)?;
        let receipt = Self::read_source_setup(run, profile, &sha256, candidate, cancellation)?;
        if receipt.setup_sha256 != approved.digest() {
            return Err(refused("native setup receipt differs"));
        }
        Ok(receipt)
    }
    pub(crate) fn read_source_context(
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        selection: &soda_execution_protocol::ContextSelection,
        cancellation: AgentCancellation,
    ) -> io::Result<soda_execution_protocol::SourceContext> {
        if !id(run, 32)
            || !id(profile, 32)
            || !id(sha256, 64)
            || !id(candidate, 40)
            || !selection.valid()
            || selection.source_commit != candidate
        {
            return Err(refused("context selector invalid"));
        }
        let bytes = serde_json::to_vec(selection).map_err(io::Error::other)?;
        exchange_controlled(
            json!({"kind":"read_context","version":1,"run":run,"profile":profile,"sha256":sha256,"candidate":candidate,"length":bytes.len()}),
            Some(&bytes),
            PREPARE_TIME,
            Some(cancellation),
            1024 * 1024,
        )
    }
    pub fn read_source_setup(
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        cancellation: AgentCancellation,
    ) -> io::Result<soda_execution_protocol::SetupReceipt> {
        if !id(run, 32) || !id(profile, 32) || !id(sha256, 64) || !id(candidate, 40) {
            return Err(refused("native setup read scope invalid"));
        }
        let receipt: soda_execution_protocol::SetupReceipt = exchange_controlled(
            json!({"kind":"read_setup","version":1,"run":run,"profile":profile,"sha256":sha256,"candidate":candidate}),
            None,
            PREPARE_TIME,
            Some(cancellation),
            2 * 1024 * 1024,
        )?;
        if receipt.worker_uid == 0
            || receipt.worker_uid == unsafe { libc::geteuid() }
            || receipt.source_commit != candidate
            || receipt.output.len() > soda_execution_protocol::MAX_SETUP_OUTPUT
            || receipt
                .setup_exit
                .into_iter()
                .chain(receipt.check_exit)
                .any(|code| !(0..=255).contains(&code))
        {
            return Err(refused("native setup receipt differs"));
        }
        Ok(receipt)
    }
    /// Inspect one root-owned selector for this exact native profile. The
    /// caller must hold the profile's exclusive supervisor lock before using
    /// this to clean a prior desktop incarnation.
    pub(super) fn next_profile_state(
        profile: &str,
    ) -> io::Result<Option<(String, String, bool, bool)>> {
        if !id(profile, 32) {
            return Err(refused("broker profile inspection declaration invalid"));
        }
        let reply: ProfileRun = exchange(
            json!({"kind":"next_profile_run","version":2,"profile":profile}),
            None,
            PREPARE_TIME,
        )?;
        if reply.kind != "profile_run" || reply.version != 2 || reply.profile != profile {
            return Err(refused("broker profile inspection identity invalid"));
        }
        match (reply.run, reply.sha256, reply.launched, reply.retiring) {
            (None, None, false, false) => Ok(None),
            (Some(run), Some(sha256), launched, retiring)
                if id(&run, 32) && id(&sha256, 64) && !(launched && retiring) =>
            {
                Ok(Some((run, sha256, launched, retiring)))
            }
            _ => Err(refused("broker profile inspection selector invalid")),
        }
    }

    /// A true final flag also covers an interrupted retirement, whose original
    /// launch outcome is unknown to callers outside startup reconciliation.
    pub fn next_profile_run(profile: &str) -> io::Result<Option<(String, String, bool)>> {
        Ok(Self::next_profile_state(profile)?
            .map(|(run, sha256, launched, retiring)| (run, sha256, launched || retiring)))
    }

    /// Recover stale root staging before a new Go product incarnation starts.
    /// Each exact retirement is separately acknowledged. A consumed launch
    /// remains for Go outcome reconciliation; this startup path cannot safely
    /// infer its result from a stopped process or an absent desktop owner.
    pub fn reconcile_profile(profile: &str) -> io::Result<usize> {
        let mut retired = BTreeSet::new();
        for _ in 0..64 {
            let Some((run, sha256, launched, pending)) = Self::next_profile_state(profile)? else {
                return Ok(retired.len());
            };
            if launched {
                return Err(refused(
                    "broker launched run outcome requires reconciliation",
                ));
            }
            if !retired.insert(run.clone())
                || !(if pending {
                    Self::finish_retiring(&run, profile, &sha256)?
                } else {
                    Self::retire_unlaunched(&run, profile, &sha256)?
                })
            {
                return Err(refused("broker profile cleanup did not advance"));
            }
        }
        if Self::next_profile_run(profile)?.is_some() {
            Err(refused("broker profile cleanup capacity exceeded"))
        } else {
            Ok(retired.len())
        }
    }

    pub(super) fn preparing(cancellation: AgentCancellation) -> BrokerPreparation {
        BrokerPreparation {
            cancellation: Some(cancellation),
        }
    }
    pub fn stage_bundle(
        run: &str,
        profile: &str,
        role: Role,
        tool: &str,
        prompt: &str,
        bundle: &[u8],
    ) -> io::Result<String> {
        BrokerPreparation { cancellation: None }
            .stage_bundle(run, profile, role, tool, prompt, bundle)
    }
    pub fn prepare_reviewer(
        run: &str,
        profile: &str,
        digest: &str,
        candidate: &str,
    ) -> io::Result<()> {
        BrokerPreparation { cancellation: None }.prepare_reviewer(run, profile, digest, candidate)
    }
    pub fn attest_provider_relay(
        run: &str,
        profile: &str,
        digest: &str,
        candidate: &str,
        role: Role,
    ) -> io::Result<RelayAttestation> {
        BrokerPreparation { cancellation: None }
            .attest_provider_relay(run, profile, digest, candidate, role)
    }
    pub fn arm_agent(
        run: &str,
        profile: &str,
        digest: &str,
        candidate: &str,
        role: Role,
        tool: &str,
        prompt: &str,
    ) -> io::Result<()> {
        BrokerPreparation { cancellation: None }
            .arm_agent(run, profile, digest, candidate, role, tool, prompt)
    }
    pub fn attest_agent(
        run: &str,
        profile: &str,
        digest: &str,
        candidate: &str,
        role: Role,
        tool: &str,
        prompt: &str,
    ) -> io::Result<u32> {
        BrokerPreparation { cancellation: None }
            .attest_agent(run, profile, digest, candidate, role, tool, prompt)
    }
}

pub(super) struct BrokerPreparation {
    cancellation: Option<AgentCancellation>,
}
impl BrokerPreparation {
    pub fn stage_bundle(
        &self,
        run: &str,
        profile: &str,
        role: Role,
        tool_sha256: &str,
        prompt_sha256: &str,
        bundle: &[u8],
    ) -> io::Result<String> {
        if !id(run, 32)
            || !id(profile, 32)
            || !id(tool_sha256, 64)
            || !id(prompt_sha256, 64)
            || bundle.is_empty()
            || bundle.len() > MAX_BUNDLE
        {
            return Err(refused("broker bundle declaration invalid"));
        }
        let sha256: String = Sha256::digest(bundle)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        let reply: Staged = exchange_controlled(
            json!({"kind":"stage_bundle","version":1,"run":run,"profile":profile,"role":role.as_str(),"tool_sha256":tool_sha256,"prompt_sha256":prompt_sha256,"length":bundle.len(),"sha256":sha256}),
            Some(bundle),
            PREPARE_TIME,
            self.cancellation.clone(),
            MAX_FRAME,
        )?;
        if reply.kind != "staged"
            || reply.version != 1
            || reply.run != run
            || reply.profile != profile
            || reply.role != role.as_str()
            || reply.tool_sha256 != tool_sha256
            || reply.prompt_sha256 != prompt_sha256
            || reply.sha256 != sha256
        {
            return Err(refused("broker staged identity invalid"));
        }
        Ok(sha256)
    }

    pub fn prepare_reviewer(
        &self,
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
    ) -> io::Result<()> {
        if !id(run, 32) || !id(profile, 32) || !id(sha256, 64) || !id(candidate, 40) {
            return Err(refused("broker preparation declaration invalid"));
        }
        let reply: Prepared = exchange_controlled(
            json!({"kind":"prepare_reviewer","version":1,"run":run,"profile":profile,"sha256":sha256,"candidate":candidate}),
            None,
            PREPARE_TIME,
            self.cancellation.clone(),
            MAX_FRAME,
        )?;
        if reply.kind != "prepared"
            || reply.version != 1
            || reply.run != run
            || reply.profile != profile
            || reply.sha256 != sha256
            || reply.candidate != candidate
        {
            return Err(refused("broker prepared identity invalid"));
        }
        Ok(())
    }

    pub fn attest_provider_relay(
        &self,
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        role: Role,
    ) -> io::Result<RelayAttestation> {
        if !id(run, 32) || !id(profile, 32) || !id(sha256, 64) || !id(candidate, 40) {
            return Err(refused("broker relay declaration invalid"));
        }
        let reply: Attested = exchange_controlled(
            json!({"kind":"attest_provider_relay","version":1,"run":run,"profile":profile,"sha256":sha256,"candidate":candidate}),
            None,
            ATTEST_TIME,
            self.cancellation.clone(),
            MAX_FRAME,
        )?;
        let uid = unsafe { libc::geteuid() };
        if reply.kind != "provider_relay_attested"
            || reply.version != 1
            || reply.run != run
            || reply.profile != profile
            || reply.role != role.as_str()
            || reply.candidate != candidate
            || reply.relay_uid == 0
            || reply.relay_uid == uid
            || reply.execution_available
        {
            return Err(refused("broker relay identity invalid"));
        }
        Ok(RelayAttestation {
            run: run.into(),
            profile: profile.into(),
            role,
            candidate: candidate.into(),
            sha256: sha256.into(),
            relay_uid: reply.relay_uid,
        })
    }

    /// Arm only the exact root-owned run socket and immutable role declaration.
    /// The broker remains the sole peer admitted to the worker.
    pub fn arm_agent(
        &self,
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        role: Role,
        tool_sha256: &str,
        prompt_sha256: &str,
    ) -> io::Result<()> {
        if !id(run, 32)
            || !id(profile, 32)
            || !id(sha256, 64)
            || !id(candidate, 40)
            || !id(tool_sha256, 64)
            || !id(prompt_sha256, 64)
        {
            return Err(refused("broker agent declaration invalid"));
        }
        let role = role.as_str();
        let reply: AgentArmed = exchange_controlled(
            json!({"kind":"arm_agent","version":1,"run":run,"profile":profile,"sha256":sha256,"candidate":candidate,"role":role,"tool_sha256":tool_sha256,"prompt_sha256":prompt_sha256}),
            None,
            ATTEST_TIME,
            self.cancellation.clone(),
            MAX_FRAME,
        )?;
        if reply.kind != "agent_armed"
            || reply.version != 1
            || reply.run != run
            || reply.profile != profile
            || reply.candidate != candidate
            || reply.role != role
            || reply.tool_sha256 != tool_sha256
            || reply.prompt_sha256 != prompt_sha256
            || reply.execution_available
        {
            return Err(refused("broker agent identity invalid"));
        }
        Ok(())
    }

    /// Ask the root broker to attest the isolated worker over its private
    /// socket. The desktop cannot connect to that socket directly.
    pub fn attest_agent(
        &self,
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        role: Role,
        tool_sha256: &str,
        prompt_sha256: &str,
    ) -> io::Result<u32> {
        if !id(run, 32)
            || !id(profile, 32)
            || !id(sha256, 64)
            || !id(candidate, 40)
            || !id(tool_sha256, 64)
            || !id(prompt_sha256, 64)
        {
            return Err(refused("agent attestation declaration invalid"));
        }
        let reply: AgentAttested = exchange_controlled(
            json!({"kind":"attest_agent","version":1,"run":run,"profile":profile,"sha256":sha256,"candidate":candidate,"role":role.as_str(),"tool_sha256":tool_sha256,"prompt_sha256":prompt_sha256}),
            None,
            AGENT_TIME,
            self.cancellation.clone(),
            MAX_FRAME,
        )?;
        let uid = unsafe { libc::geteuid() };
        if reply.kind != "agent_attested"
            || reply.version != 1
            || reply.run != run
            || reply.profile != profile
            || reply.candidate != candidate
            || reply.role != role.as_str()
            || reply.tool_sha256 != tool_sha256
            || reply.prompt_sha256 != prompt_sha256
            || reply.worker_uid == 0
            || reply.worker_uid == uid
            || reply.peer_uid != 0
            || !reply.readonly_source
            || reply.coder_workspace != (role == Role::Coder)
            || reply.execution_available
        {
            return Err(refused("agent attested identity invalid"));
        }
        Ok(reply.worker_uid)
    }
}

impl Broker {
    /// One broker-consumed launch. The caller supplies bytes and identities
    /// from an admitted native run; the worker rehashes the prompt and ELF
    /// under its isolated UID. A failed attempt cannot be replayed for this run.
    #[allow(clippy::too_many_arguments)]
    pub fn execute_agent(
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        role: Role,
        tool_sha256: &str,
        prompt_sha256: &str,
        prompt: &[u8],
        executable: &Path,
        placeholder: &str,
        duration: Duration,
    ) -> io::Result<AgentRun> {
        Self::execute_agent_cancellable(
            run,
            profile,
            sha256,
            candidate,
            role,
            tool_sha256,
            prompt_sha256,
            prompt,
            executable,
            placeholder,
            duration,
            AgentCancellation::default(),
        )
    }

    /// Cancel from another thread when the native owner, request or desktop
    /// lifetime ends. A cancelled call closes its broker socket and cannot
    /// publish a late execution receipt. The broker still owns tree retirement.
    #[allow(clippy::too_many_arguments)]
    pub fn execute_agent_cancellable(
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        role: Role,
        tool_sha256: &str,
        prompt_sha256: &str,
        prompt: &[u8],
        executable: &Path,
        placeholder: &str,
        duration: Duration,
        cancellation: AgentCancellation,
    ) -> io::Result<AgentRun> {
        Self::execute_agent_observed(
            run,
            profile,
            sha256,
            candidate,
            role,
            tool_sha256,
            prompt_sha256,
            prompt,
            executable,
            placeholder,
            duration,
            cancellation,
            |_| Ok(()),
        )
    }

    /// Deliver one exact broker-validated process-start observation before
    /// waiting for the terminal receipt. The trusted observer must be bounded;
    /// refusal closes the stream and leaves retirement to the native owner.
    #[allow(clippy::too_many_arguments)]
    pub fn execute_agent_observed(
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        role: Role,
        tool_sha256: &str,
        prompt_sha256: &str,
        prompt: &[u8],
        executable: &Path,
        placeholder: &str,
        duration: Duration,
        cancellation: AgentCancellation,
        on_started: impl FnOnce(&AgentStarted) -> io::Result<()>,
    ) -> io::Result<AgentRun> {
        Self::execute_agent_controlled(
            run,
            profile,
            sha256,
            candidate,
            role,
            tool_sha256,
            prompt_sha256,
            prompt,
            executable,
            placeholder,
            duration,
            cancellation,
            AgentObserver::default(),
            &soda_execution_protocol::HarnessProfile::legacy(),
            on_started,
        )
    }

    #[allow(clippy::too_many_arguments)]
    pub(super) fn execute_agent_controlled(
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        role: Role,
        tool_sha256: &str,
        prompt_sha256: &str,
        prompt: &[u8],
        executable: &Path,
        placeholder: &str,
        duration: Duration,
        cancellation: AgentCancellation,
        observer: AgentObserver,
        harness_profile: &soda_execution_protocol::HarnessProfile,
        on_started: impl FnOnce(&AgentStarted) -> io::Result<()>,
    ) -> io::Result<AgentRun> {
        if !harness_profile.valid() {
            return Err(refused("native harness profile unsupported"));
        }
        if cancellation.cancelled() {
            return Err(refused("agent execution cancelled"));
        }
        if !id(run, 32)
            || !id(profile, 32)
            || !id(sha256, 64)
            || !id(candidate, 40)
            || !id(tool_sha256, 64)
            || !id(prompt_sha256, 64)
            || !id(placeholder, 64)
            || !(1..=MAX_PROMPT).contains(&prompt.len())
            || !executable.is_absolute()
            || duration.is_zero()
            || duration.as_millis() > 4 * 60 * 60 * 1000
        {
            return Err(refused("agent execution declaration invalid"));
        }
        let digest: String = Sha256::digest(prompt)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        if digest != prompt_sha256 {
            return Err(refused("agent prompt differs from admitted digest"));
        }
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(executable)?;
        let metadata = file.metadata()?;
        if !metadata.is_file()
            || metadata.mode() & 0o111 == 0
            || metadata.mode() & 0o022 != 0
            || !(4..=MAX_EXECUTABLE).contains(&metadata.len())
        {
            return Err(refused("agent executable source invalid"));
        }
        let mut stream = connect(Duration::from_secs(120), Some(&cancellation))?;
        stream.set_read_timeout(Some(duration + Duration::from_secs(180)))?;
        let _watch = AgentWatch::start(&stream, cancellation.clone())?;
        let request = serde_json::to_vec(&json!({
            "kind":"execute_agent","version":3,"run":run,"profile":profile,
            "sha256":sha256,"candidate":candidate,"role":role.as_str(),
            "tool_sha256":tool_sha256,"prompt_sha256":prompt_sha256,
            "executable_length":metadata.len(),"prompt_length":prompt.len(),
            "placeholder":placeholder,"duration_ms":duration.as_millis() as u64,
            "harness_profile":harness_profile,
        }))?;
        if request.len() > MAX_FRAME {
            return Err(refused("agent execution request bound invalid"));
        }
        stream.write_all(&(request.len() as u32).to_le_bytes())?;
        stream.write_all(&request)?;
        stream.write_all(prompt)?;
        let sent = io::copy(&mut file.take(metadata.len()), &mut stream)?;
        if sent != metadata.len() {
            return Err(refused("agent executable source changed during transfer"));
        }
        let mut header = [0u8; 4];
        stream.read_exact(&mut header)?;
        let length = u32::from_le_bytes(header) as usize;
        if length == 0 || length > MAX_START_REPLY {
            return Err(refused("agent start announcement bound invalid"));
        }
        let mut bytes = vec![0u8; length];
        stream.read_exact(&mut bytes)?;
        let started: AgentStartedReply = serde_json::from_slice(&bytes)
            .map_err(|_| refused("agent start announcement shape invalid"))?;
        let uid = unsafe { libc::geteuid() };
        if started.kind != "agent_started"
            || started.version != 1
            || started.run != run
            || started.profile != profile
            || started.candidate != candidate
            || started.role != role.as_str()
            || started.tool_sha256 != tool_sha256
            || started.prompt_sha256 != prompt_sha256
            || started.worker_uid == 0
            || started.worker_uid == uid
            || started.peer_uid != 0
            || !id(&started.invocation, 32)
            || !boot_id(&started.system_instance)
            || !matches!(started.start_identity.parse::<u64>(), Ok(ticks) if ticks > 0)
            || started.namespace_pid == 0
            || started.started_unix_ms == 0
            || cancellation.cancelled()
        {
            return Err(refused("agent start announcement identity invalid"));
        }
        let started = AgentStarted {
            worker_uid: started.worker_uid,
            process: AgentProcess {
                invocation: started.invocation,
                system_instance: started.system_instance,
                start_identity: started.start_identity,
                namespace_pid: started.namespace_pid,
                started_unix_ms: started.started_unix_ms,
            },
        };
        let observation = observer.attach(&stream, &started)?;
        on_started(&started)?;
        if cancellation.cancelled() {
            return Err(refused("agent execution cancelled"));
        }
        let bytes = loop {
            stream.read_exact(&mut header)?;
            let length = u32::from_le_bytes(header) as usize;
            if length == 0 || length > MAX_EXEC_REPLY {
                return Err(refused("agent execution reply bound invalid"));
            }
            let mut bytes = vec![0u8; length];
            stream.read_exact(&mut bytes)?;
            let value: Value = serde_json::from_slice(&bytes)?;
            if value["kind"] != "agent_observed" {
                break bytes;
            }
            let reply: AgentObserved = serde_json::from_slice(&bytes)?;
            if reply.kind != "agent_observed" || reply.version != 1 || cancellation.cancelled() {
                return Err(refused("agent observation reply invalid"));
            }
            observation.deliver(reply.sequence, reply.live)?;
        };
        let reply: AgentExecuted = serde_json::from_slice(&bytes)
            .map_err(|_| refused("agent execution reply shape invalid"))?;
        let uid = unsafe { libc::geteuid() };
        if reply.kind != "agent_executed"
            || reply.version != 3
            || reply.run != run
            || reply.profile != profile
            || reply.candidate != candidate
            || reply.role != role.as_str()
            || reply.tool_sha256 != tool_sha256
            || reply.prompt_sha256 != prompt_sha256
            || reply.worker_uid == 0
            || reply.worker_uid == uid
            || reply.peer_uid != 0
            || !id(&reply.invocation, 32)
            || !boot_id(&reply.system_instance)
            || !matches!(reply.start_identity.parse::<u64>(), Ok(ticks) if ticks > 0)
            || reply.namespace_pid == 0
            || reply.started_unix_ms == 0
            || reply.worker_uid != started.worker_uid
            || reply.invocation != started.process.invocation
            || reply.system_instance != started.process.system_instance
            || reply.start_identity != started.process.start_identity
            || reply.namespace_pid != started.process.namespace_pid
            || reply.started_unix_ms != started.process.started_unix_ms
            || !reply.tree_retired
            || reply.output_length > MAX_OUTPUT
            || !id(&reply.output_sha256, 64)
            || !reply.candidate_valid()
            || reply
                .exit_code
                .is_some_and(|code| !(0..=255).contains(&code))
            || reply
                .exit_signal
                .is_some_and(|signal| !(1..=64).contains(&signal))
            || reply.exit_code.is_some() && reply.exit_signal.is_some()
            || reply.reason == "exited" && reply.exit_code.is_none() && reply.exit_signal.is_none()
            || !matches!(
                reply.reason.as_str(),
                "exited"
                    | "cancelled"
                    | "deadline"
                    | "output_limit"
                    | "output_unavailable"
                    | "input_unavailable"
                    | "observation_unavailable"
                    | "unconfirmed"
            )
        {
            return Err(refused("agent execution identity invalid"));
        }
        let mut output = vec![0u8; reply.output_length];
        stream.read_exact(&mut output)?;
        let observed: String = Sha256::digest(&output)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        if observed != reply.output_sha256 {
            return Err(refused("agent execution output digest invalid"));
        }
        let mut bundle = vec![0u8; reply.bundle_length];
        stream.read_exact(&mut bundle)?;
        if reply.bundle_sha256.as_deref().is_some_and(|digest| {
            let observed: String = Sha256::digest(&bundle)
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect();
            observed != digest
        }) {
            return Err(refused("agent candidate bundle digest invalid"));
        }
        let run = AgentRun {
            worker_uid: reply.worker_uid,
            process: AgentProcess {
                invocation: reply.invocation,
                system_instance: reply.system_instance,
                start_identity: reply.start_identity,
                namespace_pid: reply.namespace_pid,
                started_unix_ms: reply.started_unix_ms,
            },
            reason: reply.reason,
            exit_code: reply.exit_code,
            exit_signal: reply.exit_signal,
            output_complete: reply.output_complete,
            output,
            candidate_state: reply.candidate_state,
            result_candidate: reply.result_candidate,
            candidate_bundle: bundle,
            bundle_sha256: reply.bundle_sha256,
        };
        if cancellation.cancelled() {
            Err(refused("agent execution cancelled"))
        } else {
            observation.complete();
            Ok(run)
        }
    }

    /// Stop the exact relay and prepared checkout through the broker after the
    /// local owner is stopped. A failed retirement must be retried with the
    /// same digest; a different digest is never authority to clean this run.
    fn retire_request(kind: &str, run: &str, profile: &str, sha256: &str) -> io::Result<bool> {
        if !id(run, 32) || !id(profile, 32) || !id(sha256, 64) {
            return Err(refused("broker retirement declaration invalid"));
        }
        let reply: Retired = exchange(
            json!({"kind":kind,"version":1,"run":run,"profile":profile,"sha256":sha256}),
            None,
            PREPARE_TIME,
        )?;
        if reply.kind != "retired"
            || reply.version != 1
            || reply.run != run
            || reply.profile != profile
        {
            return Err(refused("broker retired identity invalid"));
        }
        Ok(reply.removed)
    }

    /// Setup cleanup may retire only a run whose launch marker is absent.
    /// The broker checks this under its root run lock before removing state.
    pub fn retire_unlaunched(run: &str, profile: &str, sha256: &str) -> io::Result<bool> {
        Self::retire_request("retire_unlaunched", run, profile, sha256)
    }

    pub(super) fn retire_launched(run: &str, profile: &str, sha256: &str) -> io::Result<bool> {
        Self::retire_request("retire_launched", run, profile, sha256)
    }

    fn finish_retiring(run: &str, profile: &str, sha256: &str) -> io::Result<bool> {
        Self::retire_request("finish_retiring", run, profile, sha256)
    }

    /// After a durable receipt and credential withdrawal, retire the exact
    /// launched run or finish an interrupted root retirement.
    pub fn retire_bundle(run: &str, profile: &str, sha256: &str) -> io::Result<bool> {
        Self::retire_request("retire_bundle", run, profile, sha256)
    }

    /// Stop the exact agent tree and relay while retaining its root staging.
    /// An unrecorded or cancelled execution must remain available for
    /// conservative recovery rather than being mistaken for a settled run.
    pub fn quiesce_run(run: &str, profile: &str, sha256: &str) -> io::Result<()> {
        if !id(run, 32) || !id(profile, 32) || !id(sha256, 64) {
            return Err(refused("broker quiescence declaration invalid"));
        }
        let reply: Quiesced = exchange(
            json!({"kind":"quiesce_run","version":1,"run":run,"profile":profile,"sha256":sha256}),
            None,
            PREPARE_TIME,
        )?;
        if reply.kind != "quiesced"
            || reply.version != 1
            || reply.run != run
            || reply.profile != profile
            || reply.sha256 != sha256
        {
            return Err(refused("broker quiescence identity invalid"));
        }
        Ok(())
    }
}
