//! Root-owned source variants. Preparation inputs never masquerade as an agent
//! executable/prompt binding; both variants share staging and exact retirement.
use super::*;
use soda_execution_protocol::{ApprovedFiles, SetupReceipt, MAX_SETUP_FRAME, MAX_SETUP_OUTPUT};

pub(super) enum Binding<'a> {
    Agent {
        tool: &'a str,
        prompt: &'a str,
        setup: Option<&'a ApprovedFiles>,
    },
    Preparation(&'a ApprovedFiles),
}
fn write(path: &Path, bytes: &[u8]) -> io::Result<()> {
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o444)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    file.write_all(bytes)?;
    file.sync_all()?;
    file.set_permissions(fs::Permissions::from_mode(0o444))
}
pub(super) fn approved(directory: &Path) -> io::Result<ApprovedFiles> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join("approved.json"))?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != 0
        || meta.mode() & 0o7777 != 0o444
        || meta.len() > MAX_SETUP_FRAME as u64
    {
        return Err(denied("approved setup metadata invalid"));
    }
    let mut bytes = Vec::new();
    file.take(MAX_SETUP_FRAME as u64 + 1)
        .read_to_end(&mut bytes)?;
    let input: ApprovedFiles =
        serde_json::from_slice(&bytes).map_err(|_| denied("approved setup shape invalid"))?;
    if !input.valid()
        || bytes.len() as u64 != meta.len()
        || read_digest(directory, "owner.setup_sha256")? != input.digest()
    {
        return Err(denied("approved setup identity invalid"));
    }
    Ok(input)
}
impl Binding<'_> {
    pub(super) fn verify(&self, directory: &Path) -> io::Result<()> {
        match self {
            Self::Agent {
                tool,
                prompt,
                setup,
            } => {
                match setup {
                    Some(expected) if approved(directory)? != **expected => {
                        return Err(denied("agent setup differs"))
                    }
                    None if directory.join("owner.setup_sha256").try_exists()? => {
                        return Err(denied("agent setup binding changed"))
                    }
                    _ => (),
                }
                verified_digest(directory, "owner.tool_sha256", tool)?;
                verified_digest(directory, "owner.prompt_sha256", prompt)
            }
            Self::Preparation(input) => {
                if directory.join("owner.tool_sha256").try_exists()?
                    || directory.join("owner.prompt_sha256").try_exists()?
                    || approved(directory)? != **input
                {
                    return Err(denied("source preparation input differs"));
                }
                Ok(())
            }
        }
    }
    pub(super) fn write(&self, directory: &Path) -> io::Result<()> {
        match self {
            Self::Agent {
                tool,
                prompt,
                setup,
            } => {
                write(
                    &directory.join("owner.tool_sha256"),
                    format!("{tool}\n").as_bytes(),
                )?;
                write(
                    &directory.join("owner.prompt_sha256"),
                    format!("{prompt}\n").as_bytes(),
                )?;
                if let Some(input) = setup {
                    Self::Preparation(input).write(directory)?;
                }
                Ok(())
            }
            Self::Preparation(input) => {
                if !input.valid() {
                    return Err(denied("approved setup bounds invalid"));
                }
                write(
                    &directory.join("owner.setup_sha256"),
                    format!("{}\n", input.digest()).as_bytes(),
                )?;
                write(
                    &directory.join("approved.json"),
                    &serde_json::to_vec(input)?,
                )?;
                let files = directory.join("approved");
                DirBuilder::new().mode(0o700).create(&files)?;
                for (name, bytes) in &input.files {
                    write(&files.join(name), bytes)?;
                }
                fs::set_permissions(&files, fs::Permissions::from_mode(0o555))?;
                File::open(files)?.sync_all()
            }
        }
    }
}
pub(super) fn verify(directory: &Path) -> io::Result<()> {
    let setup = directory.join("owner.setup_sha256").try_exists()?;
    if setup {
        approved(directory)?;
    }
    if !setup
        || directory.join("owner.tool_sha256").try_exists()?
        || directory.join("owner.prompt_sha256").try_exists()?
    {
        read_digest(directory, "owner.tool_sha256")?;
        read_digest(directory, "owner.prompt_sha256")?;
    }
    Ok(())
}
pub(super) fn agent_setup(
    source: &soda_execution_protocol::PreparedSourceRef,
    profile: &str,
    digest: &str,
    role: &str,
    desktop_uid: u32,
) -> io::Result<ApprovedFiles> {
    if !id(&source.run, 32) || !id(&source.candidate, 40) {
        return Err(denied("agent preparation selector invalid"));
    }
    let proof = receipt(&source.run, profile, digest, &source.candidate, desktop_uid)?;
    if proof.output_truncated
        || proof.setup_exit != Some(0)
        || proof.check_exit != Some(0)
        || !proof.missing.is_empty()
    {
        return Err(denied("agent preparation is not ready"));
    }
    let runs = state()?;
    let _lock = lock(&runs)?;
    let directory = runs.join(&source.run);
    private_dir(&directory, 0o555)?;
    verified_profile(&directory, profile)?;
    verified_role(&directory, role)?;
    if !verified_bundle(&directory, digest)? {
        return Err(denied("agent prepared source differs"));
    }
    approved(&directory)
}
pub(super) fn cleanup(directory: &Path) -> io::Result<()> {
    let files = directory.join("approved");
    match files.symlink_metadata() {
        Ok(meta) => {
            if !meta.is_dir() || meta.uid() != 0 || !matches!(meta.mode() & 0o7777, 0o555 | 0o700) {
                return Err(denied("retiring approved directory invalid"));
            }
            fs::set_permissions(&files, fs::Permissions::from_mode(0o700))?;
            for entry in fs::read_dir(&files)? {
                let entry = entry?;
                let name = entry.file_name();
                let meta = entry.path().symlink_metadata()?;
                if !name.to_str().is_some_and(ApprovedFiles::valid_name)
                    || !meta.is_file()
                    || meta.uid() != 0
                {
                    return Err(denied("retiring approved file invalid"));
                }
                fs::remove_file(entry.path())?;
            }
            fs::remove_dir(files)?;
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    for name in ["approved.json", "owner.setup_sha256"] {
        match fs::remove_file(directory.join(name)) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::NotFound => (),
            Err(error) => return Err(error),
        }
    }
    Ok(())
}
pub(super) fn receipt(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    desktop_uid: u32,
) -> io::Result<SetupReceipt> {
    let runs = state()?;
    let _lock = lock(&runs)?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("preparation bundle missing"));
    }
    let input = approved(&source)?;
    let directory =
        preparation_run(&preparations()?, run)?.ok_or_else(|| denied("preparation missing"))?;
    if prepared_output(&directory)? != Some(0o555)
        || read_manifest(&directory.join("manifest.env"))?.as_deref()
            != Some(manifest(candidate, digest).as_str())
    {
        return Err(denied("preparation not complete"));
    }
    let owner = directory.join("output/repo").symlink_metadata()?.uid();
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join("output/setup-receipt.json"))?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != owner
        || meta.mode() & 0o022 != 0
        || meta.len() > 2 * 1024 * 1024
    {
        return Err(denied("setup receipt metadata invalid"));
    }
    let mut bytes = Vec::new();
    file.take(2 * 1024 * 1024 + 1).read_to_end(&mut bytes)?;
    let receipt: SetupReceipt =
        serde_json::from_slice(&bytes).map_err(|_| denied("setup receipt invalid"))?;
    if receipt.worker_uid != owner
        || receipt.worker_uid == 0
        || receipt.worker_uid == desktop_uid
        || bytes.len() as u64 != meta.len()
        || receipt.setup_sha256 != input.digest()
        || receipt.source_commit != candidate
        || receipt.output.len() > MAX_SETUP_OUTPUT
        || receipt
            .setup_exit
            .into_iter()
            .chain(receipt.check_exit)
            .any(|code| !(0..=255).contains(&code))
        || receipt
            .tools
            .keys()
            .chain(receipt.missing.iter())
            .any(|name| !input.tools.contains(name))
        || input
            .tools
            .iter()
            .any(|name| !receipt.tools.contains_key(name) && !receipt.missing.contains(name))
    {
        return Err(denied("setup receipt scope differs"));
    }
    Ok(receipt)
}

pub(super) fn clear_context(directory: &Path) -> io::Result<()> {
    let output = directory.join("context-output");
    match output.symlink_metadata() {
        Ok(_) => {
            private_dir(&output, 0o777)?;
            match fs::remove_file(output.join("result.json")) {
                Ok(()) => (),
                Err(error) if error.kind() == io::ErrorKind::NotFound => (),
                Err(error) => return Err(error),
            }
            fs::remove_dir(output)?;
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => (),
        Err(error) => return Err(error),
    }
    for name in ["context-request.json", "context-candidate.bundle"] {
        match fs::remove_file(directory.join(name)) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::NotFound => (),
            Err(error) => return Err(error),
        }
    }
    Ok(())
}
pub(super) fn read(
    run: &str,
    profile: &str,
    digest: &str,
    candidate: &str,
    operation: &soda_execution_protocol::PreparedSourceOperation,
    delta: &[u8],
    desktop_uid: u32,
) -> io::Result<soda_execution_protocol::PreparedSourceResult> {
    let proof = receipt(run, profile, digest, candidate, desktop_uid)?;
    if proof.output_truncated
        || proof.setup_exit != Some(0)
        || proof.check_exit != Some(0)
        || !proof.missing.is_empty()
    {
        return Err(denied("context preparation is not ready"));
    }
    let runs = state()?;
    let _lock = lock(&runs)?;
    preparation_caller_current()?;
    let source = runs.join(run);
    private_dir(&source, 0o555)?;
    verified_profile(&source, profile)?;
    if !verified_bundle(&source, digest)? {
        return Err(denied("context source missing"));
    }
    let directory = preparation_run(&preparations()?, run)?
        .ok_or_else(|| denied("context preparation missing"))?;
    if prepared_output(&directory)? != Some(0o555)
        || read_manifest(&directory.join("manifest.env"))?.as_deref()
            != Some(manifest(candidate, digest).as_str())
    {
        return Err(denied("context preparation changed"));
    }
    let request = directory.join("context-request.json");
    let output = directory.join("context-output");
    manager("stop", "context", run)?;
    clear_context(&directory)?;
    write(&request, &serde_json::to_vec(operation)?)?;
    write(&directory.join("context-candidate.bundle"), delta)?;
    DirBuilder::new().mode(0o777).create(&output)?;
    fs::set_permissions(&output, fs::Permissions::from_mode(0o777))?;
    let result = (|| {
        start_preparation(&format!("soda-factory-context@{run}.service"), 90)?;
        let file = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(output.join("result.json"))?;
        let meta = file.metadata()?;
        if !meta.is_file()
            || meta.uid() == 0
            || meta.uid() == desktop_uid
            || meta.mode() & 0o022 != 0
            || meta.len() > 3 * 1024 * 1024
        {
            return Err(denied("context result metadata invalid"));
        }
        let mut bytes = Vec::new();
        file.take(3 * 1024 * 1024 + 1).read_to_end(&mut bytes)?;
        if bytes.len() as u64 != meta.len() {
            return Err(denied("context result changed"));
        }
        serde_json::from_slice(&bytes).map_err(|_| denied("context result invalid"))
    })();
    manager("stop", "context", run)?;
    clear_context(&directory)?;
    result
}
