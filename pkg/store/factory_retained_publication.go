package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"github.com/levitateos/factory/pkg/project"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

var ErrRetainedPublicationIneligible = errors.New("retained candidate publication is no longer eligible")

type reviewRetryCapture struct {
	Retry factory.ReviewRetry `json:"retry"`
	Work factory.ReviewWork `json:"work"`
}

// ReviewRetryOffer contains only an existing terminal no-send and current policy.
// Reading this offer creates neither send authority nor inference funding.
func (s *Store) ReviewRetryOffer(ctx context.Context, publication string) (factory.ReviewRetry, error) {
	p, err := s.PublicationByID(ctx, publication)
	if err != nil { return factory.ReviewRetry{}, err }
	policy, err := s.RepositoryPolicy(ctx, p.Repository)
	if err != nil { return factory.ReviewRetry{}, err }
	if p.Stage != factory.PublicationPublished || p.WithdrawRequested || len(p.ReviewOperations) == 0 ||
		!policy.Enabled || policy.Paused || policy.TargetBranch != p.TargetBranch {
		return factory.ReviewRetry{}, ErrRetainedPublicationIneligible
	}
	op := p.ReviewOperations[len(p.ReviewOperations)-1]
	if !op.DefinitiveNoSend() || op.Work.HeadOID != p.Candidate || op.Work.BaseOID != p.PullTarget().BaseOID || policy.Review.ActorID != op.Work.ActorID {
		return factory.ReviewRetry{}, ErrRetainedPublicationIneligible
	}
	r := factory.ReviewRetry{Publication:p.ID, Run:op.RunID, Operation:op.Work.OperationID, Head:p.Candidate, Base:p.PullTarget().BaseOID, Policy:policy.Revision}
	return r, r.Validate()
}

func admitReviewRetryRegistrationTx(ctx context.Context, tx *sql.Tx, previous, p factory.Publication, op factory.ReviewOperation) error {
	if previous.Stage != factory.PublicationPublished || p.Stage != factory.PublicationPublished || len(previous.ReviewOperations) == 0 {
		return ErrPublicationConflict
	}
	prior := previous.ReviewOperations[len(previous.ReviewOperations)-1]
	if prior.RunID != op.RunID || prior.Work.OperationID != op.RetryOf || !prior.DefinitiveNoSend() ||
		op.Work.HeadOID != p.Candidate || op.Work.BaseOID != p.PullTarget().BaseOID {
		return ErrPublicationConflict
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM factory_commands WHERE id=$1 AND type=$2 AND target=$3 AND finished<>''`,
		op.RetryCommand, factory.CommandRetryReview, "publication/"+p.ID).Scan(&raw); err != nil { return err }
	var capture reviewRetryCapture
	if json.Unmarshal(raw, &capture) != nil || capture.Retry.Validate() != nil || !factory.ReviewWorkEqual(capture.Work, op.Work) ||
		capture.Retry.Publication != p.ID || capture.Retry.Run != op.RunID || capture.Retry.Operation != prior.Work.OperationID ||
		capture.Retry.Head != p.Candidate || capture.Retry.Base != p.PullTarget().BaseOID ||
		op.Work.NotAfter <= time.Now().Unix() {
		return ErrAdmissionChanged
	}
	var policy factory.RepositoryPolicy
	if err := loadGrantDocumentTx(ctx, tx, &policy, `SELECT data FROM factory_policies WHERE repository=$1 FOR UPDATE`, p.Repository); err != nil { return err }
	if !policy.Enabled || policy.Paused || policy.Revision != capture.Retry.Policy || policy.TargetBranch != p.TargetBranch || policy.Review.ActorID != op.Work.ActorID {
		return ErrAdmissionChanged
	}
	var root string
	if err := tx.QueryRowContext(ctx, `SELECT data->>'attempt_root' FROM factory_assignments WHERE id=$1`, p.AssignmentID).Scan(&root); err != nil { return err }
	settled, err := attemptRootRunsSettledTx(ctx, tx, p.Repository, p.Issue, root)
	if err != nil { return err }
	if !settled { return ErrAttemptRunsPending }
	return nil
}

// RecordReviewRetry freezes the fresh five-minute submission and its command in
// the same publication transaction. Old work, outcomes and allowances stay exact.
func (s *Store) RecordReviewRetry(ctx context.Context, command, principal string, r factory.ReviewRetry, w factory.ReviewWork, now time.Time) (factory.Publication, error) {
	var empty factory.Publication
	if r.Validate() != nil || !factory.ValidID(command) || w.Validate() != nil ||
		w.OperationID != "review-retry-"+command || w.NotAfter <= now.Unix() || w.NotAfter > now.Add(5*time.Minute).Unix() {
		return empty, ErrRetainedPublicationIneligible
	}
	payload, err := json.Marshal(reviewRetryCapture{Retry:r, Work:w})
	if err != nil { return empty, err }
	cmd := factory.Command{ID:command, Type:factory.CommandRetryReview, Target:"publication/"+r.Publication, Principal:principal, Payload:string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return empty, err }
	defer tx.Rollback()
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil { return empty, err }
	if stored.Principal != principal || !created { return empty, ErrCommandConflict }
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE data->>'id'=$1 FOR UPDATE`, r.Publication).Scan(&raw); err != nil { return empty, err }
	var p factory.Publication
	if json.Unmarshal(raw, &p) != nil || p.Validate() != nil { return empty, ErrPublicationConflict }
	op := factory.ReviewOperation{RunID:r.Run, RetryOf:r.Operation, RetryCommand:command, Work:w}
	if err = p.CanAppendReviewOperation(op); err != nil { return empty, err }
	if err = finishFactoryCommandTx(ctx, tx, command, now); err != nil { return empty, err }
	p.ReviewOperations = append(p.ReviewOperations, op)
	p.Revision++
	if err = updatePublicationTx(ctx, tx, p); err != nil { return empty, err }
	if err = tx.Commit(); err != nil { return empty, err }
	return p, nil
}

// RecordRetainedPublicationCommand creates only a publication owner and its
// branch/PR journal. Original executions, uncertain effects and allowances are
// immutable. The command ID is the new owner and publication identity.
func (s *Store) RecordRetainedPublicationCommand(ctx context.Context, cmd factory.Command, a factory.Assignment, p factory.Publication, now time.Time) (factory.Publication, error) {
	if cmd.Validate() != nil || cmd.Type != factory.CommandPublishRetained || a.Validate() != nil ||
		a.RetainedPublication == nil || cmd.ID != a.ID || p.Validate() != nil || p.ID != cmd.ID || p.AssignmentID != a.ID ||
		p.Stage != factory.PublicationOpen || p.Revision != 0 || p.Publish.Attempts != 0 || p.PRCreate.Attempts != 0 ||
		p.Run != a.RetainedPublication.Run || p.Candidate != a.RetainedPublication.Candidate ||
		cmd.Target != "publication/"+a.RetainedPublication.Publication {
		return factory.Publication{}, fmt.Errorf("%w: invalid publication command or non-executing owner", ErrRetainedPublicationIneligible)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Publication{}, err
	}
	defer tx.Rollback()
	if err = checkDispatchOpenTx(ctx, tx, a.Repository); err != nil {
		return factory.Publication{}, err
	}
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return factory.Publication{}, err
	}
	if !created {
		if stored.Principal != cmd.Principal || stored.Finished == "" {
			return factory.Publication{}, ErrCommandConflict
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, cmd.ID).Scan(&raw); err != nil {
			return factory.Publication{}, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return factory.Publication{}, err
		}
		return p, p.Validate()
	}
	var original factory.Publication
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1 FOR UPDATE`, a.RetainedPublication.Assignment).Scan(&raw); err != nil {
		return factory.Publication{}, err
	}
	if json.Unmarshal(raw, &original) != nil || original.Validate() != nil || original.ID != a.RetainedPublication.Publication ||
		original.Stage != factory.PublicationFenced || original.WithdrawRequested || original.PRNumber != 0 ||
		original.Run != p.Run || original.Candidate != p.Candidate || original.BaseSHA != p.BaseSHA ||
		original.Repository != p.Repository || original.Issue != p.Issue || original.Acceptance != p.Acceptance ||
		original.ProjectID != p.ProjectID || original.Preparation != p.Preparation || original.TargetBranch != p.TargetBranch ||
		original.Authority.RequirementsID != p.Authority.RequirementsID || original.Authority.ApprovalID != p.Authority.ApprovalID ||
		original.Authority.SponsorshipConnection != p.Authority.SponsorshipConnection {
		return factory.Publication{}, fmt.Errorf("%w: original publication scope changed", ErrRetainedPublicationIneligible)
	}
	var latest string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_assignments WHERE repository=$1 AND issue=$2 AND data->>'publication_assignment'=id ORDER BY seq DESC LIMIT 1`, p.Repository, p.Issue).Scan(&latest); err != nil {
		return factory.Publication{}, err
	}
	if latest != original.AssignmentID {
		return factory.Publication{}, fmt.Errorf("%w: a later publication owner exists", ErrRetainedPublicationIneligible)
	}
	var source factory.Assignment
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, latest).Scan(&raw); err != nil {
		return factory.Publication{}, err
	}
	if json.Unmarshal(raw, &source) != nil || source.Validate() != nil || source.Stage != factory.AssignmentFinished ||
		source.Outcome != factory.Succeeded || source.Result == nil || !source.Result.Reported || source.Result.Status != "completed" ||
		source.Repository != a.Repository || source.Issue != a.Issue || source.ProjectID != a.ProjectID || source.Preparation != a.Preparation ||
		source.Acceptance != a.Acceptance || a.Acceptance != p.Acceptance || source.SourceCommit != a.SourceCommit ||
		a.SourceCommit != p.BaseSHA || a.ProjectID != p.ProjectID || a.Repository != p.Repository || a.Issue != p.Issue ||
		a.Preparation != p.Preparation || !a.Authority.SameGrants(p.Authority) {
		return factory.Publication{}, fmt.Errorf("%w: source execution scope changed", ErrRetainedPublicationIneligible)
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT
	 EXISTS(SELECT 1 FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 AND decision=$3)
	 AND NOT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$1 AND issue=$2 AND decision=$3)
	 AND EXISTS(SELECT 1 FROM factory_assignments WHERE id=$4 AND data->>'connection'=$5)
	 AND EXISTS(SELECT 1 FROM factory_attempt_allowances WHERE root_assignment=$4 AND data->>'closed'='true')
	 AND EXISTS(SELECT 1 FROM factory_runs WHERE id=$6 AND settled AND data->>'reconciled'='true' AND data->>'outcome'='succeeded')
	 AND NOT EXISTS(SELECT 1 FROM factory_runs r JOIN factory_run_views v ON v.run=r.id JOIN factory_assignments x ON x.id=v.attempt
	 WHERE x.data->>'attempt_root'=$4 AND (r.active OR NOT r.settled OR r.data->>'reconciled' IS DISTINCT FROM 'true'
	 OR (NULLIF(r.data->'native','null'::jsonb) IS NOT NULL AND
	 (r.data#>>'{native,retirement}' IS DISTINCT FROM 'retired' OR r.data#>>'{native,sealed}' IS DISTINCT FROM 'true'
	 OR COALESCE(r.data#>>'{native,provenance,custody,state}','') NOT IN ('released','not_acquired')))))`,
		p.Repository, p.Issue, p.Acceptance, original.AssignmentID, a.RetainedPublication.Connection, p.Run).Scan(&eligible)
	if err != nil {
		return factory.Publication{}, err
	}
	if !eligible || a.RetainedPublication.NotAfter <= now.Unix() {
		return factory.Publication{}, fmt.Errorf("%w: original runs or allowance remain unsettled, or admission expired", ErrRetainedPublicationIneligible)
	}
	in, err := factoryAuthorityInput(ctx, tx, a.Repository, a.Role)
	if err != nil {
		return factory.Publication{}, err
	}
	in.PreparationReady = true
	effective := factory.EvaluateAuthority(in)
	current := effective.Authority
	if err = tx.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1`, a.ProjectID).Scan(&current.RequirementsID); err != nil {
		return factory.Publication{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1`, a.ProjectID).Scan(&current.ApprovalID); err != nil {
		return factory.Publication{}, err
	}
	if !effective.Effective || !current.SameGrants(p.Authority) {
		return factory.Publication{}, fmt.Errorf("%w: current grants changed", ErrRetainedPublicationIneligible)
	}
	assignmentRaw, err := json.Marshal(a)
	if err != nil {
		return factory.Publication{}, err
	}
	publicationRaw, err := json.Marshal(p)
	if err != nil {
		return factory.Publication{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'',$4,0,$5)`, a.ID, a.Repository, a.Issue, a.Stage, string(assignmentRaw)); err != nil {
		return factory.Publication{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, a.ID, p.Repository, p.Issue, p.Run, p.Stage, string(publicationRaw)); err != nil {
		return factory.Publication{}, err
	}
	if err = finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
		return factory.Publication{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.Publication{}, err
	}
	return p, nil
}

// RetainedCandidateFundingOffer is a read-only snapshot. Only the explicit
// command below can create an allowance; neither this read nor publication does.
func (s *Store) RetainedCandidateFundingOffer(ctx context.Context, publication string) (factory.RetainedCandidateFunding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.RetainedCandidateFunding{}, err
	}
	defer tx.Rollback()
	p, a, g, err := retainedFundingScopeTx(ctx, tx, publication)
	if err != nil {
		return factory.RetainedCandidateFunding{}, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_attempt_allowances WHERE root_assignment=$1)`, a.ID).Scan(&exists); err != nil {
		return factory.RetainedCandidateFunding{}, err
	}
	previous := ""
	if exists {
		if err = admitFreshRetainedReviewTx(ctx, tx, p, a); err != nil {
			return factory.RetainedCandidateFunding{}, err
		}
		previous = a.ID
	}
	limits := g.policy.AttemptLimits
	limits.ActiveMinutes = min(limits.ActiveMinutes, 15)
	limits.ReviewRounds = min(limits.ReviewRounds, 2)
	limits.CorrectionCycles = min(limits.CorrectionCycles, 1)
	limits.MaxChildren = 1
	f := factory.RetainedCandidateFunding{Native: g.sponsorship.Native, Publication: p.ID, PreviousRoot: previous, Candidate: p.Candidate, Base: p.PullTarget().BaseOID, Acceptance: p.Acceptance, Connection: g.sponsorship.Connection, GrantID: g.sponsorship.GrantID, Generation: g.sponsorship.Generation, Policy: g.policy.Revision, Limits: limits}
	f.Requirements, f.Approval = a.Authority.RequirementsID, a.Authority.ApprovalID
	if exists {
		prior, err := lockAttemptAllowanceRootTx(ctx, tx, p.Repository, p.Issue, a.ID)
		if err != nil {
			return f, err
		}
		var launched bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE data->>'attempt_root'=$1 AND id<>$1 AND data->>'phase'='review')`, a.ID).Scan(&launched); err != nil {
			return f, err
		}
		if launched {
			f.CarryUnused, f.Limits = true, prior.Limits
			if len(prior.Reviews) >= prior.Limits.ReviewRounds || prior.Limits.ActiveMinutes > limits.ActiveMinutes || prior.Limits.ReviewRounds > limits.ReviewRounds || prior.Limits.CorrectionCycles > limits.CorrectionCycles {
				return f, ErrRetainedPublicationIneligible
			}
		}
	}
	return f, f.Validate()
}

func retainedFundingScopeTx(ctx context.Context, tx *sql.Tx, id string) (factory.Publication, factory.Assignment, admissionGrants, error) {
	var p factory.Publication
	var a factory.Assignment
	var grants admissionGrants
	var raw []byte
	fail := func(err error) (factory.Publication, factory.Assignment, admissionGrants, error) {
		return p, a, grants, err
	}
	if !factory.ValidID(id) {
		return fail(ErrRetainedPublicationIneligible)
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE data->>'id'=$1`, id).Scan(&raw); err != nil {
		return fail(err)
	}
	if json.Unmarshal(raw, &p) != nil || p.Validate() != nil || (p.Stage != factory.PublicationPublished && p.Stage != factory.PublicationFenced) || p.WithdrawRequested || p.PRNumber < 1 || p.PullTarget().BaseOID == "" {
		return fail(ErrRetainedPublicationIneligible)
	}
	if err := checkDispatchOpenTx(ctx, tx, p.Repository); err != nil {
		return fail(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1 FOR UPDATE`, p.AssignmentID).Scan(&raw); err != nil {
		return fail(err)
	}
	if json.Unmarshal(raw, &p) != nil || p.Validate() != nil || p.ID != id || (p.Stage != factory.PublicationPublished && p.Stage != factory.PublicationFenced) || p.WithdrawRequested {
		return fail(ErrRetainedPublicationIneligible)
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, p.AssignmentID).Scan(&raw); err != nil {
		return fail(err)
	}
	if json.Unmarshal(raw, &a) != nil || a.Validate() != nil || !a.PublicationOwnerReady() || a.ID != a.AttemptRoot || a.ID != a.PublicationAssignment {
		return fail(fmt.Errorf("%w: publication owner is not a completed root", ErrRetainedPublicationIneligible))
	}
	origin, err := publicationExecutionOwner(ctx, tx, p, a)
	if err != nil {
		return fail(err)
	}
	// Both ordinary and explicitly republished candidates use their original
	// completed execution. Neither path may invent a candidate or a new run.
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE run=$1`, p.Run).Scan(&raw); err != nil {
		return fail(err)
	}
	var candidate factory.Assignment
	if json.Unmarshal(raw, &candidate) != nil || candidate.Validate() != nil || candidate.Role != project.RoleCoder || candidate.AttemptRoot != origin.ID || candidate.PublicationAssignment != origin.ID || candidate.Stage != factory.AssignmentFinished || candidate.Outcome != factory.Succeeded || candidate.Result == nil || !candidate.Result.Reported || candidate.Result.Status != "completed" || candidate.Result.Candidate != p.Candidate || candidate.Repository != p.Repository || candidate.Issue != p.Issue || candidate.ProjectID != p.ProjectID || candidate.Acceptance != p.Acceptance {
		return fail(fmt.Errorf("%w: original candidate report changed", ErrRetainedPublicationIneligible))
	}
	if a.RetainedPublication != nil && (a.RetainedPublication.Run != p.Run || a.RetainedPublication.Candidate != p.Candidate) {
		return fail(ErrRetainedPublicationIneligible)
	}
	if p.Stage == factory.PublicationFenced {
		if err := admitFreshRetainedReviewTx(ctx, tx, p, a); err != nil {
			return fail(err)
		}
	}
	owner, err := currentAttemptOwnerTx(ctx, tx, p.Repository, p.Issue, a.ID)
	if err != nil || owner != a.ID {
		return fail(ErrRetainedPublicationIneligible)
	}
	var closed bool
	if err = tx.QueryRowContext(ctx, `SELECT data->>'closed'='true' FROM factory_attempt_allowances WHERE root_assignment=$1`, origin.ID).Scan(&closed); err != nil {
		return fail(err)
	}
	if !closed {
		return fail(fmt.Errorf("%w: original allowance remains open", ErrRetainedPublicationIneligible))
	}
	var accepted bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 AND decision=$3) AND NOT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$1 AND issue=$2 AND decision=$3)`, p.Repository, p.Issue, p.Acceptance).Scan(&accepted); err != nil {
		return fail(err)
	}
	if !accepted {
		return fail(ErrRetainedPublicationIneligible)
	}
	grants, err = loadAdmissionGrantsTx(ctx, tx, p.Repository, origin.Connection)
	if err != nil {
		return fail(err)
	}
	source := a
	source.Connection = origin.Connection
	current := p.Authority
	current.Policy, current.Operator, current.Capacity = grants.policy.Revision, grants.operator.Revision, grants.capacity.Revision
	current.Sponsorship, current.Environment = grants.sponsorship.Revision, grants.environment.Revision
	if err = tx.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1 FOR UPDATE`, a.ProjectID).Scan(&current.RequirementsID); err != nil {
		return fail(err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1 FOR UPDATE`, a.ProjectID).Scan(&current.ApprovalID); err != nil {
		return fail(err)
	}
	// Only the fresh funding offer captures this setup. Original publication,
	// execution and funding captures retain their prior decisions forever.
	source.Authority = current
	// Retained publication has no execution run/preparation to renew. Its current
	// standing authority, selected source and accepted inputs still must match.
	// The new command captures today's policy and exact source grant; old
	// publication intents and their original authority remain immutable.
	if err = checkAssignmentStandingAuthorityTx(ctx, tx, current, source, grants); err != nil {
		return fail(err)
	}
	if grants.policy.TargetBranch != p.TargetBranch || grants.policy.Roles[project.RoleReviewer].Account != source.Connection || !slices.Contains(grants.sponsorship.Roles, project.RoleReviewer) || grants.policy.Judgments != nil && grants.policy.Judgments.Account != source.Connection {
		return fail(ErrRetainedPublicationIneligible)
	}
	a.Authority = current
	return p, a, grants, nil
}

func (s *Store) FundRetainedCandidate(ctx context.Context, command, principal string, f factory.RetainedCandidateFunding, now time.Time) (factory.RetainedCandidateFundingReceipt, error) {
	empty := factory.RetainedCandidateFundingReceipt{}
	if f.Validate() != nil {
		return empty, ErrRetainedPublicationIneligible
	}
	payload, err := json.Marshal(f)
	if err != nil {
		return empty, err
	}
	cmd := factory.Command{ID: command, Type: factory.CommandFundRetained, Target: "publication/" + f.Publication, Principal: principal, Payload: string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	if err = cmd.Validate(); err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return empty, err
	}
	receipt := factory.RetainedCandidateFundingReceipt{CommandID: command, Funding: f}
	if !created {
		if stored.Type != cmd.Type || stored.Target != cmd.Target || stored.Principal != principal || stored.Finished == "" {
			return empty, ErrCommandConflict
		}
		return receipt, nil
	}
	p, a, g, err := retainedFundingScopeTx(ctx, tx, f.Publication)
	if err != nil {
		return empty, err
	}
	if f.Candidate != p.Candidate || f.Base != p.PullTarget().BaseOID || f.Acceptance != p.Acceptance || !f.MatchesSource(g.sponsorship) || f.Policy != g.policy.Revision {
		return empty, ErrRetainedPublicationIneligible
	}
	if f.Requirements != a.Authority.RequirementsID || f.Approval != a.Authority.ApprovalID {
		return empty, ErrAdmissionChanged
	}
	origin, err := publicationExecutionOwner(ctx, tx, p, a)
	if err != nil {
		return empty, err
	}
	settled, err := attemptRootRunsSettledTx(ctx, tx, p.Repository, p.Issue, origin.ID)
	if err != nil || !settled {
		return empty, ErrAttemptRunsPending
	}
	bounds := g.policy.AttemptLimits
	if !f.CarryUnused {
		bounds.ActiveMinutes = min(bounds.ActiveMinutes, 15)
		bounds.ReviewRounds = min(bounds.ReviewRounds, 2)
		bounds.CorrectionCycles = min(bounds.CorrectionCycles, 1)
		bounds.MaxChildren = 1
	}
	if f.Limits.ActiveMinutes > bounds.ActiveMinutes || f.Limits.ReviewRounds > bounds.ReviewRounds || f.Limits.CorrectionCycles > bounds.CorrectionCycles || f.Limits.MaxChildren > bounds.MaxChildren {
		return empty, ErrRetainedPublicationIneligible
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_attempt_allowances WHERE root_assignment=$1)`, a.ID).Scan(&exists); err != nil {
		return empty, err
	}
	if exists && f.PreviousRoot == "" || !exists && f.PreviousRoot != "" {
		return empty, ErrRetainedPublicationIneligible
	}
	if f.PreviousRoot != "" {
		if f.PreviousRoot != a.ID {
			return empty, ErrRetainedPublicationIneligible
		}
		if err = admitFreshRetainedReviewTx(ctx, tx, p, a); err != nil {
			return empty, err
		}
		var launched bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE data->>'attempt_root'=$1 AND id<>$1 AND data->>'phase'='review')`, a.ID).Scan(&launched); err != nil {
			return empty, err
		}
		if launched != f.CarryUnused {
			return empty, ErrRetainedPublicationIneligible
		}
		p.Authority = a.Authority
		p, a, err = insertFreshRetainedReviewTx(ctx, tx, p, a, command, now)
		if err != nil {
			return empty, err
		}
	}
	// The new root is distinct from the original closed/uncertain attempt. An
	// existing allowance, even closed or exhausted, can never be replenished.
	if f.CarryUnused {
		prior, err := lockAttemptAllowanceRootTx(ctx, tx, p.Repository, p.Issue, f.PreviousRoot)
		if err != nil || prior.Limits != f.Limits || len(prior.Reviews) >= prior.Limits.ReviewRounds {
			return empty, ErrRetainedPublicationIneligible
		}
		prior.RootAssignment, prior.Revision, prior.Closed, prior.Active, prior.CheckpointUnix = a.ID, 0, false, true, now.Unix()
		raw, err := json.Marshal(prior)
		if err != nil {
			return empty, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,0,$4)`, prior.Repository, prior.Issue, prior.RootAssignment, string(raw)); err != nil {
			return empty, err
		}
	} else {
		if err = insertAttemptAllowanceTx(ctx, tx, p.Repository, p.Issue, a.ID, f.Limits, time.Unix(now.Unix()+int64(f.Limits.ActiveMinutes)*60, 0), now); err != nil {
			return empty, err
		}
	}
	if err = finishFactoryCommandTx(ctx, tx, command, now); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return receipt, nil
}

// Unlaunched work can receive explicit fresh funding. An incomplete reviewer
// can carry only unused budget; typed findings and pending sends require their
// own result-driven recovery and never disappear into a fresh owner.
func admitFreshRetainedReviewTx(ctx context.Context, tx *sql.Tx, p factory.Publication, a factory.Assignment) error {
	prior, err := lockAttemptAllowanceRootTx(ctx, tx, p.Repository, p.Issue, a.ID)
	if err != nil {
		return err
	}
	if !prior.Closed || prior.Active || p.Publish.Effect != factory.OpEffectCommitted || p.Publish.Completion != factory.OpCompletionComplete || p.PRCreate.Effect != factory.OpEffectCommitted || p.PRCreate.Completion != factory.OpCompletionComplete || len(p.ReviewOperations)+len(p.Corrections)+len(p.CorrectionJournals)+len(p.DeliveryAcceptances)+len(p.ResolutionOperations) != 0 {
		return fmt.Errorf("%w: prior allowance or publication remains active", ErrRetainedPublicationIneligible)
	}
	settled, err := attemptRootRunsSettledTx(ctx, tx, p.Repository, p.Issue, a.ID)
	if err != nil || !settled {
		return ErrAttemptRunsPending
	}
	var unsafe bool
	// Settlement may wrap a missing report in a blocked phase result. That
	// exact empty wrapper is not a typed review verdict or reusable output.
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE data->>'attempt_root'=$1 AND id<>$1
		AND NOT COALESCE((stage='finished' AND data->>'phase'='review' AND data->>'outcome'='needs-human' AND data->>'reason'=$2
		AND data#>>'{result,status}'='blocked' AND data#>>'{result,reported}'='false'
		AND (NOT (data->'result' ? 'phase_result') OR data#>'{result,phase_result}'=jsonb_build_object(
			'version',$3::int,'phase','review','status','blocked','summary',data#>>'{result,summary}'))
		AND NOT (data->'result' ? 'review')
		AND COALESCE(jsonb_array_length(data#>'{result,findings}'),0)=0)
		OR ($4::boolean AND run=$5 AND stage='finished' AND data->>'phase'='implement' AND data->>'outcome'='succeeded'
		AND data#>>'{result,status}'='completed' AND data#>>'{result,reported}'='true' AND data#>>'{result,candidate}'=$6
		AND data->>'publication_assignment'=$1),FALSE))`, a.ID, factory.AssignReasonNoReport, factory.PhaseResultVersion, a.RetainedPublication == nil, p.Run, p.Candidate).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("%w: a child has reusable or unsettled output", ErrRetainedPublicationIneligible)
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_merges WHERE publication=$1 AND COALESCE((data#>>'{operation,attempts}')::int,1)>0)`, p.ID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrRetainedPublicationIneligible
	}
	return nil
}

func insertFreshRetainedReviewTx(ctx context.Context, tx *sql.Tx, prior factory.Publication, owner factory.Assignment, command string, now time.Time) (factory.Publication, factory.Assignment, error) {
	r := factory.RetainedPublication{Publication: prior.ID, Assignment: owner.ID, Run: prior.Run, Candidate: prior.Candidate, Connection: owner.Connection}
	if owner.RetainedPublication != nil {
		r = *owner.RetainedPublication
	}
	r.AdoptedFrom, r.NotAfter = prior.ID, 0
	a := factory.Assignment{
		ID: command, AttemptRoot: command, PublicationAssignment: command,
		Subject: owner.Subject, ProjectID: owner.ProjectID, Role: project.RoleCoder,
		Authority: owner.Authority, ActorID: owner.ActorID, Acceptance: owner.Acceptance,
		Preparation: owner.Preparation, SourceCommit: owner.SourceCommit,
		Stage: factory.AssignmentRetained, NativeRev: owner.NativeRev,
		Repository: owner.Repository, Issue: owner.Issue, CreatedUnix: now.Unix(),
		RetainedPublication: &r,
	}
	p := prior
	p.ID, p.AssignmentID, p.Revision, p.CreatedUnix = command, command, 0, now.Unix()
	p.ReviewCycles = nil
	p.Tuple = nil
	p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, now.Unix()
	if err := a.Validate(); err != nil {
		return p, a, err
	}
	if err := p.Validate(); err != nil {
		return p, a, err
	}
	araw, err := json.Marshal(a)
	if err != nil {
		return p, a, err
	}
	praw, err := json.Marshal(p)
	if err != nil {
		return p, a, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'',$4,0,$5)`, a.ID, a.Repository, a.Issue, a.Stage, string(araw)); err != nil {
		return p, a, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, p.ID, p.Repository, p.Issue, p.Run, p.Stage, string(praw))
	return p, a, err
}

func checkRetainedContinuationTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, g admissionGrants) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, a.AttemptRoot).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	var root factory.Assignment
	if json.Unmarshal(raw, &root) != nil {
		return ErrAdmissionChanged
	}
	if root.RetainedPublication == nil {
		return nil
	}
	expectedPublication := root.ID
	if root.RetainedPublication.AdoptedFrom != "" {
		expectedPublication = root.RetainedPublication.AdoptedFrom
	}
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM factory_commands WHERE type=$1 AND target=$2 AND finished<>''
		AND ($3='' OR id=$4)`, factory.CommandFundRetained, "publication/"+expectedPublication, root.RetainedPublication.AdoptedFrom, root.ID).Scan(&raw); err != nil {
		return ErrAdmissionChanged
	}
	var f factory.RetainedCandidateFunding
	// Policy is the funding command's admission CAS. Each later child captures
	// and rechecks its own current policy through the ordinary packet gate;
	// reconnecting the writer does not refill time or rebind the funded source.
	if json.Unmarshal(raw, &f) != nil || f.Validate() != nil || f.Publication != expectedPublication || !f.MatchesSource(g.sponsorship) || f.Acceptance != a.Acceptance {
		return ErrAdmissionChanged
	}
	if root.RetainedPublication.AdoptedFrom != "" {
		var previousRoot string
		if err := tx.QueryRowContext(ctx, `SELECT assignment FROM factory_publications WHERE data->>'id'=$1`, expectedPublication).Scan(&previousRoot); err != nil || previousRoot != f.PreviousRoot {
			return ErrAdmissionChanged
		}
	} else if f.PreviousRoot != "" {
		return ErrAdmissionChanged
	}
	requirements, approval := f.Requirements, f.Approval
	if requirements == "" && approval == "" {
		requirements, approval = root.Authority.RequirementsID, root.Authority.ApprovalID
	}
	if requirements != a.Authority.RequirementsID || approval != a.Authority.ApprovalID {
		return ErrAdmissionChanged
	}
	return nil
}

// PublicationExecutionOwner reads the actual prior owner for candidate/plan
// provenance. It never changes the retained owner's root, run or authority.
func (s *Store) PublicationExecutionOwner(ctx context.Context, p factory.Publication, owner factory.Assignment) (factory.Assignment, error) {
	return publicationExecutionOwner(ctx, s.db, p, owner)
}

func publicationExecutionOwner(ctx context.Context, q grantCommandSQL, p factory.Publication, owner factory.Assignment) (factory.Assignment, error) {
	if owner.Validate() != nil || p.AssignmentID != owner.ID {
		return factory.Assignment{}, ErrRetainedPublicationIneligible
	}
	if owner.ExternalPublication != nil {
		var raw []byte
		if err := q.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, owner.ExternalPublication.PlanAssignment).Scan(&raw); err != nil { return factory.Assignment{}, err }
		var plan factory.Assignment
		if json.Unmarshal(raw,&plan) != nil || plan.Validate() != nil || plan.Stage != factory.AssignmentFinished || plan.Outcome != factory.Succeeded ||
			plan.Phase != project.ExecutionInvestigate || plan.NativeTool == nil || plan.Result == nil || plan.Result.PhaseResult == nil || plan.Result.PhaseResult.Output == nil || plan.Result.PhaseResult.Output.Plan == nil || plan.Result.PhaseResult.Output.PlanReview=="" ||
			plan.Repository != owner.Repository || plan.Issue != owner.Issue || plan.ProjectID != owner.ProjectID || plan.Acceptance != owner.Acceptance || plan.Preparation != owner.Preparation ||
			plan.SourceCommit != owner.SourceCommit || plan.Connection != owner.ExternalPublication.Connection || !reflect.DeepEqual(p.External,&owner.ExternalPublication.Origin) {
			return factory.Assignment{}, ErrRetainedPublicationIneligible
		}
		return plan,nil
	}
	if owner.RetainedPublication == nil {
		return owner, nil
	}
	r := owner.RetainedPublication
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, r.Assignment).Scan(&raw); err != nil {
		return factory.Assignment{}, err
	}
	var origin factory.Assignment
	if json.Unmarshal(raw, &origin) != nil || origin.Validate() != nil || origin.Stage != factory.AssignmentFinished || origin.Outcome != factory.Succeeded ||
		origin.Repository != owner.Repository || origin.Issue != owner.Issue || origin.ProjectID != owner.ProjectID || origin.Acceptance != owner.Acceptance ||
		origin.Preparation != owner.Preparation || origin.SourceCommit != owner.SourceCommit || origin.Connection != r.Connection || origin.PublicationAssignment != origin.ID || origin.AttemptRoot != origin.ID {
		return factory.Assignment{}, ErrRetainedPublicationIneligible
	}
	return origin, nil
}
