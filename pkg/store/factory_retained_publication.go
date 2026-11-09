package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/levitateos/factory/pkg/project"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

var ErrRetainedPublicationIneligible = errors.New("retained candidate publication is no longer eligible")

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
	if exists {
		return factory.RetainedCandidateFunding{}, ErrRetainedPublicationIneligible
	}
	limits := g.policy.AttemptLimits
	limits.ActiveMinutes = min(limits.ActiveMinutes, 15)
	limits.ReviewRounds = min(limits.ReviewRounds, 2)
	limits.CorrectionCycles = min(limits.CorrectionCycles, 1)
	limits.MaxChildren = 1
	f := factory.RetainedCandidateFunding{Native: g.sponsorship.Native, Publication: p.ID, Candidate: p.Candidate, Base: p.PRCreate.BaseOID, Acceptance: p.Acceptance, Connection: g.sponsorship.Connection, GrantID: g.sponsorship.GrantID, Generation: g.sponsorship.Generation, Policy: g.policy.Revision, Limits: limits}
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
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, id).Scan(&raw); err != nil {
		return fail(err)
	}
	if json.Unmarshal(raw, &p) != nil || p.Validate() != nil || p.Stage != factory.PublicationPublished || p.WithdrawRequested || p.PRNumber < 1 || p.PRCreate.BaseOID == "" {
		return fail(ErrRetainedPublicationIneligible)
	}
	if err := checkDispatchOpenTx(ctx, tx, p.Repository); err != nil {
		return fail(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		return fail(err)
	}
	if json.Unmarshal(raw, &p) != nil || p.Validate() != nil || p.ID != id || p.Stage != factory.PublicationPublished || p.WithdrawRequested {
		return fail(ErrRetainedPublicationIneligible)
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, p.AssignmentID).Scan(&raw); err != nil {
		return fail(err)
	}
	if json.Unmarshal(raw, &a) != nil || a.Validate() != nil || a.RetainedPublication == nil || a.ID != a.AttemptRoot || a.ID != a.PublicationAssignment || a.RetainedPublication.Run != p.Run || a.RetainedPublication.Candidate != p.Candidate {
		return fail(ErrRetainedPublicationIneligible)
	}
	owner, err := currentAttemptOwnerTx(ctx, tx, p.Repository, p.Issue, a.ID)
	if err != nil || owner != a.ID {
		return fail(ErrRetainedPublicationIneligible)
	}
	var closed bool
	if err = tx.QueryRowContext(ctx, `SELECT data->>'closed'='true' FROM factory_attempt_allowances WHERE root_assignment=$1`, a.RetainedPublication.Assignment).Scan(&closed); err != nil {
		return fail(err)
	}
	if !closed {
		return fail(ErrRetainedPublicationIneligible)
	}
	var accepted bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 AND decision=$3) AND NOT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$1 AND issue=$2 AND decision=$3)`, p.Repository, p.Issue, p.Acceptance).Scan(&accepted); err != nil {
		return fail(err)
	}
	if !accepted {
		return fail(ErrRetainedPublicationIneligible)
	}
	grants, err = loadAdmissionGrantsTx(ctx, tx, p.Repository, a.RetainedPublication.Connection)
	if err != nil {
		return fail(err)
	}
	source := a
	source.Connection = a.RetainedPublication.Connection
	current := p.Authority
	current.Policy, current.Operator, current.Capacity = grants.policy.Revision, grants.operator.Revision, grants.capacity.Revision
	current.Sponsorship, current.Environment = grants.sponsorship.Revision, grants.environment.Revision
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
	if f.Candidate != p.Candidate || f.Base != p.PRCreate.BaseOID || f.Acceptance != p.Acceptance || !f.MatchesSource(g.sponsorship) || f.Policy != g.policy.Revision {
		return empty, ErrRetainedPublicationIneligible
	}
	settled, err := attemptRootRunsSettledTx(ctx, tx, p.Repository, p.Issue, a.RetainedPublication.Assignment)
	if err != nil || !settled {
		return empty, ErrAttemptRunsPending
	}
	bounds := g.policy.AttemptLimits
	if f.Limits.ActiveMinutes > bounds.ActiveMinutes || f.Limits.ReviewRounds > bounds.ReviewRounds || f.Limits.CorrectionCycles > bounds.CorrectionCycles || f.Limits.MaxChildren > bounds.MaxChildren {
		return empty, ErrRetainedPublicationIneligible
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_attempt_allowances WHERE root_assignment=$1)`, a.ID).Scan(&exists); err != nil {
		return empty, err
	}
	if exists {
		return empty, ErrRetainedPublicationIneligible
	}
	// The new root is distinct from the original closed/uncertain attempt. An
	// existing allowance, even closed or exhausted, can never be replenished.
	if err = insertAttemptAllowanceTx(ctx, tx, p.Repository, p.Issue, a.ID, f.Limits, time.Unix(now.Unix()+int64(f.Limits.ActiveMinutes)*60, 0), now); err != nil {
		return empty, err
	}
	if err = finishFactoryCommandTx(ctx, tx, command, now); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return receipt, nil
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
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM factory_commands WHERE type=$1 AND target=$2 AND finished<>''`, factory.CommandFundRetained, "publication/"+root.ID).Scan(&raw); err != nil {
		return ErrAdmissionChanged
	}
	var f factory.RetainedCandidateFunding
	// Policy is the funding command's admission CAS. Each later child captures
	// and rechecks its own current policy through the ordinary packet gate;
	// reconnecting the writer does not refill time or rebind the funded source.
	if json.Unmarshal(raw, &f) != nil || f.Validate() != nil || !f.MatchesSource(g.sponsorship) || f.Acceptance != a.Acceptance {
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
