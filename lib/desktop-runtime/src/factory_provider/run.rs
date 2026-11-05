//! One native-only owner for an exact isolated run. Product admission is still
//! supplied by the future Go execution port; this module cannot create it.
use super::receipt::ensure_receipts;
use super::{
    write_retained, AgentCancellation, AgentObserver, AgentRun, AgentStarted, BoundOwner, Broker,
    LaunchIntent, Owner, Policy, ReceiptScope, Role, RunCustody, WorkerMcp,
};
use crate::credentials::{Credentials, TokenCapability};
use crate::database::private_dir;
use sha2::{Digest, Sha256};
use std::fs::OpenOptions;
use std::io::{self, Read};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

struct Staged {
    run: String,
    profile: String,
    sha256: String,
    active: bool,
}

impl Staged {
    fn abort(&mut self) -> io::Result<()> {
        if self.active {
            Broker::retire_unlaunched(&self.run, &self.profile, &self.sha256)?;
            self.active = false;
        }
        Ok(())
    }
}

impl Drop for Staged {
    fn drop(&mut self) {
        let _ = self.abort();
    }
}

/// Owns the credential-bearing relay and one broker-consumed agent attempt.
/// Construct only from a current, captured Go native admission. The first
/// broker operation still requires the privileged installed system services.
pub struct RunOwner {
    owner: Owner,
    intent_digest: Option<String>,
    cancellation: AgentCancellation,
    observer: AgentObserver,
    custody: RunCustody,
    run: String,
    profile: String,
    sha256: String,
    candidate: String,
    role: Role,
    tool_sha256: String,
    prompt_sha256: String,
    deadline: Instant,
    profile_dir: PathBuf,
    attempted: bool,
    receipt_persisted: bool,
    retired: bool,
}

impl RunOwner {
    /// Bind a private credential owner before staging. The relay and worker
    /// are attested while the owner is still inert, then exact attestation
    /// activates token use. Any failure after staging tries exact retirement.
    pub fn start(
        policy: Policy,
        profile_dir: &Path,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        bundle: &[u8],
        tool_sha256: &str,
        prompt_sha256: &str,
    ) -> io::Result<Self> {
        Self::start_inner(
            policy,
            profile_dir,
            credentials,
            capability,
            bundle,
            tool_sha256,
            prompt_sha256,
            None,
            AgentCancellation::default(),
            None,
        )
    }

    /// Bind the exact-run Go MCP port before the relay or worker can start.
    pub fn start_with_mcp(
        policy: Policy,
        profile_dir: &Path,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        bundle: &[u8],
        tool_sha256: &str,
        prompt_sha256: &str,
        mcp: WorkerMcp,
    ) -> io::Result<Self> {
        Self::start_inner(
            policy,
            profile_dir,
            credentials,
            capability,
            bundle,
            tool_sha256,
            prompt_sha256,
            Some(mcp),
            AgentCancellation::default(),
            None,
        )
    }

    /// The lifecycle owns this signal before any preparation RPC starts.
    pub fn start_cancellable(
        policy: Policy,
        profile_dir: &Path,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        bundle: &[u8],
        tool_sha256: &str,
        prompt_sha256: &str,
        cancellation: AgentCancellation,
    ) -> io::Result<Self> {
        Self::start_inner(
            policy,
            profile_dir,
            credentials,
            capability,
            bundle,
            tool_sha256,
            prompt_sha256,
            None,
            cancellation,
            None,
        )
    }

    pub fn start_with_mcp_cancellable(
        policy: Policy,
        profile_dir: &Path,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        bundle: &[u8],
        tool_sha256: &str,
        prompt_sha256: &str,
        mcp: WorkerMcp,
        cancellation: AgentCancellation,
    ) -> io::Result<Self> {
        Self::start_inner(
            policy,
            profile_dir,
            credentials,
            capability,
            bundle,
            tool_sha256,
            prompt_sha256,
            Some(mcp),
            cancellation,
            None,
        )
    }

    /// The exact nonsecret Go binding is durably retained before custody/staging.
    #[allow(clippy::too_many_arguments)]
    pub fn start_with_intent(
        policy: Policy,
        profile_dir: &Path,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        bundle: &[u8],
        tool_sha256: &str,
        prompt_sha256: &str,
        mcp: Option<WorkerMcp>,
        cancellation: AgentCancellation,
        intent: &LaunchIntent,
    ) -> io::Result<Self> {
        Self::start_inner(
            policy,
            profile_dir,
            credentials,
            capability,
            bundle,
            tool_sha256,
            prompt_sha256,
            mcp,
            cancellation,
            Some(intent),
        )
    }
    fn start_inner(
        mut policy: Policy,
        profile_dir: &Path,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        bundle: &[u8],
        tool_sha256: &str,
        prompt_sha256: &str,
        mcp: Option<WorkerMcp>,
        cancellation: AgentCancellation,
        intent: Option<&LaunchIntent>,
    ) -> io::Result<Self> {
        if let Some(intent) = intent {
            intent.bind_policy(&mut policy, tool_sha256, prompt_sha256)?;
        }
        if cancellation.cancelled() {
            return Err(refused("provider preparation cancelled"));
        }
        policy
            .remaining()
            .map_err(|_| refused("provider run deadline expired"))?;
        let expected: String = Sha256::digest(bundle)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        if expected != policy.sha256() {
            return Err(refused("provider source differs from admitted bundle"));
        }
        private_dir(profile_dir).map_err(|_| refused("provider receipt profile invalid"))?;
        let identity = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(profile_dir.join("profile.id"))?;
        let metadata = identity.metadata()?;
        let mut retained_profile = String::new();
        identity.take(33).read_to_string(&mut retained_profile)?;
        if !metadata.is_file()
            || metadata.uid() != unsafe { libc::geteuid() }
            || metadata.mode() & 0o077 != 0
            || metadata.len() != 32
            || retained_profile != policy.profile()
        {
            return Err(refused("provider receipt profile identity invalid"));
        }
        ensure_receipts(profile_dir).map_err(|_| refused("provider receipt directory invalid"))?;
        let run = policy.run().to_owned();
        let profile = policy.profile().to_owned();
        let candidate = policy.candidate().to_owned();
        let role = policy.role();
        let deadline = policy.deadline;
        let connection = policy.connection().to_owned();
        let bound = BoundOwner::bind(policy, credentials, capability)?;
        let bound = match mcp {
            Some(mcp) => bound.with_mcp(mcp)?,
            None => bound,
        };
        let scope = ReceiptScope::new(
            &run,
            &profile,
            &expected,
            &candidate,
            role,
            tool_sha256,
            prompt_sha256,
        )?;
        if let Some(intent) = intent {
            intent.persist(profile_dir)?;
        } else {
            // A legacy entry point cannot sidestep an already retained intent,
            // including an interrupted start before custody was acquired.
            super::intent::require_absent(profile_dir, &scope)?;
        }
        let custody = RunCustody::prepare(profile_dir, &scope, &connection, cancellation.clone())?;
        let preparing = Broker::preparing(cancellation.clone());
        let staged = match preparing.stage_bundle(
            &run,
            &profile,
            role,
            tool_sha256,
            prompt_sha256,
            bundle,
        ) {
            Ok(staged) => staged,
            Err(error) => {
                // The broker may have committed staging before its reply was
                // lost. The exact admitted digest is the only safe cleanup
                // selector; unavailable cleanup remains uncertain.
                return if Broker::retire_unlaunched(&run, &profile, &expected).is_ok() {
                    Err(error)
                } else {
                    Err(refused("broker staging cleanup unconfirmed"))
                };
            }
        };
        let mut cleanup = Staged {
            run: run.clone(),
            profile: profile.clone(),
            sha256: staged.clone(),
            active: true,
        };
        if staged != expected {
            return if cleanup.abort().is_ok() {
                Err(refused("broker source differs from admitted bundle"))
            } else {
                Err(refused("broker source cleanup unconfirmed"))
            };
        }
        let result = (|| {
            preparing.prepare_reviewer(&run, &profile, &staged, &candidate)?;
            let relay =
                preparing.attest_provider_relay(&run, &profile, &staged, &candidate, role)?;
            preparing.arm_agent(
                &run,
                &profile,
                &staged,
                &candidate,
                role,
                tool_sha256,
                prompt_sha256,
            )?;
            preparing.attest_agent(
                &run,
                &profile,
                &staged,
                &candidate,
                role,
                tool_sha256,
                prompt_sha256,
            )?;
            bound.activate_cancellable(relay, cancellation.clone())
        })();
        let owner = match result {
            Ok(owner) => owner,
            Err(error) => {
                return if cleanup.abort().is_ok() {
                    Err(error)
                } else {
                    Err(refused("provider run setup cleanup unconfirmed"))
                };
            }
        };
        custody.activate()?;
        cleanup.active = false;
        Ok(Self {
            intent_digest: intent.map(LaunchIntent::digest),
            owner,
            cancellation,
            observer: AgentObserver::default(),
            custody,
            run,
            profile,
            sha256: staged,
            candidate,
            role,
            tool_sha256: tool_sha256.to_owned(),
            prompt_sha256: prompt_sha256.to_owned(),
            deadline,
            profile_dir: profile_dir.to_path_buf(),
            attempted: false,
            receipt_persisted: false,
            retired: false,
        })
    }

    /// Clone for the native lifecycle owner. Set it before joining `execute`
    /// on Quit, request loss or credential withdrawal; the broker then closes
    /// the exact worker stream and retires its process tree.
    pub fn cancellation(&self) -> AgentCancellation {
        self.cancellation.clone()
    }

    pub(super) fn intent_digest(&self) -> Option<&str> {
        self.intent_digest.as_deref()
    }

    pub(super) fn uses_cancellation(&self, cancellation: &AgentCancellation) -> bool {
        self.cancellation.same(cancellation)
    }

    /// Retain with the lifecycle owner; only an executing bound process can
    /// answer. A confirmed terminal result subsequently reports non-live.
    pub fn observer(&self) -> AgentObserver {
        self.observer.clone()
    }

    pub fn custody(&self) -> RunCustody {
        self.custody.clone()
    }

    fn quiesce_uncertain(&mut self) -> io::Result<()> {
        self.cancellation.cancel();
        let stopped = self.owner.stop_preserving_bundle();
        let sealed = if self.owner.locally_stopped() {
            self.custody.seal()
        } else {
            Err(refused("provider local custody retirement unconfirmed"))
        };
        let quiesced = Broker::quiesce_run(&self.run, &self.profile, &self.sha256);
        let released = if sealed.is_ok() && quiesced.is_ok() {
            self.custody.release()
        } else {
            Err(refused("provider custody retirement unconfirmed"))
        };
        stopped.and(sealed).and(quiesced).and(released)
    }

    /// One attempt only. The broker rehashes prompt and executable bytes under
    /// the worker UID. A result is released only after local token use stops
    /// and exact broker retirement succeeds.
    pub fn execute(
        &mut self,
        prompt: &[u8],
        executable: &Path,
        duration: Duration,
    ) -> io::Result<AgentRun> {
        self.execute_observed(prompt, executable, duration, |_| Ok(()))
    }

    /// Observe the broker-validated isolated process before its terminal
    /// receipt. Observation alone never activates product or MCP authority.
    pub fn execute_observed(
        &mut self,
        prompt: &[u8],
        executable: &Path,
        duration: Duration,
        on_started: impl FnOnce(&AgentStarted) -> io::Result<()>,
    ) -> io::Result<AgentRun> {
        if self.attempted || self.retired {
            return Err(refused("provider run already consumed"));
        }
        let remaining = self.deadline.saturating_duration_since(Instant::now());
        if duration.is_zero() || duration > remaining || !self.owner.running() {
            let _ = self.retire();
            return Err(refused("provider run deadline or owner unavailable"));
        }
        self.attempted = true;
        let result = Broker::execute_agent_controlled(
            &self.run,
            &self.profile,
            &self.sha256,
            &self.candidate,
            self.role,
            &self.tool_sha256,
            &self.prompt_sha256,
            prompt,
            executable,
            self.owner.placeholder(),
            duration,
            self.cancellation.clone(),
            self.observer.clone(),
            on_started,
        );
        let run = match result {
            Ok(run) => run,
            Err(_) => {
                let _ = self.quiesce_uncertain();
                return Err(refused("provider run outcome unconfirmed"));
            }
        };
        let persisted = ReceiptScope::new(
            &self.run,
            &self.profile,
            &self.sha256,
            &self.candidate,
            self.role,
            &self.tool_sha256,
            &self.prompt_sha256,
        )
        .and_then(|scope| write_retained(&self.profile_dir, &scope, &run));
        if persisted.is_err() {
            let _ = self.quiesce_uncertain();
            return Err(refused("provider run receipt not durable"));
        }
        self.receipt_persisted = true;
        // The broker may have returned just before Quit or request loss. Keep
        // that external cancellation visible through final local/root cleanup.
        let retired = self.retire_after_attempt();
        if retired.is_err() {
            return Err(refused("provider run retirement unconfirmed"));
        }
        if self.cancellation.cancelled() {
            return Err(refused("agent execution cancelled"));
        }
        Ok(run)
    }

    /// Before a receipt is durable, retirement quiesces the exact worker and
    /// relay but preserves root staging for an uncertain-outcome recovery.
    pub fn retire(&mut self) -> io::Result<()> {
        if !self.retired {
            self.cancellation.cancel();
        }
        if self.attempted && !self.receipt_persisted {
            self.quiesce_uncertain()?;
            return Err(refused("provider run outcome unconfirmed"));
        }
        self.retire_after_attempt()
    }

    fn retire_after_attempt(&mut self) -> io::Result<()> {
        if !self.retired {
            let retired = self.owner.retire();
            if !self.owner.locally_stopped() {
                return Err(refused("provider local custody retirement unconfirmed"));
            }
            self.custody.seal()?;
            retired?;
            self.custody.release()?;
            self.retired = true;
        }
        Ok(())
    }
}

impl Drop for RunOwner {
    fn drop(&mut self) {
        let _ = self.retire();
    }
}
