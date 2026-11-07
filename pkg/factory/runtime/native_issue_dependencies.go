package runtime

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

type nativeDependencyWrite struct {
	Repository     string   `json:"repository"`
	IssueID        string   `json:"issue_id"`
	Number         uint32   `json:"number"`
	BlockingID     string   `json:"blocking_id"`
	BlockingNumber uint32   `json:"blocking_number"`
	Expected       []string `json:"expected"`
	Remove         bool     `json:"remove"`
}
type nativeDependencyPreparation struct {
	Version    int                   `json:"version"`
	Account    string                `json:"account"`
	NotAfter   int64                 `json:"not_after"`
	Dependency nativeDependencyWrite `json:"dependency"`
}
type nativeDependencyEffect struct {
	Kind       string                `json:"kind"`
	CommandID  string                `json:"command_id"`
	Digest     string                `json:"digest"`
	NotAfter   int64                 `json:"not_after"`
	Dependency nativeDependencyWrite `json:"dependency"`
}
type nativeDependencyReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	IssueID          string                      `json:"issue_id"`
	Number           uint32                      `json:"number"`
	BlockingID       string                      `json:"blocking_id"`
	BlockingNumber   uint32                      `json:"blocking_number"`
	Removed          bool                        `json:"removed"`
	ProviderResponse struct {
		Protocol       string  `json:"protocol"`
		Mutation       string  `json:"mutation"`
		Status         int     `json:"status"`
		RequestID      *string `json:"request_id"`
		ResponseSHA256 string  `json:"response_sha256"`
	} `json:"provider_response"`
}
type nativeIssueDependencies struct{ *nativeProductAccount }

var _ factory.IssueDependencyWriter = nativeIssueDependencies{}

func (s nativeIssueDependencies) admit(ctx context.Context, q factory.IssueDependencyCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Issue.Number) || !nativeIndex(q.Blocking.Number) || q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Issue.Repository != s.repository.ID || !nativeRemoteID(q.Issue.ExternalID) || !nativeRemoteID(q.Blocking.ExternalID) {
		return errNativeReadScope
	}
	for _, id := range q.Expected {
		if !nativeRemoteID(id) {
			return errNativeReadScope
		}
	}
	return s.current(ctx)
}
func (s nativeIssueDependencies) PrepareIssueDependency(ctx context.Context, q factory.IssueDependencyCommand) (factory.IssueDependencyCapture, error) {
	if err := s.admit(ctx, q); err != nil {
		return factory.IssueDependencyCapture{}, err
	}
	p := nativeDependencyPreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Dependency: nativeDependencyWrite{Repository: s.repository.ExternalID, IssueID: q.Issue.ExternalID, Number: uint32(q.Issue.Number), BlockingID: q.Blocking.ExternalID, BlockingNumber: uint32(q.Blocking.Number), Expected: q.Expected, Remove: q.Remove}}
	raw, err := json.Marshal(p)
	if err != nil {
		return factory.IssueDependencyCapture{}, err
	}
	c := factory.IssueDependencyCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func validateNativeDependencyReceipt(c factory.IssueDependencyCapture, p nativeDependencyPreparation, raw []byte) error {
	var r nativeDependencyReceipt
	operation := "addBlockedBy"
	if p.Dependency.Remove {
		operation = "removeBlockedBy"
	}
	d := p.Dependency
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != d.Repository || r.IssueID != d.IssueID || r.Number != d.Number || r.BlockingID != d.BlockingID || r.BlockingNumber != d.BlockingNumber || r.Removed != d.Remove || !nativeObservedText(r.RepositoryNodeID, 1024, false) || r.ProviderResponse.Protocol != "github_graphql_issue_dependency" || r.ProviderResponse.Mutation != operation || r.ProviderResponse.Status != 200 || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) {
		return factory.ErrIssueDependencyObservation
	}
	if r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return factory.ErrIssueDependencyObservation
	}
	return nil
}
func (s nativeIssueDependencies) SubmitIssueDependency(ctx context.Context, c factory.IssueDependencyCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	if err := s.admit(ctx, c.Request); err != nil {
		return empty, err
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	var p nativeDependencyPreparation
	if c.Authority != s.humanAuthority() || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= time.Now().Unix() {
		return empty, errNativeReadScope
	}
	expected := nativeDependencyWrite{Repository: s.repository.ExternalID, IssueID: c.Request.Issue.ExternalID, Number: uint32(c.Request.Issue.Number), BlockingID: c.Request.Blocking.ExternalID, BlockingNumber: uint32(c.Request.Blocking.Number), Expected: c.Request.Expected, Remove: c.Request.Remove}
	if !reflect.DeepEqual(p.Dependency, expected) {
		return empty, factory.ErrIssueDependencyObservation
	}
	won, err := s.store.ReserveIssueDependency(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.store.LookupHumanCommandDispatch(ctx, cmd)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return empty, factory.ErrIssueDependencyObservation
	}
	outcome, err := s.callHumanEffect(ctx, nativeDependencyEffect{Kind: "set_issue_dependency", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Dependency: p.Dependency}, cmd, p.Dependency.Repository, factory.ErrIssueDependencyObservation)
	if err != nil {
		return empty, err
	}
	r := collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Issue.Repository, Actor: c.Request.Actor, Kind: collaboration.MutateGraph, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
	if outcome.State == "applied" {
		if outcome.Failure != nil || validateNativeDependencyReceipt(c, p, outcome.Receipt) != nil {
			return empty, factory.ErrIssueDependencyObservation
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_issue_dependency_changed"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	} else if !outcome.applyFailure(&r) {
		return empty, errNativeRead
	}
	return r, r.Validate()
}
