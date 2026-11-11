//! One fixed CLI-owned login. Only its public browser handoff crosses the UI.
use super::ToolSelection;
use serde::Serialize;
use std::io::{self, Read};
use std::os::fd::AsRawFd;
use std::os::unix::process::CommandExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::sync::{Arc, Mutex, atomic::{AtomicBool, Ordering}};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

#[derive(Clone, Serialize, PartialEq, Eq)]
pub struct Prompt {
    pub user_code: String,
    pub verification_uri: String,
    pub expires_at: u64,
}
#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum MusePhase { Idle, Requesting, Waiting, Complete, Cancelled, Failed }
impl MusePhase {
    pub fn active(self) -> bool { matches!(self, Self::Requesting | Self::Waiting) }
}
#[derive(Clone, Serialize)]
pub struct MuseLoginView {
    pub phase: MusePhase,
    pub prompt: Option<Prompt>,
    pub error: Option<&'static str>,
}
impl Default for MuseLoginView {
    fn default() -> Self { Self { phase: MusePhase::Idle, prompt: None, error: None } }
}
pub struct MuseLogin {
    view: Arc<Mutex<MuseLoginView>>,
    cancelled: Arc<AtomicBool>,
    thread: Option<JoinHandle<()>>,
}
fn unavailable() -> io::Error { io::Error::other("Muse sign-in is unavailable") }
fn code(value: &str) -> bool {
    value.len() == 9 && value.as_bytes()[4] == b'-'
        && value.bytes().enumerate().all(|(index, b)| index == 4 || b.is_ascii_uppercase() || b.is_ascii_digit())
}
fn prompt(bytes: &[u8], expires_at: u64) -> io::Result<Option<Prompt>> {
    let text = String::from_utf8_lossy(bytes);
    let words: Vec<_> = text.split_ascii_whitespace().collect();
    let urls: std::collections::BTreeSet<_> = words.iter().copied().filter(|word| word.starts_with("https://")).collect();
    if urls.is_empty() { return Ok(None); }
    if urls.len() != 1 { return Err(unavailable()); }
    let uri = *urls.first().ok_or_else(unavailable)?;
    let uri = url::Url::parse(uri).map_err(|_| unavailable())?;
    let query: Vec<_> = uri.query_pairs().collect();
    if uri.scheme() != "https" || uri.host_str() != Some("auth.meta.com") || uri.port().is_some()
        || !uri.username().is_empty() || uri.password().is_some() || uri.fragment().is_some()
        || uri.path() != "/oauth/device/" || query.len() != 1 || query[0].0 != "code" || !code(&query[0].1)
    { return Err(unavailable()); }
    let codes: Vec<_> = words.iter().copied().filter(|word| code(word)).collect();
    if codes.is_empty() { return Ok(None); }
    if codes.len() != 1 || codes[0] != query[0].1 { return Err(unavailable()); }
    Ok(Some(Prompt { user_code: codes[0].into(), verification_uri: uri.into(), expires_at }))
}
fn drain(input: &mut impl Read, bytes: &mut Vec<u8>) -> io::Result<()> {
    let mut buffer = [0u8; 1024];
    loop {
        match input.read(&mut buffer) {
            Ok(0) => return Ok(()),
            Ok(count) => {
                if bytes.len() + count > 16384 { return Err(unavailable()); }
                bytes.extend_from_slice(&buffer[..count]);
            }
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => return Ok(()),
            Err(error) if error.kind() == io::ErrorKind::Interrupted => (),
            Err(error) => return Err(error),
        }
    }
}
impl MuseLogin {
    pub fn available() -> bool { super::muse_helper().is_ok() }
    pub fn start(profile: &Path, selection: ToolSelection) -> io::Result<Self> {
        crate::private_fs::private_dir(profile).map_err(|_| unavailable())?;
        let helper = super::muse_helper()?;
        let profile = profile.to_path_buf();
        let view = Arc::new(Mutex::new(MuseLoginView { phase: MusePhase::Requesting, ..Default::default() }));
        let cancelled = Arc::new(AtomicBool::new(false));
        let state = view.clone();
        let stop = cancelled.clone();
        let thread = thread::Builder::new().name("factory-muse-login".into()).spawn(move || {
            let outcome = (|| -> io::Result<MusePhase> {
                let mut command = Command::new(helper);
                command.arg("--login").arg(selection.tool.executable).arg(selection.tool.sha256)
                    .current_dir(profile).env_clear().env("PATH", "/usr/bin:/bin")
                    .stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).process_group(0);
                for name in super::muse::USER_ENV {
                    if let Some(value) = std::env::var_os(name) { command.env(name, value); }
                }
                crate::child_process::parent_death(&mut command, libc::SIGKILL);
                let mut child = command.spawn()?;
                let mut exited = false;
                let result = (|| {
                    let mut output = child.stdout.take().ok_or_else(unavailable)?;
                    let mut errors = child.stderr.take().ok_or_else(unavailable)?;
                    for fd in [output.as_raw_fd(), errors.as_raw_fd()] {
                        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
                        if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0 { return Err(unavailable()); }
                    }
                    let deadline = Instant::now() + Duration::from_secs(600);
                    let expires_at = SystemTime::now().duration_since(UNIX_EPOCH).map_err(|_| unavailable())?.as_secs() + 600;
                    let mut stdout = Vec::new();
                    let mut stderr = Vec::new();
                    loop {
                        drain(&mut output, &mut stdout)?;
                        drain(&mut errors, &mut stderr)?;
                        if stdout.len() + stderr.len() > 16384 { return Err(unavailable()); }
                        let mut bytes = Vec::new();
                        for stream in [&stdout, &stderr] {
                            if let Some(end) = stream.iter().rposition(|byte| *byte == b'\n') {
                                bytes.extend_from_slice(&stream[..=end]);
                            }
                        }
                        if let Some(prompt) = prompt(&bytes, expires_at)? {
                            let mut view = state.lock().map_err(|_| unavailable())?;
                            if view.prompt.as_ref().is_some_and(|previous| previous != &prompt) { return Err(unavailable()); }
                            view.phase = MusePhase::Waiting;
                            view.prompt = Some(prompt);
                        }
                        if let Some(status) = child.try_wait()? {
                            exited = true;
                            return if status.success() && state.lock().map_err(|_| unavailable())?.prompt.is_some() { Ok(MusePhase::Complete) } else { Err(unavailable()) };
                        }
                        if stop.load(Ordering::SeqCst) { return Ok(MusePhase::Cancelled); }
                        if Instant::now() >= deadline { return Err(unavailable()); }
                        thread::sleep(Duration::from_millis(20));
                    }
                })();
                if !exited { unsafe { libc::kill(-(child.id() as i32), libc::SIGKILL); } }
                let retirement = child.wait();
                let phase = result?;
                retirement?;
                Ok(phase)
            })();
            if let Ok(mut view) = state.lock() {
                view.prompt = None;
                match outcome {
                    Ok(phase) => { view.phase = phase; view.error = None; }
                    Err(_) => { view.phase = MusePhase::Failed; view.error = Some("Muse sign-in could not finish. Check your connection and try again."); }
                }
            }
        })?;
        Ok(Self { view, cancelled, thread: Some(thread) })
    }
    pub fn view(&self) -> MuseLoginView { self.view.lock().unwrap().clone() }
    pub fn cancel(&mut self) {
        self.cancelled.store(true, Ordering::SeqCst);
        if let Some(thread) = self.thread.take() { let _ = thread.join(); }
    }
}
impl Drop for MuseLogin {
    fn drop(&mut self) { self.cancel(); }
}
