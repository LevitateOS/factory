# Subscription identity contracts

This package defines the connection, grant, lease and execution records shared
by Soda's service callers. It also validates launch requests and selects
authorized Muse connections. It performs no network access, credential storage
or native execution.

## Use inside the Soda module

Import `github.com/levitateos/sodaos/internal/identity` for canonical records
rather than defining parallel request types. Main entry points include:

- [`Connection`, `GrantRequest`, `AcquireRequest`, `Lease` and `Binding`](types.go):
  subscription metadata, delegation and native process bindings.
- `GrantRequest.Validate`, `AcquireRequest.Validate(now)` and `Binding.Validate`:
  shape, confirmation and deadline admission.
- `Execution.Validate` and `AcquisitionDigest`: an execution identity bound to
  immutable acquisition inputs; the digest excludes the deadline.
- [`SelectMuseConnection`](selection.go): choose from an already authorized list
  of ready Muse connections; multiple matches require explicit selection.
- [`LaunchRequest.Validate` and `MuseArguments`](launch.go): bounded invocation
  preferences and Muse subscription arguments.

For a pure provider check:

```go
import "github.com/levitateos/sodaos/internal/identity"

supported := identity.ProviderValid(identity.Muse)
```

## Authority and errors

Validation alone does not establish access. Trusted callers supply the actor,
project and current authorized records. Acquisition requires a future deadline
within 24 hours. Grants require both subscription and credential-exposure
confirmations. Use `errors.Is` with `ErrDenied`, `ErrBusy`, `ErrStale`,
`ErrUncertain` and `ErrNotFound` when handling broker outcomes.

`Delivery` contains private credentials and must never enter browser responses.
`LaunchRequest` carries preferences rather than credentials or caller authority.
Provider custody and execution are implemented by the Rust identity service;
Go service callers use the [private broker client](client/README.md).

See [credentials](../../docs/reference/credentials.md),
[trust model](../../docs/architecture/trust.md) and
[factory interfaces](../../docs/architecture/factory-interfaces.md) for policy.
