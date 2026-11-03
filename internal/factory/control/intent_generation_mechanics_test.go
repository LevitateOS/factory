package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func generateChoiceMechanics(t *testing.T, c *Coordinator, ctx context.Context, intent string, owner int64, chosen ChosenIntentPlan) (factory.IntentGeneration, error) {
	t.Helper()
	chosen.Attempt = "mechanics-fixture:" + chosen.Revision
	head, err := c.Store.IntentRevision(ctx, intent, owner)
	if err != nil {
		return factory.IntentGeneration{}, err
	}
	return c.generateAcceptedIntent(ctx, intent, head.ID, owner, &chosen, mechanicsPlanStub)
}

func mechanicsPlanStub(_ context.Context, g factory.IntentGeneration) (*DispatchWait, error) {
	if !strings.HasPrefix(g.PlanAssignment, "mechanics-fixture:") {
		return nil, errors.New("mechanics fixture lacks its explicit planning stub")
	}
	return nil, nil
}

// seedUnboundGeneration models persisted invalid provenance without asking a
// production creation API to manufacture it. It may be used to verify refusal
// or receipt reconciliation; it never supplies a valid planning binding.
func seedUnboundGeneration(t *testing.T, c *Coordinator, r factory.IntentRevision) factory.IntentGeneration {
	t.Helper()
	p, err := IntentGraphPlan(r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Graph.EncodeGraph(p, r.Repository)
	if err != nil {
		t.Fatal(err)
	}
	g := factory.IntentGeneration{RevisionID: r.ID, IntentID: r.Intent, OperationID: "intent-" + r.ID, ActorID: c.GraphActorID, NativeRev: 10, AuthRevision: r.ID + ":" + r.Digest(), NotAfter: time.Now().Add(time.Minute).Unix(), Payload: raw, SubmittedTasks: r.Tasks, IntentDigest: r.Digest(), State: "pending"}
	if err := c.Store.BeginIntentGeneration(context.Background(), g, factory.GraphPacketFacts{}); err != nil {
		t.Fatal(err)
	}
	return g
}
