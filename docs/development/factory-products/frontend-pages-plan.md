# Front-end pages: implementation plan

Status: proposed on 2026-11-25. Nothing in this plan is implemented. It turns the
reviewed designs into dependency-ordered slices and names the cheapest model that
should be able to finish each one.

Designs: <https://claude.ai/artifact/PqHcR4ENgouTMcfES9bDsC> (private to the
owner until shared). Each slice below names its artboard. An implementing agent
can read an artboard's exact markup and inline styles with the Artifact tool
(`read`, path `project/<Name>.dc.html`); the artboards are mock-ups in a
different component format, so they are a visual reference, not code to copy.

This plan does not replace
[implementation-tasks.md](implementation-tasks.md). It is not linked from there
yet.

## Rules every slice follows

- Work on `main`. One slice per commit. No new unit tests.
- Lit owns the UI, Go owns product APIs and durable state, Rust owns native
  execution and credentials. A UI slice that turns out to need a backend change
  stops and reports; it does not add the backend change itself.
- A feature whose backend is not ready renders the shared "unavailable" state.
  It never assumes the call works.
- Verify with the smallest real check:
  - every UI slice: `bun run typecheck`, `bun run check:lit`,
    `bun run check:boundaries`, `bun run build:storybook`
  - a slice that changes the app frame or anything the desktop loads:
    `bun run check:desktop-ui` as well, because
    `cmd/soda-desktop/src/check.rs` depends on `soda-factory-app`
  - Go and Rust slices: the existing integration or native check for the owner
    being changed
- Request amplification is a defect. Any slice that reads across several
  projects must be served from the local ledger with a bounded request count,
  proven with controlled provider responses before a connected attempt.

## Choosing a model

API list prices per million tokens, as cached on 2026-11-03:

| Model | Input | Output | Relative to Sonnet |
|---|---|---|---|
| Haiku 5.5 | $0.10 | $0.50 | 1/20 |
| Sonnet 5.5 | $2 | $10 | 1 |
| Opus 5.5 | $4 | $20 | 2 |
| Fable 5.2 | $8 | $40 | 4 |

What each tier is used for here:

- **Haiku 5.5:** work that is fully specified and stays in one place: Storybook
  fixtures and stories, copy changes, applying an already-written style to more
  elements. Not for anything that touches the `live()` guards, abort signals,
  ports or route admission.
- **Sonnet 5.5:** the default implementer. Lit render functions and CSS against
  a finished design, new presentational components that copy an existing
  pattern, small type plumbing.
- **Opus 5.5:** slices that cross Go, Rust and Lit, or that change a contract:
  MCP tool schemas, durable workflow state, route admission and host ports,
  connection and sign-in lifecycles, anything where request counts matter.
- **Fable 5.2:** two short design reviews only (marked below). No
  implementation.

Escalation rule: judge cost per finished slice, not per token. If a slice fails
its verification once on the assigned model, hand the same slice to the next
tier with the failure output. Do not retry on the same tier.

These picks are judgment, not measurement. After the first three slices, compare
what each actually cost and adjust the rest.

## Phase 0: foundations

No backend work. Everything later depends on these.

| # | Slice | Where | Model | Notes |
|---|---|---|---|---|
| 0.1 | Control styles in the standalone envelope: buttons, primary buttons, inputs, selects | `frontend/factory/styles/base.css` | Sonnet | `base.css` is imported only by the native app and Storybook. Fountain themes `.ui.button` itself, so the ~42 `ui button` elements are browser defaults in the native app today. Zero-specificity selectors, so component rules keep winning. Layout, sidebar and panel styles belong to 2.1. |
| 0.3 | Storybook stories for the pull request list and detail | `frontend/factory/pulls/`, `frontend/storybook/` | Haiku | Only "Pull comment" has a story today. Copy the issues stories' shape. |

Slice 0.2 (one shared "unavailable" presentation) was dropped from this phase
on 2026-11-25. The activity and repair panels are not mounted in the native
app yet, so the helper would have had one consumer. It moves into 3.2 (which
mounts activity and repair on Work) and 3.4 (Watch). Follow the existing
pattern there: the host declares the capability unavailable, as
`frontend/desktop/product.ts` already does with `watchAvailable: false`, and
the component renders the unavailable state without sending the request. Do
not classify every 503 as "not in this app"; Storybook already uses 503 for
offline states.

## Phase 1: Feed

The centerpiece. Uses data the feed already receives. Artboards: Feed (page 1)
and the eight sheets on page 5.

| # | Slice | Where | Model | Notes |
|---|---|---|---|---|
| 1.1 | Timeline layout: portrait gutter, byline, card, tucked reply drawer | `feed/feed-view.ts`, `styles/feed.css` | Sonnet | `feed.css` is 3,566 lines. Change the shell rules only; leave per-type rules for 1.3. |
| 1.2 | "Needs me" rail from the existing attention view | `feed/feed.ts`, `feed/feed-view.ts` | Sonnet | The "On this project" roster needs data the feed does not have. Leave it out until 2.5a exists. |
| 1.3 | Restyle each existing post type to its sheet | `feed/feed-actions.ts`, `feed/feed-record-actions.ts`, `feed/feed-communication.ts`, `shared/jev-answer.ts`, `styles/feed.css` | Sonnet | One commit per sheet (asks, progress, problems, results, decisions, run records). Rendering logic stays; this is markup order and CSS. |
| 1.4 | Fixtures and stories for every post type in 1.3 | `frontend/storybook/` | Haiku | One story per specimen on the sheets, so each can be reviewed against its artboard. |
| 1.5 | Question and suggestion outcomes: "reply received, then incorporated", applied, refused | `feed/feed-actions.ts`, `feed/feed-communication.ts` | Sonnet | `questionState` and `disposition` are already in the feed response. |
| 1.6 | Issue Maker identity (initials, edge colour) | `feed/roles.ts`, `styles/feed.css` | Haiku | No portrait or colour exists in the brand assets. Needs an owner decision on the colour first. |
| 1.7 | Decision owner line | Go feed read model, `feed/feed-response.ts`, `feed/feed-actions.ts` | Sonnet | The Go struct carries `decision_owner`; the TypeScript parser does not admit it yet. |

## Phase 2: shell and projects

Artboards: Projects (page 1); First run, Accounts, Settings, Sidebar, Jump to
(page 2).

| # | Slice | Where | Model | Notes |
|---|---|---|---|---|
| 2.1 | App frame: replace the header and button row in `soda-factory-app` with the sidebar, for one project | `app/factory-app.ts`, `styles/base.css` | Sonnet, reviewed by Opus | Shared by the native app and Storybook, and checked by the Rust native check. Run `check:desktop-ui`. |
| 2.2 | Settings as a routed page composing the existing role settings, workspace setup and verification limits | `app/routes.ts`, `app/factory-app.ts`, `work/role-settings.ts`, `work/workspace-setup.ts` | Opus | Adds a route kind and a host port. Execution controller lives in `desktop/placement.ts` as plain DOM code and has to become a component first. |
| 2.3 | Accounts as Lit components: Muse, GitHub, Fountain (shown unqualified) | `frontend/desktop/muse-login.ts`, `accounts.ts`, `fountain-login.ts`, `index.html` | Opus | These drive device-code sign-in through the Rust bridge. Behaviour must not change; only the presentation moves. |
| 2.4 | First run, sequencing the 2.3 components | `frontend/desktop/` | Sonnet | After 2.3. Ends on Projects once 2.5c exists; until then it ends on the Feed. |
| 2.5 | Project list. Three slices, in order: | | | See the design review below before starting. |
| 2.5a | Go: `GET /api/factory/projects`, a per-project summary for the repositories this profile's connections have opened (name, dispatch state, roles working, needs-you count, latest post) | `service.ProjectSummaries`, mounted only in `BindNativeProduct`; PostgreSQL read | Opus | See the decision below. One SQL statement; Go owns the single needs-you definition. |
| 2.5b | Rust and desktop host: merge the Go rows with saved-account state and the current binding; open a project here (`choose`) or in a new window (`open_window` taking account and repository) | `cmd/soda-desktop/`, `frontend/desktop/connections.ts`, `product.ts` | Opus | Each switch costs two product requests, so opening stays a user action. |
| 2.5c | Lit: Projects page and the sidebar project list, through an optional `projects` port on `FactoryAppContext` | new `frontend/factory/projects/`, `app/factory-app.ts` | Sonnet | Without the port the frame keeps the single-project sidebar. Stories for each row state (Haiku can add the fixtures). |

**Decision from the 2.5 design review (2026-11-25, Fable 5.2, read-only).**
- **Source.** Not the feed subscription: it is keyed to one actor (`pkg/factory/service/feed.go:35-51`) and the desktop never mounts the global feed. Use `collaboration_repository_connections` joined to `collaboration_connections.profile` (`pkg/store/collaboration_schema.go:22-23`), which records the repositories this profile's connections opened.
- **Authority.** Native visibility deliberately hides every repository except the selected one (`pkg/factory/runtime/native_product.go:297-311`). The list is a retained, profile-scoped read with its own named port; do not widen current visibility. Rows can be stale, so each carries an "as of" time.
- **Fields.** `repository`, `name` (Go resolves display names from the ledger), `connection`, `dispatch` (`open`, `closed` or `none`, from `Store.DispatchState`), `working` (up to 8 role and issue pairs, from `RepositoryDispatchCounts`), `needs_you` (capped integer), `latest_post`.
- **Needs-you.** Today it is derived partly in the client (`feed-thread.ts`) and partly in the store. Go must own one definition (open asks plus delivery judgments awaiting the person) so the count agrees with the feed's Needs me rail; the client then reads it rather than deriving its own.
- **Request cost.** Every native product request starts with one provider observation (`pkg/factory/runtime/native_collaboration.go:187-198`), so each list fetch costs one provider call regardless of project count. Fetch only on connect, on opening Projects and on an explicit Refresh; no polling. 2.5a must prove the count with controlled provider responses before any connected attempt.
- **Host port**, optional on `FactoryAppContext`: `projects?: {list(signal), open(key, 'here' | 'window'), reconnect?(key), disconnect?(key), signIn?(key)}`. Rows hold a host-opaque key, name, account label, provider, connection (`current`, `connected`, `not_connected`, `sign_in`), an optional summary and `asOf`. The shared UI never sees connection ids or sessions.
- **Stays unavailable for now:** Fountain projects, "Remove from list", "Not set up" as its own state, adding a project beyond the existing find form, live refresh, and rows for repositories no saved account can bind.
- **Not determined:** whether Rust caches the per-request selection observation, and whether Go can serve the list with no usable credential (`cmd/soda-desktop/src/product.rs:118-123` requires a capability). 2.5a and 2.5b settle these first.
| 2.6 | Jump to (Ctrl+K) | new component, `app/factory-app.ts` | Sonnet | After 2.5c. Keyboard behaviour needs a direct OS input check, not only a semantic UI check. |

**Design review before 2.5 (Fable 5.2, one short session):** decide how one
window lists several projects when the shell connects one repository per window.
Output is a decision and the port shape, not code.

## Phase 3: work and repository pages

Artboards: pages 3 and 4. Each is a restyle of an existing routed view. They are
independent of each other and of Phase 2 after 2.1.

| # | Slice | Where | Model |
|---|---|---|---|
| 3.1 | Request | `request/request-view.ts` (562 lines; `request.ts` logic stays) | Sonnet |
| 3.2 | Work, including the plan table and dispatch panel; mount activity and repair with host-declared availability and the shared unavailable presentation (absorbs old 0.2) | `work/work-view.ts`, `work/progress-view.ts`, `work/activity.ts`, `work/repair-funding.ts`, `frontend/desktop/product.ts`, one shared render helper | Opus (host port), then Sonnet (views) |
| 3.3 | Work detail, including journey strip and run facts | `work/detail.ts`, `work/journey-view.ts` | Sonnet |
| 3.4 | Watch, unavailable and live states, using the shared unavailable presentation from 3.2 | `watch/watch-view.ts` | Sonnet |
| 3.5 | Issues list and issue detail | `issues/issues.ts`, `issues/detail.ts`, `styles/issues.css` | Sonnet |
| 3.6 | Pull request list and detail | `pulls/list.ts`, `pulls/detail.ts` | Sonnet |
| 3.7 | Knowledge | `library/library.ts` | Sonnet |
| 3.8 | Fixtures for any state added in 3.1 to 3.7 | `frontend/storybook/` | Haiku |

## Phase 4: backend gaps that turn "unavailable" into working

Independent of the UI phases. Each one is a Go or Rust slice; the Lit change
afterwards is removing a fixture state, which Haiku can do.

| # | Slice | Model |
|---|---|---|
| 4.1 | Bind the repository activity and repair-funding stores on the native host (both return 503 today) | Opus |
| 4.2 | Mount the run-output WebSocket so Watch can attach | Opus |
| 4.3 | Deliver feedback for feed posts judged "existing work" | Opus |
| 4.4 | Confirm artifact download end to end through the desktop bridge | Sonnet to write the check; Opus if it fails |

## Phase 5: new capabilities

All proposed. None should start before the owner decision listed with it.
Artboards: page 6, plus "Proposed verbs".

| # | Slice | Model | Decision needed first |
|---|---|---|---|
| 5.1 | Typed questions: pick one, yes or no, score, pick several, value | Opus for the MCP schema, store and feed read; Sonnet for the Lit renderer | Whether these render natively from typed data. They can, and that avoids agent-written HTML entirely for questions. |
| 5.2 | Assumption challenge card in the feed | Opus (Go read and answer command), Sonnet (Lit) | None; the backend already records these. |
| 5.3 | Addressed questions | Opus, Sonnet | Whether a question may name a person or only a role. |
| 5.4 | Agent requests for allowance and for access | Opus, Sonnet | Whether a post may carry Allow and Decline, or only link to the setting. |
| 5.5 | Retract or correct a post | Opus, Sonnet | Whether the original stays visible. |
| 5.6 | "Read first by" on the composer | Opus | Changes how JEV routes posts. |
| 5.7 | Interactive HTML posts ("show") | Fable review, then Opus | See below. |

**Design review before 5.7 (Fable 5.2):** agent-written HTML running inside a
desktop app that holds credentials. The review fixes the sandbox contract: no
network, no access to the host bridge, answer-only output, a required written
fallback. This is ordinary product security engineering; involve Daybreak only
if the review itself turns into exploit work.

## Order and what can run in parallel

1. Phase 0, in order.
2. Phase 1 and slice 2.1 can run at the same time, as long as two sessions do
   not edit `styles/feed.css` or `app/factory-app.ts` together.
3. Phase 3 slices can each run in their own session after 2.1.
4. Phase 4 can start any time; it shares no files with the UI phases.
5. 2.2 to 2.6 are sequential except that 2.5a and 2.5b can overlap.
6. Phase 5 last, one capability at a time.

Sessions that write code must not share files. `styles/feed.css` and
`app/factory-app.ts` are the two files most slices want; treat each as owned by
one session at a time.

## Brief template for an implementing session

```
Slice: <number and name> from docs/development/factory-products/frontend-pages-plan.md
Read AGENTS.md first.
Design: artboard <Name> in the canvas (read project/<Name>.dc.html for exact values).
Files: <the Where column>. Do not edit other files; report if you need to.
Done when: the listed states render in Storybook to match the artboard, and
  bun run typecheck, bun run check:lit, bun run check:boundaries and
  bun run build:storybook pass. <add check:desktop-ui if the frame changed>
If the slice needs a backend change, stop and say what is missing.
Commit the slice on main when verified.
```

## Slice count by model

| Model | Slices |
|---|---|
| Haiku 5.5 | 0.3, 1.4, 1.6, 3.8, the follow-ups in Phase 4 |
| Sonnet 5.5 | 0.1, 1.1, 1.2, 1.3, 1.5, 1.7, 2.1, 2.4, 2.5c, 2.6, 3.1, 3.2 views, 3.3 to 3.7, 4.4, the Lit half of Phase 5 |
| Opus 5.5 | 2.2, 2.3, 2.5a, 2.5b, the 3.2 host port, 4.1, 4.2, 4.3, the review of 2.1, the backend half of Phase 5 |
| Fable 5.2 | two design reviews (before 2.5 and before 5.7) |

## Not verified while writing this plan

- Whether the Fountain host loads `soda-factory-app` or only individual
  components. The search found the desktop shell, Storybook and the Rust native
  check as consumers. If Fountain loads the frame, 2.1 needs Opus, not Sonnet.
- The shape of the Go read that 2.5a would add. No existing endpoint returns
  per-project counts for projects that are not open.
- How large slice 1.3 really is. It is listed as one slice per sheet, but
  `feed.css` may make some sheets two commits.
- No cost totals are given, because token use per slice is unknown until the
  first few are done.

## Decisions made on 2026-11-25

The owner gave standing permission to finish every phase. These decisions were
made under it, chosen to change behaviour as little as possible and to grant
nothing from a post:

1. **Issue Maker identity (1.6):** no portrait art exists and none can be made
   here, so Issue Maker gets a role colour `#8a5a2b` (umber, distinct in
   lightness and hue from the other ten) added to the palette and shows its
   initials "IM" as its portrait until art exists.
2. **Fountain sign-in (2.3):** stays enabled, marked "Not qualified yet".
3. **"Existing work" feedback (4.3):** fixed, not left as a displayed gap.
4. **Typed questions (5.1):** rendered natively by Lit from typed data. No
   agent-written HTML for questions.
5. **Addressed questions (5.3):** a question may name a role, not a person.
6. **Allowance and access requests (5.4):** the post explains what is asked
   and why and links to the setting. It carries no Allow or Decline control;
   granting stays in Settings.
7. **Retract or correct (5.5):** the original stays visible, marked withdrawn,
   with a link to the correction.
8. **"Read first by" (5.6):** recorded on the post as a routing hint. JEV
   still classifies the post; the hint only invites the named role.
9. **Interactive HTML (5.7):** proceeds only on the terms the Fable review
   sets; if the review finds the sandbox cannot be made safe in this app, the
   capability stays unavailable.

**Result of the 5.7 review (2026-11-25, Fable 5.2, read-only): 5.7 stays
unavailable.** Three findings block it today:
- The desktop CSP (`cmd/soda-desktop/tauri.conf.json:7`: `script-src 'self'`,
  `frame-src 'none'`) stops agent script in any `srcdoc`, `blob:` or `data:`
  frame, and loosening it with `'unsafe-inline'` would weaken the one document
  that holds the IPC bridge.
- On Linux, wry stamps IPC messages with the top-level URI and the app's
  `caller()` check (`cmd/soda-desktop/src/main.rs:393-401`) is top-level too,
  so the invoke key is the only barrier between a frame and 43 commands.
- No storage path exists: communications cap at 8 KiB and artifacts exclude
  `text/html`.

A later slice must meet this contract before anything ships, even behind a
flag. First, a frame-origin check in `caller()`. Then delivery from a dedicated
read-only scheme (never `srcdoc`) with a `sandbox allow-scripts;
default-src 'none'` CSP header, with the app CSP changing only `frame-src`. The
iframe gets `sandbox="allow-scripts"` and never `allow-same-origin`, runs on
click, one live frame at a time. Messages go one way: the frame may send only
`{v:1, type:"answer"|"height"}`, checked against `event.source` and origin
`"null"`, and an answer only pre-fills the composer for the person to send.
Bytes go through `publish_artifact` as a new capped `text/html` type with a
required summary that always renders. Residual risks are CPU freezes, spoofing
inside the box and WebRTC leaks; Windows, macOS and Fountain's own CSP were
not assessed.

## Decisions the owner still has to make

None open. The owner may revisit any decision above; each is recorded with its
slice so it can be changed in one place.
