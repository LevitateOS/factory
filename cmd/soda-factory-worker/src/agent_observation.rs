//! Bounded queries on the one broker-owned execution connection.
use soda_execution_protocol::{
    AgentObserved, AgentOutput, CANCELLATION_MARKER, MAX_OBSERVATION_QUERIES, MAX_OUTPUT,
    MAX_OUTPUT_QUERIES, MAX_OUTPUT_READ, OBSERVATION_MARKER, OBSERVATION_REQUEST_BYTES,
    OUTPUT_MARKER,
};
use soda_native_process::Process;
use std::io::{self, Write};
use std::os::fd::AsRawFd;
use std::os::unix::net::UnixStream;
use std::time::{Duration, Instant};

#[derive(Default)]
pub struct Queries {
    bytes: [u8; OBSERVATION_REQUEST_BYTES],
    length: usize,
    partial: Option<Instant>,
    sequence: u64,
    reads: u64,
    cancelled: bool,
}

impl Queries {
    pub fn poll(&mut self, stream: &mut UnixStream, process: &Process) -> io::Result<bool> {
        if self
            .partial
            .is_some_and(|start| start.elapsed() >= Duration::from_secs(1))
        {
            return Err(io::Error::other("agent observation request incomplete"));
        }
        let received = unsafe {
            libc::recv(
                stream.as_raw_fd(),
                self.bytes[self.length..].as_mut_ptr().cast(),
                self.bytes.len() - self.length,
                libc::MSG_DONTWAIT,
            )
        };
        if received == 0 {
            return Ok(true);
        }
        if received < 0 {
            let error = io::Error::last_os_error();
            return if matches!(
                error.kind(),
                io::ErrorKind::WouldBlock | io::ErrorKind::Interrupted
            ) {
                Ok(false)
            } else {
                Err(error)
            };
        }
        self.partial.get_or_insert_with(Instant::now);
        self.length += received as usize;
        if self.length != self.bytes.len() {
            return Ok(false);
        }
        let sequence = u64::from_le_bytes(self.bytes[1..].try_into().unwrap());
        if self.bytes[0] == CANCELLATION_MARKER && sequence == 0 && !self.cancelled {
            self.cancelled = true;
            self.length = 0;
            self.partial = None;
            return Ok(true);
        }
        if self.bytes[0] == OUTPUT_MARKER {
            return self.output(stream, process, sequence).map(|()| false);
        }
        if self.cancelled
            || self.bytes[0] != OBSERVATION_MARKER
            || sequence != self.sequence + 1
            || sequence > MAX_OBSERVATION_QUERIES
        {
            return Err(io::Error::other("agent observation sequence invalid"));
        }
        self.sequence = sequence;
        self.length = 0;
        self.partial = None;
        let live = process
            .is_live()
            .map_err(|_| io::Error::other("agent process observation unavailable"))?;
        let reply = serde_json::to_vec(&serde_json::to_value(AgentObserved {
            kind: "agent_observed".into(),
            version: 1,
            sequence,
            live,
        })?)?;
        stream.write_all(&(reply.len() as u32).to_le_bytes())?;
        stream.write_all(&reply)?;
        Ok(false)
    }
}

impl Queries {
    // The frame's eight bytes are an offset here. A read never changes the
    // process; only its own budget and the captured bytes bound it.
    fn output(&mut self, stream: &mut UnixStream, process: &Process, offset: u64) -> io::Result<()> {
        if self.cancelled || self.reads >= MAX_OUTPUT_QUERIES || offset > MAX_OUTPUT as u64 {
            return Err(io::Error::other("agent output request invalid"));
        }
        self.reads += 1;
        self.length = 0;
        self.partial = None;
        let (bytes, total) = process.read_output(offset as usize, MAX_OUTPUT_READ);
        let live = process
            .is_live()
            .map_err(|_| io::Error::other("agent process observation unavailable"))?;
        let reply = serde_json::to_vec(&AgentOutput {
            kind: "agent_output".into(),
            version: 1,
            offset: offset.min(total as u64),
            length: bytes.len(),
            total: total as u64,
            live,
        })?;
        stream.write_all(&(reply.len() as u32).to_le_bytes())?;
        stream.write_all(&reply)?;
        stream.write_all(&bytes)
    }
}
