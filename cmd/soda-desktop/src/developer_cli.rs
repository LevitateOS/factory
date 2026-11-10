mod developer_protocol;
use developer_protocol::{error, Operation, Request, Target, WaitState};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::io::Write;
use std::os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt};
use std::path::PathBuf;
use std::time::Duration;

fn main() {
    let (value, code) = match run() {
        Ok(v) => {
            let code = exit_code(&v);
            (v, code)
        }
        Err(e) => (error(e), 2),
    };
    println!("{}", value);
    std::process::exit(code);
}
fn exit_code(v: &Value) -> i32 {
    if v["ok"] == true {
        0
    } else {
        match v["error"].as_str() {
            Some("deadline") => 3,
            Some("outcome_unknown") => 4,
            _ => 2,
        }
    }
}
fn run() -> Result<Value, &'static str> {
    let mut options = BTreeMap::new();
    let mut words = Vec::new();
    let mut args = std::env::args().skip(1);
    while let Some(arg) = args.next() {
        if arg == "--help" {
            return Ok(
                json!({"ok":true,"usage":"factory-dev --profile ABSOLUTE_PATH [--window LABEL] status | ui tree | ui click/fill/select/check/wait --scope S [--within REGION] --role ROLE --name NAME [--action ID] [--value VALUE --expected PRIOR] [--state enabled --timeout 30]","notes":"Actions require a unique caller-supplied --action and the original --scope. check uses --value true/false --expected true/false. --label is an alias for --name. --node selects a snapshot handle. Exit: 0 success, 2 refusal/unavailable, 3 deadline, 4 unknown dispatch. No automatic retries."}),
            );
        }
        if arg.starts_with("--") {
            if ![
                "--profile",
                "--window",
                "--scope",
                "--action",
                "--within",
                "--role",
                "--name",
                "--label",
                "--node",
                "--value",
                "--expected",
                "--state",
                "--timeout",
            ]
            .contains(&arg.as_str())
            {
                return Err("unsupported_option");
            }
            let value = args.next().ok_or("missing_option_value")?;
            if options.insert(arg, value).is_some() {
                return Err("duplicate_option");
            }
        } else {
            words.push(arg);
        }
    }
    let profile = PathBuf::from(options.remove("--profile").ok_or("profile_required")?);
    if !profile.is_absolute()
        || std::fs::canonicalize(&profile).map_err(|_| "unavailable")? != profile
    {
        return Err("canonical_profile_required");
    }
    developer_protocol::private_directory(&profile)?;
    developer_protocol::private_directory(&developer_protocol::directory(&profile))?;
    let path = developer_protocol::socket(&profile);
    let m = std::fs::symlink_metadata(&path).map_err(|_| "unavailable")?;
    if !m.file_type().is_socket() || m.uid() != unsafe { libc::geteuid() } || m.mode() & 0o077 != 0
    {
        return Err("private_socket_required");
    }
    let window = options.remove("--window");
    let scope = options.remove("--scope");
    let action = options.remove("--action");
    let target = Target {
        within: options.remove("--within"),
        role: options.remove("--role"),
        name: options
            .remove("--name")
            .or_else(|| options.remove("--label")),
        node: options.remove("--node"),
    };
    let mut take = |key: &str| options.remove(key).ok_or("missing_option_value");
    let operation = match words
        .iter()
        .map(String::as_str)
        .collect::<Vec<_>>()
        .as_slice()
    {
        ["status"] => Operation::Status,
        ["ui", "tree"] => Operation::Tree,
        ["ui", "click"] => Operation::Click { target },
        ["ui", "fill"] => Operation::Fill {
            target,
            value: take("--value")?,
            expected: take("--expected")?,
        },
        ["ui", "select"] => Operation::Select {
            target,
            value: take("--value")?,
            expected: take("--expected")?,
        },
        ["ui", "check"] => Operation::Check {
            target,
            checked: take("--value")?.parse().map_err(|_| "boolean_required")?,
            expected: take("--expected")?
                .parse()
                .map_err(|_| "boolean_required")?,
        },
        ["ui", "wait"] => {
            let state = match take("--state")?.as_str() {
                "present" => WaitState::Present,
                "absent" => WaitState::Absent,
                "enabled" => WaitState::Enabled,
                "disabled" => WaitState::Disabled,
                "checked" => WaitState::Checked,
                "unchecked" => WaitState::Unchecked,
                _ => return Err("invalid_state"),
            };
            let seconds: u64 = take("--timeout")?.parse().map_err(|_| "invalid_deadline")?;
            Operation::Wait {
                target,
                state,
                timeout_ms: seconds.checked_mul(1000).ok_or("invalid_deadline")?,
            }
        }
        _ => return Err("unsupported_command_use_help"),
    };
    if !options.is_empty() {
        return Err("unused_option");
    }
    let request = Request {
        version: 1,
        window,
        scope,
        action,
        operation,
    };
    let body = serde_json::to_vec(&request).map_err(|_| "invalid_request")?;
    if body.len() > developer_protocol::REQUEST_LIMIT {
        return Err("request_too_large");
    }
    let request = Request::decode(&body)?;
    if request.is_action() {
        let scope: Value =
            serde_json::from_str(request.scope.as_ref().unwrap()).map_err(|_| "invalid_scope")?;
        let session = scope["session"]
            .as_str()
            .filter(|s| developer_protocol::valid_id(s))
            .ok_or("invalid_scope")?;
        let log = developer_protocol::directory(&profile).join(format!("client-{session}.jsonl"));
        let mut file = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW)
            .open(log)
            .map_err(|_| "intent_log_unavailable")?;
        let metadata = file.metadata().map_err(|_| "intent_log_unavailable")?;
        if !metadata.is_file()
            || metadata.uid() != unsafe { libc::geteuid() }
            || metadata.mode() & 0o077 != 0
        {
            return Err("private_intent_log_required");
        }
        let record = json!({"session":session,"action":request.action,"window":request.window,"fingerprint":developer_protocol::hex(&Sha256::digest(&body)),"provenance":"ui_dom"});
        // One append preserves a pre-send intent without writing editable content.
        let mut line = serde_json::to_vec(&record).map_err(|_| "intent_log_unavailable")?;
        line.push(b'\n');
        file.write_all(&line)
            .and_then(|_| file.sync_data())
            .map_err(|_| "intent_log_unavailable")?;
    }
    let response = soda_unix_http::request(
        &path,
        "POST",
        "/dev/v1/call",
        "factory-developer",
        &body,
        Duration::from_millis(request.timeout_ms() + 1000),
        soda_unix_http::Limits {
            header_bytes: 16384,
            body_bytes: developer_protocol::RESPONSE_LIMIT,
        },
    );
    match response {
        Ok(reply) => serde_json::from_slice(&reply.body).map_err(|_| {
            if request.is_action() {
                "outcome_unknown"
            } else {
                "invalid_response"
            }
        }),
        Err(_) => Ok(error(if request.is_action() {
            "outcome_unknown"
        } else {
            "unavailable"
        })),
    }
}
