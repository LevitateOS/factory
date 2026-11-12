# Factory desktop host

The Linux Tauri executable owns a private native profile, PostgreSQL and the Go
Factory server through [`soda-desktop-runtime`](../../lib/desktop-runtime/README.md).
It loads the production `SodaFactoryApp` entry used by the shared Factory UI.
Storybook remains the separate component workbench in `tools/factory-ui/`.

Build with Bun, Go, Rust and the [Tauri Linux prerequisites](https://v2.tauri.app/start/prerequisites/#linux).
Supply a native PostgreSQL payload prepared as described by the runtime owner:

```sh
bun run build:desktop --postgres /absolute/postgresql-payload --out /home/USER/factory-stage
/home/USER/factory-stage/soda-desktop
```

This stages an installable development bundle, not a qualified Linux distribution
package. Run its `install.sh` as your desktop user to install under `$HOME/.local`,
or pass `--prefix /absolute/location`. It installs the app/runtime, known notices,
a `bin/factory` launcher and a desktop-menu entry/icon. No Bun, Go or Rust toolchain
is needed to run the installer. Bash, coreutils and `ldd` are required; missing
GTK/WebKit runtime libraries refuse installation before writes. Existing
installation files refuse overwrite. Verified app upgrades install a separate
payload and activate its launcher last; uninstall retains profiles, credentials,
payloads and execution services. See the [upgrade and uninstall commands](../../README.md).
The bundle stage checks that the app, Go backend, PostgreSQL and native helpers
have matching 64-bit Linux architectures. Check-feature builds omit the app
installer and refuse reusing an installable stage. Installation does not copy
profiles/credentials or activate services.
The default profile is `$XDG_DATA_HOME/soda-factory`, or
`$HOME/.local/share/soda-factory`. Native `--profile`, `--postgres` and `--backend`
options accept absolute paths. The profile must be privately owned, with no
symlink components. Normal workspace builds omit GTK/WebKit requirements; the
staging script enables the `native-ui` feature.
It stages the fixed `soda-factory-muse` connector beside `soda-desktop`; keep both
files together. This helper uses Muse's existing login; the native tool owner
can acquire the current verified executable in Factory's private cache.

### Live developer inspection and control

Normal release bundles include `factory-dev`. Quit the selected app normally,
then launch it with an explicit diagnostic mode for that process lifetime:

```sh
factory --profile /home/USER/PROFILE --developer-tools inspect
factory --developer-cli --profile /home/USER/PROFILE status
factory --developer-cli --profile /home/USER/PROFILE --window main ui tree
```

`inspect` permits status, semantic UI snapshots and condition waits. Use
`--developer-tools control` to additionally operate rendered controls. The
desktop displays the enabled mode. A second launch only opens the original
app; it cannot change its diagnostic mode. Default launches create no developer
listener. The CLI does not start the app or backend.

Take `result.scope` from a successful tree response, then target an exact name
and role, optionally inside a named navigation/region/form. Multiple matches
refuse; `--node` can instead use a fresh snapshot handle.

```sh
factory --developer-cli --profile /home/USER/PROFILE --window main ui click \
  --scope "$scope" --within Factory --role button --name Work --action navigate-work-1
factory --developer-cli --profile /home/USER/PROFILE --window main ui fill \
  --scope "$scope" --label 'PR number' --expected '' --value 56 --action fill-pr-1
factory --developer-cli --profile /home/USER/PROFILE --window main ui wait \
  --scope "$scope" --role button --name 'Inspect PR' --state enabled --timeout 30
```

The scope binds the window/document, runtime generation and selected account/
repository. Selection changes require a fresh tree. Click/fill/select/check
require a caller-supplied action ID. Fills/selects require `--expected` with the
current value; checkbox changes use `--value true/false --expected true/false`.
Never replace human text without authorization. Every action passes through
the normal UI handlers and product admission. A successful action reports
`dispatched`, not product completion. Read the resulting UI/receipt separately.

An identical action ID/request returns the recorded outcome; a changed request
conflicts. After a possible send, a timeout/disconnect stays `outcome_unknown`:
inspect the original saved product command rather than issuing another click.
The session retains up to256 action IDs without eviction; exhaustion refuses
new actions. IDs from an earlier session cannot acquire replacement authority.
Quitting may lose its own action reply; observe process/endpoint retirement to
confirm shutdown. Wait timeouts do not cancel runs or withdraw funding.
GitHub account states, the public verification code and its expiry appear as
status nodes in `ui tree`; use them to diagnose sign-in without screenshots.

The local socket is `PROFILE/developer/control.sock`, restricted to its owning
UID. Pre-send action fingerprints go to private `client-SESSION.jsonl` files
there, without editable values. Keep all output private: trees can contain
repository text and drafts. Password values and native credentials are omitted.
Guest access runs the CLI over SSH; no TCP listener is opened. JSON stdout and
exit codes distinguish success(0), refusal/unavailable(2), deadline(3) and
unknown dispatch(4). `--help` lists the supported operations. UI actions are
synthetic DOM input; physical keyboard/window/visual checks remain separate.

`status.github_traffic` reports actual network attempts, received response
headers and requests suppressed by an existing rate hold, grouped by fixed
endpoint/method labels. It includes only the latest numeric quota fields under
fixed resource labels. `semantic_reads` counts validated connector reads by
fixed operation name; one such read can make several wire requests. Reading
these counters makes no provider calls or credential reads;
compare snapshots within the same native owner incarnation. It is `null` when
the runtime is absent, and counters reset with a new owner.

`github_traffic.local_budget` is separate retained state: at most 600 wire
attempts per rolling hour for this profile, across restarts and token changes.
Every GitHub request counts, including OAuth, GraphQL, Git and failed transport.
Its numeric `ceiling`, `used`, `reset`, `suppressed` and `provider_reset` facts
distinguish a local ceiling from a provider hold. Boolean `available` reports
journal health, not remaining capacity: compare `used` with `ceiling` and `reset`.
`null` means unavailable, never a fresh allowance. Factory also preserves the provider's last 20% of reported
quota. A hold refuses before sending; it never establishes the outcome of an
earlier write. Preserve the private `github-wire-budget.*` records with the
profile. Do not delete or edit them to bypass a hold or repeat qualification.
Other profiles and applications consume their own budgets.

Local worker observation and retirement keep their 10-second tick. Passive
provider discovery/delivery waits five minutes after a completed sweep; explicit
commands and newly settled results wake it immediately. To measure idle traffic,
use two local status snapshots after startup/selection has settled, with no UI
refreshes between them. Compare semantic reads and suppressions as well as wire
attempts so a quota refusal is not mistaken for quiet scheduling.
The Feed also refreshes passively five minutes after its latest page read
finishes; an explicit refresh resets that wait. Planning and repair progress
refresh every minute while work is in flight; known settled planning/generation
states stop polling. Explicit refresh and commands remain immediate.
Feed assembly observes each repository once before reading details and once
afterward, regardless of row count. Status/access inner fields share only their
request's admission; their outer permission checks and every writer remain fresh.
Native Feed responses include their small local allowance warning, avoiding a
separate full repository-status request for that notice. The final visibility
check also seals warnings when the Feed has no rows. Older hosts that omit the
optional warning field retain their existing status-read behavior. A native CI
snapshot shares one fresh PR between its review and check projections, removing
two duplicate PR reads across the before/after snapshots without caching them.
Funded merge admission reuses its completed material read's final provider
projection once, under the original context/deadline and exact publication,
source/account, policy and target binding. It still performs causal review and
acceptance proof; the final send observes the provider afresh. This is not
persisted authority, a retry permission, or a cross-request cache.
Within a GitHub merge-readiness read, the five projections share raw PR,
repository, branch and CI facts. Two complete snapshots still compare protection,
checks, current reviews and permissions, including the full test-merge binding;
the final authority recheck remains fresh. Expired allocated merge intents go to
their original cancellation journal before new material reads. Missing lookup
alone cannot establish that a write was never sent.

### Linux execution services

The stage also contains `factory-runtime/execution`: seven native helpers,
twelve systemd service/socket templates, their install inventory and
`install.sh`. On the selected Linux/systemd baseline, install for one explicit
non-root desktop account. The host needs systemd 257 or newer (for the required
private PID namespace), cgroup v2 with CPU/memory/PID controllers, the selected
user's private `/run/user/UID` session directory, and the fixed
runtime executables `/usr/bin/git`, `/usr/bin/bash` and `/usr/bin/timeout`;
the installer refuses missing runtimes before writing helpers or services.
These execution runtimes do not require a host Go/Rust/Bun compiler toolchain.

The current recipient checks use Debian 13 with GTK 3/WebKitGTK 4.1 and Rocky
Linux 10.2 with GTK 3/WebKitGTK 4.1 from stable EPEL (and CRB for its runtime
dependencies). Rocky's base repositories do not supply `webkit2gtk4.1`; install
the runtime prerequisites before running the app installer. The installer checks
actual library availability and does not configure system repositories. These
recipient checks do not establish support for other distributions or CPUs.


```sh
sudo /home/USER/factory-stage/factory-runtime/execution/install.sh --user USER
```

This writes helpers to `/usr/libexec`, renders the account/UID into units under
`/etc/systemd/system`, and enables the broker socket. Existing installation
files cause refusal before writes; retained-run-safe upgrades and uninstall are
unfinished. Native readiness still checks execution dependencies after installation.

For packaging inspection, create an empty image directory and pass
`--root /absolute/image` with the same explicit `--user`. Image mode writes
under that directory without activating host services. The inventory selects
only this build's execution files. Existing directory permissions are preserved.
A development stage/image does not qualify live root ownership, activation,
isolation, installed recovery or a signed distribution package.

Closing windows hides them and keeps the runtime alive. Launching again for the
same profile activates its main window. Quit stops admission and observations,
joins cancelled request/observation tasks, then stops Go before PostgreSQL.
Product calls await outside the lifecycle mutex. Stream cancellation or closure
of its owning window aborts and joins that polling task and its pending request.
Startup failure is visible in the toolbar. Missing execution dependencies or a
failed coordinator can leave browsing ready with execution explicitly unavailable;
new preparation and launch effects refuse. Retry startup/execution retries the
same retained profile. UI refresh and visibility changes
request fresh native readiness. A dead backend or database retires the binding.

The webview receives opaque readiness IDs and product operations. It never
receives the bootstrap key, database credentials, socket paths, provider request
URLs or filesystem/shell access. The window-owned product bridge now admits a
saved native connection and optional remote repository, then obtains canonical
account and selected repository identities through the production Go services.
Readiness and browser-supplied canonical IDs cannot establish this binding.

factory_prepare_connection retires only its window's selection and returns its
new handle before account/provider I/O. factory_connect consumes that handle once.
A superseded pending connect cannot recreate an older selection. Scoped disconnect
cannot retire a newer handle. factory_repositories uses
the defining headless Rust connector for current account repository discovery;
selection maps the chosen remote identity through Go. factory_product_request
accepts only normalized paths under the two shared product API roots, with typed
read/write/subscription operations, 2-KiB targets and 64-KiB JSON object bodies.
It cannot reach provider/native APIs or arbitrary origins. Go applies product
policy and resource authorization inside the independently admitted native scope.
Responses retain the existing 2-MiB bound and native deadlines.

Each product request returns a native request ID and one channel completion.
factory_product_cancel is scoped to its window and selection, cancels and joins
the task, and permits cancellation even after window closure. Replacement,
disconnect, closure, credential withdrawal, runtime failure/restart and Quit
retire pending work and suppress stale completions. Explicit disconnect and
replacement join cancelled product tasks; native runtime retirement also joins
its provider/callback/I/O owners before stopping children. Quit joins shell tasks
before stopping the runtime. Product I/O never holds the child lifecycle lock.
The desktop transport also discards buffered completions from an older selection
and propagates component AbortSignals to native cancellation.

The production desktop now mounts the same Lit feed, request editor, issue
list/detail/discussion and drafts, work views and knowledge library. Choose a saved
account, then a repository; load more discovery pages or open a GitHub URL or
owner/name. The Rust connector resolves names into stable remote IDs, and Go maps
the selection into canonical product IDs. Native metadata supplies the observed
default branch for relative Markdown links; missing facts do not become a guessed
branch. User-clicked web/mail links use a bounded native opener restricted to
HTTP/HTTPS/mailto without credentials, after current-window/selection admission.

Navigation uses canonical local hash routes; a route cannot enlarge native scope.
Drafts use stable profile/provider/actor scope and existing shared repositories and
subject keys. Switching/disconnect/retirement clears views and pending replies
without deleting drafts. Empty filtered issue pages retain their continuation.
Selectors are stable during readiness polling. Hide/show retains the selected
account/repository and reconnects after fresh runtime/account status; explicit
Disconnect clears that intent. The desktop explicitly starts the
single Go coordinator after Rust's callback owner is ready. Missing task/provider
dependencies remain unavailable; startup grants no execution authority. Shared PR
views and human write qualification have their own recorded evidence.
The native tool owner discovers an existing Muse launcher through absolute PATH
entries or `~/.local/bin`, then verifies the adjacent versioned ELF against the
current official stable manifest. Verification does not run the launcher or read
its login. The joined native owner refreshes official metadata every five minutes
and fetches a missing current ELF into Factory's private profile cache, preserving
the user's installation and exact binaries captured by existing runs. Browsing
remains available while selection is pending; unavailable tools cannot start work.
Work shows verification/update status, latest and selected releases and the last
successful check. Pending/failed verification keeps execution unavailable while
allowance settings stay usable. First-install onboarding without a launcher and
installed update qualification remain unfinished. Admitted execution using the existing
Muse login has scoped proof in the assumptions audit; full task execution remains
unavailable until its native task/checkpoint owner is implemented.
The old unbound query/mutation and recorded-run polling IPC surface is absent from
normal builds; its historical control probes exist only with native-check.

Linux account tokens use the login session's OS Secret Service through the
[native credential owner](../../lib/desktop-runtime/README.md#native-account-credentials).
Click **Sign in to GitHub**, then **Open GitHub**, and enter the displayed code in
your default browser. Factory requests repository and profile access using its
public device-flow client ID. Grant exchange, account verification, authenticated provider HTTP/decoding and
complete access/refresh credential storage are owned by headless Rust. Go accepts
only credential-free observations and owns canonical mapping/product transactions. The webview receives the short
user code, fixed verification URL, expiration and safe account metadata; it has no
token getter, token writer, shell opener or keyring-passphrase command.
Native metadata shows the provider-observed account name while stable provider
IDs remain the identity. **Remove from Factory** durably denies local use and
removes the saved credential; it does not revoke the GitHub OAuth app grant.

Cancel sign-in or close its owning window to stop pending authorization. Startup,
backend failure and Quit retire the attempt. Cancellation and the bounded save
have one ordering: an already completed save remains saved. Saved accounts remain
after runtime restart. A locked or absent keyring reports an error; there is no
file fallback. Native refresh transport/custody exists; automatic connection
management beyond explicit selection/reconnect remains later work. Connected
browsing now uses native admission and Go canonical binding behind the product
bridge; a saved GitHub account alone still does not admit Factory work.

`--check-control` extends the opt-in native acceptance root with a declared slow
trusted Go product binding over real private PostgreSQL/Go/IPC. It checks shell
status during a pending query, polling cancellation/window closure, bounded
native admission, dropped-future cancellation, request retirement, child failure,
instance-header refusal and Quit while a real shell query is pending. Its fixture
markers and control observer are absent from normal builds. It does not qualify
provider login, execution or an installed distribution.

For a real compositor/WebKit integration run, build with `--check`, then launch
the staged executable with `--check`. This feature alone includes a native
acceptance hook that checks shared UI/themes, private bridge refusals, bounded
observations, multiple windows, close/reopen, second-launch activation and Quit.
`--check-startup-error` checks visible failure with a deliberately unavailable
`--backend` or `--postgres`; `--check-crash` reports its exact ready process ID
for an external driver to kill and relaunch against the retained profile. These
hooks are absent from the normal `native-ui` executable. They do not qualify an
installed package, OS sleep/wake, connected providers or executor retirement.

With an isolated `execution-retry-check` marker beside its profile,
`--check-startup-error` instead observes a real coordinator lock failure. The
owned external driver holds `coordinator.lock` before launch, waits for
`execution-retry-release`, releases its lock and creates `execution-retry-released`.
The observer clicks production Retry execution, requires a new ready instance and
successful coordinator start, and keeps any absent worker isolation explicitly
unavailable before production Quit. This does not qualify worker isolation.

`--check-resume` adds an observer for an external, isolated OS sleep/wake
driver. With a fresh profile it writes `wake-ready` after native UI/IPC checks,
then waits for `wake-complete`. The driver must commit/check real store data,
suspend the **guest OS**, observe suspension and wake it before creating that
second marker. The observer checks fresh readiness, the original PG/Go process
identities, binding generation and retained webview state before Quit. The
markers alone do not prove OS suspension. The Linux kernel's
[sleep-state interface](https://kernel.org/doc/html/latest/admin-guide/pm/sleep-states.html)
and QEMU's [suspend/wakeup observations](https://www.qemu.org/docs/master/interop/qemu-ga-ref.html)
provide that external evidence. This hook never suspends the user's machine.

The desktop staging copy preserves the PostgreSQL payload's relative symlinks.
Rewriting them to absolute source paths breaks relocation onto a computer that
does not have the build cache.

`--check-credentials` extends the same opt-in native acceptance hook against an
isolated real Secret Service daemon. It uses generated fixture tokens, never
provider credentials. The external driver locks/stops/restarts only that daemon
through the existing `credential-*-ready` marker handshake. The native/webview
checks cover custody, metadata-only commands, owner/profile isolation,
reauthentication, withdrawal across reopen, locked/unavailable errors, encrypted
retention across daemon restart and actual capability expiry. Run it with a fresh
profile and a private session bus/data directory, preserving the personal keyring.

The historical `--check-signin` declared provider observer depended on the retired
Go connector and is no longer current cutover evidence. Its source is retained
for the existing check migration, without adding a second production connector.
The real `--check-signin-live`/`--check-signin-consent` paths use the Rust owner.
The opt-in consent observer checks that the current Secret Service has an
unlocked default collection before it starts a new device challenge. This
preflight catches an absent or locked fixture keyring; it does not prove that
an approved grant can be written and reopened.
The consent check additionally exercises actual WebKit IPC for native connection,
repository paging/selection, canonical Go identity, feed/issue/PR reads, explicit
request cancellation, disconnect and reconnect, then drives the actual shared
feed/issues/detail and scoped draft views through the account/repository controls.
For the current session-lifecycle check, the isolated external driver creates
`session-lifecycle-check` next to the fresh profile before launch. The existing
consent observer then minimizes/restores and hides/restores the actual native window and emits
`signin-session-unavailable-1/2` markers. The driver stops only its own private
Secret Service, waits for `signin-session-retired-1/2`, and restarts that same
isolated encrypted keyring. Its private bus must not auto-activate a replacement
Secret Service. The observer requires product retirement with preserved choices,
fresh-status reconnect/draft recovery, and explicit Disconnect preventing later
reconnection. Only after those assertions does it write
`session-lifecycle-result.json`. This exercises source/native behavior, not
installed qualification or a diagnosis of the unavailable historical crash.
In this session-only opt-in, an actual `check_changed` refusal is retained as
unavailable protection in `canonical-workflow-evidence.json`; it never counts as
a successful canonical protection read or CI/merge qualification.
It now also reads actual canonical workflow jobs and refuses changed heads or
malformed continuations; unproved CI gates and local execution stay unavailable.
Failed
attempts can reuse the same saved approved account while the external driver
retains its isolated keyring; no successful result is inferred from that reuse.
The 2026-11-05 connected run passed after a repository-navigation fix: a newly
selected repository opens its feed, while reconnecting the same repository can
keep the current route. The same approved encrypted grant was reused for that
assertion retry. Actual refresh, private reads and local account removal also
passed; remote revocation and installed-product qualification remain open.
For follow-up qualification only, a `native-check` build accepts
`--check-retain-account` with `--check-signin-consent`. It retains the owned
encrypted account on success and records local removal as false in the safe
result. Reopen that same private Secret Service/profile for subsequent checks;
retention is not withdrawal evidence. The flag is absent from normal builds.
Historically, `--check-signin` extended the same native acceptance hook for declared authorization
responses through actual Linux WebKit, native Go/PostgreSQL and an isolated Secret
Service. It checks production controls, fixed browser handoff, prompt/metadata
custody, cancellation, stale attempts, multiple windows, verification/storage,
retained account names, removal and backend restart. `--check-signin-live` uses an
actual unapproved GitHub challenge and cancels it without saving an account. Both
need an external owned fixture driver; the browser opener receiver and positive
provider responses in the declared run are explicit substitutes. Neither proves
successful real consent, connected repositories or an installed package. These
observers are absent from normal `native-ui` builds.
