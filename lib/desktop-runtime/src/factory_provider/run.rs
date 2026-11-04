//! One native-only owner for an exact isolated run. Product admission is still
//! supplied by the future Go execution port; this module cannot create it.
use super::{AgentCancellation, AgentRun, BoundOwner, Broker, Owner, Policy, Role};
use crate::credentials::{Credentials, TokenCapability};
use sha2::{Digest, Sha256};
use std::io;
use std::path::Path;
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
            Broker::retire_bundle(&self.run, &self.profile, &self.sha256)?;
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
    cancellation: AgentCancellation,
    run: String,
    profile: String,
    sha256: String,
    candidate: String,
    role: Role,
    tool_sha256: String,
    prompt_sha256: String,
    deadline: Instant,
    attempted: bool,
    retired: bool,
}

impl RunOwner {
    /// Bind a private credential owner before staging. The relay and worker
    /// are attested while the owner is still inert, then exact attestation
    /// activates token use. Any failure after staging tries exact retirement.
    pub fn start(
        policy: Policy,
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        bundle: &[u8],
        tool_sha256: &str,
        prompt_sha256: &str,
    ) -> io::Result<Self> {
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
        let run = policy.run().to_owned();
        let profile = policy.profile().to_owned();
        let candidate = policy.candidate().to_owned();
        let role = policy.role();
        let deadline = policy.deadline;
        let bound = BoundOwner::bind(policy, credentials, capability)?;
        let staged =
            match Broker::stage_bundle(&run, &profile, role, tool_sha256, prompt_sha256, bundle) {
                Ok(staged) => staged,
                Err(error) => {
                    // The broker may have committed staging before its reply was
                    // lost. The exact admitted digest is the only safe cleanup
                    // selector; unavailable cleanup remains uncertain.
                    return if Broker::retire_bundle(&run, &profile, &expected).is_ok() {
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
            Broker::prepare_reviewer(&run, &profile, &staged, &candidate)?;
            let relay = Broker::attest_provider_relay(&run, &profile, &staged, &candidate, role)?;
            Broker::arm_agent(
                &run,
                &profile,
                &staged,
                &candidate,
                role,
                tool_sha256,
                prompt_sha256,
            )?;
            Broker::attest_agent(
                &run,
                &profile,
                &staged,
                &candidate,
                role,
                tool_sha256,
                prompt_sha256,
            )?;
            bound.activate(relay)
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
        cleanup.active = false;
        Ok(Self {
            owner,
            cancellation: AgentCancellation::default(),
            run,
            profile,
            sha256: staged,
            candidate,
            role,
            tool_sha256: tool_sha256.to_owned(),
            prompt_sha256: prompt_sha256.to_owned(),
            deadline,
            attempted: false,
            retired: false,
        })
    }

    /// Clone for the native lifecycle owner. Set it before joining `execute`
    /// on Quit, request loss or credential withdrawal; the broker then closes
    /// the exact worker stream and retires its process tree.
    pub fn cancellation(&self) -> AgentCancellation {
        self.cancellation.clone()
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
        if self.attempted || self.retired {
            return Err(refused("provider run already consumed"));
        }
        let remaining = self.deadline.saturating_duration_since(Instant::now());
        if duration.is_zero() || duration > remaining || !self.owner.running() {
            let _ = self.retire();
            return Err(refused("provider run deadline or owner unavailable"));
        }
        self.attempted = true;
        let result = Broker::execute_agent_cancellable(
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
        );
        // The broker may have returned just before Quit or request loss. Keep
        // that external cancellation visible through final local/root cleanup;
        // only a caller-initiated retire should set the cancellation flag.
        let retired = self.retire_after_attempt();
        match (result, retired) {
            (Ok(run), Ok(())) if !self.cancellation.cancelled() => Ok(run),
            (Ok(_), Ok(())) => Err(refused("agent execution cancelled")),
            (Err(error), Ok(())) => Err(error),
            _ => Err(refused("provider run or retirement unconfirmed")),
        }
    }

    pub fn retire(&mut self) -> io::Result<()> {
        if !self.retired {
            self.cancellation.cancel();
        }
        self.retire_after_attempt()
    }

    fn retire_after_attempt(&mut self) -> io::Result<()> {
        if !self.retired {
            self.owner.retire()?;
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
