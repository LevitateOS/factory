//! Issue-only human collaboration over public OAuth/profile enrollment. The
//! existing native send fence and host human-operation journal own effects.
use super::{hex, invalid, unavailable, Client, Result};
use crate::github_provider::{CreateOutcome, Failure, WriteOperation};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::path::Path;

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Capture {
    version: u8,
    runtime: String,
    connection: String,
    origin: String,
    account: String,
    installation: String,
    enrollment: String,
    repository: String,
    command: String,
    digest: String,
    not_after: u64,
    kind: String,
    intent_digest: String,
    request: Value,
}
fn sha(bytes: &[u8]) -> String {
    Sha256::digest(bytes).iter().map(|b|format!("{b:02x}")).collect()
}
fn unknown() -> Failure { Failure::new(503,"fountain_issue_outcome_unresolved") }
fn method(kind: &str) -> Option<&'static str> {
    match kind {
        "create"=>Some("issue.create"), "comment"=>Some("discussion.comment.create"),
        "edit"=>Some("discussion.edit"), "state"=>Some("discussion.state.apply"),
        "lock"=>Some("discussion.lock.apply"), _=>None,
    }
}
fn success(kind: &str) -> &'static str {
    if matches!(kind,"create"|"comment") {"created"} else {"applied"}
}

impl Client {
    pub(crate) fn issue_collaboration_only(&self) -> bool { self.issues_only }

    pub(super) async fn issue_preparation(&self, kind: &str, number: u64) -> Result<Value> {
        if !self.issues_only || method(kind).is_none() && kind!="capabilities" { return Err(invalid()); }
        self.request(reqwest::Method::POST,&[&self.command,"prepare"],
            Some(serde_json::to_vec(&json!({"kind":kind,"number":number})).map_err(|_|invalid())?)).await
    }

    pub(super) async fn issue_capabilities(&self, repository: &str, number: Option<u32>) -> Result<Value> {
        let facts=self.issue_preparation("capabilities",u64::from(number.unwrap_or(0))).await?;
        if facts["repository_id"]!=repository || facts["actor_id"]!=self.capability.account().account {return Err(invalid());}
        let mut value=json!({"repository":repository,"name":format!("{}/{}",self.owner,self.repository),
            "issues_only":true,"archived":facts["archived"],"disabled":false,"issues_enabled":true,
            "can_create_issues":facts["can_create_issues"],"writer":facts["writer"],
            "merge":false,"squash":false,"rebase":false,"subject":null});
        if let Some(number)=number {
            if facts["index"]!=number.to_string() || facts["issue_id"].as_str().is_none() {return Err(invalid());}
            value["subject"]=json!({"kind":"issue","external_id":facts["issue_id"],"number":number,
                "state":if facts["is_closed"]==true {"closed"}else{"open"},"locked":facts["is_locked"],
                "can_update":facts["can_update"],"can_close":facts["can_close"],"can_reopen":facts["can_reopen"],
                "own":false,"draft":false,"merged":false,"merge_ready":false});
        }
        Ok(value)
    }

    fn issue_capture_matches(&self, c: &Capture, command: &str, digest: &str, not_after: u64) -> Result<()> {
        if !self.issues_only || c.version!=1 || c.runtime!=self.runtime || c.connection!=self.connection() ||
            c.origin!=self.origin() || c.account!=self.capability.account().account || c.enrollment!=self.command ||
            c.command!=command || c.digest!=digest || c.not_after!=not_after || !hex(command,32) || !hex(digest,64) ||
            !hex(&c.intent_digest,64) || c.installation.is_empty() || c.installation.len()>128 ||
            method(&c.kind).is_none() || c.request["kind"]!=c.kind ||
            c.request[c.kind.as_str()]["operation_id"]!=command || c.request[c.kind.as_str()]["not_after"]!=not_after ||
            c.request[c.kind.as_str()]["expected"]["actor_id"]!=c.account ||
            c.request[c.kind.as_str()]["expected"]["repository_id"]!=c.repository { return Err(invalid()); }
        if self.repository_id.lock().map_err(|_|unavailable())?.as_ref()!=Some(&c.repository) {return Err(invalid());}
        Ok(())
    }

    // An existing native capture authorizes lookup only. A missing remote
    // operation can never authorize a second POST, even before its deadline.
    pub(crate) async fn recover_issue_collaboration(&self, profile: &Path, command: &str, digest: &str, not_after: u64) -> Result<Option<(&'static str,CreateOutcome)>> {
        if !hex(command,32) || !hex(digest,64) {return Err(invalid());}
        let path=profile.join("fountain-issue-commands").join(format!("{command}.json"));
        let bytes=match read_bytes(&path,2*1024*1024) {
            Ok(bytes)=>bytes, Err(error) if error.kind()==std::io::ErrorKind::NotFound=>return Ok(None),
            Err(_)=>return Err(unavailable()),
        };
        let c:Capture=serde_json::from_slice(&bytes).map_err(|_|invalid())?;
        let current=self.status().await?;
        // Withdrawal still permits inspection of the original owner's journal.
        if current.repository.id!=c.repository {return Err(invalid());}
        self.issue_capture_matches(&c,command,digest,not_after)?;
        let lookup:Value=self.request(reqwest::Method::GET,&[&self.command,"commands",command],None).await?;
        if lookup["operation_id"]!=command || lookup["record"].is_null() {
            return Ok(Some((success(&c.kind),CreateOutcome::Indeterminate(unknown()))));
        }
        Ok(Some((success(&c.kind),self.issue_outcome(&c,&lookup["record"]))))
    }

    pub(crate) async fn collaborate_issue(&self, profile: &Path, command: &str, digest: &str, not_after: u64, operation: &WriteOperation) -> CreateOutcome {
        match self.prepare_issue_capture(profile,command,digest,not_after,operation).await {
            // A capture/storage conflict may hide an older host send. Only an
            // attributable completed host no-send can release that uncertainty.
            Err(_)=>CreateOutcome::Indeterminate(unknown()),
            Ok(None)=>match self.recover_issue_collaboration(profile,command,digest,not_after).await {
                Ok(Some((_,outcome)))=>outcome, _=>CreateOutcome::Indeterminate(unknown()),
            },
            Ok(Some(c))=>{
                let body=match serde_json::to_vec(&c.request) {Ok(body)=>body,Err(_)=>return CreateOutcome::NotSent(Failure::invalid())};
                match self.request::<Value>(reqwest::Method::POST,&[&self.command,"commands"],Some(body)).await {
                    Ok(record)=>self.issue_outcome(&c,&record),
                    Err(_)=>CreateOutcome::Indeterminate(unknown()),
                }
            }
        }
    }

    async fn prepare_issue_capture(&self, profile: &Path, command: &str, digest: &str, not_after: u64, operation: &WriteOperation) -> Result<Option<Capture>> {
        if !self.issues_only || !hex(command,32) || !hex(digest,64) ||
            not_after<=time::OffsetDateTime::now_utc().unix_timestamp().max(0) as u64 ||
            not_after>time::OffsetDateTime::now_utc().unix_timestamp().max(0) as u64+300 {return Err(invalid());}
        let (kind,args)=match operation {
            WriteOperation::CreateIssue(q)=>("create",serde_json::to_value(q)),
            WriteOperation::CreateComment(q)=>("comment",serde_json::to_value(q)),
            WriteOperation::EditDiscussion(q)=>("edit",serde_json::to_value(q)),
            WriteOperation::SetDiscussionState(q)=>("state",serde_json::to_value(q)),
            WriteOperation::SetDiscussionLock(q)=>("lock",serde_json::to_value(q)),
            _=>return Err(invalid()),
        };
        let args=args.map_err(|_|invalid())?;
        if kind!="create" && args["subject"]!="issue" {return Err(invalid());}
        let current=self.status().await?;
        if !current.active || args["repository"]!=current.repository.id {return Err(invalid());}
        let directory=profile.join("fountain-issue-commands");
        private_dir(&directory).map_err(|_|unavailable())?;
        let path=directory.join(format!("{command}.json"));
        match read_bytes(&path,2*1024*1024) {
            Ok(bytes)=>{
                let c:Capture=serde_json::from_slice(&bytes).map_err(|_|invalid())?;
                self.issue_capture_matches(&c,command,digest,not_after)?;
                return Ok(None);
            },
            Err(error) if error.kind()==std::io::ErrorKind::NotFound=>(),
            Err(_)=>return Err(unavailable()),
        }
        let mut prepare=json!({"kind":kind,"number":if kind=="create"{json!(0)}else{args["number"].clone()},"command_id":command,"not_after":not_after});
        for key in ["title","body","close","lock","reason","original_title","original_body_sha256"] {
            if let Some(value)=args.get(key).filter(|v|!v.is_null()) {prepare[key]=value.clone();}
        }
        let prepared:Value=self.request_bounded(reqwest::Method::POST,&[&self.command,"prepare"],Some(serde_json::to_vec(&prepare).map_err(|_|invalid())?),2*1024*1024).await?;
        let request=&prepared["request"];
        let payload=&request[kind];
        if request["kind"]!=kind || payload["operation_id"]!=command || payload["not_after"]!=not_after ||
            payload["expected"]["repository_id"]!=args["repository"] || payload["expected"]["actor_id"]!=current.actor.id ||
            kind!="create" && (payload["expected"]["subject"]!="issue" || payload["expected"]["subject_id"]!=args["subject_id"] || payload["expected"]["index"].as_str()!=args["number"].as_u64().map(|n|n.to_string()).as_deref()) {return Err(invalid());}
        for key in ["title","body","close","lock","reason"] {
            if args.get(key).is_some_and(|v|!v.is_null()) && payload[key]!=args[key] {return Err(invalid());}
        }
        let c=Capture{version:1,runtime:self.runtime.clone(),connection:self.connection().into(),origin:self.origin().into(),
            account:current.actor.id,
            installation:prepared["installation_id"].as_str().ok_or_else(invalid)?.into(),
            enrollment:self.command.clone(),repository:current.repository.id,
            command:command.into(),digest:digest.into(),not_after,kind:kind.into(),
            intent_digest:prepared["intent_digest"].as_str().ok_or_else(invalid)?.into(),request:request.clone()};
        self.issue_capture_matches(&c,command,digest,not_after)?;
        let bytes=serde_json::to_vec(&c).map_err(|_|invalid())?;
        if publish_bytes(&path,&bytes).is_err() {
            let saved:Capture=serde_json::from_slice(&read_bytes(&path,2*1024*1024).map_err(|_|unavailable())?).map_err(|_|invalid())?;
            self.issue_capture_matches(&saved,command,digest,not_after)?;
            return Ok(None);
        }
        Ok(Some(c))
    }

    fn issue_outcome(&self, c:&Capture, record:&Value) -> CreateOutcome {
        if record["operation_id"]!=c.command || record["installation_id"]!=c.installation || record["actor_id"]!=c.account || record["repository_id"]!=c.repository ||
            record["kind"]!=method(&c.kind).unwrap_or("") || record["intent_digest"]!=c.intent_digest ||
            record["completion_state"]!="complete" {return CreateOutcome::Indeterminate(unknown());}
        if record["effect_state"]=="not_committed" {return CreateOutcome::NotSent(Failure::new(409,"fountain_issue_not_committed"));}
        if record["effect_state"]!="committed" || !record["receipt"].is_object() || !record["installation_id"].is_string() {return CreateOutcome::Indeterminate(unknown());}
        let receipt=&record["receipt"];
        if receipt["repository_id"].as_i64().map(|id|id.to_string()).as_deref()!=Some(c.repository.as_str()) ||
            receipt["actor_id"].as_i64().map(|id|id.to_string()).as_deref()!=Some(c.account.as_str()) {return CreateOutcome::Indeterminate(unknown());}
        let raw=match serde_json::to_vec(record) {Ok(raw)=>raw,Err(_)=>return CreateOutcome::Indeterminate(unknown())};
        let payload=&c.request[c.kind.as_str()];
        let expected=&payload["expected"];
        let number=receipt["index"].as_u64().unwrap_or(0);
        let issue=receipt["issue_id"].as_i64().filter(|id|*id>0).map(|id|id.to_string());
        let Some(issue)=issue else {return CreateOutcome::Indeterminate(unknown())};
        if number==0 || c.kind!="create" && (expected["issue_id"]!=issue || expected["index"]!=number.to_string() || receipt["subject"]!="issue") {return CreateOutcome::Indeterminate(unknown());}
        let digest=|key:&str|payload[key].as_str().map(|text|sha(text.as_bytes())).unwrap_or_default();
        let mut result=json!({
            "fountain_issue":{"enrollment":c.enrollment,"command_id":c.command,"intent_digest":c.intent_digest,"record":record},
            "provider_response":{"protocol":"fountain_desktop_issue","status":200,"request_id":null,"response_sha256":sha(&raw)}});
        match c.kind.as_str() {
            "create"=>{
                if receipt["body_digest"]!=digest("body") || receipt["title"]!=payload["title"] {return CreateOutcome::Indeterminate(unknown());}
            },
            "comment"=>{
                let Some(comment)=receipt["comment_id"].as_i64().filter(|id|*id>0) else {return CreateOutcome::Indeterminate(unknown())};
                result["comment"]=json!({"external_id":comment.to_string(),"body_sha256":digest("body")});
            },
            kind=>{
                match kind {
                    "edit"=>{
                        if payload.get("title").is_some() && receipt["title"]!=payload["title"] || payload.get("body").is_some() && receipt["body_digest"]!=digest("body") {return CreateOutcome::Indeterminate(unknown());}
                    },
                    "state"=>{
                        if receipt["is_closed"]!=payload["close"] {return CreateOutcome::Indeterminate(unknown());}
                    },
                    "lock"=>{
                        if receipt["is_locked"]!=payload["lock"] || receipt["reason_digest"]!=digest("reason") {return CreateOutcome::Indeterminate(unknown());}
                    },
                    _=>return CreateOutcome::Indeterminate(unknown()),
                }
            },
        }
        if serde_json::to_vec(&result).map_or(true,|bytes|bytes.len()>4096) {return CreateOutcome::Indeterminate(unknown());}
        CreateOutcome::Created(result)
    }
}
