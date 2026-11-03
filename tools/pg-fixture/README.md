# soda-pg-fixture

Start a disposable PostgreSQL 18 container for Soda and Forgejo development
tests. Each run gets random passwords, a loopback-only port and separate roles
and databases. This tool is for test authors who need a local database fixture.

## Start and stop a fixture

Run from the repository root on Linux with Podman available. The tool pulls the
pinned PostgreSQL image if it is missing. Keep fixture secrets in a private
directory on the workspace disk:

```sh
export SODA_PG_FIXTURE_DIR="$PWD/.artifacts/pg-fixture"
fixture_env=$(cargo run -q -p soda-pg-fixture -- start) && eval "$fixture_env"
# Run your tests using the returned connection values and password files.
cargo run -q -p soda-pg-fixture -- stop "$SODA_PG_CONTAINER"
```

Use a fresh fixture directory for each concurrent run. An existing directory
must belong to the current user, have mode `0700` and have no symlink components.
Proceed with tests only if `start` succeeds. Export the returned variables if
your test process reads them from its environment.

## Connection values

Successful startup prints shell assignments; it does not print passwords.

| Variable | Meaning |
| --- | --- |
| `SODA_PG_CONTAINER` | Container name to pass to `stop`. |
| `SODA_PG_HOST` / `SODA_PG_PORT` | `127.0.0.1` and the allocated host port. |
| `SODA_PG_DATABASES` | Created database names; defaults to `forgejo soda`. Each database has a matching login role. |
| `SODA_PG_DIR` | Secret directory containing `<role>.passwd` files with mode `0600`. |
| `SODA_PG_SUPER_PASSWORD_FILE` | Path to the `postgres` password file. |

Set `SODA_PG_DATABASES` before startup to choose a space-separated list. Names
accept lowercase letters, digits and underscores, but cannot start with `pg_`.
An explicitly empty list creates only the superuser cluster.

`stop` force-removes the named fixture container and its anonymous volumes.
Password files remain in the caller-owned directory; remove that run's private
files after the container stops. Exit `3` means Podman or the pinned image is
unavailable and lets a test driver report a skipped fixture.

See [testing](../../docs/development/testing.md) for validation scope and
[local testing](../../docs/guides/local-testing.md) for retained lab access.

## Native desktop database proof

The native driver also calls the production Rust backend-bootstrap writer before
each Go command, exposing only its private path as `SODA_NATIVE_FACTORY_CONFIG`.
The [runtime contract](../../internal/factory/runtime/README.md) owns its loader;
profile identity and per-start IPC key checks can run across the same three
database lifetimes without starting a desktop or agent.

`soda-native-pg-check` uses the production Rust native database lifecycle and an
existing Go store integration check, without Podman or a system PostgreSQL server.
It initializes a private profile, runs a real multi-row transaction, checks clean
restart, deliberately crashes the owned server, checks WAL recovery, and stops.
See the [native runtime guide](../../lib/desktop-runtime/README.md#actual-native-integration-check)
for the payload layout, command, recorded result and platform limits.
The existing `soda-pg-fixture start/stop` container path remains the default binary.

For later store slices, `--once` runs one integration command on the native server
then stops cleanly, without repeating the earlier restart/crash proof:

```sh
cargo run --locked -p soda-pg-fixture --bin soda-native-pg-check -- \
  --once /absolute/native-postgres /home/USER/new-native-pg-profile -- \
  go test -count=1 -v -run '^TestCollaborationRegistry' ./internal/store
```

The driver supplies `SODA_NATIVE_PG_DSN_FILE`. Existing `postgresFixture` helpers
read it through `store.TestFixtureDSN` and still create/drop a separate database
per test. The connection URI/password never appears in command arguments or
output. Omit `--once` for the original three-lifetime retained-transaction proof.

## Managed PG/Go supervisor check

The existing native driver also exercises the production Rust supervisor and Go
executable, without containers or manually starting a database:

```sh
cargo run --locked -p soda-pg-fixture --bin soda-native-pg-check -- \
  --supervisor /absolute/native-postgres /home/USER/new-profile \
  /absolute/soda-factory-server -- \
  go test -mod=readonly -count=1 -v \
  -run '^TestNativePostgresRetainedTransaction$' ./internal/store
```

Build the server with `go build -tags=factory_native ./cmd/soda-factory-server`.
The driver refuses an existing initial profile. It starts actual children, checks
authenticated readiness and duplicate exclusion without key rotation, runs the
existing Store's retained transaction phases, and verifies clean child shutdown.
It deliberately kills only its owned backend, database group and an owned
supervisor subprocess, verifies automatic retirement, and reopens retained data.
The supervisor-death probe uses a subreaper to observe/reap its exact owned
orphans. Negative startup probes use a failing executable, missing payload and
an incompatible version marker in the owned fixture, restoring that marker
before the final retained transaction check. Keys/passwords/DSNs are never printed;
private logs are checked for secret disclosure. No provider/model or agent work
is started. This is native process/storage proof, not a Tauri or installed-product
journey or supported cluster-upgrade qualification.
