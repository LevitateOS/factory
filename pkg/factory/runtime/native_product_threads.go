package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeThreadWrite struct {
	Repository     string                      `json:"repository"`
	PullID         string                      `json:"pull_id"`
	Number         uint64                      `json:"number"`
	ThreadID       string                      `json:"thread_id"`
	Resolved       bool                        `json:"resolved"`
	OriginalAnchor *factory.ReviewThreadAnchor `json:"original_anchor"`
}
type nativeThreadPreparation struct {
	Version  int               `json:"version"`
	Account  string            `json:"account"`
	NotAfter int64             `json:"not_after"`
	Thread   nativeThreadWrite `json:"thread"`
}
type nativeThreadEffect struct {
	Kind      string            `json:"kind"`
	CommandID string            `json:"command_id"`
	Digest    string            `json:"digest"`
	NotAfter  int64             `json:"not_after"`
	Thread    nativeThreadWrite `json:"thread"`
}
type nativeThreadReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	PullID           string                      `json:"pull_id"`
	PullNodeID       string                      `json:"pull_node_id"`
	Number           uint64                      `json:"number"`
	ThreadID         string                      `json:"thread_id"`
	Resolved         *bool                       `json:"resolved"`
	OriginalAnchor   json.RawMessage             `json:"original_anchor"`
	Outdated         *bool                       `json:"outdated"`
	ResolvedBy       json.RawMessage             `json:"resolved_by"`
	PullURL          string                      `json:"pull_url"`
	ProviderResponse struct {
		Protocol       string  `json:"protocol"`
		Mutation       string  `json:"mutation"`
		Status         int     `json:"status"`
		RequestID      *string `json:"request_id"`
		ResponseSHA256 string  `json:"response_sha256"`
	} `json:"provider_response"`
}
type nativeHumanThreads struct{ *nativeProductAccount }

var _ factoryservice.HumanThreadWriter = nativeHumanThreads{}

func (s nativeHumanThreads) admit(ctx context.Context, q factory.HumanThreadCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) || !nativeThreadID(q.ThreadID) {
		return factoryservice.ErrHumanThreadQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanThreadAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanThreads) PrepareHumanThread(ctx context.Context, q factory.HumanThreadCommand) (factory.HumanThreadCapture, error) {
	var empty factory.HumanThreadCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	reads := nativeCollaborationReads{s.nativeProductAccount}
	page, err := (factoryservice.CollaborationThreads{Source: reads}).Read(ctx, q.Actor, q.Repository, q.Number, factoryservice.ThreadQuery{ThreadID: q.ThreadID, Limit: 1})
	if err != nil {
		return empty, err
	}
	if len(page.Items) != 1 {
		return empty, factoryservice.ErrHumanThreadAdmission
	}
	thread := page.Items[0]
	// This provider has not proved an original two-commit pair. Never accept a
	// stronger caller condition by discarding it or using today's PR references.
	if q.Anchor != nil || thread.Anchor != nil {
		return empty, factoryservice.ErrHumanThreadConflict
	}
	if !nativeRemoteID(thread.Pull.ExternalID) {
		return empty, factoryservice.ErrHumanThreadUnavailable
	}
	p := nativeThreadPreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Thread: nativeThreadWrite{Repository: s.repository.ExternalID, PullID: thread.Pull.ExternalID, Number: uint64(q.Number), ThreadID: q.ThreadID, Resolved: q.Resolved, OriginalAnchor: q.Anchor}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanThreadCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanThreads) decode(ctx context.Context, c factory.HumanThreadCapture) (nativeThreadPreparation, error) {
	var p nativeThreadPreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanThreadQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanThreadNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Thread.Repository != s.repository.ExternalID || p.Thread.Number != uint64(c.Request.Number) || p.Thread.ThreadID != c.Request.ThreadID || p.Thread.Resolved != c.Request.Resolved || !p.Thread.OriginalAnchor.Equal(c.Request.Anchor) || p.Thread.OriginalAnchor != nil || !nativeRemoteID(p.Thread.PullID) {
		return p, factoryservice.ErrHumanThreadConflict
	}
	return p, nil
}
func nativeThreadRecord(c factory.HumanThreadCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ResolveReviewThread, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanThreads) LookupHumanThread(ctx context.Context, c factory.HumanThreadCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	if _, err := s.decode(ctx, c); err != nil {
		return empty, err
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	return s.store.LookupHumanCommandDispatch(ctx, cmd)
}
func (s nativeHumanThreads) SubmitHumanThread(ctx context.Context, c factory.HumanThreadCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanThreadAdmission
	}
	won, err := s.store.ReserveHumanThreadDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanThread(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeThreadRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	outcome, err := s.callHumanEffect(ctx, nativeThreadEffect{Kind: "set_thread_resolution", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Thread: p.Thread}, cmd, p.Thread.Repository, factoryservice.ErrHumanThreadConflict)
	if err != nil {
		return empty, err
	}
	r := nativeThreadRecord(c)
	switch outcome.State {
	case "applied":
		var receipt nativeThreadReceipt
		mutation := "unresolveReviewThread"
		if c.Request.Resolved {
			mutation = "resolveReviewThread"
		}
		if outcome.Failure != nil || len(outcome.Receipt) > 4096 || nativeDecode(outcome.Receipt, &receipt) != nil || !nativeActor(receipt.Account) || receipt.Account.ExternalID != p.Account || receipt.Repository != p.Thread.Repository || receipt.PullID != p.Thread.PullID || receipt.Number != p.Thread.Number || receipt.ThreadID != p.Thread.ThreadID || receipt.Resolved == nil || *receipt.Resolved != c.Request.Resolved || receipt.Outdated == nil || string(receipt.OriginalAnchor) != "null" || !nativeObservedText(receipt.RepositoryNodeID, 1024, false) || !nativeObservedText(receipt.PullNodeID, 1024, false) || !nativeObservedText(receipt.PullURL, 2048, false) || receipt.ProviderResponse.Protocol != "github_graphql_thread_resolution" || receipt.ProviderResponse.Mutation != mutation || receipt.ProviderResponse.Status != 200 || !nativeCreationDigest(receipt.ProviderResponse.ResponseSHA256) {
			return empty, factoryservice.ErrHumanThreadConflict
		}
		if c.Request.Resolved {
			var resolver struct {
				ExternalID string `json:"external_id"`
				NodeID     string `json:"node_id"`
				Login      string `json:"login"`
			}
			if nativeDecode(receipt.ResolvedBy, &resolver) != nil || resolver.ExternalID != p.Account || !nativeObservedText(resolver.NodeID, 1024, false) || !nativeObservedText(resolver.Login, 512, false) {
				return empty, factoryservice.ErrHumanThreadConflict
			}
		} else if string(receipt.ResolvedBy) != "null" {
			return empty, factoryservice.ErrHumanThreadConflict
		}
		if receipt.ProviderResponse.RequestID != nil && !nativeObservedText(*receipt.ProviderResponse.RequestID, 256, false) {
			return empty, factoryservice.ErrHumanThreadConflict
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_thread_resolution_applied"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanThreadUnavailable
		}
	}
	return r, r.Validate()
}
func (s nativeHumanThreads) CancelHumanThread(ctx context.Context, c factory.HumanThreadCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanThreadDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native thread cancellation unavailable")
	}
	return *o.Record, nil
}

var _ factoryservice.HumanThreadReader = nativeHumanThreads{}

func (s nativeHumanThreads) ReadHumanThreadSelection(ctx context.Context, q factory.HumanThreadSelectionQuery) (factory.HumanThreadSelection, error) {
	var empty factory.HumanThreadSelection
	if q.Validate() != nil || !nativeIndex(q.Number) || !nativeThreadID(q.ThreadID) {
		return empty, factoryservice.ErrHumanThreadQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return empty, factoryservice.ErrHumanThreadAdmission
	}
	if err := s.current(ctx); err != nil {
		return empty, err
	}
	page, err := (factoryservice.CollaborationThreads{Source: nativeCollaborationReads{s.nativeProductAccount}}).Read(ctx, q.Actor, q.Repository, q.Number, factoryservice.ThreadQuery{ThreadID: q.ThreadID, Limit: 1})
	if err != nil {
		return empty, err
	}
	if len(page.Items) != 1 || page.Items[0].Anchor != nil {
		return empty, factoryservice.ErrHumanThreadUnavailable
	}
	thread := page.Items[0]
	if thread.Resolution != "resolved" && thread.Resolution != "unresolved" {
		return empty, factoryservice.ErrHumanThreadUnavailable
	}
	unavailable := collaboration.Capability{State: collaboration.Unavailable, Reason: "native_github_thread_write_unqualified"}
	return factory.HumanThreadSelection{Host: q.Host, Repository: q.Repository, Actor: q.Actor, Number: q.Number, ThreadID: q.ThreadID, OriginalAnchor: nil, Resolved: thread.Resolution == "resolved", Resolve: unavailable, Unresolve: unavailable}, nil
}
