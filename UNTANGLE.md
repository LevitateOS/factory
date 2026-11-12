# Untangle log

## Scope

Source audit of all GitHub/Fountain egress owned by Factory: authenticated REST,
GraphQL, Git, OAuth, delegated host adapters, callers, UI, background work,
polling/recovery, setup tunnels and auxiliary tooling/build jobs. Factory is at
`9482c81aa452153e5f86ae49bcbd2559f484aac3`, on `main`.

Phase 1 only. No product code changes, provider requests, app launches, inference,
builds, tests or quota waiting. The owner explicitly selected **neither** optional
comment nor test cleanup. Preserve credentials, profiles, receipts, journals,
stashes and human text. No branch/worktree is created. Factory had no root log;
Fountain's existing independent `UNTANGLE.md` is untouched.

Scope includes the actual sibling appliance adapter interfaces used by Factory,
not every internal route of the whole Forgejo server. Owner references are
read-only: SodaOS `4d124129dc1945c3b9559f86d6f9b2a144678904`, Fountain
`984ea1cc33cc49020b296338003c774666bd6ba7`.

## Baseline

Working tree was clean at entry; the installed app was stopped. This audit adds
only this document. No new runtime baseline was taken. Previously retained
`request75-progress-pg.log`, `request75-external-pg.log`,
`request75-backend-build.log`, `request75-harness-build.log` under
`/home/vince/Projects/factory-real-cycle-20261110/` record passing scoped checks;
they do not qualify installed behavior or actual provider request volume.

The existing local build entry points are `go build ./cmd/soda-factory-server`
and `go build ./tools/native-checks/native_mcp_signing_check.go`, using local Go
1.27 with `GOTOOLCHAIN=local GOPROXY=off`. Existing native integration entry is
`target/debug/soda-native-pg-check --once POSTGRESQL PROFILE -- HARNESS progress-gate`
(or `external-preparation`). These are reproduction instructions, **not commands
executed by this audit**. Preserve the original profiles; later execution needs
its own scoped plan.

## Map

### Ownership and dependency direction

| Area | Defining responsibility and downstream dependency |
|---|---|
| `frontend/factory`, `frontend/desktop` | Lit view/action lifetimes → local Factory APIs/Tauri commands; no direct authenticated provider fetch |
| `pkg/factory/control` | Durable workflow/scheduling → product ports; a port invocation is not one wire request |
| `pkg/factory/runtime/native_*.go` | Product port → captured account/canonical repository → native Unix-socket IPC |
| `lib/desktop-runtime/src/collaboration.rs`, `github.rs` | Custody admission → native semantic read/write dispatch |
| `github_provider` | Complete GitHub connector → one transport owner and retained traffic budget |
| `fountain_provider`, `fountain_oauth.rs` | Configured Fountain host OAuth, enrollment, operation grants, snapshots and publication → native HTTPS |
| `factory_provider/controller.rs` | Execution authority → Fountain operation/claim API and per-lease liveness polling |
| SodaOS `internal/forgejo`, `internal/web` | Appliance ports → shared admitted Fountain SDK/socket client, some REST and Git subprocesses |
| Fountain `sdk` | Defines host callback protocol; host authority is outside Factory standalone engine |
| `scripts`, `tools`, `.github` | Distribution/legal downloads, controlled/live fixture tools, dependency publication; separate egress from app transport |

The important dependency inversion is Fountain importing shared read/write
contracts from the **GitHub implementation** namespace. The important hidden
composition is each product/UI action expanding across several independently
bracketed readers. Source imports do not imply requests: a Go `github.com/...`
module import, local SQL call, channel send or literal documentation URL is not
provider egress.

### Reproducible source views and history

Source-only script and detailed inventories are retained outside the repository
at `/home/vince/Projects/factory-provider-audit-20261112/`:

- `map.ts`: rerunnable tracked-source census, signatures, import edges and local
  Git history; `source-map.json`, `imports.tsv`, `skeleton.txt`,
  `network-candidates.tsv`, `churn.tsv`, `cochange.tsv`, `summary.json`.
- `github-wire.md`: every native GitHub REST producer and `provider_request`
  invocation, exact formulas, admission/retry behavior and defining callers.
- `fountain-wire.md`: native endpoints, formulas and canonical appliance adapter.
- `call-triggers.md`: Go/UI/background/tooling triggers and per-family call sites.
- `fresh-coverage.md`, `final-coverage.md`: independent coverage passes and
  omissions comparison. The final pass found no additional call family or
  worthwhile new entry within this scope.

The script covers 1,210 tracked source files and 8,948 signatures. TypeScript uses
an AST; Go/Rust skeletons are lexical navigation aids, not parser/typechecker
proof. The 2,730 broad search hits include non-network `.get`/`.send` operations;
**they are not a request count**. Import edges are recorded without claiming a
whole-program cycle proof.

Local history covers 836 commits since 2025-11-12. Highest churn: native Go
harness (179), credential-operation harness (108), collaboration Rust (72),
GitHub request/reads/transport (37/35/32). Strong co-change: Go/native harnesses
(56), collaboration/Go harness (41), GitHub reads/request (32), GitHub
request/product-observations Go (26). Pair counts omit commits touching over 60
files to reduce bulk-import noise. These support cross-boundary contract review;
churn itself is not a defect.

### Direct GitHub wire inventory

Abbreviations below: `gp` = `lib/desktop-runtime/src/github_provider`,
`fp` = `lib/desktop-runtime/src/fountain_provider`, `rt` = `pkg/factory/runtime`,
`ctl` = `pkg/factory/control`, `ui` = `frontend/factory`.
Unqualified Rust connector files are under `lib/desktop-runtime/src`;
unqualified native Go files are under `rt`. SodaOS filenames in its adapter
table are under `/home/vince/Projects/sodaos/internal/forgejo`; SDK filenames
are under `/home/vince/Projects/forgejo-ext/sdk`.
`R = /repos/{owner}/{repo}`. REST paths use `https://api.github.com`;
GraphQL is always **POST `/graphql`**. Git and OAuth use `https://github.com`.
All native GitHub wire attempts converge at `gp/transport.rs:710`.

| Method/path or operation family | Source producers / callers |
|---|---|
| GET `/user` | `gp/mod.rs:132`; every accepted semantic read, write admission, verification; extra canonical issue identity in `github.rs:358` |
| GET `/repositories/{id}`, `R`, `/user/repos`, `/repos/{name}` | `gp/reads.rs:73,105,391,473`; selected repository/catalog page, numeric Reader construction, currentness brackets |
| GET `R/issues`, `R/issues/{n}`, `R/pulls`, `R/pulls/{n}` | `gp/reads.rs:513,524`; subject/page reads, most dependent operation preflights |
| GET `R/issues/{n}/comments` | `gp/reads.rs:573`; issue/PR discussion between subject and repository observations |
| GET `R/pulls/{n}/reviews[/{id}]`, `R/pulls/{n}/comments`, `R/pulls/comments/{id}` | `gp/reads.rs:609`; review/inline detail/pages |
| GET `R/commits/{head}/check-runs`, `/statuses` | `gp/reads.rs:679`, `gp/automation_checks.rs:141`; checks/status pages and paired latest-source evaluation |
| GET `R/collaborators/{login}/permission` | `gp/reads.rs:408`, `gp/external_pulls.rs:176`; current creator/candidate author permission, up to 16 distinct authors per external page |
| GET `R/git/ref/heads/{branch}` | `gp/reads.rs:427`; target tip before/after |
| GET `R/actions/runs`, `R/actions/runs/{run}/attempts/{attempt}`, `…/jobs` | `gp/evidence.rs:73,109,169`; run/job pages, run attempt brackets |
| GET `R/actions/runs/{run}` | `gp/automation_checks.rs:183`; each distinct parsed GitHub Actions run in that snapshot, cache not shared across repeated snapshots |
| GET `R/branches/{branch}`, `…/protection`, `R/rules/branches/{branch}` | `gp/evidence.rs:237`, `gp/automation_checks.rs:57`; protections/rules, base branch, repeated complete snapshots |
| GET `R/issues/comments/{id}` | `gp/automation_inputs.rs:54`; each selected comment's node identity before input GraphQL |
| GET `/users/{login}`, `/user/{external_id}` | `gp/review_requests.rs:118,291`; reviewer lookup and selected-target identity |
| GET `R/compare/{base}...{head}` | `gp/diff.rs:8`, `gp/merges.rs:158`; merge base per diff/files page; merged-result ancestry |
| GET `R/pulls/{n}/files` | `gp/diff.rs:26`; each requested file page, with repeated pull/base/repository bracket |
| GET `R/contents/{path}?ref={commit}` | `gp/git_fetch.rs:87`; each distinct fixed/requested setup file, batches of 8 |
| GET `/{repo}.git/info/refs?service=git-upload-pack` or `git-receive-pack` | `gp/transport.rs:376`, `gp/git_fetch.rs:118`, `gp/git_publish.rs:155`; source/setup and publication ref advertisements |
| POST `/{repo}.git/git-upload-pack` or `git-receive-pack` | `gp/transport.rs:376,436`; source fetch and one publication upload |
| POST `/login/device/code`, `/login/oauth/access_token` | `gp/transport.rs:536`, `gp/oauth.rs:163`; device start/poll and single refresh exchange |
| GraphQL `FactoryIssueInputs` | `gp/automation_inputs.rs:97`; paired selected issue/timeline/dependency observations |
| GraphQL `FactoryCheckEvaluation` | `gp/automation_checks.rs:251`; actual test-merge/evaluated commit |
| GraphQL `FactoryExternalPullCandidates` | `gp/external_pulls.rs:51,155`; first/confirm candidate page, size 16 |
| GraphQL `FactoryDeliveryOpinions` | `gp/delivery_opinions.rs:55`; pages of 100, up to 256 opinionated reviews |
| GraphQL commit connection | `gp/commits.rs:25`; selected cursor page |
| GraphQL `FactoryWriteCapabilities` | `gp/capabilities.rs:43`; standalone and merge readiness |
| GraphQL `FactoryReviewThreads` / `FactoryReviewThread` | `gp/review_threads.rs:377`; catalogue pages of 50 or selected detail |
| GraphQL review requests/team/name | `gp/review_requests.rs:426`; request pages of 50, max 1,000, selected team lookup |
| GraphQL pending reviews/detail/comments | `gp/review_preparation.rs:657`; pending pages 50/max 1,000, comment pages 2/max 1,000 and byte limits |
| GraphQL `FactoryBranchCreation`, `createRef` | `gp/branch_create.rs:198,226`; paired branch facts, one conditional creation |
| GraphQL `FactoryPullBranches` | `gp/pull_branches.rs:201`; paired base/head ref observations; repeated in PR creation |
| GraphQL `FactoryDependencySet`, add/remove blocked-by | `gp/dependencies.rs:59,129`; complete dependency preflight, one edge mutation |
| GraphQL `addComment`, `createIssue`, update issue/PR | `gp/comments.rs:162`, `gp/issue_create.rs:135`, `gp/edits.rs:191` |
| GraphQL close/reopen issue/PR, lock/unlock | `gp/states.rs:177`, `gp/locks.rs:164` |
| GraphQL resolve/unresolve review thread | `gp/thread_resolution.rs:208` |
| GraphQL `requestReviews`; DELETE `R/pulls/{n}/requested_reviewers` | `gp/review_request_effect.rs:241,281`; add and remove have distinct transport/payload contracts |
| GraphQL create/update/submit review | `gp/review_create.rs:199`, `gp/review_submit.rs:212`; agent review remains COMMENT where required |
| GraphQL add inline review / pending review thread | `gp/inline_create.rs:127`, `gp/inline_draft.rs:269`; complete inline preparation repeated before write |
| GraphQL `createPullRequest` | `gp/pull_create.rs:262`; repeated branch preparation |
| GraphQL `FactoryMergedCommit`, `mergePullRequest` | `gp/merges.rs:98,295`; result observation and one allowed merge mutation |

Transport has no redirects or automatic HTTP retries (`gp/transport.rs:199`).
The durable ceiling is **600 attempted sends per rolling hour**, with provider
reserve/hold handling (`gp/traffic_budget.rs:14,142,174`); refused admission sends
nothing. It is a ceiling, not an acceptable normal operating request rate.
Device login polls pending responses at its granted interval (default 5 s,
slow-down +5 s, expiry ≤900 s). Writes are spaced by 1 s, not retried. Mutation
attempt identity is retained before dispatch; later uncertainty is not replay
permission. Ordinary semantic reads have 8 s deadlines; required/automation
checks and merge readiness have 15 s (`gp/request.rs:331`).

#### GitHub request expansion

Counts are static successful-path wire expansions **inside one native semantic
read**, before additional Go/UI admission. Cancellation/deadlines/budget refusal
can stop a path earlier. Variable page/author/run counts remain variables.

| Native operation | HTTP requests |
|---|---:|
| Identity; repository/catalog/find | 1; 2 |
| Issue/PR/list page; canonical issue | 4; 5 |
| Comment/review/check/status page; diff header; files page | 6; 6; 7 |
| Workflow run page; job page; PR jobs with run | 4; 6; 9 |
| Protection; inputs | 11; 5 + selected comments |
| External candidate page | 5 + distinct authors (≤16) |
| Delivery opinions | 6 + opinion pages |
| Pull branch pair, even same repository | 9 |
| Branch preparation; branch create write | 8; 9 |
| Plain publication refs; plain source bundle | 7; 7 |
| Setup source | 6 + distinct selected files |
| Plain branch publish; fork-correction branch publish | 6; 14 |
| Automation review | 11 + 2 × fork resolution |
| Automation checks | 13 + 2 × fork resolution + 2E |
| Required checks | 16 + E1 + E2 |
| Merge readiness | 21 + 2 × fork resolution + E1 + E2 + opinion pages in both snapshots |
| Comment/edit/state/lock write; issue create; dependency write; merge mutation | 5; 3; 7; 7 |

`E` is evaluation-source cost: 2 + distinct Actions run lookups, or 4 + lookups
when the empty test-merge source falls back. Fork resolution is the extra
repository resolution in each snapshot. Inline preparation costs
`5 × file_pages + 4 + pending_review_collection` internally, plus outer admission.
At 3,000 files / page 10, that is **1,500 file-related attempts alone** if allowed
to finish, exceeding the 600 ceiling. It does not mean those attempts all ran.

### Direct native Fountain wire inventory

Configured HTTPS origin only; redirects and automatic HTTP retries disabled.
`B = /api/v1/repos/{owner}/{repo}/factory/desktop/{grants|issue-grants}`,
`E = B/{enrollment}`, `G = E/operations/{grant}`. The issue-only branch is selected
from saved `write:issue` scope. `fp` requests converge at
`fountain_provider.rs:302`; OAuth at `fountain_oauth.rs:99`.

| Method/path | Source / callers |
|---|---|
| GET `/api/v1/factory/desktop/oauth`, `/oauth/control`, `/oauth/issues` | `fountain_oauth.rs:134,151`; sign-in and refresh configuration |
| Browser `/login/oauth/authorize`; POST `/login/oauth/access_token`; GET `/api/v1/factory/desktop/identity` | `fountain_oauth.rs:188,228,263`; browser consent, code/refresh exchange, immutable identity. URL construction alone sends nothing |
| POST B; GET E; DELETE E | `fountain_provider.rs:355,368,371`; enroll, status/currentness, withdraw |
| GET `E/repository` | `fountain_provider.rs:378`; exposed connector API, no production caller found |
| GET/POST `E/placement`; POST `E/operations` | `fp/controller.rs:187,201,238`; controller observation/selection/privileged authorization |
| GET G; GET `E/native-revision` | `fp/operation.rs:141,163`; grants/liveness/captured authority; publication registration |
| GET `G/journal/{operation}`; POST `G/submit` | `fp/operation.rs:216,323`, `fp/publication.rs:236,472`; typed operation submission and causal recovery |
| GET `G/claims/{claim}`; POST `G/claims`; POST `G/claims/{claim}/retire` | `fp/execution.rs:179,206,244`; execution claim/recovery/retirement |
| POST `E/source`; POST `E/snapshot` | `fp/source.rs:29`, `fp/snapshot.rs:224`; source bundle; selected host facts with status brackets |
| POST `E/prepare`; POST `E/commands`; GET `E/commands/{command}` | `fp/issues.rs:47,85,104,122`; issue-only preparation, dispatch and retained receipt recovery |
| POST `/{owner}/{repo}.git/git-receive-pack` | `fp/publication.rs:721`; candidate upload once, then journal reconciliation |
| GET `/api/v1/repos/{owner}/{repo}` | `fp/product.rs:198`, `fp/publication.rs:97`, `fp/snapshot.rs:148`; repository/publication/merge facts |
| GET `R/branches/{branch}`, `R/contents/{path}?ref={commit}` | `fp/product.rs:396,404`; target/setup/files; here R uses `/api/v1/repos` |
| GET `R/issues[/{n}]`, `R/pulls[/{n}]` | `fp/product.rs:422,433,439`; selected detail/page, page+limit+state, subject brackets |
| GET `R/pulls/{n}/reviews[/{id}]`, `R/issues/{n}/comments` | `fp/product.rs:327,340,354`; selected review/discussion page with subject brackets |
| GET `R/pulls/{n}/reviews/{review}/comments?page=1&limit=256` | `fp/discussion.rs:126`; each visible review during complete thread reconstruction; extra page refuses |

OAuth sign-in/refresh each cost 3 native HTTPS requests (config+exchange+identity),
plus browser traffic for sign-in. No native Fountain OAuth polling/SSE exists.
Each request renews local custody capability, not a network token refresh.

Let **S = one host snapshot = 3–7 requests**: enrollment GET, 1–5 snapshot POSTs,
enrollment GET. Only 409 retries, sleeps 50/100/150/200 ms (`fp/snapshot.rs:224`).
Each product read adds another two status requests around its selected branch.

| Native Fountain operation, including outer product bracket | HTTPS requests |
|---|---:|
| Identity / issue-only repository; ordinary repository/issue/PR/page | 2; 3 |
| Operation grant; target tip; discussion/review page | 5; 5; 5 |
| Setup source; source bundle; required checks | 4 + distinct files; 7; 5 |
| Publication refs / branch creation; pull branches | 4+S; 5+S |
| Delivery opinions; check-run/status page | 4+2S; 4+2S each |
| Automation review; pending-review preparation | 5+2S; 6+2S |
| Automation checks | 8+6S = 26–50 |
| Merge preparation | 17+12S = **53–101** |
| Merge completion | 7+2S = 13–21 |
| Automation inputs | 4+S, or 4+2S with comments |
| Creator permission | 5+S |
| Thread read/page | 4 + 2(T+V)S + Rv |

For threads, T is total snapshot pages, V=1 when threads exist, Rv is review
comment GET count (≤50). One requested local page/thread still collects and
replays all host captures, ≤256 thread members, pages of ≤50.

Publication registration is `3+2N` = 5–15 requests (N=1–6 native-revision
attempts); failed submission adds one journal GET. New branch publication adds
3; review adds 1; pull creation adds its branch snapshot; merge adds
`merge_facts`, totaling 56–114 before optional failed-submit lookup. Native
receive-pack is never resent. Existing publication capture recovery is grant
GET+journal GET. New issue collaboration is status+prepare+submit (3), recovery
status+journal (2). A complete controller UI action normally costs 5 requests.

`factory_provider/controller.rs:285` reads the operation grant then sleeps
**one second per entered execution/preparation lease**, until stop, expiry or failure.
This is separate from five-minute product reconciliation.

### Delegated appliance boundary

SodaOS `internal/web/server.go:131` injects one shared background admission client
into graph/readiness/creation/publication/review/resolution/merge/check ports.
REST origin is configured `ForgejoInternalURL`; public canonical origin is
`ForgejoURL`. This is separate from standalone native connector composition.

| Transport / paths | Owner and behavior |
|---|---|
| REST GET `/api/v1/user`, `/repositories/{id}` | SodaOS `internal/forgejo/client.go:76,104`, `observe.go:138,227`; actor cache/single-flight, numeric repository resolution |
| REST GET `R/pulls/{n}/reviews/{id}` | SodaOS `delivery_material.go:135`; one per review body |
| REST GET/POST `R/issues/{n}/comments` | SodaOS `issue_feedback.go:39`; marker lookup pages 50 with no explicit page ceiling before posting |
| Other adapter helpers: DELETE `/user/token`, GET `/user/keys`, `/users/{login}/orgs/{organization}/permissions` | SodaOS `client.go:113`, `own_keys.go:27`, `ownership.go:20`; setup/admin helpers, not desktop collaboration |
| Unix HTTP POST `/v1/background/bootstrap` | SodaOS `background_admission.go:145`; admission/one shared rebootstrap after 401 |
| POST `/v1/background/{revision,snapshot,submit,get,cancel,creations,operations,issue-intake}` | SodaOS `background.go:52`, `creation_recovery.go:63`, `background_operations.go:13`, `issue_intake.go:88` |
| POST `/v1/background/controller/{grant,claim,claim/get,claim/retire}` | SodaOS `background_controller.go:12`; current controller claim/retirement |
| Unix HTTP POST `/v1/native` | Fountain `sdk/native.go:325`; request-admitted actor/repository/source/controller, collaboration read and human-operation families; SDK operation names are payloads, not separate URLs |
| Git `push --no-follow-tags` | SodaOS `publish/operation_push.go:56`; configured remote; failed push→lookup, retained pending outcome can permit one rebound admission/push, then lookup |
| POST `/v1/extensions/manager/{state,snapshot,reconcile,cache}` | SodaOS `extension_manager.go:16`; ancillary extension admin, one attempt |

Background HTTP uses configured Fountain Unix socket and verified peer, not
internet `http://extensionHost`. One request ordinarily sends once; 401 permits
one shared bootstrap and one retry (`background_transport.go:43`). Bracketed
read is revision+snapshot+revision (3 host calls); each has that envelope.
Uncertain submit can add one lookup. SDK human reads bracket live actor and
repository; pagination is caller-directed. No SSE subscription was found.
Sibling adapter coverage is complete at transport/endpoint/operation-family
level, not a full per-method expansion of every SodaOS web handler.

### Callers, triggers and cadence

| Trigger | Indirect provider work / defining sites |
|---|---|
| Every bound product HTTP request | `rt/native_product.go:26`, `native_collaboration.go:190`; selection performs GitHub repository/identity, Fountain identity+repository, followed by local currentness. Adds to native operation counts above |
| Local callback routes | `rt/native_collaboration.go:246`; `/api/native/collaboration[/background]` over Unix socket; this IPC is not another provider HTTP attempt |
| Shared reads / discussions / evidence | `rt/native_product_reads.go:14`, `native_product_discussions.go:23`, `native_product_evidence.go:143`; each check/status page also projects required checks |
| Human preparations/writes | `rt/native_product_{branch_creates,pull_creates,merges,review_creates}.go`; every write goes through `native_product_creations.go:85` plus operation-specific preflight. Lookups/cancels normally use local journals; Fountain recovery can read host journals |
| Post-write preservation | `rt/native_product_inlines.go:271`, `native_product_inline_creates.go:170`, `native_product_pending_reviews.go:239`; explicit provider readback is separate from sending |
| Source/setup/publication | `rt/native_external_source.go:20`, `native_setup_onboarding.go:102`, `native_publications.go:168`; source/review/accepted-input brackets, branch refs and delivery opinions |
| Startup | `ctl/coordinator.go:178`; reconciliation, merge/publication/check/graph/creation/readiness work occurs before UI interaction |
| Periodic progress | `ctl/progress.go:137,253`; local tick every 1 s, broad provider stages every 5 min or new terminal work; exact authorized merge wake can target next tick without replaying whole queue |
| Sweep bounds | Merge/publication up to 256 pending plus 256 new; publication child pages 64; checks negative cache 1 min; readiness 120 s/8 pages/32 assessments/52 evidence/nominal156 RPC/64 selections. These bound units, not necessarily actual wire calls |
| Creation recovery | `ctl/creation_recovery.go:35`, `rt/native_creation_discovery.go:85`; one repository/page16, idle60s; two complete open-issue lists plus up to 3 semantic reads per candidate |
| Completion inventory | `ctl/completion_observations.go:11`, `rt/native_completion.go:35,65`; two complete issue inventories with input read per issue, two local captures each reading target tip twice |
| External intake / merge | `rt/native_external_intake.go:46`, `native_merges.go:135`; candidate/review/check/accepted-input currentness separate from own-issue polling |
| PR navigation | `ui/pulls/detail.ts:130,359`; pull+selected tab, then eager review/merge controls each request identity+pull+capabilities. Conversation view ≥8 backend requests; files view ≥9, before other controls/admission |
| Thread/file navigation | `ui/pulls/detail.ts:176,215,223`; page/change/expanded comments can repeat full native collection; refresh remounts control admission |
| Work/progress views | `ui/work/detail.ts:234,255`, `ui/work/progress.ts:307,330`; detail parallel material then runs/retry status; progress requests N issue details plus N plans |
| Feed / request / retry status | `ui/feed/feed.ts:235`, `ui/request/request.ts:513`, `ui/work/verification-retry.ts:239`; visible Feed5min; unsettled request and retry60s; stop guards apply. Exact source locations in trigger inventory |
| Desktop visibility/navigation | `frontend/desktop/main.ts:26,75`, `connections.ts:339,407`; status2s is local, hide retires view/show reconnects; no direct provider poll just because timer exists |

Completion's native lower bound is **2×(ceil(N/50)+N)+4 semantic effects** for
N open issues (N=0 still requires both empty list observations). Creation's is
**2×ceil(N/50)+3K**, K≤16 eligible candidates, plus empty-page and admission
costs. Each semantic effect then expands in Rust. These formulas expose why
nominal Go RPC accounting is not a wire-volume bound.

### Auxiliary and indirect egress

- `scripts/desktop-distribution.ts:66,71`: missing packaged license files cause
  a GitHub contents request at exact source commit, then raw.githubusercontent
  GET per legal file; JS provenance first requests npm metadata. Mozilla MPL
  text is a different service. No retry loop in this helper.
- `scripts/build-desktop.ts`: dependency/version/provider downloads and package
  manager commands; actual installed installer uses local bundle. Dependency
  acquisition can fetch GitHub-backed archives outside native app budget.
- `tools/native-checks/native_mcp_signing_check.go:8327,8340,8346`: controlled
  fixture download/stress helper includes repeated Bun/curl attempts with curl
  retry2. Live credential fixture in `tools/native-checks/src/native_github_pull_branches.rs:2303`
  uses gh. These are check tools, not ordinary app polling.
- `.github/workflows/dependencies.yml`: hourly/manual/selected PR job;
  checkout/setup/package-manager/rustup/apt/bun/cargo/go downloads are runner
  traffic. `.github/dependabot.yml` configures five hourly ecosystems, with up
  to20 PRs in selected ecosystems. They do not consume the app transport counter.
- `factory_provider/setup_network.rs:20,90,160`: credential-free HTTPS CONNECT
  may carry public dependency traffic to GitHub or configured public hosts.
  It has 512MiB/1,024 accepted connections, concurrency8 and 120s connection
  deadlines, tied to approved preparation lifetime. Encrypted requests inside a
  tunnel are not counted by GitHub HTTP owner; exact URLs/counts are dynamic.
- `factory_provider/tool.rs:19,165,238` official Muse metadata/artifacts and
  `factory_provider/owner.rs:820` model forwarding are separate provider traffic;
  their presence in the census is not evidence of GitHub calls.
- `pkg/factory/client/projects.go:20` `http://factory/projects`, native callback
  HTTP, browser product APIs and MCP routes are local Unix/in-process traffic.
  `typesafe_transport.go` calls Typesafe, not GitHub/Fountain. Legacy `pkg/github`
  contains only its retirement README; it is not another connector.

## Problems

Counts: **20 hidden links, 7 needless links, 2 surplus pieces, 24 special cases**
(53 entries; special cases include necessary safety requirements). Entries describe existing
change-together relationships; no redesign is prescribed. A `[special]` records
a requirement/exception to understand, **not automatically a bug to remove**.

- [hidden] `gp/reads.rs:25` and `gp/transport.rs:363` manually share semantic diagnostic names; additions can fall into `other` without exposing the new read kind.
- [hidden] `gp/reads.rs:414` and `gp/external_pulls.rs:202` duplicate collaborator/permission interpretation, so actor-authority changes need coordinated edits.
- [hidden] `gp/automation_checks.rs:61`, `gp/evidence.rs:285`, `gp/merges.rs:138` separately encode branch naming/endpoints for the same facts.
- [hidden] `gp/request.rs:331`, `gp/writes.rs:124`, `gp/transport.rs:490` compose independent deadlines across nested work; aggregate request cost is outside the deadline owner's vocabulary.
- [hidden] `gp/inline_preparation.rs:59` accepts a 3,000-file catalogue using page10/full diff brackets, potentially 1,500 attempts before pending-review work, while `gp/traffic_budget.rs:14` permits600/hour.
- [hidden] `gp/review_preparation.rs:56,187` and `gp/review_submit.rs:128` share pending-comment digest/dispatch conventions outside a single contract owner.
- [hidden] `gp/inline_preparation.rs:149`, `gp/inline_draft.rs:103`, `gp/inline_create.rs:79` share selection/catalogue/pending hashes through independently maintained JSON shapes.
- [hidden] `rt/native_product_observations.go:148,204`, semantic emitters and Rust read enum manually share operation-kind vocabulary across IPC dispatch.
- [hidden] `ctl/completion_observations.go:12` charges a fixed bracket while `rt/native_completion.go:65` performs two full inventory/input traversals; nominal pass accounting changes independently from real provider fan-out.
- [hidden] `ctl/creation_recovery.go:69` charges a page unit while `rt/native_creation_discovery.go:104,168` reads complete membership twice plus candidate facts; retained page size does not bound wire reads.
- [hidden] `ctl/readiness_work.go:122` mirrors a Forgejo page cap while native creation/completion use their own limits; shared budget comments name a different implementation owner.
- [hidden] `ui/pulls/detail.ts:359`, `ui/pulls/review.ts:76`, `ui/pulls/merge.ts:85` compose eager controls with separate authority reads; opening one view implicitly creates several independent observations.
- [hidden] `ui/issues/detail.ts:142` and `ui/issues/screen.ts:88` refresh/rebind child actions, so observation cost depends on component lifecycle as well as explicit navigation.
- [hidden] `frontend/desktop/connections.ts:339,407` ties visibility retirement/reconnect to product-view remount and repeated admission.
- [hidden] `frontend/desktop/fountain-login.ts:6`, `cmd/soda-desktop/src/fountain_signin.rs:168`, `fp/enrollment.rs:68`, `fountain_oauth.rs:164` separately classify identical scope literals into consent/config/enrollment behavior.
- [hidden] `gp/git_fetch.rs:54,75,80` and `fp/product.rs:235,244,249` independently own four fixed setup filenames and their union with up to64 selected paths (`gp/request.rs:386`), so setup vocabulary changes alter both provider network plans.
- [hidden] `fp/issues.rs:33,197` and `rt/native_fountain_issues.go:35,61` independently encode issue-operation receipt/protocol assumptions across callback boundary.
- [hidden] `fp/snapshot.rs:92,139,170` nests independently bracketed helpers into merge preparation's53–101 requests, while deadline selection remains in `gp/request.rs:331`.
- [hidden] `fp/discussion.rs:91,126,164,169` couples requested thread/page selection to full collection, every review's comments and replay of all captures.
- [hidden] SodaOS `internal/forgejo/background.go:11` and `internal/web/server.go:131,137` require one shared admission instance because another bootstrap revokes the first; construction identity is part of correctness beyond port types.
- [needless] `gp/review_threads.rs:156,239` rereads the whole≤1,000-thread catalogue and only then slices the requested local page, tying one page's cost to all threads.
- [needless] `gp/inline_preparation.rs:59,141` composes a full diff bracket per file page and another final header; catalogue paging repeats already observed common facts.
- [needless] `gp/pull_branches.rs:60,62,102` constructs/rechecks a second Reader even for the identical repository; same-repository observation pays the fork path's cost.
- [needless] `github.rs:358,397` reads identity before canonical mapping then invokes `gp/reads.rs:65`, which reads it again.
- [needless] `ui/pulls/review.ts:74` and `ui/pulls/merge.ts:83` independently implement the same identity→pull/capabilities admission sequence.
- [needless] `ui/work/progress.ts:307,330` fetches one detail and one plan per issue; page observation cost grows with every displayed member.
- [needless] `fp/product.rs:4`, `fp/publication.rs:7`, `fp/issues.rs:4` import shared semantic types/outcomes from GitHub implementation namespace, binding providers that change for different reasons.
- [surplus] `ctl/readiness_work.go:137,155` exposes completion page-accounting helpers with no production callers found; their comments imply bounds that native completion does not use.
- [surplus] `fountain_provider.rs:378` and `fp/operation.rs:241,323` expose repository-resource/completion-note APIs with no production consumers found, retaining additional network/capture behavior to maintain.
- [special] `gp/review_request_effect.rs:133,136` must add reviewers through GraphQL and remove through REST with distinct actor payloads.
- [special] `gp/merges.rs:91` and `gp/mod.rs:76` bind result verification to an API-version-dependent GraphQL merged-commit convention.
- [special] `gp/merges.rs:278` and `gp/git_publish.rs:236` reject Fountain-only fields in shared operation structures; provider vocabulary leaks into peer validation.
- [special] `gp/evidence.rs:208` and `gp/protection.rs:43` preserve403/404 as unknown protection evidence rather than absence; authority concealment must remain fail-closed.
- [special] `gp/automation_checks.rs:183` parses GitHub Actions details URLs to retrieve app-bound run facts, adding data-dependent HTTP cost.
- [special] `gp/writes.rs:82` and `gp/transport.rs:693` can retain a dispatched mutation before transport-budget refusal, producing indeterminate outcome even for suppressed wire dispatch; lookup absence still must not authorize resend.
- [special] `gp/oauth.rs:163` polls authorization pending/slow-down, unlike other transport requests; OAuth cadence and ceiling share the same wire budget.
- [special] `gp/writes.rs:82` spaces writes1s; `fp/snapshot.rs:224` retries409 with four sleeps; `fp/publication.rs:551` has bounded revision attempts. These are separate pacing/currentness requirements, not a common retry policy.
- [special] `rt/native_collaboration.go:275` selects16s IPC budgets for three read kinds versus9s ordinarily; deadline names must track native selectors.
- [special] `rt/native_collaboration.go:305` gives source bundles separate size handling; inline-preparation has its own bounded envelope at `:262`.
- [special] `ctl/merge.go:256`, `ctl/progress.go:261,318` distinguish a fresh exact wake from replay, using transient wake hints plus durable authority.
- [special] `ctl/review_cycle.go:116,134` and `ctl/progress.go:70,109` retain1min negative/closed caches with explicit-command bypass rules.
- [special] `ctl/progress.go:269` and `ctl/creation_recovery.go:63` compose5min outer reconciliation with60s inner eligibility; the effective cadence is not the inner timer alone.
- [special] `ui/pulls/detail.ts:145,151` swallows capability fallback failure and disables head-bound controls; unavailable evidence is not permission.
- [special] `ui/work/progress.ts:316,323` falls back when plans cannot be decoded; Feed allowance aggregation drops failed reads (`ui/feed/feed.ts:534,544`), hiding per-item failures behind aggregate rendering.
- [special] `rt/native_merges.go:171,220`, `rt/native_product_pending_reviews.go:239`, `rt/native_product_inlines.go:271` require repeated accepted-input/review/preservation proof; counts must preserve these safety obligations.
- [special] `fp/discussion.rs:67,72` runs the complete status bracket even for check-run projection returning no items, duplicating native evidence work across the two shared check families.
- [special] `factory_provider/controller.rs:285` reads grants then sleeps1s per lease, independently of product reconciliation and snapshot status checks.
- [special] SodaOS `internal/forgejo/issue_feedback.go:45,60` scans arbitrary50-comment pages for idempotence markers before posting; history length controls lookup cost.
- [special] SodaOS `background_transport.go:43` permits one shared bootstrap/retry after401; native Fountain transport has no equivalent automatic retry.
- [special] SodaOS `publish/operation_push.go:66` permits one rebound push for a retained pending host operation, unlike native receive-pack's no-resend rule; recovery semantics differ by owner.
- [special] `scripts/desktop-distribution.ts:49,71` makes exact legal-file acquisition conditional on package contents/provenance, creating build-only GitHub calls outside app budget.
- [special] `tools/native-checks/native_mcp_signing_check.go:8340,8346` contains deliberate download repetition/curl retries; check-tool traffic must stay distinct from product cadence.
- [special] `factory_provider/setup_network.rs:90,160` bounds encrypted public HTTPS tunnels by connections/bytes, while GitHub request accounting sees no individual tunneled requests.

## Fixes

Not started. The next phase is **untangle-redesign**. Currentness, captured
identity/source, human-text preservation, causal journals, no-uncertain-replay,
withdrawal and provider-specific authority remain requirements, even where
observation composition duplicates work.

## Progress

- 2026-11-12: baseline and source census captured. Three area audits completed;
  representative thread/inline/branch/UI/completion/creation/Fountain nesting
  checked directly against source. No code or retained state changed.
- Two fresh independent passes completed. The first added the duplicated setup
  filename rule and independently derived the Fountain expansion; the final
  pass found no additional worthwhile entry/call-family omission. Merged counts:
  20 hidden / 7 needless / 2 surplus / 24 special.
- Source audit complete within the stated boundary. Runtime volume remains
  unmeasured; this is not connected/installed verification. Next executable
  phase: untangle-redesign using this inventory, without replaying effects.
