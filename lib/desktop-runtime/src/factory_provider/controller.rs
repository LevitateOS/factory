//! Host authority surrounds native custody; it is never inference funding.
//! Original claim replies alone admit a worker. Recovery only retires claims
//! from exact no-start evidence or the existing physical retirement receipt.
use super::{AgentCancellation, LaunchIntent};
use crate::credentials::Credentials;
use crate::fountain_provider::{
    captured_operation_grant, Client, ExecutionClaimRequest, ExecutionRetirement, OperationGrant,
};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use crate::supervisor::NativeChannel;
use sha2::{Digest, Sha256};
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::time::Duration;
use tokio::{runtime::Handle, sync::Notify};

#[derive(Default)]
pub struct GrantWatches(std::sync::Mutex<Vec<std::sync::Weak<GrantWatch>>>);
struct WatchMember {
    cancellation: AgentCancellation,
    deadline: i64,
    alive: Arc<AtomicBool>,
}
struct WatchState {
    members: std::sync::Mutex<std::collections::BTreeMap<u64, WatchMember>>,
    next: std::sync::atomic::AtomicU64,
    changed: Notify,
    stopped: AtomicBool,
}
struct GrantWatch {
    client: Arc<Client>,
    grant: OperationGrant,
    state: Arc<WatchState>,
    monitor: std::sync::Mutex<Option<std::thread::JoinHandle<()>>>,
}
pub struct GrantWatchLease {
    watch: Arc<GrantWatch>,
    id: u64,
}
impl GrantWatches {
    /// Observe liveness only. This never admits a claim, launch, send or funding.
    pub fn observe_execution(
        &self,
        client: Arc<Client>,
        grant: OperationGrant,
        deadline: i64,
        cancellation: AgentCancellation,
        alive: Arc<AtomicBool>,
    ) -> io::Result<GrantWatchLease> {
        if !grant.allows_current("execution.launch")
            || deadline <= time::OffsetDateTime::now_utc().unix_timestamp()
            || deadline > grant.not_after
            || cancellation.cancelled()
            || !alive.load(Ordering::SeqCst)
        {
            return Err(unavailable());
        }
        self.attach(
            client,
            grant,
            WatchMember {
                deadline,
                cancellation,
                alive,
            },
        )
    }
    fn attach(
        &self,
        client: Arc<Client>,
        grant: OperationGrant,
        member: WatchMember,
    ) -> io::Result<GrantWatchLease> {
        let mut watches = self.0.lock().map_err(|_| unavailable())?;
        watches.retain(|watch| watch.strong_count() > 0);
        for weak in watches.iter() {
            if let Some(watch) = weak.upgrade() {
                if watch.client.same_custody(&client) && same_grant(&watch.grant, &grant) {
                    let mut members = watch.state.members.lock().map_err(|_| unavailable())?;
                    if !watch.state.stopped.load(Ordering::SeqCst) {
                        let id = watch.state.next.fetch_add(1, Ordering::SeqCst);
                        if id == 0 {
                            return Err(unavailable());
                        }
                        members.insert(id, member);
                        drop(members);
                        watch.state.changed.notify_one();
                        return Ok(GrantWatchLease { watch, id });
                    }
                }
            }
        }
        let state = Arc::new(WatchState {
            members: std::sync::Mutex::new([(1, member)].into()),
            next: std::sync::atomic::AtomicU64::new(2),
            changed: Notify::new(),
            stopped: AtomicBool::new(false),
        });
        let watch = Arc::new(GrantWatch {
            client: client.clone(),
            grant: grant.clone(),
            state: state.clone(),
            monitor: std::sync::Mutex::new(None),
        });
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?;
        let monitor = std::thread::Builder::new()
            .name("factory-controller-watch".into())
            .spawn(move || runtime.block_on(monitor_grant(client, grant, state)))?;
        *watch.monitor.lock().map_err(|_| unavailable())? = Some(monitor);
        watches.push(Arc::downgrade(&watch));
        Ok(GrantWatchLease { watch, id: 1 })
    }
}
impl WatchState {
    fn remaining(&self) -> Option<Duration> {
        let mut members = self.members.lock().unwrap_or_else(|e| e.into_inner());
        let now = time::OffsetDateTime::now_utc();
        members.retain(|_, member| {
            let live = member.alive.load(Ordering::SeqCst)
                && member.deadline > now.unix_timestamp()
                && !member.cancellation.cancelled();
            if !live {
                member.cancellation.cancel();
            }
            live
        });
        if members.is_empty() || self.stopped.load(Ordering::SeqCst) {
            self.stopped.store(true, Ordering::SeqCst);
            return None;
        }
        members
            .values()
            .map(|m| {
                Duration::from_millis(
                    time::OffsetDateTime::from_unix_timestamp(m.deadline)
                        .map(|expiry| (expiry - now).whole_milliseconds().max(0) as u64)
                        .unwrap_or(0),
                )
            })
            .min()
    }
    fn cancel_all(&self) {
        let mut members = self.members.lock().unwrap_or_else(|e| e.into_inner());
        self.stopped.store(true, Ordering::SeqCst);
        for member in members.values() {
            member.cancellation.cancel();
        }
        members.clear();
    }
}
async fn monitor_grant(client: Arc<Client>, grant: OperationGrant, state: Arc<WatchState>) {
    loop {
        if state.remaining().is_none() {
            return;
        }
        let read = client.operation_grant(&grant.id);
        tokio::pin!(read);
        let current = loop {
            let Some(remaining) = state.remaining() else {
                return;
            };
            tokio::select! {
             value=&mut read=>break value,
             _=state.changed.notified()=>{},
             _=tokio::time::sleep(remaining)=>{},
            }
        };
        if !current.is_ok_and(|current| {
            current.allows_current("execution.launch") && same_grant(&current, &grant)
        }) {
            state.cancel_all();
            return;
        }
        let pause = tokio::time::sleep(Duration::from_secs(1));
        tokio::pin!(pause);
        loop {
            let Some(remaining) = state.remaining() else {
                return;
            };
            tokio::select! {_= &mut pause=>break,_=state.changed.notified()=>{},_=tokio::time::sleep(remaining)=>{}}
        }
    }
}
impl Drop for GrantWatchLease {
    fn drop(&mut self) {
        let last = {
            let mut members = self
                .watch
                .state
                .members
                .lock()
                .unwrap_or_else(|e| e.into_inner());
            members.remove(&self.id);
            let last = members.is_empty();
            if last {
                self.watch.state.stopped.store(true, Ordering::SeqCst);
            }
            last
        };
        self.watch.state.changed.notify_one();
        if last {
            if let Some(monitor) = self
                .watch
                .monitor
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .take()
            {
                let _ = monitor.join();
            }
        }
    }
}

type RetireFn = fn(String, String) -> ExecutionRetirement;
fn unavailable() -> io::Error {
    io::Error::other("captured Fountain execution claim unavailable")
}
fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn same_grant(a: &OperationGrant, b: &OperationGrant) -> bool {
    a.id == b.id
        && a.enrollment_id == b.enrollment_id
        && a.runtime_id == b.runtime_id
        && a.installation_id == b.installation_id
        && a.generation == b.generation
        && a.actor.id == b.actor.id
        && a.repository.id == b.repository.id
        && a.operations == b.operations
        && a.not_after == b.not_after
}
fn no_start_path(profile: &Path, intent: &LaunchIntent) -> PathBuf {
    profile
        .join("native-run-intents")
        .join(format!("{}.controller-not-started", intent.run()))
}
fn no_start_bytes(intent: &LaunchIntent) -> io::Result<Vec<u8>> {
    serde_json::to_vec(&serde_json::json!({"kind":"factory_controller_not_started","version":1,"intent_sha256":intent.digest()})).map_err(io::Error::other)
}
async fn captured(
    credentials: Arc<Credentials>,
    profile: &Path,
    intent: &LaunchIntent,
) -> io::Result<(Arc<Client>, OperationGrant, ExecutionClaimRequest)> {
    let binding = intent.controller().ok_or_else(unavailable)?;
    let (client, grant) = captured_operation_grant(
        credentials,
        profile,
        intent.scope()?.profile,
        &binding.connection,
        &binding.grant_id,
    )
    .await
    .map_err(|_| unavailable())?;
    let request = ExecutionClaimRequest {
        command_id: intent.run().into(),
        manifest_digest: binding.manifest_digest.clone(),
        not_after: intent.deadline_unix()?.min(grant.not_after),
    };
    Ok((Arc::new(client), grant, request))
}

/// Admitted: constructed but not yet entered, so Drop records no-start.
/// Entered: the worker owns custody and the watcher runs.
enum Phase {
    Admitted,
    Entered {
        cancellation: AgentCancellation,
        watch: GrantWatchLease,
    },
}
pub(super) struct Lease {
    client: Arc<Client>,
    grant: OperationGrant,
    request: ExecutionClaimRequest,
    intent_digest: String,
    admission_route: &'static str,
    admission_kind: Option<&'static str>,
    no_start: PathBuf,
    no_start_receipt: Vec<u8>,
    alive: Arc<AtomicBool>,
    phase: Phase,
    runtime: Handle,
    channel: NativeChannel,
    admission: zeroize::Zeroizing<Vec<u8>>,
}
impl Lease {
    pub(super) async fn admit_preparation(
        credentials: Arc<Credentials>,
        profile: PathBuf,
        request: &serde_json::Value,
        key: &str,
        alive: Arc<AtomicBool>,
        channel: NativeChannel,
    ) -> io::Result<Self> {
        let (client, grant, claim, bytes) =
            preparation_capture(credentials, &profile, request, key).await?;
        if !alive.load(Ordering::SeqCst)
            || !grant.allows_current("execution.launch")
            || claim.not_after <= time::OffsetDateTime::now_utc().unix_timestamp()
        {
            return Err(unavailable());
        }
        let root = profile.join("native-preparations").join(key);
        let no_start = root.join("controller-not-started.json");
        let no_start_receipt = preparation_no_start(&claim.manifest_digest)?;
        let acquired = client.claim_execution(&profile, &grant, &claim).await;
        if !acquired.is_ok_and(|value| value.acquired()) {
            // This owner has not entered a constructor, even if the claim's
            // original reply was lost. Absence is never dispatch authority.
            publish_bytes(&no_start, &no_start_receipt)?;
            return Err(unavailable());
        }
        Ok(Self {
            client: Arc::new(client),
            grant,
            request: claim.clone(),
            intent_digest: claim.manifest_digest,
            admission_route: "/api/native/preparation/admission",
            admission_kind: Some(
                if request
                    .get("input")
                    .is_some_and(|input| input.get("source_preparation").is_some())
                {
                    "candidate"
                } else {
                    "standing"
                },
            ),
            no_start,
            no_start_receipt,
            alive,
            phase: Phase::Admitted,
            runtime: Handle::current(),
            channel,
            admission: zeroize::Zeroizing::new(bytes),
        })
    }
    pub(super) async fn admit(
        credentials: Arc<Credentials>,
        profile: PathBuf,
        intent: &LaunchIntent,
        alive: Arc<AtomicBool>,
        channel: NativeChannel,
        admission: serde_json::Value,
    ) -> io::Result<Self> {
        let admission =
            zeroize::Zeroizing::new(serde_json::to_vec(&admission).map_err(|_| unavailable())?);
        if admission.len() > 2 * 1024 * 1024 {
            return Err(unavailable());
        }
        let (client, grant, request) = captured(credentials, &profile, intent).await?;
        if !alive.load(Ordering::SeqCst)
            || !grant.allows_current("execution.launch")
            || request.not_after <= time::OffsetDateTime::now_utc().unix_timestamp()
        {
            return Err(unavailable());
        }
        // Fence this original claim without consuming the worker constructor's
        // separate intent. An uncertain claim reply cannot authorize a retry.
        let root = profile.join("native-run-intents");
        private_dir(&root).map_err(|_| unavailable())?;
        publish_bytes(
            &root.join(format!("{}.controller", intent.run())),
            intent.raw().as_bytes(),
        )?;
        let claim = client
            .claim_execution(&profile, &grant, &request)
            .await
            .map_err(|_| unavailable())?;
        if !claim.acquired() {
            return Err(unavailable());
        }
        let lease = Self {
            client,
            grant,
            request,
            intent_digest: intent.digest(),
            admission_route: "/api/native/execution/launch-admission",
            admission_kind: None,
            no_start: no_start_path(&profile, intent),
            no_start_receipt: no_start_bytes(intent)?,
            alive,
            phase: Phase::Admitted,
            runtime: Handle::current(),
            channel,
            admission,
        };
        let current = lease
            .client
            .operation_grant(&lease.grant.id)
            .await
            .map_err(|_| unavailable())?;
        if !lease.alive.load(Ordering::SeqCst)
            || !current.allows_current("execution.launch")
            || !same_grant(&current, &lease.grant)
            || lease.request.not_after <= time::OffsetDateTime::now_utc().unix_timestamp()
        {
            return Err(unavailable()); // Drop records definitive pre-constructor no-start.
        }
        Ok(lease)
    }
    // Called only inside the execution owner's blocking preparation thread.
    pub(super) fn enter(&mut self, cancellation: AgentCancellation) -> io::Result<()> {
        let current = self
            .runtime
            .block_on(self.client.operation_grant(&self.grant.id))
            .map_err(|_| unavailable())?;
        if cancellation.cancelled()
            || !self.alive.load(Ordering::SeqCst)
            || self.request.not_after <= time::OffsetDateTime::now_utc().unix_timestamp()
            || !current.allows_current("execution.launch")
            || !same_grant(&current, &self.grant)
        {
            return Err(unavailable());
        }
        let request: serde_json::Value =
            serde_json::from_slice(&self.admission).map_err(|_| unavailable())?;
        let mut payload = serde_json::json!({
            "request": request, "origin": self.client.origin(),
            "actor": current.actor.id, "repository": current.repository.id,
        });
        if let Some(kind) = self.admission_kind {
            payload["kind"] = serde_json::json!(kind);
        }
        let payload =
            zeroize::Zeroizing::new(serde_json::to_vec(&payload).map_err(|_| unavailable())?);
        let response = self
            .runtime
            .block_on(soda_unix_http::request_authenticated_async(
                &self.channel.socket,
                "POST",
                self.admission_route,
                "soda-native",
                &payload,
                &self.channel.key,
                &[("X-Soda-Native-Instance", self.channel.instance.as_str())],
                Duration::from_secs(20),
                soda_unix_http::Limits {
                    header_bytes: 8192,
                    body_bytes: 1024,
                },
            ))
            .map_err(|_| unavailable())?;
        let binding: serde_json::Value =
            serde_json::from_slice(&response.body).map_err(|_| unavailable())?;
        if response.status != 200
            || binding
                .get("intent_digest")
                .and_then(|value| value.as_str())
                != Some(self.intent_digest.as_str())
            || cancellation.cancelled()
            || !self.alive.load(Ordering::SeqCst)
            || self.request.not_after <= time::OffsetDateTime::now_utc().unix_timestamp()
        {
            return Err(unavailable());
        }
        let cancel_handle = cancellation.clone();
        let watch=self.channel.controller_watches.observe_execution(self.client.clone(),self.grant.clone(),self.request.not_after,cancellation,self.alive.clone()).inspect_err(|_|cancel_handle.cancel())?;
        self.phase = Phase::Entered {
            cancellation: cancel_handle,
            watch,
        };
        Ok(())
    }
}

fn preparation_no_start(digest: &str) -> io::Result<Vec<u8>> {
    serde_json::to_vec(&serde_json::json!({"kind":"factory_controller_preparation_not_started","version":1,"intent_sha256":digest})).map_err(io::Error::other)
}
async fn preparation_capture(
    credentials: Arc<Credentials>,
    profile: &Path,
    request: &serde_json::Value,
    key: &str,
) -> io::Result<(Client, OperationGrant, ExecutionClaimRequest, Vec<u8>)> {
    let binding = &request["controller"];
    let field = |name| binding[name].as_str().ok_or_else(unavailable);
    let runtime = request["native"]["profile"]
        .as_str()
        .ok_or_else(unavailable)?;
    let (client, grant) = captured_operation_grant(
        credentials,
        profile,
        runtime,
        field("connection")?,
        field("grant_id")?,
    )
    .await
    .map_err(|error| {
        io::Error::other(format!(
            "Fountain captured preparation grant: {}",
            error.code
        ))
    })?;
    if !super::hex(key, 32) || binding["not_after"].as_i64() != Some(grant.not_after) {
        return Err(unavailable());
    }
    let mut not_after = grant.not_after;
    if let Some(deadline) = request["input"].get("deadline") {
        let deadline = time::OffsetDateTime::parse(
            deadline.as_str().ok_or_else(unavailable)?,
            &time::format_description::well_known::Rfc3339,
        )
        .map_err(|_| unavailable())?;
        not_after = not_after.min(deadline.unix_timestamp());
    }
    let bytes = serde_json::to_vec(request).map_err(io::Error::other)?;
    let claim = ExecutionClaimRequest {
        command_id: key.into(),
        manifest_digest: digest(&bytes),
        not_after,
    };
    Ok((client, grant, claim, bytes))
}

// Physical retirement/no-start evidence is required even after expiry. This
// uses only the original journal: lookup absence never sends another claim.
pub(super) async fn recover_preparation(
    credentials: Arc<Credentials>,
    profile: &Path,
    request: &serde_json::Value,
    key: &str,
    retired: Option<&[u8]>,
) -> io::Result<()> {
    if request
        .get("controller")
        .is_none_or(serde_json::Value::is_null)
    {
        return Ok(());
    }
    let (client, grant, claim, _) = preparation_capture(credentials, profile, request, key).await?;
    let marker = profile
        .join("native-preparations")
        .join(key)
        .join("controller-not-started.json");
    let (retire, receipt) = match read_bytes(&marker, 1024) {
        Ok(bytes) if bytes == preparation_no_start(&claim.manifest_digest)? => {
            (ExecutionRetirement::not_started as RetireFn, bytes)
        }
        Ok(_) => return Err(unavailable()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            let Some(retired) = retired else {
                return Ok(());
            };
            (ExecutionRetirement::retired as RetireFn,serde_json::to_vec(&serde_json::json!({"kind":"factory_controller_preparation_retired","version":1,"intent_sha256":claim.manifest_digest,"retirement_sha256":digest(retired)})).map_err(io::Error::other)?)
        }
        Err(error) => return Err(error),
    };
    client
        .retire_execution(
            profile,
            &grant,
            &claim,
            &retire(claim.manifest_digest.clone(), digest(&receipt)),
        )
        .await
        .map_err(|error| {
            io::Error::other(format!("Fountain preparation retirement: {}", error.code))
        })?;
    Ok(())
}
impl Drop for Lease {
    fn drop(&mut self) {
        match std::mem::replace(&mut self.phase, Phase::Admitted) {
            Phase::Entered {
                cancellation,
                watch,
            } => {
                cancellation.cancel();
                drop(watch);
            }
            Phase::Admitted => {
                let _ = publish_bytes(&self.no_start, &self.no_start_receipt);
            }
        }
    }
}

pub(super) async fn recover(
    credentials: Arc<Credentials>,
    profile: &Path,
    intent: &LaunchIntent,
) -> io::Result<()> {
    if intent.controller().is_none() {
        return Ok(());
    }
    let capture = profile
        .join("native-run-intents")
        .join(format!("{}.controller", intent.run()));
    match read_bytes(&capture, 16384) {
        Ok(bytes) if bytes == intent.raw().as_bytes() => (),
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        _ => return Err(unavailable()),
    }
    let no_start = match read_bytes(&no_start_path(profile, intent), 1024) {
        Ok(bytes) if bytes == no_start_bytes(intent)? => Some(bytes),
        Ok(_) => return Err(unavailable()),
        Err(e) if e.kind() == io::ErrorKind::NotFound => None,
        Err(e) => return Err(e),
    };
    let retirement = if let Some(bytes) = no_start {
        Some((ExecutionRetirement::not_started as RetireFn, digest(&bytes)))
    } else if let Some(run) = super::muse_receipt::retired(profile, intent)? {
        if !run.root_retired || !run.process.tree_retired || !run.process.finished {
            return Ok(());
        }
        let bytes = serde_json::to_vec(&serde_json::json!({"kind":"factory_controller_retirement","version":1,"intent_sha256":intent.digest(),"process":run.process})).map_err(io::Error::other)?;
        Some((ExecutionRetirement::retired as RetireFn, digest(&bytes)))
    } else {
        None
    };
    if let Some((retire, receipt_digest)) = retirement {
        let (client, grant, request) = captured(credentials, profile, intent).await?;
        client
            .retire_execution(
                profile,
                &grant,
                &request,
                &retire(request.manifest_digest.clone(), receipt_digest),
            )
            .await
            .map_err(|_| unavailable())?;
    }
    Ok(())
}
