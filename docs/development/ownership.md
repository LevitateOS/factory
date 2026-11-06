# Factory source ownership

The selected [language allocation](factory-products/language-allocation.md)
defines responsibility and dependency directions. Preserve one defining owner.

| Location | Responsibility |
| --- | --- |
| `cmd/` | Go server and native desktop, worker and broker entrypoints |
| `pkg/factory/` | Canonical product contracts, services, coordinator and APIs |
| `pkg/store/` | PostgreSQL product schema, migrations and transactions |
| `lib/` | Rust native execution, resources, credentials and provider connectors |
| `frontend/factory/` | Shared Lit product UI for desktop and Fountain |
| `frontend/desktop/` | Desktop UI host bootstrap |
| `frontend/storybook/` | Existing interactive presentation/behavior scenarios |
| `tools/` | Existing native integration checks and development analysis |
| `scripts/` | Build orchestration and source boundaries |
| `assets/`, `system/licenses/` | Canonical browser assets and attribution |

Go owns durable workflows, canonical identity and product transactions. Rust
owns native effects, resource lifetime, credential custody and the complete
authenticated desktop connector. Lit owns presentation and retained drafts.
Fountain server adapters and native provider authority stay outside the
standalone engine's dependency closure; `bun run check:boundaries` checks the
actual transitive Go graph. Do not replace this with a full Go-engine rewrite.

Use the existing defining package and callers. A move must update imports,
manifest/build selection, installed identity and affected checks together.
Preserve the shared UI and host-specific admission/lifetime boundaries. Current
verification commands live in [testing](testing.md); outstanding work lives in
the [canonical plan](factory-products/implementation-tasks.md).

[Predecessor ownership guidance](history/predecessor-ownership.md) is retained as
history; its absent paths and other products are not this repository's layout.
