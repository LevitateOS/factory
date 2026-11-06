package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanReviewObservation = errors.New("human review observation conflicts with captured command")

func ValidateHumanReviewObservation(c HumanReviewCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.SubmitReview, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanReviewObservation
	}
	return nil
}

func HumanReviewFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanReviewObservation(c HumanReviewCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanReviewObservation(c, old) != nil || ValidateHumanReviewObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewObservation
	}
	return result, nil
}
