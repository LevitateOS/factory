# Retained integration work

The working-tree patches preserve all source changes present at extraction,
including tracked edits and untracked files, from the original SodaOS and
Fountain checkouts. Base revisions and patch hashes are recorded in
[`docs/source-import.json`](../docs/source-import.json).

For recovery, apply each patch in a clean checkout of its recorded base revision:

```sh
git apply --binary /absolute/factory/integrations/soda-forge/working-tree.patch
# In the separate Fountain checkout:
git apply --binary /absolute/factory/integrations/fountain/working-tree.patch
```

Do not apply these over the original dirty working trees: their changes are
already present there. These patches include implementation and integration
fixtures; they exclude ignored build artifacts, account tokens and local profiles.
They are archival source snapshots, not patches that should be applied during
normal Factory builds, nor active duplicate owners of product components.

The Go consumer cutover is committed in Forge as `a1cd0499`, using the pinned
published Factory module and removing duplicate Go owners. The shared UI consumer
cutover is Forge commit `5e695dfb`: it removes the component/story/design/build
copies and reads the linked Factory source package. Native consumer commit
`6bb9b60d` removes the duplicate Rust product owners and pins the shared Factory
runtime/transport crates for Forge's retained consumers. Its actual managed
native lifecycle check passes. SDK distribution, deployed parity and connected
product journeys remain unfinished. These
recovery patches describe the initial import, not the subsequent consumer commit.
A standalone build does not establish parity with an installed Forge.
Fountain's retained server changes keep their GPL-3.0-or-later license. Original
SodaOS changes keep their existing license and inherited per-file notices.

`soda-forge/control-integration-sources.tar.gz` preserves the original coordinator
integration tests and fixtures with their original paths. Those tests currently
instantiate appliance hosts and native Fountain adapters; they still run in their
original SodaOS owner. They are archived here rather than introducing the
appliance and SDK into the standalone module. The first import commit also
retains their unmodified source. Retargeting these journeys to the extracted
module is part of the consumer cutover, not a claimed passing Factory check.
Store integration tests remain active in `pkg/store/`.
