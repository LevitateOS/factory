//! Non-authorizing continuation for one exact thread observation scope.
use super::{Failure, Result};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Cursor {
    version: u8,
    scope: String,
    page: u32,
    pub(crate) after: String,
    pub(crate) total: Option<u64>,
    pub(crate) offset: u64,
}
pub(crate) fn scope(value: &Value) -> Result<String> {
    Ok(
        Sha256::digest(serde_json::to_vec(value).map_err(|_| Failure::invalid())?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect(),
    )
}
pub(crate) fn decode(raw: &str, scope: &str, page: u32) -> Result<Cursor> {
    if raw.is_empty() && page == 1 {
        return Ok(Cursor {
            version: 1,
            scope: scope.into(),
            page,
            after: String::new(),
            total: None,
            offset: 0,
        });
    }
    let fail = || Failure::new(409, "native_thread_cursor_changed");
    if raw.len() > 2048 {
        return Err(fail());
    }
    let bytes = URL_SAFE_NO_PAD.decode(raw).map_err(|_| fail())?;
    let cursor: Cursor = serde_json::from_slice(&bytes).map_err(|_| fail())?;
    if cursor.version != 1
        || cursor.scope != scope
        || cursor.page != page
        || cursor.offset > 1000
        || cursor.total.is_some_and(|total| total > 1000 || cursor.offset > total)
        || cursor.after.len() > 1024
        || cursor.after.chars().any(char::is_control)
    {
        return Err(fail());
    }
    Ok(cursor)
}
pub(crate) fn encode(
    scope: &str,
    page: u32,
    after: &str,
    total: Option<u64>,
    offset: u64,
) -> Result<String> {
    if page == 0 {
        return Ok(String::new());
    }
    if after.len() > 1024 || after.chars().any(char::is_control) {
        return Err(Failure::protocol());
    }
    Ok(URL_SAFE_NO_PAD.encode(
        serde_json::to_vec(&Cursor {
            version: 1,
            scope: scope.into(),
            page,
            after: after.into(),
            total,
            offset,
        })
        .map_err(|_| Failure::protocol())?,
    ))
}
