//! New project creation: a repository owned by the signed-in GitHub account.
//! No Factory repository exists before this effect, so no Go journal entry can
//! reserve it. The owner's repository name is the idempotency key: an unknown
//! outcome is settled by reading that name, and only a repository created
//! during this attempt is adopted.
use super::{oauth::now, records as r, request::remote_id, Client, CreateOutcome, Failure, Result};
use serde::Deserialize;
use serde_json::{json, Value};
use std::time::Duration;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RepositoryCreate {
    pub name: String,
    pub description: String,
    pub private: bool,
}

impl RepositoryCreate {
    pub fn validate(&self) -> Result<()> {
        let name = self.name.as_str();
        if !(1..=100).contains(&name.len())
            || matches!(name, "." | "..")
            || name.to_ascii_lowercase().ends_with(".git")
            || !name.bytes().all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'-' | b'_'))
            || self.description.chars().count() > 350
            || self.description.chars().any(char::is_control)
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

/// The created repository in the discovery projection, checked against the
/// request and the acting account.
fn created(raw: &Value, owner: &str, wanted: &RepositoryCreate) -> Result<Value> {
    if r::id(&raw["owner"]["id"])? != owner
        || !r::text(raw, "name")?.eq_ignore_ascii_case(&wanted.name)
        || raw["private"].as_bool() != Some(wanted.private)
    {
        return Err(Failure::protocol());
    }
    r::repository(raw)
}

impl Client {
    pub(crate) async fn create_repository(
        &self,
        token: &[u8],
        expected: &str,
        wanted: &RepositoryCreate,
    ) -> CreateOutcome {
        let started = match now() {
            Ok(value) => value,
            Err(failure) => return CreateOutcome::NotSent(failure),
        };
        let outcome = self
            .run_mutation_within(None, Duration::from_secs(25), |mutation| async move {
                wanted.validate()?;
                if !remote_id(expected) {
                    return Err(Failure::new(400, "invalid_native_write"));
                }
                let (mut permit, _identity) = mutation.admit(self, token, expected).await?;
                let proof = permit.dispatch("github_repository_creation_expired")?;
                let body = json!({"name":wanted.name,"description":wanted.description,"private":wanted.private,
                    "auto_init":true,"has_issues":true});
                let token = zeroize::Zeroizing::new(token.to_vec());
                let response = self
                    .provider_request(
                        move |http| Ok((super::transport::rest_post(http, &token, "/user/repos", body)?, 1024 * 1024)),
                        Some(&proof),
                    )
                    .await?;
                created(&response.value, expected, wanted)
            })
            .await
            .creation();
        match outcome {
            // GitHub refused the name (taken or invalid): nothing to adopt.
            CreateOutcome::Indeterminate(failure) if failure.code != "github_write_rejected" => {
                match self.settle_repository(token, expected, wanted, started).await {
                    Ok(Some(repository)) => CreateOutcome::Created(repository),
                    _ => CreateOutcome::Indeterminate(failure),
                }
            }
            other => other,
        }
    }
    /// Reads the requested name until GitHub shows it (creating a repository with
    /// its first commit can outlast the write deadline) and adopts it only when
    /// this account owns it and GitHub dates it within this attempt.
    async fn settle_repository(
        &self,
        token: &[u8],
        expected: &str,
        wanted: &RepositoryCreate,
        started: u64,
    ) -> Result<Option<Value>> {
        let read = async {
            let identity = self.identity(token).await?;
            if identity.external_id != expected {
                return Err(Failure::new(409, "github_account_changed"));
            }
            let path = format!("/repos/{}/{}", identity.login, wanted.name);
            let mut reads = 0;
            let raw = loop {
                match self.get(&path, &[], token).await {
                    Ok(response) => break response.value,
                    Err(failure) if failure.status == 404 && reads < 8 => {
                        reads += 1;
                        self.delay(Duration::from_secs(2)).await?;
                    }
                    Err(failure) if failure.status == 404 => return Ok(None),
                    Err(failure) => return Err(failure),
                }
            };
            let created_at = raw["created_at"]
                .as_str()
                .and_then(|value| time::OffsetDateTime::parse(value, &time::format_description::well_known::Rfc3339).ok())
                .ok_or_else(Failure::protocol)?
                .unix_timestamp();
            if created_at + 60 < started as i64 {
                return Ok(None);
            }
            created(&raw, expected, wanted).map(Some)
        };
        self.scope(async {
            tokio::time::timeout(Duration::from_secs(20), read)
                .await
                .unwrap_or_else(|_| Err(Failure::new(504, "github_deadline")))
        })
        .await?
    }
}
