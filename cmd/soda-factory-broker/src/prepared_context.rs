//! Read Git objects only in the isolated, read-only context service.
use super::{git, private_process, refused, CHECKOUT};
use soda_execution_protocol::{
    ContextFile, ContextSelection, SourceContext, MAX_CONTEXT_BYTES, MAX_CONTEXT_REQUEST,
};
use std::collections::BTreeSet;
use std::fs::{File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::process::Stdio;

fn query(args: &[&str], limit: usize) -> io::Result<Vec<u8>> {
    let mut command = git(&[
        "-c",
        "safe.directory=/run/factory-output/repo",
        "-c",
        "core.fsmonitor=false",
        "-c",
        "core.hooksPath=/dev/null",
        "-c",
        "diff.external=",
        "-C",
        CHECKOUT,
    ]);
    let mut child = command
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .spawn()?;
    let mut bytes = Vec::new();
    let result = child
        .stdout
        .take()
        .ok_or_else(|| refused("context pipe unavailable"))?
        .take(limit as u64 + 1)
        .read_to_end(&mut bytes);
    if result.is_err() || bytes.len() > limit {
        let _ = child.kill();
        let _ = child.wait();
        return Err(refused("context exceeds byte bound"));
    }
    if !child.wait()?.success() {
        return Err(refused("context Git query failed"));
    }
    Ok(bytes)
}
fn file(commit: &str, path: &str) -> io::Result<Option<Vec<u8>>> {
    let entry = query(
        &["ls-tree", "-z", commit, "--", &format!(":(literal){path}")],
        1024,
    )?;
    if entry.is_empty() {
        return Ok(None);
    }
    let tab = entry
        .iter()
        .position(|b| *b == b'\t')
        .ok_or_else(|| refused("context tree entry invalid"))?;
    if !entry.ends_with(&[0]) || &entry[tab + 1..entry.len() - 1] != path.as_bytes() {
        return Err(refused("context path differs"));
    }
    let header =
        std::str::from_utf8(&entry[..tab]).map_err(|_| refused("context object invalid"))?;
    let fields: Vec<_> = header.split(' ').collect();
    if fields.len() != 3 || !matches!(fields[0], "100644" | "100755") || fields[1] != "blob" {
        return Err(refused("context selection is not a regular file"));
    }
    let bytes = query(&["cat-file", "blob", fields[2]], 32768)?;
    if std::str::from_utf8(&bytes).is_err() {
        return Err(refused("context file is not text"));
    }
    Ok(Some(bytes))
}
pub(super) fn run() -> io::Result<()> {
    private_process()?;
    let input = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open("/run/factory-context-request")?;
    let meta = input.metadata()?;
    if !meta.is_file()
        || meta.uid() != 0
        || meta.mode() & 0o7777 != 0o444
        || meta.len() > MAX_CONTEXT_REQUEST as u64
    {
        return Err(refused("context request metadata invalid"));
    }
    let mut bytes = Vec::new();
    input
        .take(MAX_CONTEXT_REQUEST as u64 + 1)
        .read_to_end(&mut bytes)?;
    let request: ContextSelection =
        serde_json::from_slice(&bytes).map_err(|_| refused("context request invalid"))?;
    if !request.valid() {
        return Err(refused("context selection invalid"));
    }
    for commit in [
        &request.source_commit,
        &request.approved_base,
        &request.diff_base,
        &request.candidate,
    ] {
        if query(
            &["rev-parse", "--verify", &format!("{commit}^{{commit}}")],
            41,
        )? != format!("{commit}\n").as_bytes()
        {
            return Err(refused("context commit differs"));
        }
    }
    if !query(
        &[
            "merge-base",
            "--is-ancestor",
            &request.approved_base,
            &request.candidate,
        ],
        0,
    )?
    .is_empty()
    {
        return Err(refused("context candidate ancestry invalid"));
    }
    let mut paths = BTreeSet::from(["AGENTS.md".to_owned()]);
    for path in &request.paths {
        paths.insert(path.clone());
        let mut parent = std::path::Path::new(path).parent();
        while let Some(directory) = parent.filter(|p| !p.as_os_str().is_empty()) {
            paths.insert(format!("{}/AGENTS.md", directory.display()));
            parent = directory.parent();
        }
    }
    let mut files = Vec::new();
    let mut total = 0;
    for path in paths {
        if let Some(content) = file(&request.candidate, &path)? {
            total += content.len();
            if files.len() >= 12 || total > MAX_CONTEXT_BYTES {
                return Err(refused("context instruction closure exceeds bounds"));
            }
            files.push(ContextFile { path, content });
        }
    }
    let diff = query(
        &[
            "diff",
            "--no-ext-diff",
            "--no-textconv",
            "--no-renames",
            "--no-color",
            &request.diff_base,
            &request.candidate,
            "--",
        ],
        MAX_CONTEXT_BYTES - total,
    )?;
    if std::str::from_utf8(&diff).is_err() {
        return Err(refused("context diff is not text"));
    }
    let result = SourceContext { files, diff };
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o644)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open("/run/factory-context-output/result.json")?;
    output.write_all(&serde_json::to_vec(&result).map_err(io::Error::other)?)?;
    output.sync_all()?;
    File::open("/run/factory-context-output")?.sync_all()
}
