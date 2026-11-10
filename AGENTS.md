# Working on Factory

- Minimize the total work needed to deliver the requested result correctly.
  Treat tokens, tool calls, builds and the user's attention as costs. Never
  prolong work to use available context, tokens or runtime; activity and output
  volume are not progress.
- Before adding investigation, scaffolding, checks or documentation, identify
  the concrete requirement, observed failure or unresolved risk it addresses.
  Omit work without that connection. Reuse established facts until a relevant
  change or failure gives a reason to revisit them.
- Define completion from the requested behavior and required verification.
  Once those are satisfied, commit and move to the next authorized slice or
  stop. Avoid optional polish, speculative hardening and repeated analysis.
  Keep explanations and updates brief; do not create extra reporting machinery
  to demonstrate compliance with these rules.
- Spend at least 80% of active work on implementing and integrating product
  behavior. Treat planning, test infrastructure, evidence formatting and status
  reporting as support work. This is a working priority, not a claimed progress
  percentage or permission to skip a required qualification gate.
- Choose the next dependency-ready slice by the usable product journey it
  advances. Implement its defining behavior across the necessary Go, Rust and UI
  boundaries before creating another internal checkpoint. Keep unsupported
  provider behavior explicitly unavailable, then move to independent work.
- Reuse the existing command journal, native connector, integration check and
  documentation structure. Consolidate repeated operation plumbing when doing
  so makes the current slice simpler. Do not create a new framework, parallel
  state machine, per-operation fixture suite or speculative edge-case matrix
  without a concrete requirement that the existing owner cannot meet.
- Use `untangle-codebase` (`/home/vince/.codex/skills/untangle-codebase/SKILL.md`)
  as ongoing design guidance: ask whether pieces should change together. Put
  related behavior together, separate independent concerns, hide internals and
  make dependencies point toward more stable contracts.
- In code touched by the current slice, look for hidden links, needless links
  and surplus: duplicated rules, implicit conventions, coupled internals,
  pass-through wrappers, unused extension points and dead code. Treat special
  cases as evidence of requirements; fix the underlying design when that removes
  several problems while preserving intended behavior and necessary exceptions.
- Applying these principles does not start the skill's full workflow. A whole
  repository inventory/refactor, new goal, subagent sweep or optional comment/test
  cleanup needs explicit task scope. Keep ordinary improvements within the
  authorized slice and use the existing verification and stopping rules.
- Verify the behavior and risks changed by the slice with the smallest real
  check that can prove them. Fix a failed check and rerun the affected check;
  broaden testing only for a demonstrated gap or required gate. Do not repeat
  successful builds, long expiry waits or connected journeys merely to collect
  more evidence. A fixture pass does not qualify a connected or installed gate.
- For desktop state and navigation, first read the live developer-tool guide
  in `cmd/soda-desktop/README.md` ("Live developer inspection and control").
  Recover the existing app/profile, release and original command receipts, then
  prefer `factory --developer-cli --profile ABSOLUTE_PATH status`, `ui tree`
  and `ui wait` over repeated screenshots or coordinate guessing. Use labeled
  controls and captured scopes for authorized actions; `dispatched` alone does
  not prove product completion. On `outcome_unknown`, inspect the original saved
  command/receipt rather than issuing another click or replacement action ID.
  A missing endpoint means diagnostics are unavailable, not that work stopped.
  Preserve paused/active work when enabling diagnostics or upgrading an older
  app. Use screenshots for concrete visual questions and direct OS input for
  keyboard/window behavior; semantic UI checks do not establish those facts.
- Keep evidence concise and actionable: record the result, command or retained
  artifact location, material limitations and next dependency once. Add hashes
  only where integrity or an explicit gate needs them. Preserve historical
  evidence, but stop appending duplicate narratives and intermediate attempts
  to the canonical plan or security handoff. If support work repeatedly crowds
  out implementation, state the blocker briefly and return to product code.
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
