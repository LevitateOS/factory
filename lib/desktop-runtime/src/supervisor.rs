//! Native children stay on one owner thread. No browser command, connection,
//! credential or executor authority is provided by process readiness.
use crate::child_process::parent_death;
use crate::database::NativePostgres;
use crate::private_fs::{private_dir, private_file};
use soda_unix_http::{request_authenticated, Limits};
use std::fs::{self, File};
use std::io::Read;
use std::os::unix::fs::PermissionsExt;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

type Result<T> = std::result::Result<T, String>;
const START_TIMEOUT: Duration = Duration::from_secs(15);
const STOP_TIMEOUT: Duration = Duration::from_secs(30);

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeReady {
    pub profile: String,
    pub instance: String,
    pub backend_process: u32,
    pub database_process: u32,
}
enum Control {
    Probe(Sender<Result<NativeReady>>),
    Stop,
    GithubChannel(tokio::sync::oneshot::Sender<Result<NativeChannel>>),
}
#[derive(Clone)]
pub(crate) struct NativeChannel {
    pub socket: PathBuf,
    pub key: zeroize::Zeroizing<String>,
    pub instance: String,
    pub profile: String,
}
struct Retire(Arc<AtomicBool>);
impl Drop for Retire {
    fn drop(&mut self) {
        self.0.store(false, Ordering::SeqCst);
    }
}
struct RetireExecutions(Option<Arc<Mutex<crate::factory_provider::Executions>>>);
impl RetireExecutions {
    fn finish(&mut self) -> Result<()> {
        use crate::factory_provider::{CustodyState, ExecutionOutcome};
        let Some(executions) = self.0.take() else {
            return Ok(());
        };
        let reports = executions
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .retire();
        if reports.iter().any(|report| {
            !matches!(report.outcome, ExecutionOutcome::Complete(_))
                || !matches!(&report.custody, Ok(custody) if custody.sealed && custody.state == CustodyState::Released)
        }) {
            return Err("native execution retirement requires reconciliation".into());
        }
        Ok(())
    }
}
impl Drop for RetireExecutions {
    fn drop(&mut self) {
        if let Err(error) = self.finish() {
            eprintln!("native execution retirement: {error}");
        }
    }
}
/// Owns this profile's children and admitted native ports. Private channel
/// material stays behind the native host API.
/// Window closure must retain this object; explicit Quit consumes it with stop.
pub struct NativeSupervisor {
    control: Sender<Control>,
    owner: Option<JoinHandle<Result<()>>>,
    alive: Arc<AtomicBool>,
    products: Option<crate::product::Products>,
    github: Option<crate::github_provider::Client>,
    collaboration: Option<crate::collaboration::Port>,
    executions: Option<Arc<Mutex<crate::factory_provider::Executions>>>,
    execution_profile: Option<PathBuf>,
}
impl NativeSupervisor {
    pub fn start(payload: &Path, backend: &Path, profile: &Path) -> Result<Self> {
        let (control, commands) = mpsc::channel();
        let (started, ready) = mpsc::channel();
        let payload = payload.to_path_buf();
        let backend = backend.to_path_buf();
        let profile = profile.to_path_buf();
        let alive = Arc::new(AtomicBool::new(true));
        let owner_alive = alive.clone();
        let owner = thread::Builder::new()
            .name("factory-native-owner".into())
            .spawn(move || {
                let _retire = Retire(owner_alive.clone());
                let mut children = match Children::start(&payload, &backend, &profile) {
                    Ok(children) => children,
                    Err(error) => {
                        let _ = started.send(Err(error.clone()));
                        return Err(error);
                    }
                };
                let ready = match children.health() {
                    Ok(ready) => ready,
                    Err(error) => {
                        let _ = started.send(Err(error));
                        return children.stop();
                    }
                };
                let channel = NativeChannel {
                    socket: children.profile.join("backend.sock"),
                    key: zeroize::Zeroizing::new(children.key.clone()),
                    instance: ready.instance,
                    profile: ready.profile,
                };
                let mut products =
                    match crate::product::Owner::start(channel.clone(), owner_alive.clone()) {
                        Ok(products) => products,
                        Err(error) => {
                            let _ = started.send(Err(error));
                            return children.stop();
                        }
                    };
                if children.broker {
                    let recovery = (|| {
                        let runtime = tokio::runtime::Builder::new_current_thread()
                            .enable_all()
                            .build()
                            .map_err(|_| "native recovery runtime could not start".to_owned())?;
                        runtime
                            .block_on(crate::factory_provider::recovery::recover_profile(
                                products.handle.clone(),
                                &children.identity,
                            ))
                            .map_err(|error| {
                                format!("native broker restart recovery unconfirmed: {error}")
                            })?;
                        children.health().map(|_| ())
                    })();
                    if let Err(error) = recovery {
                        let _ = started.send(Err(error.clone()));
                        let _ = products.stop();
                        let _ = children.stop();
                        return Err(error);
                    }
                }
                let mut github =
                    match crate::github_provider::Owner::start_with_alive(owner_alive.clone()) {
                        Ok(github) => github,
                        Err(_) => {
                            let _ = started.send(Err("native GitHub owner could not start".into()));
                            products.stop()?;
                            return children.stop();
                        }
                    };
                let executions = match crate::factory_provider::Executions::new(
                    &channel.profile,
                    &channel.instance,
                    8,
                ) {
                    Ok(executions) => Arc::new(Mutex::new(executions)),
                    Err(error) => {
                        let _ = started.send(Err(error.to_string()));
                        let _ = github.stop();
                        let _ = products.stop();
                        let _ = children.stop();
                        return Err(error.to_string());
                    }
                };
                let mut collaboration = match crate::collaboration::Owner::start(
                    channel.clone(),
                    github.client(),
                    owner_alive.clone(),
                    executions.clone(),
                ) {
                    Ok(owner) => owner,
                    Err(error) => {
                        let _ = started.send(Err(error));
                        let _ = github.stop();
                        let _ = products.stop();
                        return children.stop();
                    }
                };
                // This guard also joins executions if the owner unwinds. It is
                // declared after their dependencies so it retires first.
                let mut execution_owner = RetireExecutions(Some(executions.clone()));
                if started
                    .send(Ok((
                        products.handle.clone(),
                        github.client(),
                        collaboration.port.clone(),
                        executions,
                        children.profile.clone(),
                    )))
                    .is_err()
                {
                    owner_alive.store(false, Ordering::SeqCst);
                    let executions_stopped = execution_owner.finish();
                    let _ = collaboration.stop();
                    let _ = github.stop();
                    let _ = products.stop();
                    return executions_stopped.and(children.stop());
                }
                let failure =
                    run_owner(&mut children, &products, &github, &collaboration, commands);
                owner_alive.store(false, Ordering::SeqCst);
                let executions_stopped = execution_owner.finish();
                let collaboration_stopped = collaboration.stop();
                let provider_stopped = github.stop().map_err(|error| error.to_string());
                let requests_stopped = products.stop();
                let stopped = children.stop();
                match (failure, stopped) {
                    (Some(error), _) => Err(error),
                    (None, stopped) => collaboration_stopped
                        .and(executions_stopped)
                        .and(provider_stopped)
                        .and(requests_stopped)
                        .and(stopped),
                }
            })
            .map_err(|_| "native supervisor owner could not start")?;
        let mut supervisor = Self {
            control,
            owner: Some(owner),
            alive,
            products: None,
            github: None,
            collaboration: None,
            executions: None,
            execution_profile: None,
        };
        match ready.recv() {
            Ok(Ok((products, github, collaboration, executions, profile))) => {
                supervisor.products = Some(products);
                supervisor.github = Some(github);
                supervisor.collaboration = Some(collaboration);
                supervisor.executions = Some(executions);
                supervisor.execution_profile = Some(profile);
                Ok(supervisor)
            }
            Ok(Err(error)) => {
                let _ = supervisor.stop_inner();
                Err(error)
            }
            Err(_) => {
                let _ = supervisor.stop_inner();
                Err("native supervisor startup stopped".into())
            }
        }
    }
    /// Fresh bounded store/process readiness, never execution or account admission.
    pub fn health(&self) -> Result<NativeReady> {
        let (reply, response) = mpsc::channel();
        self.control
            .send(Control::Probe(reply))
            .map_err(|_| "native supervisor is unavailable")?;
        response
            .recv_timeout(Duration::from_secs(5))
            .map_err(|_| "native readiness is unavailable")?
    }
    /// Native host boundary only. GUI callers must use the shell's typed route
    /// descriptors; this method never accepts another socket or capability.
    pub fn product(
        &self,
        method: &str,
        target: &str,
        body: &[u8],
    ) -> Result<soda_unix_http::Response> {
        self.products
            .as_ref()
            .ok_or("native runtime is unavailable")?
            .blocking(method, target, body)
    }
    /// Owns request inputs and never borrows the supervisor while I/O is pending.
    /// Dropping the future cancels its request; retirement joins the request owner.
    pub fn product_request(
        &self,
        method: &str,
        target: &str,
        body: &[u8],
    ) -> impl std::future::Future<Output = Result<soda_unix_http::Response>> {
        let request = self
            .products
            .as_ref()
            .map(|products| products.request(method, target, body));
        async move { request.ok_or("native runtime is unavailable")?.await }
    }
    /// Trusted native lifecycle read. No browser route, caller-supplied path,
    /// current process authority or recovery effect is admitted by this view.
    pub fn native_recovery(
        &self,
        broker_run: &str,
    ) -> impl std::future::Future<Output = std::io::Result<crate::factory_provider::NativeRecovery>>
    {
        let products = self.products.clone();
        let broker_run = broker_run.to_owned();
        async move {
            let products =
                products.ok_or_else(|| std::io::Error::other("native runtime unavailable"))?;
            crate::factory_provider::NativeRecovery::read(products, &broker_run).await
        }
    }
    /// Trusted native composition only: Go must have captured current admission
    /// before this call. Neither this API nor an intent's shape admits product work.
    pub fn launch_admitted_execution(
        &self,
        start: crate::factory_provider::ExecutionStart,
    ) -> Result<crate::factory_provider::ExecutionTicket> {
        let profile = self
            .execution_profile
            .as_ref()
            .ok_or("native execution unavailable")?;
        self.with_executions(|executions| executions.launch(profile.clone(), start))
    }
    pub fn execution_ticket(
        &self,
        intent: &crate::factory_provider::LaunchIntent,
    ) -> Result<crate::factory_provider::ExecutionTicket> {
        let binding = crate::factory_provider::ExecutionBinding::from_intent(intent)
            .map_err(|error| error.to_string())?;
        self.with_executions(|executions| executions.ticket(&binding))
    }
    pub fn execution_outcome(
        &self,
        intent: &crate::factory_provider::LaunchIntent,
    ) -> Result<crate::factory_provider::ExecutionOutcome> {
        let binding = crate::factory_provider::ExecutionBinding::from_intent(intent)
            .map_err(|error| error.to_string())?;
        self.with_executions(|executions| executions.outcome(&binding))
    }
    pub fn cancel_execution(&self, intent: &crate::factory_provider::LaunchIntent) -> Result<()> {
        let binding = crate::factory_provider::ExecutionBinding::from_intent(intent)
            .map_err(|error| error.to_string())?;
        self.with_executions(|executions| executions.cancel(&binding))
    }
    fn with_executions<T>(
        &self,
        effect: impl FnOnce(&mut crate::factory_provider::Executions) -> std::io::Result<T>,
    ) -> Result<T> {
        let mut executions = self
            .executions
            .as_ref()
            .ok_or("native execution unavailable")?
            .lock()
            .map_err(|_| "native execution owner failed")?;
        if !self.alive.load(Ordering::SeqCst) {
            return Err("native execution owner retired".into());
        }
        effect(&mut executions).map_err(|error| error.to_string())
    }
    /// Native host admission only; the shell must first admit its window and
    /// selection. The capability is finite and account verification still occurs
    /// in the Rust connector before Go may bind a product actor.
    pub fn admit_product(
        &self,
        credentials: Arc<crate::credentials::Credentials>,
        capability: crate::credentials::TokenCapability,
        repository: Option<String>,
    ) -> Result<crate::collaboration::NativeRequest> {
        self.collaboration
            .as_ref()
            .ok_or("native collaboration unavailable")?
            .admit(
                self.products.clone().ok_or("native products unavailable")?,
                credentials,
                capability,
                repository,
            )
    }
    pub fn stop(mut self) -> Result<()> {
        self.stop_inner()
    }
    pub(crate) fn github_client(&self) -> Result<crate::github_provider::Client> {
        if !self.alive.load(Ordering::SeqCst) {
            return Err("native runtime is retired".into());
        }
        self.github
            .clone()
            .ok_or("native GitHub owner is unavailable".into())
    }
    pub(crate) fn github_channel(
        &self,
    ) -> Result<(
        tokio::sync::oneshot::Receiver<Result<NativeChannel>>,
        Arc<AtomicBool>,
    )> {
        if !self.alive.load(Ordering::SeqCst) {
            return Err("native runtime is retired".into());
        }
        let (send, receive) = tokio::sync::oneshot::channel();
        self.control
            .send(Control::GithubChannel(send))
            .map_err(|_| "native runtime is unavailable")?;
        Ok((receive, self.alive.clone()))
    }
    fn stop_inner(&mut self) -> Result<()> {
        let Some(owner) = self.owner.take() else {
            return Ok(());
        };
        self.alive.store(false, Ordering::SeqCst);
        if let Some(collaboration) = &self.collaboration {
            collaboration.retire();
        }
        if let Some(github) = &self.github {
            github.retire();
        }
        if let Some(products) = &self.products {
            products.retire();
        }
        let _ = self.control.send(Control::Stop);
        owner.join().map_err(|_| "native supervisor owner failed")?
    }
}
impl Drop for NativeSupervisor {
    fn drop(&mut self) {
        if let Err(error) = self.stop_inner() {
            eprintln!("native supervisor retirement: {error}");
        }
    }
}
fn run_owner(
    children: &mut Children,
    products: &crate::product::Owner,
    github: &crate::github_provider::Owner,
    collaboration: &crate::collaboration::Owner,
    commands: Receiver<Control>,
) -> Option<String> {
    loop {
        match commands.recv_timeout(Duration::from_millis(100)) {
            Ok(Control::Stop) | Err(mpsc::RecvTimeoutError::Disconnected) => return None,
            Ok(Control::Probe(reply)) => {
                let result = children.health();
                let failed = result.as_ref().err().cloned();
                let _ = reply.send(result);
                if failed.is_some() {
                    return failed;
                }
            }
            Ok(Control::GithubChannel(reply)) => {
                let result = children.health().map(|ready| NativeChannel {
                    socket: children.profile.join("backend.sock"),
                    key: zeroize::Zeroizing::new(children.key.clone()),
                    instance: ready.instance,
                    profile: ready.profile,
                });
                let _ = reply.send(result);
            }
            Err(mpsc::RecvTimeoutError::Timeout) => (),
        }
        if let Err(error) = children.check_children() {
            return Some(error);
        }
        if let Err(error) = products.check_running() {
            return Some(error);
        }
        if let Err(error) = collaboration.check_running() {
            return Some(error);
        }
        if let Err(error) = github.check_running() {
            return Some(error.to_string());
        }
    }
}
struct Children {
    database: Option<NativePostgres>,
    backend: Option<Child>,
    profile: PathBuf,
    identity: String,
    key: String,
    instance: Option<String>,
    _lock: File,
    broker: bool,
}
impl Children {
    fn start(payload: &Path, backend: &Path, profile: &Path) -> Result<Self> {
        let backend =
            fs::canonicalize(backend).map_err(|_| "native backend executable is unavailable")?;
        let metadata =
            fs::metadata(&backend).map_err(|_| "native backend executable is unavailable")?;
        if !metadata.is_file() || metadata.permissions().mode() & 0o111 == 0 {
            return Err("native backend executable is unavailable".into());
        }
        private_dir(profile).map_err(|error| error.to_string())?;
        let profile = fs::canonicalize(profile).map_err(|_| "native profile is unavailable")?;
        let lock = private_file(&profile.join("supervisor.lock"), false)
            .map_err(|_| "native supervisor lock is unavailable")?;
        lock.try_lock()
            .map_err(|_| "another native supervisor owns this profile")?;
        let mut database =
            NativePostgres::start(payload, &profile).map_err(|error| error.to_string())?;
        let config = database
            .backend_config()
            .map_err(|error| error.to_string())?;
        let identity = read_private(&profile.join("profile.id"), 32)?;
        // The supervisor lock excludes another live owner of this profile.
        // Inspect installation before starting Go. Exact recovery follows
        // Store readiness, before product/provider startup is published.
        let broker = match fs::symlink_metadata("/run/soda-factory/broker.sock") {
            Ok(_) => true,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                for unit in [
                    "/etc/systemd/system/soda-factory-broker.socket",
                    "/run/systemd/system/soda-factory-broker.socket",
                    "/usr/lib/systemd/system/soda-factory-broker.socket",
                ] {
                    match fs::symlink_metadata(unit) {
                        Ok(_) => return Err("installed native broker socket unavailable".into()),
                        Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
                        Err(_) => {
                            return Err("native broker installation inspection unavailable".into())
                        }
                    }
                }
                false
            }
            Err(_) => return Err("native broker inspection unavailable".into()),
        };
        let key = read_private(&profile.join("backend.key"), 64)?;
        let log = private_file(&profile.join("backend.log"), false)
            .map_err(|_| "native backend log is unavailable")?;
        let mut command = Command::new(backend);
        command
            .arg("--config")
            .arg(config)
            .env_clear()
            .env("PATH", "/usr/bin:/bin")
            .env("LANG", "C.UTF-8")
            .env("TMPDIR", profile.join("tmp"))
            .stdin(Stdio::null())
            .stdout(
                log.try_clone()
                    .map_err(|_| "native backend log is unavailable")?,
            )
            .stderr(log)
            .process_group(0);
        parent_death(&mut command, libc::SIGTERM);
        let child = command
            .spawn()
            .map_err(|_| "native backend could not start")?;
        let mut children = Self {
            database: Some(database),
            backend: Some(child),
            profile,
            identity,
            key,
            instance: None,
            _lock: lock,
            broker,
        };
        let deadline = Instant::now() + START_TIMEOUT;
        loop {
            children.check_children()?;
            if children.health().is_ok() {
                return Ok(children);
            }
            if Instant::now() >= deadline {
                return Err("native backend readiness timed out; inspect backend.log".into());
            }
            thread::sleep(Duration::from_millis(25));
        }
    }
    fn check_children(&mut self) -> Result<()> {
        self.database
            .as_mut()
            .ok_or("native database is stopped")?
            .check_running()
            .map_err(|_| "owned native database exited")?;
        if self
            .backend
            .as_mut()
            .ok_or("native backend is stopped")?
            .try_wait()
            .map_err(|_| "native backend observation failed")?
            .is_some()
        {
            return Err("owned native backend exited; inspect backend.log".into());
        }
        Ok(())
    }
    fn health(&mut self) -> Result<NativeReady> {
        self.check_children()?;
        let response = request_authenticated(
            &self.profile.join("backend.sock"),
            "GET",
            "/healthz",
            "soda-native",
            &[],
            &self.key,
            Duration::from_secs(3),
            Limits {
                header_bytes: 8192,
                body_bytes: 4096,
            },
        )
        .map_err(|_| "native readiness could not be read")?;
        if response.status != 200 {
            return Err("native readiness was refused".into());
        }
        let value: serde_json::Value =
            serde_json::from_slice(&response.body).map_err(|_| "native readiness is invalid")?;
        let row = value.as_object().ok_or("native readiness is invalid")?;
        let instance = row
            .get("instance")
            .and_then(|value| value.as_str())
            .ok_or("native readiness lacks instance")?;
        if row.len() != 3
            || row.get("status").and_then(|value| value.as_str()) != Some("ready")
            || row.get("profile").and_then(|value| value.as_str()) != Some(&self.identity)
            || !hex(instance, 32)
            || self
                .instance
                .as_ref()
                .is_some_and(|previous| previous != instance)
        {
            return Err("native readiness identity changed".into());
        }
        self.instance = Some(instance.into());
        Ok(NativeReady {
            profile: self.identity.clone(),
            instance: instance.into(),
            backend_process: self.backend.as_ref().unwrap().id(),
            database_process: self.database.as_ref().unwrap().process_id(),
        })
    }
    fn stop(&mut self) -> Result<()> {
        // Stop Go admission/drain before stopping PostgreSQL. Failure still retires
        // both owned children and is never reported as a clean shutdown.
        let backend = match self.backend.take() {
            Some(mut child) => stop_backend(&mut child),
            None => Ok(()),
        };
        let database = match self.database.take() {
            Some(database) => database.stop().map_err(|error| error.to_string()),
            None => Ok(()),
        };
        backend.and(database)
    }
}
impl Drop for Children {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}
fn read_private(path: &Path, length: usize) -> Result<String> {
    let file = private_file(path, false).map_err(|_| "native private input is unavailable")?;
    let mut value = String::new();
    file.take(length as u64 + 1)
        .read_to_string(&mut value)
        .map_err(|_| "native private input is invalid")?;
    if !hex(&value, length) {
        return Err("native private input is invalid".into());
    }
    Ok(value)
}
fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}
fn stop_backend(child: &mut Child) -> Result<()> {
    if child
        .try_wait()
        .map_err(|_| "native backend observation failed")?
        .is_some()
    {
        return Err("native backend exited before shutdown".into());
    }
    if unsafe { libc::kill(child.id() as i32, libc::SIGTERM) } != 0 {
        let _ = child.kill();
        let _ = child.wait();
        return Err("native backend retirement signal failed".into());
    }
    let deadline = Instant::now() + STOP_TIMEOUT;
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                return if status.success() {
                    Ok(())
                } else {
                    Err("native backend shutdown failed".into())
                }
            }
            Ok(None) => (),
            Err(_) => return Err("native backend observation failed".into()),
        }
        if Instant::now() >= deadline {
            unsafe {
                libc::kill(-(child.id() as i32), libc::SIGKILL);
            }
            let _ = child.wait();
            return Err("native backend shutdown timed out and was killed".into());
        }
        thread::sleep(Duration::from_millis(25));
    }
}

/// Prepare the fixed native profile before a webview can use its own data dir.
pub fn prepare_profile(profile: &Path) -> Result<()> {
    use std::os::unix::fs::DirBuilderExt;
    let mut dirs = std::fs::DirBuilder::new();
    dirs.recursive(true)
        .mode(0o700)
        .create(profile)
        .map_err(|_| "native profile directory is unavailable")?;
    private_dir(profile).map_err(|error| error.to_string())
}
