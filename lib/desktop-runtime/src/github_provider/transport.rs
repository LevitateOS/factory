use super::{Failure, Result, API_VERSION};
use reqwest::{header, redirect, Client as Http};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};
use tokio::sync::{mpsc, oneshot, watch, OwnedSemaphorePermit, Semaphore};
use tokio::task::JoinSet;
use zeroize::Zeroizing;

const MAX_REQUESTS: usize = 16;
const TIMEOUT: Duration = Duration::from_secs(8);
pub(crate) struct Response {
    pub value: Value,
    pub next: u32,
    pub last: u32,
}
pub(super) struct MutationResponse {
    pub value: Value,
    pub request_id: Option<String>,
    pub response_sha256: String,
}
struct Wire {
    status: u16,
    headers: header::HeaderMap,
    bytes: Zeroizing<Vec<u8>>,
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
    Comment {
        node: String,
        body: String,
        token: Zeroizing<Vec<u8>>,
    },
    IssueCreate {
        node: String,
        title: String,
        body: String,
        token: Zeroizing<Vec<u8>>,
    },
    State {
        node: String,
        mutation: super::states::StateMutation,
        token: Zeroizing<Vec<u8>>,
    },
    Edit {
        node: String,
        pull: bool,
        title: Option<String>,
        body: Option<String>,
        token: Zeroizing<Vec<u8>>,
    },
    Lock {
        node: String,
        lock: bool,
        reason: Option<String>,
        token: Zeroizing<Vec<u8>>,
    },
    Threads {
        query: super::review_threads::ThreadQuery,
        token: Zeroizing<Vec<u8>>,
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
    pub(super) mutations: Arc<tokio::sync::Mutex<Instant>>,
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
            mutations: Arc::new(tokio::sync::Mutex::new(Instant::now())),
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
    // Retire the whole admitted native operation as well as each wire request.
    // This includes waits for Go mapping and native conditional credential I/O.
    pub(crate) async fn scope<F: std::future::Future>(&self, work: F) -> Result<F::Output> {
        self.current()?;
        let mut retirement = self.retired.subscribe();
        let output = tokio::select! {biased; _=retirement.changed()=>return Err(Failure::unavailable()),value=work=>value};
        self.current()?;
        Ok(output)
    }
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
        let next = link_page(&wire.headers, "next")?;
        let last = link_page(&wire.headers, "last")?;
        self.current()?;
        Ok(Response { value, next, last })
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
        // Redirects and non-success bodies without an OAuth error cannot grant
        // credentials, even if their body otherwise resembles a complete grant.
        if wire.status != 200
            && (!(400..=499).contains(&wire.status)
                || !serde_json::from_slice::<Value>(&wire.bytes)
                    .ok()
                    .is_some_and(|body| {
                        body["error"].as_str().is_some_and(|code| !code.is_empty())
                    }))
        {
            return Err(Failure::new(502, "device_authorization_failed"));
        }
        Ok(wire.bytes)
    }
    // Fixed semantic endpoint; callers cannot select an HTTP method or URL.
    // Every failure after calling this is conservatively an uncertain effect.
    pub(super) async fn post_comment(
        &self,
        node: &str,
        body: &str,
        token: &[u8],
    ) -> Result<MutationResponse> {
        self.mutation(Request::Comment {
            node: node.into(),
            body: body.into(),
            token: Zeroizing::new(token.to_vec()),
        })
        .await
    }
    pub(super) async fn post_issue(
        &self,
        node: &str,
        title: &str,
        body: &str,
        token: &[u8],
    ) -> Result<MutationResponse> {
        self.mutation(Request::IssueCreate {
            node: node.into(),
            title: title.into(),
            body: body.into(),
            token: Zeroizing::new(token.to_vec()),
        })
        .await
    }
    pub(super) async fn post_state(
        &self,
        node: &str,
        mutation: super::states::StateMutation,
        token: &[u8],
    ) -> Result<MutationResponse> {
        self.mutation(Request::State {
            node: node.into(),
            mutation,
            token: Zeroizing::new(token.to_vec()),
        })
        .await
    }
    pub(super) async fn post_edit(
        &self,
        node: &str,
        pull: bool,
        title: Option<&str>,
        body: Option<&str>,
        token: &[u8],
    ) -> Result<MutationResponse> {
        self.mutation(Request::Edit {
            node: node.into(),
            pull,
            title: title.map(str::to_owned),
            body: body.map(str::to_owned),
            token: Zeroizing::new(token.to_vec()),
        })
        .await
    }
    pub(super) async fn post_lock(
        &self,
        node: &str,
        lock: bool,
        reason: Option<&str>,
        token: &[u8],
    ) -> Result<MutationResponse> {
        self.mutation(Request::Lock {
            node: node.into(),
            lock,
            reason: reason.map(str::to_owned),
            token: Zeroizing::new(token.to_vec()),
        })
        .await
    }
    pub(super) async fn thread_query(
        &self,
        query: super::review_threads::ThreadQuery,
        token: &[u8],
    ) -> Result<Value> {
        Ok(self
            .mutation(Request::Threads {
                query,
                token: Zeroizing::new(token.to_vec()),
            })
            .await?
            .value)
    }
    async fn mutation(&self, request: Request) -> Result<MutationResponse> {
        let read_query = matches!(&request, Request::Threads { .. });
        let wire = self.request(request).await?;
        if wire.status != 200 {
            if matches!(wire.status, 400 | 422) {
                return Err(Failure::new(
                    400,
                    if read_query {
                        "github_query_rejected"
                    } else {
                        "github_write_rejected"
                    },
                ));
            }
            return Err(provider_failure(&wire));
        }
        let request_ids = wire.headers.get_all("x-github-request-id");
        if request_ids.iter().count() > 1 {
            return Err(Failure::protocol());
        }
        let request_id = request_ids
            .iter()
            .next()
            .map(|value| {
                value
                    .to_str()
                    .ok()
                    .filter(|value| {
                        !value.is_empty()
                            && value.len() <= 256
                            && value.bytes().all(|byte| (33..=126).contains(&byte))
                    })
                    .map(str::to_owned)
                    .ok_or_else(Failure::protocol)
            })
            .transpose()?;
        let value: Value = serde_json::from_slice(&wire.bytes).map_err(|_| Failure::protocol())?;
        if value.get("errors").is_some_and(|errors| {
            !errors.is_null() && !errors.as_array().is_some_and(Vec::is_empty)
        }) {
            return Err(Failure::protocol());
        }
        Ok(MutationResponse {
            value,
            request_id,
            response_sha256: sha256(wire.bytes.as_slice()),
        })
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
        Request::Comment { node, body, token } => {
            if !super::request::node_id(&node) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            (
                graphql(
                    http,
                    &token,
                    super::comments::CREATE_COMMENT,
                    serde_json::json!({"subject":node,"body":body}),
                )?,
                1024 * 1024,
            )
        }
        Request::Threads { query, token } => {
            let (query, variables) = query.wire();
            (graphql(http, &token, &query, variables)?, 8 * 1024 * 1024)
        }
        Request::Lock {
            node,
            lock,
            reason,
            token,
        } => {
            if !super::request::node_id(&node) || !lock && reason.is_some() {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let mut input = serde_json::Map::new();
            input.insert("lockableId".into(), serde_json::json!(node));
            if let Some(reason) = reason {
                input.insert("lockReason".into(), serde_json::json!(reason));
            }
            (
                graphql(
                    http,
                    &token,
                    if lock {
                        super::locks::LOCK
                    } else {
                        super::locks::UNLOCK
                    },
                    serde_json::json!({"input":input}),
                )?,
                1024 * 1024,
            )
        }
        Request::Edit {
            node,
            pull,
            title,
            body,
            token,
        } => {
            if !super::request::node_id(&node) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            let mut input = serde_json::Map::new();
            input.insert(
                if pull { "pullRequestId" } else { "id" }.into(),
                serde_json::json!(node),
            );
            if let Some(title) = title {
                input.insert("title".into(), serde_json::json!(title));
            }
            if let Some(body) = body {
                input.insert("body".into(), serde_json::json!(body));
            }
            (
                graphql(
                    http,
                    &token,
                    if pull {
                        super::edits::EDIT_PULL
                    } else {
                        super::edits::EDIT_ISSUE
                    },
                    serde_json::json!({"input":input}),
                )?,
                1024 * 1024,
            )
        }
        Request::State {
            node,
            mutation,
            token,
        } => {
            if !super::request::node_id(&node) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            (
                graphql(
                    http,
                    &token,
                    mutation.query(),
                    serde_json::json!({"subject":node}),
                )?,
                1024 * 1024,
            )
        }
        Request::IssueCreate {
            node,
            title,
            body,
            token,
        } => {
            if !super::request::node_id(&node) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            (
                graphql(
                    http,
                    &token,
                    super::issue_create::CREATE_ISSUE,
                    serde_json::json!({"repository":node,"title":title,"body":body}),
                )?,
                1024 * 1024,
            )
        }
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
    })
}
fn network_failure(error: reqwest::Error) -> Failure {
    if error.is_timeout() {
        Failure::new(504, "github_deadline")
    } else {
        Failure::unavailable()
    }
}
fn graphql(
    http: &Http,
    token: &[u8],
    query: &str,
    variables: Value,
) -> Result<reqwest::RequestBuilder> {
    if !secret(token) {
        return Err(Failure::new(400, "invalid_native_write"));
    }
    let bytes = Zeroizing::new([b"Bearer ", token].concat());
    let mut authorization = header::HeaderValue::from_bytes(&bytes)
        .map_err(|_| Failure::new(400, "invalid_native_write"))?;
    authorization.set_sensitive(true);
    Ok(http
        .post("https://api.github.com/graphql")
        .header(header::AUTHORIZATION, authorization)
        .header("X-GitHub-Api-Version", API_VERSION)
        .header(header::ACCEPT, "application/vnd.github+json")
        .header(header::CONTENT_TYPE, "application/json")
        .body(
            serde_json::to_vec(&serde_json::json!({"query":query,"variables":variables}))
                .map_err(|_| Failure::new(400, "invalid_native_write"))?,
        ))
}
pub(super) fn sha256(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
pub(crate) fn secret(value: &[u8]) -> bool {
    (1..=8192).contains(&value.len()) && value.iter().all(|b| (33..=126).contains(b))
}
fn integer(headers: &header::HeaderMap, name: &str) -> Option<u64> {
    headers.get(name)?.to_str().ok()?.parse().ok()
}
fn provider_failure(wire: &Wire) -> Failure {
    // GitHub can report a secondary limit without a numeric Retry-After.
    // Inspect the bounded provider error locally, retaining only safe facts.
    let rate_limited = serde_json::from_slice::<Value>(&wire.bytes)
        .ok()
        .is_some_and(|value| {
            value["message"].as_str().is_some_and(|message| {
                let message = message.to_ascii_lowercase();
                message.contains("secondary rate limit") || message.contains("rate limit exceeded")
            }) || value["documentation_url"]
                .as_str()
                .is_some_and(|url| url.contains("secondary-rate-limits"))
        });
    let (status, code) = match wire.status {
        401 => (401, "github_sign_in_required"),
        404 => (404, "github_not_visible"),
        403 if integer(&wire.headers, "x-ratelimit-remaining") == Some(0)
            || wire.headers.contains_key("retry-after")
            || rate_limited =>
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
fn link_page(headers: &header::HeaderMap, relation: &str) -> Result<u32> {
    let mut next = 0;
    for value in headers.get_all(header::LINK) {
        for link in value.to_str().map_err(|_| Failure::protocol())?.split(',') {
            let mut pieces = link.trim().split(';');
            let target = pieces.next().ok_or_else(Failure::protocol)?.trim();
            if !pieces.any(|piece| piece.trim() == format!("rel=\"{relation}\"")) {
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
                || next != 0
            {
                return Err(Failure::protocol());
            }
            // GitHub legitimately uses /repositories/{id} links for a request
            // made through /repos/{owner}/{name}. Extract only the bounded page
            // number. Never follow a Link URL or adopt its path/query/resource;
            // the semantic reader reconstructs the next request in its scope.
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
