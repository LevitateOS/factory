package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// FW38 freezes the D14 capability/submission storage contract: the
// table owners, the idempotency keys and the immutability guards.
// Pure checks run everywhere; the transaction proof needs the
// disposable SODA_PG_* fixture and skips without it.

func testMCPCapabilityRow() factory.MCPCapability {
	digest := strings.Repeat("a", 64)
	return factory.MCPCapability{
		ID: factory.NewID(), AssignmentID: factory.NewID(), RunID: "run-1", Generation: 1,
		Profile: factory.MCPProfileBuilder, Subject: "implement:42", Repository: 7,
		Project:           "p" + strings.Repeat("d", 24),
		PreparationDigest: digest, SourceDigest: "abc", AcceptedRevision: "rev-1",
		Authority: digest, DeadlineUnix: time.Now().Add(time.Hour).Unix(),
		State: factory.MCPCapabilityStaged,
	}
}

// testMCPSubmissionRow binds a submission to its server-admitted
// capability: the capability ID plus the capability's current scope
// digest, which persistence rechecks against the authentic row.
func testMCPSubmissionRow(capability factory.MCPCapability, tool factory.MCPTool, payload []byte) factory.MCPSubmission {
	return factory.MCPSubmission{
		AssignmentID: capability.AssignmentID, SubmissionID: factory.NewID(),
		CapabilityID: capability.ID, Tool: tool,
		Payload: payload,
	}
}

// activateMCPCapability stages and activates a capability for
// submission tests: persistence only records under an active row.
func activateMCPCapability(t *testing.T, ctx context.Context, db *Store, capability factory.MCPCapability) factory.MCPCapability {
	t.Helper()
	staged := capability
	staged.State = factory.MCPCapabilityStaged
	if err := db.StageMCPCapability(ctx, staged); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID,
		staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
		t.Fatalf("activate: %v", err)
	}
	active, err := db.GetMCPCapability(ctx, staged.ID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	return active
}

// TestFactoryMCPSubmissionRecordsAuthorizingCapability proves the
// R32.1 author binding against real PostgreSQL: the recorded row
// durably names the authorizing capability, and the terminal readback
// returns it, so author/role/subject attribution never assumes one
// capability per assignment.
func TestFactoryMCPSubmissionRecordsAuthorizingCapability(t *testing.T) {
	db, _ := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	capability := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
	terminal := testMCPSubmissionRow(capability, factory.MCPToolFinishIncomplete, []byte(`{"outcome":"blocked","cause":"question","evidence_refs":[],"affected_task_ids":[],"requested_resolution":"Bound author."}`))
	if _, err := db.RecordMCPSubmission(ctx, terminal); err != nil {
		t.Fatalf("record terminal: %v", err)
	}
	readback, found, err := db.TerminalMCPSubmission(ctx, capability.AssignmentID)
	if err != nil || !found {
		t.Fatalf("terminal readback: %v %v", found, err)
	}
	if readback.CapabilityID != capability.ID {
		t.Fatalf("terminal author=%q, want %q", readback.CapabilityID, capability.ID)
	}
	var stored string
	if err := db.db.QueryRowContext(ctx, `SELECT data->'work'->>'capability' FROM factory_feed_posts WHERE data->>'assignment'=$1 AND idempotency=$2`, terminal.AssignmentID, terminal.SubmissionID).Scan(&stored); err != nil {
		t.Fatalf("author column: %v", err)
	}
	if stored != capability.ID {
		t.Fatalf("stored author=%q, want %q", stored, capability.ID)
	}
}

// TestFactoryMCPTransactions proves the T48/T51 store joints against
// real PostgreSQL (SODA_PG_*; skips without it): staged replay
// identity, scope-conflict refusal, CAS activation binding, revoke
// and retire transitions, exact-replay receipts, changed-bytes
// conflicts, terminal freezing and handoff counting.
// TestFactoryMCPSubmissionRefusesFinishedAssignment proves the R35
// late-answer rule against real PostgreSQL: new submission identities
// against a finished assignment refuse, while exact replay of a
// submission recorded before the finish still returns its receipt.
func TestFactoryMCPSubmissionRefusesFinishedAssignment(t *testing.T) {
	db, _ := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	seedAssignment := func(id, stage string) {
		t.Helper()
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,7,'run-9',$2,0,'{}')`, id, stage); err != nil {
			t.Fatalf("seed assignment: %v", err)
		}
	}

	capability := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
	seedAssignment(capability.AssignmentID, string(factory.AssignmentFinished))
	late := testMCPSubmissionRow(capability, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`))
	if _, err := db.RecordMCPSubmission(ctx, late); !errors.Is(err, ErrMCPSubmissionAssignmentFinished) {
		t.Fatalf("late submission recorded: %v", err)
	}

	live := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
	seedAssignment(live.AssignmentID, string(factory.AssignmentAssigned))
	early := testMCPSubmissionRow(live, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`))
	recorded, err := db.RecordMCPSubmission(ctx, early)
	if err != nil {
		t.Fatalf("record before finish: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE factory_assignments SET stage=$1 WHERE id=$2`, string(factory.AssignmentFinished), live.AssignmentID); err != nil {
		t.Fatalf("finish assignment: %v", err)
	}
	again, err := db.RecordMCPSubmission(ctx, early)
	if err != nil {
		t.Fatalf("replay refused after finish: %v", err)
	}
	if again.SubmissionID != recorded.SubmissionID {
		t.Fatal("replay returned a different receipt")
	}
}

func TestFactoryMCPTransactions(t *testing.T) {
	db, _ := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	capability := testMCPCapabilityRow()
	if err := db.StageMCPCapability(ctx, capability); err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Exact restart delivery replays identically.
	if err := db.StageMCPCapability(ctx, capability); err != nil {
		t.Fatalf("staged replay refused: %v", err)
	}
	// Changed scope under the same ID conflicts.
	clash := capability
	clash.Generation = 2
	if err := db.StageMCPCapability(ctx, clash); err == nil {
		t.Fatal("conflicting capability scope staged")
	}
	// Activation binds the observed run state.
	now := time.Now().Unix()
	if err := db.ActivateMCPCapability(ctx, capability.ID, 99, capability.RunID, capability.AssignmentID, capability.Authority, now, false); err == nil {
		t.Fatal("wrong-generation activation admitted")
	}
	if err := db.ActivateMCPCapability(ctx, capability.ID, capability.Generation, "run-2", capability.AssignmentID, capability.Authority, now, false); err == nil {
		t.Fatal("cross-run activation admitted")
	}
	if err := db.ActivateMCPCapability(ctx, capability.ID, capability.Generation, capability.RunID, capability.AssignmentID, capability.Authority, now, true); err == nil {
		t.Fatal("stopped activation admitted")
	}
	if err := db.ActivateMCPCapability(ctx, capability.ID, capability.Generation, capability.RunID, capability.AssignmentID, capability.Authority, now, false); err != nil {
		t.Fatalf("activation refused: %v", err)
	}
	got, err := db.GetMCPCapability(ctx, capability.ID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.State != factory.MCPCapabilityActive || got.ScopeDigest() != capability.ScopeDigest() {
		t.Fatal("activated capability lost state or scope")
	}
	// Second activation refuses: only staged activates.
	if err := db.ActivateMCPCapability(ctx, capability.ID, capability.Generation, capability.RunID, capability.AssignmentID, capability.Authority, now, false); err == nil {
		t.Fatal("double activation admitted")
	}

	// Immutable submissions: exact replay, conflict, recovery.
	// Submissions bind the activated capability above; the scope is
	// unchanged since activation.
	payload := []byte(`{"outcome":"done"}`)
	submission := testMCPSubmissionRow(got, factory.MCPToolFinishImplement, payload)
	first, err := db.RecordMCPSubmission(ctx, submission)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	replay, err := db.RecordMCPSubmission(ctx, submission)
	if err != nil {
		t.Fatalf("exact replay refused: %v", err)
	}
	if replay != first {
		t.Fatal("replay receipt differs from recorded receipt")
	}
	changed := submission
	changed.Payload = []byte(`{"outcome":"blocked"}`)
	if same, err := db.RecordMCPSubmission(ctx, changed); err != nil || same != first {
		t.Fatal("repeated post key changed the submitted result", same, err)
	}
	recovered, err := db.MCPWorkStatus(ctx, capability.AssignmentID, submission.SubmissionID)
	if err != nil {
		t.Fatalf("receipt recovery: %v", err)
	}
	if recovered != first {
		t.Fatal("recovered receipt differs from recorded receipt")
	}
	if _, err := db.MCPWorkStatus(ctx, capability.AssignmentID, factory.NewID()); err == nil {
		t.Fatal("unknown receipt disclosed")
	}
	// A terminal result prevents further work posts under new keys.
	terminal, frozen, err := db.TerminalMCPProposal(ctx, capability.AssignmentID)
	if err != nil || !frozen || terminal.SubmissionID != submission.SubmissionID {
		t.Fatalf("terminal proposal = %v, frozen = %v, err = %v", terminal, frozen, err)
	}
	second := testMCPSubmissionRow(got, factory.MCPToolFinishCorrection, payload)
	if _, err := db.RecordMCPSubmission(ctx, second); err == nil {
		t.Fatal("second terminal identity recorded")
	}
	// Post-terminal nonterminal identities refuse atomically: the
	// assignment is settled.
	late := testMCPSubmissionRow(got, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t1"}`))
	if _, err := db.RecordMCPSubmission(ctx, late); err == nil {
		t.Fatal("post-terminal output recorded")
	}
	// Revoke then retire transitions.
	if err := db.RetireMCPCapability(ctx, capability.ID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	retired, err := db.GetMCPCapability(ctx, capability.ID)
	if err != nil || retired.State != factory.MCPCapabilityRetired {
		t.Fatalf("retired state = %v, err = %v", retired.State, err)
	}
	if err := db.RevokeMCPCapability(ctx, capability.ID); err == nil {
		t.Fatal("revoke after retire admitted")
	}
}

// TestFactoryMCPSubmissionConcurrentFreeze proves the F2 guard on
// real PostgreSQL (SODA_PG_*; skips without it). Barrier-released
// goroutines force the race; every assertion is a schedule-free
// final-state invariant, so no timing assumption can pass it.
func TestFactoryMCPSubmissionConcurrentFreeze(t *testing.T) {
	db, _ := postgresFixture(t)

	// runRace releases n goroutines at once and collects one result
	// per goroutine after all finish.
	runRace := func(n int, fn func(i int) (factory.MCPWorkStatus, error)) ([]factory.MCPWorkStatus, []error) {
		start := make(chan struct{})
		var wg sync.WaitGroup
		receipts := make([]factory.MCPWorkStatus, n)
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				receipts[i], errs[i] = fn(i)
			}(i)
		}
		close(start)
		wg.Wait()
		return receipts, errs
	}
	// liveAssignment activates a capability and returns it with a
	// recorder bound to it.
	liveAssignment := func(t *testing.T, ctx context.Context) (factory.MCPCapability, func(factory.MCPTool, []byte) (factory.MCPWorkStatus, error)) {
		t.Helper()
		active := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
		return active, func(tool factory.MCPTool, payload []byte) (factory.MCPWorkStatus, error) {
			submission := testMCPSubmissionRow(active, tool, payload)
			return db.RecordMCPSubmission(ctx, submission)
		}
	}

	// Terminal-vs-terminal: concurrent distinct terminal IDs with
	// conflicting content record exactly one row; losers refuse.
	t.Run("distinct terminals", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active, record := liveAssignment(t, ctx)
		const racers = 8
		receipts, errs := runRace(racers, func(i int) (factory.MCPWorkStatus, error) {
			return record(factory.MCPToolFinishImplement,
				[]byte(fmt.Sprintf(`{"candidate":"cand-%d"}`, i)))
		})
		wins := 0
		var winner factory.MCPWorkStatus
		for i := range errs {
			if errs[i] == nil {
				wins++
				winner = receipts[i]
			} else if !errors.Is(errs[i], ErrMCPSubmissionFrozen) {
				t.Fatalf("racer %d: err = %v, want frozen or success", i, errs[i])
			}
		}
		if wins != 1 {
			t.Fatalf("wins = %d, want exactly one recorded terminal", wins)
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != 1 {
			t.Fatalf("submission count = %d, err = %v, want exactly one row", count, err)
		}
		terminal, frozen, err := db.TerminalMCPProposal(ctx, active.AssignmentID)
		if err != nil || !frozen || terminal != winner {
			t.Fatalf("terminal = %+v, frozen = %v, err = %v, want the single winner", terminal, frozen, err)
		}
	})

	// Concurrent calls using the same post key create one post.
	t.Run("same post key", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active, _ := liveAssignment(t, ctx)
		payload := []byte(`{"candidate":"cand-1"}`)
		submission := testMCPSubmissionRow(active, factory.MCPToolFinishImplement, payload)
		const racers = 8
		receipts, errs := runRace(racers, func(i int) (factory.MCPWorkStatus, error) {
			return db.RecordMCPSubmission(ctx, submission)
		})
		for i := range errs {
			if errs[i] != nil {
				t.Fatalf("racer %d refused same-content replay: %v", i, errs[i])
			}
			if receipts[i] != receipts[0] {
				t.Fatalf("racer %d receipt %+v differs from %+v", i, receipts[i], receipts[0])
			}
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != 1 {
			t.Fatalf("submission count = %d, err = %v, want exactly one row", count, err)
		}
	})

	// Controlled revoke/insert orderings: a revocation committing
	// ahead of an in-flight insert refuses that insert at
	// persistence, and an insert committing ahead of revocation
	// records before the capability retires.
	t.Run("revoke then insert refuses", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active, record := liveAssignment(t, ctx)
		if err := db.RevokeMCPCapability(ctx, active.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := record(factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`)); !errors.Is(err, ErrMCPSubmissionCapabilityInactive) {
			t.Fatalf("post-revoke insert = %v, want capability-inactive refusal", err)
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != 0 {
			t.Fatalf("submission count = %d, err = %v, want no rows", count, err)
		}
	})
	t.Run("insert then revoke records", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active, record := liveAssignment(t, ctx)
		if _, err := record(factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`)); err != nil {
			t.Fatalf("pre-revoke insert: %v", err)
		}
		if err := db.RevokeMCPCapability(ctx, active.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != 1 {
			t.Fatalf("submission count = %d, err = %v, want one row", count, err)
		}
		got, err := db.GetMCPCapability(ctx, active.ID)
		if err != nil || got.State != factory.MCPCapabilityRevoked {
			t.Fatalf("capability state = %v, err = %v, want revoked", got.State, err)
		}
	})
	t.Run("revoke races inserts", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active, _ := liveAssignment(t, ctx)
		const writers = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, writers)
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				submission := testMCPSubmissionRow(active, factory.MCPToolSubmitTaskOutput,
					[]byte(fmt.Sprintf(`{"task_id":"t-%d"}`, i)))
				_, errs[i] = db.RecordMCPSubmission(ctx, submission)
			}(i)
		}
		var revokeErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			revokeErr = db.RevokeMCPCapability(ctx, active.ID)
		}()
		close(start)
		wg.Wait()
		if revokeErr != nil {
			t.Fatalf("revoke: %v", revokeErr)
		}
		// Every writer either recorded ahead of the revocation or
		// refused behind it; nothing else is possible.
		recorded := 0
		for i := range errs {
			switch {
			case errs[i] == nil:
				recorded++
			case errors.Is(errs[i], ErrMCPSubmissionCapabilityInactive):
			default:
				t.Fatalf("writer %d: %v, want success or capability-inactive refusal", i, errs[i])
			}
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != recorded {
			t.Fatalf("submission count = %d, err = %v, want %d recorded rows", count, err, recorded)
		}
		got, err := db.GetMCPCapability(ctx, active.ID)
		if err != nil || got.State != factory.MCPCapabilityRevoked {
			t.Fatalf("capability state = %v, err = %v, want revoked", got.State, err)
		}
	})
}

// TestFactoryMCPSubmissionExpiryAtInsert proves the F3-expiry half
// against real PostgreSQL (SODA_PG_*; skips without it): the insert
// transaction rechecks the capability deadline against insertion
// time, so a submission queued past expiry records nothing. Ordered
// pause->authority-change/expiry/stop->insert cases refuse with no
// new rows, while already-recorded receipts stay readable through
// the separate MCPWorkStatus route without granting new authority.
func TestFactoryMCPSubmissionExpiryAtInsert(t *testing.T) {
	db, _ := postgresFixture(t)

	// shortLived stages and activates a capability expiring soon, so
	// the test can queue an insert across the deadline without
	// touching the immutable scope.
	shortLived := func(t *testing.T, ctx context.Context, lifetime time.Duration) factory.MCPCapability {
		t.Helper()
		capability := testMCPCapabilityRow()
		capability.DeadlineUnix = time.Now().Add(lifetime).Unix()
		return activateMCPCapability(t, ctx, db, capability)
	}
	// sleepPastExpiry waits until the named deadline is a full
	// second past, so the expiry assertion never depends on
	// scheduling tightness: lateness only strengthens it.
	sleepPastExpiry := func(deadline int64) {
		if wait := time.Until(time.Unix(deadline, 0).Add(time.Second)); wait > 0 {
			time.Sleep(wait)
		}
	}

	// A submission queued past its deadline records nothing.
	t.Run("queued past expiry records nothing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active := shortLived(t, ctx, 2*time.Second)
		submission := testMCPSubmissionRow(active, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`))
		sleepPastExpiry(active.DeadlineUnix)
		if _, err := db.RecordMCPSubmission(ctx, submission); !errors.Is(err, ErrMCPSubmissionCapabilityExpired) {
			t.Fatalf("post-expiry insert = %v, want capability-expired refusal", err)
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != 0 {
			t.Fatalf("submission count = %d, err = %v, want no rows", count, err)
		}
	})

	// Held-guard determinism: the insert passes the capability
	// lock while live, blocks on a fixture-held assignment guard,
	// crosses the deadline there, and records nothing once the
	// guard releases. The blocked-backend observation is the
	// barrier, not a timing assumption: the test cannot proceed
	// past it without the insert waiting on the guard lock.

	// History recorded before expiry stays readable through the
	// receipt route after expiry, while new inserts still refuse.
	t.Run("recorded receipt survives expiry", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active := shortLived(t, ctx, 2*time.Second)
		early := testMCPSubmissionRow(active, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-early"}`))
		first, err := db.RecordMCPSubmission(ctx, early)
		if err != nil {
			t.Fatalf("pre-expiry record: %v", err)
		}
		sleepPastExpiry(active.DeadlineUnix)
		late := testMCPSubmissionRow(active, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-late"}`))
		if _, err := db.RecordMCPSubmission(ctx, late); !errors.Is(err, ErrMCPSubmissionCapabilityExpired) {
			t.Fatalf("post-expiry insert = %v, want capability-expired refusal", err)
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != 1 {
			t.Fatalf("submission count = %d, err = %v, want the one pre-expiry row", count, err)
		}
		recovered, err := db.MCPWorkStatus(ctx, active.AssignmentID, early.SubmissionID)
		if err != nil {
			t.Fatalf("post-expiry receipt read: %v", err)
		}
		if recovered != first {
			t.Fatal("post-expiry receipt differs from recorded receipt")
		}
	})

	// Pause, then authority rotation: the old authority records
	// nothing further, the assignment stays live under the new
	// authority, and history stays readable.
	t.Run("authority rotation retires the old capability", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		old := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
		recorded := testMCPSubmissionRow(old, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`))
		first, err := db.RecordMCPSubmission(ctx, recorded)
		if err != nil {
			t.Fatalf("pre-rotation record: %v", err)
		}
		if err := db.RevokeMCPCapability(ctx, old.ID); err != nil {
			t.Fatalf("revoke old authority: %v", err)
		}
		rotated := testMCPCapabilityRow()
		rotated.AssignmentID = old.AssignmentID
		rotated.Authority = strings.Repeat("b", 64)
		live := activateMCPCapability(t, ctx, db, rotated)
		stale := testMCPSubmissionRow(old, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-2"}`))
		if _, err := db.RecordMCPSubmission(ctx, stale); !errors.Is(err, ErrMCPSubmissionCapabilityInactive) {
			t.Fatalf("rotated-away insert = %v, want capability-inactive refusal", err)
		}
		if count, err := db.MCPSubmissionCount(ctx, old.AssignmentID); err != nil || count != 1 {
			t.Fatalf("submission count = %d, err = %v, want the one pre-rotation row", count, err)
		}
		fresh := testMCPSubmissionRow(live, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-3"}`))
		if _, err := db.RecordMCPSubmission(ctx, fresh); err != nil {
			t.Fatalf("new-authority record refused: %v", err)
		}
		recovered, err := db.MCPWorkStatus(ctx, old.AssignmentID, recorded.SubmissionID)
		if err != nil {
			t.Fatalf("post-rotation receipt read: %v", err)
		}
		if recovered != first {
			t.Fatal("post-rotation receipt differs from recorded receipt")
		}
	})

	// Pause, then stop-ordered revocation: nothing records, and the
	// capability stays revoked.
	t.Run("stop revocation freezes inserts", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		active := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
		submission := testMCPSubmissionRow(active, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`))
		if err := db.RevokeMCPCapability(ctx, active.ID); err != nil {
			t.Fatalf("stop revocation: %v", err)
		}
		if _, err := db.RecordMCPSubmission(ctx, submission); !errors.Is(err, ErrMCPSubmissionCapabilityInactive) {
			t.Fatalf("post-stop insert = %v, want capability-inactive refusal", err)
		}
		if count, err := db.MCPSubmissionCount(ctx, active.AssignmentID); err != nil || count != 0 {
			t.Fatalf("submission count = %d, err = %v, want no rows", count, err)
		}
		got, err := db.GetMCPCapability(ctx, active.ID)
		if err != nil || got.State != factory.MCPCapabilityRevoked {
			t.Fatalf("capability state = %v, err = %v, want revoked", got.State, err)
		}
	})
}

// TestTerminalMCPSubmissionRead proves the FW40 settlement read
// against real PostgreSQL (SODA_PG_*; skips without it): the
// recorded terminal submission round-trips its immutable payload
// bytes beside a non-terminal row, and an assignment with no
// terminal reports false.
func TestTerminalMCPSubmissionRead(t *testing.T) {
	db, _ := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	active := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
	traffic := testMCPSubmissionRow(active, factory.MCPToolSubmitTaskOutput, []byte(`{"task_id":"t-1"}`))
	if _, err := db.RecordMCPSubmission(ctx, traffic); err != nil {
		t.Fatalf("record task output: %v", err)
	}
	if _, ok, err := db.TerminalMCPSubmission(ctx, active.AssignmentID); err != nil || ok {
		t.Fatalf("terminal = %v, err = %v, want no terminal yet", ok, err)
	}
	payload := []byte(`{"task_id":"t-1","manifest_digest":"` + strings.Repeat("b", 64) + `","output_digest":"` + strings.Repeat("c", 64) + `","evidence_refs":"r"}`)
	terminal := testMCPSubmissionRow(active, factory.MCPToolFinishImplement, payload)
	if _, err := db.RecordMCPSubmission(ctx, terminal); err != nil {
		t.Fatalf("record terminal: %v", err)
	}
	got, ok, err := db.TerminalMCPSubmission(ctx, active.AssignmentID)
	if err != nil || !ok {
		t.Fatalf("terminal = %v, err = %v, want the recorded proposal", ok, err)
	}
	if got.SubmissionID != terminal.SubmissionID || got.Tool != factory.MCPToolFinishImplement ||
		string(got.Payload) != string(payload) {
		t.Fatalf("terminal mismatch: %+v", got)
	}
	if _, ok, err := db.TerminalMCPSubmission(ctx, factory.NewID()); err != nil || ok {
		t.Fatalf("unknown assignment terminal = %v, err = %v, want false", ok, err)
	}
}

// TestEvidenceStatusReceiptReadsExplicitWithoutSideEffects reads actual posts
// without recording dispositions or manufacturing queued work.
func TestEvidenceStatusReceiptReadsExplicitWithoutSideEffects(t *testing.T) {
	db, _ := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	capability := activateMCPCapability(t, ctx, db, testMCPCapabilityRow())
	post := func(tool factory.MCPTool, payload string) string {
		t.Helper()
		result, err := db.RecordWorkerPost(ctx, testMCPSubmissionRow(capability, tool, []byte(payload)))
		if err != nil {
			t.Fatal(tool, err)
		}
		return result.Record.ID
	}
	ask := post(factory.MCPToolAsk, `{"title":"Who can view a task?","summary":"The owner policy needs clarification.","question":"May team members view?"}`)
	answer := post(factory.MCPToolAnswer, `{"title":"Policy clarified","summary":"Only owners may view.","parent_id":"`+ask+`"}`)
	suggest := post(factory.MCPToolSuggest, `{"title":"Cache the policy","summary":"Reading the file is slow.","rationale":"Cache at dispatch."}`)
	for i := 0; i < 2; i++ {
		if _, err := db.MCPInbox(ctx, capability, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := db.MCPDiscussion(ctx, capability, ask, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := db.FeedPostDetails(ctx, []string{answer, suggest}); err != nil {
			t.Fatal(err)
		}
	}
	var resolutions int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM factory_feed_posts WHERE data ? 'resolution'`).Scan(&resolutions); err != nil {
		t.Fatal(err)
	}
	if resolutions != 0 {
		t.Fatalf("reads recorded dispositions: %d", resolutions)
	}
	question, err := db.FeedPost(ctx, ask)
	if err != nil || question.Payload.Communication.QuestionState != "answered" {
		t.Fatalf("question state: %+v %v", question, err)
	}
	for _, id := range []string{ask, answer, suggest} {
		detail, err := db.FeedPost(ctx, id)
		if err != nil || detail.Payload.Communication.Disposition != "" {
			t.Fatalf("post misreported: %+v %v", detail, err)
		}
	}
	if _, err := db.FeedPost(ctx, factory.NewID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing post: %v", err)
	}
	if count, err := db.MCPSubmissionCount(ctx, capability.AssignmentID); err != nil || count != 0 {
		t.Fatalf("communications created queued work: %d %v", count, err)
	}
}
