//! Native workspace commands. The plan supplies ownership and executable argv;
//! commands select tasks and text edits, never desktop paths or shell authority.
use serde::{Deserialize, Serialize};

pub const MAX_COMMAND: usize = 96 * 1024;
pub const MAX_REPLY: usize = 17 * 1024 * 1024;
pub const MAX_TEXT: usize = 32768;

#[derive(Deserialize, Serialize)]
#[serde(tag = "action", rename_all = "snake_case", deny_unknown_fields)]
pub enum Command {
    Start {
        task: String,
    },
    Read {
        path: String,
        offset: usize,
        limit: usize,
    },
    Edit {
        path: String,
        offset: usize,
        expected: String,
        replacement: String,
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
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub checks: Vec<CheckResult>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub events: Vec<Event>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub bundle: Vec<u8>,
}
