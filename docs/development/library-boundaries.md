# Library boundary contracts

These are technical constraints for changes to existing callers. Completed
adoptions need no renewed inventory, consultation receipt or approval record.
Unfinished product work belongs in the [backlog](factory-remaining-tasks.md).

## Library contracts

### L01 Deadline and evidence repair

Use finite child deadlines even when the parent is unbounded. QMP exchanges share one absolute deadline across connection, buffered events and I/O. Join output pumps and propagate read, write and close failures before publishing captured output. Redaction must handle malformed URLs, binary input and bounded buffers.

### L02 Trust-key and signature repair

Use typed strict SPKI and DER signature parsing. Preserve role separation, uncompressed-point policy, original DER fingerprints and raw TBS verification. Reject off-curve keys, malformed integers and tampered signatures.

### L03 Hash, curve and randomness owners

Use sha2, the existing typed curve libraries and getrandom. Preserve canonical fingerprint recipes and point-validation policy. Entropy acquisition failures must propagate; PID/time fallbacks and predictable tokens are invalid.

### L04 JSON and Base64 profiles

Keep input bounds, duplicate-field handling, number semantics and encoding policy with each caller. Use upstream parsers and encoders without changing signed bytes or canonical fingerprint inputs.

### L05 SSH formats

Use ssh-key for formats while callers retain algorithm, options, line, count and size policies. Preserve forever-certificate timestamps and canonical fingerprints. Parsing a certificate does not confer trust or expiry authority.

### L06 Local CA parsing

Use typed X.509 parsing with the existing signature libraries. Preserve the same-file input bound, certificate-envelope restrictions, CA/key-usage and critical-extension policy, original DER fingerprints and TBS bytes.

### L07 Native SQL parameters

Use typed database parameters and row decoding. Preserve NULL, bytes, booleans, integer ranges and affected-row semantics; callers own schema and transaction policy.

### L08 PostgreSQL driver

Keep tokio-postgres and its internal runtime behind the existing synchronous repository boundary. Bound the complete operation, including connection and lock waits. Transaction guards span the whole closure; errors and cancellation must release or discard the connection safely. Go retains canonical schema ownership.

### L09 Unix HTTP and WebSocket engines

Use the selected HTTP/WebSocket libraries for protocol handling. Callers retain Unix peer admission, credentials, deadlines, byte limits, cancellation and socket/process lifetimes.

### L10 External HTTP adapters

Use ureq for setup and retain the provider curl/Executor boundary where it supplies the required resolver-inclusive deadline and process custody. Preserve response bounds, safe errors and caller-owned authentication.

### L11 URL, IP and time adapters

Keep caller-owned admission around standard IP handling, URL/percent-encoding libraries and wire-time. Preserve signed deadline text and timestamp semantics; parsing must not silently rewrite signed input.

### L12 File, FD and process ownership

Check bounds against the same opened file descriptor used for reading. Keep private-file permissions, exclusive publication, cancellation, child reaping and owned-resource cleanup with the existing caller.

### L13 Archive and release formats

Use the selected archive, compression, executable and XML libraries. Preserve decoded-size and EOF checks, path policy, archive-entry limits, artifact digests and publication atomicity.

### L14 CLI and target discovery

Use existing CLI and target-discovery owners. Keep actual binary targets, installed names, argument behavior and error propagation connected to build and staging callers.

### L15 SDK input admission

Read bounded input with a cap-plus-one check before strict decoding. Preserve duplicate/schema policy, read errors, peer admission and credential custody. Changes to an external SDK require authorization for that repository.

### L16 Evidence matching

Retain the current redaction matcher unless a demonstrated need justifies replacement. Preserve longest-at-earliest-position matching, escaped variants, streaming boundaries, sticky errors and bounded collection/output. Do not add a matcher or benchmark project merely to satisfy an old proposal.

### L17 Configuration evidence

Keep the owner-selected fixed Forgejo data directory and existing environment-file responsibilities. Podman env-file behavior governs the wrapper; do not recreate a configurable-path parser from historical investigations.

### L18 Dead machinery removal

Remove obsolete implementations with their unused callers and fixtures. Preserve live domain policy and useful failure tests; do not add compatibility wrappers for retired internal code.

## Verification

Test the changed caller and its meaningful failure boundaries. Use the installed
tools. Report the result in chat; no receipt or dependency inventory is required.
