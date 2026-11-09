//! Public host controller commands, retained before HTTPS. These are metadata
//! and finite grants; they never launch a worker or spend inference allowance.
use super::{hex, invalid, unavailable, Client, Result};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use serde::{Deserialize, Serialize};
use std::path::Path;

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
impl Client {
    fn retain_control(
        &self,
        profile: &Path,
        command: &str,
        kind: &str,
        request: &impl Serialize,
    ) -> Result<Vec<u8>> {
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
        match read_bytes(&path, 8192) {
            Ok(prior) if prior == capture => (),
            Ok(_) => return Err(invalid()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                if publish_bytes(&path, &capture).is_err()
                    && read_bytes(&path, 8192).map_err(|_| unavailable())? != capture
                {
                    return Err(invalid());
                }
            }
            Err(_) => return Err(unavailable()),
        }
        Ok(body)
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
        let body = self.retain_control(
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
            || receipt.outcome == "applied" && request.action == "activate"
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
        let body = self.retain_control(
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
