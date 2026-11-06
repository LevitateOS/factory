//! Only sequenced process queries and one cancellation may follow the upload.
use soda_execution_protocol::{
    AgentObserved as Observed, CANCELLATION_MARKER, MAX_OBSERVATION_QUERIES, OBSERVATION_MARKER,
    OBSERVATION_REQUEST_BYTES,
};
use std::io::{self, Write};
use std::os::unix::net::UnixStream;
use std::time::{Duration, Instant};

#[derive(Default)]
pub struct Queries {
    bytes: [u8; OBSERVATION_REQUEST_BYTES],
    length: usize,
    partial: Option<Instant>,
    sequence: u64,
    pending: Option<u64>,
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
        let sequence = u64::from_le_bytes(self.bytes[1..].try_into().unwrap());
        if self.bytes[0] == CANCELLATION_MARKER && sequence == 0 && !self.cancelled {
            worker.write_all(&self.bytes)?;
            self.cancelled = true;
            self.length = 0;
            self.partial = None;
            return Ok(());
        }
        if self.cancelled
            || self.bytes[0] != OBSERVATION_MARKER
            || sequence != self.sequence + 1
            || sequence > MAX_OBSERVATION_QUERIES
            || self.pending.is_some()
        {
            return Err(io::Error::other("agent observation request invalid"));
        }
        worker.write_all(&self.bytes)?;
        self.sequence = sequence;
        self.pending = Some(sequence);
        self.length = 0;
        self.partial = None;
        Ok(())
    }

    pub fn accept(&mut self, bytes: &[u8]) -> io::Result<bool> {
        let value: serde_json::Value = serde_json::from_slice(bytes)?;
        if value["kind"] != "agent_observed" {
            return Ok(false);
        }
        let reply: Observed = serde_json::from_slice(bytes)?;
        if reply.kind != "agent_observed"
            || reply.version != 1
            || self.pending != Some(reply.sequence)
        {
            return Err(io::Error::other("agent observation reply invalid"));
        }
        let _live = reply.live;
        self.pending = None;
        Ok(true)
    }
}
