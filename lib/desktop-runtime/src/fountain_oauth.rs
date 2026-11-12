//! Configured Fountain public-client OAuth. Loopback code, PKCE verifier and
//! refresh secrets stay in the native owner; only the authorization URL is UI.
use crate::credentials::{
    Account, AccountStatus, Credentials, Grant, Provider, Token, TokenCapability,
};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use serde::{Deserialize, Deserializer, Serialize};
use sha2::{Digest, Sha256};
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use zeroize::Zeroizing;

const IO_LIMIT: usize = 65_536;
const HTTP_TIMEOUT: Duration = Duration::from_secs(8);
const LOGIN_TIMEOUT: Duration = Duration::from_secs(300);
const SCOPE: &str = "read:repository";
const CONTROL_SCOPE: &str = "write:repository";
const COLLABORATION_SCOPE: &str = "read:repository read:issue";
const COLLABORATION_CONTROL_SCOPE: &str = "write:repository write:issue";
const ISSUES_SCOPE: &str = "write:issue";

/// Consent purpose owns discovery, accepted scopes and enrollment family.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ConsentPurpose { Read, Control, Issues }
impl ConsentPurpose {
    pub fn from_scope(scope: &str) -> Option<Self> {
        match scope {
            SCOPE | COLLABORATION_SCOPE => Some(Self::Read),
            CONTROL_SCOPE | COLLABORATION_CONTROL_SCOPE => Some(Self::Control),
            ISSUES_SCOPE => Some(Self::Issues),
            _ => None,
        }
    }
    fn discovery(self) -> &'static str {
        match self {
            Self::Read => "api/v1/factory/desktop/oauth",
            Self::Control => "api/v1/factory/desktop/oauth/control",
            Self::Issues => "api/v1/factory/desktop/oauth/issues",
        }
    }
    pub fn enrollment_family(self) -> &'static str {
        if self == Self::Issues { "issue-grants" } else { "grants" }
    }
}

#[derive(Serialize)]
pub struct CredentialView {
    #[serde(flatten)] pub account: AccountStatus,
    #[serde(skip_serializing_if="Option::is_none")]
    pub purpose: Option<ConsentPurpose>,
}
impl CredentialView {
    pub fn new(account: AccountStatus) -> Self {
        let purpose = if account.provider == Provider::Fountain {
            account.grant.as_ref().and_then(|grant| ConsentPurpose::from_scope(&grant.scope))
        } else { None };
        Self { account, purpose }
    }
}

#[derive(Clone, Copy, Debug)]
pub enum Error {
    Invalid,
    Unavailable,
    Denied,
    Expired,
    Reauthorize,
    Credential(crate::credentials::Error),
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Self::Invalid => "Fountain sign-in could not be verified",
            Self::Unavailable => "Fountain sign-in is unavailable",
            Self::Denied => "Fountain sign-in was declined",
            Self::Expired => "Fountain sign-in expired",
            Self::Reauthorize => "Sign in to Fountain again to refresh this older saved account",
            Self::Credential(_) => "Fountain credential storage is unavailable",
        })
    }
}
impl std::error::Error for Error {}
type Result<T> = std::result::Result<T, Error>;
#[derive(Clone, Serialize)]
pub struct Prompt {
    pub authorization_uri: String,
    pub expires_at: u64,
}
#[derive(Clone)]
pub struct Client {
    http: reqwest::Client,
    origin: url::Url,
    client_id: String,
    scope: &'static str,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Config {
    client_id: String,
    scope: String,
}
struct Secret(Zeroizing<String>);
impl<'de> Deserialize<'de> for Secret {
    fn deserialize<D: Deserializer<'de>>(d: D) -> std::result::Result<Self, D::Error> {
        String::deserialize(d).map(|s| Self(Zeroizing::new(s)))
    }
}
#[derive(Deserialize)]
struct TokenReply {
    access_token: Secret,
    token_type: String,
    refresh_token: Secret,
    expires_in: u64,
    #[serde(default)]
    scope: Option<String>,
}
fn now() -> Result<u64> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|t| t.as_secs())
        .map_err(|_| Error::Unavailable)
}
fn random() -> Result<String> {
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes).map_err(|_| Error::Unavailable)?;
    Ok(URL_SAFE_NO_PAD.encode(bytes))
}
fn client_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_'))
}
async fn json<T: serde::de::DeserializeOwned>(request: reqwest::RequestBuilder) -> Result<T> {
    let mut response = request.send().await.map_err(|_| Error::Unavailable)?;
    if !response.status().is_success() {
        return Err(if matches!(response.status().as_u16(), 400 | 401 | 403) {
            Error::Denied
        } else {
            Error::Unavailable
        });
    }
    if response
        .content_length()
        .is_some_and(|n| n > IO_LIMIT as u64)
    {
        return Err(Error::Invalid);
    }
    let mut bytes = Zeroizing::new(Vec::new());
    while let Some(chunk) = response.chunk().await.map_err(|_| Error::Unavailable)? {
        if bytes.len() + chunk.len() > IO_LIMIT {
            return Err(Error::Invalid);
        }
        bytes.extend_from_slice(&chunk);
    }
    serde_json::from_slice(&bytes).map_err(|_| Error::Invalid)
}
impl Client {
    pub async fn configured(origin: &str) -> Result<Self> {
        Self::configured_for(origin, ConsentPurpose::Read).await
    }
    /// Only an explicit user choice requests privileged OAuth consent. It
    /// still creates no repository controller, operation grant or funding.
    pub async fn configured_control(origin: &str) -> Result<Self> {
        Self::configured_for(origin, ConsentPurpose::Control).await
    }
    pub async fn configured_issues(origin: &str) -> Result<Self> {
        Self::configured_for(origin, ConsentPurpose::Issues).await
    }
    pub async fn configured_for(origin: &str, purpose: ConsentPurpose) -> Result<Self> {
        Account::new(Provider::Fountain, origin, "origin-validation")
            .map_err(|_| Error::Invalid)?;
        let origin = url::Url::parse(origin).map_err(|_| Error::Invalid)?;
        let http = reqwest::Client::builder()
            .https_only(true)
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .referer(false)
            .connect_timeout(HTTP_TIMEOUT)
            .timeout(HTTP_TIMEOUT)
            .http1_max_headers(32)
            .pool_max_idle_per_host(1)
            .user_agent("factory-native")
            .build()
            .map_err(|_| Error::Unavailable)?;
        let configured: Config = json(
            http.get(
                origin
                    .join(purpose.discovery())
                    .map_err(|_| Error::Invalid)?,
            ).header("X-Factory-Desktop-Protocol", "2"),
        )
        .await?;
        if ConsentPurpose::from_scope(&configured.scope)!=Some(purpose){return Err(Error::Invalid);}
        let scope=match configured.scope.as_str(){SCOPE=>SCOPE,CONTROL_SCOPE=>CONTROL_SCOPE,COLLABORATION_SCOPE=>COLLABORATION_SCOPE,COLLABORATION_CONTROL_SCOPE=>COLLABORATION_CONTROL_SCOPE,ISSUES_SCOPE=>ISSUES_SCOPE,_=>return Err(Error::Invalid)};
        if !client_id(&configured.client_id) {
            return Err(Error::Invalid);
        }
        Ok(Self {
            http,
            origin,
            client_id: configured.client_id,
            scope,
        })
    }
    pub fn origin(&self) -> String {
        self.origin.origin().ascii_serialization()
    }
    pub fn application(&self) -> &str {
        &self.client_id
    }
    pub async fn begin(self) -> Result<Authorization> {
        let listener = TcpListener::bind((std::net::Ipv4Addr::LOCALHOST, 0))
            .await
            .map_err(|_| Error::Unavailable)?;
        let redirect = format!(
            "http://127.0.0.1:{}/",
            listener
                .local_addr()
                .map_err(|_| Error::Unavailable)?
                .port()
        );
        let state = Zeroizing::new(random()?);
        let verifier = Zeroizing::new(random()?);
        let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));
        let mut uri = self
            .origin
            .join("login/oauth/authorize")
            .map_err(|_| Error::Invalid)?;
        uri.query_pairs_mut().extend_pairs([
            ("client_id", self.client_id.as_str()),
            ("redirect_uri", redirect.as_str()),
            ("response_type", "code"),
            ("scope", self.scope),
            ("state", state.as_str()),
            ("code_challenge_method", "S256"),
            ("code_challenge", challenge.as_str()),
        ]);
        Ok(Authorization {
            client: self,
            listener,
            redirect,
            state,
            verifier,
            deadline: Instant::now() + LOGIN_TIMEOUT,
            prompt: Prompt {
                authorization_uri: uri.into(),
                expires_at: now()? + LOGIN_TIMEOUT.as_secs(),
            },
        })
    }
    async fn exchange(&self, fields: &[(&str, &str)], scope: &str) -> Result<Grant> {
        let body = {
            let mut serializer = url::form_urlencoded::Serializer::new(String::new());
            serializer.extend_pairs(fields.iter().copied());
            serializer.finish()
        };
        let request = self
            .http
            .post(
                self.origin
                    .join("login/oauth/access_token")
                    .map_err(|_| Error::Invalid)?,
            )
            .header("Content-Type", "application/x-www-form-urlencoded")
            .body(body);
        let reply: TokenReply = json(request).await?;
        if !reply.token_type.eq_ignore_ascii_case("bearer")
            || reply.expires_in == 0
            || reply.expires_in > 366 * 86400
            || reply.scope.as_deref().is_some_and(|s| s != scope)
        {
            return Err(Error::Invalid);
        }
        Ok(Grant {
            access: Token::new(reply.access_token.0.as_bytes().to_vec())
                .map_err(Error::Credential)?,
            refresh: Some(
                Token::new(reply.refresh_token.0.as_bytes().to_vec()).map_err(Error::Credential)?,
            ),
            expires_at: Some(now()? + reply.expires_in),
            refresh_expires_at: None,
            scope: scope.into(),
            client_id: Some(self.client_id.clone()),
        })
    }
    async fn verify(&self, grant: Grant, expected: Option<&str>) -> Result<(Account, Grant)> {
        let token = std::str::from_utf8(grant.access.as_bytes()).map_err(|_| Error::Invalid)?;
        let identity: crate::fountain_provider::Actor = json(
            self.http
                .get(
                    self.origin
                        .join("api/v1/factory/desktop/identity")
                        .map_err(|_| Error::Invalid)?,
                )
                .bearer_auth(token),
        )
        .await?;
        if identity
            .id
            .parse::<u64>()
            .ok()
            .filter(|id| *id > 0 && id.to_string() == identity.id)
            .is_none()
            || identity.username.is_empty()
            || identity.username.len() > 256
            || identity.username.chars().any(char::is_control)
            || expected.is_some_and(|id| id != identity.id)
        {
            return Err(Error::Invalid);
        }
        let account = Account::new(Provider::Fountain, self.origin.as_str(), &identity.id)
            .map_err(Error::Credential)?
            .with_label(&identity.username)
            .map_err(Error::Credential)?;
        Ok((account, grant))
    }
    pub async fn refresh(
        &self,
        credentials: &Arc<Credentials>,
        capability: &TokenCapability,
    ) -> Result<AccountStatus> {
        let account = capability.account();
        // Capabilities capture revision/identity without reading grant secrets.
        // Read client metadata through custody; rotate_grant still checks the
        // exact captured revision before any refresh I/O or publication.
        let status = credentials
            .status()
            .await
            .map_err(Error::Credential)?
            .into_iter()
            .find(|value| {
                value.connection == account.connection
                    && value.provider == account.provider
                    && value.origin == account.origin
                    && value.account == account.account
            })
            .ok_or(Error::Credential(crate::credentials::Error::Retired))?;
        match status.state {
            crate::credentials::CredentialState::RefreshPending => {
                return Err(Error::Credential(crate::credentials::Error::RefreshPending))
            }
            crate::credentials::CredentialState::Locked => {
                return Err(Error::Credential(crate::credentials::Error::Locked))
            }
            crate::credentials::CredentialState::Withdrawn => {
                return Err(Error::Credential(crate::credentials::Error::Withdrawn))
            }
            _ => (),
        }
        let saved = status.grant.as_ref().ok_or(Error::Reauthorize)?;
        if saved.client_id.is_none() {
            return Err(Error::Reauthorize);
        }
        if account.provider != Provider::Fountain
            || account.origin != self.origin()
            || saved.client_id.as_deref() != Some(self.client_id.as_str())
        {
            return Err(Error::Invalid);
        }
        let mut failure = None;
        let refreshed = credentials
            .rotate_grant(capability, async |refresh| {
                let result = async {
                    let token = std::str::from_utf8(refresh).map_err(|_| Error::Invalid)?;
                    let grant = self
                        .exchange(
                            &[
                                ("grant_type", "refresh_token"),
                                ("client_id", &self.client_id),
                                ("refresh_token", token),
                            ],
                            &saved.scope,
                        )
                        .await?;
                    self.verify(grant, Some(&account.account)).await
                }
                .await;
                result.map_err(|error| {
                    failure = Some(error);
                    crate::credentials::Error::RefreshPending
                })
            })
            .await;
        refreshed.map_err(|error| failure.unwrap_or(Error::Credential(error)))
    }
}
pub struct Authorization {
    client: Client,
    listener: TcpListener,
    redirect: String,
    state: Zeroizing<String>,
    verifier: Zeroizing<String>,
    deadline: Instant,
    prompt: Prompt,
}
impl Authorization {
    pub fn prompt(&self) -> &Prompt {
        &self.prompt
    }
    pub async fn wait(self) -> Result<(Account, Grant)> {
        let work = async {
            loop {
                let (mut stream, _) = self
                    .listener
                    .accept()
                    .await
                    .map_err(|_| Error::Unavailable)?;
                let mut request = Zeroizing::new(Vec::new());
                let read = async {
                    loop {
                        let mut part = [0u8; 1024];
                        let n = stream.read(&mut part).await.map_err(|_| Error::Invalid)?;
                        if n == 0 || request.len() + n > 8192 {
                            return Err(Error::Invalid);
                        }
                        request.extend_from_slice(&part[..n]);
                        if request.windows(4).any(|w| w == b"\r\n\r\n") {
                            return Ok(());
                        }
                    }
                };
                if !matches!(tokio::time::timeout(HTTP_TIMEOUT, read).await, Ok(Ok(()))) {
                    continue;
                }
                let parsed = (|| {
                    let text = std::str::from_utf8(&request).map_err(|_| Error::Invalid)?;
                    let line = text.lines().next().ok_or(Error::Invalid)?;
                    let fields: Vec<_> = line.split_whitespace().collect();
                    if fields.len() != 3
                        || fields[0] != "GET"
                        || fields[2] != "HTTP/1.1"
                        || !fields[1].starts_with("/?")
                    {
                        return Err(Error::Invalid);
                    }
                    let hosts: Vec<_> = text
                        .lines()
                        .skip(1)
                        .filter_map(|line| line.split_once(':'))
                        .filter(|(key, _)| key.eq_ignore_ascii_case("host"))
                        .map(|(_, value)| value.trim())
                        .collect();
                    if hosts.len() != 1
                        || hosts[0]
                            != self
                                .redirect
                                .trim_start_matches("http://")
                                .trim_end_matches('/')
                    {
                        return Err(Error::Invalid);
                    }
                    let uri = url::Url::parse(&format!(
                        "{}{}",
                        self.redirect.trim_end_matches('/'),
                        fields[1]
                    ))
                    .map_err(|_| Error::Invalid)?;
                    let values: Vec<_> = uri.query_pairs().collect();
                    let take = |key: &str| {
                        let matches: Vec<_> = values.iter().filter(|(k, _)| k == key).collect();
                        if matches.len() == 1 {
                            Some(matches[0].1.to_string())
                        } else {
                            None
                        }
                    };
                    if take("state").as_deref() != Some(self.state.as_str()) {
                        return Err(Error::Invalid);
                    }
                    if take("error").is_some() {
                        return Err(Error::Denied);
                    }
                    let code = take("code").ok_or(Error::Invalid)?;
                    if code.is_empty() || code.len() > 2048 || code.chars().any(char::is_control) {
                        return Err(Error::Invalid);
                    }
                    Ok(Zeroizing::new(code))
                })();
                let message = if parsed.is_ok() {
                    "Sign-in received. You can return to Factory."
                } else {
                    "This sign-in response was not accepted."
                };
                let response=format!("HTTP/1.1 200 OK\r\nContent-Type: text/plain; charset=utf-8\r\nCache-Control: no-store\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",message.len(),message);
                let _ =
                    tokio::time::timeout(HTTP_TIMEOUT, stream.write_all(response.as_bytes())).await;
                let code = match parsed {
                    Ok(code) => code,
                    Err(Error::Denied) => return Err(Error::Denied),
                    Err(_) => continue,
                };
                let grant = self
                    .client
                    .exchange(
                        &[
                            ("grant_type", "authorization_code"),
                            ("client_id", &self.client.client_id),
                            ("redirect_uri", &self.redirect),
                            ("code", &code),
                            ("code_verifier", &self.verifier),
                        ],
                        self.client.scope,
                    )
                    .await?;
                return self.client.verify(grant, None).await;
            }
        };
        tokio::time::timeout_at(self.deadline.into(), work)
            .await
            .map_err(|_| Error::Expired)?
    }
}
