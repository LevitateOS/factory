//! Bounded code reads through the current profile enrollment, without host
//! callback impersonation. Source observations never grant execution authority.
use super::{hex, invalid, Client, Result};
use base64::Engine;
use serde::Deserialize;
use std::collections::BTreeMap;

pub struct RepositorySource {
    pub repository: String,
    pub commit: String,
    pub bundle: Vec<u8>,
    pub files: BTreeMap<String, Vec<u8>>,
    pub writers: BTreeMap<String, bool>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Wire {
    repository_id: String,
    commit: String,
    bundle: String,
    files: BTreeMap<String, String>,
    #[serde(default)]
    writers: BTreeMap<String, bool>,
}

impl Client {
    pub async fn source(
        &self,
        commit: &str,
        paths: &[String],
        actors: &[String],
    ) -> Result<RepositorySource> {
        if (!commit.is_empty() && !hex(commit, 40)) || paths.len() > 64 || actors.len() > 8 {
            return Err(invalid());
        }
        let mut selected = std::collections::BTreeSet::new();
        for path in paths {
            if path.is_empty()
                || path.len() > 1024
                || path.starts_with('/')
                || path.contains(['\\', '\0', '\r', '\n'])
                || path
                    .split('/')
                    .any(|part| matches!(part, "" | "." | ".." | ".git" | ".soda-home"))
                || !selected.insert(path)
            {
                return Err(invalid());
            }
        }
        let mut writers = std::collections::BTreeSet::new();
        for actor in actors {
            if !actor
                .parse::<u64>()
                .is_ok_and(|id| id > 0 && id.to_string() == *actor)
                || !writers.insert(actor)
            {
                return Err(invalid());
            }
        }
        let enrollment = self.status().await?;
        if !enrollment.active {
            return Err(invalid());
        }
        let body=serde_json::to_vec(&serde_json::json!({"source_commit":commit,"source_paths":paths,"source_actors":actors})).map_err(|_|invalid())?;
        let wire: Wire = self
            .request_bounded(
                reqwest::Method::POST,
                &[&self.command, "source"],
                Some(body),
                1024 * 1024,
            )
            .await?;
        if wire.repository_id != enrollment.repository.id
            || !hex(&wire.commit, 40)
            || !commit.is_empty() && wire.commit != commit
            || wire.files.len() != paths.len()
            || wire.writers.len() != actors.len()
            || actors.iter().any(|actor| !wire.writers.contains_key(actor))
        {
            return Err(invalid());
        }
        let bundle = base64::engine::general_purpose::STANDARD
            .decode(wire.bundle)
            .map_err(|_| invalid())?;
        if bundle.is_empty() || bundle.len() > 512 * 1024 {
            return Err(invalid());
        }
        let mut files = BTreeMap::new();
        let mut total = 0;
        for (path, encoded) in wire.files {
            if !selected.contains(&path) {
                return Err(invalid());
            }
            let bytes = base64::engine::general_purpose::STANDARD
                .decode(encoded)
                .map_err(|_| invalid())?;
            total += bytes.len();
            if bytes.len() > 32 * 1024 || total > 128 * 1024 {
                return Err(invalid());
            }
            files.insert(path, bytes);
        }
        let current = self.status().await?;
        if !current.active
            || current.repository.id != enrollment.repository.id
            || current.actor.id != enrollment.actor.id
        {
            return Err(invalid());
        }
        Ok(RepositorySource {
            repository: wire.repository_id,
            commit: wire.commit,
            bundle,
            files,
            writers: wire.writers,
        })
    }
}
