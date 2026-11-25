//! What GitHub already told this session. A token's account never changes, a
//! repository keeps its id and an issue or pull keeps its ids, so each is
//! asked once. GitHub still decides every
//! real request: a refusal or move on a later request forgets what it
//! contradicts, and the next request asks again.
use super::{Failure, Identity};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::sync::Mutex;

type Key = [u8; 32];

#[derive(Default)]
pub(super) struct Known {
    accounts: Mutex<HashMap<Key, Identity>>,
    /// (token, repository id) → (provider repository, its snapshot).
    repositories: Mutex<HashMap<(Key, String), (Value, Value)>>,
    /// (token, repository id, number) → the subject's ids.
    subjects: Mutex<HashMap<(Key, String, u64), Subject>>,
}

fn key(token: &[u8]) -> Key {
    Sha256::digest(token).into()
}

impl Known {
    pub fn account(&self, token: &[u8]) -> Option<Identity> {
        self.accounts.lock().ok()?.get(&key(token)).cloned()
    }
    pub fn remember_account(&self, token: &[u8], identity: &Identity) {
        if let Ok(mut accounts) = self.accounts.lock() {
            accounts.insert(key(token), identity.clone());
        }
    }
    pub fn repository(&self, token: &[u8], id: &str) -> Option<(Value, Value)> {
        self.repositories.lock().ok()?.get(&(key(token), id.to_owned())).cloned()
    }
    pub fn remember_repository(&self, token: &[u8], id: &str, raw: &Value, snapshot: &Value) {
        if let Ok(mut repositories) = self.repositories.lock() {
            repositories.insert((key(token), id.to_owned()), (raw.clone(), snapshot.clone()));
        }
    }
    pub fn subject(&self, token: &[u8], repository: &str, number: u64) -> Option<Subject> {
        self.subjects.lock().ok()?.get(&(key(token), repository.to_owned(), number)).cloned()
    }
    pub fn remember_subject(&self, token: &[u8], repository: &str, number: u64, subject: Subject) {
        if let Ok(mut subjects) = self.subjects.lock() {
            subjects.insert((key(token), repository.to_owned(), number), subject);
        }
    }
    /// GitHub refused or moved a request for `path`: forget the account on a
    /// sign-in refusal (with its repositories), and the repository the path
    /// names on a hidden, forbidden or moved answer.
    pub fn refused(&self, token: &[u8], path: &str, failure: &Failure) {
        let token = key(token);
        if failure.status == 401 {
            if let Ok(mut accounts) = self.accounts.lock() {
                accounts.remove(&token);
            }
            if let Ok(mut repositories) = self.repositories.lock() {
                repositories.retain(|(owner, _), _| *owner != token);
            }
            return;
        }
        if !matches!(failure.code, "github_not_visible" | "github_forbidden" | "github_resource_moved") {
            return;
        }
        let Ok(mut repositories) = self.repositories.lock() else { return };
        if let Some(rest) = path.strip_prefix("/repositories/") {
            let id = rest.split('/').next().unwrap_or("");
            repositories.remove(&(token, id.to_owned()));
        } else if let Some(rest) = path.strip_prefix("/repos/") {
            let mut parts = rest.splitn(3, '/');
            let name = format!("{}/{}", parts.next().unwrap_or(""), parts.next().unwrap_or(""));
            repositories.retain(|(owner, _), (_, snapshot)| {
                *owner != token || !snapshot["name"].as_str().is_some_and(|known| known.eq_ignore_ascii_case(&name))
            });
        }
    }
}

/// An issue's or pull's ids, which never change.
#[derive(Clone)]
pub(super) struct Subject {
    pub id: String,
    pub node: String,
    pub pull: bool,
}
