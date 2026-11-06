package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanIssueCreateObservation = errors.New("human issue creation observation conflicts with captured command")

func ValidateHumanIssueCreateObservation(c HumanIssueCreateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.OpenIssue, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanIssueCreateObservation
	}
	return nil
}

func HumanIssueCreateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanIssueCreateObservation(c HumanIssueCreateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanIssueCreateObservation(c, old) != nil || ValidateHumanIssueCreateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanIssueCreateObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanIssueCreateObservation
	}
	return result, nil
}
