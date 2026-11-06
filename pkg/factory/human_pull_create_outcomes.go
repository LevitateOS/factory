package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanPullCreateObservation = errors.New("human pull creation observation conflicts with captured command")

func ValidateHumanPullCreateObservation(c HumanPullCreateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.OpenPull, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanPullCreateObservation
	}
	return nil
}

func HumanPullCreateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanPullCreateObservation(c HumanPullCreateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanPullCreateObservation(c, old) != nil || ValidateHumanPullCreateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanPullCreateObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanPullCreateObservation
	}
	return result, nil
}
