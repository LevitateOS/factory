//! Central execution claims bind native custody, not model funding. Persist
//! each exact request before I/O; recovery only reads the original host journal.
use super::{hex, invalid, Client, OperationGrant, Result};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::path::Path;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ExecutionClaimRequest {
    pub command_id: String,
    pub manifest_digest: String,
    pub not_after: i64,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ExecutionClaim {
    pub command_id: String,
    pub grant_id: String,
    pub manifest_digest: String,
    pub not_after: i64,
    pub state: String,
    pub retirement_digest: String,
    pub acquired: bool,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ExecutionRetirement {
    pub manifest_digest: String,
    pub state: String,
    pub receipt_digest: String,
}
impl Client {
    fn check_execution_scope(
        &self,
        grant: &OperationGrant,
        request: &ExecutionClaimRequest,
    ) -> Result<()> {
        if !hex(&grant.id, 32)
            || grant.enrollment_id != self.command
            || grant.runtime_id != self.runtime
            || grant.actor.id != self.capability.account().account
            || !grant.repository.owner.eq_ignore_ascii_case(&self.owner)
            || !grant.repository.name.eq_ignore_ascii_case(&self.repository)
            || !grant
                .operations
                .iter()
                .any(|kind| kind == "execution.launch")
            || !hex(&request.command_id, 32)
            || !hex(&request.manifest_digest, 64)
            || request.not_after <= 0
            || request.not_after > grant.not_after
        {
            return Err(invalid());
        }
        Ok(())
    }
    fn check_execution_claim(
        &self,
        grant: &OperationGrant,
        request: &ExecutionClaimRequest,
        claim: &ExecutionClaim,
    ) -> Result<()> {
        self.check_execution_scope(grant, request)?;
        if claim.command_id != request.command_id
            || claim.grant_id != grant.id
            || claim.manifest_digest != request.manifest_digest
            || claim.not_after != request.not_after
            || match claim.state.as_str() {
                "claimed" => !claim.retirement_digest.is_empty(),
                "retired" | "not_started" => !hex(&claim.retirement_digest, 64) || claim.acquired,
                _ => true,
            }
        {
            return Err(invalid());
        }
        Ok(())
    }
    /// This observation can never acquire dispatch. Missing history is an
    /// error, even when a previous reservation might not have reached the host.
    pub async fn execution_claim(
        &self,
        grant: &OperationGrant,
        request: &ExecutionClaimRequest,
    ) -> Result<ExecutionClaim> {
        self.check_execution_scope(grant, request)?;
        let claim: ExecutionClaim = self
            .request(
                reqwest::Method::GET,
                &[
                    &self.command,
                    "operations",
                    &grant.id,
                    "claims",
                    &request.command_id,
                ],
                None,
            )
            .await?;
        self.check_execution_claim(grant, request, &claim)?;
        if claim.acquired {
            return Err(invalid());
        }
        Ok(claim)
    }
    /// Only the original successful reserve reply can acquire one launch.
    /// Reopen, a concurrent caller and a lost reply all use lookup only.
    pub async fn claim_execution(
        &self,
        profile: &Path,
        grant: &OperationGrant,
        request: &ExecutionClaimRequest,
    ) -> Result<ExecutionClaim> {
        self.check_execution_scope(grant, request)?;
        let (_, first) = self.retain_control(
            profile,
            &request.command_id,
            "execution.claim",
            &serde_json::json!({"grant_id":grant.id,"request":request}),
        )?;
        // The retained envelope includes its grant; the host wire carries the
        // typed request only. Neither a changed grant nor payload can reuse it.
        let claim = if first {
            if !grant.allows_current("execution.launch") {
                return Err(invalid());
            }
            match self
                .request(
                    reqwest::Method::POST,
                    &[&self.command, "operations", &grant.id, "claims"],
                    Some(serde_json::to_vec(request).map_err(|_| invalid())?),
                )
                .await
            {
                Ok(claim) => claim,
                Err(_) => self.execution_claim(grant, request).await?,
            }
        } else {
            self.execution_claim(grant, request).await?
        };
        self.check_execution_claim(grant, request, &claim)?;
        Ok(claim)
    }
    /// Caller supplies the exact native custody/no-start receipt. This method
    /// never treats expiry, a missing worker or absent lookup as retirement.
    pub async fn retire_execution(
        &self,
        profile: &Path,
        grant: &OperationGrant,
        request: &ExecutionClaimRequest,
        retirement: &ExecutionRetirement,
    ) -> Result<ExecutionClaim> {
        self.check_execution_scope(grant, request)?;
        if retirement.manifest_digest != request.manifest_digest
            || !matches!(retirement.state.as_str(), "retired" | "not_started")
            || !hex(&retirement.receipt_digest, 64)
        {
            return Err(invalid());
        }
        // A separate stable local capture preserves the reservation and refuses
        // changing its retirement evidence. This is not new dispatch authority.
        let key: String = Sha256::digest(format!(
            "factory.fountain.retire/v1\0{}\0{}",
            grant.id, request.command_id
        ))
        .iter()
        .take(16)
        .map(|byte| format!("{byte:02x}"))
        .collect();
        let (_, first) = self.retain_control(
            profile,
            &key,
            "execution.retire",
            &serde_json::json!({"grant_id":grant.id,"claim":request,"retirement":retirement}),
        )?;
        let claim = if first {
            match self
                .request(
                    reqwest::Method::POST,
                    &[
                        &self.command,
                        "operations",
                        &grant.id,
                        "claims",
                        &request.command_id,
                        "retire",
                    ],
                    Some(serde_json::to_vec(retirement).map_err(|_| invalid())?),
                )
                .await
            {
                Ok(claim) => claim,
                Err(_) => self.execution_claim(grant, request).await?,
            }
        } else {
            self.execution_claim(grant, request).await?
        };
        self.check_execution_claim(grant, request, &claim)?;
        if claim.acquired
            || claim.state != retirement.state
            || claim.retirement_digest != retirement.receipt_digest
        {
            return Err(invalid());
        }
        Ok(claim)
    }
}
