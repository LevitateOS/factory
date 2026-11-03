# Factory

Factory is a Linux-first desktop application and Go workflow engine with a shared
Lit frontend for the native Tauri host and Soda Forge/Fountain.

This repository imports the implementation in progress from SodaOS. It is not a
completed or released desktop product. Connected GitHub login/writes, host
execution, automatic intake, full PR journeys and installed distribution remain
unfinished. The dependency-ordered backlog is in
[the implementation plan](docs/development/factory-products/implementation-tasks.md).

The Go engine lives in public `pkg/` packages under
`github.com/levitateos/factory`. The native backend does not depend on the SodaOS
module, an appliance, Fountain or its SDK. Existing versions and lockfiles are
imported as working source, without claiming a fresh release baseline.

## Development

Use Go, Bun and Rust. Native Tauri builds also need the usual Linux GTK/WebKit
development dependencies. Keep temporary files and build output under `/home`.

```sh
export TMPDIR=/home/USER/tmp
mkdir -p "$TMPDIR"
bun install --frozen-lockfile
bun run build:server
bun run build:factory-ui
bun run build:desktop-ui
bun run typecheck
bun run check:boundaries
bun run build:storybook
cargo check --locked --workspace
```

The shared browser module is `.artifacts/factory-ui/factory-app.js`, with its CSS,
fonts, portraits and notices alongside it. Both hosts consume the defining source
in `frontend/factory/`. Storybook uses those production components directly.

To stage the native development app, supply a native PostgreSQL payload as
described in [the runtime guide](lib/desktop-runtime/README.md):

```sh
bun run build:desktop --postgres /absolute/postgresql-payload --out /home/USER/factory-stage
/home/USER/factory-stage/soda-desktop
```

This is a development stage, not an installer. The app currently opens its shared
disconnected screen; readiness alone does not establish a provider connection or
authorize work. [The desktop guide](cmd/soda-desktop/README.md) describes lifecycle,
credential custody and opt-in native acceptance checks.

## Source and integration history

[Source provenance](docs/source-import.json) records the original revisions and
SHA-256 hashes of imported product files. The `integrations/` directory also
preserves the complete tracked and untracked source changes from both working
trees as Git binary patches. These include unfinished integration code and
existing human work; the original working trees have not been reset or committed.
They are recovery snapshots, not a second active implementation of Factory.

Fountain server implementation belongs to Fountain. Its retained patch and
license remain under `integrations/fountain/`; they are not dependencies of the
standalone backend. See [integration notes](integrations/README.md).

Original SodaOS work remains Apache-2.0. Existing third-party and inherited
notices remain applicable; see [LICENSE](LICENSE), [NOTICE](NOTICE), and the
licenses alongside the imported integration material.
