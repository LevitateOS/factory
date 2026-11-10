//! A retained candidate has one registration and one conditional Git send.
//! Recovery reads the original host journal; it never repeats either send.
use super::{hex, invalid, unavailable, Client, OperationGrant, OperationRecord, Result};
use crate::github_provider::{BranchPublish, Failure as NativeFailure, PullCreate, WriteOutcome};
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
    #[serde(default, skip_serializing_if = "Option::is_none")]
    branch: Option<BranchPublish>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pull: Option<PullCreate>,
    intent: Value,
    intent_digest: String,
}
impl Capture {
    fn scope(&self) -> Result<(&str, &str, &str)> {
        match (&self.branch, &self.pull) {
            (Some(q), None) if self.version == 1 && q.bundle.is_empty() => {
                Ok(("git.ref.publish", &q.repository, &q.controller_grant_id))
            }
            (None, Some(q)) if self.version == 2 => {
                Ok(("pull_request.create", &q.repository, &q.controller_grant_id))
            }
            _ => Err(invalid()),
        }
    }
}
enum Request {
    Ref(BranchPublish),
    Pull(PullCreate),
}
pub enum PublicationOutcome {
    Ref(WriteOutcome),
    Pull(WriteOutcome),
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
        let selected = if target == base {
            vec![target.as_str()]
        } else {
            vec![target.as_str(), base.as_str()]
        };
        let (_, snapshot) = self
            .snapshot(json!({"families":["refs"],"refs":selected}))
            .await?;
        let refs = snapshot["refs"]
            .as_array()
            .filter(|v| v.len() == selected.len())
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
    pub(super) async fn pull_branches(
        &self,
        repository: &str,
        head_repository: &str,
        base: &str,
        head: &str,
    ) -> Result<Value> {
        if repository != head_repository {
            return Err(super::Failure {
                status: 503,
                code: "fountain_fork_publication_unavailable",
            });
        }
        let refs = self.publication_refs(repository, head, base).await?;
        let (raw, _): (Value, _) = self
            .request_url(
                reqwest::Method::GET,
                self.product_url(&[])?,
                None,
                512 * 1024,
            )
            .await?;
        if super::product::id(&raw["id"])? != repository || raw["full_name"] != refs["name"] {
            return Err(invalid());
        }
        let owner = super::product::id(&raw["owner"]["id"])?;
        let owner_login = super::product::text(&raw["owner"], "login", 256)?;
        let owner_type = raw["owner"]["type"]
            .as_str()
            .filter(|v| matches!(*v, "User" | "Organization"))
            .unwrap_or("");
        let repository_facts = json!({"external_id":repository,"node_id":format!("fountain:repository:{repository}"),"name":raw["full_name"],
            "owner":{"external_id":owner,"node_id":format!("fountain:actor:{owner}"),"login":owner_login,"type":owner_type},
            "private":raw["private"].as_bool(),"archived":raw["archived"].as_bool(),"disabled":false,"fork":raw["fork"].as_bool(),"push":raw["permissions"]["push"].as_bool(),"parent_repository":null,"source_repository":null});
        let side = |name: &str, tip: &Value| -> Result<Value> {
            if !tip.as_str().is_some_and(|v| hex(v, 40)) {
                return Err(invalid());
            }
            Ok(
                json!({"repository":repository_facts,"branch":{"name":name,"node_id":format!("fountain:branch:{repository}:{name}"),"commit":tip}}),
            )
        };
        Ok(json!({"base":side(base,&refs["comparison"])?,"head":side(head,&refs["tip"])?}))
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
        let (_, repository, grant) = capture.scope()?;
        if capture.profile != self.runtime
            || capture.connection != self.connection()
            || capture.origin != self.origin()
            || capture.account != self.capability.account().account
            || capture.enrollment != self.command
            || capture.command != command
            || !hex(grant, 32)
            || repository.parse::<i64>().is_err()
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
        let (kind, repository, grant) = c.scope()?;
        if r.controller_grant_id != g.id
            || grant != g.id
            || r.installation_id != g.installation_id
            || r.operation_id != c.command
            || r.actor_id != c.account
            || r.repository_id != repository
            || r.kind != kind
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
            if let Some(q) = &c.pull {
                if p["repository_id"]
                    .as_i64()
                    .map(|n| n.to_string())
                    .as_deref()
                    != Some(q.repository.as_str())
                    || p["author_id"].as_i64().map(|n| n.to_string()).as_deref()
                        != Some(c.account.as_str())
                    || !["pr_id", "issue_id", "pr_number"]
                        .iter()
                        .all(|key| p[key].as_i64().is_some_and(|n| n > 0))
                    || p["head_ref"] != format!("refs/heads/{}", q.head_branch)
                    || p["base_ref"] != format!("refs/heads/{}", q.base_branch)
                    || p["head_oid"] != q.head_pin.commit
                    || p["base_oid"] != q.base_pin.commit
                {
                    return Err(invalid());
                }
                return Ok(());
            }
            let q = c.branch.as_ref().ok_or_else(invalid)?;
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
        let response = match serde_json::to_vec(r) {
            Ok(bytes) => sha(&bytes),
            Err(_) => return WriteOutcome::Indeterminate(unknown()),
        };
        let host = json!({"grant_id":g.id,"installation_id":g.installation_id,"operation_id":c.command,"intent_digest":r.intent_digest,"receipt":r.receipt});
        if let Some(q) = &c.pull {
            let Some(p)=r.receipt.as_ref() else{return WriteOutcome::Indeterminate(unknown())};
            let (Some(id),Some(number),Some(issue))=(p["pr_id"].as_i64(),p["pr_number"].as_i64(),p["issue_id"].as_i64()) else{return WriteOutcome::Indeterminate(unknown())};
            let (id,issue)=(id.to_string(),issue.to_string());
            let mut url = self.base.clone();
            if url
                .path_segments_mut()
                .map(|mut path| {
                    path.clear().extend([
                        self.owner.as_str(),
                        self.repository.as_str(),
                        "pulls",
                        &number.to_string(),
                    ]);
                })
                .is_err()
            {
                return WriteOutcome::Indeterminate(unknown());
            }
            return WriteOutcome::Applied(
                json!({"account":{"external_id":g.actor.id,"login":g.actor.username},"repository":q.repository,"head_repository":q.head_repository,
                "base":{"branch":q.base_branch,"pin":q.base_pin},"head":{"branch":q.head_branch,"pin":q.head_pin},"branch_condition":"atomic_head_base",
                "pull":{"external_id":id,"issue_id":issue,"node_id":format!("fountain:pull:{}:{id}",q.repository),"number":number,"url":url.as_str(),
                    "author":{"external_id":g.actor.id,"node_id":format!("fountain:actor:{}",g.actor.id),"login":g.actor.username},"state":"open","draft":false,
                    "allow_maintainer_edit":false,"title_sha256":sha(q.title.as_bytes()),"body_sha256":sha(q.body.as_bytes()),"created_at":null,"updated_at":null},
                "provider_response":{"protocol":"fountain_native_operation","status":200,"request_id":null,"response_sha256":response},"host_operation":host}),
            );
        }
        let q = match &c.branch {
            Some(q) => q,
            None => return WriteOutcome::Indeterminate(unknown()),
        };
        WriteOutcome::Applied(
            json!({"account":{"external_id":g.actor.id,"login":g.actor.username},"repository":q.repository,
            "name":format!("{}/{}",self.owner,self.repository),"repository_condition":"current_host_permission",
            "branch":format!("refs/heads/{}",q.branch),"old":q.expected_old,"new":q.candidate,"comparison":q.comparison_oid,
            "comparison_branch":format!("refs/heads/{}",q.comparison_branch),"comparison_condition":"atomic_comparison_oid",
            "ref_condition":"exact_old_oid","bundle_sha256":q.bundle_sha256,"provider_response_sha256":response,
            "host_operation":host}),
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
    ) -> Result<Option<PublicationOutcome>> {
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
        let (_, _, grant_id) = c.scope()?;
        let grant = self.operation_grant(grant_id).await?;
        let record = self.publication_record(&c, &grant).await?;
        let outcome = self.publication_outcome(&c, &grant, &record);
        Ok(Some(if c.pull.is_some() {
            PublicationOutcome::Pull(outcome)
        } else {
            PublicationOutcome::Ref(outcome)
        }))
    }
    async fn register_publication(
        &self,
        profile: &Path,
        command: &str,
        digest: &str,
        not_after: u64,
        request: Request,
        sent: &mut bool,
    ) -> Result<(Capture, OperationGrant, OperationRecord)> {
        let (repository, grant_id, connection, kind, payload, branch, pull, version) = match request
        {
            Request::Ref(mut q) => {
                q.bundle.clear();
                let payload = json!({"ref":format!("refs/heads/{}",q.branch),"expected_old":if q.expected_old.is_empty(){"absent"}else{&q.expected_old},
                    "new_oid":q.candidate,"comparison_ref":format!("refs/heads/{}",q.comparison_branch),"expected_comparison_oid":q.comparison_oid});
                (
                    q.repository.clone(),
                    q.controller_grant_id.clone(),
                    q.source_connection.clone(),
                    "git.ref.publish",
                    payload,
                    Some(q),
                    None,
                    1,
                )
            }
            Request::Pull(q) => {
                let payload = json!({"head_repository_id":q.head_repository.parse::<i64>().map_err(|_|invalid())?,"head_ref":format!("refs/heads/{}",q.head_branch),"base_ref":format!("refs/heads/{}",q.base_branch),
                    "expected_head_oid":q.head_pin.commit,"expected_base_oid":q.base_pin.commit,"title":q.title,"body":q.body,"allow_maintainer_edit":false});
                (
                    q.repository.clone(),
                    q.controller_grant_id.clone(),
                    q.source_connection.clone(),
                    "pull_request.create",
                    payload,
                    None,
                    Some(q),
                    2,
                )
            }
        };
        if !hex(command, 32)
            || !hex(digest, 64)
            || !hex(&grant_id, 32)
            || connection != self.connection()
        {
            return Err(invalid());
        }
        let (owner, grant) = super::captured_operation_grant(
            self.credentials.clone(),
            profile,
            &self.runtime,
            self.connection(),
            &grant_id,
        )
        .await?;
        if owner.command != self.command
            || owner.origin() != self.origin()
            || grant.enrollment_id != self.command
        {
            return Err(invalid());
        }
        let current = self.status().await?;
        let now = time::OffsetDateTime::now_utc().unix_timestamp() as u64;
        if repository != current.repository.id
            || !grant.allows_current(kind)
            || not_after <= now
            || not_after > grant.not_after as u64
            || not_after > now.saturating_add(300)
        {
            return Err(invalid());
        }
        let revision = self.native_revision().await?;
        if !revision.idle || revision.inhibited {
            return Err(invalid());
        }
        let canonical = Canonical {
            operation_id: command,
            actor_id: grant.actor.id.parse().map_err(|_| invalid())?,
            repository_id: repository.parse().map_err(|_| invalid())?,
            kind,
            authorization_revision: &grant.id,
            expected_native_revision: revision.revision,
            not_after,
            payload: &payload,
        };
        let bytes = serde_json::to_string(&canonical)
            .map_err(|_| invalid())?
            .replace('&', "\\u0026")
            .replace('<', "\\u003c")
            .replace('>', "\\u003e")
            .replace('\u{2028}', "\\u2028")
            .replace('\u{2029}', "\\u2029");
        let intent = json!({"controller_grant_id":grant.id,"installation_id":grant.installation_id,"operation_id":command,"actor_id":grant.actor.id,
            "repository_id":repository,"kind":kind,"authorization_revision":grant.id,"expected_native_revision":revision.revision,"not_after":not_after,"payload":payload});
        let capture = Capture {
            version,
            profile: self.runtime.clone(),
            connection,
            origin: self.origin().into(),
            account: grant.actor.id.clone(),
            enrollment: self.command.clone(),
            command: command.into(),
            digest: digest.into(),
            not_after,
            branch,
            pull,
            intent,
            intent_digest: sha(bytes.as_bytes()),
        };
        let dir = profile.join("fountain-operations");
        private_dir(&dir).map_err(|_| unavailable())?;
        // Existing history is a fence even when no registration reply survived.
        publish_bytes(
            &dir.join(format!("{command}.publication.json")),
            &serde_json::to_vec(&capture).map_err(|_| invalid())?,
        )
        .map_err(|_| super::Failure {
            status: 409,
            code: "fountain_publication_history_held",
        })?;
        *sent = true;
        let record: OperationRecord = match self
            .request(
                reqwest::Method::POST,
                &[&self.command, "operations", &grant.id, "submit"],
                Some(serde_json::to_vec(&capture.intent).map_err(|_| invalid())?),
            )
            .await
        {
            Ok(record) => record,
            Err(_) => self.publication_record(&capture, &grant).await?,
        };
        self.check_publication_record(&capture, &grant, &record)?;
        Ok((capture, grant, record))
    }
    pub(crate) async fn create_pull(
        &self,
        profile: &Path,
        command: &str,
        digest: &str,
        not_after: u64,
        q: &PullCreate,
    ) -> WriteOutcome {
        let mut sent = false;
        let result = async {
            q.validate().map_err(|_| invalid())?;
            if q.repository != q.head_repository || q.allow_maintainer_edit {
                return Err(invalid());
            }
            let branches = self
                .pull_branches(
                    &q.repository,
                    &q.head_repository,
                    &q.base_branch,
                    &q.head_branch,
                )
                .await?;
            for (side, pin) in [
                (&branches["base"], &q.base_pin),
                (&branches["head"], &q.head_pin),
            ] {
                if side["repository"]["node_id"] != pin.repository_node_id
                    || side["repository"]["owner"]["node_id"] != pin.owner_node_id
                    || side["branch"]["node_id"] != pin.reference_node_id
                    || side["branch"]["commit"] != pin.commit
                {
                    return Err(invalid());
                }
            }
            let (capture, grant, record) = self
                .register_publication(
                    profile,
                    command,
                    digest,
                    not_after,
                    Request::Pull(q.clone()),
                    &mut sent,
                )
                .await?;
            Ok(self.publication_outcome(&capture, &grant, &record))
        }
        .await;
        match result {
            Ok(outcome) => outcome,
            Err(error) if sent => WriteOutcome::Indeterminate(failure(error)),
            Err(error) => WriteOutcome::NotSent(failure(error)),
        }
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
        let result = async {
            q.validate().map_err(|_| invalid())?;
            let pack = q.pack().map_err(|_| invalid())?;
            let (c, grant, registered) = self
                .register_publication(
                    profile,
                    command,
                    digest,
                    not_after,
                    Request::Ref(q.clone()),
                    &mut sent,
                )
                .await?;
            let dir = profile.join("fountain-operations");
            let capture = serde_json::to_vec(&c).map_err(|_| invalid())?;
            if registered.effect_state != "pending" {
                return Ok(self.publication_outcome(&c, &grant, &registered));
            }
            // The registration is known; transfer is its only remaining phase.
            // Persist its separate send fence before touching the receive route.
            let current = self.operation_grant(&grant.id).await?;
            if !current.allows_current("git.ref.publish")
                || time::OffsetDateTime::now_utc().unix_timestamp() as u64 >= not_after
            {
                return Err(invalid());
            }
            publish_bytes(&dir.join(format!("{command}.receive.json")), &capture)
                .map_err(|_| unavailable())?;
            let old = if q.expected_old.is_empty() {
                "0000000000000000000000000000000000000000"
            } else {
                &q.expected_old
            };
            let line = format!(
                "{old} {} refs/heads/{}\0report-status ofs-delta\n",
                q.candidate, q.branch
            );
            let mut body = format!("{:04x}{line}0000", line.len() + 4).into_bytes();
            body.extend_from_slice(&pack);
            let mut url = self.base.clone();
            url.path_segments_mut()
                .map_err(|_| invalid())?
                .clear()
                .extend([
                    self.owner.as_str(),
                    &format!("{}.git", self.repository),
                    "git-receive-pack",
                ]);
            // The host journal supplies the causal receipt, including lost or
            // interrupted wire replies. A reply is never grounds for a resend.
            let _reply = self
                .request_wire(
                    reqwest::Method::POST,
                    url,
                    Some(body),
                    "application/x-git-receive-pack-request",
                    Some((&grant.id, command)),
                    2 * 1024 * 1024,
                )
                .await;
            let record = self.publication_record(&c, &grant).await?;
            Ok(self.publication_outcome(&c, &grant, &record))
        }
        .await;
        match result {
            Ok(outcome) => outcome,
            Err(error) if sent => WriteOutcome::Indeterminate(failure(error)),
            Err(error) => WriteOutcome::NotSent(failure(error)),
        }
    }
}
