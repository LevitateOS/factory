use super::{Error, Result};
use sha2::{Digest, Sha256};
use std::fs::{File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::fs::{FileExt, MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};

const MAX_EXECUTABLE_BYTES: u64 = 512 * 1024 * 1024;

/// Captured native ELF bytes, sealed before any launch. This is native authority,
/// not a serializable command supplied by Go or the webview. Script/bundle and
/// provider freshness admission belong to the harness consumer.
pub struct Executable {
    pub(super) file: File,
    source: PathBuf,
    sha256: String,
}
impl Executable {
    fn empty() -> Result<File> {
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
        Ok(unsafe { File::from_raw_fd(fd) })
    }
    fn seal(mut file: File, source: PathBuf, sha256: String) -> Result<Self> {
        file.seek(SeekFrom::Start(0))
            .map_err(|_| Error::Executable)?;
        let fd = file.as_raw_fd();
        let required =
            libc::F_SEAL_WRITE | libc::F_SEAL_GROW | libc::F_SEAL_SHRINK | libc::F_SEAL_SEAL;
        let sealed = unsafe {
            libc::fchmod(fd, 0o500) == 0 && libc::fcntl(fd, libc::F_ADD_SEALS, required) == 0
        };
        let seals = unsafe { libc::fcntl(fd, libc::F_GET_SEALS) };
        if !sealed || seals < 0 || seals & required != required {
            return Err(Error::Executable);
        }
        Ok(Self {
            file,
            source,
            sha256,
        })
    }
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
            || meta.len() > MAX_EXECUTABLE_BYTES
        {
            return Err(Error::Executable);
        }
        let mut file = Self::empty()?;
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
            if size > MAX_EXECUTABLE_BYTES {
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
        Self::seal(
            file,
            source,
            hash.finalize().iter().map(|b| format!("{b:02x}")).collect(),
        )
    }
    /// Import bounded executable bytes through a stream into a new worker-owned
    /// sealed memfd. The source path is provenance only, never used for worker
    /// filesystem access or execution. The caller must bound the stream's time
    /// and ensure the declared bytes are the entire executable payload.
    pub fn from_stream<R: Read>(
        reader: &mut R,
        length: u64,
        source: &Path,
        expected_sha256: &str,
    ) -> Result<Self> {
        if !source.is_absolute()
            || source.as_os_str().len() > 4096
            || !(4..=MAX_EXECUTABLE_BYTES).contains(&length)
            || expected_sha256.len() != 64
            || !expected_sha256
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        {
            return Err(Error::Invalid);
        }
        let mut file = Self::empty()?;
        let mut header = [0u8; 4];
        reader
            .read_exact(&mut header)
            .map_err(|_| Error::Executable)?;
        if &header != b"\x7fELF" {
            return Err(Error::Executable);
        }
        file.write_all(&header).map_err(|_| Error::Executable)?;
        let mut hash = Sha256::new();
        hash.update(header);
        let mut remaining = length - header.len() as u64;
        let mut buffer = [0u8; 65536];
        while remaining != 0 {
            let want = buffer.len().min(remaining as usize);
            reader
                .read_exact(&mut buffer[..want])
                .map_err(|_| Error::Executable)?;
            file.write_all(&buffer[..want])
                .map_err(|_| Error::Executable)?;
            hash.update(&buffer[..want]);
            remaining -= want as u64;
        }
        let digest: String = hash.finalize().iter().map(|b| format!("{b:02x}")).collect();
        if digest != expected_sha256 {
            return Err(Error::Executable);
        }
        Self::seal(file, source.into(), digest)
    }
    pub fn source(&self) -> &Path {
        &self.source
    }
    pub fn len(&self) -> Result<u64> {
        let length = self.file.metadata().map_err(|_| Error::Executable)?.len();
        if !(4..=MAX_EXECUTABLE_BYTES).contains(&length) {
            return Err(Error::Executable);
        }
        Ok(length)
    }
    /// Send the exact sealed capture without consulting its mutable source
    /// path. The receiver must rehash and seal its own imported copy.
    pub fn copy_to<W: Write>(&self, writer: &mut W) -> Result<()> {
        let length = self.len()?;
        let mut buffer = [0u8; 65536];
        let mut offset = 0u64;
        while offset < length {
            let want = buffer.len().min((length - offset) as usize);
            let n = self
                .file
                .read_at(&mut buffer[..want], offset)
                .map_err(|_| Error::Executable)?;
            if n == 0 {
                return Err(Error::Executable);
            }
            writer
                .write_all(&buffer[..n])
                .map_err(|_| Error::Executable)?;
            offset += n as u64;
        }
        Ok(())
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
