//! Native repository enrollment against the implemented Fountain gateway.
//! OAuth custody remains in Credentials; no token or enrollment proof is a
//! browser/Go value. Execution and private operation delegation are not offered.
use crate::credentials::{Credentials, Provider, TokenCapability};
use crate::mcp_signing::Signer;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use zeroize::Zeroizing;

const TIMEOUT: Duration = Duration::from_secs(8);
const MAX_REPLY: usize = 65536;
const PROOF_HEADER: &str = "X-Factory-Desktop-Proof";

#[derive(Clone, Copy, Debug)]
pub struct Failure {
    pub status: u16,
    pub code: &'static str,
}
type Result<T> = std::result::Result<T, Failure>;
fn invalid() -> Failure {
    Failure {
        status: 400,
        code: "fountain_scope_invalid",
    }
}
fn unavailable() -> Failure {
    Failure {
        status: 503,
        code: "fountain_unavailable",
    }
}
fn hex(value: &str, size: usize) -> bool {
    value.len() == size
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}
fn name(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 100
        && value != "."
        && value != ".."
        && !value.chars().any(char::is_control)
        && !value.contains(['/', '\\'])
}

#[derive(Clone, Deserialize, Serialize)]
pub struct Actor {
    pub id: String,
    pub username: String,
    #[serde(default)]
    pub site_admin: bool,
}
#[derive(Clone, Deserialize, Serialize)]
pub struct Repository {
    pub id: String,
    pub owner: String,
    pub name: String,
    pub permission: String,
    #[serde(default)]
    pub default_branch: String,
}
#[derive(Clone, Deserialize, Serialize)]
pub struct Enrollment {
    pub id: String,
    pub runtime_id: String,
    pub application_id: String,
    pub actor: Actor,
    pub repository: Repository,
    pub active: bool,
    pub operations: Vec<String>,
    #[serde(default)]
    pub runtime_bound: bool,
}

pub struct Client {
    http: reqwest::Client,
    credentials: Arc<Credentials>,
    capability: TokenCapability,
    base: url::Url,
    runtime: String,
    command: String,
    application: String,
    proof: Zeroizing<String>,
    owner: String,
    repository: String,
    repository_id: Mutex<Option<String>>,
}
impl Client {
    /// The caller holds the native profile owner. Both account scope and
    /// request lifetime are rechecked by the existing custody lease per call.
    pub fn new(
        credentials: Arc<Credentials>,
        capability: TokenCapability,
        signer: &Signer,
        runtime: &str,
        application: &str,
        owner: &str,
        repository: &str,
        command: &str,
    ) -> Result<Self> {
        let account = capability.account();
        if account.provider != Provider::Fountain
            || !credentials.is_profile(runtime)
            || !hex(runtime, 32)
            || !hex(command, 32)
            || !name(owner)
            || !name(repository)
            || application.is_empty()
            || application.len() > 128
            || !application
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_'))
        {
            return Err(invalid());
        }
        let mut base = url::Url::parse(&account.origin).map_err(|_| invalid())?;
        if base.scheme() != "https"
            || base.host_str().is_none()
            || !base.username().is_empty()
            || base.password().is_some()
            || base.query().is_some()
            || base.fragment().is_some()
            || base.path() != "/"
        {
            return Err(invalid());
        }
        let owner = owner.to_ascii_lowercase();
        let repository = repository.to_ascii_lowercase();
        let scope = serde_json::to_vec(&[
            base.origin().ascii_serialization(),
            account.account.clone(),
            runtime.into(),
            application.into(),
            owner.clone(),
            repository.clone(),
            command.into(),
        ])
        .map_err(|_| invalid())?;
        let proof = signer
            .derive_desktop_enrollment(&scope)
            .map_err(|_| unavailable())?;
        base.path_segments_mut().map_err(|_| invalid())?.extend([
            "api",
            "v1",
            "repos",
            owner.as_str(),
            repository.as_str(),
            "factory",
            "desktop",
            "grants",
        ]);
        let http = reqwest::Client::builder()
            .https_only(true)
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .referer(false)
            .connect_timeout(TIMEOUT)
            .timeout(TIMEOUT)
            .http1_max_headers(32)
            .pool_max_idle_per_host(1)
            .user_agent("factory-native")
            .build()
            .map_err(|_| unavailable())?;
        Ok(Self {
            http,
            credentials,
            capability,
            base,
            runtime: runtime.into(),
            command: command.into(),
            application: application.into(),
            proof,
            owner,
            repository,
            repository_id: Mutex::new(None),
        })
    }
    async fn call<T: serde::de::DeserializeOwned>(
        &self,
        method: reqwest::Method,
        resource: bool,
        body: Option<Vec<u8>>,
    ) -> Result<T> {
        let mut url = self.base.clone();
        if method != reqwest::Method::POST {
            url.path_segments_mut()
                .map_err(|_| invalid())?
                .push(&self.command);
        }
        if resource {
            url.path_segments_mut()
                .map_err(|_| invalid())?
                .push("repository");
        }
        let capability = self
            .credentials
            .renew_capability(&self.capability)
            .await
            .map_err(|_| Failure {
                status: 403,
                code: "fountain_credentials_unavailable",
            })?;
        self.credentials
            .with_token_operation(&capability, async |token| {
                let authorization = std::str::from_utf8(token).map_err(|_| invalid())?;
                let mut proof = reqwest::header::HeaderValue::from_str(self.proof.as_str())
                    .map_err(|_| invalid())?;
                proof.set_sensitive(true);
                let mut request = self
                    .http
                    .request(method, url)
                    .bearer_auth(authorization)
                    .header(PROOF_HEADER, proof)
                    .header("Accept", "application/json");
                if let Some(body) = body {
                    request = request
                        .header("Content-Type", "application/json")
                        .body(body);
                }
                let mut response = request.send().await.map_err(|_| unavailable())?;
                if !matches!(response.status().as_u16(), 200 | 201) {
                    return Err(Failure {
                        status: response.status().as_u16(),
                        code: "fountain_request_refused",
                    });
                }
                let mut bytes = Vec::new();
                while let Some(chunk) = response.chunk().await.map_err(|_| unavailable())? {
                    if bytes.len().saturating_add(chunk.len()) > MAX_REPLY {
                        return Err(unavailable());
                    }
                    bytes.extend_from_slice(&chunk);
                }
                serde_json::from_slice(&bytes).map_err(|_| unavailable())
            })
            .await
            .map_err(|_| Failure {
                status: 403,
                code: "fountain_credentials_unavailable",
            })?
    }
    fn repository_matches(&self, repository: &Repository) -> Result<()> {
        if repository.id.is_empty()
            || repository.id.len() > 32
            || !repository.id.bytes().all(|byte| byte.is_ascii_digit())
            || !repository.owner.eq_ignore_ascii_case(&self.owner)
            || !repository.name.eq_ignore_ascii_case(&self.repository)
            || repository.permission != "read"
        {
            return Err(invalid());
        }
        let mut pinned = self.repository_id.lock().map_err(|_| unavailable())?;
        if pinned.as_ref().is_some_and(|id| id != &repository.id) {
            return Err(invalid());
        }
        *pinned = Some(repository.id.clone());
        Ok(())
    }
    fn checked(&self, value: Enrollment) -> Result<Enrollment> {
        if value.id != self.command
            || value.runtime_id != self.runtime
            || value.application_id != self.application
            || value.actor.id != self.capability.account().account
            || !value.runtime_bound
            || value.operations != ["repository.read"]
        {
            return Err(invalid());
        }
        self.repository_matches(&value.repository)?;
        Ok(value)
    }
    pub async fn enroll(&self) -> Result<Enrollment> {
        let hash: String = Sha256::digest(self.proof.as_bytes())
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        let body = serde_json::to_vec(&serde_json::json!({
            "command_id": self.command,
            "runtime_id": self.runtime,
            "proof_sha256": hash,
        }))
        .map_err(|_| invalid())?;
        self.checked(self.call(reqwest::Method::POST, false, Some(body)).await?)
    }
    pub async fn status(&self) -> Result<Enrollment> {
        self.checked(self.call(reqwest::Method::GET, false, None).await?)
    }
    pub async fn withdraw(&self) -> Result<Enrollment> {
        let value = self.checked(self.call(reqwest::Method::DELETE, false, None).await?)?;
        if value.active {
            return Err(unavailable());
        }
        Ok(value)
    }
    pub async fn repository(&self) -> Result<Repository> {
        let value = self.call(reqwest::Method::GET, true, None).await?;
        self.repository_matches(&value)?;
        Ok(value)
    }
}
