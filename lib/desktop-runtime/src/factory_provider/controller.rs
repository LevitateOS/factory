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
        monitor: std::thread::JoinHandle<()>,
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
    stop: Arc<Notify>,
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
            stop: Arc::new(Notify::new()),
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
            stop: Arc::new(Notify::new()),
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
        let (client, grant, deadline, alive, stop) = (
            self.client.clone(),
            self.grant.clone(),
            self.request.not_after,
            self.alive.clone(),
            self.stop.clone(),
        );
        // The watcher has its own executor so native retirement can join it
        // from either a blocking owner or an async desktop inspection caller.
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .inspect_err(|_| cancellation.cancel())?;
        let monitor = std::thread::Builder::new().name("factory-controller-watch".into()).spawn(move || runtime.block_on(async move {
            loop {
                let remaining = time::OffsetDateTime::from_unix_timestamp(deadline)
                    .map(|expires| (expires-time::OffsetDateTime::now_utc()).whole_milliseconds()).unwrap_or(0);
                if remaining <= 0 { cancellation.cancel(); return; }
                let current = tokio::select! {
                    _ = stop.notified() => return,
                    _ = tokio::time::sleep(Duration::from_millis(remaining as u64)) => { cancellation.cancel(); return; },
                    value = client.operation_grant(&grant.id) => value,
                };
                if !alive.load(Ordering::SeqCst) || deadline <= time::OffsetDateTime::now_utc().unix_timestamp() || !current.is_ok_and(|current| current.allows_current("execution.launch") && same_grant(&current, &grant)) {
                    cancellation.cancel();
                    return;
                }
                tokio::select! { _ = stop.notified() => return, _ = tokio::time::sleep(Duration::from_secs(1)) => {} }
            }
        })).inspect_err(|_| cancel_handle.cancel())?;
        self.phase = Phase::Entered {
            cancellation: cancel_handle,
            monitor,
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
                monitor,
            } => {
                cancellation.cancel();
                self.stop.notify_one();
                // The worker has joined before this lease. The watcher never
                // depends on the caller's executor to acknowledge retirement.
                let _ = monitor.join();
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
