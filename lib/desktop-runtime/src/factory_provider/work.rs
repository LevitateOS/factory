//! Human recovery into a native-registered fresh checkout. No provider state.
use super::{read_intent, read_retained, LaunchIntent};
use crate::private_fs::{private_dir, read_bytes, write_bytes};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::BTreeMap;
use std::ffi::CString;
use std::fs::{self, File, OpenOptions};
use std::io;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use std::time::{Duration, Instant};

fn denied() -> io::Error {
    io::Error::new(
        io::ErrorKind::PermissionDenied,
        "native work recovery target or retained work unavailable",
    )
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Target {
    id: String,
    path: PathBuf,
    device: u64,
    inode: u64,
}
impl Target {
    fn current(&self) -> io::Result<()> {
        let meta = self.path.symlink_metadata()?;
        if !meta.is_dir()
            || meta.uid() != unsafe { libc::geteuid() }
            || meta.dev() != self.device
            || meta.ino() != self.inode
            || self.path.canonicalize()? != self.path
        {
            return Err(denied());
        }
        Ok(())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Installed {
    intent: String,
    target: String,
    candidate: String,
}
pub(crate) struct Work {
    directory: PathBuf,
    profile: PathBuf,
    profile_id: String,
    alive: Arc<AtomicBool>,
    targets: Mutex<BTreeMap<String, Arc<Mutex<Target>>>>,
}
impl Work {
    pub(crate) fn new(
        profile: &Path,
        profile_id: &str,
        alive: Arc<AtomicBool>,
    ) -> io::Result<Self> {
        let directory = profile.join("native-recovery-targets");
        private_dir(&directory).map_err(|_| denied())?;
        let mut targets = BTreeMap::new();
        for entry in fs::read_dir(&directory)? {
            if targets.len() >= 64 {
                return Err(denied());
            }
            let path = entry?.path();
            let id = path
                .file_stem()
                .and_then(|s| s.to_str())
                .filter(|s| super::hex(s, 32))
                .ok_or_else(denied)?;
            if path.extension().and_then(|s| s.to_str()) != Some("json") {
                return Err(denied());
            }
            let target: Target =
                serde_json::from_slice(&read_bytes(&path, 8192)?).map_err(|_| denied())?;
            if target.id != id || !target.path.is_absolute() {
                return Err(denied());
            }
            targets.insert(target.id.clone(), Arc::new(Mutex::new(target)));
        }
        Ok(Self {
            directory,
            profile: profile.into(),
            profile_id: profile_id.into(),
            alive,
            targets: Mutex::new(targets),
        })
    }
    pub(crate) fn register(&self, path: &Path) -> io::Result<String> {
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        let path = path.canonicalize()?;
        let meta = path.symlink_metadata()?;
        if !meta.is_dir() || meta.uid() != unsafe { libc::geteuid() } {
            return Err(denied());
        }
        let mut targets = self.targets.lock().map_err(|_| denied())?;
        for target in targets.values() {
            let target = target.lock().map_err(|_| denied())?;
            if target.path == path && target.device == meta.dev() && target.inode == meta.ino() {
                return Ok(target.id.clone());
            }
        }
        if targets.len() >= 64 || fs::read_dir(&path)?.next().is_some() {
            return Err(denied());
        }
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).map_err(io::Error::other)?;
        let id: String = random.iter().map(|b| format!("{b:02x}")).collect();
        let target = Target {
            id: id.clone(),
            path,
            device: meta.dev(),
            inode: meta.ino(),
        };
        write_bytes(
            &self.directory.join(format!("{id}.json")),
            &serde_json::to_vec(&target).map_err(io::Error::other)?,
        )?;
        targets.insert(id.clone(), Arc::new(Mutex::new(target)));
        Ok(id)
    }
    pub(crate) fn recover(
        &self,
        raw: &str,
        id: &str,
        preparations: &super::preparations::Preparations,
    ) -> io::Result<Value> {
        if !self.alive.load(Ordering::SeqCst) || !super::hex(id, 32) {
            return Err(denied());
        }
        let target = self
            .targets
            .lock()
            .map_err(|_| denied())?
            .get(id)
            .cloned()
            .ok_or_else(denied)?;
        let target = target.lock().map_err(|_| denied())?;
        target.current()?;
        let intent = LaunchIntent::from_go(raw)?;
        let scope = intent.scope()?;
        if scope.profile != self.profile_id
            || read_intent(&self.profile, &scope)?.is_none_or(|saved| saved.raw() != raw)
        {
            return Err(denied());
        }
        let receipt = read_retained(&self.profile, &scope)?.ok_or_else(denied)?;
        let candidate = match receipt.candidate_state.as_str() {
            "captured" => receipt.result_candidate.as_deref().ok_or_else(denied)?,
            "unchanged" => scope.candidate,
            "not_applicable" if scope.role == super::Role::Reviewer => scope.candidate,
            _ => return Err(denied()),
        };
        let expected = Installed {
            intent: intent.digest(),
            target: id.into(),
            candidate: candidate.into(),
        };
        let checkout = target.path.join("checkout");
        if checkout.try_exists()? {
            let metadata = checkout.symlink_metadata()?;
            let git = checkout.join(".git");
            let git_meta = git.symlink_metadata()?;
            if !metadata.is_dir()
                || metadata.uid() != unsafe { libc::geteuid() }
                || !git_meta.is_dir()
                || git_meta.uid() != unsafe { libc::geteuid() }
            {
                return Err(denied());
            }
            let installed: Installed =
                serde_json::from_slice(&read_bytes(&git.join("factory-recovery.json"), 8192)?)
                    .map_err(|_| denied())?;
            if installed != expected {
                return Err(denied());
            }
            return Ok(
                json!({"intent_digest":expected.intent,"checkout_id":id,"candidate":candidate,"reused":true}),
            );
        }
        let base = preparations.recovery_bundle(&intent)?;
        let pending = target.path.join(format!(".factory-recovery-{id}"));
        if pending.try_exists()? {
            return Err(io::Error::other(
                "interrupted native recovery requires a fresh empty target",
            ));
        }
        private_dir(&pending).map_err(|_| denied())?;
        private_dir(&pending.join("tmp")).map_err(|_| denied())?;
        write_bytes(&pending.join("base.bundle"), &base)?;
        let work = pending.join("checkout");
        private_dir(&work).map_err(|_| denied())?;
        git(
            &self.alive,
            &pending,
            &pending,
            &[
                "clone",
                "--quiet",
                "--no-local",
                "--no-checkout",
                "--",
                path_text(&pending.join("base.bundle"))?,
                path_text(&work)?,
            ],
        )?;
        if git(
            &self.alive,
            &pending,
            &work,
            &["rev-parse", "--verify", "HEAD^{commit}"],
        )? != format!("{}\n", scope.candidate).as_bytes()
        {
            return Err(denied());
        }
        if receipt.candidate_state == "captured" {
            write_bytes(&pending.join("candidate.bundle"), &receipt.candidate_bundle)?;
            git(
                &self.alive,
                &pending,
                &work,
                &[
                    "fetch",
                    "--quiet",
                    "--no-tags",
                    "--no-write-fetch-head",
                    path_text(&pending.join("candidate.bundle"))?,
                    "HEAD:refs/heads/factory-recovered",
                ],
            )?;
            if git(
                &self.alive,
                &pending,
                &work,
                &[
                    "rev-parse",
                    "--verify",
                    "refs/heads/factory-recovered^{commit}",
                ],
            )? != format!("{candidate}\n").as_bytes()
            {
                return Err(denied());
            }
            git(
                &self.alive,
                &pending,
                &work,
                &["merge-base", "--is-ancestor", scope.candidate, candidate],
            )?;
            git(
                &self.alive,
                &pending,
                &work,
                &["symbolic-ref", "HEAD", "refs/heads/factory-recovered"],
            )?;
        }
        git(
            &self.alive,
            &pending,
            &work,
            &["remote", "remove", "origin"],
        )?;
        let tree = git(
            &self.alive,
            &pending,
            &work,
            &["ls-tree", "-r", "-l", "-z", "HEAD"],
        )?;
        let mut entries = 0usize;
        let mut total = 0u64;
        for entry in tree.split(|b| *b == 0).filter(|s| !s.is_empty()) {
            entries += 1;
            let tab = entry.iter().position(|b| *b == b'\t').ok_or_else(denied)?;
            let header = std::str::from_utf8(&entry[..tab]).map_err(|_| denied())?;
            let fields: Vec<_> = header.split_whitespace().collect();
            if fields.len() != 4
                || !matches!(fields[0], "100644" | "100755" | "120000")
                || fields[1] != "blob"
            {
                return Err(denied());
            }
            total = total
                .checked_add(fields[3].parse::<u64>().map_err(|_| denied())?)
                .ok_or_else(denied)?;
            if entries > 20000 || total > 64 * 1024 * 1024 {
                return Err(io::Error::other("recovered source exceeds checkout bounds"));
            }
        }
        git(
            &self.alive,
            &pending,
            &work,
            &["reset", "--hard", "--quiet", "HEAD"],
        )?;
        if !git(
            &self.alive,
            &pending,
            &work,
            &["status", "--porcelain=v1", "-z", "--untracked-files=all"],
        )?
        .is_empty()
        {
            return Err(denied());
        }
        let mut paths = vec![(work.clone(), 0usize)];
        let mut entries = 0usize;
        let mut bytes = 0u64;
        while let Some((path, depth)) = paths.pop() {
            if depth > 64 {
                return Err(denied());
            }
            for entry in fs::read_dir(&path)? {
                let entry = entry?;
                if path == work && entry.file_name() == ".git" {
                    continue;
                }
                entries += 1;
                let meta = entry.path().symlink_metadata()?;
                if meta.is_dir() {
                    paths.push((entry.path(), depth + 1));
                } else if meta.is_file() {
                    bytes = bytes.checked_add(meta.len()).ok_or_else(denied)?;
                } else if meta.file_type().is_symlink() {
                    if fs::read_link(entry.path())?.is_absolute()
                        || !entry.path().canonicalize()?.starts_with(&work)
                    {
                        return Err(denied());
                    }
                } else {
                    return Err(denied());
                }
                if entries > 20000 || bytes > 64 * 1024 * 1024 {
                    return Err(io::Error::other("recovered working tree exceeds bounds"));
                }
            }
        }
        write_bytes(
            &work.join(".git/factory-recovery.json"),
            &serde_json::to_vec(&expected).map_err(io::Error::other)?,
        )?;
        target.current()?;
        if !self.alive.load(Ordering::SeqCst) {
            return Err(denied());
        }
        let from = CString::new(work.as_os_str().as_encoded_bytes()).map_err(|_| denied())?;
        let to = CString::new(checkout.as_os_str().as_encoded_bytes()).map_err(|_| denied())?;
        if unsafe {
            libc::renameat2(
                libc::AT_FDCWD,
                from.as_ptr(),
                libc::AT_FDCWD,
                to.as_ptr(),
                libc::RENAME_NOREPLACE,
            )
        } != 0
        {
            return Err(io::Error::last_os_error());
        }
        File::open(&target.path)?.sync_all()?;
        // Only files owned by this successful staging operation are removed.
        fs::remove_file(pending.join("base.bundle"))?;
        if receipt.candidate_state == "captured" {
            fs::remove_file(pending.join("candidate.bundle"))?;
        }
        fs::remove_file(pending.join("git-output"))?;
        fs::remove_dir(pending.join("tmp"))?;
        fs::remove_dir(pending)?;
        Ok(
            json!({"intent_digest":expected.intent,"checkout_id":id,"candidate":candidate,"reused":false}),
        )
    }
}
fn path_text(path: &Path) -> io::Result<&str> {
    path.to_str().ok_or_else(denied)
}
fn git(alive: &AtomicBool, scratch: &Path, directory: &Path, args: &[&str]) -> io::Result<Vec<u8>> {
    if !alive.load(Ordering::SeqCst) {
        return Err(denied());
    }
    let output = OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(scratch.join("git-output"))?;
    let mut command = Command::new("/usr/bin/timeout");
    command
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .env("LANG", "C.UTF-8")
        .env("HOME", scratch)
        .env("TMPDIR", scratch.join("tmp"))
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_LFS_SKIP_SMUDGE", "1")
        .env("GIT_ATTR_NOSYSTEM", "1")
        .env("GIT_NO_REPLACE_OBJECTS", "1")
        .current_dir(directory)
        .args([
            "-k",
            "1s",
            "20s",
            "/usr/bin/git",
            "-c",
            "core.hooksPath=/dev/null",
            "-c",
            "core.fsmonitor=false",
            "-c",
            "credential.helper=",
            "-c",
            "protocol.allow=never",
            "-c",
            "protocol.file.allow=always",
            "-c",
            "core.attributesFile=/dev/null",
            "-c",
            "core.autocrlf=false",
            "-c",
            "gc.auto=0",
            "-c",
            "maintenance.auto=false",
        ])
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::from(output))
        .stderr(Stdio::null())
        .process_group(0);
    crate::child_process::parent_death(&mut command, libc::SIGKILL);
    unsafe {
        command.pre_exec(|| {
            for (resource, maximum) in [
                (libc::RLIMIT_FSIZE, 64 * 1024 * 1024),
                (libc::RLIMIT_AS, 512 * 1024 * 1024),
            ] {
                let limit = libc::rlimit {
                    rlim_cur: maximum,
                    rlim_max: maximum,
                };
                if libc::setrlimit(resource, &limit) != 0 {
                    return Err(io::Error::last_os_error());
                }
            }
            Ok(())
        });
    }
    let mut child = command.spawn()?;
    let deadline = Instant::now() + Duration::from_secs(22);
    let status = loop {
        match child.try_wait() {
            Ok(Some(status)) => break status,
            Ok(None) => (),
            Err(error) => {
                unsafe { libc::kill(-(child.id() as i32), libc::SIGKILL) };
                let _ = child.wait();
                return Err(error);
            }
        }
        if !alive.load(Ordering::SeqCst) || Instant::now() >= deadline {
            unsafe { libc::kill(-(child.id() as i32), libc::SIGKILL) };
            let _ = child.wait();
            return Err(io::Error::other("native recovery Git operation cancelled"));
        }
        std::thread::sleep(Duration::from_millis(20));
    };
    if !status.success() {
        return Err(io::Error::other("native recovery Git operation failed"));
    }
    read_bytes(&scratch.join("git-output"), 8 * 1024 * 1024)
}
