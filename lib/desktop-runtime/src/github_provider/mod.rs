//! Complete native GitHub session. No Tauri, product SQL, or browser HTTP bridge.
//! The transport owner is joined on retirement, including its TLS/HTTP drivers.
mod diff;
mod oauth;
mod patch;
mod reads;
mod records;
mod request;
mod transport;

use crate::credentials::{Account, Provider, Token};
pub use oauth::{DeviceAuthorization, DevicePrompt};
pub use request::Read;
use serde::Serialize;
pub use transport::{Client, Owner};

pub const API_VERSION: &str = "2026-03-10";
pub const OAUTH_CLIENT_ID: &str = "Ov23liI9YQMPuYjBL6nn";

/// Safe provider outcome. Never retains an upstream error, URL, body or grant.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Failure {
    pub status: u16,
    pub code: &'static str,
    pub retry_after: Option<u64>,
    pub reset: Option<u64>,
}
impl Failure {
    pub(crate) fn new(status: u16, code: &'static str) -> Self {
        Self {
            status,
            code,
            retry_after: None,
            reset: None,
        }
    }
    pub(crate) fn unavailable() -> Self {
        Self::new(503, "github_unavailable")
    }
    pub(crate) fn invalid() -> Self {
        Self::new(400, "invalid_native_read")
    }
    pub(crate) fn protocol() -> Self {
        Self::new(502, "invalid_github_response")
    }
}
impl std::fmt::Display for Failure {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.code)
    }
}
impl std::error::Error for Failure {}
pub(crate) type Result<T> = std::result::Result<T, Failure>;

/// Provider-local identity. Canonical identity and connection binding stay in Go.
#[derive(Serialize)]
pub struct Identity {
    pub external_id: String,
    pub login: String,
}
impl Identity {
    pub(crate) fn account(&self) -> Result<Account> {
        Account::new(Provider::Github, "https://github.com", &self.external_id)
            .and_then(|value| value.with_label(&self.login))
            .map_err(|_| Failure::protocol())
    }
}
impl Client {
    pub(crate) async fn identity(&self, token: &[u8]) -> Result<Identity> {
        let raw = self.get("/user", &[], token).await?.value;
        let id = raw["id"]
            .as_i64()
            .filter(|id| *id > 0)
            .ok_or_else(Failure::protocol)?;
        let login = raw["login"]
            .as_str()
            .filter(|login| !login.is_empty())
            .ok_or_else(Failure::protocol)?;
        let identity = Identity {
            external_id: id.to_string(),
            login: login.into(),
        };
        identity.account()?;
        self.current()?;
        Ok(identity)
    }
    /// An unpersisted token must be verified through /user before native custody.
    pub async fn verify(&self, token: Token) -> Result<(Account, Token)> {
        let identity = self.identity(token.as_bytes()).await?;
        Ok((identity.account()?, token))
    }
}
