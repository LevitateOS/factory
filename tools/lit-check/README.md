# Required Lit analysis

`bun run typecheck` checks the product compiler boundaries and this tool with TS7.
Run `bun run check:lit` for product analysis and `bun run check:lit --fixtures`
for its existing positive/negative fixtures. The analysis compiler and
analyzer are development dependencies in this workspace and the single root lock.
Use `bun install --frozen-lockfile --ignore-scripts`; no Node process is required.

## Why the small build adapter exists

Upstream lit-analyzer omits TypeScript from its runtime dependency graph. In Bun's
isolated workspace layout, its bare `require('typescript')` falls back to the root
TS7 package, which lacks the classic API. A workspace dependency or package alias
alone does **not** fix this. Preserving symlinks instead loses the analyzer's other
isolated dependencies. Both failed approaches were executed and retained.

`scripts/check-lit.ts` builds an ignored, development-only runner. Every compiler
import in the analyzer, web-component-analyzer and ts-simple-type is resolved to
this workspace's installed classic compiler (currently the final JavaScript API
channel, TypeScript 6.0.3). The product remains on current TS7; changing this
analyzer's compiler to TS7 requires an upstream API replacement. Other packages stay external in their
installed directories (the CSS language service has relative runtime requires).
This is a normal tool build, not a patch/fork of their source or a node_modules edit.
The runner asserts the analyzer context's *default* compiler version, the tool's
resolution and the unchanged product compiler before analysis. Its declaration-only
paths mapping applies solely to this tool's TS7 check, not browser/Cockpit checking.
A new checkout builds its own runner; the artifact contains local absolute paths
and must never be distributed. Fresh frozen-lock installation was exercised in an
isolated ignored directory, without package lifecycle scripts.

## Coverage and limitations

The actual `tsconfig.browser.json` source inventory drives analysis, including
stateless template functions; empty input or missing sources fail. Strict preset
plus explicit `no-unknown-event` applies. Every diagnostic, including warnings,
fails the source check. Exceptions/crashes are fatal. Positive static-properties /
`declare`, nested typed templates and `repeat` authoring must pass; ten separate
negative fixtures each assert their expected rule, so another error cannot hide a
broken rule. Negative fixtures are intentionally outside the product compiler input.

Callable event handlers with the wrong event parameter kind remain an upstream
checker limitation. Typed helper arguments and real keyboard/pointer/command tests
complement this gate; it is not TSX-equivalent checking or native terminal proof.

The browser build rejects imports of analysis packages and this tool. Native build
input metadata retains this package manifest alongside the root lock, but no analyzer
executable, compiler or runner is staged in the browser/appliance runtime payload.
