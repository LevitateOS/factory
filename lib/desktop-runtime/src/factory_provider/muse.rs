//! Native owner of the fixed CLI-managed provider connector. It has no generic
//! command/argv operation and never opens Muse's authentication store.
use sha2::{Digest, Sha256};
use soda_execution_protocol::{MuseInvocation, MuseObservation, MAX_MUSE_INPUT, MAX_OUTPUT};
use std::fs::{DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, ChildStdout, Command, Stdio};
use std::time::{Duration, Instant};

fn refused(message: &'static str) -> io::Error {
    io::Error::other(message)
}
fn private(path: &Path) -> io::Result<()> {
    let meta = path.symlink_metadata()?;
    if !meta.is_dir()
        || meta.uid() != unsafe { libc::geteuid() }
        || meta.mode() & 0o077 != 0
        || path.canonicalize()? != path
    {
        return Err(refused("Muse native storage identity invalid"));
    }
    Ok(())
}

pub struct MuseConnector {
    child: Child,
    input: Option<ChildStdin>,
    output: ChildStdout,
    buffer: Vec<u8>,
    unit: String,
    directory: PathBuf,
    run: String,
    service: String,
    tool_sha256: String,
    prompt_sha256: String,
    output_limit: usize,
    terminal: Option<(MuseObservation, Vec<u8>)>,
    started: Option<(String, String, String, u32, u64)>,
    sequence: u64,
    retired: bool,
}

impl MuseConnector {
    /// Native-only composition supplies the installed helper and officially
    /// verified CLI selection. Neither path is accepted through browser IPC.
    /// The owning execution registry must retain this handle until settlement.
    pub fn start(profile: &Path, helper: &Path, mut request: MuseInvocation) -> io::Result<Self> {
        private(profile)?;
        let helper = helper.canonicalize()?;
        let meta = helper.symlink_metadata()?;
        if !meta.is_file()
            || meta.mode() & 0o022 != 0
            || meta.mode() & 0o111 == 0
            || !matches!(meta.uid(), 0) && meta.uid() != unsafe { libc::geteuid() }
        {
            return Err(refused("Muse native helper identity invalid"));
        }
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).map_err(io::Error::other)?;
        request.service = random.iter().map(|b| format!("{b:02x}")).collect();
        if !request.valid() {
            return Err(refused("Muse native invocation invalid"));
        }
        let runs = profile.join("native-muse-runs");
        match DirBuilder::new().mode(0o700).create(&runs) {
            Ok(()) => File::open(profile)?.sync_all()?,
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => (),
            Err(e) => return Err(e),
        }
        private(&runs)?;
        let directory = runs.join(&request.service);
        DirBuilder::new().mode(0o700).create(&directory)?;
        File::open(&runs)?.sync_all()?;
        let stderr = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(directory.join("connector.log"))?;
        let encoded = serde_json::to_vec(&request)?;
        if encoded.len() > MAX_MUSE_INPUT {
            return Err(refused("Muse native input bound exceeded"));
        }
        let unit = format!(
            "soda-factory-muse-{}-{}.service",
            request.run, request.service
        );
        let prompt_sha256: String = Sha256::digest(request.prompt.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        // Persist the exact owned service before starting it; a lost reply must
        // leave a recoverable identity, not authorize a second inference.
        let binding = serde_json::json!({"kind":"factory_muse_binding","version":1,
            "run":request.run,"service":request.service,"unit":unit,
            "executable_sha256":request.executable_sha256,"prompt_sha256":prompt_sha256});
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(directory.join("binding.json"))?;
        file.write_all(&serde_json::to_vec(&binding)?)?;
        file.sync_all()?;
        File::open(&directory)?.sync_all()?;
        let mut command = Command::new("/usr/bin/systemd-run");
        command
            .args([
                "--user",
                "--pipe",
                "--wait",
                "--collect",
                "--quiet",
                "--service-type=exec",
            ])
            .arg(format!("--unit={unit}"))
            .args([
                "--property=Delegate=cpu memory pids",
                "--property=DelegateSubgroup=init",
                "--property=KillMode=control-group",
                "--property=TimeoutStopSec=10s",
                "--property=UMask=0077",
                "--property=LimitCORE=0",
            ])
            .arg(format!(
                "--property=RuntimeMaxSec={}s",
                request.duration_ms.div_ceil(1000) + 150
            ))
            .arg(format!("--working-directory={}", directory.display()));
        for name in [
            "HOME",
            "USER",
            "LOGNAME",
            "XDG_CONFIG_HOME",
            "XDG_DATA_HOME",
            "XDG_CACHE_HOME",
            "XDG_STATE_HOME",
            "XDG_RUNTIME_DIR",
            "DBUS_SESSION_BUS_ADDRESS",
        ] {
            if let Some(value) = std::env::var_os(name) {
                let mut argument = std::ffi::OsString::from(format!("--setenv={name}="));
                argument.push(value);
                command.arg(argument);
            }
        }
        command
            .arg("--")
            .arg(&helper)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(stderr);
        let mut child = command.spawn()?;
        let output = child
            .stdout
            .take()
            .ok_or_else(|| refused("Muse connector output missing"))?;
        let input = child
            .stdin
            .take()
            .ok_or_else(|| refused("Muse connector input missing"))?;
        let mut owner = Self {
            child,
            input: Some(input),
            output,
            buffer: Vec::new(),
            unit,
            directory,
            run: request.run,
            service: request.service,
            tool_sha256: request.executable_sha256,
            prompt_sha256,
            output_limit: request.output_bytes,
            terminal: None,
            started: None,
            sequence: 0,
            retired: false,
        };
        let flags = unsafe { libc::fcntl(owner.output.as_raw_fd(), libc::F_GETFL) };
        if flags < 0
            || unsafe {
                libc::fcntl(
                    owner.output.as_raw_fd(),
                    libc::F_SETFL,
                    flags | libc::O_NONBLOCK,
                )
            } < 0
        {
            return Err(io::Error::last_os_error());
        }
        let input = owner.input.as_mut().expect("owned connector stdin");
        input.write_all(&(encoded.len() as u32).to_be_bytes())?;
        input.write_all(&encoded)?;
        input.flush()?;
        Ok(owner)
    }

    pub fn directory(&self) -> &Path {
        &self.directory
    }
    pub fn cancel(&mut self) -> io::Result<()> {
        if let Some(input) = &mut self.input {
            input.write_all(b"C")?;
            input.flush()?;
        }
        Ok(())
    }

    pub fn inspect(&mut self, timeout: Duration) -> io::Result<Option<MuseObservation>> {
        if self.terminal.is_some() {
            return Ok(None);
        }
        if timeout.is_zero()
            || timeout > Duration::from_secs(5)
            || self.sequence >= soda_execution_protocol::MAX_OBSERVATION_QUERIES
        {
            return Err(refused("Muse observation bounds invalid"));
        }
        self.sequence += 1;
        if let Some(input) = &mut self.input {
            input.write_all(b"O")?;
            input.write_all(&self.sequence.to_le_bytes())?;
            input.flush()?;
        }
        let deadline = Instant::now() + timeout;
        while self.terminal.is_none() && Instant::now() < deadline {
            if let Some(value) = self.observe(deadline.saturating_duration_since(Instant::now()))? {
                if value.sequence == self.sequence && value.live.is_some() {
                    return Ok(Some(value));
                }
            }
        }
        if self.terminal.is_some() {
            Ok(None)
        } else {
            Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "Muse process query timed out",
            ))
        }
    }

    /// Wait for one native observation. A timeout retains partial frames and
    /// does not cancel the process or admit another run.
    pub fn observe(&mut self, timeout: Duration) -> io::Result<Option<MuseObservation>> {
        if self.terminal.is_some() {
            return Err(refused("Muse terminal observation already retained"));
        }
        let deadline = Instant::now() + timeout;
        loop {
            if self.buffer.len() >= 4 {
                let length =
                    u32::from_be_bytes(self.buffer[..4].try_into().expect("length")) as usize;
                if !(1..=4096).contains(&length) {
                    return Err(refused("Muse observation header bound exceeded"));
                }
                if self.buffer.len() >= 4 + length {
                    let observation: MuseObservation =
                        serde_json::from_slice(&self.buffer[4..4 + length])?;
                    self.validate(&observation)?;
                    let total = 4 + length + observation.output_length;
                    if self.buffer.len() >= total {
                        let output = self.buffer[4 + length..total].to_vec();
                        self.buffer.drain(..total);
                        if observation.finished {
                            let digest: String = Sha256::digest(&output)
                                .iter()
                                .map(|b| format!("{b:02x}"))
                                .collect();
                            if observation.output_sha256.as_deref() != Some(&digest) {
                                return Err(refused("Muse observation output digest differs"));
                            }
                            self.terminal = Some((observation, output));
                            return Ok(None);
                        }
                        return Ok(Some(observation));
                    }
                }
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Ok(None);
            }
            let mut event = libc::pollfd {
                fd: self.output.as_raw_fd(),
                events: libc::POLLIN,
                revents: 0,
            };
            let ready =
                unsafe { libc::poll(&mut event, 1, remaining.as_millis().min(1000) as i32) };
            if ready < 0 {
                if io::Error::last_os_error().kind() == io::ErrorKind::Interrupted {
                    continue;
                }
                return Err(io::Error::last_os_error());
            }
            if ready == 0 {
                continue;
            }
            let mut bytes = [0u8; 65536];
            match self.output.read(&mut bytes) {
                Ok(0) => {
                    return Err(refused(
                        "Muse connector closed without a complete observation",
                    ))
                }
                Ok(count) => {
                    if self.buffer.len() + count > MAX_OUTPUT + 8192 {
                        return Err(refused("Muse observation bound exceeded"));
                    }
                    self.buffer.extend_from_slice(&bytes[..count]);
                }
                Err(e)
                    if matches!(
                        e.kind(),
                        io::ErrorKind::WouldBlock | io::ErrorKind::Interrupted
                    ) =>
                {
                    ()
                }
                Err(e) => return Err(e),
            }
        }
    }

    fn validate(&mut self, observation: &MuseObservation) -> io::Result<()> {
        if observation.kind != "factory_muse_process"
            || observation.version != 1
            || observation.run != self.run
            || observation.service != self.service
            || observation.executable_sha256 != self.tool_sha256
            || observation.prompt_sha256 != self.prompt_sha256
            || observation.uid != unsafe { libc::geteuid() }
            || observation.sequence > self.sequence
            || observation.finished
                && (observation.sequence != 0 || observation.live != Some(false))
            || !observation.finished && (observation.sequence == 0) != observation.live.is_none()
            || observation.pid == 0
            || !super::hex(&observation.invocation, 32)
            || observation.system_instance.len() != 36
            || !observation
                .start_identity
                .parse::<u64>()
                .is_ok_and(|n| n > 0)
            || observation.output_length > self.output_limit
            || !observation.finished
                && (observation.output_length != 0
                    || observation.output_sha256.is_some()
                    || observation.tree_retired
                    || observation.output_complete
                    || observation.reason.is_some()
                    || observation.exit_code.is_some()
                    || observation.exit_signal.is_some())
        {
            return Err(refused(
                "Muse native observation differs from admitted process",
            ));
        }
        let identity = (
            observation.invocation.clone(),
            observation.system_instance.clone(),
            observation.start_identity.clone(),
            observation.pid,
            observation.started_unix_ms,
        );
        if self
            .started
            .as_ref()
            .is_some_and(|started| started != &identity)
        {
            return Err(refused("Muse native process identity changed"));
        }
        self.started = Some(identity);
        Ok(())
    }

    pub fn terminal(&self) -> Option<&MuseObservation> {
        self.terminal.as_ref().map(|(state, _)| state)
    }

    /// Consume a complete result only after helper exit and confirmed child
    /// retirement. These facts do not assert a model or product success.
    pub fn finish(mut self) -> io::Result<(MuseObservation, Vec<u8>)> {
        let deadline = Instant::now() + Duration::from_secs(12);
        while self.terminal.is_none() && Instant::now() < deadline {
            self.observe(deadline.saturating_duration_since(Instant::now()))?;
        }
        let terminal = self
            .terminal
            .as_ref()
            .ok_or_else(|| refused("Muse native result unavailable"))?;
        if !terminal.0.tree_retired || !terminal.0.output_complete {
            return Err(refused("Muse native process retirement unconfirmed"));
        }
        self.input.take();
        while Instant::now() < deadline {
            if let Some(status) = self.child.try_wait()? {
                if !status.success() {
                    return Err(refused("Muse connector retirement failed"));
                }
                self.retired = true;
                return Ok(self.terminal.take().expect("retained terminal"));
            }
            std::thread::sleep(Duration::from_millis(10));
        }
        Err(refused("Muse connector exit unconfirmed"))
    }
}

impl Drop for MuseConnector {
    fn drop(&mut self) {
        if self.retired {
            return;
        }
        let _ = self.cancel();
        self.input.take();
        // The fresh native service name was retained before launch. Stop only
        // that service, including descendants if a protocol reply was lost.
        let stop = Command::new("/usr/bin/systemctl")
            .args(["--user", "stop", "--", &self.unit])
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn();
        if let Ok(mut stop) = stop {
            let until = Instant::now() + Duration::from_secs(12);
            while Instant::now() < until {
                if stop.try_wait().ok().flatten().is_some() {
                    break;
                }
                std::thread::sleep(Duration::from_millis(20));
            }
            let _ = stop.kill();
            let _ = stop.wait();
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
