//! Fetch one observed branch tip for the existing isolated source importer.
use super::{
    reads::Reader,
    records as r,
    transport::{advertised, packet, GitService},
    Failure, Result,
};
use base64::Engine;
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use soda_execution_protocol::MAX_STAGE_BUNDLE;

impl Reader<'_> {
    async fn source_tip(&self, target: &str, commit: &str) -> Result<()> {
        let (repository, advertisement) = tokio::try_join!(
            self.get("", &[]),
            self.client
                .git_request(&self.name, self.token, GitService::UploadPack, None)
        )?;
        if r::id(&repository.value["id"])? != self.repository
            || repository.value["full_name"] != self.name
            || advertised(&advertisement.0, GitService::UploadPack)?
                .get(target)
                .map(String::as_str)
                != Some(commit)
        {
            return Err(Failure::new(409, "github_source_tip_changed"));
        }
        self.client.current()
    }

    pub(super) async fn source_bundle(&self, q: &Value) -> Result<Value> {
        let base = r::text(q, "base")?;
        let commit = r::text(q, "commit")?;
        let target = r::text(q, "target_ref")?;
        self.source_tip(target, commit).await?;
        // Protocol v0, one known protected ancestor, no thin pack or sideband.
        // Git validates objects, prerequisites and ancestry inside the importer.
        let want = format!("want {commit} ofs-delta\n");
        let have = format!("have {base}\n");
        let body = format!(
            "{:04x}{want}0000{:04x}{have}0009done\n",
            want.len() + 4,
            have.len() + 4
        )
        .into_bytes();
        let (reply, _) = self
            .client
            .git_request(&self.name, self.token, GitService::UploadPack, Some(body))
            .await?;
        let mut pack = reply.as_slice();
        let acknowledgement = packet(&mut pack)?;
        if acknowledgement != Some(b"NAK\n".as_slice())
            && acknowledgement != Some(format!("ACK {base}\n").as_bytes())
        {
            return Err(Failure::protocol());
        }
        if pack.len() < 32 || !pack.starts_with(b"PACK") {
            return Err(Failure::protocol());
        }
        let mut bundle =
            format!("# v2 git bundle\n-{base} protected source\n{commit} HEAD\n\n").into_bytes();
        if bundle.len() + pack.len() > MAX_STAGE_BUNDLE {
            return Err(Failure::new(413, "github_source_bundle_too_large"));
        }
        bundle.extend_from_slice(pack);
        self.source_tip(target, commit).await?;
        let sha256: String = Sha256::digest(&bundle)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        Ok(
            json!({"repository":self.repository,"base":base,"commit":commit,"target":target,"sha256":sha256,
            "bundle":base64::engine::general_purpose::STANDARD.encode(bundle)}),
        )
    }
}
