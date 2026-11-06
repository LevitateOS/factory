package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanStateObservation = errors.New("human state observation conflicts with captured command")

func ValidateHumanStateObservation(c HumanStateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ApplyDiscussionState, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanStateObservation
	}
	return nil
}

func HumanStateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanStateObservation(c HumanStateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanStateObservation(c, old) != nil || ValidateHumanStateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanStateObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanStateObservation
	}
	return result, nil
}
