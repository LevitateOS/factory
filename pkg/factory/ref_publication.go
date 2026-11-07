package factory

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandRefPublish = string(collaboration.PublishRef)

// The branch selector is shared with creation. Its expected old commit and
// distinct command kind authorize an update, never another branch creation.
type RefPublicationCapture struct {
	Request        HumanBranchCreateCommand `json:"request"`
	ExpectedOld    string                   `json:"expected_old"`
	Authority      string                   `json:"authority"`
	OperationID    string                   `json:"operation_id"`
	ProviderIntent []byte                   `json:"provider_intent"`
}

func (c RefPublicationCapture) Command() (Command, error) {
	if c.Request.Validate() != nil || !ValidCommit(c.ExpectedOld) || c.ExpectedOld == string(c.Request.Commit) || !collaboration.ValidText(c.Authority, 256) || c.OperationID != c.Request.ID || len(c.ProviderIntent) > 4096 || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid conditional ref publication")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandRefPublish, Target: string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":branches", Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}
func DecodeRefPublicationCapture(cmd Command) (RefPublicationCapture, error) {
	var c RefPublicationCapture
	if cmd.Validate() != nil || cmd.Type != CommandRefPublish || json.Unmarshal([]byte(cmd.Payload), &c) != nil {
		return c, errors.New("invalid ref publication command")
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target || want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return RefPublicationCapture{}, errors.New("ref publication journal binding differs")
	}
	return c, nil
}

var ErrRefPublicationObservation = errors.New("ref publication observation conflicts with captured command")

func ValidateRefPublicationObservation(c RefPublicationCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{host: c.Request.Host, authority: c.Authority, operation: c.OperationID, binding: collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.PublishRef, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}}
	if err != nil || !binding.valid(o) {
		return ErrRefPublicationObservation
	}
	return nil
}
func RefPublicationFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }
func AdvanceRefPublicationObservation(c RefPublicationCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateRefPublicationObservation(c, old) != nil || ValidateRefPublicationObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrRefPublicationObservation
	}
	result, err := advanceHumanCommandObservation(old, retainHumanCancellationUncertainty(old, next))
	if err != nil {
		return collaboration.OperationLookup{}, ErrRefPublicationObservation
	}
	return result, nil
}
