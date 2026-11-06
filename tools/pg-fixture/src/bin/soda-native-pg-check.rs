//! Real native PostgreSQL/store acceptance. No container engine or system setup.
use soda_desktop_runtime::database::NativePostgres;
use soda_desktop_runtime::supervisor::{NativeReady, NativeSupervisor};
use std::error::Error;
use std::ffi::OsString;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::os::unix::fs::OpenOptionsExt;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command};
use std::thread;
use std::time::{Duration, Instant};

fn main() {
    if let Err(error) = run() {
        eprintln!("native PostgreSQL check: {error}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn Error>> {
    let mut args: Vec<_> = std::env::args_os().skip(1).collect();
    if args.first().is_some_and(|arg| arg == "--supervisor") {
        return check_supervisor(&args[1..]);
    }
    if args.first().is_some_and(|arg| arg == "--supervisor-owner") {
        return supervisor_owner(&args[1..]);
    }
    let mut once = false;
    let mut selected_case = None;
    let mut case_input = None;
    loop {
        match args.first().and_then(|arg| arg.to_str()) {
            Some("--once") if !once => {
                once = true;
                args.remove(0);
            }
            Some("--case") | Some("--case-input") => {
                if args.len() < 2 {
                    return Err("case option requires a value".into());
                }
                let option = args.remove(0);
                let value = args.remove(0);
                let target = if option == "--case" {
                    &mut selected_case
                } else {
                    &mut case_input
                };
                if value.is_empty() || target.replace(value).is_some() {
                    return Err("case option must have one nonempty value".into());
                }
            }
            _ => break,
        }
    }
    if case_input.is_some() && selected_case.is_none() {
        return Err("--case-input requires --case".into());
    }
    if args.len() < 4 || args[2] != "--" {
        return Err(
            "usage: soda-native-pg-check [--once] [--case CASE] [--case-input PATH] PAYLOAD NEW_PROFILE -- STORE_CHECK_COMMAND [ARGS...]"
                .into(),
        );
    }
    let payload = fs::canonicalize(PathBuf::from(&args[0]))?;
    let profile = PathBuf::from(&args[1]);
    if fs::symlink_metadata(&profile).is_ok() {
        return Err("check requires a new profile; existing data is never removed".into());
    }
    let started = Instant::now();
    let mut database = NativePostgres::start(&payload, &profile)?;
    println!("Native init/start: {} ms", started.elapsed().as_millis());
    let backend_config = database.backend_config()?;
    let dsn_file = profile.join("connection.dsn");
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&dsn_file)?
        .write_all(database.connection_uri().as_bytes())?;
    let check = |phase: &str| -> Result<(), Box<dyn Error>> {
        let mut command = Command::new(&args[3]);
        command
            .args(&args[4..])
            .env("SODA_NATIVE_PG_DSN_FILE", &dsn_file)
            .env("SODA_NATIVE_FACTORY_CONFIG", &backend_config)
            .env("SODA_NATIVE_PG_PHASE", phase)
            .env("SODA_NATIVE_PG_POST_ID_FILE", profile.join("proof-post-id"));
        if let Some(case) = &selected_case {
            command.env("SODA_NATIVE_PG_CASE", case);
        }
        if let Some(input) = &case_input {
            command.env("SODA_NATIVE_PG_CASE_INPUT", input);
        }
        let status = command.status()?;
        if !status.success() {
            return Err(format!("store transaction check failed in {phase}").into());
        }
        Ok(())
    };
    check("write")?;
    database.stop()?;
    stopped(&profile)?;
    if once {
        println!("PASS: native PostgreSQL integration command and clean shutdown");
        return Ok(());
    }
    let started = Instant::now();
    let mut database = NativePostgres::start(&payload, &profile)?;
    database.backend_config()?;
    println!(
        "Native retained restart: {} ms",
        started.elapsed().as_millis()
    );
    check("restart")?;
    // This is an intentionally abrupt crash of this child's own process group,
    // not a selected system service or caller-supplied PID.
    if unsafe { libc::kill(-(database.process_id() as i32), libc::SIGKILL) } != 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    if database.stop().is_ok() {
        return Err("crashed PostgreSQL reported a clean exit".into());
    }
    let started = Instant::now();
    let mut database = NativePostgres::start(&payload, &profile)?;
    database.backend_config()?;
    println!(
        "Native crash recovery: {} ms",
        started.elapsed().as_millis()
    );
    check("recover")?;
    database.stop()?;
    stopped(&profile)?;
    println!("PASS: real store transaction, restart, WAL recovery and clean native shutdown");
    Ok(())
}

fn stopped(profile: &Path) -> Result<(), Box<dyn Error>> {
    if profile.join("data/postmaster.pid").try_exists()?
        || profile.join("socket/.s.PGSQL.5432").try_exists()?
    {
        return Err("PostgreSQL retained a live PID/socket after clean shutdown".into());
    }
    Ok(())
}

// Existing native integration driver: real production children and retained
// Store commands, not a substitute process protocol or a new unit-test root.
fn check_supervisor(args: &[OsString]) -> Result<(), Box<dyn Error>> {
    if args.len() < 5 || args[3] != "--" {
        return Err("usage: soda-native-pg-check --supervisor PAYLOAD NEW_PROFILE BACKEND -- STORE_CHECK [ARGS...]".into());
    }
    let payload = fs::canonicalize(PathBuf::from(&args[0]))?;
    let profile = PathBuf::from(&args[1]);
    let backend = fs::canonicalize(PathBuf::from(&args[2]))?;
    if fs::symlink_metadata(&profile).is_ok() {
        return Err("supervisor check requires a new profile".into());
    }
    let check = |phase: &str| -> Result<(), Box<dyn Error>> {
        let status = Command::new(&args[4])
            .args(&args[5..])
            .env("SODA_NATIVE_PG_DSN_FILE", profile.join("backend.dsn"))
            .env("SODA_NATIVE_FACTORY_CONFIG", profile.join("backend.json"))
            .env("SODA_NATIVE_PG_PHASE", phase)
            .env("SODA_NATIVE_PG_POST_ID_FILE", profile.join("proof-post-id"))
            .status()?;
        if !status.success() {
            return Err("managed native Store check failed".into());
        }
        Ok(())
    };
    let started = Instant::now();
    let supervisor = NativeSupervisor::start(&payload, &backend, &profile)?;
    let first = supervisor.health()?;
    println!(
        "Managed native init/readiness: {} ms",
        started.elapsed().as_millis()
    );
    let password = fs::read(profile.join("database.password"))?;
    let key = fs::read(profile.join("backend.key"))?;
    let identity = fs::read(profile.join("profile.id"))?;
    if NativeSupervisor::start(&payload, &backend, &profile).is_ok() {
        return Err("duplicate supervisor was admitted".into());
    }
    if fs::read(profile.join("backend.key"))? != key || supervisor.health()? != first {
        return Err("duplicate supervisor rotated/replaced live backend".into());
    }
    for name in [
        "supervisor.lock",
        "backend.key",
        "backend.dsn",
        "backend.json",
        "backend.log",
        "profile.id",
    ] {
        if fs::metadata(profile.join(name))?.permissions().mode() & 0o077 != 0 {
            return Err("supervisor inputs are not private".into());
        }
    }
    let limits = soda_unix_http::Limits {
        header_bytes: 8192,
        body_bytes: 4096,
    };
    let absent = soda_unix_http::request(
        &profile.join("backend.sock"),
        "GET",
        "/healthz",
        "soda-native",
        &[],
        Duration::from_secs(3),
        limits,
    )
    .map_err(|_| "unauthenticated readiness failed to reach actual server")?;
    if absent.status != 401 {
        return Err("readiness allowed a missing capability".into());
    }
    let wrong = soda_unix_http::request_authenticated(
        &profile.join("backend.sock"),
        "GET",
        "/healthz",
        "soda-native",
        &[],
        &"0".repeat(64),
        Duration::from_secs(3),
        limits,
    )
    .map_err(|_| "wrong-capability probe failed to reach actual server")?;
    if wrong.status != 401 {
        return Err("readiness allowed a wrong capability".into());
    }
    let malformed = soda_unix_http::request_authenticated(
        &profile.join("backend.sock"),
        "GET",
        "/healthz",
        "soda-native",
        &[],
        "x\r\nOrigin: https://example.invalid",
        Duration::from_secs(3),
        limits,
    );
    if malformed != Err(soda_unix_http::Error::InvalidRequest) {
        return Err("private client allowed header injection".into());
    }
    check("write")?;
    supervisor.stop()?;
    gone(&first)?;
    stopped(&profile)?;
    let supervisor = NativeSupervisor::start(&payload, &backend, &profile)?;
    let second = supervisor.health()?;
    if second.profile != first.profile
        || second.instance == first.instance
        || fs::read(profile.join("profile.id"))? != identity
        || fs::read(profile.join("database.password"))? != password
        || fs::read(profile.join("backend.key"))? == key
    {
        return Err("managed restart lost identity/password or reused capability/instance".into());
    }
    check("restart")?;
    supervisor.stop()?;
    gone(&second)?;
    stopped(&profile)?;
    let supervisor = NativeSupervisor::start(&payload, &backend, &profile)?;
    let crashed = supervisor.health()?;
    unsafe {
        if libc::kill(crashed.backend_process as i32, libc::SIGKILL) != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
    }
    await_unavailable(&supervisor)?;
    if supervisor.stop().is_ok() {
        return Err("backend crash reported clean shutdown".into());
    }
    gone(&crashed)?;
    stopped(&profile)?;
    let supervisor = NativeSupervisor::start(&payload, &backend, &profile)?;
    let crashed = supervisor.health()?;
    unsafe {
        if libc::kill(-(crashed.database_process as i32), libc::SIGKILL) != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
    }
    await_unavailable(&supervisor)?;
    if supervisor.stop().is_ok() {
        return Err("database crash reported clean shutdown".into());
    }
    gone(&crashed)?;
    let supervisor = NativeSupervisor::start(&payload, &backend, &profile)?;
    let recovered = supervisor.health()?;
    check("recover")?;
    supervisor.stop()?;
    gone(&recovered)?;
    stopped(&profile)?;
    // Test abrupt death of the process owning the dedicated runtime thread.
    // Adopt and reap only the exact children recorded by this owned subprocess.
    unsafe {
        if libc::prctl(libc::PR_SET_CHILD_SUBREAPER, 1) != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
    }
    let mut owner = OwnerProcess(
        Command::new(std::env::current_exe()?)
            .arg("--supervisor-owner")
            .arg(&payload)
            .arg(&profile)
            .arg(&backend)
            .spawn()?,
    );
    let ready_path = profile.join("supervisor-check-ready");
    let deadline = Instant::now() + Duration::from_secs(20);
    while !ready_path.try_exists()? {
        if owner.0.try_wait()?.is_some() {
            return Err("owned supervisor exited before crash check".into());
        }
        if Instant::now() > deadline {
            let _ = owner.0.kill();
            let _ = owner.0.wait();
            return Err("owned supervisor check timed out".into());
        }
        thread::sleep(Duration::from_millis(25));
    }
    let pids = fs::read_to_string(&ready_path)?
        .lines()
        .map(str::parse::<i32>)
        .collect::<std::result::Result<Vec<_>, _>>()?;
    if pids.len() != 2 || pids.iter().any(|pid| *pid <= 0) {
        return Err("invalid owned supervisor readiness".into());
    }
    owner.0.kill()?;
    let _ = owner.0.wait()?;
    for pid in pids {
        let deadline = Instant::now() + Duration::from_secs(15);
        loop {
            let mut status = 0;
            let observed = unsafe { libc::waitpid(pid, &mut status, libc::WNOHANG) };
            if observed == pid {
                break;
            }
            if observed < 0 {
                return Err("owned orphan was not reaped".into());
            }
            if Instant::now() > deadline {
                return Err("supervisor death left a live owned child".into());
            }
            thread::sleep(Duration::from_millis(25));
        }
    }
    stopped(&profile)?;
    let supervisor = NativeSupervisor::start(&payload, &backend, &profile)?;
    let recovered = supervisor.health()?;
    check("recover")?;
    supervisor.stop()?;
    gone(&recovered)?;
    stopped(&profile)?;
    if NativeSupervisor::start(&payload, &std::env::current_exe()?, &profile).is_ok() {
        return Err("failing native backend became ready".into());
    }
    stopped(&profile)?;
    if NativeSupervisor::start(&profile.join("missing-payload"), &backend, &profile).is_ok() {
        return Err("missing native database became ready".into());
    }
    stopped(&profile)?;
    let version_path = profile.join("data/PG_VERSION");
    let version = fs::read(&version_path)?;
    fs::write(&version_path, b"999\n")?;
    let refused = NativeSupervisor::start(&payload, &backend, &profile).is_err();
    let kept = fs::read(&version_path)? == b"999\n";
    fs::write(&version_path, &version)?;
    if !refused || !kept {
        return Err("incompatible database was admitted or replaced".into());
    }
    let supervisor = NativeSupervisor::start(&payload, &backend, &profile)?;
    let ready = supervisor.health()?;
    check("recover")?;
    supervisor.stop()?;
    gone(&ready)?;
    stopped(&profile)?;
    if fs::read(profile.join("profile.id"))? != identity
        || fs::read(profile.join("database.password"))? != password
    {
        return Err("failure/recovery replaced retained profile credentials".into());
    }
    for name in ["backend.log", "postgres.log"] {
        let log = fs::read(profile.join(name))?;
        for secret in [
            &password,
            &key,
            &fs::read(profile.join("backend.key"))?,
            &fs::read(profile.join("backend.dsn"))?,
        ] {
            if log.windows(secret.len()).any(|window| window == secret) {
                return Err("native child log exposed private input".into());
            }
        }
    }
    println!("PASS: managed PG/Go readiness, retained Store transaction/restart, duplicate refusal, child/owner crash retirement, startup failure, key rotation and clean ordered shutdown");
    Ok(())
}
fn await_unavailable(supervisor: &NativeSupervisor) -> Result<(), Box<dyn Error>> {
    let deadline = Instant::now() + Duration::from_secs(10);
    loop {
        if supervisor.health().is_err() {
            return Ok(());
        }
        if Instant::now() > deadline {
            return Err("crashed native runtime remained ready".into());
        }
        thread::sleep(Duration::from_millis(25));
    }
}
fn gone(ready: &NativeReady) -> Result<(), Box<dyn Error>> {
    for pid in [ready.backend_process, ready.database_process] {
        if unsafe { libc::kill(pid as i32, 0) } == 0 {
            return Err("native shutdown left an owned process".into());
        }
    }
    Ok(())
}
fn supervisor_owner(args: &[OsString]) -> Result<(), Box<dyn Error>> {
    if args.len() != 3 {
        return Err("invalid supervisor crash-check inputs".into());
    }
    let supervisor = NativeSupervisor::start(
        Path::new(&args[0]),
        Path::new(&args[2]),
        Path::new(&args[1]),
    )?;
    let ready = supervisor.health()?;
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(Path::new(&args[1]).join(".supervisor-check-ready"))?;
    write!(
        file,
        "{}\n{}\n",
        ready.backend_process, ready.database_process
    )?;
    file.sync_all()?;
    fs::rename(
        Path::new(&args[1]).join(".supervisor-check-ready"),
        Path::new(&args[1]).join("supervisor-check-ready"),
    )?;
    loop {
        thread::park_timeout(Duration::from_secs(1));
        supervisor.health()?;
    }
}

struct OwnerProcess(Child);
impl Drop for OwnerProcess {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
