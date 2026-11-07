//! Exact-run MCP forwarding port. Go owns protocol and product admission;
//! Rust owns its delivery and cancellation within the isolated attempt.
use std::collections::BTreeSet;
use std::future::Future;
use std::io;
use std::pin::Pin;
use std::sync::Arc;
use std::time::{Duration, Instant};

pub(super) const MAX_MCP_BODY: usize = 64 * 1024 + 8192;
pub(super) const MAX_MCP_REPLY: usize = 1024 * 1024;
pub(super) const MAX_MCP_CALLS: usize = 256;

pub struct WorkerMcpReply {
    pub status: u16,
    pub body: Vec<u8>,
}

pub(super) fn bounded_reply(reply: Option<WorkerMcpReply>) -> WorkerMcpReply {
    match reply {
        Some(reply)
            if reply.status == 200
                && reply.body.len() <= MAX_MCP_REPLY
                && serde_json::from_slice::<serde_json::Value>(&reply.body)
                    .is_ok_and(|value| value.is_object()) =>
        {
            reply
        }
        Some(reply) if reply.status == 202 && reply.body.is_empty() => reply,
        Some(reply) if matches!(reply.status, 400 | 401 | 403 | 410 | 429) => WorkerMcpReply {
            status: reply.status,
            body: br#"{"error":"factory_mcp_refused"}"#.to_vec(),
        },
        _ => WorkerMcpReply {
            status: 503,
            body: br#"{"error":"factory_mcp_unavailable"}"#.to_vec(),
        },
    }
}

/// One loopback MCP adapter for the CLI connector. The listener and forwarding
/// futures retire together; the CLI supplies no upstream destination or scope.
pub(super) struct McpOwner {
    pub configuration: soda_execution_protocol::MuseMcp,
    stop: tokio::sync::watch::Sender<bool>,
    thread: Option<std::thread::JoinHandle<io::Result<()>>>,
}

impl McpOwner {
    pub fn start(mcp: WorkerMcp, deadline: Instant) -> io::Result<Self> {
        let listener = std::net::TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, 0))?;
        listener.set_nonblocking(true)?;
        let port = listener.local_addr()?.port();
        let mut random = [0u8; 32];
        getrandom::fill(&mut random).map_err(io::Error::other)?;
        let authorization: String = random.iter().map(|b| format!("{b:02x}")).collect();
        let configuration = soda_execution_protocol::MuseMcp {
            port,
            authorization: authorization.clone(),
            tools: mcp.tools.iter().cloned().collect(),
        };
        let expected = zeroize::Zeroizing::new(format!("Bearer {authorization}"));
        let mcp = Arc::new(mcp);
        let calls = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let (stop, mut stopped) = tokio::sync::watch::channel(false);
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?;
        let thread=std::thread::Builder::new().name("factory-muse-mcp".into()).spawn(move||runtime.block_on(async move {
            use http_body_util::{BodyExt,Full,Limited};
            use hyper::{Request,Response,body::{Bytes,Incoming},server::conn::http1,service::service_fn};
            use hyper_util::rt::TokioIo;
            let listener=tokio::net::TcpListener::from_std(listener)?;
            let expected=Arc::new(expected);
            let mut tasks=tokio::task::JoinSet::new();
            let mut accepted=0usize;
            let result=loop {
                tokio::select! {
                    biased;
                    _=stopped.changed()=>break Ok(()),
                    _=tokio::time::sleep_until(tokio::time::Instant::from_std(deadline))=>break Ok(()),
                    incoming=listener.accept()=>{
                        let (stream,peer)=match incoming {Ok(value)=>value,Err(e)=>break Err(e)};
                        accepted+=1;
                        if !peer.ip().is_loopback() || accepted>512 || tasks.len()>=4 {continue;}
                        let mcp=mcp.clone(); let expected=expected.clone();let calls=calls.clone();
                        tasks.spawn(async move {
                            let service=service_fn(move|request:Request<Incoming>| {
                                let mcp=mcp.clone();let expected=expected.clone();let calls=calls.clone();
                                async move {
                                    let admitted=request.method()==hyper::Method::POST && request.uri().path()=="/mcp"
                                        && request.uri().query().is_none()
                                        && request.headers().get_all("authorization").iter().count()==1
                                        && request.headers().get("authorization").is_some_and(|value|value.as_bytes()==expected.as_bytes());
                                    let reply=if !admitted {WorkerMcpReply {status:403,body:Vec::new()}}
                                    else if calls.fetch_add(1,std::sync::atomic::Ordering::SeqCst)>=MAX_MCP_CALLS {WorkerMcpReply {status:429,body:Vec::new()}}
                                    else {
                                        match Limited::new(request.into_body(),MAX_MCP_BODY).collect().await {
                                            Ok(body)=>{
                                                let body=body.to_bytes().to_vec();
                                                if !serde_json::from_slice::<serde_json::Value>(&body).is_ok_and(|value|value.is_object()) {WorkerMcpReply {status:400,body:Vec::new()}}
                                                else {bounded_reply((mcp.forward)(body).await.ok())}
                                            },
                                            Err(_)=>WorkerMcpReply {status:400,body:Vec::new()},
                                        }
                                    };
                                    let reply=bounded_reply(Some(reply));
                                    let response=Response::builder().status(reply.status)
                                        .header("Content-Type","application/json").header("Cache-Control","no-store")
                                        .body(Full::new(Bytes::from(reply.body))).map_err(io::Error::other)?;
                                    Ok::<_,io::Error>(response)
                                }
                            });
                            let duration=deadline.saturating_duration_since(Instant::now()).min(Duration::from_secs(30));
                            let _=tokio::time::timeout(duration,http1::Builder::new().keep_alive(false).max_headers(32).max_buf_size(16384)
                                .serve_connection(TokioIo::new(stream),service)).await;
                        });
                    },
                    _=tasks.join_next(),if !tasks.is_empty()=>{},
                }
            };
            drop(listener); tasks.shutdown().await; result
        }))?;
        Ok(Self {
            configuration,
            stop,
            thread: Some(thread),
        })
    }
    pub fn stop(&mut self) -> io::Result<()> {
        let _ = self.stop.send(true);
        self.thread.take().map_or(Ok(()), |thread| {
            thread
                .join()
                .unwrap_or_else(|_| Err(io::Error::other("Muse MCP owner failed")))
        })
    }
}
impl Drop for McpOwner {
    fn drop(&mut self) {
        let _ = self.stop();
    }
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
