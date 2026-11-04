#![cfg(target_os = "linux")]
//! Fixed, retryable cleanup of one stopped preparer's private checkout.
//! The privileged broker must stop and reconcile the preparer and reviewer
//! instances before asking the manager to start this service. The broker owns
//! removal of the now-empty root-owned per-run directory.
use std::fs::{self, File};
use std::io;
use std::os::unix::fs::MetadataExt;
use std::path::Path;

const STATE: &str = "/run/soda-factory-broker";
const PREPARATIONS: &str = "/run/soda-factory-broker/preparations";

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

fn run_id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn root_directory(path: &Path, modes: &[u32]) -> io::Result<bool> {
    let metadata = match path.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
        Err(error) => return Err(error),
    };
    if !metadata.is_dir() || metadata.uid() != 0 || !modes.contains(&(metadata.mode() & 0o7777)) {
        return Err(refused("preparation directory invalid"));
    }
    Ok(true)
}

fn root_manifest(path: &Path) -> io::Result<bool> {
    let metadata = match path.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
        Err(error) => return Err(error),
    };
    if !metadata.is_file() || metadata.uid() != 0 || metadata.mode() & 0o7777 != 0o400 {
        return Err(refused("preparation manifest invalid"));
    }
    Ok(true)
}

fn clean() -> io::Result<()> {
    let args: Vec<String> = std::env::args_os()
        .map(|value| {
            value
                .into_string()
                .map_err(|_| refused("preparation cleaner argument encoding invalid"))
        })
        .collect::<io::Result<_>>()?;
    if args.len() != 2 || !run_id(&args[1]) {
        return Err(refused("preparation cleaner run invalid"));
    }
    if unsafe { libc::geteuid() } != 0
        || unsafe { libc::getuid() } != 0
        || unsafe { libc::prctl(libc::PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) } != 1
    {
        return Err(refused("preparation cleaner service identity invalid"));
    }
    if !root_directory(Path::new(STATE), &[0o700])?
        || !root_directory(Path::new(PREPARATIONS), &[0o700])?
    {
        println!("preparation {} already absent", args[1]);
        return Ok(());
    }
    let run = Path::new(PREPARATIONS).join(&args[1]);
    if !root_directory(&run, &[0o700])? {
        println!("preparation {} already absent", args[1]);
        return Ok(());
    }
    for entry in fs::read_dir(&run)? {
        let name = entry?.file_name();
        if name != "output" && name != "manifest.env" {
            return Err(refused("preparation has unexpected entries"));
        }
    }
    let output = run.join("output");
    let has_output = root_directory(&output, &[0o700, 0o777, 0o555])?;
    let manifest = run.join("manifest.env");
    let has_manifest = root_manifest(&manifest)?;
    if has_output {
        fs::remove_dir_all(&output)?;
        File::open(&run)?.sync_all()?;
    }
    if has_manifest {
        fs::remove_file(&manifest)?;
        File::open(&run)?.sync_all()?;
    }
    if fs::read_dir(&run)?.next().is_some() {
        return Err(refused("preparation has unexpected entries"));
    }
    println!("preparation {} contents removed", args[1]);
    Ok(())
}

fn main() {
    if let Err(error) = clean() {
        eprintln!("Factory preparation cleanup refused: {error}");
        std::process::exit(1);
    }
}
