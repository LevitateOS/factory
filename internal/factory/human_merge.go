package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/collaboration"
)

const CommandHumanMerge = string(collaboration.Merge)
const MaxHumanMergeMessageBytes = 64 << 10
const MaxHumanMergeProviderIntentBytes = 1 << 20
const MaxHumanMergeCaptureBytes = 2 << 20

// HumanMergeCommand captures product intent, not authority or a computed result.
// Manual marking selects an existing commit and has no new commit message.
// A provider must explicitly support the selected method before preparing it.
type HumanMergeCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Number     int64                      `json:"number,string"`
	Method     string                     `json:"method"`
	Message    string                     `json:"message"`
	CommitOID  string                     `json:"commit_oid"`
}

func (q HumanMergeCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 || q.Number <= 0 {
		return errors.New("invalid human merge scope")
	}
	return nil
}

func (q HumanMergeCommand) Validate() error {
	if q.ValidateScope() != nil || !utf8.ValidString(q.Message) || len(q.Message) > MaxHumanMergeMessageBytes || strings.ContainsRune(q.Message, 0) {
		return errors.New("invalid human merge request")
	}
	if q.Method == "manual" {
		if q.Message != "" || !validHumanMergeCommit(q.CommitOID) {
			return errors.New("invalid manual merge commit")
		}
		return nil
	}
	if q.CommitOID != "" {
		return errors.New("ordinary merge cannot select a result commit")
	}
	switch q.Method {
	case "merge", "rebase-merge", "squash":
		if strings.TrimSpace(q.Message) == "" {
			return errors.New("merge message is empty")
		}
	case "rebase", "fast-forward-only":
	default:
		return errors.New("unknown human merge method")
	}
	return nil
}

func validHumanMergeCommit(oid string) bool {
	if len(oid) != 40 && len(oid) != 64 {
		return false
	}
	for _, b := range []byte(oid) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return strings.Trim(oid, "0") != ""
}

func (q HumanMergeCommand) SameRequest(other HumanMergeCommand) bool { return q == other }

// Capture is persisted before dispatch. Provider preparation stays opaque and
// byte-exact; retry never refreshes its installation, deadline or operation ID.
// The 2 MiB envelope covers base64 preparation plus the escaped 64 KiB message.
type HumanMergeCapture struct {
	Request        HumanMergeCommand `json:"request"`
	Authority      string            `json:"authority"`
	OperationID    string            `json:"operation_id"`
	ProviderIntent []byte            `json:"provider_intent"`
}

func (c HumanMergeCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) || len(c.ProviderIntent) > MaxHumanMergeProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human merge preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanMerge, Target: string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":pull:" + strconv.FormatInt(q.Number, 10), Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

func DecodeHumanMergeCapture(cmd Command) (HumanMergeCapture, error) {
	var c HumanMergeCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanMerge {
		return c, errors.New("command is not a human merge")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target || want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanMergeCapture{}, errors.New("human merge journal binding differs")
	}
	return c, nil
}
