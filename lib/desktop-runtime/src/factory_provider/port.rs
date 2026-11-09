//! Private Go callbacks over the supervisor's actual execution registry.
//! The native host composes typed launches with its account and source owners.
use super::{
    AttemptPhase, CustodyState, ExecutionBinding, ExecutionOutcome, Executions, LaunchIntent,
};
use serde::Deserialize;
use serde_json::{json, Value};
use soda_execution_protocol::workspace::{Command as TaskCommand, Parent, Reply as TaskReply};
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
    BeginJudgment {
        request: super::judgment::Request,
    },
    InspectJudgment {
        id: String,
        digest: String,
        cancel: bool,
    },
    Task {
        intent: String,
        parent: Parent,
        command: TaskCommand,
    },
    ReadTaskReceipt {
        intent: String,
        parent: Parent,
        command: TaskCommand,
    },
    TaskEvents {
        intent: String,
        parent: Parent,
    },
    Completed,
    Acknowledge {
        intent: String,
        reference: String,
    },
    InspectMuseLogin,
    InspectAccount {
        connection: String,
    },
    RecoveryTarget,
    Recover {
        intent: String,
        checkout_id: String,
    },
    Launch {
        intent: String,
        #[serde(default)]
        manifest: String,
        prompt: String,
        capability: String,
        secret: String,
        scope_digest: String,
        tools: Vec<String>,
        #[serde(default)]
        admission: Option<Value>,
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
        Call::Task {
            intent,
            parent,
            command,
        } => {
            return tokio::task::spawn_blocking(move || {
                if !alive.load(Ordering::SeqCst)
                    || matches!(command, TaskCommand::BindParent { .. } | TaskCommand::Close)
                {
                    return Err(io::Error::other("native task unavailable"));
                }
                let (intent, manifest) = task_binding(&profile, &profile_id, &intent, &parent)?;
                if command.retains_receipt() {
                    if let Ok(reply) = super::Broker::retained_task_receipt(
                        &profile,
                        &intent.scope()?,
                        &manifest,
                        &parent,
                        &command,
                    ) {
                        return task_result(&intent, reply);
                    }
                }
                let ticket = executions
                    .lock()
                    .map_err(|_| io::Error::other("native registry unavailable"))?
                    .ticket(&ExecutionBinding::from_intent(&intent)?)?;
                let reply = match ticket.task(&parent, &command) {
                    Ok(reply) => reply,
                    Err(e) if e.kind() == io::ErrorKind::WouldBlock => TaskReply {
                        status: "busy".into(),
                        ..TaskReply::default()
                    },
                    Err(e) => return Err(e),
                };
                task_result(&intent, reply)
            })
            .await
            .map_err(|_| io::Error::other("native task call failed"))?;
        }
        Call::ReadTaskReceipt {
            intent,
            parent,
            command,
        } => {
            return tokio::task::spawn_blocking(move || {
                if !alive.load(Ordering::SeqCst) {
                    return Err(io::Error::other("native owner retired"));
                }
                let (intent, manifest) = task_binding(&profile, &profile_id, &intent, &parent)?;
                let result = super::Broker::retained_task_receipt(
                    &profile,
                    &intent.scope()?,
                    &manifest,
                    &parent,
                    &command,
                )?;
                task_result(&intent, result)
            })
            .await
            .map_err(|_| io::Error::other("native task read failed"))?;
        }
        Call::TaskEvents { intent, parent } => {
            return tokio::task::spawn_blocking(move || {
                if !alive.load(Ordering::SeqCst) {
                    return Err(io::Error::other("native owner retired"));
                }
                let (intent, manifest) = task_binding(&profile, &profile_id, &intent, &parent)?;
                let receipts = super::workspace_receipts::Receipts::open(
                    &profile,
                    &intent.scope()?,
                    &manifest,
                    &parent,
                )?;
                Ok(json!({"intent_digest":intent.digest(),"events":receipts.events()?}))
            })
            .await
            .map_err(|_| io::Error::other("native journal read failed"))?;
        }
        Call::Completed => {
            if !alive.load(Ordering::SeqCst) {
                return Err(io::Error::other("native owner retired"));
            }
            let items: Vec<Value> = executions
                .lock()
                .map_err(|_| io::Error::other("native registry unavailable"))?
                .completed()
                .into_iter()
                .map(|(intent, reference)| json!({"intent":intent,"reference":reference}))
                .collect();
            return Ok(json!({"items":items}));
        }
        Call::Acknowledge { intent, reference } => {
            let intent = LaunchIntent::from_go(&intent)?;
            let binding = ExecutionBinding::from_intent(&intent)?;
            if !alive.load(Ordering::SeqCst) {
                return Err(io::Error::other("native owner retired"));
            }
            match executions
                .lock()
                .map_err(|_| io::Error::other("native registry unavailable"))?
                .acknowledge(&binding, &reference)
            {
                Ok(()) => (),
                Err(error) if error.kind() == io::ErrorKind::NotFound => (),
                Err(error) => return Err(error),
            }
            return Ok(json!({"intent_digest":intent.digest(),"retained":false}));
        }
        Call::BeginJudgment { .. } | Call::InspectJudgment { .. } => {
            return Err(io::Error::other("native judgment owner required"))
        }
        Call::InspectMuseLogin => return Err(io::Error::other("native source owner required")),
        Call::InspectAccount { .. } => {
            return Err(io::Error::other("native account owner required"))
        }
        Call::RecoveryTarget | Call::Recover { .. } => {
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
                if intent.muse_login() {
                    let result = super::muse_receipt::retained(&profile, &intent)?
                        .filter(|result| result.root_retired)
                        .ok_or_else(|| io::Error::other("native CLI receipt/retirement missing"))?;
                    if !alive.load(Ordering::SeqCst) {
                        return Err(io::Error::other("native owner retired"));
                    }
                    return Ok(muse_observation(
                        &intent,
                        Some(&result.process),
                        Some(&result),
                        true,
                        false,
                    ));
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

fn task_binding(
    profile: &std::path::Path,
    profile_id: &str,
    raw: &str,
    parent: &Parent,
) -> io::Result<(LaunchIntent, Vec<u8>)> {
    let intent = LaunchIntent::from_go(raw)?;
    if !intent.muse_login()
        || !parent.valid()
        || parent.executable_sha256 != intent.tool_sha256()
        || intent.scope()?.profile != profile_id
        || super::read_intent(profile, &intent.scope()?)?.is_none_or(|saved| saved.raw() != raw)
    {
        return Err(io::Error::other("native task binding differs"));
    }
    let manifest = intent.retained_manifest(profile)?;
    if manifest.is_empty() {
        return Err(io::Error::other("native task manifest missing"));
    }
    Ok((intent, manifest))
}
fn task_result(intent: &LaunchIntent, mut reply: TaskReply) -> io::Result<Value> {
    use sha2::{Digest, Sha256};
    let receipt_digest: String = Sha256::digest(serde_json::to_vec(&reply)?)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    // The durable result owns candidate bytes and the complete event stream.
    // Product checkpoints consume their bounded projection; export/journal
    // operations read those facts separately without rerunning checks.
    reply.bundle.clear();
    reply.events.clear();
    Ok(json!({"intent_digest":intent.digest(),"receipt_digest":receipt_digest,"result":reply}))
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
    if intent.muse_login() {
        let complete = match &outcome {
            ExecutionOutcome::MuseComplete(result) => Some(result),
            _ => None,
        };
        let process = if let Some(result) = complete {
            Some(result.process.clone())
        } else {
            ticket.inspect_muse(Duration::from_secs(2)).ok().flatten()
        };
        let retired = complete.is_some_and(|result| result.root_retired)
            && attempt.phase == AttemptPhase::Retired;
        if !alive.load(Ordering::SeqCst) {
            return Err(refused());
        }
        return Ok(muse_observation(
            &intent,
            process.as_ref(),
            complete.map(AsRef::as_ref),
            retired,
            retired,
        ));
    }
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

fn muse_observation(
    intent: &LaunchIntent,
    process: Option<&soda_execution_protocol::MuseObservation>,
    complete: Option<&super::MuseRun>,
    sealed: bool,
    retired: bool,
) -> Value {
    let receipt=process.map(|process|json!({"worker_uid":0,"connector_uid":process.uid,"service":process.service,
        "invocation":process.invocation,"system_instance":process.system_instance,"start_identity":process.start_identity,
        "namespace_pid":0,"host_pid":process.pid,"started_unix_ms":process.started_unix_ms,"stopped_unix_ms":process.stopped_unix_ms,"exit_code":process.exit_code,"exit_signal":process.exit_signal}));
    let output = complete.and_then(|result| result.product_output.as_deref());
    json!({"intent_digest":intent.digest(),"receipt":receipt,
        "custody":{"state":"not_acquired","acquisition_id":"","connection":"","generation":"0","sealed":sealed},
        "live":process.and_then(|process|process.live),"retired":retired,
        "reason":complete.and_then(|result|result.process.reason.as_deref()),"candidate_state":complete.map(|result|
            if result.candidate.is_some() {"captured"} else if intent.has_tasks() {"dirty"} else {"unchanged"}),
        "candidate":complete.and_then(|result|result.candidate.as_deref()),"output":output,"output_complete":complete.is_some_and(|result|result.process.output_complete)&&output.is_some()})
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

/// Borrowed callback owners; the collaboration runtime retains and joins them.
pub(crate) struct Callbacks<'a> {
    pub channel: &'a crate::supervisor::NativeChannel,
    pub signer: &'a crate::mcp_signing::Signer,
    pub alive: &'a Arc<AtomicBool>,
    pub execution_error: &'a Mutex<Option<String>>,
    pub executions: &'a Arc<Mutex<Executions>>,
    pub judgments: &'a Mutex<super::judgment::Judgments>,
    pub tools: &'a std::sync::RwLock<Option<Arc<super::Tools>>>,
    pub preparations: &'a Arc<super::preparations::Preparations>,
    pub execution_accounts:
        &'a Arc<std::sync::RwLock<std::collections::BTreeMap<String, Arc<super::launch::Account>>>>,
    pub work: &'a Arc<super::work::Work>,
    pub credentials: &'a std::sync::RwLock<Option<Arc<crate::credentials::Credentials>>>,
}
impl Callbacks<'_> {
    pub(crate) async fn call(
        &self,
        call: Call,
    ) -> std::result::Result<Value, crate::github_provider::Failure> {
        use crate::github_provider::Failure;
        match call {
            Call::BeginJudgment { request } => {
                let tools = self
                    .tools
                    .read()
                    .map_err(|_| Failure::unavailable())?
                    .clone()
                    .ok_or_else(Failure::unavailable)?;
                let tool = tools.inspect().await.map_err(|_| Failure::unavailable())?;
                return self
                    .judgments
                    .lock()
                    .map_err(|_| Failure::unavailable())?
                    .begin(
                        self.channel
                            .socket
                            .parent()
                            .ok_or_else(Failure::unavailable)?,
                        &self.channel.profile,
                        request,
                        tool,
                        self.alive.clone(),
                    )
                    .map_err(|_| Failure::new(503, "native_judgment_unavailable"));
            }
            Call::InspectJudgment { id, digest, cancel } => {
                return self
                    .judgments
                    .lock()
                    .map_err(|_| Failure::unavailable())?
                    .inspect(
                        self.channel
                            .socket
                            .parent()
                            .ok_or_else(Failure::unavailable)?,
                        &id,
                        &digest,
                        cancel,
                    )
                    .map_err(|_| Failure::new(503, "native_judgment_unavailable"));
            }
            Call::InspectMuseLogin => {
                let connection = crate::factory_provider::muse_connection(&self.channel.profile);
                let tools = self
                    .tools
                    .read()
                    .map_err(|_| Failure::unavailable())?
                    .clone();
                let tool_status = tools
                    .as_ref()
                    .map(|tools| tools.status())
                    .transpose()
                    .map_err(|_| Failure::unavailable())?;
                let judgment_available = crate::factory_provider::muse_helper().is_ok()
                    && tool_status.as_ref().is_some_and(|status| {
                        status.state == "ready"
                            && status.checked_at_unix.is_some_and(|checked| {
                                std::time::SystemTime::now()
                                    .duration_since(std::time::UNIX_EPOCH)
                                    .is_ok_and(|now| {
                                        now.as_secs().saturating_sub(checked)
                                            < crate::factory_provider::Tools::REFRESH.as_secs()
                                    })
                            })
                    });
                let available = judgment_available
                    && self
                        .execution_error
                        .lock()
                        .is_ok_and(|error| error.is_none());
                return Ok(json!({"connection":connection,"label":"This Muse login",
                "native":{"profile":self.channel.profile,"authentication":"muse_login"},
                "execution_available":available,"judgment_available":judgment_available,"judgment_models":[crate::factory_provider::MODEL],"tool_status":tool_status}));
            }
            Call::InspectAccount { connection } => {
                let account = self
                    .execution_accounts
                    .read()
                    .map_err(|_| Failure::unavailable())?
                    .get(&connection)
                    .cloned()
                    .ok_or_else(Failure::unavailable)?;
                let observed = account
                    .observe(&self.channel.profile)
                    .await
                    .map_err(|_| Failure::unavailable())?;
                if !self.alive.load(Ordering::SeqCst) {
                    return Err(Failure::unavailable());
                }
                return Ok(observed);
            }
            Call::RecoveryTarget => {
                return self
                    .work
                    .recovery_target()
                    .map_err(|_| Failure::new(503, "native_work_recovery_unavailable"));
            }
            Call::Recover {
                intent,
                checkout_id,
            } => {
                let owner = self.work.clone();
                let preparations = self.preparations.clone();
                return tokio::task::spawn_blocking(move || {
                    owner.recover(&intent, &checkout_id, &preparations)
                })
                .await
                .map_err(|_| Failure::unavailable())?
                .map_err(|error| {
                    eprintln!("native work recovery: {error}");
                    Failure::new(503, "native_work_recovery_unavailable")
                });
            }
            Call::Launch {
                intent,
                manifest,
                prompt,
                capability,
                secret,
                scope_digest,
                tools,
                admission,
            } => {
                if self
                    .execution_error
                    .lock()
                    .map_or(true, |error| error.is_some())
                {
                    return Err(Failure::new(503, "native_execution_unavailable"));
                }
                if !self
                    .signer
                    .verify(&capability, &scope_digest, &secret)
                    .map_err(|_| Failure::unavailable())?
                {
                    return Err(Failure::invalid());
                }
                let credentials = self.credentials.read().map_err(|_| Failure::unavailable())?.clone();
                return crate::factory_provider::launch::launch(
                    self.channel.clone(),
                    self.alive.clone(),
                    self.executions.clone(),
                    self.preparations.clone(),
                    self.execution_accounts.clone(),
                    credentials,
                    crate::factory_provider::launch::Input {
                        intent,
                        manifest,
                        prompt,
                        capability,
                        secret,
                        tools,
                        admission,
                    },
                )
                .await
                .map_err(|error| {
                    eprintln!("native launch callback: {error}");
                    Failure::new(503, "native_launch_unavailable")
                });
            }
            call => {
                let retained = match &call {
                    Call::Inspect { intent } | Call::InspectRetained { intent } | Call::Cancel { intent } => {
                        Some(LaunchIntent::from_go(intent).map_err(|_| Failure::invalid())?)
                    }
                    _ => None,
                };
                let profile = self.channel.socket.parent().ok_or_else(Failure::unavailable)?.to_path_buf();
                let credentials = self.credentials.read().map_err(|_| Failure::unavailable())?.clone();
                if let Some(intent) = &retained {
                    if intent.controller().is_some() {
                        super::controller::recover(credentials.clone().ok_or_else(Failure::unavailable)?, &profile, intent).await.map_err(|_| Failure::new(503, "native_controller_retirement_unavailable"))?;
                    }
                }
                let tools = self
                    .tools
                    .read()
                    .map_err(|_| Failure::unavailable())?
                    .clone();
                let observed = crate::factory_provider::port::call(
                    self.executions.clone(),
                    self.alive.clone(),
                    tools,
                    profile.clone(),
                    self.channel.profile.clone(),
                    call,
                )
                .await
                .map_err(|error| {
                    eprintln!("native execution callback: {error}");
                    Failure::new(503, "native_execution_unavailable")
                })?;
                if let Some(intent) = &retained {
                    if intent.controller().is_some() {
                        super::controller::recover(credentials.ok_or_else(Failure::unavailable)?, &profile, intent).await.map_err(|_| Failure::new(503, "native_controller_retirement_unavailable"))?;
                    }
                }
                return Ok(observed);
            }
        }
    }
}
