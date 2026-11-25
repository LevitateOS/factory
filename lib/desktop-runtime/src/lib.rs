//! Native lifecycle and bounded semantic effects. Product APIs, durable workflows
//! and SQL remain in Go.
#[cfg(unix)]
pub mod bootstrap;
#[cfg(unix)]
pub mod collaboration;
#[cfg(unix)]
pub mod credentials;
#[cfg(unix)]
pub mod database;
#[cfg(unix)]
mod private_fs;
#[cfg(unix)]
mod unix_socket;
#[cfg(unix)]
pub mod factory_provider;
#[cfg(unix)]
pub mod github;
#[cfg(unix)]
pub mod github_device;
#[cfg(unix)]
pub mod fountain_provider;
#[cfg(unix)]
pub mod fountain_oauth;
#[cfg(unix)]
pub mod github_provider;
#[cfg(unix)]
pub mod mcp_signing;
#[cfg(unix)]
mod product;
#[cfg(unix)]
mod product_request;
#[cfg(unix)]
pub use product_request::{
    product_artifact_read, product_request_body_limit, ProductRequest, PRODUCT_ARTIFACT_BYTES,
};
#[cfg(unix)]
pub mod supervisor;

#[cfg(unix)]
pub mod provider_contract;
