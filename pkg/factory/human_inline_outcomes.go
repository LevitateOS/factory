package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanInlineObservation = errors.New("human inline observation conflicts with captured command")

func ValidateHumanInlineObservation(c HumanInlineCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateReviewComment, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanInlineObservation
	}
	return nil
}

func HumanInlineFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanInlineObservation(c HumanInlineCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanInlineObservation(c, old) != nil || ValidateHumanInlineObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanInlineObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanInlineObservation
	}
	return result, nil
}
