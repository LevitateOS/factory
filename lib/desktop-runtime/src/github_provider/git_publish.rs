//! Transfer the retained candidate pack and conditionally update one Git ref.
//! Git's receive-pack checks the advertised old OID. The comparison branch is a
//! bounded observation, not an atomic condition on that second ref.
use super::transport::{advertised, packet, GitService};
use super::{
    reads::Reader,
    records as r,
    request::{branch_name, commit, remote_id},
    Client, Failure, Result, WriteOutcome,
};
use crate::credentials::Token;
use base64::Engine;
use serde::{Deserialize, Serialize};
use serde_json::json;
use sha2::{Digest, Sha256};
use soda_execution_protocol::MAX_STAGE_BUNDLE;
use std::time::Instant;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BranchCorrection {
    pub number: u32,
    pub expected_author_id: i64,
    pub expected_pr_id: i64,
    pub expected_issue_id: i64,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BranchPublish {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub controller_grant_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source_connection: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pull_request: Option<BranchCorrection>,
    pub repository: String,
    pub branch: String,
    pub expected_old: String,
    pub candidate: String,
    pub base: String,
    pub comparison_branch: String,
    pub comparison_oid: String,
    pub bundle_sha256: String,
    pub bundle: String,
}
impl BranchPublish {
    pub fn validate(&self) -> Result<()> {
        if !remote_id(&self.repository)
            || !branch_name(&self.branch)
            || !branch_name(&self.comparison_branch)
            || self.branch == self.comparison_branch
            || !commit(&self.candidate)
            || !commit(&self.base)
            || !commit(&self.comparison_oid)
            || !(self.expected_old.is_empty() || commit(&self.expected_old))
            || self.expected_old == self.candidate
            || self.pull_request.as_ref().is_some_and(|c| {
                self.controller_grant_id.is_empty() || self.expected_old.is_empty()
                    || c.number == 0 || c.expected_author_id <= 0
                    || c.expected_pr_id <= 0 || c.expected_issue_id <= 0
            })
        {
            return Err(Failure::new(400, "invalid_native_write"));
        }
        self.pack().map(|_| ())
    }
    pub(crate) fn pack(&self) -> Result<Vec<u8>> {
        if self.bundle.is_empty() || self.bundle.len() > MAX_STAGE_BUNDLE.div_ceil(3) * 4 {
            return Err(Failure::new(413, "github_candidate_too_large"));
        }
        let bundle = base64::engine::general_purpose::STANDARD
            .decode(&self.bundle)
            .map_err(|_| Failure::invalid())?;
        if bundle.len() > MAX_STAGE_BUNDLE
            || Sha256::digest(&bundle)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect::<String>()
                != self.bundle_sha256
        {
            return Err(Failure::new(400, "github_candidate_digest_differs"));
        }
        let end = bundle
            .windows(2)
            .position(|bytes| bytes == b"\n\n")
            .ok_or_else(Failure::invalid)?;
        let header = std::str::from_utf8(&bundle[..end]).map_err(|_| Failure::invalid())?;
        let mut lines = header.lines();
        if lines.next() != Some("# v2 git bundle") {
            return Err(Failure::new(400, "github_candidate_bundle_unsupported"));
        }
        let mut target = false;
        let mut prerequisite = false;
        for line in lines {
            if let Some(value) = line.strip_prefix('-') {
                let (oid, _) = value.split_once(' ').ok_or_else(Failure::invalid)?;
                if prerequisite || oid != self.base {
                    return Err(Failure::invalid());
                }
                prerequisite = true;
            } else {
                if target || line != format!("{} HEAD", self.candidate) {
                    return Err(Failure::invalid());
                }
                target = true;
            }
        }
        let pack = &bundle[end + 2..];
        if !target || pack.len() < 32 || &pack[..4] != b"PACK" {
            return Err(Failure::invalid());
        }
        Ok(pack.to_vec())
    }
}

impl Reader<'_> {
    pub(super) async fn publication_refs(
        &self,
        q: &serde_json::Value,
    ) -> Result<serde_json::Value> {
        let branch = r::text(q, "name")?;
        let base = r::text(q, "base_branch")?;
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
            Ok(
                json!({"repository":self.repository,"name":self.name,"repository_node_id":r::text(&raw,"node_id")?,"push":r::boolean(&raw["permissions"],"push")?,"archived":r::boolean(&raw,"archived")?,"disabled":r::boolean(&raw,"disabled")?,"branch":branch,"tip":refs.get(&format!("refs/heads/{branch}")).map(String::as_str).unwrap_or(""),"comparison_branch":base,"comparison":refs.get(&format!("refs/heads/{base}")).map(String::as_str).unwrap_or("")}),
            )
        };
        let before = observe().await?;
        self.recheck().await?;
        if observe().await? != before {
            return Err(Failure::new(409, "github_publication_refs_changed"));
        }
        Ok(before)
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
            if !q.controller_grant_id.is_empty() || !q.source_connection.is_empty() || q.pull_request.is_some() { return Err(Failure::invalid()); }
            q.validate()?;
            let pack = q.pack()?;
            let (mut permit, actor) = mutation.admit(self, token, expected).await?;
            let reader = Reader::new(self, token, &q.repository).await?;
            let repository = reader.get("", &[]).await?.value;
            if r::id(&repository["id"])? != q.repository || repository["full_name"] != reader.name
                || repository["permissions"]["push"] != true || repository["archived"] != false || repository["disabled"] != false {
                return Err(Failure::new(403, "github_publication_unavailable"));
            }
            let (advertisement, _) = self.git_request(&reader.name, token, GitService::ReceivePack, None).await?;
            let refs = advertised(&advertisement, GitService::ReceivePack)?;
            let target = format!("refs/heads/{}", q.branch);
            let comparison = format!("refs/heads/{}", q.comparison_branch);
            if refs.get(&comparison).map(String::as_str) != Some(q.comparison_oid.as_str())
                || refs.get(&target).map(String::as_str).unwrap_or("") != q.expected_old {
                return Err(Failure::new(409, "github_publication_refs_changed"));
            }
            reader.recheck().await?;
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
            let receipt = json!({"account":{"external_id":actor.external_id,"login":actor.login},"repository":q.repository,"name":reader.name,"repository_condition":"bounded_observation","branch":target,"old":q.expected_old,"new":q.candidate,"comparison":q.comparison_oid,"comparison_branch":comparison,"comparison_condition":"bounded_observation","ref_condition":"exact_old_oid","bundle_sha256":q.bundle_sha256,"provider_response_sha256":response_sha256});
            super::writes::check_receipt(&receipt)?;
            Ok(receipt)
        }).await.update()
    }
}
