package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanLockObservation = errors.New("human lock observation conflicts with captured command")

func ValidateHumanLockObservation(c HumanLockCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ApplyDiscussionLock, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanLockObservation
	}
	return nil
}

func HumanLockFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanLockObservation(c HumanLockCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanLockObservation(c, old) != nil || ValidateHumanLockObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanLockObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanLockObservation
	}
	return result, nil
}
