//! One native-only owner for an exact isolated run. Product admission is still
//! supplied by the future Go execution port; this module cannot create it.
use super::{AgentCancellation, AgentRun, BoundOwner, Broker, Owner, Policy, Role};
use crate::credentials::{Credentials, TokenCapability};
use crate::database::private_dir;
use serde_json::json;
use sha2::{Digest, Sha256};
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
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
    receipts: PathBuf,
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
        let receipts = profile_dir.join("native-run-receipts");
        private_dir(&receipts).map_err(|_| refused("provider receipt directory invalid"))?;
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
            receipts,
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

    // A complete, bounded broker observation is retained before root cleanup.
    // The Go owner will consume the exact file under its own run transaction;
    // a file by itself never authorizes a successful product outcome.
    fn persist_receipt(&self, run: &AgentRun) -> io::Result<()> {
        private_dir(&self.receipts).map_err(|_| refused("provider receipt directory invalid"))?;
        let desktop_uid = unsafe { libc::geteuid() };
        if desktop_uid == 0 || run.worker_uid == 0 || run.worker_uid == desktop_uid {
            return Err(refused("provider receipt isolation identity invalid"));
        }
        if run.output.len() > 16 * 1024 * 1024 {
            return Err(refused("provider receipt output bound exceeded"));
        }
        let observed_bundle_sha256: String = Sha256::digest(&run.candidate_bundle)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        let captured = run.candidate_state == "captured"
            && self.role == Role::Coder
            && run.result_candidate.as_ref().is_some_and(|commit| {
                commit.len() == 40
                    && commit
                        .bytes()
                        .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
            })
            && (1..=4 * 1024 * 1024).contains(&run.candidate_bundle.len())
            && run.bundle_sha256.as_deref() == Some(observed_bundle_sha256.as_str());
        let no_bundle = match self.role {
            Role::Reviewer => run.candidate_state == "not_applicable",
            Role::Coder => matches!(
                run.candidate_state.as_str(),
                "unchanged" | "dirty" | "unavailable"
            ),
        } && run.result_candidate.is_none()
            && run.candidate_bundle.is_empty()
            && run.bundle_sha256.is_none();
        if !captured && !no_bundle {
            return Err(refused("provider receipt candidate bundle invalid"));
        }
        let role = match self.role {
            Role::Reviewer => "reviewer",
            Role::Coder => "coder",
        };
        let output_sha256: String = Sha256::digest(&run.output)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        let header = serde_json::to_vec(&json!({
            "kind":"native_agent_receipt","version":2,
            "run":self.run,"profile":self.profile,"source_sha256":self.sha256,
            "candidate":self.candidate,"role":role,
            "tool_sha256":self.tool_sha256,"prompt_sha256":self.prompt_sha256,
            "desktop_uid":desktop_uid,"worker_uid":run.worker_uid,
            "invocation":run.process.invocation,
            "system_instance":run.process.system_instance,
            "start_identity":run.process.start_identity,
            "namespace_pid":run.process.namespace_pid,
            "started_unix_ms":run.process.started_unix_ms,
            "reason":run.reason,"exit_code":run.exit_code,"exit_signal":run.exit_signal,
            "output_complete":run.output_complete,
            "output_length":run.output.len(),"output_sha256":output_sha256,
            "candidate_state":run.candidate_state,
            "result_candidate":run.result_candidate,
            "bundle_length":run.candidate_bundle.len(),
            "bundle_sha256":run.bundle_sha256,
        }))
        .map_err(|_| refused("provider receipt header invalid"))?;
        if header.is_empty() || header.len() > 2048 {
            return Err(refused("provider receipt header bound exceeded"));
        }
        let pending = self.receipts.join(format!("{}.pending", self.run));
        let committed = self.receipts.join(format!("{}.receipt", self.run));
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&pending)?;
        file.write_all(&(header.len() as u32).to_le_bytes())?;
        file.write_all(&header)?;
        file.write_all(&run.output)?;
        file.write_all(&run.candidate_bundle)?;
        file.sync_all()?;
        fs::hard_link(&pending, &committed)?;
        File::open(&self.receipts)?.sync_all()?;
        fs::remove_file(&pending)?;
        File::open(&self.receipts)?.sync_all()
    }

    fn quiesce_uncertain(&mut self) -> io::Result<()> {
        self.cancellation.cancel();
        let stopped = self.owner.stop();
        let quiesced = Broker::quiesce_run(&self.run, &self.profile, &self.sha256);
        stopped.and(quiesced)
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
        let run = match result {
            Ok(run) => run,
            Err(_) => {
                let _ = self.quiesce_uncertain();
                return Err(refused("provider run outcome unconfirmed"));
            }
        };
        if self.persist_receipt(&run).is_err() {
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
