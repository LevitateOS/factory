package factory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
)

// One relation is one atomic provider command. A complete planned graph uses
// the separate serial generation ledger and never claims provider atomicity.
const CommandIssueDependency = string(collaboration.MutateGraph)

var ErrIssueDependencyObservation = errors.New("issue dependency observation conflicts with captured command")

type IssueDependencyCommand struct {
	ID       string                 `json:"id"`
	Host     collaboration.HostID   `json:"host"`
	Actor    collaboration.ActorID  `json:"actor,string"`
	Issue    collaboration.IssueRef `json:"issue"`
	Blocking collaboration.IssueRef `json:"blocking"`
	Expected []string               `json:"expected"`
	Remove   bool                   `json:"remove"`
}

func (q IssueDependencyCommand) Validate() error {
	if !ValidID(q.ID) || !collaboration.ValidID(string(q.Host)) || q.Actor <= 0 || q.Issue.Kind != collaboration.SubjectIssue || q.Blocking.Kind != collaboration.SubjectIssue || q.Issue.Repository <= 0 || q.Issue.Repository != q.Blocking.Repository || q.Issue.Number <= 0 || q.Blocking.Number <= 0 || q.Issue.Number == q.Blocking.Number || q.Issue.ExternalID == q.Blocking.ExternalID || !collaboration.ValidText(q.Issue.ExternalID, 256) || !collaboration.ValidText(q.Blocking.ExternalID, 256) || q.Expected == nil || len(q.Expected) > 50 {
		return errors.New("invalid issue dependency command")
	}
	seen := map[string]bool{}
	for _, id := range q.Expected {
		if !collaboration.ValidText(id, 256) || seen[id] || id == q.Issue.ExternalID {
			return errors.New("invalid expected dependency set")
		}
		seen[id] = true
	}
	if seen[q.Blocking.ExternalID] != q.Remove {
		return errors.New("dependency request does not change its expected set")
	}
	return nil
}
func (q IssueDependencyCommand) SameRequest(other IssueDependencyCommand) bool {
	return reflect.DeepEqual(q, other)
}

type IssueDependencyCapture struct {
	Request        IssueDependencyCommand `json:"request"`
	Authority      string                 `json:"authority"`
	OperationID    string                 `json:"operation_id"`
	ProviderIntent []byte                 `json:"provider_intent"`
}

func (c IssueDependencyCapture) Command() (Command, error) {
	if c.Request.Validate() != nil || !collaboration.ValidText(c.Authority, 256) || c.OperationID != c.Request.ID || len(c.ProviderIntent) > 16*1024 || !json.Valid(c.ProviderIntent) {
		return Command{}, errors.New("invalid issue dependency capture")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Command{}, err
	}
	q := c.Request
	cmd := Command{ID: q.ID, Type: CommandIssueDependency, Target: string(q.Host) + ":" + strconv.FormatInt(int64(q.Issue.Repository), 10) + ":issue:" + strconv.FormatInt(q.Issue.Number, 10) + ":dependency:" + strconv.FormatInt(q.Blocking.Number, 10), Principal: strconv.FormatInt(int64(q.Actor), 10), Payload: string(raw)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}
func DecodeIssueDependencyCapture(cmd Command) (IssueDependencyCapture, error) {
	var c IssueDependencyCapture
	if cmd.Type != CommandIssueDependency || json.Unmarshal([]byte(cmd.Payload), &c) != nil {
		return c, ErrIssueDependencyObservation
	}
	want, err := c.Command()
	if err != nil || want.ID != cmd.ID || want.Type != cmd.Type || want.Target != cmd.Target || want.Principal != cmd.Principal || want.Digest != cmd.Digest || want.Payload != cmd.Payload {
		return IssueDependencyCapture{}, ErrIssueDependencyObservation
	}
	return c, nil
}
func ValidateIssueDependencyObservation(c IssueDependencyCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	binding := humanCommandObservation{host: c.Request.Host, authority: c.Authority, operation: c.OperationID, binding: collaboration.OperationBinding{Repository: c.Request.Issue.Repository, Actor: c.Request.Actor, Kind: collaboration.MutateGraph, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}}
	if err != nil || !binding.valid(o) {
		return ErrIssueDependencyObservation
	}
	return nil
}
func IssueDependencyFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }
func AdvanceIssueDependencyObservation(c IssueDependencyCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateIssueDependencyObservation(c, old) != nil || ValidateIssueDependencyObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrIssueDependencyObservation
	}
	return advanceHumanCommandObservation(old, retainHumanCancellationUncertainty(old, next))
}

// IssueDependencyWriter is implemented by the authenticated native connector.
// It carries no workflow authority and performs one fixed relation mutation.
type IssueDependencyWriter interface {
	PrepareIssueDependency(context.Context, IssueDependencyCommand) (IssueDependencyCapture, error)
	SubmitIssueDependency(context.Context, IssueDependencyCapture) (collaboration.OperationRecord, error)
}
