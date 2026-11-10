//! Public host controller commands, retained before HTTPS. These are metadata
//! and finite grants; they never launch a worker or spend inference allowance.
use super::{hex, invalid, unavailable, Client, Result};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use crate::{credentials::Credentials, mcp_signing::Signer};
use serde::{Deserialize, Serialize};
use std::path::Path;
use std::sync::Arc;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Placement {
    pub repository_id: String,
    pub runtime_id: String,
    pub owner_kind: String,
    pub owner_id: String,
    pub generation: String,
    pub revision: String,
    pub state: String,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Selection {
    pub command_id: String,
    pub expected_revision: String,
    pub action: String,
    pub not_after: i64,
}
#[derive(Clone, Deserialize, Serialize)]
pub struct SelectionReceipt {
    pub command_id: String,
    pub outcome: String,
    #[serde(default)]
    pub reason_code: Option<String>,
    pub placement: Placement,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct OperationAuthorization {
    pub command_id: String,
    pub expected_placement: String,
    pub generation: String,
    pub operations: Vec<String>,
    pub not_after: i64,
}
#[derive(Clone, Deserialize, Serialize)]
pub struct OperationGrant {
    pub id: String,
    pub enrollment_id: String,
    pub runtime_id: String,
    pub installation_id: String,
    pub generation: String,
    pub actor: super::Actor,
    pub repository: super::Repository,
    pub operations: Vec<String>,
    pub not_after: i64,
    pub active: bool,
}
impl OperationGrant {
    /// Host activity, the captured expiry and the named capability must all
    /// remain current. History lookup and retirement do not use this predicate.
    pub fn allows_current(&self, kind: &str) -> bool {
        self.active
            && self.not_after > time::OffsetDateTime::now_utc().unix_timestamp()
            && self.operations.iter().any(|value| value == kind)
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CapturedAuthorization {
    version: u8,
    profile: String,
    connection: String,
    origin: String,
    account: String,
    application: String,
    enrollment: String,
    owner: String,
    repository: String,
    kind: String,
    request: OperationAuthorization,
}

/// Resolve only the exact native connection/profile that authorized this
/// immutable grant. There is no enrollment, source selection or authorization
/// POST here. A current grant observation still does not authorize dispatch.
pub async fn captured_operation_grant(
    credentials: Arc<Credentials>,
    profile: &Path,
    runtime: &str,
    connection: &str,
    id: &str,
) -> Result<(Client, OperationGrant)> {
    if !hex(id, 32) || !credentials.is_profile(runtime) {
        return Err(invalid());
    }
    let bytes = read_bytes(
        &profile.join("fountain-commands").join(format!("{id}.json")),
        8192,
    )
    .map_err(|_| super::Failure{status:503,code:"fountain_captured_grant_unavailable"})?;
    let capture: CapturedAuthorization = serde_json::from_slice(&bytes).map_err(|_| invalid())?;
    if capture.version != 1
        || capture.profile != runtime
        || capture.kind != "runtime.operation.authorize"
        || capture.request.command_id != id
        || capture.connection != connection
        || !hex(&capture.connection, 32)
        || !hex(&capture.enrollment, 32)
        || !hex(&capture.request.expected_placement, 32)
    {
        return Err(invalid());
    }
    let capability = credentials
        .capability(&capture.connection)
        .await
        .map_err(|_| super::Failure{status:503,code:"fountain_captured_credential_unavailable"})?;
    if capability.account().origin != capture.origin
        || capability.account().account != capture.account
    {
        return Err(invalid());
    }
    let signer = Signer::open(profile).map_err(|_| super::Failure{status:503,code:"fountain_captured_proof_unavailable"})?;
    let client = Client::new(
        credentials,
        capability,
        &signer,
        runtime,
        &capture.application,
        &capture.owner,
        &capture.repository,
        &capture.enrollment,
    )?;
    let grant = client.operation_grant(id).await?;
    let mut operations = capture.request.operations;
    operations.sort();
    if grant.generation != capture.request.generation
        || grant.not_after != capture.request.not_after
        || grant.operations != operations
    {
        return Err(invalid());
    }
    Ok((client, grant))
}
impl Client {
    pub(super) fn retain_control(
        &self,
        profile: &Path,
        command: &str,
        kind: &str,
        request: &impl Serialize,
    ) -> Result<(Vec<u8>, bool)> {
        if !hex(command, 32) {
            return Err(invalid());
        }
        let body = serde_json::to_vec(request).map_err(|_| invalid())?;
        if body.len() > 4096 {
            return Err(invalid());
        }
        let capture = serde_json::to_vec(&serde_json::json!({
            "version": 1, "profile": self.runtime, "connection": self.capability.account().connection,
            "origin": self.capability.account().origin, "account": self.capability.account().account,
            "application": self.application, "enrollment": self.command, "owner": self.owner,
            "repository": self.repository, "kind": kind, "request": request,
        })).map_err(|_| invalid())?;
        let directory = profile.join("fountain-commands");
        private_dir(&directory).map_err(|_| unavailable())?;
        let path = directory.join(format!("{command}.json"));
        let first = match read_bytes(&path, 8192) {
            Ok(prior) if prior == capture => false,
            Ok(_) => return Err(invalid()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                if publish_bytes(&path, &capture).is_ok() {
                    true
                } else {
                    if read_bytes(&path, 8192).map_err(|_| unavailable())? != capture {
                        return Err(invalid());
                    }
                    false
                }
            }
            Err(_) => return Err(unavailable()),
        };
        Ok((body, first))
    }
    pub async fn placement(&self) -> Result<Placement> {
        let enrollment = self.status().await?;
        let value: Placement = self
            .request(reqwest::Method::GET, &[&self.command, "placement"], None)
            .await?;
        if value.repository_id != enrollment.repository.id
            || !matches!(value.state.as_str(), "active" | "retiring" | "inactive")
            || value.generation.parse::<u64>().is_err()
            || (!value.revision.is_empty() && !hex(&value.revision, 32))
        {
            return Err(invalid());
        }
        Ok(value)
    }
    pub async fn select_runtime(
        &self,
        profile: &Path,
        request: &Selection,
    ) -> Result<SelectionReceipt> {
        if !matches!(request.action.as_str(), "activate" | "withdraw" | "retire")
            || (!request.expected_revision.is_empty() && !hex(&request.expected_revision, 32))
        {
            return Err(invalid());
        }
        let enrollment = self.status().await?;
        let (body, _) = self.retain_control(
            profile,
            &request.command_id,
            "runtime.controller.select",
            request,
        )?;
        let receipt: SelectionReceipt = self
            .request(
                reqwest::Method::POST,
                &[&self.command, "placement"],
                Some(body),
            )
            .await?;
        if receipt.command_id != request.command_id
            || !matches!(receipt.outcome.as_str(), "applied" | "refused")
            || receipt.placement.repository_id != enrollment.repository.id
            || receipt.outcome == "applied"
                && request.action == "activate"
                && (receipt.placement.owner_kind != "desktop"
                    || receipt.placement.owner_id != self.command
                    || receipt.placement.runtime_id != self.runtime)
        {
            return Err(invalid());
        }
        Ok(receipt)
    }
    pub async fn authorize_operations(
        &self,
        profile: &Path,
        request: &OperationAuthorization,
    ) -> Result<OperationGrant> {
        if !hex(&request.expected_placement, 32)
            || request
                .generation
                .parse::<u64>()
                .ok()
                .filter(|v| *v > 0)
                .is_none()
            || request.operations.is_empty()
            || request.operations.len() > 10
        {
            return Err(invalid());
        }
        let enrollment = self.status().await?;
        let (body, _) = self.retain_control(
            profile,
            &request.command_id,
            "runtime.operation.authorize",
            request,
        )?;
        let grant: OperationGrant = self
            .request(
                reqwest::Method::POST,
                &[&self.command, "operations"],
                Some(body),
            )
            .await?;
        let mut operations = request.operations.clone();
        operations.sort();
        if grant.id != request.command_id
            || grant.enrollment_id != self.command
            || grant.runtime_id != self.runtime
            || grant.generation != request.generation
            || grant.actor.id != enrollment.actor.id
            || grant.repository.id != enrollment.repository.id
            || grant.not_after != request.not_after
            || grant.operations != operations
        {
            return Err(invalid());
        }
        Ok(grant)
    }
}
