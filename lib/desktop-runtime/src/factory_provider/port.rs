//! Private Go callbacks over the supervisor's actual execution registry.
//! Requests observe or cancel an existing captured intent; they cannot launch.
use super::{
    AttemptPhase, CustodyState, ExecutionBinding, ExecutionOutcome, Executions, LaunchIntent,
};
use serde::Deserialize;
use serde_json::{json, Value};
use std::io;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use std::time::Duration;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Call {
    intent: String,
    operation: Operation,
}
#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
enum Operation {
    Inspect,
    Cancel,
}

pub(crate) async fn call(
    executions: Arc<Mutex<Executions>>,
    alive: Arc<AtomicBool>,
    call: Call,
) -> io::Result<Value> {
    // Kernel observation is bounded and runs outside the HTTP owner and the
    // registry lock. Runtime shutdown joins this blocking read; it owns no run.
    tokio::task::spawn_blocking(move || snapshot(&executions, &alive, call))
        .await
        .map_err(|_| io::Error::other("native execution observation failed"))?
}

fn snapshot(executions: &Mutex<Executions>, alive: &AtomicBool, call: Call) -> io::Result<Value> {
    let refused = || {
        io::Error::new(
            io::ErrorKind::PermissionDenied,
            "native execution unavailable",
        )
    };
    let intent = LaunchIntent::from_go(&call.intent)?;
    let binding = ExecutionBinding::from_intent(&intent)?;
    let (ticket, outcome) = {
        let mut executions = executions.lock().map_err(|_| refused())?;
        if !alive.load(Ordering::SeqCst) {
            return Err(refused());
        }
        if matches!(call.operation, Operation::Cancel) {
            executions.cancel(&binding)?;
        }
        (executions.ticket(&binding)?, executions.outcome(&binding)?)
    };
    let attempt = ticket.observe();
    let custody = ticket.custody().ok();
    let custody_json = custody.as_ref().map(|custody| json!({
        "state":match custody.state { CustodyState::Held=>"held", CustodyState::Released=>"released", CustodyState::Uncertain=>"uncertain" },
        "acquisition_id":custody.acquisition_id,"connection":custody.connection,
        "generation":custody.generation.to_string(),"sealed":custody.sealed,
    }));
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
    let receipt = started.map(|started| json!({
        "worker_uid":started.worker_uid,"invocation":started.process.invocation,
        "system_instance":started.process.system_instance,"start_identity":started.process.start_identity,
        "namespace_pid":started.process.namespace_pid,"started_unix_ms":started.process.started_unix_ms,
        "exit_code":terminal.and_then(|result| result.exit_code),
        "exit_signal":terminal.and_then(|result| result.exit_signal),
    }));
    let retired = terminal.is_some()
        && attempt.phase == AttemptPhase::Retired
        && custody
            .as_ref()
            .is_some_and(|custody| custody.sealed && custody.state == CustodyState::Released);
    // Product output is text with its existing 64 KiB limit. Larger or binary
    // native receipts remain retained, but cannot qualify a product result.
    let output = terminal.and_then(|result| {
        (result.output.len() <= 65536)
            .then(|| std::str::from_utf8(&result.output).ok())
            .flatten()
    });
    if !alive.load(Ordering::SeqCst) {
        return Err(refused());
    }
    Ok(
        json!({"intent_digest":intent.digest(),"receipt":receipt,"custody":custody_json,
            "live":live,"retired":retired,"reason":terminal.map(|result| result.reason.as_str()),
            "output":output,"output_complete":terminal.is_some_and(|result| result.output_complete) && output.is_some(),
        }),
    )
}
