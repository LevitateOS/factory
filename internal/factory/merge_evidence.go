package factory

import (
	"errors"
)

// VerifyMergeCheckEvidence enforces the ST11 read path: the latest
// persisted assessment must pass on this exact head and verified base
// under exactly the current adopted definitions. A pass never survives
// a changed head, base or policy without fresh assessment.
func VerifyMergeCheckEvidence(w MergeWork, a CheckAssessment, policy RepositoryPolicy) error {
	if err := w.ValidateTarget(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return errors.New("merge check evidence is not a recorded assessment")
	}
	if policy.Repository != w.Repository {
		return errors.New("merge policy differs from its repository")
	}
	if a.Repository != w.Repository || a.PRNumber != w.PRNumber {
		return errors.New("merge check evidence differs from its PR")
	}
	if a.Verdict != CheckPass {
		return errors.New("merge check evidence does not pass")
	}
	if a.HeadOID != w.HeadOID || a.BaseOID != w.BaseOID {
		return errors.New("merge check evidence is stale")
	}
	if a.PolicyRevision != policy.Revision || a.ChecksDigest != ChecksDigest(policy.Checks) {
		return errors.New("merge check evidence predates its adopted definitions")
	}
	if w.AssessmentRevision != 0 && a.Revision != w.AssessmentRevision {
		return errors.New("merge check evidence differs from its bound assessment")
	}
	return nil
}

// MergeObservation is one executor observation: the bracketed idle
// native revision plus the exact PR linkage, tips, consumed approval
// and check evidence behind it. PRAuthorID and ReviewerID are canonical;
// PR/issue/review IDs remain provider subjects.
type MergeObservation struct {
	Checks       ObservedChecks
	NativeRev    int64
	ObservedUnix int64
	PRID         int64
	PRNumber     int64
	IssueID      int64
	PRAuthorID   int64
	HeadRef      string
	BaseRef      string
	HeadOID      string
	BaseOID      string
	ReviewerID   int64
	ReviewID     int64
}

// MergeConfirmation is one observed native completion: the realized
// merge commit and stamps plus the exact base tip and issue closure
// behind them. A committed ref without this confirmation never finishes
// the bookkeeping. MergerID is canonical; closure occurrence and commit facts
// retain their provider identities.
type MergeConfirmation struct {
	MergedCommit              string
	BaseTip                   string
	BaseContainsMergedCommit  bool
	MergerID                  int64
	MergedUnix                int64
	ClosedUnix                int64
	NativeRev                 int64
	ObservedUnix              int64
	IssueClosed               bool
	OriginalClosed            bool
	OriginalClosingOccurrence string
	OriginalClosedUnix        int64
}

// MergeOutcome is one reconciled native merge: the operation state plus
// the exact PR identity and realized commit adopted from its own
// committed receipt.
type MergeOutcome struct {
	Operation    OperationOutcome
	HeadRef      string
	BaseRef      string
	HeadOID      string
	BaseOID      string
	MergedCommit string
	PRNumber     int64
	PRID         int64
	IssueID      int64
	ActorID      int64
}
