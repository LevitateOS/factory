# Native Factory component workbench

Run the production Lit stories in a Linux Tauri window. This development tool
checks the shared frontend independently of Fountain. The production desktop
host lives in [`cmd/soda-desktop`](../../cmd/soda-desktop/README.md); local agent
execution and account connections remain later slices. This workbench uses the
clearly labelled Storybook data.

```sh
bun run dev:desktop-ui
```

Build requires the [Tauri Linux prerequisites](https://v2.tauri.app/start/prerequisites/#linux)
including GTK3 and WebKitGTK 4.1 development packages, and a working graphical
session. Use Bun and the repository Rust workspace. It bundles the Storybook
output locally and does not load a remote site with native permissions.
The scripts enable the `native-ui` Cargo feature explicitly, so ordinary workspace
builds do not acquire GTK/WebKit development requirements.

```sh
bun run check:desktop-ui
```

The check opens an actual native webview, waits for the production feed to render,
and verifies its light and dark backgrounds before exiting. It is an integration
check of the built assets, not a mock renderer. Headless CI needs a real compositor
such as Weston; merely building the command does not prove native rendering.

`bun run check:desktop-diff` runs the production diff story in the native webview.
It waits for real keyboard input: select old-side lines 2–3 with the line form,
then enter `Native review café` in the focused review draft. The remaining checks
run automatically, including virtualization, pinned drafts and active disposal.
The 13 checks are reported before the workbench exits. Headless automation needs
a keyboard-capable compositor and input client; a seatless Weston session only
proves rendering. See the [renderer guide](../../frontend/factory/review/README.md)
for the actual Linux input evidence and fixture constraints.

The global Tauri API and relaxed Storybook CSP belong only to this development
workbench. It exposes rendering-report/input-stage commands and no credential, shell,
filesystem or Factory operations. Production desktop permissions/CSP must be
defined for that application's actual bridge.
