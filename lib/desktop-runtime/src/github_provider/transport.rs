use super::{Failure, Result, API_VERSION};
use reqwest::{header, redirect, Client as Http};
use serde_json::Value;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::thread::{self, JoinHandle};
use std::time::Duration;
use tokio::sync::{mpsc, oneshot, watch, OwnedSemaphorePermit, Semaphore};
use tokio::task::JoinSet;
use zeroize::Zeroizing;

const MAX_REQUESTS: usize = 16;
const TIMEOUT: Duration = Duration::from_secs(8);
pub(crate) struct Response {
    pub value: Value,
    pub next: u32,
}
struct Wire {
    status: u16,
    headers: header::HeaderMap,
    bytes: Zeroizing<Vec<u8>>,
    path: String,
}
enum Request {
    Api {
        path: String,
        query: Vec<(String, String)>,
        token: Zeroizing<Vec<u8>>,
    },
    OAuth {
        endpoint: &'static str,
        form: Zeroizing<String>,
    },
}
struct Completion {
    result: Result<Wire>,
    _permit: OwnedSemaphorePermit,
}
struct Job {
    request: Request,
    reply: oneshot::Sender<Completion>,
    cancelled: watch::Receiver<bool>,
    permit: OwnedSemaphorePermit,
}
struct Cancel(watch::Sender<bool>);
impl Drop for Cancel {
    fn drop(&mut self) {
        self.0.send_replace(true);
    }
}

/// Cloneable native handle; all operations are semantic and bounded. No URL or
/// arbitrary HTTP method is accepted by the public interface.
#[derive(Clone)]
pub struct Client {
    requests: mpsc::Sender<Job>,
    retired: watch::Sender<bool>,
    slots: Arc<Semaphore>,
    alive: Arc<AtomicBool>,
}
/// Headless owner. Stop/drop cancels and joins outstanding provider requests.
pub struct Owner {
    pub(crate) handle: Client,
    thread: Option<JoinHandle<()>>,
}
impl Owner {
    pub fn start() -> Result<Self> {
        Self::start_with_alive(Arc::new(AtomicBool::new(true)))
    }
    pub(crate) fn start_with_alive(alive: Arc<AtomicBool>) -> Result<Self> {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|_| Failure::unavailable())?;
        let http = runtime.block_on(async {
            Http::builder()
                .https_only(true)
                .redirect(redirect::Policy::none())
                .retry(reqwest::retry::never())
                .referer(false)
                .no_proxy()
                .connect_timeout(TIMEOUT)
                .timeout(TIMEOUT)
                .http1_max_headers(64)
                .pool_max_idle_per_host(2)
                .user_agent("soda-factory")
                .build()
                .map_err(|_| Failure::unavailable())
        })?;
        let (requests, mut inbox) = mpsc::channel::<Job>(MAX_REQUESTS);
        let (retired, mut retirement) = watch::channel(false);
        let handle = Client {
            requests,
            retired,
            slots: Arc::new(Semaphore::new(MAX_REQUESTS)),
            alive: alive.clone(),
        };
        let thread = thread::Builder::new().name("factory-github-session".into()).spawn(move || {
            runtime.block_on(async move {
                let mut tasks = JoinSet::new();
                loop {
                    tokio::select! {
                        biased;
                        _ = retirement.changed() => break,
                        Some(job) = inbox.recv() => { tasks.spawn(run(job, http.clone(), alive.clone())); }
                        _ = tasks.join_next(), if !tasks.is_empty() => {}
                    }
                }
                inbox.close();
                while let Ok(job) = inbox.try_recv() { drop(job); }
                tasks.shutdown().await;
            });
            // This runtime owns DNS/TLS/HTTP work too, so joining retires it all.
        }).map_err(|_| Failure::unavailable())?;
        Ok(Self {
            handle,
            thread: Some(thread),
        })
    }
    pub fn client(&self) -> Client {
        self.handle.clone()
    }
    pub fn stop(&mut self) -> Result<()> {
        self.handle.retire();
        if let Some(thread) = self.thread.take() {
            thread.join().map_err(|_| Failure::unavailable())?;
        }
        Ok(())
    }
    pub(crate) fn check_running(&self) -> Result<()> {
        if self.handle.alive.load(Ordering::SeqCst)
            && self
                .thread
                .as_ref()
                .is_none_or(|thread| thread.is_finished())
        {
            return Err(Failure::unavailable());
        }
        Ok(())
    }
}
impl Drop for Owner {
    fn drop(&mut self) {
        let _ = self.stop();
    }
}
impl Client {
    pub(crate) fn current(&self) -> Result<()> {
        if self.alive.load(Ordering::SeqCst) && !*self.retired.borrow() {
            Ok(())
        } else {
            Err(Failure::unavailable())
        }
    }
    pub(crate) fn retire(&self) {
        self.alive.store(false, Ordering::SeqCst);
        self.slots.close();
        self.retired.send_replace(true);
    }
    fn request(&self, request: Request) -> impl std::future::Future<Output = Result<Wire>> {
        let (reply, response) = oneshot::channel();
        let admission = (|| {
            self.current()?;
            let permit = self
                .slots
                .clone()
                .try_acquire_owned()
                .map_err(|_| Failure::unavailable())?;
            let (cancelled, observe) = watch::channel(false);
            self.requests
                .try_send(Job {
                    request,
                    reply,
                    cancelled: observe,
                    permit,
                })
                .map_err(|_| Failure::unavailable())?;
            Ok(Cancel(cancelled))
        })();
        let client = self.clone();
        async move {
            let _cancel = admission?;
            let completion = tokio::time::timeout(Duration::from_secs(9), response)
                .await
                .map_err(|_| Failure::new(504, "github_deadline"))?
                .map_err(|_| Failure::unavailable())?;
            client.current()?;
            completion.result
        }
    }
    pub(crate) async fn get(
        &self,
        path: &str,
        query: &[(&str, String)],
        token: &[u8],
    ) -> Result<Response> {
        if !path.starts_with('/')
            || path.starts_with("//")
            || path.len() > 2048
            || path
                .bytes()
                .any(|b| b.is_ascii_control() || matches!(b, b'?' | b'#' | b'\\'))
            || !secret(token)
        {
            return Err(Failure::invalid());
        }
        let wire = self
            .request(Request::Api {
                path: path.into(),
                query: query
                    .iter()
                    .map(|(k, v)| ((*k).into(), v.clone()))
                    .collect(),
                token: Zeroizing::new(token.to_vec()),
            })
            .await?;
        if wire.status != 200 {
            return Err(provider_failure(&wire));
        }
        let value: Value = serde_json::from_slice(&wire.bytes).map_err(|_| Failure::protocol())?;
        if value.is_null() {
            return Err(Failure::protocol());
        }
        let next = next_page(&wire.headers, &wire.path)?;
        Ok(Response { value, next })
    }
    pub(crate) async fn oauth(
        &self,
        endpoint: &'static str,
        form: Zeroizing<String>,
    ) -> Result<Zeroizing<Vec<u8>>> {
        if !matches!(endpoint, "/login/device/code" | "/login/oauth/access_token") {
            return Err(Failure::invalid());
        }
        let wire = self.request(Request::OAuth { endpoint, form }).await?;
        // OAuth errors may use either HTTP success or a refusal status. Decode
        // their bounded JSON in the operation owner; never forward provider prose.
        if !(200..=499).contains(&wire.status) {
            return Err(Failure::new(502, "device_authorization_failed"));
        }
        Ok(wire.bytes)
    }
    pub(crate) async fn delay(&self, duration: Duration) -> Result<()> {
        self.current()?;
        let mut retirement = self.retired.subscribe();
        tokio::select! { biased; _ = retirement.changed() => Err(Failure::unavailable()), _ = tokio::time::sleep(duration) => self.current() }
    }
}
async fn run(mut job: Job, http: Http, alive: Arc<AtomicBool>) {
    let result = if !alive.load(Ordering::SeqCst) {
        Err(Failure::unavailable())
    } else {
        tokio::select! {
            biased;
            _ = job.cancelled.changed() => Err(Failure::unavailable()),
            value = wire(&http, job.request) => value,
        }
    };
    let result = if alive.load(Ordering::SeqCst) {
        result
    } else {
        Err(Failure::unavailable())
    };
    let _ = job.reply.send(Completion {
        result,
        _permit: job.permit,
    });
}
async fn wire(http: &Http, request: Request) -> Result<Wire> {
    let (request, limit) = match request {
        Request::Api { path, query, token } => {
            let mut url = url::Url::parse(&format!("https://api.github.com{path}"))
                .map_err(|_| Failure::invalid())?;
            if !query.is_empty() {
                url.query_pairs_mut().extend_pairs(query);
            }
            let authorization_bytes = Zeroizing::new([b"Bearer ", token.as_slice()].concat());
            let mut authorization = header::HeaderValue::from_bytes(&authorization_bytes)
                .map_err(|_| Failure::invalid())?;
            authorization.set_sensitive(true);
            (
                http.get(url)
                    .header(header::AUTHORIZATION, authorization)
                    .header("X-GitHub-Api-Version", API_VERSION)
                    .header(header::ACCEPT, "application/vnd.github+json"),
                8 * 1024 * 1024,
            )
        }
        Request::OAuth { endpoint, form } => (
            http.post(format!("https://github.com{endpoint}"))
                .header(header::ACCEPT, "application/json")
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(form.as_bytes().to_vec()),
            16384,
        ),
    };
    let mut response = request.send().await.map_err(network_failure)?;
    if response
        .headers()
        .iter()
        .map(|(k, v)| k.as_str().len() + v.len() + 4)
        .sum::<usize>()
        > 16384
    {
        return Err(Failure::protocol());
    }
    let status = response.status().as_u16();
    let headers = response.headers().clone();
    let path = response.url().path().into();
    let mut bytes = Zeroizing::new(Vec::new());
    while let Some(chunk) = response.chunk().await.map_err(network_failure)? {
        if bytes.len() + chunk.len() > limit {
            return Err(Failure::new(413, "github_response_too_large"));
        }
        bytes.extend_from_slice(&chunk);
    }
    Ok(Wire {
        status,
        headers,
        bytes,
        path,
    })
}
fn network_failure(error: reqwest::Error) -> Failure {
    if error.is_timeout() {
        Failure::new(504, "github_deadline")
    } else {
        Failure::unavailable()
    }
}
pub(crate) fn secret(value: &[u8]) -> bool {
    (1..=8192).contains(&value.len()) && value.iter().all(|b| (33..=126).contains(b))
}
fn integer(headers: &header::HeaderMap, name: &str) -> Option<u64> {
    headers.get(name)?.to_str().ok()?.parse().ok()
}
fn provider_failure(wire: &Wire) -> Failure {
    let (status, code) = match wire.status {
        401 => (401, "github_sign_in_required"),
        404 => (404, "github_not_visible"),
        403 if integer(&wire.headers, "x-ratelimit-remaining") == Some(0)
            || wire.headers.contains_key("retry-after") =>
        {
            (429, "github_rate_limited")
        }
        403 => (403, "github_forbidden"),
        429 => (429, "github_rate_limited"),
        301 | 302 | 303 | 307 | 308 => (409, "github_resource_moved"),
        400 | 422 => (400, "invalid_native_read"),
        _ => (503, "github_unavailable"),
    };
    Failure {
        status,
        code,
        retry_after: integer(&wire.headers, "retry-after"),
        reset: integer(&wire.headers, "x-ratelimit-reset"),
    }
}
fn next_page(headers: &header::HeaderMap, path: &str) -> Result<u32> {
    let mut next = 0;
    for value in headers.get_all(header::LINK) {
        for link in value.to_str().map_err(|_| Failure::protocol())?.split(',') {
            let mut pieces = link.trim().split(';');
            let target = pieces.next().ok_or_else(Failure::protocol)?.trim();
            if !pieces.any(|piece| piece.trim() == "rel=\"next\"") {
                continue;
            }
            let target = target
                .strip_prefix('<')
                .and_then(|v| v.strip_suffix('>'))
                .ok_or_else(Failure::protocol)?;
            let url = url::Url::parse(target).map_err(|_| Failure::protocol())?;
            if url.scheme() != "https"
                || url.host_str() != Some("api.github.com")
                || url.port().is_some()
                || !url.username().is_empty()
                || url.password().is_some()
                || url.fragment().is_some()
                || url.path() != path
                || next != 0
            {
                return Err(Failure::protocol());
            }
            let pages: Vec<_> = url
                .query_pairs()
                .filter(|(k, _)| k == "page")
                .map(|(_, v)| v.into_owned())
                .collect();
            if pages.len() != 1 {
                return Err(Failure::protocol());
            }
            next = pages[0]
                .parse::<u32>()
                .ok()
                .filter(|page| (1..=1_000_000).contains(page) && page.to_string() == pages[0])
                .ok_or_else(Failure::protocol)?;
        }
    }
    Ok(next)
}
