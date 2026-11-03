package control

import (
	"context"

	"github.com/levitateos/sodaos/internal/factory"
)

type ResolutionExecutor interface {
	SubmitResolution(context.Context, factory.ResolveWork) (factory.OperationOutcome, error)
	LookupResolution(context.Context, string) (factory.OperationOutcome, error)
	CancelResolution(context.Context, string) (factory.OperationOutcome, error)
	ConfirmResolution(context.Context, factory.DeliveryResolutionOperation) (bool, error)
}
