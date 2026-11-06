# Factory validation

Choose the smallest existing check that exercises the changed behavior. All
scratch, profiles, build outputs and caches belong under `/home`; set `TMPDIR`.
Preserve retained profiles and evidence. The [freshness policy](freshness.md)
applies to release qualification; cached builds establish only their actual scope.

## Source and UI

```sh
bun run typecheck
bun run check:lit
bun run check:lit --fixtures
bun run check:ts-unitban
bun run check:boundaries
bun run build:server
```

`typecheck` runs the three TypeScript compiler configurations. Lit analysis and
its positive/negative fixtures are separate commands. The shared UI's existing
interactive scenarios live in `frontend/storybook`; `bun run check:desktop-ui`
and `bun run check:desktop-diff` build them and run the native UI checker. They
require a working native display environment. Component fixtures do not qualify
a connected account, provider, or installed product.

## Real integrations

Existing Go MCP checks run with `go test ./pkg/factory/mcpserver`; they exercise
the SDK subprocess and local HTTP admission. Store fixtures in `pkg/store`
require real disposable PostgreSQL and explicit case inputs. Native executables
in `tools/native-checks` and `tools/pg-fixture` own the composed checks, including
desktop provider operations and PostgreSQL restart/recovery. Use their existing
case entrypoints and owned disposable profiles; do not run against retained
accounts or databases just to check a refactor.

Build a desktop stage with explicit native PostgreSQL input:

```sh
bun run build:desktop --postgres /ABS/PATH/TO/postgresql --out /ABS/PATH/TO/stage
```

Add `--check` only for the native-check variant. The stage contains the actual
Cargo-produced executable, Go server and supplied PostgreSQL payload. A local
stage build does not qualify installation or security behavior.

## Completion evidence

The [canonical plan](factory-products/implementation-tasks.md) owns outstanding
product and qualification work. Report what ran, retained result location,
limitations and the next dependency once. Keep connected and installed gates
open until their actual journeys pass. Follow `AGENTS.md` and the
[unit-test policy](why-no-unit-tests.md); reuse existing integration/native checks.

[Predecessor validation guidance](history/predecessor-testing.md) is retained as
history. Its appliance paths, CI workflows and commands are not Factory commands.
