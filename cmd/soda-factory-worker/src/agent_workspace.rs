//! Sequential task effects inside the existing isolated agent service. No model
//! process is granted access to this control stream or to Git administration.
use super::{
    agent_candidate, git, refused, Executable, Invocation, Limits, Processes, RepositoryIdentity,
    StopReason, PRIVATE, PRIVATE_HOME, PRIVATE_TMP, WORKSPACE,
};
use base64::Engine;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use soda_execution_protocol::workspace::{
    CheckResult, Command, Event, Reply, MAX_COMMAND, MAX_REPLY, MAX_TEXT,
};
use std::collections::{BTreeMap, BTreeSet};
use std::ffi::OsString;
use std::fs::{self, OpenOptions};
use std::io::{self, Read, Seek, SeekFrom, Write};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

const MAX_FILE: u64 = 32 * 1024 * 1024;
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Manifest {
    plan: String,
    plan_digest: String,
    invocation: String,
    phase: String,
    run: String,
    deadline: String,
    effort: String,
    #[serde(default)]
    contributor: bool,
    max_children: u8,
}
// Go owns full product plan validation. These are the fields defining native
// effects; native validation additionally binds their paths and argv locally.
#[derive(Deserialize)]
struct Plan {
    source_commit: String,
    tasks: Vec<Task>,
    #[serde(default, deserialize_with = "null_vec")]
    checks: Vec<Check>,
}
#[derive(Deserialize)]
struct Task {
    id: String,
    #[serde(default, deserialize_with = "null_vec")]
    depends_on: Vec<String>,
    #[serde(default, deserialize_with = "null_vec")]
    owned_paths: Vec<String>,
    #[serde(default, deserialize_with = "null_vec")]
    check_ids: Vec<String>,
}
#[derive(Deserialize)]
struct Check {
    id: String,
    argv: Vec<String>,
    cwd: String,
    timeout_seconds: u64,
    scope: String,
    required: bool,
}
fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn null_vec<'de, D: serde::Deserializer<'de>, T: Deserialize<'de>>(
    de: D,
) -> Result<Vec<T>, D::Error> {
    Ok(Option::<Vec<T>>::deserialize(de)?.unwrap_or_default())
}
fn key(value: &str) -> bool {
    (1..=32).contains(&value.len())
        && value.as_bytes()[0].is_ascii_lowercase()
        && value
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_' || c == b'-')
}
fn relative(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 1024
        && !value.contains(['\\', '*', '?', '[', ']', '{', '}'])
        && !value.chars().any(char::is_control)
        && value
            .split('/')
            .all(|part| !part.is_empty() && ![".", "..", ".git", ".soda-home"].contains(&part))
}
fn oid(bytes: Vec<u8>) -> io::Result<String> {
    let text = std::str::from_utf8(&bytes)
        .map_err(|_| refused("workspace Git encoding invalid"))?
        .trim_end_matches(['\r', '\n']);
    if text.len() != 40 || !text.bytes().all(|c| c.is_ascii_hexdigit()) {
        return Err(refused("workspace Git identity invalid"));
    }
    Ok(text.into())
}
struct Workspace<'a> {
    plan: Plan,
    manifest_digest: String,
    invocation: String,
    deadline: Instant,
    head: String,
    active: Option<usize>,
    frozen: Option<(String, String)>,
    completed: BTreeSet<String>,
    failed: bool,
    events: Vec<Event>,
    identity: &'a RepositoryIdentity,
    processes: &'a mut Processes,
}
impl<'a> Workspace<'a> {
    fn open(
        run: &str,
        candidate: &str,
        expected: &str,
        invocation: &str,
        identity: &'a RepositoryIdentity,
        processes: &'a mut Processes,
    ) -> io::Result<Self> {
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open("/run/factory-captured-inputs/task.manifest")?;
        let meta = file.metadata()?;
        if !meta.is_file()
            || meta.uid() != 0
            || meta.mode() & 0o7777 != 0o444
            || meta.len() == 0
            || meta.len() > soda_execution_protocol::MAX_TASK_MANIFEST as u64
        {
            return Err(refused("workspace manifest metadata invalid"));
        }
        let mut bytes = Vec::new();
        file.take(soda_execution_protocol::MAX_TASK_MANIFEST as u64 + 1)
            .read_to_end(&mut bytes)?;
        if bytes.len() as u64 != meta.len() || digest(&bytes) != expected {
            return Err(refused("workspace manifest digest differs"));
        }
        let manifest: Manifest =
            serde_json::from_slice(&bytes).map_err(|_| refused("workspace manifest invalid"))?;
        let raw = base64::engine::general_purpose::STANDARD
            .decode(&manifest.plan)
            .map_err(|_| refused("workspace plan encoding invalid"))?;
        let until = OffsetDateTime::parse(&manifest.deadline, &Rfc3339)
            .map_err(|_| refused("workspace deadline invalid"))?
            - OffsetDateTime::now_utc();
        if manifest.run != run
            || manifest.max_children != 0
            || !matches!(manifest.phase.as_str(), "implement" | "correct")
            || !matches!(
                manifest.effort.as_str(),
                "low" | "medium" | "high" | "xhigh"
            )
            || manifest.invocation.is_empty()
            || manifest.invocation.len() > 128
            || manifest.invocation.chars().any(char::is_control)
            || digest(&raw) != manifest.plan_digest
            || until.whole_milliseconds() <= 0
            || until.whole_seconds() > 4 * 60 * 60
        {
            return Err(refused("workspace manifest binding invalid"));
        }
        let _ = manifest.contributor;
        let plan: Plan =
            serde_json::from_slice(&raw).map_err(|_| refused("workspace plan invalid"))?;
        if plan.source_commit != candidate
            || plan.tasks.is_empty()
            || plan.tasks.len() > 64
            || plan.checks.len() > 16
            || manifest.phase == "correct" && plan.tasks.len() != 1
        {
            return Err(refused("workspace plan scope invalid"));
        }
        let mut checks = BTreeSet::new();
        for check in &plan.checks {
            if !key(&check.id)
                || !checks.insert(check.id.clone())
                || check.argv.is_empty()
                || check.argv.len() > 64
                || check
                    .argv
                    .iter()
                    .any(|arg| arg.is_empty() || arg.contains('\0'))
                || check.argv.iter().map(String::len).sum::<usize>() > 16384
                || !(1..=600).contains(&check.timeout_seconds)
                || !matches!(check.scope.as_str(), "task" | "integration")
                || check.cwd != "." && !relative(check.cwd.trim_end_matches('/'))
            {
                return Err(refused("workspace declared check invalid"));
            }
        }
        let mut known = BTreeSet::new();
        for task in &plan.tasks {
            if !key(&task.id)
                || known.contains(&task.id)
                || task.depends_on.iter().any(|id| !known.contains(id))
                || task.owned_paths.len() > 32
                || task
                    .owned_paths
                    .iter()
                    .any(|p| !relative(p.strip_suffix('/').unwrap_or(p)))
                || task
                    .check_ids
                    .iter()
                    .any(|id| !plan.checks.iter().any(|c| &c.id == id))
            {
                return Err(refused("workspace task graph invalid"));
            }
            known.insert(task.id.clone());
        }
        if invocation.is_empty()
            || invocation.len() > 128
            || invocation.chars().any(char::is_control)
        {
            return Err(refused("workspace observed invocation invalid"));
        }
        Ok(Self {
            plan,
            manifest_digest: expected.into(),
            invocation: invocation.into(),
            deadline: Instant::now() + Duration::from_millis(until.whole_milliseconds() as u64),
            head: candidate.into(),
            active: None,
            frozen: None,
            completed: BTreeSet::new(),
            failed: false,
            events: Vec::new(),
            identity,
            processes,
        })
    }
    fn git(&mut self, args: &[&str]) -> io::Result<Vec<u8>> {
        if Instant::now() >= self.deadline {
            return Err(refused("workspace deadline expired"));
        }
        if &agent_candidate::repository_identity(Path::new(WORKSPACE))? != self.identity {
            return Err(refused("workspace repository identity changed"));
        }
        let safe = format!("safe.directory={WORKSPACE}");
        let mut all = vec![
            "-C",
            WORKSPACE,
            "-c",
            safe.as_str(),
            "-c",
            "core.hooksPath=/dev/null",
            "-c",
            "core.fsmonitor=false",
        ];
        all.extend_from_slice(args);
        git(
            self.processes,
            &all,
            Duration::from_secs(20).min(self.deadline.saturating_duration_since(Instant::now())),
        )
    }
    fn event(&mut self, kind: &str, task: &str, detail: String) -> io::Result<()> {
        if self.events.len() >= 512 {
            return Err(refused("workspace journal full"));
        }
        let prev_digest = self
            .events
            .last()
            .map(Event::digest)
            .transpose()?
            .unwrap_or_default();
        let event = Event {
            sequence: self.events.len() as i64 + 1,
            prev_digest,
            kind: kind.into(),
            task_key: task.into(),
            invocation: self.invocation.clone(),
            plan_digest: self.manifest_digest.clone(),
            detail,
        };
        let mut file = OpenOptions::new()
            .append(true)
            .create(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(Path::new(PRIVATE).join("task.events"))?;
        file.write_all(&serde_json::to_vec(&event)?)?;
        file.write_all(b"\n")?;
        file.sync_all()?;
        self.events.push(event);
        Ok(())
    }
    fn path(&self, relative_path: &str) -> io::Result<PathBuf> {
        if !relative(relative_path) {
            return Err(refused("workspace relative path invalid"));
        }
        let mut path = PathBuf::from(WORKSPACE);
        let parts: Vec<_> = relative_path.split('/').collect();
        for (i, part) in parts.iter().enumerate() {
            path.push(part);
            match path.symlink_metadata() {
                Ok(meta)
                    if meta.file_type().is_symlink()
                        || meta.uid() != unsafe { libc::geteuid() }
                        || i + 1 < parts.len() && !meta.is_dir() =>
                {
                    return Err(refused("workspace path identity invalid"))
                }
                Ok(_) => (),
                Err(e) if e.kind() == io::ErrorKind::NotFound && i + 1 == parts.len() => (),
                Err(e) => return Err(e),
            }
        }
        Ok(path)
    }
    fn owns(&self, path: &str) -> bool {
        self.active.is_some_and(|i| {
            self.plan.tasks[i]
                .owned_paths
                .iter()
                .any(|owned| owned == path || owned.ends_with('/') && path.starts_with(owned))
        })
    }
    fn tree(&mut self) -> io::Result<String> {
        if &agent_candidate::repository_identity(Path::new(WORKSPACE))? != self.identity
            || oid(self.git(&["rev-parse", "HEAD"])?)? != self.head
        {
            return Err(refused("workspace repository identity changed"));
        }
        let status = self.git(&["status", "--porcelain=v1", "-z", "--untracked-files=all"])?;
        for entry in status.split(|b| *b == 0).filter(|e| !e.is_empty()) {
            // The control API performs text edits, no rename/copy operations.
            // A declared check must not introduce an unowned or hidden rename.
            let path = entry
                .get(3..)
                .and_then(|p| std::str::from_utf8(p).ok())
                .ok_or_else(|| refused("workspace changed path invalid"))?;
            if entry[0] == b'R'
                || entry[1] == b'R'
                || entry[0] == b'C'
                || entry[1] == b'C'
                || !self.owns(path)
            {
                return Err(refused("workspace change outside task ownership"));
            }
            self.path(path)?;
        }
        self.git(&["add", "-A", "--", "."])?;
        oid(self.git(&["write-tree"])?)
    }
    fn checks(&mut self, ids: &[String]) -> io::Result<Vec<CheckResult>> {
        let mut results = Vec::new();
        for id in ids {
            let check = self
                .plan
                .checks
                .iter()
                .find(|c| &c.id == id)
                .ok_or_else(|| refused("workspace unknown check"))?;
            let directory = if check.cwd == "." {
                PathBuf::from(WORKSPACE)
            } else {
                self.path(check.cwd.trim_end_matches('/'))?
            };
            let tool_path = soda_execution_protocol::setup_path(PRIVATE_HOME);
            let tool = if check.argv[0].contains('/') {
                if Path::new(&check.argv[0]).is_absolute() {
                    PathBuf::from(&check.argv[0])
                } else {
                    directory.join(&check.argv[0])
                }
            } else {
                tool_path
                    .split(':')
                    .map(|dir| Path::new(dir).join(&check.argv[0]))
                    .find(|path| path.is_file())
                    .ok_or_else(|| refused("workspace check executable missing"))?
            };
            let executable = Executable::capture(&tool)
                .map_err(|_| refused("workspace check executable invalid"))?;
            let environment = [
                ("PATH", tool_path.as_str()),
                ("HOME", PRIVATE_HOME),
                ("TMPDIR", PRIVATE_TMP),
                ("LANG", "C.UTF-8"),
                ("GIT_CONFIG_NOSYSTEM", "1"),
                ("GIT_CONFIG_GLOBAL", "/dev/null"),
                ("GIT_TERMINAL_PROMPT", "0"),
            ]
            .into_iter()
            .map(|(k, v)| (OsString::from(k), OsString::from(v)))
            .collect::<BTreeMap<_, _>>();
            let duration = Duration::from_secs(check.timeout_seconds)
                .min(self.deadline.saturating_duration_since(Instant::now()));
            let process = self
                .processes
                .launch(Invocation {
                    executable: &executable,
                    directory: &directory,
                    arguments: check.argv[1..].iter().map(OsString::from).collect(),
                    environment,
                    limits: Limits {
                        duration,
                        output_bytes: 1024 * 1024,
                        memory_bytes: 1024 * 1024 * 1024,
                        processes: 128,
                        cpu_percent: 100,
                    },
                    columns: 80,
                    rows: 24,
                })
                .map_err(|_| refused("workspace check launch unavailable"))?;
            let observed = process.wait(duration + Duration::from_secs(2));
            if !observed.finished || !observed.tree_retired || !observed.output_complete {
                return Err(refused("workspace check retirement unconfirmed"));
            }
            let bytes = fs::read(observed.output)?;
            results.push(CheckResult {
                id: id.clone(),
                passed: observed.reason == Some(StopReason::Exited)
                    && observed.exit.and_then(|e| e.code) == Some(0),
                log_digest: digest(&bytes),
            });
        }
        Ok(results)
    }
    fn command(&mut self, command: Command) -> io::Result<Reply> {
        if Instant::now() >= self.deadline {
            return Err(refused("workspace deadline expired"));
        }
        if self.failed && !matches!(&command, Command::Read { .. } | Command::Close) {
            return Err(refused("workspace task check failed"));
        }
        let mut reply = Reply {
            status: "ok".into(),
            head: self.head.clone(),
            ..Reply::default()
        };
        match command {
            Command::Start { task } => {
                if self.active.is_some() || self.completed.contains(&task) {
                    return Err(refused("workspace task already active or complete"));
                }
                let i = self
                    .plan
                    .tasks
                    .iter()
                    .position(|t| t.id == task)
                    .ok_or_else(|| refused("workspace unknown task"))?;
                if self.plan.tasks[i]
                    .depends_on
                    .iter()
                    .any(|id| !self.completed.contains(id))
                {
                    return Err(refused("workspace dependency incomplete"));
                }
                self.event("parent_task_started", &task, String::new())?;
                self.active = Some(i);
            }
            Command::Read {
                path,
                offset,
                limit,
            } => {
                if !(1..=MAX_TEXT).contains(&limit) {
                    return Err(refused("workspace read bound invalid"));
                }
                let mut file = OpenOptions::new()
                    .read(true)
                    .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                    .open(self.path(&path)?)?;
                let meta = file.metadata()?;
                if !meta.is_file() || meta.len() > MAX_FILE || offset as u64 > meta.len() {
                    return Err(refused("workspace read file invalid"));
                }
                file.seek(SeekFrom::Start(offset as u64))?;
                let mut bytes = vec![];
                file.take(limit as u64).read_to_end(&mut bytes)?;
                // Text pages never silently corrupt a multibyte character.
                let text = match std::str::from_utf8(&bytes) {
                    Ok(text) => text,
                    Err(e)
                        if e.error_len().is_none()
                            && offset as u64 + (bytes.len() as u64) < meta.len() =>
                    {
                        std::str::from_utf8(&bytes[..e.valid_up_to()])
                            .map_err(|_| refused("workspace text invalid"))?
                    }
                    Err(_) => return Err(refused("workspace text invalid")),
                };
                reply.content = Some(text.into());
            }
            Command::Edit {
                path,
                offset,
                expected,
                replacement,
            } => {
                if self.frozen.is_some()
                    || !self.owns(&path)
                    || expected.len() > MAX_TEXT
                    || replacement.len() > MAX_TEXT
                {
                    return Err(refused("workspace edit outside active task"));
                }
                let path = self.path(&path)?;
                let mut bytes = Vec::new();
                let existing = match OpenOptions::new()
                    .read(true)
                    .write(true)
                    .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                    .open(&path)
                {
                    Ok(mut file) => {
                        let meta = file.metadata()?;
                        if !meta.is_file() || meta.nlink() != 1 || meta.len() > MAX_FILE {
                            return Err(refused("workspace edit file invalid"));
                        }
                        Read::by_ref(&mut file)
                            .take(MAX_FILE + 1)
                            .read_to_end(&mut bytes)?;
                        Some(file)
                    }
                    Err(e) if e.kind() == io::ErrorKind::NotFound => None,
                    Err(e) => return Err(e),
                };
                let text = std::str::from_utf8(&bytes)
                    .map_err(|_| refused("workspace edit requires text"))?;
                let end = offset
                    .checked_add(expected.len())
                    .ok_or_else(|| refused("workspace edit range invalid"))?;
                if text.get(offset..end) != Some(expected.as_str())
                    || !text.is_char_boundary(end)
                    || bytes.len() - expected.len() + replacement.len() > MAX_FILE as usize
                {
                    return Err(refused("workspace edit preimage differs"));
                }
                bytes.splice(offset..end, replacement.bytes());
                let mut file = match existing {
                    Some(file) => file,
                    None => OpenOptions::new()
                        .write(true)
                        .create_new(true)
                        .mode(0o600)
                        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                        .open(&path)?,
                };
                file.seek(SeekFrom::Start(0))?;
                file.write_all(&bytes)?;
                file.set_len(bytes.len() as u64)?;
                file.sync_all()?;
            }
            Command::Release { task } => {
                if self.active.map(|i| self.plan.tasks[i].id.as_str()) != Some(&task) {
                    return Err(refused("workspace task not active"));
                }
                if let Some((tree, output)) = &self.frozen {
                    reply.tree = tree.clone();
                    reply.output_digest = output.clone();
                } else {
                    let tree = self.tree()?;
                    let output = digest(
                        format!(
                            "factory-task-output-v1\0{}\0{task}\0{}\0{tree}",
                            self.manifest_digest, self.head
                        )
                        .as_bytes(),
                    );
                    self.event("parent_output_observed", &task, output.clone())?;
                    self.frozen = Some((tree.clone(), output.clone()));
                    reply.tree = tree;
                    reply.output_digest = output;
                }
            }
            Command::Checkpoint {
                task,
                output_digest,
                expected_head,
            } => {
                if expected_head != self.head
                    || self.active.map(|i| self.plan.tasks[i].id.as_str()) != Some(&task)
                    || !self
                        .frozen
                        .as_ref()
                        .is_some_and(|(_, output)| *output == output_digest)
                {
                    return Err(refused("workspace checkpoint binding differs"));
                }
                let frozen = self.frozen.as_ref().unwrap().0.clone();
                let ids = self.plan.tasks[self.active.unwrap()].check_ids.clone();
                reply.checks = self.checks(&ids)?;
                if self.tree()? != frozen {
                    return Err(refused("workspace check changed frozen output"));
                }
                if reply.checks.iter().any(|c| !c.passed) {
                    self.event("task_failed", &task, "check_failed".into())?;
                    self.failed = true;
                    reply.status = "failed".into();
                    reply.events = self.events.clone();
                    return Ok(reply);
                }
                self.event("checkpoint_prepared", &task, ids.join(" "))?;
                let head = self.head.clone();
                let commit = oid(self.git(&[
                    "-c",
                    "user.name=Factory Native Checkpoint",
                    "-c",
                    "user.email=factory@invalid.local",
                    "-c",
                    "commit.gpgsign=false",
                    "commit-tree",
                    &frozen,
                    "-p",
                    &head,
                    "-m",
                    "Factory task checkpoint",
                ])?)?;
                self.git(&["update-ref", "HEAD", &commit, &head])?;
                self.event(
                    "task_committed",
                    &task,
                    format!("tree={frozen} commit={commit}"),
                )?;
                self.head = commit.clone();
                self.completed.insert(task);
                self.active = None;
                self.frozen = None;
                reply.status = "committed".into();
                reply.head = commit;
                reply.tree = frozen;
            }
            Command::Final { expected_head } => {
                if expected_head != self.head
                    || self.active.is_some()
                    || self.completed.len() != self.plan.tasks.len()
                {
                    return Err(refused("workspace final tasks incomplete"));
                }
                let tree = oid(self.git(&["rev-parse", "HEAD^{tree}"])?)?;
                let ids = self
                    .plan
                    .checks
                    .iter()
                    .filter(|c| c.scope == "integration" && c.required)
                    .map(|c| c.id.clone())
                    .collect::<Vec<_>>();
                reply.checks = self.checks(&ids)?;
                if reply.checks.iter().any(|c| !c.passed) {
                    reply.status = "failed".into();
                    return Ok(reply);
                }
                if !self
                    .git(&["status", "--porcelain=v1", "-z", "--untracked-files=all"])?
                    .is_empty()
                    || oid(self.git(&["rev-parse", "HEAD"])?)? != self.head
                {
                    return Err(refused("workspace final check changed candidate"));
                }
                let base = self.plan.source_commit.clone();
                let identity = self.identity;
                let candidate = agent_candidate::capture(
                    Path::new(WORKSPACE),
                    &base,
                    &Path::new(PRIVATE).join("task-candidate.bundle"),
                    identity,
                    |args, duration| git(self.processes, args, duration),
                )?;
                let agent_candidate::Candidate::Captured { commit, bundle, .. } = candidate else {
                    return Err(refused("workspace final bundle missing"));
                };
                if commit != self.head {
                    return Err(refused("workspace final bundle identity differs"));
                }
                reply.status = "committed".into();
                reply.tree = tree;
                reply.bundle = bundle;
            }
            Command::Close => reply.status = "closed".into(),
        }
        reply.events = self.events.clone();
        Ok(reply)
    }
}
pub(super) fn serve(
    stream: &mut UnixStream,
    processes: &mut Processes,
    run: &str,
    candidate: &str,
    expected: &str,
    invocation: &str,
    identity: &RepositoryIdentity,
) -> io::Result<()> {
    let mut workspace = Workspace::open(run, candidate, expected, invocation, identity, processes)?;
    fn send(stream: &mut UnixStream, reply: &Reply) -> io::Result<()> {
        let bytes = serde_json::to_vec(reply)?;
        if bytes.len() > MAX_REPLY {
            return Err(refused("workspace reply exceeds bound"));
        }
        stream.write_all(&(bytes.len() as u32).to_le_bytes())?;
        stream.write_all(&bytes)
    }
    send(
        stream,
        &Reply {
            status: "ready".into(),
            head: candidate.into(),
            ..Reply::default()
        },
    )?;
    for _ in 0..4096 {
        stream.set_read_timeout(Some(
            workspace.deadline.saturating_duration_since(Instant::now()),
        ))?;
        let mut header = [0u8; 4];
        stream.read_exact(&mut header)?;
        let length = u32::from_le_bytes(header) as usize;
        if length == 0 || length > MAX_COMMAND {
            return Err(refused("workspace command exceeds bound"));
        }
        stream.set_read_timeout(Some(
            workspace
                .deadline
                .saturating_duration_since(Instant::now())
                .min(Duration::from_secs(3)),
        ))?;
        let mut bytes = vec![0; length];
        stream.read_exact(&mut bytes)?;
        let command =
            serde_json::from_slice(&bytes).map_err(|_| refused("workspace command invalid"))?;
        let reply = workspace.command(command)?;
        let closed = reply.status == "closed";
        send(stream, &reply)?;
        if closed {
            return Ok(());
        }
    }
    Err(refused("workspace command limit reached"))
}
