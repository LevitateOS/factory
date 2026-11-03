# What to reuse, adapt and build

Selections support the [implementation plan](ideal-filetree-plan.md). Prefer a
maintained dependency for a focused mechanism, preserve existing Soda behavior,
and write the product-specific coordination once. Do not import another product's
framework, account model or entire process tree to obtain one widget.

## Existing source is the first donor

| Donor | Take | Keep out of the portable product |
| --- | --- | --- |
| Soda Lit feed and browser fixtures | Actual paging, replies, composer, pending commands, idempotency, state rendering, portraits and conversation behavior | Fountain mount types, provider URL construction, Forge Project/application inventory assumptions |
| Factory domain/control/store | Accepted-input lifecycle, assignment journal, capacity/sponsorship limits, reconciliation, publication/review/correction states | SDK snapshots/receipts and appliance runtime provenance in shared contracts |
| Fountain connector | Conditional operation semantics, retained lookup/cancellation, current native authority verification | Private admissions, socket/file paths and credential contents in desktop |
| Rust host and provider tools | Git/candidate validation, receipt ownership, bounded output/deadlines, supported provider launch semantics, verified executable selection | Appliance Podman/systemd/cgroup/root assumptions in native host execution; qualify ELF separately from later macOS bundles |
| Soda theme/fonts and current issue/PR presentation | Palette, typography, layout, workflow states, keyboard/permission behavior and acceptance examples | Native document selectors, jQuery/HTMX/form/CSRF lifecycle as shared component dependencies |

Adapted Forgejo templates have GPL-3.0-or-later provenance; the repository's root
Apache license does not erase it. Preserve the existing subtree's attribution.
Use the presentation as reference for new Lit components; any actual source
copy/adaptation must retain its applicable notices and be recorded in the existing
[licensing owner](../../research/licensing.md). Avoid copying whole native templates
into an Apache-labelled product package. This is a source selection constraint;
distribution licensing is checked against the actual selected files/dependencies.

## Selected upstream mechanisms

| Mechanism | Donor and verified license source | Concrete reuse and acceptance |
| --- | --- | --- |
| Native application shell | [Tauri](https://github.com/tauri-apps/tauri), [MIT option](https://github.com/tauri-apps/tauri/blob/dev/LICENSE-MIT) | Use window/menu/tray/dialog/lifecycle primitives and native [sidecar packaging](https://v2.tauri.app/develop/sidecar/). Rust owns a narrow native bridge. No generic webview shell/keychain API or privileged remote page. |
| Component workbench | [Storybook Web Components/Vite](https://storybook.js.org/docs/get-started/frameworks/web-components-vite), [MIT](https://github.com/storybookjs/storybook/blob/next/LICENSE) | Import production Lit components/styles. Controls drive fixture clients; stories cover actual state combinations. Prove Bun, TS7, Vite, Node/tooling requirements and repository source guards rather than assuming the CLI fits unchanged. |
| Diff rendering | [Pierre Diffs vanilla API](https://diffs.com/docs), [package source](https://github.com/pierrecomputer/pierre/tree/main/packages/diffs), [Apache-2.0](https://github.com/pierrecomputer/pierre/blob/main/packages/diffs/LICENSE.md) | Selected 1.6.2 in T00-DIFF after actual Fountain/CSP/Linux WebKitGTK/keyboard/anchor proof. One production Lit wrapper owns immutable review drafts/anchors and uses local JS highlighting and virtualization. No React or worker pool. [Evidence](../../../frontend/factory/review/README.md). |
| GitHub patch parsing | [Sourcegraph go-diff](https://github.com/sourcegraph/go-diff), [MIT](https://github.com/sourcegraph/go-diff/blob/master/LICENSE) | Selected v0.9.0 from official Go proxy metadata on 2026-11-02 for bounded provider hunk fragments. Check declared hunk lengths and provider addition/deletion totals after parsing; parser success alone does not prove completeness. Real public complete patches also pass independent `git apply --numstat`. Native/shared-renderer consumption remains downstream. |
| Comment formatting controls | [GitHub Markdown toolbar](https://github.com/github/markdown-toolbar-element), [MIT](https://github.com/github/markdown-toolbar-element/blob/main/LICENSE) | Framework-neutral custom element already used by Fountain. Reuse formatting/focus mechanics; shared Lit editor owns draft, preview, upload and submit state. |
| Markdown parsing | [Marked](https://marked.js.org/), [MIT](https://github.com/markedjs/marked/blob/master/LICENSE) | Parse Markdown once in the shared presentation. Marked does not sanitize; feed/issue/PR bodies remain untrusted content. Handle relative links, mentions and authenticated attachments through typed product context. |
| HTML sanitization | [DOMPurify](https://github.com/cure53/DOMPurify), [Apache-2.0 option](https://github.com/cure53/DOMPurify/blob/main/LICENSE) | Sanitize rendered Markdown with one explicit policy. No event handlers, native command URLs or remote privileged HTML. Reuse in all shared body views. |
| GitHub REST transport | [google/go-github](https://github.com/google/go-github), [BSD-3-Clause](https://github.com/google/go-github/blob/master/LICENSE) | T06-GITHUB-REST selects current v93.0.0 and go-querystring v1.2.0 on 2026-11-02. The bounded [Go client](../../../internal/github/README.md) uses REST 2026-03-10, explicit native transport and typed failure/paging facts. Soda retains authorization, operation identity, unknown outcomes and retry policy. No second JavaScript GitHub client in Lit. |
| Native credential storage | Linux: [secret-service](https://github.com/open-source-cooperative/secret-service-rs), MIT/Apache-2.0. Later macOS: [Apple native store](https://github.com/open-source-cooperative/apple-native-keyring-store). | T05-CREDENTIALS selects current secret-service 5.2.0 directly for encrypted Linux D-Bus sessions, attribute-scoped discovery and explicit locked/missing-service errors, with Rust crypto. The OS supplies its existing keyring. Rust exposes metadata and native-only finite token capabilities; the webview has status/withdrawal only. Real GNOME Keyring and native WebKit checks passed. Provider login remains T06/T10; the Mac store is deferred. |
| Public-client login primitives | [cli/oauth](https://github.com/cli/oauth), [MIT](https://github.com/cli/oauth/blob/main/LICENSE) | Candidate Go authorization helper if its supported public-client flow fits the chosen provider. Rust retains storage and browser integration. Do not build a new token parser from provider CLI private files. |

Read-only official metadata checked on 2026-10-31: Storybook 10.6.5; Tauri core
2.12.5 and CLI 2.12.4; go-github 93.0.0; Pierre Diffs 1.6.2; Marked 18.1.2;
DOMPurify 3.4.17. These are research observations, not pins, installed versions,
compatibility claims or a release baseline. Tauri's repository latest-release API
may name the CLI rather than the core. Resolve each component's current stable
channel and license at implementation time, then record the actual selected bytes.

Implemented selection: Storybook 10.6.5, Web Components/Vite adapter 10.6.5 and
Vite 8.3.5 are in `package.json`/`bun.lock`; Tauri 2.12.5 and tauri-build 2.7.4
are in the opt-in `tools/factory-ui` crate and `Cargo.lock`. Current official
metadata was checked before selecting these versions. The Linux workbench build
and actual WebKitGTK light/dark rendering pass. Other entries remain candidates,
not installed dependencies or qualified product behavior.

## Reference implementations, not product dependencies

[Postgres.app](https://github.com/PostgresApp/PostgresApp) is a directly relevant
native database packaging/lifecycle reference. Its
[source documentation](https://github.com/PostgresApp/PostgresApp#under-the-hood)
describes bundled PostgreSQL, per-user data and init/start/stop behavior under
the PostgreSQL License. Reuse that approach and bounded build knowledge in the
Rust supervisor, without importing its Swift UI, public-port defaults or full
extension bundle. Selected PG binaries/dependencies retain their own notices.
[PostgreSQL license](https://www.postgresql.org/about/licence/)

[VS Code's GitHub PR extension](https://github.com/microsoft/vscode-pull-request-github)
is useful for studying pending reviews, outdated threads, checkout/review flows
and account switching. Its [MIT source](https://github.com/microsoft/vscode-pull-request-github/blob/main/LICENSE)
can supply a bounded algorithm after checking its imports and notices. Its VS Code
APIs and webview application are not a shared Lit package. Do not copy its entire
controller model, editor host or authentication plumbing into Soda.

Fountain's `web_src/js/features/repo-diff.js`, native review templates and local
`repo/pulls`/`repo/diff` templates are behavior references. Their jQuery, HTMX,
server fragments and form session assumptions are exactly the coupling to remove.

[diff2html](https://github.com/rtfpessoa/diff2html),
[MIT](https://github.com/rtfpessoa/diff2html/blob/master/LICENSE.md), is the bounded
comparison/fallback if Pierre fails the required integration proof. Prefer its
patch parser with owned Lit interactions over wholesale injected UI. Choose one
renderer; do not build a permanent renderer abstraction or ship both.

GitButler is relevant product research, but its current
[Fair Source terms](https://github.com/gitbutlerapp/gitbutler#license) make it an
unsuitable default source donor for a competing product. Do not assume every
public desktop Git application's source is permissively reusable. Monaco/editor
shells also do not supply PR permissions, review state or collaboration semantics;
there is no editor requirement justifying that dependency in the initial viewer.

## Provider evidence that shapes the design

GitHub exposes [issues](https://docs.github.com/en/rest/issues),
[PRs and files](https://docs.github.com/en/rest/pulls/pulls) and
[review comments](https://docs.github.com/en/rest/pulls/comments). These are
separate provider resources; normalize their presentation, not away their meaning.
Preserve head/base SHA, comment side/range, original position and outdated state.

GitHub's [API practices](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api)
favor webhooks where available. A no-inbound desktop uses authenticated conditional
polling with appropriate scheduling, pagination and rate-limit backoff. Polling is
current-state reconciliation, not a durable historical event stream. Webhook mode
also needs reconciliation: GitHub does not automatically redeliver failed
deliveries. [Delivery behavior](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries)

GitHub's [app authentication model](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/about-authentication-with-a-github-app)
distinguishes app, installation and user identity. Desktop initial mode acts as
its connected user using a supported public-client flow. An installation/bot mode
must have a trusted source of installation tokens; bundling the product app's
private key is not an implementation option.

## What must be built here

Shared issue/PR screens; typed Factory client/navigation/catalogue/drafts; neutral
collaboration records and adapter translations; local session admission; native
execution/custody; private PG supervision; Fountain remote grant/gateway and
candidate transfer; GitHub durable mutation/reconciliation and policy-driven
intake; external PR adoption; actual two-host integration and native journeys.
None of the selected libraries supplies those product semantics.
