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

This stages a source development build, not an installer or a qualified Linux
distribution package. The default profile is `$XDG_DATA_HOME/soda-factory`, or
`$HOME/.local/share/soda-factory`. Native `--profile`, `--postgres` and `--backend`
options accept absolute paths. The profile must be privately owned, with no
symlink components. Normal workspace builds omit GTK/WebKit requirements; the
staging script enables the `native-ui` feature.

Closing windows hides them and keeps the runtime alive. Launching again for the
same profile activates its main window. Quit stops admission and observations,
joins cancelled request/observation tasks, then stops Go before PostgreSQL.
Product calls await outside the lifecycle mutex. Stream cancellation or closure
of its owning window aborts and joins that polling task and its pending request.
Startup failure is visible in the toolbar;
Retry startup retries the same retained profile. UI refresh and visibility changes
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
Selectors are stable during readiness polling. Missing execution is explicit;
no coordinator or execution port is activated. Shared PR views remain T08-READ.
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
request cancellation, disconnect and reconnect. It keeps missing job evidence
unavailable and does not claim the shared connected views are mounted. Failed
attempts can reuse the same saved approved account while the external driver
retains its isolated keyring; no successful result is inferred from that reuse.
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
