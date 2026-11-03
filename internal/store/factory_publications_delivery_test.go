package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

func deliveryStoreAcceptance(publicationID, attemptRoot string, round int, kind, decision string, intents []string) factory.DeliveryAcceptance {
	head, base, diffBase := strings.Repeat("2", 40), strings.Repeat("1", 40), strings.Repeat("3", 40)
	return factory.DeliveryAcceptance{
		Tuple:         factory.CandidateTuple{Head: head, Base: base, DiffBase: diffBase},
		ID:            factory.DeliveryAcceptanceID(publicationID, attemptRoot, round, kind),
		PublicationID: publicationID, AttemptRoot: attemptRoot,
		ScopeDigest: strings.Repeat("a", 64), FindingDigest: strings.Repeat("b", 64),
		CoverageDigest: strings.Repeat("c", 64), CheckDigest: strings.Repeat("d", 64),
		PolicyRevision:    "policy-revision-9",
		DecisionID:        decision,
		OriginReceipt:     strings.Repeat("4", 32),
		ResolutionIntents: intents,
		Kind:              kind, Round: round,
	}
}

func deliveryStorePublication() factory.Publication {
	return factory.Publication{
		Authority:    factory.AuthorityRef{},
		ID:           strings.Repeat("7", 32),
		AssignmentID: strings.Repeat("8", 32),
		ProjectID:    "p" + strings.Repeat("d", 24),
		Role:         project.RoleCoder,
		Acceptance:   "d" + strings.Repeat("a", 24),
		Candidate:    strings.Repeat("2", 40),
		BaseSHA:      strings.Repeat("1", 40),
		TargetBranch: "main",
		Stage:        factory.PublicationOpen,
		Repository:   7,
		Issue:        3,
		Revision:     1,
		CreatedUnix:  1,
	}
}

func TestPublicationDeliveryAcceptancesAppendOnly(t *testing.T) {
	publicationID, attemptRoot := strings.Repeat("7", 32), strings.Repeat("b", 32)
	clean := deliveryStoreAcceptance(publicationID, attemptRoot, 1, factory.AcceptanceCleanReview, "", nil)
	if err := clean.Validate(); err != nil {
		t.Fatalf("clean acceptance fixture refused: %v", err)
	}
	old := deliveryStorePublication()
	next := deliveryStorePublication()
	next.Revision = old.Revision + 1
	next.DeliveryAcceptances = []factory.DeliveryAcceptance{clean}
	requireAttemptFidelity(t, old, next)
	if err := publicationUpdateAllowed(old, next); err != nil {
		t.Fatalf("appending a delivery acceptance refused: %v", err)
	}
	jev := deliveryStoreAcceptance(publicationID, attemptRoot, 2, factory.AcceptanceJevAcceptance, "decision-1", []string{strings.Repeat("6", 32)})
	if err := jev.Validate(); err != nil {
		t.Fatalf("funded acceptance fixture refused: %v", err)
	}
	third := deliveryStorePublication()
	third.Revision = next.Revision + 1
	third.DeliveryAcceptances = []factory.DeliveryAcceptance{clean, jev}
	requireAttemptFidelity(t, next, third)
	if err := publicationUpdateAllowed(next, third); err != nil {
		t.Fatalf("appending the funded variant refused: %v", err)
	}
	rewritten := deliveryStorePublication()
	rewritten.Revision = next.Revision + 1
	requireAttemptFidelity(t, next, rewritten)
	mutated := clean
	mutated.FindingDigest = strings.Repeat("e", 64)
	rewritten.DeliveryAcceptances = []factory.DeliveryAcceptance{mutated}
	if err := publicationUpdateAllowed(next, rewritten); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("rewritten finding digest allowed: %v", err)
	}
	dropped := deliveryStorePublication()
	dropped.Revision = next.Revision + 1
	requireAttemptFidelity(t, next, dropped)
	if err := publicationUpdateAllowed(next, dropped); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("dropped acceptance allowed: %v", err)
	}
	swapped := deliveryStorePublication()
	swapped.Revision = third.Revision + 1
	requireAttemptFidelity(t, third, swapped)
	changed := jev
	changed.ResolutionIntents = []string{strings.Repeat("5", 32)}
	swapped.DeliveryAcceptances = []factory.DeliveryAcceptance{clean, changed}
	if err := publicationUpdateAllowed(third, swapped); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("rewritten resolution intent allowed: %v", err)
	}
}

// The current-tuple guard pins the publication Tuple lifecycle inside the
// actual publicationUpdateAllowed path. Fixtures build on the existing
// model-valid publicationTestRecord constructor (same package) so every
// next packet below passes Publication.Validate; each refusal therefore
// comes from the tuple transition guard, never from a malformed fixture.
// Head is the record Candidate, B0 the immutable admitted BaseSHA, B1 a
// newer current native base, M0/M1 observed merge-bases. UNRUN: cases are
// written for root review, not executed in this lane.
func tupleGuardHead() string { return strings.Repeat("2", 40) }

func tupleGuardAdmittedBase() string { return strings.Repeat("1", 40) }

func tupleGuardOld(t *testing.T) factory.Publication {
	t.Helper()
	old := publicationTestRecord()
	if err := old.Validate(); err != nil {
		t.Fatalf("tuple guard base fixture refused: %v", err)
	}
	return old
}

func requireAttemptFidelity(t *testing.T, old, next factory.Publication) {
	t.Helper()
	if next.Revision != old.Revision+1 {
		t.Fatalf("attempt lacks production revision fidelity: old revision %d, next revision %d", old.Revision, next.Revision)
	}
}

func tupleGuardNext(t *testing.T, old factory.Publication, tuple *factory.CandidateTuple) factory.Publication {
	t.Helper()
	next := old
	next.Tuple = tuple
	next.Revision = old.Revision + 1
	if err := next.Validate(); err != nil {
		t.Fatalf("tuple guard next fixture refused: %v", err)
	}
	requireAttemptFidelity(t, old, next)
	return next
}

func TestPublicationTupleGuardNilNilLegacyPermitted(t *testing.T) {
	old := tupleGuardOld(t)
	next := old
	next.Revision = old.Revision + 1
	requireAttemptFidelity(t, old, next)
	next.NativeRev, next.ObservedUnix = 9, 1150
	if err := next.Validate(); err != nil {
		t.Fatalf("nondependent next fixture refused: %v", err)
	}
	if err := publicationUpdateAllowed(old, next); err != nil {
		t.Fatalf("nil-nil nondependent update refused: %v", err)
	}
}

func TestPublicationTupleGuardInitRev0Allowed(t *testing.T) {
	old := tupleGuardOld(t)
	next := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 0,
	})
	if err := publicationUpdateAllowed(old, next); err != nil {
		t.Fatalf("rev0 tuple initialization refused: %v", err)
	}
}

func TestPublicationTupleGuardInitRevNonzeroRefused(t *testing.T) {
	old := tupleGuardOld(t)
	next := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 1,
	})
	if err := publicationUpdateAllowed(old, next); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("nonzero-revision tuple initialization allowed: %v", err)
	}
}

func TestPublicationTupleGuardDropRefused(t *testing.T) {
	old := tupleGuardOld(t)
	withTuple := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 0,
	})
	dropped := withTuple
	dropped.Tuple = nil
	dropped.Revision = withTuple.Revision + 1
	requireAttemptFidelity(t, withTuple, dropped)
	if err := dropped.Validate(); err != nil {
		t.Fatalf("dropped-tuple next fixture refused: %v", err)
	}
	if err := publicationUpdateAllowed(withTuple, dropped); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("dropped current tuple allowed: %v", err)
	}
}

func TestPublicationTupleGuardRewindRefused(t *testing.T) {
	old := tupleGuardOld(t)
	advanced := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 1,
	})
	rewound := advanced
	rewound.Revision = advanced.Revision + 1
	requireAttemptFidelity(t, advanced, rewound)
	rewound.Tuple = &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 0,
	}
	if err := rewound.Validate(); err != nil {
		t.Fatalf("rewound next fixture refused: %v", err)
	}
	if err := publicationUpdateAllowed(advanced, rewound); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("tuple revision rewind allowed: %v", err)
	}
}

func TestPublicationTupleGuardSameRevisionTripleEditRefused(t *testing.T) {
	old := tupleGuardOld(t)
	before := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 0,
	})
	edited := before
	edited.Revision = before.Revision + 1
	requireAttemptFidelity(t, before, edited)
	edited.Tuple = &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("5", 40), Revision: 0,
	}
	if err := edited.Validate(); err != nil {
		t.Fatalf("same-revision edit fixture refused: %v", err)
	}
	if err := publicationUpdateAllowed(before, edited); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("same-revision DiffBase replacement allowed: %v", err)
	}
}

func TestPublicationTupleGuardRevisionJumpRefused(t *testing.T) {
	old := tupleGuardOld(t)
	before := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 0,
	})
	jumped := before
	jumped.Revision = before.Revision + 1
	requireAttemptFidelity(t, before, jumped)
	jumped.Tuple = &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 2,
	}
	if err := jumped.Validate(); err != nil {
		t.Fatalf("jumped-revision fixture refused: %v", err)
	}
	if err := publicationUpdateAllowed(before, jumped); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("tuple revision jump allowed: %v", err)
	}
}

func TestPublicationTupleGuardCurrentBaseAdvanceAllowed(t *testing.T) {
	old := tupleGuardOld(t)
	before := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 0,
	})
	advanced := before
	advanced.Revision = before.Revision + 1
	requireAttemptFidelity(t, before, advanced)
	advanced.Tuple = &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: strings.Repeat("4", 40),
		DiffBase: strings.Repeat("5", 40), Revision: 1,
	}
	// The independent current-base advance stays model-valid even though
	// Tuple.Base no longer equals the immutable admitted BaseSHA.
	if err := advanced.Validate(); err != nil {
		t.Fatalf("current-base advance fixture refused: %v", err)
	}
	if advanced.BaseSHA != tupleGuardAdmittedBase() {
		t.Fatalf("advance changed the immutable admitted base: %q", advanced.BaseSHA)
	}
	if advanced.PRCreate != before.PRCreate {
		t.Fatalf("advance rewrote PRCreate history: %+v", advanced.PRCreate)
	}
	if err := publicationUpdateAllowed(before, advanced); err != nil {
		t.Fatalf("valid current-base advance refused: %v", err)
	}
}

func TestPublicationTupleGuardEqualValueNewPointerAllowed(t *testing.T) {
	old := tupleGuardOld(t)
	before := tupleGuardNext(t, old, &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: tupleGuardAdmittedBase(),
		DiffBase: strings.Repeat("3", 40), Revision: 1,
	})
	same := before
	same.Revision = before.Revision + 1
	requireAttemptFidelity(t, before, same)
	copied := *before.Tuple
	same.Tuple = &copied
	if same.Tuple == before.Tuple {
		t.Fatal("equal-value case reuses the same pointer")
	}
	if err := publicationUpdateAllowed(before, same); err != nil {
		t.Fatalf("equal-value tuple under a new pointer refused: %v", err)
	}
}

func TestPublicationTupleModelKeepsHeadBinding(t *testing.T) {
	p := publicationTestRecord()
	p.Tuple = &factory.CandidateTuple{
		Head: tupleGuardHead(), Base: strings.Repeat("4", 40),
		DiffBase: strings.Repeat("5", 40), Revision: 0,
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("tuple with independent current base refused: %v", err)
	}
	moved := *p.Tuple
	moved.Head = strings.Repeat("3", 40)
	p.Tuple = &moved
	if err := p.Validate(); err == nil {
		t.Fatal("tuple head outside the publication candidate accepted")
	}
}
