//! One admitted call's send allowance. Nested readers inherit it; transport
//! owners receive the same counter before leaving the calling task.
use std::future::Future;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

struct State {
    attempts: u32,
    closed: bool,
    limit: u32,
    conflicts: u32,
    deadline: Instant,
}
pub(crate) struct Operation(Mutex<State>);
tokio::task_local! { static CURRENT: Arc<Operation>; }
impl Operation {
    pub(crate) fn new(limit: u32, duration: Duration) -> Arc<Self> {
        Arc::new(Self(Mutex::new(State {
            attempts: 0,
            closed: false,
            limit: limit.min(384),
            conflicts: 0,
            deadline: Instant::now() + duration,
        })))
    }
    pub(crate) fn current() -> Option<Arc<Self>> {
        CURRENT.try_with(Arc::clone).ok()
    }
    pub(crate) async fn scope<F: Future>(self: &Arc<Self>, work: F) -> F::Output {
        CURRENT.scope(self.clone(), work).await
    }
    pub(crate) async fn owned_scope<F: Future>(self: &Arc<Self>, work: F) -> F::Output {
        struct Close(Arc<Operation>);
        impl Drop for Close {
            fn drop(&mut self) {
                self.0.close();
            }
        }
        let _close = Close(self.clone());
        self.scope(work).await
    }
    pub(crate) fn restrict(&self, limit: u32, deadline: Instant) {
        if let Ok(mut state) = self.0.lock() {
            state.limit = state.limit.min(limit);
            state.deadline = state.deadline.min(deadline);
        }
    }
    pub(crate) fn deadline(&self) -> Option<Instant> {
        self.0.lock().ok().map(|state| state.deadline)
    }
    pub(crate) fn close(&self) -> u32 {
        self.0
            .lock()
            .map(|mut state| {
                state.closed = true;
                state.attempts
            })
            .unwrap_or(384)
    }
    pub(crate) fn can_finish(&self, sends: u32) -> bool {
        self.0.lock().is_ok_and(|state| {
            !state.closed
                && Instant::now() < state.deadline
                && sends <= state.limit.saturating_sub(state.attempts)
        })
    }
    pub(crate) fn debit(&self) -> bool {
        let Ok(mut state) = self.0.lock() else {
            return false;
        };
        if state.closed || Instant::now() >= state.deadline || state.attempts >= state.limit {
            return false;
        }
        state.attempts += 1;
        true
    }
    pub(crate) fn unsent(&self) {
        if let Ok(mut state) = self.0.lock() {
            state.attempts = state.attempts.saturating_sub(1);
        }
    }
    pub(crate) fn retry_conflict(&self) -> bool {
        let Ok(mut state) = self.0.lock() else {
            return false;
        };
        if state.closed
            || Instant::now() >= state.deadline
            || state.conflicts >= 4
            || state.attempts >= state.limit
        {
            return false;
        }
        state.conflicts += 1;
        true
    }
}
