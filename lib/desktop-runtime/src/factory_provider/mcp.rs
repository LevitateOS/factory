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
    /// The private host route is fixed; neither workers nor Go supply a URL.
    /// Native launch composition captures these values after Go admission.
    pub(crate) fn connected(
        channel: crate::supervisor::NativeChannel,
        intent: &super::LaunchIntent,
        tools: &[String],
        capability: &str,
        secret: &str,
    ) -> io::Result<Self> {
        if capability != intent.run()
            || !super::hex(secret, 64)
            || channel.instance != intent.instance()
            || channel.profile != intent.scope()?.profile
        {
            return Err(io::Error::new(
                io::ErrorKind::PermissionDenied,
                "native worker binding invalid",
            ));
        }
        let raw = intent.raw().to_owned();
        let capability = capability.to_owned();
        let secret = zeroize::Zeroizing::new(secret.to_owned());
        Self::new(intent.scope()?.run, tools, move |body| {
            let channel = channel.clone();
            let raw = raw.clone();
            let capability = capability.clone();
            let secret = secret.clone();
            async move {
                let payload: serde_json::Value =
                    serde_json::from_slice(&body).map_err(io::Error::other)?;
                let encoded=serde_json::to_vec(&serde_json::json!({"intent":raw,"capability":capability,"secret":secret.as_str(),"payload":payload})).map_err(io::Error::other)?;
                let response = soda_unix_http::request_authenticated_async(
                    &channel.socket,
                    "POST",
                    "/api/native/execution/mcp",
                    "soda-native",
                    &encoded,
                    &channel.key,
                    &[("X-Soda-Native-Instance", channel.instance.as_str())],
                    std::time::Duration::from_secs(20),
                    soda_unix_http::Limits {
                        header_bytes: 8192,
                        body_bytes: 1024 * 1024,
                    },
                )
                .await
                .map_err(|_| io::Error::other("native worker MCP unavailable"))?;
                Ok(WorkerMcpReply {
                    status: response.status,
                    body: response.body,
                })
            }
        })
    }
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
