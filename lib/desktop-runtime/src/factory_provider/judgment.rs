//! Bounded tool-free inference through the existing Muse process owner. Product
//! selection, allowance and result adoption remain in Go's command journal.
use super::{MuseConnector, ToolSelection};
use crate::private_fs::{private_dir, publish_bytes, read_bytes};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use soda_execution_protocol::{HarnessProfile, MuseInvocation, MAX_MUSE_INPUT, MAX_PROMPT};
use std::collections::BTreeMap;
use std::io;
use std::path::Path;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};
use std::thread::{self, JoinHandle};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub id: String,
    pub digest: String,
    pub connection: String,
    pub tool_version: String,
    pub tool_sha256: String,
    pub prompt_sha256: String,
    pub harness: HarnessProfile,
    pub deadline_unix_ms: u64,
    pub prompt: String,
}
fn denied() -> io::Error {
    io::Error::other("native judgment unavailable or differs")
}
fn now() -> io::Result<u64> {
    Ok(SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(io::Error::other)?
        .as_millis() as u64)
}
fn valid_id(id: &str) -> bool {
    super::hex(id, 32)
}
fn transcript_limit(prompt: &str) -> io::Result<usize> {
    // Muse emits the supplied prompt in its input and lifecycle JSON events.
    // A large choice distribution also emits many small output-delta events;
    // their envelopes need room beyond the separately bounded 64 KiB answer.
    // The final answer remains bounded separately by final_text and Go's journal.
    Ok(2 * serde_json::to_vec(prompt)?.len() + 16 * 65536)
}
struct Job {
    cancel: Arc<AtomicBool>,
    thread: JoinHandle<io::Result<()>>,
}
#[derive(Default)]
pub struct Judgments {
    jobs: BTreeMap<String, Job>,
}
impl Judgments {
    pub fn begin(
        &mut self,
        profile: &Path,
        profile_id: &str,
        request: Request,
        tool: ToolSelection,
        alive: Arc<AtomicBool>,
    ) -> io::Result<Value> {
        let current = now()?;
        if !alive.load(Ordering::SeqCst)
            || !valid_id(&request.id)
            || !super::hex(&request.digest, 64)
            || request.connection != super::muse_connection(profile_id)
        {
            return Err(denied());
        }
        let directory = profile.join("native-judgments");
        private_dir(&directory).map_err(|_| denied())?;
        let binding = directory.join(format!("{}.request", request.id));
        match read_bytes(&binding, MAX_MUSE_INPUT) {
            Ok(bytes) => {
                if serde_json::from_slice::<Request>(&bytes)? != request {
                    return Err(denied());
                }
                return self.inspect(profile, &request.id, &request.digest, false);
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => (),
            Err(error) => return Err(error),
        }
        let prompt_sha256: String = Sha256::digest(request.prompt.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        if !alive.load(Ordering::SeqCst)
            || !valid_id(&request.id)
            || !super::hex(&request.digest, 64)
            || request.connection != super::muse_connection(profile_id)
            || request.tool_version != tool.tool.version
            || request.tool_sha256 != tool.tool.sha256
            || request.prompt_sha256 != prompt_sha256
            || !request.harness.valid()
            || request.deadline_unix_ms <= current
            || request.deadline_unix_ms > current.saturating_add(30_000)
            || request.prompt.is_empty()
            || request.prompt.len() > MAX_PROMPT
        {
            return Err(denied());
        }
        if self.jobs.len() >= 16 {
            return Err(denied());
        }
        // The immutable native binding fences another launch even after a crash.
        publish_bytes(&binding, &serde_json::to_vec(&request)?)?;
        let helper = super::muse_helper()?;
        let profile = profile.to_path_buf();
        let cancel = Arc::new(AtomicBool::new(false));
        let signal = cancel.clone();
        let id = request.id.clone();
        let thread = thread::Builder::new().name("factory-native-judgment".into()).spawn(move || {
            let mut connector = MuseConnector::start(&profile, &helper, MuseInvocation {
                run: request.id.clone(), service: String::new(),
                executable: tool.tool.executable.to_string_lossy().into_owned(),
                executable_sha256: request.tool_sha256.clone(), prompt: request.prompt.clone(), harness: request.harness.clone(),
                duration_ms: request.deadline_unix_ms.saturating_sub(now()?).min(30_000),
                deadline_unix_ms: request.deadline_unix_ms, max_model_steps: 1,
                output_bytes: transcript_limit(&request.prompt)?, mcp: None,
            })?;
            let mut cancelling = false;
            loop {
                if (!alive.load(Ordering::SeqCst) || signal.load(Ordering::SeqCst) || now()? >= request.deadline_unix_ms) && !cancelling {
                    connector.cancel()?;
                    cancelling = true;
                }
                if connector.terminal().is_some() { break; }
                let _ = connector.observe(Duration::from_millis(20))?;
            }
            let (process, output) = connector.finish_judgment()?;
            let text = process.output_complete.then(|| super::muse_attempt::final_text(&output)).flatten();
                publish_bytes(&directory.join(format!("{}.output", request.id)), &output)?;
            let result = json!({"id":request.id,"digest":request.digest,"process":process,"output":text});
            publish_bytes(&directory.join(format!("{}.result", request.id)), &serde_json::to_vec(&result)?)
        })?;
        self.jobs.insert(id, Job { cancel, thread });
        Ok(json!({"state":"pending"}))
    }
    pub fn inspect(
        &mut self,
        profile: &Path,
        id: &str,
        digest: &str,
        cancel: bool,
    ) -> io::Result<Value> {
        if !valid_id(id) || !super::hex(digest, 64) {
            return Err(denied());
        }
        let directory = profile.join("native-judgments");
        let request: Request =
            match read_bytes(&directory.join(format!("{id}.request")), MAX_MUSE_INPUT) {
                Ok(bytes) => serde_json::from_slice(&bytes)?,
                Err(error) if error.kind() == io::ErrorKind::NotFound => {
                    return Ok(json!({"state":"unknown"}))
                }
                Err(error) => return Err(error),
            };
        if request.id != id || request.digest != digest {
            return Err(denied());
        }
        if cancel {
            if let Some(job) = self.jobs.get(id) {
                job.cancel.store(true, Ordering::SeqCst);
            }
        }
        let bytes = match read_bytes(&directory.join(format!("{id}.result")), 512 * 1024) {
            Ok(bytes) => bytes,
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                if !self.jobs.contains_key(id) {
                    if let Some((process, output)) = super::muse_receipt::retained_process(
                        profile,
                        id,
                        &request.tool_sha256,
                        &request.prompt_sha256,
                        transcript_limit(&request.prompt)?,
                    )? {
                        super::muse_receipt::service_stopped(profile, &process)?;
                        let text = process
                            .output_complete
                            .then(|| super::muse_attempt::final_text(&output))
                            .flatten();
                        let result =
                            json!({"id":id,"digest":digest,"process":process,"output":text});
                        publish_bytes(&directory.join(format!("{id}.output")), &output)?;
                        publish_bytes(
                            &directory.join(format!("{id}.result")),
                            &serde_json::to_vec(&result)?,
                        )?;
                        return Ok(json!({"state":"complete","result":result}));
                    }
                }
                let state = if self
                    .jobs
                    .get(id)
                    .is_some_and(|job| !job.thread.is_finished())
                {
                    "pending"
                } else {
                    "unknown"
                };
                return Ok(json!({"state":state}));
            }
            Err(error) => return Err(error),
        };
        let result: Value = serde_json::from_slice(&bytes)?;
        if result["id"] != id || result["digest"] != digest {
            return Err(denied());
        }
        if let Some(job) = self.jobs.remove(id) {
            job.thread.join().map_err(|_| denied())??;
        }
        Ok(json!({"state":"complete","result":result}))
    }
    pub fn retire(&mut self) -> io::Result<()> {
        for job in self.jobs.values() {
            job.cancel.store(true, Ordering::SeqCst);
        }
        let mut failed = false;
        for (_, job) in std::mem::take(&mut self.jobs) {
            if !matches!(job.thread.join(), Ok(Ok(()))) {
                failed = true;
            }
        }
        if failed {
            Err(denied())
        } else {
            Ok(())
        }
    }
}
impl Drop for Judgments {
    fn drop(&mut self) {
        let _ = self.retire();
    }
}
