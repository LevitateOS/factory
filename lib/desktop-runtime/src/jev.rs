//! JEV by TypeSafe: custody of the TypeSafe API key and the account check.
//!
//! The key lives in the credential store (the macOS Keychain without any
//! dialog) and in the one request that uses it. Nothing here returns it, logs
//! it or writes it to disk. A small non-secret record next to the profile keeps
//! what the Accounts card shows: when the key was added and checked, whether
//! TypeSafe refused it, the models it offers and the default model.
use crate::credentials::{self, Account, CredentialState, Credentials, Provider, Token};
use crate::private_fs::{read_bytes, write_bytes};
use reqwest::header::{HeaderValue, AUTHORIZATION, CONTENT_TYPE, RETRY_AFTER};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use zeroize::Zeroizing;

/// TypeSafe's API origin; the only host a saved key is ever sent to.
pub const ORIGIN: &str = "https://api.typesafe.ai";
/// The alias Factory uses unless the person pins a version.
pub const DEFAULT_MODEL: &str = "jev-latest";
const ACCOUNT: &str = "default";
const TIMEOUT: Duration = Duration::from_secs(15);
const MAX_REPLY: usize = 256 * 1024;
const MAX_RECORD: usize = 64 * 1024;
const MAX_MODELS: usize = 64;
const ASK_TIMEOUT: Duration = Duration::from_secs(30);
const MAX_ASK: usize = 1024 * 1024;
const MAX_ANSWER: usize = 1024 * 1024;
const OFFLINE_WAIT: u64 = 60;
const BUSY_FIRST: u64 = 15;
const BUSY_MAX: u64 = 600;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Error {
    /// TypeSafe answered 401: the key is not valid.
    NotAccepted,
    /// No answer from TypeSafe: offline, DNS, TLS or timeout.
    Offline,
    /// Any other TypeSafe status, or a reply Factory cannot read (status 0).
    Unexpected(u16),
    /// Not something that can be a TypeSafe key, or an unknown model.
    Invalid,
    /// There is no saved key.
    Missing,
    Credential(credentials::Error),
    /// The non-secret record could not be read or written.
    Storage,
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotAccepted => f.write_str("TypeSafe did not accept the key"),
            Self::Offline => f.write_str("TypeSafe cannot be reached"),
            Self::Unexpected(status) => write!(f, "TypeSafe answered with status {status}"),
            Self::Invalid => f.write_str("invalid TypeSafe key or model"),
            Self::Missing => f.write_str("no TypeSafe key is saved"),
            Self::Credential(error) => write!(f, "{error}"),
            Self::Storage => f.write_str("JEV record storage is unavailable"),
        }
    }
}
impl std::error::Error for Error {}
type Result<T> = std::result::Result<T, Error>;

#[derive(Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Model {
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub release_date: String,
}

#[derive(Clone, Default, Serialize, Deserialize)]
struct Record {
    added_at: u64,
    checked_at: u64,
    #[serde(default)]
    refused_at: Option<u64>,
    default_model: String,
    #[serde(default)]
    models: Vec<Model>,
}

#[derive(Clone, Copy, Serialize, PartialEq, Eq, Debug)]
#[serde(rename_all = "snake_case")]
pub enum State {
    /// No key is saved.
    None,
    Connected,
    /// TypeSafe answered 401 for the saved key.
    Refused,
    /// The keyring is locked, so the key cannot be read.
    Locked,
}

/// What the shell may see. Never contains the key.
#[derive(Clone, Serialize)]
pub struct Status {
    pub state: State,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last4: Option<String>,
    pub added_at: u64,
    pub checked_at: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub refused_at: Option<u64>,
    pub default_model: String,
    pub models: Vec<Model>,
    /// Set while the per-Mac gate is closed after busy or offline.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub problem: Option<Problem>,
}

/// What one ask came to. `Answered` carries TypeSafe's whole reply, which Go
/// validates; every other class sends nothing back from TypeSafe but its
/// status and a short `detail`.
#[derive(Clone, Copy, Serialize, PartialEq, Eq, Debug)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    Answered,
    /// 401 now, or earlier and not yet replaced or checked: nothing is sent.
    KeyRefused,
    NotConnected,
    /// 429 or 529: the gate stays closed until `retry_at`.
    Busy,
    /// No answer: the gate stays closed for a minute.
    Offline,
    /// 413, or 400 `max_tokens_exceeded`. Never retried.
    TooLarge,
    /// 400, 415 or 422: Factory sent something TypeSafe cannot take.
    Rejected,
    Unexpected,
}

#[derive(Serialize, Debug)]
pub struct Asked {
    pub outcome: Outcome,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub retry_at: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub http_status: Option<u16>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub detail: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub reply: Option<Value>,
}
impl Asked {
    fn of(outcome: Outcome) -> Self {
        Self { outcome, retry_at: None, http_status: None, detail: None, reply: None }
    }
    fn status(outcome: Outcome, status: u16, detail: Option<String>) -> Self {
        Self { http_status: Some(status), detail, ..Self::of(outcome) }
    }
}

/// Why the per-Mac gate is closed, and until when.
#[derive(Clone, Copy, Serialize, PartialEq, Eq, Debug)]
pub struct Problem {
    pub class: Outcome,
    pub since: u64,
    pub retry_at: u64,
}

/// TypeSafe limits requests per organization, so every ask on this Mac goes
/// through one gate and one at a time. Rust keeps no queue: a closed gate
/// answers at once and Go keeps the waiting decision.
struct Gate {
    problem: Option<Problem>,
    busy_wait: u64,
}
static GATE: Mutex<Gate> = Mutex::new(Gate { problem: None, busy_wait: 0 });
static ASKING: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

fn closed_gate() -> Option<Problem> {
    let gate = GATE.lock().ok()?;
    gate.problem.filter(|problem| problem.retry_at > now())
}
fn settle_gate(asked: &mut Asked) {
    let Ok(mut gate) = GATE.lock() else { return };
    let now = now();
    match asked.outcome {
        Outcome::Busy => {
            gate.busy_wait = if gate.busy_wait == 0 { BUSY_FIRST } else { (gate.busy_wait * 2).min(BUSY_MAX) };
            let retry_at = asked.retry_at.unwrap_or(now + gate.busy_wait);
            asked.retry_at = Some(retry_at);
            gate.problem = Some(Problem { class: Outcome::Busy, since: now, retry_at });
        }
        Outcome::Offline => {
            let retry_at = now + OFFLINE_WAIT;
            asked.retry_at = Some(retry_at);
            gate.problem = Some(Problem { class: Outcome::Offline, since: now, retry_at });
        }
        Outcome::Answered => {
            gate.busy_wait = 0;
            gate.problem = None;
        }
        _ => gate.problem = None,
    }
}
/// Controlled checks only: open the gate instead of waiting out `retry_at`.
#[cfg(feature = "controlled-replies")]
pub fn open_gate() {
    if let Ok(mut gate) = GATE.lock() {
        gate.problem = None;
    }
}

#[derive(Deserialize)]
struct ModelList {
    models: Vec<Model>,
}

/// The TypeSafe account connector for one profile directory.
pub struct Jev {
    base: url::Url,
    http: reqwest::Client,
    dir: PathBuf,
}
impl Jev {
    pub fn new(profile_dir: &Path) -> Result<Self> {
        Self::at(profile_dir, ORIGIN)
    }
    /// Controlled TypeSafe replies for native checks: loopback HTTP only.
    #[cfg(feature = "controlled-replies")]
    pub fn controlled(profile_dir: &Path, base: &str) -> Result<Self> {
        let parsed = url::Url::parse(base).map_err(|_| Error::Invalid)?;
        if parsed.scheme() != "http" || parsed.host_str() != Some("127.0.0.1") {
            return Err(Error::Invalid);
        }
        Self::at(profile_dir, base)
    }
    fn at(profile_dir: &Path, base: &str) -> Result<Self> {
        let base = url::Url::parse(base).map_err(|_| Error::Invalid)?;
        let http = reqwest::Client::builder()
            .https_only(base.scheme() == "https")
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .referer(false)
            .connect_timeout(TIMEOUT)
            .timeout(TIMEOUT)
            .http1_max_headers(64)
            .pool_max_idle_per_host(1)
            .user_agent("factory-native")
            .build()
            .map_err(|_| Error::Offline)?;
        Ok(Self {
            base,
            http,
            dir: profile_dir.to_path_buf(),
        })
    }

    /// Check a pasted key with TypeSafe and save it only if TypeSafe accepts
    /// it. A saved key is replaced only by one that passed this check.
    pub async fn connect(&self, store: &Credentials, key: Token) -> Result<Status> {
        let bytes = key.as_bytes();
        if bytes.len() < 12 {
            return Err(Error::Invalid);
        }
        let last4 = String::from_utf8_lossy(&bytes[bytes.len() - 4..]).into_owned();
        let models = self.models(bytes).await?;
        let account = Account::new(Provider::Typesafe, ORIGIN, ACCOUNT)
            .and_then(|account| account.with_label(&last4))
            .map_err(Error::Credential)?;
        store.store(account, key).await.map_err(Error::Credential)?;
        let previous = self.read(store).unwrap_or_default();
        let now = now();
        let default_model = if previous.default_model.is_empty() || !offers(&models, &previous.default_model) {
            DEFAULT_MODEL.to_owned()
        } else {
            previous.default_model
        };
        self.write(
            store,
            &Record {
                added_at: now,
                checked_at: now,
                refused_at: None,
                default_model,
                models,
            },
        )?;
        self.status(store).await
    }

    /// Ask TypeSafe whether the saved key still works. A refusal is recorded
    /// and shown; it is not an error. Run only when the person asks.
    pub async fn check(&self, store: &Credentials) -> Result<Status> {
        let connection = self.connection(store).await?.ok_or(Error::Missing)?;
        let cap = store.capability(&connection).await.map_err(Error::Credential)?;
        let reply = store
            .with_token_operation_timeout(&cap, TIMEOUT, async |key: &[u8]| self.models(key).await)
            .await
            .map_err(Error::Credential)?;
        let mut record = self.read(store).unwrap_or_default();
        match reply {
            Ok(models) => {
                record.checked_at = now();
                record.refused_at = None;
                if !record.default_model.is_empty() && !offers(&models, &record.default_model) {
                    record.default_model = DEFAULT_MODEL.into();
                }
                record.models = models;
            }
            Err(Error::NotAccepted) => record.refused_at = Some(now()),
            Err(error) => return Err(error),
        }
        self.write(store, &record)?;
        self.status(store).await
    }

    /// Record that TypeSafe refused the saved key, so nothing sends it again
    /// until the person replaces or checks it.
    pub fn mark_refused(&self, store: &Credentials) -> Result<()> {
        let mut record = self.read(store).unwrap_or_default();
        if record.refused_at.is_none() {
            record.refused_at = Some(now());
            self.write(store, &record)?;
        }
        Ok(())
    }

    /// One decision: `POST /v1/systemone` with the saved key, at most once.
    /// Nothing is sent while the gate is closed, no key is saved or TypeSafe
    /// refused the saved key. `model` is a project pin; without one the
    /// account default is used.
    pub async fn ask(&self, store: &Credentials, model: Option<&str>, state: &Value, questions: &Value) -> Asked {
        let _turn = ASKING.lock().await;
        if let Some(problem) = closed_gate() {
            return Asked { retry_at: Some(problem.retry_at), ..Asked::of(problem.class) };
        }
        let connection = match self.connection(store).await {
            Ok(Some(connection)) => connection,
            Ok(None) => return Asked::of(Outcome::NotConnected),
            Err(_) => return Asked { detail: Some("keyring".into()), ..Asked::of(Outcome::NotConnected) },
        };
        let record = self.read(store).unwrap_or_default();
        if record.refused_at.is_some() {
            return Asked::of(Outcome::KeyRefused);
        }
        let model = match model.filter(|model| !model.is_empty()) {
            Some(model) => model.to_owned(),
            None if record.default_model.is_empty() => DEFAULT_MODEL.to_owned(),
            None => record.default_model,
        };
        if !valid_name(&model) {
            return Asked { detail: Some("model".into()), ..Asked::of(Outcome::Rejected) };
        }
        let Ok(body) = serde_json::to_vec(&serde_json::json!({"model": model, "state": state, "questions": questions})) else {
            return Asked::of(Outcome::Rejected);
        };
        if body.len() > MAX_ASK {
            return Asked::of(Outcome::TooLarge);
        }
        let cap = match store.capability(&connection).await {
            Ok(cap) => cap,
            Err(credentials::Error::Locked) => return Asked { detail: Some("keyring_locked".into()), ..Asked::of(Outcome::NotConnected) },
            Err(_) => return Asked { detail: Some("keyring".into()), ..Asked::of(Outcome::NotConnected) },
        };
        let body = Zeroizing::new(body);
        let mut asked = store
            .with_token_operation_timeout(&cap, ASK_TIMEOUT + Duration::from_secs(5), async |key: &[u8]| self.systemone(key, &body).await)
            .await
            .unwrap_or_else(|_| Asked { detail: Some("keyring".into()), ..Asked::of(Outcome::Unexpected) });
        if asked.outcome == Outcome::KeyRefused && self.mark_refused(store).is_err() {
            eprintln!("jev: the key refusal could not be recorded");
        }
        settle_gate(&mut asked);
        let usage = asked.reply.as_ref().and_then(|reply| reply.get("usage"));
        eprintln!(
            "jev ask: {:?} status={} model={} input_tokens={} output_tokens={}",
            asked.outcome,
            asked.http_status.unwrap_or(0),
            asked.reply.as_ref().and_then(|reply| reply.get("model")).and_then(Value::as_str).unwrap_or(&model),
            usage.and_then(|u| u.get("input_tokens")).and_then(Value::as_u64).unwrap_or(0),
            usage.and_then(|u| u.get("output_tokens")).and_then(Value::as_u64).unwrap_or(0),
        );
        asked
    }

    pub async fn set_model(&self, store: &Credentials, model: &str) -> Result<Status> {
        self.connection(store).await?.ok_or(Error::Missing)?;
        let mut record = self.read(store).unwrap_or_default();
        if model != DEFAULT_MODEL && !offers(&record.models, model) {
            return Err(Error::Invalid);
        }
        record.default_model = model.into();
        self.write(store, &record)?;
        self.status(store).await
    }

    /// Delete the key from this Mac and forget the record. The key stays valid
    /// at TypeSafe until the person deletes it there.
    pub async fn remove(&self, store: &Credentials) -> Result<Status> {
        if let Some(connection) = self.connection(store).await? {
            store.withdraw(&connection).await.map_err(Error::Credential)?;
        }
        match std::fs::remove_file(self.record_path(store)) {
            Ok(()) => (),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err(Error::Storage),
        }
        self.status(store).await
    }

    pub async fn status(&self, store: &Credentials) -> Result<Status> {
        let saved = store
            .status()
            .await
            .map_err(Error::Credential)?
            .into_iter()
            .filter(|account| account.provider == Provider::Typesafe)
            .collect::<Vec<_>>();
        if saved.len() > 1 {
            return Err(Error::Credential(credentials::Error::Ambiguous));
        }
        let record = self.read(store).unwrap_or_default();
        let (state, last4) = match saved.into_iter().next() {
            None => (State::None, None),
            Some(account) => match account.state {
                CredentialState::Locked => (State::Locked, account.label),
                CredentialState::Withdrawn => (State::None, None),
                _ if record.refused_at.is_some() => (State::Refused, account.label),
                _ => (State::Connected, account.label),
            },
        };
        if state == State::None {
            return Ok(Status {
                state,
                last4: None,
                added_at: 0,
                checked_at: 0,
                refused_at: None,
                default_model: DEFAULT_MODEL.into(),
                models: Vec::new(),
                problem: None,
            });
        }
        Ok(Status {
            state,
            last4,
            added_at: record.added_at,
            checked_at: record.checked_at,
            refused_at: record.refused_at,
            default_model: if record.default_model.is_empty() {
                DEFAULT_MODEL.into()
            } else {
                record.default_model
            },
            models: record.models,
            problem: closed_gate(),
        })
    }

    async fn connection(&self, store: &Credentials) -> Result<Option<String>> {
        let mut found = store
            .status()
            .await
            .map_err(Error::Credential)?
            .into_iter()
            .filter(|account| {
                account.provider == Provider::Typesafe
                    && !matches!(account.state, CredentialState::Withdrawn)
            });
        let first = found.next().map(|account| account.connection);
        if found.next().is_some() {
            return Err(Error::Credential(credentials::Error::Ambiguous));
        }
        Ok(first)
    }

    /// `GET /v1/models`: the cheapest authenticated TypeSafe call.
    async fn models(&self, key: &[u8]) -> Result<Vec<Model>> {
        let url = self.base.join("/v1/models").map_err(|_| Error::Invalid)?;
        let mut header = Zeroizing::new(b"Bearer ".to_vec());
        header.extend_from_slice(key);
        let mut auth = HeaderValue::from_bytes(&header).map_err(|_| Error::Invalid)?;
        auth.set_sensitive(true);
        let mut response = self
            .http
            .get(url)
            .header(AUTHORIZATION, auth)
            .send()
            .await
            .map_err(|_| Error::Offline)?;
        match response.status().as_u16() {
            200 => (),
            401 => return Err(Error::NotAccepted),
            status => return Err(Error::Unexpected(status)),
        }
        let mut body = Vec::new();
        while let Some(chunk) = response.chunk().await.map_err(|_| Error::Offline)? {
            if body.len() + chunk.len() > MAX_REPLY {
                return Err(Error::Unexpected(0));
            }
            body.extend_from_slice(&chunk);
        }
        let list: ModelList = serde_json::from_slice(&body).map_err(|_| Error::Unexpected(0))?;
        let models: Vec<Model> = list
            .models
            .into_iter()
            .filter(|model| valid_name(&model.name))
            .map(|mut model| {
                model.description.truncate(500);
                model.release_date.truncate(10);
                model
            })
            .take(MAX_MODELS)
            .collect();
        Ok(models)
    }

    async fn systemone(&self, key: &[u8], body: &[u8]) -> Asked {
        let Ok(url) = self.base.join("/v1/systemone") else { return Asked::of(Outcome::Unexpected) };
        let mut header = Zeroizing::new(b"Bearer ".to_vec());
        header.extend_from_slice(key);
        let Ok(mut auth) = HeaderValue::from_bytes(&header) else { return Asked::of(Outcome::NotConnected) };
        auth.set_sensitive(true);
        let sent = self
            .http
            .post(url)
            .timeout(ASK_TIMEOUT)
            .header(AUTHORIZATION, auth)
            .header(CONTENT_TYPE, "application/json")
            .body(body.to_vec())
            .send()
            .await;
        let Ok(mut response) = sent else { return Asked::of(Outcome::Offline) };
        let status = response.status().as_u16();
        let retry_after = response
            .headers()
            .get(RETRY_AFTER)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.trim().parse::<u64>().ok())
            .map(|seconds| now() + seconds.min(3600));
        let limit = if status == 200 { MAX_ANSWER } else { 16 * 1024 };
        let mut reply = Vec::new();
        loop {
            match response.chunk().await {
                Ok(Some(chunk)) if reply.len() + chunk.len() <= limit => reply.extend_from_slice(&chunk),
                Ok(Some(_)) => return Asked::status(Outcome::Unexpected, status, Some("reply too long".into())),
                Ok(None) => break,
                Err(_) if status == 200 => return Asked::of(Outcome::Offline),
                Err(_) => break,
            }
        }
        if status == 200 {
            return match serde_json::from_slice::<Value>(&reply) {
                Ok(reply @ Value::Object(_)) => Asked { reply: Some(reply), ..Asked::status(Outcome::Answered, 200, None) },
                _ => Asked::status(Outcome::Unexpected, 200, Some("unreadable reply".into())),
            };
        }
        let detail = serde_json::from_slice::<Value>(&reply).ok().and_then(|value| match value.get("detail")? {
            Value::String(text) => Some(text.clone()),
            other => Some(other.to_string()),
        });
        let detail = detail.map(|mut text| {
            if text.len() > 500 {
                let mut end = 500;
                while !text.is_char_boundary(end) {
                    end -= 1;
                }
                text.truncate(end);
            }
            text
        });
        let outcome = match status {
            401 => Outcome::KeyRefused,
            429 | 529 => Outcome::Busy,
            413 => Outcome::TooLarge,
            400 if detail.as_deref().is_some_and(|text| text.contains("max_tokens_exceeded")) => Outcome::TooLarge,
            400 | 415 | 422 => Outcome::Rejected,
            _ => Outcome::Unexpected,
        };
        Asked { retry_at: if outcome == Outcome::Busy { retry_after } else { None }, ..Asked::status(outcome, status, detail) }
    }

    fn record_path(&self, store: &Credentials) -> PathBuf {
        self.dir.join(format!("jev-{}.json", store.profile_id()))
    }
    fn read(&self, store: &Credentials) -> Option<Record> {
        let bytes = read_bytes(&self.record_path(store), MAX_RECORD).ok()?;
        serde_json::from_slice(&bytes).ok()
    }
    fn write(&self, store: &Credentials, record: &Record) -> Result<()> {
        let path = self.record_path(store);
        let bytes = serde_json::to_vec(record).map_err(|_| Error::Storage)?;
        let mut suffix = [0u8; 8];
        getrandom::fill(&mut suffix).map_err(|_| Error::Storage)?;
        let suffix: String = suffix.iter().map(|b| format!("{b:02x}")).collect();
        let pending = path.with_extension(format!("json.{suffix}"));
        write_bytes(&pending, &bytes).map_err(|_| Error::Storage)?;
        if std::fs::rename(&pending, &path).is_err() {
            let _ = std::fs::remove_file(&pending);
            return Err(Error::Storage);
        }
        std::fs::File::open(&self.dir)
            .and_then(|dir| dir.sync_all())
            .map_err(|_| Error::Storage)
    }
}

fn offers(models: &[Model], name: &str) -> bool {
    models.iter().any(|model| model.name == name)
}
fn valid_name(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= 64
        && name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'.' | b'_'))
}
fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|elapsed| elapsed.as_secs())
        .unwrap_or(0)
}

/// Go's two JEV operations over the native channel (`/api/native/jev`).
#[derive(Deserialize)]
#[serde(tag = "operation", rename_all = "snake_case", deny_unknown_fields)]
pub enum Call {
    /// The key-free status. Local; costs no TypeSafe request.
    InspectJev,
    JevAsk {
        #[serde(default)]
        model: Option<String>,
        state: Value,
        questions: Value,
    },
}

pub(crate) async fn callback(
    profile: &Path,
    credentials: Option<std::sync::Arc<Credentials>>,
    call: Call,
) -> std::result::Result<Value, crate::provider_contract::Failure> {
    use crate::provider_contract::Failure;
    let store = credentials.ok_or_else(|| Failure::new(503, "jev_unavailable"))?;
    let jev = Jev::new(profile).map_err(|_| Failure::new(503, "jev_unavailable"))?;
    let value = match call {
        Call::InspectJev => serde_json::to_value(jev.status(&store).await.map_err(|_| Failure::new(503, "jev_unavailable"))?),
        Call::JevAsk { model, state, questions } => serde_json::to_value(jev.ask(&store, model.as_deref(), &state, &questions).await),
    };
    value.map_err(|_| Failure::new(503, "jev_unavailable"))
}
