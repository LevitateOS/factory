//! macOS unit host: the launchd broker's replacement for systemd templated
//! units. Each started unit gets a detached root supervisor that leases a
//! dedicated hidden pool user, exports the unit's paths through a root-owned
//! view directory, confines the unit with a generated Seatbelt profile, hands
//! over its listening socket or readiness channel, and applies memory, CPU and
//! process ceilings to every process of that pool user. Stopping a unit kills
//! the supervisor and every process of its pool user, then removes the unit's
//! runtime paths, so stop never depends on the supervisor being alive.
use super::{denied, id, STATE};
use soda_execution_protocol::host;
use soda_native_process::{Limits, UserScope};
use std::ffi::{CStr, CString};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, OwnedFd, RawFd};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::net::{UnixDatagram, UnixListener};
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

const POOL_PREFIX: &str = "_sodafactory";
const POOL_SIZE: u32 = 32;

#[derive(Clone, Copy, PartialEq, Eq)]
enum Mode {
    /// Runs to completion; start succeeds only when it exits 0.
    Oneshot,
    /// Started once its readiness datagram says `READY=1`.
    Notify,
    /// Socket activation: start binds the socket; the unit runs on the first
    /// connection and receives the listener as fd 3.
    Socket,
}
#[derive(Clone, Copy, PartialEq, Eq)]
enum Network {
    None,
    /// Loopback to this run's provider relay only, when one is active.
    RelayClient,
    /// Authenticated connections handed over by the supervisor.
    RelayServer,
}
struct Spec {
    kind: &'static str,
    binary: &'static str,
    mode: Mode,
    pool: bool,
    runtime: Option<&'static str>,
    /// The unit may not see its runtime directory (Linux overlays it).
    runtime_hidden: bool,
    umask: libc::mode_t,
    memory: u64,
    processes: u32,
    start: Duration,
    runtime_max: Option<Duration>,
    network: Network,
}
const MIB: u64 = 1024 * 1024;
const SPECS: &[Spec] = &[
    Spec {
        kind: "preparer",
        binary: "soda-factory-preparer",
        mode: Mode::Oneshot,
        pool: true,
        runtime: None,
        runtime_hidden: false,
        umask: 0o022,
        memory: 1024 * MIB,
        processes: 128,
        start: Duration::from_secs(3660),
        runtime_max: None,
        network: Network::RelayClient,
    },
    Spec {
        kind: "context",
        binary: "soda-factory-preparer",
        mode: Mode::Oneshot,
        pool: true,
        runtime: None,
        runtime_hidden: false,
        umask: 0o022,
        memory: 128 * MIB,
        processes: 32,
        start: Duration::from_secs(30),
        runtime_max: None,
        network: Network::None,
    },
    Spec {
        kind: "preparation-cleaner",
        binary: "soda-factory-preparation-cleaner",
        mode: Mode::Oneshot,
        pool: false,
        runtime: None,
        runtime_hidden: false,
        umask: 0o077,
        memory: 64 * MIB,
        processes: 8,
        start: Duration::from_secs(15),
        runtime_max: None,
        network: Network::None,
    },
    Spec {
        kind: "provider-relay",
        binary: "soda-factory-provider-relay",
        mode: Mode::Notify,
        pool: true,
        runtime: Some(host::RELAY_RUNTIME),
        runtime_hidden: false,
        umask: 0o077,
        memory: 64 * MIB,
        processes: 24,
        start: Duration::from_secs(10),
        runtime_max: Some(Duration::from_secs(4 * 60 * 60)),
        network: Network::RelayServer,
    },
    Spec {
        kind: "agent",
        binary: "soda-factory-agent",
        mode: Mode::Socket,
        pool: true,
        runtime: Some(host::AGENT_RUNTIME),
        runtime_hidden: true,
        umask: 0o077,
        memory: 4096 * MIB,
        processes: 128,
        start: Duration::from_secs(10),
        runtime_max: Some(Duration::from_secs(4 * 60 * 60 + 5 * 60)),
        network: Network::RelayClient,
    },
    Spec {
        kind: "reviewer",
        binary: "soda-factory-reviewer",
        mode: Mode::Socket,
        pool: true,
        runtime: Some(host::REVIEWER_RUNTIME),
        runtime_hidden: false,
        umask: 0o077,
        memory: 128 * MIB,
        processes: 32,
        start: Duration::from_secs(10),
        runtime_max: Some(Duration::from_secs(15)),
        network: Network::None,
    },
];

enum Entry {
    Read(PathBuf),
    Write(PathBuf),
    /// A private bounded volume mounted in the view, owned by root.
    Scratch { bytes: u64, mode: u32 },
}
struct Plan {
    spec: &'static Spec,
    run: String,
    args: Vec<String>,
    view: Vec<(&'static str, Entry)>,
    socket: Option<PathBuf>,
    /// Paths a root unit writes directly (no view).
    direct: Vec<PathBuf>,
    connect: Vec<PathBuf>,
}

fn unit_name(spec: &Spec, run: &str) -> String {
    format!("{}@{run}", spec.kind)
}
fn units_dir() -> PathBuf {
    Path::new(STATE).join("units")
}
fn record(name: &str) -> PathBuf {
    units_dir().join(name)
}
fn port_file(run: &str) -> PathBuf {
    units_dir().join(format!("provider-relay@{run}.port"))
}
fn view_dir(name: &str) -> PathBuf {
    Path::new(STATE).join("views").join(name)
}
fn scratch_image(name: &str, entry: &str) -> PathBuf {
    Path::new(STATE).join("scratch").join(format!("{name}.{entry}.sparseimage"))
}

/// Parse `soda-factory-KIND@RUN.service|socket`.
fn parse(unit: &str) -> io::Result<(&'static Spec, String)> {
    let rest = unit
        .strip_prefix("soda-factory-")
        .and_then(|rest| rest.strip_suffix(".service").or_else(|| rest.strip_suffix(".socket")))
        .ok_or_else(|| denied("unit name invalid"))?;
    let (kind, run) = rest.split_once('@').ok_or_else(|| denied("unit name invalid"))?;
    let spec = SPECS
        .iter()
        .find(|spec| spec.kind == kind)
        .ok_or_else(|| denied("unit kind unavailable on macOS"))?;
    if !id(run, 32) {
        return Err(denied("unit instance invalid"));
    }
    Ok((spec, run.to_owned()))
}

fn root_dir(path: &Path, mode: u32) -> io::Result<()> {
    match DirBuilder::new().mode(0o700).create(path) {
        Ok(()) => fs::set_permissions(path, fs::Permissions::from_mode(mode))?,
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
        Err(error) => return Err(error),
    }
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir() || metadata.uid() != 0 || metadata.mode() & 0o7777 != mode {
        return Err(denied("unit host directory invalid"));
    }
    Ok(())
}

fn desktop_uid() -> io::Result<u32> {
    let uid = host::broker_socket().symlink_metadata()?.uid();
    if uid == 0 {
        return Err(denied("broker socket owner invalid"));
    }
    Ok(uid)
}

fn plan(spec: &'static Spec, run: &str) -> io::Result<Plan> {
    let state = Path::new(STATE);
    let runs = state.join("runs").join(run);
    let preparation = state.join("preparations").join(run);
    let mut plan = Plan {
        spec,
        run: run.to_owned(),
        args: Vec::new(),
        view: Vec::new(),
        socket: None,
        direct: Vec::new(),
        connect: Vec::new(),
    };
    match spec.kind {
        "preparer" => {
            let mut manifest = String::new();
            File::open(preparation.join("manifest.env"))?
                .take(194)
                .read_to_string(&mut manifest)?;
            let (candidate, digest) =
                super::manifest_identity(&manifest).ok_or_else(|| denied("preparer manifest invalid"))?;
            plan.args = vec![run.into(), candidate.into(), digest.into()];
            plan.view.push(("factory-source", Entry::Read(runs.clone())));
            if runs.join("prepared").symlink_metadata().is_ok() {
                plan.view.push(("factory-prepared", Entry::Read(runs.join("prepared"))));
            }
            plan.view.push(("factory-output", Entry::Write(preparation.join("output"))));
        }
        "context" => {
            let context = state.join("contexts").join(run);
            plan.args = vec!["context".into()];
            for (name, file) in [
                ("factory-context-request", "context-request.json"),
                ("factory-context-base.bundle", "context-base.bundle"),
                ("factory-context-candidate.bundle", "context-candidate.bundle"),
            ] {
                plan.view.push((name, Entry::Read(context.join(file))));
            }
            plan.view.push(("factory-context-output", Entry::Write(context.join("context-output"))));
            plan.view.push((
                "factory-context-private",
                Entry::Scratch { bytes: 96 * MIB, mode: 0o777 },
            ));
        }
        "preparation-cleaner" => {
            plan.args = vec![run.into()];
            plan.direct.push(preparation);
        }
        "provider-relay" => {
            let desktop = desktop_uid()?;
            plan.args = vec![run.into(), desktop.to_string()];
            match fs::read(runs.join("network.env")) {
                Ok(value) if value == b"FACTORY_RELAY_MODE=setup\n" => plan.args.push("setup".into()),
                Ok(_) => return Err(denied("relay network declaration invalid")),
                Err(error) if error.kind() == io::ErrorKind::NotFound => (),
                Err(error) => return Err(error),
            }
            let owner = host::provider_sockets(desktop).join(format!("{run}.sock"));
            plan.view.push(("factory-provider.sock", Entry::Read(owner.clone())));
            plan.connect.push(owner);
        }
        "agent" => {
            let desktop = desktop_uid()?;
            plan.args = vec![run.into(), desktop.to_string()];
            plan.view.push(("factory-source", Entry::Read(preparation.join("output/repo"))));
            plan.view.push(("factory-prepared", Entry::Read(preparation.join("output"))));
            plan.view.push(("factory-captured-inputs", Entry::Read(runs)));
            plan.view.push(("factory-role", Entry::Read(preparation.join("agent.role"))));
            plan.view.push((
                "factory-agent-private",
                Entry::Scratch { bytes: 3 * 1024 * MIB, mode: 0o1777 },
            ));
            plan.socket = Some(host::agent_socket(run));
        }
        "reviewer" => {
            plan.args = vec![run.into()];
            plan.view.push(("factory-worktree", Entry::Read(preparation.join("output/repo"))));
            plan.socket = Some(preparation.join("reviewer.sock"));
        }
        _ => return Err(denied("unit kind unavailable on macOS")),
    }
    Ok(plan)
}

// ---- pool users -----------------------------------------------------------

struct Account {
    uid: u32,
    gid: u32,
    name: String,
}
fn account(name: &str) -> Option<Account> {
    let cname = CString::new(name).ok()?;
    let entry = unsafe { libc::getpwnam(cname.as_ptr()) };
    if entry.is_null() {
        return None;
    }
    let entry = unsafe { &*entry };
    (entry.pw_uid != 0 && entry.pw_uid < 1000).then(|| Account {
        uid: entry.pw_uid,
        gid: entry.pw_gid,
        name: name.to_owned(),
    })
}
fn pool_dir() -> PathBuf {
    Path::new(STATE).join("pool")
}
/// Lease an idle pool user for `name`. Leftover processes of a leased UID
/// are killed first; a UID holds at most one unit at a time.
fn lease(name: &str) -> io::Result<Account> {
    root_dir(&pool_dir(), 0o700)?;
    for index in 1..=POOL_SIZE {
        let Some(account) = account(&format!("{POOL_PREFIX}{index:02}")) else { continue };
        let path = pool_dir().join(account.uid.to_string());
        let file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&path);
        let mut file = match file {
            Ok(file) => file,
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => continue,
            Err(error) => return Err(error),
        };
        file.write_all(name.as_bytes())?;
        file.sync_all()?;
        retire_uid(account.uid)?;
        return Ok(account);
    }
    Err(denied("no idle Factory pool user; run the macOS execution installer"))
}
fn leased(name: &str) -> io::Result<Option<u32>> {
    let entries = match fs::read_dir(pool_dir()) {
        Ok(entries) => entries,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    for entry in entries {
        let entry = entry?;
        if fs::read(entry.path())? == name.as_bytes() {
            let uid = entry
                .file_name()
                .to_str()
                .and_then(|value| value.parse::<u32>().ok())
                .ok_or_else(|| denied("pool lease invalid"))?;
            return Ok(Some(uid));
        }
    }
    Ok(None)
}
fn leases_for(run: &str, kinds: &[&str]) -> io::Result<Vec<u32>> {
    kinds
        .iter()
        .filter_map(|kind| leased(&format!("{kind}@{run}")).transpose())
        .collect()
}
fn uid_processes(uid: u32) -> Vec<i32> {
    let mut pids = Vec::new();
    for kind in [4u32, 5] {
        let mut buffer = vec![0i32; 4096];
        let bytes = (buffer.len() * std::mem::size_of::<i32>()) as i32;
        let read = unsafe { libc::proc_listpids(kind, uid, buffer.as_mut_ptr().cast(), bytes) };
        if read > 0 {
            buffer.truncate(read as usize / std::mem::size_of::<i32>());
            pids.extend(buffer.into_iter().filter(|pid| *pid > 1));
        }
    }
    pids.sort_unstable();
    pids.dedup();
    pids.retain(|pid| !zombie(*pid));
    pids
}
fn bsd(pid: i32) -> Option<libc::proc_bsdinfo> {
    let mut value = std::mem::MaybeUninit::<libc::proc_bsdinfo>::zeroed();
    let size = std::mem::size_of::<libc::proc_bsdinfo>() as i32;
    let read =
        unsafe { libc::proc_pidinfo(pid, libc::PROC_PIDTBSDINFO, 0, value.as_mut_ptr().cast(), size) };
    (read == size).then(|| unsafe { value.assume_init() })
}
fn zombie(pid: i32) -> bool {
    bsd(pid).is_none_or(|info| info.pbi_status == libc::SZOMB)
}
fn start_time(pid: i32) -> Option<u64> {
    bsd(pid).map(|info| info.pbi_start_tvsec * 1_000_000 + info.pbi_start_tvusec)
}
fn retire_uid(uid: u32) -> io::Result<()> {
    let deadline = Instant::now() + Duration::from_secs(3);
    loop {
        let pids = uid_processes(uid);
        if pids.is_empty() {
            return Ok(());
        }
        for pid in pids {
            unsafe { libc::kill(pid, libc::SIGKILL) };
        }
        if Instant::now() >= deadline {
            return Err(denied("pool user processes survived retirement"));
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}
/// The pool user's own Darwin temporary directory, created by dirhelper.
fn user_temp(account: &Account) -> io::Result<PathBuf> {
    let (mut reader, writer) = std::os::unix::net::UnixStream::pair()?;
    let pid = unsafe { libc::fork() };
    if pid < 0 {
        return Err(io::Error::last_os_error());
    }
    if pid == 0 {
        drop(reader);
        let mut buffer = [0 as libc::c_char; 1024];
        let ok = unsafe {
            libc::setgid(account.gid) == 0
                && libc::setuid(account.uid) == 0
                && libc::confstr(libc::_CS_DARWIN_USER_TEMP_DIR, buffer.as_mut_ptr(), buffer.len()) > 0
        };
        if ok {
            let bytes = unsafe { CStr::from_ptr(buffer.as_ptr()) }.to_bytes();
            let _ = (&writer).write_all(bytes);
        }
        unsafe { libc::_exit(if ok { 0 } else { 1 }) };
    }
    drop(writer);
    let mut bytes = Vec::new();
    let read = (&mut reader).take(1024).read_to_end(&mut bytes);
    let mut status = 0;
    unsafe { libc::waitpid(pid, &mut status, 0) };
    read?;
    if bytes.is_empty() || bytes.contains(&0) {
        return Err(denied("pool user temporary directory invalid"));
    }
    let path = PathBuf::from(<std::ffi::OsStr as std::os::unix::ffi::OsStrExt>::from_bytes(&bytes));
    let path = fs::canonicalize(path)?;
    let metadata = path.symlink_metadata()?;
    if !path.starts_with("/private/var/folders") || !metadata.is_dir() || metadata.uid() != account.uid {
        return Err(denied("pool user temporary directory invalid"));
    }
    Ok(path)
}

// ---- Seatbelt ---------------------------------------------------------------

fn quote(path: &Path) -> String {
    let value = path.to_string_lossy();
    format!("\"{}\"", value.replace('\\', "\\\\").replace('"', "\\\""))
}
/// Executables that change identity on exec. A unit may run system tools but
/// never one of these.
fn setid_programs() -> Vec<PathBuf> {
    let mut found = Vec::new();
    for root in ["/usr/bin", "/usr/sbin", "/usr/libexec", "/bin", "/sbin", "/opt"] {
        let mut stack = vec![PathBuf::from(root)];
        while let Some(directory) = stack.pop() {
            let Ok(entries) = fs::read_dir(&directory) else { continue };
            for entry in entries.flatten() {
                let Ok(metadata) = entry.path().symlink_metadata() else { continue };
                if metadata.is_dir() && stack.len() < 4096 {
                    stack.push(entry.path());
                } else if metadata.is_file() && metadata.mode() & 0o6000 != 0 {
                    found.push(entry.path());
                }
            }
        }
    }
    found
}
fn profile(plan: &Plan, view: &Path, temp: Option<&Path>, relay_port: Option<u16>, notify: Option<&Path>) -> String {
    let mut rules = String::from(
        "(version 1)\n(deny default)\n(import \"system.sb\")\n\
         (allow process-fork)\n(allow signal (target same-sandbox))\n(allow sysctl-read)\n\
         (allow ipc-posix-shm-read-data (ipc-posix-name \"apple.shm.notification_center\"))\n\
         (allow mach-lookup (global-name \"com.apple.system.opendirectoryd.libinfo\") \
         (global-name \"com.apple.system.notification_center\") (global-name \"com.apple.logd\") \
         (global-name \"com.apple.system.logger\") (global-name \"com.apple.bsd.dirhelper\") \
         (global-name \"com.apple.trustd\") (global-name \"com.apple.trustd.agent\"))\n",
    );
    // Linux units see the whole read-only system except homes, temporary
    // directories and other runs; macOS mirrors that with system roots.
    rules.push_str("(allow file-read* file-test-existence process-exec");
    for root in [
        "/usr",
        "/bin",
        "/sbin",
        "/opt",
        "/Library/Developer/CommandLineTools",
        "/Applications/Xcode.app",
    ] {
        rules.push_str(&format!(" (subpath {})", quote(Path::new(root))));
    }
    rules.push_str(&format!(
        " (literal {}) (literal {}))\n",
        quote(&Path::new(host::LIBEXEC).join(plan.spec.binary)),
        quote(Path::new(host::TIMEOUT))
    ));
    rules.push_str("(allow file-read* file-test-existence (subpath \"/private/etc\") (literal \"/private/var/db/xcode_select_link\"))\n");
    // Resolving a developer tool's real path stats each ancestor directory.
    rules.push_str("(allow file-read-metadata (literal \"/Library\") (literal \"/Library/Developer\") (literal \"/Applications\"))\n");
    // Units track their own trees; the agent also owns ptys for launches.
    rules.push_str("(allow process-info* (target same-sandbox))\n(allow process-info-listpids)\n");
    if plan.spec.kind == "agent" {
        rules.push_str(
            "(allow pseudo-tty)\n(allow file-read* file-write* file-ioctl (literal \"/dev/ptmx\") (regex #\"^/dev/ttys[0-9]+$\"))\n",
        );
    }
    if let (Some(runtime), true) = (plan.spec.runtime, plan.spec.runtime_hidden) {
        rules.push_str(&format!("(allow file-read-metadata (literal {}))\n", quote(Path::new(runtime))));
    }
    let mut readable = vec![view.to_path_buf()];
    let mut writable = Vec::new();
    for (name, entry) in &plan.view {
        match entry {
            Entry::Read(path) => readable.push(path.clone()),
            Entry::Write(path) => writable.push(path.clone()),
            Entry::Scratch { .. } => writable.push(view.join(name)),
        }
    }
    writable.extend(plan.direct.iter().cloned());
    if let Some(temp) = temp {
        writable.push(temp.to_path_buf());
        rules.push_str(&format!("(allow process-exec (subpath {}))\n", quote(temp)));
    }
    if let (Some(runtime), false) = (plan.spec.runtime, plan.spec.runtime_hidden) {
        writable.push(Path::new(runtime).join(&plan.run));
    }
    for path in readable.iter().chain(&writable).chain(&plan.connect).chain(plan.socket.iter()) {
        rules.push_str(&format!(
            "(allow file-read-metadata file-test-existence (path-ancestors {}))\n",
            quote(path)
        ));
    }
    if let Some(socket) = &plan.socket {
        rules.push_str(&format!("(allow file-read-metadata (literal {}))\n", quote(socket)));
    }
    for path in &readable {
        rules.push_str(&format!("(allow file-read* file-test-existence (subpath {}))\n", quote(path)));
    }
    for path in &writable {
        rules.push_str(&format!("(allow file-read* file-write* (subpath {}))\n", quote(path)));
    }
    for path in plan.connect.iter().map(PathBuf::as_path).chain(notify) {
        rules.push_str(&format!("(allow network-outbound (literal {}))\n", quote(path)));
    }
    if let Some(port) = relay_port {
        rules.push_str(&format!("(allow network-outbound (remote ip \"localhost:{port}\"))\n"));
    }
    let denied: Vec<String> = setid_programs().iter().map(|path| quote(path)).collect();
    if !denied.is_empty() {
        rules.push_str(&format!("(deny process-exec (literal {}))\n", denied.join(") (literal ")));
    }
    rules
}
extern "C" {
    fn sandbox_init_with_parameters(
        profile: *const libc::c_char,
        flags: u64,
        parameters: *const *const libc::c_char,
        error: *mut *mut libc::c_char,
    ) -> libc::c_int;
}

// ---- scratch volumes --------------------------------------------------------

fn hdiutil(args: &[&std::ffi::OsStr]) -> io::Result<()> {
    let status = Command::new("/usr/bin/hdiutil")
        .env_clear()
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()?;
    if !status.success() {
        return Err(denied("unit scratch volume operation failed"));
    }
    Ok(())
}
fn mount_scratch(name: &str, entry: &str, mount: &Path, bytes: u64, mode: u32) -> io::Result<()> {
    root_dir(&Path::new(STATE).join("scratch"), 0o700)?;
    let image = scratch_image(name, entry);
    let _ = fs::remove_file(&image);
    DirBuilder::new().mode(0o700).create(mount)?;
    let size = format!("{}m", bytes / MIB);
    hdiutil(&[
        "create".as_ref(),
        "-quiet".as_ref(),
        "-size".as_ref(),
        size.as_ref(),
        "-type".as_ref(),
        "SPARSE".as_ref(),
        "-fs".as_ref(),
        "APFS".as_ref(),
        "-volname".as_ref(),
        "factory".as_ref(),
        image.as_os_str(),
    ])?;
    hdiutil(&[
        "attach".as_ref(),
        "-quiet".as_ref(),
        "-nobrowse".as_ref(),
        "-noautoopen".as_ref(),
        "-owners".as_ref(),
        "on".as_ref(),
        "-mountpoint".as_ref(),
        mount.as_os_str(),
        image.as_os_str(),
    ])?;
    // APFS creates housekeeping entries; the unit sees an empty root.
    for entry in fs::read_dir(mount)? {
        let entry = entry?;
        if entry.file_name().to_string_lossy().starts_with('.') {
            let _ = fs::remove_dir_all(entry.path()).or_else(|_| fs::remove_file(entry.path()));
        }
    }
    let path = CString::new(mount.as_os_str().as_encoded_bytes()).map_err(|_| denied("scratch path"))?;
    if unsafe { libc::chown(path.as_ptr(), 0, 0) } != 0 {
        return Err(io::Error::last_os_error());
    }
    fs::set_permissions(mount, fs::Permissions::from_mode(mode))
}
fn unmount_scratch(name: &str, entry: &str, mount: &Path) -> io::Result<()> {
    if mount.symlink_metadata().is_ok() {
        let parent = mount.parent().map(|path| path.symlink_metadata()).transpose()?;
        let mounted = mount.symlink_metadata()?.dev() != parent.map(|meta| meta.dev()).unwrap_or(0);
        if mounted {
            hdiutil(&["detach".as_ref(), "-quiet".as_ref(), "-force".as_ref(), mount.as_os_str()])?;
        }
        fs::remove_dir(mount)?;
    }
    match fs::remove_file(scratch_image(name, entry)) {
        Err(error) if error.kind() != io::ErrorKind::NotFound => Err(error),
        _ => Ok(()),
    }
}

// ---- relay peer admission ---------------------------------------------------

/// Whether the connecting loopback socket `local_port -> relay_port` is held by
/// a process of one of `uids`. Root reads other users' descriptor tables.
fn connection_owned_by(uids: &[u32], local_port: u16, relay_port: u16) -> bool {
    const PROC_PIDFDSOCKETINFO: libc::c_int = 3;
    const SOCKINFO_TCP: i32 = 2;
    // socket_fdinfo: proc_fileinfo (24) then socket_info, whose soi_kind is at
    // offset 232 and protocol union at 240; in_sockinfo starts with the foreign
    // and local ports in network order.
    const KIND: usize = 24 + 232;
    const FOREIGN_PORT: usize = 24 + 240;
    const LOCAL_PORT: usize = 24 + 244;
    for uid in uids {
        for pid in uid_processes(*uid) {
            let mut fds = vec![
                libc::proc_fdinfo { proc_fd: 0, proc_fdtype: 0 };
                1024
            ];
            let size = (fds.len() * std::mem::size_of::<libc::proc_fdinfo>()) as i32;
            let read = unsafe {
                libc::proc_pidinfo(pid, libc::PROC_PIDLISTFDS, 0, fds.as_mut_ptr().cast(), size)
            };
            if read <= 0 {
                continue;
            }
            fds.truncate(read as usize / std::mem::size_of::<libc::proc_fdinfo>());
            for fd in fds.iter().filter(|fd| fd.proc_fdtype == libc::PROX_FDTYPE_SOCKET as u32) {
                let mut info = [0u8; 1024];
                let read = unsafe {
                    libc::proc_pidfdinfo(
                        pid,
                        fd.proc_fd,
                        PROC_PIDFDSOCKETINFO,
                        info.as_mut_ptr().cast(),
                        info.len() as i32,
                    )
                };
                if read < (LOCAL_PORT + 4) as i32 {
                    continue;
                }
                let int = |at: usize| i32::from_ne_bytes(info[at..at + 4].try_into().unwrap());
                if int(KIND) == SOCKINFO_TCP
                    && u16::from_be(int(LOCAL_PORT) as u16) == local_port
                    && u16::from_be(int(FOREIGN_PORT) as u16) == relay_port
                {
                    return true;
                }
            }
        }
    }
    false
}
fn send_fd(channel: RawFd, fd: RawFd) -> io::Result<()> {
    let mut byte = [0u8; 1];
    let mut iov = libc::iovec { iov_base: byte.as_mut_ptr().cast(), iov_len: 1 };
    let space = unsafe { libc::CMSG_SPACE(std::mem::size_of::<RawFd>() as u32) } as usize;
    let mut control = vec![0u8; space];
    let mut message: libc::msghdr = unsafe { std::mem::zeroed() };
    message.msg_iov = &mut iov;
    message.msg_iovlen = 1;
    message.msg_control = control.as_mut_ptr().cast();
    message.msg_controllen = space as libc::socklen_t;
    unsafe {
        let header = libc::CMSG_FIRSTHDR(&message);
        (*header).cmsg_level = libc::SOL_SOCKET;
        (*header).cmsg_type = libc::SCM_RIGHTS;
        (*header).cmsg_len = libc::CMSG_LEN(std::mem::size_of::<RawFd>() as u32) as libc::socklen_t;
        std::ptr::write_unaligned(libc::CMSG_DATA(header).cast::<RawFd>(), fd);
        if libc::sendmsg(channel, &message, 0) != 1 {
            return Err(io::Error::last_os_error());
        }
    }
    Ok(())
}

// ---- lifecycle --------------------------------------------------------------

/// The supervisor recorded for `name` while it is the same live process.
fn supervisor(name: &str) -> io::Result<Option<i32>> {
    let value = match fs::read_to_string(record(name)) {
        Ok(value) => value,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    let (pid, start) = value
        .trim_end()
        .split_once(' ')
        .ok_or_else(|| denied("unit record invalid"))?;
    let pid: i32 = pid.parse().map_err(|_| denied("unit record invalid"))?;
    let start: u64 = start.parse().map_err(|_| denied("unit record invalid"))?;
    Ok((pid > 1 && start_time(pid) == Some(start) && !zombie(pid)).then_some(pid))
}

pub(super) fn is_active(unit: &str) -> io::Result<()> {
    let (spec, run) = parse(unit)?;
    match supervisor(&unit_name(spec, &run))? {
        Some(_) => Ok(()),
        None => Err(denied("fixed preparation service is not active")),
    }
}

/// Kill the supervisor and every process of the unit's pool user, then
/// remove the unit's runtime paths. Absent units stop successfully.
pub(super) fn stop(unit: &str) -> io::Result<()> {
    let (spec, run) = parse(unit)?;
    let name = unit_name(spec, &run);
    if let Some(pid) = supervisor(&name)? {
        unsafe { libc::kill(pid, libc::SIGKILL) };
        let deadline = Instant::now() + Duration::from_secs(3);
        while start_time(pid).is_some() && !zombie(pid) {
            if Instant::now() >= deadline {
                return Err(denied("unit supervisor survived stop"));
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
    cleanup(spec, &run)
}

fn cleanup(spec: &'static Spec, run: &str) -> io::Result<()> {
    let name = unit_name(spec, run);
    let uid = leased(&name)?;
    if let Some(uid) = uid {
        retire_uid(uid)?;
    }
    let plan = plan(spec, run).ok();
    if let Some(socket) = plan.as_ref().and_then(|plan| plan.socket.as_ref()) {
        remove_if_present(socket)?;
    }
    let view = view_dir(&name);
    if let Some(plan) = &plan {
        for (entry, kind) in &plan.view {
            if matches!(kind, Entry::Scratch { .. }) {
                unmount_scratch(&name, entry, &view.join(entry))?;
            }
        }
    }
    if view.symlink_metadata().is_ok() {
        fs::remove_dir_all(&view)?;
    }
    if let Some(runtime) = spec.runtime {
        let path = Path::new(runtime).join(run);
        if path.symlink_metadata().is_ok() {
            fs::remove_dir_all(&path)?;
        }
    }
    if spec.kind == "provider-relay" {
        remove_if_present(&port_file(run))?;
    }
    remove_if_present(&units_dir().join(format!("{name}.notify")))?;
    remove_if_present(&record(&name))?;
    if let Some(uid) = uid {
        remove_if_present(&pool_dir().join(uid.to_string()))?;
    }
    Ok(())
}
fn remove_if_present(path: &Path) -> io::Result<()> {
    match fs::remove_file(path) {
        Err(error) if error.kind() != io::ErrorKind::NotFound => Err(error),
        _ => Ok(()),
    }
}

/// The `start` command the broker runs and may cancel: the current broker
/// executable in unit-host mode.
pub(super) fn start_command(unit: &str) -> io::Result<Command> {
    parse(unit)?;
    let mut command = Command::new(std::env::current_exe()?);
    command.env_clear().args(["unit-start", unit]);
    Ok(command)
}

/// Entry for `soda-factory-broker unit-start UNIT`: fork the detached
/// supervisor and exit 0 once the unit is started (or, for a oneshot unit,
/// has completed successfully).
pub fn start_main(unit: &str) -> ! {
    let code = match start(unit) {
        Ok(()) => 0,
        Err(error) => {
            eprintln!("Factory unit {unit} failed to start: {error}");
            1
        }
    };
    std::process::exit(code)
}

fn start(unit: &str) -> io::Result<()> {
    if unsafe { libc::geteuid() } != 0 {
        return Err(denied("unit host requires root"));
    }
    let (spec, run) = parse(unit)?;
    let name = unit_name(spec, &run);
    root_dir(&units_dir(), 0o700)?;
    if supervisor(&name)?.is_some() {
        return if spec.mode == Mode::Oneshot {
            Err(denied("oneshot unit already running"))
        } else {
            Ok(())
        };
    }
    cleanup(spec, &run)?;
    let plan = plan(spec, &run)?;
    let (mut reader, writer) = std::os::unix::net::UnixStream::pair()?;
    let pid = unsafe { libc::fork() };
    if pid < 0 {
        return Err(io::Error::last_os_error());
    }
    if pid == 0 {
        drop(reader);
        unsafe { libc::setsid() };
        let code = match supervise(plan, writer) {
            Ok(code) => code,
            Err(error) => {
                eprintln!("Factory unit {name} supervisor failed: {error}");
                let _ = cleanup(spec, &run);
                1
            }
        };
        unsafe { libc::_exit(code) };
    }
    drop(writer);
    let mut byte = [0u8; 1];
    let read = reader.read(&mut byte)?;
    if read == 1 && byte[0] == b'R' {
        Ok(())
    } else {
        Err(denied("fixed preparation service failed"))
    }
}

fn supervise(plan: Plan, ready: std::os::unix::net::UnixStream) -> io::Result<i32> {
    let spec = plan.spec;
    let name = unit_name(spec, &plan.run);
    let own = unsafe { libc::getpid() };
    let start = start_time(own).ok_or_else(|| denied("unit supervisor identity unavailable"))?;
    let pending = units_dir().join(format!(".{name}"));
    fs::write(&pending, format!("{own} {start}\n"))?;
    fs::rename(&pending, record(&name))?;
    let account = if spec.pool { Some(lease(&name)?) } else { None };
    let temp = account.as_ref().map(user_temp).transpose()?;
    if let Some(temp) = &temp {
        // A previous holder of this pool user leaves nothing behind.
        for entry in fs::read_dir(temp)? {
            let path = entry?.path();
            let _ = fs::remove_dir_all(&path).or_else(|_| fs::remove_file(&path));
        }
    }

    // View directory and scratch volumes.
    let views = Path::new(STATE).join("views");
    root_dir(&views, 0o711)?;
    let view = view_dir(&name);
    root_dir(&view, 0o755)?;
    for (entry, kind) in &plan.view {
        match kind {
            Entry::Read(path) | Entry::Write(path) => {
                std::os::unix::fs::symlink(path, view.join(entry))?;
            }
            Entry::Scratch { bytes, mode } => {
                mount_scratch(&name, entry, &view.join(entry), *bytes, *mode)?;
            }
        }
    }
    if let (Some(runtime), Some(account)) = (spec.runtime, &account) {
        root_dir(Path::new(runtime), if spec.runtime_hidden { 0o700 } else { 0o755 })?;
        let path = Path::new(runtime).join(&plan.run);
        DirBuilder::new().mode(0o700).create(&path)?;
        let cpath = CString::new(path.as_os_str().as_encoded_bytes()).map_err(|_| denied("runtime"))?;
        if unsafe { libc::chown(cpath.as_ptr(), account.uid, account.gid) } != 0 {
            return Err(io::Error::last_os_error());
        }
    }

    // Listening socket for socket activation.
    let listener = match &plan.socket {
        Some(path) => {
            let parent = path.parent().ok_or_else(|| denied("unit socket path invalid"))?;
            if spec.kind == "agent" {
                root_dir(Path::new(host::SOCKETS), 0o755)?;
                root_dir(parent, 0o755)?;
            }
            remove_if_present(path)?;
            let listener = UnixListener::bind(path)?;
            fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
            Some(listener)
        }
        None => None,
    };

    // Relay networking: the server's loopback listener stays with this root
    // supervisor; clients learn the active relay port.
    let mut relay_listener = None;
    let mut relay_channel = None;
    let mut relay_port = None;
    match spec.network {
        Network::RelayServer => {
            let listener = std::net::TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, 0))?;
            relay_port = Some(listener.local_addr()?.port());
            listener.set_nonblocking(true)?;
            relay_listener = Some(listener);
            let (ours, theirs) = std::os::unix::net::UnixStream::pair()?;
            relay_channel = Some((ours, theirs));
        }
        Network::RelayClient => {
            if supervisor(&format!("provider-relay@{}", plan.run))?.is_some() {
                relay_port = fs::read_to_string(port_file(&plan.run))
                    .ok()
                    .and_then(|value| value.trim().parse::<u16>().ok());
            }
        }
        Network::None => (),
    }
    let notify = if spec.mode == Mode::Notify {
        let path = units_dir().join(format!("{name}.notify"));
        remove_if_present(&path)?;
        let socket = UnixDatagram::bind(&path)?;
        if let Some(account) = &account {
            let cpath = CString::new(path.as_os_str().as_encoded_bytes()).map_err(|_| denied("notify"))?;
            if unsafe { libc::chown(cpath.as_ptr(), account.uid, account.gid) } != 0 {
                return Err(io::Error::last_os_error());
            }
        }
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600))?;
        socket.set_read_timeout(Some(Duration::from_millis(50)))?;
        Some((socket, path))
    } else {
        None
    };
    let client_port = if spec.network == Network::RelayClient { relay_port } else { None };
    let profile = profile(
        &plan,
        &view,
        temp.as_deref(),
        client_port,
        notify.as_ref().map(|(_, path)| path.as_path()),
    );
    let profile = CString::new(profile).map_err(|_| denied("unit profile invalid"))?;

    let mut ready = Some(ready);
    let report = |ready: &mut Option<std::os::unix::net::UnixStream>, byte: u8| {
        if let Some(mut stream) = ready.take() {
            let _ = stream.write_all(&[byte]);
        }
    };
    if spec.mode == Mode::Socket {
        report(&mut ready, b'R');
        // Socket activation: wait for the first connection.
        let fd = listener.as_ref().expect("socket unit listener").as_raw_fd();
        loop {
            let mut poll = libc::pollfd { fd, events: libc::POLLIN, revents: 0 };
            let result = unsafe { libc::poll(&mut poll, 1, 1000) };
            if result > 0 {
                break;
            }
            if result < 0 && io::Error::last_os_error().kind() != io::ErrorKind::Interrupted {
                return Err(io::Error::last_os_error());
            }
        }
    }

    let log = units_dir().join(format!("{name}.log"));
    let log = OpenOptions::new()
        .create(true)
        .append(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(log)?;
    let mut command = Command::new(Path::new(host::LIBEXEC).join(spec.binary));
    command
        .env_clear()
        .args(&plan.args)
        .current_dir("/")
        .env("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
        .env("LANG", "C.UTF-8")
        .env(host::UNIT_VIEW, &view)
        .stdin(Stdio::null())
        .stdout(Stdio::from(log.try_clone()?))
        .stderr(Stdio::from(log))
        .process_group(0);
    if let Some(account) = &account {
        command
            .uid(account.uid)
            .gid(account.gid)
            .env("USER", &account.name)
            .env("LOGNAME", &account.name)
            .env("HOME", "/var/empty");
    }
    if let Some(temp) = &temp {
        command.env("TMPDIR", temp);
    }
    if let Some(runtime) = spec.runtime {
        command.env("RUNTIME_DIRECTORY", Path::new(runtime).join(&plan.run));
    }
    if let Some((_, path)) = &notify {
        command.env("NOTIFY_SOCKET", path);
    }
    if let Some(port) = client_port {
        command.env(host::RELAY_PORT, port.to_string());
    }
    let handover: Option<RawFd> = listener
        .as_ref()
        .map(|listener| listener.as_raw_fd())
        .or_else(|| relay_channel.as_ref().map(|(_, theirs)| theirs.as_raw_fd()));
    if handover.is_some() {
        command.env("LISTEN_FDS", "1");
    }
    let processes = spec.processes as libc::rlim_t;
    let umask = spec.umask;
    unsafe {
        command.pre_exec(move || {
            libc::umask(umask);
            let core = libc::rlimit { rlim_cur: 0, rlim_max: 0 };
            let tasks = libc::rlimit { rlim_cur: processes, rlim_max: processes };
            if libc::setrlimit(libc::RLIMIT_CORE, &core) != 0
                || libc::setrlimit(libc::RLIMIT_NPROC, &tasks) != 0
            {
                return Err(io::Error::last_os_error());
            }
            if let Some(fd) = handover {
                if fd != 3 && libc::dup2(fd, 3) != 3 {
                    return Err(io::Error::last_os_error());
                }
                let flags = libc::fcntl(3, libc::F_GETFD);
                if flags < 0 || libc::fcntl(3, libc::F_SETFD, flags & !libc::FD_CLOEXEC) < 0 {
                    return Err(io::Error::last_os_error());
                }
            }
            let mut error = std::ptr::null_mut();
            if sandbox_init_with_parameters(profile.as_ptr(), 0, std::ptr::null(), &mut error) != 0 {
                return Err(io::Error::new(io::ErrorKind::PermissionDenied, "unit sandbox refused"));
            }
            Ok(())
        });
    }
    let mut child = command.spawn()?;
    drop(command);
    let relay_channel = relay_channel.map(|(ours, _theirs)| ours);
    let mut scope = account
        .as_ref()
        .map(|account| {
            UserScope::new(
                account.uid,
                Limits {
                    duration: Duration::from_secs(4 * 60 * 60),
                    output_bytes: 1,
                    memory_bytes: spec.memory,
                    processes: spec.processes,
                    cpu_percent: 100,
                },
            )
        })
        .transpose()
        .map_err(|_| denied("unit ceilings unavailable"))?;
    let began = Instant::now();
    if spec.mode == Mode::Socket {
        drop(listener);
    }
    let mut started = spec.mode == Mode::Socket;
    if spec.mode == Mode::Notify {
        // Port first, so clients started after readiness can find it.
        if let Some(port) = relay_port {
            let pending = units_dir().join(format!(".provider-relay@{}.port", plan.run));
            fs::write(&pending, format!("{port}\n"))?;
            fs::rename(&pending, port_file(&plan.run))?;
        }
    }
    let status = loop {
        if let Some(status) = child.try_wait()? {
            break status;
        }
        if let Some(scope) = scope.as_mut() {
            scope.enforce().map_err(|_| denied("unit ceilings unavailable"))?;
        }
        let elapsed = began.elapsed();
        if !started && spec.mode != Mode::Oneshot && elapsed > spec.start {
            break kill_unit(&mut child, scope.as_mut());
        }
        if spec.mode == Mode::Oneshot && elapsed > spec.start {
            break kill_unit(&mut child, scope.as_mut());
        }
        if spec.runtime_max.is_some_and(|max| elapsed > max) {
            break kill_unit(&mut child, scope.as_mut());
        }
        if let Some((socket, _)) = &notify {
            let mut buffer = [0u8; 512];
            match socket.recv(&mut buffer) {
                Ok(length) => {
                    if !started
                        && std::str::from_utf8(&buffer[..length])
                            .is_ok_and(|value| value.lines().any(|line| line == "READY=1"))
                    {
                        started = true;
                        report(&mut ready, b'R');
                    }
                }
                Err(error)
                    if matches!(error.kind(), io::ErrorKind::WouldBlock | io::ErrorKind::TimedOut) => {}
                Err(error) => return Err(error),
            }
        } else if let (Some(listener), Some(channel)) = (&relay_listener, &relay_channel) {
            admit(listener, channel.as_raw_fd(), &plan.run, relay_port.unwrap_or(0));
            std::thread::sleep(Duration::from_millis(20));
        } else {
            std::thread::sleep(Duration::from_millis(50));
        }
    };
    if let Some(scope) = scope.as_mut() {
        scope.kill();
    }
    let success = status.success();
    if spec.mode == Mode::Oneshot {
        // Retire the tree before reporting, as systemd does for oneshot units.
        cleanup(spec, &plan.run)?;
        report(&mut ready, if success { b'R' } else { b'F' });
        return Ok(if success { 0 } else { 1 });
    }
    report(&mut ready, b'F');
    cleanup(spec, &plan.run)?;
    Ok(0)
}

fn kill_unit(child: &mut std::process::Child, scope: Option<&mut UserScope>) -> std::process::ExitStatus {
    if let Some(scope) = scope {
        scope.kill();
    }
    let _ = child.kill();
    child.wait().unwrap_or_else(|_| std::os::unix::process::ExitStatusExt::from_raw(9))
}

/// Hand authenticated loopback connections to the relay. Only processes of
/// this run's preparer and agent pool users may use the run's relay.
fn admit(listener: &std::net::TcpListener, channel: RawFd, run: &str, port: u16) {
    loop {
        let (stream, address) = match listener.accept() {
            Ok(accepted) => accepted,
            Err(_) => return,
        };
        let peers = leases_for(run, &["preparer", "agent"]).unwrap_or_default();
        let owned = address.ip().is_loopback()
            && !peers.is_empty()
            && connection_owned_by(&peers, address.port(), port);
        if owned {
            let fd: OwnedFd = stream.into();
            if send_fd(channel, fd.as_raw_fd()).is_err() {
                return;
            }
        }
    }
}

/// Entry for `soda-factory-broker prepare-host UID`, run by launchd at boot
/// and by the installer: `/private/var/run` is cleared at boot, so the socket
/// directories and the desktop-owned provider socket directory are recreated.
pub fn prepare_host_main(uid: &str) -> ! {
    let result = (|| {
        if unsafe { libc::geteuid() } != 0 {
            return Err(denied("host preparation requires root"));
        }
        let uid: u32 = uid.parse().map_err(|_| denied("desktop UID invalid"))?;
        if uid == 0 || uid.to_string().len() > 10 {
            return Err(denied("desktop UID invalid"));
        }
        let sockets = Path::new(host::SOCKETS);
        root_dir(sockets, 0o755)?;
        root_dir(&sockets.join("agents"), 0o755)?;
        root_dir(&sockets.join("providers"), 0o755)?;
        let providers = host::provider_sockets(uid);
        if let Err(error) = DirBuilder::new().mode(0o700).create(&providers) {
            if error.kind() != io::ErrorKind::AlreadyExists {
                return Err(error);
            }
        }
        let path = CString::new(providers.as_os_str().as_encoded_bytes()).map_err(|_| denied("path"))?;
        if unsafe { libc::lchown(path.as_ptr(), uid, libc::gid_t::MAX) } != 0 {
            return Err(io::Error::last_os_error());
        }
        fs::set_permissions(&providers, fs::Permissions::from_mode(host::PROVIDER_SOCKETS_MODE))?;
        let metadata = providers.symlink_metadata()?;
        if !metadata.is_dir() || metadata.uid() != uid {
            return Err(denied("provider socket directory invalid"));
        }
        root_dir(Path::new(STATE), host::TRAVERSE_MODE)
    })();
    match result {
        Ok(()) => std::process::exit(0),
        Err(error) => {
            eprintln!("Factory host preparation failed: {error}");
            std::process::exit(1)
        }
    }
}
