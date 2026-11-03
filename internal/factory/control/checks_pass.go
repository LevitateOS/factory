package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// CheckObserver brackets one exact head/base and returns the observed
// check evidence. The production check assessor satisfies it; while no
// assessor is supplied, assessment waits instead of guessing.
type CheckObserver interface {
	ObserveChecks(ctx context.Context, target factory.CheckTarget, actorID int64) (factory.ObservedChecks, error)
}

// CheckLink is one assessed publication: its exact head/base, the verdict
// and the stored assessment revision the merge consumes.
type CheckLink struct {
	AssignmentID  string `json:"assignment_id"`
	PublicationID string `json:"publication_id"`
	HeadOID       string `json:"head_oid"`
	BaseOID       string `json:"base_oid"`
	Verdict       string `json:"verdict"`
	Revision      int64  `json:"revision"`
}

// CheckWait is one publication that did not assess this pass and why.
type CheckWait struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// CheckError is one unexpected assessment infrastructure failure. The
// affected publication keeps its recorded state; a later pass retries.
type CheckError struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// CheckReport is the durable outcome of one assessment sweep: the
// publications it assessed and every publication that waited or errored.
type CheckReport struct {
	Assessed    []CheckLink  `json:"assessed"`
	Waits       []CheckWait  `json:"waits,omitempty"`
	Errors      []CheckError `json:"errors,omitempty"`
	Unavailable bool         `json:"unavailable,omitempty"`
}

func checkError(report *CheckReport, id, reason string) {
	report.Errors = append(report.Errors, CheckError{ID: id, Reason: reason})
}

func checkWait(report *CheckReport, id, reason string) {
	report.Waits = append(report.Waits, CheckWait{ID: id, Reason: reason})
}

// AssessPublicationChecks records one ST11 verdict per named published
// publication: it observes the exact current head and verified base
// through the supplied assessor, evaluates the adopted required-check
// set and stores the verdict for the merge to consume. Every semantic
// outcome records, including refusals and failures; only unreadable
// observations wait or error. Publications that never published, or
// whose merge already finished, are left alone.
func (c *Coordinator) AssessPublicationChecks(ctx context.Context, assessor CheckObserver, actorID int64, assignmentIDs ...string) CheckReport {
	report := CheckReport{Assessed: []CheckLink{}}
	if assessor == nil {
		report.Unavailable = true
		return report
	}
	for _, id := range assignmentIDs {
		c.assessPublicationChecksOne(ctx, assessor, actorID, id, &report)
	}
	return report
}

func (c *Coordinator) assessPublicationChecksOne(ctx context.Context, assessor CheckObserver, actorID int64, assignmentID string, report *CheckReport) {
	p, err := c.Store.PublicationByAssignment(ctx, assignmentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			checkWait(report, assignmentID, "publication_missing")
		} else {
			checkError(report, assignmentID, "store_unavailable")
		}
		return
	}
	if p.Stage != factory.PublicationPublished || p.PRNumber <= 0 || p.PRID <= 0 || p.PRCreate.Work == nil {
		checkWait(report, p.ID, "publication_unpublished")
		return
	}
	if m, err := c.Store.MergeByPublication(ctx, p.ID); err == nil {
		if m.Stage == factory.MergeMerged {
			return
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		checkError(report, p.ID, "store_unavailable")
		return
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		checkError(report, p.ID, "store_unavailable")
		return
	}
	target := factory.CheckTarget{
		Repository: p.Repository, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PRCreate.IssueID,
		HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef,
		HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID,
	}
	adopted := factory.AdoptedChecks{
		Checks:         append([]string(nil), policy.Checks...),
		PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks),
	}
	observed, err := assessor.ObserveChecks(ctx, target, actorID)
	if err != nil {
		var wait *factory.PublicationWait
		var refused *factory.PublicationRefusal
		switch {
		case errors.As(err, &wait):
			checkWait(report, p.ID, wait.Reason)
		case errors.As(err, &refused):
			checkError(report, p.ID, refused.Reason)
		default:
			checkError(report, p.ID, "checks_unavailable")
		}
		return
	}
	assessment, err := factory.VerifyChecks(target, adopted, policy, observed, time.Now().Unix())
	if err != nil {
		checkError(report, p.ID, "assessment_invalid")
		return
	}
	stored, err := c.Store.RecordCheckAssessment(ctx, assessment)
	if err != nil {
		checkError(report, p.ID, "store_unavailable")
		return
	}
	report.Assessed = append(report.Assessed, CheckLink{
		AssignmentID: p.AssignmentID, PublicationID: p.ID,
		HeadOID: stored.HeadOID, BaseOID: stored.BaseOID,
		Verdict: stored.Verdict, Revision: stored.Revision,
	})
}
