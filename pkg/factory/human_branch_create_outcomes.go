package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanBranchCreateObservation = errors.New("human branch creation observation conflicts with captured command")

func ValidateHumanBranchCreateObservation(c HumanBranchCreateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateBranch, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanBranchCreateObservation
	}
	return nil
}

func HumanBranchCreateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanBranchCreateObservation(c HumanBranchCreateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanBranchCreateObservation(c, old) != nil || ValidateHumanBranchCreateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanBranchCreateObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanBranchCreateObservation
	}
	return result, nil
}
