//! Native custody of causal write outcomes. The product journal still owns
//! policy and adoption. Missing/incomplete records never authorize another send.
use super::{Command, Descriptor, Failure, Scope, State};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::path::PathBuf;

#[derive(Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Capture {
    version: u8,
    profile: String,
    connection: String,
    origin: String,
    account: String,
    repository: String,
    descriptor: Descriptor,
    request_sha256: String,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Completed {
    capture_sha256: String,
    outcome: Value,
}
fn sha(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn directory(state: &State) -> Result<PathBuf, Failure> {
    let root = state
        .channel
        .socket
        .parent()
        .ok_or_else(Failure::unavailable)?;
    let path = root.join("native-write-outcomes");
    private_dir(&path).map_err(|_| Failure::unavailable())?;
    Ok(path)
}
fn matches(state: &State, scope: &Scope, descriptor: &Descriptor, capture: &Capture) -> bool {
    let account = scope.capability.account();
    capture.version == 1
        && capture.profile == state.channel.profile
        && capture.connection == account.connection
        && capture.origin == account.origin
        && capture.account == account.account
        && scope.repository.as_deref() == Some(capture.repository.as_str())
        && capture.descriptor == *descriptor
}
pub(super) struct Attempt {
    path: PathBuf,
    capture_sha256: String,
}
impl Attempt {
    /// Publish the exact intent before provider I/O. An existing intent is a
    /// one-shot fence even if its reply/outcome was never retained.
    pub(super) fn reserve(
        state: &State,
        scope: &Scope,
        command: &Command,
    ) -> Result<Self, Failure> {
        let directory = directory(state)?;
        let account = scope.capability.account();
        let request = serde_json::to_vec(&command.operation).map_err(|_| Failure::invalid())?;
        let capture = Capture {
            version: 1,
            profile: state.channel.profile.clone(),
            connection: account.connection.clone(),
            origin: account.origin.clone(),
            account: account.account.clone(),
            repository: scope.repository.clone().ok_or_else(Failure::invalid)?,
            descriptor: command.descriptor.clone(),
            request_sha256: sha(&request),
        };
        let bytes = serde_json::to_vec(&capture).map_err(|_| Failure::invalid())?;
        let intent = directory.join(format!("{}.intent.json", command.descriptor.command_id));
        publish_bytes(&intent, &bytes)
            .map_err(|_| Failure::new(409, "native_write_history_held"))?;
        Ok(Self {
            path: directory.join(format!("{}.outcome.json", command.descriptor.command_id)),
            capture_sha256: sha(&bytes),
        })
    }
    pub(super) fn finish(self, outcome: Value) -> Result<Value, Failure> {
        let record = Completed {
            capture_sha256: self.capture_sha256,
            outcome,
        };
        let bytes = serde_json::to_vec(&record).map_err(|_| Failure::unavailable())?;
        if bytes.len() > 8192 {
            return Err(Failure::unavailable());
        }
        publish_bytes(&self.path, &bytes).map_err(|_| Failure::unavailable())?;
        Ok(record.outcome)
    }
}
/// What the receipt directory holds for one command. `Reserved` is the
/// outcome-unknown state: `reserve` refuses any resend of it.
pub(super) enum Lookup {
    Absent,
    Reserved,
    Done(Value),
}
impl Lookup {
    /// The reply to Go. Absent and Reserved are deliberately the same bytes
    /// (`null`); the wire has no way to tell them apart.
    pub(super) fn into_wire(self) -> Value {
        match self {
            Self::Done(outcome) => outcome,
            Self::Absent | Self::Reserved => Value::Null,
        }
    }
}
pub(super) fn lookup(
    state: &State,
    scope: &Scope,
    descriptor: &Descriptor,
) -> Result<Lookup, Failure> {
    let directory = directory(state)?;
    let intent = directory.join(format!("{}.intent.json", descriptor.command_id));
    let bytes = match read_bytes(&intent, 4096) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Lookup::Absent),
        Err(_) => return Err(Failure::unavailable()),
    };
    let capture: Capture = serde_json::from_slice(&bytes).map_err(|_| Failure::unavailable())?;
    if !matches(state, scope, descriptor, &capture) {
        return Err(Failure::new(409, "native_write_capture_changed"));
    }
    let path = directory.join(format!("{}.outcome.json", descriptor.command_id));
    let completed = match read_bytes(&path, 8192) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Lookup::Reserved),
        Err(_) => return Err(Failure::unavailable()),
    };
    let completed: Completed =
        serde_json::from_slice(&completed).map_err(|_| Failure::unavailable())?;
    if completed.capture_sha256 != sha(&bytes) {
        return Err(Failure::unavailable());
    }
    Ok(Lookup::Done(completed.outcome))
}
