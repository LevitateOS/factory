package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
)

const (
	prepTestProject = "p0123456789abcdef01234567"
	prepTestID      = "f0123456789abcdef01234567"
	prepTestDigest  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	prepTestCommit  = "0123456789abcdef0123456789abcdef01234567"
)

func prepTestStore(t *testing.T) (*Store, context.Context, string) {
	t.Helper()
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	if err := s.UpsertUser(ctx, User{ID: 3, Login: "soda-tester"}); err != nil {
		t.Fatal(err)
	}
	profile := &project.Profile{ID: project.RockyHeadless, Distribution: "rocky", Version: "10.2", Interface: "headless", Architecture: "amd64", Image: "sha256:" + strings.Repeat("a", 64), Revision: strings.Repeat("b", 40)}
	if err := s.CreateProject(ctx, Project{ID: prepTestProject, Name: "n", RepositoryID: 7, OwnerID: 3, Repository: "o/n", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test"); err != nil {
		t.Fatal(err)
	}
	return s, ctx, dsn
}

func prepTestPreparation() execution.PreparationRecord {
	return execution.PreparationRecord{Preparation: project.Preparation{
		ID: prepTestID, Project: prepTestProject, Role: project.RoleCoder,
		Requirements: project.RequirementAcceptance{ID: "d0123456789abcdef01234567", Revision: 1, Approver: 7, SourceCommit: prepTestCommit, Digest: prepTestDigest},
		Approval:     project.AdminApproval{ID: "d123456789abcdef012345678", Revision: 1, Approver: 9, EffectsDigest: prepTestDigest},
		SourceCommit: prepTestCommit, SetupDigest: prepTestDigest, Tools: []string{"python3"},
	}}
}

func TestLifecycleGrantRevisionRejectsStaleWriters(t *testing.T) {
	s, ctx, _ := prepTestStore(t)
	grant := project.LifecycleGrant{Project: prepTestProject, Owner: 3, Active: true}
	profile := &project.Profile{ID: project.RockyHeadless, Distribution: "rocky", Version: "10.2", Interface: "headless", Architecture: "amd64", Image: "sha256:" + strings.Repeat("a", 64), Revision: strings.Repeat("b", 40)}
	grant.Profile = profile
	if err := s.SaveLifecycleGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	stored, err := s.LifecycleGrant(ctx, prepTestProject)
	if err != nil || !stored.Active || stored.Revision != 1 {
		t.Fatalf("grant: %+v %v", stored, err)
	}
	stored.Active = false
	if err = s.SaveLifecycleGrant(ctx, stored); err != nil {
		t.Fatal(err)
	}
	grant.Active = true
	if err = s.SaveLifecycleGrant(ctx, grant); err == nil {
		t.Fatal("stale grant revision accepted")
	}
}

func TestMaintenanceHoldPersistsWithCAS(t *testing.T) {
	s, ctx, _ := prepTestStore(t)
	if _, err := s.MaintenanceHold(ctx, prepTestProject); err != sql.ErrNoRows {
		t.Fatalf("missing hold: %v", err)
	}
	if err := s.SaveMaintenanceHold(ctx, project.MaintenanceHold{Project: prepTestProject, Hold: true}); err != nil {
		t.Fatal(err)
	}
	held, err := s.MaintenanceHold(ctx, prepTestProject)
	if err != nil || !held.Hold || held.Revision != 1 {
		t.Fatalf("hold: %+v %v", held, err)
	}
	if err = s.SaveMaintenanceHold(ctx, project.MaintenanceHold{Project: prepTestProject, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMaintenanceHold(ctx, project.MaintenanceHold{Project: prepTestProject, Revision: 1, Hold: true}); err == nil {
		t.Fatal("stale hold revision accepted")
	}
}

func TestAdmitPreparationIsIdempotentForExactInputs(t *testing.T) {
	s, ctx, _ := prepTestStore(t)
	first, admitted, err := s.AdmitPreparation(ctx, prepTestPreparation())
	if err != nil || !admitted {
		t.Fatalf("admit: %v %v", admitted, err)
	}
	second, admitted, err := s.AdmitPreparation(ctx, prepTestPreparation())
	if err != nil || admitted || second.Preparation.ID != first.Preparation.ID {
		t.Fatalf("repeat: %v %v %+v", admitted, err, second)
	}
	changed := prepTestPreparation()
	changed.Preparation.SetupDigest = strings.Repeat("c", 64)
	if _, _, err = s.AdmitPreparation(ctx, changed); err == nil {
		t.Fatal("changed inputs reused the preparation identity")
	}
}

func TestPreparationRefsAreImmutable(t *testing.T) {
	s, ctx, dsn := prepTestStore(t)
	if _, _, err := s.AdmitPreparation(ctx, prepTestPreparation()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_preparations SET requirements='d99999999999999999999999' WHERE id=$1`, prepTestID); err == nil {
		t.Fatal("requirement reference mutation accepted")
	}
	current, err := s.Preparation(ctx, prepTestID)
	if err != nil {
		t.Fatal(err)
	}
	current.State = execution.PreparationState{Container: strings.Repeat("c", 64), SourceCommit: current.Preparation.SourceCommit, SetupDigest: current.Preparation.SetupDigest, ID: prepTestID, Project: prepTestProject, Role: project.RoleCoder, Phase: project.PrepareReady, Ready: true}
	if err = s.ObservePreparation(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err = s.ObservePreparation(ctx, current); err == nil {
		t.Fatal("stale preparation observation accepted")
	}
	observed, err := s.Preparation(ctx, prepTestID)
	if err != nil || !observed.State.Ready || observed.Preparation.Revision != 1 {
		t.Fatalf("observed: %+v %v", observed, err)
	}
	listed, err := s.ProjectPreparations(ctx, prepTestProject)
	if err != nil || len(listed) != 1 || listed[0].Preparation.Role != project.RoleCoder {
		t.Fatalf("listed: %+v %v", listed, err)
	}
	// Native records retain an owner before any observation. No container,
	// executable or setup success is invented by persistence or reopening.
	native := prepTestPreparation()
	native.Preparation.ID = "f" + strings.Repeat("a", 24)
	native.Native = &execution.NativeWorkspaceRef{Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: "owned-checkout"}
	if _, admitted, err := s.AdmitPreparation(ctx, native); err != nil || !admitted {
		t.Fatal("native preparation could not be recorded", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_preparations SET data=data-'native' WHERE id=$1`, native.Preparation.ID); err == nil {
		t.Fatal("SQL removed captured native preparation ownership")
	}
	native.State = execution.PreparationState{ID: native.Preparation.ID, Project: native.Preparation.Project, Role: native.Preparation.Role, SourceCommit: native.Preparation.SourceCommit, SetupDigest: native.Preparation.SetupDigest, Phase: project.PrepareReady, Ready: true, Native: native.Native}
	if err := s.ObservePreparation(ctx, native); err != nil {
		t.Fatal("native readiness required a container", err)
	}
	if err := s.ObservePreparation(ctx, native); err == nil {
		t.Fatal("stale native observation bypassed CAS")
	}
	native, err = s.Preparation(ctx, native.Preparation.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := native
	owner := *native.Native
	owner.WorktreeID = "foreign-checkout"
	foreign.Native, foreign.State.Native = &owner, &owner
	if _, _, err := s.AdmitPreparation(ctx, foreign); err == nil {
		t.Fatal("native preparation replay changed owner")
	}
	if err := s.ObservePreparation(ctx, foreign); err == nil {
		t.Fatal("native preparation observation changed owner")
	}
	foreignData, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_preparations SET data=$1 WHERE id=$2`, string(foreignData), native.Preparation.ID); err == nil {
		t.Fatal("SQL rewrote native preparation owner")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{state,container}','"fabricated"') WHERE id=$1`, native.Preparation.ID); err == nil {
		t.Fatal("SQL mixed native and appliance preparation state")
	}
	// Retained private candidate metadata shares the canonical record encoding.
	// This writes a fixture envelope, not a native execution authorization.
	candidate := native
	candidate.Preparation.ID, candidate.Preparation.Revision = "f"+strings.Repeat("b", 24), 0
	candidate.State.ID = candidate.Preparation.ID
	if _, _, err := s.AdmitPreparation(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	meta := candidatePreparationRegistration{Repository: 7, Issue: 1, ActorID: 3, Project: prepTestProject, OwnerAssignment: strings.Repeat("c", 32), ChildAssignment: strings.Repeat("d", 32), AttemptRoot: strings.Repeat("e", 32), Connection: "native-fixture", NotAfter: 2000000000}
	candidateData, err := marshalCandidatePreparation(candidate, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_preparations SET data=$1 WHERE id=$2`, string(candidateData), candidate.Preparation.ID); err != nil {
		t.Fatal(err)
	}
	private, err := s.CandidatePreparation(ctx, candidate.Preparation.ID)
	if err != nil || private.Admission.OwnerAssignment != meta.OwnerAssignment || private.NotAfter != meta.NotAfter || private.Preparation.Native == nil || *private.Preparation.Native != *native.Native {
		t.Fatal("native record serialization lost candidate admission", err)
	}
	stop := execution.PreparationState{ID: candidate.Preparation.ID, Project: prepTestProject, Phase: project.PrepareStopped, Native: native.Native, Stopped: true, Retirement: "uncertain"}
	foreignStop := stop
	foreignStop.Native = &owner
	if err := s.RecordCandidatePreparationStop(ctx, candidate.Preparation.ID, foreignStop); err == nil {
		t.Fatal("candidate stop changed captured native ownership")
	}
	if err := s.RecordCandidatePreparationStop(ctx, candidate.Preparation.ID, stop); err != nil {
		t.Fatal(err)
	}
	stop.Retirement = "confirmed"
	if err := s.RecordCandidatePreparationStop(ctx, candidate.Preparation.ID, stop); err != nil {
		t.Fatal(err)
	}
	native.State.Phase, native.State.Ready, native.State.Stopped, native.State.Retirement = project.PrepareStopped, false, true, "confirmed"
	if err := s.ObservePreparation(ctx, native); err != nil {
		t.Fatal(err)
	}
	// Reopen actual retained appliance/native rows from the prior unconditional
	// Project FK layout. Upgrading the reference must preserve every journal byte.
	var priorRows string
	if err := s.db.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(p)-'appliance_project_id' ORDER BY id)::text FROM project_preparations p`).Scan(&priorRows); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_preparations DROP CONSTRAINT project_preparations_appliance_project_fkey;
ALTER TABLE project_preparations DROP COLUMN appliance_project_id;
ALTER TABLE project_preparations ADD CONSTRAINT project_preparations_project_id_fkey FOREIGN KEY(project_id) REFERENCES projects(id)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var upgradedRows string
	if err := s.db.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(p)-'appliance_project_id' ORDER BY id)::text FROM project_preparations p`).Scan(&upgradedRows); err != nil || upgradedRows != priorRows {
		t.Fatal("native workspace reference upgrade changed retained preparations", err)
	}
	retained, err := s.Preparation(ctx, native.Preparation.ID)
	if err != nil || retained.Native == nil || *retained.Native != *native.Native || retained.State.Container != "" || retained.State.RetirementState() != execution.Retired {
		t.Fatal("native preparation identity or retirement changed on reopen", err)
	}
	private, err = s.CandidatePreparation(ctx, candidate.Preparation.ID)
	if err != nil || private.NotAfter != meta.NotAfter || !private.Stopped || private.Retirement != "confirmed" || !private.Preparation.State.Ready || private.Preparation.Native == nil || private.Preparation.State.Native == nil || *private.Preparation.Native != *native.Native || *private.Preparation.State.Native != *native.Native {
		t.Fatal("native candidate stop rewrote observed setup or was lost on reopen", err)
	}
	retained.State.Retirement = "uncertain"
	if err := s.ObservePreparation(ctx, retained); err == nil {
		t.Fatal("confirmed native retirement was downgraded")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{state,retirement}','"uncertain"') WHERE id=$1`, native.Preparation.ID); err == nil {
		t.Fatal("SQL downgraded confirmed native retirement")
	}
}
