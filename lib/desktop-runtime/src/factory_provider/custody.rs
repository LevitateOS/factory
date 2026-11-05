//! Native acquisition identity and irreversible local retirement evidence.
//! This journal never decides product admission, outcome or SQL settlement.
use super::{receipt, AgentCancellation, ReceiptScope};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CustodyState {
    Held,
    Uncertain,
    Released,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CustodyObservation {
    pub run: String,
    pub profile: String,
    pub acquisition_id: String,
    pub connection: String,
    /// One acquisition per immutable run identity. This is not an OS credential
    /// revision, process incarnation, placement or Go capability generation.
    pub generation: u64,
    pub sealed: bool,
    pub state: CustodyState,
}

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Header {
    kind: String,
    version: u8,
    run: String,
    profile: String,
    scope_sha256: String,
    acquisition_id: String,
    connection: String,
    generation: u64,
}

struct Shared {
    directory: PathBuf,
    header: Header,
    active: Mutex<bool>,
    cancellation: AgentCancellation,
}

/// Retain independently of a request or the execution handle. Only the live
/// native owner can report Held; a reopened record without release is uncertain.
#[derive(Clone)]
pub struct RunCustody(Arc<Shared>);

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn private_directory(path: &Path) -> io::Result<()> {
    let meta = path.symlink_metadata()?;
    if !meta.is_dir()
        || meta.uid() != unsafe { libc::geteuid() }
        || meta.mode() & 0o077 != 0
        || fs::canonicalize(path)? != path
    {
        return Err(refused("native custody directory invalid"));
    }
    Ok(())
}

fn read_file(path: &Path) -> io::Result<Vec<u8>> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)?;
    let meta = file.metadata()?;
    if !meta.is_file()
        || meta.uid() != unsafe { libc::geteuid() }
        || meta.mode() & 0o077 != 0
        || meta.nlink() != 1
        || meta.len() > 1024
    {
        return Err(refused("native custody file invalid"));
    }
    let mut bytes = Vec::new();
    (&file).take(1025).read_to_end(&mut bytes)?;
    if bytes.len() as u64 != meta.len() {
        return Err(refused("native custody file changed"));
    }
    file.sync_all()?;
    Ok(bytes)
}

fn write_file(directory: &Path, name: &str, bytes: &[u8]) -> io::Result<()> {
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(directory.join(name))?;
    file.write_all(bytes)?;
    file.sync_all()?;
    File::open(directory)?.sync_all()
}

fn marker(header: &Header) -> io::Result<Vec<u8>> {
    let digest = Sha256::digest(serde_json::to_vec(header)?);
    let mut value: String = digest.iter().map(|byte| format!("{byte:02x}")).collect();
    value.push('\n');
    Ok(value.into_bytes())
}

fn marked(directory: &Path, name: &str, expected: &[u8]) -> io::Result<bool> {
    match read_file(&directory.join(name)) {
        Ok(bytes) if bytes == expected => Ok(true),
        Ok(_) => Err(refused("native custody retirement marker differs")),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(false),
        Err(error) => Err(error),
    }
}

fn snapshot(directory: &Path, expected: &Header, active: bool) -> io::Result<CustodyObservation> {
    private_directory(directory)?;
    let header: Header = serde_json::from_slice(&read_file(&directory.join("acquisition.json"))?)?;
    if &header != expected {
        return Err(refused("native custody acquisition differs"));
    }
    let marker = marker(&header)?;
    let sealed = marked(directory, "sealed", &marker)?;
    let released = marked(directory, "released", &marker)?;
    if released && !sealed {
        return Err(refused("native custody release is not sealed"));
    }
    // An independent reader may see a complete marker before its writer's
    // directory sync returns. Confirm durability before publishing evidence.
    File::open(directory)?.sync_all()?;
    Ok(CustodyObservation {
        run: header.run,
        profile: header.profile,
        acquisition_id: header.acquisition_id,
        connection: header.connection,
        generation: header.generation,
        sealed,
        state: if released {
            CustodyState::Released
        } else if active && !sealed {
            CustodyState::Held
        } else {
            CustodyState::Uncertain
        },
    })
}

impl RunCustody {
    pub(super) fn prepare(
        profile_dir: &Path,
        scope: &ReceiptScope<'_>,
        connection: &str,
        cancellation: AgentCancellation,
    ) -> io::Result<Self> {
        receipt::profile(profile_dir, scope.profile)?;
        if !hex(connection, 32) {
            return Err(refused("native custody connection invalid"));
        }
        // Older retained attempts predate this journal. Their receipts and
        // interrupted writes still fence reuse without being rewritten.
        let receipts = receipt::ensure_receipts(profile_dir)?;
        for suffix in ["receipt", "pending"] {
            match receipts
                .join(format!("{}.{suffix}", scope.run))
                .symlink_metadata()
            {
                Err(error) if error.kind() == io::ErrorKind::NotFound => (),
                Err(error) => return Err(error),
                Ok(_) => return Err(refused("native custody run already retained")),
            }
        }
        let root = profile_dir.join("native-run-custody");
        match DirBuilder::new().mode(0o700).create(&root) {
            Ok(()) => (),
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
        private_directory(&root)?;
        File::open(profile_dir)?.sync_all()?;
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).map_err(|_| refused("native custody identity unavailable"))?;
        let header = Header {
            kind: "factory_native_custody".into(),
            version: 1,
            run: scope.run.into(),
            profile: scope.profile.into(),
            scope_sha256: scope.fingerprint(),
            acquisition_id: random.iter().map(|byte| format!("{byte:02x}")).collect(),
            connection: connection.into(),
            generation: 1,
        };
        let directory = root.join(scope.run);
        // The directory is the one-shot fence, including interrupted creation.
        // Never overwrite or silently acquire again for a retained run.
        DirBuilder::new().mode(0o700).create(&directory)?;
        File::open(&root)?.sync_all()?;
        write_file(
            &directory,
            "acquisition.json",
            &serde_json::to_vec(&header)?,
        )?;
        Ok(Self(Arc::new(Shared {
            directory,
            header,
            active: Mutex::new(false),
            cancellation,
        })))
    }

    pub(super) fn activate(&self) -> io::Result<()> {
        let mut active = self
            .0
            .active
            .lock()
            .map_err(|_| refused("native custody unavailable"))?;
        let observed = snapshot(&self.0.directory, &self.0.header, false)?;
        if *active || observed.sealed || self.0.cancellation.cancelled() {
            return Err(refused("native custody activation unavailable"));
        }
        *active = true;
        Ok(())
    }

    pub fn observe(&self) -> io::Result<CustodyObservation> {
        let active = self
            .0
            .active
            .lock()
            .map_err(|_| refused("native custody unavailable"))?;
        snapshot(
            &self.0.directory,
            &self.0.header,
            *active && !self.0.cancellation.cancelled(),
        )
    }

    pub(super) fn seal(&self) -> io::Result<()> {
        self.commit("sealed")
    }

    pub(super) fn release(&self) -> io::Result<()> {
        if !self.observe()?.sealed {
            return Err(refused("native custody release requires sealing"));
        }
        self.commit("released")
    }

    fn commit(&self, name: &str) -> io::Result<()> {
        let _state = self
            .0
            .active
            .lock()
            .map_err(|_| refused("native custody unavailable"))?;
        snapshot(&self.0.directory, &self.0.header, false)?;
        let marker = marker(&self.0.header)?;
        if !marked(&self.0.directory, name, &marker)? {
            write_file(&self.0.directory, name, &marker)?;
        }
        // Retrying an already committed marker still confirms its durability.
        File::open(self.0.directory.join(name))?.sync_all()?;
        File::open(&self.0.directory)?.sync_all()
    }
}

/// Read exact retained native evidence without activating an acquisition or
/// asserting that an old process is dead. Missing/partial records never prove
/// NotAcquired or Released. Recovery retirement remains a separate operation.
pub fn read_custody(
    profile_dir: &Path,
    scope: &ReceiptScope<'_>,
    connection: &str,
) -> io::Result<Option<CustodyObservation>> {
    receipt::profile(profile_dir, scope.profile)?;
    let root = profile_dir.join("native-run-custody");
    match private_directory(&root) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        other => other?,
    }
    let directory = root.join(scope.run);
    match private_directory(&directory) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        other => other?,
    }
    let header: Header = serde_json::from_slice(&read_file(&directory.join("acquisition.json"))?)?;
    if header.kind != "factory_native_custody"
        || header.version != 1
        || header.run != scope.run
        || header.profile != scope.profile
        || header.scope_sha256 != scope.fingerprint()
        || header.connection != connection
        || !hex(connection, 32)
        || !hex(&header.acquisition_id, 32)
        || header.generation != 1
    {
        return Err(refused("native custody retained scope differs"));
    }
    snapshot(&directory, &header, false).map(Some)
}
