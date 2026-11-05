//! Exact-run MCP forwarding port. Go owns protocol and product admission;
//! Rust owns its delivery and cancellation within the isolated attempt.
use std::collections::BTreeSet;
use std::future::Future;
use std::io;
use std::pin::Pin;

pub struct WorkerMcpReply {
    pub status: u16,
    pub body: Vec<u8>,
}

type Reply = Pin<Box<dyn Future<Output = io::Result<WorkerMcpReply>> + Send>>;

/// Construct from the trusted Go execution binding. The closure captures only
/// that run's admitted endpoint and capability; workers supply no destination,
/// secret, profile, instance or capability selector. Its future must cancel
/// its underlying I/O when dropped and must not spawn detached effects.
pub struct WorkerMcp {
    pub(super) run: String,
    pub(super) tools: BTreeSet<String>,
    pub(super) forward: Box<dyn Fn(Vec<u8>) -> Reply + Send + Sync>,
}

impl WorkerMcp {
    /// `tools` is the canonical Go catalog for this capability, captured before
    /// launch (for example `factory.context`), never a worker discovery result.
    /// The aliases adapt that catalog to Muse's `mcp__factory` namespace.
    pub fn new<F, Fut>(run: &str, tools: &[String], forward: F) -> io::Result<Self>
    where
        F: Fn(Vec<u8>) -> Fut + Send + Sync + 'static,
        Fut: Future<Output = io::Result<WorkerMcpReply>> + Send + 'static,
    {
        if !super::hex(run, 32) {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "worker MCP run invalid",
            ));
        }
        let mut aliases = BTreeSet::new();
        if tools.is_empty()
            || tools.len() > 128
            || tools.iter().any(|tool| {
                let Some((namespace, name)) = tool.split_once('.') else {
                    return true;
                };
                !matches!(namespace, "factory" | "systemone")
                    || name.is_empty()
                    || name.len() > 96
                    || !name
                        .bytes()
                        .all(|byte| byte.is_ascii_lowercase() || byte == b'_')
                    || !aliases.insert(format!("{namespace}_{name}"))
            })
        {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "worker MCP catalog invalid",
            ));
        }
        Ok(Self {
            run: run.into(),
            tools: aliases,
            forward: Box::new(move |body| Box::pin(forward(body))),
        })
    }
}
