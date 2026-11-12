//! Transfer the retained candidate pack and conditionally update one Git ref.
//! Git's receive-pack checks the advertised old OID. The comparison branch is a
//! bounded observation, not an atomic condition on that second ref.
use super::transport::{advertised, packet, GitService};
use super::{
    reads::Reader,
    records as r,
    Client, Failure, Result, WriteOutcome,
};
use crate::credentials::Token;
use serde_json::json;
use std::time::Instant;

pub use crate::provider_contract::BranchPublish;

impl Reader<'_> {
    pub(super) async fn publication_refs(
        &self,
        q: &serde_json::Value,
    ) -> Result<serde_json::Value> {
        let branch = r::text(q, "name")?;
        let base = r::text(q, "base_branch")?;
        let comparison_repository = q["comparison_repository"].as_str().unwrap_or("");
        let comparison = if comparison_repository.is_empty() {
            None
        } else {
            Some(Reader::new(self.client, self.token, comparison_repository).await?)
        };
        let observe = async || -> Result<serde_json::Value> {
            let raw = self.get("", &[]).await?.value;
            if r::id(&raw["id"])? != self.repository || raw["full_name"] != self.name {
                return Err(Failure::protocol());
            }
            let (bytes, _) = self
                .client
                .git_request(&self.name, self.token, GitService::ReceivePack, None)
                .await?;
            let refs = advertised(&bytes, GitService::ReceivePack)?;
            let mut result = json!({"repository":self.repository,"name":self.name,"repository_node_id":r::text(&raw,"node_id")?,"push":r::boolean(&raw["permissions"],"push")?,"archived":r::boolean(&raw,"archived")?,"disabled":r::boolean(&raw,"disabled")?,"branch":branch,"tip":refs.get(&format!("refs/heads/{branch}")).map(String::as_str).unwrap_or(""),"comparison_branch":base,"comparison":refs.get(&format!("refs/heads/{base}")).map(String::as_str).unwrap_or("")});
            if let Some(comparison) = &comparison {
                result["comparison"] = json!(comparison.automation_branch_tip(base).await?);
                result["comparison_repository"] = json!(comparison_repository);
            }
            Ok(result)
        };
        let before = observe().await?;
        self.recheck().await?;
        if let Some(comparison) = &comparison {
            comparison.recheck().await?;
        }
        if observe().await? != before {
            return Err(Failure::new(409, "github_publication_refs_changed"));
        }
        Ok(before)
    }

    async fn correction_pull(
        &self,
        q: &BranchPublish,
        head_name: &str,
    ) -> Result<serde_json::Value> {
        let c = q.pull_request.as_ref().ok_or_else(Failure::invalid)?;
        let (pull, issue, base_tip) = tokio::try_join!(
            self.raw_subject(true, c.number.into()),
            self.raw_subject(false, c.number.into()),
            self.automation_branch_tip(&q.comparison_branch)
        )?;
        if pull["state"] != "open"
            || pull["draft"] != false
            || pull["merged"] != false
            || r::id(&pull["id"])? != c.expected_pr_id.to_string()
            || r::id(&issue["id"])? != c.expected_issue_id.to_string()
            || r::id(&pull["user"]["id"])? != c.expected_author_id.to_string()
            || issue["pull_request"]["url"] != self.url(&format!("/pulls/{}", c.number))
            || r::id(&pull["base"]["repo"]["id"])? != c.repository
            || pull["base"]["ref"] != q.comparison_branch
            || pull["base"]["sha"] != q.comparison_oid
            || base_tip != q.comparison_oid
            || r::id(&pull["head"]["repo"]["id"])? != q.repository
            || pull["head"]["repo"]["full_name"] != head_name
            || pull["head"]["ref"] != q.branch
            || pull["head"]["sha"] != q.expected_old
        {
            return Err(Failure::new(409, "github_publication_pull_changed"));
        }
        Ok(
            json!({"pull":c,"head_repository":q.repository,"head":q.expected_old,"head_ref":q.branch,
            "base_repository":c.repository,"base":q.comparison_oid,"base_ref":q.comparison_branch}),
        )
    }
}

impl Client {
    pub async fn publish_branch(
        &self,
        token: Token,
        expected: &str,
        q: &BranchPublish,
    ) -> WriteOutcome {
        self.branch_publish_account(token.as_bytes(), expected, q, None)
            .await
    }
    pub(crate) async fn branch_publish_account(
        &self,
        token: &[u8],
        expected: &str,
        q: &BranchPublish,
        deadline: Option<Instant>,
    ) -> WriteOutcome {
        self.run_mutation(deadline, |mutation| async move {
            if !q.controller_grant_id.is_empty() || !q.source_connection.is_empty() { return Err(Failure::invalid()); }
            q.validate()?;
            let pack = q.pack()?;
            let (mut permit, actor) = mutation.admit(self, token, expected).await?;
            let reader = Reader::new(self, token, &q.repository).await?;
            // A fork correction observes its base in the captured PR repository;
            // the fork's default branch never stands in for that comparison.
            let comparison_reader = if q.pull_request.is_some() {
                Some(Reader::new(self, token, &q.comparison_repository).await?)
            } else { None };
            let pull = if let Some(comparison) = &comparison_reader {
                Some(comparison.correction_pull(q, &reader.name).await?)
            } else { None };
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != q.repository || repository["full_name"] != reader.name
                || repository["permissions"]["push"] != true || repository["archived"] != false || repository["disabled"] != false {
                return Err(Failure::new(403, "github_publication_unavailable"));
            }
            let (advertisement, _) = self.git_request(&reader.name, token, GitService::ReceivePack, None).await?;
            let refs = advertised(&advertisement, GitService::ReceivePack)?;
            let target = format!("refs/heads/{}", q.branch);
            let comparison = format!("refs/heads/{}", q.comparison_branch);
            if comparison_reader.is_none() && refs.get(&comparison).map(String::as_str) != Some(q.comparison_oid.as_str())
                || refs.get(&target).map(String::as_str).unwrap_or("") != q.expected_old {
                return Err(Failure::new(409, "github_publication_refs_changed"));
            }
            reader.recheck().await?;
            if let Some(comparison) = &comparison_reader {
                comparison.recheck().await?;
                if Some(comparison.correction_pull(q, &reader.name).await?) != pull {
                    return Err(Failure::new(409, "github_publication_pull_changed"));
                }
            }
            let old = if q.expected_old.is_empty() { "0000000000000000000000000000000000000000" } else { &q.expected_old };
            let command = format!("{old} {} {target}\0report-status ofs-delta\n", q.candidate);
            let mut body = format!("{:04x}{command}0000", command.len() + 4).into_bytes();
            body.extend_from_slice(&pack);
            let proof = permit.dispatch("github_publication_expired")?;
            let (reply, response_sha256) = self.git_push(&proof, &reader.name, token, body).await?;
            let mut bytes = reply.as_slice();
            let unpack = packet(&mut bytes)?;
            let status = packet(&mut bytes)?;
            if unpack.map(|line| line.strip_suffix(b"\n").unwrap_or(line)) != Some(b"unpack ok".as_slice())
                || status.map(|line| line.strip_suffix(b"\n").unwrap_or(line)) != Some(format!("ok {target}").as_bytes())
                || bytes.is_empty() || bytes.len() % 4 != 0 || !bytes.chunks_exact(4).all(|packet|packet==b"0000") {
                return Err(Failure::new(409, "github_publication_reply_unconfirmed"));
            }
            let mut receipt = json!({"account":{"external_id":actor.external_id,"login":actor.login},"repository":q.repository,"name":reader.name,"repository_condition":"bounded_observation","branch":target,"old":q.expected_old,"new":q.candidate,"comparison":q.comparison_oid,"comparison_branch":comparison,"comparison_condition":"bounded_observation","ref_condition":"exact_old_oid","bundle_sha256":q.bundle_sha256,"provider_response_sha256":response_sha256});
            if let Some(c) = &q.pull_request {
                receipt["comparison_repository"] = json!(q.comparison_repository);
                receipt["pull_request"] = json!(c);
            }
            super::writes::check_receipt(&receipt)?;
            Ok(receipt)
        }).await.update()
    }
}
