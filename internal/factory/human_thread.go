package factory

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/sodaos/internal/collaboration"
)

// ResolveReviewThread sets the desired state for both resolve and unresolve.
const CommandHumanThread = string(collaboration.ResolveReviewThread)
const MaxHumanThreadCaptureBytes = 128 << 10
const MaxHumanThreadProviderIntentBytes = 64 << 10

// HumanThreadSelectionQuery is a read scope, without a command or provider proof.
type HumanThreadSelectionQuery struct {
	Host       collaboration.HostID
	Repository collaboration.RepositoryID
	Actor      collaboration.ActorID
	Number     int64
	ThreadID   string
}

func (q HumanThreadSelectionQuery) Validate() error {
	if !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 || q.Number <= 0 || !collaboration.ValidText(q.ThreadID, 1024) {
		return errors.New("invalid human thread selection scope")
	}
	return nil
}

// HumanThreadSelection exposes retained context for command construction. The
// original anchor is stored metadata, not proven Git placement; nil is explicitly
// unknown. Action facts describe this read and grant no mutation authority.
type HumanThreadSelection struct {
	Host           collaboration.HostID       `json:"host"`
	Repository     collaboration.RepositoryID `json:"repository,string"`
	Actor          collaboration.ActorID      `json:"actor,string"`
	Number         int64                      `json:"number,string"`
	ThreadID       string                     `json:"thread_id"`
	OriginalAnchor *ReviewThreadAnchor        `json:"original_anchor"`
	Resolved       bool                       `json:"resolved"`
	Resolve        collaboration.Capability   `json:"resolve"`
	Unresolve      collaboration.Capability   `json:"unresolve"`
}

func (s HumanThreadSelection) Validate(q HumanThreadSelectionQuery) error {
	if q.Validate() != nil || s.Host != q.Host || s.Repository != q.Repository || s.Actor != q.Actor || s.Number != q.Number || s.ThreadID != q.ThreadID || s.OriginalAnchor != nil && s.OriginalAnchor.Validate() != nil {
		return errors.New("invalid human thread selection")
	}
	for _, action := range []collaboration.Capability{s.Resolve, s.Unresolve} {
		switch action.State {
		case collaboration.Allowed, collaboration.Denied, collaboration.Unsupported, collaboration.Unavailable, collaboration.Unknown:
		default:
			return errors.New("invalid human thread action")
		}
		if action.Reason != "" && !collaboration.ValidText(action.Reason, 256) {
			return errors.New("invalid human thread action reason")
		}
	}
	return nil
}

// HumanThreadCommand identifies an exact provider-observed thread within a
// canonical host/repository/PR. ThreadID is opaque and host scoped. A nil anchor
// retains unknown original context; it must never be reconstructed from today.
// Captured intent grants no authority and does not establish provider state.
type HumanThreadCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Number     int64                      `json:"number,string"`
	ThreadID   string                     `json:"thread_id"`
	Anchor     *ReviewThreadAnchor        `json:"anchor"`
	Resolved   bool                       `json:"resolved"`
}

func (q HumanThreadCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 || q.Number <= 0 {
		return errors.New("invalid human thread scope")
	}
	return nil
}

func (q HumanThreadCommand) Validate() error {
	if q.ValidateScope() != nil || !collaboration.ValidText(q.ThreadID, 1024) || q.Anchor != nil && q.Anchor.Validate() != nil {
		return errors.New("invalid human thread command")
	}
	return nil
}

func (q HumanThreadCommand) SameRequest(other HumanThreadCommand) bool {
	if !q.Anchor.Equal(other.Anchor) {
		return false
	}
	q.Anchor, other.Anchor = nil, nil
	return q == other
}

// HumanThreadCapture retains the adapter's credential-free preparation BEFORE
// dispatch. Opaque bytes preserve whitespace and integer precision. Replay never
// refreshes the captured authority namespace, operation, deadline or revision.
type HumanThreadCapture struct {
	Request        HumanThreadCommand `json:"request"`
	Authority      string             `json:"authority"`
	OperationID    string             `json:"operation_id"`
	ProviderIntent []byte             `json:"provider_intent"`
}

func (c HumanThreadCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) || len(c.ProviderIntent) > MaxHumanThreadProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human thread preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanThread, Target: string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":pull:" + strconv.FormatInt(q.Number, 10), Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// DecodeHumanThreadCapture verifies the complete immutable journal envelope.
// Authenticating preparation/receipts and live admission belongs to consumers.
func DecodeHumanThreadCapture(cmd Command) (HumanThreadCapture, error) {
	var c HumanThreadCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanThread {
		return c, errors.New("command is not a human thread")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target || want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanThreadCapture{}, errors.New("human thread journal binding differs")
	}
	return c, nil
}
