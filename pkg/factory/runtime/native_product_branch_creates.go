package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"time"
)

type nativeBranchCreateWrite struct {
	Repository       string `json:"repository"`
	RepositoryNodeID string `json:"repository_node_id"`
	Name             string `json:"name"`
	Commit           string `json:"commit"`
}
type nativeCreatedBranch struct {
	Name   string `json:"name"`
	NodeID string `json:"node_id"`
	Commit string `json:"commit"`
}
type nativeBranchCreateFacts struct {
	Repository       string          `json:"repository"`
	Name             string          `json:"name"`
	RepositoryNodeID string          `json:"repository_node_id"`
	Archived         *bool           `json:"archived"`
	Disabled         *bool           `json:"disabled"`
	Push             *bool           `json:"push"`
	RequestedBranch  string          `json:"requested_branch"`
	RequestedCommit  string          `json:"requested_commit"`
	Branch           json.RawMessage `json:"branch"`
	Condition        string          `json:"condition"`
}
type nativeBranchCreatePreparation struct {
	Version  int                     `json:"version"`
	Account  string                  `json:"account"`
	NotAfter int64                   `json:"not_after"`
	Branch   nativeBranchCreateWrite `json:"branch"`
}
type nativeBranchCreateEffect struct {
	Kind      string                  `json:"kind"`
	CommandID string                  `json:"command_id"`
	Digest    string                  `json:"digest"`
	NotAfter  int64                   `json:"not_after"`
	Branch    nativeBranchCreateWrite `json:"branch"`
}
type nativeBranchCreateReceipt struct {
	Account           collaboration.ObservedActor `json:"account"`
	Repository        string                      `json:"repository"`
	RepositoryNodeID  string                      `json:"repository_node_id"`
	Branch            nativeCreatedBranch         `json:"branch"`
	CreationCondition string                      `json:"creation_condition"`
	ProviderResponse  nativeCreationResponse      `json:"provider_response"`
}
type nativeHumanBranchCreates struct{ *nativeProductAccount }

var _ factoryservice.HumanBranchCreateWriter = nativeHumanBranchCreates{}

func (s nativeHumanBranchCreates) readBranch(ctx context.Context, q factory.HumanBranchCreateCommand) (nativeBranchCreateFacts, error) {
	var observed nativeBranchCreateFacts
	if err := s.read(ctx, nativeRead{Kind: "branch_creation", Repository: s.repository.ExternalID, Name: q.Name, Commit: string(q.Commit)}, &observed); err != nil {
		return observed, err
	}
	if observed.Repository != s.repository.ExternalID || !nativeReviewNode(observed.RepositoryNodeID, 1024) || observed.RequestedBranch != q.Name || observed.RequestedCommit != string(q.Commit) || len(observed.Branch) == 0 || observed.Condition != "bounded_observation" {
		return observed, errNativeRead
	}
	return observed, nil
}
func (s nativeHumanBranchCreates) PrepareHumanBranchCreate(ctx context.Context, q factory.HumanBranchCreateCommand) (factory.HumanBranchCreateCapture, error) {
	var empty factory.HumanBranchCreateCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	if err := s.visible(ctx, q.Actor, q.Repository); err != nil {
		return empty, err
	}
	observed, err := s.readBranch(ctx, q)
	if err != nil {
		return empty, err
	}
	if string(observed.Branch) != "null" {
		return empty, factoryservice.ErrHumanBranchCreateConflict
	}
	if _, err := s.mapRead(ctx, observed.Repository, observed.Name, nil, nil); err != nil {
		return empty, err
	}
	p := nativeBranchCreatePreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Branch: nativeBranchCreateWrite{Repository: observed.Repository, RepositoryNodeID: observed.RepositoryNodeID, Name: q.Name, Commit: string(q.Commit)}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanBranchCreateCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanBranchCreates) decode(ctx context.Context, c factory.HumanBranchCreateCapture) (nativeBranchCreatePreparation, error) {
	var p nativeBranchCreatePreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanBranchCreateQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanBranchCreateNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Branch.Repository != s.repository.ExternalID || !nativeReviewNode(p.Branch.RepositoryNodeID, 1024) || p.Branch.Name != c.Request.Name || p.Branch.Commit != string(c.Request.Commit) {
		return p, factoryservice.ErrHumanBranchCreateConflict
	}
	return p, nil
}
func validateNativeBranchCreateReceipt(c factory.HumanBranchCreateCapture, p nativeBranchCreatePreparation, raw json.RawMessage) (nativeBranchCreateReceipt, error) {
	var r nativeBranchCreateReceipt
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != p.Branch.Repository || r.RepositoryNodeID != p.Branch.RepositoryNodeID || r.Branch.Name != c.Request.Name || r.Branch.Commit != string(c.Request.Commit) || !nativeReviewNode(r.Branch.NodeID, 1024) || r.CreationCondition != "new_ref_at_exact_commit" || r.ProviderResponse.Protocol != "github_graphql_create_ref" || r.ProviderResponse.Status != 200 || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) || r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return r, factoryservice.ErrHumanBranchCreateConflict
	}
	return r, nil
}
func (s nativeHumanBranchCreates) ReadCreatedHumanBranch(ctx context.Context, c factory.HumanBranchCreateCapture, o collaboration.OperationLookup) (collaboration.GitRef, error) {
	var empty collaboration.GitRef
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanBranchCreateObservation(c, o) != nil || o.Record == nil || o.Record.Effect != collaboration.EffectCommitted || o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return empty, factoryservice.ErrHumanBranchCreateQuery
	}
	r, err := validateNativeBranchCreateReceipt(c, p, o.Record.Receipt.Payload)
	if err != nil {
		return empty, err
	}
	observed, err := s.readBranch(ctx, c.Request)
	if err != nil {
		return empty, err
	}
	var branch nativeCreatedBranch
	if observed.RepositoryNodeID != p.Branch.RepositoryNodeID || nativeDecode(observed.Branch, &branch) != nil || branch.NodeID != r.Branch.NodeID || branch.Name != p.Branch.Name || !collaboration.ValidCommitID(collaboration.CommitID(branch.Commit)) {
		return empty, factoryservice.ErrHumanBranchCreateUnavailable
	}
	mapping, err := s.mapRead(ctx, observed.Repository, observed.Name, nil, nil)
	if err != nil {
		return empty, err
	}
	commit := collaboration.CommitID(branch.Commit)
	return collaboration.GitRef{Repository: mapping.Repository, Name: branch.Name, Commit: &commit}, nil
}
func (s nativeHumanBranchCreates) admit(ctx context.Context, q factory.HumanBranchCreateCommand) error {
	if q.Validate() != nil {
		return factoryservice.ErrHumanBranchCreateQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanBranchCreateAdmission
	}
	return s.current(ctx)
}
func nativeBranchCreateRecord(c factory.HumanBranchCreateCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateBranch, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanBranchCreates) LookupHumanBranchCreate(ctx context.Context, c factory.HumanBranchCreateCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	if _, err := s.decode(ctx, c); err != nil {
		return empty, err
	}
	state, err := s.store.HumanBranchCreateDispatchState(ctx, c)
	if err != nil {
		return empty, err
	}
	cmd, err := s.store.FactoryCommand(ctx, c.Request.ID)
	if err != nil {
		return empty, err
	}
	if cmd.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(cmd.Outcome), &prior) != nil || factory.ValidateHumanBranchCreateObservation(c, prior) != nil {
			return empty, factory.ErrHumanBranchCreateObservation
		}
		if prior.Record != nil {
			return prior, nil
		}
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupNotObserved, Reason: "local_dispatch_unclaimed"}
	if state != factory.DispatchUnclaimed {
		r := nativeBranchCreateRecord(c)
		if state == factory.DispatchLegacyUnknown {
			r.Reason = "legacy_dispatch_unknown"
		}
		o.State, o.Record = collaboration.LookupFound, &r
	}
	return o, nil
}
func (s nativeHumanBranchCreates) SubmitHumanBranchCreate(ctx context.Context, c factory.HumanBranchCreateCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanBranchCreateAdmission
	}
	won, err := s.store.ReserveHumanBranchCreateDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanBranchCreate(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeBranchCreateRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	var outcome nativeCreationReply
	scope, err := s.port.call(ctx, nativeBranchCreateEffect{Kind: "create_branch", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Branch: p.Branch}, &outcome)
	if err != nil {
		return empty, err
	}
	if scope.Connection != s.identity.Connection || scope.Repository != p.Branch.Repository || outcome.CommandID != c.OperationID || outcome.Digest != cmd.Digest {
		return empty, factoryservice.ErrHumanBranchCreateConflict
	}
	r := nativeBranchCreateRecord(c)
	switch outcome.State {
	case "created":
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanBranchCreateUnavailable
		}
		if _, err := validateNativeBranchCreateReceipt(c, p, outcome.Receipt); err != nil {
			return empty, err
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_branch_created"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	case "not_sent", "indeterminate":
		if string(outcome.Receipt) != "null" || outcome.Failure == nil || !nativeErrorCode(outcome.Failure.Code) || outcome.Failure.Status < 400 || outcome.Failure.Status > 599 {
			return empty, factoryservice.ErrHumanBranchCreateUnavailable
		}
		r.Reason = outcome.Failure.Code
		if outcome.State == "not_sent" {
			r.Effect, r.Completion = collaboration.EffectNotCommitted, collaboration.CompletionComplete
		}
	default:
		return empty, factoryservice.ErrHumanBranchCreateUnavailable
	}
	return r, r.Validate()
}
func (s nativeHumanBranchCreates) CancelHumanBranchCreate(ctx context.Context, c factory.HumanBranchCreateCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanBranchCreateDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native branch cancellation unavailable")
	}
	return *o.Record, nil
}
