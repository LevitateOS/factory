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

pub(super) struct Lease {
    client: Arc<Client>,
    grant: OperationGrant,
    request: ExecutionClaimRequest,
    profile: PathBuf,
    intent: LaunchIntent,
    alive: Arc<AtomicBool>,
    entered: bool,
    cancellation: Option<AgentCancellation>,
    stop: Arc<Notify>,
    monitor: Option<std::thread::JoinHandle<()>>,
    runtime: Handle,
    channel: NativeChannel,
    admission: zeroize::Zeroizing<Vec<u8>>,
}
impl Lease {
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
        if !claim.acquired || claim.state != "claimed" {
            return Err(unavailable());
        }
        let lease = Self {
            client,
            grant,
            request,
            profile,
            intent: intent.clone(),
            alive,
            entered: false,
            cancellation: None,
            stop: Arc::new(Notify::new()),
            monitor: None,
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
        let payload = zeroize::Zeroizing::new(
            serde_json::to_vec(&serde_json::json!({
                "request": request, "origin": self.client.origin(),
                "actor": current.actor.id, "repository": current.repository.id,
            }))
            .map_err(|_| unavailable())?,
        );
        let response = self
            .runtime
            .block_on(soda_unix_http::request_authenticated_async(
                &self.channel.socket,
                "POST",
                "/api/native/execution/launch-admission",
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
                != Some(self.intent.digest().as_str())
            || cancellation.cancelled()
            || !self.alive.load(Ordering::SeqCst)
            || self.request.not_after <= time::OffsetDateTime::now_utc().unix_timestamp()
        {
            return Err(unavailable());
        }
        self.cancellation = Some(cancellation.clone());
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
            .build()?;
        self.monitor = Some(std::thread::Builder::new().name("factory-controller-watch".into()).spawn(move || runtime.block_on(async move {
            loop {
                let current = tokio::select! { _ = stop.notified() => return, value = client.operation_grant(&grant.id) => value };
                if !alive.load(Ordering::SeqCst) || deadline <= time::OffsetDateTime::now_utc().unix_timestamp() || !current.is_ok_and(|current| current.allows_current("execution.launch") && same_grant(&current, &grant)) {
                    cancellation.cancel();
                    return;
                }
                tokio::select! { _ = stop.notified() => return, _ = tokio::time::sleep(Duration::from_secs(1)) => {} }
            }
        }))?);
        self.entered = true;
        Ok(())
    }
}
impl Drop for Lease {
    fn drop(&mut self) {
        if let Some(cancel) = &self.cancellation {
            cancel.cancel();
        }
        if let Some(monitor) = self.monitor.take() {
            self.stop.notify_one();
            // The worker has joined before this lease. The watcher never
            // depends on the caller's executor to acknowledge retirement.
            let _ = monitor.join();
        }
        if !self.entered {
            if let Ok(bytes) = no_start_bytes(&self.intent) {
                let _ = publish_bytes(&no_start_path(&self.profile, &self.intent), &bytes);
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
        Some(("not_started", digest(&bytes)))
    } else if let Some(run) = super::muse_receipt::retired(profile, intent)? {
        if !run.root_retired || !run.process.tree_retired || !run.process.finished {
            return Ok(());
        }
        let bytes = serde_json::to_vec(&serde_json::json!({"kind":"factory_controller_retirement","version":1,"intent_sha256":intent.digest(),"process":run.process})).map_err(io::Error::other)?;
        Some(("retired", digest(&bytes)))
    } else {
        None
    };
    if let Some((state, receipt_digest)) = retirement {
        let (client, grant, request) = captured(credentials, profile, intent).await?;
        client
            .retire_execution(
                profile,
                &grant,
                &request,
                &ExecutionRetirement {
                    manifest_digest: request.manifest_digest.clone(),
                    state: state.into(),
                    receipt_digest,
                },
            )
            .await
            .map_err(|_| unavailable())?;
    }
    Ok(())
}
