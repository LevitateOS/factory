//! Bounded lifecycle ownership, independent of product requests and SQL.
use super::{
    AgentCancellation, AgentObservation, AgentRun, AttemptObservation, AttemptPhase,
    CustodyObservation, CustodyState, LaunchIntent, ReceiptScope, RunExecution, RunOwner, RunView,
};
use std::collections::BTreeMap;
use std::io;
use std::path::PathBuf;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};
use std::thread::{self, JoinHandle};
use std::time::Duration;

const MAX_EXECUTIONS: usize = 8;

fn hex(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn refused(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::PermissionDenied, message)
}

/// Captured by trusted native composition from the admitted operation. These
/// IDs are not product admission and do not synthesize a canonical Go address.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ExecutionBinding {
    profile: String,
    instance: String,
    run: String,
    scope: String,
    connection: String,
    intent_digest: Option<String>,
}

impl ExecutionBinding {
    pub fn new(instance: &str, scope: &ReceiptScope<'_>, connection: &str) -> io::Result<Self> {
        if !hex(instance) || !hex(connection) {
            return Err(refused("native execution binding invalid"));
        }
        Ok(Self {
            profile: scope.profile.into(),
            instance: instance.into(),
            run: scope.run.into(),
            scope: scope.fingerprint(),
            connection: connection.into(),
            intent_digest: None,
        })
    }

    pub fn from_intent(intent: &LaunchIntent) -> io::Result<Self> {
        let mut binding = Self::new(intent.instance(), &intent.scope()?, intent.connection())?;
        binding.intent_digest = Some(intent.digest());
        Ok(binding)
    }
    pub fn run(&self) -> &str {
        &self.run
    }
    pub fn instance(&self) -> &str {
        &self.instance
    }
}

enum TicketState {
    Preparing,
    Ready(AttemptView),
    Uncertain,
}

#[derive(Clone)]
enum AttemptView {
    Worker(RunView),
    Muse(super::muse_attempt::MuseView),
}
impl AttemptView {
    fn observe(&self) -> AttemptObservation {
        match self {
            Self::Worker(view) => view.observe(),
            Self::Muse(view) => view.observe(),
        }
    }
}
pub(super) enum OwnedAttempt {
    Worker(RunExecution),
    Muse(super::muse_attempt::MuseAttempt),
}
impl OwnedAttempt {
    fn view(&self) -> AttemptView {
        match self {
            Self::Worker(run) => AttemptView::Worker(run.view()),
            Self::Muse(run) => AttemptView::Muse(run.view()),
        }
    }
    fn finish(self) -> ExecutionOutcome {
        match self {
            Self::Worker(run) => run
                .finish()
                .map(|run| ExecutionOutcome::Complete(Arc::new(run))),
            Self::Muse(run) => run
                .finish()
                .map(|run| ExecutionOutcome::MuseComplete(Arc::new(run))),
        }
        .unwrap_or(ExecutionOutcome::Uncertain)
    }
}

/// Requests retain only a view. Kernel queries never hold the registry while
/// the lifecycle owner cancels and joins its attempts.
#[derive(Clone)]
pub struct ExecutionTicket {
    binding: ExecutionBinding,
    state: Arc<Mutex<TicketState>>,
    active: Arc<AtomicBool>,
}

impl ExecutionTicket {
    pub fn binding(&self) -> &ExecutionBinding {
        &self.binding
    }
    fn view(&self) -> io::Result<AttemptView> {
        match &*self
            .state
            .lock()
            .map_err(|_| refused("native preparation state unavailable"))?
        {
            TicketState::Ready(view) => Ok(view.clone()),
            _ => Err(refused("native execution not observed")),
        }
    }
    pub fn observe(&self) -> AttemptObservation {
        match &*self.state.lock().unwrap_or_else(|error| error.into_inner()) {
            TicketState::Preparing => AttemptObservation {
                phase: AttemptPhase::Starting,
                started: None,
            },
            TicketState::Ready(view) => view.observe(),
            TicketState::Uncertain => AttemptObservation {
                phase: AttemptPhase::Uncertain,
                started: None,
            },
        }
    }
    pub fn custody(&self) -> io::Result<CustodyObservation> {
        match self.view()? {
            AttemptView::Worker(view) => view.custody().observe(),
            AttemptView::Muse(_) => Err(refused(
                "Muse owns its authentication; no credential acquisition",
            )),
        }
    }
    fn uncertain(&self) {
        *self.state.lock().unwrap_or_else(|error| error.into_inner()) = TicketState::Uncertain;
    }

    /// Check the captured owner's lifetime before and after process I/O.
    /// Cached phase and retained custody remain available as scoped evidence;
    /// consumers still apply their current product/context admission.
    pub fn inspect(&self, timeout: Duration) -> io::Result<AgentObservation> {
        if !self.active.load(Ordering::SeqCst) {
            return Err(refused("native execution owner retired"));
        }
        let result = match self.view()? {
            AttemptView::Worker(view) => view.inspect(timeout),
            AttemptView::Muse(_) => Err(refused(
                "native CLI process has a distinct observation contract",
            )),
        };
        if !self.active.load(Ordering::SeqCst) {
            return Err(refused("native execution owner retired"));
        }
        result
    }
    pub(super) fn inspect_muse(
        &self,
        timeout: Duration,
    ) -> io::Result<Option<soda_execution_protocol::MuseObservation>> {
        if !self.active.load(Ordering::SeqCst) {
            return Err(refused("native execution owner retired"));
        }
        let result = match self.view()? {
            AttemptView::Muse(view) => view.inspect(timeout),
            _ => Err(refused("not a CLI provider process")),
        };
        if !self.active.load(Ordering::SeqCst) {
            return Err(refused("native execution owner retired"));
        }
        result
    }
}

#[derive(Clone)]
pub enum ExecutionOutcome {
    Pending,
    Complete(Arc<AgentRun>),
    MuseComplete(Arc<super::muse_attempt::MuseRun>),
    Uncertain,
}

struct Entry {
    ticket: ExecutionTicket,
    preparation: Option<JoinHandle<io::Result<OwnedAttempt>>>,
    cancellation: AgentCancellation,
    execution: Option<OwnedAttempt>,
    outcome: ExecutionOutcome,
}

impl Entry {
    fn reap(&mut self, join: bool) {
        if self
            .preparation
            .as_ref()
            .is_some_and(|preparation| join || preparation.is_finished())
        {
            match self.preparation.take().unwrap().join() {
                Ok(Ok(execution)) => self.execution = Some(execution),
                _ => {
                    self.ticket.uncertain();
                    self.outcome = ExecutionOutcome::Uncertain;
                }
            }
        }
        let terminal = matches!(
            self.ticket.observe().phase,
            AttemptPhase::Retired | AttemptPhase::Uncertain
        );
        if (join || terminal) && self.execution.is_some() {
            self.outcome = self.execution.take().unwrap().finish();
        }
    }
}

pub struct RetirementReport {
    pub binding: ExecutionBinding,
    pub attempt: AttemptObservation,
    pub custody: io::Result<CustodyObservation>,
    pub outcome: ExecutionOutcome,
}
impl RetirementReport {
    pub(crate) fn settled(&self) -> bool {
        match &self.outcome {
            ExecutionOutcome::Complete(_) => {
                matches!(&self.custody,Ok(custody) if custody.sealed && custody.state==CustodyState::Released)
            }
            ExecutionOutcome::MuseComplete(result) => result.root_retired,
            _ => false,
        }
    }
}

/// One native profile/runtime lifetime. Capacity counts retained unacknowledged
/// entries too, bounding native result memory and fencing uncertain cleanup.
/// The host, rather than a request or browser, must retain this object.
pub struct Executions {
    profile: String,
    instance: String,
    capacity: usize,
    active: Arc<AtomicBool>,
    entries: BTreeMap<String, Entry>,
}

impl Executions {
    /// Native composition holds the profile's exclusive lifecycle lock and
    /// supplies its actual current instance. Constructing this owner grants no
    /// product authority; callers must admit each operation before its factory.
    pub fn new(profile: &str, instance: &str, capacity: usize) -> io::Result<Self> {
        if !hex(profile) || !hex(instance) || !(1..=MAX_EXECUTIONS).contains(&capacity) {
            return Err(refused("native execution owner configuration invalid"));
        }
        Ok(Self {
            profile: profile.into(),
            instance: instance.into(),
            capacity,
            active: Arc::new(AtomicBool::new(true)),
            entries: BTreeMap::new(),
        })
    }

    fn entry(&mut self, binding: &ExecutionBinding) -> io::Result<&mut Entry> {
        if binding.profile != self.profile || binding.instance != self.instance {
            return Err(refused("native execution lifetime differs"));
        }
        let entry = self
            .entries
            .get_mut(&binding.run)
            .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "native execution not owned"))?;
        if &entry.ticket.binding != binding {
            return Err(refused("native execution scope differs"));
        }
        Ok(entry)
    }

    /// Retain one already admitted native operation. After scope/capacity
    /// checks, return a pending ticket and retain the preparation thread.
    /// The trusted factory must use the supplied signal with
    /// `RunOwner::start_cancellable` (or its MCP counterpart), and capture its
    /// policy/deadline at admission rather than extending it inside the thread.
    /// Its own work before that constructor must also observe cancellation.
    /// An exact binding replay returns its existing view without another launch
    /// or extending its original deadline. Product admission stays in Go.
    #[allow(clippy::too_many_arguments)]
    pub fn ensure(
        &mut self,
        binding: ExecutionBinding,
        create: impl FnOnce(AgentCancellation) -> io::Result<RunOwner> + Send + 'static,
        prompt: Vec<u8>,
        executable: PathBuf,
        duration: Duration,
    ) -> io::Result<ExecutionTicket> {
        let captured = binding.clone();
        self.ensure_attempt(binding, move |cancel| {
            let owner = create(cancel.clone())?;
            let custody = owner.custody();
            let observed = custody.observe()?;
            if owner.intent_digest() != captured.intent_digest.as_deref()
                || !owner.uses_cancellation(&cancel)
                || !custody.matches(
                    &captured.run,
                    &captured.profile,
                    &captured.scope,
                    &captured.connection,
                )
                || observed.state != CustodyState::Held
                || observed.sealed
                || cancel.cancelled()
            {
                return Err(refused("native prepared owner differs or retired"));
            }
            owner
                .spawn(prompt, executable, duration)
                .map(OwnedAttempt::Worker)
        })
    }

    pub(super) fn ensure_attempt(
        &mut self,
        binding: ExecutionBinding,
        create: impl FnOnce(AgentCancellation) -> io::Result<OwnedAttempt> + Send + 'static,
    ) -> io::Result<ExecutionTicket> {
        if !self.active.load(Ordering::SeqCst) {
            return Err(refused("native execution owner retired"));
        }
        if binding.profile != self.profile || binding.instance != self.instance {
            return Err(refused("native execution lifetime differs"));
        }
        if self.entries.contains_key(&binding.run) {
            return self.ticket(&binding);
        }
        if self.entries.len() >= self.capacity {
            return Err(io::Error::new(
                io::ErrorKind::WouldBlock,
                "native execution capacity retained",
            ));
        }
        let cancellation = AgentCancellation::default();
        let ticket = ExecutionTicket {
            binding: binding.clone(),
            state: Arc::new(Mutex::new(TicketState::Preparing)),
            active: self.active.clone(),
        };
        let prepared_ticket = ticket.clone();
        let cancel = cancellation.clone();
        let preparation = thread::Builder::new()
            .name("factory-native-preparation".into())
            .spawn(move || {
                let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    if cancel.cancelled() {
                        return Err(refused("native preparation cancelled"));
                    }
                    let execution = create(cancel.clone())?;
                    *prepared_ticket
                        .state
                        .lock()
                        .unwrap_or_else(|error| error.into_inner()) =
                        TicketState::Ready(execution.view());
                    Ok(execution)
                }))
                .unwrap_or_else(|_| Err(io::Error::other("native preparation failed")));
                if result.is_err() {
                    prepared_ticket.uncertain();
                }
                result
            });
        let (preparation, outcome, failure) = match preparation {
            Ok(preparation) => (Some(preparation), ExecutionOutcome::Pending, None),
            Err(error) => {
                ticket.uncertain();
                (None, ExecutionOutcome::Uncertain, Some(error))
            }
        };
        self.entries.insert(
            binding.run,
            Entry {
                ticket: ticket.clone(),
                preparation,
                cancellation,
                execution: None,
                outcome,
            },
        );
        if let Some(error) = failure {
            return Err(error);
        }
        Ok(ticket)
    }

    pub fn ticket(&mut self, binding: &ExecutionBinding) -> io::Result<ExecutionTicket> {
        let entry = self.entry(binding)?;
        entry.reap(false);
        Ok(entry.ticket.clone())
    }

    pub fn outcome(&mut self, binding: &ExecutionBinding) -> io::Result<ExecutionOutcome> {
        let entry = self.entry(binding)?;
        entry.reap(false);
        Ok(entry.outcome.clone())
    }

    pub fn cancel(&mut self, binding: &ExecutionBinding) -> io::Result<()> {
        let entry = self.entry(binding)?;
        if entry.preparation.is_some() || entry.execution.is_some() {
            entry.cancellation.cancel();
        } else if matches!(entry.outcome, ExecutionOutcome::Uncertain) {
            return Err(refused(
                "native execution cancellation requires reconciliation",
            ));
        }
        Ok(())
    }

    /// Drop only the in-memory completed entry after its product owner records
    /// the result. Retained native evidence stays intact; uncertain entries
    /// require reconciliation and cannot silently free native capacity.
    pub fn acknowledge(&mut self, binding: &ExecutionBinding, acquisition: &str) -> io::Result<()> {
        let entry = self.entry(binding)?;
        entry.reap(false);
        let custody = entry.ticket.custody()?;
        if !matches!(entry.outcome, ExecutionOutcome::Complete(_))
            || !custody.sealed
            || custody.state != CustodyState::Released
            || custody.acquisition_id != acquisition
        {
            return Err(refused("native execution acknowledgement unconfirmed"));
        }
        self.entries.remove(&binding.run);
        Ok(())
    }

    /// Fence launches and queries, cancel every owned handle before joining one,
    /// then report all independent outcomes and custody facts. Failure of one
    /// run never skips retirement of the others.
    pub fn retire(&mut self) -> Vec<RetirementReport> {
        self.active.store(false, Ordering::SeqCst);
        for entry in self.entries.values() {
            entry.cancellation.cancel();
        }
        self.entries
            .values_mut()
            .map(|entry| {
                entry.reap(true);
                RetirementReport {
                    binding: entry.ticket.binding.clone(),
                    attempt: entry.ticket.observe(),
                    custody: entry.ticket.custody(),
                    outcome: entry.outcome.clone(),
                }
            })
            .collect()
    }
}

impl Drop for Executions {
    fn drop(&mut self) {
        let _ = self.retire();
    }
}
