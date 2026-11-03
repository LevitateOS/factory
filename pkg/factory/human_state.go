package factory

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandHumanState = string(collaboration.ApplyDiscussionState)
const MaxHumanStateProviderIntentBytes = 4096

// HumanStateCommand records direct issue/PR close/reopen after current human
// admission. Canonical identities contain no provider numbers; this is not
// permission and does not include comment posting or PR ref synchronization.
type HumanStateCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Subject    string                     `json:"subject"`
	Number     int64                      `json:"number,string"`
	Close      bool                       `json:"close"`
}

func (q HumanStateCommand) Validate() error { return q.ValidateScope() }

func (q HumanStateCommand) SameRequest(other HumanStateCommand) bool { return q == other }

func (q HumanStateCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 ||
		(q.Subject != "issue" && q.Subject != "pull") || q.Number <= 0 {
		return errors.New("invalid human state scope")
	}
	return nil
}

// HumanStateCapture retains credential-free preparation BEFORE provider dispatch.
// Provider JSON remains opaque and byte-exact. Authority pins the installation
// or account namespace. Retries never refresh it, the deadline or operation ID.
// The entire capture uses the journal's ordinary 8192-byte command bound.
type HumanStateCapture struct {
	Request        HumanStateCommand `json:"request"`
	Authority      string            `json:"authority"`
	OperationID    string            `json:"operation_id"`
	ProviderIntent []byte            `json:"provider_intent"`
}

func (c HumanStateCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) ||
		len(c.ProviderIntent) > MaxHumanStateProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human state preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanState,
		Target:    string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":" + q.Subject + ":" + strconv.FormatInt(q.Number, 10),
		Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanStateCapture verifies the complete journal binding. It neither
// interprets provider preparation nor authenticates a receipt or human authority.
func DecodeHumanStateCapture(cmd Command) (HumanStateCapture, error) {
	var c HumanStateCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanState {
		return c, errors.New("command is not a human state transition")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target ||
		want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanStateCapture{}, errors.New("human state journal binding differs")
	}
	return c, nil
}
