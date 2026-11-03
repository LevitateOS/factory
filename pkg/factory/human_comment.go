package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandHumanComment = string(collaboration.CreateComment)
const MaxHumanCommentBytes = 64 << 10
const MaxHumanCommentCaptureBytes = 2 << 20

// HumanCommentCommand is human intent after current caller admission. Canonical
// identities never contain provider numbers. This record grants no permission.
type HumanCommentCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Subject    string                     `json:"subject"`
	Number     int64                      `json:"number,string"`
	Body       string                     `json:"body"`
}

func (q HumanCommentCommand) Validate() error {
	if q.ValidateScope() != nil || len(q.Body) > MaxHumanCommentBytes ||
		!utf8.ValidString(q.Body) || strings.ContainsRune(q.Body, 0) || strings.TrimSpace(q.Body) == "" {
		return errors.New("invalid human comment command")
	}
	return nil
}

func (q HumanCommentCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 ||
		(q.Subject != "issue" && q.Subject != "pull") || q.Number <= 0 {
		return errors.New("invalid human comment scope")
	}
	return nil
}

// HumanCommentCapture retains the exact preparation BEFORE provider dispatch.
// ProviderIntent is opaque, credential-free JSON produced by the trusted adapter;
// byte encoding preserves it verbatim through the surrounding JSON envelope.
// Authority pins the provider installation/account namespace. Reopening never
// refreshes the preparation, deadline, authority or operation identity.
type HumanCommentCapture struct {
	Request        HumanCommentCommand `json:"request"`
	Authority      string              `json:"authority"`
	OperationID    string              `json:"operation_id"`
	ProviderIntent []byte              `json:"provider_intent"`
}

func (c HumanCommentCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) ||
		len(c.ProviderIntent) > 512<<10 || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human comment preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanComment,
		Target:    string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":" + q.Subject + ":" + strconv.FormatInt(q.Number, 10),
		Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanCommentCapture verifies both the envelope and its existing journal
// binding. It does not interpret provider JSON or authenticate a provider receipt.
func DecodeHumanCommentCapture(cmd Command) (HumanCommentCapture, error) {
	var c HumanCommentCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanComment {
		return c, errors.New("command is not a human comment")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target ||
		want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanCommentCapture{}, errors.New("human comment journal binding differs")
	}
	return c, nil
}
