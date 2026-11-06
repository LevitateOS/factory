package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanThreadObservation = errors.New("human thread observation conflicts with captured command")

func ValidateHumanThreadObservation(c HumanThreadCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ResolveReviewThread, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanThreadObservation
	}
	return nil
}

func HumanThreadFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanThreadObservation(c HumanThreadCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanThreadObservation(c, old) != nil || ValidateHumanThreadObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanThreadObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanThreadObservation
	}
	return result, nil
}
