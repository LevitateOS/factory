//! Construct an owned execution only from a captured Go intent and native-only
//! credential/MCP capabilities. Request lifetime never owns the resulting run.
use super::{
    ExecutionBinding, ExecutionTicket, Executions, LaunchIntent, Policy, RunOwner, RunStart,
    WorkerMcp,
};
use crate::credentials::{Credentials, Provider, TokenCapability};
use sha2::{Digest, Sha256};
use soda_execution_protocol::{MAX_PROMPT, MAX_STAGE_BUNDLE};
use std::io;
use std::path::PathBuf;
use std::sync::Arc;

pub struct ExecutionStart {
    pub intent: LaunchIntent,
    pub policy: Policy,
    pub credentials: Arc<Credentials>,
    pub capability: TokenCapability,
    pub bundle: Vec<u8>,
    pub prompt: Vec<u8>,
    pub executable: PathBuf,
    pub mcp: WorkerMcp,
}

impl Executions {
    pub(crate) fn launch(
        &mut self,
        profile: PathBuf,
        mut start: ExecutionStart,
    ) -> io::Result<ExecutionTicket> {
        let refuse = || {
            io::Error::new(
                io::ErrorKind::PermissionDenied,
                "native launch differs from captured admission",
            )
        };
        if start.bundle.is_empty()
            || start.bundle.len() > MAX_STAGE_BUNDLE
            || start.prompt.is_empty()
            || start.prompt.len() > MAX_PROMPT
            || start.executable != start.intent.executable()
            || start.capability.account().provider != Provider::Muse
            || start.capability.account().connection != start.intent.connection()
            || start.mcp.run != start.policy.run()
        {
            return Err(refuse());
        }
        let tool = start.intent.tool_sha256().to_owned();
        let digest = |bytes: &[u8]| -> String {
            Sha256::digest(bytes)
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect()
        };
        let prompt = digest(&start.prompt);
        if digest(&start.bundle) != start.policy.sha256() {
            return Err(refuse());
        }
        start
            .intent
            .bind_policy(&mut start.policy, &tool, &prompt)?;
        let duration = start.policy.remaining().map_err(|_| refuse())?;
        let binding = ExecutionBinding::from_intent(&start.intent)?;
        let ExecutionStart {
            intent,
            policy,
            credentials,
            capability,
            bundle,
            prompt: bytes,
            executable,
            mcp,
        } = start;
        self.ensure(
            binding,
            move |cancellation| {
                RunOwner::prepare(RunStart {
                    policy,
                    profile_dir: &profile,
                    credentials,
                    capability,
                    bundle: &bundle,
                    tool_sha256: &tool,
                    prompt_sha256: &prompt,
                    mcp: Some(mcp),
                    cancellation,
                    intent: Some(&intent),
                })
            },
            bytes,
            executable,
            duration,
        )
    }
}
