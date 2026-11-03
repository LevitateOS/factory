package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

func TestST10PublishableAssignmentsExcludeReviewer(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	coder := recordFinishedAssignment(t, db, now, 3, true, "completed")
	reviewRun := factory.NewID()
	report := &factory.ReviewReport{Verdict: "approve", Summary: "review complete", Body: "Reviewed the exact candidate.", Findings: []string{}}
	reviewer := coder
	reviewer.ID, reviewer.Run, reviewer.RunHistory = factory.NewID(), reviewRun, []string{reviewRun}
	reviewer.AttemptRoot, reviewer.PublicationAssignment = coder.AttemptRoot, coder.ID
	reviewer.ParentAssignment = coder.ID
	reviewer.Role = project.RoleReviewer
	reviewer.Phase = project.ExecutionReview
	reviewer.Stage, reviewer.Outcome, reviewer.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
	reviewer.Revision, reviewer.Attempts, reviewer.FinishedUnix = 0, 1, now.Unix()
	reviewer.Result = &factory.AssignmentResult{
		AssignmentID: reviewer.ID, RunID: reviewRun, Status: "completed", Summary: report.Summary,
		Candidate: reviewer.SourceCommit, Findings: []string{}, Review: report, Reported: true, RecordedUnix: now.Unix(),
	}
	if err := reviewer.Validate(); err != nil {
		t.Fatalf("reviewer fixture invalid: %v", err)
	}
	// This selector regression needs only a current typed reviewer assignment
	// linked to the coder's publication. Admission, preparation and native
	// launch are deliberately outside this fixture; their production guards
	// correctly require the coder-owned publication root.
	data, err := json.Marshal(reviewer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, reviewer.ID, reviewer.Repository, reviewer.Issue, reviewer.Run, reviewer.Stage, reviewer.Revision, string(data)); err != nil {
		t.Fatal(err)
	}
	got, err := db.PublishableAssignments(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != coder.ID {
		t.Fatal("publication queue did not select only the coder assignment")
	}
}
