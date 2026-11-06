package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanMergeObservation = errors.New("human merge observation conflicts with captured command")

func ValidateHumanMergeObservation(c HumanMergeCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.Merge, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanMergeObservation
	}
	return nil
}

func HumanMergeFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanMergeObservation(c HumanMergeCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanMergeObservation(c, old) != nil || ValidateHumanMergeObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanMergeObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanMergeObservation
	}
	return result, nil
}
