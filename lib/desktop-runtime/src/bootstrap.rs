//! Private inputs for the Go network server. No provider credentials, socket
//! listener, account admission or workflow ownership are created here.
use crate::database::{private_file, NativePostgres};
use std::error::Error;
use std::fs::{self, File};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};

type Result<T> = std::result::Result<T, Box<dyn Error>>;

impl NativePostgres {
    /// Prepare one backend start while owning the native database/profile.
    /// Call before starting the backend, or after stopping the previous child:
    /// the supervisor owns that ordering. Profile identity is retained; each
    /// start gets a fresh IPC capability. No browser sees these private inputs.
    pub fn backend_config(&mut self) -> Result<PathBuf> {
        let profile = self.profile();
        let identity_path = profile.join("profile.id");
        let identity = match fs::symlink_metadata(&identity_path) {
            Ok(_) => {
                let file = private_file(&identity_path, false)?;
                let mut identity = String::new();
                file.take(33).read_to_string(&mut identity)?;
                identity
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                let identity = random_hex(16)?;
                let mut file = private_file(&identity_path, true)?;
                file.write_all(identity.as_bytes())?;
                file.sync_all()?;
                identity
            }
            Err(error) => return Err(error.into()),
        };
        if identity.len() != 32
            || !identity
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        {
            return Err(
                "native profile identity is invalid; retained data was not replaced".into(),
            );
        }
        replace_private(
            &profile.join("backend.dsn"),
            self.connection_uri().as_bytes(),
        )?;
        replace_private(&profile.join("backend.key"), random_hex(32)?.as_bytes())?;
        let config = profile.join("backend.json");
        replace_private(
            &config,
            format!("{{\"profile_id\":\"{identity}\"}}\n").as_bytes(),
        )?;
        Ok(config)
    }
}

fn random_hex(bytes: usize) -> Result<String> {
    let mut random = vec![0; bytes];
    getrandom::fill(&mut random)?;
    Ok(random.iter().map(|b| format!("{b:02x}")).collect())
}

fn replace_private(path: &Path, bytes: &[u8]) -> Result<()> {
    match fs::symlink_metadata(path) {
        Ok(_) => drop(private_file(path, false)?),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
        Err(error) => return Err(error.into()),
    }
    let parent = path.parent().ok_or("native bootstrap has no profile")?;
    let temporary = parent.join(format!(".bootstrap-{}", random_hex(16)?));
    let result = (|| -> Result<()> {
        let mut file = private_file(&temporary, true)?;
        file.write_all(bytes)?;
        file.sync_all()?;
        drop(file);
        fs::rename(&temporary, path)?;
        File::open(parent)?.sync_all()?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(temporary);
    }
    result
}
