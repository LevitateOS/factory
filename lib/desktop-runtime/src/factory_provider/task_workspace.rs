//! A coding run's task protocol, served in this process against a visible git
//! worktree. Agents are not isolated (AGENTS.md): the project clone lives at
//! `~/Factory/<owner>/<repo>` with no network remote and no credential helper,
//! and each coding run gets its own worktree beside it. Muse edits the worktree
//! directly (inside its own sandbox) or through the task tools; Factory alone
//! commits checkpoints and hands the final bundle to Go for publication.
use super::{AgentCancellation, ReceiptScope, Role};
use base64::Engine;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use soda_execution_protocol::workspace::{
    CheckResult, Command, Event, Parent, Reply, Status, MAX_COMMAND, MAX_TEXT,
};
use std::collections::BTreeSet;
use std::fs::{self, OpenOptions};
use std::io::{self, Read, Seek, SeekFrom, Write};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::process::{CommandExt, ExitStatusExt};
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::{Duration, Instant};
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

const MAX_FILE: u64 = 32 * 1024 * 1024;
const MAX_CHECK_OUTPUT: u64 = 1024 * 1024;

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}
fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect()
}
fn null_vec<'de, D: serde::Deserializer<'de>, T: Deserialize<'de>>(de: D) -> Result<Vec<T>, D::Error> {
    Ok(Option::<Vec<T>>::deserialize(de)?.unwrap_or_default())
}
fn key(value: &str) -> bool {
    (1..=32).contains(&value.len())
        && value.as_bytes()[0].is_ascii_lowercase()
        && value.bytes().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_' || c == b'-')
}
fn relative(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 1024
        && !value.contains(['\\', '*', '?', '[', ']', '{', '}'])
        && !value.chars().any(char::is_control)
        && value.split('/').all(|part| !part.is_empty() && ![".", "..", ".git"].contains(&part))
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
/// `owner/repo` as GitHub names it, safe as two path components.
fn repository_path(repository: &str) -> io::Result<PathBuf> {
    let safe = |part: &str| {
        !part.is_empty()
            && part.len() <= 100
            && part != "."
            && part != ".."
            && part.bytes().all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'-'))
    };
    match repository.split_once('/') {
        Some((owner, name)) if safe(owner) && safe(name) => {
            let home = std::env::var_os("HOME").ok_or_else(|| refused("home folder unavailable"))?;
            Ok(Path::new(&home).join("Factory").join(owner).join(name))
        }
        _ => Err(refused("repository name invalid")),
    }
}
/// Git without repository hooks, fsmonitor, prompts or system config.
fn git(directory: &Path, args: &[&str], limit: Duration) -> io::Result<Vec<u8>> {
    let mut child = std::process::Command::new("git")
        .args(["-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "credential.helper="])
        .args(args)
        .current_dir(directory)
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .process_group(0)
        .spawn()?;
    let mut out = child.stdout.take().ok_or_else(|| refused("git output unavailable"))?;
    let reader = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        (&mut out).take(MAX_FILE).read_to_end(&mut bytes).map(|_| bytes)
    });
    let until = Instant::now() + limit;
    let status = loop {
        if let Some(status) = child.try_wait()? {
            break status;
        }
        if Instant::now() >= until {
            unsafe { libc::killpg(child.id() as i32, libc::SIGKILL) };
            let _ = child.wait();
            return Err(refused("git command timed out"));
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    let bytes = reader.join().map_err(|_| refused("git output unavailable"))??;
    if !status.success() {
        return Err(refused("git command failed"));
    }
    Ok(bytes)
}

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
    #[allow(dead_code)]
    contributor: bool,
    max_children: u8,
}
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

/// The single task slot: which task is held and how far it has progressed.
enum Slot {
    Idle,
    Active(usize),
    Frozen { task: usize, tree: String, output: String },
    /// A checkpoint check failed; only `Read` and `Close` remain allowed.
    Failed(usize),
}

struct Workspace {
    plan: Plan,
    phase: String,
    manifest_digest: String,
    invocation: String,
    deadline: Instant,
    head: String,
    task: Slot,
    completed: BTreeSet<String>,
    events: Vec<Event>,
    root: PathBuf,
    journal: PathBuf,
    cancellation: AgentCancellation,
}

impl Workspace {
    fn open(
        run: &str,
        candidate: &str,
        manifest: &[u8],
        root: PathBuf,
        journal: PathBuf,
        cancellation: AgentCancellation,
    ) -> io::Result<Self> {
        let parsed: Manifest =
            serde_json::from_slice(manifest).map_err(|_| refused("workspace manifest invalid"))?;
        let raw = base64::engine::general_purpose::STANDARD
            .decode(&parsed.plan)
            .map_err(|_| refused("workspace plan encoding invalid"))?;
        let until = OffsetDateTime::parse(&parsed.deadline, &Rfc3339)
            .map_err(|_| refused("workspace deadline invalid"))?
            - OffsetDateTime::now_utc();
        if parsed.run != run
            || parsed.max_children != 0
            || !matches!(parsed.phase.as_str(), "implement" | "correct")
            || !matches!(parsed.effort.as_str(), "low" | "medium" | "high" | "xhigh")
            || parsed.invocation.is_empty()
            || parsed.invocation.len() > 128
            || parsed.invocation.chars().any(char::is_control)
            || digest(&raw) != parsed.plan_digest
            || until.whole_milliseconds() <= 0
            || until.whole_seconds() > 4 * 60 * 60
        {
            return Err(refused("workspace manifest binding invalid"));
        }
        let plan: Plan = serde_json::from_slice(&raw).map_err(|_| refused("workspace plan invalid"))?;
        if plan.source_commit != candidate
            || plan.tasks.is_empty()
            || plan.tasks.len() > 64
            || plan.checks.len() > 16
            || parsed.phase == "correct" && plan.tasks.len() != 1
        {
            return Err(refused("workspace plan scope invalid"));
        }
        let mut checks = BTreeSet::new();
        for check in &plan.checks {
            if !key(&check.id)
                || !checks.insert(check.id.clone())
                || check.argv.is_empty()
                || check.argv.len() > 64
                || check.argv.iter().any(|arg| arg.is_empty() || arg.contains('\0'))
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
                || task.owned_paths.iter().any(|p| !relative(p.strip_suffix('/').unwrap_or(p)))
                || task.check_ids.iter().any(|id| !plan.checks.iter().any(|c| &c.id == id))
            {
                return Err(refused("workspace task graph invalid"));
            }
            known.insert(task.id.clone());
        }
        Ok(Self {
            plan,
            phase: parsed.phase,
            manifest_digest: digest(manifest),
            invocation: String::new(),
            deadline: Instant::now() + Duration::from_millis(until.whole_milliseconds() as u64),
            head: candidate.into(),
            task: Slot::Idle,
            completed: BTreeSet::new(),
            events: Vec::new(),
            root,
            journal,
            cancellation,
        })
    }
    fn remaining(&self) -> io::Result<Duration> {
        let left = self.deadline.saturating_duration_since(Instant::now());
        if left.is_zero() || self.cancellation.cancelled() {
            return Err(refused("workspace deadline expired or cancelled"));
        }
        Ok(left)
    }
    fn git(&self, args: &[&str]) -> io::Result<Vec<u8>> {
        git(&self.root, args, Duration::from_secs(20).min(self.remaining()?))
    }
    fn event(&mut self, kind: &str, task: &str, detail: String) -> io::Result<()> {
        if self.events.len() >= 512 {
            return Err(refused("workspace journal full"));
        }
        let prev_digest = self.events.last().map(Event::digest).transpose()?.unwrap_or_default();
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
            .open(&self.journal)?;
        file.write_all(&serde_json::to_vec(&event)?)?;
        file.write_all(b"\n")?;
        file.sync_all()?;
        self.events.push(event);
        Ok(())
    }
    /// A path inside the worktree that is not, and does not pass through, a link.
    fn path(&self, relative_path: &str) -> io::Result<PathBuf> {
        if !relative(relative_path) {
            return Err(refused("workspace relative path invalid"));
        }
        let mut path = self.root.clone();
        let parts: Vec<_> = relative_path.split('/').collect();
        for (i, part) in parts.iter().enumerate() {
            path.push(part);
            match path.symlink_metadata() {
                Ok(meta) if meta.file_type().is_symlink() || i + 1 < parts.len() && !meta.is_dir() => {
                    return Err(refused("workspace path identity invalid"))
                }
                Ok(_) => (),
                Err(e) if e.kind() == io::ErrorKind::NotFound && i + 1 == parts.len() => (),
                Err(e) => return Err(e),
            }
        }
        Ok(path)
    }
    fn current(&self) -> Option<usize> {
        match self.task {
            Slot::Active(i) | Slot::Frozen { task: i, .. } | Slot::Failed(i) => Some(i),
            Slot::Idle => None,
        }
    }
    fn active_task(&self, id: &str) -> Option<usize> {
        self.current().filter(|&i| self.plan.tasks[i].id == id)
    }
    fn mutable_task(&self, id: &str) -> bool {
        matches!(self.task, Slot::Active(_)) && self.active_task(id).is_some()
    }
    fn owns(&self, path: &str) -> bool {
        self.current().is_some_and(|i| {
            self.plan.tasks[i]
                .owned_paths
                .iter()
                .any(|owned| owned == path || owned.ends_with('/') && path.starts_with(owned))
        })
    }
    fn owns_directory(&self, path: &str) -> bool {
        let prefix = format!("{path}/");
        self.owns(&prefix)
            || self.current().is_some_and(|i| {
                self.plan.tasks[i].owned_paths.iter().any(|owned| owned.starts_with(&prefix))
            })
    }
    /// Stage the worktree and return its tree, refusing changes outside the task.
    fn tree(&self) -> io::Result<String> {
        if oid(self.git(&["rev-parse", "HEAD"])?)? != self.head {
            return Err(refused("workspace HEAD moved outside Factory"));
        }
        let status = self.git(&["status", "--porcelain=v1", "-z", "--untracked-files=all"])?;
        let mut entries = status.split(|b| *b == 0).filter(|e| !e.is_empty());
        while let Some(entry) = entries.next() {
            let path = entry
                .get(3..)
                .and_then(|p| std::str::from_utf8(p).ok())
                .ok_or_else(|| refused("workspace changed path invalid"))?;
            if matches!(entry[0], b'R' | b'C') || matches!(entry[1], b'R' | b'C') {
                // A rename's second field is its source path; both must be owned.
                let source = entries
                    .next()
                    .and_then(|p| std::str::from_utf8(p).ok())
                    .ok_or_else(|| refused("workspace changed path invalid"))?;
                if !self.owns(source) {
                    return Err(refused("workspace change outside task ownership"));
                }
            }
            if !self.owns(path) {
                return Err(refused("workspace change outside task ownership"));
            }
            self.path(path)?;
        }
        self.git(&["add", "-A", "--", "."])?;
        oid(self.git(&["write-tree"])?)
    }
    /// Declared checks run as this user with this user's tools, each in its
    /// own process group, killed at its time limit or on cancellation.
    fn checks(&self, ids: &[String]) -> io::Result<Vec<CheckResult>> {
        let mut results = Vec::new();
        for id in ids {
            let check = self
                .plan
                .checks
                .iter()
                .find(|c| &c.id == id)
                .ok_or_else(|| refused("workspace unknown check"))?;
            let directory = if check.cwd == "." {
                self.root.clone()
            } else {
                self.path(check.cwd.trim_end_matches('/'))?
            };
            let log = self.journal.with_extension(format!("check-{id}.log"));
            let output = OpenOptions::new().write(true).create(true).truncate(true).mode(0o600).open(&log)?;
            // A relative program path names a file in the check's folder; a bare
            // name is found on this user's PATH.
            let program = if check.argv[0].contains('/') && !check.argv[0].starts_with('/') {
                directory.join(&check.argv[0])
            } else {
                PathBuf::from(&check.argv[0])
            };
            let mut child = std::process::Command::new(&program)
                .args(&check.argv[1..])
                .current_dir(&directory)
                .env("GIT_TERMINAL_PROMPT", "0")
                .stdin(Stdio::null())
                .stdout(Stdio::from(output.try_clone()?))
                .stderr(Stdio::from(output))
                .process_group(0)
                .spawn()
                .map_err(|_| refused("workspace check executable unavailable"))?;
            let until = Instant::now() + Duration::from_secs(check.timeout_seconds).min(self.remaining()?);
            let status = loop {
                if let Some(status) = child.try_wait()? {
                    break Some(status);
                }
                if Instant::now() >= until || self.cancellation.cancelled() {
                    unsafe { libc::killpg(child.id() as i32, libc::SIGKILL) };
                    let _ = child.wait();
                    break None;
                }
                std::thread::sleep(Duration::from_millis(50));
            };
            // Descendants that outlive the check are part of it.
            unsafe { libc::killpg(child.id() as i32, libc::SIGKILL) };
            let mut bytes = Vec::new();
            fs::File::open(&log)?.take(MAX_CHECK_OUTPUT).read_to_end(&mut bytes)?;
            results.push(CheckResult {
                id: id.clone(),
                passed: status.is_some_and(|s| s.success() && s.signal().is_none()),
                log_digest: digest(&bytes),
            });
        }
        Ok(results)
    }
    fn command(&mut self, command: Command) -> io::Result<Reply> {
        if !command.valid_operation_id() {
            return Err(refused("workspace operation identity invalid"));
        }
        self.remaining()?;
        if matches!(self.task, Slot::Failed(_)) && !matches!(&command, Command::Read { .. } | Command::Close) {
            return Err(refused("workspace task check failed"));
        }
        let mut reply = Reply { status: Status::Ok, head: self.head.clone(), ..Reply::default() };
        if let Command::BindParent { invocation } = &command {
            if !self.invocation.is_empty()
                || invocation.is_empty()
                || invocation.len() > 128
                || invocation.chars().any(char::is_control)
            {
                return Err(refused("workspace observed parent binding invalid"));
            }
            self.invocation = invocation.clone();
            if self.phase == "correct" {
                let task = self.plan.tasks[0].id.clone();
                self.event("parent_task_started", &task, String::new())?;
                self.task = Slot::Active(0);
                reply.events = self.events.clone();
            }
            reply.status = Status::Bound;
            return Ok(reply);
        }
        if self.invocation.is_empty() {
            return Err(refused("workspace parent not observed"));
        }
        let guarded_start = matches!(&command, Command::StartAfter { .. });
        if let Command::StartAfter { expected_head, .. } = &command {
            if expected_head != &self.head {
                reply.status = Status::StaleHead;
                return Ok(reply);
            }
        }
        match command {
            Command::BindParent { .. } => unreachable!(),
            Command::Observe => reply.events = self.events.clone(),
            Command::Start { task } | Command::StartAfter { task, .. } => {
                if self.active_task(&task).is_some() {
                    reply.events = self.events.clone();
                    return Ok(reply);
                }
                if !matches!(self.task, Slot::Idle) || self.completed.contains(&task) {
                    if guarded_start {
                        reply.status =
                            if self.completed.contains(&task) { Status::TaskCompleted } else { Status::TaskBusy };
                        return Ok(reply);
                    }
                    return Err(refused("workspace task already active or complete"));
                }
                let i = self
                    .plan
                    .tasks
                    .iter()
                    .position(|t| t.id == task)
                    .ok_or_else(|| refused("workspace unknown task"))?;
                if self.plan.tasks[i].depends_on.iter().any(|id| !self.completed.contains(id)) {
                    if guarded_start {
                        reply.status = Status::DependencyPending;
                        return Ok(reply);
                    }
                    return Err(refused("workspace dependency incomplete"));
                }
                self.event("parent_task_started", &task, String::new())?;
                self.task = Slot::Active(i);
            }
            Command::Read { task, path, offset, limit } => {
                if self.active_task(&task).is_none() {
                    return Err(refused("workspace task not active"));
                }
                if !(1..=MAX_TEXT).contains(&limit) {
                    return Err(refused("workspace read bound invalid"));
                }
                let open = self.path(&path).and_then(|path| {
                    OpenOptions::new().read(true).custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC).open(path)
                });
                let mut file = match open {
                    Ok(file) => file,
                    Err(error) if error.kind() == io::ErrorKind::NotFound => {
                        reply.status = Status::Missing;
                        return Ok(reply);
                    }
                    Err(error) => return Err(error),
                };
                let meta = file.metadata()?;
                if !meta.is_file() || meta.len() > MAX_FILE || offset as u64 > meta.len() {
                    return Err(refused("workspace read file invalid"));
                }
                let mut all = Vec::new();
                Read::by_ref(&mut file).take(MAX_FILE).read_to_end(&mut all)?;
                reply.file_digest = digest(&all);
                reply.total_bytes = Some(meta.len());
                reply.executable = Some(meta.mode() & 0o111 != 0);
                file.seek(SeekFrom::Start(offset as u64))?;
                let mut bytes = vec![];
                file.take(limit as u64).read_to_end(&mut bytes)?;
                // Text pages never silently corrupt a multibyte character.
                let text = match std::str::from_utf8(&bytes) {
                    Ok(text) => text,
                    Err(e) if e.error_len().is_none() && offset as u64 + (bytes.len() as u64) < meta.len() => {
                        std::str::from_utf8(&bytes[..e.valid_up_to()])
                            .map_err(|_| refused("workspace text invalid"))?
                    }
                    Err(_) => return Err(refused("workspace text invalid")),
                };
                reply.content = Some(text.into());
                if ((offset + text.len()) as u64) < meta.len() {
                    reply.next_offset = Some((offset + text.len()) as u64);
                }
            }
            Command::Edit { operation_id: _, task, path, offset, expected, replacement } => {
                if !self.mutable_task(&task)
                    || !self.owns(&path)
                    || expected.len() > MAX_TEXT
                    || replacement.len() > MAX_TEXT
                {
                    return Err(refused("workspace edit outside active task"));
                }
                let path = self.path(&path)?;
                let bytes = match fs::read(&path) {
                    Ok(bytes) => bytes,
                    Err(e) if e.kind() == io::ErrorKind::NotFound => Vec::new(),
                    Err(e) => return Err(e),
                };
                if bytes.len() as u64 > MAX_FILE {
                    return Err(refused("workspace edit file invalid"));
                }
                let text = std::str::from_utf8(&bytes).map_err(|_| refused("workspace edit requires text"))?;
                let end = offset.checked_add(expected.len()).ok_or_else(|| refused("workspace edit range invalid"))?;
                if text.get(offset..end) != Some(expected.as_str())
                    || bytes.len() - expected.len() + replacement.len() > MAX_FILE as usize
                {
                    reply.status = Status::PreimageMismatch;
                    reply.events = self.events.clone();
                    return Ok(reply);
                }
                let mut bytes = bytes;
                bytes.splice(offset..end, replacement.bytes());
                fs::write(&path, &bytes)?;
            }
            Command::SetExecutable { operation_id: _, task, path, expected_digest, executable } => {
                if !self.mutable_task(&task) || !self.owns(&path) {
                    return Err(refused("workspace executable outside task ownership"));
                }
                let path = self.path(&path)?;
                let meta = path.symlink_metadata()?;
                if !meta.is_file() || meta.len() > MAX_FILE {
                    return Err(refused("workspace executable file invalid"));
                }
                if digest(&fs::read(&path)?) != expected_digest {
                    reply.status = Status::PreimageMismatch;
                    reply.events = self.events.clone();
                    return Ok(reply);
                }
                // Only Git's executable bit is selectable.
                let mode = meta.mode() & 0o666;
                fs::set_permissions(
                    &path,
                    fs::Permissions::from_mode(if executable { mode | ((mode & 0o444) >> 2) } else { mode }),
                )?;
                reply.executable = Some(executable);
            }
            Command::MakeDirectory { operation_id: _, task, path } => {
                if !self.mutable_task(&task) || !self.owns_directory(&path) {
                    return Err(refused("workspace directory outside task ownership"));
                }
                let path = self.path(&path)?;
                if let Err(error) = fs::create_dir(&path) {
                    if error.kind() != io::ErrorKind::AlreadyExists || !path.symlink_metadata()?.is_dir() {
                        return Err(error);
                    }
                }
            }
            Command::Remove { operation_id: _, task, path, expected_digest } => {
                if !self.mutable_task(&task) || !self.owns(&path) {
                    return Err(refused("workspace removal outside task ownership"));
                }
                let path = self.path(&path)?;
                if !path.symlink_metadata()?.is_file() || digest(&fs::read(&path)?) != expected_digest {
                    return Err(refused("workspace removal preimage differs"));
                }
                fs::remove_file(&path)?;
            }
            Command::Release { task } => {
                let Some(i) = self.active_task(&task) else {
                    return Err(refused("workspace task not active"));
                };
                if let Slot::Frozen { tree, output, .. } = &self.task {
                    reply.tree = tree.clone();
                    reply.output_digest = output.clone();
                } else {
                    let tree = self.tree()?;
                    let output = digest(
                        format!("factory-task-output-v1\0{}\0{task}\0{}\0{tree}", self.manifest_digest, self.head)
                            .as_bytes(),
                    );
                    self.event("parent_output_observed", &task, output.clone())?;
                    self.task = Slot::Frozen { task: i, tree: tree.clone(), output: output.clone() };
                    reply.tree = tree;
                    reply.output_digest = output;
                }
            }
            Command::Checkpoint { task, output_digest, expected_head } => {
                let (i, frozen) = match &self.task {
                    Slot::Frozen { task: i, tree, output }
                        if expected_head == self.head
                            && self.plan.tasks[*i].id == task
                            && *output == output_digest =>
                    {
                        (*i, tree.clone())
                    }
                    _ => return Err(refused("workspace checkpoint binding differs")),
                };
                let ids = self.plan.tasks[i].check_ids.clone();
                reply.checks = self.checks(&ids)?;
                if self.tree()? != frozen {
                    return Err(refused("workspace check changed frozen output"));
                }
                if reply.checks.iter().any(|c| !c.passed) {
                    self.event("task_failed", &task, "check_failed".into())?;
                    self.task = Slot::Failed(i);
                    reply.status = Status::Failed;
                    reply.events = self.events.clone();
                    return Ok(reply);
                }
                self.event("checkpoint_prepared", &task, ids.join(" "))?;
                let head = self.head.clone();
                let commit = oid(self.git(&[
                    "-c", "user.name=Factory Native Checkpoint",
                    "-c", "user.email=factory@invalid.local",
                    "-c", "commit.gpgsign=false",
                    "commit-tree", &frozen, "-p", &head, "-m", "Factory task checkpoint",
                ])?)?;
                self.git(&["update-ref", "HEAD", &commit, &head])?;
                self.event("task_committed", &task, format!("tree={frozen} commit={commit}"))?;
                self.head = commit.clone();
                self.completed.insert(task);
                self.task = Slot::Idle;
                reply.status = Status::Committed;
                reply.head = commit;
                reply.tree = frozen;
            }
            Command::Final { expected_head } => {
                if expected_head != self.head
                    || !matches!(self.task, Slot::Idle)
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
                    reply.status = Status::Failed;
                    reply.events = self.events.clone();
                    return Ok(reply);
                }
                if !self.git(&["status", "--porcelain=v1", "-z", "--untracked-files=all"])?.is_empty()
                    || oid(self.git(&["rev-parse", "HEAD"])?)? != self.head
                {
                    return Err(refused("workspace final check changed candidate"));
                }
                let base = self.plan.source_commit.clone();
                if self.head == base {
                    return Err(refused("workspace final made no change"));
                }
                self.git(&["merge-base", "--is-ancestor", &base, &self.head])?;
                let target = self.journal.with_extension("candidate.bundle");
                let _ = fs::remove_file(&target);
                let target_text = target.to_string_lossy().into_owned();
                let exclude = format!("^{base}");
                self.git(&["bundle", "create", &target_text, "HEAD", &exclude])?;
                let bundle = fs::read(&target);
                let _ = fs::remove_file(&target);
                let bundle = bundle?;
                if bundle.is_empty() || bundle.len() > soda_execution_protocol::MAX_STAGE_BUNDLE {
                    return Err(refused("workspace final bundle exceeds bounds"));
                }
                reply.status = Status::Committed;
                reply.tree = tree;
                reply.bundle = bundle;
            }
            Command::Close => reply.status = Status::Closed,
        }
        reply.events = self.events.clone();
        Ok(reply)
    }
}

/// One coding run's worktree and its retained task receipts.
pub struct TaskWorkspace {
    workspace: Workspace,
    receipts: Option<super::workspace_receipts::Receipts>,
    profile_path: PathBuf,
    manifest: Vec<u8>,
    scope: OwnedScope,
}
struct OwnedScope {
    run: String,
    profile: String,
    source_sha256: String,
    candidate: String,
    tool_sha256: String,
    prompt_sha256: String,
}
impl OwnedScope {
    fn scope(&self) -> io::Result<ReceiptScope<'_>> {
        ReceiptScope::new(
            &self.run,
            &self.profile,
            &self.source_sha256,
            &self.candidate,
            Role::Coder,
            &self.tool_sha256,
            &self.prompt_sha256,
        )
    }
}

impl TaskWorkspace {
    /// Bring the run's staged source into the project clone and give the run
    /// its own worktree at the admitted commit.
    pub fn prepare(
        profile: &Path,
        scope: &ReceiptScope<'_>,
        manifest: &[u8],
        repository: &str,
        cancellation: AgentCancellation,
    ) -> io::Result<Self> {
        if scope.role != Role::Coder || manifest.is_empty() || manifest.len() > soda_execution_protocol::MAX_TASK_MANIFEST {
            return Err(refused("workspace manifest declaration invalid"));
        }
        super::receipt::profile(profile, scope.profile)?;
        let clone = repository_path(repository)?;
        let time = Duration::from_secs(60);
        if !clone.join(".git").is_dir() {
            fs::create_dir_all(&clone)?;
            git(&clone, &["init", "--quiet"], time)?;
        }
        // No network remote and no credential helper: an agent shell can't
        // reach the user's GitHub login through this repository.
        git(&clone, &["config", "credential.helper", ""], time)?;
        let bundle = super::local::run_bundle(scope.run, scope.profile)?;
        let bundle_text = bundle.to_string_lossy().into_owned();
        let base = format!("+HEAD:refs/factory/base/{}", scope.run);
        git(&clone, &["fetch", "--quiet", "--no-tags", "--no-write-fetch-head", &bundle_text, &base], time)?;
        let name = clone.file_name().ok_or_else(|| refused("repository name invalid"))?.to_string_lossy().into_owned();
        let worktree = clone.with_file_name(format!("{name}-tasks")).join(&scope.run[..12]);
        if worktree.exists() {
            return Err(refused("run worktree already exists"));
        }
        fs::create_dir_all(worktree.parent().ok_or_else(|| refused("worktree parent missing"))?)?;
        let worktree_text = worktree.to_string_lossy().into_owned();
        git(&clone, &["worktree", "add", "--quiet", "--detach", &worktree_text, scope.candidate], time)?;
        // The agent process launcher starts only in a directory private to
        // this user; git creates the worktree world-readable.
        fs::set_permissions(&worktree, fs::Permissions::from_mode(0o700))?;
        let journal = super::local::run_directory(scope.run, scope.profile)?.join("task.events");
        let workspace = Workspace::open(scope.run, scope.candidate, manifest, worktree, journal, cancellation)?;
        if oid(workspace.git(&["rev-parse", "HEAD"])?)? != scope.candidate {
            return Err(refused("workspace readiness differs"));
        }
        Ok(Self {
            workspace,
            receipts: None,
            profile_path: profile.to_path_buf(),
            manifest: manifest.to_vec(),
            scope: OwnedScope {
                run: scope.run.into(),
                profile: scope.profile.into(),
                source_sha256: scope.source_sha256.into(),
                candidate: scope.candidate.into(),
                tool_sha256: scope.tool_sha256().into(),
                prompt_sha256: scope.prompt_sha256().into(),
            },
        })
    }
    /// The worktree the agent works in.
    pub fn path(&self) -> &Path {
        &self.workspace.root
    }
    /// Bind only the invocation independently observed by the native provider
    /// owner. Preparation supplies no task authority before this one-time bind.
    pub fn bind_parent(&mut self, parent: &Parent) -> io::Result<()> {
        if self.receipts.is_some() {
            return Err(refused("workspace parent already bound"));
        }
        let receipts = super::workspace_receipts::Receipts::create(
            &self.profile_path,
            &self.scope.scope()?,
            &self.manifest,
            parent,
        )?;
        let command = Command::BindParent { invocation: parent.invocation.clone() };
        receipts.begin(&command)?;
        let reply = self.workspace.command(command)?;
        let command = Command::BindParent { invocation: parent.invocation.clone() };
        if reply.status != Status::Bound || reply.head != self.scope.candidate {
            return Err(refused("workspace parent acknowledgement differs"));
        }
        receipts.complete(&command, &reply)?;
        self.receipts = Some(receipts);
        Ok(())
    }
    pub fn command(&mut self, command: &Command) -> io::Result<Reply> {
        if matches!(command, Command::BindParent { .. }) {
            return Err(refused("workspace parent is native-owned"));
        }
        let receipts = self.receipts.as_ref().ok_or_else(|| refused("workspace parent not observed"))?.clone();
        let bytes = serde_json::to_vec(command)?;
        if bytes.is_empty() || bytes.len() > MAX_COMMAND {
            return Err(refused("workspace command exceeds bound"));
        }
        if command.retains_receipt() {
            if let Some(reply) = receipts.reply(command)? {
                return Ok(reply);
            }
            if receipts.request(command)? {
                return Err(io::Error::new(io::ErrorKind::WouldBlock, "task outcome remains pending"));
            }
            receipts.begin(command)?;
        }
        let reply = self.workspace.command(serde_json::from_slice(&bytes)?)?;
        if command.retains_receipt() {
            receipts.complete(command, &reply)?;
        }
        Ok(reply)
    }
}
