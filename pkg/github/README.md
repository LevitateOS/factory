# GitHub collaboration adapter

The selected [language allocation](../../docs/development/factory-products/language-allocation.md)
moves the complete authenticated desktop session to Rust. This Go provider code
is a retained migration input; `T06-GITHUB-RUST` must cut over OAuth, refresh,
authenticated HTTP and provider decoding together and retire its Go native
token routes. Canonical mapping/SQL/product authority remain in Go. Do not extend
this transitional authenticated Go connector with the next desktop feature.

This package owns the GitHub REST client and its later collaboration adapters.
The current client uses go-github v93.0.0 (BSD-3-Clause), selected from official
release metadata on 2026-11-02, with the supported `2026-03-10` REST API version.
SDK types stay in this provider package and its adapter consumers; the Factory
engine and Store retain their provider-neutral contracts.

`New` requires an explicit trusted native `http.RoundTripper`. It accepts no
token string, token file or environment credential. Native composition must
freshly bind/admit the selected connection through the existing Rust credential
custody and honor the request context. A stored credential alone is not a
verified account or Factory execution authority. Native transport is wired below;
Native desktop login is implemented and live consent/storage/private reads/refresh
were qualified in T06-GITHUB-LIVE. Product account selection and normalized
issue/PR/review/check surfaces remain consumers.

The client makes only GETs to `https://api.github.com`, with no redirects,
cookies or alternate origins. Reads have an eight-second deadline and an 8 MiB
whole-response limit. Invalid or null successful JSON refuses. Failures expose
typed authentication, visibility, permission, rate-limit, moved-resource and
availability states, HTTP status and retry/reset facts without raw provider
prose, token-bearing URL errors or bodies. No mutation or retry loop is present.

`AuthenticatedUser` reads `/user`; it never treats a public username lookup as
sign-in. The connected repository catalogue refuses failed authentication;
`PublicRepositories` is a distinct explicit public lookup. Pages expose numeric
continuations and bounded limits, not arbitrary provider Link URLs. They are
observations of a live list, not a globally atomic catalogue. Repository locators
resolve to actual remote IDs; mutable owner/name labels are not identity keys.

`IssueList` and `Issue` accept a stable remote repository ID, resolve its current
name and make a bounded SDK read. Lists default to open, also support closed/all,
sort by creation and expose the next provider page. GitHub includes PR wrappers:
these are filtered from lists without scanning more pages. An empty filtered
page may still have a continuation. Detail refuses a PR wrapper. Actual remote
issue IDs remain distinct from repository-scoped display numbers. Full raw
Markdown, nullable author/lock/timestamps, assignees and labels are preserved;
no action capabilities are inferred. The final repository lookup must still
match the selected stable ID before any issue data returns. Rename, name reuse,
visibility failure and cancelled/expired reads refuse rather than return an
unverified page. The whole list/detail operation shares an eight-second budget.

IssueRecord/IssuePage contain provider-local facts, not canonical product IDs.
`CanonicalIssueSource` maps current-account repository/participant identity through
the existing registry. Public provider reads alone still do not admit an account.

`Discussion` reads one ordinary issue or PR comment page for an explicit parent
kind/number and stable repository ID. It verifies the parent using the matching
issue/pull endpoint before and after I/O, and rechecks repository identity and
visibility before returning bodies. A PR uses its actual pull ID, not the ID of
its issue wrapper. Wrong-kind or missing parents refuse before comment reads.
Comment URLs must belong to the selected parent and ordinary-comment endpoint;
review comments, submissions and lifecycle events remain separate. Full raw
Markdown, comment IDs, nullable authors/timestamps and observed minimization
reasons are retained. Empty pages still name the verified parent. Paging shares
the same bounded limits and whole-operation eight-second deadline; no scan or
atomic-history claim is made. DiscussionPage also contains provider-local facts
which need canonical registry mapping in the connected consumer.

`PullList` and `Pull` read PR pages and any-state detail from the actual pull
endpoint. Stable repository lookup/recheck, bounded paging and the shared
eight-second operation deadline apply. Full raw descriptions, actual pull IDs,
authors/assignees/labels and dates are retained. The closed filter includes both
merged and unmerged PRs; observed merge facts distinguish their record states.
Draft, merged, mergeability and rebaseability fields retain null when GitHub
omits them or has not computed a result. No retry loop or merge capability is
inferred from these facts.

Base/head references preserve their reported branch name, repository ID/name
and validated full commit ID. A deleted or unavailable fork stays null, while
surviving branch/commit facts remain visible. These are observations of the PR
payload; immutable diff pairs and current target-tip/protection facts must be
resolved by the file/capability readers. PullRecord/PullPage also require the
connected consumer's canonical identity mapping before becoming product records.

`PullDiff` resolves a PR's merge-base/head identity before file or anchored-review
reads. It captures the actual pull ID, PR-payload base SHA, head SHA and target
branch label, compares those full commits, then rechecks the PR and repository
under one eight-second budget. A requested full pair must match the observed
merge-base/head; mismatches or changed PR state return ErrDiffChanged. Discovery
accepts two empty commit selectors. Half-pairs, branch names and malformed IDs
refuse. A missing head/base commit makes the scope unavailable.

PullDiffScope keeps the captured `pull_base` separate from the comparison's
verified `base` (merge base). Closed PR history is preserved; current target-tip
and protection observations belong to their consumers. Compare metadata uses
page two with one commit, which avoids the first page's limited file array.
The file reader below enforces its own immutable-pair paging, patch
completeness and provider ceilings. Real public discovery and matching/mismatched
requested pairs are qualified, alongside production-native refusal for both
empty and saved full-pair descriptors. Successful authenticated/private scope,
canonical mapping and file/UI integration remain with their consumers.

`PullFiles` produces one provider-local file page, with a limit of 1–10. Only
page one permits empty-pair discovery. Continuations require the verified full
merge-base/head pair and retain the same limit. Each read captures and rechecks
the actual PR ID, base/head, base branch and changed-file count around the files
endpoint, then rechecks repository identity/visibility. A changed requested or
observed pair refuses. GitHub's [3000-file ceiling](https://docs.github.com/en/rest/pulls/pulls#list-pull-requests-files)
returns ErrFilesTooLarge before file I/O; missing totals are unavailable. Page
length and numeric continuation must agree with the captured total, so a limited
prefix cannot silently appear complete. The reads share one eight-second budget.
These are bracketed live observations; the provider offers no atomic snapshot
or conditional-pair selector for this endpoint.

File records preserve old/new paths, status and nullable addition/deletion
counts. Removed files map to deleted; renames/copies require the actual previous
filename. Paths are display/anchor data, never local filesystem access or URLs.
`patch` is GitHub's hunk fragment, without standalone file headers. Canonical
mapping and shared-renderer input construction belong to the connected consumer.

Patches are bounded to 16 KiB per file, cut at a UTF-8 boundary and marked
truncated. Smaller fragments use Sourcegraph go-diff v0.9.0 (MIT, selected from
official Go proxy release metadata on 2026-11-02) for hunk syntax, then verify
each declared hunk span and the provider's full addition/deletion totals before
marking complete. A short hunk or omitted changes remain truncated. Unparseable
or unverifiable fragments are unavailable. Missing patches also stay unavailable:
GitHub does not distinguish binary files from omitted large text here. Neither
an extension nor zero statistics is treated as proof of binary content. File
records remain provider-local. Real public observations qualify missing patches
for independently inspected PNG and 306 KB UTF-8 text bytes, actual previous/new
rename paths, and early refusal of a 13,846-file PR. A real 20,951-byte provider
fragment is qualified as a valid UTF-8 16,384-byte original prefix marked
truncated, on a page pinned to the independently verified merge-base/head.
Authenticated/private/canonical/UI consumption remains downstream.

`PullReviews` and `PullReview` read review submissions through the SDK's
[dedicated review endpoints](https://docs.github.com/en/rest/pulls/reviews).
Lists are chronological live provider pages, bounded to 1–50; detail takes the
actual positive 64-bit review ID, separate from the PR number. Both verify the
actual PR before/after I/O and recheck the selected stable repository before
returning bodies. Every review's parent URL must match that PR. Empty pages still
identify their verified parent; missing, foreign-parent and ordinary-issue
selectors refuse. The whole operation shares an eight-second budget.

ReviewRecord retains full raw Markdown, actual review/author IDs, nullable
author/commit/submission facts and the original provider state. Known states map
to pending/approved/changes_requested/commented/dismissed; future or missing
states remain unknown. Pending records cannot claim a submitted timestamp.
Reported commits are historical review facts, with no substituted current head
or latest-head approval. Ordinary issue/PR comments and inline threads remain
separate. Real public paging/detail/empty/refusal checks and three submitted
states are qualified; authenticated pending/private, null/deleted-author edge
cases, canonical mapping and UI integration remain in the full provider gate.

`PullReviewComments` and `PullReviewComment` read inline review comments as live
pages/detail, separately from ordinary discussions and review submissions. They
verify the actual PR before/after I/O and recheck stable repository visibility
under one eight-second budget. Detail lookup uses the repository-wide comment
endpoint, so its returned PR URL must also match the selected parent; a comment
from another PR refuses rather than appearing under the requested subject.

Comment/review/reply IDs, full raw Markdown and diff excerpts, author/association,
nullable paths, commits, line ranges/sides, legacy positions, subject type and
dates remain provider evidence. A reply's parent may be outside its current page
and can be fetched by detail. Original commit is a review commit, not an immutable
diff base. Null current lines do not establish resolution or a complete immutable
anchor; GraphQL thread identity/resolution and verified diff-pair translation
remain consumers. No thread is fabricated from the REST reply links. See the
[review-comment endpoint](https://docs.github.com/en/rest/pulls/comments#list-review-comments-on-a-pull-request).

`PullCommitStatuses` reads one live legacy commit-status history page (1–50)
for a captured PR head. Discovery accepts an empty head only on page one;
continuations require the full captured commit ID. It verifies the actual PR
before/after the status read and rechecks stable repository identity, under one
eight-second budget. Changed/requested heads refuse with ErrChecksChanged.

Status IDs, contexts, creator IDs, nullable raw state/description/target URL and
timestamps remain provider observations. Known pending/success/failure/error
states retain their spelling; unknown/null states remain unknown. The endpoint
returns newest-first history, so repeated contexts and earlier updates are
preserved. Page numbers are not an atomic history snapshot. Empty status history
does not establish success, and this reader does not combine contexts/check runs
or infer branch requirements, current merge gates or write authority. See the
[provider endpoint](https://docs.github.com/en/rest/commits/statuses#list-commit-statuses-for-a-reference).

`PullCheckRuns` reads one live check-run page (1–50) for a captured PR head.
Only page one accepts empty-head discovery; later pages require the captured
full commit ID. The PR's actual ID/head and stable repository visibility are
rechecked before returning. Requested or observed head changes return
ErrChecksChanged. Every reported check run must name the captured SHA and its
actual repository check-run URL. The operation shares one eight-second budget.

Records retain actual check-run IDs, names, nullable raw status/conclusion,
timestamps, details URL, app identity and output title/summary/text/count facts.
Known states/conclusions normalize explicitly; missing/future values stay unknown.
Check-run IDs are not Actions job or workflow-run IDs. No action, requiredness,
combined CI verdict or merge authority is inferred. Annotation bodies are not
fetched and provider URLs are not followed. Output/URLs remain untrusted data for
the canonical presentation consumer.

The SDK request selects `filter=all` within the provider's available window.
`total` describes that window, not an all-time history. GitHub
[limits this endpoint to the 1000 most recent check suites](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference)
when there are more; exhausting its pages alone cannot prove complete CI history
or a satisfied merge gate. The canonical gate consumer must account for this
limit, separate commit statuses, branch requirements, app/context selection and
live changes to checks on the same head. Real public discovery/pinned continuation
is qualified; broader state/race/private/gate/UI qualification remains downstream.

`NewNativeReadHandler` is mounted only by the native executable's private host,
behind its peer/key/instance and application admission/drain gates. Rust's typed
`github_read` port lends one credential with `with_token_operation`, verifies
fresh scope after I/O and cancels Unix HTTP on future drop. Its per-request
sensitive header is removed before dispatch and is not persisted. The Go handler
has a six-second context, always verifies `/user` against the captured account,
then optionally reads one bounded repository page or a stable repository ID.
Typed `issues`/`issue` selectors read a page or detail through the same current
account check and return the provider-local issue observations described above.
Typed `pulls`/`pull` selectors do the same for PR metadata and nullable facts.
Typed `pull_diff` resolves or verifies the commit pair through the same native
account gate. Changed pairs return HTTP 409/github_diff_changed. Rust validates
the returned parent, repository, full commit identities and any requested pair.
Typed `pull_files` reads one bounded page behind the same account/credential
gate. Go shares the producer's query validation and returns HTTP
413/github_files_too_large for the provider ceiling. Rust checks the nested diff
repository/PR/pair and the requested page/limit, total, item count and numeric
continuation before returning success. Discovery and pinned descriptors are
qualified through real isolated OS custody/native PG/Go/GitHub authentication
refusal. A generated invalid token never qualifies successful file access;
that remains part of the authenticated provider gate.
Typed `pull_reviews`/`pull_review` read submission pages or an actual review ID
behind the same account/credential gate. Go validates review selectors and Rust
checks returned repository, PR kind/number, page cardinality and any requested
detail ID. Real isolated native keyring/PG/Go/GitHub 401 qualification covers
both descriptors, including a review ID larger than 32 bits. It proves refusal
and custody, not a successful authenticated review read.
Typed `pull_check_runs` carries page/limit and optional full head through that
same account/credential boundary. Rust checks the returned repository, PR
kind/number, full head/requested pin, bounded item count and each item's head.
Changed check heads return HTTP 409/github_checks_changed. Real isolated native
OS custody/PG/backend/GitHub 401 qualifies discovery and a saved real full-head
continuation descriptor, without qualifying successful authenticated CI access.
Typed `issue_comments`/`pull_comments` selectors use the same admission and
current-account verification. Malformed state/page/number descriptors refuse
before provider I/O. Rust checks kind, account, connection, selected resource
repository and discussion parent kind/number on success.
Only provider-local read facts or sanitized failures return. The
borrowed transport/header exists only for that call; no SDK token source or
background retry worker is installed. This is not a public HTTP proxy or a
webview token command, and it does not admit Factory work.

The native RPC's separate `verify` operation accepts a new unpersisted token,
with no claimed account, connection, repository or page. It reads only `/user`
and returns the actual remote account ID/login. Rust's `github_verify` consumes
that native token, checks the response and current runtime, then returns an
opaque `Account`/`Token` pair to native login code for OS custody. Refusals return
only HTTP status; neither token nor raw response reaches the browser. This
bootstrap does not create a saved account or admit Factory work. The other read
operations still require a stored connection and matching actual account.
Successful real OAuth verification/storage is qualified separately. Never store a
guessed placeholder account to bootstrap the existing-account port.

T06-GITHUB still owns complete normalized issue/PR/review/check/capability
adapters, connected canonical private reads and remote revocation. The owned
public OAuth client ID with device flow enabled is configured; no client secret
or shared app key belongs in the desktop. No connected-plugin, GitHub CLI or
personal account credentials are silently borrowed by this package.

The first live component observation made seven read-only requests to actual
GitHub without credentials: public repository resolution and stable-ID lookup,
two public pages, real 404 and 401 responses. Invalid selectors and cancelled
contexts made no I/O. Evidence is recorded in the canonical implementation
backlog; those public observations do not qualify authenticated login/private
reads. No synthetic server or public-data fallback completes that gate.
The native transport observation additionally uses the production executable,
real isolated keyring/PG/Go and actual GitHub 401 replies with generated invalid
tokens. A diagnostic composition uses the same host/handler/SDK and observes
headers/pauses before real HTTPS to prove rotation, withdrawal and cancellation.
It does not replace GitHub responses or qualify an authenticated account.

## Canonical native account and repository observations

`NewNativeAccountHandler(network, NativeAccountScope)` composes the same private
handler with the runtime-owned profile and canonical registry port. The runtime
creates that scope after opening its Store; neither profile nor provider endpoint
comes from a browser or read payload. `NewNativeHandler`/`NewNativeReadHandler`
without that scope retain their provider-local behavior and refuse canonical
operations. All operations remain behind the same peer/key/instance/admission
gates and fixed GitHub network origin.

Typed `binding` and `canonical_repositories` reads first verify `/user` against
the expected current native credential account. Only that observed ID/login can
enter atomic Store binding. GitHub web origin `https://github.com` remains the
credential locator; canonical host API base is `https://api.github.com`. A stored
connection remains distinct from an active product actor or permission.

The canonical repository operation makes one authenticated provider page read,
then maps each actually observed repository and exact connection association.
It returns the neutral `collaboration.Page[RepositoryRef]`, with local string IDs,
opaque remote IDs, numeric-string next-page cursor and a content-derived version
of that connection/page observation. Limits and numeric continuation checks are
bounded; the revision is not a global inventory version. Failed provider reads
return sanitized failures instead of cached observations or public fallback.

Rust checks the owned credential/runtime profile, current owner/revision/lease,
returned binding scope, host origin, remote/local ID separation, page bounds,
continuation and duplicate identities before exposing successful native results.
No token command or browser direct-provider route is introduced. Current
acceptance qualifies the actual Rust/Go/OS-keyring/PostgreSQL path with declared
positive provider responses, account/repository rename, reconnect, cancellation,
refusal and backend restart, plus actual GitHub invalid-credential 401s. It does
not relabel declared positive canonical reads as successful live GitHub reads.

## Canonical issues

`CanonicalIssueSource` implements the neutral `CollaborationIssueSource` inside
one current native credential operation. It validates actor/query scope, freshly
verifies `/user`, resolves the selected canonical repository on its configured
host, and reuses the existing stable-ID/locator-checked SDK issue reads. Only then
does it re-observe repository/connection and map actual author/assignee identities.
Missing/query errors retain neutral service and provider classifications.

`canonical_issues`/`canonical_issue` take canonical local repository IDs; the
existing `issues`/`issue` selectors retain remote-ID semantics. Canonical replies
contain binding, explicit local repository scope and neutral issue pages. Rust
checks profile/connection/host, issue kind/repository/number, participant scope,
requested state/detail and bounded paging. Cross-provider collisions refuse.

Full Markdown, labels, dates and nullable author/lock facts are preserved. Absent
SDK assignee/label arrays remain null; observed empty arrays remain empty. The
neutral issue contract carries nullable `locked` (optional for older TypeScript
producers). Capabilities remain null until their current actor/resource source
observes them. Content versions describe scoped reads, never mutation authority.

PR wrappers are filtered without scanning. An empty page may have a continuation;
the canonical service/native transport accepts this. The connected UI consumer
must remove its current empty-nonterminal parser refusal before claiming parity.
Actual native acceptance covers declared positive issues over real keyring/PG/
Go/Rust, real authentication refusal, cancellation, rename and restart. Live
positive canonical issue reads, discussion, PR normalization and UI remain pending.
