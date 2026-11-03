package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandHumanInline = string(collaboration.CreateReviewComment)
const MaxHumanInlineBytes = 64 << 10
const MaxHumanInlineCaptureBytes = 2 << 20
const MaxHumanInlineProviderIntentBytes = 512 << 10

// HumanInlineCommand records admitted human intent on one exact immutable
// selection. Canonical identities never contain provider numbers. Draft appends
// and immediate publication are distinct requests; neither grants authority.
type HumanInlineCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Number     int64                      `json:"number,string"`
	Anchor     ReviewThreadAnchor         `json:"anchor"`
	Draft      bool                       `json:"draft"`
	Body       string                     `json:"body"`
}

func (q HumanInlineCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 || q.Number <= 0 {
		return errors.New("invalid human inline scope")
	}
	return nil
}

func (q HumanInlineCommand) Validate() error {
	if q.ValidateScope() != nil || q.Anchor.Validate() != nil || len(q.Body) > MaxHumanInlineBytes || !utf8.ValidString(q.Body) || strings.ContainsRune(q.Body, 0) || strings.TrimSpace(q.Body) == "" {
		return errors.New("invalid human inline command")
	}
	return nil
}

func (q HumanInlineCommand) SameRequest(other HumanInlineCommand) bool { return q == other }

// Capture retains the adapter's exact credential-free provider JSON BEFORE any
// dispatch. Byte encoding preserves whitespace and integer precision. Authority
// pins the installation/account namespace; reopening never refreshes this proof,
// revision, deadline or operation identity. The store does not interpret it.
type HumanInlineCapture struct {
	Request        HumanInlineCommand `json:"request"`
	Authority      string             `json:"authority"`
	OperationID    string             `json:"operation_id"`
	ProviderIntent []byte             `json:"provider_intent"`
}

func (c HumanInlineCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) || len(c.ProviderIntent) > MaxHumanInlineProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human inline preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanInline, Target: string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":pull:" + strconv.FormatInt(q.Number, 10), Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// Decode verifies the immutable journal envelope. It neither authenticates a
// provider receipt nor reconstructs preparation from today's provider state.
func DecodeHumanInlineCapture(cmd Command) (HumanInlineCapture, error) {
	var c HumanInlineCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanInline {
		return c, errors.New("command is not a human inline")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target || want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanInlineCapture{}, errors.New("human inline journal binding differs")
	}
	return c, nil
}
