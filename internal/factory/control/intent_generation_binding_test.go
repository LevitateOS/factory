package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "forgejo.org/extension-sdk"
	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/forgejo"
	"github.com/levitateos/sodaos/internal/store"
)

func recordedGenerationFixture(t *testing.T) (*Coordinator, *generatedGraphFixture, dispatchFixture, factory.IntentRevision) {
	t.Helper()
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	c := choicePlanCoordinator(t, db, fx, &candidateChoiceStub{selected: "cand-a", confidence: 1})
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	r := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	return c, f, fx, r
}

// Persist the pre-cutover SDK encoding itself, then reopen PostgreSQL and use
// the new codec. This qualifies existing packet bytes/digests through JSONB;
// it does not claim that a native provider executed this packet.
func TestGraphCodecRetainsApprovedNativePacketAcrossStoreReopen(t *testing.T) {
	ctx := context.Background()
	db, dsn := postgresFixture(t)
	revision := broadJoinAcceptedRevision()
	if err := db.SaveIntentRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	const nativeID = int64(9007199254740999)
	key := revision.Tasks[0].Key
	legacy := sdk.IssueGraphPayload{
		Creates: []sdk.IssueGraphCreate{{Key: key, Title: "New task", Body: "Exact new body\n"}},
		Edits:   []sdk.IssueGraphEdit{{IssueID: nativeID, ExpectedTitle: "Old title", ExpectedBody: "Old body\n", ExpectedContentVersion: 0, Title: "Revised title", Body: "Revised body <with markers>\n"}},
		Dependencies: []sdk.IssueGraphDependency{
			{Issue: sdk.IssueGraphRef{Key: key}, Prerequisite: sdk.IssueGraphRef{IssueID: nativeID}},
			{Issue: sdk.IssueGraphRef{IssueID: nativeID}, Prerequisite: sdk.IssueGraphRef{IssueID: 101}, Remove: true},
		},
	}
	approved, err := json.Marshal(legacy)
	if err != nil || sdk.ValidateIssueGraphPayload(legacy) != nil {
		t.Fatal("invalid retained native packet", err)
	}
	codec := forgejo.GraphCodec{}
	plan := collaboration.GraphIntent{
		Creates: []collaboration.GraphCreate{{Key: key, Title: "New task", Body: "Exact new body\n"}},
		Edits:   []collaboration.GraphEdit{{Issue: boundGraphIssue(revision.Repository, nativeID, 2), Expected: collaboration.IssueContent{Title: "Old title", Body: "Old body\n", Revision: "0"}, Title: "Revised title", Body: "Revised body <with markers>\n"}},
		Dependencies: []collaboration.GraphDependency{
			{Issue: collaboration.GraphRef{Key: key}, Prerequisite: boundGraphRef(revision.Repository, nativeID, 2)},
			{Issue: boundGraphRef(revision.Repository, nativeID, 2), Prerequisite: boundGraphRef(revision.Repository, 101, 3), Remove: true},
		},
	}
	encoded, err := codec.EncodeGraph(plan, revision.Repository)
	if err != nil || !bytes.Equal(encoded, approved) {
		t.Fatal("neutral graph changed the existing conditional packet", err)
	}
	generation := factory.IntentGeneration{RevisionID: revision.ID, IntentID: revision.Intent, OperationID: "retained-native-packet", ActorID: 91, NativeRev: 12, AuthRevision: revision.ID, NotAfter: 2000000000, Payload: approved, SubmittedTasks: revision.Tasks, IntentDigest: revision.Digest(), State: "pending"}
	if err := db.BeginIntentGeneration(ctx, generation, factory.GraphPacketFacts{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	retained, err := reopened.IntentGeneration(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := codec.InspectGraph(retained.Payload)
	if err != nil || !bytes.Equal(facts.Canonical, approved) || packetDigest(facts.Canonical) != packetDigest(approved) || len(facts.Creates) != 1 || len(facts.Edits) != 1 || facts.Edits[nativeID].Body != "Revised body <with markers>\n" || facts.DependencyChanges != 2 {
		t.Fatal("JSONB reopen changed an approved packet or digest", facts, err)
	}
	if retained.OperationID != generation.OperationID || retained.IntentDigest != revision.Digest() {
		t.Fatal("retained operation identity changed")
	}
}

type malformedReceiptGraph struct {
	*generatedGraphFixture
	mutate func(*sdk.IssueGraphReceipt)
}

func (g *malformedReceiptGraph) SubmitGraph(ctx context.Context, generation factory.IntentGeneration, repository int64) (factory.GraphOperationOutcome, error) {
	outcome, err := g.generatedGraphFixture.SubmitGraph(ctx, generation, repository)
	if err != nil {
		return outcome, err
	}
	var receipt sdk.IssueGraphReceipt
	if err := json.Unmarshal(outcome.Receipt, &receipt); err != nil {
		return factory.GraphOperationOutcome{}, err
	}
	g.mutate(&receipt)
	outcome.Receipt, err = json.Marshal(receipt)
	return outcome, err
}

// PostgreSQL must retain an attributable commit even when receipt facts refuse
// adoption. Retrying that committed generation never replaces it or writes again.
func TestGenerationMalformedReceiptRetainsCommitWithoutAdoption(t *testing.T) {
	for _, mode := range []string{"actor", "repository", "duplicate_issue", "duplicate_key", "invalid_issue", "negative_version"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, native, _, revision := recordedGenerationFixture(t)
			c.Graph = &malformedReceiptGraph{generatedGraphFixture: native, mutate: func(receipt *sdk.IssueGraphReceipt) {
				switch mode {
				case "actor":
					receipt.ActorID++
				case "repository":
					receipt.RepositoryID++
				case "duplicate_issue":
					receipt.Issues = append(receipt.Issues, receipt.Issues[0])
				case "duplicate_key":
					entry := receipt.Issues[0]
					entry.IssueID += 1000
					receipt.Issues = append(receipt.Issues, entry)
				case "invalid_issue":
					receipt.Issues[0].IssueIndex = 0
				case "negative_version":
					receipt.Issues[0].ContentVersion = -1
				}
			}}
			generation, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, revision.Intent, revision.ID, revision.Owner)
			if err == nil || generation.State != "committed" || native.submissions != 1 {
				t.Fatal("malformed receipt adopted or lost its commit", generation, err)
			}
			saved, err := c.Store.IntentGeneration(ctx, revision.ID)
			if err != nil || saved.State != "committed" || len(saved.Receipt) == 0 || saved.OperationID != generation.OperationID {
				t.Fatal("committed provider evidence was discarded", saved, err)
			}
			if _, err := c.GenerateAcceptedIntent(ctx, revision.Intent, revision.ID, revision.Owner); err == nil || native.submissions != 1 {
				t.Fatal("receipt retry duplicated work or adopted invalid facts", err)
			}
			bindings, err := c.Store.IntentTaskBindings(ctx, revision.ID)
			if err != nil || len(bindings) != 0 {
				t.Fatal("malformed receipt acquired acceptance", bindings, err)
			}
		})
	}
}

type unresolvedGraphFixture struct {
	*generatedGraphFixture
	lookup *factory.GraphOperationLookup
}

func (g *unresolvedGraphFixture) LookupGraph(ctx context.Context, generation factory.IntentGeneration, repository int64) (factory.GraphOperationLookup, error) {
	if g.lookup != nil {
		return *g.lookup, nil
	}
	return g.generatedGraphFixture.LookupGraph(ctx, generation, repository)
}

// Real PostgreSQL retains the accepted generation while provider observations
// are unresolved. Neither a partial effect nor missing unknown/unavailable
// records may create acceptances, consume the plan, or trigger a second write.
func TestGenerationUnresolvedOutcomeRetainsOriginalOperation(t *testing.T) {
	for _, mode := range []string{"pending", "indeterminate", "partial", "unknown", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, native, _, revision := recordedGenerationFixture(t)
			chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, revision.Owner, revision)
			if err != nil || wait != nil {
				t.Fatal("recorded plan did not resolve", wait, err)
			}
			original, err := c.beginChosenIntentGeneration(ctx, revision, native.revision, &chosen)
			if err != nil {
				t.Fatal(err)
			}
			lookup := factory.GraphOperationLookup{State: collaboration.LookupFound, Record: &factory.GraphOperationOutcome{OperationID: original.OperationID, Effect: collaboration.EffectState(mode)}}
			if mode == "unknown" || mode == "unavailable" {
				lookup.State, lookup.Record = collaboration.LookupState(mode), nil
			}
			graph := &unresolvedGraphFixture{generatedGraphFixture: native, lookup: &lookup}
			c.Graph = graph
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := c.GenerateAcceptedIntent(ctx, revision.Intent, revision.ID, revision.Owner); err == nil {
					t.Fatal("unresolved operation completed", mode)
				}
			}
			saved, err := c.Store.IntentGeneration(ctx, revision.ID)
			if err != nil || saved.State != "pending" || saved.OperationID != original.OperationID || saved.PlanAssignment != original.PlanAssignment || !sameEncodedJSON(saved.Payload, original.Payload) || len(saved.Receipt) != 0 || native.submissions != 0 {
				t.Fatal("uncertain observation changed or repeated the durable operation", saved, err)
			}
			bindings, err := c.Store.IntentTaskBindings(ctx, revision.ID)
			if err != nil || len(bindings) != 0 {
				t.Fatal("uncertain observation adopted work", bindings, err)
			}
			// Once the source explicitly observes absence, ordinary accepted
			// plan/owner/revision/deadline guards may admit this exact identity.
			graph.lookup = nil
			completed, err := c.GenerateAcceptedIntent(ctx, revision.Intent, revision.ID, revision.Owner)
			if err != nil || completed.State != "completed" || completed.OperationID != original.OperationID || completed.PlanAssignment != original.PlanAssignment || native.submissions != 1 {
				t.Fatal("resolved operation failed to use its original durable identity", completed, err)
			}
		})
	}
}

func TestGenerationRequiresOriginalPlanBinding(t *testing.T) {
	for _, mode := range []string{"fresh", "pending", "committed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, f, _, r := recordedGenerationFixture(t)
			if mode == "fresh" {
				if _, err := c.GenerateAcceptedIntent(ctx, r.Intent, r.ID, r.Owner); err == nil {
					t.Fatal("resume entry created a fresh graph")
				}
				if _, err := c.GenerateAcceptedIntentFromChoice(ctx, r.Intent, r.ID, r.Owner, chosenFor(r)); err == nil {
					t.Fatal("unbound choice created a fresh graph")
				}
				if _, err := c.Store.IntentGeneration(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("refusal created durable operation: %v", err)
				}
				return
			}
			g := seedUnboundGeneration(t, c, r)
			if mode == "committed" {
				if _, err := f.SubmitGraph(ctx, g, r.Repository); err != nil {
					t.Fatal(err)
				}
			}
			before := f.submissions
			if _, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, r.Intent, r.ID, r.Owner); err == nil {
				t.Fatal("existing unbound operation acquired today's plan")
			}
			got, err := c.Store.IntentGeneration(ctx, r.ID)
			if err != nil || got.PlanAssignment != "" || f.submissions != before {
				t.Fatalf("retroactive binding or duplicate native effect: %+v, %v", got, err)
			}
			if mode == "committed" && (got.State != "committed" || len(got.Receipt) == 0) {
				t.Fatal("lost attributable committed receipt")
			}
			got.PlanAssignment = factory.NewID()
			if err := c.Store.RecordIntentGeneration(ctx, got, factory.GraphReceiptFacts{}); !errors.Is(err, store.ErrCommandConflict) {
				t.Fatalf("store allowed retrospective plan binding: %v", err)
			}
		})
	}
}

func TestGenerationRejectsTasksOutsideRecordedCandidate(t *testing.T) {
	ctx := context.Background()
	c, f, _, r := recordedGenerationFixture(t)
	chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, r.Owner, r)
	if err != nil || wait != nil {
		t.Fatalf("resolve: %v %+v", err, wait)
	}
	// These tasks fit the human scope, but differ from the recorded candidate.
	chosen.Tasks = append([]factory.IntentTask(nil), chosen.Tasks...)
	chosen.Tasks[0].Objective += " with an unrecorded variation"
	work := r
	work.Tasks = chosen.Tasks
	if err := work.Validate(); err != nil {
		t.Fatalf("fixture must remain inside accepted scope: %v", err)
	}
	if _, err := c.GenerateAcceptedIntentFromChoice(ctx, r.Intent, r.ID, r.Owner, chosen); err == nil || f.submissions != 0 {
		t.Fatalf("unrecorded tasks submitted: %d, %v", f.submissions, err)
	}
}

type movingAdoptionGraph struct {
	*generatedGraphFixture
	move func()
}

func (g *movingAdoptionGraph) ReadGeneratedIssue(ctx context.Context, repo, issue, actor int64) (factory.GeneratedIssueObservation, int64, error) {
	if g.move != nil {
		g.move()
		g.move = nil
	}
	return g.generatedGraphFixture.ReadGeneratedIssue(ctx, repo, issue, actor)
}

func TestGenerationRechecksPlanAfterNativeAdoptionReads(t *testing.T) {
	ctx := context.Background()
	c, f, fx, r := recordedGenerationFixture(t)
	c.Graph = &movingAdoptionGraph{generatedGraphFixture: f, move: func() {
		rotateChoicePlanSource(t, ctx, c.Store, fx, strings.Repeat("d", 40))
	}}
	g, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, r.Intent, r.ID, r.Owner)
	if err == nil || g.State != "committed" || f.submissions != 1 || len(g.Receipt) == 0 {
		t.Fatalf("stale adoption did not retain committed receipt: state=%s submits=%d err=%v", g.State, f.submissions, err)
	}
	bindings, err := c.Store.IntentTaskBindings(ctx, r.ID)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("stale native read adopted authority: %+v %v", bindings, err)
	}
	if _, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, r.Intent, r.ID, r.Owner); err == nil || f.submissions != 1 {
		t.Fatal("retry duplicated native work or admitted stale plan")
	}
}
