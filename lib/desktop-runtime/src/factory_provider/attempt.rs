//! Native ownership of one admitted attempt, independent of a request's lifetime.
use super::{
    AgentCancellation, AgentObservation, AgentObserver, AgentRun, AgentStarted, RunCustody,
    RunOwner,
};
use std::io;
use std::path::PathBuf;
use std::sync::{Arc, Condvar, Mutex};
use std::thread::{self, JoinHandle};
use std::time::Duration;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AttemptPhase {
    Starting,
    /// The start was observed and no terminal result has arrived. This is not
    /// a fresh assertion that the model process is still live.
    Executing,
    /// The result was retained and local custody and broker retirement passed.
    Retired,
    /// No complete retained result and retirement proof can be returned.
    Uncertain,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AttemptObservation {
    pub phase: AttemptPhase,
    pub started: Option<AgentStarted>,
}

struct Shared {
    observation: Mutex<AttemptObservation>,
    changed: Condvar,
}

/// The native lifecycle owner retains this handle. Dropping it cancels and
/// joins its attempt; a request can inspect or wait without taking ownership.
/// These native observations never decide product work or activate authority.
pub struct RunExecution {
    shared: Arc<Shared>,
    cancellation: AgentCancellation,
    observer: AgentObserver,
    custody: RunCustody,
    thread: Option<JoinHandle<io::Result<AgentRun>>>,
}

impl RunExecution {
    pub fn custody(&self) -> RunCustody {
        self.custody.clone()
    }

    /// Query the exact isolated process now. This does not change attempt
    /// lifetime or turn an observation timeout into cancellation.
    pub fn inspect(&self, timeout: Duration) -> io::Result<AgentObservation> {
        self.observer.inspect(timeout)
    }

    pub fn observe(&self) -> AttemptObservation {
        self.shared
            .observation
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .clone()
    }

    /// A timeout leaves the attempt running under its original hard deadline.
    pub fn wait(&self, timeout: Duration) -> AttemptObservation {
        let observation = self
            .shared
            .observation
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        let (observation, _) = self
            .shared
            .changed
            .wait_timeout_while(observation, timeout, |observation| {
                matches!(
                    observation.phase,
                    AttemptPhase::Starting | AttemptPhase::Executing
                )
            })
            .unwrap_or_else(|error| error.into_inner());
        observation.clone()
    }

    /// Cancellation requests retirement; it does not confirm the result.
    pub fn cancel(&self) {
        self.cancellation.cancel();
    }

    /// Join the native owner and consume the one complete terminal result.
    pub fn finish(mut self) -> io::Result<AgentRun> {
        self.thread
            .take()
            .expect("owned native attempt thread")
            .join()
            .unwrap_or_else(|_| Err(io::Error::other("native attempt thread failed")))
    }
}

impl Drop for RunExecution {
    fn drop(&mut self) {
        if let Some(thread) = self.thread.take() {
            self.cancellation.cancel();
            let _ = thread.join();
        }
    }
}

impl RunOwner {
    /// Transfer this exact staged owner to a native thread. The caller keeps
    /// the returned handle until Quit, withdrawal or result reconciliation.
    pub fn spawn(
        self,
        prompt: Vec<u8>,
        executable: PathBuf,
        duration: Duration,
    ) -> io::Result<RunExecution> {
        let cancellation = self.cancellation();
        let observer = self.observer();
        let custody = self.custody();
        let shared = Arc::new(Shared {
            observation: Mutex::new(AttemptObservation {
                phase: AttemptPhase::Starting,
                started: None,
            }),
            changed: Condvar::new(),
        });
        let observed = shared.clone();
        let thread = thread::Builder::new()
            .name("factory-native-attempt".into())
            .spawn(move || {
                // Unwinding also drops the RunOwner before completion becomes
                // observable. A panic cannot leave a waiter stuck in Executing.
                let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    let mut owner = self;
                    owner.execute_observed(&prompt, &executable, duration, |started| {
                        let mut observation = observed
                            .observation
                            .lock()
                            .unwrap_or_else(|error| error.into_inner());
                        observation.started = Some(started.clone());
                        observation.phase = AttemptPhase::Executing;
                        observed.changed.notify_all();
                        Ok(())
                    })
                }))
                .unwrap_or_else(|_| Err(io::Error::other("native attempt failed")));
                let mut observation = observed
                    .observation
                    .lock()
                    .unwrap_or_else(|error| error.into_inner());
                observation.phase = if result.is_ok() {
                    AttemptPhase::Retired
                } else {
                    AttemptPhase::Uncertain
                };
                observed.changed.notify_all();
                result
            })?;
        Ok(RunExecution {
            shared,
            cancellation,
            observer,
            custody,
            thread: Some(thread),
        })
    }
}
