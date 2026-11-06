//! Native-only provider request admission for one broker-attested Factory run.
//! The eventual desktop owner applies this before borrowing an OS credential;
//! no worker-supplied URL, model, tool namespace or spend limit is authority.
mod attempt;
mod broker;
mod candidate;
mod custody;
mod executions;
mod intent;
pub(crate) mod launch;
mod mcp;
mod observation;
mod owner;
pub(crate) mod port;
pub(crate) mod preparations;
mod receipt;
pub(crate) mod recovery;
mod response;
mod run;
mod start;
mod tool;
pub use attempt::{AttemptObservation, AttemptPhase, RunExecution, RunView};
pub use broker::{
    AgentCancellation, AgentProcess, AgentRun, AgentStarted, Broker, RelayAttestation,
};
pub use custody::{read_custody, CustodyObservation, CustodyState, RunCustody};
pub use executions::{
    ExecutionBinding, ExecutionOutcome, ExecutionTicket, Executions, RetirementReport,
};
pub use intent::{read_intent, LaunchIntent};
pub use mcp::{WorkerMcp, WorkerMcpReply};
pub use observation::{AgentObservation, AgentObserver};
pub use owner::{BoundOwner, Owner};
pub use receipt::{read_retained, retire_recovered_receipt, write_retained, ReceiptScope};
pub use recovery::{NativeRecovery, NativeRecoveryEvidence};
pub use run::{RunOwner, RunStart};
use serde_json::Value;
pub use start::ExecutionStart;
use std::collections::BTreeSet;
use std::time::{Duration, Instant};
pub use tool::{ToolSelection, Tools};

const MAX_RUN: Duration = Duration::from_secs(4 * 60 * 60);
const MAX_REQUEST_BYTES: usize = 256 * 1024;
const MAX_INPUT_BYTES: usize = 2 * 1024 * 1024;
const MAX_REQUESTS: u8 = 16;
const MAX_OUTPUT_TOKENS: u64 = 65_536;
const MAX_OUTPUT_PER_REQUEST: u64 = 4_096;
pub const MODEL: &str = "muse-spark-1.3-contributor";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Role {
    Reviewer,
    Coder,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Route {
    Models,
    Responses,
}
impl Route {
    pub fn upstream(self) -> &'static str {
        match self {
            Self::Models => "https://api.meta.ai/muse-code/models",
            Self::Responses => "https://api.meta.ai/v1/responses",
        }
    }
}

pub struct Admitted {
    pub route: Route,
    pub body: Vec<u8>,
    pub output_tokens: u64,
    pub offered_tools: BTreeSet<OfferedTool>,
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct OfferedTool {
    pub namespace: String,
    pub name: String,
}

pub struct Budget {
    calls: u8,
    input_bytes: usize,
    output_tokens: u64,
}
impl Budget {
    pub fn new(calls: u8, input_bytes: usize, output_tokens: u64) -> Result<Self, Error> {
        if !(1..=MAX_REQUESTS).contains(&calls)
            || !(1..=MAX_INPUT_BYTES).contains(&input_bytes)
            || !(16..=MAX_OUTPUT_TOKENS).contains(&output_tokens)
        {
            return Err(Error::Invalid);
        }
        Ok(Self {
            calls,
            input_bytes,
            output_tokens,
        })
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Error {
    Invalid,
    Retired,
    Budget,
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Self::Invalid => "provider request outside admitted run policy",
            Self::Retired => "provider run retired",
            Self::Budget => "provider run budget exhausted",
        })
    }
}
impl std::error::Error for Error {}

pub struct Policy {
    run: String,
    profile: String,
    sha256: String,
    candidate: String,
    connection: String,
    role: Role,
    deadline: Instant,
    calls_left: u8,
    input_left: usize,
    output_left: u64,
    model_reads_left: u8,
    retired: bool,
    mcp_tools: BTreeSet<String>,
    harness_profile: Option<soda_execution_protocol::HarnessProfile>,
}
impl Policy {
    /// Values come from a trusted run admission, never an IPC request body.
    pub fn new(
        run: &str,
        profile: &str,
        sha256: &str,
        candidate: &str,
        connection: &str,
        role: Role,
        duration: Duration,
        budget: Budget,
    ) -> Result<Self, Error> {
        if !hex(run, 32)
            || !hex(profile, 32)
            || !hex(sha256, 64)
            || !hex(candidate, 40)
            || !hex(connection, 32)
            || duration.is_zero()
            || duration > MAX_RUN
        {
            return Err(Error::Invalid);
        }
        Ok(Self {
            run: run.into(),
            profile: profile.into(),
            sha256: sha256.into(),
            candidate: candidate.into(),
            connection: connection.into(),
            role,
            deadline: Instant::now() + duration,
            calls_left: budget.calls,
            input_left: budget.input_bytes,
            output_left: budget.output_tokens,
            model_reads_left: 2,
            retired: false,
            mcp_tools: BTreeSet::new(),
            harness_profile: None,
        })
    }

    /// Capture the defining Go catalog carried by the trusted exact-run port.
    /// This does not infer role authority from a worker's advertised schemas.
    pub fn bind_mcp(&mut self, mcp: &WorkerMcp) -> Result<(), Error> {
        if self.run != mcp.run || !self.mcp_tools.is_empty() {
            return Err(Error::Invalid);
        }
        self.mcp_tools = mcp.tools.clone();
        Ok(())
    }

    pub fn run(&self) -> &str {
        &self.run
    }

    pub fn profile(&self) -> &str {
        &self.profile
    }

    pub fn role(&self) -> Role {
        self.role
    }

    pub fn sha256(&self) -> &str {
        &self.sha256
    }

    pub fn candidate(&self) -> &str {
        &self.candidate
    }

    pub fn connection(&self) -> &str {
        &self.connection
    }

    pub fn remaining(&self) -> Result<Duration, Error> {
        if self.retired {
            return Err(Error::Retired);
        }
        self.deadline
            .checked_duration_since(Instant::now())
            .filter(|duration| !duration.is_zero())
            .ok_or(Error::Retired)
    }

    pub fn retire(&mut self) {
        self.retired = true;
    }

    pub fn admit(&mut self, method: &str, path: &str, body: &[u8]) -> Result<Admitted, Error> {
        self.remaining()?;
        match (method, path) {
            ("GET", "/muse-code/models") if body.is_empty() => {
                if self.model_reads_left == 0 {
                    return Err(Error::Budget);
                }
                self.model_reads_left -= 1;
                Ok(Admitted {
                    route: Route::Models,
                    body: Vec::new(),
                    output_tokens: 0,
                    offered_tools: BTreeSet::new(),
                })
            }
            ("POST", "/responses" | "/v1/responses") => self.admit_response(body),
            _ => Err(Error::Invalid),
        }
    }

    fn admit_response(&mut self, body: &[u8]) -> Result<Admitted, Error> {
        if body.is_empty() || body.len() > MAX_REQUEST_BYTES {
            return Err(Error::Invalid);
        }
        if self.calls_left == 0 || body.len() > self.input_left || self.output_left < 16 {
            return Err(Error::Budget);
        }
        let mut request: Value = serde_json::from_slice(body).map_err(|_| Error::Invalid)?;
        let object = request.as_object_mut().ok_or(Error::Invalid)?;
        const FIELDS: &[&str] = &[
            "include",
            "input",
            "instructions",
            "max_output_tokens",
            "model",
            "parallel_tool_calls",
            "prompt_cache_key",
            "reasoning",
            "store",
            "stream",
            "tools",
        ];
        if object.keys().any(|key| !FIELDS.contains(&key.as_str()))
            || object.get("model").and_then(Value::as_str) != Some(MODEL)
            || object.get("stream") != Some(&Value::Bool(true))
            || object.get("store") != Some(&Value::Bool(false))
            || !object
                .get("input")
                .is_some_and(|value| value.is_array() || value.is_string())
            || !object.get("instructions").is_some_and(Value::is_string)
            || !object.get("reasoning").is_some_and(Value::is_object)
            || !object
                .get("prompt_cache_key")
                .is_some_and(|value| value.as_str().is_some_and(|key| key.len() <= 256))
        {
            return Err(Error::Invalid);
        }
        let requested = object
            .get("max_output_tokens")
            .and_then(Value::as_u64)
            .filter(|tokens| *tokens >= 16)
            .ok_or(Error::Invalid)?;
        if let Some(profile) = &self.harness_profile {
            if object
                .get("reasoning")
                .and_then(|value| value.get("effort"))
                .and_then(Value::as_str)
                != Some(profile.effort.as_str())
            {
                return Err(Error::Invalid);
            }
        }
        let granted = requested.min(MAX_OUTPUT_PER_REQUEST).min(self.output_left);
        let include = object
            .get_mut("include")
            .and_then(Value::as_array_mut)
            .ok_or(Error::Invalid)?;
        if include.iter().any(|value| !value.is_string()) {
            return Err(Error::Invalid);
        }
        include.retain(|value| value.as_str() == Some("reasoning.encrypted_content"));
        let tools = object
            .get_mut("tools")
            .and_then(Value::as_array_mut)
            .ok_or(Error::Invalid)?;
        if tools.is_empty() || tools.len() > 2 {
            return Err(Error::Invalid);
        }
        let mut namespaces = BTreeSet::new();
        let mut offered_tools = BTreeSet::new();
        for namespace in tools {
            let fields = namespace.as_object().ok_or(Error::Invalid)?;
            let name = fields
                .get("name")
                .and_then(Value::as_str)
                .ok_or(Error::Invalid)?
                .to_owned();
            if fields.get("type").and_then(Value::as_str) != Some("namespace")
                || !matches!(name.as_str(), "muse" | "mcp__factory")
                || !namespaces.insert(name.clone())
                || fields
                    .keys()
                    .any(|key| !["type", "name", "description", "tools"].contains(&key.as_str()))
                || !fields.get("description").is_some_and(Value::is_string)
            {
                return Err(Error::Invalid);
            }
            let nested = namespace
                .get_mut("tools")
                .and_then(Value::as_array_mut)
                .ok_or(Error::Invalid)?;
            let mut names = BTreeSet::new();
            if nested.len() > if name == "muse" { 32 } else { 128 }
                || nested.iter().any(|tool| {
                    let Some(function) = tool.as_object() else {
                        return true;
                    };
                    function.keys().any(|key| {
                        !["type", "name", "description", "parameters", "strict"]
                            .contains(&key.as_str())
                    }) || function.get("type").and_then(Value::as_str) != Some("function")
                        || !function.get("description").is_some_and(Value::is_string)
                        || !function.get("parameters").is_some_and(Value::is_object)
                        || function
                            .get("strict")
                            .is_some_and(|value| !value.is_boolean())
                        || !function
                            .get("name")
                            .and_then(Value::as_str)
                            .is_some_and(|name| names.insert(name.to_owned()))
                })
            {
                return Err(Error::Invalid);
            }
            if name == "muse" {
                nested.retain(|tool| {
                    tool.get("name")
                        .and_then(Value::as_str)
                        .is_some_and(|name| allowed(self.role, name))
                });
            } else if self.mcp_tools.is_empty() || names != self.mcp_tools {
                return Err(Error::Invalid);
            }
            offered_tools.extend(
                nested
                    .iter()
                    .filter_map(|tool| tool.get("name").and_then(Value::as_str))
                    .map(|tool| OfferedTool {
                        namespace: name.clone(),
                        name: tool.to_owned(),
                    }),
            );
        }
        if !namespaces.contains("muse")
            || namespaces.contains("mcp__factory") != !self.mcp_tools.is_empty()
        {
            return Err(Error::Invalid);
        }
        // Keep provider cache affinity inside this admitted run. The isolated
        // worker may ask for a key, but it cannot link another run's context.
        object.insert("prompt_cache_key".into(), Value::from(self.run.clone()));
        object.insert("max_output_tokens".into(), Value::from(granted));
        object.insert("parallel_tool_calls".into(), Value::Bool(false));
        let output = serde_json::to_vec(&request).map_err(|_| Error::Invalid)?;
        if output.len() > MAX_REQUEST_BYTES {
            return Err(Error::Invalid);
        }
        self.calls_left -= 1;
        self.input_left -= body.len();
        self.output_left -= granted;
        Ok(Admitted {
            route: Route::Responses,
            body: output,
            output_tokens: granted,
            offered_tools,
        })
    }
}

fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn allowed(role: Role, tool: &str) -> bool {
    match role {
        Role::Reviewer => matches!(tool, "read_file" | "search"),
        Role::Coder => matches!(
            tool,
            "read_file" | "search" | "write_file" | "edit_file" | "bash" | "bash_input"
        ),
    }
}
