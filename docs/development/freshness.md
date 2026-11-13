# Mandatory software freshness

**Freshness takes priority over reproducibility. AI provider CLIs MUST stay on
the latest official stable release, or the owner's explicitly selected channel.
Known stale provider CLIs must not start new work.** This rule applies to Codex,
Muse and every additional provider, across normal terminals, Factory, broker
enrollment, hosts, new and retained Projects, development and qualification.
It supersedes older plans that hold dependencies at obsolete versions.

This document owns the freshness rule; [AGENTS.md](../../AGENTS.md#working-on-factory)
makes it mandatory for coding agents. Installed update delivery remains in
[T14-UPGRADE](factory-products/implementation-tasks.md#sequential-implementation-order).
A documented requirement is not proof of deployed
automation. Never claim that an installation is current without an actual check.

## Keep every managed input current

Apply the same priority to Factory-managed libraries, lockfiles, build tools,
language runtimes and toolchains, native PostgreSQL, provider tools, GitHub
Actions, browser assets, SDKs and build/release inputs. Host OS updates belong to
the host's owner; Fountain and SodaOS own their respective release inputs and
update paths. Follow official current stable channels unless the owner selects
another channel. Adopt major releases too; repair actual integration failures
instead of indefinitely ignoring them. Preserve unrelated project work and data.

AI CLIs need automatic release discovery and activation independently of host OS
updates: consume upstream release events where available, poll metadata at least
every five minutes while online, and refresh before new work when that check is
due. A detected release must be fetched and checked promptly, without a waiting
period or a paid model call. Check actual required flags and protocols; do not
substitute version-number compatibility guesses. Keep the chosen model/profile
and resource limits unchanged by a CLI update.

The native Muse owner discovers an existing installation and checks official stable
metadata, manifests and executable integrity before selection. Its joined refresh
owner checks every five minutes and before new work when due. A missing current
ELF is downloaded without authentication into a private, version/digest-addressed
Factory cache; the user's launcher, login and retained binaries are preserved.
The actual public-artifact acquisition/Go readback/restart check is recorded in
A16 of the standalone assumptions audit. Discovery with no installed launcher
now uses that same cache owner; an empty-HOME acquisition and restart check passed.
Initial native CLI-owned sign-in, retained-login reopen, interrupted transfers,
changed-release refusal and captured active/new executable selection now have
[Q02 source/native evidence](untangle-codebase.md#q02-verified-increment-initial-muse-login-2026-11-08).
The real five-minute refresh was exercised without shortening its interval.
Installed Muse distribution/activation now has scoped Q28 evidence; other
providers remain unavailable until their own adapters exist.
Shared Work now shows checking/download/failure/verified status, upstream and
selected versions and the last successful verification. Source execution
availability requires current native verification as well as its other local
dependencies. Native status readback and declared browser states have scoped
proof in A16/Q02. Actual installed118 public acquisition, current selection,
cached reopen and retained interruption/status evidence are joined in the
[GitHub/Muse installed update ledger](untangle-codebase.md#githubmuse-installed-updates-completion-2026-11-13).
This does not establish a fresh baseline for every bundled dependency or qualify
other consumers.

Other dependencies need event-driven updates where available and frequent
automatic checks otherwise. The source now configures hourly Dependabot discovery
for Bun/workspace libraries, Cargo/workspace libraries, Go modules, the Rust
toolchain and GitHub Actions, with no cooldown or major-release exclusion.
The dependency workflow selects current stable Go/Bun/Rust, then runs the existing
locked UI/analyzer/native configurations and real PostgreSQL/Go checks. These
remote source job has retained default-branch publication evidence at
`3afe351d99b0dd58bbe5065f958459e6ba4e2f2e` and passing
[run42742502789](https://github.com/LevitateOS/factory/actions/runs/42742502789).
The scheduled run42789329677/574182f is also retained in Q30. These historical
runs qualify source-job activation, not current inputs, product release or
installed update delivery. Local configuration alone is insufficient. The explicitly separate classic analyzer
compiler follows the current classic API channel; the product compiler stays TS7.
Every new container fixture refreshes the official PostgreSQL stable image and
captures its immutable image ID. Desktop packaging checks the supplied native
PostgreSQL version against official current-stable metadata before building and
records that selection. It does not upgrade a retained database. Independently
published SDKs use their package owners; installed Factory payload delivery and
activation remain Q28. A bot proposal alone does not
make a dependency updated: apply it, run the affected checks and ship it through
the owning component's update path. Do not rebuild the host OS to update an
independent CLI or rerun unrelated expensive checks.

Discovery intervals bound detection latency; they cannot guarantee simultaneous
installation with an upstream publication. Show last successful check, upstream
selection, installed/active selection and failures. Network or upstream failures
are unresolved freshness, not evidence that an old release is current. Known
stale or unverified inputs must block claims of a fresh baseline/qualification;
known stale provider binaries must block new provider work until corrected.

## Create a fresh baseline

1. Resolve official upstream release metadata immediately before selecting inputs
   for a new build, baseline or qualification. Refresh stale locks, checksums,
   source/SDK selections and base images first. Reuse cached bytes only when they
   match that current selection.
2. Verify the selected artifact's integrity and actual required behavior. If the
   current release fails, report and fix that precise incompatibility. Do not
   silently restore an older pin to obtain a green result.
3. Record the resolved release identities, digests and check time, then hold those
   exact bytes for that running attempt. This record explains what ran; it is not
   a mandate to reuse those versions in the next attempt.
4. Recheck freshness before promotion. If an upstream input has
   advanced, update the affected component and its evidence before claiming a
   current baseline. Preserve prior artifacts/results as historical evidence;
   never rewrite an immutable release or relabel older proof as current.

Historical replay may use the original inputs when explicitly selected as replay,
but it must remain labelled historical and cannot stand in for current testing.
Existing sessions retain their captured executable until completion so an update
does not corrupt active work. New sessions select the newly current release.
Retain only current, previous and still-referenced CLI bundles; reference cleanup
must preserve credentials, accounts, budgets, data and required failed evidence.
