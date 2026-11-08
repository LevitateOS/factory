package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

func currentReviewSourceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, id, run, phase string) (factory.Assignment, error) {
	var source factory.Assignment
	err := contextJSONRow(ctx, tx, &source, `SELECT data FROM factory_assignments WHERE id=$1`, id)
	if err != nil {
		return source, err
	}
	if source.ID != id || source.ActorID <= 0 || source.ActorID != a.ActorID || source.Repository != a.Repository || source.Issue != a.Issue || source.ProjectID != a.ProjectID || source.AttemptRoot != a.AttemptRoot || source.PublicationAssignment != a.PublicationAssignment || source.Acceptance != a.Acceptance || source.Phase != phase || (run != "" && source.Run != run) {
		return source, errors.New("current review source is outside cycle grant")
	}
	return source, nil
}

func currentReviewFactsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, p factory.Publication, current *factory.CurrentReviewContext) error {
	if len(p.ReviewCycles) > 512 || len(p.CorrectionJournals) > 512 {
		return errors.New("current review sources exceed their finite bound")
	}
	carried := map[string]bool{}
	for _, finding := range current.CarriedFindings {
		carried[finding.Key] = true
	}
	for _, cycle := range p.ReviewCycles {
		if cycle.Round > current.Cycle.Round {
			continue
		}
		if cycle.PublicationID != p.ID || cycle.AttemptRoot != a.AttemptRoot {
			return errors.New("current review source cycle is malformed")
		}
		// Correction rounds have a synthetic reviewer marker; they never
		// grant a nonexistent reviewer assignment or its conversation. Current
		// cycles share the review phase and identify correction by assignment.
		if cycle.Phase != "correct" && cycle.CorrectionAssignment == "" {
			if err := currentReviewAssessmentsTx(ctx, tx, a, p, cycle, carried, current); err != nil {
				return err
			}
		}
		for _, journal := range p.CorrectionJournals {
			if journal.CycleID != cycle.ID {
				continue
			}
			if cycle.CorrectionAssignment == "" {
				return errors.New("current correction source is malformed")
			}
			source, err := currentReviewSourceTx(ctx, tx, a, cycle.CorrectionAssignment, journal.RunID, "correct")
			if err != nil {
				return err
			}
			if !currentReviewResultBound(source) || source.Result.PhaseResult.Output.Correction == nil {
				return errors.New("current correction fact lacks its bound report")
			}
			for _, disposition := range journal.Report.Dispositions {
				if !carried[disposition.Key] {
					continue
				}
				fact := factory.CurrentCorrectionFact{CycleID: cycle.ID, RunID: journal.RunID, SetDigest: journal.Report.SetDigest, NewCandidate: journal.Report.NewCandidate, Key: disposition.Key, Disposition: disposition.Disposition, ChangedPaths: []string{}, Commits: []string{}}
				for _, evidence := range journal.Report.Evidence {
					if evidence.Key == disposition.Key {
						fact.ChangedPaths, fact.Commits = evidence.ChangedPaths, evidence.Commits
					}
				}
				current.Corrections = append(current.Corrections, fact)
			}
		}
	}
	return nil
}

func currentReviewResultBound(source factory.Assignment) bool {
	return source.Stage == factory.AssignmentFinished && source.Outcome == factory.Succeeded && source.Result != nil && source.Result.Reported && source.Result.PhaseResult != nil && source.Result.PhaseResult.Output != nil
}

func currentReviewAssessmentsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, p factory.Publication, cycle factory.ReviewCycle, carried map[string]bool, current *factory.CurrentReviewContext) error {
	source, err := currentReviewSourceTx(ctx, tx, a, cycle.ReviewerAssignment, "", "review")
	if err != nil {
		return err
	}
	if source.SourceCommit != cycle.Head {
		return errors.New("current review source differs from cycle candidate")
	}
	if currentReviewResultBound(source) {
		var assessments []factory.FindingAssessment
		output := source.Result.PhaseResult.Output
		if output.Review != nil {
			if output.Review.CycleID != cycle.ID {
				return errors.New("current review assessment differs from cycle")
			}
			assessments = output.Review.Assessments
		}
		if output.Clear != nil {
			if output.Clear.CycleID != cycle.ID || output.Clear.Head != cycle.Head || output.Clear.BaseOID != cycle.BaseOID {
				return errors.New("current clear assessment differs from cycle")
			}
			assessments = output.Clear.Assessments
		}
		for _, assessment := range assessments {
			if carried[assessment.Key] {
				current.Assessments = append(current.Assessments, factory.CurrentFindingAssessment{CycleID: cycle.ID, Key: assessment.Key, Status: assessment.Status, Evidence: assessment.Evidence})
			}
		}
	}
	for _, operation := range p.ReviewOperations {
		if operation.RunID != source.Run {
			continue
		}
		outcome := operation.Outcome
		current.Heads = append(current.Heads, factory.CurrentReviewHead{CycleID: cycle.ID, RunID: source.Run, OperationID: operation.Work.OperationID, HeadOID: operation.Work.HeadOID, BaseOID: operation.Work.BaseOID, Event: operation.Work.Event, Operation: factory.CurrentReviewOperation{Effect: outcome.Effect, Cancellation: outcome.Cancellation, Completion: outcome.Completion, Reason: outcome.Reason, NotObserved: outcome.NotObserved}})
	}
	return nil
}
