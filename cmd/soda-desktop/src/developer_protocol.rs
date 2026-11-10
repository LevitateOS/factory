//! Local diagnostics wire contract. Product commands retain their own identities.
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};

pub const REQUEST_LIMIT: usize = 65536;
pub const RESPONSE_LIMIT: usize = 1 << 20;
pub fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Request {
    pub version: u8,
    #[serde(default)]
    pub window: Option<String>,
    #[serde(default)]
    pub scope: Option<String>,
    #[serde(default)]
    pub action: Option<String>,
    #[serde(flatten)]
    pub operation: Operation,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(tag = "op", rename_all = "snake_case", deny_unknown_fields)]
pub enum Operation {
    Status,
    Tree,
    Click {
        target: Target,
    },
    Fill {
        target: Target,
        value: String,
        expected: String,
    },
    Select {
        target: Target,
        value: String,
        expected: String,
    },
    Check {
        target: Target,
        checked: bool,
        expected: bool,
    },
    Wait {
        target: Target,
        state: WaitState,
        timeout_ms: u64,
    },
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Target {
    #[serde(default)]
    pub within: Option<String>,
    #[serde(default)]
    pub role: Option<String>,
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub node: Option<String>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum WaitState {
    Present,
    Absent,
    Enabled,
    Disabled,
    Checked,
    Unchecked,
}

impl Request {
    pub fn decode(bytes: &[u8]) -> Result<Self, &'static str> {
        let request: Self = serde_json::from_slice(bytes).map_err(|_| "invalid_request")?;
        // Serde's flattened internally-tagged unit variants ignore extra fields.
        // Derive the permitted keys from the same serialized contract instead
        // of maintaining a second operation/field allowlist.
        let original: Value = serde_json::from_slice(bytes).map_err(|_| "invalid_request")?;
        let canonical = serde_json::to_value(&request).map_err(|_| "invalid_request")?;
        if !original
            .as_object()
            .is_some_and(|object| object.keys().all(|key| canonical.get(key).is_some()))
        {
            return Err("invalid_request");
        }
        request.validate()?;
        Ok(request)
    }
    pub fn is_action(&self) -> bool {
        matches!(
            self.operation,
            Operation::Click { .. }
                | Operation::Fill { .. }
                | Operation::Select { .. }
                | Operation::Check { .. }
        )
    }
    pub fn timeout_ms(&self) -> u64 {
        match self.operation {
            Operation::Wait { timeout_ms, .. } => timeout_ms + 2000,
            _ => 5000,
        }
    }
    pub fn validate(&self) -> Result<(), &'static str> {
        if self.version != 1 {
            return Err("unsupported_version");
        }
        if self.window.as_ref().is_some_and(|s| s.len() > 128)
            || self.scope.as_ref().is_some_and(|s| s.len() > 8192)
        {
            return Err("invalid_request");
        }
        if self.is_action() {
            if !self.action.as_ref().is_some_and(|s| valid_id(s)) || self.scope.is_none() {
                return Err("action_and_scope_required");
            }
        } else if self.action.is_some() {
            return Err("invalid_request");
        }
        let target = match &self.operation {
            Operation::Status | Operation::Tree => None,
            Operation::Click { target }
            | Operation::Fill { target, .. }
            | Operation::Select { target, .. }
            | Operation::Check { target, .. }
            | Operation::Wait { target, .. } => Some(target),
        };
        if target.is_some() && self.scope.is_none() {
            return Err("scope_required");
        }
        if let Some(t) = target {
            if [
                t.within.as_ref(),
                t.role.as_ref(),
                t.name.as_ref(),
                t.node.as_ref(),
            ]
            .iter()
            .flatten()
            .any(|s| s.is_empty() || s.len() > 4096)
            {
                return Err("invalid_target");
            }
            if t.node.is_some() == (t.name.is_some() || t.role.is_some())
                || (t.node.is_none() && t.name.is_none())
            {
                return Err("invalid_target");
            }
        }
        if let Operation::Wait { timeout_ms, .. } = self.operation {
            if !(1..=120000).contains(&timeout_ms) {
                return Err("invalid_deadline");
            }
        }
        if let Operation::Fill {
            value, expected, ..
        }
        | Operation::Select {
            value, expected, ..
        } = &self.operation
        {
            if value.len() > 16384 || expected.len() > 16384 {
                return Err("value_too_large");
            }
        }
        Ok(())
    }
}
pub fn valid_id(s: &str) -> bool {
    !s.is_empty()
        && s.len() <= 128
        && s.bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_-".contains(&b))
}
pub fn error(code: &str) -> Value {
    json!({"ok":false,"error":code})
}
pub fn directory(profile: &Path) -> PathBuf {
    profile.join("developer")
}
pub fn socket(profile: &Path) -> PathBuf {
    directory(profile).join("control.sock")
}
pub fn private_directory(path: &Path) -> Result<(), &'static str> {
    let m = std::fs::symlink_metadata(path).map_err(|_| "unavailable")?;
    if !m.is_dir() || m.uid() != unsafe { libc::geteuid() } || m.mode() & 0o077 != 0 {
        return Err("private_directory_required");
    }
    Ok(())
}
