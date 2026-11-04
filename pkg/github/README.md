# Native GitHub ownership moved

The authenticated desktop connector now lives in
[headless Rust](../../lib/desktop-runtime/src/github_provider/mod.rs): OAuth,
refresh, identity, HTTPS, provider-local decoding and bounded cancellation.
The superseded Go SDK implementation and credential-carrying native routes are
removed together. This directory is no longer a Go package.

Go retains [canonical observation admission/projection](../factory/runtime/native_observations.go),
[neutral fact contracts](../collaboration/observations.go) and
[atomic registry transactions](../store/collaboration_observations.go).
No provider credential is accepted by that Go port. Product authorization,
workflow decisions and the complete product SQL owner remain in Go.

Historical implementation and evidence remain in Git history; see the adopted
[allocation](../../docs/development/factory-products/language-allocation.md)
and current [queue](../../docs/development/factory-products/implementation-tasks.md).
