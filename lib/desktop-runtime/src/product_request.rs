//! Shared desktop/headless product request decoding. This admits bounded HTTP
//! shapes, not provider authority; current caller/resource policy remains in Go.
use serde::Deserialize;
use serde_json::Value;

/// Human writing can escape each byte of 64 KiB into six bytes. Only exact
/// creation/edit submission routes get that envelope; lookup/cancel do not.
pub fn product_request_body_limit(method: &str, target: &str) -> usize {
    if target.len() > 2048 || !target.is_ascii() {
        return 65536;
    }
    let Some(rest) = target.strip_prefix("/api/factory/collaboration/repositories/") else {
        return 65536;
    };
    let parts: Vec<_> = rest.split('/').collect();
    let positive = |value: &str| {
        value
            .parse::<i64>()
            .is_ok_and(|n| n > 0 && n.to_string() == value)
    };
    if method == "POST"
        && parts.len() == 2
        && positive(parts[0])
        && matches!(parts[1], "issues" | "pulls")
    {
        return 512 * 1024;
    }
    if method == "POST"
        && parts.len() == 4
        && positive(parts[0])
        && matches!(parts[1], "issues" | "pulls")
        && positive(parts[2])
        && matches!(parts[3], "comments" | "edits" | "locks")
    {
        512 * 1024
    } else {
        65536
    }
}

/// Shared product HTTP operations, never provider requests or native URLs.
/// Native custody and selected-resource scope are supplied independently.
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum ProductRequest {
    Read { path: String },
    Write { path: String, body: Value },
    Subscription { body: Value },
}
impl ProductRequest {
    pub fn request(self) -> Result<(&'static str, String, Vec<u8>), String> {
        let (method, path, body) = match self {
            Self::Read { path } => ("GET", path, None),
            Self::Write { path, body } => ("POST", path, Some(body)),
            Self::Subscription { body } => {
                ("PUT", "/api/factory/feed/subscription".into(), Some(body))
            }
        };
        let pathname = path.split('?').next().unwrap_or("");
        if path.len() > 2048
            || !path.is_ascii()
            || path.bytes().any(|b| b.is_ascii_control() || b == b'#')
            || !(pathname.starts_with("/api/factory/")
                || pathname.starts_with("/api/repositories/"))
            || pathname.split('/').skip(1).any(|part| {
                part.is_empty()
                    || !part
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_'))
            })
            || body.as_ref().is_some_and(|b| !b.is_object())
        {
            return Err("unsupported Factory product operation".into());
        }
        let body = body
            .map(|b| serde_json::to_vec(&b))
            .transpose()
            .map_err(|_| "invalid Factory product body")?
            .unwrap_or_default();
        if body.len() > product_request_body_limit(method, &path) {
            return Err("Factory product body exceeds the native limit".into());
        }
        Ok((method, path, body))
    }
}
