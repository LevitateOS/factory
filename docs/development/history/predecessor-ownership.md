# Source ownership

## Owner language policy

Network-facing servers use Go. System and privileged applications use Rust.
Do not add authored Python programs. Preserve the existing host, installer,
identity and release implementations and their installed executable identities.

## Packages and files

| Location | Responsibility |
| --- | --- |
| `cmd/` | Installed commands and their entrypoints |
| `lib/` | Rust implementation libraries |
| `internal/` | Go server, domain, storage and client packages |
| `tools/` | Development and build helpers |
| `frontend/` | Authored browser code and Forgejo presentation |
| `system/` | Images, services and provisioning |
| `assets/` | Canonical assets, licenses and attribution |
| `tests/` | Integration checks and fixtures |
| `docs/public/` | Public handbook source |

Use the existing defining package and caller. Keep domain types canonical and
preserve dependency direction. A file move must update its imports, manifest or
build selection, installed path and relevant tests together. Keep private tests
with their implementation instead of exporting internals for test access.

The Go store owns the canonical database schema. Host and release consumers must
use the same schema. Moving files does not create another schema owner.

The Factory engine and store consume neutral domain/runtime ports. Provider SDKs,
appliance adapters and private transport clients belong outside their production
dependency closure. Forge's `internal/factory/client` is a separate IPC consumer;
the engine cannot import it. `scripts/check-factory-boundaries.sh` enforces this
through the actual transitive Go dependency graph in local source checks and CI.

Fountain owns native shell, route, authentication and mount lifetimes. Soda owns
its product behavior and consumes the host capabilities. Soda deliberately
overrides all Forgejo pages; preserve that design scope and verify integration
when changing it.

Use the [library boundaries](library-boundaries.md) when changing those callers,
and the [backlog](factory-remaining-tasks.md) for unfinished product work. Update
guidance that a change makes misleading; no source census or review ledger is
required.
