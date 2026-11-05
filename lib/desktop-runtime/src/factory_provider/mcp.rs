//! Exact-run MCP forwarding port. Go owns protocol and product admission;
//! Rust owns its delivery and cancellation within the isolated attempt.
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
    pub(super) forward: Box<dyn Fn(Vec<u8>) -> Reply + Send + Sync>,
}

impl WorkerMcp {
    pub fn new<F, Fut>(run: &str, forward: F) -> io::Result<Self>
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
        Ok(Self {
            run: run.into(),
            forward: Box::new(move |body| Box::pin(forward(body))),
        })
    }
}
