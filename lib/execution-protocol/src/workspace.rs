//! Native workspace commands. The plan supplies ownership and executable argv;
//! commands select tasks and text edits, never desktop paths or shell authority.
use serde::{Deserialize, Serialize};

pub const MAX_COMMAND: usize = 96 * 1024;
pub const MAX_REPLY: usize = 17 * 1024 * 1024;
pub const MAX_TEXT: usize = 32768;
pub const MAX_COMMANDS: usize = 4096;

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Parent {
    pub invocation: String,
    pub system_instance: String,
    pub start_identity: String,
    pub pid: u32,
    pub executable_sha256: String,
}
impl Parent {
    pub fn from_muse(process: &crate::MuseObservation) -> Self {
        Self {
            invocation: process.invocation.clone(),
            system_instance: process.system_instance.clone(),
            start_identity: process.start_identity.clone(),
            pid: process.pid,
            executable_sha256: process.executable_sha256.clone(),
        }
    }
    pub fn valid(&self) -> bool {
        self.pid > 0
            && [
                &self.invocation,
                &self.system_instance,
                &self.start_identity,
            ]
            .iter()
            .all(|s| !s.is_empty() && s.len() <= 128 && !s.chars().any(char::is_control))
            && self.executable_sha256.len() == 64
            && self
                .executable_sha256
                .bytes()
                .all(|b| b.is_ascii_hexdigit())
    }
}

#[derive(Deserialize, Serialize)]
#[serde(tag = "action", rename_all = "snake_case", deny_unknown_fields)]
pub enum Command {
    BindParent {
        invocation: String,
    },
    Start {
        task: String,
    },
    Observe,
    StartAfter {
        task: String,
        expected_head: String,
    },
    Read {
        task: String,
        path: String,
        offset: usize,
        limit: usize,
    },
    Edit {
        operation_id: String,
        task: String,
        path: String,
        offset: usize,
        expected: String,
        replacement: String,
    },
    MakeDirectory {
        operation_id: String,
        task: String,
        path: String,
    },
    Remove {
        operation_id: String,
        task: String,
        path: String,
        expected_digest: String,
    },
    Release {
        task: String,
    },
    Checkpoint {
        task: String,
        output_digest: String,
        expected_head: String,
    },
    Final {
        expected_head: String,
    },
    Close,
}
impl Command {
    pub fn retains_receipt(&self) -> bool {
        matches!(
            self,
            Self::BindParent { .. }
                | Self::Edit { .. }
                | Self::MakeDirectory { .. }
                | Self::Remove { .. }
                | Self::Start { .. }
                | Self::StartAfter { .. }
                | Self::Release { .. }
                | Self::Checkpoint { .. }
                | Self::Final { .. }
        )
    }
    pub fn receipt_key(&self) -> Result<String, serde_json::Error> {
        use sha2::{Digest, Sha256};
        if let Self::Edit { operation_id, .. }
        | Self::MakeDirectory { operation_id, .. }
        | Self::Remove { operation_id, .. } = self
        {
            return Ok(
                Sha256::digest(format!("factory-workspace-operation-v1\0{operation_id}"))
                    .iter()
                    .map(|b| format!("{b:02x}"))
                    .collect(),
            );
        }
        Ok(Sha256::digest(serde_json::to_vec(self)?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect())
    }
    pub fn valid_operation_id(&self) -> bool {
        match self {
            Self::Edit { operation_id, .. }
            | Self::MakeDirectory { operation_id, .. }
            | Self::Remove { operation_id, .. } => {
                !operation_id.is_empty()
                    && operation_id.len() <= 128
                    && !operation_id.chars().any(char::is_control)
            }
            _ => true,
        }
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CheckResult {
    pub id: String,
    pub passed: bool,
    pub log_digest: String,
}

// Field order and omitted empty fields match project.TaskEvent's canonical
// encoding. Detail is restricted to native-produced keys, hashes and OIDs.
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Event {
    pub sequence: i64,
    pub prev_digest: String,
    #[serde(rename = "type")]
    pub kind: String,
    pub task_key: String,
    pub invocation: String,
    pub plan_digest: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub detail: String,
}
impl Event {
    pub fn digest(&self) -> Result<String, serde_json::Error> {
        use sha2::{Digest, Sha256};
        // Go's canonical json.Marshal also escapes HTML and these separators.
        let canonical = serde_json::to_string(self)?
            .replace('&', "\\u0026")
            .replace('<', "\\u003c")
            .replace('>', "\\u003e")
            .replace('\u{2028}', "\\u2028")
            .replace('\u{2029}', "\\u2029");
        Ok(Sha256::digest(canonical.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect())
    }
}

#[derive(Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Reply {
    pub status: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub head: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub tree: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub output_digest: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub content: Option<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub file_digest: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub total_bytes: Option<u64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub next_offset: Option<u64>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub checks: Vec<CheckResult>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub events: Vec<Event>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub bundle: Vec<u8>,
}
