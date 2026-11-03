package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/collaboration"
)

const CommandHumanLock = string(collaboration.ApplyDiscussionLock)
const MaxHumanLockReasonBytes = 64 << 10
const MaxHumanLockCaptureBytes = 2 << 20

// HumanLockCommand is human intent after current caller admission. Canonical
// identities never contain provider numbers. This record grants no permission.
type HumanLockCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Subject    string                     `json:"subject"`
	Number     int64                      `json:"number,string"`
	Lock       bool                       `json:"lock"`
	Reason     string                     `json:"reason"`
}

// Validate preserves the exact reason, including blank text. Native configured
// reasons are checked by the provider at dispatch. Unlock carries no reason.
func (q HumanLockCommand) Validate() error {
	if q.ValidateScope() != nil || len(q.Reason) > MaxHumanLockReasonBytes || !utf8.ValidString(q.Reason) || strings.ContainsRune(q.Reason, 0) || !q.Lock && q.Reason != "" {
		return errors.New("invalid human lock command")
	}
	return nil
}

func (q HumanLockCommand) SameRequest(other HumanLockCommand) bool { return q == other }

func (q HumanLockCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 ||
		(q.Subject != "issue" && q.Subject != "pull") || q.Number <= 0 {
		return errors.New("invalid human lock scope")
	}
	return nil
}

// HumanLockCapture retains the exact preparation BEFORE provider dispatch.
// ProviderIntent is opaque, credential-free JSON produced by the trusted adapter;
// byte encoding preserves it verbatim through the surrounding JSON envelope.
// Authority pins the provider installation/account namespace. Reopening never
// refreshes the preparation, deadline, authority or operation identity.
type HumanLockCapture struct {
	Request        HumanLockCommand `json:"request"`
	Authority      string           `json:"authority"`
	OperationID    string           `json:"operation_id"`
	ProviderIntent []byte           `json:"provider_intent"`
}

func (c HumanLockCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) ||
		len(c.ProviderIntent) > 512<<10 || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human lock preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanLock,
		Target:    string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":" + q.Subject + ":" + strconv.FormatInt(q.Number, 10),
		Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanLockCapture verifies both the envelope and its existing journal
// binding. It does not interpret provider JSON or authenticate a provider receipt.
func DecodeHumanLockCapture(cmd Command) (HumanLockCapture, error) {
	var c HumanLockCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanLock {
		return c, errors.New("command is not a human lock")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target ||
		want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanLockCapture{}, errors.New("human lock journal binding differs")
	}
	return c, nil
}
