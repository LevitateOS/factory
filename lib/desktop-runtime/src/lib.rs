//! Native lifecycle and bounded semantic effects. Product APIs, durable workflows
//! and SQL remain in Go.
#[cfg(target_os = "linux")]
pub mod bootstrap;
#[cfg(target_os = "linux")]
pub mod collaboration;
#[cfg(target_os = "linux")]
pub mod credentials;
#[cfg(target_os = "linux")]
pub mod database;
#[cfg(target_os = "linux")]
pub mod factory_provider;
#[cfg(target_os = "linux")]
pub mod github;
#[cfg(target_os = "linux")]
pub mod github_device;
#[cfg(target_os = "linux")]
pub mod github_provider;
#[cfg(target_os = "linux")]
mod product;
#[cfg(target_os = "linux")]
pub mod supervisor;
