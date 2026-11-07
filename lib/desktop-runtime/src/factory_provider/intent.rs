//! Exact nonsecret Go launch binding, retained before custody and root staging.
//! This is native effect evidence; Go still owns current product admission.
use super::{receipt, Policy, ReceiptScope, Role};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs::{self, DirBuilder, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use std::time::Instant;
use time::{format_description::well_known::Rfc3339, OffsetDateTime};

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}
fn hex(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}
fn text(value: &str, limit: usize) -> bool {
    !value.is_empty() && value.len() <= limit && !value.chars().any(char::is_control)
}

// Defining wire: pkg/factory/execution/native_intent.go. Only native selector
// constraints are enforced here; product policy and SQL stay in Go.
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Wire {
    kind: String,
    version: u8,
    run: String,
    workspace: String,
    role: String,
    source_commit: String,
    connection: String,
    deadline: String,
    #[serde(default)]
    model: String,
    #[serde(default)]
    effort: String,
    #[serde(default)]
    contributor: bool,
    native: Native,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Native {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    authentication: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    credential_sha256: String,
    workspace: Workspace,
    tool: Tool,
    bundle_sha256: String,
    prompt_sha256: String,
    runtime: String,
    placement_generation: String,
    trust_mode: String,
    broker_run: String,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Workspace {
    profile: String,
    instance_id: String,
    worktree_id: String,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Tool {
    harness: String,
    version: String,
    sha256: String,
    executable: String,
}

/// Decode only from a trusted admitted Go operation. Shape validation grants
/// no spending, placement, account or execution authority. Raw bytes stay exact.
#[derive(Clone)]
pub struct LaunchIntent {
    raw: String,
    wire: Wire,
}

impl LaunchIntent {
    pub fn from_go(raw: &str) -> io::Result<Self> {
        if raw.is_empty() || raw.len() > 16384 {
            return Err(refused("native intent bound exceeded"));
        }
        let wire: Wire =
            serde_json::from_str(raw).map_err(|_| refused("native intent shape invalid"))?;
        let n = &wire.native;
        if wire.kind != "factory_native_launch_intent"
            || !matches!(wire.version, 1 | 2 | 3 | 4)
            || !hex(&wire.run, 32)
            || !text(&wire.workspace, 128)
            || !hex(&wire.connection, 32)
            || !hex(&n.workspace.instance_id, 32)
            || !text(&n.workspace.worktree_id, 128)
            || !hex(&n.runtime, 32)
            || !matches!(n.placement_generation.parse::<i64>(), Ok(value) if value>0 && value.to_string()==n.placement_generation)
            || !matches!(
                n.trust_mode.as_str(),
                "isolated_system_worker" | "cli_managed_provider"
            )
            || (wire.version == 4) != (n.trust_mode == "cli_managed_provider")
            || !text(&n.tool.harness, 128)
            || !text(&n.tool.version, 128)
            || !Path::new(&n.tool.executable).is_absolute()
            || !text(&n.tool.executable, 4096)
            || OffsetDateTime::parse(&wire.deadline, &Rfc3339).is_err()
        {
            return Err(refused("native intent selectors invalid"));
        }
        if wire.version == 1
            && (!wire.model.is_empty() || !wire.effort.is_empty() || wire.contributor)
            || wire.version >= 2
                && (wire.model.is_empty()
                    || !matches!(
                        wire.effort.as_str(),
                        "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra"
                    ))
        {
            return Err(refused("native intent harness profile invalid"));
        }
        if wire.version < 3 && !n.credential_sha256.is_empty()
            || wire.version == 3 && !hex(&n.credential_sha256, 64)
            || wire.version < 4 && !n.authentication.is_empty()
            || wire.version == 4
                && (n.authentication != "muse_login" || !n.credential_sha256.is_empty())
        {
            return Err(refused("native intent credential binding invalid"));
        }
        let intent = Self {
            raw: raw.into(),
            wire,
        };
        intent.scope()?;
        Ok(intent)
    }
    pub fn raw(&self) -> &str {
        &self.raw
    }
    pub fn digest(&self) -> String {
        Sha256::digest(self.raw.as_bytes())
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    }
    pub fn run(&self) -> &str {
        &self.wire.run
    }
    pub fn workspace(&self) -> &str {
        &self.wire.workspace
    }
    pub fn instance(&self) -> &str {
        &self.wire.native.workspace.instance_id
    }
    pub fn worktree(&self) -> &str {
        &self.wire.native.workspace.worktree_id
    }
    pub fn connection(&self) -> &str {
        &self.wire.connection
    }
    pub(crate) fn muse_login(&self) -> bool {
        self.wire.version == 4
    }
    pub(super) fn remaining(&self) -> io::Result<std::time::Duration> {
        let deadline = OffsetDateTime::parse(&self.wire.deadline, &Rfc3339)
            .map_err(|_| refused("native intent deadline invalid"))?;
        let remaining: std::time::Duration = (deadline - OffsetDateTime::now_utc())
            .try_into()
            .map_err(|_| refused("native intent deadline expired"))?;
        if remaining.is_zero() || remaining > super::MAX_RUN {
            return Err(refused("native intent deadline outside limits"));
        }
        Ok(remaining)
    }
    pub(super) fn credential_sha256(&self) -> Option<&str> {
        (self.wire.version == 3).then_some(self.wire.native.credential_sha256.as_str())
    }
    pub(super) fn executable(&self) -> &Path {
        Path::new(&self.wire.native.tool.executable)
    }
    pub(super) fn tool_sha256(&self) -> &str {
        &self.wire.native.tool.sha256
    }
    pub(super) fn current_profile(&self) -> io::Result<soda_execution_protocol::HarnessProfile> {
        if self.wire.version < 2 {
            return Err(refused("historical native intent cannot start new work"));
        }
        let profile = soda_execution_protocol::HarnessProfile {
            model: self.wire.model.clone(),
            effort: self.wire.effort.clone(),
            contributor: self.wire.contributor,
        };
        if !profile.valid() {
            return Err(refused("native harness profile unsupported"));
        }
        Ok(profile)
    }
    pub(super) fn scope(&self) -> io::Result<ReceiptScope<'_>> {
        let role = match self.wire.role.as_str() {
            "soda-coder" => Role::Coder,
            "soda-reviewer" => Role::Reviewer,
            _ => return Err(refused("native intent role unsupported")),
        };
        ReceiptScope::new(
            &self.wire.native.broker_run,
            &self.wire.native.workspace.profile,
            &self.wire.native.bundle_sha256,
            &self.wire.source_commit,
            role,
            &self.wire.native.tool.sha256,
            &self.wire.native.prompt_sha256,
        )
    }
    pub(super) fn bind_policy(
        &self,
        policy: &mut Policy,
        tool: &str,
        prompt: &str,
    ) -> io::Result<()> {
        let scope = self.scope()?;
        let expected = ReceiptScope::new(
            policy.run(),
            policy.profile(),
            policy.sha256(),
            policy.candidate(),
            policy.role(),
            tool,
            prompt,
        )?;
        if scope.fingerprint() != expected.fingerprint() || self.connection() != policy.connection()
        {
            return Err(refused("native intent differs from run policy"));
        }
        let deadline = OffsetDateTime::parse(&self.wire.deadline, &Rfc3339)
            .map_err(|_| refused("native intent deadline invalid"))?;
        let remaining: std::time::Duration = (deadline - OffsetDateTime::now_utc())
            .try_into()
            .map_err(|_| refused("native intent deadline expired"))?;
        if remaining.is_zero() {
            return Err(refused("native intent deadline expired"));
        }
        policy.deadline = policy.deadline.min(Instant::now() + remaining);
        if self.wire.version >= 2 {
            policy.harness_profile = Some(self.current_profile()?);
        }
        Ok(())
    }
    pub(super) fn matches_record(
        &self,
        run: &str,
        workspace: &str,
        instance: &str,
        worktree: &str,
        deadline: &str,
        placement: Option<&serde_json::Value>,
    ) -> bool {
        self.run() == run
            && self.workspace() == workspace
            && self.instance() == instance
            && self.worktree() == worktree
            && OffsetDateTime::parse(&self.wire.deadline, &Rfc3339).ok()
                == OffsetDateTime::parse(deadline, &Rfc3339).ok()
            && placement.is_none_or(|placement| {
                serde_json::to_value(&self.wire.native).ok().as_ref() == Some(placement)
            })
    }
    pub(super) fn persist(&self, profile: &Path) -> io::Result<()> {
        let scope = self.scope()?;
        receipt::profile(profile, scope.profile)?;
        // A fresh intent cannot retroactively adopt a historical acquisition
        // or receipt, even when its selectors happen to match.
        for path in [
            profile.join("native-run-custody").join(scope.run),
            profile
                .join("native-run-receipts")
                .join(format!("{}.receipt", scope.run)),
            profile
                .join("native-run-receipts")
                .join(format!("{}.pending", scope.run)),
        ] {
            absent(&path)?;
        }
        let root = profile.join("native-run-intents");
        match DirBuilder::new().mode(0o700).create(&root) {
            Ok(()) => File::open(profile)?.sync_all()?,
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error),
        }
        private_directory(&root)?;
        let committed = root.join(format!("{}.json", scope.run));
        let pending = root.join(format!("{}.pending", scope.run));
        absent(&committed)?;
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&pending)?;
        file.write_all(self.raw.as_bytes())?;
        file.sync_all()?;
        fs::hard_link(&pending, &committed)?;
        File::open(&root)?.sync_all()?;
        fs::remove_file(&pending)?;
        File::open(&root)?.sync_all()
    }
}

fn absent(path: &Path) -> io::Result<()> {
    match path.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
        Ok(_) => Err(refused("native intent identity already consumed")),
    }
}

pub(super) fn require_absent(profile: &Path, scope: &ReceiptScope<'_>) -> io::Result<()> {
    let root = profile.join("native-run-intents");
    match root.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
        Ok(_) => private_directory(&root)?,
    }
    absent(&root.join(format!("{}.json", scope.run)))?;
    absent(&root.join(format!("{}.pending", scope.run)))
}
fn private_directory(path: &Path) -> io::Result<()> {
    let metadata = path.symlink_metadata()?;
    if !metadata.is_dir()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
        || fs::canonicalize(path)? != path
    {
        return Err(refused("native intent directory invalid"));
    }
    Ok(())
}

/// Read without replay or filling missing history. A pending file is unknown,
/// even if a committed link also exists after an interrupted publication.
pub fn read_intent(profile: &Path, scope: &ReceiptScope<'_>) -> io::Result<Option<LaunchIntent>> {
    receipt::profile(profile, scope.profile)?;
    let root: PathBuf = profile.join("native-run-intents");
    match root.symlink_metadata() {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
        Ok(_) => private_directory(&root)?,
    }
    absent(&root.join(format!("{}.pending", scope.run)))?;
    let file = match OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(root.join(format!("{}.json", scope.run)))
    {
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        other => other?,
    };
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.uid() != unsafe { libc::geteuid() }
        || metadata.mode() & 0o077 != 0
        || metadata.nlink() != 1
        || metadata.len() > 16384
    {
        return Err(refused("native intent file invalid"));
    }
    let mut raw = String::new();
    (&file).take(16385).read_to_string(&mut raw)?;
    if raw.len() as u64 != metadata.len() {
        return Err(refused("native intent changed while reading"));
    }
    let intent = LaunchIntent::from_go(&raw)?;
    if intent.scope()?.fingerprint() != scope.fingerprint() {
        return Err(refused("native intent retained scope differs"));
    }
    Ok(Some(intent))
}
