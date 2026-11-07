//! Private Go callbacks over the supervisor's actual execution registry.
//! The native host composes typed launches with its account and source owners.
use super::{
    AttemptPhase, CustodyState, ExecutionBinding, ExecutionOutcome, Executions, LaunchIntent,
};
use serde::Deserialize;
use serde_json::{json, Value};
use std::io;
use std::path::PathBuf;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use std::time::Duration;

#[derive(Deserialize)]
#[serde(tag = "operation", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum Call {
    InspectMuseLogin,
    InspectAccount {
        connection: String,
    },
    Recover {
        intent: String,
        checkout_id: String,
    },
    Launch {
        intent: String,
        prompt: String,
        capability: String,
        secret: String,
        scope_digest: String,
        tools: Vec<String>,
    },
    Inspect {
        intent: String,
    },
    InspectRetained {
        intent: String,
    },
    Cancel {
        intent: String,
    },
    InspectTool {
        harness: String,
    },
    Export {
        intent: String,
        candidate: String,
    },
}

pub(crate) async fn call(
    executions: Arc<Mutex<Executions>>,
    alive: Arc<AtomicBool>,
    tools: Option<Arc<super::Tools>>,
    profile: PathBuf,
    profile_id: String,
    call: Call,
) -> io::Result<Value> {
    let (intent, cancel) = match call {
        Call::InspectMuseLogin => return Err(io::Error::other("native source owner required")),
        Call::InspectAccount { .. } => {
            return Err(io::Error::other("native account owner required"))
        }
        Call::Recover { .. } => {
            return Err(io::Error::other("native work recovery owner required"))
        }
        Call::Launch { .. } => return Err(io::Error::other("native launch owner required")),
        Call::InspectRetained { intent } => {
            return tokio::task::spawn_blocking(move || {
                if !alive.load(Ordering::SeqCst) {
                    return Err(io::Error::other("native owner retired"));
                }
                let intent = LaunchIntent::from_go(&intent)?;
                let scope = intent.scope()?;
                if scope.profile != profile_id
                    || super::read_intent(&profile, &scope)?
                        .is_none_or(|saved| saved.raw() != intent.raw())
                {
                    return Err(io::Error::other("native retained intent differs"));
                }
                let result = super::read_retained(&profile, &scope)?
                    .ok_or_else(|| io::Error::other("native terminal receipt missing"))?;
                let custody = super::read_custody(&profile, &scope, intent.connection())?
                    .filter(|custody| custody.sealed && custody.state == CustodyState::Released)
                    .ok_or_else(|| io::Error::other("native credential release unconfirmed"))?;
                if !alive.load(Ordering::SeqCst) {
                    return Err(io::Error::other("native owner retired"));
                }
                // This read cannot prove root retirement. Go must already have
                // the exact durable retirement acknowledgement before using it.
                Ok(observation(
                    &intent,
                    Some(super::AgentStarted {
                        worker_uid: result.worker_uid,
                        process: result.process.clone(),
                    }),
                    Some(&result),
                    Some(&custody),
                    Some(false),
                    false,
                ))
            })
            .await
            .map_err(|_| io::Error::other("native retained observation failed"))?;
        }
        Call::Export { intent, candidate } => {
            return tokio::task::spawn_blocking(move || {
                if !alive.load(Ordering::SeqCst) {
                    return Err(io::Error::other("native owner retired"));
                }
                let result = super::candidate::export(&profile, &profile_id, &intent, &candidate)?;
                if !alive.load(Ordering::SeqCst) {
                    return Err(io::Error::other("native owner retired"));
                }
                Ok(result)
            })
            .await
            .map_err(|_| io::Error::other("native export failed"))?;
        }
        Call::InspectTool { harness } => {
            if harness != "muse" || !alive.load(Ordering::SeqCst) {
                return Err(io::Error::other("native tool unavailable"));
            }
            let selected = tools
                .ok_or_else(|| io::Error::other("native tool selection unavailable"))?
                .inspect()
                .await?;
            if !alive.load(Ordering::SeqCst) {
                return Err(io::Error::other("native tool owner retired"));
            }
            return serde_json::to_value(selected).map_err(io::Error::other);
        }
        Call::Inspect { intent } => (intent, false),
        Call::Cancel { intent } => (intent, true),
    };
    // Kernel observation is bounded and runs outside the HTTP owner and the
    // registry lock. Runtime shutdown joins this blocking read; it owns no run.
    tokio::task::spawn_blocking(move || snapshot(&executions, &alive, &intent, cancel))
        .await
        .map_err(|_| io::Error::other("native execution observation failed"))?
}

fn snapshot(
    executions: &Mutex<Executions>,
    alive: &AtomicBool,
    raw: &str,
    cancel: bool,
) -> io::Result<Value> {
    let refused = || {
        io::Error::new(
            io::ErrorKind::PermissionDenied,
            "native execution unavailable",
        )
    };
    let intent = LaunchIntent::from_go(raw)?;
    let binding = ExecutionBinding::from_intent(&intent)?;
    let (ticket, outcome) = {
        let mut executions = executions.lock().map_err(|_| refused())?;
        if !alive.load(Ordering::SeqCst) {
            return Err(refused());
        }
        if cancel {
            executions.cancel(&binding)?;
        }
        (executions.ticket(&binding)?, executions.outcome(&binding)?)
    };
    let attempt = ticket.observe();
    let custody = ticket.custody().ok();
    let (started, live, terminal) = match &outcome {
        ExecutionOutcome::Complete(result) => (
            Some(super::AgentStarted {
                worker_uid: result.worker_uid,
                process: result.process.clone(),
            }),
            Some(false),
            Some(result),
        ),
        _ => match ticket.inspect(Duration::from_secs(2)) {
            Ok(observed) => (Some(observed.started), Some(observed.live), None),
            Err(_) => (attempt.started.clone(), None, None),
        },
    };
    let retired = terminal.is_some()
        && attempt.phase == AttemptPhase::Retired
        && custody
            .as_ref()
            .is_some_and(|custody| custody.sealed && custody.state == CustodyState::Released);
    if !alive.load(Ordering::SeqCst) {
        return Err(refused());
    }
    Ok(observation(
        &intent,
        started,
        terminal.map(AsRef::as_ref),
        custody.as_ref(),
        live,
        retired,
    ))
}

fn observation(
    intent: &LaunchIntent,
    started: Option<super::AgentStarted>,
    terminal: Option<&super::AgentRun>,
    custody: Option<&super::CustodyObservation>,
    live: Option<bool>,
    retired: bool,
) -> Value {
    let custody_json = custody.map(|custody| json!({
        "state":match custody.state { CustodyState::Held=>"held", CustodyState::Released=>"released", CustodyState::Uncertain=>"uncertain" },
        "acquisition_id":custody.acquisition_id,"connection":custody.connection,
        "generation":custody.generation.to_string(),"sealed":custody.sealed,
    }));
    let receipt = started.map(|started| json!({
        "worker_uid":started.worker_uid,"invocation":started.process.invocation,
        "system_instance":started.process.system_instance,"start_identity":started.process.start_identity,
        "namespace_pid":started.process.namespace_pid,"started_unix_ms":started.process.started_unix_ms,
        "exit_code":terminal.and_then(|result| result.exit_code),
        "exit_signal":terminal.and_then(|result| result.exit_signal),
    }));
    // Product output is text with its existing 64 KiB limit. Larger or binary
    // native receipts remain retained, but cannot qualify a product result.
    let output = terminal.and_then(|result| {
        (result.output.len() <= 65536)
            .then(|| std::str::from_utf8(&result.output).ok())
            .flatten()
    });
    json!({"intent_digest":intent.digest(),"receipt":receipt,"custody":custody_json,
        "live":live,"retired":retired,"reason":terminal.map(|result| result.reason.as_str()),
        "candidate_state":terminal.map(|result|result.candidate_state.as_str()),
        "candidate":terminal.and_then(|result|result.result_candidate.as_deref()),
        "output":output,"output_complete":terminal.is_some_and(|result| result.output_complete) && output.is_some(),
    })
}
