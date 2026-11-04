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
#[cfg(all(
    target_os = "linux",
    feature = "trusted-host-process-probe",
    not(debug_assertions)
))]
compile_error!("trusted-host-process-probe cannot be enabled in release builds");
#[cfg(all(
    target_os = "linux",
    feature = "trusted-host-process-probe",
    debug_assertions
))]
// Direct process diagnostics only. The production runtime cannot compile this
// same-user launch path into a selectable native agent executor.
pub mod execution;
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
