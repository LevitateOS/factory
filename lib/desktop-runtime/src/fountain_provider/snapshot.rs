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
    pub(super) async fn review_facts(&self, repository: &str, number: u32, prepare: bool) -> Result<Value> {
        let before=self.product_pull(number,repository).await?;
        let head=text(&before["head"],"name",512)?;
        let base=text(&before["base"],"name",512)?;
        if before["state"]!="open" || before["draft"]!=false || before["locked"]!=false
            || before["head"]["repository"]["external_id"]!=repository || head==base {
            return Err(invalid());
        }
        let head_ref=format!("refs/heads/{head}");
        let base_ref=format!("refs/heads/{base}");
        let selection=json!({"families":["pull","refs","reviews"],"pull_number":number.to_string(),"refs":[head_ref,base_ref],"limit":50});
        let (revision,snapshot)=self.snapshot(selection.clone()).await?;
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
        let current=self.status().await?;
        let mut pending=0;
        for review in reviews {
            remote(&review["id"])?;
            if review["visible"]!=true || review["complete"]!=true || review["issue_id"]!=issue {return Err(invalid());}
            if review["type"]=="PENDING" && review["reviewer_id"]==current.actor.id {pending+=1;}
        }
        if pending!=0 {return Err(super::Failure{status:409,code:"fountain_pending_review_requires_submission"});}
        let (after_revision,after)=self.snapshot(selection).await?;
        if revision!=after_revision || snapshot!=after || self.product_pull(number,repository).await?!=before {return Err(invalid());}
        let pull_id=remote(&pull["id"])?;
        let author=&before["author"];
        remote(&author["external_id"])?;
        if !prepare {
            return Ok(json!({"repository":repository,"number":number,"pull_id":pull_id,"issue_id":issue,"author":author,
                "head_ref":head_ref,"base_ref":base_ref,"head_tip":tip(&head_ref)?,"base_tip":tip(&base_ref)?}));
        }
        let (repo,_):(Value,_)=self.request_url(reqwest::Method::GET,self.product_url(&[])?,None,512*1024).await?;
        if super::product::id(&repo["id"])?!=repository || repo["archived"]!=false {return Err(invalid());}
        Ok(json!({"repository":repository,"name":format!("{}/{}",self.owner,self.repository),"repository_node_id":format!("fountain:repository:{repository}"),
            "actor_node_id":format!("fountain:actor:{}",current.actor.id),"archived":false,"disabled":false,"visible_pending_count":0,"pending":null,
            "viewer_is_author":author["external_id"]==current.actor.id,"author_id":author["external_id"],"head_ref":head_ref,"base_ref":base_ref,"base_tip":tip(&base_ref)?,
            "pull":{"external_id":pull_id,"node_id":format!("fountain:pull:{repository}:{pull_id}"),"number":number,"state":"open","locked":false,"draft":false,"merged":false,"head":tip(&head_ref)?}}))
    }

    pub(super) async fn snapshot(&self, mut selection: Value) -> Result<(i64, Value)> {
        let enrollment = self.status().await?;
        if !enrollment.active {
            return Err(invalid());
        }
        selection["repository_id"] = json!(enrollment.repository.id);
        selection["actor_id"] = json!(enrollment.actor.id);
        let response: Value = self
            .request_bounded(
                reqwest::Method::POST,
                &[&self.command, "snapshot"],
                Some(serde_json::to_vec(&selection).map_err(|_| invalid())?),
                2 * 1024 * 1024,
            )
            .await?;
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
