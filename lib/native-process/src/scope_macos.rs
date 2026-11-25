//! macOS process tree ownership. There is no cgroup: the owner tracks the
//! launched session leader and every process that descends from it, shares its
//! process group or keeps its terminal as the controlling tty. Memory and
//! process ceilings are enforced by killing the whole tree, and the CPU ceiling
//! by a stop/continue duty cycle. Membership is recorded under the owner's
//! private storage so a crashed owner's survivors are retired on reopen.
use super::{id, Error, Limits, Result};
use std::collections::BTreeMap;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

const PROC_PGRP_ONLY: u32 = 2;
const PROC_TTY_ONLY: u32 = 3;
const PROC_PPID_ONLY: u32 = 6;
const PROC_UID_ONLY: u32 = 4;
const PROC_RUID_ONLY: u32 = 5;

pub(super) struct Root {
    records: PathBuf,
}
impl Root {
    /// `storage` is the owner's exclusively locked private directory.
    pub fn open(storage: &Path, namespace: &str) -> Result<Self> {
        if !id(namespace) {
            return Err(Error::Invalid);
        }
        let records = storage.join(format!("scopes-{namespace}"));
        super::storage::private_dir(&records).map_err(|_| Error::Storage)?;
        // Retire a prior owner's recorded members before any new launch. A
        // recorded PID counts only while its start time still matches.
        for entry in fs::read_dir(&records).map_err(|_| Error::Retirement)? {
            let entry = entry.map_err(|_| Error::Retirement)?;
            let name = entry.file_name();
            let Some(name) = name.to_str() else {
                return Err(Error::Retirement);
            };
            if name.starts_with(".pending-") {
                fs::remove_file(entry.path()).map_err(|_| Error::Retirement)?;
                continue;
            }
            if !id(name) {
                return Err(Error::Retirement);
            }
            let value = fs::read_to_string(entry.path()).map_err(|_| Error::Retirement)?;
            let mut members = BTreeMap::new();
            for line in value.lines() {
                let (pid, start) = line.split_once(' ').ok_or(Error::Retirement)?;
                let pid: i32 = pid.parse().map_err(|_| Error::Retirement)?;
                let start: u64 = start.parse().map_err(|_| Error::Retirement)?;
                if pid <= 1 {
                    return Err(Error::Retirement);
                }
                members.insert(pid, start);
            }
            let mut old = Scope::empty(entry.path(), None);
            old.members = members;
            old.kill()?;
            let deadline = Instant::now() + Duration::from_secs(3);
            while old.alive() {
                if Instant::now() >= deadline {
                    return Err(Error::Retirement);
                }
                std::thread::sleep(Duration::from_millis(10));
                old.kill()?;
            }
            old.members.clear();
            old.remove()?;
        }
        Ok(Self { records })
    }
    pub fn create(&self, invocation: &str, limits: &Limits) -> Result<Scope> {
        let path = self.records.join(invocation);
        if path.symlink_metadata().is_ok() {
            return Err(Error::Scope);
        }
        let scope = Scope::empty(path, Some(limits.clone()));
        scope.record()?;
        Ok(scope)
    }
}

#[derive(Clone, Copy)]
struct Info {
    parent: i32,
    start: u64,
    zombie: bool,
}
fn info(pid: i32) -> Option<Info> {
    let mut value = std::mem::MaybeUninit::<libc::proc_bsdinfo>::zeroed();
    let size = std::mem::size_of::<libc::proc_bsdinfo>() as i32;
    let read = unsafe {
        libc::proc_pidinfo(pid, libc::PROC_PIDTBSDINFO, 0, value.as_mut_ptr().cast(), size)
    };
    if read != size {
        return None;
    }
    let value = unsafe { value.assume_init() };
    Some(Info {
        parent: value.pbi_ppid as i32,
        start: value.pbi_start_tvsec * 1_000_000 + value.pbi_start_tvusec,
        zombie: value.pbi_status == libc::SZOMB,
    })
}
/// Exact start time of a live process, used as its kernel start identity.
pub(super) fn start_identity(pid: u32) -> Result<String> {
    info(pid as i32)
        .map(|info| info.start.to_string())
        .ok_or(Error::Spawn)
}
fn list(kind: u32, value: u32) -> Result<Vec<i32>> {
    let mut pids = vec![0i32; 256];
    loop {
        let bytes = (pids.len() * std::mem::size_of::<i32>()) as i32;
        let read = unsafe { libc::proc_listpids(kind, value, pids.as_mut_ptr().cast(), bytes) };
        if read < 0 {
            return Err(Error::Observation);
        }
        let count = read as usize / std::mem::size_of::<i32>();
        if count < pids.len() {
            pids.truncate(count);
            pids.retain(|pid| *pid > 0);
            return Ok(pids);
        }
        if pids.len() >= 1 << 16 {
            return Err(Error::Observation);
        }
        pids.resize(pids.len() * 2, 0);
    }
}
#[repr(C)]
struct Timebase {
    numer: u32,
    denom: u32,
}
extern "C" {
    fn mach_timebase_info(info: *mut Timebase) -> libc::c_int;
}
/// rusage CPU times are Mach absolute units; convert with the host timebase.
fn timebase() -> (u64, u64) {
    let mut base = Timebase { numer: 0, denom: 0 };
    if unsafe { mach_timebase_info(&mut base) } != 0 || base.denom == 0 {
        return (1, 1);
    }
    (base.numer as u64, base.denom as u64)
}

pub(super) struct Scope {
    record: PathBuf,
    limits: Option<Limits>,
    leader: Option<i32>,
    tty: Option<u32>,
    /// A dedicated pool user: every process with this real or effective UID is
    /// a member, whatever its ancestry.
    uid: Option<u32>,
    members: BTreeMap<i32, u64>,
    cpu: BTreeMap<i32, u64>,
    retired_cpu: u64,
    began: Instant,
    timebase: (u64, u64),
    stopped: bool,
    removed: bool,
}
impl Scope {
    fn empty(record: PathBuf, limits: Option<Limits>) -> Self {
        Self {
            record,
            limits,
            leader: None,
            tty: None,
            uid: None,
            members: BTreeMap::new(),
            cpu: BTreeMap::new(),
            retired_cpu: 0,
            began: Instant::now(),
            timebase: timebase(),
            stopped: false,
            removed: false,
        }
    }
    /// Track every process of a dedicated pool `uid`. The UID itself is the
    /// durable membership, so nothing is recorded.
    pub fn user(uid: u32, limits: Limits) -> Self {
        let mut scope = Self::empty(PathBuf::new(), Some(limits));
        scope.uid = Some(uid);
        scope
    }
    fn user_processes(&self) -> Result<Vec<i32>> {
        let Some(uid) = self.uid else { return Ok(Vec::new()) };
        let mut pids = list(PROC_UID_ONLY, uid)?;
        pids.extend(list(PROC_RUID_ONLY, uid)?);
        pids.sort_unstable();
        pids.dedup();
        Ok(pids)
    }
    fn record(&self) -> Result<()> {
        if self.uid.is_some() {
            return Ok(());
        }
        let directory = self.record.parent().ok_or(Error::Scope)?;
        let name = self.record.file_name().and_then(|name| name.to_str()).ok_or(Error::Scope)?;
        let pending = directory.join(format!(".pending-{name}"));
        let mut file = OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&pending)
            .map_err(|_| Error::Scope)?;
        let body: String = self
            .members
            .iter()
            .map(|(pid, start)| format!("{pid} {start}\n"))
            .collect();
        file.write_all(body.as_bytes())
            .and_then(|_| file.sync_all())
            .map_err(|_| Error::Scope)?;
        fs::rename(&pending, &self.record).map_err(|_| Error::Scope)
    }
    /// Admit the launched session leader before readiness is exposed.
    pub fn adopt(&mut self, pid: u32, tty: u32) -> Result<()> {
        let pid = pid as i32;
        let leader = info(pid).ok_or(Error::Spawn)?;
        self.leader = Some(pid);
        self.tty = Some(tty);
        self.members.insert(pid, leader.start);
        self.began = Instant::now();
        self.record()
    }
    fn alive(&self) -> bool {
        if self.uid.is_some() {
            return self.user_processes().map_or(true, |pids| {
                pids.iter().any(|pid| info(*pid).is_some_and(|info| !info.zombie))
            });
        }
        self.members
            .iter()
            .any(|(pid, start)| info(*pid).is_some_and(|info| info.start == *start && !info.zombie))
    }
    /// Re-establish membership from the kernel. A child counts only when its
    /// parent is a member that started no later than it did.
    fn refresh(&mut self) -> Result<()> {
        let before = self.members.len();
        let mut changed = false;
        self.members.retain(|pid, start| {
            let keep = info(*pid).is_some_and(|info| info.start == *start && !info.zombie);
            changed |= !keep;
            keep
        });
        let mut candidates = Vec::new();
        if let Some(leader) = self.leader {
            candidates.extend(list(PROC_PGRP_ONLY, leader as u32)?);
        }
        if let Some(tty) = self.tty {
            candidates.extend(list(PROC_TTY_ONLY, tty)?);
        }
        for pid in self.user_processes()? {
            if let Some(found) = info(pid).filter(|info| !info.zombie) {
                self.members.entry(pid).or_insert(found.start);
            }
        }
        let mut frontier: Vec<i32> = self.members.keys().copied().collect();
        while let Some(parent) = frontier.pop() {
            for child in list(PROC_PPID_ONLY, parent as u32)? {
                if self.members.contains_key(&child) {
                    continue;
                }
                let Some(found) = info(child) else { continue };
                let parent_start = self.members.get(&parent).copied().unwrap_or(u64::MAX);
                if found.zombie || found.parent != parent || found.start < parent_start {
                    continue;
                }
                self.members.insert(child, found.start);
                frontier.push(child);
            }
        }
        for candidate in candidates {
            if candidate <= 1 || self.members.contains_key(&candidate) {
                continue;
            }
            if let Some(found) = info(candidate).filter(|info| !info.zombie) {
                // Group and terminal members are recorded by exact identity;
                // their descendants join on the next pass by parent.
                if found.start >= self.members.values().copied().min().unwrap_or(0) {
                    self.members.insert(candidate, found.start);
                }
            }
        }
        if changed || self.members.len() != before {
            self.record()?;
        }
        Ok(())
    }
    fn signal(&self, signal: i32) {
        for (pid, start) in &self.members {
            if info(*pid).is_some_and(|info| info.start == *start) {
                unsafe { libc::kill(*pid, signal) };
            }
        }
    }
    /// Track the tree and apply native ceilings. Exceeding memory or process
    /// count kills the whole tree, matching a cgroup OOM group kill.
    pub fn enforce(&mut self) -> Result<()> {
        self.refresh()?;
        let Some(limits) = self.limits.clone() else { return Ok(()) };
        if self.members.len() > limits.processes as usize {
            return self.kill();
        }
        let mut memory = 0u64;
        let mut cpu = BTreeMap::new();
        for pid in self.members.keys() {
            let mut usage = std::mem::MaybeUninit::<libc::rusage_info_v2>::zeroed();
            let read = unsafe {
                libc::proc_pid_rusage(*pid, libc::RUSAGE_INFO_V2, usage.as_mut_ptr().cast())
            };
            if read != 0 {
                continue;
            }
            let usage = unsafe { usage.assume_init() };
            memory = memory.saturating_add(usage.ri_phys_footprint);
            cpu.insert(*pid, usage.ri_user_time.saturating_add(usage.ri_system_time));
        }
        for (pid, used) in &self.cpu {
            if !cpu.contains_key(pid) {
                self.retired_cpu = self.retired_cpu.saturating_add(*used);
            }
        }
        self.cpu = cpu;
        if memory > limits.memory_bytes {
            return self.kill();
        }
        let used = self.retired_cpu.saturating_add(self.cpu.values().sum::<u64>());
        let used = used as u128 * self.timebase.0 as u128 / self.timebase.1 as u128;
        let allowed = self.began.elapsed().as_nanos() * limits.cpu_percent as u128 / 100;
        if !self.stopped && used > allowed + 200_000_000 {
            self.signal(libc::SIGSTOP);
            self.stopped = true;
        } else if self.stopped && used <= allowed {
            self.signal(libc::SIGCONT);
            self.stopped = false;
        }
        Ok(())
    }
    pub fn kill(&mut self) -> Result<()> {
        self.refresh().ok();
        if let Some(leader) = self.leader {
            unsafe { libc::kill(-leader, libc::SIGKILL) };
        }
        self.signal(libc::SIGKILL);
        for pid in self.user_processes().unwrap_or_default() {
            if pid > 1 {
                unsafe { libc::kill(pid, libc::SIGKILL) };
            }
        }
        Ok(())
    }
    pub fn populated(&mut self) -> Result<bool> {
        self.refresh().map_err(|_| Error::Retirement)?;
        Ok(!self.members.is_empty())
    }
    pub fn remove(&mut self) -> Result<()> {
        if self.alive() {
            return Err(Error::Retirement);
        }
        match fs::remove_file(&self.record) {
            Ok(()) => (),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err(Error::Retirement),
        }
        self.removed = true;
        Ok(())
    }
}
impl Drop for Scope {
    fn drop(&mut self) {
        if !self.removed {
            let _ = self.kill();
        }
    }
}
