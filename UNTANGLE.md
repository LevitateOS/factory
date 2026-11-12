# Untangle log

## Scope

Source audit of all GitHub/Fountain egress owned by Factory: authenticated REST,
GraphQL, Git, OAuth, delegated host adapters, callers, UI, background work,
polling/recovery, setup tunnels and auxiliary tooling/build jobs. Factory is at
`9482c81aa452153e5f86ae49bcbd2559f484aac3`, on `main`.

Phases1–2 recorded the audit/design; phase3 refactoring is now authorized.
Use controlled local checks only: no live provider requests, inference or quota
waiting. The owner explicitly selected **neither** optional
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

Audit baseline: **20 hidden links, 7 needless links, 2 surplus pieces and
24 special cases** (53 entries). Stable IDs P01–P53 follow the original audit
order. Each original entry appears exactly once below:40 under F1–F8 and13 under
Leave as is. Their locations describe the audited source, not completed changes.

### Requirements revealed by all24 special cases

| Entry | Requirement the design must preserve or make explicit | Placement |
|---|---|---|
| P30 | Reviewer addition/removal use the provider's distinct supported APIs and actor payloads | Leave as is |
| P31 | Verify the resulting merge commit separately from the reviewed candidate under the captured API contract | Leave as is |
| P32 | Provider-specific command authority/fields must not become accepted peer-provider input | Leave as is |
| P33 | Concealed protection is unknown, never proof of no required policy | Leave as is |
| P34 | CI must prove the actual app/run and evaluated test-merge commit, with bounded distinct-run resolution | F2 |
| P35 | Conservative uncertain-send classification and original causal identity survive transport refusal | Leave as is |
| P36 | Device consent polls pending/slow-down with provider interval and device expiry | Leave as is |
| P37 | Write pacing, read-conflict recovery and native-revision attempts have different purposes; all fit an overall bound | F2 |
| P38 | IPC must allow the native bounded result/refusal to arrive without a second drifting selector/deadline list | F1 |
| P39 | Source bundles and inline captures need explicit bounded request/response classes | F1 |
| P40 | A fresh exact command wake must not replay the entire queue or grant authority to transient hints | Leave as is |
| P41 | Negative/closed observations may suppress passive work while explicit newly authorized commands remain distinct | Leave as is |
| P42 | The effective polling eligibility belongs to one scheduler calculation, not multiplication of nested timers | F4 |
| P43 | Missing capability/head facts disable actions and remain explainable to the user | F5 |
| P44 | Per-item plan/allowance failures must stay unavailable/unknown rather than disappear into aggregate success | F5 |
| P45 | Accepted inputs, human blockers/reviews and completed human writing require current proof and preservation | F2 |
| P46 | Shared check/status projections need one consistent source set, including honest empty projections | F2 |
| P47 | Execution grant withdrawal/expiry must cancel every admitted consumer within the existing observation bound | F6 |
| P48 | Feedback recovery needs exact provenance and no duplicate comment, without unbounded history scans | F8 |
| P49 | Appliance admission may rebootstrap once after definite401 without duplicating owners or uncertain writes | F6 |
| P50 | Host-defined pending-operation rebound and native never-resend publication remain different contracts | Leave as is |
| P51 | Distribution carries exact legal/provenance inputs, with build traffic distinguished from app traffic | Leave as is |
| P52 | Deliberate network stress fixtures remain explicitly scoped check tools | Leave as is |
| P53 | Approved dependency HTTPS retains credential isolation and honest connection/byte bounds; encrypted request counts are unknown | Leave as is |

## Fixes

Adopted phase2 design; implementation status and scoped checks are in Progress.
The owner reviewed the
proposed fixes and replied “No additional constraints.” This is the agreed
design handoff; phase3 is now executing it. No optional comment/test cleanup is included. Source baseline remains
9482c81; 6076304 added the audit only, so the existing skeleton/import/history
views were reused and checked against the current defining modules.

The missing concept is the **bounded observation for one operation**: the
operation owns the required before/after evidence, selected collection, deadline
and actual send allowance. Its helpers return facts; they do not each reopen
an entire authority/consistency cycle. This is a small refactor of the existing
connectors and journals, not another workflow engine or generic request planner.

Order below respects the actual dependencies. F5–F8 are independent ready work;
they need not wait for Fountain installation or connected GitHub qualification.

| Fix | One-line outcome | Entries | Depends on |
|---|---|---:|---|
| F1 | Put shared connector contracts below both providers | 6 | None |
| F2 | Make one operation own observation boundaries and actual send cost | 10 | F1 |
| F3 | Page selected collections without repeating whole preparations | 6 | F2 |
| F4 | Resume inventory work within an honest provider budget | 5 | F2 |
| F5 | Let the mounted view own display reads and action admission | 7 | None |
| F6 | Let the Fountain connection own consent, admission and grant watching | 4 | None |
| F7 | Remove native Fountain egress with no consumers | 1 | None |
| F8 | Journal feedback delivery and bound legacy receipt searches | 1 | None |
| Leave as is | Preserve necessary protocol, recovery and auxiliary boundaries | 13 | — |

### F1 — Put shared connector contracts below both providers

Move only genuinely shared selector, observation and outcome definitions to a
small provider-neutral module in the existing native runtime. Keep provider
clients, credentials, GraphQL, Fountain grants and provider-specific mutation
payloads in their current provider owners. `Read` supplies exhaustive diagnostic
names, validation and native operation limits; supported variants cannot become
`other` through an unrelated manual match. Put the four setup filenames and
path-union rules beside the shared setup-source contract; each provider still
owns fetching and batching.

Use an explicitly versioned IPC envelope with one tagged payload rather than
parallel optional result fields plus a second string-routing catalogue. Go owns
its typed product projections and strict decoder. Keep the wire binding explicit
and cover both language bindings in the existing native integration harness;
no new code generator, metadata server or generic operation registry is needed.
The enclosing Go IPC timeout covers the permitted native deadline, and the
same contract declares bounded source/inline payload classes. Neither a larger
envelope nor a descriptor grants execution or provider permissions.

Do not reserialize captured commands or change their digest interpretation.
Preserve exact old encodings through the existing known-version decoder; unknown
versions remain unavailable. Current schema or target state never gives a
historical command new authority. Legacy flat mutation DTOs retain their exact
fields and provider-specific admission fences; do not add a second mutation
protocol merely to remove those compatibility checks (P17/P32 below).

**Done when:** Fountain no longer imports semantic contracts from the GitHub
implementation; adding a shared read has one native metadata owner and one
explicit Go binding; mismatched kind/version/payload fails closed; setup unions
agree; retained hashes/receipts and known-prior decoding remain unchanged.
**Depends on:** none.

- [hidden] **P01** `gp/reads.rs:25` and `gp/transport.rs:363` manually share semantic diagnostic names; additions can fall into `other` without exposing the new read kind.
- [hidden] **P08** `rt/native_product_observations.go:148,204`, semantic emitters and Rust read enum manually share operation-kind vocabulary across IPC dispatch.
- [hidden] **P16** `gp/git_fetch.rs:54,75,80` and `fp/product.rs:235,244,249` independently own four fixed setup filenames and their union with up to64 selected paths (`gp/request.rs:386`), so setup vocabulary changes alter both provider network plans.
- [needless] **P27** `fp/product.rs:4`, `fp/publication.rs:7`, `fp/issues.rs:4` import shared semantic types/outcomes from GitHub implementation namespace, binding providers that change for different reasons.
- [special] **P38** `rt/native_collaboration.go:275` selects16s IPC budgets for three read kinds versus9s ordinarily; deadline names must track native selectors.
- [special] **P39** `rt/native_collaboration.go:305` gives source bundles separate size handling; inline-preparation has its own bounded envelope at `:262`.

### F2 — Make one operation own observation boundaries and actual send cost

Keep one operation-scoped reader inside the existing Rust connector. It owns
captured credential/profile generation, host, actor, repository, exact head/base
and required current permission/enrollment/placement facts. Split composing
helpers into fact acquisition and pure projection. Canonical mapping reuses the
identity already obtained with the same credential generation. Same-repository
branch work uses one repository reader; fork work keeps its distinct repository
and permission proofs. Branch lookup and collaborator-permission interpretation
have one defining helper inside GitHub, not a cross-provider utility.

Reuse a fact only within the same operation **and same observation phase**.
Immutable commit-bound data can be shared; an opening mutable observation can
never satisfy its closing check. Distinct permissions, policy, head/base,
review/veto state and app-bound/test-merge CI sources still receive the required
fresh closing evidence. A write starts its own current admission and exact
conditional send. Read-only reuse never becomes a durable execution grant.

For Fountain, collect the supported family union for the selected pull and
project review/check/merge results from it instead of nesting `review_facts`
inside `automation_checks` inside `merge_facts`. The configured host already
checks enrollment and equal snapshots/revision inside each POST
(`forgejo-ext/routers/api/v1/repo/desktop_source.go:80`). Keep the necessary
outer REST/enrollment and before/after material comparisons. Check/status
projections consume one acquired evidence set; an empty projection does not
start another status collection. Unsupported family combinations remain
unavailable; do not invent a stronger host consistency guarantee.

Pass one absolute deadline and finite remaining-send allowance through the
existing call path to the actual native HTTP sink. Every attempted request,
including failed transport, pagination and conflict retries, debits it before
send. The current durable GitHub ceiling/provider holds stay in force. Across
Go calls, the existing pass budget allocates bounded sub-allowances and charges
returned actual usage; an IPC timeout charges its outstanding allocation
conservatively. The browser cannot enlarge native limits. Keep typed refusal,
wire attempts and local IPC counts distinct in existing diagnostics.

The operation owns one conflict retry allowance, not a fresh allowance in each
nested helper. Device OAuth and mutation spacing retain their own protocol
rules. Budget exhaustion returns a bounded hold; it does not sleep for quota,
reset history, rotate identity or retry a mutation.

**Done when:** controlled responses prove additive page/source cost rather than
recursive full brackets, no duplicate canonical identity/equal-repository reads,
actual attempted-wire accounting through refusal/cancellation, and refusal on
changed authority/material. Proposed Fountain target for complete, one-page,
same-repository merge facts: at most11 native HTTPS requests without conflict,
plus at most4 operation-wide409 retries, excluding outer Go selection. This is
a design acceptance target, not a measured result; any extra required proof must
be explained rather than silently removed. GitHub Actions run lookups are once
per distinct run per observation phase, still freshly checked in the closing
phase. No old mutation outcome is reclassified from new traffic information.
**Depends on:** F1 for the explicit shared operation/usage contract.

- [hidden] **P02** `gp/reads.rs:414` and `gp/external_pulls.rs:202` duplicate collaborator/permission interpretation, so actor-authority changes need coordinated edits.
- [hidden] **P03** `gp/automation_checks.rs:61`, `gp/evidence.rs:285`, `gp/merges.rs:138` separately encode branch naming/endpoints for the same facts.
- [hidden] **P04** `gp/request.rs:331`, `gp/writes.rs:124`, `gp/transport.rs:490` compose independent deadlines across nested work; aggregate request cost is outside the deadline owner's vocabulary.
- [hidden] **P18** `fp/snapshot.rs:92,139,170` nests independently bracketed helpers into merge preparation's53–101 requests, while deadline selection remains in `gp/request.rs:331`.
- [needless] **P23** `gp/pull_branches.rs:60,62,102` constructs/rechecks a second Reader even for the identical repository; same-repository observation pays the fork path's cost.
- [needless] **P24** `github.rs:358,397` reads identity before canonical mapping then invokes `gp/reads.rs:65`, which reads it again.
- [special] **P34** `gp/automation_checks.rs:183` parses GitHub Actions details URLs to retrieve app-bound run facts, adding data-dependent HTTP cost.
- [special] **P37** `gp/writes.rs:82` spaces writes1s; `fp/snapshot.rs:224` retries409 with four sleeps; `fp/publication.rs:551` has bounded revision attempts. These are separate pacing/currentness requirements, not a common retry policy.
- [special] **P45** `rt/native_merges.go:171,220`, `rt/native_product_pending_reviews.go:239`, `rt/native_product_inlines.go:271` require repeated accepted-input/review/preservation proof; counts must preserve these safety obligations.
- [special] **P46** `fp/discussion.rs:67,72` runs the complete status bracket even for check-run projection returning no items, duplicating native evidence work across the two shared check families.

### F3 — Page selected collections without repeating whole preparations

Make thread browsing, immutable file-catalogue acquisition and pending-review
preparation distinct collection operations inside their existing provider
modules. Ordinary thread reads request the selected page/detail using the
existing cursor infrastructure; bind cursors to scope, repository, pull and
observed head/base. In Fountain, fetch bodies only for reviews referenced by
selected native threads. Where the host can only supply a complete bounded
catalogue, acquire it once for this operation and project locally; repeated
helper calls must not recollect it. Any reuse across requests needs a fresh
matching host revision and current enrollment; it remains display evidence.

Inline preparation owns one opening bracket, one complete immutable file walk
and one closing bracket. Each file page is just a file page; it does not repeat
pull/repository/comparison reads. Use supported larger pages only if existing
response/byte bounds permit them. At the current page10 size, 3,000 files then
require300 file pages plus fixed checks, rather than1,500 file-related requests.
Completion of the whole catalogue is still required before a seal is usable.

Keep the review's own capture/seal type next to its acquisition, hashing and
submission validation; inline selection uses that capture rather than copying
its JSON/digest recipe. Preserve the original byte encodings and pending human
comments. A retained seal is evidence to compare under fresh authority, not
permission to send after expiry. No page-level partial result means a complete
pending review. Do not increase pending-comment page sizes blindly: the current
two-comment page exists to bound large bodies/JSON responses. Cost admission
must refuse an over-budget complete preparation before a bulk walk, or resume
non-authorizing collection through the existing preparation journal. A resumed
collection needs its complete closing checks before fresh preparation authority.

**Done when:** thread page cost follows selected pages/reviews rather than all
history; inline file-page cost has coefficient1 with a constant-size common
bracket; typed capture creation and verification share one implementation;
worst-case bytes/pages/attempts have enforced bounds, with explicit incomplete
or too-costly results; page/head/base/withdrawal races preserve all human work
and refuse stale submission. Numeric legacy cursors remain safely decodable or
explicitly require a new read, without renewing a historical command.
**Depends on:** F2's fact readers and operation budget.

- [hidden] **P05** `gp/inline_preparation.rs:59` accepts a 3,000-file catalogue using page10/full diff brackets, potentially 1,500 attempts before pending-review work, while `gp/traffic_budget.rs:14` permits600/hour.
- [hidden] **P06** `gp/review_preparation.rs:56,187` and `gp/review_submit.rs:128` share pending-comment digest/dispatch conventions outside a single contract owner.
- [hidden] **P07** `gp/inline_preparation.rs:149`, `gp/inline_draft.rs:103`, `gp/inline_create.rs:79` share selection/catalogue/pending hashes through independently maintained JSON shapes.
- [hidden] **P19** `fp/discussion.rs:91,126,164,169` couples requested thread/page selection to full collection, every review's comments and replay of all captures.
- [needless] **P21** `gp/review_threads.rs:156,239` rereads the whole≤1,000-thread catalogue and only then slices the requested local page, tying one page's cost to all threads.
- [needless] **P22** `gp/inline_preparation.rs:59,141` composes a full diff bracket per file page and another final header; catalogue paging repeats already observed common facts.

### F4 — Resume inventory work within an honest provider budget

Use the existing Go inventory collector, cursor and readiness-work records as
the owner of each finite scan. Replace the native `ReadIssues` path that returns
a whole repository with bounded provider page/evidence steps behind the
existing completion enumerator. Native receipts report actual sends consumed;
remove the obsolete completion accounting exports once all real adapters are
wired to the defining budget. Control does not mirror a Forgejo page constant.

Creation discovery advances its retained source cursor; it does not fetch two
whole memberships again for every16 candidates. Share facts already acquired
in the same scan/phase. Where the provider supports it, acquire the necessary
selected-issue fields in one bounded query rather than N individually bracketed
calls. Do not substitute list summaries for missing original/unedited creation,
creator permission, dependency or input evidence. Remaining proof steps consume
the same pass allowance, including their closing checks.

Preserve completion's two independent complete inventories and local/target
captures. Store partial observations and hashes through the existing collector;
a restart continues observation work only. It does not assert a provider-wide
clock or mark a partial scan complete. Changed generation, local material or
observed membership invalidates the relevant collection; dispatch still reads
current authority. If a provider cannot prove required equality within bounded
work, completion remains pending with its exact reason, rather than spinning
inside one RPC or silently weakening the proof.

Unify the existing next-eligible/due calculation so the outer5min scheduler and
inner60s guards express one actual cadence. Budget deferral resumes a known
remaining step; an explicit action wakes only its exact retained target.

**Done when:** small controlled send allowances bound total native attempts
across pages and candidate counts; repeated creation pages do not repeat prior
full traversals; a large inventory progresses through retained page steps;
interruption/withdrawal preserves cursors without duplicate effects; completion
seals only two complete equal observations. No API ceiling is used as a normal
scheduling strategy and no unused accounting function claims coverage.
**Depends on:** F2 for native wire usage/refusal, not F3's UI collections.

- [hidden] **P09** `ctl/completion_observations.go:12` charges a fixed bracket while `rt/native_completion.go:65` performs two full inventory/input traversals; nominal pass accounting changes independently from real provider fan-out.
- [hidden] **P10** `ctl/creation_recovery.go:69` charges a page unit while `rt/native_creation_discovery.go:104,168` reads complete membership twice plus candidate facts; retained page size does not bound wire reads.
- [hidden] **P11** `ctl/readiness_work.go:122` mirrors a Forgejo page cap while native creation/completion use their own limits; shared budget comments name a different implementation owner.
- [surplus] **P28** `ctl/readiness_work.go:137,155` exposes completion page-accounting helpers with no production callers found; their comments imply bounds that native completion does not use.
- [special] **P42** `ctl/progress.go:269` and `ctl/creation_recovery.go:63` compose5min outer reconciliation with60s inner eligibility; the effective cadence is not the inner timer alone.

### F5 — Let the mounted view own display reads and action admission

Keep one selected-repository/pull view model in the existing Lit parent. It
owns the pull, display capabilities, selected tab, request cancellation and
in-flight deduplication for that scope/generation. Child review/merge controls
consume those display facts and fetch action-specific preparation only when
opened or used. Opening conversation must not automatically run both complete
review and merge admission sequences. Command submission still goes through
fresh backend/native authority; passing display facts grants nothing.

Retain drafts and journal identities across ordinary refresh/hide/show. Hiding
aborts I/O and retires live authority while preserving inert presentation and
writing; resume first verifies the selected generation/account locally, then
refreshes visible stale material once. Keep command contexts retired on actual
scope/account/generation change. Do not use mount/remount as a refresh protocol.

Project the work progress list's recorded plan/detail data together in the
existing Go read API, with bounded pagination. SQL/local workflow display
should not cause one provider-bound request per displayed member. Render
per-item unavailable/unknown states, including failed allowance reads, rather
than treating missing evidence as empty/success. Preserve the existing denied
state when capability fallback fails and existing permission-aware disclosure.

**Done when:** one conversation navigation does its pull+selected-content reads
without eager child admission; each opened action consumes shared display facts
and only its own preparation; repeated render/resume has no duplicate active
fetch; work-page requests are bounded by pages rather than2N; drafts/original
command lookups survive hide/reconnect; failures stay visible without enabling
an action. Use the existing semantic developer controls/harness in refactoring,
not screenshots or live API polling to infer request counts.
**Depends on:** none; existing backend preflight remains authoritative. F2/F3
improve the called operations but are not prerequisites for removing UI fan-out.

- [hidden] **P12** `ui/pulls/detail.ts:359`, `ui/pulls/review.ts:76`, `ui/pulls/merge.ts:85` compose eager controls with separate authority reads; opening one view implicitly creates several independent observations.
- [hidden] **P13** `ui/issues/detail.ts:142` and `ui/issues/screen.ts:88` refresh/rebind child actions, so observation cost depends on component lifecycle as well as explicit navigation.
- [hidden] **P14** `frontend/desktop/connections.ts:339,407` ties visibility retirement/reconnect to product-view remount and repeated admission.
- [needless] **P25** `ui/pulls/review.ts:74` and `ui/pulls/merge.ts:83` independently implement the same identity→pull/capabilities admission sequence.
- [needless] **P26** `ui/work/progress.ts:307,330` fetches one detail and one plan per issue; page observation cost grows with every displayed member.
- [special] **P43** `ui/pulls/detail.ts:145,151` swallows capability fallback failure and disables head-bound controls; unavailable evidence is not permission.
- [special] **P44** `ui/work/progress.ts:316,323` falls back when plans cannot be decoded; Feed allowance aggregation drops failed reads (`ui/feed/feed.ts:534,544`), hiding per-item failures behind aggregate rendering.

### F6 — Let the Fountain connection own consent, admission and grant watching

Inside Fountain's current native owner, define consent purpose once and derive
scopes, discovery endpoint and enrollment family from it. The UI selects a
purpose and displays native-supplied consent facts; it does not parse scope
literals to recreate policy. Issue-only read authority stays distinct from code,
execution and publication authority.

One supervisor-owned watch per exact host/account/enrollment/grant/runtime/
generation shares the existing liveness result among attached leases. Keep the
current one-second-after-response polling cadence initially; reduce duplicate
watchers without slowing withdrawal detection. Claim/admission and sends still
perform current checks. Each lease keeps its own hard deadline/retirement; a
failed, withdrawn or changed watch cancels all attached leases. Last-detach
stops its task. A matching cached watch is not authority for a new claim.

In SodaOS, construct one admitted background owner and pass that owner into all
ports; make independent bootstrapping inaccessible to those ports. Keep the
existing at-most-one rebootstrap after a definite401, with single-flight
coordination and original operation lookup rules. Do not introduce that replay
policy into native HTTPS or uncertain mutation handling. No SSE service or new
long-lived authority channel is required for this change.

**Done when:** consent purpose cannot select mismatched scopes/routes; L leases
sharing one exact grant cause one watcher stream, not L; differing grants remain
separate; expiry/failure/withdrawal cancels each consumer on the existing bound;
one cold appliance admission is shared by all ports and simultaneous401 replies
cause at most one rebootstrap. No retired lease, grant or command is renewed.
**Depends on:** none; host SDK/composition changes stay in canonical owners and
do not add a Fountain-server dependency to the standalone engine.

- [hidden] **P15** `frontend/desktop/fountain-login.ts:6`, `cmd/soda-desktop/src/fountain_signin.rs:168`, `fp/enrollment.rs:68`, `fountain_oauth.rs:164` separately classify identical scope literals into consent/config/enrollment behavior.
- [hidden] **P20** SodaOS `internal/forgejo/background.go:11` and `internal/web/server.go:131,137` require one shared admission instance because another bootstrap revokes the first; construction identity is part of correctness beyond port types.
- [special] **P47** `factory_provider/controller.rs:285` reads grants then sleeps1s per lease, independently of product reconciliation and snapshot status checks.
- [special] **P49** SodaOS `background_transport.go:43` permits one shared bootstrap/retry after401; native Fountain transport has no equivalent automatic retry.

### F7 — Remove native Fountain egress with no consumers

Reconfirm callers across Factory, SodaOS and canonical Fountain before deleting
unused native repository-resource and completion-note APIs/capture plumbing.
Keep the real repository product read, execution claim/retirement and publication
submission/lookup paths. Do not remove public Fountain server endpoints merely
because the desktop adapter has no caller. A discovered external consumer is a
reason to revise this decision, not to delete its interface silently.

**Done when:** the unused native functions/protocol branches and their dead
exports are gone; all actual consumers still build and use their original
journals; no compatibility stub introduces new traffic. Historical captures
remain preserved and known versions refuse/recover under their original rules.
**Depends on:** none; caller confirmation precedes deletion.

- [surplus] **P29** `fountain_provider.rs:378` and `fp/operation.rs:241,323` expose repository-resource/completion-note APIs with no production consumers found, retaining additional network/capture behavior to maintain.

### F8 — Journal feedback delivery and bound legacy receipt searches

Put feedback dispatch identity/state in the existing Go post-effect/command
journal, keyed by source post/revision, exact actor/host/repository/issue and
body digest. The SodaOS adapter returns a native comment receipt for that
identity; a fresh admitted intent can dispatch once, and uncertain dispatch
becomes lookup-only. It must not call the request-bound human SDK under an
agent identity or infer authority from read enrollment.

Carry a bounded comment-search cursor through existing recovery work for old
marker-based sends. Matching requires the exact known provenance/marker and
captured content, while preserving human edits and reporting mismatches for
attention. A completed search with no match does not authorize another old POST.
New commands with a retained receipt do direct receipt lookup instead of
walking history. No host API addition or synthetic receipt is assumed.

**Done when:** new feedback has at most one POST per original identity; lost
reply/restart recovers the original comment or remains uncertain; a large old
comment history is scanned in bounded resumable batches; no marker absence,
expired authorization or edited human text causes another send or rewrite.
**Depends on:** none. This extends the existing Go effect journal/adapter, not
a second durable state machine or F6's watch service.

- [special] **P48** SodaOS `internal/forgejo/issue_feedback.go:45,60` scans arbitrary50-comment pages for idempotence markers before posting; history length controls lookup cost.

### Leave as is

These13 entries are real requirements or deliberately separate traffic owners.
They remain visible acceptance constraints for the refactor; leaving them does
not mean the product is verified. No broad retry policy or generic transport
will erase the distinctions.

- [hidden] **P17** `fp/issues.rs:33,197` and `rt/native_fountain_issues.go:35,61` independently encode issue-operation receipt/protocol assumptions across callback boundary. **Reason:** This is a real cross-language trust boundary: Rust validates host effect/provenance while Go validates the captured canonical intent and receipt. Keep the frozen explicit binding and independent checks; a second write/receipt protocol solely to remove five literals adds migration risk without reducing calls.
- [special] **P32** `gp/merges.rs:278` and `gp/git_publish.rs:236` reject Fountain-only fields in shared operation structures; provider vocabulary leaks into peer validation. **Reason:** Known-prior flat mutation captures must retain their original bytes and provider-only fields. Preserve the fail-closed provider fence before admission; the new read envelope does not authorize rewriting historical mutation captures.
- [special] **P30** `gp/review_request_effect.rs:133,136` must add reviewers through GraphQL and remove through REST with distinct actor payloads. **Reason:** GitHub's supported add/remove reviewer APIs genuinely differ; hide the difference inside its connector, with no transport unification.
- [special] **P31** `gp/merges.rs:91` and `gp/mod.rs:76` bind result verification to an API-version-dependent GraphQL merged-commit convention. **Reason:** Keep exact reviewed-candidate versus resulting-commit verification under the configured API version; no evidence supports removing it.
- [special] **P33** `gp/evidence.rs:208` and `gp/protection.rs:43` preserve403/404 as unknown protection evidence rather than absence; authority concealment must remain fail-closed. **Reason:** Concealed/unknown protection must stay unknown and block unsupported merge behavior.
- [special] **P35** `gp/writes.rs:82` and `gp/transport.rs:693` can retain a dispatched mutation before transport-budget refusal, producing indeterminate outcome even for suppressed wire dispatch; lookup absence still must not authorize resend. **Reason:** Preserve the conservative original dispatch classification. A new local budget observation cannot retroactively prove an old effect was not sent.
- [special] **P36** `gp/oauth.rs:163` polls authorization pending/slow-down, unlike other transport requests; OAuth cadence and ceiling share the same wire budget. **Reason:** Device authorization needs its provider-defined pending/slow-down polling; it remains bounded by device expiry and the original traffic budget.
- [special] **P40** `ctl/merge.go:256`, `ctl/progress.go:261,318` distinguish a fresh exact wake from replay, using transient wake hints plus durable authority. **Reason:** Exact explicit wakes and durable journal authority prevent queue replay and stay intact under the new read scheduling.
- [special] **P41** `ctl/review_cycle.go:116,134` and `ctl/progress.go:70,109` retain1min negative/closed caches with explicit-command bypass rules. **Reason:** Negative/closed caches and explicit fresh-command bypass have different purposes; retain their present semantics while eliminating duplicate reads.
- [special] **P50** SodaOS `publish/operation_push.go:66` permits one rebound push for a retained pending host operation, unlike native receive-pack's no-resend rule; recovery semantics differ by owner. **Reason:** Retained pending appliance operations have a host-defined rebound contract; do not extend it to native publication or infer it from lookup absence.
- [special] **P51** `scripts/desktop-distribution.ts:49,71` makes exact legal-file acquisition conditional on package contents/provenance, creating build-only GitHub calls outside app budget. **Reason:** Exact dependency legal/provenance acquisition is required build traffic; existing exact inputs and local packaged files are already preferred. No unrelated download framework is justified.
- [special] **P52** `tools/native-checks/native_mcp_signing_check.go:8340,8346` contains deliberate download repetition/curl retries; check-tool traffic must stay distinct from product cadence. **Reason:** Deliberate fixture stress/retry traffic is opt-in check behavior, not product polling. Preserve it and keep counters/results scoped separately.
- [special] **P53** `factory_provider/setup_network.rs:90,160` bounds encrypted public HTTPS tunnels by connections/bytes, while GitHub request accounting sees no individual tunneled requests. **Reason:** Encrypted public setup tunnels have connection/byte/lifetime bounds, not visible HTTP counts. Keep credential isolation and declare that limitation; do not intercept TLS or claim the app's authenticated request counter covers tunnels/builds.

### Dependency and acceptance review

After these changes, UI depends on product read/command contracts; Go workflows
and adapters depend on native protocol bindings; both Rust providers depend on
neutral semantic types and their own transport/fact owners. Fountain host SDK
and authority stay outside the standalone engine. Stable contracts import no
provider clients. No new provider-neutral policy engine, global mutable cache,
parallel workflow journal, webhook/SSE service or generated binding framework
is part of this design.

Pieces that change together now share their real owner: contract and metadata
(F1), operation facts/boundaries/cost (F2), collection and seals (F3), scan progress
and accounting (F4), view lifetime and reads (F5), Fountain connection/grant
lifetime (F6), dead APIs (F7), original feedback effect and receipt (F8). Facts
from different providers, human actions, funding and native execution retain
their distinct authority owners. F2/F3 reuse observations only where their
proof boundary permits it; no savings target permits skipping fresh evidence.

Refactor verification must first use controlled responses and existing native /
Go integration entry points: count actual attempts for normal, maximum-page,
changed-policy/head/base/permission, withdrawal, refusal and interrupted-recovery
paths. A refusal must stop sends and preserve partial observations/effects.
Retained unknown outcomes are never resent; paid inference is unnecessary for
these contracts. Reuse the existing developer API for UI facts and counters.
Only an affected connected journey can subsequently qualify product behavior;
source/build/component success alone cannot do that. There are no new tests,
builds, apps or provider requests in this design phase.

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

- 2026-11-12: phase2 redesign completed at6076304 plus this document change;
  all53 findings assigned exactly once, all24 special cases translated into
  requirements. Eight fixes address42 entries;11 necessary cases remain.
  Existing source maps were reused because the intervening commit changed only
  the audit document. No builds, tests, provider requests or product changes.
  Owner review: “No additional constraints.”
  Next phase: untangle-refactor, beginning F1/F2 or an explicitly independent
  ready fix; preserve original receipts and prove wire counts locally first.

- Phase3 ACTIVE, F1 COMPLETE in c7a417d and34f0a6f. Shared native DTOs/validation/outcomes are below both
  providers; setup vocabulary and read limits have defining contract owners.
  Semantic names derive from serde tags. Version1 uses one payload; known prior
  read shapes and all captured mutation bytes/digests remain intact. Unknown
  versions, mixed payloads and excess native response size refuse.
  Baseline backend/harness and native compile plus progress/external realPG
  cases pass in fresh pg3/pg4 profiles. Core checks: f1-rust-final.log,
  f1-native-contract.log, f1-progress-pg.log (legacy/v1/future/mixed and closing
  withdrawal), f1-external-pg.log. Limit bindings: f1-limits-build.log,
  f1-limits-go.log, f1-limits-contract-final.log (actual encoder refuses oversized payload),
  f1-limits-build-final.log. All under
  /home/vince/Projects/factory-provider-audit-20261112/refactor/.
  Independent review caught two field mappings, corrected and added to the
  existing check; final source recheck found no further checkpoint issue.
  Plan refinement: retain P17/P32 as required legacy receipt/provider boundaries;
  removing those mappings/fences would change captured write semantics for no
  request reduction. 40 entries remain assigned to fixes,13 retained with reasons.
  F2 COMPLETE (binding slice dfbd48e and operation-budget/compound-read76fa12c). One inherited allowance/deadline reaches both HTTP sinks; native
  cancellation closes admission before usage publication. Go refunds only
  validated actual usage and retains lost/invalid replies' whole allocation.
  Native limits64 ordinary/80 setup/384 inline are clamped by the pass remainder;
  no caller can increase them. Existing provider ceiling/holds and conservative
  mutation classification remain. Actions run facts share one observation phase.
  Fountain review/check/merge projections consume one supported family union
  with equal closing revision/material/enrollment, plus paired repository facts.
  Evidence under the existing refactor directory: f2-budget-local.log (60 branch
  cases,12 invalid descriptors,retirement and exact0/1/5/8-send refusal),
  f2-fountain-final-local.log (merge11/checks9/empty-runs4, four conflicts+4,
  operation-wide retry exhaustion, delayed-body deadline, changed revision/head/
  permission/enrollment, human veto/pending), f2-host-contract.log (canonical SDK
  validates union offline), f2-usage-progress-pg.log (actual-use/double-refund/
  uncertain-allocation assertions plus existing realPG progress gates),
  f2-native-final-build.log. Fresh review's finalization, inherited ownership,
  setup allowance and direct Fountain body-deadline findings were corrected.
  Native async reads are boxed at the connector boundary to avoid multiplying
  the already large selector future on the stack; local branch check passes.
  These controlled checks qualify this refactor, not connected/installed gates.
  F3 IN PROGRESS: inline file pages now have one opening/closing diff bracket,
  coefficient1 page cost and complete pending-body preflight. Same owner handles
  pending comparison. f3-inline-local.log passes selected existing native cases:
  13files24 sends;3000files322 sends; changed pending/count/base refusal,
  invalid selectors and owner retirement. First fixture's unfiltered negative
  loop hit its local600 ceiling; preserved, selector corrected, no live traffic
  or original budget/state cleared. Fresh file-catalogue review found no issue;
  capacity error ordering was restored. F3 file slice is committed separately.
  Uncommitted F3 thread integration: selected GitHub GraphQL page; scope-bound
  thread/comment continuation through Go; Fountain selects metadata/members then
  reads only their referenced reviews, retaining paired snapshots/body hashes.
  Native and Go builds pass, but thread controlled qualification and shared
  capture/seal consolidation remain. F4–F8 pending. No live/model effects or
  original profiles/uncertain outcomes changed. No checks running.
  Next: qualify new thread cursors/selected-page request bounds through the
  existing no-forwarding thread fixture; finish capture owner, then F4.
