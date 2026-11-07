package control

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
)

// ReviewExecutor observes an exact candidate and submits its body and exact
// inline findings through its admitted provider actor. Agent independence is
// bound to the reviewer assignment; shared-account inspections use COMMENT,
// never a provider self-approval. Receipt adoption receives
// context for scoped identity lookup. It provides no publication capability.
type ReviewExecutor interface {
	ObserveReview(ctx context.Context, work factory.ReviewWork) (factory.ReviewObservation, error)
	SubmitReview(ctx context.Context, work factory.ReviewWork) (factory.OperationOutcome, error)
	LookupOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	CancelOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	AdoptReview(ctx context.Context, work factory.ReviewWork, outcome factory.OperationOutcome) (factory.ReviewOutcome, error)
}

// ReviewRecovery is an explicit executor guarantee, not an interpretation of
// NotObserved. Its owner must prove that every send uses its durable fence and
// may resume only the same unexpired work. An uncertain/reserved send is held.
type ReviewRecovery interface {
	ResumeReview(context.Context, factory.ReviewWork) (factory.OperationOutcome, error)
}
