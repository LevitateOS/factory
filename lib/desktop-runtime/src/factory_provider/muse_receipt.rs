//! Durable CLI-process evidence. Reading never restarts inference or infers
//! root/MCP retirement from a process exit. Startup recovery records those
//! separate facts only after checking the original service and broker root.
use super::{LaunchIntent, MuseRun};
use crate::private_fs::publish_bytes as publish;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use soda_execution_protocol::workspace::Status;
use soda_execution_protocol::{MuseObservation, StopReason, MAX_OUTPUT};
use std::fs::{self, OpenOptions};
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

const HEADER_LIMIT: usize = 8192;
fn denied() -> io::Error {
    io::Error::other("retained Muse evidence unavailable or differs")
}
fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn read(path: &Path, limit: usize) -> io::Result<Vec<u8>> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != unsafe { libc::geteuid() }
        || meta.mode() & 0o077 != 0
        || meta.len() > limit as u64
    {
        return Err(denied());
    }
    let mut bytes = Vec::with_capacity(meta.len() as usize);
    file.take(limit as u64 + 1).read_to_end(&mut bytes)?;
    if bytes.len() != meta.len() as usize {
        return Err(denied());
    }
    Ok(bytes)
}
fn path(profile: &Path, intent: &LaunchIntent, suffix: &str) -> io::Result<PathBuf> {
    if !intent.muse_login() {
        return Err(denied());
    }
    let scope = intent.scope()?;
    super::receipt::profile(profile, scope.profile)?;
    Ok(super::receipt::ensure_receipts(profile)?.join(format!("{}.muse.{suffix}", scope.run)))
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Header {
    kind: String,
    version: u8,
    intent_sha256: String,
    process: MuseObservation,
}

fn valid(intent: &LaunchIntent, process: &MuseObservation, output: &[u8]) -> bool {
    process.output_complete
        && valid_process(
            process,
            intent.run(),
            intent.tool_sha256(),
            intent
                .scope()
                .ok()
                .map(|s| s.prompt_sha256().to_owned())
                .as_deref()
                .unwrap_or(""),
            output,
            MAX_OUTPUT,
        )
}
fn valid_process(
    process: &MuseObservation,
    run: &str,
    tool: &str,
    prompt: &str,
    output: &[u8],
    limit: usize,
) -> bool {
    process.kind == "factory_muse_process"
        && process.version == 1
        && process.run == run
        && super::hex(&process.service, 32)
        && super::hex(&process.invocation, 32)
        && process.uid == unsafe { libc::geteuid() }
        && process.uid > 0
        && process.pid > 0
        && process.system_instance.len() == 36
        && process.start_identity.parse::<u64>().is_ok_and(|v| v > 0)
        && process.started_unix_ms > 0
        && process
            .stopped_unix_ms
            .is_none_or(|stopped| stopped >= process.started_unix_ms)
        && process.executable_sha256 == tool
        && process.prompt_sha256 == prompt
        && process.finished
        && process.live == Some(false)
        && process.sequence == 0
        && process.tree_retired
        && (process.output_complete || process.reason == Some(StopReason::OutputLimit))
        && output.len() <= limit
        && process.output_length == output.len()
        && process.output_sha256.as_deref() == Some(&digest(output))
        && process
            .reason
            .is_some_and(|reason| reason != StopReason::Unconfirmed)
        && (process.exit_code.is_some() != process.exit_signal.is_some())
        && process.exit_code.is_none_or(|v| (0..=255).contains(&v))
        && process.exit_signal.is_none_or(|v| (1..=64).contains(&v))
}
pub(super) fn write(
    profile: &Path,
    intent: &LaunchIntent,
    process: &MuseObservation,
    output: &[u8],
) -> io::Result<()> {
    if !valid(intent, process, output) {
        return Err(denied());
    }
    let header = serde_json::to_vec(&Header {
        kind: "factory_muse_receipt".into(),
        version: 1,
        intent_sha256: intent.digest(),
        process: process.clone(),
    })?;
    if header.len() > HEADER_LIMIT {
        return Err(denied());
    }
    let mut bytes = Vec::with_capacity(4 + header.len() + output.len());
    bytes.extend_from_slice(&(header.len() as u32).to_be_bytes());
    bytes.extend_from_slice(&header);
    bytes.extend_from_slice(output);
    publish(&path(profile, intent, "receipt")?, &bytes)
}
pub(super) fn mark_retired(
    profile: &Path,
    intent: &LaunchIntent,
    process: &MuseObservation,
) -> io::Result<()> {
    let marker = serde_json::json!({"kind":"factory_muse_retired","version":1,"intent_sha256":intent.digest(),"invocation":process.invocation});
    let path = path(profile, intent, "retired")?;
    match read(&path, 1024) {
        Ok(bytes) if serde_json::from_slice::<serde_json::Value>(&bytes)? == marker => Ok(()),
        Ok(_) => Err(denied()),
        Err(e) if e.kind() == io::ErrorKind::NotFound => {
            publish(&path, &serde_json::to_vec(&marker)?)
        }
        Err(e) => Err(e),
    }
}
pub(super) fn retained(profile: &Path, intent: &LaunchIntent) -> io::Result<Option<MuseRun>> {
    let path = path(profile, intent, "receipt")?;
    let bytes = match read(&path, 4 + HEADER_LIMIT + MAX_OUTPUT) {
        Ok(bytes) => bytes,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return legacy(profile, intent),
        Err(e) => return Err(e),
    };
    if bytes.len() < 4 {
        return Err(denied());
    }
    let length = u32::from_be_bytes(bytes[..4].try_into().map_err(|_| denied())?) as usize;
    if length > HEADER_LIMIT || 4 + length > bytes.len() {
        return Err(denied());
    }
    let header: Header = serde_json::from_slice(&bytes[4..4 + length])?;
    let output = bytes[4 + length..].to_vec();
    if header.kind != "factory_muse_receipt"
        || header.version != 1
        || header.intent_sha256 != intent.digest()
        || !valid(intent, &header.process, &output)
    {
        return Err(denied());
    }
    result(profile, intent, header.process, output).map(Some)
}

// A live process need not have a terminal result yet. Controller retirement
// recovery must not turn that absence into failed live observation.
pub(super) fn retired(profile: &Path, intent: &LaunchIntent) -> io::Result<Option<MuseRun>> {
    match read(&path(profile, intent, "retired")?, 1024) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
        Ok(_) => (),
    }
    retained(profile, intent)
}
fn result(
    profile: &Path,
    intent: &LaunchIntent,
    process: MuseObservation,
    output: Vec<u8>,
) -> io::Result<MuseRun> {
    let root_retired = match read(&path(profile, intent, "retired")?, 1024) {
        Ok(bytes) => {
            let marker: serde_json::Value = serde_json::from_slice(&bytes)?;
            if marker
                != serde_json::json!({"kind":"factory_muse_retired","version":1,"intent_sha256":intent.digest(),"invocation":process.invocation})
            {
                return Err(denied());
            }
            true
        }
        Err(e) if e.kind() == io::ErrorKind::NotFound => false,
        Err(e) => return Err(e),
    };
    Ok(MuseRun {
        candidate: task_candidate(profile, intent, &process),
        product_output: super::muse_attempt::final_text(&output),
        process,
        output,
        root_retired,
    })
}
pub(super) fn task_candidate(
    profile: &Path,
    intent: &LaunchIntent,
    process: &MuseObservation,
) -> Option<String> {
    if !intent.has_tasks() {
        return None;
    }
    let manifest = intent.retained_manifest(profile).ok()?;
    let receipts = super::workspace_receipts::Receipts::open(
        profile,
        &intent.scope().ok()?,
        &manifest,
        &soda_execution_protocol::workspace::Parent::from_muse(process),
    )
    .ok()?;
    let result = receipts.final_receipt().ok()??;
    (result.status == Status::Committed
        && super::hex(&result.head, 40)
        && !result.bundle.is_empty())
    .then_some(result.head)
}

// Earlier connector components retained the same process/output facts in their
// private service directory. Import only one exact binding; never select a
// directory by modification time or infer that its service/root has stopped.
fn legacy(profile: &Path, intent: &LaunchIntent) -> io::Result<Option<MuseRun>> {
    if let Some((process, output)) = retained_process(
        profile,
        intent.run(),
        intent.tool_sha256(),
        intent.scope()?.prompt_sha256(),
        MAX_OUTPUT,
    )? {
        write(profile, intent, &process, &output)?;
        result(profile, intent, process, output).map(Some)
    } else {
        Ok(None)
    }
}

// A judgment has the same native helper evidence as an execution, without a
// worker root or LaunchIntent. Import exactly one bound process; never launch.
pub(super) fn retained_process(
    profile: &Path,
    run: &str,
    tool: &str,
    prompt: &str,
    limit: usize,
) -> io::Result<Option<(MuseObservation, Vec<u8>)>> {
    let root = profile.join("native-muse-runs");
    let entries = match fs::read_dir(&root) {
        Ok(entries) => entries,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e),
    };
    super::muse::private(&root)?;
    let mut found = None;
    for (index, entry) in entries.enumerate() {
        if index >= 64 {
            return Err(denied());
        }
        let entry = entry?;
        let name = entry.file_name().to_str().ok_or_else(denied)?.to_owned();
        super::muse::private(&entry.path())?;
        if !super::hex(&name, 32) {
            return Err(denied());
        }
        let meta = entry.path().symlink_metadata()?;
        if !meta.is_dir() || meta.uid() != unsafe { libc::geteuid() } || meta.mode() & 0o077 != 0 {
            return Err(denied());
        }
        let binding: serde_json::Value =
            serde_json::from_slice(&read(&entry.path().join("binding.json"), 4096)?)?;
        if binding["run"] != run {
            continue;
        }
        if found.is_some()
            || binding["kind"] != "factory_muse_binding"
            || binding["version"] != 1
            || binding["service"] != name
            || binding["unit"] != format!("soda-factory-muse-{run}-{name}.service")
            || binding["executable_sha256"] != tool
            || binding["prompt_sha256"] != prompt
        {
            return Err(denied());
        }
        let process: MuseObservation =
            serde_json::from_slice(&read(&entry.path().join("result.json"), HEADER_LIMIT)?)?;
        if process.service != name || !super::hex(&process.invocation, 32) {
            return Err(denied());
        }
        let invocation = entry.path().join(&process.invocation);
        super::muse::private(&invocation)?;
        let output = read(&invocation.join("terminal.log"), MAX_OUTPUT)?;
        if !valid_process(&process, run, tool, prompt, &output, limit) {
            return Err(denied());
        }
        found = Some((process, output));
    }
    Ok(found)
}

pub(super) fn service_stopped(profile: &Path, process: &MuseObservation) -> io::Result<()> {
    let unit = format!(
        "soda-factory-muse-{}-{}.service",
        process.run, process.service
    );
    let mut child = Command::new("/usr/bin/systemctl")
        .env_remove("DBUS_SESSION_BUS_ADDRESS")
        .args([
            "--user",
            "show",
            "--property=LoadState,ActiveState,MainPID,WorkingDirectory",
            "--",
            &unit,
        ])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()?;
    let until = Instant::now() + Duration::from_secs(2);
    loop {
        if child.try_wait()?.is_some() {
            break;
        }
        if Instant::now() >= until {
            let _ = child.kill();
            let _ = child.wait();
            return Err(denied());
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    let mut bytes = Vec::new();
    child
        .stdout
        .take()
        .ok_or_else(denied)?
        .take(8193)
        .read_to_end(&mut bytes)?;
    if bytes.len() > 8192 {
        return Err(denied());
    }
    let text = String::from_utf8(bytes).map_err(io::Error::other)?;
    let fields: std::collections::BTreeMap<_, _> = text
        .lines()
        .filter_map(|line| line.split_once('='))
        .collect();
    if fields.get("MainPID") != Some(&"0")
        || !matches!(fields.get("ActiveState"), Some(&"inactive" | &"failed"))
    {
        return Err(denied());
    }
    let expected = profile.join("native-muse-runs").join(&process.service);
    match fields.get("LoadState") {
        Some(&"not-found") => Ok(()),
        Some(&"loaded") if fields.get("WorkingDirectory").copied() == expected.to_str() => Ok(()),
        _ => Err(denied()),
    }
}
