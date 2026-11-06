//! Single-flight current-process queries on the already bound execution stream.
use super::AgentStarted;
use soda_execution_protocol::{
    CANCELLATION_MARKER, MAX_OBSERVATION_QUERIES, OBSERVATION_MARKER, OBSERVATION_REQUEST_BYTES,
};
use std::io::{self, Write};
use std::os::unix::net::UnixStream;
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AgentObservation {
    pub started: AgentStarted,
    pub live: bool,
}

#[derive(Default)]
struct State {
    writer: Option<UnixStream>,
    started: Option<AgentStarted>,
    sequence: u64,
    pending: Option<(u64, Option<bool>)>,
    exited: bool,
    cancelling: bool,
}

#[derive(Default)]
struct Shared {
    state: Mutex<State>,
    changed: Condvar,
    querying: Mutex<()>,
}

/// Query only the process bound to this native execution. A timeout leaves its
/// lifetime unchanged. Missing, busy or lost observations refuse, rather than
/// turning cached start state into liveness or uncertain cleanup into exit.
#[derive(Clone, Default)]
pub struct AgentObserver {
    shared: Arc<Shared>,
}

impl AgentObserver {
    pub fn inspect(&self, timeout: Duration) -> io::Result<AgentObservation> {
        if timeout.is_zero() || timeout > Duration::from_secs(5) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "observation timeout invalid",
            ));
        }
        let _query = self.shared.querying.try_lock().map_err(|_| {
            io::Error::new(
                io::ErrorKind::WouldBlock,
                "native observation already pending",
            )
        })?;
        let deadline = Instant::now() + timeout;
        let mut state = self
            .shared
            .state
            .lock()
            .map_err(|_| io::Error::other("native observation failed"))?;
        let started = state.started.clone().ok_or_else(|| {
            io::Error::new(io::ErrorKind::NotConnected, "native process not started")
        })?;
        // A complete bound terminal receipt proves this exact process cannot
        // become live again. It does not imply owner/credential retirement.
        if state.exited {
            return Ok(AgentObservation {
                started,
                live: false,
            });
        }
        if state.cancelling {
            return Err(io::Error::new(
                io::ErrorKind::WouldBlock,
                "native cancellation awaits its terminal receipt",
            ));
        }
        if matches!(state.pending, Some((_, None))) {
            return Err(io::Error::new(
                io::ErrorKind::WouldBlock,
                "native observation reply pending",
            ));
        }
        if state.sequence >= MAX_OBSERVATION_QUERIES {
            return Err(io::Error::other("native observation limit reached"));
        }
        let sequence = state.sequence + 1;
        let writer = state.writer.as_mut().ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::NotConnected,
                "native observation unavailable",
            )
        })?;
        writer.set_write_timeout(Some(timeout.min(Duration::from_secs(1))))?;
        let mut request = [0u8; OBSERVATION_REQUEST_BYTES];
        request[0] = OBSERVATION_MARKER;
        request[1..].copy_from_slice(&sequence.to_le_bytes());
        writer.write_all(&request)?;
        state.sequence = sequence;
        state.pending = Some((sequence, None));
        loop {
            if state.exited {
                return Ok(AgentObservation {
                    started,
                    live: false,
                });
            }
            if let Some((actual, Some(live))) = state.pending {
                if actual != sequence {
                    return Err(io::Error::other("native observation sequence changed"));
                }
                state.pending = None;
                return Ok(AgentObservation { started, live });
            }
            if state.writer.is_none() {
                return Err(io::Error::new(
                    io::ErrorKind::NotConnected,
                    "native observation lost",
                ));
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "native observation timed out",
                ));
            }
            state = self
                .shared
                .changed
                .wait_timeout(state, remaining)
                .map_err(|_| io::Error::other("native observation failed"))?
                .0;
        }
    }

    // Serialize with observation writes so cancellation cannot split a frame.
    // The broker/worker still own actual termination and its bound receipt.
    pub(super) fn cancel(&self) -> io::Result<()> {
        let mut state = self
            .shared
            .state
            .lock()
            .map_err(|_| io::Error::other("native control unavailable"))?;
        if state.exited || state.cancelling {
            return Ok(());
        }
        let writer = state.writer.as_mut().ok_or_else(|| {
            io::Error::new(io::ErrorKind::NotConnected, "native process not started")
        })?;
        let mut request = [0u8; OBSERVATION_REQUEST_BYTES];
        request[0] = CANCELLATION_MARKER;
        writer.set_write_timeout(Some(Duration::from_secs(1)))?;
        writer.write_all(&request)?;
        state.cancelling = true;
        Ok(())
    }

    pub(super) fn attach(
        &self,
        stream: &UnixStream,
        started: &AgentStarted,
    ) -> io::Result<ObservationLease> {
        let mut state = self
            .shared
            .state
            .lock()
            .map_err(|_| io::Error::other("native observation failed"))?;
        if state.started.is_some() {
            return Err(io::Error::other("native observation already bound"));
        }
        state.writer = Some(stream.try_clone()?);
        state.started = Some(started.clone());
        Ok(ObservationLease(self.clone()))
    }
}

pub(super) struct ObservationLease(AgentObserver);

impl ObservationLease {
    pub(super) fn deliver(&self, sequence: u64, live: bool) -> io::Result<()> {
        let mut state = self
            .0
            .shared
            .state
            .lock()
            .map_err(|_| io::Error::other("native observation failed"))?;
        if state.pending != Some((sequence, None)) || state.writer.is_none() {
            return Err(io::Error::other("native observation reply differs"));
        }
        state.pending = Some((sequence, Some(live)));
        self.0.shared.changed.notify_all();
        Ok(())
    }

    pub(super) fn complete(&self) {
        let mut state = self
            .0
            .shared
            .state
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        state.exited = true;
        state.writer = None;
        self.0.shared.changed.notify_all();
    }
}

impl Drop for ObservationLease {
    fn drop(&mut self) {
        let mut state = self
            .0
            .shared
            .state
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        state.writer = None;
        state.pending = None;
        self.0.shared.changed.notify_all();
    }
}
