# Factory structural audit — 2026-11-08

## Assessment

Serious, repairable complexity debt. Fragmented ownership, repeated mechanisms
and incomplete vertical integration increase delivery cost and hide unusable
product paths. Targeted integration and consolidation are justified within the
selected Go/Rust/Lit allocation.

This was a read-only source audit with three independent review passes. No
implementation, builds, runtime checks or security qualification ran as part of
the audit. Coverage was a repository inventory plus focused source/reference
tracing of workflows, persistence, native execution/providers, desktop/UI and
Fountain native operations; it was not inspection of every statement.

Audited revisions:

- Factory: `502147c6931cfb6dbb4dde1fd148264ab61c57cc`.
- Fountain baseline: `1b523c6e3d7370b4abfd3894f7c6cf9bf1853c03`.
- Fountain gateway: `1a29a5e86e06e370486afeee689eaaf54ea04b7b`.

The later consolidation brought Factory's implementation history and the four
existing Daybreak commits through `0c0db8f` into canonical Factory main. Those
Daybreak changes were not newly qualified by this audit or consolidation. The
original main-checkout plan edit remains uncommitted: its T06-CONNECT `Active`
status overrides the incoming committed `Complete` status, and its original
appended note is preserved verbatim.

The standalone Fountain gateway work is retained as two inspectable patches in
[fountain-gateway-review](fountain-gateway-review/README.md). Its defining source
remains in the separate Fountain repository.

## Scale

Physical line counts at the audited Factory revision, including comments and
blank lines; these measure maintenance burden rather than removable code.

| Material | Files | Lines |
| --- | ---: | ---: |
| Go source | 743 | 133,112 |
| Rust source | 125 | 44,003 |
| TS/JS source | 167 | 36,521 |
| Tests, integration checks and stories | 118 | 62,304 |
| Markdown documentation | 55 | 51,100 |

The classification separates the named test/check/story roots from source. Some
feature-gated check support also lives inside product files.

Particularly large owners:

- Canonical implementation plan: 32,013 lines.
- Security handoff: 5,611 lines.
- `tools/native-checks/native_mcp_signing_check.go`: 7,043 lines.
- `tools/native-checks/src/native_github_review_requests.rs`: 5,230 lines.
- `frontend/factory/request/request.ts`: 3,286 lines.

Size alone does not establish overengineering. The findings below identify
concrete hidden links, needless links and surplus maintenance work.

## Findings and minimal design directions

### 1. High: finish mandatory native product composition

`pkg/factory/runtime/native_product.go:80` constructs the issue-work service with
Store and Visibility but without Status, Authority or Membership.
`pkg/factory/service/issues.go:44` requires all those dependencies and returns
unavailable when they are absent. Native run-status composition at
`native_product.go:88` similarly lacks the ports required by
`service/run_status.go:44`; native run commands also lack their command service.
No later assignments in the native binder supply those fields.

Impact: component implementation exists while issue-work inspection, run
inspection and intervention paths remain unusable in this composition. Required
product dependencies are represented as optional zero-value fields.

Direction: finish the mandatory bindings for one complete inspection/intervention
journey and validate dependencies when enabling that route group. Preserve
fail-closed authorization.

A related delivery gap is `lib/desktop-runtime/src/fountain_provider.rs`: its only
external caller is the credential integration checker. Desktop sign-in remains
GitHub-specific. This is unfinished integration, not proof the enrollment checks
are unnecessary.

### 2. High: freeze historical schema transitions

Every Store open invokes `initializeDeliveryRepairSchema`
(`pkg/store/store.go:125`). The initializer scans current CREATE TABLE strings,
cuts out an expression using column/punctuation delimiters, and derives the old
constraint by removing one exact final branch
(`pkg/store/factory_delivery_repair_schema.go:40-60`).

Impact: an otherwise valid schema edit can break old-profile startup unless its
author understands the hidden dependency on literal DDL formatting and branch
ordering. The failure scenario was not reproduced during this audit.

Direction: freeze explicit prior/current migration definitions and select
transitions through recorded schema versions. Preserve rejection of unknown
retained constraints.

### 3. Medium: make progress ownership cancellation-aware

`pkg/factory/control/progress.go:18` obtains an ordinary global mutex.
`advanceProgressPage` holds it across settlement and multiple workflow/provider
stages. Readiness work has a 120-second pass limit. Meanwhile,
`control/completion_retry.go:22` establishes a 30-second deadline and then waits
on the same ordinary mutex, whose wait does not observe that context.

Impact: unrelated slow work can delay an interactive command beyond its own
deadline. Diagnosing the delay requires understanding the whole progress pass.
Latency was not reproduced during this audit.

Direction: first make admission to progress ownership cancellation-aware; then
narrow serialization around genuinely conflicting state transitions. Retain the
single durable coordinator owner.

### 4. Medium: give issue-detail refresh one snapshot owner

`frontend/factory/work/detail.ts:480` refreshes the parent. Acceptance and
completion events also call only the parent's load method. The journey and
completion-retry children independently fetch overlapping data but return early
when their transport/repository/issue context remains unchanged
(`work/journey.ts:46`, `work/completion-retry.ts:67`).

Impact: a refreshed screen can combine fresh parent information with older
journey/control state. This is source-level evidence; no browser reproduction
ran during the audit.

Direction: one issue-detail read/refresh owner supplies decoded data and lifetime
to the child controls. Preserve server admission and revalidation before effects.

### 5. Medium: move Fountain ownership protocol below its consumers

Fountain `services/issue/own_collab.go:21` explicitly documents a mirrored claim
protocol because importing the canonical native-operation service would create
an import cycle. Resource builders, owner strings and scope encoding must remain
compatible with the service implementation and recovery parser. Pull ownership
has the same dependency-direction problem in `services/pull/own_ordinary.go:20`.

Impact: changing the protocol requires coordinated edits across packages; drift
can strand recovery. This finding concerns the added native-operation architecture,
not inherited Forgejo file count.

Direction: put the common ownership primitive and resource encoding in a small
lower-level owner independent of issue/pull execution. Keep operation-specific
permission and effect rules explicit.

### 6. Medium: reduce the native collaboration dispatcher's responsibilities

`lib/desktop-runtime/src/collaboration.rs:54` combines provider effects,
credentials, preparation, execution and signing. Mutation metadata is repeated
in foreground/background representations and several conversion, validation,
metadata-extraction and execution matches.

Impact: a new collaboration operation requires updates to parallel lists and
knowledge of execution-adjacent infrastructure.

Direction: retain shared authenticated admission/lifetime handling, represent
common mutation metadata once around a typed operation, and keep execution and
preparation dispatch with their existing owners. Preserve the distinct
background allowlist, deadlines, one-shot claims and uncertain outcomes.

### 7. Medium: encapsulate scenario support in the existing check owner

`cmd/soda-desktop/src/main.rs:58` introduces feature-gated check state into the
desktop owner; scenario-specific configuration and combinations occupy the
argument parser beginning at line 408. Several integration drivers also contain
thousands of lines of scenario orchestration.

Impact: ordinary startup/lifecycle changes require understanding the surrounding
check matrix. Fixture maintenance becomes another substantial implementation
surface. The compile-time feature gate is legitimate and does keep this support
out of ordinary builds.

Direction: encapsulate scenario configuration/hooks in the existing check owner
and retain a narrow desktop lifecycle interface. Preserve checks through the
real application. This audit did not establish that existing coverage should be
deleted.

### 8. High process cost: separate current execution view from historical evidence

The 32,013-line canonical plan combines current requirements, corrections and
extensive historical evidence. The 5,611-line handoff and separate assumptions
register contain overlapping progress narratives.

Impact: determining the next authoritative requirement involves archaeology;
corrections require maintaining several narratives. The original main-checkout
uncommitted Active status conflicting with later completion is a concrete example
of different retained views needing reconciliation.

Direction: maintain a short current execution view with references to preserved
historical evidence. Preserve original evidence and human text.

## Preserve

The shared command journal, immutable receipts, credential custody, isolated
worker boundary and explicit uncertain outcomes address real requirements.
Factory already shares human-command preparation/reconciliation and row-locked
journal reduction. Thin typed wrappers preserving distinct operation contracts
are not automatically surplus. Fountain SQL effects, Git publication and
notification completion genuinely have different atomicity boundaries.

The retained setup approval uses the existing PendingRecord owner. The new
Fountain gateway is a narrow read-enrollment component; this pass found no
comparably strong structural problem in that component itself.

## Conclusion

The main delivery risk is fragmented ownership combined with component-first
delivery. Finish the native product bindings, remove the demonstrated hidden
migration/ownership links, and consolidate read/refresh ownership before adding
further operation breadth. The earlier untangling improved specific areas but
did not establish that the overall structure was resolved.

These are audit recommendations, not implemented fixes. Existing provider,
installed-host, custody/IPC and D1/D2/D3/D5 gates retain their original status and
required evidence. No removable-code percentage or repair-duration estimate was
established by this source audit.
