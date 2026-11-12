//! Read Git objects only in the isolated, read-only context service.
use super::{git, private_process, refused};
use soda_execution_protocol::{
    ContextEntry, ContextFile, ContextPage, ContextReadPage, ContextSelection,
    PreparedSourceOperation, PreparedSourceResult, SourceContext, MAX_CONTEXT_BYTES,
    MAX_CONTEXT_ENTRIES, MAX_CONTEXT_FILE_BYTES, MAX_CONTEXT_REQUEST, MAX_PREPARED_TREE_BYTES,
};
use std::collections::BTreeSet;
use std::fs::{File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::process::{Command, Stdio};

pub(super) fn query_at(repository: &str, args: &[&str], limit: usize) -> io::Result<Vec<u8>> {
    let mut child = command_at(repository)
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

fn command_at(repository: &str) -> Command {
    git(&[
        "-c",
        "core.fsmonitor=false",
        "-c",
        "core.hooksPath=/dev/null",
        "-c",
        "diff.external=",
        "-C",
        repository,
    ])
}
fn blob(repository: &str, commit: &str, path: &str) -> io::Result<Option<String>> {
    let entry = query_at(
        repository,
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
    Ok(Some(fields[2].to_owned()))
}

fn read_page(
    repository: &str,
    object: &str,
    request: &ContextReadPage,
) -> io::Result<(Vec<u8>, ContextPage)> {
    let size = query_at(repository, &["cat-file", "-s", object], 32)?;
    let total: u64 = std::str::from_utf8(&size)
        .ok()
        .and_then(|text| text.trim().parse().ok())
        .filter(|size| *size <= MAX_PREPARED_TREE_BYTES && request.offset <= *size)
        .ok_or_else(|| refused("source page offset or file size exceeds bounds"))?;
    let mut child = command_at(repository)
        .args(["cat-file", "blob", object])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .spawn()?;
    let mut output = child
        .stdout
        .take()
        .ok_or_else(|| refused("source page pipe unavailable"))?;
    let count = (total - request.offset).min(request.limit as u64);
    let result = (|| {
        if io::copy(&mut output.by_ref().take(request.offset), &mut io::sink())? != request.offset {
            return Err(refused("source page offset unavailable"));
        }
        let mut bytes = Vec::new();
        output.by_ref().take(count).read_to_end(&mut bytes)?;
        if bytes.len() as u64 != count {
            return Err(refused("source page incomplete"));
        }
        if let Err(error) = std::str::from_utf8(&bytes) {
            if error.error_len().is_some() || request.offset + count == total {
                return Err(refused("source page is not UTF-8 text"));
            }
            bytes.truncate(error.valid_up_to());
        }
        let next = request.offset + bytes.len() as u64;
        Ok((
            bytes,
            ContextPage {
                path: request.path.clone(),
                offset: request.offset,
                total_bytes: total,
                next_offset: (next < total).then_some(next),
            },
        ))
    })();
    // Drain without retaining the rest of the blob, then join the existing
    // timeout/Git process tree. A completed page leaves no live reader behind.
    let drained = io::copy(&mut output, &mut io::sink());
    if drained.is_err() {
        let _ = child.kill();
        let _ = child.wait();
        return Err(refused("source page reader failed"));
    }
    if !child.wait()?.success() || result.is_ok() && drained? != total - request.offset - count {
        return Err(refused("source page reader incomplete"));
    }
    result
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
    let request: PreparedSourceOperation =
        serde_json::from_slice(&bytes).map_err(|_| refused("context request invalid"))?;
    let result = match request {
        PreparedSourceOperation::Context { selection } => {
            if !selection.valid() {
                return Err(refused("context selection invalid"));
            }
            let repository = super::prepared_candidate::source(&selection.source_commit)?;
            PreparedSourceResult::Context {
                context: read(repository, selection)?,
            }
        }
        PreparedSourceOperation::CandidateContext {
            selection,
            delta_length,
            delta_sha256,
        } => {
            if !selection.valid() {
                return Err(refused("context selection invalid"));
            }
            let repository = super::prepared_candidate::import(
                &selection.source_commit,
                &selection.candidate,
                delta_length,
                &delta_sha256,
            )?;
            PreparedSourceResult::Context {
                context: read(repository, selection)?,
            }
        }
        PreparedSourceOperation::Candidate {
            base,
            candidate,
            delta_length,
            delta_sha256,
        } => super::prepared_candidate::run(&base, &candidate, delta_length, &delta_sha256)?,
    };
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
fn read(repository: &str, request: ContextSelection) -> io::Result<SourceContext> {
    if !request.valid() {
        return Err(refused("context selection invalid"));
    }
    for commit in [
        &request.source_commit,
        &request.approved_base,
        &request.diff_base,
        &request.candidate,
    ] {
        if query_at(
            repository,
            &["rev-parse", "--verify", &format!("{commit}^{{commit}}")],
            41,
        )? != format!("{commit}\n").as_bytes()
        {
            return Err(refused("context commit differs"));
        }
    }
    if !query_at(
        repository,
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
    let directory_instructions = request
        .directory
        .as_ref()
        .filter(|path| !path.is_empty())
        .map(|directory| format!("{directory}/AGENTS.md"));
    for path in request.paths.iter().chain(directory_instructions.iter()) {
        paths.insert(path.clone());
        let mut parent = std::path::Path::new(path).parent();
        while let Some(directory) = parent.filter(|p| !p.as_os_str().is_empty()) {
            paths.insert(format!("{}/AGENTS.md", directory.display()));
            parent = directory.parent();
        }
    }
    let mut files = Vec::new();
    let mut total = 0;
    let mut page = None;
    for path in paths {
        if let Some(object) = blob(repository, &request.candidate, &path)? {
            let content =
                if let Some(selection) = request.page.as_ref().filter(|page| page.path == path) {
                    let (content, metadata) = read_page(repository, &object, selection)?;
                    page = Some(metadata);
                    content
                } else {
                    let content = query_at(
                        repository,
                        &["cat-file", "blob", &object],
                        MAX_CONTEXT_FILE_BYTES,
                    )?;
                    if std::str::from_utf8(&content).is_err() {
                        return Err(refused("context file is not text"));
                    }
                    content
                };
            total += content.len();
            if files.len() >= 12 || total > MAX_CONTEXT_BYTES {
                return Err(refused("context instruction closure exceeds bounds"));
            }
            files.push(ContextFile { path, content });
        }
    }
    let entries = if let Some(directory) = &request.directory {
        directory_entries(
            repository,
            &request.candidate,
            directory,
            MAX_CONTEXT_BYTES - total,
        )?
    } else {
        Vec::new()
    };
    total += entries
        .iter()
        .map(|entry| entry.path.len() + entry.kind.len())
        .sum::<usize>();
    let diff = query_at(
        repository,
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
    Ok(SourceContext {
        files,
        diff,
        entries,
        page,
    })
}

fn directory_entries(
    repository: &str,
    commit: &str,
    directory: &str,
    limit: usize,
) -> io::Result<Vec<ContextEntry>> {
    let tree = if directory.is_empty() {
        commit.to_owned()
    } else {
        format!("{commit}:{directory}")
    };
    let bytes = query_at(repository, &["ls-tree", "-z", &tree], limit)?;
    let mut entries = Vec::new();
    for entry in bytes.split(|b| *b == 0).filter(|entry| !entry.is_empty()) {
        let tab = entry
            .iter()
            .position(|b| *b == b'\t')
            .ok_or_else(|| refused("source tree entry invalid"))?;
        let header = std::str::from_utf8(&entry[..tab])
            .map_err(|_| refused("source tree header invalid"))?;
        let name = std::str::from_utf8(&entry[tab + 1..])
            .map_err(|_| refused("source tree path is not text"))?;
        let path = if directory.is_empty() {
            name.to_owned()
        } else {
            format!("{directory}/{name}")
        };
        if name.contains('/')
            || !ContextSelection::valid_path(&path)
            || entries.len() >= MAX_CONTEXT_ENTRIES
        {
            return Err(refused("source directory exceeds path or entry bounds"));
        }
        let fields: Vec<_> = header.split(' ').collect();
        let kind = match fields.as_slice() {
            ["100644" | "100755", "blob", _] => "file",
            ["040000", "tree", _] => "directory",
            ["120000", "blob", _] => "symlink",
            ["160000", "commit", _] => "submodule",
            _ => return Err(refused("source tree entry kind invalid")),
        };
        entries.push(ContextEntry {
            path,
            kind: kind.to_owned(),
        });
    }
    Ok(entries)
}
