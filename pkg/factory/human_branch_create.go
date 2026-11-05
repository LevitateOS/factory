package factory

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
)

const CommandHumanBranchCreate = string(collaboration.CreateBranch)
const MaxHumanBranchCreateProviderIntentBytes = 8 << 10
const MaxHumanBranchCreateCaptureBytes = 32 << 10

// HumanBranchCreateCommand creates a new literal branch at an exact immutable
// commit. It never grants permission to update an existing ref. Provider IDs,
// observed absence, current permission and deadline belong to the preparation.
type HumanBranchCreateCommand struct {
	ID         string                     `json:"id"`
	Host       collaboration.HostID       `json:"host"`
	Repository collaboration.RepositoryID `json:"repository,string"`
	Actor      collaboration.ActorID      `json:"actor,string"`
	Name       string                     `json:"name"`
	Commit     collaboration.CommitID     `json:"commit"`
}

func (q HumanBranchCreateCommand) ValidateScope() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Repository <= 0 || q.Actor <= 0 {
		return errors.New("invalid human branch creation scope")
	}
	return nil
}
func (q HumanBranchCreateCommand) Validate() error {
	if q.ValidateScope() != nil || !validHumanBranch(q.Name) || !collaboration.ValidCommitID(q.Commit) {
		return errors.New("invalid human branch creation command")
	}
	return nil
}
func (q HumanBranchCreateCommand) SameRequest(other HumanBranchCreateCommand) bool { return q == other }

// The trusted adapter's credential-free JSON is byte-preserved in the existing
// command journal before dispatch. Reopening never refreshes preparation.
type HumanBranchCreateCapture struct {
	Request        HumanBranchCreateCommand `json:"request"`
	Authority      string                   `json:"authority"`
	OperationID    string                   `json:"operation_id"`
	ProviderIntent []byte                   `json:"provider_intent"`
}

func (c HumanBranchCreateCapture) Command() (Command, error) {
	if err := c.Request.Validate(); err != nil {
		return Command{}, err
	}
	if !collaboration.ValidText(c.Authority, 256) || !collaboration.ValidText(c.OperationID, 256) || len(c.ProviderIntent) > MaxHumanBranchCreateProviderIntentBytes || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid human branch creation preparation")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandHumanBranchCreate, Target: string(q.Host) + ":" + strconv.FormatInt(int64(q.Repository), 10) + ":branches", Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}
func DecodeHumanBranchCreateCapture(cmd Command) (HumanBranchCreateCapture, error) {
	var c HumanBranchCreateCapture
	if err := cmd.Validate(); err != nil {
		return c, err
	}
	if cmd.Type != CommandHumanBranchCreate {
		return c, errors.New("command is not a human branch creation")
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &c); err != nil {
		return c, err
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target || want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return HumanBranchCreateCapture{}, errors.New("human branch creation journal binding differs")
	}
	return c, nil
}
