package control

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// TestDispatchPassReachesRunnableBehindWaitingPrefix proves repeated
// bounded dispatch passes rotate past a permanently waiting prefix to
// later runnable work.
func TestDispatchPassReachesRunnableBehindWaitingPrefix(t *testing.T) {
	for _, withdrawn := range []bool{false, true} {
		t.Run(fmt.Sprintf("withdrawn_authority=%t", withdrawn), func(t *testing.T) {
			dispatchBehindWaitingPrefix(t, withdrawn)
		})
	}
}

func dispatchBehindWaitingPrefix(t *testing.T, withdrawn bool) {
	t.Helper()
	db, dsn := dispatchTestDB(t)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtureDB.Close() }()
	fx := dispatchSeed(t, db)
	ctx := context.Background()
	forgetAdmissionHeader := func(issue int64) {
		t.Helper()
		if _, err := fixtureDB.ExecContext(ctx, `DELETE FROM factory_readiness_sources WHERE id=$1`, fmt.Sprintf("root:%d/%d", fx.repo, issue)); err != nil {
			t.Fatalf("remove emitted fixture header for issue %d: %v", issue, err)
		}
	}
	const waiting = MaxDispatchVisits
	base := time.Now().Add(-time.Hour)
	for i := int64(1); i <= waiting; i++ {
		issue := 100 + i
		fx.accept(t, issue, fmt.Sprintf("d%024x", issue))
		forgetAdmissionHeader(issue)
		fx.queueAt(t, issue, fx.decision[issue].ID, base.Add(time.Duration(i)*time.Second))
		if withdrawn {
			control, err := db.IssueControl(ctx, fx.repo, issue)
			if err != nil {
				t.Fatal(err)
			}
			control.Readiness, control.Reason = factory.ReadinessNotAuthorized, factory.BlockerAuthorityMissing
			control.Blockers = []factory.Blocker{acceptanceBlocker(factory.BlockerAuthorityMissing, factory.MissingDispatch)}
			control.Fingerprint = strings.Repeat("e", 64)
			if _, _, err := db.RecordIssueAssessment(ctx, control, base.Add(time.Duration(i)*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		// The accepted objective changed after admission: every pass
		// must wait on these without consuming or finishing them.
		inputs := fx.reads.inputs[fmt.Sprintf("%d/%d", fx.repo, issue)]
		inputs.Issue.TitleDigest = strings.Repeat("f", 64)
		fx.reads.inputs[fmt.Sprintf("%d/%d", fx.repo, issue)] = inputs
	}
	runnable := int64(500)
	fx.accept(t, runnable, fmt.Sprintf("d%024x", runnable))
	forgetAdmissionHeader(runnable)
	fx.queueAt(t, runnable, fx.decision[runnable].ID, base.Add(time.Duration(waiting+1)*time.Second))
	deps := fx.deps()
	// The shared traversal cursor is owned by the coordinator in
	// production; the pass advances whatever cursor it is given.
	deps.Queue = NewDispatchQueueCursor()
	for pass := 0; pass < 2; pass++ {
		report := DispatchPass(ctx, deps)
		if len(report.Errors) != 0 {
			t.Fatalf("pass %d errored: %+v", pass, report.Errors)
		}
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("repeated passes launched %d runs, want the 1 runnable issue", len(fx.host.launches))
	}
	if _, err := db.AssignmentByRun(ctx, fx.host.launches[0].Run.ID); err != nil {
		t.Fatal("launched run has no dispatch packet:", err)
	}
}
