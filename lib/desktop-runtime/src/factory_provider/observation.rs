//! Single-flight current-process queries on the already bound execution stream.
use super::AgentStarted;
use soda_execution_protocol::{
    AgentOutput, CANCELLATION_MARKER, MAX_OBSERVATION_QUERIES, MAX_OUTPUT, MAX_OUTPUT_QUERIES,
    OBSERVATION_MARKER, OBSERVATION_REQUEST_BYTES, OUTPUT_MARKER,
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

/// Captured bytes from `offset`; `total` is what the process wrote so far.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AgentOutputRead {
    pub offset: u64,
    pub total: u64,
    pub bytes: Vec<u8>,
    pub live: bool,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Request {
    Observe(u64),
    Output(u64),
}

enum Reply {
    Observed(bool),
    Output(AgentOutputRead),
}

#[derive(Default)]
struct State {
    writer: Option<UnixStream>,
    started: Option<AgentStarted>,
    sequence: u64,
    reads: u64,
    pending: Option<(Request, Option<Reply>)>,
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
        match self.query(timeout, None)? {
            (started, None) => Ok(AgentObservation {
                started,
                live: false,
            }),
            (started, Some(Reply::Observed(live))) => Ok(AgentObservation { started, live }),
            (_, Some(Reply::Output(_))) => Err(io::Error::other("native observation reply differs")),
        }
    }

    /// Read captured output from `offset` with its own budget. After the
    /// terminal receipt this refuses: the receipt owns the complete output.
    pub fn output(&self, offset: u64, timeout: Duration) -> io::Result<AgentOutputRead> {
        if offset > MAX_OUTPUT as u64 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "native output offset invalid",
            ));
        }
        match self.query(timeout, Some(offset))? {
            (_, Some(Reply::Output(read))) => Ok(read),
            (_, None) => Err(io::Error::new(
                io::ErrorKind::NotConnected,
                "native output belongs to the terminal receipt",
            )),
            (_, Some(Reply::Observed(_))) => Err(io::Error::other("native output reply differs")),
        }
    }

    // `None` from a complete bound terminal receipt: this exact process cannot
    // become live again. It does not imply owner/credential retirement.
    fn query(
        &self,
        timeout: Duration,
        output: Option<u64>,
    ) -> io::Result<(AgentStarted, Option<Reply>)> {
        if timeout.is_zero() || timeout > Duration::from_secs(5) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "observation timeout invalid",
            ));
        }
        let deadline = Instant::now() + timeout;
        // Launch polling, first MCP admission and output reads may query
        // concurrently. Serialize fresh queries within the caller's deadline.
        let _query = loop {
            match self.shared.querying.try_lock() {
                Ok(query) => break query,
                Err(std::sync::TryLockError::Poisoned(_)) => {
                    return Err(io::Error::other("native observation failed"));
                }
                Err(std::sync::TryLockError::WouldBlock) => {
                    let remaining = deadline.saturating_duration_since(Instant::now());
                    if remaining.is_zero() {
                        return Err(io::Error::new(
                            io::ErrorKind::TimedOut,
                            "native observation busy",
                        ));
                    }
                    std::thread::sleep(remaining.min(Duration::from_millis(10)));
                }
            }
        };
        let mut state = self
            .shared
            .state
            .lock()
            .map_err(|_| io::Error::other("native observation failed"))?;
        let started = state.started.clone().ok_or_else(|| {
            io::Error::new(io::ErrorKind::NotConnected, "native process not started")
        })?;
        if state.exited {
            return Ok((started, None));
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
        let request = match output {
            None if state.sequence >= MAX_OBSERVATION_QUERIES => {
                return Err(io::Error::other("native observation limit reached"));
            }
            Some(_) if state.reads >= MAX_OUTPUT_QUERIES => {
                return Err(io::Error::other("native output read limit reached"));
            }
            None => Request::Observe(state.sequence + 1),
            Some(offset) => Request::Output(offset),
        };
        let writer = state.writer.as_mut().ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::NotConnected,
                "native observation unavailable",
            )
        })?;
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "native observation timed out",
            ));
        }
        writer.set_write_timeout(Some(remaining.min(Duration::from_secs(1))))?;
        let mut frame = [0u8; OBSERVATION_REQUEST_BYTES];
        let (marker, value) = match request {
            Request::Observe(sequence) => (OBSERVATION_MARKER, sequence),
            Request::Output(offset) => (OUTPUT_MARKER, offset),
        };
        frame[0] = marker;
        frame[1..].copy_from_slice(&value.to_le_bytes());
        writer.write_all(&frame)?;
        match request {
            Request::Observe(sequence) => state.sequence = sequence,
            Request::Output(_) => state.reads += 1,
        }
        state.pending = Some((request, None));
        loop {
            if state.exited {
                return Ok((started, None));
            }
            if matches!(&state.pending, Some((actual, Some(_))) if *actual == request) {
                let reply = state.pending.take().and_then(|(_, reply)| reply);
                return Ok((started, reply));
            }
            if state.pending.as_ref().is_none_or(|(actual, _)| *actual != request) {
                return Err(io::Error::other("native observation sequence changed"));
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
        self.reply(Request::Observe(sequence), Reply::Observed(live))
    }

    pub(super) fn deliver_output(&self, reply: &AgentOutput, bytes: Vec<u8>) -> io::Result<()> {
        let requested = match self.0.shared.state.lock().map(|state| state.pending.as_ref().map(|(request, reply)| (*request, reply.is_none()))) {
            Ok(Some((Request::Output(offset), true))) => offset,
            _ => return Err(io::Error::other("native output reply differs")),
        };
        if !reply.valid(requested) || bytes.len() != reply.length {
            return Err(io::Error::other("native output reply invalid"));
        }
        let read = AgentOutputRead { offset: reply.offset, total: reply.total, bytes, live: reply.live };
        self.reply(Request::Output(requested), Reply::Output(read))
    }

    fn reply(&self, request: Request, reply: Reply) -> io::Result<()> {
        let mut state = self
            .0
            .shared
            .state
            .lock()
            .map_err(|_| io::Error::other("native observation failed"))?;
        if !matches!(state.pending, Some((actual, None)) if actual == request) || state.writer.is_none() {
            return Err(io::Error::other("native observation reply differs"));
        }
        state.pending = Some((request, Some(reply)));
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
