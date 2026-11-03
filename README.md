# Factory

Factory is a Linux-first desktop application and Go workflow engine with a shared
Lit frontend for the native Tauri host and Soda Forge/Fountain.

This repository imports the implementation in progress from SodaOS. It is not a
completed or released desktop product. Connected GitHub login/writes, host
execution, automatic intake, full PR journeys and installed distribution remain
unfinished. The dependency-ordered backlog is in
[the implementation plan](docs/development/factory-products/implementation-tasks.md).

The initial commit preserves the product source before adapting its build
boundaries. Subsequent extraction commits make this checkout independent of the
original source trees. Existing versions and lockfiles are imported as working
source, without claiming a fresh release baseline.

## Source and integration history

[Source provenance](docs/source-import.json) records the original revisions and
SHA-256 hashes of imported product files. The `integrations/` directory also
preserves the complete tracked and untracked source changes from both working
trees as Git binary patches. These include unfinished integration code and
existing human work; the original working trees have not been reset or committed.
They are recovery snapshots, not a second active implementation of Factory.

Fountain server implementation belongs to Fountain. Its retained patch and
license remain under `integrations/fountain/`; they are not dependencies of the
standalone backend. See [integration notes](integrations/README.md).

Original SodaOS work remains Apache-2.0. Existing third-party and inherited
notices remain applicable; see [LICENSE](LICENSE), [NOTICE](NOTICE), and the
licenses alongside the imported integration material.
