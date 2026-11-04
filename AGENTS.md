# Working on Factory

- Implement one dependency-ready slice at a time using
  `docs/development/factory-products/implementation-tasks.md`. Do not confuse a
  completed slice with a finished product.
- Commit early and often: checkpoint each coherent, verified change locally.
  Do not accumulate completed implementation slices in the working tree.
- Follow the selected ownership in
  `docs/development/factory-products/language-allocation.md`: Go owns product
  APIs, durable workflows, canonical identity and PostgreSQL product transactions.
  Rust owns native execution, resource lifetime, credential custody and complete
  authenticated desktop provider connectors. Lit owns the shared UI. Preserve
  these dependency directions and keep one defining implementation.
- Use a user-started Daybreak session only when the work or a check looks like
  hacking. Daybreak has the anti-hacking audit for that work; it is not a general
  security gate. Routine product security engineering belongs in the normal
  implementation queue. Normal coding agents may implement and verify D3 worker
  isolation, credential custody, authentication and IPC admission, including
  ordinary checks. Apply the same real evidence and completion rules to these
  gates as to other slices.
  Do not claim a gate complete from source checks or component probes alone, and
  do not infer Daybreak access from a model name.
- Preserve existing work, retained profiles, credentials and historical evidence.
  All builds, caches, state and scratch belong under `/home`; set TMPDIR there.
- No authored Python, new unit tests, Rust test modules in `src/`, or new unit-test
  roots. Use the smallest useful existing real integration/native check.
- Keep dependencies and providers current following
  `docs/development/freshness.md`. Imported source/locks are historical working
  inputs, not proof of a fresh or qualified release.
- Do not publish credentials, profiles, generated state or process artifacts.
  Keep coordination logs and temporary evidence outside this repository.
- Preserve the shared UI for the native app and Fountain. Fountain server
  implementation and native-provider authority stay outside the standalone
  engine's dependency closure.
- Report what actually ran and what is unfinished. Do not invent completion
  percentages or treat local builds as connected/installed qualification.
