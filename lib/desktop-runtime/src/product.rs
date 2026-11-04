//! Bounded product I/O has its own joined runtime, independent of child control.
use crate::supervisor::NativeChannel;
use soda_unix_http::{request_authenticated_async, Limits, Response};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    mpsc as blocking, Arc,
};
use std::thread::{self, JoinHandle};
use std::time::Duration;
use tokio::sync::{mpsc, oneshot, watch, OwnedSemaphorePermit, Semaphore};
use tokio::task::JoinSet;

type Result<T> = std::result::Result<T, String>;
const MAX_REQUESTS: usize = 16;
const WAIT: Duration = Duration::from_secs(20);
struct Completion {
    result: Result<Response>,
    // Keep admission bounded even when a caller does not poll its response.
    _permit: OwnedSemaphorePermit,
}
enum Reply {
    Async(oneshot::Sender<Completion>),
    Blocking(blocking::Sender<Completion>),
}
impl Reply {
    fn send(self, completion: Completion) {
        match self {
            Self::Async(reply) => {
                let _ = reply.send(completion);
            }
            Self::Blocking(reply) => {
                let _ = reply.send(completion);
            }
        }
    }
}
struct Job {
    method: String,
    target: String,
    body: Vec<u8>,
    operation: Option<String>,
    reply: Reply,
    cancelled: watch::Receiver<bool>,
    permit: OwnedSemaphorePermit,
}
// Dropping a caller's future (including an unpolled one) cancels its request.
struct Cancel(watch::Sender<bool>);
impl Drop for Cancel {
    fn drop(&mut self) {
        self.0.send_replace(true);
    }
}
#[derive(Clone)]
pub(crate) struct Products {
    requests: mpsc::Sender<Job>,
    retired: watch::Sender<bool>,
    slots: Arc<Semaphore>,
    alive: Arc<AtomicBool>,
}
pub(crate) struct Owner {
    pub handle: Products,
    thread: Option<JoinHandle<()>>,
}
impl Owner {
    pub fn check_running(&self) -> Result<()> {
        if self.handle.alive.load(Ordering::SeqCst)
            && self
                .thread
                .as_ref()
                .is_none_or(|thread| thread.is_finished())
        {
            return Err("native request owner stopped".into());
        }
        Ok(())
    }
    pub fn start(channel: NativeChannel, alive: Arc<AtomicBool>) -> Result<Self> {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_io()
            .enable_time()
            .build()
            .map_err(|_| "native request runtime could not start")?;
        let (requests, mut inbox) = mpsc::channel::<Job>(MAX_REQUESTS);
        let (retired, mut retirement) = watch::channel(false);
        let handle = Products {
            requests,
            retired,
            slots: Arc::new(Semaphore::new(MAX_REQUESTS)),
            alive: alive.clone(),
        };
        let thread = thread::Builder::new().name("factory-native-requests".into()).spawn(move || {
            runtime.block_on(async move {
                let mut tasks = JoinSet::new();
                loop {
                    tokio::select! {
                        biased;
                        _ = retirement.changed() => break,
                        Some(job) = inbox.recv() => { tasks.spawn(run(job, channel.clone(), alive.clone())); }
                        _ = tasks.join_next(), if !tasks.is_empty() => {}
                    }
                }
                inbox.close();
                while let Ok(job) = inbox.try_recv() { drop(job); }
                tasks.shutdown().await;
            });
            // Dropping this current-thread runtime also retires its HTTP drivers
            // before the native owner may stop Go and PostgreSQL.
        }).map_err(|_| "native request owner could not start")?;
        Ok(Self {
            handle,
            thread: Some(thread),
        })
    }
    pub fn stop(&mut self) -> Result<()> {
        self.handle.retire();
        if let Some(thread) = self.thread.take() {
            thread.join().map_err(|_| "native request owner failed")?;
        }
        Ok(())
    }
}
impl Drop for Owner {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}
impl Products {
    pub fn retire(&self) {
        self.alive.store(false, Ordering::SeqCst);
        self.slots.close();
        self.retired.send_replace(true);
    }
    fn submit(
        &self,
        method: &str,
        target: &str,
        body: &[u8],
        operation: Option<&str>,
        reply: Reply,
    ) -> Result<Cancel> {
        if !(matches!(method, "GET" | "POST") || operation.is_some() && method == "PUT")
            || target.len() > 2048
            || !(target.starts_with("/api/factory/")
                || operation.is_some() && target.starts_with("/api/repositories/"))
            || target.bytes().any(|b| b.is_ascii_control())
            || body.len() > 65536
        {
            return Err("unsupported native product request".into());
        }
        if !self.alive.load(Ordering::SeqCst) {
            return Err("native runtime is retired".into());
        }
        let permit = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| "native request limit or retirement reached")?;
        let (cancelled, observe) = watch::channel(false);
        self.requests
            .try_send(Job {
                method: method.into(),
                target: target.into(),
                body: body.into(),
                operation: operation.map(str::to_owned),
                reply,
                cancelled: observe,
                permit,
            })
            .map_err(|_| "native request admission unavailable")?;
        Ok(Cancel(cancelled))
    }
    pub fn request(
        &self,
        method: &str,
        target: &str,
        body: &[u8],
    ) -> impl std::future::Future<Output = Result<Response>> {
        self.request_scoped(method, target, body, None)
    }
    pub(crate) fn admitted_request(
        &self,
        method: &str,
        target: &str,
        body: &[u8],
        operation: &str,
    ) -> impl std::future::Future<Output = Result<Response>> {
        self.request_scoped(method, target, body, Some(operation))
    }
    fn request_scoped(
        &self,
        method: &str,
        target: &str,
        body: &[u8],
        operation: Option<&str>,
    ) -> impl std::future::Future<Output = Result<Response>> {
        let (reply, response) = oneshot::channel();
        let admission = self.submit(method, target, body, operation, Reply::Async(reply));
        let alive = self.alive.clone();
        async move {
            let _cancel = admission?;
            let completion = tokio::time::timeout(WAIT, response)
                .await
                .map_err(|_| "native product request timed out")?
                .map_err(|_| "native product request retired")?;
            if !alive.load(Ordering::SeqCst) {
                return Err("native runtime is retired".into());
            }
            completion.result
        }
    }
    pub fn blocking(&self, method: &str, target: &str, body: &[u8]) -> Result<Response> {
        let (reply, response) = blocking::channel();
        let _cancel = self.submit(method, target, body, None, Reply::Blocking(reply))?;
        let completion = response
            .recv_timeout(WAIT)
            .map_err(|_| "native product request retired or timed out")?;
        if !self.alive.load(Ordering::SeqCst) {
            return Err("native runtime is retired".into());
        }
        completion.result
    }
}
async fn run(mut job: Job, channel: NativeChannel, alive: Arc<AtomicBool>) {
    let mut headers = vec![("X-Soda-Native-Instance", channel.instance.as_str())];
    if let Some(operation) = &job.operation {
        headers.push(("X-Soda-Native-Operation", operation));
    }
    let result = if !alive.load(Ordering::SeqCst) {
        Err("native runtime is retired".into())
    } else {
        tokio::select! {
            biased;
            _ = job.cancelled.changed() => Err("native product request cancelled".into()),
            reply = request_authenticated_async(&channel.socket, &job.method, &job.target, "soda-native", &job.body, &channel.key,
                &headers, Duration::from_secs(15), Limits {header_bytes:16384,body_bytes:2*1024*1024}) =>
                reply.map_err(|_| "native product response is unavailable".into()),
        }
    };
    let result = if alive.load(Ordering::SeqCst) {
        result
    } else {
        Err("native runtime is retired".into())
    };
    job.reply.send(Completion {
        result,
        _permit: job.permit,
    });
}
