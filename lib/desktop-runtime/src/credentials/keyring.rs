//! The operating-system keyring behind `Credentials`: the freedesktop Secret
//! Service on Linux and the login Keychain on macOS. Both expose the same
//! small item model: string attributes searchable by application, profile and
//! connection, plus one secret byte string. Callers own policy and bounds.
use super::{Error, Result};
use std::collections::HashMap;
use zeroize::Zeroizing;

pub(super) type Attributes<'a> = HashMap<&'a str, &'a str>;

#[cfg(target_os = "linux")]
pub(super) use linux::{Item, Keyring, Transport};
#[cfg(target_os = "macos")]
pub(super) use macos::{Item, Keyring, Transport};

#[cfg(target_os = "linux")]
mod linux {
    use super::*;
    use secret_service::{EncryptionType, SecretService};

    impl From<secret_service::Error> for Error {
        fn from(value: secret_service::Error) -> Self {
            match value {
                secret_service::Error::Locked => Self::Locked,
                secret_service::Error::NoResult => Self::Missing,
                _ => Self::Unavailable,
            }
        }
    }

    pub(in crate::credentials) struct Keyring(SecretService<'static>);
    pub(in crate::credentials) struct Item<'a>(secret_service::Item<'a>);

    #[derive(Default)]
    pub(in crate::credentials) struct Transport {
        connection: Option<zbus::Connection>,
        service: Option<Keyring>,
        owner: Option<String>,
    }
    impl Transport {
        async fn owner(connection: &zbus::Connection) -> Result<Option<String>> {
            let bus = zbus::fdo::DBusProxy::new(connection)
                .await
                .map_err(|_| Error::Unavailable)?;
            let name = "org.freedesktop.secrets"
                .try_into()
                .map_err(|_| Error::Unavailable)?;
            match bus.get_name_owner(name).await {
                Ok(owner) => Ok(Some(owner.to_string())),
                Err(zbus::fdo::Error::NameHasNoOwner(_)) => Ok(None),
                Err(_) => Err(Error::Unavailable),
            }
        }
        /// Drop a possibly stale keyring session; the next use reconnects.
        pub(in crate::credentials) fn reset(&mut self) {
            self.service = None;
        }
        pub(in crate::credentials) async fn keyring(&mut self) -> Result<&Keyring> {
            if self.connection.as_ref().is_some_and(|c| c.is_closed()) {
                *self = Self::default();
            }
            if self.connection.is_none() {
                self.connection = Some(
                    zbus::Connection::session()
                        .await
                        .map_err(|_| Error::Unavailable)?,
                );
            }
            let connection = self.connection.as_ref().ok_or(Error::Unavailable)?;
            let before = Self::owner(connection).await?;
            if before != self.owner {
                self.service = None;
            }
            if self.service.is_none() {
                // Keep the bus peer alive even if a bounded caller cancels while
                // the keyring is still negotiating this encrypted session.
                let service =
                    SecretService::connect_with_existing(EncryptionType::Dh, connection.clone())
                        .await?;
                let after = Self::owner(connection).await?;
                if after.is_none() || before.is_some() && before != after {
                    return Err(Error::Unavailable);
                }
                self.owner = after;
                self.service = Some(Keyring(service));
            }
            self.service.as_ref().ok_or(Error::Unavailable)
        }
    }
    impl Keyring {
        /// Matching items, each with whether it is currently locked.
        pub(in crate::credentials) async fn search(
            &self,
            attrs: Attributes<'_>,
        ) -> Result<Vec<(Item<'_>, bool)>> {
            let found = self.0.search_items(attrs).await?;
            Ok(found
                .unlocked
                .into_iter()
                .map(|i| (Item(i), false))
                .chain(found.locked.into_iter().map(|i| (Item(i), true)))
                .collect())
        }
        /// The default collection exists and is unlocked.
        pub(in crate::credentials) async fn ready(&self) -> Result<()> {
            if self.0.get_default_collection().await?.is_locked().await? {
                return Err(Error::Locked);
            }
            Ok(())
        }
        pub(in crate::credentials) async fn create(
            &self,
            label: &str,
            attrs: Attributes<'_>,
            secret: &[u8],
        ) -> Result<()> {
            let collection = self.0.get_default_collection().await?;
            if collection.is_locked().await? {
                return Err(Error::Locked);
            }
            collection
                .create_item(label, attrs, secret, false, "application/octet-stream")
                .await?;
            Ok(())
        }
    }
    impl Item<'_> {
        pub(in crate::credentials) async fn attributes(&self) -> Result<HashMap<String, String>> {
            Ok(self.0.get_attributes().await?)
        }
        pub(in crate::credentials) async fn secret(&self) -> Result<Zeroizing<Vec<u8>>> {
            Ok(Zeroizing::new(self.0.get_secret().await?))
        }
        pub(in crate::credentials) async fn replace(
            &self,
            secret: &[u8],
            attrs: Attributes<'_>,
        ) -> Result<()> {
            self.0
                .set_secret(secret, "application/octet-stream")
                .await?;
            self.0.set_attributes(attrs).await?;
            Ok(())
        }
        pub(in crate::credentials) async fn delete(&self) -> Result<()> {
            Ok(self.0.delete().await?)
        }
    }
}

// Each Factory item is a generic password whose service names the application
// and profile and whose account is the connection ID, so the Keychain itself
// keeps one item per connection. The complete nonsecret attribute map is JSON
// in the item's generic attribute. Keychain calls block (and may show an
// access prompt), so they run on the blocking pool under the caller's bound.
#[cfg(target_os = "macos")]
mod macos {
    use super::*;
    use core_foundation::base::{CFType, TCFType};
    use core_foundation::boolean::CFBoolean;
    use core_foundation::data::CFData;
    use core_foundation::dictionary::CFDictionary;
    use core_foundation::string::{CFString, CFStringRef};
    use security_framework_sys::item::{
        kSecAttrAccount, kSecAttrLabel, kSecAttrService, kSecClass, kSecClassGenericPassword,
        kSecMatchLimit, kSecMatchLimitAll, kSecReturnAttributes, kSecReturnData, kSecValueData,
    };
    use security_framework_sys::keychain_item::{
        SecItemAdd, SecItemCopyMatching, SecItemDelete, SecItemUpdate,
    };
    use std::marker::PhantomData;

    #[link(name = "Security", kind = "framework")]
    extern "C" {
        static kSecAttrGeneric: CFStringRef;
    }
    const ERR_USER_CANCELED: i32 = -128;
    const ERR_AUTH_FAILED: i32 = -25293;
    const ERR_DUPLICATE_ITEM: i32 = -25299;
    const ERR_ITEM_NOT_FOUND: i32 = -25300;
    const ERR_INTERACTION_NOT_ALLOWED: i32 = -25308;
    const METADATA_LIMIT: usize = 16384;

    fn status(code: i32) -> Result<()> {
        match code {
            0 => Ok(()),
            ERR_ITEM_NOT_FOUND => Err(Error::Missing),
            ERR_INTERACTION_NOT_ALLOWED | ERR_AUTH_FAILED | ERR_USER_CANCELED => Err(Error::Locked),
            ERR_DUPLICATE_ITEM => Err(Error::Ambiguous),
            _ => Err(Error::Unavailable),
        }
    }
    fn key(key: CFStringRef) -> CFString {
        unsafe { CFString::wrap_under_get_rule(key) }
    }
    fn dictionary(pairs: &[(CFStringRef, CFType)]) -> CFDictionary<CFString, CFType> {
        let pairs: Vec<_> = pairs
            .iter()
            .map(|(name, value)| (key(*name), value.clone()))
            .collect();
        CFDictionary::from_CFType_pairs(&pairs)
    }
    fn item_query(service: &str, account: Option<&str>) -> Vec<(CFStringRef, CFType)> {
        let mut query = unsafe {
            vec![
                (kSecClass, key(kSecClassGenericPassword).as_CFType()),
                (kSecAttrService, CFString::new(service).as_CFType()),
            ]
        };
        if let Some(account) = account {
            query.push((
                unsafe { kSecAttrAccount },
                CFString::new(account).as_CFType(),
            ));
        }
        query
    }
    fn service_name(attrs: &Attributes<'_>) -> Result<String> {
        match (attrs.get("application"), attrs.get("profile")) {
            (Some(application), Some(profile)) => Ok(format!("{application}/{profile}")),
            _ => Err(Error::Invalid),
        }
    }
    fn metadata(attrs: &Attributes<'_>) -> Result<Vec<u8>> {
        let json = serde_json::to_vec(attrs).map_err(|_| Error::Invalid)?;
        if json.len() > METADATA_LIMIT {
            return Err(Error::Invalid);
        }
        Ok(json)
    }
    async fn blocking<T: Send + 'static>(
        work: impl FnOnce() -> Result<T> + Send + 'static,
    ) -> Result<T> {
        tokio::task::spawn_blocking(work)
            .await
            .map_err(|_| Error::Unavailable)?
    }

    pub(in crate::credentials) struct Keyring;
    pub(in crate::credentials) struct Item<'a> {
        service: String,
        account: String,
        keyring: PhantomData<&'a Keyring>,
    }

    #[derive(Default)]
    pub(in crate::credentials) struct Transport;
    impl Transport {
        pub(in crate::credentials) fn reset(&mut self) {}
        pub(in crate::credentials) async fn keyring(&mut self) -> Result<&Keyring> {
            Ok(&Keyring)
        }
    }
    impl Keyring {
        /// Matching items. A locked Keychain refuses the search rather than
        /// listing locked items, so every returned item is unlocked.
        pub(in crate::credentials) async fn search(
            &self,
            attrs: Attributes<'_>,
        ) -> Result<Vec<(Item<'_>, bool)>> {
            let service = service_name(&attrs)?;
            let account = attrs.get("connection").map(|value| value.to_string());
            let wanted: HashMap<String, String> = attrs
                .iter()
                .map(|(name, value)| (name.to_string(), value.to_string()))
                .collect();
            let accounts = blocking(move || {
                let mut query = item_query(&service, account.as_deref());
                unsafe {
                    query.push((kSecReturnAttributes, CFBoolean::true_value().as_CFType()));
                    query.push((kSecMatchLimit, key(kSecMatchLimitAll).as_CFType()));
                }
                let query = dictionary(&query);
                let mut result = std::ptr::null();
                match status(unsafe {
                    SecItemCopyMatching(query.as_concrete_TypeRef(), &mut result)
                }) {
                    Err(Error::Missing) => return Ok((service, Vec::new())),
                    other => other?,
                }
                let result = unsafe { CFType::wrap_under_create_rule(result) };
                let items = result
                    .downcast::<core_foundation::array::CFArray>()
                    .ok_or(Error::Invalid)?;
                let mut accounts = Vec::new();
                for item in items.iter() {
                    let item = unsafe {
                        CFDictionary::<CFString, CFType>::wrap_under_get_rule(*item as _)
                    };
                    let account = item
                        .find(key(unsafe { kSecAttrAccount }))
                        .and_then(|value| value.downcast::<CFString>())
                        .ok_or(Error::Invalid)?
                        .to_string();
                    let stored = decode(&item)?;
                    if wanted
                        .iter()
                        .all(|(name, value)| stored.get(name) == Some(value))
                    {
                        accounts.push(account);
                    }
                }
                Ok((service, accounts))
            })
            .await?;
            let (service, accounts) = accounts;
            Ok(accounts
                .into_iter()
                .map(|account| {
                    (
                        Item {
                            service: service.clone(),
                            account,
                            keyring: PhantomData,
                        },
                        false,
                    )
                })
                .collect())
        }
        /// The login Keychain answers a search. Its item attributes stay
        /// readable while it is locked; reading a secret then asks to unlock.
        pub(in crate::credentials) async fn ready(&self) -> Result<()> {
            blocking(|| {
                let query = item_query("dev.soda.factory/readiness", None);
                let query = dictionary(&query);
                let mut result = std::ptr::null();
                match status(unsafe {
                    SecItemCopyMatching(query.as_concrete_TypeRef(), &mut result)
                }) {
                    Ok(()) => {
                        drop(unsafe { CFType::wrap_under_create_rule(result) });
                        Ok(())
                    }
                    Err(Error::Missing) => Ok(()),
                    Err(error) => Err(error),
                }
            })
            .await
        }
        pub(in crate::credentials) async fn create(
            &self,
            label: &str,
            attrs: Attributes<'_>,
            secret: &[u8],
        ) -> Result<()> {
            let service = service_name(&attrs)?;
            let account = attrs.get("connection").ok_or(Error::Invalid)?.to_string();
            let metadata = metadata(&attrs)?;
            let label = label.to_owned();
            let secret = Zeroizing::new(secret.to_vec());
            blocking(move || {
                let mut item = item_query(&service, Some(&account));
                unsafe {
                    item.push((kSecAttrLabel, CFString::new(&label).as_CFType()));
                    item.push((kSecAttrGeneric, CFData::from_buffer(&metadata).as_CFType()));
                    item.push((kSecValueData, CFData::from_buffer(&secret).as_CFType()));
                }
                let item = dictionary(&item);
                status(unsafe { SecItemAdd(item.as_concrete_TypeRef(), std::ptr::null_mut()) })
            })
            .await
        }
    }
    fn decode(item: &CFDictionary<CFString, CFType>) -> Result<HashMap<String, String>> {
        let bytes = item
            .find(key(unsafe { kSecAttrGeneric }))
            .and_then(|value| value.downcast::<CFData>())
            .ok_or(Error::Invalid)?;
        if bytes.len() as usize > METADATA_LIMIT {
            return Err(Error::Invalid);
        }
        serde_json::from_slice(bytes.bytes()).map_err(|_| Error::Invalid)
    }
    impl Item<'_> {
        fn copy(&self, attributes: bool) -> Result<CFType> {
            let mut query = item_query(&self.service, Some(&self.account));
            unsafe {
                let wanted = if attributes {
                    kSecReturnAttributes
                } else {
                    kSecReturnData
                };
                query.push((wanted, CFBoolean::true_value().as_CFType()));
            }
            let query = dictionary(&query);
            let mut result = std::ptr::null();
            status(unsafe { SecItemCopyMatching(query.as_concrete_TypeRef(), &mut result) })?;
            Ok(unsafe { CFType::wrap_under_create_rule(result) })
        }
        fn owned(&self) -> Item<'static> {
            Item {
                service: self.service.clone(),
                account: self.account.clone(),
                keyring: PhantomData,
            }
        }
        pub(in crate::credentials) async fn attributes(&self) -> Result<HashMap<String, String>> {
            let item = self.owned();
            blocking(move || {
                let found = item.copy(true)?;
                let found = found.downcast::<CFDictionary>().ok_or(Error::Invalid)?;
                let found = unsafe {
                    CFDictionary::<CFString, CFType>::wrap_under_get_rule(
                        found.as_concrete_TypeRef(),
                    )
                };
                decode(&found)
            })
            .await
        }
        pub(in crate::credentials) async fn secret(&self) -> Result<Zeroizing<Vec<u8>>> {
            let item = self.owned();
            blocking(move || {
                let data = item
                    .copy(false)?
                    .downcast::<CFData>()
                    .ok_or(Error::Invalid)?;
                Ok(Zeroizing::new(data.bytes().to_vec()))
            })
            .await
        }
        pub(in crate::credentials) async fn replace(
            &self,
            secret: &[u8],
            attrs: Attributes<'_>,
        ) -> Result<()> {
            if service_name(&attrs)? != self.service
                || attrs.get("connection") != Some(&self.account.as_str())
            {
                return Err(Error::Invalid);
            }
            let metadata = metadata(&attrs)?;
            let secret = Zeroizing::new(secret.to_vec());
            let item = self.owned();
            blocking(move || {
                let query = dictionary(&item_query(&item.service, Some(&item.account)));
                let changes = unsafe {
                    dictionary(&[
                        (kSecValueData, CFData::from_buffer(&secret).as_CFType()),
                        (kSecAttrGeneric, CFData::from_buffer(&metadata).as_CFType()),
                    ])
                };
                status(unsafe {
                    SecItemUpdate(query.as_concrete_TypeRef(), changes.as_concrete_TypeRef())
                })
            })
            .await
        }
        pub(in crate::credentials) async fn delete(&self) -> Result<()> {
            let item = self.owned();
            blocking(move || {
                let query = dictionary(&item_query(&item.service, Some(&item.account)));
                status(unsafe { SecItemDelete(query.as_concrete_TypeRef()) })
            })
            .await
        }
    }
}
