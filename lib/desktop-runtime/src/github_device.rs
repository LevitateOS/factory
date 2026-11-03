//! Native-only device-issued OAuth. Initial authorization produces an opaque
//! unverified grant; refresh verifies identity and conditionally rotates custody.
//! Desktop login lifecycle and all browser-facing commands remain consumers.
use crate::credentials::{
    AccountStatus, Credentials, Error as CredentialError, Grant as CredentialGrant, Provider,
    Token, TokenCapability,
};
use crate::github::{verified_account, Error};
use crate::supervisor::{NativeChannel, NativeSupervisor};
use serde::{Deserialize, Deserializer, Serialize};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use zeroize::Zeroizing;

/// The only device authorization fields suitable for a future webview command.
#[derive(Serialize)]
pub struct DevicePrompt {
    pub user_code: String,
    pub verification_uri: String,
    pub expires_at: u64,
}

/// No Serialize, Debug or Clone: one native attempt owns one captured instance.
pub struct DeviceAuthorization {
    prompt: DevicePrompt,
    code: Secret,
    interval: u64,
    channel: NativeChannel,
    alive: Arc<AtomicBool>,
}

struct Secret(Zeroizing<String>);
impl<'de> Deserialize<'de> for Secret {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        String::deserialize(deserializer).map(|value| Self(Zeroizing::new(value)))
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Challenge {
    device_code: Secret,
    user_code: String,
    verification_uri: String,
    expires_at: u64,
    interval: u64,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Grant {
    access_token: Secret,
    refresh_token: Option<Secret>,
    expires_at: Option<u64>,
    refresh_expires_at: Option<u64>,
    scope: String,
}

fn now() -> Result<u64, Error> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|time| time.as_secs())
        .map_err(|_| Error::Unavailable)
}

async fn exchange(
    channel: &NativeChannel,
    alive: &AtomicBool,
    target: &str,
    body: &[u8],
    timeout: Duration,
) -> Result<Zeroizing<Vec<u8>>, Error> {
    if !alive.load(Ordering::SeqCst) {
        return Err(Error::Unavailable);
    }
    let response = soda_unix_http::request_authenticated_async(
        &channel.socket,
        "POST",
        target,
        "soda-native",
        body,
        &channel.key,
        &[("X-Soda-Native-Instance", &channel.instance)],
        timeout,
        soda_unix_http::Limits {
            header_bytes: 16384,
            body_bytes: 32768,
        },
    )
    .await
    .map_err(|_| Error::Unavailable)?;
    let bytes = Zeroizing::new(response.body);
    if !alive.load(Ordering::SeqCst) {
        return Err(Error::Unavailable);
    }
    if response.status != 200 {
        return Err(if (400..=599).contains(&response.status) {
            Error::Provider(response.status)
        } else {
            Error::Protocol
        });
    }
    Ok(bytes)
}

impl NativeSupervisor {
    /// One explicitly requested native refresh, never an automatic HTTP retry.
    /// Custody reserves before network dispatch and conditionally publishes only
    /// the same freshly verified account. No future borrows the supervisor.
    pub fn github_refresh<'a>(
        &self,
        credentials: &'a Credentials,
        capability: &'a TokenCapability,
    ) -> impl std::future::Future<Output = Result<AccountStatus, Error>> + 'a {
        let channel = self.github_channel();
        async move {
            let account = capability.account();
            if account.provider != Provider::Github || account.origin != "https://github.com" {
                return Err(Error::Invalid);
            }
            let (receive, alive) = channel.map_err(|_| Error::Unavailable)?;
            let channel = tokio::time::timeout(Duration::from_secs(2), receive)
                .await
                .map_err(|_| Error::Unavailable)?
                .map_err(|_| Error::Unavailable)?
                .map_err(|_| Error::Unavailable)?;
            if !alive.load(Ordering::SeqCst) {
                return Err(Error::Unavailable);
            }
            let mut provider_failure = None;
            let result = credentials
                .rotate_grant(capability, async |refresh| {
                    let result = async {
                        if !alive.load(Ordering::SeqCst) {
                            return Err(Error::Unavailable);
                        }
                        let value = std::str::from_utf8(refresh).map_err(|_| Error::Invalid)?;
                        let response = soda_unix_http::request_authenticated_async(
                            &channel.socket,
                            "POST",
                            "/api/native/github/refresh",
                            "soda-native",
                            b"{}",
                            &channel.key,
                            &[
                                ("X-Soda-Native-Instance", &channel.instance),
                                ("X-Soda-GitHub-Refresh", value),
                            ],
                            Duration::from_secs(7),
                            soda_unix_http::Limits {
                                header_bytes: 16384,
                                body_bytes: 32768,
                            },
                        )
                        .await
                        .map_err(|_| Error::Unavailable)?;
                        let bytes = Zeroizing::new(response.body);
                        if !alive.load(Ordering::SeqCst) {
                            return Err(Error::Unavailable);
                        }
                        if response.status != 200 {
                            return Err(if (400..=599).contains(&response.status) {
                                Error::Provider(response.status)
                            } else {
                                Error::Protocol
                            });
                        }
                        #[derive(Deserialize)]
                        #[serde(deny_unknown_fields)]
                        struct User {
                            external_id: String,
                            login: String,
                        }
                        #[derive(Deserialize)]
                        #[serde(deny_unknown_fields)]
                        struct Refreshed {
                            account: User,
                            grant: Grant,
                        }
                        let raw: Refreshed =
                            serde_json::from_slice(&bytes).map_err(|_| Error::Protocol)?;
                        let account =
                            verified_account(&raw.account.external_id, &raw.account.login)?;
                        let grant = decode_grant(raw.grant)?;
                        if grant.refresh.is_none()
                            || grant.expires_at.is_none()
                            || grant.refresh_expires_at.is_none()
                        {
                            return Err(Error::Protocol);
                        }
                        Ok((account, grant))
                    }
                    .await;
                    result.map_err(|error| {
                        provider_failure = Some(error);
                        CredentialError::RefreshPending
                    })
                })
                .await;
            match result {
                Ok(status) => Ok(status),
                Err(error) => Err(provider_failure.unwrap_or(Error::Credential(error))),
            }
        }
    }

    /// Does not borrow the supervisor while awaiting I/O. Dropping the future
    /// closes the private socket; stopping the host retires its captured channel.
    pub fn github_device_start(
        &self,
    ) -> impl std::future::Future<Output = Result<DeviceAuthorization, Error>> {
        let channel = self.github_channel();
        async move {
            tokio::time::timeout(Duration::from_secs(10), async move {
                let (receive, alive) = channel.map_err(|_| Error::Unavailable)?;
                let channel = receive
                    .await
                    .map_err(|_| Error::Unavailable)?
                    .map_err(|_| Error::Unavailable)?;
                let bytes = exchange(
                    &channel,
                    &alive,
                    "/api/native/github/device/start",
                    b"{}",
                    Duration::from_secs(9),
                )
                .await?;
                let raw: Challenge = serde_json::from_slice(&bytes).map_err(|_| Error::Protocol)?;
                let time = now()?;
                if raw.device_code.0.is_empty()
                    || raw.device_code.0.len() > 8192
                    || !raw.device_code.0.bytes().all(|b| (33..=126).contains(&b))
                    || raw.verification_uri != "https://github.com/login/device"
                    || raw.user_code.len() != 9
                    || raw.user_code.as_bytes()[4] != b'-'
                    || !raw
                        .user_code
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b == b'-')
                    || raw.expires_at <= time
                    || raw.expires_at > time + 900
                    || !(1..=60).contains(&raw.interval)
                {
                    return Err(Error::Protocol);
                }
                Ok(DeviceAuthorization {
                    prompt: DevicePrompt {
                        user_code: raw.user_code,
                        verification_uri: raw.verification_uri,
                        expires_at: raw.expires_at,
                    },
                    code: raw.device_code,
                    interval: raw.interval,
                    channel,
                    alive,
                })
            })
            .await
            .map_err(|_| Error::Unavailable)?
        }
    }
}

impl DeviceAuthorization {
    pub fn prompt(&self) -> &DevicePrompt {
        &self.prompt
    }

    /// Poll with upstream interval/backoff until authorization/refusal/expiry.
    /// Cancellation drops the socket and zeroizes native request/response data.
    /// A grant still needs github_verify before any native account persistence.
    pub async fn wait(self) -> Result<CredentialGrant, Error> {
        let remaining = self
            .prompt
            .expires_at
            .checked_sub(now()?)
            .filter(|seconds| *seconds > 0 && *seconds <= 900)
            .ok_or(Error::Invalid)?;
        #[derive(Serialize)]
        struct Request<'a> {
            device_code: &'a str,
            expires_at: u64,
            interval: u64,
        }
        let body = Zeroizing::new(
            serde_json::to_vec(&Request {
                device_code: &self.code.0,
                expires_at: self.prompt.expires_at,
                interval: self.interval,
            })
            .map_err(|_| Error::Invalid)?,
        );
        let bytes = exchange(
            &self.channel,
            &self.alive,
            "/api/native/github/device/wait",
            &body,
            Duration::from_secs(remaining + 2),
        )
        .await?;
        let raw: Grant = serde_json::from_slice(&bytes).map_err(|_| Error::Protocol)?;
        decode_grant(raw)
    }
}

fn decode_grant(raw: Grant) -> Result<CredentialGrant, Error> {
    let time = now()?;
    if raw.scope.len() > 1024
        || raw.expires_at.is_some_and(|expiry| expiry <= time)
        || raw.refresh_expires_at.is_some_and(|expiry| expiry <= time)
        || (raw.expires_at.is_some() || raw.refresh_expires_at.is_some())
            && raw.refresh_token.is_none()
    {
        return Err(Error::Protocol);
    }
    Ok(CredentialGrant {
        access: Token::new(raw.access_token.0.as_bytes().to_vec()).map_err(|_| Error::Protocol)?,
        refresh: raw
            .refresh_token
            .map(|secret| Token::new(secret.0.as_bytes().to_vec()))
            .transpose()
            .map_err(|_| Error::Protocol)?,
        expires_at: raw.expires_at,
        refresh_expires_at: raw.refresh_expires_at,
        scope: raw.scope,
    })
}
