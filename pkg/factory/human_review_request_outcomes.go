package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanReviewRequestObservation = errors.New("human reviewer observation conflicts with captured command")

func ValidateHumanReviewRequestObservation(c HumanReviewRequestCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.RequestReview, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanReviewRequestObservation
	}
	return nil
}

func HumanReviewRequestFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanReviewRequestObservation(c HumanReviewRequestCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanReviewRequestObservation(c, old) != nil || ValidateHumanReviewRequestObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewRequestObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewRequestObservation
	}
	return result, nil
}
