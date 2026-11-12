//! One profile's hard wire ceiling. Admission is durable before any HTTP send;
//! a missing/corrupt retained record never grants a replacement budget.
use crate::private_fs::{private_dir, private_file, read_bytes, write_bytes};
use serde::{Deserialize, Serialize};
use std::fs::{self, File};
use std::io::{self, Write};
use std::os::fd::AsRawFd;
use std::path::{Path, PathBuf};
use std::sync::{
    atomic::{AtomicBool, AtomicU64, Ordering},
    Mutex,
};

const CEILING: usize = 600;
const WINDOW: u64 = 3600;
const MAX_RECORD: usize = 24 * 1024;

#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct State {
    last: u64,
    attempts: Vec<u64>,
    provider_until: u64,
}
impl State {
    fn valid(&self) -> bool {
        self.attempts.len() <= CEILING
            && self.attempts.windows(2).all(|pair| pair[0] <= pair[1])
            && self.attempts.iter().all(|at| *at <= self.last)
            && self.provider_until <= self.last.saturating_add(86400)
    }
    // An interrupted replacement may conservatively retain an unsent debit or
    // a newly observed provider hold. It may never erase an earlier debit.
    fn extends(&self, old: &Self) -> bool {
        let prior: Vec<_> = old
            .attempts
            .iter()
            .copied()
            .filter(|at| at.saturating_add(WINDOW) > self.last)
            .collect();
        self.last >= old.last
            && self.attempts.starts_with(&prior)
            && (old.provider_until <= self.last || self.provider_until >= old.provider_until)
    }
}

#[derive(Clone, Serialize)]
pub(crate) struct Snapshot {
    pub available: bool,
    pub ceiling: usize,
    pub used: usize,
    pub reset: u64,
    pub suppressed: u64,
    pub provider_reset: u64,
}
pub(crate) struct Hold {
    pub reset: u64,
    pub local: bool,
}
pub(crate) struct Budget {
    profile: Option<PathBuf>,
    state: Mutex<State>,
    suppressed: AtomicU64,
    unavailable: AtomicBool,
}
impl Budget {
    pub(crate) fn memory() -> Self {
        Self {
            profile: None,
            state: Mutex::new(State::default()),
            suppressed: AtomicU64::new(0),
            unavailable: AtomicBool::new(false),
        }
    }
    pub(crate) fn retained(profile: &Path) -> io::Result<Self> {
        private_dir(profile).map_err(|error| io::Error::other(error.to_string()))?;
        let budget = Self {
            profile: Some(profile.into()),
            ..Self::memory()
        };
        budget.update(0, |_, _| Ok(((), false)))?;
        Ok(budget)
    }
    fn update<T>(
        &self,
        now: u64,
        change: impl FnOnce(&mut State, u64) -> io::Result<(T, bool)>,
    ) -> io::Result<T> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| io::Error::other("GitHub budget unavailable"))?;
        let mut lock = self
            .profile
            .as_ref()
            .map(|profile| {
                private_file(&profile.join("github-wire-budget.lock"), false)
                    .map_err(|error| io::Error::other(error.to_string()))
            })
            .transpose()?;
        if let Some(file) = &lock {
            // Never block the native owner's cancellation/join on another
            // controller holding this profile's budget lock.
            if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } != 0 {
                return Err(io::Error::last_os_error());
            }
        }
        let mut next = if let Some(profile) = &self.profile {
            load(
                profile,
                lock.as_ref()
                    .ok_or_else(|| io::Error::other("GitHub budget lock missing"))?,
            )?
        } else {
            state.clone()
        };
        if now != 0 && now < next.last {
            return Err(io::Error::other("GitHub budget clock moved backwards"));
        }
        let now = now.max(next.last);
        next.attempts.retain(|at| at.saturating_add(WINDOW) > now);
        if next.provider_until <= now {
            next.provider_until = 0;
        }
        let (result, changed) = change(&mut next, now)?;
        if changed {
            next.last = now;
            if let Some(profile) = &self.profile {
                save(profile, &next)?;
                let file = lock
                    .as_mut()
                    .ok_or_else(|| io::Error::other("GitHub budget lock missing"))?;
                if file.metadata()?.len() == 0 {
                    file.write_all(b"retained-v1\n")?;
                    file.sync_all()?;
                }
            }
        }
        *state = next;
        Ok(result)
    }
    pub(crate) fn admit(&self, now: u64, authenticated: bool) -> io::Result<Option<Hold>> {
        if self.unavailable.load(Ordering::SeqCst) {
            return Err(io::Error::other("GitHub budget unavailable"));
        }
        let held = self.update(now, |state, now| {
            let reset = if authenticated && state.provider_until > now {
                state.provider_until
            } else if state.attempts.len() >= CEILING {
                state.attempts[0].saturating_add(WINDOW)
            } else {
                0
            };
            if reset != 0 {
                return Ok((
                    Some(Hold {
                        reset,
                        local: !(authenticated && state.provider_until > now),
                    }),
                    false,
                ));
            }
            state.attempts.push(now);
            Ok((None, true))
        })?;
        if held.is_some() {
            self.suppressed.fetch_add(1, Ordering::Relaxed);
        }
        Ok(held)
    }
    pub(crate) fn fence(&self) {
        self.unavailable.store(true, Ordering::SeqCst);
    }
    pub(crate) fn observe(
        &self,
        now: u64,
        authenticated: bool,
        limit: Option<u64>,
        remaining: Option<u64>,
        reset: Option<u64>,
        retry_after: Option<u64>,
        rate_limited: bool,
    ) -> io::Result<()> {
        if !authenticated {
            return Ok(());
        }
        let reserve = remaining == Some(0)
            || matches!((limit, remaining), (Some(limit), Some(left)) if limit > 0 && left <= limit / 5);
        if !reserve && !rate_limited {
            return Ok(());
        }
        let result = self.update(now, |state, now| {
            let seconds = if reserve {
                reset.map(|until| until.saturating_sub(now)).or(retry_after)
            } else {
                retry_after.or_else(|| reset.map(|until| until.saturating_sub(now)))
            }
            .unwrap_or(60)
            .clamp(1, 86400);
            let until = now.saturating_add(seconds);
            let changed = state.provider_until < until;
            state.provider_until = state.provider_until.max(until);
            Ok(((), changed))
        });
        if result.is_err() {
            self.fence();
        }
        result
    }
    pub(crate) fn snapshot(&self, now: u64) -> io::Result<Snapshot> {
        self.update(now, |state, _| {
            Ok((
                Snapshot {
                    available: !self.unavailable.load(Ordering::SeqCst),
                    ceiling: CEILING,
                    used: state.attempts.len(),
                    reset: state
                        .attempts
                        .first()
                        .map(|at| at.saturating_add(WINDOW))
                        .unwrap_or(0),
                    suppressed: self.suppressed.load(Ordering::Relaxed),
                    provider_reset: state.provider_until,
                },
                false,
            ))
        })
    }
}

fn read(path: &Path) -> io::Result<Option<State>> {
    match read_bytes(path, MAX_RECORD) {
        Ok(bytes) => {
            let state: State = serde_json::from_slice(&bytes).map_err(io::Error::other)?;
            if !state.valid() {
                return Err(io::Error::other("GitHub budget record invalid"));
            }
            Ok(Some(state))
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error),
    }
}
fn load(profile: &Path, lock: &File) -> io::Result<State> {
    let path = profile.join("github-wire-budget.json");
    let current = read(&path)?;
    if let Some(pending) = read(&profile.join("github-wire-budget.json.pending"))? {
        if current.as_ref().is_some_and(|old| !pending.extends(old)) {
            return Err(io::Error::other("GitHub budget replacement differs"));
        }
        fs::rename(profile.join("github-wire-budget.json.pending"), &path)?;
        File::open(profile)?.sync_all()?;
        return Ok(pending);
    }
    match current {
        Some(state) => Ok(state),
        None if lock.metadata()?.len() == 0 => Ok(State::default()),
        None => Err(io::Error::other("GitHub retained budget missing")),
    }
}
fn save(profile: &Path, state: &State) -> io::Result<()> {
    let bytes = serde_json::to_vec(state).map_err(io::Error::other)?;
    let pending = profile.join("github-wire-budget.json.pending");
    write_bytes(&pending, &bytes)?;
    fs::rename(pending, profile.join("github-wire-budget.json"))?;
    File::open(profile)?.sync_all()
}
