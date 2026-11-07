//! CLI-provider variant of the existing native attempt lifecycle. Go retains
//! product admission and settlement; this owner retains local effects only.
use super::{
    AgentCancellation, AttemptObservation, AttemptPhase, LaunchIntent, MuseConnector, WorkerMcp,
};
use soda_execution_protocol::{MuseInvocation, MuseObservation, PreparedSourceRef, MAX_OUTPUT};
use std::io;
use std::path::PathBuf;
use std::sync::{mpsc, Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

pub struct MuseRun {
    pub process: MuseObservation,
    pub output: Vec<u8>,
    pub product_output: Option<String>,
    pub root_retired: bool,
}

// Keep the bounded native transcript private. Product output is the CLI's
// final answer, not its lifecycle and tool-protocol event stream.
pub(super) fn final_text(output: &[u8]) -> Option<String> {
    let mut text = None;
    for line in std::str::from_utf8(output).ok()?.lines() {
        let Ok(event) = serde_json::from_str::<serde_json::Value>(line) else {
            continue;
        };
        if event["payload_type"] == "run.terminal.completed" {
            if text.is_some()
                || event["schema_version"] != 1
                || event["payload"]["terminal"] != "completed"
            {
                return None;
            }
            let value = event["payload"]["text"].as_str()?;
            if value.len() > 65536 {
                return None;
            }
            text = Some(value.into());
        } else if matches!(
            event["payload_type"].as_str(),
            Some("run.terminal.failed" | "run.terminal.cancelled")
        ) {
            return None;
        }
    }
    text
}
struct Query {
    until: Instant,
    reply: mpsc::SyncSender<io::Result<Option<MuseObservation>>>,
}
struct State {
    phase: AttemptPhase,
    process: Option<MuseObservation>,
}
#[derive(Clone)]
pub(super) struct MuseView {
    state: Arc<Mutex<State>>,
    query: mpsc::SyncSender<Query>,
}
impl MuseView {
    pub fn observe(&self) -> AttemptObservation {
        AttemptObservation {
            phase: self.state.lock().unwrap_or_else(|e| e.into_inner()).phase,
            started: None,
        }
    }
    pub fn inspect(&self, timeout: Duration) -> io::Result<Option<MuseObservation>> {
        if timeout.is_zero() || timeout > Duration::from_secs(5) {
            return Err(io::Error::other("Muse observation timeout invalid"));
        }
        let state = self
            .state
            .lock()
            .map_err(|_| io::Error::other("Muse observation unavailable"))?;
        if state.phase == AttemptPhase::Retired {
            return Ok(state.process.clone());
        }
        drop(state);
        let (reply, result) = mpsc::sync_channel(1);
        self.query
            .try_send(Query {
                until: Instant::now() + timeout,
                reply,
            })
            .map_err(|_| io::Error::other("Muse observation busy or unavailable"))?;
        result
            .recv_timeout(timeout)
            .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "Muse observation timed out"))?
    }
}
pub(super) struct MuseAttempt {
    view: MuseView,
    cancellation: AgentCancellation,
    thread: Option<JoinHandle<io::Result<MuseRun>>>,
}
impl MuseAttempt {
    pub fn start(
        profile: PathBuf,
        helper: PathBuf,
        intent: LaunchIntent,
        bundle: Vec<u8>,
        prompt: Vec<u8>,
        manifest: Vec<u8>,
        preparation: PreparedSourceRef,
        mcp: WorkerMcp,
        cancellation: AgentCancellation,
    ) -> io::Result<Self> {
        if !intent.muse_login() || cancellation.cancelled() {
            return Err(io::Error::other("Muse source unavailable or cancelled"));
        }
        let scope = intent.scope()?;
        let digest = |bytes: &[u8]| -> String {
            use sha2::Digest;
            sha2::Sha256::digest(bytes)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect()
        };
        if digest(&bundle) != scope.source_sha256
            || digest(&prompt) != scope.prompt_sha256()
            || mcp.run != scope.run
        {
            return Err(io::Error::other(
                "Muse material differs from captured admission",
            ));
        }
        let harness = intent.current_profile()?;
        let prompt = String::from_utf8(prompt).map_err(io::Error::other)?;
        let deadline = Instant::now() + intent.remaining()?;
        intent.persist_manifest(&profile, &manifest)?;
        let mut staged = super::run::Staged::source(
            &scope,
            &bundle,
            Some(&preparation),
            cancellation.clone(),
            deadline,
        )?;
        let state = Arc::new(Mutex::new(State {
            phase: AttemptPhase::Starting,
            process: None,
        }));
        let observed = state.clone();
        let cancel = cancellation.clone();
        let (query, queries) = mpsc::sync_channel::<Query>(4);
        let view = MuseView { state, query };
        let thread = thread::Builder::new()
            .name("factory-muse-attempt".into())
            .spawn(move || {
                let outcome = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    let mut mcp = super::mcp::McpOwner::start(mcp, deadline)?;
                    let remaining = intent
                        .remaining()?
                        .min(deadline.saturating_duration_since(Instant::now()));
                    if cancel.cancelled() || remaining.is_zero() {
                        return Err(io::Error::other("Muse admission expired or cancelled"));
                    }
                    let mut connector = MuseConnector::start(
                        &profile,
                        &helper,
                        MuseInvocation {
                            run: intent.run().into(),
                            service: String::new(),
                            executable: intent.executable().to_string_lossy().into_owned(),
                            executable_sha256: intent.tool_sha256().into(),
                            prompt,
                            harness,
                            duration_ms: remaining.as_millis() as u64,
                            deadline_unix_ms: 0,
                            max_model_steps: 16,
                            output_bytes: MAX_OUTPUT,
                            mcp: Some(mcp.configuration.clone()),
                        },
                    )?;
                    let mut cancelling = false;
                    loop {
                        if (cancel.cancelled() || Instant::now() >= deadline) && !cancelling {
                            connector.cancel()?;
                            cancelling = true;
                        }
                        while let Ok(query) = queries.try_recv() {
                            let remaining = query.until.saturating_duration_since(Instant::now());
                            let result = if remaining.is_zero() {
                                Err(io::Error::new(
                                    io::ErrorKind::TimedOut,
                                    "Muse query expired",
                                ))
                            } else {
                                connector.inspect(remaining.min(Duration::from_secs(2)))
                            };
                            let _ = query.reply.send(result);
                        }
                        if connector.terminal().is_some() {
                            break;
                        }
                        if let Some(process) = connector.observe(Duration::from_millis(20))? {
                            let mut state = observed.lock().unwrap_or_else(|e| e.into_inner());
                            state.phase = AttemptPhase::Executing;
                            state.process = Some(process);
                        }
                    }
                    let (process, output) = connector.finish()?;
                    super::muse_receipt::write(&profile, &intent, &process, &output)?;
                    mcp.stop()?;
                    staged.abort()?;
                    super::muse_receipt::mark_retired(&profile, &intent, &process)?;
                    Ok(MuseRun {
                        process,
                        product_output: final_text(&output),
                        output,
                        root_retired: true,
                    })
                }))
                .unwrap_or_else(|_| Err(io::Error::other("Muse attempt failed")));
                let mut state = observed.lock().unwrap_or_else(|e| e.into_inner());
                state.phase = if outcome.is_ok() {
                    AttemptPhase::Retired
                } else {
                    AttemptPhase::Uncertain
                };
                if let Ok(result) = &outcome {
                    state.process = Some(result.process.clone());
                }
                outcome
            })?;
        Ok(Self {
            view,
            cancellation,
            thread: Some(thread),
        })
    }
    pub fn view(&self) -> MuseView {
        self.view.clone()
    }
    pub fn finish(mut self) -> io::Result<MuseRun> {
        self.join()
    }
    fn join(&mut self) -> io::Result<MuseRun> {
        self.thread
            .take()
            .expect("owned Muse attempt")
            .join()
            .unwrap_or_else(|_| Err(io::Error::other("Muse attempt join failed")))
    }
}
impl Drop for MuseAttempt {
    fn drop(&mut self) {
        if self.thread.is_some() {
            self.cancellation.cancel();
            let _ = self.join();
        }
    }
}
