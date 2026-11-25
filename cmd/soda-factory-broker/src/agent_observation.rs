//! Only sequenced process queries, bounded output reads and one cancellation
//! may follow the upload. One request of either kind is in flight at a time.
use soda_execution_protocol::{
    AgentObserved as Observed, AgentOutput, CANCELLATION_MARKER, MAX_OBSERVATION_QUERIES,
    MAX_OUTPUT, MAX_OUTPUT_QUERIES, OBSERVATION_MARKER, OBSERVATION_REQUEST_BYTES, OUTPUT_MARKER,
};
use std::io::{self, Write};
use std::os::unix::net::UnixStream;
use std::time::{Duration, Instant};

#[derive(Clone, Copy, PartialEq, Eq)]
enum Pending {
    Observation(u64),
    Output(u64),
}

#[derive(Default)]
pub struct Queries {
    bytes: [u8; OBSERVATION_REQUEST_BYTES],
    length: usize,
    partial: Option<Instant>,
    sequence: u64,
    reads: u64,
    pending: Option<Pending>,
    cancelled: bool,
}

impl Queries {
    pub fn pump(&mut self, worker: &mut UnixStream) -> io::Result<()> {
        if self
            .partial
            .is_some_and(|start| start.elapsed() >= Duration::from_secs(1))
        {
            return Err(io::Error::other("agent observation request incomplete"));
        }
        let received = unsafe {
            libc::recv(
                0,
                self.bytes[self.length..].as_mut_ptr().cast(),
                self.bytes.len() - self.length,
                libc::MSG_DONTWAIT,
            )
        };
        if received == 0 {
            return Err(io::Error::other("agent caller left before retirement"));
        }
        if received < 0 {
            let error = io::Error::last_os_error();
            return if matches!(
                error.kind(),
                io::ErrorKind::WouldBlock | io::ErrorKind::Interrupted
            ) {
                Ok(())
            } else {
                Err(error)
            };
        }
        self.partial.get_or_insert_with(Instant::now);
        self.length += received as usize;
        if self.length != self.bytes.len() {
            return Ok(());
        }
        let value = u64::from_le_bytes(self.bytes[1..].try_into().unwrap());
        if self.bytes[0] == CANCELLATION_MARKER && value == 0 && !self.cancelled {
            worker.write_all(&self.bytes)?;
            self.cancelled = true;
            self.length = 0;
            self.partial = None;
            return Ok(());
        }
        let pending = match self.bytes[0] {
            OBSERVATION_MARKER
                if value == self.sequence + 1 && value <= MAX_OBSERVATION_QUERIES =>
            {
                Pending::Observation(value)
            }
            OUTPUT_MARKER if self.reads < MAX_OUTPUT_QUERIES && value <= MAX_OUTPUT as u64 => {
                Pending::Output(value)
            }
            _ => return Err(io::Error::other("agent observation request invalid")),
        };
        if self.cancelled || self.pending.is_some() {
            return Err(io::Error::other("agent observation request invalid"));
        }
        worker.write_all(&self.bytes)?;
        match pending {
            Pending::Observation(sequence) => self.sequence = sequence,
            Pending::Output(_) => self.reads += 1,
        }
        self.pending = Some(pending);
        self.length = 0;
        self.partial = None;
        Ok(())
    }

    /// `None` is not a query reply. Otherwise the reply is admitted and that
    /// many raw output bytes follow it on the worker stream.
    pub fn accept(&mut self, bytes: &[u8]) -> io::Result<Option<usize>> {
        let value: serde_json::Value = serde_json::from_slice(bytes)?;
        let trailing = match (value["kind"].as_str(), self.pending) {
            (Some("agent_observed"), Some(Pending::Observation(sequence))) => {
                let reply: Observed = serde_json::from_slice(bytes)?;
                if reply.kind != "agent_observed" || reply.version != 1 || reply.sequence != sequence {
                    return Err(io::Error::other("agent observation reply invalid"));
                }
                0
            }
            (Some("agent_output"), Some(Pending::Output(offset))) => {
                let reply: AgentOutput = serde_json::from_slice(bytes)?;
                if !reply.valid(offset) {
                    return Err(io::Error::other("agent output reply invalid"));
                }
                reply.length
            }
            (Some("agent_observed" | "agent_output"), _) => {
                return Err(io::Error::other("agent observation reply invalid"))
            }
            _ => return Ok(None),
        };
        self.pending = None;
        Ok(Some(trailing))
    }
}
