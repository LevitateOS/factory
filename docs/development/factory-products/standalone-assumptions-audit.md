# Standalone Factory assumptions audit

2026-11-06. Source baseline: `aa6b6d9`; the working tree was clean when this
audit started. Scope: requirements, authority, supported workflows, runtime,
UI and delivery assumptions across standalone Factory and its shared consumers.
The old implementation queue was paused for this audit. Corrections now follow
the owner decisions below. Audit recommendations alone do not adopt requirements
or qualify any security gate.

## Owner decisions after the audit

The user started the correction goal and selected these requirements on
2026-11-06:

- The local computer's owner controls local execution and spending. Provider
  permissions are checked separately for each remote action (A02/A03).
- Factory always requires the full automated pipeline. The proposed planning,
  local-change and PR-only delivery modes were not selected (A07/A10). Preserve
  required review, CI and merge stages while correcting incompatible contracts.
- Independent agent inspection works with one GitHub account. A second provider
  account or bot is optional; submitted approvals and merges must still obey
  the provider's actual rules (A04). A second personal account is not required
  for Factory and cannot substitute for independent human approval. The earlier
  second account was an optional two-user integration fixture.
- Existing Muse session reuse and a browser link only when authentication is
  needed remain the earlier explicit instructions (A01).
- Hiding/minimizing Factory retains the account/repository choices and reconnects
  after fresh status on return. Explicit Disconnect keeps it disconnected (A12).
- Support ordinary Go/Rust/Node/Bun project setup, including installing declared
  dependencies inside isolated workspaces (A17). The current system-tools-only,
  offline preparer does not meet this requirement. Approved provisioning and
  meaningful resource limits must be implemented and qualified. On 2026-11-07,
  the owner approved public HTTPS for reviewed setup, with private/local network
  destinations and desktop credentials unavailable.
- Automated GitHub delivery must use an enabled GitHub merge/squash/rebase method
  rather than requiring fast-forward-only delivery (A07).
- Drafts/history require a GitHub connection; offline profile ownership was not
  selected (A09).
- Each role selects its provider, model and paying account/subscription explicitly;
  do not switch spending accounts automatically (A11).
- For Muse, a role may explicitly select **this Muse login**. Muse owns the saved
  authentication choice, including changes made inside Muse; Factory does not pin
  or claim a verified subscription/billing identity for that source. This is the
  owner's selected exception to account pinning, not permission to copy login
  credentials or switch between Factory sources automatically (A01/A11/A19).
- For that CLI-managed source, Muse owns provider token/request limits. Factory
  enforces the selected source, run allowance, deadline, model-step cap and
  captured-output limit. The owner approved this change from the historical
  relay's per-request token cap; do not claim equivalent provider limits (A19).
- Ship execution first on Linux desktops with systemd and one configured execution
  user per machine. Other environments retain browsing with execution unavailable
  (A18); installed isolation/lifecycle qualification remains required.
- Include the owner's own newly created GitHub issues in automatic intake. Prevent
  recursion using recorded Factory operations/origin, not account inequality (A08).
- On 2026-11-07, retain repository navigation independently of the current route
  and keep Feed scoped to the selected repository. Aggregate Feed was not selected
  (A24).
- On 2026-11-07, one Builder implements planned tasks sequentially. Required
  parent/child orchestration was not selected. Preserve task checks/checkpoints,
  independent review, CI and merge; no full-pipeline prerequisite is removed
  (A04/A19). New native implement manifests use `max_children: 0`; historical
  child manifests remain immutable and readable.

The original audit and its recommendations below retain their baseline scope.
They are not all selected requirements. In particular, A07's proposed simpler
delivery modes are superseded by the full-pipeline decision above. Execution
corrections are now active; the old feature queue waits for their resolution.

### Verified corrections

- **A01/A11 — CLI-managed setup and admitted execution implemented; qualification pending.**
  Rust supplies a stable profile-scoped “This Muse login” reference, not a Meta
  account ID. Go records `authentication: muse_login` without a credential
  fingerprint; the historical token-bound lane retains its exact binding.
  Shared Work configures/withdraws the source's local minute allowance and saves
  explicit independent role choices. No source is selected automatically. Native
  product requests admit the exact source selector. Version-4 launch intents now
  capture the CLI-managed variant separately from token-bearing worker launches.
  Actual Rust/Go/PostgreSQL setup, readback, stale-revision refusal and withdrawal
  passed in `/home/vince/tmp/fmuse-source-native7.fPtpBb/result.log`; actual shared
  Chromium interactions passed against a declared peer in
  `/home/vince/tmp/fmuse-source-ui7.SX4Qoc/result.log`. Neither check accessed Muse
  authentication or ran a model. Those checks qualify configuration only.
  A separate supported CLI call using the existing saved authentication completed
  one model request and returned `OK` in
  `/home/vince/tmp/fmuse-login7.UGvmC3/result.json` (`output.jsonl`, `stderr.log`).
  It inherited no `META_API_KEY`, used no new sign-in and changed no personal
  keyring. This proves saved-login inference, not Factory execution, billing
  identity, role-tool confinement or isolated/installed qualification.
  The native CLI connector now reuses the existing process owner in a dedicated
  delegated user service, seals the selected ELF, applies the selected model,
  effort/contributor/step/deadline/output choices and retains exact process facts.
  Built-in file/shell/web tools are disabled, hook overrides are applied, and inherited API-key
  variables are excluded. Local finite/cancel/fresh-liveness and tree/service
  retirement checks passed in `/home/vince/tmp/fmuse-connector7.GGkebr/result.log`.
  Real saved-login inference through
  that connector passed in `/home/vince/tmp/fmuse-owned-login7.co8TBb/result.log`:
  one request, `OK`, exact captured output, process tree retired and service stopped.
  That earlier check was a native connector component, not a Go-admitted run.
  The existing execution registry now owns both variants. Shared source staging
  and MCP response handling retain one implementation. Actual admitted Go/native/
  PostgreSQL execution passed in `/home/vince/tmp/fmuse-admitted-live7.fLSf9g/result.log`:
  three completed Muse requests, only the scoped artifact/incomplete-result tools,
  exact artifact bytes, source-specific minute usage, coordinator settlement of
  the intentional incomplete fixture result, and process/root retirement. Provider
  repository permissions were declared; no GitHub writes or implementation ran.
  The final host-PID receipt projection and answer-only product output passed
  with a local ELF in `/home/vince/tmp/fmuse-admitted7.eFQxB0/result.log`.
  The shared token lane's admitted artifact/retained-restart settlement passed in
  `/home/vince/tmp/fmuse-legacy7.xRn8IG/result.log` with its private dummy keyring.
  Source availability now reflects the installed native helper/tool owner; missing
  support waits without switching sources. Sequential task phases now have a
  Muse-login runtime; the token task lane stays unavailable. Completed-entry acknowledgement now uses the existing
  progress loop and bounded native inventory after canonical assignment/usage
  settlement. Durable CLI receipts remain readable after release and restart;
  the composed local check passed in `/home/vince/tmp/fmuse-recovery7.hYZb3R/result.log`.
  Exact original deadline-run reconciliation passed in
  `/home/vince/tmp/fmuse-timeout-reconcile7.N2QE1d/result.log`, preserving its
  before-state and charging one admitted minute without rerunning inference.
  Accounting now cannot extend beyond the admitted deadline during later recovery.
  Selected-source withdrawal now atomically revokes the run capability and retries
  native retirement through the existing progress loop, even when another source
  keeps repository dispatch open. Actual local CLI/MCP/process/root cancellation,
  original-source usage, unrelated reviewer selection and refusal to revive the
  old capability passed in `/home/vince/tmp/fmuse-withdraw7.1jegf3/result.log`.
  No provider request or saved-login mutation ran in that check.
  New CLI receipts now retain the process-tree stop time using elapsed native
  time from launch, before output persistence. Go and PostgreSQL preserve it
  immutably and end usage there even after delayed recovery. The original local
  process from `/home/vince/tmp/fmuse-stop-accounting7.cLGP1Y` was recovered without
  replay in `/home/vince/tmp/fmuse-stop-recovery7.vmkMK3/result.log`; its before-state
  is retained there. Exact stop-based usage and database mutation refusal passed.
  Historical receipts without a stop time still use the admitted deadline bound;
  no historical timestamp is invented. The token lane has no stop-time receipt yet.
  Desktop staging now builds and requires `soda-factory-muse` beside the app,
  matching the native helper lookup. The actual cached development stage passed
  with the retained desktop sysroot; both staged executables matched Cargo's
  output and retained executable permissions. Build log:
  `.artifacts/desktop-muse-stage7-build.log`. This is not installed qualification
  or a Muse/toolchain/service installer.
  Active-service/incomplete-receipt recovery, connected withdrawal/refusal, reviewer execution, broader
  role/configuration confinement and installed qualification remain open.
  No broad security or full-pipeline gate is closed.

- **A05/A19 — agent source reads implemented; pipeline qualification pending.**
  `factory.read_source` exposes the existing Rust isolated preparation reader
  through the Go worker service. The capability selects assignment, preparation
  and immutable source commit; the agent selects a relative path and bounded
  byte page. Results include ancestor `AGENTS.md` instructions. Existing native
  context and 64 KiB MCP reply bounds remain; whole-file/instruction reads retain
  their 32 KiB bound. Missing/non-text/oversized selections refuse.
  Discovery, launch policy and current-role metadata share
  the available catalog; hosts without a source reader omit this tool.
  The actual admitted dummy worker compared source commit, file bytes and root/
  nested instructions against its prepared repository, then confirmed a missing
  file refuses. MCP artifact publication, explicit incomplete result, resource
  retirement and restart settlement passed in
  `/home/vince/tmp/fsource-admitted7.r9qiEb/result.log`; the exact source-read
  marker is retained in that profile's run receipt. Current Go builds, affected
  existing MCP transport/context tests and dependency boundaries passed.
  `factory.list_source` now lists immediate tracked entries at that same captured
  commit, with root/nested instruction closure. The shared context contract
  carries an optional directory and bounded path/kind entries; Rust performs the
  Git tree read in the existing isolated context service. It identifies files,
  directories, symlinks and submodules without following links. At most 256
  entries and the existing context/reply bounds apply; oversized results refuse.
  The actual admitted worker compared root/nested names, kinds and commit,
  including a symlink, and confirmed missing-directory refusal in
  `/home/vince/tmp/fsource-directory7.p48gW7/result.log`. File/instruction reads,
  artifacts, explicit incomplete result, retirement and restart also passed.
  `read_source` now supports UTF-8 pages above that historical whole-file bound:
  default 8 KiB, optional 4–32 KiB limit, byte offset/total and a next offset.
  Native Git output is drained without retaining the rest of the blob, then
  joined under the existing timeout. Source/context owners validate exact page
  binding and progress; other preparation reads retain their whole-file contract.
  The admitted worker reconstructed all 42,017 bytes over six pages, including
  a character split at the nominal page boundary, and refused an offset inside
  a multibyte character. Artifact publication, explicit incomplete product result,
  retirement and restart settlement passed in
  `/home/vince/tmp/fsource-pages-final7.04RK9B/result.log`. The existing integration
  observer now uses the canonical admitted deadline plus retirement grace; its
  old independent 15-second cutoff cancelled an earlier run. That original was
  separately recovered without relaunch after correcting the Go recovery
  inventory to include observed processes whose final callback was missed.
  The security handoff records original before/after and durable accounting/
  blocked-result proof; missing-evidence and other retained profiles remain open.
  Native/Go builds, affected existing MCP tests and boundaries passed. No model
  request, saved-login change or GitHub action ran. These are immutable source
  reads/listings, not mutable task access, directory paging, native task execution
  or installed/authority-race qualification. Submodule/over-limit listings and
  large-file workload/resource ceilings were not qualified by these checks.

- **A04/A19 — sequential native task runtime composed; qualification pending.**
  New task intents use version 5 to capture the exact phase and manifest digest
  in the Go transaction. Go launch transports the original manifest bytes;
  Rust bounds and binds its run, plan bytes, deadline, profile and child limit,
  then retains it beside the one-shot intent before custody/source effects.
  The run constructor owns publication once; retained readers refuse missing,
  changed or pending material. Historical non-task intent encodings are preserved.
  Actual Go-encoded token/CLI manifest readback, altered-byte refusal and a
  pending-marker refusal passed in
  `/home/vince/tmp/ftask-binding-only7.tjaJI6/result.log`. This uses declared
  fixture metadata and an unexecuted plan; it does not qualify task admission,
  the version-5 publisher under interruption, child execution or checkpoints.
  The current Go-admitted non-task MCP/artifact, retirement and restart journey
  passed in `/home/vince/tmp/ftask-admitted-fixed7.mWt3yM/result.log` after fixing
  duplicate intent publication. Native/Go builds and dependency boundaries passed.
  No model or GitHub operation ran in that manifest/non-task check. Its original
  scope did not implement task effects or qualify an admitted coding lifecycle.
  Failed earlier fixtures remain unresolved in the security handoff. The owner
  selected sequential Builder execution. New native implement dispatch stages
  zero children; manifest readers retain historical child variants. Go imports
  parent task events across the multi-task plan with a one-active-task limit;
  Builder output release and context use that captured mode.
  Go-produced sequential token/CLI manifests and retained child/correction modes
  passed the existing Rust sidecar readback, changed-byte and pending-marker
  checks in `/home/vince/tmp/fsequential-contract7.LviXGj/result.log`.
  Go/native builds and affected existing MCP checks passed. This is contract
  readback only: no task ran, no typed native event journal was produced and no
  checkpoint or sequential task lifecycle is qualified by it.
  The existing isolated agent now consumes a root-captured manifest and owns
  sequential task edits, bounded text reads, frozen outputs, exact declared
  checks, Git checkpoints and final checked candidate export. Its typed events
  use the existing Go import contract. The real native integration passed two
  dependent tasks and exact Go event replay, plus failed-check, ownership and
  dependency refusal, cancellation during a checkpoint request, and exact root
  retirement: `/home/vince/tmp/fworkspace-capture7.SGFYsj/result.log`.
  No model/provider request or Go-admitted coding run was involved; the parent
  invocation was fixture-supplied. The credential-free session retains the
  existing relay namespace with a non-serving desktop socket, without borrowing
  credentials or forwarding requests. Task-control requests are now retained in
  the private native profile before effects; the broker retains completed
  replies before delivery, and the native owner retains receipts/journal/final
  candidate bytes before returning them. Exact retries, broker readback after
  withholding a disposable local copy, foreign invocation refusal, and exact
  task/final/candidate readback from a new process after root retirement passed:
  `/home/vince/tmp/fworkspace-receipts7.XNzS1t/result.log`. A cancelled checkpoint's
  pending request survives without a fabricated result or effect replay. This
  does not qualify actual lost transport replies or publication interruption.
  The Muse-login run owner now prepares the isolated task workspace before
  inference and binds it to the actual observed process incarnation. Scoped
  worker MCP exposes task admission, bounded text read/edit, directory creation,
  exact-digest file removal and output release. Go implements the task, task/final
  checkpoint, journal and candidate ports using the existing native callback.
  Stable mutation identities retain exact replies; current Go admission and
  independent Rust process queries remain required. Stored receipt uncertainty
  is not treated as a liveness decision. Native terminal/startup recovery copies
  completed pending receipts before retiring task roots. Token task sources stay
  unavailable. Admitted verification and remaining limits are recorded below.
  Actual admitted investigation produced and settled its plan; the fixture then
  supplied phase advancement through the canonical plan/dispatch gates. Its
  implementation used the actual observed parent, role MCP, directory/file
  operations, exact edit replay, two sequential checks/commits, eight imported
  events, final integration check and retained candidate export. Evidence:
  `/home/vince/tmp/fadmitted-tasks7.NgVBjI/result.log`. That log ends at a
  premature settlement assertion; the original completed run was resumed and
  passed canonical settlement, exact checkpoint/candidate reads and source usage
  after restart without task/inference replay:
  `/home/vince/tmp/fadmitted-tasks7.NgVBjI/recovery-verified.log`.
  Repository authority and phase advancement were fixture supplied; the local
  CLI made no model/provider request. Go/native builds, affected existing MCP
  tests and dependency boundaries passed. No new unit-test suite was introduced.
  Exact file ownership now also permits creating its containing directory, and
  mkdir accepts an already existing valid directory without broadening file-edit
  ownership. The existing isolated workspace check passed with exact nested file
  paths, directory reuse, both checkpoints, journal replay, retained receipts and
  its affected failure/cancellation/retirement cases:
  `/home/vince/tmp/fworkspace-parent7.1n7a8K/result.log`. No provider request ran.
  The owner-authorized real Muse one-file journey passed in
  `/home/vince/tmp/fmuse-task-wait7.oE1hXM/result.log`: actual planning, Builder
  edit/readback, task/integration checks, candidate export, source usage, retirement
  and retained restart readback without inference replay. The same declared
  repository authority and phase advancement remain fixture inputs. Tool schemas
  now exclude rejected submission IDs; workspace mutations advertise their required
  identity. Context accepts the preceding `execution_revision` as `after_execution`
  to wait at most ten seconds for checkpoint changes, within current admission and
  the existing run deadline/16 model-step limit. The two earlier retained
  `fmuse-task-live7.bqTprF` and `fmuse-task-schema7.jek3Gf` attempts ended before
  final integration and remain failures, not completed coding evidence.
  Remaining: actual correction, admitted cancellation and
  interrupted recovery/publication, broader workloads, connected authority,
  review/publication/CI/merge and installed qualification. Earlier failed local
  fixture profiles remain retained; they supply no completion evidence.
  These changes do not qualify D3, custody/IPC, the full pipeline or installed
  execution.

- **A17 — artifact/log coupling corrected; normal dependency setup pending.**
  Setup/check diagnostics now use a separate bounded collector. Regular build
  files use the existing 64 MiB checkout envelope instead of the 256 KiB log cap.
  A truthful `output_truncated` receipt refuses readiness and dependent native
  launch/context use, even when the script exits zero; normal historical receipts
  keep their existing wire shape. The actual Go/Rust/PostgreSQL/systemd preparation
  check built and ran a captured Node v22.23.2 project with a larger artifact,
  refused diagnostic overflow and confirmed both retirements in
  `/home/vince/tmp/fsetup-node7.JzWINh/result.log`. This was offline with system
  tools and no provider call. The log's inherited final cancellation summary was
  corrected in the check source; this run supplies no new live-cancellation proof.
  Toolchain/dependency provisioning, setup network policy, larger source/storage
  handling and suitable owner-approved memory/storage budgets remain unfinished. A17 and D3 stay
  open.

- **A17 — approved setup/check time budgets implemented.** Optional reviewed
  `setup-limits.json` uses the existing approved-file digest; each deadline is
  1–1800 seconds, with historical ten-second defaults when absent. The preparer,
  worker environment check and native waits use those bounds. Actual composed
  Go/Rust/PostgreSQL/systemd checks passed: a 31-second setup and a one-second
  timeout with check skipped and readiness refused
  (`/home/vince/tmp/fsetup-budget7.UK05cw/result.log`); an eleven-second check in
  the preparation and isolated worker, finite fixture execution, retirement and
  restart export (`/home/vince/tmp/fworker-budget7.MdfKLo/result.log`). No model
  call ran; the worker check used only an owned dummy credential. The earlier
  fixture observer expired at twelve seconds; its preserved uncertainty is in
  the security handoff. Network/toolchain provisioning, memory/storage budgets,
  representative cold builds and broader concurrent/installed qualification remain
  unfinished. Setup waits now retain a per-run directory lock while releasing
  global admission state; active preparations reserve their full storage charge.
  `/home/vince/tmp/fsetup-parallel7.m62A3r/result.log` observed two live preparers,
  prompt cancellation of one while the other remained live, independent setup
  completion and exact root retirement. Same-run replacement/waiter races,
  saturated capacity, lost replies and restart during overlap remain unqualified.

- **A17 — approved public HTTPS dependency setup implemented; wider setup pending.**
  Reviewed `setup-network.json` (`{"access":"public_https"}`) opts the exact
  preparation into a credential-free native CONNECT owner through the existing
  isolated loopback relay. No file keeps the historical offline behavior. Actual
  Go/Rust/PostgreSQL/systemd preparation installed a cold, lockfile-pinned public
  npm dependency and ran its check; loopback IPv4/IPv6/localhost and plain HTTP
  were refused. The same cold install without approval failed and refused
  readiness. Exact preparation/root retirement passed:
  `/home/vince/tmp/fsetup-public-fixed7.vkJXNm/result.log`. The affected legacy
  dummy-token MCP/retained-restart path passed in
  `/home/vince/tmp/fsetup-legacy7.TV4biP/result.log`. No model, personal credentials
  or GitHub operation ran. This used system Node/npm with lifecycle scripts
  disabled; ordinary Go/Rust/Bun provisioning, larger cold builds, meaningful
  memory/storage budgets and installed qualification remain pending. Network
  interruption/recovery and broader address/DNS/limit evidence remain in the
  security handoff; no broad D3 or network gate is closed.

- **A17 — bounded preparation capacity expanded; workload qualification pending.**
  The preparer now has a fixed 1 GiB/no-swap/128-task envelope. Preparation and
  worker transfer share 256 MiB/100,000-entry tree bounds. Active reservations
  charge the full 1 GiB; retained storage is capped at 4 GiB/one million entries
  and still requires the independent `/run` reserve. Actual setup touched a
  192 MiB Node buffer and wrote 80 MiB files into both repository and private
  HOME. The isolated worker copied both trees and reran the approved check;
  admitted dummy-token MCP, exact retirement and retained restart passed in
  `/home/vince/tmp/fsetup-resources7.fkieHH/result.log`. No model or personal
  credentials were used. These are fixed supported ceilings, not selectable
  per-run memory budgets or proof of normal Go/Rust/Bun toolchain provisioning.
  Saturated capacity/low-free-space refusal and installed workload evidence
  remain open. Reviewed time/network choices retain their existing semantics.

- **A17 — private toolchain provisioning and prepared launch reuse implemented.**
  Approved setup can install tools into its isolated HOME; preparation discovers
  required tools after setup. Preparation, worker checks and model children use
  the same private tool directories without inheriting desktop configuration.
  Launch copies the verified, frozen repository/HOME into its own snapshot and
  reruns the check offline instead of repeating setup downloads. Root ownership,
  source inode/receipt matching and a source directory lock fence that copy;
  launch receipts identify the original preparation. Contained absolute HOME
  cache links become relative before publication; escaping links remain refused.
  Actual current Bun 1.4.4 acquisition with official SHA verification, cold
  dependency install, one-time setup, worker recheck, dummy-token MCP/artifact,
  exact retirement and retained restart settlement passed in
  `/home/vince/tmp/fsetup-bun-final7.sTBBZv/result.log`. Native builds and the
  Go/native boundary check passed. No model, personal credential or GitHub write
  ran. An intervening cold tarball request received proxy 403; its exact cause
  remains unverified. Normal Go/Rust provisioning, larger projects, snapshot
  cancellation/replacement/restart races, network failures and installed D3
  qualification remain open. Earlier unresolved launch attempts are preserved
  in the security handoff; this success does not settle them.

- **A17 — ordinary Go/Rust cold project setup verified.** Prepared repository and
  HOME trees now allow 1 GiB each, accommodating an SDK and build cache. The
  worker's private tmpfs is 3 GiB with a 4 GiB service ceiling; its approved
  environment check allows 1 GiB/128 tasks. Preparer memory, full active charge,
  retained quota and independent free-space admission remain 1 GiB, 1 GiB,
  4 GiB and the existing reserve. Rust components are extracted directly into
  the private SDK location to avoid duplicate installer trees. Actual official
  Go 1.27.3 plus `github.com/google/uuid v1.6.0`, and Rust/Cargo/std 1.99.1 plus
  `itoa 1.0.18`, were acquired with verified hashes and built from cold public
  dependencies. Both copied snapshots reran builds offline in the worker, then
  completed dummy-token MCP/artifact execution, exact retirement and restart
  settlement: `/home/vince/tmp/fsetup-go-bounded7.GreToE/result.log` and
  `/home/vince/tmp/fsetup-rust-headers7.qJiQR6/result.log`. Native/Go builds used
  the current patch compilers in a private cache; the dependency boundary check
  passed. No user toolchain/login was changed and no model or GitHub write ran.
  These small projects demonstrate supported provisioning, not all project sizes
  or installed D3. Repeated public artifact HTTP refusals remain unexplained;
  lifecycle scripts/private registries, larger builds, saturation and interrupted/
  overlapping/installed resource retirement remain unqualified.

- **A24 — selected workspace navigation corrected.** The host now supplies
  `selectedRepository` separately from the destination. Desktop passes its admitted
  selection; shared tabs survive New request and Feed links retain the repository.
  The browser check also exposed Request state fields shadowing Lit's update
  accessors; their declarations/defaults are corrected. Actual shared-frame
  Request → Work → Feed navigation, visible Request rendering, desktop context
  selection and retirement on Disconnect passed against the existing declared
  peer in `/home/vince/tmp/fnav-workspace7.b4HkPN/result-fixed.log`. Typechecking
  and shared/desktop UI bundle builds passed. No provider/native request or
  authority qualification ran. Other hosts
  must supply the optional selection field to retain navigation on routes without
  a repository; actual Fountain/installed host parity remains unqualified.
  The shared frame now also normalizes default Feed routes to that selection,
  and prompts without resolving/fetching when no repository is selected. Desktop
  refuses unscoped Feed resolution. Actual declared-browser positive/empty
  selection, navigation and Disconnect passed in
  `/home/vince/tmp/ffeed-selection7.YF38N6/result.log`; typechecking and shared/
  desktop bundles passed. This does not qualify provider scope or the older
  Fountain host composition outside this checkout.

- **A20 — standalone initialization separated; migration qualification pending.**
  Native startup now uses `OpenStandaloneContext`. Schema 93 records the
  application-hosting component: new standalone databases omit its tables;
  retained schemas keep them and their required checks. No retained tables are
  removed. Actual native startup/restart and a schema-92 upgrade preserving
  appliance identity/application rows passed in
  `/home/vince/tmp/fschema-standalone7.bJBah4/result.log`; a missing retained
  hosting guard and an appliance open of a standalone database were refused.
  Actual admitted preparation/MCP/artifact/settlement, retirement and retained
  restart on the new standalone composition passed in
  `/home/vince/tmp/fschema-execution7.AXoLjR/result.log`, using only an owned dummy
  token. No model or personal credential ran. Application DDL and verification
  now share `pkg/store/schema_application_hosting.go`.
  Schema 94 additionally omits appliance users/keys/Projects/memberships,
  maintenance/lifecycle and identity custody from new standalone profiles.
  Setup journals reference native workspaces directly. Retained profiles keep
  their components; `schema_appliance_compatibility.go` owns their defining DDL
  and verification. Native admission uses its canonical decision heads, and
  preparation feed events now resolve the native workspace's repository.
  Actual startup/restart, read-only integrity, five native journal references and
  schema-92 preservation of identity/key/membership/dummy-credential/application
  rows passed in `/home/vince/tmp/fschema-native-only7.hAneg7/result.log`.
  The actual native preparation/MCP/artifact/settlement/retirement/restart journey
  passed in `/home/vince/tmp/fschema-native-journey7.D8pDXJ/result.log` without
  appliance tables or a model call. The two failed intermediate fixtures remain
  retained; the one acquired preparation has its exact `retired.json` receipt.
  Installed, older-upgrade, retained schema-93 composition and concurrent/
  interrupted migration qualification remain pending. PostgreSQL and the Go
  transaction owner are retained; no broad security gate is closed.

- **A08 — same-account intake and causal loop guard implemented.** Initial
  acceptance retains verified original-creation, current content and creator-write
  requirements. Account equality with automation actors no longer excludes human
  issues. Existing post, Issue Maker and intent-generation journals identify
  automated creations; unresolved creates hold automatic intake in that repository
  until reconciliation. Root post results survive source edits/revocation.
  Coordinator/PostgreSQL evidence and remaining connected limitations are recorded
  in the security handoff. This does not close D1 or T11-INTAKE.

- **A11 — role-specific funding admission and saved-account editor verified.**
  Policy role selections now capture an explicit paying `account` beside harness,
  model and execution profile. Enabling policy requires both account choices;
  legacy policies remain readable/disableable without inventing a choice.
  Authority, dispatch/retries, transactional packet admission, run snapshots and
  completion role permissions use the selected role's exact sponsorship. Missing,
  withdrawn or role-ineligible grants do not fall back. Existing PostgreSQL checks
  passed in `/home/vince/tmp/frole-account6.NQJS52/result.log`, including a reviewer
  child on a distinct paying account; direct wrong-account refusal passed in
  `/home/vince/tmp/frole-choice-refusal6.830BIu/result.log`. Historical policy
  disable/readback passed in `/home/vince/tmp/frole-legacy6.nSi7mz/result.log`.
  The existing native settings route/readback also passed with separate declared
  role choices in `/home/vince/tmp/frole-native-declared6.yqCIsE/result.log`; policy
  remained disabled, credentials dummy, no paid call. Go/native builds and
  boundaries passed. The shared Work view now edits models and eligible saved
  accounts per role through the existing policy command. Chromium interaction
  checks passed with a declared peer in `/home/vince/tmp/froles-ui7.eFa9EH/result.log`,
  including explicit choices, profile/policy preservation, readback, conflict,
  withdrawal without fallback and parent retirement. Native access readback passed
  in `/home/vince/tmp/froles-native7.xlR4x8/result.log`: local-owner editability
  remains separate from false provider write/admin powers. Native spending-account
  ownership/usage is withheld while authenticated identity discovery is unavailable.
  The shared application's first-policy flow now requires the target branch,
  required CI checks, explicit role models and consent to use its connected
  collaboration account. It offers only observed enabled merge methods and saves
  a disabled policy without inventing spending accounts. Chromium bootstrap and
  rejected-input draft retention passed with a declared peer in
  `/home/vince/tmp/fpolicy-setup-ui7.GaHZ18/result.log`. Actual composed native
  options/bootstrap/readback passed in
  `/home/vince/tmp/fpolicy-setup-native7.QgIhyD/result.log`, including disabled
  method exclusion and a subsequent separate-account policy update. Provider
  identity/permissions/options were declared; automation remained disabled and
  remote writes refused. Other hosts must provide current policy options to use
  this initial flow. Actual existing-Muse-session use, connected
  enablement/execution and other AI provider connectors remain unfinished;
  only Muse is offered.
  No connected billing, custody or installed execution gate is qualified here.
- **A07 — merge-method contract verified; native automation pending.** Policy now
  accepts merge/squash/rebase as well as retained Fountain methods. Immutable
  merge intents capture the method; historical omitted methods remain
  fast-forward-only. New methods require fresh native method availability and
  complete/eligible provider rules. Receipt adoption binds the exact method,
  actor, PR and candidate/base; completion confirms that receipt's resulting
  commit. Material/activity/source projections retain candidate and result
  separately. Existing PostgreSQL policy/intent/completion checks passed in
  `/home/vince/tmp/fmerge-method6.j7C0l6/result.log`; source provenance passed in
  `/home/vince/tmp/fmerge-provenance6.tYJZeB/result.log`, and failed/unreconciled
  source refusal in `/home/vince/tmp/fmerge-source-current6.Mlv6gf/result.log`.
  Go build and boundaries passed. No provider mutation ran. Native automated
  merge/receipt composition and connected rules/races remain unqualified.
  A transformed result is not a coder export: subsequent native preparation
  still needs supported fetching/verification of the provider-created result.
- **A16 — installed executable discovery verified.** Native startup discovers
  Muse through absolute PATH entries or the normal `~/.local/bin` location,
  resolves its launcher directory and applies the existing official stable
  manifest/ELF proof. Initial verification runs under the joined native lifecycle
  owner outside short product RPCs; browsing remains available while it runs.
  The existing actual Go/native callback check passed without explicit directory
  enrollment in `/home/vince/tmp/fdiscover-ready6.ukS3ab/result.log` using installed
  `1.4.14-R7309.1` bytes. No launcher execution, credential read, sign-in or model
  call ran. The native owner now periodically refreshes official metadata and
  acquires a missing current ELF into a private version/digest-addressed Factory
  cache. The user's launcher/login and previously captured executables are
  preserved. Actual public stable artifact acquisition, Go/native SHA readback,
  executable permissions, temporary-file retirement and same-inode reuse after
  restart passed in `/home/vince/tmp/ftool-update-fixed7.FRHqTQ/result.log` using
  `1.4.14-R7309.1`. The fixture supplied only a launcher marker; neither it nor Muse
  was executed, and no credentials or model request were used. Discovery now also
  supports no installed launcher and uses that same cache owner. Actual empty-HOME/
  system-PATH acquisition, Go/native hash/status/permissions and same-inode restart
  passed in `/home/vince/tmp/ftool-first-install7.9aSS0Z/result.log`, with no CLI,
  login or model execution. Provider sign-in/onboarding, interrupted transfers,
  changed-release/active-run races, timer cadence and installed distribution
  qualification remain unfinished. Existing-session admitted execution has its
  separate A01/A11 proof; this tool check does not qualify a full task or updater.
  Native tool status now projects checking/downloading/failure/verified facts,
  upstream/selected versions and last successful verification through Go to Work.
  Source execution availability requires current verification; configuration
  stays usable while unavailable. Native status readback passed against the
  retained official artifact (`status-result.log` in the same evidence root).
  Shared Work's four states, version/time display, configuration and withdrawal
  passed with a declared peer in `/home/vince/tmp/fmuse-tool-status-ui7.HFKUai/result.log`.
  Typecheck, desktop UI build and boundaries passed. Real in-flight update/status
  failure races and installed UX remain unqualified.
- **A18 — execution installation payload implemented; installed gate pending.**
  The real desktop stager now builds and includes the broker, preparer/cleaner,
  agent, relay, reviewer and generic worker, plus all twelve service/socket
  templates. Its inventory drives a single-user installer that renders the
  explicit OS account/UID, preserves existing directory permissions, refuses
  existing installation files before writing, and can stage an image without
  activating host services. The normal desktop/Go/helper build passed using the
  retained GTK/WebKit sysroot and PostgreSQL payload, not a fresh distribution
  baseline: `.artifacts/desktop-execution-stage7-build.log`. Actual image
  installation preserved exact Cargo helper bytes and modes, rendered all units,
  and refused repeat installation without changing file metadata/content:
  `.artifacts/execution-image-inventory7-{result,repeat}.log` and the matching
  before/after inventories. No host service, profile, credential or model changed.
  A subsequent controlled live install used the shipped installer, current helper
  bytes in /usr/libexec and real /etc/systemd/system units. Broker activation,
  two task checkpoints/journal replay, retained receipts, failure/cancellation and
  exact root retirement passed on this host:
  `/home/vince/tmp/finstalled-runtime7.Kj2qsf/result.log`. The check removed only
  its newly installed files/activation and retained profiles/evidence. This is
  installed execution-boundary evidence, with fixture-supplied manifests; it is
  not installed desktop/provider or fresh release qualification. Supported-host
  matrices, distribution integrity, interrupted installation, retained-run-safe
  upgrade/uninstall and installed app/provider journeys remain open. A18/D3 stay
  open.

- **A23 — active guidance corrected.** README now describes implemented binding
  and human GitHub journeys separately from pending agent execution/installed
  delivery. The milestone summary matches the fifteen pending table rows.
  Freshness guidance names Factory's actual scope and missing updater automation;
  stale OS design links and the nonexistent Dependabot claim are removed.
  Desktop lifecycle guidance reflects A12/A15. Documentation review only; no
  qualification gate or implementation row is closed by these edits.
- **A15 — native degraded startup and healthy execution verified.** Product
  health is now separate from execution availability. Missing worker installation,
  an installed missing socket or failed coordinator startup keeps browsing alive;
  new preparation/launch effects refuse while inspection and retained recovery
  remain available. Desktop reports execution unavailable and offers explicit
  retry. Actual native product reads and preparation refusal passed with isolation
  absent in `/home/vince/tmp/fdeg6.cXjzVR/result.log`, an installed missing socket
  in `/home/vince/tmp/fdeg-owned6.KIZ1xh/missing-socket/result.log`, and owned
  controller lock refusal in `/home/vince/tmp/fdeg-coordinator6.6gkvTX/result.log`.
  The healthy admitted worker/MCP/artifact/retirement/restart journey also passed
  in `/home/vince/tmp/fdeg-execution6.4F2QOX/run.log`. Provider facts were declared
  and credentials dummy. Native UI compile, typecheck and UI build passed;
  physical UI retry and unresolved-root recovery failures remain unqualified.
- **A12 — selected behavior implemented; UI build verified.** Hidden windows
  retire their obsolete UI binding while retaining connection intent. Fresh
  runtime/account status on return restores the selected account/repository;
  unknown status waits, removed accounts and changed profiles clear the intent,
  and explicit Disconnect prevents automatic reconnection. Runtime-generation
  reset no longer reports an empty account list as observed removal. Typecheck
  and desktop UI build passed. Physical minimize/restore and connected readiness
  races remain part of native UI qualification; no auth/IPC gate is closed.
- **A13 — source correction and UI build verified.** Unknown account/connection
  failures no longer prescribe signing in or unlocking the keyring. Native error
  messages retain their reported cause; observed locked/expired/unconfirmed-refresh
  states receive distinct guidance. A failed status read retains the last observed
  saved account choices rather than reporting them removed. Every command still
  rechecks native admission. Typecheck and desktop UI build passed; a connected
  status-failure journey was not run and no authentication gate is qualified.
- **A14 — native capability projection verified with declared Read access.**
  Authenticated comment/review presentation no longer requires push permission;
  merge still does. Self-approval, locked/closed subjects, draft verdict limits
  and archived/disabled repositories retain their restrictions. Existing native
  Rust/Go/PostgreSQL readback passed in
  `/home/vince/tmp/fcap6.HXgTKE/result.log`, with no provider mutations or retained
  owned grant. This follows the linked GitHub role contract; actual token grants,
  interaction restrictions, required-approval eligibility and the connected
  permission matrix remain subject to action admission and qualification.
- **A04 — Go/storage contract verified; native automation pending.** Independent
  review remains bound to its separate reviewer assignment and exact cycle.
  When its provider account also authored the PR, the coordinator records an
  explicit agent verdict and submits a COMMENT. Correction routing consumes the
  agent verdict; shared UI distinguishes that inspection from provider approval.
  Merge intents bind the comment event, require the exact adopted review receipt
  and a canonical clear, and wait for explicit complete/eligible provider rules.
  Another run cannot manufacture a missing provider approval. Existing PostgreSQL
  review/correction/merge integration checks passed in
  `/home/vince/tmp/frev6.u0FQ5M/result.log`; typecheck, builds and boundaries passed.
  This is not connected automation or branch-protection qualification. Native
  automated review/merge adapters still need to supply and enforce these facts.
- **A05 — native source integration verified; workflow qualification pending.**
  Planning assembly and source recheck now use the enrolled execution workspace.
  Completion source/material and priority capture share a transactional recorded
  workspace resolver; delivery/verification/repair use the execution resolver.
  Pause/resume use recorded ownership and retry uses the canonical run view,
  rather than an appliance Project join. The existing actual native integration
  passed planning's approved README bytes, source-qualified completion capture,
  overview and admitted worker/MCP/artifact/restart settlement in
  `/home/vince/tmp/fwrk6.DHbN7l/run.log`. Go build and boundaries passed.
  Provider authority/policy were declared fixture inputs; no model call ran.
  Native planning dispatch/choice, delivery/repair and pause/resume/retry journeys
  remain unqualified, as do connected/installed and broad security gates.
- **A05/A21 — native issue acceptance inputs and admission implemented.** Rust's
  authenticated GitHub reader obtains issue edit counts, title/lifecycle/edge
  history, selected comments and target branch, and compares two bounded reads.
  Incomplete history or pagination refuses; current bounds are 100 relevant
  events, 50 selected comments/dependencies and 256 KiB of projected inputs.
  Go maps opaque history IDs to immutable canonical occurrences and versions the
  issue/edge snapshot without treating comment selection or branch movement as
  a new issue revision. The native product now supplies acceptance preview and
  local-owner admission through the existing coordinator and SQL journal; a
  request-specific source does not mutate background coordinator ports.
  Actual provider read: `/home/vince/tmp/fnative-inputs7.epjQVr/provider.log`.
  Actual Rust/native-account/Go/PostgreSQL preview, local approval and identical
  replay after restart passed in
  `/home/vince/tmp/fnative-acceptance7.qYtoUp/result.log`. It used the current
  gh token in a private test keyring, then removed that owned local entry. No
  personal keyring, GitHub content, model or execution changed. This qualifies
  the stable existing disposable issue with no selected comments/dependencies;
  it does not qualify Factory OAuth, arbitrary history/visibility or race cases.
  The older completed task profile also opened with the new native input tables
  and retained exact checkpoints/candidate/accounting after restart, without
  replay: `/home/vince/tmp/fadmitted-tasks7.NgVBjI/native-inputs-upgrade.log`.
  Background inputs now resolve the policy's exact saved native connection and
  reacquire current Rust custody for restricted read-only operations. The same
  source supplies acceptance and dispatch inputs; readiness reassesses issue
  evidence instead of reusing cached controls without a global provider revision.
  `/home/vince/tmp/fnative-background7.uR5leo/result.log` passed actual GitHub
  input/branch reads without a foreground operation, application-owned readiness
  under a disabled unfunded policy, restart replay and exact credential withdrawal.
  Only disabled local policy was fixture supplied; no model, GitHub write or
  execution ran. A restarted owner waits for trusted foreground credential
  admission; it cannot infer custody from saved SQL metadata. This does not prove
  automatic dispatch/phase advancement or publication. Stale/hidden inputs,
  nonempty dependencies/comments, UI and installed journeys remain unqualified.
  Native/Go builds and dependency boundaries passed. No broad gate is closed.

- **A05/A07/A21 — native automated CI observations connected.** The application
  now supplies its check observer from the policy's exact saved merge account.
  Rust reads two complete bounded PR/branch/check/status projections; Go maps
  the exact PR and issue IDs, stores a local observation revision and applies
  the existing required-check verdict. This revision is not a global GitHub
  barrier. Latest commit statuses supersede older ones; different providers
  with the same context must all pass. Factory retains its strict success-only
  policy, including non-passing skipped/neutral results. Same-repository open,
  non-draft PRs are supported; ambiguous, incomplete or changing reads wait.
  `/home/vince/tmp/fnative-ci-budget7.nfbspU/result.log` passed actual account/PR/ref
  reads, latest-status selection, Go/PostgreSQL pass/failure/missing verdicts,
  stale-candidate and different-actor refusal, plus retained restart/withdrawal.
  Three labeled statuses were published on the disposable fixture commit;
  check definitions and disabled local policy were fixture supplied. No model,
  workflow, merge or personal credential change ran. Nonempty check-run sources,
  moving refs/statuses, app-specific branch rules and test-merge requirements
  remain unqualified. Full publication/review/merge journeys and full-pipeline,
  OAuth and installed gates remain open.
  The shared PR check view now consumes a pinned native required-check gate,
  keeping GitHub readiness separate from Factory's success-only check policy
  and overall merge eligibility. Independent source reads overlap within the
  unchanged eight-second bound; PR/branch/policy and latest sources stay paired.
  `/home/vince/tmp/fnative-required-parallel7.acDejV/result.log` passed actual
  canonical reads for hidden classic policy (`unknown`) and visible policy with
  no required checks (`not_required`), preserving its separate required approval.
  Nonempty/app-bound requirements, check-run states, test-merge evaluation and
  concurrent policy/status changes still need connected evidence. Earlier timeout
  profiles are retained as failures. No workflow or merge ran.

- **A05/A07/A21 — initial native candidate publication connected.** Go now
  composes `NativePublication` with the application, retaining workflow/SQL
  ownership and the existing branch/PR command journal. Rust transfers the exact
  retained candidate bundle over Git HTTP, validates the expected old ref and
  returns its causal report. The comparison branch and repository-name binding
  remain bounded observations, explicitly distinguished in the receipt.
  `/home/vince/tmp/fnative-publication-linked7.Ssqo9S/result.log` passed actual
  Git pack/new-branch transfer, PR creation, exact head/base/PR/issue adoption,
  disabled-policy refusal, journal replay and retained restart/withdrawal reads.
  [Disposable PR 4](https://github.com/LevitateOS/factory-team-public-check-20261106/pull/4)
  contains only the fixture addition. Run completion, local policy authority and
  phase advancement were declared inputs; no model, merge or complete coordinator
  execution ran. This supplies no Builder-to-PR, installed or security closure.
  The real report exposed additional trailing empty flush packets, now accepted
  only after the exact unpack/ref statuses. Two original uncertain commands stay
  fenced in the security handoff; the later pass does not complete them.
  Correction ref updates now use a distinct ref-publish capture in the same
  journal. The native transfer binds the prior head and the actual correction
  run's source commit, preserving the initial publication and PR description.
  `/home/vince/tmp/fnative-correction-inputs7.qeC8u8/result.log` passed the real
  existing-PR update, exact old/new receipt, replay, changed frozen-lease refusal,
  cancellation before capture and restart/withdrawal readback. `pr-text-head.log`
  there verifies the applied head and unchanged title/body. Original workflow,
  run completion and local authority were declared; no model or merge ran.
  Schema 95 admits the new command kind without releasing existing reservations.
  The retained schema-94 publication profile passed branch/PR history adoption
  under the current binary without credentials in
  `/home/vince/tmp/fnative-publication-linked7.Ssqo9S/schema95-history.log`.
  Recorded PR creation links now support historical adoption without requiring
  the original PR head or live credentials after a correction.
  Remaining: complete coordinator/model correction and execution-to-publication,
  interrupted/expired sends and first adoption, repository identity, PR-retarget
  and permission/withdrawal races, merge and installed qualification. No broad
  gate is closed.

- **A04/A07/A21 — native automated merge connected.**
  `runtime/native_merges.go` composes the existing human merge writer and command
  journal with Go's automatic merge owner. It binds the frozen workflow intent,
  actual review receipt/current review, current checks, enabled method and visible
  provider rules. Unsupported rule combinations stay unavailable: required
  approvals, code owners, last-push approvals, signatures, linear history, resolved
  threads and up-to-date requirements are not qualified by this implementation.
  `/home/vince/tmp/fnative-merge7.p2RoFZ/recovery-result.log` proves the original
  command's actual completed merge, distinct result adoption, base/PR/original
  issue closure, replay and cancellation before send. Public disposable PR 6 closed
  issue 5. Local generation/phase authority and the independent agent verdict were
  declared; the selected CI success was synthetic, with no actual model/CI runner
  or full coordinator qualification. The final provider rule case required no
  external status checks; an earlier unspecified classic app binding correctly
  remained unavailable. The pinned REST API omitted `merge_commit_sha`; readback
  now uses identity-bound GraphQL `mergeCommit`. The original merge was never
  resubmitted. `retained-result.log` in that root passed retained restart and
  withdrawal readback without a provider replay or alternate account.
  Remaining: squash/rebase and complex-rule qualification, rule/base/permission
  races, lost replies/in-flight cancellation, complete automatic workflow,
  fetching transformed results for subsequent native setup and installed gates.

- **A04/A07/A21 — native automated review submission implemented.** The native
  application supplies `NativeReviews` through the existing review port. It
  binds registered immutable workflow work to the policy's exact saved review
  account, current enabled policy and dispatch state. Paired provider reads bind
  PR/issue/author/ref identities; the existing command journal reserves one send
  and retains its causal receipt. New receipts verify head and base before send
  and in the provider reply, without claiming an atomic GitHub ref condition.
  The author account publishes COMMENT and retains the independent agent verdict
  separately; no provider approval is fabricated. Historical receipt adoption
  does not require live credentials. Existing pending reviews and historical
  inline batches remain unavailable to this new-review adapter.
  `/home/vince/tmp/fnative-review-base7.llZxjL/result.log` passed an actual labeled
  COMMENT review, head/base receipt binding, disabled-policy refusal, replay,
  cancellation before send, a retained unknown-send fence, restart and credential
  withdrawal. Publication and the independent verdict were declared fixture
  inputs; the check did not run the coordinator's actual review phase or a model.
  It used the current gh token only in an isolated test keyring and removed that
  owned entry. Go/Rust builds and dependency boundaries passed. Other-account
  approvals/request changes, actual pending-review refusal, inline finding
  batches, authority/permission races, lost provider replies, Factory OAuth and
  installed/full-pipeline qualification remain open. A separate summary-comment
  ID is not invented from the review receipt.
  New base-bound receipts now include the actual merge base from the same Rust
  comparison owner used by PR diffs. Go adopts it into the existing candidate
  tuple, including recovery after the completed receipt was saved without a tuple.
  `/home/vince/tmp/fnative-review-base7.0hKOOP/result.log` passed a real COMMENT,
  completed-receipt tuple recovery, replay, cancellation/unknown-send holds and
  retained restart/withdrawal. Its `comparison.json` and `native-review.json`
  agree on the common ancestor, distinct from both head and current base tip.
  Publication and verdict remain declared inputs; no model or complete automatic
  review phase ran. Historical receipts without this observation remain readable
  and seed no fabricated tuple. Base movement/retarget races and installed review
  qualification remain open.
  Go recovery now uses the executor's explicit `ReviewRecovery` guarantee;
  generic lookup absence never authorizes resubmission. Native recovery reuses
  the existing command reservation and frozen unexpired work. A missing branch
  publication adapter no longer strands registered reviews. Actual Go publication
  pass after a controlled native-owner stop/restart passed in
  `/home/vince/tmp/fnative-review-recovery-linked7.V9gw8u/result.log`, including
  one COMMENT review, unchanged work/deadline, generic absence, cancelled/reserved/
  expired holds and retained readback after credential withdrawal. Publication,
  verdict and reserved-send inputs were declared; no model or actual lost provider
  reply ran. Definitive no-send retry generations and fresh owner authorization
  after expiry remain unfinished; recovery does not renew deadlines or rerun a
  model. The earlier fixture-shape refusal remains retained in the handoff.

- **A02/A03 — local native integration verified.** Desktop now exposes explicit
  run-limit and repository-run consent alongside controller/workspace controls.
  Local-owner admission replaces remote-admin admission for local resource and
  policy settings; native settings readback and typed policy writes are wired.
  The existing composed product check passed in
  `/home/vince/tmp/floc6.1PARrb/result.log`: actual Rust/Go/PostgreSQL settings,
  disabled full-pipeline policy and remote-write refusal with declared
  `push=false` and no admin observation. Typecheck and desktop UI build passed.
  No actual GitHub permission matrix, physical UI or broad security closure is
  claimed. Same-account review/merge semantics (A04) remain pending.
- **A06 — local native integration verified.** Saved bound-request overview now
  reports explicit unavailable setup when a host has no reader. Desktop supplies
  a native workspace reader; ready requires both approved role preparations in
  the current runtime. Shared presentation no longer labels unknown setup ready.
  Actual coder/reviewer preparation, HTTP overview, stale instance/profile,
  retirement and unchanged draft passed in
  `/home/vince/tmp/fsov6.BHDkXg/run.log`, followed by the existing admitted local
  worker/restart journey. Provider authority was declared and credentials dummy;
  connected UI/installed behavior and broad security gates remain unqualified.

## Conclusion

The problem is broader than Muse authentication. Some appliance assumptions
remain active in shared workflow contracts; some native adapters cannot satisfy
those contracts; other restrictions were introduced during extraction without a
clear standalone user requirement. Structural cleanup did not resolve these
semantic mismatches.

The most consequential findings are:

- Native dispatch requires operator/capacity grants that the connected desktop
  cannot grant through its current product routes (A02).
- Native policy binds author and reviewer to one account, while automatic merge
  validation requires different accounts (A04).
- Draft planning still resolves an adopted Fountain appliance Project rather
  than the native workspace (A05).
- Repository-bound request overview needs a setup reader absent from native
  composition (A06).
- Existing Muse session reuse is missing; that does not establish a need for a
  fresh sign-in, new credential store or personal keyring interaction (A01).

The supported first journey needs to be specified before more adapters are built.
These findings support targeted contract corrections, not a full engine rewrite.

## What establishes a requirement

1. Current user instructions control: standalone Factory, existing Muse session
   reuse, a browser link if sign-in is actually needed, preservation of existing
   work, and the selected targeted Go/Rust allocation.
2. The plan records earlier owner choices: Linux first, Tauri, shared UI and
   workflows across desktop/Forge, PostgreSQL initially, native worker isolation
   and permission for privileged installation. Those are documented owner
   decisions, not independently recovered original conversations. Revisit them
   explicitly where necessary; do not silently discard them as imported residue.
3. Code, tests and agent-written implementation notes establish current behavior.
   They do not by themselves establish that the behavior is wanted.
4. Provider contracts constrain remote operations. A local machine permission,
   repository permission, provider login and spending authorization are distinct.

The isolation choice is recorded in [the plan](ideal-filetree-plan.md#d3-native-agent-isolation-decision).
The earlier [reuse table](reuse.md#existing-source-is-the-first-donor) says to keep
appliance systemd/cgroup/root assumptions out; later D3 deliberately selects
some of those mechanisms for Linux isolation. The later choice needs an explicit
platform rationale, not an inference that every appliance prerequisite survived.

## Findings

**Mismatch** means source establishes inconsistent or missing composition.
**Restriction** means an actual constraint needs correction or justification.
**Decision** means the desired behavior is unresolved; this report does not call
the existing choice a demonstrated runtime defect.

### A01 — Existing provider login is confused with Factory credential enrollment

**Mismatch; desired reuse is already selected by the user.** Native custody
searches only Factory application/profile Secret Service entries, and execution
enrollment requires that owner's Muse capability. The worker receives a private
HOME and a run placeholder rather than the user's normal login state.

Evidence: [credential lookup](../../../lib/desktop-runtime/src/credentials.rs#L304),
[execution account](../../../lib/desktop-runtime/src/factory_provider/launch.rs#L19),
[worker launch](../../../cmd/soda-factory-worker/src/bin/soda-factory-agent.rs#L672).
There is no supported existing-session reuse path in these owners. The fresh
Muse sign-in/unlock additions attempted before this audit were removed and are
not part of this baseline.

**Correction:** define supported reuse through the native custody boundary. Ask
for browser sign-in only after an actual authentication requirement is observed.
Do not mount the whole user HOME, pass login files into the worker, scrape
undocumented secrets or assume that reuse is already implemented. Muse documents
stored browser sessions and API-key precedence; subscription onboarding keys and
additional billed API keys are different lanes. Preserve the selected lane.
Sources: [Muse authentication](https://dev.meta.ai/docs/muse-code/auth),
[Muse subscriptions](https://dev.meta.ai/docs/muse-code/subscriptions).

Follow-up inspection: the installed CLI exposes `muse serve` and an offline
stable MSP schema export; `auth --help` exposes credential storage, not a token
export interface. Public MSP provides session/turn control and model/usage reads.
These establish a supported integration surface, not authenticated inference,
account/billing proof or an implemented isolated delegation path. No sign-in,
personal keyring operation or model call ran during this inspection.

**Owner clarification, 2026-11-07:** select “this Muse login”; Muse owns its
account choice. A verified, pinned subscription identity is no longer a
prerequisite for this explicitly selected source. Represent it as CLI-managed
authentication, separate from the existing Factory-token/fingerprint lane.
Do not fabricate a credential fingerprint or promise a particular billed account.
The native CLI path must still bound the run and expose only its admitted tools;
workspace effects must retain their isolated native execution boundary.

A credential-free, private-HOME echo check accepted `--preset empty`,
`hooks={}`, `managed_hooks_path=null` and `run.toolset=[]`, then exited zero
(`/home/vince/tmp/fmuse-source7.aQQIGl/output.jsonl`, `stderr.log`). An earlier
attempt refused `mcpServers={}`: the supported override addresses individual
servers or fields, not the whole map. This check used no saved login and proves
neither saved-configuration isolation nor exact nonempty role tool selection.
Factory's current MCP catalog lacks general workspace file/shell effects; an
empty-preset CLI outside the worker cannot replace those effects by itself.
Saved-login inference now uses actual Go admission, scoped MCP, minute settlement
and native retirement as recorded above. Complete role/configuration confinement,
isolated task/workspace effects, active withdrawal/refusal and broader crash/
installed qualification remain unfinished. Terminal receipt recovery and
canonical acknowledgement work in the recorded scopes above.

### A02 — Desktop authority requires grants its product routes cannot issue

**Mismatch.** Effective authority always requires an operator grant and capacity.
Native product composition omits the callbacks needed to authorize their writes.
The exported coordinator/store writers exist; they are not a reachable desktop
setup journey. Checks can seed those rows directly.

Evidence: [authority](../../../pkg/factory/effective_authority.go#L21),
[native wiring](../../../pkg/factory/runtime/native_product.go#L114),
[grant service](../../../pkg/factory/service/runtime_grants.go#L59),
[fixture seeding](../../../tools/native-checks/native_mcp_signing_check.go#L1372).

**Correction:** define the local owner's concurrency/resource consent and expose
its actual setup path. Decide whether separate shared-appliance operator grants
have a standalone purpose. Do not bypass consent by inventing grant rows.

### A03 — Owning this computer is conflated with administering a remote repository

**Restriction; owner decision.** Placement, environment grants and policy writes
all use current repository `Admin` access. A normal contributor cannot configure
their own Factory execution environment through those routes.

Evidence: [shared native admission](../../../pkg/factory/runtime/native_product.go#L111),
[admin check](../../../pkg/factory/runtime/native_product_environment.go#L12).

**Correction:** separate local resource ownership, spending consent, automation
policy and permission for each remote effect. Decide which read/write/fork
contributors Factory supports. This does not authorize weakening provider access
checks or enrolling somebody else's machine/account.

### A04 — Single-account native policy cannot satisfy automatic merge identity

**Mismatch.** Every native operation actor must be the currently admitted account.
Policy therefore binds PR creation and review to the same actor. Automatic merge
construction uses those bindings, but its validator rejects equal author/reviewer
identities. A successful policy save does not prove a usable delivery policy.

Evidence: [native binding](../../../pkg/factory/runtime/native_policies.go#L14),
[policy actors](../../../pkg/factory/service/policies.go#L93),
[merge assembly](../../../pkg/factory/control/merge.go#L151),
[identity validation](../../../pkg/factory/merge.go#L169).

**Correction:** distinguish independent agent inspection, submitted provider
review, human approval and branch protection. Decide the supported single-account
journey and whether separate accounts/bots are optional or required for a
particular automatic delivery mode. Human GitHub writes are a separate path;
this finding does not invalidate their completed checks.

### A05 — Draft planning still requires the appliance's Fountain Project mapping

**Mismatch in the pending native planning path.** The planner resolves
`NativeProjectForCollaborationRepository`, whose SQL joins only an adopted
Fountain host and appliance `projects`. A GitHub native workspace cannot satisfy
that lookup. Other dispatch paths already use the separate workspace contract.

Evidence: [planning lookup](../../../pkg/factory/control/intent_plan_assembly.go#L136),
[Fountain-only join](../../../pkg/store/collaboration_resolution.go#L74),
[native workspaces](../../../pkg/store/factory_native_workspaces.go#L82),
[ported dispatch](../../../pkg/factory/control/dispatch_timeonly.go#L240).
Related completion/priority duties still use appliance joins in
[completion material](../../../pkg/store/factory_completion_material.go#L40),
[completion source](../../../pkg/store/factory_completion_source.go#L133) and
[priority inventory](../../../pkg/store/factory_priority_inventory.go#L479).

**Correction:** carry native workspace/source identity through the complete
supported workflow; keep appliance Project translation in its host integration.
Do not manufacture appliance Projects for desktop. This planner is not currently
wired into normal desktop operation, so no connected planning failure is claimed.

### A06 — Saving a bound request and reading its overview have different availability

**Mismatch.** Native request composition supplies Store and write admission but
no setup reader. Repository-bound overview refuses when that reader is absent.

Evidence: [native requests](../../../pkg/factory/runtime/native_product.go#L78),
[overview requirement](../../../pkg/factory/service/requests.go#L331).

**Correction:** provide native setup readback, with unavailable execution shown
as a capability/status within a readable request. Missing execution should not
invalidate the retained request overview. This needs no new product policy.

### A07 — One automation policy mandates the entire delivery pipeline

**Restriction; owner decision.** Every `RepositoryPolicy` requires exactly coder
and reviewer roles, nonempty CI checks, fast-forward merge and all publication/
review/merge actor bindings. The contract cannot express a simpler qualifying
automation policy for planning, local work or PR-only delivery.

Evidence: [policy validation](../../../pkg/factory/grants.go#L201),
[actor binding](../../../pkg/factory/service/policies.go#L93),
[planning authority](../../../pkg/factory/control/dispatch_timeonly.go#L229).

**Correction:** decide supported delivery modes and apply their actual
prerequisites. Preserve candidate integrity and provider branch protections.
These restrictions apply to shared automation policy, not all Factory actions:
human GitHub merge separately supports merge/squash/rebase. Do not infer an
observed native automatic merge failure from an unwired executor.

### A08 — Original-creation intake assumes human and automation identities differ

**Owner decision implemented; connected qualification pending.** The owner selected
automatic intake of their own new issues. Initial acceptance still requires
corroborated original, unedited creation and creator write permission. Causal
operation journals now exclude generated issues and hold unresolved creations;
sharing an automation actor account no longer excludes a human-created issue.

Evidence: [initial acceptance](../../../pkg/factory/control/acceptance_initial.go#L80),
[creator exclusion](../../../pkg/factory/control/acceptance_initial.go#L108).
Explicit maintainer acceptance is a different existing path:
[acceptance](../../../pkg/factory/control/acceptance.go#L119).

**Remaining:** qualify authenticated GitHub creation intake and its operation/result
races through the full pipeline. Older superseded operations without retained
origin/result evidence cannot be retroactively classified by this source change.
Explicit maintainer adoption remains separate; D1 remains unresolved.

### A09 — Local product ownership depends on live collaboration-provider admission

**Decision.** Product binding verifies the provider account and selected
repository before constructing even local product handlers. Private drafts are
owned by that provider actor. Local planning/history access therefore inherits
network/account availability and account switching semantics.

Evidence: [binding before routes](../../../pkg/factory/runtime/native_product.go#L27),
[draft ownership](../../../pkg/factory/runtime/native_product.go#L68).

**Correction:** decide whether local drafts/history/planning should work before
GitHub connection, offline and across account changes. If so, define local profile
ownership and attach collaboration identities at the appropriate boundary.
Continue rechecking provider authority for private remote data and remote effects.
Offline operation is a proposed requirement, not an established promise.

### A10 — Understanding/planning work requires an approved execution environment

**Restriction; owner decision.** Planning selects a ready coder preparation and
approved source paths. Standing authority also expects requirement/approval heads
and ready role preparations. Some approval language describes privileged setup
inspection inherited from the appliance.

Evidence: [planning preparation](../../../pkg/factory/control/intent_plan_assembly.go#L85),
[standing prerequisites](../../../pkg/store/factory_grant_commands.go#L82),
[approval contract](../../../pkg/project/grants.go#L57).

**Correction:** distinguish read-only source understanding from setup and code
execution. Decide which planning operations genuinely require installed project
tools. Preserve explicit consent before setup scripts run and exact source inputs.

### A11 — Multiple funding records do not provide usable role-specific selection

**Mismatch when multiple sponsorships are intended.** Authority selects the
first active sponsorship; dispatch waits if that grant does not permit the role,
without selecting a later suitable grant. A funded reviewer can remain unused.

Evidence: [selection](../../../pkg/store/factory_grant_commands.go#L60),
[role refusal](../../../pkg/factory/control/dispatch_selection.go#L15).

**Correction:** choose an explicit account/allowance per supported role, or offer
one understandable local allowance if collaborative sponsorship is outside the
initial scope. Preserve account and spending consent; do not silently switch
funding accounts as a fallback.

### A12 — Window visibility is treated as UI-session disconnection

**Restriction.** `document.hidden` retires the active product binding and native
UI session. Returning visible refreshes readiness but does not reconnect.
Account/repository selectors survive; this is not logout, draft deletion,
ordinary blur or evidence that background execution is cancelled.

Evidence: [visibility handler](../../../frontend/desktop/connections.ts#L298),
[binding retirement](../../../frontend/desktop/connections.ts#L90),
[visible refresh](../../../frontend/desktop/main.ts#L65).

**Correction:** preserve the user's connection intent, suspend obsolete reads,
then revalidate on return. Confirm whether manual reconnection on every actual
visibility change is intended. Do not equate hiding a window with revocation.

### A13 — Unknown errors prescribe credential changes

**Restriction.** Generic status/connection failures suggest unlocking the keyring
or signing in, even when the cause could be transport, runtime or validation.

Evidence: [account error](../../../frontend/desktop/accounts.ts#L121),
[connection error](../../../frontend/desktop/connections.ts#L208),
[credential-state handling](../../../frontend/desktop/connections.ts#L320).

**Correction:** retain distinct failure classes and recommend the remedy proved
necessary. A login/keyring action is not a generic retry mechanism. These prompts
reinforce the unsupported assumption that an unknown failure requires new
authentication; source does not establish what caused the earlier agent decision.

### A14 — Human review/comment UI capability is reduced to push permission

**Restriction.** Rust maps WRITE/MAINTAIN/ADMIN to `Writer`; Go uses it to expose
ordinary comment/review capabilities. The UI permits only `Allowed`. Backend
effect admission is separate and does not universally require this Writer flag.

Evidence: [permission mapping](../../../lib/desktop-runtime/src/github_provider/capabilities.rs#L86),
[capability projection](../../../pkg/factory/runtime/native_product_capabilities.go#L88),
[UI gate](../../../frontend/factory/api/capability-response.ts#L30).
GitHub's [role table](https://docs.github.com/en/organizations/managing-user-access-to-your-organizations-repositories/managing-repository-roles/repository-roles-for-an-organization)
allows Read users to submit PR reviews; actual token grants, subject restrictions
and branch-protection approval eligibility remain separate.

**Correction:** derive action-specific capabilities from the actual provider
contract. Preserve unknown/refused states when the required facts are missing.
No broader connected permission matrix was exercised by this audit.

### A15 — Execution recovery/readiness can take browsing down

**Restriction; recovery policy decision.** Supervisor startup fails if an
installed broker unit exists but its socket is absent. Desktop readiness also
requires coordinator startup. A completely absent broker installation is allowed;
missing optional provider ports do not inherently fail coordinator startup.

Evidence: [broker inspection](../../../lib/desktop-runtime/src/supervisor.rs#L579),
[desktop ready transition](../../../cmd/soda-desktop/src/main.rs#L91),
[D3 scope](ideal-filetree-plan.md#decisions-that-gate-specific-work).

**Correction:** specify a degraded browsing/recovery state for execution failures
where safe. Preserve uncertain-run fences and retained evidence; do not resume or
settle work from absence alone. D3 was explicitly scoped to execution, not browsing.

### A16 — Executable discovery assumes an enrolled directory layout

**Restriction.** Selection resolves official current metadata, then expects
`muse-bin-VERSION` in a native-enrolled directory. It does not discover supported
installations through PATH. An existing compatible directory can satisfy the
contract; a duplicate installation is not demonstrably required.

Evidence: [tool selection](../../../lib/desktop-runtime/src/factory_provider/tool.rs#L125),
[expected layout](../../../lib/desktop-runtime/src/factory_provider/tool.rs#L157),
[native enrollment](../../../lib/desktop-runtime/src/collaboration.rs#L165).

**Correction:** discover/reuse a supported installed executable, verify and capture
its bytes, and install only when needed. Keep mandatory current-channel checking,
integrity and immutable active-run executable identity. Installation/update
activation remains unfinished; fresh executable identity does not imply new login.

### A17 — The supported project environment is implicit and unusually constrained

**Restriction; workload decision.** Preparation clears the environment, limits
tool lookup to system paths, hides HOME and denies network. This excludes direct
use of common user-managed toolchains and cold dependency installation unless
Factory explicitly provisions them. Setup/check limits have not been justified
against a declared supported project workload.

Evidence: [setup environment/limits](../../../cmd/soda-factory-broker/src/prepared_setup.rs#L33),
[tool lookup](../../../cmd/soda-factory-broker/src/prepared_setup.rs#L109),
[preparer service](../../../cmd/soda-factory-broker/systemd/soda-factory-preparer@.service#L12),
[worker environment](../../../cmd/soda-factory-worker/src/agent_environment.rs#L56).

| Actual material/phase | Current bound | Defining source |
| --- | --- | --- |
| Staged source Git bundle, not raw repository size | 512 KiB | `lib/execution-protocol/src/lib.rs:7` |
| Candidate/result bundle | 4 MiB | `lib/execution-protocol/src/lib.rs:13` |
| Each copied prepared tree: coder repository or HOME | 1 GiB / 100,000 entries independently | Shared `lib/execution-protocol/src/lib.rs` constants, consumed by preparer and worker |
| Each approved setup/check script | Reviewed `setup-limits.json`: 1–1800 seconds each; absent file retains 10 seconds | `lib/execution-protocol/src/lib.rs`, `cmd/soda-factory-broker/src/prepared_setup.rs`, `cmd/soda-factory-worker/src/agent_environment.rs` |
| Regular file written by setup descendants (`RLIMIT_FSIZE`) | 1 GiB, using the shared checkout envelope | `cmd/soda-factory-broker/src/prepared_setup.rs` |
| Combined setup/check diagnostics | 256 KiB; truncation refuses readiness independently of process exit | `cmd/soda-factory-broker/src/prepared_setup.rs` |
| Preparer resources / active reservation | 1 GiB, no swap, 128 tasks; full 1 GiB reserved while active | Preparer unit and `cmd/soda-factory-broker/src/preparation_quota.rs` |
| Retained preparation material | 4 GiB / one million entries, plus independent `/run` free-space admission | `cmd/soda-factory-broker/src/preparation_quota.rs` |
| Agent tmpfs / service memory | 3 GiB / 4 GiB | `cmd/soda-factory-worker/systemd/soda-factory-agent@.service.in:17` |
| Worker environment check | 1 GiB / 128 tasks, reviewed check deadline | `cmd/soda-factory-worker/src/agent_environment.rs` |

**Selected correction:** support ordinary Go/Rust/Node/Bun setup and declared
dependency installation inside isolated workspaces. Implement approved toolchain
and dependency provisioning, setup network policy and meaningful bounds, then
qualify representative cold builds and cleanup. The correction entries above
record the implemented public setup path and actual small Node/Bun/Go/Rust
workloads. Broader workload and installed qualification remain dependencies of
native executor qualification.
Keep enforced isolation
and quotas. These constants are observed restrictions, not measured failures on
all normal projects; packed bundles, expanded trees and build output differ.

### A18 — “Linux desktop” conceals a narrower installed support model

**Selected first-release boundary.** The owner accepted Linux desktops with
systemd and one configured execution user per machine. Other environments keep
browsing available and execution unavailable. Execution requires private namespaces, cgroup v2 controllers
and a conventional runtime directory. Global worker/relay templates embed one
desktop user/UID. Multiuser installation of the same global units is not supplied.

Evidence: [scope requirements](../../../lib/native-process/src/scope.rs#L15),
[runtime path](../../../lib/desktop-runtime/src/factory_provider/owner.rs#L102),
[broker socket](../../../cmd/soda-factory-broker/systemd/soda-factory-broker.socket.in#L5),
[relay template](../../../cmd/soda-factory-worker/systemd/soda-factory-provider-relay@.service.in#L7).

**Remaining:** package and qualify this single-user systemd baseline, including
required session/controller capabilities and degraded browsing. Multiuser global
unit installation is outside the selected initial release. Do not introduce a
weaker execution fallback or a login remedy for missing platform facilities.

### A19 — CLI integration includes a second provider wire-policy implementation

**Decision.** Factory's relay validates/restricts request fields, model, tool
catalog and output/cache choices, then validates bounded SSE responses. A new CLI
release can require corresponding relay changes even when the CLI itself works.
This is more responsibility than merely launching a supported CLI.

Evidence: [request policy](../../../lib/desktop-runtime/src/factory_provider.rs#L247),
[response policy](../../../lib/desktop-runtime/src/factory_provider/response.rs#L125).

**Correction:** choose the smallest supported provider/CLI integration that
preserves role authority, spending limits and secret custody. Justify retained
wire rewriting explicitly. CLI flags alone were previously insufficient for role
enforcement; removing the relay without an equivalent boundary is not a correction.
The selected CLI-managed source delegates provider request/token internals to
Muse, as approved above. Its remaining boundaries are exact admitted role tools,
isolated workspace effects, Factory run limits and truthful lifecycle receipts;
the historical token-bound relay keeps its existing limits.

### A20 — Shared database initialization still includes appliance application state

**Coupling; migration decision.** Native startup initializes the shared schema,
including appliance users/keys/Projects/memberships and application hosting
records. Native workspace support adds dual-owner relationships. This creates
startup/migration responsibility; it does not mean desktop users must configure
SSH, containers or appliance hosting.

Evidence: [store initialization](../../../pkg/store/store.go#L90),
[schema](../../../pkg/store/schema.go#L23),
[native workspace relationships](../../../pkg/store/factory_native_workspaces.go#L33).

**Correction:** separate retained compatibility obligations from new standalone
requirements and initialization duties. Preserve data and the defining schema
owner. PostgreSQL is a selected engineering choice; this audit provides no basis
for an SQLite rewrite or deleting imported tables.

**Current correction:** schema 94 explicitly records application hosting and
appliance compatibility. New standalone profiles omit those tables and bind
their setup journals to native workspaces. Retained databases preserve their
compatibility obligations. The verified-corrections entry above records exact
evidence and remaining migration/installed qualification.

### A21 — Check fixtures can satisfy prerequisites that a user cannot reach

**Evidence boundary.** The joined native check explicitly seeds policy, capacity,
operator and sponsorship facts before exercising real packet/worker/MCP/storage
behavior. This is legitimate component evidence and is documented as such, but
it cannot prove normal desktop onboarding or remove A02/A04–A06.

Evidence: [declared scope](../../../tools/native-checks/native_mcp_signing_check.go#L1261),
[seeded prerequisites](../../../tools/native-checks/native_mcp_signing_check.go#L1372),
[shell execution availability](../../../frontend/desktop/index.html#L61).

**Correction:** define the user journey first and finish its reachable composition.
Use retained authorized sessions for checks when they prove the changed behavior.
Require fresh authentication when authentication itself is being tested, or an
observed session limitation requires it. Do not build another fixture framework.

### A22 — Whole-product completion is broader than an independently usable release

**Release decision.** Packaging and installed GitHub work have independent paths.
However, upgrade/parity and the definition of complete Linux work require Forge,
GitHub desktop and Fountain desktop journeys. Fountain does not block implementing
or installing GitHub Factory through the existing dependency graph.

Evidence: [release dependencies](implementation-tasks.md#sequential-implementation-order),
specifically rows T14-PACKAGE, T13-GITHUB, T14-UPGRADE and T14-PARITY.

**Correction:** explicitly choose the first independently shippable host/provider
milestone and its upgrade requirements. Preserve shared-consumer compatibility
and later full parity; do not silently abandon Fountain scope or call the entire
project finished after one host works.

### A23 — Current guidance mixes historical status, active requirements and OS policy

**Documentation defect.** README says GitHub login/writes and canonical binding
are unfinished, while current code and table record their implementation. The
remaining-milestone summary still lists completed T07/T08 work. The active
freshness document additionally refers to an absent OS updater design and a
Dependabot configuration not present in this tracked checkout, and discusses OS
images/Canlang beyond standalone Factory's ownership.

Evidence: [README](../../../README.md#factory),
[milestone summary](implementation-tasks.md#remaining-product-milestones-2026-11-06),
[current completed rows](implementation-tasks.md#sequential-implementation-order),
[freshness ownership](../freshness.md#mandatory-software-freshness).

**Correction:** make active requirement/status summaries current and keep history
as history. Bound Factory's updater responsibilities to what it owns, and name
actual external owners. The mandatory freshness rule remains in force; missing
updater machinery is not permission to run known stale tools. Neither source
refactoring nor repeated historical evidence validates a requirement.

### A24 — Route scope and selected workspace are treated as the same UI fact

**Restriction; navigation decision.** Shared tabs depend on the current route
containing a repository, so New request removes repository navigation despite
desktop retaining its selected repository. Desktop also deliberately provides a
selected-repository feed/catalogue rather than the shared global-feed experience.

Evidence: [shared frame](../../../frontend/factory/app/factory-app.ts#L445),
[desktop route scope](../../../frontend/desktop/product.ts#L50),
[catalogue](../../../frontend/desktop/product.ts#L169).

**Correction:** define selected workspace/navigation independently of destination
routes. Decide repository-local versus aggregate Feed behavior. Do not broaden
the current admitted repository scope implicitly to implement aggregation.

**Selected correction:** the owner selected repository-local Feed on 2026-11-07.
Desktop now supplies its selected workspace independently of route shape; shared
navigation retains it on New request. See the verified-corrections entry for the
browser evidence and remaining host parity scope.

## What remains justified

- Go owns canonical decisions, durable workflows and SQL; Rust owns native
  execution, resource lifetime and credential-bearing desktop operations; Lit
  owns shared presentation. The audit does not establish a reason to rewrite Go.
- Stable canonical identity must survive rename, account refresh and reconnect;
  a provider locator, credential connection and execution workspace differ.
- Preserve human work, current remote authority, explicit spend/resource limits,
  exact accepted input/candidate identity and causal command outcomes. Unknown
  remote effects must not be replayed as though they failed.
- Isolated native execution, revocable scoped capabilities, actual task/checkpoint
  lifecycle and honest retirement remain requirements until explicitly changed.
  Missing native task ports cannot be replaced with stubs or discarded manifests.
- Native PostgreSQL and shared-host compatibility remain selected obligations.
  Sharing a workflow does not imply sharing appliance administration/onboarding.
- Source, connected-provider and installed/security qualification remain different
  evidence. All broad security gates remain open.

## Decisions before dependent implementation resumes

| Question | Recommended requirement to review | Affected findings |
| --- | --- | --- |
| Who controls local execution? | The local owner controls this computer and spending; provider authority governs each remote operation. | A02–A03, A09, A11 |
| What is the first supported work journey? | Specify whether Factory supports planning/local work/PR-only delivery before mandatory CI and automatic merge. | A05–A08, A10 |
| What does independent review mean? | Separate agent inspection from provider account identity and human/branch-protection approval. | A04, A07–A08 |
| Which projects and Linux installations must work? | Name representative toolchains, dependency provisioning, meaningful limits and installed user/platform support. | A17–A18 |
| What is the first shippable product milestone? | Give standalone GitHub desktop its own explicit release/upgrade criteria while retaining the other host commitments. | A22 |

Existing Muse reuse and a browser link when necessary are already instructed;
do not ask the user to select that again. The engineering mechanism still needs
supported-provider evidence and preservation of the selected custody boundary.

## Correction order

1. Settle the local-owner/account/delivery requirements above. Record decisions
   in the existing plan; recommendations here are not adopted automatically.
2. Correct the authority bootstrap and workspace/setup contracts (A02–A06) and
   existing-provider integration (A01/A16) as complete reachable user journeys.
3. Correct UI failure/visibility/capability projection and readiness boundaries
   (A12–A15); finish the selected project environment contract.
4. Resume the dependency-ready product queue using actual user/setup paths.
   Qualify changed connected/installed boundaries once, at their required scope.

## Review coverage and limits

The 1,227 tracked paths were inventoried at root/package level. Review traced
requirements and established workflow boundaries across the following domains;
it does not claim every source line was reviewed for correctness.

| Domain | Review | Independent challenge |
| --- | --- | --- |
| Product scope, plan, language ownership, provenance and status | Root | Findings reconciled against the three specialist reviews |
| Native custody, execution, source/setup, artifacts, tooling and platform | Native reviewer | Product reviewer checked reuse, startup, layout and exact bounds |
| Go identity, policy, grants, planning, intake, review/merge and storage | Engine reviewer | Native reviewer checked grant reachability, acceptance alternatives, policy scope and Project lookup |
| Human collaboration, shell/shared UI, navigation and distribution | Product reviewer | Engine reviewer checked visibility semantics, UI versus effect authority, release dependencies and status |
| Manifests, source-import record, assets/licenses and check tooling | Root inventory and specialist consumer review | Ownership/consumer scope only; no license, build or exhaustive test audit |

Methods used: the requirements/workflow review from `ideal-filetree-audit` and
the existing `untangle-codebase` guidance, scoped to this requested assumptions
audit. Independent challenge narrowed several findings; those qualifications are
included above. No full filetree redesign, source refactor or test cleanup ran.

Evidence is source inspection, current user instructions and the linked public
provider documentation. No builds, tests, services, model calls, sign-ins,
keyring operations or security probes ran during this audit. No credentials or
private profiles were inspected. No security gate was implemented, bypassed or
marked complete. Future changes must retain the exact remaining closure evidence
in [the security handoff](daybreak-security-handoff.md).
