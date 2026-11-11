//! Export only a retained, scoped worker receipt; Go selects the product result.
use super::{read_intent, read_retained, LaunchIntent};
use base64::Engine;
use serde_json::{json, Value};
use soda_execution_protocol::{workspace::Status, CandidateState};
use std::io;
use std::path::Path;

pub(super) fn muse_bundle(
    profile: &Path,
    intent: &LaunchIntent,
    receipt: &super::MuseRun,
    candidate: &str,
) -> io::Result<Vec<u8>> {
    if !receipt.root_retired || receipt.candidate.as_deref() != Some(candidate) {
        return Err(io::Error::other("native candidate unavailable"));
    }
    let manifest = intent.retained_manifest(profile)?;
    let result = super::Broker::retained_task_receipt(
        profile,
        &intent.scope()?,
        &manifest,
        &soda_execution_protocol::workspace::Parent::from_muse(&receipt.process),
        &soda_execution_protocol::workspace::Command::Final {
            expected_head: candidate.into(),
        },
    )?;
    if result.status != Status::Committed || result.bundle.is_empty() {
        return Err(io::Error::other("native candidate unavailable"));
    }
    Ok(result.bundle)
}

pub(super) fn export(
    profile: &Path,
    profile_id: &str,
    raw: &str,
    candidate: &str,
) -> io::Result<Value> {
    let denied = || {
        io::Error::new(
            io::ErrorKind::PermissionDenied,
            "native candidate unavailable",
        )
    };
    let intent = LaunchIntent::from_go(raw)?;
    let scope = intent.scope()?;
    if scope.profile != profile_id
        || !super::hex(candidate, 40)
        || !matches!(scope.role, super::Role::Coder)
    {
        return Err(denied());
    }
    let saved = read_intent(profile, &scope)?.ok_or_else(denied)?;
    if saved.raw() != raw {
        return Err(denied());
    }
    if intent.muse_login() {
        let receipt = super::muse_receipt::retained(profile, &intent)?
            .filter(|r| r.root_retired)
            .ok_or_else(denied)?;
        if receipt.candidate.as_deref() != Some(candidate) {
            return Ok(json!({"intent_digest":intent.digest(),"status":"candidate_mismatch"}));
        }
        let bundle = muse_bundle(profile, &intent, &receipt, candidate)?;
        use sha2::{Digest, Sha256};
        let sha256: String = Sha256::digest(&bundle)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        return Ok(
            json!({"intent_digest":intent.digest(),"status":"exported","candidate":candidate,"sha256":sha256,
            "bundle":base64::engine::general_purpose::STANDARD.encode(&bundle)}),
        );
    }
    let receipt = read_retained(profile, &scope)?.ok_or_else(denied)?;
    if receipt.candidate_state != CandidateState::Captured
        || receipt.result_candidate.as_deref() != Some(candidate)
    {
        return Ok(json!({"intent_digest":intent.digest(),"status":"candidate_mismatch"}));
    }
    let sha256 = receipt.bundle_sha256.as_deref().ok_or_else(denied)?;
    if receipt.candidate_bundle.is_empty() {
        return Err(denied());
    }
    Ok(
        json!({"intent_digest":intent.digest(),"status":"exported","candidate":candidate,"sha256":sha256,
        "bundle":base64::engine::general_purpose::STANDARD.encode(&receipt.candidate_bundle)}),
    )
}
