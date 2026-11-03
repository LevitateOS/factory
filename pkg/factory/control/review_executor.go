package control

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
)

// ReviewExecutor observes an exact candidate and submits its body and exact
// inline findings through a separate native actor. Receipt adoption receives
// context for scoped identity lookup. It provides no publication capability.
type ReviewExecutor interface {
	ObserveReview(ctx context.Context, work factory.ReviewWork) (factory.ReviewObservation, error)
	SubmitReview(ctx context.Context, work factory.ReviewWork) (factory.OperationOutcome, error)
	LookupOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	CancelOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	AdoptReview(ctx context.Context, work factory.ReviewWork, outcome factory.OperationOutcome) (factory.ReviewOutcome, error)
}
