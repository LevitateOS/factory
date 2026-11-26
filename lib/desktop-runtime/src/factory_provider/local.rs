//! Same-user source staging. Agents are not isolated (see AGENTS.md): a run's
//! source bundle, setup and context reads live in this profile and git does the
//! work, as the person running Factory. No root, no other users, no broker.
use super::AgentCancellation;
use sha2::{Digest, Sha256};
use soda_execution_protocol::{
    ApprovedFiles, ContextEntry, ContextFile, ContextPage, ContextReadPage, ContextSelection,
    ProfileRunState, SetupReceipt, SourceContext, MAX_CONTEXT_BYTES, MAX_CONTEXT_ENTRIES,
    MAX_CONTEXT_FILE_BYTES, MAX_PREPARED_TREE_BYTES, MAX_SETUP_OUTPUT, MAX_STAGE_BUNDLE,
};
use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::io::{self, Read};
use std::os::unix::process::{CommandExt, ExitStatusExt};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::OnceLock;
use std::time::{Duration, Instant};

static ROOT: OnceLock<PathBuf> = OnceLock::new();

/// Called once the profile is locked, before recovery or any run.
pub(crate) fn configure(profile: &Path) {
    let _ = ROOT.set(profile.join("native-local"));
}

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}
fn id(value: &str, len: usize) -> bool {
    value.len() == len && value.bytes().all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
}
pub(crate) fn hash(bytes: &[u8]) -> String {
    Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect()
}

/// Runs and preparations are kept apart, as the broker kept them: startup
/// recovery retires leftover runs, while preparations outlive a restart.
#[derive(Clone, Copy)]
enum Kind {
    Run,
    Preparation,
}
fn place(kind: Kind, profile: &str, name: &str) -> io::Result<PathBuf> {
    if !id(profile, 32) || !id(name, 32) {
        return Err(refused("local staging selector invalid"));
    }
    let root = ROOT.get().ok_or_else(|| refused("local staging is not configured"))?;
    let folder = match kind {
        Kind::Run => "runs",
        Kind::Preparation => "preparations",
    };
    Ok(root.join(profile).join(folder).join(name))
}
fn create_private(path: &Path) -> io::Result<()> {
    use std::os::unix::fs::DirBuilderExt;
    fs::DirBuilder::new().recursive(true).mode(0o700).create(path)
}

/// Git with repository hooks, fsmonitor and external diff turned off, and no
/// prompts. Output past `limit` bytes is refused.
fn git(cwd: &Path, args: &[&str], limit: usize) -> io::Result<Vec<u8>> {
    let mut child = Command::new("git")
        .args(["-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "diff.external="])
        .args(args)
        .current_dir(cwd)
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()?;
    let mut bytes = Vec::new();
    let read = child
        .stdout
        .take()
        .ok_or_else(|| refused("git output unavailable"))?
        .take(limit as u64 + 1)
        .read_to_end(&mut bytes);
    if read.is_err() || bytes.len() > limit {
        let _ = child.kill();
        let _ = child.wait();
        return Err(refused("git output exceeds bound"));
    }
    if !child.wait()?.success() {
        return Err(refused("git command failed"));
    }
    Ok(bytes)
}
fn head_is(repository: &Path, reference: &str, commit: &str) -> io::Result<bool> {
    Ok(git(repository, &["rev-parse", "--verify", &format!("{reference}^{{commit}}")], 41)?
        == format!("{commit}\n").as_bytes())
}

/// A checkout-free clone of `bundle` in a fresh directory under `parent`.
fn clone_bundle(parent: &Path, bundle: &Path, name: &str, checkout: bool) -> io::Result<PathBuf> {
    let target = parent.join(name);
    if target.exists() {
        fs::remove_dir_all(&target)?;
    }
    let bundle = bundle.to_string_lossy();
    let target_text = target.to_string_lossy();
    let mut args = vec!["clone", "--quiet", "--no-local"];
    if !checkout {
        args.push("--no-checkout");
    }
    args.extend(["--", bundle.as_ref(), target_text.as_ref()]);
    git(parent, &args, 0)?;
    Ok(target)
}

fn write_bundle(directory: &Path, bundle: &[u8]) -> io::Result<String> {
    if bundle.is_empty() || bundle.len() > MAX_STAGE_BUNDLE {
        return Err(refused("local source bundle exceeds bounds"));
    }
    let sha256 = hash(bundle);
    let path = directory.join("bundle");
    match fs::read(&path) {
        Ok(existing) if hash(&existing) == sha256 => return Ok(sha256),
        Ok(_) => return Err(refused("local staging holds a different source")),
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    fs::write(directory.join(".bundle"), bundle)?;
    fs::rename(directory.join(".bundle"), &path)?;
    Ok(sha256)
}
fn staged_bundle(directory: &Path, sha256: &str) -> io::Result<Vec<u8>> {
    let bundle = fs::read(directory.join("bundle"))?;
    if hash(&bundle) != sha256 {
        return Err(refused("local staged source differs"));
    }
    Ok(bundle)
}

/// Keep a run's source and, when it came from a preparation, which one.
pub(crate) fn stage(
    run: &str,
    profile: &str,
    bundle: &[u8],
    preparation: Option<&str>,
) -> io::Result<String> {
    let directory = place(Kind::Run, profile, run)?;
    create_private(&directory)?;
    let sha256 = write_bundle(&directory, bundle)?;
    if let Some(preparation) = preparation {
        place(Kind::Preparation, profile, preparation)?;
        fs::write(directory.join("preparation"), preparation)?;
    }
    Ok(sha256)
}

/// Run approved `setup.sh` then `check.sh` in a fresh checkout of the source,
/// as this user with this user's tools. Never call this with an outside
/// contributor's head: Go only approves setup for the owner's own source.
pub(crate) fn prepare_source(
    key: &str,
    profile: &str,
    bundle: &[u8],
    approved: &ApprovedFiles,
    candidate: &str,
    cancellation: AgentCancellation,
) -> io::Result<SetupReceipt> {
    if !id(candidate, 40) || !approved.valid() {
        return Err(refused("local source preparation invalid"));
    }
    let limits = approved.limits().ok_or_else(|| refused("approved setup limits invalid"))?;
    let directory = place(Kind::Preparation, profile, key)?;
    create_private(&directory)?;
    write_bundle(&directory, bundle)?;
    let checkout = clone_bundle(&directory, &directory.join("bundle"), "checkout", true)?;
    if !head_is(&checkout, "HEAD", candidate)? {
        return Err(refused("prepared source differs from the admitted commit"));
    }
    let scripts = directory.join("approved");
    if scripts.exists() {
        fs::remove_dir_all(&scripts)?;
    }
    create_private(&scripts)?;
    for (name, content) in &approved.files {
        fs::write(scripts.join(name), content)?;
    }
    let mut receipt = SetupReceipt {
        prepared_source: None,
        worker_uid: unsafe { libc::geteuid() },
        setup_sha256: approved.digest(),
        source_commit: candidate.into(),
        setup_exit: None,
        check_exit: None,
        tools: BTreeMap::new(),
        missing: Vec::new(),
        output: String::new(),
        output_truncated: false,
    };
    let mut output = Vec::new();
    let setup = script(&checkout, &scripts.join("setup.sh"), limits.setup_seconds, &cancellation, &mut output)?;
    receipt.setup_exit = Some(setup);
    (receipt.tools, receipt.missing) = resolve_tools(approved);
    if setup == 0 && output.len() <= MAX_SETUP_OUTPUT && receipt.missing.is_empty() {
        let check = script(&checkout, &scripts.join("check.sh"), limits.check_seconds, &cancellation, &mut output)?;
        receipt.check_exit = Some(check);
    }
    if output.len() > MAX_SETUP_OUTPUT {
        let marker = b"\n[Factory: setup diagnostic output limit exceeded]\n";
        output.truncate(MAX_SETUP_OUTPUT - marker.len());
        output.extend_from_slice(marker);
        receipt.output_truncated = true;
    }
    receipt.output = String::from_utf8_lossy(&output).into_owned();
    fs::write(directory.join("setup.json"), serde_json::to_vec(&receipt)?)?;
    Ok(receipt)
}

/// One approved script under bash, killed with its whole process group at the
/// time limit or on cancellation. Output is kept up to one byte past the bound.
fn script(
    checkout: &Path,
    path: &Path,
    seconds: u32,
    cancellation: &AgentCancellation,
    output: &mut Vec<u8>,
) -> io::Result<i32> {
    let log = tempfile_in(checkout.parent().unwrap_or(checkout))?;
    let mut child = Command::new("/bin/bash")
        .args(["--noprofile", "--norc", "-e", "-u"])
        .arg(path)
        .current_dir(checkout)
        .env("GIT_TERMINAL_PROMPT", "0")
        .stdin(Stdio::null())
        .stdout(Stdio::from(log.0.try_clone()?))
        .stderr(Stdio::from(log.0.try_clone()?))
        .process_group(0)
        .spawn()?;
    let deadline = Instant::now() + Duration::from_secs(seconds.into());
    let status = loop {
        if let Some(status) = child.try_wait()? {
            break status;
        }
        if cancellation.cancelled() || Instant::now() >= deadline {
            unsafe { libc::killpg(child.id() as i32, libc::SIGKILL) };
            break child.wait()?;
        }
        std::thread::sleep(Duration::from_millis(50));
    };
    let mut captured = Vec::new();
    fs::File::open(&log.1)?
        .take((MAX_SETUP_OUTPUT + 1).saturating_sub(output.len()) as u64)
        .read_to_end(&mut captured)?;
    let _ = fs::remove_file(&log.1);
    output.extend_from_slice(&captured);
    if cancellation.cancelled() {
        return Err(io::Error::other("setup was cancelled"));
    }
    Ok(status.code().unwrap_or_else(|| 128 + status.signal().unwrap_or(0)))
}
fn tempfile_in(directory: &Path) -> io::Result<(fs::File, PathBuf)> {
    let path = directory.join(format!(".output-{}", std::process::id()));
    let file = fs::OpenOptions::new().write(true).create(true).truncate(true).open(&path)?;
    Ok((file, path))
}
/// Approved tool names found on this user's PATH.
fn resolve_tools(approved: &ApprovedFiles) -> (BTreeMap<String, String>, Vec<String>) {
    use std::os::unix::fs::PermissionsExt;
    let search = std::env::var_os("PATH").unwrap_or_default();
    let mut tools = BTreeMap::new();
    let mut missing = Vec::new();
    for name in &approved.tools {
        let found = std::env::split_paths(&search).find_map(|directory| {
            let path = directory.join(name).canonicalize().ok()?;
            path.metadata()
                .is_ok_and(|meta| meta.is_file() && meta.permissions().mode() & 0o111 != 0)
                .then_some(path)
        });
        match found {
            Some(path) => {
                tools.insert(name.clone(), path.to_string_lossy().into_owned());
            }
            None => missing.push(name.clone()),
        }
    }
    (tools, missing)
}

/// The setup receipt for a preparation, or for the preparation a run came from.
pub(crate) fn read_setup(
    run: &str,
    profile: &str,
    sha256: &str,
    candidate: &str,
) -> io::Result<SetupReceipt> {
    let own = place(Kind::Preparation, profile, run)?;
    let directory = if own.join("setup.json").exists() {
        staged_bundle(&own, sha256)?;
        own
    } else {
        let staged = place(Kind::Run, profile, run)?;
        staged_bundle(&staged, sha256)?;
        let preparation = fs::read_to_string(staged.join("preparation"))?;
        place(Kind::Preparation, profile, preparation.trim())?
    };
    let receipt: SetupReceipt = serde_json::from_slice(&fs::read(directory.join("setup.json"))?)
        .map_err(|_| refused("local setup receipt invalid"))?;
    if receipt.source_commit != candidate {
        return Err(refused("local setup receipt differs"));
    }
    Ok(receipt)
}

/// Finish a candidate's source from its base bundle and the run's delta.
pub(crate) fn complete_candidate(
    key: &str,
    profile: &str,
    sha256: &str,
    base: &str,
    candidate: &str,
    delta: &[u8],
) -> io::Result<Vec<u8>> {
    let source = [Kind::Preparation, Kind::Run]
        .into_iter()
        .map(|kind| place(kind, profile, key))
        .collect::<io::Result<Vec<_>>>()?
        .into_iter()
        .find(|directory| directory.join("bundle").exists())
        .ok_or_else(|| refused("candidate base source is not staged"))?;
    staged_bundle(&source, sha256)?;
    let scratch = scratch(profile)?;
    let result = (|| {
        let repository = import(&scratch, &source.join("bundle"), base, candidate, delta)?;
        let out = scratch.join("complete.bundle");
        let out_text = out.to_string_lossy();
        git(&repository, &["bundle", "create", out_text.as_ref(), "HEAD"], 0)?;
        let bundle = fs::read(&out)?;
        if bundle.is_empty() || bundle.len() > MAX_STAGE_BUNDLE {
            return Err(refused("complete candidate source exceeds bounds"));
        }
        Ok(bundle)
    })();
    let _ = fs::remove_dir_all(&scratch);
    result
}
fn scratch(profile: &str) -> io::Result<PathBuf> {
    let root = ROOT.get().ok_or_else(|| refused("local staging is not configured"))?;
    let mut name = [0u8; 16];
    getrandom(&mut name)?;
    let path = root.join(profile).join("scratch").join(hash(&name)[..32].to_owned());
    create_private(&path)?;
    Ok(path)
}
fn getrandom(bytes: &mut [u8]) -> io::Result<()> {
    fs::File::open("/dev/urandom")?.read_exact(bytes)
}
/// Clone `bundle` (at `base`), then fetch the candidate delta on top of it.
fn import(scratch: &Path, bundle: &Path, base: &str, candidate: &str, delta: &[u8]) -> io::Result<PathBuf> {
    let repository = clone_bundle(scratch, bundle, "repo", false)?;
    if !head_is(&repository, "HEAD", base)? {
        return Err(refused("candidate base differs"));
    }
    if delta.is_empty() {
        return Ok(repository);
    }
    if delta.len() > MAX_STAGE_BUNDLE {
        return Err(refused("candidate delta exceeds bounds"));
    }
    let path = scratch.join("delta.bundle");
    fs::write(&path, delta)?;
    let path_text = path.to_string_lossy();
    git(
        &repository,
        &["fetch", "--quiet", "--no-tags", "--no-write-fetch-head", path_text.as_ref(), "HEAD:refs/heads/factory-candidate"],
        0,
    )?;
    if !head_is(&repository, "refs/heads/factory-candidate", candidate)? {
        return Err(refused("candidate imported head differs"));
    }
    git(&repository, &["merge-base", "--is-ancestor", base, candidate], 0)?;
    git(&repository, &["symbolic-ref", "HEAD", "refs/heads/factory-candidate"], 0)?;
    Ok(repository)
}

/// Repository files, directory entries and a diff for a source selection.
pub(crate) fn context(
    key: &str,
    profile: &str,
    sha256: &str,
    candidate: &str,
    selection: &ContextSelection,
    source: &[u8],
    delta: &[u8],
) -> io::Result<SourceContext> {
    if !selection.valid() || selection.source_commit != candidate {
        return Err(refused("context selector invalid"));
    }
    let scratch = scratch(profile)?;
    let result = (|| {
        let bundle = if source.is_empty() {
            let directory = place(Kind::Preparation, profile, key)?;
            staged_bundle(&directory, sha256)?;
            directory.join("bundle")
        } else {
            let path = scratch.join("source.bundle");
            fs::write(&path, source)?;
            path
        };
        let repository = import(&scratch, &bundle, candidate, &selection.candidate, delta)?;
        read_context(&repository, selection)
    })();
    let _ = fs::remove_dir_all(&scratch);
    result
}
fn read_context(repository: &Path, request: &ContextSelection) -> io::Result<SourceContext> {
    for commit in [&request.source_commit, &request.approved_base, &request.diff_base, &request.candidate] {
        if !head_is(repository, commit, commit)? {
            return Err(refused("context commit differs"));
        }
    }
    git(repository, &["merge-base", "--is-ancestor", &request.approved_base, &request.candidate], 0)?;
    let mut paths = BTreeSet::from(["AGENTS.md".to_owned()]);
    let instructions = request
        .directory
        .as_ref()
        .filter(|path| !path.is_empty())
        .map(|directory| format!("{directory}/AGENTS.md"));
    for path in request.paths.iter().chain(instructions.iter()) {
        paths.insert(path.clone());
        let mut parent = Path::new(path).parent();
        while let Some(directory) = parent.filter(|p| !p.as_os_str().is_empty()) {
            paths.insert(format!("{}/AGENTS.md", directory.display()));
            parent = directory.parent();
        }
    }
    let mut files = Vec::new();
    let mut total = 0;
    let mut page = None;
    for path in paths {
        let Some(object) = blob(repository, &request.candidate, &path)? else { continue };
        let content = match request.page.as_ref().filter(|page| page.path == path) {
            Some(selection) => {
                let (content, metadata) = read_page(repository, &object, selection)?;
                page = Some(metadata);
                content
            }
            None => {
                let content = git(repository, &["cat-file", "blob", &object], MAX_CONTEXT_FILE_BYTES)?;
                if std::str::from_utf8(&content).is_err() {
                    return Err(refused("context file is not text"));
                }
                content
            }
        };
        total += content.len();
        if files.len() >= 12 || total > MAX_CONTEXT_BYTES {
            return Err(refused("context instruction closure exceeds bounds"));
        }
        files.push(ContextFile { path, content });
    }
    let entries = match &request.directory {
        Some(directory) => directory_entries(repository, &request.candidate, directory, MAX_CONTEXT_BYTES - total)?,
        None => Vec::new(),
    };
    total += entries.iter().map(|entry| entry.path.len() + entry.kind.len()).sum::<usize>();
    let diff = git(
        repository,
        &["diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", &request.diff_base, &request.candidate, "--"],
        MAX_CONTEXT_BYTES - total,
    )?;
    if std::str::from_utf8(&diff).is_err() {
        return Err(refused("context diff is not text"));
    }
    Ok(SourceContext { files, diff, entries, page })
}
fn blob(repository: &Path, commit: &str, path: &str) -> io::Result<Option<String>> {
    let entry = git(repository, &["ls-tree", "-z", commit, "--", &format!(":(literal){path}")], 1024)?;
    if entry.is_empty() {
        return Ok(None);
    }
    let tab = entry.iter().position(|b| *b == b'\t').ok_or_else(|| refused("context tree entry invalid"))?;
    if !entry.ends_with(&[0]) || &entry[tab + 1..entry.len() - 1] != path.as_bytes() {
        return Err(refused("context path differs"));
    }
    let header = std::str::from_utf8(&entry[..tab]).map_err(|_| refused("context object invalid"))?;
    let fields: Vec<_> = header.split(' ').collect();
    if fields.len() != 3 || !matches!(fields[0], "100644" | "100755") || fields[1] != "blob" {
        return Err(refused("context selection is not a regular file"));
    }
    Ok(Some(fields[2].to_owned()))
}
fn read_page(repository: &Path, object: &str, request: &ContextReadPage) -> io::Result<(Vec<u8>, ContextPage)> {
    let size = git(repository, &["cat-file", "-s", object], 32)?;
    let total: u64 = std::str::from_utf8(&size)
        .ok()
        .and_then(|text| text.trim().parse().ok())
        .filter(|size| *size <= MAX_PREPARED_TREE_BYTES && request.offset <= *size)
        .ok_or_else(|| refused("source page offset or file size exceeds bounds"))?;
    let all = git(repository, &["cat-file", "blob", object], total as usize)?;
    let end = (request.offset + request.limit as u64).min(total) as usize;
    let mut bytes = all[request.offset as usize..end].to_vec();
    if let Err(error) = std::str::from_utf8(&bytes) {
        if error.error_len().is_some() || end as u64 == total {
            return Err(refused("source page is not UTF-8 text"));
        }
        bytes.truncate(error.valid_up_to());
    }
    let next = request.offset + bytes.len() as u64;
    Ok((
        bytes,
        ContextPage { path: request.path.clone(), offset: request.offset, total_bytes: total, next_offset: (next < total).then_some(next) },
    ))
}
fn directory_entries(repository: &Path, commit: &str, directory: &str, limit: usize) -> io::Result<Vec<ContextEntry>> {
    let tree = if directory.is_empty() { commit.to_owned() } else { format!("{commit}:{directory}") };
    let bytes = git(repository, &["ls-tree", "-z", &tree], limit)?;
    let mut entries = Vec::new();
    for entry in bytes.split(|b| *b == 0).filter(|entry| !entry.is_empty()) {
        let tab = entry.iter().position(|b| *b == b'\t').ok_or_else(|| refused("source tree entry invalid"))?;
        let header = std::str::from_utf8(&entry[..tab]).map_err(|_| refused("source tree header invalid"))?;
        let name = std::str::from_utf8(&entry[tab + 1..]).map_err(|_| refused("source tree path is not text"))?;
        let path = if directory.is_empty() { name.to_owned() } else { format!("{directory}/{name}") };
        if name.contains('/') || !ContextSelection::valid_path(&path) || entries.len() >= MAX_CONTEXT_ENTRIES {
            return Err(refused("source directory exceeds path or entry bounds"));
        }
        let kind = match header.split(' ').collect::<Vec<_>>().as_slice() {
            ["100644" | "100755", "blob", _] => "file",
            ["040000", "tree", _] => "directory",
            ["120000", "blob", _] => "symlink",
            ["160000", "commit", _] => "submodule",
            _ => return Err(refused("source tree entry kind invalid")),
        };
        entries.push(ContextEntry { path, kind: kind.to_owned() });
    }
    Ok(entries)
}

/// Remove a staged run or preparation holding exactly this source. False when
/// nothing was staged.
pub(crate) fn retire(run: &str, profile: &str, sha256: &str) -> io::Result<bool> {
    for kind in [Kind::Run, Kind::Preparation] {
        let directory = place(kind, profile, run)?;
        if directory.join("bundle").exists() {
            staged_bundle(&directory, sha256)?;
            fs::remove_dir_all(&directory)?;
            return Ok(true);
        }
    }
    Ok(false)
}
/// A leftover staged run for startup recovery. Same-user runs have no
/// separate launch declaration, so every leftover reports as launched and
/// goes through the run's own recovery.
pub(crate) fn next_run(profile: &str) -> io::Result<Option<(String, String, ProfileRunState)>> {
    let runs = place(Kind::Run, profile, &"0".repeat(32))?.parent().map(Path::to_path_buf).unwrap_or_default();
    let entries = match fs::read_dir(&runs) {
        Ok(entries) => entries,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    for entry in entries {
        let entry = entry?;
        let name = entry.file_name().to_string_lossy().into_owned();
        if !id(&name, 32) {
            continue;
        }
        match fs::read(entry.path().join("bundle")) {
            Ok(bundle) => return Ok(Some((name, hash(&bundle), ProfileRunState::Launched))),
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                fs::remove_dir_all(entry.path())?;
            }
            Err(error) => return Err(error),
        }
    }
    Ok(None)
}

/// A staged run's own folder, for files that live and retire with the run.
pub(crate) fn run_directory(run: &str, profile: &str) -> io::Result<PathBuf> {
    let directory = place(Kind::Run, profile, run)?;
    if !directory.join("bundle").is_file() {
        return Err(refused("run source is not staged"));
    }
    Ok(directory)
}
/// The staged source bundle of a run.
pub(crate) fn run_bundle(run: &str, profile: &str) -> io::Result<PathBuf> {
    Ok(run_directory(run, profile)?.join("bundle"))
}
