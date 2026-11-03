# Native support provenance and notices

Support code was selectively adapted from [Soda OS](https://github.com/LevitateOS/soda-os/tree/bc1d3e0dbec48dfaa6a20f9d0453ad3e4cdf353c), revision `bc1d3e0dbec48dfaa6a20f9d0453ad3e4cdf353c`:

- `internal/acceptance/qmp.go` and its tests: QMP negotiation and request/response handling.
- `internal/acceptance/remote.go`, `command.go`, `evidence.go`, `processes.go`, `cleanup.go`, `qemu.go`: literal transport, evidence/error boundaries and exact child ownership ideas, adapted to the current core contract.
- `internal/build/release/inspection.go`: artifact/platform identity techniques only; no release record graph.
- `scripts/soda-release-executor`: fresh checkout and exact-revision phase boundaries only; no publisher, account or login-shell installation.
- `tests/acceptance/check-native-service-ordering.sh`: ordering observations adapted to current units.

The predecessor checkout remains unchanged. No new license is assigned to inherited code. Upstream source attribution and existing third-party licenses remain applicable. This file is not a substitute for those licenses or permission to relicense assets.

Tea's upstream MIT license is delivered beside this notice and inside the project image. Other packages and container layers retain their upstream notices/licenses; the input record identifies resolved bytes. Canonical Soda branding is reused, not redrawn or relicensed. See the source repository's `docs/design/branding.md`, `docs/research/predecessor-reuse.md`, `docs/guides/project-clis.md` and dependency locks.

The Soda extension package staged from `system/containers/extension/` and its SodaOS
backend are original Apache-2.0 SodaOS work. Their source is retained in the
candidate's SodaOS source archive, and `soda-LICENSE` plus `soda-NOTICE` accompany
the bundle. Fountain is a modified Forgejo 15.0 LTS executable, licensed under
GPL-3.0-or-later: the candidate retains its exact source archive, including the
standalone extension SDK, root GPL license and the Apache-2.0 license and local
change note for the vendored `code.forgejo.org/go-chi/session` copy. These records
do not relicense Forgejo, upstream templates, dependencies or inherited branding,
or close the remaining actual-artifact third-party/corresponding-source obligations.

The deployment bundle is application/service content for upstream Fedora CoreOS, **not a bootc host image, installer ISO, preinstalled disk, signed release or product acceptance certificate**. Instance provisioning and credentials are never bundle content.
