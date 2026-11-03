package factory

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/sodaos/internal/collaboration"
)

const CommandHumanReviewRequest = string(collaboration.RequestReview)
const MaxHumanReviewRequestProviderIntentBytes = 64 << 10

const MaxHumanReviewRequestCaptureBytes = 128 << 10

// HumanReviewRequestCommand selects one canonical user or team on a PR and sets
// its desired requested state. Exactly one target is positive; teams never occupy
// the actor namespace. Capture is intent, not membership or mutation authority.
type HumanReviewRequestCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Number     int64                      `json:"number,string"`
	Reviewer   collaboration.ActorID      `json:"reviewer,string"`
	Team       collaboration.TeamID       `json:"team,string"`
	Requested  bool                       `json:"requested"`
}

func (q HumanReviewRequestCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 || q.Number <= 0 {
		return errors.New("invalid human reviewer request scope")
	}
	return nil
}

func (q HumanReviewRequestCommand) Validate() error {
	if q.ValidateScope() != nil || q.Reviewer < 0 || q.Team < 0 || (q.Reviewer > 0) == (q.Team > 0) {
		return errors.New("invalid human reviewer target")
	}
	return nil
}

func (q HumanReviewRequestCommand) SameRequest(other HumanReviewRequestCommand) bool {
	return q == other
}

// HumanReviewRequestCapture retains credential-free preparation BEFORE provider dispatch.
// Provider JSON remains opaque and byte-exact. Authority pins the installation
// or account namespace. Retries never refresh it, the deadline or operation ID.
// Preparation is bounded to 64 KiB; its base64 envelope fits within 128 KiB.
type HumanReviewRequestCapture struct {
	Request        HumanReviewRequestCommand `json:"request"`
	Authority      string                    `json:"authority"`
	OperationID    string                    `json:"operation_id"`
	ProviderIntent []byte                    `json:"provider_intent"`
}

func (c HumanReviewRequestCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) ||
		len(c.ProviderIntent) > MaxHumanReviewRequestProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human reviewer request preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanReviewRequest,
		Target:    string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":pull:" + strconv.FormatInt(q.Number, 10),
		Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanReviewRequestCapture verifies the complete journal binding. It neither
// interprets provider preparation nor authenticates a receipt or human authority.
func DecodeHumanReviewRequestCapture(cmd Command) (HumanReviewRequestCapture, error) {
	var c HumanReviewRequestCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanReviewRequest {
		return c, errors.New("command is not a human reviewer request transition")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target ||
		want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanReviewRequestCapture{}, errors.New("human reviewer request journal binding differs")
	}
	return c, nil
}
