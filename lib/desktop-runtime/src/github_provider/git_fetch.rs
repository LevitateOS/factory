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
    pub(super) async fn setup_source(&self, q: &Value) -> Result<Value> {
        let commit = r::text(q, "commit")?;
        let target = r::text(q, "target_ref")?;
        self.source_tip(target, commit).await?;
        let mut files = serde_json::Map::new();
        let mut source_files = serde_json::Map::new();
        let paths:Vec<_>=q["paths"].as_array().into_iter().flatten().map(|path|path.as_str().ok_or_else(Failure::invalid)).collect::<Result<_>>()?;
        let mut names:std::collections::BTreeSet<&str>=[
            "setup.sh",
            "check.sh",
            "setup-network.json",
            "setup-limits.json",
        ].into_iter().collect();
        names.extend(paths.iter().copied());
        let mut total=0usize;
        for name in names {
            let mut path=url::Url::parse("https://api.github.com/contents/").map_err(|_|Failure::invalid())?;
            path.path_segments_mut().map_err(|_|Failure::invalid())?.pop_if_empty().extend(name.split('/'));
            let response = match self
                .get(path.path(), &[("ref", commit.into())])
                .await
            {
                Ok(response) => response,
                Err(failure) if failure.status == 404 => continue,
                Err(failure) => return Err(failure),
            };
            let raw = response.value;
            if raw["type"] != "file" || raw["path"] != name || raw["encoding"] != "base64" {
                return Err(Failure::protocol());
            }
            let encoded: String = r::text(&raw, "content")?
                .chars()
                .filter(|c| !c.is_ascii_whitespace())
                .collect();
            let bytes = base64::engine::general_purpose::STANDARD
                .decode(encoded)
                .map_err(|_| Failure::protocol())?;
            if bytes.len() > 32768
                || raw["size"].as_u64() != Some(bytes.len() as u64)
                || std::str::from_utf8(&bytes).is_err()
            {
                return Err(Failure::new(413, "github_setup_file_unavailable"));
            }
            let encoded=json!(base64::engine::general_purpose::STANDARD.encode(&bytes));
            if paths.contains(&name) {
                total+=bytes.len();
                if total>128*1024 {return Err(Failure::new(413,"github_setup_source_too_large"));}
                source_files.insert(name.into(),encoded.clone());
            }
            if ["setup.sh","check.sh","setup-network.json","setup-limits.json"].contains(&name) { files.insert(name.into(),encoded); }
        }
        self.source_tip(target, commit).await?;
        Ok(json!({"repository":self.repository,"commit":commit,"target_ref":target,"files":files,"source_files":source_files}))
    }

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
        let have = if base.is_empty() {
            String::new()
        } else {
            let line = format!("have {base}\n");
            format!("{:04x}{line}", line.len() + 4)
        };
        let body = format!("{:04x}{want}0000{have}0009done\n", want.len() + 4,).into_bytes();
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
        let prerequisite = if base.is_empty() {
            String::new()
        } else {
            format!("-{base} protected source\n")
        };
        let mut bundle = format!("# v2 git bundle\n{prerequisite}{commit} HEAD\n\n").into_bytes();
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
