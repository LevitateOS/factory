//! Read-only shared product observations from one explicitly enrolled repository.
//! Enrollment supplies neither write permission nor native execution authority.
use super::{invalid, Client, Result};
use crate::provider_contract::Read;
use base64::Engine;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeSet;

pub(super) fn id(value: &Value) -> Result<String> {
    value
        .as_i64()
        .filter(|id| *id > 0)
        .map(|id| id.to_string())
        .ok_or_else(invalid)
}
pub(super) fn text<'a>(value: &'a Value, key: &str, bound: usize) -> Result<&'a str> {
    value[key]
        .as_str()
        .filter(|s| s.len() <= bound && !s.contains('\0'))
        .ok_or_else(invalid)
}
pub(super) fn actor(value: &Value) -> Result<Value> {
    if value.is_null() {
        return Ok(Value::Null);
    }
    let login = text(value, "login", 512)?;
    if login.is_empty() || login.chars().any(char::is_control) {
        return Err(invalid());
    }
    Ok(json!({"external_id":id(&value["id"])?,"login":login}))
}
pub(super) fn date(value: &Value, key: &str) -> Result<Value> {
    if value[key].is_null() {
        return Ok(Value::Null);
    }
    let raw = text(value, key, 64)?;
    time::OffsetDateTime::parse(raw, &time::format_description::well_known::Rfc3339)
        .map_err(|_| invalid())?;
    Ok(json!(raw))
}
fn issue(value: &Value, number: Option<u32>) -> Result<Value> {
    let index = value["number"]
        .as_u64()
        .filter(|n| *n > 0 && *n <= u32::MAX as u64)
        .ok_or_else(invalid)?;
    if !value["pull_request"].is_null()
        || number.is_some_and(|n| u64::from(n) != index)
        || !matches!(value["state"].as_str(), Some("open" | "closed"))
    {
        return Err(invalid());
    }
    let actors = match &value["assignees"] {
        Value::Null => Vec::new(),
        Value::Array(items) if items.len() <= 100 => {
            items.iter().map(actor).collect::<Result<Vec<_>>>()?
        }
        _ => return Err(invalid()),
    };
    let labels=match &value["labels"] {
        Value::Null=>Vec::new(),Value::Array(items) if items.len()<=100=>items.iter().map(|item|Ok(json!({"external_id":id(&item["id"])?,"name":text(item,"name",512)?,"color":text(item,"color",32)?}))).collect::<Result<Vec<_>>>()?,_=>return Err(invalid()),
    };
    Ok(
        json!({"external_id":id(&value["id"])?,"number":index,"title":text(value,"title",65536)?,"body":text(value,"body",128*1024)?,
        "state":value["state"],"locked":value["is_locked"].as_bool().ok_or_else(invalid)?,"author":actor(&value["user"])?,"assignees":actors,"labels":labels,
        "created_at":date(value,"created_at")?,"updated_at":date(value,"updated_at")?,"closed_at":date(value,"closed_at")?}),
    )
}

fn branch(value: &Value) -> Result<Value> {
    if value.is_null() {
        return Ok(Value::Null);
    }
    let commit = text(value, "sha", 64)?;
    let name = text(value, "ref", 512)?;
    if !matches!(commit.len(), 40 | 64)
        || !commit.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        || name.is_empty() || name.chars().any(char::is_control)
    {
        return Err(invalid());
    }
    let repository = if value["repo"].is_null() {
        Value::Null
    } else {
        let repository = &value["repo"];
        if id(&repository["id"])? != id(&value["repo_id"])? {
            return Err(invalid());
        }
        json!({"external_id":id(&repository["id"])?,"name":text(repository,"full_name",512)?})
    };
    Ok(json!({"repository":repository,"name":name,"commit":commit}))
}
fn pull(value: &Value, number: Option<u32>, repository: &str) -> Result<Value> {
    let mut record = issue(value, number)?;
    let merged = value["merged"].as_bool().ok_or_else(invalid)?;
    let merged_at = date(value, "merged_at")?;
    if merged || !merged_at.is_null() {
        if !merged || value["state"] != "closed" {
            return Err(invalid());
        }
        record["state"] = json!("merged");
    }
    let base = branch(&value["base"])?;
    if base["repository"]["external_id"] != repository {
        return Err(invalid());
    }
    record["base"] = base;
    record["head"] = branch(&value["head"])?;
    record["draft"] = json!(value["draft"].as_bool().ok_or_else(invalid)?);
    record["merged"] = json!(merged);
    record["merged_at"] = merged_at;
    // Host mergeability has different semantics from GitHub's automatic merge
    // gate. These views grant no check, fork-execution or merge authority.
    record["mergeable"] = Value::Null;
    record["mergeable_state"] = Value::Null;
    record["rebaseable"] = Value::Null;
    Ok(record)
}

fn review(value:&Value)->Result<Value> {
    let provider=text(value,"state",64)?;
    let state=match provider {"PENDING"=>"pending","APPROVED"=>"approved","REQUEST_CHANGES"=>"changes_requested","COMMENT"=>"commented","REQUEST_REVIEW"=>"unknown",_=>return Err(invalid())};
    let state=if value["dismissed"]==true && matches!(state,"approved"|"changes_requested"){"dismissed"}else{state};
    let commit=text(value,"commit_id",64)?;
    if !commit.is_empty() && !super::hex(commit,40) {return Err(invalid());}
    Ok(json!({"external_id":id(&value["id"])?,"author":actor(&value["user"])?,"body":text(value,"body",128*1024)?,"state":state,"provider_state":provider,
        "commit":if commit.is_empty(){Value::Null}else{json!(commit)},"official":value["official"].as_bool().ok_or_else(invalid)?,"stale":value["stale"].as_bool().ok_or_else(invalid)?,"submitted_at":if provider=="PENDING" || provider=="REQUEST_REVIEW" {Value::Null}else{date(value,"submitted_at")?}}))
}

impl Client {
    pub(crate) async fn product_current(&self, repository: &str) -> Result<()> {
        let current = self.status().await?;
        if !current.active || current.repository.id != repository {
            return Err(invalid());
        }
        Ok(())
    }

    /// Read a bounded semantic product observation from this exact enrollment.
    /// These observations carry neither operation nor execution authority.
    pub async fn product_read(&self, repository: &str, query: &Read) -> Result<Value> {
        query.validate().map_err(|_| invalid())?;
        let current = self.status().await?;
        if !current.active || current.repository.id != repository {
            return Err(invalid());
        }
        if self.issues_only && !matches!(query, Read::Identity | Read::Repository {..} | Read::Issues {..} |
            Read::Issue {..} | Read::IssueComments {..} | Read::WriteCapabilities {pull:false,..}) {
            return Err(super::Failure{status:403,code:"fountain_issue_scope_only"});
        }
        let account = json!({"external_id":current.actor.id,"login":current.actor.username});
        let result = match query {
            Read::WriteCapabilities {repository:selected,number,pull:false} if self.issues_only && selected==repository => {
                json!({"kind":"write_capabilities","account":account,"capabilities":self.issue_capabilities(repository,*number).await?})
            }
            Read::Repository {repository:selected} if self.issues_only && selected==repository => {
                json!({"kind":"repository","account":account,"repository":{"external_id":repository,
                    "name":format!("{}/{}",current.repository.owner,current.repository.name),"private":null,
                    "default_branch":"","push":false,"admin":false,"merge_methods":{}}})
            }
            Read::PullRequiredChecks {repository:selected,number,head,base} if selected==repository=>json!({"kind":"pull_required_checks","account":account,"required_checks":self.visible_required_checks(repository,*number,head,base).await?}),
            Read::DeliveryOpinions {repository:selected,number} if selected==repository=>json!({"kind":"delivery_opinions","account":account,"delivery_opinions":self.delivery_opinions(repository,*number).await?}),
            Read::PullThreads {repository:selected,number,page,limit,thread_id} if selected==repository=>json!({"kind":"pull_threads","account":account,"threads":self.discussion_threads(repository,*number,*page,*limit,thread_id).await?}),
            Read::PullCheckRuns {repository:selected,number,page,limit,head} | Read::PullCommitStatuses {repository:selected,number,page,limit,head} if selected==repository=>{
                let runs=matches!(query,Read::PullCheckRuns {..});
                let value=self.visible_checks(repository,*number,*page,*limit,head,runs).await?;
                if runs {json!({"kind":"pull_check_runs","account":account,"checks":value})}else{json!({"kind":"pull_commit_statuses","account":account,"statuses":value})}
            }
            Read::AutomationChecks {repository:selected,number} if selected==repository=>json!({"kind":"automation_checks","account":account,"automation_checks":self.automation_checks(repository,*number).await?}),
            Read::MergePreparation {repository:selected,number} if selected==repository=>json!({"kind":"merge_preparation","account":account,"preparation":self.merge_facts(repository,*number).await?}),
            Read::MergeCompletion {repository:selected,number} if selected==repository=>json!({"kind":"merge_completion","account":account,"publication":self.merge_completion(repository,*number).await?}),
            Read::AutomationReview {repository:selected,number} if selected==repository => {
                json!({"kind":"automation_review","account":account,"automation_review":self.review_facts(repository,*number,false).await?})
            }
            Read::PullPendingReviewSnapshot {repository:selected,number} if selected==repository => {
                json!({"kind":"pull_pending_review_snapshot","account":account,"preparation":self.review_facts(repository,*number,true).await?})
            }
            Read::PullBranches {repository:selected,head_repository,base_branch,head_branch} if selected==repository=>{
                json!({"kind":"pull_branches","account":account,"branches":self.pull_branches(repository,head_repository,base_branch,head_branch).await?})
            }
            Read::PublicationRefs { repository:selected,name,base_branch,comparison_repository } if selected==repository && comparison_repository.is_empty() => {
                json!({"kind":"publication_refs","account":account,"publication":self.publication_refs(repository,name,base_branch).await?})
            }
            Read::BranchCreation { repository:selected,name,commit } if selected==repository => {
                let observed=self.publication_refs(repository,name,&current.repository.default_branch).await?;
                let tip=observed["tip"].as_str().ok_or_else(invalid)?;
                json!({"kind":"branch_creation","account":account,"preparation":{"repository":repository,"name":observed["name"],
                    "repository_node_id":observed["repository_node_id"],"archived":observed["archived"],"disabled":observed["disabled"],"push":observed["push"],
                    "requested_branch":name,"requested_commit":commit,"branch":if tip.is_empty(){Value::Null}else{json!({"name":name,"node_id":format!("fountain:branch:{repository}:{name}"),"commit":tip})},"condition":"bounded_observation"}})
            }
            Read::Identity => json!({"kind":"identity","account":account}),
            Read::AutomationInputs {repository:selected,number,comments,target_ref} if selected == repository => {
                json!({"kind":"automation_inputs","account":account,"inputs":self.automation_inputs(*number,comments,target_ref).await?})
            }
            Read::IssueCreatorPermission {repository:selected,number,creator} if selected == repository => {
                json!({"kind":"issue_creator_permission","account":account,"capabilities":self.issue_creator_permission(*number,creator).await?})
            }
            Read::Repository {
                repository: selected,
            } if selected == repository => {
                let (raw, _): (Value, _) = self.request_url(reqwest::Method::GET,
                    self.product_url(&[])?, None, 512 * 1024).await?;
                if id(&raw["id"])? != repository || raw["full_name"] != format!("{}/{}",current.repository.owner,current.repository.name)
                    || raw["default_branch"] != current.repository.default_branch { return Err(invalid()); }
                json!({"kind":"repository","account":account,"repository":{
                    "external_id":current.repository.id,
                    "name":format!("{}/{}",current.repository.owner,current.repository.name),
                    "private":null,"default_branch":current.repository.default_branch,
                    "push":raw["permissions"]["push"].as_bool(),"admin":raw["permissions"]["admin"].as_bool(),
                    "merge_methods":{"fast-forward-only":raw["allow_fast_forward_only_merge"].as_bool()}
                }})
            }
            Read::OperationGrant { repository: selected, grant_id } if selected == repository => {
                let grant = self.operation_grant(grant_id).await?;
                let placement = self.placement().await?;
                if grant.repository.id != repository || grant.actor.id != current.actor.id || !current.runtime_bound
                    || !grant.active || grant.not_after <= time::OffsetDateTime::now_utc().unix_timestamp()
                    || placement.state != "active" || placement.runtime_id != self.runtime || placement.owner_kind != "desktop"
                    || placement.owner_id != current.id || placement.generation != grant.generation {
                    return Err(invalid());
                }
                json!({"kind":"operation_grant","account":account,"capabilities":{
                    "repository":repository,"actor":grant.actor.id,"grant_id":grant.id,"runtime":grant.runtime_id,
                    "generation":grant.generation,"not_after":grant.not_after,"operations":grant.operations
                }})
            }
            Read::TargetTip { repository: selected, target_ref } if selected == repository => {
                let tip = self.product_tip(target_ref).await?;
                self.product_current(repository).await?;
                if self.product_tip(target_ref).await? != tip { return Err(invalid()); }
                json!({"kind":"target_tip","account":account,"publication":{"repository":repository,"target_ref":target_ref,"tip":tip}})
            }
            Read::SetupSource { repository: selected, commit, target_ref, paths } if selected == repository => {
                if self.product_tip(target_ref).await? != *commit { return Err(invalid()); }
                let setup = crate::provider_contract::SETUP_FILES;
                let mut names: BTreeSet<&str> = setup.into_iter().collect();
                names.extend(paths.iter().map(String::as_str));
                let mut files = serde_json::Map::new();
                let mut source_files = serde_json::Map::new();
                let mut total = 0usize;
                for name in names {
                    if let Some(bytes) = self.product_file(commit, name).await? {
                        let encoded = json!(base64::engine::general_purpose::STANDARD.encode(&bytes));
                        if paths.iter().any(|path| path == name) {
                            total += bytes.len();
                            if total > 128 * 1024 { return Err(invalid()); }
                            source_files.insert(name.into(), encoded.clone());
                        }
                        if setup.contains(&name) { files.insert(name.into(), encoded); }
                    }
                }
                if self.product_tip(target_ref).await? != *commit { return Err(invalid()); }
                json!({"kind":"setup_source","account":account,"publication":{"repository":repository,"commit":commit,"target_ref":target_ref,"files":files,"source_files":source_files}})
            }
            Read::SourceBundle { repository: selected, base, expected_base, commit, target_ref, number, head_repository } if selected == repository && expected_base.is_empty() && number.is_none() && head_repository.is_empty() => {
                if self.product_tip(target_ref).await? != *commit { return Err(invalid()); }
                // The host source owner validates objects and reachability. A
                // complete bundle is also valid when the importer knows a base.
                let source = self.source(commit, &[], &[]).await?;
                if source.repository != repository || source.commit != *commit
                    || self.product_tip(target_ref).await? != *commit { return Err(invalid()); }
                let sha256: String = Sha256::digest(&source.bundle).iter().map(|b| format!("{b:02x}")).collect();
                json!({"kind":"source_bundle","account":account,"publication":{"repository":repository,"base":base,"commit":commit,"target":target_ref,"sha256":sha256,"bundle":base64::engine::general_purpose::STANDARD.encode(&source.bundle)}})
            }
            Read::Issues {
                repository: selected,
                page,
                limit,
                state,
            } if selected == repository => {
                let (raw, next) = self
                    .product_page(
                        &["issues"],
                        *page,
                        *limit,
                        &[("type", "issues"), ("state", state)],
                    )
                    .await?;
                let mut seen = BTreeSet::new();
                let mut items = Vec::new();
                for value in raw {
                    let value = issue(&value, None)?;
                    if !seen.insert(
                        value["external_id"]
                            .clone()
                            .as_str()
                            .ok_or_else(invalid)?
                            .to_owned(),
                    ) {
                        return Err(invalid());
                    }
                    items.push(value);
                }
                json!({"kind":"issues","account":account,"issues":{"repository":repository,"name":format!("{}/{}",current.repository.owner,current.repository.name),"items":items,"next_page":next}})
            }
            Read::Issue {
                repository: selected,
                number,
            } if selected == repository => {
                let value = self.product_issue(*number).await?;
                json!({"kind":"issue","account":account,"issues":{"repository":repository,"name":format!("{}/{}",current.repository.owner,current.repository.name),"items":[value],"next_page":0}})
            }
            Read::Pulls { repository: selected, page, limit, state } if selected == repository => {
                let (raw, next) = self.product_page(&["pulls"], *page, *limit, &[("state", state)]).await?;
                let mut seen = BTreeSet::new();
                let mut numbers = BTreeSet::new();
                let mut items = Vec::new();
                for value in raw {
                    if state != "all" && value["state"] != *state {
                        return Err(invalid());
                    }
                    let value = pull(&value, None, repository)?;
                    if !seen.insert(value["external_id"].as_str().ok_or_else(invalid)?.to_owned())
                        || !numbers.insert(value["number"].as_u64().ok_or_else(invalid)?) {
                        return Err(invalid());
                    }
                    items.push(value);
                }
                json!({"kind":"pulls","account":account,"pulls":{"repository":repository,"name":format!("{}/{}",current.repository.owner,current.repository.name),"items":items,"next_page":next}})
            }
            Read::Pull { repository: selected, number } if selected == repository => {
                let value = self.product_pull(*number, repository).await?;
                json!({"kind":"pull","account":account,"pulls":{"repository":repository,"name":format!("{}/{}",current.repository.owner,current.repository.name),"items":[value],"next_page":0}})
            }
            Read::PullReviews {repository:selected,number,page,limit} if selected==repository=>{
                let before=self.product_pull(*number,repository).await?;
                let (raw,next)=self.product_page(&["pulls",&number.to_string(),"reviews"],*page,*limit,&[]).await?;
                let mut seen=BTreeSet::new();
                let mut items=Vec::new();
                for raw in raw {
                    let item=review(&raw)?;
                    if !seen.insert(item["external_id"].as_str().ok_or_else(invalid)?.to_owned()){return Err(invalid());}
                    items.push(item);
                }
                if self.product_pull(*number,repository).await?!=before {return Err(invalid());}
                json!({"kind":"pull_reviews","account":account,"reviews":{"repository":repository,"name":format!("{}/{}",self.owner,self.repository),"pull":{"kind":"pull","external_id":before["external_id"],"number":number},"items":items,"next_page":next}})
            }
            Read::PullReview {repository:selected,number,review_id} if selected==repository=>{
                let before=self.product_pull(*number,repository).await?;
                let (raw,_):(Value,_)=self.request_url(reqwest::Method::GET,self.product_url(&["pulls",&number.to_string(),"reviews",review_id])?,None,512*1024).await?;
                let item=review(&raw)?;
                if item["external_id"]!=*review_id || self.product_pull(*number,repository).await?!=before {return Err(invalid());}
                json!({"kind":"pull_review","account":account,"reviews":{"repository":repository,"name":format!("{}/{}",self.owner,self.repository),"pull":{"kind":"pull","external_id":before["external_id"],"number":number},"items":[item],"next_page":0}})
            }
            Read::IssueComments {
                repository: selected,
                number,
                page,
                limit,
            } | Read::PullComments { repository: selected, number, page, limit } if selected == repository => {
                let is_pull = matches!(query, Read::PullComments { .. });
                let before = if is_pull { self.product_pull(*number, repository).await? } else { self.product_issue(*number).await? };
                let (raw, next) = self
                    .product_page(
                        &["issues", &number.to_string(), "comments"],
                        *page,
                        *limit,
                        &[],
                    )
                    .await?;
                let mut seen = BTreeSet::new();
                let mut items = Vec::new();
                for value in raw {
                    let id = id(&value["id"])?;
                    if !seen.insert(id.clone()) {
                        return Err(invalid());
                    }
                    items.push(json!({"external_id":id,"author":actor(&value["user"])?,"body":text(&value,"body",128*1024)?,"created_at":date(&value,"created_at")?,"updated_at":date(&value,"updated_at")?,"minimized_reason":null}));
                }
                let after = if is_pull { self.product_pull(*number, repository).await? } else { self.product_issue(*number).await? };
                if after != before {
                    return Err(invalid());
                }
                json!({"kind":if is_pull {"pull_comments"} else {"issue_comments"},"account":account,"discussion":{"repository":repository,"name":format!("{}/{}",current.repository.owner,current.repository.name),"subject":{"kind":if is_pull {"pull"} else {"issue"},"external_id":before["external_id"],"number":number},"items":items,"next_page":next}})
            }
            _ => {
                return Err(super::Failure {
                    status: 503,
                    code: "fountain_product_read_unavailable",
                })
            }
        };
        self.product_current(repository).await?;
        Ok(result)
    }

    pub(super) fn product_url(&self, parts: &[&str]) -> Result<url::Url> {
        let mut url = self.base.clone();
        url.path_segments_mut()
            .map_err(|_| invalid())?
            .clear()
            .extend(["api", "v1", "repos", &self.owner, &self.repository])
            .extend(parts);
        Ok(url)
    }
    pub(super) async fn product_tip(&self, target: &str) -> Result<String> {
        let branch = target.strip_prefix("refs/heads/").ok_or_else(invalid)?;
        let (raw, _): (Value, _) = self.request_url(reqwest::Method::GET,
            self.product_url(&["branches", branch])?, None, 128 * 1024).await?;
        let commit = text(&raw["commit"], "id", 40)?;
        if raw["name"] != branch || !super::hex(commit, 40) { return Err(invalid()); }
        Ok(commit.into())
    }
    async fn product_file(&self, commit: &str, name: &str) -> Result<Option<Vec<u8>>> {
        let mut parts = vec!["contents"];
        parts.extend(name.split('/'));
        let mut url = self.product_url(&parts)?;
        url.query_pairs_mut().append_pair("ref", commit);
        let (raw, _): (Value, _) = match self.request_url(reqwest::Method::GET, url, None, 128 * 1024).await {
            Ok(value) => value,
            Err(error) if error.status == 404 => return Ok(None),
            Err(error) => return Err(error),
        };
        if raw["type"] != "file" || raw["path"] != name || raw["encoding"] != "base64" { return Err(invalid()); }
        let encoded: String = text(&raw, "content", 64 * 1024)?.chars().filter(|c| !c.is_ascii_whitespace()).collect();
        let bytes = base64::engine::general_purpose::STANDARD.decode(encoded).map_err(|_| invalid())?;
        if bytes.len() > 32768 || raw["size"].as_u64() != Some(bytes.len() as u64) || std::str::from_utf8(&bytes).is_err() {
            return Err(invalid());
        }
        Ok(Some(bytes))
    }
    pub(super) async fn product_issue(&self, number: u32) -> Result<Value> {
        let (value, _): (Value, _) = self
            .request_url(
                reqwest::Method::GET,
                self.product_url(&["issues", &number.to_string()])?,
                None,
                512 * 1024,
            )
            .await?;
        issue(&value, Some(number))
    }
    pub(super) async fn product_pull(&self, number: u32, repository: &str) -> Result<Value> {
        let (value, _): (Value, _) = self.request_url(
            reqwest::Method::GET, self.product_url(&["pulls", &number.to_string()])?, None, 512 * 1024,
        ).await?;
        pull(&value, Some(number), repository)
    }
    pub(super) async fn product_page(
        &self,
        parts: &[&str],
        page: u32,
        limit: u32,
        extra: &[(&str, &str)],
    ) -> Result<(Vec<Value>, u32)> {
        let mut url = self.product_url(parts)?;
        url.query_pairs_mut()
            .extend_pairs(extra.iter().copied())
            .append_pair("page", &page.to_string())
            .append_pair("limit", &limit.to_string());
        let (values, headers): (Vec<Value>, _) = self
            .request_url(reqwest::Method::GET, url.clone(), None, 2 * 1024 * 1024)
            .await?;
        if values.len() > limit as usize {
            return Err(invalid());
        }
        let mut next = 0;
        if let Some(link) = headers.get(reqwest::header::LINK) {
            let link = link.to_str().map_err(|_| invalid())?;
            if link.len() > 8192 {
                return Err(invalid());
            }
            for entry in link.split(',') {
                let Some((address, relation)) = entry.trim().split_once(';') else {
                    return Err(invalid());
                };
                if relation.trim() != "rel=\"next\"" {
                    continue;
                }
                let target = url
                    .join(
                        address
                            .trim()
                            .strip_prefix('<')
                            .and_then(|s| s.strip_suffix('>'))
                            .ok_or_else(invalid)?,
                    )
                    .map_err(|_| invalid())?;
                if next != 0
                    || target.origin() != url.origin()
                    || target.path() != url.path()
                    || target.username() != ""
                    || target.password().is_some()
                    || target.fragment().is_some()
                {
                    return Err(invalid());
                }
                let params: std::collections::BTreeMap<_, _> = target.query_pairs().collect();
                if params.get("page").map(|s| s.as_ref()) != Some((page + 1).to_string().as_str())
                    || params.get("limit").map(|s| s.as_ref()) != Some(limit.to_string().as_str())
                {
                    return Err(invalid());
                }
                next = page + 1;
            }
        }
        Ok((values, next))
    }
}
