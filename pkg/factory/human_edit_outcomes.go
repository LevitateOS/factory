package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanEditObservation = errors.New("human edit observation conflicts with captured command")

func ValidateHumanEditObservation(c HumanEditCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{
		host: c.Request.Host, authority: c.Authority, operation: c.OperationID,
		binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.EditDiscussion, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
	}
	if err != nil || !binding.valid(o) {
		return ErrHumanEditObservation
	}
	if o.Record != nil && o.Record.Effect == collaboration.EffectCommitted && !c.ExpectedCurrent() {
		return ErrHumanEditObservation
	}
	return nil
}

func HumanEditFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

func AdvanceHumanEditObservation(c HumanEditCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanEditObservation(c, old) != nil || ValidateHumanEditObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanEditObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanEditObservation
	}
	return result, nil
}
