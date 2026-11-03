# Factory authority and runtime contracts

`access` owns application admission and references to existing standing grants.
[`collaboration`](../../collaboration/README.md) owns remote identities and
collaboration records. [`execution/provenance.go`](../execution/provenance.go)
owns the native/appliance evidence union; [`runtime/placement.go`](../runtime/placement.go)
owns retained repository runtime selections. These are SDK-free contract producers,
not a second workflow, grant ledger, launcher or automation controller.

## Admission and identity

Trusted Fountain session, native-account, gateway and runtime-grant adapters
establish current authority and canonical account/repository bindings. Only then
may they call `Admit`. `ActorEvidence` must never be decoded from browser JSON as
an admission assertion. `AdmittedActor` has private fields: decoding a claimed
actor cannot create admission, and its zero value denies. Evidence accessors copy
permissions and optional identities so callers cannot mutate an admitted value.

`Require` checks exact host/repository, explicit permission and expiry. It does
not re-query revocation. Mutation services still need fresh provider/gateway/grant
checks at their transaction boundary. A valid account establishes neither Factory
configuration permission nor permission to execute, publish or spend. Runtime
grants cannot configure their own policy. No conversion to `RunAuthority` exists.

The application consumer is `service.RepositoryAuthority`. Its trusted adapter
must freshly verify authority on every read/mutation-boundary check, including
canonical principal identity and revocation. The consumer pins source and
connection/runtime scope in addition to actor/host/resource/permission/expiry.
It returns no partial metadata when those checks fail. No HTTP session or native
callback is fabricated for local account admission.

`RunAuthority` is the trusted dispatch snapshot referencing the current repository,
capacity, operator, sponsorship and admission records. Their existing revisions,
transactions, balances and revocation remain authoritative. Structural validation
does not grant permission or establish that those records are active.

It preserves four independently assigned identities, which may coincide but
cannot be inferred from one another:

- The initiating human, nullable for automation without a human initiating this run.
- The optional publishing actor; read-only work need not invent a publisher.
- The scoped execution principal, including runtime, principal ID and execution actor.
- The sponsorship decision-maker and AI-provider custody connection/generation.

AI-provider `ProviderConnectionID` is distinct from a collaboration
`ConnectionID`. Credentials enter neither record. All numeric identities and
retained generations use JSON strings, including nullable actor fields and author
lists. Adapter/store lookup must verify their canonical host and grant scope.

## Placement and policy

`Placement` retains host/repository/runtime/generation, selection actor, revision,
coordination mode and inactive/active/retiring state. `RequireDispatch` refuses
stale generations, other runtimes/resources and non-active selections. It is not
a lease or lock. T12 must hold the owning exclusion/CAS boundary through dispatch,
retire old work before switching and advance the generation. Fountain uses central
coordination; GitHub's first release uses one configured controller. A local lock
does not establish exclusion against independently installed controllers.

`PolicyProposal` deliberately has no active flag or activation method. Account
connection, runtime selection and reading/parsing a proposal enable no automation.
It records eligible authors, trigger classes, fork terms and finite limits, with
an explicit original-creation-evidence or current-state-adoption model. Existing
standing grant owners still determine actual capacity and funding.

`IntakeAdoption` pins one policy/content revision, observation, authority, adopter,
source author, runtime/generation and input digest. Issue and PR refs are exclusive;
PRs require exact head/base and head repository, and a fork requires explicit
proposal terms. That allowance alone grants no fork execution. Original-creation
mode requires a reference to the full authoritative engine proof; current-state
mode cannot present a creation marker as that proof. Unknown source authors cannot
pass explicit author eligibility. Historical creator permission cannot be inferred
from polling, signatures or current author metadata.

D1 still gates GitHub automatic intake activation. Proposal/adoption validation
checks consistency, not enrollment, policy-model qualification, proof authenticity,
current maintainer authority or spending admission. T11/T12 own their actual
activation and dispatch consumers. This producer does not resolve D1 by adding a
qualification label or auto-enrolling a login.

## Execution evidence

`execution.Provenance` requires exactly one native/appliance variant and binds run,
runtime and placement generation. Custody generation, placement generation and
appliance boundary generation remain distinct.

Native evidence explicitly names `same_user_trusted_host`: profile, supervisor
instance, worktree identity and optional process. A started process retains its
invocation, PID, kernel instance/start identity, start time, exact executable path
and SHA-256, plus observed exit code or signal. On Linux, the instance/start facts
are boot UUID and `/proc/PID/stat` start ticks. PID alone is insufficient. Local
paths are server-side evidence, never browser-selected launch instructions.

Native custody separately records not-acquired/held/released/uncertain material
and its acquisition/connection/generation. Released concerns this run's custody;
it does not prove that same-user code never copied credentials or erased the
user's global login. Native evidence has no container/image/unit/isolated-UID or
broker-return fields. D3 remains a real native executor/custody qualification gate.

Appliance evidence retains the actual Project/boundary generation, image,
container, unit, invocation, UID/GID/login, credential-return fact and separate
broker execution/binding/lease observation. A never-started tombstone may have no
boundary or broker binding. A missing/unreachable authority is not retirement
proof. `Settled` requires recorded retirement and custody resolution; callers must
still reconcile the real runtime/broker before releasing reservations. Process
exit alone cannot establish credential cleanup.

## T01-RUNTIME evidence and handoff

Passed Go compilation/source guard and inspection of the three new packages'
dependency closure: no Fountain SDK, concrete host, Project or identity-broker
imports. This does not establish the full engine closure; T02 owns that cutover.

An external integration diagnostic exercised actor scope/expiry/JSON and copy
boundaries, independent authority identities, stale/retiring placement refusal,
policy/content/model/fork bindings and string identities above `2^53`. It captured
a real Linux child using kernel boot/start identity and executable SHA-256,
observed its unsettled live record, and retired it with SIGTERM/wait. No credentials
were acquired and no model/provider work was performed.

The diagnostic stored these records using the existing accepted-generation JSON
fields and Store APIs on private native PostgreSQL. The actual native process
record, independent authority references, proposal/adoption and generation survived
clean restart and forced-crash/WAL recovery. A synthetic uncertain-custody shape
remained fenced; this is not a real credential-withdrawal proof. Final PG shutdown
was clean. No new journal/schema or unit tests were introduced.

Logs: `/home/vince/factory-runtime-contract-go.log`,
`/home/vince/factory-runtime-contract-source-guard.log`,
`/home/vince/factory-runtime-contract-deps.log` and
`/home/vince/factory-runtime-native-journal.log`. Diagnostic:
`/home/vince/factory-runtime-probe/`; retained disposable profile:
`/home/vince/fpg-runtime-contracts-20261031`.

T02 replaces SDK/host-bearing engine/store records through real translators;
T03 establishes application admission outside HTTP; T05/T09 qualify native
lifecycle/custody/execution; T10 qualifies gateway enrollment; T11/T12 implement
actual automation enrollment and runtime placement. Existing production dispatch,
grants, adoption, cancellation and recovery behavior must survive those cutovers.
