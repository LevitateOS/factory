//! Fixed trusted provider connector. Untrusted workspace commands remain in the
//! isolated worker; this process lets Muse read its own saved authentication.
//! Launched only by the native owner in a dedicated delegated user service.
#[cfg(target_os = "linux")]
mod connector {
    use sha2::{Digest, Sha256};
    use soda_execution_protocol::{MuseInvocation, MuseObservation, MAX_MUSE_INPUT};
    use soda_native_process::{Executable, Invocation, Limits, Observation, Processes};
    use std::collections::BTreeMap;
    use std::ffi::{OsStr, OsString};
    use std::fs::{self, File, OpenOptions};
    use std::io::{self, Read, Write};
    use std::os::fd::FromRawFd;
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
    use std::path::{Component, Path};
    use std::time::{Duration, Instant, UNIX_EPOCH};

    fn refused(message: &'static str) -> io::Error {
        io::Error::new(io::ErrorKind::PermissionDenied, message)
    }
    fn digest(bytes: &[u8]) -> String {
        Sha256::digest(bytes)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    }
    fn private(path: &Path) -> io::Result<()> {
        let meta = path.symlink_metadata()?;
        if !meta.is_dir()
            || meta.uid() != unsafe { libc::geteuid() }
            || meta.mode() & 0o077 != 0
            || path.canonicalize()? != path
        {
            return Err(refused("Muse connector storage identity invalid"));
        }
        Ok(())
    }
    fn create(path: &Path, bytes: &[u8]) -> io::Result<()> {
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(path)?;
        file.write_all(bytes)?;
        file.sync_all()
    }
    fn process_owner(root: &Path, run: &str, service: &str) -> io::Result<Processes> {
        let value = fs::read_to_string("/proc/self/cgroup")?;
        let path = value
            .strip_prefix("0::")
            .and_then(|v| v.strip_suffix('\n'))
            .ok_or_else(|| refused("Muse connector cgroup declaration invalid"))?;
        let child = Path::new(path);
        let parent = child
            .parent()
            .ok_or_else(|| refused("Muse connector delegation missing"))?;
        let unit = format!("soda-factory-muse-{run}-{service}.service");
        if value.len() > 4096
            || path.contains('\n')
            || !child.is_absolute()
            || child.file_name() != Some(OsStr::new("init"))
            || parent.file_name() != Some(OsStr::new(&unit))
            || child
                .components()
                .any(|c| !matches!(c, Component::RootDir | Component::Normal(_)))
        {
            return Err(refused("Muse connector delegated service identity invalid"));
        }
        let delegated =
            Path::new("/sys/fs/cgroup").join(parent.strip_prefix("/").map_err(io::Error::other)?);
        let meta = delegated.symlink_metadata()?;
        if !meta.is_dir()
            || meta.uid() != unsafe { libc::geteuid() }
            || !fs::read_to_string(delegated.join("cgroup.procs"))?
                .trim()
                .is_empty()
        {
            return Err(refused("Muse connector delegation owner invalid"));
        }
        OpenOptions::new()
            .write(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(delegated.join("cgroup.subtree_control"))?
            .write_all(b"+cpu +memory +pids")?;
        Processes::open(root, &delegated, run).map_err(io::Error::other)
    }

    fn environment(tmp: &Path, request: &MuseInvocation) -> BTreeMap<OsString, OsString> {
        let mut environment = BTreeMap::from([
            (OsString::from("PATH"), OsString::from("/usr/bin:/bin")),
            (OsString::from("LANG"), OsString::from("C.UTF-8")),
            (OsString::from("TMPDIR"), tmp.as_os_str().to_owned()),
        ]);
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
                environment.insert(name.into(), value);
            }
        }
        if let Some(mcp) = &request.mcp {
            environment.insert(
                "SODA_FACTORY_RUN_AUTH".into(),
                format!("Bearer {}", mcp.authorization).into(),
            );
        }
        environment
    }
    fn arguments(root: &Path, request: &MuseInvocation) -> io::Result<Vec<OsString>> {
        let mut arguments: Vec<OsString> = [
            "exec",
            "--provider",
            "meta",
            "--preset",
            "empty",
            "--json",
            "--worktree",
            "off",
            "--no-session-log",
            "--disable-write",
            "--disable-shell",
            "--disable-web-tools",
            "--disable-reminders",
            "--disable-request-user-input",
            "--no-foreign-personal-context",
            "--approval-mode",
            "never",
            "--approval-judge",
            "off",
            "--max-tool-output-bytes",
            "65536",
            "--no-parallel-tool-calls",
            "-c",
            "hooks={}",
            "-c",
            "managed_hooks_path=null",
        ]
        .into_iter()
        .map(OsString::from)
        .collect();
        arguments.extend([
            OsString::from("--workspace"),
            root.join("work").into_os_string(),
            OsString::from("--prompt-file"),
            root.join("prompt").into_os_string(),
            OsString::from("--model"),
            request.harness.model.clone().into(),
            OsString::from("--reasoning-effort"),
            request.harness.effort.clone().into(),
            OsString::from("--contributor-mode"),
            if request.harness.contributor {
                "on"
            } else {
                "off"
            }
            .into(),
            OsString::from("--max-model-steps"),
            request.max_model_steps.to_string().into(),
        ]);
        let tools: Vec<String> = request
            .mcp
            .as_ref()
            .map(|mcp| {
                mcp.tools
                    .iter()
                    .map(|tool| format!("mcp__factory__{tool}"))
                    .collect()
            })
            .unwrap_or_default();
        arguments.extend([
            OsString::from("-c"),
            format!("run.toolset={}", serde_json::to_string(&tools)?).into(),
        ]);
        if let Some(mcp) = &request.mcp {
            let server = serde_json::json!({"enabled":true,"transport":"streamable_http",
                "url":format!("http://127.0.0.1:{}/mcp",mcp.port),
                "headers":{"Authorization":"${SODA_FACTORY_RUN_AUTH}"}});
            arguments.extend([
                OsString::from("-c"),
                format!("mcpServers.factory={server}").into(),
            ]);
        }
        Ok(arguments)
    }
    fn observation(
        request: &MuseInvocation,
        state: &Observation,
        output: Option<&[u8]>,
    ) -> io::Result<MuseObservation> {
        Ok(MuseObservation {
            kind: "factory_muse_process".into(),
            version: 1,
            run: request.run.clone(),
            service: request.service.clone(),
            sequence: 0,
            live: state.finished.then_some(false),
            executable_sha256: state.executable_sha256.clone(),
            prompt_sha256: digest(request.prompt.as_bytes()),
            invocation: state.invocation.clone(),
            system_instance: state.system_instance.clone(),
            start_identity: state.start_identity.clone(),
            pid: state.pid,
            uid: unsafe { libc::geteuid() },
            started_unix_ms: state
                .started_at
                .duration_since(UNIX_EPOCH)
                .map_err(io::Error::other)?
                .as_millis()
                .try_into()
                .map_err(io::Error::other)?,
            stopped_unix_ms: state
                .stopped_at
                .map(|stopped| {
                    stopped
                        .duration_since(UNIX_EPOCH)
                        .map_err(io::Error::other)?
                        .as_millis()
                        .try_into()
                        .map_err(io::Error::other)
                })
                .transpose()?,
            finished: state.finished,
            tree_retired: state.finished && state.tree_retired,
            output_complete: state.finished && state.output_complete,
            output_length: output.map_or(0, <[u8]>::len),
            output_sha256: output.map(digest),
            exit_code: state
                .finished
                .then_some(state.exit)
                .flatten()
                .and_then(|e| e.code),
            exit_signal: state
                .finished
                .then_some(state.exit)
                .flatten()
                .and_then(|e| e.signal),
            reason: state
                .finished
                .then_some(state.reason)
                .flatten()
                .map(|reason| {
                    match reason {
                        soda_native_process::StopReason::Exited => "exited",
                        soda_native_process::StopReason::Cancelled => "cancelled",
                        soda_native_process::StopReason::Deadline => "deadline",
                        soda_native_process::StopReason::OutputLimit => "output_limit",
                        soda_native_process::StopReason::OutputUnavailable => "output_unavailable",
                        soda_native_process::StopReason::InputUnavailable => "input_unavailable",
                        soda_native_process::StopReason::ObservationUnavailable => {
                            "observation_unavailable"
                        }
                    }
                    .into()
                }),
        })
    }
    fn reply(output: &mut impl Write, value: &MuseObservation, bytes: &[u8]) -> io::Result<()> {
        let header = serde_json::to_vec(value)?;
        output.write_all(&(header.len() as u32).to_be_bytes())?;
        output.write_all(&header)?;
        output.write_all(bytes)?;
        output.flush()
    }

    pub fn run() -> io::Result<()> {
        unsafe {
            libc::umask(0o077);
        }
        if std::env::args_os().len() != 1 || unsafe { libc::geteuid() } == 0 {
            return Err(refused("Muse connector requires the desktop user"));
        }
        let root = std::env::current_dir()?;
        private(&root)?;
        let descriptor = unsafe { libc::dup(0) };
        if descriptor < 0 {
            return Err(io::Error::last_os_error());
        }
        let mut input = unsafe { File::from_raw_fd(descriptor) };
        let mut length = [0u8; 4];
        input.read_exact(&mut length)?;
        let length = u32::from_be_bytes(length) as usize;
        if !(1..=MAX_MUSE_INPUT).contains(&length) {
            return Err(refused("Muse connector input bound exceeded"));
        }
        let mut bytes = vec![0; length];
        input.read_exact(&mut bytes)?;
        let request: MuseInvocation = serde_json::from_slice(&bytes)?;
        if !request.valid() {
            return Err(refused("Muse connector input invalid"));
        }
        let mut processes = process_owner(&root, &request.run, &request.service)?;
        let executable =
            Executable::capture(Path::new(&request.executable)).map_err(io::Error::other)?;
        if executable.sha256() != request.executable_sha256 {
            return Err(refused("Muse executable differs from captured admission"));
        }
        for name in ["tmp", "work"] {
            fs::create_dir(root.join(name))?;
            private(&root.join(name))?;
        }
        create(&root.join("prompt"), request.prompt.as_bytes())?;
        let now = std::time::SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(io::Error::other)?
            .as_millis() as u64;
        let remaining = request
            .deadline_unix_ms
            .checked_sub(now)
            .filter(|value| *value > 0)
            .ok_or_else(|| refused("Muse admitted deadline expired before execution"))?
            .min(request.duration_ms);
        let process = processes
            .launch(Invocation {
                executable: &executable,
                directory: &root.join("work"),
                arguments: arguments(&root, &request)?,
                environment: environment(&root.join("tmp"), &request),
                limits: Limits {
                    duration: Duration::from_millis(remaining),
                    output_bytes: request.output_bytes,
                    memory_bytes: 768 * 1024 * 1024,
                    processes: 64,
                    cpu_percent: 100,
                },
                columns: 80,
                rows: 24,
            })
            .map_err(io::Error::other)?;
        let mut output = io::stdout().lock();
        let started = process.observe();
        if !started.finished {
            reply(&mut output, &observation(&request, &started, None)?, &[])?;
        }
        let deadline = Instant::now() + Duration::from_millis(remaining) + Duration::from_secs(10);
        let result = (|| {
            let mut sequence = 0u64;
            loop {
                let state = process.observe();
                if state.finished {
                    let bytes = fs::read(&state.output)?;
                    if bytes.len() != state.output_bytes || bytes.len() > request.output_bytes {
                        return Err(refused("Muse captured output differs"));
                    }
                    let terminal = observation(&request, &state, Some(&bytes))?;
                    create(&root.join("result.json"), &serde_json::to_vec(&terminal)?)?;
                    File::open(&root)?.sync_all()?;
                    reply(&mut output, &terminal, &bytes)?;
                    return Ok(());
                }
                if Instant::now() >= deadline {
                    return Err(refused("Muse retirement deadline exceeded"));
                }
                let mut event = libc::pollfd {
                    fd: 0,
                    events: libc::POLLIN,
                    revents: 0,
                };
                let ready = unsafe { libc::poll(&mut event, 1, 20) };
                if ready < 0 {
                    if io::Error::last_os_error().kind() == io::ErrorKind::Interrupted {
                        continue;
                    }
                    return Err(io::Error::last_os_error());
                }
                if ready > 0 {
                    let mut command = [0u8; 1];
                    if input.read_exact(&mut command).is_err() {
                        process.cancel();
                        break;
                    }
                    match command[0] {
                        b'O' => {
                            let mut bytes = [0u8; 8];
                            input.read_exact(&mut bytes)?;
                            let next = u64::from_le_bytes(bytes);
                            if next != sequence + 1
                                || next > soda_execution_protocol::MAX_OBSERVATION_QUERIES
                            {
                                return Err(refused("Muse observation sequence invalid"));
                            }
                            sequence = next;
                            let live = process.is_live().map_err(io::Error::other)?;
                            let state = process.observe();
                            if state.finished {
                                continue;
                            }
                            let mut value = observation(&request, &state, None)?;
                            value.sequence = sequence;
                            value.live = Some(live);
                            reply(&mut output, &value, &[])?;
                        }
                        b'C' => process.cancel(),
                        _ => return Err(refused("Muse connector control invalid")),
                    }
                }
            }
            // A vanished parent still requires actual process retirement. Its
            // disconnected pipe cannot acknowledge or erase the retained facts.
            let state = process.wait(Duration::from_secs(10));
            if !state.finished {
                return Err(refused("Muse disconnected retirement unconfirmed"));
            }
            let bytes = fs::read(&state.output)?;
            create(
                &root.join("result.json"),
                &serde_json::to_vec(&observation(&request, &state, Some(&bytes))?)?,
            )?;
            File::open(&root)?.sync_all()
        })();
        process.cancel();
        let retirement = processes.retire().map_err(io::Error::other);
        result.and(retirement)
    }
}

fn main() {
    #[cfg(target_os = "linux")]
    if let Err(error) = connector::run() {
        eprintln!("Factory Muse connector: {error}");
        std::process::exit(1);
    }
    #[cfg(not(target_os = "linux"))]
    std::process::exit(1);
}
