//! Private native connector input, never a product API or browser command.
//! Muse owns authentication and provider request limits. No account credential
//! crosses this contract; MCP authorization is an ephemeral Factory run secret.
use super::{HarnessProfile, MAX_OUTPUT, MAX_PROMPT};
use serde::{Deserialize, Serialize};
use std::collections::BTreeSet;

pub const MAX_MUSE_INPUT: usize = 6 * MAX_PROMPT + 32 * 1024;
pub const MAX_MUSE_MODEL_STEPS: u8 = 32;

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct MuseInvocation {
    pub run: String,
    /// Fresh native service identity, separate from retained product run IDs.
    pub service: String,
    pub executable: String,
    pub executable_sha256: String,
    pub prompt: String,
    pub harness: HarnessProfile,
    pub duration_ms: u64,
    pub deadline_unix_ms: u64,
    pub max_model_steps: u8,
    pub output_bytes: usize,
    pub mcp: Option<MuseMcp>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct MuseMcp {
    pub port: u16,
    pub authorization: String,
    /// Muse aliases of the exact admitted Go catalog, without `mcp__factory__`.
    pub tools: Vec<String>,
}

fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

impl MuseInvocation {
    pub fn valid(&self) -> bool {
        hex(&self.run, 32)
            && hex(&self.service, 32)
            && std::path::Path::new(&self.executable).is_absolute()
            && self.executable.len() <= 4096
            && !self.executable.chars().any(char::is_control)
            && hex(&self.executable_sha256, 64)
            && (1..=MAX_PROMPT).contains(&self.prompt.len())
            && !self.prompt.contains('\0')
            && self.harness.valid()
            && (1..=14_400_000).contains(&self.duration_ms)
            && (1..=MAX_MUSE_MODEL_STEPS).contains(&self.max_model_steps)
            && (1..=MAX_OUTPUT).contains(&self.output_bytes)
            && self.mcp.as_ref().is_none_or(|mcp| {
                let mut unique = BTreeSet::new();
                mcp.port != 0
                    && hex(&mcp.authorization, 64)
                    && (1..=128).contains(&mcp.tools.len())
                    && mcp.tools.iter().all(|tool| {
                        let name = tool
                            .strip_prefix("factory_")
                            .or_else(|| tool.strip_prefix("systemone_"));
                        name.is_some_and(|name| {
                            !name.is_empty()
                                && name.len() <= 96
                                && name.bytes().all(|b| b.is_ascii_lowercase() || b == b'_')
                        }) && unique.insert(tool)
                    })
            })
    }
}

/// Process facts, not model success, billing identity or isolated-worker proof.
/// A terminal frame is followed by exactly output_length raw captured bytes.
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct MuseObservation {
    pub kind: String,
    pub version: u8,
    pub run: String,
    pub service: String,
    pub sequence: u64,
    pub live: Option<bool>,
    pub executable_sha256: String,
    pub prompt_sha256: String,
    pub invocation: String,
    pub system_instance: String,
    pub start_identity: String,
    pub pid: u32,
    pub uid: u32,
    pub started_unix_ms: u64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stopped_unix_ms: Option<u64>,
    pub finished: bool,
    pub tree_retired: bool,
    pub output_complete: bool,
    pub output_length: usize,
    pub output_sha256: Option<String>,
    pub exit_code: Option<i32>,
    pub exit_signal: Option<i32>,
    pub reason: Option<String>,
}
