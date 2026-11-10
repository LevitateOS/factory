//! Retain the native enrollment command before HTTPS. These provider-local
//! captures carry no execution authority; the host owns grant state/permissions.
use super::{hex, invalid, name, unavailable, Client, Failure, Result};
use crate::{
    credentials::{Credentials, Provider, TokenCapability},
    mcp_signing::Signer,
    private_fs::{private_dir, publish_bytes, read_bytes},
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{path::Path, sync::Arc};
#[derive(Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct Capture {
    #[serde(default, skip_serializing_if = "is_false")]
    issues_only: bool,
    version: u8,
    profile: String,
    connection: String,
    origin: String,
    account: String,
    application: String,
    owner: String,
    repository: String,
    command: String,
}
fn is_false(value: &bool) -> bool { !*value }
/// Adopt the original command after a lost reply/reopen. A withdrawn host grant
/// remains withdrawn; neither current login nor a missing local reply renews it.
pub async fn retained_client(
    credentials: Arc<Credentials>,
    capability: TokenCapability,
    profile: &Path,
    runtime: &str,
    owner: &str,
    repository: &str,
) -> Result<Client> {
    let account = capability.account();
    if account.provider != Provider::Fountain
        || !credentials.is_profile(runtime)
        || !hex(runtime, 32)
        || !name(owner)
        || !name(repository)
    {
        return Err(invalid());
    }
    let current = credentials
        .status()
        .await
        .map_err(|_| unavailable())?
        .into_iter()
        .find(|value| {
            value.connection == account.connection
                && value.provider == account.provider
                && value.account == account.account
                && value.origin == account.origin
        })
        .ok_or_else(unavailable)?;
    let grant = current.grant.ok_or(Failure {
        status: 409,
        code: "fountain_sign_in_again",
    })?;
    let issues_only = grant.scope == "write:issue";
    let application = grant.client_id.ok_or(Failure {
        status: 409,
        code: "fountain_sign_in_again",
    })?;
    let owner = owner.to_ascii_lowercase();
    let repository = repository.to_ascii_lowercase();
    let mut scope = serde_json::to_vec(&(
        runtime,
        &account.connection,
        &account.origin,
        &account.account,
        &application,
        &owner,
        &repository,
    ))
    .map_err(|_| invalid())?;
    if issues_only { scope.extend_from_slice(b"\x00issues"); }
    let key = Sha256::digest(&scope)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let directory = profile.join("fountain-enrollments");
    private_dir(&directory).map_err(|_| unavailable())?;
    let path = directory.join(format!("{key}.json"));
    let mut capture = Capture {
        issues_only,
        version: 1,
        profile: runtime.into(),
        connection: account.connection.clone(),
        origin: account.origin.clone(),
        account: account.account.clone(),
        application,
        owner,
        repository,
        command: String::new(),
    };
    let prior = match read_bytes(&path, 4096) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            let mut random = [0u8; 16];
            getrandom::fill(&mut random).map_err(|_| unavailable())?;
            capture.command = random.iter().map(|byte| format!("{byte:02x}")).collect();
            let bytes = serde_json::to_vec(&capture).map_err(|_| invalid())?;
            if publish_bytes(&path, &bytes).is_err() {
                read_bytes(&path, 4096).map_err(|_| unavailable())?
            } else {
                bytes
            }
        }
        Err(_) => return Err(unavailable()),
    };
    let saved: Capture = serde_json::from_slice(&prior).map_err(|_| unavailable())?;
    if !hex(&saved.command, 32) {
        return Err(unavailable());
    }
    capture.command = saved.command.clone();
    if saved != capture {
        return Err(unavailable());
    }
    let signer = Signer::open(profile).map_err(|_| unavailable())?;
    Client::new_scoped(
        credentials,
        capability,
        &signer,
        runtime,
        &saved.application,
        &saved.owner,
        &saved.repository,
        &saved.command,
        issues_only,
    )
}
