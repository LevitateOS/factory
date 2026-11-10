//! Read-only shared product observations from one explicitly enrolled repository.
//! Enrollment supplies neither write permission nor native execution authority.
use super::{invalid, Client, Result};
use crate::github_provider::Read;
use serde_json::{json, Value};
use std::collections::BTreeSet;

fn id(value: &Value) -> Result<String> {
    value
        .as_i64()
        .filter(|id| *id > 0)
        .map(|id| id.to_string())
        .ok_or_else(invalid)
}
fn text<'a>(value: &'a Value, key: &str, bound: usize) -> Result<&'a str> {
    value[key]
        .as_str()
        .filter(|s| s.len() <= bound && !s.contains('\0'))
        .ok_or_else(invalid)
}
fn actor(value: &Value) -> Result<Value> {
    if value.is_null() {
        return Ok(Value::Null);
    }
    let login = text(value, "login", 512)?;
    if login.is_empty() || login.chars().any(char::is_control) {
        return Err(invalid());
    }
    Ok(json!({"external_id":id(&value["id"])?,"login":login}))
}
fn date(value: &Value, key: &str) -> Result<Value> {
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

impl Client {
    pub(crate) async fn product_current(&self, repository: &str) -> Result<()> {
        let current = self.status().await?;
        if !current.active || current.repository.id != repository {
            return Err(invalid());
        }
        Ok(())
    }

    pub(crate) async fn product_read(&self, repository: &str, query: &Read) -> Result<Value> {
        query.validate().map_err(|_| invalid())?;
        let current = self.status().await?;
        if !current.active || current.repository.id != repository {
            return Err(invalid());
        }
        let account = json!({"external_id":current.actor.id,"login":current.actor.username});
        let result = match query {
            Read::Identity => json!({"kind":"identity","account":account}),
            Read::Repository {
                repository: selected,
            } if selected == repository => {
                json!({"kind":"repository","account":account,"repository":{
                    "external_id":current.repository.id,
                    "name":format!("{}/{}",current.repository.owner,current.repository.name),
                    "private":null,"default_branch":current.repository.default_branch,
                    "push":null,"admin":null
                }})
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
            Read::IssueComments {
                repository: selected,
                number,
                page,
                limit,
            } if selected == repository => {
                let before = self.product_issue(*number).await?;
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
                if self.product_issue(*number).await? != before {
                    return Err(invalid());
                }
                json!({"kind":"issue_comments","account":account,"discussion":{"repository":repository,"name":format!("{}/{}",current.repository.owner,current.repository.name),"subject":{"kind":"issue","external_id":before["external_id"],"number":number},"items":items,"next_page":next}})
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

    fn product_url(&self, parts: &[&str]) -> Result<url::Url> {
        let mut url = self.base.clone();
        url.path_segments_mut()
            .map_err(|_| invalid())?
            .clear()
            .extend(["api", "v1", "repos", &self.owner, &self.repository])
            .extend(parts);
        Ok(url)
    }
    async fn product_issue(&self, number: u32) -> Result<Value> {
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
    async fn product_page(
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
