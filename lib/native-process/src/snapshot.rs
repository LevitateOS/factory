//! Bounded transfer from a frozen, credential-free native snapshot.
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{symlink, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::Path;
fn refused(message: &str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}
fn readonly(path: &Path) -> io::Result<bool> {
    let path = std::ffi::CString::new(path.as_os_str().as_encoded_bytes())
        .map_err(|_| refused("snapshot path invalid"))?;
    let mut stat = std::mem::MaybeUninit::<libc::statvfs>::uninit();
    if unsafe { libc::statvfs(path.as_ptr(), stat.as_mut_ptr()) } != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(unsafe { stat.assume_init() }.f_flag & libc::ST_RDONLY as u64 != 0)
}

// Repository names such as `system/` must not select systemd's filename-based
// SELinux transitions inside a worker snapshot. Inherit the already-owned
// destination's label for this synchronous copy, then restore the thread state.
struct CreationContext(Option<(File, Vec<u8>)>);
impl CreationContext {
    fn inherit(target: &Path) -> io::Result<Self> {
        let directory = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(target)?;
        let metadata = directory.metadata()?;
        if metadata.uid() != unsafe { libc::geteuid() } || metadata.mode() & 0o7777 != 0o700 {
            return Err(refused("snapshot destination is not private"));
        }
        let mut label = vec![0; 4096];
        let size = unsafe {
            libc::fgetxattr(
                directory.as_raw_fd(),
                c"security.selinux".as_ptr(),
                label.as_mut_ptr().cast(),
                label.len(),
            )
        };
        if size < 0 {
            let error = io::Error::last_os_error();
            return if matches!(error.raw_os_error(), Some(libc::ENODATA | libc::ENOTSUP)) {
                Ok(Self(None))
            } else {
                Err(error)
            };
        }
        if size == 0 {
            return Err(refused("snapshot destination label missing"));
        }
        label.truncate(size as usize);
        let mut attribute = OpenOptions::new()
            .read(true)
            .write(true)
            .open("/proc/thread-self/attr/fscreate")?;
        let mut previous = Vec::new();
        (&mut attribute).take(4097).read_to_end(&mut previous)?;
        if previous.len() > 4096 {
            return Err(refused("snapshot creation label invalid"));
        }
        let context = Self(Some((attribute, previous)));
        Self::write(&context.0.as_ref().unwrap().0, &label)?;
        Ok(context)
    }
    fn write(attribute: &File, label: &[u8]) -> io::Result<()> {
        // A zero-length write resets the default; write_all([]) would skip it.
        if unsafe { libc::write(attribute.as_raw_fd(), label.as_ptr().cast(), label.len()) }
            != label.len() as isize
        {
            return Err(io::Error::last_os_error());
        }
        Ok(())
    }
    fn restore(&mut self) -> io::Result<()> {
        if let Some((attribute, previous)) = &self.0 {
            Self::write(attribute, previous)?;
        }
        self.0 = None;
        Ok(())
    }
}
impl Drop for CreationContext {
    fn drop(&mut self) {
        if self.restore().is_err() {
            // An isolated worker must not continue with unknown creation state.
            std::process::abort();
        }
    }
}
pub fn copy_snapshot(
    source: &Path,
    target: &Path,
    max_bytes: u64,
    max_entries: usize,
) -> io::Result<()> {
    let root = source.symlink_metadata()?;
    if !root.is_dir() || root.uid() == 0 || !readonly(source)? {
        return Err(refused("prepared snapshot is not read-only"));
    }
    let canonical = source.canonicalize()?;
    let mut context = CreationContext::inherit(target)?;
    let result = (|| {
        let mut pending = vec![(source.to_path_buf(), target.to_path_buf(), 0usize)];
        let mut total = 0u64;
        let mut entries = 0;
        while let Some((from, to, depth)) = pending.pop() {
            if entries > max_entries || depth > 64 {
                return Err(refused("prepared environment inventory exceeds bounds"));
            }
            for entry in fs::read_dir(from)? {
                entries += 1;
                if entries > max_entries {
                    return Err(refused("prepared environment inventory exceeds bounds"));
                }
                let entry = entry?;
                let from = entry.path();
                let to = to.join(entry.file_name());
                let meta = from.symlink_metadata()?;
                if meta.is_dir() {
                    fs::create_dir(&to)?;
                    fs::set_permissions(&to, fs::Permissions::from_mode(0o700))?;
                    pending.push((from, to, depth + 1));
                } else if meta.is_file() {
                    total = total
                        .checked_add(meta.len())
                        .ok_or_else(|| refused("prepared environment size invalid"))?;
                    if total > max_bytes {
                        return Err(refused("prepared environment byte bound exceeded"));
                    }
                    let file = OpenOptions::new()
                        .read(true)
                        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                        .open(&from)?;
                    let mut output = OpenOptions::new()
                        .write(true)
                        .create_new(true)
                        .mode(0o600 | (meta.mode() & 0o111))
                        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
                        .open(&to)?;
                    if io::copy(&mut file.take(meta.len() + 1), &mut output)? != meta.len() {
                        return Err(refused("prepared environment file changed"));
                    }
                    output.flush()?;
                } else if meta.file_type().is_symlink() {
                    let link = fs::read_link(&from)?;
                    if link.is_absolute() || !from.canonicalize()?.starts_with(&canonical) {
                        return Err(refused("prepared environment link escapes snapshot"));
                    }
                    symlink(link, to)?;
                } else {
                    return Err(refused("prepared environment has special file"));
                }
            }
        }
        Ok(())
    })();
    let restored = context.restore();
    result.and(restored)
}
