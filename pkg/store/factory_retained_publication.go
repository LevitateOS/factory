package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

var ErrRetainedPublicationIneligible = errors.New("retained candidate publication is no longer eligible")

type reviewRetryCapture struct {
	Retry factory.ReviewRetry `json:"retry"`
	Work  factory.ReviewWork  `json:"work"`
}

// ReviewRetryOffer contains only an existing terminal no-send and current policy.
// Reading this offer creates neither send authority nor inference funding.
func (s *Store) ReviewRetryOffer(ctx context.Context, publication string) (factory.ReviewRetry, error) {
	p, err := s.PublicationByID(ctx, publication)
	if err != nil {
		return factory.ReviewRetry{}, err
	}
	policy, err := s.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		return factory.ReviewRetry{}, err
	}
	if p.Stage != factory.PublicationPublished || p.WithdrawRequested || len(p.ReviewOperations) == 0 ||
		!policy.Enabled || policy.Paused || policy.TargetBranch != p.TargetBranch {
		return factory.ReviewRetry{}, ErrRetainedPublicationIneligible
	}
	op := p.ReviewOperations[len(p.ReviewOperations)-1]
	if !op.DefinitiveNoSend() || op.Work.HeadOID != p.Candidate || op.Work.BaseOID != p.PullTarget().BaseOID || policy.Review.ActorID != op.Work.ActorID {
		return factory.ReviewRetry{}, ErrRetainedPublicationIneligible
	}
	r := factory.ReviewRetry{Publication: p.ID, Run: op.RunID, Operation: op.Work.OperationID, Head: p.Candidate, Base: p.PullTarget().BaseOID, Policy: policy.Revision}
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
		op.RetryCommand, factory.CommandRetryReview, "publication/"+p.ID).Scan(&raw); err != nil {
		return err
	}
	var capture reviewRetryCapture
	if json.Unmarshal(raw, &capture) != nil || capture.Retry.Validate() != nil || !factory.ReviewWorkEqual(capture.Work, op.Work) ||
		capture.Retry.Publication != p.ID || capture.Retry.Run != op.RunID || capture.Retry.Operation != prior.Work.OperationID ||
		capture.Retry.Head != p.Candidate || capture.Retry.Base != p.PullTarget().BaseOID ||
		op.Work.NotAfter <= time.Now().Unix() {
		return ErrAdmissionChanged
	}
	var policy factory.RepositoryPolicy
	if err := loadGrantDocumentTx(ctx, tx, &policy, `SELECT data FROM factory_policies WHERE repository=$1 FOR UPDATE`, p.Repository); err != nil {
		return err
	}
	if !policy.Enabled || policy.Paused || policy.Revision != capture.Retry.Policy || policy.TargetBranch != p.TargetBranch || policy.Review.ActorID != op.Work.ActorID {
		return ErrAdmissionChanged
	}
	var root string
	if err := tx.QueryRowContext(ctx, `SELECT data->>'attempt_root' FROM factory_assignments WHERE id=$1`, p.AssignmentID).Scan(&root); err != nil {
		return err
	}
	settled, err := attemptRootRunsSettledTx(ctx, tx, p.Repository, p.Issue, root)
	if err != nil {
		return err
	}
	if !settled {
		return ErrAttemptRunsPending
	}
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
	payload, err := json.Marshal(reviewRetryCapture{Retry: r, Work: w})
	if err != nil {
		return empty, err
	}
	cmd := factory.Command{ID: command, Type: factory.CommandRetryReview, Target: "publication/" + r.Publication, Principal: principal, Payload: string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return empty, err
	}
	if stored.Principal != principal || !created {
		return empty, ErrCommandConflict
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE data->>'id'=$1 FOR UPDATE`, r.Publication).Scan(&raw); err != nil {
		return empty, err
	}
	var p factory.Publication
	if json.Unmarshal(raw, &p) != nil || p.Validate() != nil {
		return empty, ErrPublicationConflict
	}
	op := factory.ReviewOperation{RunID: r.Run, RetryOf: r.Operation, RetryCommand: command, Work: w}
	if err = p.CanAppendReviewOperation(op); err != nil {
		return empty, err
	}
	if err = finishFactoryCommandTx(ctx, tx, command, now); err != nil {
		return empty, err
	}
	p.ReviewOperations = append(p.ReviewOperations, op)
	p.Revision++
	if err = updatePublicationTx(ctx, tx, p); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
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
	if json.Unmarshal(raw, &source) != nil || source.Validate() != nil || !source.Succeeded() ||
		source.Result == nil || !source.Result.Reported || source.Result.Status != "completed" ||
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
	f.Requirements, f.Approval = p.Authority.RequirementsID, p.Authority.ApprovalID
	if exists {
		prior, err := lockAttemptAllowanceRootTx(ctx, tx, p.Repository, p.Issue, a.ID)
		if err != nil {
			return f, err
		}
		unused, err := retainedUnusedReviewBudgetTx(ctx, tx, p, prior, limits)
		if err != nil {
			return f, err
		}
		if unused {
			f.CarryUnused, f.Limits = true, prior.Limits
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
	candidateRun := p.Run
	if candidateRun == "" && a.RetainedPublication != nil {
		candidateRun = a.RetainedPublication.Run
	}
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE run=$1`, candidateRun).Scan(&raw); err != nil {
		return fail(err)
	}
	var candidate factory.Assignment
	if json.Unmarshal(raw, &candidate) != nil || candidate.Validate() != nil || candidate.Role != project.RoleCoder || !candidate.Succeeded() || candidate.Result == nil || !candidate.Result.Reported || candidate.Result.Status != "completed" || candidate.Result.Candidate != p.Candidate || candidate.Repository != p.Repository || candidate.Issue != p.Issue || candidate.ProjectID != p.ProjectID || candidate.Acceptance != p.Acceptance || candidate.Connection != origin.Connection {
		return fail(fmt.Errorf("%w: original candidate report changed", ErrRetainedPublicationIneligible))
	}
	history, err := publicationReviewHistory(ctx, tx, p)
	if err != nil {
		return fail(err)
	}
	// An explicitly republished original Builder keeps its original assignment;
	// later corrected candidates must instead join their admitted review history.
	boundCandidate := candidate.ID == origin.ID && origin.Run == candidateRun &&
		origin.AttemptRoot == origin.ID && origin.PublicationAssignment == origin.ID
	for _, source := range history {
		if source.Run == candidateRun && source.Candidate == p.Candidate && candidate.AttemptRoot == source.AssignmentID && candidate.PublicationAssignment == source.AssignmentID {
			boundCandidate = true
		}
	}
	if !boundCandidate || a.RetainedPublication != nil && p.Run == "" && (a.RetainedPublication.Run != candidateRun || a.RetainedPublication.Candidate != p.Candidate) {
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
	// Keep immutable execution/planning captures valid. This local publication
	// projection carries current authority for a new continuation only.
	p.Authority = current
	return p, a, grants, nil
}

func (s *Store) FundRetainedCandidate(ctx context.Context, command, principal string, f factory.RetainedCandidateFunding, now time.Time) (factory.RetainedCandidateFundingReceipt, error) {
	return s.FundRetainedCandidateObserved(ctx, command, principal, f, nil, now)
}

// FundRetainedCandidateObserved admits a fresh authenticated external origin
// only for this new command. Old publication captures and deadlines never move.
func (s *Store) FundRetainedCandidateObserved(ctx context.Context, command, principal string, f factory.RetainedCandidateFunding, observed *factory.ExternalPublicationOrigin, now time.Time) (factory.RetainedCandidateFundingReceipt, error) {
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
	if f.Requirements != p.Authority.RequirementsID || f.Approval != p.Authority.ApprovalID {
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
		if p.External != nil && (observed == nil || g.environment.Native == nil || g.environment.Native.Profile != observed.Profile ||
			g.policy.Review.NativeProfile != observed.Profile || g.policy.Review.NativeConnection != observed.Connection || g.policy.Review.ActorID != observed.ActorID) {
			return empty, ErrAdmissionChanged
		}
		prior, err := lockAttemptAllowanceRootTx(ctx, tx, p.Repository, p.Issue, a.ID)
		if err != nil {
			return empty, err
		}
		unused, err := retainedUnusedReviewBudgetTx(ctx, tx, p, prior, bounds)
		if err != nil {
			return empty, err
		}
		if f.CarryUnused != unused {
			return empty, ErrRetainedPublicationIneligible
		}
		if !prior.Closed {
			// A changed setup may replace only a proved unlaunched retained
			// continuation. Retire its old grant in this same admission; never
			// rewrite its setup capture, spent rounds or command receipt.
			if err = transitionLockedAttemptRootTx(ctx, tx, prior, true, now); err != nil {
				return empty, err
			}
		}
		p, a, err = insertFreshRetainedReviewTx(ctx, tx, p, a, command, observed, now)
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

// A fresh command can fund another finite review after confirmed retirement.
// Known reports remain in the admitted history; an uncertain effect or a
// missing source binding cannot disappear into a new owner.
func admitFreshRetainedReviewTx(ctx context.Context, tx *sql.Tx, p factory.Publication, a factory.Assignment) error {
	prior, err := lockAttemptAllowanceRootTx(ctx, tx, p.Repository, p.Issue, a.ID)
	if err != nil {
		return err
	}
	if !prior.Closed || prior.Active {
		if prior.Closed || !prior.Active || a.RetainedPublication == nil ||
			(a.Authority.RequirementsID == p.Authority.RequirementsID && a.Authority.ApprovalID == p.Authority.ApprovalID) ||
			len(p.ReviewOperations)+len(p.Corrections)+len(p.CorrectionJournals)+len(p.DeliveryAcceptances)+len(p.ResolutionOperations) != 0 {
			return ErrRetainedPublicationIneligible
		}
		// Dispatch records its assignment/run atomically before starting a
		// model; preparation admission precedes native I/O. Absence of both
		// proves this owner never launched either effect, rather than relying
		// on a missing provider lookup or a terminal label.
		launched, err := retainedReviewHasAdmittedWork(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if launched {
			return ErrAttemptRunsPending
		}
	}
	if !p.HasPullTarget() || p.External == nil &&
		(p.Publish.Effect != factory.OpEffectCommitted || p.Publish.Completion != factory.OpCompletionComplete || p.PRCreate.Effect != factory.OpEffectCommitted || p.PRCreate.Completion != factory.OpCompletionComplete || len(p.Corrections) != 0) {
		return fmt.Errorf("%w: prior allowance or publication remains active", ErrRetainedPublicationIneligible)
	}
	for _, op := range p.ReviewOperations {
		if op.Outcome.NotObserved || op.Outcome.Completion != factory.OpCompletionComplete ||
			(op.Outcome.Effect != factory.OpEffectCommitted && op.Outcome.Effect != factory.OpEffectNotCommitted) {
			return ErrAttemptRunsPending
		}
	}
	for _, op := range p.Corrections {
		if op.Effect != factory.OpEffectCommitted || op.Completion != factory.OpCompletionComplete {
			return ErrAttemptRunsPending
		}
	}
	for _, op := range p.ResolutionOperations {
		if op.Outcome.NotObserved || op.Outcome.Completion != factory.OpCompletionComplete ||
			(op.Outcome.Effect != factory.OpEffectCommitted && op.Outcome.Effect != factory.OpEffectNotCommitted) {
			return ErrAttemptRunsPending
		}
	}
	settled, err := attemptRootRunsSettledTx(ctx, tx, p.Repository, p.Issue, a.ID)
	if err != nil || !settled {
		return ErrAttemptRunsPending
	}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_assignments WHERE data->>'attempt_root'=$1 AND id<>$1 ORDER BY seq LIMIT 513`, a.ID)
	if err != nil {
		return err
	}
	children := 0
	for rows.Next() {
		var raw []byte
		var child factory.Assignment
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(raw, &child) != nil || child.Validate() != nil || !child.Finished() || child.Result == nil || child.PublicationAssignment != p.AssignmentID || children >= 512 {
			rows.Close()
			return ErrRetainedPublicationIneligible
		}
		children++
		switch child.Phase {
		case project.ExecutionReview:
			result := child.Result
			if result.Reported {
				if child.Outcome != factory.Succeeded || result.PhaseResult == nil || result.PhaseResult.Output == nil || (result.PhaseResult.Output.Review == nil && result.PhaseResult.Output.Clear == nil) {
					rows.Close()
					return ErrRetainedPublicationIneligible
				}
			} else if result.Review != nil || len(result.Findings) != 0 || result.PhaseResult != nil && result.PhaseResult.Output != nil ||
				(child.Outcome != factory.Cancelled && child.Outcome != factory.Failed && !(child.Outcome == factory.NeedsHuman && child.Reason == factory.AssignReasonNoReport)) {
				rows.Close()
				return ErrRetainedPublicationIneligible
			}
		case project.ExecutionCorrect:
			bound := false
			for _, op := range p.Corrections {
				if op.RunID == child.Run && op.Work != nil && op.Work.Candidate == child.Result.Candidate {
					bound = true
				}
			}
			if !bound || child.Outcome != factory.Succeeded || !child.Result.Reported || child.Result.PhaseResult == nil || child.Result.PhaseResult.Output == nil || child.Result.PhaseResult.Output.Correction == nil {
				rows.Close()
				return ErrRetainedPublicationIneligible
			}
		case project.ExecutionImplement:
			if a.RetainedPublication != nil || child.Run != p.Run || child.Outcome != factory.Succeeded || !child.Result.Reported || child.Result.Candidate != p.Candidate {
				rows.Close()
				return ErrRetainedPublicationIneligible
			}
		default:
			rows.Close()
			return ErrRetainedPublicationIneligible
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
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

func retainedUnusedReviewBudgetTx(ctx context.Context, tx *sql.Tx, p factory.Publication, prior factory.AttemptAllowance, bounds factory.AttemptLimits) (bool, error) {
	eligible := len(prior.Reviews) > 0 && len(prior.Reviews) < prior.Limits.ReviewRounds &&
		len(p.ReviewOperations)+len(p.Corrections)+len(p.CorrectionJournals)+len(p.DeliveryAcceptances)+len(p.ResolutionOperations) == 0 &&
		prior.Limits.ActiveMinutes <= bounds.ActiveMinutes && prior.Limits.ReviewRounds <= bounds.ReviewRounds && prior.Limits.CorrectionCycles <= bounds.CorrectionCycles
	if !eligible {
		return false, nil
	}
	// A reserved identity without an admitted reviewer packet did not launch.
	// An incomplete admitted reviewer can hand off only its unused budget.
	var launched bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE data->>'attempt_root'=$1 AND id<>$1 AND data->>'phase'='review')`, p.AssignmentID).Scan(&launched)
	return launched, err
}

func insertFreshRetainedReviewTx(ctx context.Context, tx *sql.Tx, prior factory.Publication, owner factory.Assignment, command string, observed *factory.ExternalPublicationOrigin, now time.Time) (factory.Publication, factory.Assignment, error) {
	origin, err := publicationExecutionOwner(ctx, tx, prior, owner)
	if err != nil {
		return prior, owner, err
	}
	r := factory.RetainedPublication{Publication: prior.ID, Assignment: origin.ID, Run: prior.Run, Candidate: prior.Candidate, Connection: origin.Connection}
	if owner.RetainedPublication != nil {
		r = *owner.RetainedPublication
		if prior.Run != "" {
			r.Run, r.Candidate = prior.Run, prior.Candidate
		}
	}
	r.AdoptedFrom, r.NotAfter = prior.ID, 0
	a := factory.Assignment{
		ID: command, AttemptRoot: command, PublicationAssignment: command,
		Subject: owner.Subject, ProjectID: owner.ProjectID, Role: project.RoleCoder,
		Authority: prior.Authority, ActorID: owner.ActorID, Acceptance: owner.Acceptance,
		Preparation: owner.Preparation, SourceCommit: owner.SourceCommit,
		Stage: factory.AssignmentRetained, NativeRev: owner.NativeRev,
		Repository: owner.Repository, Issue: owner.Issue, CreatedUnix: now.Unix(),
		RetainedPublication: &r,
	}
	p := prior
	p.ID, p.AssignmentID, p.Revision, p.CreatedUnix = command, command, 0, now.Unix()
	p.ReviewCycles = nil
	p.ReviewOperations, p.Corrections, p.CorrectionJournals = nil, nil, nil
	p.DeliveryAcceptances, p.ResolutionOperations = nil, nil
	if prior.Tuple != nil {
		tuple := *prior.Tuple
		tuple.Revision = 0
		p.Tuple = &tuple
	}
	if prior.External != nil {
		if observed == nil || observed.Validate(prior.Repository) != nil || observed.CommandID != command || observed.Policy != prior.Authority.Policy || observed.ObservedUnix > now.Unix() || now.Unix()-observed.ObservedUnix > 30 ||
			observed.Target != (factory.PublicationPullTarget{AuthorID: prior.PullTarget().AuthorID, IssueID: prior.PullTarget().IssueID, HeadRepository: prior.PullTarget().HeadRepository, HeadRef: prior.PullTarget().HeadRef, BaseRef: prior.PullTarget().BaseRef, HeadOID: prior.Candidate, BaseOID: prior.PullTarget().BaseOID}) ||
			observed.Host != prior.External.Host || observed.Profile != prior.External.Profile || observed.Connection != prior.External.Connection || observed.ActorID != prior.External.ActorID || observed.ForkConsent != "" {
			return p, a, ErrAdmissionChanged
		}
		var revision int64
		if err = tx.QueryRowContext(ctx, `SELECT revision FROM factory_native_review_heads WHERE repository=$1 AND pull=$2 FOR UPDATE`, p.Repository, p.PRNumber).Scan(&revision); err != nil || revision != observed.ObservationRevision {
			return p, a, ErrAdmissionChanged
		}
		p.External, p.Run = observed, ""
		p.Publish, p.PRCreate = factory.PublicationOperation{Kind: factory.OpRefPublish}, factory.PublicationOperation{Kind: factory.OpPRCreate}
		p.NativeRev, p.ObservedUnix, a.NativeRev = observed.ObservationRevision, observed.ObservedUnix, observed.ObservationRevision
	} else if observed != nil {
		return p, a, ErrAdmissionChanged
	}
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
		if err := q.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, owner.ExternalPublication.PlanAssignment).Scan(&raw); err != nil {
			return factory.Assignment{}, err
		}
		var plan factory.Assignment
		if json.Unmarshal(raw, &plan) != nil || plan.Validate() != nil || !plan.Succeeded() ||
			plan.Phase != project.ExecutionInvestigate || plan.NativeTool == nil || plan.Result == nil || plan.Result.PhaseResult == nil || plan.Result.PhaseResult.Output == nil || plan.Result.PhaseResult.Output.Plan == nil || plan.Result.PhaseResult.Output.PlanReview == "" ||
			plan.Repository != owner.Repository || plan.Issue != owner.Issue || plan.ProjectID != owner.ProjectID || plan.Acceptance != owner.Acceptance || plan.Preparation != owner.Preparation ||
			plan.SourceCommit != owner.SourceCommit || plan.Connection != owner.ExternalPublication.Connection || !reflect.DeepEqual(p.External, &owner.ExternalPublication.Origin) {
			return factory.Assignment{}, ErrRetainedPublicationIneligible
		}
		return plan, nil
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
	if json.Unmarshal(raw, &origin) != nil || origin.Validate() != nil || !origin.Succeeded() ||
		origin.Repository != owner.Repository || origin.Issue != owner.Issue || origin.ProjectID != owner.ProjectID || origin.Acceptance != owner.Acceptance ||
		origin.Preparation != owner.Preparation || origin.SourceCommit != owner.SourceCommit || origin.Connection != r.Connection || origin.PublicationAssignment != origin.ID || origin.AttemptRoot != origin.ID {
		return factory.Assignment{}, ErrRetainedPublicationIneligible
	}
	return origin, nil
}

// PublicationReviewHistory follows only explicitly funded retained ownership.
// Reports and native effects stay on their original publication and attempt.
func (s *Store) PublicationReviewHistory(ctx context.Context, p factory.Publication) ([]factory.Publication, error) {
	return publicationReviewHistory(ctx, s.db, p)
}

func publicationReviewHistory(ctx context.Context, q grantCommandSQL, p factory.Publication) ([]factory.Publication, error) {
	history := []factory.Publication{}
	seen := map[string]bool{}
	total := 0
	for {
		if len(history) >= 32 || seen[p.ID] || p.Validate() != nil {
			return nil, ErrRetainedPublicationIneligible
		}
		total += len(p.ReviewCycles) + len(p.CorrectionJournals)
		if total > 512 {
			return nil, ErrRetainedPublicationIneligible
		}
		seen[p.ID] = true
		history = append(history, p)
		var raw []byte
		if err := q.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, p.AssignmentID).Scan(&raw); err != nil {
			return nil, err
		}
		var owner factory.Assignment
		if json.Unmarshal(raw, &owner) != nil || owner.Validate() != nil || owner.ID != p.AssignmentID ||
			owner.AttemptRoot != owner.ID || owner.PublicationAssignment != owner.ID ||
			owner.Repository != p.Repository || owner.Issue != p.Issue || owner.ProjectID != p.ProjectID || owner.Acceptance != p.Acceptance {
			return nil, ErrRetainedPublicationIneligible
		}
		r := owner.RetainedPublication
		if len(history) > 1 && r != nil && len(p.ReviewCycles) > 0 &&
			len(p.ReviewOperations)+len(p.Corrections)+len(p.CorrectionJournals)+len(p.DeliveryAcceptances)+len(p.ResolutionOperations) == 0 {
			// A later finished funding command admits this predecessor. Only a
			// closed, never-launched retained root can have reserved cycles with
			// no report. Omit those markers from this derived view, retaining
			// their canonical cycles and spent identities unchanged in Store.
			var allowance factory.AttemptAllowance
			var revision int64
			if err := q.QueryRowContext(ctx, `SELECT revision,data FROM factory_attempt_allowances WHERE repository=$1 AND issue=$2 AND root_assignment=$3`, p.Repository, p.Issue, owner.ID).Scan(&revision, &raw); err != nil {
				return nil, err
			}
			if json.Unmarshal(raw, &allowance) != nil || validateAttemptAllowanceRow(allowance, p.Repository, p.Issue, owner.ID, revision) != nil {
				return nil, ErrRetainedPublicationIneligible
			}
			if allowance.Closed && !allowance.Active {
				launched, err := retainedReviewHasAdmittedWork(ctx, q, owner.ID)
				if err != nil {
					return nil, err
				}
				if !launched {
					history[len(history)-1].ReviewCycles = nil
				}
			}
		}
		if r == nil || r.AdoptedFrom == "" {
			break
		}
		if err := q.QueryRowContext(ctx, `SELECT payload FROM factory_commands WHERE id=$1 AND type=$2 AND target=$3 AND finished<>''`,
			owner.ID, factory.CommandFundRetained, "publication/"+r.AdoptedFrom).Scan(&raw); err != nil {
			return nil, err
		}
		var funding factory.RetainedCandidateFunding
		if json.Unmarshal(raw, &funding) != nil || funding.Validate() != nil || funding.Publication != r.AdoptedFrom ||
			funding.Connection != r.Connection || funding.Candidate != p.InitialCandidate() || funding.Acceptance != p.Acceptance || funding.Base != p.PullTarget().BaseOID {
			return nil, ErrRetainedPublicationIneligible
		}
		if err := q.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE data->>'id'=$1`, r.AdoptedFrom).Scan(&raw); err != nil {
			return nil, err
		}
		var parent factory.Publication
		if json.Unmarshal(raw, &parent) != nil || parent.Validate() != nil || parent.ID != r.AdoptedFrom ||
			parent.AssignmentID != funding.PreviousRoot || parent.Candidate != funding.Candidate ||
			parent.Repository != p.Repository || parent.Issue != p.Issue || parent.ProjectID != p.ProjectID || parent.Acceptance != p.Acceptance ||
			parent.PRNumber != p.PRNumber || parent.PRID != p.PRID || parent.TargetBranch != p.TargetBranch {
			return nil, ErrRetainedPublicationIneligible
		}
		before, after := parent.PullTarget(), p.PullTarget()
		// Heads and observation revisions change only through the new exact
		// funding capture; the PR, source repository and reference identity do not.
		before.HeadOID, after.HeadOID = "", ""
		if before != after || (parent.External == nil) != (p.External == nil) {
			return nil, ErrRetainedPublicationIneligible
		}
		if p.External != nil && (p.External.Policy != funding.Policy || parent.External.Host != p.External.Host ||
			parent.External.Profile != p.External.Profile || parent.External.Connection != p.External.Connection || parent.External.ActorID != p.External.ActorID) {
			return nil, ErrRetainedPublicationIneligible
		}
		if err := q.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, parent.AssignmentID).Scan(&raw); err != nil {
			return nil, err
		}
		var parentOwner factory.Assignment
		if json.Unmarshal(raw, &parentOwner) != nil || parentOwner.ActorID != owner.ActorID {
			return nil, ErrRetainedPublicationIneligible
		}
		origin, err := publicationExecutionOwner(ctx, q, p, owner)
		if err != nil {
			return nil, err
		}
		parentOrigin, err := publicationExecutionOwner(ctx, q, parent, parentOwner)
		if err != nil {
			return nil, err
		}
		if origin.ID != parentOrigin.ID || origin.Connection != funding.Connection || origin.SourceCommit != parentOrigin.SourceCommit || origin.Preparation != parentOrigin.Preparation {
			return nil, ErrRetainedPublicationIneligible
		}
		// Preserve the authentication source captured by the original run, rather
		// than accepting whichever source happens to be selected while reading.
		if funding.Native != nil {
			if err := q.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1`, origin.Run).Scan(&raw); err != nil {
				return nil, err
			}
			var run factory.Run
			if json.Unmarshal(raw, &run) != nil || run.Validate() != nil || run.Native == nil || run.Admission == nil || run.Admission.Native == nil ||
				run.Native.Provenance.Profile != funding.Native.Profile || !run.Admission.Native.MatchesAuthentication(funding.Native.Authentication, funding.Native.CredentialSHA256) {
				return nil, ErrRetainedPublicationIneligible
			}
		}
		p = parent
	}
	slices.Reverse(history)
	return history, nil
}

func retainedReviewHasAdmittedWork(ctx context.Context, q grantCommandSQL, root string) (bool, error) {
	var launched bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE data->>'attempt_root'=$1 AND id<>$1)
	 OR EXISTS(SELECT 1 FROM project_preparations WHERE data#>>'{factory_admission,attempt_root}'=$1)`, root).Scan(&launched)
	return launched, err
}
