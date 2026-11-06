use super::transport::secret;
use super::{Client, Failure, Result, OAUTH_CLIENT_ID};
use crate::credentials::{Grant, Token};
use serde::{Deserialize, Deserializer, Serialize};
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use zeroize::Zeroizing;

#[derive(Serialize)]
pub struct DevicePrompt {
    pub user_code: String,
    pub verification_uri: String,
    pub expires_at: u64,
}
/// Single-use native attempt; its device code cannot be serialized or logged.
pub struct DeviceAuthorization {
    pub(crate) client: Client,
    prompt: DevicePrompt,
    code: Secret,
    interval: u64,
}
struct Secret(Zeroizing<String>);
impl<'de> Deserialize<'de> for Secret {
    fn deserialize<D: Deserializer<'de>>(d: D) -> std::result::Result<Self, D::Error> {
        String::deserialize(d).map(|s| Self(Zeroizing::new(s)))
    }
}
#[derive(Deserialize)]
struct Challenge {
    device_code: Secret,
    user_code: String,
    verification_uri: String,
    verification_uri_complete: Option<String>,
    expires_in: u64,
    interval: Option<u64>,
}
#[derive(Deserialize)]
struct OAuthReply {
    access_token: Option<Secret>,
    refresh_token: Option<Secret>,
    token_type: Option<String>,
    scope: Option<String>,
    expires_in: Option<u64>,
    refresh_token_expires_in: Option<u64>,
    error: Option<String>,
}
pub(crate) fn now() -> Result<u64> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|t| t.as_secs())
        .map_err(|_| Failure::unavailable())
}
fn form(fields: &[(&str, &str)]) -> Zeroizing<String> {
    Zeroizing::new(
        url::form_urlencoded::Serializer::new(String::new())
            .extend_pairs(fields.iter().copied())
            .finish(),
    )
}
impl Client {
    pub async fn device_start(&self) -> Result<DeviceAuthorization> {
        let start = now()?;
        let raw = self
            .oauth(
                "/login/device/code",
                form(&[("client_id", OAUTH_CLIENT_ID), ("scope", "repo read:user read:org")]),
            )
            .await?;
        let challenge: Challenge = serde_json::from_slice(&raw).map_err(|_| Failure::protocol())?;
        let interval = challenge.interval.filter(|v| *v != 0).unwrap_or(5);
        if !secret(challenge.device_code.0.as_bytes())
            || challenge.user_code.len() != 9
            || challenge.user_code.as_bytes()[4] != b'-'
            || !challenge
                .user_code
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'-')
            || challenge.verification_uri != "https://github.com/login/device"
            || challenge
                .verification_uri_complete
                .as_ref()
                .is_some_and(|s| !s.is_empty())
            || !(1..=900).contains(&challenge.expires_in)
            || !(1..=60).contains(&interval)
            || start + challenge.expires_in <= now()?
        {
            return Err(Failure::protocol());
        }
        self.current()?;
        Ok(DeviceAuthorization {
            client: self.clone(),
            prompt: DevicePrompt {
                user_code: challenge.user_code,
                verification_uri: challenge.verification_uri,
                expires_at: start + challenge.expires_in,
            },
            code: challenge.device_code,
            interval,
        })
    }
    /// Caller must reserve durable native rotation before this one-shot exchange.
    /// No retry and no reuse of the old pair on an uncertain outcome.
    pub(crate) async fn refresh(
        &self,
        refresh: &[u8],
    ) -> Result<(crate::credentials::Account, Grant)> {
        if !secret(refresh) {
            return Err(Failure::invalid());
        }
        let start = now()?;
        let refresh = std::str::from_utf8(refresh).map_err(|_| Failure::invalid())?;
        let raw = self
            .oauth(
                "/login/oauth/access_token",
                form(&[
                    ("client_id", OAUTH_CLIENT_ID),
                    ("grant_type", "refresh_token"),
                    ("refresh_token", refresh),
                ]),
            )
            .await?;
        let reply: OAuthReply = serde_json::from_slice(&raw)
            .map_err(|_| Failure::new(502, "github_refresh_unconfirmed"))?;
        if let Some(code) = &reply.error {
            return Err(match code.as_str() {
                "bad_refresh_token" => Failure::new(401, "github_sign_in_required"),
                "incorrect_client_credentials" => {
                    Failure::new(403, "github_refresh_client_rejected")
                }
                _ => Failure::new(502, "github_refresh_unconfirmed"),
            });
        }
        if reply
            .refresh_token
            .as_ref()
            .is_none_or(|value| value.0.as_str() == refresh)
            || reply.expires_in.is_none()
            || reply.refresh_token_expires_in.is_none()
        {
            return Err(Failure::protocol());
        }
        let grant = decode(reply, start)?;
        let (account, access) = self.verify(grant.access).await?;
        self.current()?;
        Ok((account, Grant { access, ..grant }))
    }
}
impl DeviceAuthorization {
    pub fn prompt(&self) -> &DevicePrompt {
        &self.prompt
    }
    pub fn verifier(&self) -> Client {
        self.client.clone()
    }
    pub async fn wait(self) -> Result<Grant> {
        let deadline = self.prompt.expires_at;
        let remaining = deadline
            .checked_sub(now()?)
            .filter(|v| (1..=900).contains(v))
            .ok_or_else(|| Failure::new(408, "device_expired"))?;
        tokio::time::timeout(Duration::from_secs(remaining), async move {
            let mut interval = self.interval;
            loop {
                self.client.delay(Duration::from_secs(interval)).await?;
                if now()? >= deadline {
                    return Err(Failure::new(408, "device_expired"));
                }
                let start = now()?;
                let raw = self
                    .client
                    .oauth(
                        "/login/oauth/access_token",
                        form(&[
                            ("client_id", OAUTH_CLIENT_ID),
                            ("grant_type", "urn:ietf:params:oauth:grant-type:device_code"),
                            ("device_code", &self.code.0),
                        ]),
                    )
                    .await?;
                let reply: OAuthReply =
                    serde_json::from_slice(&raw).map_err(|_| Failure::protocol())?;
                match reply.error.as_deref() {
                    Some("authorization_pending") => continue,
                    Some("slow_down") => {
                        interval = interval
                            .checked_add(5)
                            .filter(|v| *v <= 900)
                            .ok_or_else(Failure::protocol)?;
                        continue;
                    }
                    Some("access_denied") => return Err(Failure::new(403, "device_denied")),
                    Some("expired_token") => return Err(Failure::new(408, "device_expired")),
                    Some(_) => return Err(Failure::new(502, "device_authorization_failed")),
                    None => {
                        let grant = decode(reply, start)?;
                        self.client.current()?;
                        return Ok(grant);
                    }
                }
            }
        })
        .await
        .map_err(|_| Failure::new(408, "device_expired"))?
    }
}
fn decode(reply: OAuthReply, start: u64) -> Result<Grant> {
    let access = reply.access_token.ok_or_else(Failure::protocol)?;
    let scope = reply.scope.unwrap_or_default();
    if reply.error.is_some()
        || !reply
            .token_type
            .as_deref()
            .is_some_and(|s| s.eq_ignore_ascii_case("bearer"))
        || scope.len() > 1024
        || scope.chars().any(char::is_control)
        || (reply.expires_in.is_some() || reply.refresh_token_expires_in.is_some())
            && reply.refresh_token.is_none()
    {
        return Err(Failure::protocol());
    }
    let expiry = |seconds: Option<u64>| -> Result<Option<u64>> {
        seconds
            .map(|v| {
                if v == 0 || v > 366 * 24 * 60 * 60 || start + v <= now()? {
                    return Err(Failure::protocol());
                }
                Ok(start + v)
            })
            .transpose()
    };
    Ok(Grant {
        access: Token::new(access.0.as_bytes().to_vec()).map_err(|_| Failure::protocol())?,
        refresh: reply
            .refresh_token
            .map(|token| Token::new(token.0.as_bytes().to_vec()))
            .transpose()
            .map_err(|_| Failure::protocol())?,
        expires_at: expiry(reply.expires_in)?,
        refresh_expires_at: expiry(reply.refresh_token_expires_in)?,
        scope,
    })
}
