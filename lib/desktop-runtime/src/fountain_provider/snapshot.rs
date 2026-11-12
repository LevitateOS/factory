//! Current native issue history and permissions through public profile-bound
//! reads. The host owns provenance, visibility and revision bracketing; Go owns
//! acceptance, spending and every resulting work decision.
use super::product::text;
use super::{invalid, Client, Result};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeSet;

fn remote(value: &Value) -> Result<&str> {
    value
        .as_str()
        .filter(|id| {
            id.parse::<i64>()
                .is_ok_and(|n| n > 0 && n.to_string() == *id)
        })
        .ok_or_else(invalid)
}
fn version(value: &Value) -> Result<u64> {
    if value.is_null() {
        return Ok(0);
    }
    value
        .as_u64()
        .filter(|n| *n <= i32::MAX as u64)
        .ok_or_else(invalid)
}
fn items<'a>(page: &'a Value, issue: &str) -> Result<&'a [Value]> {
    if page["issue_id"] != issue || page["complete"] != true {
        return Err(invalid());
    }
    let values = match &page["items"] {
        Value::Null => &[][..],
        Value::Array(items) if items.len() <= 50 => items.as_slice(),
        _ => return Err(invalid()),
    };
    if page["total"].as_u64() != Some(values.len() as u64) {
        return Err(invalid());
    }
    Ok(values)
}
fn hash(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn content<'a>(value: &'a Value, key: &str) -> Result<&'a str> {
    if value[key].is_null() {
        Ok("")
    } else {
        text(value, key, 65536)
    }
}
fn unix(value: &Value) -> Result<i64> {
    if value.is_null() {
        return Ok(0);
    }
    time::OffsetDateTime::parse(
        value.as_str().ok_or_else(invalid)?,
        &time::format_description::well_known::Rfc3339,
    )
    .map(|date| date.unix_timestamp())
    .map_err(|_| invalid())
}
impl Client {
    pub(super) async fn merge_completion(&self,repository:&str,number:u32)->Result<Value> {
        let before=self.product_pull(number,repository).await?;
        if before["merged"]!=true || before["state"]!="merged" {return Err(invalid());}
        let base_ref=format!("refs/heads/{}",text(&before["base"],"name",512)?);
        let head_ref=format!("refs/heads/{}",text(&before["head"],"name",512)?);
        let tip=self.product_tip(&base_ref).await?;
        let (raw,_):(Value,_)=self.request_url(reqwest::Method::GET,self.product_url(&["pulls",&number.to_string()])?,None,512*1024).await?;
        let commit=text(&raw,"merge_commit_sha",40)?;
        if !super::hex(commit,40) || raw["merged"]!=true || raw["number"].as_u64()!=Some(u64::from(number)) || super::product::id(&raw["id"])?!=before["external_id"] {return Err(invalid());}
        let selection=json!({"families":["pull","refs","ancestry"],"pull_number":number.to_string(),"refs":[base_ref],"ancestry":{"ancestor_oid":commit,"descendant_oid":tip}});
        let (revision,snapshot)=self.snapshot(selection.clone()).await?;
        let pull=&snapshot["pull"];let ancestry=&snapshot["ancestry"];
        let merged=super::product::date(&raw,"merged_at")?;
        let closed=super::product::date(&raw,"closed_at")?;
        let merger=super::product::actor(&raw["merged_by"])?;
        let head=text(&before["head"],"commit",40)?;
        if pull["visible"]!=true || pull["complete"]!=true || pull["has_merged"]!=true || pull["merged_commit"]!=commit || pull["head_tip"]!=head
            || pull["merger_id"]!=merger["external_id"] || unix(&merged)?<=0 || pull["merged_unix"].as_i64()!=Some(unix(&merged)?) || unix(&closed)?<=0
            || ancestry["ancestor_oid"]!=commit || ancestry["descendant_oid"]!=tip || !ancestry["reachable"].is_boolean(){return Err(invalid());}
        let (after_revision,after)=self.snapshot(selection).await?;
        if revision!=after_revision || snapshot!=after || self.product_pull(number,repository).await?!=before || self.product_tip(&base_ref).await?!=tip {return Err(invalid());}
        Ok(json!({"repository":repository,"number":number,"pull_id":pull["id"],"issue_id":pull["issue_id"],"head":head,"head_ref":head_ref,"base_ref":base_ref,"commit":commit,
            "base_tip":tip,"base_contains":ancestry["reachable"],"merger":merger,"merged_at":merged,"closed_at":closed,"issue_closed":true}))
    }

    pub(super) async fn automation_checks(&self,repository:&str,number:u32)->Result<Value> {
        let facts = self.pull_facts(repository, number, false, true, false).await?;
        let mut result = facts.review;
        let head = result["head_tip"].as_str().ok_or_else(invalid)?.to_owned();
        let observed = project_checks(&head, &facts.snapshot)?;
        result.as_object_mut().ok_or_else(invalid)?.remove("author");
        result["evaluation_commit"]=json!(head);result["checks"]=observed["checks"].clone();result["sources"]=observed["sources"].clone();
        Ok(result)
    }

    pub(super) async fn current_checks(&self,head:&str)->Result<Value> {
        let selection=json!({"families":["checks"],"sha":head,"limit":50});
        let (revision,snapshot)=self.snapshot(selection.clone()).await?;
        let result = project_checks(head, &snapshot)?;
        let (after_revision,after)=self.snapshot(selection).await?;
        if revision!=after_revision || snapshot!=after {return Err(invalid());}
        Ok(result)
    }

    pub(super) async fn merge_facts(&self,repository:&str,number:u32)->Result<Value> {
        let facts = self.pull_facts(repository, number, true, true, true).await?;
        let before = facts.review;
        let revision = facts.revision;
        let snapshot = facts.snapshot;
        let head=before["pull"]["head"].as_str().ok_or_else(invalid)?;
        let base=before["base_tip"].as_str().ok_or_else(invalid)?;
        let gate=&snapshot["merge_eligibility"];let ancestry=&snapshot["ancestry"];
        if gate["visible"]!=true || gate["complete"]!=true || gate["pull_number"]!=number.to_string() || gate["head_tip"]!=head || gate["base_tip"]!=base
            || ancestry["ancestor_oid"]!=base || ancestry["descendant_oid"]!=head || !ancestry["reachable"].is_boolean() || !gate["eligible"].is_boolean(){return Err(invalid());}
        let repo = facts.repository;
        if super::product::id(&repo["id"])?!=repository {return Err(invalid());}
        let enabled=repo["allow_fast_forward_only_merge"].as_bool().ok_or_else(invalid)?;
        let push=repo["permissions"]["push"].as_bool().ok_or_else(invalid)?;
        let archived=repo["archived"].as_bool().ok_or_else(invalid)?;
        let rows=items(&snapshot["reviews"],remote(&snapshot["reviews"]["issue_id"])?)?;
        let mut veto=false;
        for row in rows {
            if row["visible"]!=true || row["complete"]!=true {return Err(invalid());}
            veto|=row["type"]=="REQUEST_CHANGES" && row["dismissed"]!=true && row["stale"]!=true;
        }
        let checks = project_checks(head, &snapshot)?;
        let available=enabled&&ancestry["reachable"]==true;
        let eligible=available&&push&&!archived&&!veto&&gate["eligible"]==true;
        Ok(json!({"repository":repository,"name":before["name"],"repository_node_id":before["repository_node_id"],"pull_id":before["pull"]["external_id"],"pull_node_id":before["pull"]["node_id"],
            "number":number,"head":head,"base":base,"head_ref":before["head_ref"],"base_ref":before["base_ref"],"state":"open","draft":false,"merged":false,
            "mergeable":available,"mergeable_state":if eligible{"clean"}else{"blocked"},"archived":archived,"disabled":false,"push":push,"methods":{"fast-forward-only":available},
            "head_condition":"atomic_head_oid","base_condition":"atomic_base_oid","provider_complete":true,"provider_eligible":eligible,"host_revision":revision,"checks":checks["checks"]}))
    }

    pub(super) async fn review_facts(&self, repository: &str, number: u32, prepare: bool) -> Result<Value> {
        Ok(self.pull_facts(repository, number, prepare, false, false).await?.review)
    }

    async fn pull_facts(&self, repository: &str, number: u32, prepare: bool, checks: bool, merge: bool) -> Result<PullFacts> {
        let before = self.product_pull(number, repository).await?;
        let head = text(&before["head"], "name", 512)?;
        let base = text(&before["base"], "name", 512)?;
        if before["state"] != "open" || before["draft"] != false || before["locked"] != false
            || before["head"]["repository"]["external_id"] != repository || head == base { return Err(invalid()); }
        let current = self.status().await?;
        if !current.active || current.repository.id != repository { return Err(invalid()); }
        let mut families = vec!["pull", "refs", "reviews"];
        if checks { families.push("checks"); }
        if merge { families.extend(["merge_eligibility", "ancestry"]); }
        let mut selection = json!({"families":families,"pull_number":number.to_string(),
            "refs":[format!("refs/heads/{head}"),format!("refs/heads/{base}")],"limit":50});
        if checks { selection["sha"] = before["head"]["commit"].clone(); }
        if merge { selection["ancestry"] = json!({"ancestor_oid":before["base"]["commit"],"descendant_oid":before["head"]["commit"]}); }
        let (revision, snapshot) = self.snapshot_enrolled(selection.clone(), &current).await?;
        let repo = if prepare {
            let (repo, _):(Value, _) = self.request_url(reqwest::Method::GET, self.product_url(&[])?, None, 512*1024).await?;
            repo
        } else { Value::Null };
        let review = project_review(repository, number, prepare, &before, &snapshot, &current, &repo, &self.owner, &self.repository)?;
        let (after_revision, after) = self.snapshot_enrolled(selection, &current).await?;
        if revision != after_revision || snapshot != after || self.product_pull(number, repository).await? != before { return Err(invalid()); }
        if prepare {
            let (after_repo, _):(Value, _) = self.request_url(reqwest::Method::GET, self.product_url(&[])?, None, 512*1024).await?;
            if after_repo != repo { return Err(invalid()); }
        }
        Ok(PullFacts { review, revision, snapshot, repository: repo })
    }

    pub(super) async fn snapshot(&self, selection: Value) -> Result<(i64, Value)> {
        let enrollment = self.status().await?;
        self.snapshot_enrolled(selection, &enrollment).await
    }
    async fn snapshot_enrolled(&self, mut selection: Value, enrollment: &super::Enrollment) -> Result<(i64, Value)> {
        if !enrollment.active {
            return Err(invalid());
        }
        selection["repository_id"] = json!(enrollment.repository.id);
        selection["actor_id"] = json!(enrollment.actor.id);
        let body=serde_json::to_vec(&selection).map_err(|_|invalid())?;
        // Snapshot POSTs are read-only. An ordinary CI writer can briefly hold
        // their revision bracket; retry that read, never a privileged send.
        let mut attempt=0;
        let operation = crate::provider_contract::operation::Operation::current();
        let response:Value=loop {
            match self.request_bounded(reqwest::Method::POST,&[&self.command,"snapshot"],Some(body.clone()),2*1024*1024).await {
                Ok(response)=>break response,
                Err(error) if error.status==409 && attempt<4 && operation.as_ref().is_none_or(|work| work.retry_conflict())=>{
                    attempt+=1;
                    tokio::time::sleep(std::time::Duration::from_millis(attempt*50)).await;
                }
                Err(error)=>return Err(error),
            }
        };
        let revision = response["revision"]
            .as_i64()
            .filter(|n| *n > 0)
            .ok_or_else(invalid)?;
        if response["snapshot"]["repository_id"] != enrollment.repository.id {
            return Err(invalid());
        }
        self.product_current(&enrollment.repository.id).await?;
        Ok((revision, response["snapshot"].clone()))
    }

    pub(super) async fn automation_inputs(
        &self,
        number: u32,
        comments: &[String],
        target: &str,
    ) -> Result<Value> {
        let before = self.product_issue(number).await?;
        let (revision,native)=self.snapshot(json!({"families":["issue","dependencies","refs"],"issue_index":number.to_string(),"refs":[target],"limit":50,"dependency_locators":true})).await?;
        let issue = &native["issue"];
        let issue_id = remote(&issue["id"])?;
        let title = text(issue, "title", 4096)?;
        let body = content(issue, "content")?;
        let author = remote(&issue["provenance"]["poster_id"])?;
        let created = unix(&before["created_at"])?;
        let closed = unix(&before["closed_at"])?;
        if before["external_id"] != issue_id
            || issue["index"] != number.to_string()
            || title.is_empty()
            || issue["visible"] != true
            || issue["complete"] != true
            || issue["provenance"]["verified"] != true
            || issue["provenance"]["first_created"] != true
            || issue["title_digest"] != hash(title.as_bytes())
            || issue["content_digest"] != hash(body.as_bytes())
            || before["title"] != title
            || before["body"] != body
            || before["author"]["external_id"] != author
            || created <= 0
            || issue["created_unix"].as_i64() != Some(created)
            || issue["provenance"]["created_unix"].as_i64() != Some(created)
            || issue["closed_unix"].as_i64().unwrap_or(0) != closed
            || issue["is_closed"].as_bool().unwrap_or(false) != (closed > 0)
            || issue["is_locked"].as_bool().unwrap_or(false)
                != before["locked"].as_bool().ok_or_else(invalid)?
        {
            return Err(invalid());
        }
        let mut title_occurrence = "original".to_owned();
        let mut lifecycle_occurrence = "original".to_owned();
        let mut closing_occurrence = String::new();
        let mut last_title = None;
        let mut seen = BTreeSet::new();
        let events = match &issue["lifecycle"] {
            Value::Null => &[][..],
            Value::Array(events) if events.len() <= 192 => events.as_slice(),
            _ => return Err(invalid()),
        };
        for event in events {
            let id = format!("fountain-comment/{}", remote(&event["occurrence_id"])?);
            if !seen.insert(id.clone()) {
                return Err(invalid());
            }
            match text(event, "kind", 32)? {
                "retitled" => {
                    title_occurrence = id;
                    last_title = Some(text(event, "new_title", 4096)?);
                }
                "closed" => {
                    lifecycle_occurrence = id.clone();
                    closing_occurrence = id;
                }
                "reopened" => {
                    lifecycle_occurrence = id;
                    closing_occurrence.clear();
                }
                _ => return Err(invalid()),
            }
        }
        if last_title.is_some_and(|value| value != title)
            || (closed > 0) != !closing_occurrence.is_empty()
        {
            return Err(invalid());
        }
        let mut dependencies = Vec::new();
        let mut edges = BTreeSet::new();
        for edge in items(&native["dependencies"], issue_id)? {
            if edge["visible"] != true || edge["complete"] != true {
                return Err(invalid());
            }
            let id = remote(&edge["dependency_id"])?;
            if !edges.insert(id) {
                return Err(invalid());
            }
            let occurrence = format!("fountain-dependency/{}", remote(&edge["occurrence_id"])?);
            dependencies.push(json!({"occurrence":occurrence,"depends_on":id,"repository":remote(&edge["repository_id"])?,"number":remote(&edge["index"])?.parse::<u32>().map_err(|_|invalid())?}));
        }
        dependencies.sort_by_key(|value| value["depends_on"].as_str().unwrap_or("").to_owned());
        let mut selected = Vec::new();
        if !comments.is_empty() {
            let (comment_revision, comment_snapshot) = self
                .snapshot(json!({"families":["comments"],"comment_ids":comments,"limit":50}))
                .await?;
            if comment_revision != revision {
                return Err(invalid());
            }
            let rows = items(&comment_snapshot["comments"], issue_id)?;
            if rows.len() != comments.len() {
                return Err(invalid());
            }
            for id in comments {
                let comment = rows
                    .iter()
                    .find(|row| row["id"] == *id)
                    .ok_or_else(invalid)?;
                let content = content(comment, "content")?;
                if comment["visible"] != true
                    || comment["complete"] != true
                    || comment["issue_id"] != issue_id
                    || comment["type"] != "comment"
                    || comment["content_digest"] != hash(content.as_bytes())
                {
                    return Err(invalid());
                }
                selected.push(
                    json!({"id":id,"body":content,"version":version(&comment["content_version"])?}),
                );
            }
        }
        let refs = native["refs"]
            .as_array()
            .filter(|refs| refs.len() == 1)
            .ok_or_else(invalid)?;
        let reference = &refs[0];
        let tip = text(reference, "oid", 40)?;
        if reference["ref"] != target
            || reference["visible"] != true
            || reference["complete"] != true
            || reference["exists"] != true
            || !super::hex(tip, 40)
            || self.product_issue(number).await? != before
        {
            return Err(invalid());
        }
        let projected = json!({"issue":{"id":issue_id,"number":number,"title":title,"body":body,"version":version(&issue["content_version"])?,"title_occurrence":title_occurrence,"lifecycle_occurrence":lifecycle_occurrence,"closing_occurrence":closing_occurrence,"author":before["author"],"created_at":before["created_at"],"closed_at":before["closed_at"],"closed":closed>0,"locked":before["locked"]},"dependencies":dependencies,"comments":selected,"tip":tip,"tip_ref":target});
        if serde_json::to_vec(&projected).map_err(|_| invalid())?.len() > 256 * 1024 {
            return Err(invalid());
        }
        Ok(projected)
    }

    pub(super) async fn issue_creator_permission(
        &self,
        number: u32,
        creator: &str,
    ) -> Result<Value> {
        let before = self.product_issue(number).await?;
        if before["author"]["external_id"] != creator {
            return Err(invalid());
        }
        let (_,snapshot)=self.snapshot(json!({"families":["issue","repository_writers"],"issue_index":number.to_string(),"writer_ids":[creator]})).await?;
        let current = self.status().await?;
        if snapshot["issue"]["visible"] != true
            || snapshot["issue"]["complete"] != true
            || snapshot["issue"]["id"] != before["external_id"]
            || snapshot["issue"]["provenance"]["poster_id"] != creator
            || self.product_issue(number).await? != before
        {
            return Err(invalid());
        }
        let write = snapshot["writers"][creator].as_bool().ok_or_else(invalid)?;
        Ok(
            json!({"repository":current.repository.id,"issue_id":before["external_id"],"number":number,"creator":creator,"write":write}),
        )
    }
}


fn project_checks(head: &str, snapshot: &Value) -> Result<Value> {
        let set=&snapshot["checks"];
        if set["sha"]!=head || set["complete"]!=true {return Err(invalid());}
        let rows=match &set["items"] {Value::Null=>&[][..],Value::Array(items) if items.len()<=50=>items.as_slice(),_=>return Err(invalid())};
        if set["total"].as_u64()!=Some(rows.len() as u64){return Err(invalid());}
        let mut latest=std::collections::BTreeMap::<String,(i64,&Value)>::new();
        let mut identities=BTreeSet::new();
        for row in rows {
            let id=remote(&row["id"])?;
            let creator=if row["creator_id"]=="-2" {"-2"} else {remote(&row["creator_id"])?};
            let name=text(row,"context",256)?;
            let state=text(row,"state",64)?;
            let index=row["index"].as_i64().filter(|v|*v>0).ok_or_else(invalid)?;
            if row["sha"]!=head || row["visible"]!=true || row["complete"]!=true || name.trim().is_empty() || name.chars().any(char::is_control)
                || state.chars().any(char::is_control) || creator.is_empty() || !identities.insert(id) {return Err(invalid());}
            match latest.get(name) {
                Some((prior,_)) if *prior==index=>return Err(invalid()),
                Some((prior,_)) if *prior>index=>(),
                _=>{latest.insert(name.into(),(index,row));}
            }
        }
        let mut checks=Vec::new();let mut sources=Vec::new();
        for (context,(_,row)) in latest {
            let creator=row["creator_id"].as_str().ok_or_else(invalid)?;
            let bound=if creator=="-2" {format!("actions:{context}")} else {format!("actor:{creator}:{context}")};
            if bound.len()>256 {return Err(invalid());}
            checks.push(json!({"context":bound,"state":row["state"]}));
            sources.push(json!({"kind":"fountain_commit_status","id":row["id"],"index":row["index"],"creator":row["creator_id"],"commit":head,"context":context,"state":row["state"]}));
        }
        Ok(json!({"checks":checks,"sources":sources}))
}

struct PullFacts {
    review: Value,
    revision: i64,
    snapshot: Value,
    repository: Value,
}

fn project_review(repository: &str, number: u32, prepare: bool, before: &Value,
    snapshot: &Value, current: &super::Enrollment, repo: &Value, owner: &str, repository_name: &str) -> Result<Value> {
    let head=text(&before["head"],"name",512)?;
    let base=text(&before["base"],"name",512)?;
    let head_ref=format!("refs/heads/{head}");
    let base_ref=format!("refs/heads/{base}");
        let pull=&snapshot["pull"];
        if pull["visible"]!=true || pull["complete"]!=true || pull["number"]!=number.to_string()
            || pull["head_repo_id"]!=repository || pull["head_branch"]!=head || pull["base_branch"]!=base
            || pull["head_tip"]!=before["head"]["commit"] || pull["has_merged"].as_bool().unwrap_or(false)
            || !super::hex(text(pull,"merge_base",40)?,40) {
            return Err(invalid());
        }
        let refs=snapshot["refs"].as_array().ok_or_else(invalid)?;
        let tip=|name:&str|->Result<&str>{
            let matches:Vec<_>=refs.iter().filter(|v|v["ref"]==name).collect();
            if matches.len()!=1 || matches[0]["visible"]!=true || matches[0]["complete"]!=true || matches[0]["exists"]!=true {return Err(invalid());}
            text(matches[0],"oid",40)
        };
        if tip(&head_ref)?!=before["head"]["commit"].as_str().ok_or_else(invalid)?
            || tip(&base_ref)?!=before["base"]["commit"].as_str().ok_or_else(invalid)? {return Err(invalid());}
        let issue=remote(&pull["issue_id"])?;
        let reviews=items(&snapshot["reviews"],issue)?;
        let mut pending=0;
        for review in reviews {
            remote(&review["id"])?;
            if review["visible"]!=true || review["complete"]!=true || review["issue_id"]!=issue {return Err(invalid());}
            if review["type"]=="PENDING" && review["reviewer_id"]==current.actor.id {pending+=1;}
        }
        if pending!=0 {return Err(super::Failure{status:409,code:"fountain_pending_review_requires_submission"});}
        let pull_id=remote(&pull["id"])?;
        let author=&before["author"];
        remote(&author["external_id"])?;
        if !prepare {
            return Ok(json!({"repository":repository,"number":number,"pull_id":pull_id,"issue_id":issue,"author":author,
                "head_ref":head_ref,"base_ref":base_ref,"head_tip":tip(&head_ref)?,"base_tip":tip(&base_ref)?}));
        }
        if super::product::id(&repo["id"])?!=repository || repo["archived"]!=false {return Err(invalid());}
        Ok(json!({"repository":repository,"name":format!("{owner}/{repository_name}"),"repository_node_id":format!("fountain:repository:{repository}"),
            "actor_node_id":format!("fountain:actor:{}",current.actor.id),"archived":false,"disabled":false,"visible_pending_count":0,"pending":null,
            "viewer_is_author":author["external_id"]==current.actor.id,"author_id":author["external_id"],"head_ref":head_ref,"base_ref":base_ref,"base_tip":tip(&base_ref)?,
            "pull":{"external_id":pull_id,"node_id":format!("fountain:pull:{repository}:{pull_id}"),"number":number,"state":"open","locked":false,"draft":false,"merged":false,"head":tip(&head_ref)?}}))
    }
