//! Versioned secret payloads. Only the encrypted Secret Service value contains
//! grant data; public native grants have no Serialize, Deserialize, Debug or Clone.
use super::{Error, Result, Token};
use serde::{Deserialize, Deserializer, Serialize};
use std::time::{SystemTime, UNIX_EPOCH};
use zeroize::Zeroizing;
// A grant can never be mistaken for a printable raw token if its non-secret
// lookup attributes are missing/damaged. In particular, never forward a whole
// serialized grant (including its refresh secret) as an access credential.
const MAGIC: &[u8] = b"\0factory-oauth-grant-v1\n";

pub struct Grant {
    pub access: Token,
    pub refresh: Option<Token>,
    pub expires_at: Option<u64>,
    pub refresh_expires_at: Option<u64>,
    pub scope: String,
    pub client_id: Option<String>,
}

/// Non-secret facts only. The presence of refresh authority is not a promise
/// that the provider will accept it; only the native refresh consumer can know.
#[derive(Clone, Serialize)]
pub struct GrantStatus {
    pub expires_at: Option<u64>,
    pub refresh_expires_at: Option<u64>,
    pub refresh_available: bool,
    pub scope: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub client_id: Option<String>,
}

impl Grant {
    fn validate(&self) -> Result<()> {
        if self.scope.len() > 1024
            || self.scope.chars().any(char::is_control)
            || self.expires_at == Some(0)
            || self.refresh_expires_at == Some(0)
            || self.client_id.as_ref().is_some_and(|value| {
                value.is_empty()
                    || value.len() > 128
                    || !value
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_'))
            })
            || (self.expires_at.is_some() || self.refresh_expires_at.is_some())
                && self.refresh.is_none()
        {
            return Err(Error::Invalid);
        }
        Ok(())
    }
}

pub(super) enum Secret {
    Token(Token),
    Grant(Grant),
}

fn now() -> Result<u64> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|time| time.as_secs())
        .map_err(|_| Error::Unavailable)
}

impl Secret {
    pub(super) fn validate(&self) -> Result<()> {
        match self {
            Self::Token(_) => Ok(()),
            Self::Grant(grant) => grant.validate(),
        }
    }
    pub(super) fn format(&self) -> &'static str {
        match self {
            Self::Token(_) => "token",
            Self::Grant(grant) if grant.client_id.is_some() => "oauth-grant-v2",
            Self::Grant(_) => "oauth-grant-v1",
        }
    }
    pub(super) fn access(&self) -> Result<&Token> {
        match self {
            Self::Token(token) => Ok(token),
            Self::Grant(grant) => {
                let time = now()?;
                if grant.expires_at.is_some_and(|expiry| expiry <= time) {
                    return Err(Error::Expired);
                }
                Ok(&grant.access)
            }
        }
    }
    pub(super) fn refresh(&self) -> Result<&Token> {
        let Self::Grant(grant) = self else {
            return Err(Error::Missing);
        };
        let time = now()?;
        if grant
            .refresh_expires_at
            .is_some_and(|expiry| expiry <= time)
        {
            return Err(Error::Expired);
        }
        grant.refresh.as_ref().ok_or(Error::Missing)
    }
    pub(super) fn status(&self) -> Result<Option<GrantStatus>> {
        let Self::Grant(grant) = self else {
            return Ok(None);
        };
        let time = now()?;
        Ok(Some(GrantStatus {
            expires_at: grant.expires_at,
            refresh_expires_at: grant.refresh_expires_at,
            refresh_available: grant.refresh.is_some()
                && grant.refresh_expires_at.is_none_or(|expiry| expiry > time),
            scope: grant.scope.clone(),
            client_id: grant.client_id.clone(),
        }))
    }
    pub(super) fn encode(&self, revision: &str) -> Result<Zeroizing<Vec<u8>>> {
        let Self::Grant(grant) = self else {
            return self.access().map(|token| Zeroizing::new(token.0.to_vec()));
        };
        grant.validate()?;
        #[derive(Serialize)]
        struct Stored<'a> {
            version: u8,
            revision: &'a str,
            access: &'a str,
            refresh: Option<&'a str>,
            expires_at: Option<u64>,
            refresh_expires_at: Option<u64>,
            scope: &'a str,
            #[serde(skip_serializing_if = "Option::is_none")]
            client_id: Option<&'a str>,
        }
        fn text(token: &Token) -> Result<&str> {
            std::str::from_utf8(token.as_bytes()).map_err(|_| Error::Invalid)
        }
        let json = Zeroizing::new(
            serde_json::to_vec(&Stored {
                version: if grant.client_id.is_some() { 2 } else { 1 },
                revision,
                access: text(&grant.access)?,
                refresh: grant
                    .refresh
                    .as_ref()
                    .map(|token| text(token))
                    .transpose()?,
                expires_at: grant.expires_at,
                refresh_expires_at: grant.refresh_expires_at,
                scope: &grant.scope,
                client_id: grant.client_id.as_deref(),
            })
            .map_err(|_| Error::Invalid)?,
        );
        let mut bytes = Zeroizing::new(Vec::with_capacity(MAGIC.len() + json.len()));
        bytes.extend_from_slice(MAGIC);
        bytes.extend_from_slice(&json);
        Ok(bytes)
    }
    pub(super) async fn load(item: &secret_service::Item<'_>, revision: &str) -> Result<Self> {
        let attrs = item.get_attributes().await?;
        let bytes = Zeroizing::new(item.get_secret().await?);
        match attrs.get("format").map(String::as_str) {
            None | Some("token") => Token::new(bytes.to_vec()).map(Self::Token),
            Some(format @ ("oauth-grant-v1" | "oauth-grant-v2")) => {
                if bytes.len() > 65536 {
                    return Err(Error::Invalid);
                }
                let json = bytes.strip_prefix(MAGIC).ok_or(Error::Invalid)?;
                let raw: Stored = serde_json::from_slice(json).map_err(|_| Error::Invalid)?;
                if !(format == "oauth-grant-v1" && raw.version == 1 && raw.client_id.is_none()
                    || format == "oauth-grant-v2" && raw.version == 2 && raw.client_id.is_some())
                {
                    return Err(Error::Invalid);
                }
                if raw.revision != revision {
                    return Err(Error::Retired);
                }
                let grant = Grant {
                    access: Token::new(raw.access.0.as_bytes().to_vec())?,
                    refresh: raw
                        .refresh
                        .map(|token| Token::new(token.0.as_bytes().to_vec()))
                        .transpose()?,
                    expires_at: raw.expires_at,
                    refresh_expires_at: raw.refresh_expires_at,
                    scope: raw.scope,
                    client_id: raw.client_id,
                };
                grant.validate()?;
                Ok(Self::Grant(grant))
            }
            _ => Err(Error::Invalid),
        }
    }
}

struct SecretText(Zeroizing<String>);
impl<'de> Deserialize<'de> for SecretText {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> std::result::Result<Self, D::Error> {
        String::deserialize(deserializer).map(|value| Self(Zeroizing::new(value)))
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Stored {
    version: u8,
    revision: String,
    access: SecretText,
    refresh: Option<SecretText>,
    expires_at: Option<u64>,
    refresh_expires_at: Option<u64>,
    scope: String,
    #[serde(default)]
    client_id: Option<String>,
}
