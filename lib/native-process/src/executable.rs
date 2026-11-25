use super::{Error, Result};
use sha2::{Digest, Sha256};
use std::fs::{File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::fd::AsRawFd;
#[cfg(target_os = "linux")]
use std::os::fd::FromRawFd;
use std::os::unix::fs::{FileExt, MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};

const MAX_EXECUTABLE_BYTES: u64 = 512 * 1024 * 1024;

#[cfg(target_os = "linux")]
fn writable(file: &mut File) -> &mut File {
    file
}
#[cfg(target_os = "macos")]
fn writable(file: &mut (File, PathBuf)) -> &mut File {
    &mut file.0
}

/// Captured native executable bytes, sealed before any launch. This is native authority,
/// not a serializable command supplied by Go or the webview. Script/bundle and
/// provider freshness admission belong to the harness consumer.
pub struct Executable {
    pub(super) file: File,
    source: PathBuf,
    sha256: String,
    #[cfg(target_os = "macos")]
    pinned: std::sync::Arc<Pinned>,
}
/// The host's own executable format: ELF on Linux; thin 64-bit or universal
/// Mach-O on macOS.
fn native(magic: &[u8]) -> bool {
    if cfg!(target_os = "macos") {
        matches!(magic, [0xcf, 0xfa, 0xed, 0xfe] | [0xca, 0xfe, 0xba, 0xbe])
    } else {
        magic == b"\x7fELF"
    }
}
// macOS has neither memfd nor fexecve. The captured copy is a fresh 0500 file
// in the caller's private per-user temporary directory, and launch executes
// that path only while it still names the hashed inode.
#[cfg(target_os = "macos")]
struct Pinned(PathBuf);
#[cfg(target_os = "macos")]
impl Drop for Pinned {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}
#[cfg(target_os = "macos")]
fn pinned_directory() -> Result<PathBuf> {
    use std::os::unix::fs::DirBuilderExt;
    let mut buffer = [0 as libc::c_char; 1024];
    let length =
        unsafe { libc::confstr(libc::_CS_DARWIN_USER_TEMP_DIR, buffer.as_mut_ptr(), buffer.len()) };
    if length == 0 || length > buffer.len() {
        return Err(Error::Executable);
    }
    let base = unsafe { std::ffi::CStr::from_ptr(buffer.as_ptr()) };
    let base = std::fs::canonicalize(<std::ffi::OsStr as std::os::unix::ffi::OsStrExt>::from_bytes(base.to_bytes()))
        .map_err(|_| Error::Executable)?;
    let directory = base.join("factory-native-executables");
    match std::fs::DirBuilder::new().mode(0o700).create(&directory) {
        Ok(()) => (),
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => (),
        Err(_) => return Err(Error::Executable),
    }
    let meta = std::fs::symlink_metadata(&directory).map_err(|_| Error::Executable)?;
    if !meta.is_dir() || meta.uid() != unsafe { libc::geteuid() } || meta.mode() & 0o077 != 0 {
        return Err(Error::Executable);
    }
    Ok(directory)
}
impl Executable {
    #[cfg(target_os = "macos")]
    fn empty() -> Result<(File, PathBuf)> {
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).map_err(|_| Error::Executable)?;
        let name: String = random.iter().map(|b| format!("{b:02x}")).collect();
        let path = pinned_directory()?.join(name);
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .create_new(true)
            .mode(0o700)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&path)
            .map_err(|_| Error::Executable)?;
        Ok((file, path))
    }
    #[cfg(target_os = "macos")]
    fn seal(mut file: (File, PathBuf), source: PathBuf, sha256: String) -> Result<Self> {
        let pinned = std::sync::Arc::new(Pinned(file.1));
        file.0.seek(SeekFrom::Start(0)).map_err(|_| Error::Executable)?;
        if unsafe { libc::fchmod(file.0.as_raw_fd(), 0o500) } != 0 || file.0.sync_all().is_err() {
            return Err(Error::Executable);
        }
        Ok(Self { file: file.0, source, sha256, pinned })
    }
    #[cfg(target_os = "linux")]
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
    #[cfg(target_os = "linux")]
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
            if size == 0 && (n < 4 || !native(&buffer[..4])) {
                return Err(Error::Executable);
            }
            size += n as u64;
            if size > MAX_EXECUTABLE_BYTES {
                return Err(Error::Executable);
            }
            hash.update(&buffer[..n]);
            writable(&mut file).write_all(&buffer[..n])
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
        if !native(&header) {
            return Err(Error::Executable);
        }
        writable(&mut file).write_all(&header).map_err(|_| Error::Executable)?;
        let mut hash = Sha256::new();
        hash.update(header);
        let mut remaining = length - header.len() as u64;
        let mut buffer = [0u8; 65536];
        while remaining != 0 {
            let want = buffer.len().min(remaining as usize);
            reader
                .read_exact(&mut buffer[..want])
                .map_err(|_| Error::Executable)?;
            writable(&mut file).write_all(&buffer[..want])
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
            #[cfg(target_os = "macos")]
            pinned: self.pinned.clone(),
        })
    }
    #[cfg(target_os = "linux")]
    pub(super) fn command(&self) -> Result<std::process::Command> {
        // The descriptor is close-on-exec; Linux opens an ELF before closing it.
        // No mutable source-path lookup occurs in the child.
        Ok(std::process::Command::new(format!("/proc/self/fd/{}", self.file.as_raw_fd())))
    }
    #[cfg(target_os = "macos")]
    pub(super) fn command(&self) -> Result<std::process::Command> {
        let held = self.file.metadata().map_err(|_| Error::Executable)?;
        let named = std::fs::symlink_metadata(&self.pinned.0).map_err(|_| Error::Executable)?;
        if !named.is_file()
            || named.dev() != held.dev()
            || named.ino() != held.ino()
            || named.mode() & 0o7777 != 0o500
            || named.uid() != unsafe { libc::geteuid() }
        {
            return Err(Error::Executable);
        }
        Ok(std::process::Command::new(&self.pinned.0))
    }
}
