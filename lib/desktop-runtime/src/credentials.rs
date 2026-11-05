//! Linux login-session credential custody. No browser token getter or file fallback.
use crate::database::{private_dir, private_file};
use secret_service::{EncryptionType, Item, SecretService};
use serde::Serialize;
use std::collections::{BTreeSet, HashMap};
use std::future::Future;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};
use tokio::sync::watch;
use zeroize::Zeroizing;
mod grant;
mod rotation;
use grant::Secret;
pub use grant::{Grant, GrantStatus};
use rotation::Reservation;

const APP: &str = "dev.soda.factory";
const DEADLINE: Duration = Duration::from_secs(5);
const LEASE: Duration = Duration::from_secs(30);
const OPERATION: Duration = Duration::from_secs(8);
const STREAM_RECHECK: Duration = Duration::from_secs(2);
const MAX_STREAM: Duration = Duration::from_secs(4 * 60 * 60);
type Result<T> = std::result::Result<T, Error>;
#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Error {
    Unavailable,
    Locked,
    Missing,
    Invalid,
    Ambiguous,
    Withdrawn,
    Retired,
    Storage,
    Deadline,
    Expired,
    RefreshPending,
}
impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(match self {
            Self::Unavailable => "desktop credential service unavailable",
            Self::Locked => "unlock your desktop keyring and retry",
            Self::Missing => "saved account is unavailable",
            Self::Invalid => "invalid native credential descriptor",
            Self::Ambiguous => "multiple saved credentials require reconciliation",
            Self::Withdrawn => "account credential withdrawn",
            Self::Retired => "native token capability retired",
            Self::Storage => "credential storage is unavailable",
            Self::Deadline => "native credential operation timed out",
            Self::Expired => "saved credential expired; refresh or sign in again",
            Self::RefreshPending => "credential refresh is unconfirmed; sign in again",
        })
    }
}
impl std::error::Error for Error {}
impl From<secret_service::Error> for Error {
    fn from(value: secret_service::Error) -> Self {
        match value {
            secret_service::Error::Locked => Self::Locked,
            secret_service::Error::NoResult => Self::Missing,
            _ => Self::Unavailable,
        }
    }
}
#[derive(Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Provider {
    Github,
    Fountain,
    Muse,
}
impl Provider {
    fn name(self) -> &'static str {
        match self {
            Self::Github => "github",
            Self::Fountain => "fountain",
            Self::Muse => "muse",
        }
    }
}
#[derive(Clone)]
pub struct Account {
    provider: Provider,
    origin: String,
    account: String,
    label: Option<String>,
}
impl Account {
    /// Only a native connector may supply a remotely verified account identity.
    pub fn new(provider: Provider, origin: &str, account: &str) -> Result<Self> {
        let parsed = url::Url::parse(origin).map_err(|_| Error::Invalid)?;
        if parsed.scheme() != "https"
            || parsed.host_str().is_none()
            || !parsed.username().is_empty()
            || parsed.password().is_some()
            || parsed.query().is_some()
            || parsed.fragment().is_some()
            || parsed.path() != "/"
            || origin.len() > 512
            || account.is_empty()
            || account.len() > 256
            || account.chars().any(char::is_control)
        {
            return Err(Error::Invalid);
        }
        let origin = parsed.origin().ascii_serialization();
        if (provider == Provider::Github && origin != "https://github.com")
            || (provider == Provider::Muse && origin != "https://api.meta.ai")
        {
            return Err(Error::Invalid);
        }
        Ok(Self {
            provider,
            origin,
            account: account.into(),
            label: None,
        })
    }
    /// A provider-observed display name, never a stable identity or authority.
    pub fn with_label(mut self, label: &str) -> Result<Self> {
        if label.is_empty() || label.len() > 256 || label.chars().any(char::is_control) {
            return Err(Error::Invalid);
        }
        self.label = Some(label.into());
        Ok(self)
    }
}
/// Intentionally has neither Serialize nor Debug. Consumed by native login code.
pub struct Token(Zeroizing<Vec<u8>>);
impl Token {
    pub fn new(value: Vec<u8>) -> Result<Self> {
        let value = Zeroizing::new(value);
        if value.is_empty() || value.len() > 8192 || !value.iter().all(|b| (33..=126).contains(b)) {
            return Err(Error::Invalid);
        }
        Ok(Self(value))
    }
    /// Crate-private borrowing for the native connector; never a browser getter.
    pub(crate) fn as_bytes(&self) -> &[u8] {
        &self.0
    }
}
#[derive(Clone, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum CredentialState {
    Stored,
    Expired,
    RefreshPending,
    Locked,
    Withdrawn,
}
#[derive(Clone, Serialize)]
pub struct AccountStatus {
    pub connection: String,
    pub provider: Provider,
    pub origin: String,
    pub account: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub label: Option<String>,
    pub state: CredentialState,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub grant: Option<GrantStatus>,
}
/// Rust-only, profile/owner/revision scoped, finite authority. Never serialized.
pub struct TokenCapability {
    connection: String,
    revision: String,
    owner: String,
    expires: Instant,
    account: AccountStatus,
}
impl TokenCapability {
    /// Captured non-secret scope; this is not remote account verification.
    pub fn account(&self) -> &AccountStatus {
        &self.account
    }
    fn admits(&self, account: &AccountStatus, revision: &str) -> bool {
        revision == self.revision
            && account.provider == self.account.provider
            && account.origin == self.account.origin
            && account.account == self.account.account
            && account.connection == self.account.connection
    }
}
pub struct Credentials {
    profile: String,
    markers: PathBuf,
    refreshes: PathBuf,
    mutation: PathBuf,
    owner: String,
    retired: AtomicBool,
    withdrawn: Mutex<BTreeSet<String>>,
    changes: watch::Sender<()>,
    gate: tokio::sync::Mutex<()>,
}
impl Credentials {
    pub(crate) fn is_profile(&self, profile: &str) -> bool {
        self.profile == profile
    }
    pub fn open(profile_dir: &Path, profile: &str) -> Result<Self> {
        if !id(profile) {
            return Err(Error::Invalid);
        }
        private_dir(profile_dir).map_err(|_| Error::Storage)?;
        let markers = profile_dir.join("withdrawn-credentials");
        private_dir(&markers).map_err(|_| Error::Storage)?;
        let refreshes = profile_dir.join("refreshing-credentials");
        private_dir(&refreshes).map_err(|_| Error::Storage)?;
        let (changes, _) = watch::channel(());
        Ok(Self {
            profile: profile.into(),
            markers,
            refreshes,
            mutation: profile_dir.join("credential-mutation.lock"),
            owner: random()?,
            retired: AtomicBool::new(false),
            withdrawn: Mutex::new(BTreeSet::new()),
            changes,
            gate: tokio::sync::Mutex::new(()),
        })
    }
    pub fn retire(&self) {
        self.retired.store(true, Ordering::SeqCst);
        self.changes.send_replace(());
    }
    fn alive(&self) -> Result<()> {
        if self.retired.load(Ordering::SeqCst) {
            Err(Error::Retired)
        } else {
            Ok(())
        }
    }
    async fn bounded<T>(&self, work: impl Future<Output = Result<T>>) -> Result<T> {
        self.alive()?;
        let result = tokio::time::timeout(DEADLINE, work)
            .await
            .map_err(|_| Error::Unavailable)?;
        self.alive()?;
        result
    }
    fn blocked(&self, connection: &str) -> Result<bool> {
        if !id(connection) {
            return Err(Error::Invalid);
        }
        if self.withdrawn.lock().unwrap().contains(connection) {
            return Ok(true);
        }
        let path = self.markers.join(connection);
        match std::fs::symlink_metadata(&path) {
            Ok(_) => {
                private_file(&path, false).map_err(|_| Error::Storage)?;
                Ok(true)
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
            Err(_) => Err(Error::Storage),
        }
    }
    fn block(&self, connection: &str) -> Result<()> {
        if !id(connection) {
            return Err(Error::Invalid);
        }
        self.withdrawn.lock().unwrap().insert(connection.into());
        self.changes.send_replace(());
        private_file(&self.markers.join(connection), false)
            .and_then(|file| Ok(file.sync_all()?))
            .map_err(|_| Error::Storage)?;
        self.sync_markers()
    }
    fn sync_markers(&self) -> Result<()> {
        std::fs::File::open(&self.markers)
            .and_then(|file| file.sync_all())
            .map_err(|_| Error::Storage)
    }
    fn unblock(&self, connection: &str) -> Result<()> {
        match std::fs::remove_file(self.markers.join(connection)) {
            Ok(()) => (),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
            Err(_) => return Err(Error::Storage),
        }
        self.sync_markers()?;
        self.withdrawn.lock().unwrap().remove(connection);
        Ok(())
    }
    async fn items<'a>(
        &self,
        service: &'a SecretService<'a>,
        connection: Option<&str>,
    ) -> Result<Vec<(Item<'a>, bool)>> {
        let mut attrs = HashMap::from([("application", APP), ("profile", self.profile.as_str())]);
        if let Some(connection) = connection {
            if !id(connection) {
                return Err(Error::Invalid);
            }
            attrs.insert("connection", connection);
        }
        let found = service.search_items(attrs).await?;
        if found.unlocked.len() + found.locked.len() > 32 {
            return Err(Error::Ambiguous);
        }
        Ok(found
            .unlocked
            .into_iter()
            .map(|i| (i, false))
            .chain(found.locked.into_iter().map(|i| (i, true)))
            .collect())
    }
    async fn metadata(&self, item: &Item<'_>, locked: bool) -> Result<(AccountStatus, String)> {
        let attrs = item.get_attributes().await?;
        let get = |key: &str| attrs.get(key).map(String::as_str).ok_or(Error::Invalid);
        if get("application")? != APP || get("profile")? != self.profile {
            return Err(Error::Invalid);
        }
        let provider = match get("provider")? {
            "github" => Provider::Github,
            "fountain" => Provider::Fountain,
            "muse" => Provider::Muse,
            _ => return Err(Error::Invalid),
        };
        let mut account = Account::new(provider, get("origin")?, get("account")?)?;
        if let Some(label) = attrs.get("label").filter(|label| !label.is_empty()) {
            account = account.with_label(label)?;
        }
        let connection = get("connection")?.to_owned();
        let revision = get("revision")?.to_owned();
        if !id(&connection) || !id(&revision) {
            return Err(Error::Invalid);
        }
        let state = if self.blocked(&connection)? {
            CredentialState::Withdrawn
        } else if locked {
            CredentialState::Locked
        } else if self.refresh_pending(&connection)? {
            CredentialState::RefreshPending
        } else {
            CredentialState::Stored
        };
        Ok((
            AccountStatus {
                connection,
                provider,
                origin: account.origin,
                account: account.account,
                label: account.label,
                state,
                grant: None,
            },
            revision,
        ))
    }
    pub async fn status(&self) -> Result<Vec<AccountStatus>> {
        self.bounded(async {
            let _guard = self.gate.lock().await;
            let service = SecretService::connect(EncryptionType::Dh).await?;
            let mut statuses = Vec::new();
            let mut seen = BTreeSet::new();
            for (item, locked) in self.items(&service, None).await? {
                let (mut status, revision) = self.metadata(&item, locked).await?;
                if matches!(status.state, CredentialState::Stored) {
                    let secret = Secret::load(&item, &revision).await?;
                    status.grant = secret.status()?;
                    match secret.access() {
                        Err(Error::Expired) => status.state = CredentialState::Expired,
                        Err(error) => return Err(error),
                        Ok(_) => (),
                    }
                }
                if !seen.insert(status.connection.clone()) {
                    return Err(Error::Ambiguous);
                }
                statuses.push(status);
            }
            statuses.sort_by(|a, b| a.connection.cmp(&b.connection));
            Ok(statuses)
        })
        .await
    }
    /// Check that the session has an unlocked default collection before a
    /// consent flow asks the user to approve a grant that must be saved.
    pub async fn storage_ready(&self) -> Result<()> {
        self.bounded(async {
            let service = SecretService::connect(EncryptionType::Dh).await?;
            let collection = service.get_default_collection().await?;
            if collection.is_locked().await? {
                return Err(Error::Locked);
            }
            Ok(())
        })
        .await
    }
    /// Login writes come from a native connector, never a webview command.
    pub async fn store(&self, account: Account, token: Token) -> Result<AccountStatus> {
        self.store_secret(account, Secret::Token(token)).await
    }
    /// Store the complete verified native grant, never just its access token.
    /// Identity verification remains the connector's prerequisite.
    pub async fn store_grant(&self, account: Account, grant: Grant) -> Result<AccountStatus> {
        self.store_secret(account, Secret::Grant(grant)).await
    }
    async fn store_secret(&self, account: Account, secret: Secret) -> Result<AccountStatus> {
        self.bounded(async {
            secret.validate()?;
            secret.access()?;
            let _mutation = self.mutation().await?;
            let _guard = self.gate.lock().await;
            let service = SecretService::connect(EncryptionType::Dh).await?;
            let mut existing = None;
            for (item, locked) in self.items(&service, None).await? {
                let (status, _) = self.metadata(&item, locked).await?;
                if status.provider == account.provider
                    && status.origin == account.origin
                    && status.account == account.account
                {
                    if locked {
                        return Err(Error::Locked);
                    }
                    if existing.replace((status.connection, item)).is_some() {
                        return Err(Error::Ambiguous);
                    }
                }
            }
            let (connection, existing) = match existing {
                Some((id, item)) => (id, Some(item)),
                None => (random()?, None),
            };
            self.persist_secret(&service, account, connection, existing, secret, None)
                .await
        })
        .await
    }
    // Caller holds the cross-owner mutation lease and this owner's keyring gate.
    async fn persist_secret(
        &self,
        service: &SecretService<'_>,
        account: Account,
        connection: String,
        existing: Option<Item<'_>>,
        secret: Secret,
        cap: Option<&TokenCapability>,
    ) -> Result<AccountStatus> {
        self.block(&connection)?;
        let revision = random()?;
        let bytes = secret.encode(&revision)?;
        let attrs = HashMap::from([
            ("application", APP),
            ("profile", self.profile.as_str()),
            ("connection", connection.as_str()),
            ("provider", account.provider.name()),
            ("origin", account.origin.as_str()),
            ("account", account.account.as_str()),
            ("revision", revision.as_str()),
            ("label", account.label.as_deref().unwrap_or("")),
            ("format", secret.format()),
        ]);
        if let Some(item) = existing {
            item.set_secret(&bytes, "application/octet-stream").await?;
            item.set_attributes(attrs).await?;
        } else {
            let collection = service.get_default_collection().await?;
            if collection.is_locked().await? {
                return Err(Error::Locked);
            }
            collection
                .create_item(
                    "Factory account",
                    attrs,
                    &bytes,
                    false,
                    "application/octet-stream",
                )
                .await?;
        }
        self.alive()?;
        secret.access()?;
        if cap.is_some_and(|cap| cap.expires <= Instant::now()) {
            return Err(Error::Retired);
        }
        self.clear_refresh(&connection)?;
        self.unblock(&connection)?;
        Ok(AccountStatus {
            connection,
            provider: account.provider,
            origin: account.origin,
            account: account.account,
            label: account.label,
            state: CredentialState::Stored,
            grant: secret.status()?,
        })
    }
    pub async fn capability(&self, connection: &str) -> Result<TokenCapability> {
        self.bounded(async {
            let _guard = self.gate.lock().await;
            if self.blocked(connection)? {
                return Err(Error::Withdrawn);
            }
            let service = SecretService::connect(EncryptionType::Dh).await?;
            let mut found = self.items(&service, Some(connection)).await?;
            if found.len() > 1 {
                return Err(Error::Ambiguous);
            }
            let (item, locked) = found.pop().ok_or(Error::Missing)?;
            let (account, revision) = self.metadata(&item, locked).await?;
            if locked {
                return Err(Error::Locked);
            }
            Ok(TokenCapability {
                connection: connection.into(),
                revision,
                owner: self.owner.clone(),
                expires: Instant::now() + LEASE,
                account,
            })
        })
        .await
    }
    /// Only trusted native code receives this borrowed token. It must apply its
    /// own finite operation deadline; the token is cleared when the callback ends.
    pub async fn with_token<T>(
        &self,
        cap: &TokenCapability,
        consume: impl FnOnce(&[u8]) -> T,
    ) -> Result<T> {
        self.with_secret(cap, false, consume).await
    }
    async fn with_secret<T>(
        &self,
        cap: &TokenCapability,
        refresh: bool,
        consume: impl FnOnce(&[u8]) -> T,
    ) -> Result<T> {
        self.with_secret_in(cap, refresh, None, consume).await
    }
    async fn with_secret_in<T>(
        &self,
        cap: &TokenCapability,
        refresh: bool,
        reservation: Option<&Reservation>,
        consume: impl FnOnce(&[u8]) -> T,
    ) -> Result<T> {
        self.bounded(async {
            let _guard = self.gate.lock().await;
            if cap.owner != self.owner || cap.expires <= Instant::now() {
                return Err(Error::Retired);
            }
            if self.blocked(&cap.connection)? {
                return Err(Error::Withdrawn);
            }
            self.check_refresh(&cap.connection, reservation)?;
            let service = SecretService::connect(EncryptionType::Dh).await?;
            let mut found = self.items(&service, Some(&cap.connection)).await?;
            if found.len() > 1 {
                return Err(Error::Ambiguous);
            }
            let (item, locked) = found.pop().ok_or(Error::Missing)?;
            let (account, revision) = self.metadata(&item, locked).await?;
            if locked {
                return Err(Error::Locked);
            }
            if !cap.admits(&account, &revision) {
                return Err(Error::Retired);
            }
            let secret = Secret::load(&item, &revision).await?;
            let (current, current_revision) = self.metadata(&item, false).await?;
            if !cap.admits(&current, &current_revision) {
                return Err(Error::Retired);
            }
            self.alive()?;
            if cap.expires <= Instant::now() {
                return Err(Error::Retired);
            }
            if self.blocked(&cap.connection)? {
                return Err(Error::Withdrawn);
            }
            self.check_refresh(&cap.connection, reservation)?;
            let token = if refresh {
                secret.refresh()?
            } else {
                secret.access()?
            };
            Ok(consume(&token.0))
        })
        .await
    }
    /// A native connector can await one operation without holding the keyring
    /// gate. Withdrawal/reauthentication may proceed while I/O is pending. Before
    /// releasing a result, re-read the same capability's actual OS state.
    ///
    /// The borrowed bytes and future stay native-only. Dropping this future
    /// cancels its callback and clears the owned token; transports must honor
    /// cancellation rather than detach work or retain credential copies.
    pub async fn with_token_operation<T>(
        &self,
        cap: &TokenCapability,
        operation: impl AsyncFnOnce(&[u8]) -> T,
    ) -> Result<T> {
        self.with_secret_operation(cap, false, operation).await
    }
    /// Reissue a short lease only while the same native owner, connection,
    /// account and OS revision remain current. A run may use this between
    /// streaming calls without turning a replaced key into fresh authority.
    pub async fn renew_capability(&self, original: &TokenCapability) -> Result<TokenCapability> {
        let current = self.capability(&original.connection).await?;
        if current.owner != original.owner || !original.admits(&current.account, &current.revision)
        {
            return Err(Error::Retired);
        }
        Ok(current)
    }
    async fn current_stream_revision(&self, original: &TokenCapability) -> Result<()> {
        let current = self.renew_capability(original).await?;
        self.with_secret(&current, false, |_| ()).await
    }
    /// Run-owned streaming provider I/O. The native caller supplies its finite
    /// run deadline and must drop the callback on cancellation. A fresh OS
    /// capability is checked every two seconds, so an operation can outlive
    /// its initial 30-second admission lease without retaining stale authority.
    /// Local withdrawal, replacement and owner retirement drop the callback
    /// immediately; independent keyring mutation is detected at the next
    /// current-revision check. No secret leaves this native callback.
    pub async fn with_token_stream<T>(
        &self,
        cap: &TokenCapability,
        duration: Duration,
        operation: impl AsyncFnOnce(&[u8]) -> T,
    ) -> Result<T> {
        if duration.is_zero() || duration > MAX_STREAM {
            return Err(Error::Invalid);
        }
        let deadline = Instant::now() + duration;
        let mut changes = self.changes.subscribe();
        let work = async {
            let token = self
                .with_secret(cap, false, |bytes| Token(Zeroizing::new(bytes.to_vec())))
                .await?;
            let operation = operation(&token.0);
            tokio::pin!(operation);
            let mut recheck = tokio::time::interval(STREAM_RECHECK);
            recheck.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
            loop {
                tokio::select! {
                    biased;
                    changed = changes.changed() => {
                        let _ = changed;
                        return Err(Error::Retired);
                    }
                    _ = recheck.tick() => self.current_stream_revision(cap).await?,
                    result = &mut operation => {
                        self.current_stream_revision(cap).await?;
                        if changes.has_changed().unwrap_or(true) {
                            return Err(Error::Retired);
                        }
                        return Ok(result);
                    }
                }
            }
        };
        tokio::time::timeout_at(tokio::time::Instant::from_std(deadline), work)
            .await
            .unwrap_or(Err(Error::Deadline))
    }
    /// Native refresh transport only. Expired access authority is never borrowed
    /// here; the independent refresh expiration and current revision still gate
    /// both sides of I/O. No refresh secret is returned to a browser.
    pub async fn with_refresh_operation<T>(
        &self,
        cap: &TokenCapability,
        operation: impl AsyncFnOnce(&[u8]) -> T,
    ) -> Result<T> {
        self.with_secret_operation(cap, true, operation).await
    }
    async fn with_secret_operation<T>(
        &self,
        cap: &TokenCapability,
        refresh: bool,
        operation: impl AsyncFnOnce(&[u8]) -> T,
    ) -> Result<T> {
        let deadline = cap.expires.min(Instant::now() + OPERATION);
        let work = async {
            let token = self
                .with_secret(cap, refresh, |bytes| Token(Zeroizing::new(bytes.to_vec())))
                .await?;
            let result = operation(&token.0).await;
            // A successful provider reply cannot revive withdrawn/replaced
            // credentials, a retired native owner, or an expired capability.
            self.with_secret(cap, refresh, |_| ()).await?;
            Ok(result)
        };
        match tokio::time::timeout_at(tokio::time::Instant::from_std(deadline), work).await {
            Ok(result) => result,
            Err(_) if cap.expires <= Instant::now() => Err(Error::Retired),
            Err(_) => Err(Error::Deadline),
        }
    }
    /// Persist denial before OS deletion. Locked/unavailable deletion remains
    /// visible and retryable, but never re-enables this account on restart.
    pub async fn withdraw(&self, connection: &str) -> Result<()> {
        self.alive()?;
        self.block(connection)?;
        self.bounded(async {
            let _mutation = self.mutation().await?;
            // A preceding writer may have cleared the early denial while this
            // withdrawal waited. Reassert it under the shared mutation lease.
            self.block(connection)?;
            let _guard = self.gate.lock().await;
            let service = SecretService::connect(EncryptionType::Dh).await?;
            for (item, locked) in self.items(&service, Some(connection)).await? {
                if locked {
                    return Err(Error::Locked);
                }
                item.delete().await?;
            }
            Ok(())
        })
        .await
    }
}
fn id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn random() -> Result<String> {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes).map_err(|_| Error::Unavailable)?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}
