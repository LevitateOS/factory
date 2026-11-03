# Shared diff renderer

`diff.ts` defines the production `<soda-factory-diff>` Lit component. Both hosts
and Storybook consume this source. It uses the vanilla `@pierre/diffs` 1.6.2 API,
local Shiki JavaScript highlighting and a bounded virtualized viewport. There is
no React dependency, worker pool, remote asset loader or second renderer.

Pass a decoded `ChangedFile`, normalized `ReviewThread[]`, the host's light/dark
scheme and its controlled `ReviewDraft | null`. Selection emits
`factory-review-selection` with the canonical immutable `ReviewAnchor`.
`factory-review-draft-change` emits the next draft or null; the host stores it and
updates `.draft`. The component performs no provider writes and holds no credentials.

The host owns draft persistence under its profile/account/host/repository scope.
It must not transfer a draft to another identity or replace its original commits,
paths, side or inclusive range after a force-push. The component retains controlled
drafts outside the virtualized renderer, refuses to overwrite a nonempty draft
when another selection is made, and labels drafts belonging to another revision.
T08's mutation service must recheck current permissions and exact reviewed heads.

Mouse selection and the keyboard-accessible side/first/last-line form use the same
anchor conversion. Cross-side, missing and out-of-patch ranges are refused and
clear the old selection. Programmatic selection updates suppress renderer callback
notifications to avoid feedback. File comments use explicit file anchors.

Complete patches render as split or unified diffs. GitHub-style hunk-only patches
receive a git header with canonical quoted paths. A patch must describe exactly
one matching file. Invalid/mismatched patches do not create line-comment targets.
Binary, truncated and unavailable patches remain distinct states; absent content
never becomes an empty successful diff. Added/deleted/renamed files retain their
original path pair and old/new line coordinates.

Current inline annotations use the thread's original anchor. A provider-observed
relocation never replaces that original identity. Threads for a different revision
remain in an original-thread disclosure instead of being placed on the new head.
Threads with null original anchors remain visible in a recorded-location
disclosure, including their available comments. Recorded coordinates are not
promoted to an invented original diff. T06-FOUNTAIN-THREADS verified this case
in the actual Linux Tauri/WebKit workbench, alongside its existing input and
renderer checks. The current annotation body is safe plain text; complete conversation/Markdown,
review submission and resolution experience belongs to T08.

Disconnect and renderer replacement dispose the Pierre instance and virtualizer.
The controlled draft remains the host's property. No compatibility wrapper or
renderer-choice framework is introduced.

## T00-DIFF verification

Fresh npm metadata selected 1.6.2; official crate-index metadata reconfirmed Tauri
2.12.5 and tauri-build 2.7.4 before the native build. The upstream Apache-2.0
license is retained in `system/licenses/pierre-diffs-LICENSE`; T14 owns complete
installable-package notices, including transitive dependencies.

Actual browser checks ran the production component on a disposable Fountain
15.0.10-fountain.4 login page, including its real CSS and response policy. That
page sends no CSP header. A second run imposed self-only scripts/connections,
no workers, no eval/WASM permission and local/inline styles. It needed no policy
relaxation on Fountain. The fixture values are explicitly renderer fixtures;
there were no authenticated provider reads or mutations.

Both browser runs passed old/new multiline anchors, string IDs above `2^53`,
actual gutter drag selection, keyboard selection, rejection clearing, escaped
inline text, historical threads, split/unified switching, 6,000-line virtualization
(100 rendered line nodes at the observed end), draft retention after scrolling
and force-push, binary/partial/unavailable/add/delete/hunk-only/malformed states
and disposal of an active renderer.

The actual Linux Tauri/WebKitGTK workbench passed all 13 native checks. A private
headless Cage session and wtype 0.5 supplied trusted keyboard events: old-side lines
2–3 and Unicode draft text `Native review café`. Native checks also covered new-side
anchors, layout, virtualization, revision pinning, file states, safe threads,
invalid patches and disposal. This proves input in the native webview, not merely
JavaScript-dispatched keyboard events. No provider/credential/executor was exposed.

The native fixture used user-extracted GTK/compositor packages and private mount
bindings for their compiled helper paths. Missing `/dev` initially prevented JSC
entropy initialization; the fixture was corrected with `/dev` and `/proc` mounts.
Sandbox-disabling experiments did not fix it and were removed. Programmatic
selection notification and Lit class-field shadowing failures were fixed in the
component, and the hunk-only path header was corrected. No upstream patch or
second library was needed. Cage/Xwayland software-rendering/keymap warnings did
not prevent the passing Wayland keyboard checks.

Full TS/Lit/source guards, Oxlint, formatting, Storybook and native build pass.
`bun run dev:ui` built/staged assets but could not activate them: there is no active
development deployment. No deployment was created. The actual Fountain fixture
above is a separate local renderer check, not a claim of an installed extension
or connected review journey. Those remain T08/T13.

Logs: `/home/vince/factory-diff-browser.log`,
`/home/vince/factory-diff-native.log`, `/home/vince/factory-diff-native-driver.log`,
`/home/vince/factory-diff-native-build.log`, `/home/vince/factory-diff-storybook.log`,
`/home/vince/factory-diff-typecheck.log` and `/home/vince/factory-diff-lint.log`.
Fountain response evidence: `/home/vince/factory-diff-fountain-headers.txt`.
The browser and native orchestration diagnostics stay outside the repository at
`/home/vince/factory-diff-browser.ts` and `/home/vince/factory-diff-native-run.ts`.
The reusable native webview check is `frontend/storybook/diff-native-check.ts`.
No unit tests or new test-runner files were added.
