//! One consumed connection to the existing isolated agent. The broker captures
//! exact manifest bytes before opening task effects; it does not schedule tasks.
use super::*;
use soda_execution_protocol::workspace::{Command as WorkspaceCommand, MAX_COMMAND, MAX_REPLY};

pub(super) fn serve<R: Read, W: Write>(
    input: &mut R,
    output: &mut W,
    run: &str,
    profile: &str,
    source_sha: &str,
    candidate: &str,
    tool: &str,
    prompt: &str,
    manifest_digest: &str,
    manifest: &[u8],
    invocation: &str,
) -> io::Result<()> {
    {
        let runs = state()?;
        let (_lock, _run_lock) = lock_run(&runs, run)?;
        let directory = runs.join(run);
        private_dir(&directory, 0o555)?;
        verified_profile(&directory, profile)?;
        verified_role(&directory, "coder")?;
        verified_digest(&directory, "owner.tool_sha256", tool)?;
        verified_digest(&directory, "owner.prompt_sha256", prompt)?;
        if !verified_bundle(&directory, source_sha)? {
            return Err(denied("task source missing"));
        }
        if run_lifecycle(&directory)? != Some(false) {
            return Err(denied("task source already launched"));
        }
        // The broker has no DAC override capability. Unlock only this exact
        // unlaunched run under its staging lock and restore its immutable mode.
        fs::set_permissions(&directory, fs::Permissions::from_mode(0o700))?;
        let captured = (|| {
            let mut file = OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o444)
                .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                .open(directory.join("task.manifest"))?;
            file.write_all(manifest)?;
            file.sync_all()?;
            file.set_permissions(fs::Permissions::from_mode(0o444))?;
            File::open(&directory)?.sync_all()
        })();
        let restored = fs::set_permissions(&directory, fs::Permissions::from_mode(0o555));
        captured.and(restored)?;
    }
    arm_agent(
        run, profile, source_sha, candidate, "coder", tool, prompt, true,
    )?;
    let directory = PathBuf::from(STATE).join("preparations").join(run);
    let result = (|| {
        let path = PathBuf::from(format!("/run/soda-factory/agents/{run}.sock"));
        let mut worker = UnixStream::connect(&path)?;
        worker.set_read_timeout(Some(agent_start_time(run)?))?;
        worker.set_write_timeout(Some(Duration::from_secs(3)))?;
        let mut ready = [0];
        worker.read_exact(&mut ready)?;
        if ready != *b"R" {
            return Err(denied("task worker readiness invalid"));
        }
        mark_root_launch(run, profile, source_sha)?;
        let mut marker = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o444)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(directory.join("agent.launch"))?;
        marker.write_all(b"consumed\n")?;
        marker.sync_all()?;
        marker.set_permissions(fs::Permissions::from_mode(0o444))?;
        File::open(&directory)?.sync_all()?;
        manager_unit("stop", &format!("soda-factory-agent@{run}.socket"))?;
        if path.exists() {
            return Err(denied("task launch socket survived consumption"));
        }
        let request = serde_json::to_vec(&serde_json::json!({"kind":"task_workspace","version":1,
            "run":run,"profile":profile,"candidate":candidate,"manifest_digest":manifest_digest,"invocation":invocation}))?;
        worker.write_all(&(request.len() as u32).to_le_bytes())?;
        worker.write_all(&request)?;
        let deadline = Instant::now() + Duration::from_secs(4 * 60 * 60);
        fn frame<W: Write>(
            input: &mut UnixStream,
            output: &mut W,
            max: usize,
            deadline: Instant,
        ) -> io::Result<()> {
            let mut header = [0; 4];
            read_preparation(input, &mut header, deadline)?;
            let length = u32::from_le_bytes(header) as usize;
            if length == 0 || length > max {
                return Err(denied("task workspace frame bound invalid"));
            }
            let mut bytes = vec![0; length];
            read_preparation(input, &mut bytes, deadline)?;
            output.write_all(&header)?;
            output.write_all(&bytes)?;
            output.flush()
        }
        frame(&mut worker, output, MAX_REPLY, deadline)?;
        for _ in 0..4096 {
            if Instant::now() >= deadline {
                return Err(denied("task session deadline expired"));
            }
            let mut header = [0; 4];
            let mut offset = 0;
            while offset < header.len() {
                if Instant::now() >= deadline {
                    return Err(denied("task session deadline expired"));
                }
                let mut state = libc::pollfd {
                    fd: worker.as_raw_fd(),
                    events: 0,
                    revents: 0,
                };
                if unsafe { libc::poll(&mut state, 1, 0) } < 0 {
                    return Err(io::Error::last_os_error());
                }
                if state.revents & (libc::POLLHUP | libc::POLLERR | libc::POLLNVAL) != 0 {
                    return Err(denied("task worker retired while waiting for command"));
                }
                match input.read(&mut header[offset..]) {
                    Ok(0) => return Err(io::Error::from(io::ErrorKind::UnexpectedEof)),
                    Ok(n) => offset += n,
                    Err(e)
                        if offset == 0
                            && matches!(
                                e.kind(),
                                io::ErrorKind::WouldBlock
                                    | io::ErrorKind::TimedOut
                                    | io::ErrorKind::Interrupted
                            ) =>
                    {
                        ()
                    }
                    Err(e) => return Err(e),
                }
            }
            let length = u32::from_le_bytes(header) as usize;
            if length == 0 || length > MAX_COMMAND {
                return Err(denied("task workspace command bound invalid"));
            }
            let mut bytes = vec![0; length];
            read_client_exact(input, &mut bytes, deadline)?;
            let command: WorkspaceCommand =
                serde_json::from_slice(&bytes).map_err(|_| denied("task command shape invalid"))?;
            worker.write_all(&header)?;
            worker.write_all(&bytes)?;
            frame(&mut worker, output, MAX_REPLY, deadline)?;
            if matches!(command, WorkspaceCommand::Close) {
                return Ok(());
            }
        }
        Err(denied("task session command limit reached"))
    })();
    // Join the exact service after EOF or failure. Root staging remains until
    // the native owner retains and acknowledges the result.
    let stopped = stop_agent(run, &directory);
    match (result, stopped) {
        (Ok(()), Ok(())) => Ok(()),
        (Err(error), _) => Err(error),
        (_, Err(error)) => Err(error),
    }
}
