package control

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
)

// Complete-inventory bounds (D13): every open non-PR native issue of
// the repository, ordered by issue index, with the exact delivered
// context needed to interpret them. Missing permissions or pages,
// excess size and unstable evidence visibly hold the gate; nothing is
// truncated and an empty page is never confused with a complete empty
// inventory.
const (
	maxCompletionInventoryPages = 256
	maxCollectorRestarts        = 3
)

// Inventory hold reasons name the resumable step for the cycle wait
// reason. They carry no evidence bytes.
const (
	HoldInventoryPage        = "inventory_page_invalid"
	HoldInventoryRevision    = "inventory_revision_changed"
	HoldInventoryOversize    = "inventory_evidence_oversize"
	HoldInventoryUnavailable = "inventory_unavailable"
	HoldInventoryInProgress  = "inventory_collecting"
)

// NativeInventoryIssue is one enumerated open non-PR native issue with
// its canonical evidence bytes. The producer (the FW32 native-page
// joint over the FW29 repository_issues family plus existing individual
// families, under one stable revision) orders items by ascending issue
// index and bounds each Data; the collector bounds the total.
type NativeInventoryIssue struct {
	Data  string
	Index int64
}

// NativeInventoryPage is one bounded enumeration page. A terminal page
// carries complete=true with an empty cursor; a nonterminal page
// carries nonempty items with the cursor equal to the final returned
// index, strictly greater than the input. An empty incomplete page is
// invalid, as is a nonempty cursor that is not a canonical positive
// int64 decimal index.
type NativeInventoryPage struct {
	Issues     []NativeInventoryIssue
	NextCursor string
	Revision   int64
	Complete   bool
	Idle       bool
}

// CompletionIssueEnumerator supplies native pages plus the idle
// revision bracket. Control owns this interface because the native
// snapshot adapter lives outside the control import boundary; the FW32
// handoff binds it to the FW29 repository_issues family. Tests supply
// deterministic fixtures; production never consults a live model here.
type CompletionIssueEnumerator interface {
	ObserveRevision(ctx context.Context) (int64, bool, error)
	EnumerateIssues(ctx context.Context, repository int64, cursor string) (NativeInventoryPage, error)
}

// SealedInventory is one durable complete inventory: every open
// non-PR native issue of the repository at one idle native revision,
// ordered by issue index, with the bound evidence and the local
// bindings that interpret it. An empty issue set seals as a valid
// complete empty inventory with its own digest: each completion gets
// its own decision even when none remain. Bodies carries the parsed
// substance behind every collected Data payload, index-aligned with
// Indexes, so the seal persists bodies — not just the digest — for the
// funded decision.
type SealedInventory struct {
	Local      factory.CompletionLocal
	Indexes    []int64
	Bodies     []factory.CompletionIssueEvidence
	Digest     string
	Repository int64
	Generation int64
	Revision   int64
	Issues     int
	Evidence   int
}

// InventoryCollector resumes one bounded collection across passes. The
// cursor restarts from empty whenever the native revision moves so a
// concurrent insertion can never be omitted; stable revisions make
// progress instead. Progress serializes into the cycle row so restarts
// and crashes resume instead of rescanning.
type InventoryCollector struct {
	Running  [32]byte
	Issues   []NativeInventoryIssue
	Local    factory.CompletionLocal
	Cursor   string
	Revision int64
	Restarts int
}

// parseInventoryCursor validates a nonempty cursor as a canonical
// positive int64 decimal index.
func parseInventoryCursor(cursor string) (int64, error) {
	index, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || index < 1 || strconv.FormatInt(index, 10) != cursor {
		return 0, errors.New("invalid inventory cursor")
	}
	return index, nil
}

// checkInventoryPage validates one page against the cursor that
// fetched it and the collector bounds.
func checkInventoryPage(page NativeInventoryPage, cursor string, collected int, evidence int) error {
	if page.Revision < 0 || !page.Idle {
		return errors.New(HoldInventoryRevision)
	}
	if page.Complete {
		if page.NextCursor != "" {
			return errors.New(HoldInventoryPage)
		}
	} else {
		if len(page.Issues) == 0 {
			return errors.New(HoldInventoryPage)
		}
		last, err := parseInventoryCursor(page.NextCursor)
		if err != nil {
			return errors.New(HoldInventoryPage)
		}
		if cursor != "" {
			current, err := parseInventoryCursor(cursor)
			if err != nil || last <= current {
				return errors.New(HoldInventoryPage)
			}
		}
		// The cursor must also match the final returned index: pages
		// chain exactly, so a resumed scan can never skip an issue.
		if last != page.Issues[len(page.Issues)-1].Index {
			return errors.New(HoldInventoryPage)
		}
	}
	var base int64
	if cursor != "" {
		var err error
		base, err = parseInventoryCursor(cursor)
		if err != nil {
			return errors.New(HoldInventoryPage)
		}
	}
	prior := base
	for _, issue := range page.Issues {
		if issue.Index <= prior {
			return errors.New(HoldInventoryPage)
		}
		prior = issue.Index
		evidence += len(issue.Data)
		if evidence > factory.MaxReassessmentEvidenceBytes {
			return errors.New(HoldInventoryOversize)
		}
	}
	if collected+len(page.Issues) > factory.MaxReassessmentIssues {
		return errors.New(HoldInventoryOversize)
	}
	return nil
}

// foldInventoryPage appends one validated page to the running digest,
// one deterministic hash step per ordered (index, exact canonical body)
// item. The per-item chain is associative over any page partition:
// folding page-by-page during collection matches folding the whole
// ordered list at reseal, so the digest is independent of pagination.
// The chain stays order-sensitive on purpose: a reordered or otherwise
// malformed sequence is never sorted into admission here. Repository,
// generation, revision and index binding stays in sealInventoryDigest.
func foldInventoryPage(running [32]byte, repository, generation int64, issues []NativeInventoryIssue) [32]byte {
	_, _ = repository, generation
	for _, issue := range issues {
		h := sha256.New()
		h.Write(running[:])
		h.Write([]byte(strconv.FormatInt(issue.Index, 10)))
		h.Write([]byte{0})
		h.Write([]byte(issue.Data))
		h.Write([]byte{0})
		copy(running[:], h.Sum(nil))
	}
	return running
}

// sealInventoryDigest binds the sealed set: repository, generation,
// bracket revision, ordered indexes and the folded evidence.
func sealInventoryDigest(repository, generation, revision int64, running [32]byte, indexes []int64) string {
	return factory.ReassessmentInventoryDigest(repository, generation, revision, running, indexes)
}

// Collect advances one bounded collection: it brackets an idle native
// revision, resumes from the saved cursor, validates every page shape,
// and either seals the complete inventory or reports the hold reason
// with updated resumable progress. A revision move restarts from empty
// (bounded restarts per call); anything else holds without consuming
// the restart budget. Empty complete inventories seal valid.
func (c *InventoryCollector) Collect(ctx context.Context, enum CompletionIssueEnumerator, repository, generation int64, local factory.CompletionLocal) (*SealedInventory, *InventoryCollector, error) {
	if err := local.Validate(); err != nil {
		return nil, c, errors.New(HoldInventoryUnavailable)
	}
	if enum == nil {
		return nil, c, errors.New(HoldInventoryUnavailable)
	}
	g := &inventoryGather{ctx: ctx, enum: enum, repository: repository, generation: generation, local: local, budget: readinessBudgetFrom(ctx), next: *c}
	// The collector prepays its TWO revision observations (opening +
	// confirming) upfront against the existing budget before any
	// native read. The outer bracket's own 2-reservation in
	// completion_drive.go stays unchanged and prepaid: these units
	// cover the collector's own two calls, never the outer ones, so
	// no observation is charged twice. A short budget holds
	// collecting with the incoming prefix/cursor untouched and zero
	// native spend. A nil budget (store-free fixtures) carries no
	// charge and keeps existing test behavior.
	if g.budget != nil {
		if !g.budget.reserveNativeRPC(2) {
			return nil, c, errors.New(HoldInventoryInProgress)
		}
		// TWO upfront units dedicated to the opening observation
		// and the eventual confirmation. Every extra restart or
		// re-bracket observation reserves its OWN unit before the
		// call and never consumes the held confirmation.
		g.confirmReserved = true
	}
	// The opening observation spends its dedicated upfront unit.
	if out, err := g.openCollector(); err != nil {
		return nil, out, err
	}
	for pages := 0; pages < maxCompletionInventoryPages; pages++ {
		sealed, out, done, err := g.collectPage()
		if done {
			return sealed, out, err
		}
	}
	return nil, g.progress(), errors.New(HoldInventoryInProgress)
}

// inventoryGather carries the shared state behind one inventory
// collection so each duty reads through one small check.
type inventoryGather struct {
	ctx             context.Context
	enum            CompletionIssueEnumerator
	repository      int64
	generation      int64
	local           factory.CompletionLocal
	budget          *readinessPass
	confirmReserved bool
	revision        int64
	next            InventoryCollector
	evidence        int
}

// progress reports the current resumable collector prefix.
func (g *inventoryGather) progress() *InventoryCollector {
	out := g.next
	return &out
}

// observeRestart reserves its own unit before every call: restart
// re-brackets never spend the held confirmation. A refused
// reservation holds collecting with the current prefix/cursor
// preserved: no partial seal, no decision or provider charge.
func (g *inventoryGather) observeRestart() (int64, bool, error) {
	if g.budget != nil {
		if !g.budget.reserveNativeRPC(1) {
			return 0, false, errors.New(HoldInventoryInProgress)
		}
	}
	return g.enum.ObserveRevision(g.ctx)
}

// observeConfirm spends the held confirmation once; past it, every
// further confirmation reserves anew before the call.
func (g *inventoryGather) observeConfirm() (int64, bool, error) {
	if g.budget != nil {
		if g.confirmReserved {
			g.confirmReserved = false
		} else if !g.budget.reserveNativeRPC(1) {
			return 0, false, errors.New(HoldInventoryInProgress)
		}
	}
	return g.enum.ObserveRevision(g.ctx)
}

// openCollector takes the opening observation and binds the working
// collector, resetting or restarting on a moved revision.
func (g *inventoryGather) openCollector() (*InventoryCollector, error) {
	revision, idle, err := g.enum.ObserveRevision(g.ctx)
	if err != nil {
		return g.progress(), errors.New(HoldInventoryRevision)
	}
	if !idle {
		return g.progress(), errors.New(HoldInventoryRevision)
	}
	if g.next.Revision != 0 && g.next.Revision != revision {
		if g.next.Restarts >= maxCollectorRestarts {
			return g.progress(), errors.New(HoldInventoryRevision)
		}
		g.next = InventoryCollector{Revision: revision, Restarts: g.next.Restarts + 1}
	}
	if g.next.Revision == 0 {
		g.next = InventoryCollector{Revision: revision, Local: g.local, Restarts: g.next.Restarts}
	}
	g.next.Local = g.local
	for _, issue := range g.next.Issues {
		g.evidence += len(issue.Data)
	}
	g.revision = revision
	return nil, nil
}

// collectPage collects one enumerated page, reporting done when the
// collection sealed, held or errored.
func (g *inventoryGather) collectPage() (*SealedInventory, *InventoryCollector, bool, error) {
	if err := g.ctx.Err(); err != nil {
		return nil, g.progress(), true, err
	}
	page, err := g.enum.EnumerateIssues(g.ctx, g.repository, g.next.Cursor)
	if err != nil {
		return nil, g.progress(), true, routeInventoryPageError(err)
	}
	if page.Revision != g.revision || !page.Idle {
		return g.rebridgeRevision()
	}
	if err := checkInventoryPage(page, g.next.Cursor, len(g.next.Issues), g.evidence); err != nil {
		return nil, g.progress(), true, err
	}
	g.next.Running = foldInventoryPage(g.next.Running, g.repository, g.generation, page.Issues)
	g.next.Issues = append(g.next.Issues, page.Issues...)
	for _, issue := range page.Issues {
		g.evidence += len(issue.Data)
	}
	// The cursor advances with the folded prefix, so any hold
	// below carries actually-resumable progress: resuming never
	// re-appends the already-folded final page.
	g.next.Cursor = page.NextCursor
	if !page.Complete {
		return nil, nil, false, nil
	}
	return g.confirmSeal()
}

// routeInventoryPageError names one enumeration failure's collecting
// hold with the current prefix/cursor preserved for resume; the
// producer names exact holds visibly: oversize, malformed and unstable
// evidence keep their reason instead of collapsing into unavailable.
func routeInventoryPageError(err error) error {
	if errors.Is(err, ErrReadinessPassPending) {
		return errors.New(HoldInventoryInProgress)
	}
	switch err.Error() {
	case HoldInventoryOversize, HoldInventoryPage, HoldInventoryRevision:
		return err
	default:
		return errors.New(HoldInventoryUnavailable)
	}
}

// rebridgeRevision re-brackets and restarts one collection whose page
// revision moved, holding when restarts or reservations run out.
func (g *inventoryGather) rebridgeRevision() (*SealedInventory, *InventoryCollector, bool, error) {
	if g.next.Restarts >= maxCollectorRestarts {
		return nil, g.progress(), true, errors.New(HoldInventoryRevision)
	}
	// Re-bracket before restarting: the saved cursor belongs
	// to the moved revision and resuming it could omit the
	// concurrent insertion. The re-bracket reserves its own
	// unit and never spends the held confirmation; a refused
	// reservation holds with the advanced cursor, which
	// actually resumes.
	fresh, idle, err := g.observeRestart()
	if err != nil {
		if err.Error() == HoldInventoryInProgress {
			return nil, g.progress(), true, err
		}
		return nil, g.progress(), true, errors.New(HoldInventoryRevision)
	}
	if !idle {
		return nil, g.progress(), true, errors.New(HoldInventoryRevision)
	}
	g.revision = fresh
	g.next = InventoryCollector{Revision: fresh, Local: g.local, Restarts: g.next.Restarts + 1}
	g.evidence = 0
	return nil, nil, false, nil
}

// confirmSeal confirms one complete prefix and seals it, restarting
// or resetting honestly when confirmation fails.
func (g *inventoryGather) confirmSeal() (*SealedInventory, *InventoryCollector, bool, error) {
	confirmed, idle, err := g.observeConfirm()
	// A complete prefix is never held for resume: resuming
	// it would re-append the final page. Non-seal exits
	// below restart or reset honestly instead.
	if err != nil && err.Error() == HoldInventoryInProgress {
		return nil, &InventoryCollector{Revision: g.revision, Local: g.local, Restarts: g.next.Restarts + 1}, true, err
	}
	if err != nil || !idle || confirmed != g.revision {
		return g.resetUnconfirmed(confirmed, idle, err)
	}
	return g.sealInventory()
}

// resetUnconfirmed restarts one unconfirmed prefix, reserving the
// replacement confirmation before fetching another page.
func (g *inventoryGather) resetUnconfirmed(confirmed int64, idle bool, err error) (*SealedInventory, *InventoryCollector, bool, error) {
	if g.next.Restarts >= maxCollectorRestarts {
		return nil, g.progress(), true, errors.New(HoldInventoryRevision)
	}
	// The held confirmation is spent (or the call
	// failed). Reserve its replacement BEFORE fetching
	// another page; without it, return a valid reset
	// hold — a moved revision invalidates the old-rev
	// prefix — rather than an unconfirmable prefix.
	fresh := g.revision
	if err == nil && idle && confirmed != 0 && confirmed != g.revision {
		fresh = confirmed
	}
	if g.budget != nil && !g.budget.reserveNativeRPC(1) {
		return nil, &InventoryCollector{Revision: fresh, Local: g.local, Restarts: g.next.Restarts + 1}, true, errors.New(HoldInventoryInProgress)
	}
	g.confirmReserved = true
	g.revision = fresh
	g.next = InventoryCollector{Revision: fresh, Local: g.local, Restarts: g.next.Restarts + 1}
	g.evidence = 0
	return nil, nil, false, nil
}

// sealInventory parses and seals one confirmed prefix into its
// substance: a non-canonical body refuses the seal instead of
// persisting bytes the decision cannot assess.
func (g *inventoryGather) sealInventory() (*SealedInventory, *InventoryCollector, bool, error) {
	indexes := make([]int64, 0, len(g.next.Issues))
	for _, issue := range g.next.Issues {
		indexes = append(indexes, issue.Index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	byIndex := make(map[int64]factory.CompletionIssueEvidence, len(g.next.Issues))
	for _, issue := range g.next.Issues {
		body, err := factory.ParseCompletionIssueEvidence(issue.Data)
		if err != nil {
			return nil, g.progress(), true, errors.New(HoldInventoryPage)
		}
		if body.Index != issue.Index {
			return nil, g.progress(), true, errors.New(HoldInventoryPage)
		}
		byIndex[issue.Index] = body
	}
	bodies := make([]factory.CompletionIssueEvidence, 0, len(indexes))
	for _, index := range indexes {
		bodies = append(bodies, byIndex[index])
	}
	// The seal supersedes progress: resuming from a sealed
	// collector would re-observe a closed evidence set.
	return &SealedInventory{
		Local: g.next.Local, Indexes: indexes, Bodies: bodies,
		Digest:     sealInventoryDigest(g.repository, g.generation, g.revision, g.next.Running, indexes),
		Repository: g.repository, Generation: g.generation, Revision: g.revision,
		Issues: len(g.next.Issues), Evidence: g.evidence,
	}, nil, true, nil
}
