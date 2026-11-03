package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/levitateos/sodaos/internal/factory"
)

func currentReviewTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, out *factory.CurrentContext) error {
	if a.ReviewContext == nil {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "required_review_context_unavailable")
		return nil
	}
	var owner factory.Assignment
	if err := contextJSONRow(ctx, tx, &owner, `SELECT data FROM factory_assignments WHERE id=$1`, a.PublicationAssignment); err != nil {
		return err
	}
	// A publication link grants only this registered review/correction
	// context; it does not grant the owner's prompt or submissions.
	if a.ActorID <= 0 || owner.ActorID != a.ActorID || owner.Repository != a.Repository || owner.ProjectID != a.ProjectID || owner.AttemptRoot != a.AttemptRoot || owner.Acceptance != a.Acceptance {
		return errors.New("review publication is outside current context grant")
	}
	var p factory.Publication
	if err := contextJSONRow(ctx, tx, &p, `SELECT data FROM factory_publications WHERE assignment=$1 AND repository=$2 AND issue=$3`, a.PublicationAssignment, a.Repository, a.Issue); err != nil {
		return err
	}
	if p.ID != a.ReviewContext.Publication.ID || p.ProjectID != a.ProjectID || p.Acceptance != a.Acceptance || p.AssignmentID != owner.ID {
		return errors.New("review publication differs from assigned cycle")
	}
	found := false
	for _, cycle := range p.ReviewCycles {
		if cycle.ID != a.ReviewContext.Cycle.ID {
			continue
		}
		found = true
	}
	if !found {
		return errors.New("review context cycle is unavailable")
	}
	tuple := factory.CandidateTuple{Head: p.Candidate, Base: p.BaseSHA, DiffBase: p.CurrentDiffBase(), Revision: p.NativeRev}
	if p.Tuple != nil {
		tuple = *p.Tuple
	}
	current := *a.ReviewContext
	current.Assessments = []factory.CurrentFindingAssessment{}
	current.Corrections = []factory.CurrentCorrectionFact{}
	current.Heads = []factory.CurrentReviewHead{}
	current.CurrentTuple = &tuple
	current.Publication = factory.CurrentPublicationContext{ID: p.ID, AssignmentID: p.AssignmentID, Revision: p.Revision, PRID: p.PRID, PRNumber: p.PRNumber, Stage: p.Stage}
	if err := currentReviewFactsTx(ctx, tx, a, p, &current); err != nil {
		return err
	}
	content, err := factory.FindingContentDigest(current.CarriedFindings)
	if err != nil {
		return err
	}
	current.ClearCoverageDigest, err = factory.ClearCoverageDigest(current.Cycle.Head, current.Cycle.BaseOID, content)
	if err != nil {
		return err
	}
	if err := currentPublishedImplementationTx(ctx, tx, a, owner, p, &current); err != nil {
		return err
	}
	var assessment factory.CheckAssessment
	err = contextJSONRow(ctx, tx, &assessment, `SELECT data FROM factory_check_assessments WHERE repository=$1 AND pr=$2`, p.Repository, p.PRNumber)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var policy factory.RepositoryPolicy
		if err := contextJSONRow(ctx, tx, &policy, `SELECT data FROM factory_policies WHERE repository=$1`, p.Repository); err != nil {
			return err
		}
		if assessment.Repository == p.Repository && assessment.PRNumber == p.PRNumber && assessment.PRID == p.PRID && assessment.IssueID == p.PRCreate.IssueID && assessment.HeadOID == current.AdmittedTuple.Head && assessment.HeadOID == p.Candidate && assessment.BaseOID == p.PRCreate.BaseOID && assessment.HeadRef == p.PRCreate.HeadRef && assessment.BaseRef == p.PRCreate.BaseRef && assessment.PolicyRevision == policy.Revision && slices.Equal(assessment.Checks, policy.Checks) && assessment.ChecksDigest == factory.ChecksDigest(policy.Checks) && (assessment.Verdict == factory.CheckPass || assessment.Verdict == factory.CheckFailed) {
			current.CheckAssessment = &assessment
		}
	}
	out.Role.Review = &current
	if tuple.Head != current.AdmittedTuple.Head || tuple.Base != current.AdmittedTuple.Base || tuple.DiffBase != current.AdmittedTuple.DiffBase {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "review_candidate_inputs_changed")
	}
	return nil
}
