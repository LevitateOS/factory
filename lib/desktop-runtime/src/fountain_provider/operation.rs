//! Typed native effect transport. The host's original journal is authoritative;
//! a retained intent permits lookup only, never a second POST after uncertainty.
use super::{hex, invalid, unavailable, Client, Failure, OperationGrant, Result};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use serde::{Deserialize, Serialize};
use std::path::Path;

#[derive(Deserialize, Serialize)]
pub struct NativeRevision {
    pub revision: i64,
    pub idle: bool,
    pub inhibited: bool,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Note {
    pub issue_id: i64,
    pub content: String,
    pub merge_ref: String,
}
#[derive(Clone, Deserialize, Serialize)]
pub struct OperationRecord {
    pub controller_grant_id: String,
    pub installation_id: String,
    pub operation_id: String,
    pub actor_id: String,
    pub repository_id: String,
    pub kind: String,
    pub outcome: String,
    #[serde(default)]
    pub reason_code: String,
    pub effect_state: String,
    pub cancellation_status: String,
    #[serde(default)]
    pub completion_state: String,
    pub intent_digest: String,
    #[serde(default)]
    pub receipt: Option<serde_json::Value>,
}
#[derive(Deserialize)]
struct Lookup {
    installation_id: String,
    operation_id: String,
    status: String,
    record: Option<OperationRecord>,
}
#[derive(Serialize)]
struct Intent<'a> {
    controller_grant_id: &'a str,
    installation_id: &'a str,
    operation_id: &'a str,
    actor_id: &'a str,
    repository_id: &'a str,
    kind: &'static str,
    authorization_revision: &'a str,
    expected_native_revision: i64,
    not_after: i64,
    payload: &'a Note,
}
fn unknown() -> Failure {
    Failure {
        status: 503,
        code: "fountain_operation_unknown",
    }
}
impl Client {
    pub async fn operation_grant(&self, id: &str) -> Result<OperationGrant> {
        if !hex(id, 32) {
            return Err(invalid());
        }
        let grant: OperationGrant = self
            .request(
                reqwest::Method::GET,
                &[&self.command, "operations", id],
                None,
            )
            .await?;
        if grant.id != id
            || grant.enrollment_id != self.command
            || grant.runtime_id != self.runtime
            || grant.actor.id != self.capability.account().account
            || grant.repository.owner.to_ascii_lowercase() != self.owner
            || grant.repository.name.to_ascii_lowercase() != self.repository
        {
            return Err(invalid());
        }
        Ok(grant)
    }
    pub async fn native_revision(&self) -> Result<NativeRevision> {
        self.status().await?;
        let revision: NativeRevision = self
            .request(
                reqwest::Method::GET,
                &[&self.command, "native-revision"],
                None,
            )
            .await?;
        if revision.revision < 1 {
            return Err(invalid());
        }
        Ok(revision)
    }
    fn check_note_record(
        &self,
        grant: &OperationGrant,
        operation: &str,
        note: &Note,
        record: &OperationRecord,
    ) -> Result<()> {
        if record.controller_grant_id != grant.id
            || record.installation_id != grant.installation_id
            || record.operation_id != operation
            || record.actor_id != grant.actor.id
            || record.repository_id != grant.repository.id
            || record.kind != "issue.comment.publish"
            || !hex(&record.intent_digest, 64)
            || record.outcome != record.effect_state
            || !matches!(
                record.effect_state.as_str(),
                "pending" | "committed" | "not_committed" | "indeterminate"
            )
        {
            return Err(invalid());
        }
        if record.effect_state == "committed" {
            let receipt = record
                .receipt
                .as_ref()
                .and_then(|v| v.as_object())
                .ok_or_else(invalid)?;
            if receipt.get("issue_id").and_then(|v| v.as_i64()) != Some(note.issue_id)
                || receipt.get("merge_ref").and_then(|v| v.as_str())
                    != Some(note.merge_ref.as_str())
                || receipt
                    .get("comment_id")
                    .and_then(|v| v.as_i64())
                    .filter(|id| *id > 0)
                    .is_none()
            {
                return Err(invalid());
            }
        }
        Ok(())
    }
    async fn lookup_note(
        &self,
        grant: &OperationGrant,
        operation: &str,
        note: &Note,
    ) -> Result<OperationRecord> {
        let lookup: Lookup = self
            .request(
                reqwest::Method::GET,
                &[&self.command, "operations", &grant.id, "journal", operation],
                None,
            )
            .await?;
        if lookup.installation_id != grant.installation_id || lookup.operation_id != operation {
            return Err(invalid());
        }
        let record = lookup.record.ok_or_else(unknown)?;
        if lookup.status != record.effect_state {
            return Err(invalid());
        }
        self.check_note_record(grant, operation, note, &record)?;
        Ok(record)
    }
    /// A fresh login may read the original profile's history. It cannot replace
    /// the captured source, deadline or grant, and this method never submits.
    pub async fn recover_note(
        &self,
        profile: &Path,
        grant: &OperationGrant,
        operation: &str,
    ) -> Result<OperationRecord> {
        if !hex(operation, 32)
            || grant.enrollment_id != self.command
            || grant.runtime_id != self.runtime
            || grant.actor.id != self.capability.account().account
        {
            return Err(invalid());
        }
        let directory = profile.join("fountain-operations");
        let bytes = read_bytes(&directory.join(format!("{operation}.intent.json")), 16384)
            .map_err(|_| unavailable())?;
        let capture: serde_json::Value = serde_json::from_slice(&bytes).map_err(|_| invalid())?;
        let scope = capture.as_object().ok_or_else(invalid)?;
        let text = |key: &str| scope.get(key).and_then(|v| v.as_str());
        if scope.len() != 8
            || scope.get("version").and_then(|v| v.as_u64()) != Some(1)
            || text("profile") != Some(self.runtime.as_str())
            || text("origin") != Some(self.capability.account().origin.as_str())
            || text("account") != Some(self.capability.account().account.as_str())
            || text("application") != Some(self.application.as_str())
            || text("enrollment") != Some(self.command.as_str())
            || !text("source_connection").is_some_and(|value| hex(value, 32))
        {
            return Err(invalid());
        }
        let intent = scope
            .get("intent")
            .and_then(|v| v.as_object())
            .ok_or_else(invalid)?;
        let field = |key: &str| intent.get(key).and_then(|v| v.as_str());
        if intent.len() != 10
            || field("operation_id") != Some(operation)
            || field("controller_grant_id") != Some(grant.id.as_str())
            || field("installation_id") != Some(grant.installation_id.as_str())
            || field("actor_id") != Some(grant.actor.id.as_str())
            || field("repository_id") != Some(grant.repository.id.as_str())
            || field("kind") != Some("issue.comment.publish")
            || field("authorization_revision") != Some(grant.id.as_str())
            || intent
                .get("expected_native_revision")
                .and_then(|v| v.as_i64())
                .filter(|n| *n > 0)
                .is_none()
            || intent
                .get("not_after")
                .and_then(|v| v.as_i64())
                .filter(|n| *n > 0 && *n <= grant.not_after)
                .is_none()
        {
            return Err(invalid());
        }
        let note: Note = serde_json::from_value(intent.get("payload").ok_or_else(invalid)?.clone())
            .map_err(|_| invalid())?;
        let receipt_path = directory.join(format!("{operation}.receipt.json"));
        let record = match read_bytes(&receipt_path, 65536) {
            Ok(bytes) => serde_json::from_slice(&bytes).map_err(|_| invalid())?,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                self.lookup_note(grant, operation, &note).await?
            }
            Err(_) => return Err(unavailable()),
        };
        self.check_note_record(grant, operation, &note, &record)?;
        if record.effect_state == "not_committed"
            || record.effect_state == "committed" && record.completion_state == "complete"
        {
            let bytes = serde_json::to_vec(&record).map_err(|_| invalid())?;
            if publish_bytes(&receipt_path, &bytes).is_err()
                && read_bytes(&receipt_path, 65536).map_err(|_| unavailable())? != bytes
            {
                return Err(unknown());
            }
        }
        Ok(record)
    }
    /// Append one typed completion note through the existing native effect
    /// owner. This cannot edit human text or publish an arbitrary issue comment.
    pub async fn submit_note(
        &self,
        profile: &Path,
        grant: &OperationGrant,
        operation: &str,
        revision: i64,
        not_after: i64,
        note: &Note,
    ) -> Result<OperationRecord> {
        if !hex(operation, 32)
            || revision < 1
            || not_after <= 0
            || not_after > grant.not_after
            || grant.enrollment_id != self.command
            || grant.runtime_id != self.runtime
            || grant.actor.id != self.capability.account().account
            || grant.repository.owner.to_ascii_lowercase() != self.owner
            || grant.repository.name.to_ascii_lowercase() != self.repository
            || !hex(&grant.id, 32)
            || !grant
                .operations
                .iter()
                .any(|kind| kind == "issue.comment.publish")
            || note.issue_id <= 0
            || note.content.trim().is_empty()
            || note.content.chars().count() > 1000
            || note.content.contains('\0')
            || note.merge_ref.is_empty()
            || note.merge_ref.len() > 512
        {
            return Err(invalid());
        }
        let intent = Intent {
            controller_grant_id: &grant.id,
            installation_id: &grant.installation_id,
            operation_id: operation,
            actor_id: &grant.actor.id,
            repository_id: &grant.repository.id,
            kind: "issue.comment.publish",
            authorization_revision: &grant.id,
            expected_native_revision: revision,
            not_after,
            payload: note,
        };
        let capture=serde_json::to_vec(&serde_json::json!({"version":1,"profile":self.runtime,"source_connection":self.capability.account().connection,
            "origin":self.capability.account().origin,"account":self.capability.account().account,"application":self.application,"enrollment":self.command,"intent":intent})).map_err(|_|invalid())?;
        let directory = profile.join("fountain-operations");
        private_dir(&directory).map_err(|_| unavailable())?;
        let path = directory.join(format!("{operation}.intent.json"));
        let receipt_path = directory.join(format!("{operation}.receipt.json"));
        let first = match read_bytes(&path, 16384) {
            Ok(prior) if prior == capture => false,
            Ok(_) => return Err(invalid()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                if publish_bytes(&path, &capture).is_ok() {
                    true
                } else {
                    if read_bytes(&path, 16384).map_err(|_| unavailable())? != capture {
                        return Err(invalid());
                    }
                    false
                }
            }
            Err(_) => return Err(unavailable()),
        };
        match read_bytes(&receipt_path, 65536) {
            Ok(bytes) => {
                let record = serde_json::from_slice(&bytes).map_err(|_| invalid())?;
                self.check_note_record(grant, operation, note, &record)?;
                return Ok(record);
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err(unavailable()),
        }
        let record = if first {
            let body = serde_json::to_vec(&intent).map_err(|_| invalid())?;
            match self
                .request(
                    reqwest::Method::POST,
                    &[&self.command, "operations", &grant.id, "submit"],
                    Some(body),
                )
                .await
            {
                Ok(record) => record,
                Err(_) => self.lookup_note(grant, operation, note).await?,
            }
        } else {
            self.lookup_note(grant, operation, note).await?
        };
        self.check_note_record(grant, operation, note, &record)?;
        if record.effect_state == "not_committed"
            || record.effect_state == "committed" && record.completion_state == "complete"
        {
            let bytes = serde_json::to_vec(&record).map_err(|_| invalid())?;
            if publish_bytes(&receipt_path, &bytes).is_err()
                && read_bytes(&receipt_path, 65536).map_err(|_| unavailable())? != bytes
            {
                return Err(unknown());
            }
        }
        Ok(record)
    }
}
