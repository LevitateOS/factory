package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandHumanIssueCreate = string(collaboration.OpenIssue)
const MaxHumanIssueCreateBytes = 64 << 10
const MaxHumanIssueCreateTitleBytes = 255
const MaxHumanIssueCreateCaptureBytes = 2 << 20

// HumanIssueCreateCommand retains plain human issue intent after live caller
// admission. Canonical identities contain no predicted issue ID or index.
type HumanIssueCreateCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Title      string                     `json:"title"`
	Body       string                     `json:"body"`
}

func (q HumanIssueCreateCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 {
		return errors.New("invalid human issue creation scope")
	}
	return nil
}

// Validate preserves exact native title/body bytes. Empty bodies are valid;
// unpadded titles avoid native trimming and silent title truncation on dispatch.
func (q HumanIssueCreateCommand) Validate() error {
	if q.ValidateScope() != nil || q.Title == "" || strings.TrimSpace(q.Title) != q.Title ||
		len(q.Title) > MaxHumanIssueCreateTitleBytes || !utf8.ValidString(q.Title) || strings.ContainsRune(q.Title, 0) ||
		len(q.Body) > MaxHumanIssueCreateBytes || !utf8.ValidString(q.Body) || strings.ContainsRune(q.Body, 0) {
		return errors.New("invalid human issue creation command")
	}
	return nil
}

func (q HumanIssueCreateCommand) SameRequest(other HumanIssueCreateCommand) bool {
	return q == other
}

// HumanIssueCreateCapture is committed BEFORE dispatch. ProviderIntent is
// opaque credential-free JSON from the trusted adapter, preserved byte-for-byte
// by its byte encoding. Authority pins the provider installation/account. A
// retry never refreshes the operation, preparation, namespace or deadline.
type HumanIssueCreateCapture struct {
	Request        HumanIssueCreateCommand `json:"request"`
	Authority      string                  `json:"authority"`
	OperationID    string                  `json:"operation_id"`
	ProviderIntent []byte                  `json:"provider_intent"`
}

func (c HumanIssueCreateCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) ||
		len(c.ProviderIntent) > 512<<10 || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human issue creation preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanIssueCreate,
		Target:    string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":issues",
		Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanIssueCreateCapture verifies the envelope and existing journal
// binding. It interprets neither provider preparation nor a provider receipt.
func DecodeHumanIssueCreateCapture(cmd Command) (HumanIssueCreateCapture, error) {
	var c HumanIssueCreateCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanIssueCreate {
		return c, errors.New("command is not a human issue creation")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target ||
		want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanIssueCreateCapture{}, errors.New("human issue creation journal binding differs")
	}
	return c, nil
}
