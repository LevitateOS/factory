use super::{Error, Result};
use sha2::{Digest, Sha256};
use std::fs::{File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};

/// Captured native ELF bytes, sealed before any launch. This is native authority,
/// not a serializable command supplied by Go or the webview. Script/bundle and
/// provider freshness admission belong to the harness consumer.
pub struct Executable {
    pub(super) file: File,
    source: PathBuf,
    sha256: String,
}
impl Executable {
    pub fn capture(path: &Path) -> Result<Self> {
        if !path.is_absolute() {
            return Err(Error::Invalid);
        }
        let source = std::fs::canonicalize(path).map_err(|_| Error::Executable)?;
        let mut original = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&source)
            .map_err(|_| Error::Executable)?;
        let meta = original.metadata().map_err(|_| Error::Executable)?;
        if !meta.is_file()
            || meta.mode() & 0o111 == 0
            || meta.mode() & 0o022 != 0
            || meta.len() < 4
            || meta.len() > 128 * 1024 * 1024
        {
            return Err(Error::Executable);
        }
        let fd = unsafe {
            libc::memfd_create(
                c"factory-executable".as_ptr(),
                libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING,
            )
        };
        if fd < 0 {
            return Err(Error::Executable);
        }
        // SAFETY: memfd_create returned a new exclusively owned descriptor.
        let mut file = unsafe { File::from_raw_fd(fd) };
        let mut hash = Sha256::new();
        let mut buffer = [0; 65536];
        let mut size = 0u64;
        loop {
            let n = original.read(&mut buffer).map_err(|_| Error::Executable)?;
            if n == 0 {
                break;
            }
            if size == 0 && (n < 4 || &buffer[..4] != b"\x7fELF") {
                return Err(Error::Executable);
            }
            size += n as u64;
            if size > 128 * 1024 * 1024 {
                return Err(Error::Executable);
            }
            hash.update(&buffer[..n]);
            file.write_all(&buffer[..n])
                .map_err(|_| Error::Executable)?;
        }
        let after = original.metadata().map_err(|_| Error::Executable)?;
        if size != meta.len()
            || meta.len() != after.len()
            || meta.mtime() != after.mtime()
            || meta.mtime_nsec() != after.mtime_nsec()
            || meta.ctime() != after.ctime()
            || meta.ctime_nsec() != after.ctime_nsec()
        {
            return Err(Error::Executable);
        }
        file.seek(SeekFrom::Start(0))
            .map_err(|_| Error::Executable)?;
        let sealed = unsafe {
            libc::fchmod(fd, 0o500) == 0
                && libc::fcntl(
                    fd,
                    libc::F_ADD_SEALS,
                    libc::F_SEAL_WRITE
                        | libc::F_SEAL_GROW
                        | libc::F_SEAL_SHRINK
                        | libc::F_SEAL_SEAL,
                ) == 0
        };
        if !sealed {
            return Err(Error::Executable);
        }
        Ok(Self {
            file,
            source,
            sha256: hash.finalize().iter().map(|b| format!("{b:02x}")).collect(),
        })
    }
    pub fn source(&self) -> &Path {
        &self.source
    }
    pub fn sha256(&self) -> &str {
        &self.sha256
    }
    pub(super) fn duplicate(&self) -> Result<Self> {
        Ok(Self {
            file: self.file.try_clone().map_err(|_| Error::Executable)?,
            source: self.source.clone(),
            sha256: self.sha256.clone(),
        })
    }
    pub(super) fn command(&self) -> std::process::Command {
        // The descriptor is close-on-exec; Linux opens an ELF before closing it.
        // No mutable source-path lookup occurs in the child.
        std::process::Command::new(format!("/proc/self/fd/{}", self.file.as_raw_fd()))
    }
}
