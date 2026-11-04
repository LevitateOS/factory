//! Native OAuth adapter. Provider HTTP/decoding stays in the headless Rust owner;
//! durable rotation reservations and conditional publication stay in custody.
use crate::credentials::{
    AccountStatus, Credentials, Error as CredentialError, Grant, Provider, TokenCapability,
};
use crate::github::{provider_error, Error, Verifier};
pub use crate::github_provider::DevicePrompt;
use crate::supervisor::NativeSupervisor;

pub struct DeviceAuthorization(crate::github_provider::DeviceAuthorization);
impl DeviceAuthorization {
    pub fn prompt(&self) -> &DevicePrompt {
        self.0.prompt()
    }
    pub fn verifier(&self) -> Verifier {
        Verifier {
            client: self.0.verifier(),
        }
    }
    pub async fn wait(self) -> Result<Grant, Error> {
        self.0.wait().await.map_err(provider_error)
    }
}
impl NativeSupervisor {
    pub fn github_device_start(
        &self,
    ) -> impl std::future::Future<Output = Result<DeviceAuthorization, Error>> {
        let client = self.github_client();
        async move {
            client
                .map_err(|_| Error::Unavailable)?
                .device_start()
                .await
                .map(DeviceAuthorization)
                .map_err(provider_error)
        }
    }
    pub fn github_refresh<'a>(
        &self,
        credentials: &'a Credentials,
        capability: &'a TokenCapability,
    ) -> impl std::future::Future<Output = Result<AccountStatus, Error>> + 'a {
        let client = self.github_client();
        async move {
            let account = capability.account();
            if account.provider != Provider::Github || account.origin != "https://github.com" {
                return Err(Error::Invalid);
            }
            let client = client.map_err(|_| Error::Unavailable)?;
            client
                .scope(async {
                    let mut provider_failure = None;
                    let result = credentials
                        .rotate_grant(capability, async |refresh| {
                            client.refresh(refresh).await.map_err(|error| {
                                provider_failure = Some(provider_error(error));
                                CredentialError::RefreshPending
                            })
                        })
                        .await;
                    match result {
                        Ok(status) => Ok(status),
                        Err(error) => Err(provider_failure.unwrap_or(Error::Credential(error))),
                    }
                })
                .await
                .map_err(provider_error)?
        }
    }
}
