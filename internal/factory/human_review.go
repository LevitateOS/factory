package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/collaboration"
)

const CommandHumanReview = string(collaboration.SubmitReview)
const MaxHumanReviewBytes = 64 << 10
const MaxHumanReviewCaptureBytes = 2 << 20
const MaxHumanReviewProviderIntentBytes = 512 << 10

// Review events express product intent; adapters translate them to native wire
// events. Empty draft/comment prose can operate on an existing pending review.
type HumanReviewEvent string

const (
	HumanReviewDraft          HumanReviewEvent = "draft"
	HumanReviewComment        HumanReviewEvent = "comment"
	HumanReviewApprove        HumanReviewEvent = "approve"
	HumanReviewRequestChanges HumanReviewEvent = "request_changes"
)

// HumanReviewCommand is human intent after current caller admission. Canonical
// identities never contain provider numbers. This record grants no permission.
type HumanReviewCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Event      HumanReviewEvent           `json:"event"`
	Number     int64                      `json:"number,string"`
	Body       string                     `json:"body"`
}

func (q HumanReviewCommand) Validate() error {
	if q.ValidateScope() != nil || len(q.Body) > MaxHumanReviewBytes ||
		!utf8.ValidString(q.Body) || strings.ContainsRune(q.Body, 0) {
		return errors.New("invalid human review command")
	}
	switch q.Event {
	case HumanReviewDraft, HumanReviewComment, HumanReviewApprove:
	case HumanReviewRequestChanges:
		if strings.TrimSpace(q.Body) == "" {
			return errors.New("request changes requires review prose")
		}
	default:
		return errors.New("invalid human review event")
	}
	return nil
}

func (q HumanReviewCommand) SameRequest(other HumanReviewCommand) bool { return q == other }

func (q HumanReviewCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 ||
		q.Number <= 0 {
		return errors.New("invalid human review scope")
	}
	return nil
}

// HumanReviewCapture retains the exact preparation BEFORE provider dispatch.
// ProviderIntent is opaque, credential-free JSON produced by the trusted adapter;
// byte encoding preserves it verbatim through the surrounding JSON envelope.
// Authority pins the provider installation/account namespace. Reopening never
// refreshes the preparation, deadline, authority or operation identity.
type HumanReviewCapture struct {
	Request        HumanReviewCommand `json:"request"`
	Authority      string             `json:"authority"`
	OperationID    string             `json:"operation_id"`
	ProviderIntent []byte             `json:"provider_intent"`
}

func (c HumanReviewCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) ||
		len(c.ProviderIntent) > MaxHumanReviewProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human review preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanReview,
		Target:    string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":pull:" + strconv.FormatInt(q.Number, 10),
		Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanReviewCapture verifies both the envelope and its existing journal
// binding. It does not interpret provider JSON or authenticate a provider receipt.
func DecodeHumanReviewCapture(cmd Command) (HumanReviewCapture, error) {
	var c HumanReviewCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanReview {
		return c, errors.New("command is not a human review")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target ||
		want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanReviewCapture{}, errors.New("human review journal binding differs")
	}
	return c, nil
}
