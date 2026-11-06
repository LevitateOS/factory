package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanCommentObservation = errors.New("human comment observation conflicts with captured command")

func ValidateHumanCommentObservation(c HumanCommentCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateComment, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanCommentObservation
	}
	return nil
}

func HumanCommentFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanCommentObservation(c HumanCommentCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanCommentObservation(c, old) != nil || ValidateHumanCommentObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanCommentObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanCommentObservation
	}
	return result, nil
}
