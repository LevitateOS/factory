# Factory

Factory is a Linux-first desktop application and Go workflow engine with a shared
Lit frontend for the native Tauri host and Soda Forge/Fountain.

This repository imports the implementation in progress from SodaOS. It is not a
completed or released desktop product. Connected GitHub sign-in, account/repository
binding and human issue/PR reads and writes have recorded qualification. Actual
agent execution with a real provider, automatic intake/PR delivery and installed
distribution remain unfinished. The dependency-ordered backlog is in
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
fonts, portraits and notices alongside it. The defining source lives in
`frontend/factory/`, and Storybook uses those production components directly.
Forge imports this source through the standard `@factory/product` Bun link;
its local component and Storybook copies have been removed. From the Forge
checkout, run `bash scripts/setup-factory-ui.sh ../factory` to use this editable
checkout. Both source builds then read the same files, and Forge's browser watcher
includes the linked component/design tree. CI can omit the checkout argument to
fetch the published Factory commit selected by Forge's `go.mod`.

To stage the native development app, supply a native PostgreSQL payload as
described in [the runtime guide](lib/desktop-runtime/README.md):

```sh
bun run build:desktop --postgres /absolute/postgresql-payload --out /home/USER/factory-stage
/home/USER/factory-stage/soda-desktop
```

This is a development stage, not an installer. The desktop shell supports GitHub
device sign-in, verification, native encrypted credential storage and saved-account
removal. Click **Sign in to GitHub**, open the verification page in your browser
and enter the displayed code. Choose a saved account and repository to connect the
shared application. Signing in alone does not authorize execution: local resource
and workspace consent, policy, funding and run admission are separate requirements.
Native fixture execution is not proof of real provider inference.
[The desktop guide](cmd/soda-desktop/README.md) describes lifecycle,
credential custody and opt-in native acceptance checks.

## Source and integration history

[Source provenance](docs/source-import.json) records the original revisions and
SHA-256 hashes of imported product files. The initial extraction snapshots remain
in [Git history](https://github.com/LevitateOS/factory/tree/870c784024c4b194cc5f3bf503ccf37d12e36879).
The recovery patches and inactive coordinator-test archive have been retired
from the working tree. The Forge Go consumer cutover is committed as `a1cd0499`.
Forge now uses the pinned public Factory Go module and the shared UI source
package (UI consumer commit `5e695dfb`). Unrelated staged work remains preserved
in the original checkout. Forge's native consumer cutover is `6bb9b60d`: its
retained native consumers use this repository's pinned Git crates, and its
duplicate desktop/runtime/workbench/transport owners are removed. Connected
product journeys, SDK distribution and installed parity remain unfinished.
Fountain server implementation belongs to
[Fountain](https://github.com/veighnsche/fountain/tree/main), and appliance
composition belongs to [SodaOS](https://github.com/LevitateOS/sodaos).

Original SodaOS work remains Apache-2.0. Existing third-party and inherited
notices remain applicable; see [LICENSE](LICENSE), [NOTICE](NOTICE), and the
licenses alongside the retained source and assets.
