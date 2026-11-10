//! A retained candidate has one registration and one conditional Git send.
//! Recovery reads the original host journal; it never repeats either send.
use super::{hex, invalid, unavailable, Client, OperationGrant, OperationRecord, Result};
use crate::github_provider::{BranchPublish, Failure as NativeFailure, WriteOutcome};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::path::Path;

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Capture {
    version: u8,
    profile: String,
    connection: String,
    origin: String,
    account: String,
    enrollment: String,
    command: String,
    digest: String,
    not_after: u64,
    branch: BranchPublish,
    intent: Value,
    intent_digest: String,
}
#[derive(Serialize)]
struct Canonical<'a> {
    operation_id: &'a str,
    actor_id: i64,
    repository_id: i64,
    kind: &'static str,
    authorization_revision: &'a str,
    expected_native_revision: i64,
    not_after: u64,
    payload: &'a Value,
}
fn sha(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn failure(error: super::Failure) -> NativeFailure {
    NativeFailure::new(error.status, error.code)
}
fn unknown() -> NativeFailure {
    NativeFailure::new(503, "fountain_publication_unresolved")
}
impl Client {
    pub(super) async fn publication_refs(
        &self,
        repository: &str,
        name: &str,
        comparison: &str,
    ) -> Result<Value> {
        let (raw, _): (Value, _) = self
            .request_url(
                reqwest::Method::GET,
                self.product_url(&[])?,
                None,
                512 * 1024,
            )
            .await?;
        let target = format!("refs/heads/{name}");
        let base = format!("refs/heads/{comparison}");
        let (_, snapshot) = self
            .snapshot(json!({"families":["refs"],"refs":[target,base]}))
            .await?;
        let refs = snapshot["refs"]
            .as_array()
            .filter(|v| v.len() == 2)
            .ok_or_else(invalid)?;
        let tip = |reference: &str| -> Result<String> {
            let r = refs
                .iter()
                .find(|r| r["ref"] == reference)
                .ok_or_else(invalid)?;
            if r["visible"] != true || r["complete"] != true {
                return Err(invalid());
            }
            match &r["exists"] {
                Value::Null | Value::Bool(false) => Ok(String::new()),
                Value::Bool(true) => r["oid"]
                    .as_str()
                    .filter(|v| hex(v, 40))
                    .map(str::to_owned)
                    .ok_or_else(invalid),
                _ => Err(invalid()),
            }
        };
        let before = raw.clone();
        let (after, _): (Value, _) = self
            .request_url(
                reqwest::Method::GET,
                self.product_url(&[])?,
                None,
                512 * 1024,
            )
            .await?;
        if before != after
            || super::product::id(&raw["id"])? != repository
            || raw["full_name"] != format!("{}/{}", self.owner, self.repository)
        {
            return Err(invalid());
        }
        Ok(
            json!({"repository":repository,"name":raw["full_name"],"repository_node_id":format!("fountain:repository:{repository}"),
            "push":raw["permissions"]["push"].as_bool().ok_or_else(invalid)?,"archived":raw["archived"].as_bool().ok_or_else(invalid)?,"disabled":false,
            "branch":name,"tip":tip(&target)?,"comparison_branch":comparison,"comparison":tip(&base)?}),
        )
    }
    fn publication_capture(&self, profile: &Path, command: &str) -> Result<Capture> {
        if !hex(command, 32) {
            return Err(invalid());
        }
        let bytes = read_bytes(
            &profile
                .join("fountain-operations")
                .join(format!("{command}.publication.json")),
            16384,
        )
        .map_err(|_| unavailable())?;
        let capture: Capture = serde_json::from_slice(&bytes).map_err(|_| invalid())?;
        if capture.version != 1
            || capture.profile != self.runtime
            || capture.connection != self.connection()
            || capture.origin != self.origin()
            || capture.account != self.capability.account().account
            || capture.enrollment != self.command
            || capture.command != command
            || !capture.branch.bundle.is_empty()
            || !hex(&capture.digest, 64)
            || !hex(&capture.intent_digest, 64)
        {
            return Err(invalid());
        }
        Ok(capture)
    }
    async fn publication_record(
        &self,
        capture: &Capture,
        grant: &OperationGrant,
    ) -> Result<OperationRecord> {
        let response: Value = self
            .request(
                reqwest::Method::GET,
                &[
                    &self.command,
                    "operations",
                    &grant.id,
                    "journal",
                    &capture.command,
                ],
                None,
            )
            .await?;
        if response["installation_id"] != grant.installation_id
            || response["operation_id"] != capture.command
        {
            return Err(invalid());
        }
        let record: OperationRecord =
            serde_json::from_value(response["record"].clone()).map_err(|_| unavailable())?;
        self.check_publication_record(capture, grant, &record)?;
        if response["status"] != record.effect_state {
            return Err(invalid());
        }
        Ok(record)
    }
    fn check_publication_record(
        &self,
        c: &Capture,
        g: &OperationGrant,
        r: &OperationRecord,
    ) -> Result<()> {
        if r.controller_grant_id != g.id
            || r.installation_id != g.installation_id
            || r.operation_id != c.command
            || r.actor_id != c.account
            || r.repository_id != c.branch.repository
            || r.kind != "git.ref.publish"
            || r.intent_digest != c.intent_digest
            || r.outcome != r.effect_state
            || !matches!(
                r.effect_state.as_str(),
                "pending" | "committed" | "not_committed" | "indeterminate"
            )
        {
            return Err(invalid());
        }
        if r.effect_state == "committed" {
            let p = r.receipt.as_ref().ok_or_else(invalid)?;
            let q = &c.branch;
            if p["repository_id"]
                .as_i64()
                .map(|n| n.to_string())
                .as_deref()
                != Some(q.repository.as_str())
                || p["actor_id"].as_i64().map(|n| n.to_string()).as_deref()
                    != Some(c.account.as_str())
                || p["ref"] != format!("refs/heads/{}", q.branch)
                || p["new_oid"] != q.candidate
                || p["old_oid"].as_str()
                    != Some(if q.expected_old.is_empty() {
                        "0000000000000000000000000000000000000000"
                    } else {
                        q.expected_old.as_str()
                    })
                || p["comparison_ref"] != format!("refs/heads/{}", q.comparison_branch)
                || p["comparison_oid"] != q.comparison_oid
            {
                return Err(invalid());
            }
        }
        Ok(())
    }
    fn publication_outcome(
        &self,
        c: &Capture,
        g: &OperationGrant,
        r: &OperationRecord,
    ) -> WriteOutcome {
        if self.check_publication_record(c, g, r).is_err() {
            return WriteOutcome::Indeterminate(unknown());
        }
        if r.effect_state == "not_committed" {
            return WriteOutcome::NotSent(NativeFailure::new(
                409,
                "fountain_publication_not_committed",
            ));
        }
        if r.effect_state != "committed" || r.completion_state != "complete" {
            return WriteOutcome::Indeterminate(unknown());
        }
        let q = &c.branch;
        let response = match serde_json::to_vec(r) {
            Ok(bytes) => sha(&bytes),
            Err(_) => return WriteOutcome::Indeterminate(unknown()),
        };
        WriteOutcome::Applied(
            json!({"account":{"external_id":g.actor.id,"login":g.actor.username},"repository":q.repository,
            "name":format!("{}/{}",self.owner,self.repository),"repository_condition":"current_host_permission",
            "branch":format!("refs/heads/{}",q.branch),"old":q.expected_old,"new":q.candidate,"comparison":q.comparison_oid,
            "comparison_branch":format!("refs/heads/{}",q.comparison_branch),"comparison_condition":"atomic_comparison_oid",
            "ref_condition":"exact_old_oid","bundle_sha256":q.bundle_sha256,"provider_response_sha256":response,
            "host_operation":{"grant_id":g.id,"installation_id":g.installation_id,"operation_id":c.command,
                "intent_digest":r.intent_digest,"receipt":r.receipt}}),
        )
    }
    /// Only the captured original operation can be adopted, including after its
    /// send deadline. Neither lookup absence nor current target state dispatches.
    pub async fn recover_publication(
        &self,
        profile: &Path,
        command: &str,
        digest: &str,
        not_after: u64,
    ) -> Result<Option<WriteOutcome>> {
        let path = profile
            .join("fountain-operations")
            .join(format!("{command}.publication.json"));
        match std::fs::symlink_metadata(&path) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(_) => return Err(unavailable()),
            Ok(_) => (),
        }
        let c = self.publication_capture(profile, command)?;
        if c.digest != digest || c.not_after != not_after {
            return Err(invalid());
        }
        let grant = self.operation_grant(&c.branch.controller_grant_id).await?;
        let record = self.publication_record(&c, &grant).await?;
        Ok(Some(self.publication_outcome(&c, &grant, &record)))
    }
    pub(crate) async fn publish_candidate(
        &self,
        profile: &Path,
        command: &str,
        digest: &str,
        not_after: u64,
        q: &BranchPublish,
    ) -> WriteOutcome {
        let mut sent = false;
        let result=async {
            q.validate().map_err(|_|invalid())?;
            if !hex(command,32)||!hex(digest,64)||!hex(&q.controller_grant_id,32)||q.source_connection!=self.connection() { return Err(invalid()); }
            let pack=q.pack().map_err(|_|invalid())?;
            let (owner,grant)=super::captured_operation_grant(self.credentials.clone(),profile,&self.runtime,self.connection(),&q.controller_grant_id).await?;
            if owner.command!=self.command || owner.origin()!=self.origin() || grant.enrollment_id!=self.command { return Err(invalid()); }
            let current=self.status().await?;
            if q.repository!=current.repository.id || !grant.allows_current("git.ref.publish")
                || not_after<=time::OffsetDateTime::now_utc().unix_timestamp() as u64 || not_after>grant.not_after as u64
                || not_after>(time::OffsetDateTime::now_utc().unix_timestamp() as u64).saturating_add(300) { return Err(invalid()); }
            let revision=self.native_revision().await?;
            if !revision.idle||revision.inhibited { return Err(invalid()); }
            let payload=json!({"ref":format!("refs/heads/{}",q.branch),"expected_old":if q.expected_old.is_empty(){"absent"}else{&q.expected_old},
                "new_oid":q.candidate,"comparison_ref":format!("refs/heads/{}",q.comparison_branch),"expected_comparison_oid":q.comparison_oid});
            let canonical=Canonical{operation_id:command,actor_id:grant.actor.id.parse().map_err(|_|invalid())?,repository_id:q.repository.parse().map_err(|_|invalid())?,
                kind:"git.ref.publish",authorization_revision:&grant.id,expected_native_revision:revision.revision,not_after,payload:&payload};
            let bytes=serde_json::to_string(&canonical).map_err(|_|invalid())?.replace('&',"\\u0026").replace('<',"\\u003c").replace('>',"\\u003e").replace('\u{2028}',"\\u2028").replace('\u{2029}',"\\u2029");
            let intent=json!({"controller_grant_id":grant.id,"installation_id":grant.installation_id,"operation_id":command,"actor_id":grant.actor.id,
                "repository_id":q.repository,"kind":"git.ref.publish","authorization_revision":grant.id,"expected_native_revision":revision.revision,"not_after":not_after,"payload":payload});
            let mut metadata=q.clone(); metadata.bundle.clear();
            let c=Capture{version:1,profile:self.runtime.clone(),connection:self.connection().into(),origin:self.origin().into(),account:grant.actor.id.clone(),enrollment:self.command.clone(),
                command:command.into(),digest:digest.into(),not_after,branch:metadata,intent,intent_digest:sha(bytes.as_bytes())};
            let dir=profile.join("fountain-operations"); private_dir(&dir).map_err(|_|unavailable())?;
            let capture=serde_json::to_vec(&c).map_err(|_|invalid())?;
            // This is the existing operation's one-shot fence. Even a crash
            // before registration can only recover, never repeat registration.
            publish_bytes(&dir.join(format!("{command}.publication.json")),&capture).map_err(|_|super::Failure{status:409,code:"fountain_publication_history_held"})?;
            sent=true;
            let registered:OperationRecord=match self.request(reqwest::Method::POST,&[&self.command,"operations",&grant.id,"submit"],Some(serde_json::to_vec(&c.intent).map_err(|_|invalid())?)).await {
                Ok(record)=>record,Err(_)=>self.publication_record(&c,&grant).await?
            };
            self.check_publication_record(&c,&grant,&registered)?;
            if registered.effect_state!="pending" { return Ok(self.publication_outcome(&c,&grant,&registered)); }
            // The registration is known; transfer is its only remaining phase.
            // Persist its separate send fence before touching the receive route.
            let current=self.operation_grant(&grant.id).await?;
            if !current.allows_current("git.ref.publish") || time::OffsetDateTime::now_utc().unix_timestamp() as u64>=not_after { return Err(invalid()); }
            publish_bytes(&dir.join(format!("{command}.receive.json")),&capture).map_err(|_|unavailable())?;
            let old=if q.expected_old.is_empty(){"0000000000000000000000000000000000000000"}else{&q.expected_old};
            let line=format!("{old} {} refs/heads/{}\0report-status ofs-delta\n",q.candidate,q.branch);
            let mut body=format!("{:04x}{line}0000",line.len()+4).into_bytes(); body.extend_from_slice(&pack);
            let mut url=self.base.clone(); url.path_segments_mut().map_err(|_|invalid())?.clear().extend([self.owner.as_str(),&format!("{}.git",self.repository),"git-receive-pack"]);
            // The host journal supplies the causal receipt, including lost or
            // interrupted wire replies. A reply is never grounds for a resend.
            let _reply=self.request_wire(reqwest::Method::POST,url,Some(body),"application/x-git-receive-pack-request",Some((&grant.id,command)),2*1024*1024).await;
            let record=self.publication_record(&c,&grant).await?;
            Ok(self.publication_outcome(&c,&grant,&record))
        }.await;
        match result {
            Ok(outcome) => outcome,
            Err(error) if sent => WriteOutcome::Indeterminate(failure(error)),
            Err(error) => WriteOutcome::NotSent(failure(error)),
        }
    }
}
