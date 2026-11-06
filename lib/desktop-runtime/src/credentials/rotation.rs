//! One durable refresh attempt per captured revision. This is native custody,
//! not a provider client: the callback must verify the replacement identity.
use super::{
    Account, AccountStatus, Credentials, Error, Grant, Result, Secret, Token, TokenCapability,
    OPERATION,
};
use crate::private_fs::private_file;
use secret_service::{EncryptionType, SecretService};
use std::fs::{File, OpenOptions};
use std::io::{Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::time::{Duration, Instant};
use zeroize::Zeroizing;

pub(super) struct Reservation {
    connection: String,
    identity: String,
}
pub(super) struct Mutation {
    // Closing the descriptor releases flock, including on cancellation/panic.
    _file: File,
}

impl Credentials {
    pub(super) async fn mutation(&self) -> Result<Mutation> {
        let file = private_file(&self.mutation, false).map_err(|_| Error::Storage)?;
        loop {
            self.alive()?;
            if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0 {
                return Ok(Mutation { _file: file });
            }
            if std::io::Error::last_os_error().kind() != std::io::ErrorKind::WouldBlock {
                return Err(Error::Storage);
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    }
    fn refresh_marker(&self, connection: &str) -> Result<Option<String>> {
        if !super::id(connection) {
            return Err(Error::Invalid);
        }
        let file = match OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOFOLLOW)
            .open(self.refreshes.join(connection))
        {
            Ok(file) => file,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(_) => return Err(Error::Storage),
        };
        let metadata = file.metadata().map_err(|_| Error::Storage)?;
        if !metadata.is_file()
            || metadata.uid() != unsafe { libc::geteuid() }
            || metadata.mode() & 0o077 != 0
        {
            return Err(Error::Storage);
        }
        let mut identity = String::new();
        file.take(129)
            .read_to_string(&mut identity)
            .map_err(|_| Error::Storage)?;
        // An incomplete marker still blocks replay; it never grants authority.
        Ok(Some(identity))
    }
    pub(super) fn refresh_pending(&self, connection: &str) -> Result<bool> {
        Ok(self.refresh_marker(connection)?.is_some())
    }
    pub(super) fn check_refresh(
        &self,
        connection: &str,
        reservation: Option<&Reservation>,
    ) -> Result<()> {
        match (self.refresh_marker(connection)?, reservation) {
            (None, None) => Ok(()),
            (Some(value), Some(expected))
                if expected.connection == connection && value == expected.identity =>
            {
                Ok(())
            }
            (_, Some(_)) => Err(Error::Retired),
            (Some(_), None) => Err(Error::RefreshPending),
        }
    }
    fn sync_refreshes(&self) -> Result<()> {
        File::open(&self.refreshes)
            .and_then(|file| file.sync_all())
            .map_err(|_| Error::Storage)
    }
    pub(super) fn clear_refresh(&self, connection: &str) -> Result<()> {
        match std::fs::remove_file(self.refreshes.join(connection)) {
            Ok(()) => self.sync_refreshes(),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(_) => Err(Error::Storage),
        }
    }
    fn reserve_refresh(&self, cap: &TokenCapability) -> Result<Reservation> {
        self.check_refresh(&cap.connection, None)?;
        let identity = format!("{}{}", cap.revision, super::random()?);
        let mut marker = private_file(&self.refreshes.join(&cap.connection), true)
            .map_err(|_| Error::Storage)?;
        marker
            .write_all(identity.as_bytes())
            .and_then(|_| marker.sync_all())
            .map_err(|_| Error::Storage)?;
        self.changes.send_replace(());
        self.sync_refreshes()?;
        Ok(Reservation {
            connection: cap.connection.clone(),
            identity,
        })
    }

    /// Reserve before dispatch because provider refresh can invalidate both old
    /// tokens. Cancellation/failure leaves a durable unconfirmed marker: neither
    /// access use nor another refresh can silently replay that revision.
    /// Explicit verified reauthentication can replace it via store_grant.
    ///
    /// No keyring or mutation lock is held while the callback performs I/O.
    /// The returned account must be freshly provider-verified. Publication checks
    /// the captured identity/revision/reservation and never resurrects deletion.
    pub async fn rotate_grant(
        &self,
        cap: &TokenCapability,
        operation: impl AsyncFnOnce(&[u8]) -> Result<(Account, Grant)>,
    ) -> Result<AccountStatus> {
        let deadline = cap.expires.min(Instant::now() + OPERATION);
        let work = async {
            let (token, reservation) = self
                .bounded(async {
                    let _mutation = self.mutation().await?;
                    let token = self
                        .with_secret(cap, true, |bytes| Token(Zeroizing::new(bytes.to_vec())))
                        .await?;
                    let reservation = self.reserve_refresh(cap)?;
                    Ok((token, reservation))
                })
                .await?;
            self.alive()?;
            if cap.expires <= Instant::now() {
                return Err(Error::Retired);
            }
            let (account, grant) = operation(&token.0).await?;
            if account.provider != cap.account.provider
                || account.origin != cap.account.origin
                || account.account != cap.account.account
            {
                return Err(Error::Retired);
            }
            if grant.refresh.is_none() {
                return Err(Error::Invalid);
            }
            let secret = Secret::Grant(grant);
            secret.validate()?;
            secret.access()?;
            self.bounded(async {
                let _mutation = self.mutation().await?;
                self.with_secret_in(cap, true, Some(&reservation), |_| ())
                    .await?;
                let _gate = self.gate.lock().await;
                let service = SecretService::connect(EncryptionType::Dh).await?;
                let mut found = self.items(&service, Some(&cap.connection)).await?;
                if found.len() > 1 {
                    return Err(Error::Ambiguous);
                }
                let (item, locked) = found.pop().ok_or(Error::Missing)?;
                let (current, revision) = self.metadata(&item, locked).await?;
                if locked {
                    return Err(Error::Locked);
                }
                if !cap.admits(&current, &revision) {
                    return Err(Error::Retired);
                }
                self.check_refresh(&cap.connection, Some(&reservation))?;
                self.persist_secret(
                    &service,
                    account,
                    cap.connection.clone(),
                    Some(item),
                    secret,
                    Some(cap),
                )
                .await
            })
            .await
        };
        match tokio::time::timeout_at(tokio::time::Instant::from_std(deadline), work).await {
            Ok(result) => result,
            Err(_) if cap.expires <= Instant::now() => Err(Error::Retired),
            Err(_) => Err(Error::Deadline),
        }
    }
}
