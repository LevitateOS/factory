//! One bounded provider effect. Go must commit its exact intent and win the
//! durable one-shot reservation before invoking this operation. Nothing here
//! provides retry authorization, a remote idempotency key or conditional writes.
use super::{reads::Reader, records as r, request::remote_id, Client, Failure};
use crate::credentials::Token;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::time::{Duration, Instant};

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Comment {
    pub repository: String,
    pub subject: String,
    pub subject_id: String,
    pub number: u64,
    pub body: String,
}
impl Comment {
    pub fn validate(&self) -> Result<(), Failure> {
        if !remote_id(&self.repository)
            || !remote_id(&self.subject_id)
            || !matches!(self.subject.as_str(), "issue" | "pull")
            || self.number == 0
            || self.number > i64::MAX as u64
            || self.body.len() > 64 * 1024
            || self.body.trim().is_empty()
            || self.body.contains('\0')
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        Ok(())
    }
}

/// Errors after dispatch starts remain uncertain, including malformed success
/// bodies and retirement. A provider refusal is not promoted to a cancellation
/// receipt. The owning Go journal must preserve the distinction durably.
pub enum CommentOutcome {
    NotSent(Failure),
    Indeterminate(Failure),
    Created(Value),
}
impl Client {
    /// Native integration surface, matching semantic reads: no URL, method,
    /// canonical mapping, browser capability or SQL authority enters this port.
    pub async fn create_comment(
        &self,
        token: Token,
        expected: &str,
        comment: &Comment,
    ) -> CommentOutcome {
        self.comment_account(token.as_bytes(), expected, comment)
            .await
    }
    pub(crate) async fn comment_account(
        &self,
        token: &[u8],
        expected: &str,
        comment: &Comment,
    ) -> CommentOutcome {
        let mut attempted = false;
        let work = async {
            comment.validate()?;
            if !remote_id(expected) {
                return Err(Failure::new(400, "invalid_native_write"));
            }
            self.current()?;
            // Serialize mutation operations across clones. Reads keep their
            // separate concurrency bound. Waits remain inside the total deadline.
            let mut next = self.mutations.lock().await;
            self.delay(next.saturating_duration_since(Instant::now()))
                .await?;
            let identity = self.identity(token).await?;
            if identity.external_id != expected {
                return Err(Failure::new(409, "github_account_changed"));
            }
            let reader = Reader::new(self, token, &comment.repository).await?;
            let pull = comment.subject == "pull";
            let subject = reader.raw_subject(pull, comment.number).await?;
            if r::id(&subject["id"])? != comment.subject_id
                || !pull && !subject["pull_request"].is_null()
            {
                return Err(Failure::new(409, "github_subject_changed"));
            }
            // A lock can still change at the provider before POST. This is a
            // conservative preflight, never an atomic provider revision guard.
            if subject["locked"].as_bool() != Some(false) {
                return Err(Failure::new(403, "github_discussion_locked"));
            }
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != comment.repository {
                return Err(Failure::new(409, "github_repository_changed"));
            }
            if repository["archived"].as_bool() != Some(false)
                || repository["disabled"].as_bool() != Some(false)
            {
                return Err(Failure::new(403, "github_repository_read_only"));
            }
            self.current()?;
            *next = Instant::now() + Duration::from_secs(1);
            attempted = true;
            let raw = self
                .post_comment(&reader.name, comment.number, &comment.body, token)
                .await?;
            let id = r::id(&raw["id"])?;
            if raw["url"] != reader.url(&format!("/issues/comments/{id}"))
                || raw["issue_url"] != reader.url(&format!("/issues/{}", comment.number))
                || raw["body"].as_str() != Some(comment.body.as_str())
                || r::id(&raw["user"]["id"])? != expected
            {
                return Err(Failure::protocol());
            }
            let projected = r::comment(&raw)?;
            self.current()?;
            Ok(
                json!({"account":identity,"repository":comment.repository,"subject":comment.subject,
                "subject_id":comment.subject_id,"number":comment.number,"comment":projected}),
            )
        };
        let result = self
            .scope(async {
                tokio::time::timeout(Duration::from_secs(8), work)
                    .await
                    .unwrap_or_else(|_| Err(Failure::new(504, "github_deadline")))
            })
            .await
            .and_then(|result| result);
        match result {
            Ok(value) => CommentOutcome::Created(value),
            Err(failure) if attempted => CommentOutcome::Indeterminate(failure),
            Err(failure) => CommentOutcome::NotSent(failure),
        }
    }
}
