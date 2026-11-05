package factory

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandHumanPullCreate = string(collaboration.OpenPull)
const MaxHumanPullCreateTitleBytes = 255
const MaxHumanPullCreateBodyBytes = 64 << 10
const MaxHumanPullCreateProviderIntentBytes = 1 << 20
const MaxHumanPullCreateCaptureBytes = 2 << 20

// HumanPullCreateCommand selects existing branches in explicit canonical
// repositories. Native IDs, tips, revision and deadline belong to the winning
// provider preparation, never predicted PR IDs or implicit owned-fork selection.
type HumanPullCreateCommand struct {
	ID                  string                     `json:"id"`
	Host                collaboration.HostID       `json:"host"`
	Repository          collaboration.RepositoryID `json:"repository,string"`
	HeadRepository      collaboration.RepositoryID `json:"head_repository,string"`
	Actor               collaboration.ActorID      `json:"actor,string"`
	BaseBranch          string                     `json:"base_branch"`
	HeadBranch          string                     `json:"head_branch"`
	Title               string                     `json:"title"`
	Body                string                     `json:"body"`
	AllowMaintainerEdit bool                       `json:"allow_maintainer_edit"`
}

func (q HumanPullCreateCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.HeadRepository <= 0 || q.Actor <= 0 {
		return errors.New("invalid human pull creation scope")
	}
	return nil
}

func validHumanBranch(branch string) bool {
	if branch == "" || len("refs/heads/")+len(branch) > 512 || !utf8.ValidString(branch) || strings.TrimSpace(branch) != branch || strings.HasSuffix(branch, ".") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.ContainsAny(branch, " ~^:?*[\\") {
		return false
	}
	for _, c := range branch {
		if c < ' ' || c == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func (q HumanPullCreateCommand) Validate() error {
	if q.ValidateScope() != nil || !validHumanBranch(q.BaseBranch) || !validHumanBranch(q.HeadBranch) || q.Repository == q.HeadRepository && q.BaseBranch == q.HeadBranch ||
		q.Title == "" || strings.TrimSpace(q.Title) != q.Title || len(q.Title) > MaxHumanPullCreateTitleBytes || !utf8.ValidString(q.Title) || strings.ContainsRune(q.Title, 0) ||
		len(q.Body) > MaxHumanPullCreateBodyBytes || !utf8.ValidString(q.Body) || strings.ContainsRune(q.Body, 0) {
		return errors.New("invalid human pull creation command")
	}
	return nil
}

func (q HumanPullCreateCommand) SameRequest(other HumanPullCreateCommand) bool { return q == other }

// Capture is persisted before dispatch. The trusted adapter's credential-free
// JSON remains opaque, byte-exact and pinned to its original operation/authority.
type HumanPullCreateCapture struct {
	Request        HumanPullCreateCommand `json:"request"`
	Authority      string                 `json:"authority"`
	OperationID    string                 `json:"operation_id"`
	ProviderIntent []byte                 `json:"provider_intent"`
}

func (c HumanPullCreateCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) || len(c.ProviderIntent) > MaxHumanPullCreateProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human pull creation preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanPullCreate, Target: string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":pulls", Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

func DecodeHumanPullCreateCapture(cmd Command) (HumanPullCreateCapture, error) {
	var c HumanPullCreateCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanPullCreate {
		return c, errors.New("command is not a human pull creation")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target || want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanPullCreateCapture{}, errors.New("human pull creation journal binding differs")
	}
	return c, nil
}
