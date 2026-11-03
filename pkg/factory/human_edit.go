package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandHumanEdit = string(collaboration.EditDiscussion)
const MaxHumanEditBytes = 64 << 10
const MaxHumanEditTitleBytes = 255
const MaxHumanEditCaptureBytes = 2 << 20

// HumanEditBase identifies the original content reviewed by an editor. It is a
// client condition, never authority or a provider preparation. The body digest
// is SHA-256 of the exact UTF-8 Markdown exposed by the normal subject read.
type HumanEditBase struct {
	ExternalID string `json:"external_id"`
	Title      string `json:"title"`
	BodyDigest string `json:"body_digest"`
}

func (b HumanEditBase) Validate() error {
	if !collaboration.ValidText(b.ExternalID, 512) || len(b.Title) > 64<<10 || !utf8.ValidString(b.Title) || strings.ContainsRune(b.Title, 0) || len(b.BodyDigest) != 64 {
		return errors.New("invalid human edit original content")
	}
	for _, c := range b.BodyDigest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return errors.New("invalid human edit body digest")
		}
	}
	return nil
}

func sameHumanEditBase(a, b *HumanEditBase) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// HumanEditCommand is human intent after current caller admission. Canonical
// identities never contain provider numbers. This record grants no permission.
type HumanEditCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Subject    string                     `json:"subject"`
	Number     int64                      `json:"number,string"`
	Title      *string                    `json:"title,omitempty"`
	Body       *string                    `json:"body,omitempty"`
	Expected   *HumanEditBase             `json:"expected,omitempty"`
}

// Validate preserves requested field presence and bytes; an explicitly empty
// body clears the description. Provider adapters must still validate admission.
func (q HumanEditCommand) Validate() error {
	if q.ValidateScope() != nil || q.Title == nil && q.Body == nil {
		return errors.New("invalid human edit command")
	}
	if q.Title != nil && (len(*q.Title) > MaxHumanEditTitleBytes || strings.TrimSpace(*q.Title) == "" || !utf8.ValidString(*q.Title) || strings.ContainsRune(*q.Title, 0)) {
		return errors.New("invalid human edit title")
	}
	if q.Body != nil && (len(*q.Body) > MaxHumanEditBytes || !utf8.ValidString(*q.Body) || strings.ContainsRune(*q.Body, 0)) {
		return errors.New("invalid human edit body")
	}
	if q.Expected != nil {
		return q.Expected.Validate()
	}
	return nil
}

// SameRequest compares field presence and original strings, not pointer identity.
func (q HumanEditCommand) SameRequest(other HumanEditCommand) bool {
	sameField := func(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
	return q.ID == other.ID && q.Host == other.Host && q.Repository == other.Repository && q.Actor == other.Actor && q.Subject == other.Subject && q.Number == other.Number && sameField(q.Title, other.Title) && sameField(q.Body, other.Body) && sameHumanEditBase(q.Expected, other.Expected)
}

func (q HumanEditCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 ||
		(q.Subject != "issue" && q.Subject != "pull") || q.Number <= 0 {
		return errors.New("invalid human edit scope")
	}
	return nil
}

// HumanEditCapture retains the exact preparation BEFORE provider dispatch.
// ProviderIntent is opaque, credential-free JSON produced by the trusted adapter;
// byte encoding preserves it verbatim through the surrounding JSON envelope.
// Authority pins the provider installation/account namespace. Reopening never
// refreshes the preparation, deadline, authority or operation identity.
type HumanEditCapture struct {
	Request        HumanEditCommand `json:"request"`
	Authority      string           `json:"authority"`
	OperationID    string           `json:"operation_id"`
	ProviderIntent []byte           `json:"provider_intent"`
	// Observed is the trusted adapter's projection of that exact preparation,
	// supplied only when the caller requested an original-content condition.
	Observed *HumanEditBase `json:"observed,omitempty"`
}

// ExpectedCurrent compares captured facts; it does not reprepare on retry.
func (c HumanEditCapture) ExpectedCurrent() bool {
	return c.Request.Expected == nil || sameHumanEditBase(c.Request.Expected, c.Observed)
}

func (c HumanEditCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if (c.Request.Expected == nil) != (c.Observed == nil) || c.Observed != nil && c.Observed.Validate() != nil {
		return Command{}, errors.New("invalid human edit captured original content")
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) ||
		len(c.ProviderIntent) > 512<<10 || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human edit preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanEdit,
		Target:    string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":" + q.Subject + ":" + strconv.FormatInt(q.Number, 10),
		Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanEditCapture verifies both the envelope and its existing journal
// binding. It does not interpret provider JSON or authenticate a provider receipt.
func DecodeHumanEditCapture(cmd Command) (HumanEditCapture, error) {
	var c HumanEditCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanEdit {
		return c, errors.New("command is not a human edit")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target ||
		want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanEditCapture{}, errors.New("human edit journal binding differs")
	}
	return c, nil
}
