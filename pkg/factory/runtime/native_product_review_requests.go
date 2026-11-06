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

type nativeReviewerTarget struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
}

func (t nativeReviewerTarget) valid() bool {
	if t.Kind == "user" {
		return nativeRemoteID(t.ExternalID)
	}
	if t.Kind != "team" || !nativeObservedText(t.ExternalID, 512, false) {
		return false
	}
	decimal := true
	for _, c := range []byte(t.ExternalID) {
		if c < 33 || c > 126 {
			return false
		}
		decimal = decimal && c >= '0' && c <= '9'
	}
	return !decimal
}

type nativeReviewRequestWrite struct {
	Repository string               `json:"repository"`
	PullID     string               `json:"pull_id"`
	Number     uint64               `json:"number"`
	Target     nativeReviewerTarget `json:"target"`
	Requested  bool                 `json:"requested"`
}
type nativeReviewRequestPreparation struct {
	Version  int                      `json:"version"`
	Account  string                   `json:"account"`
	NotAfter int64                    `json:"not_after"`
	Request  nativeReviewRequestWrite `json:"request"`
}
type nativeReviewRequestEffect struct {
	Kind      string                   `json:"kind"`
	CommandID string                   `json:"command_id"`
	Digest    string                   `json:"digest"`
	NotAfter  int64                    `json:"not_after"`
	Request   nativeReviewRequestWrite `json:"request"`
}
type nativeReviewRequestReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	PullID           string                      `json:"pull_id"`
	PullNodeID       string                      `json:"pull_node_id"`
	Number           uint64                      `json:"number"`
	Target           struct {
		Kind       string `json:"kind"`
		ExternalID string `json:"external_id"`
		NodeID     string `json:"node_id"`
	} `json:"target"`
	Requested                  *bool           `json:"requested"`
	Actor                      json.RawMessage `json:"actor"`
	TargetSelector             string          `json:"target_selector"`
	SelectorSHA256             string          `json:"selector_sha256"`
	UnrelatedRequestsPreserved *uint64         `json:"unrelated_requests_preserved"`
	BeforeSHA256               string          `json:"before_sha256"`
	AfterSHA256                string          `json:"after_sha256"`
	ProviderResponse           struct {
		Protocol       string  `json:"protocol"`
		Method         string  `json:"method"`
		Status         int     `json:"status"`
		RequestID      *string `json:"request_id"`
		ResponseSHA256 string  `json:"response_sha256"`
	} `json:"provider_response"`
}
type nativeHumanReviewRequests struct{ *nativeProductAccount }

var _ factoryservice.HumanReviewRequestWriter = nativeHumanReviewRequests{}

func (s nativeHumanReviewRequests) admit(ctx context.Context, q factory.HumanReviewRequestCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanReviewRequestQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanReviewRequestAdmission
	}
	return s.current(ctx)
}

// Resolve only the canonical target identity, never membership or permission.
// Rust freshly observes that target and provider authorization at dispatch.
func (s nativeHumanReviewRequests) target(ctx context.Context, q factory.HumanReviewRequestCommand) (nativeReviewerTarget, error) {
	var t nativeReviewerTarget
	if q.Reviewer > 0 {
		r, err := s.store.CollaborationActorOnHost(ctx, q.Host, q.Reviewer)
		if err != nil {
			return t, err
		}
		t = nativeReviewerTarget{Kind: "user", ExternalID: r.ExternalID}
	} else {
		r, err := s.store.CollaborationTeamOnHost(ctx, q.Host, q.Team)
		if err != nil {
			return t, err
		}
		t = nativeReviewerTarget{Kind: "team", ExternalID: r.ExternalID}
	}
	if !t.valid() {
		return t, factoryservice.ErrHumanReviewRequestUnavailable
	}
	return t, nil
}
func (s nativeHumanReviewRequests) PrepareHumanReviewRequest(ctx context.Context, q factory.HumanReviewRequestCommand) (factory.HumanReviewRequestCapture, error) {
	var empty factory.HumanReviewRequestCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	target, err := s.target(ctx, q)
	if err != nil {
		return empty, err
	}
	page, err := (nativeCollaborationReads{s.nativeProductAccount}).ReadPulls(ctx, q.Actor, q.Repository, factoryservice.CollaborationPullQuery{Number: q.Number, Limit: 1})
	if err != nil {
		return empty, err
	}
	if len(page.Items) != 1 || !nativeRemoteID(page.Items[0].Ref.ExternalID) {
		return empty, factoryservice.ErrHumanReviewRequestAdmission
	}
	p := nativeReviewRequestPreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Request: nativeReviewRequestWrite{Repository: s.repository.ExternalID, PullID: page.Items[0].Ref.ExternalID, Number: uint64(q.Number), Target: target, Requested: q.Requested}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanReviewRequestCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanReviewRequests) decode(ctx context.Context, c factory.HumanReviewRequestCapture) (nativeReviewRequestPreparation, error) {
	var p nativeReviewRequestPreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanReviewRequestQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanReviewRequestNamespace
	}
	target, err := s.target(ctx, c.Request)
	if err != nil {
		return p, err
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Request.Repository != s.repository.ExternalID || p.Request.Number != uint64(c.Request.Number) || p.Request.Target != target || p.Request.Requested != c.Request.Requested || !nativeRemoteID(p.Request.PullID) {
		return p, factoryservice.ErrHumanReviewRequestConflict
	}
	return p, nil
}
func nativeReviewRequestRecord(c factory.HumanReviewRequestCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.RequestReview, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanReviewRequests) LookupHumanReviewRequest(ctx context.Context, c factory.HumanReviewRequestCapture) (collaboration.OperationLookup, error) {
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
func (s nativeHumanReviewRequests) SubmitHumanReviewRequest(ctx context.Context, c factory.HumanReviewRequestCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanReviewRequestAdmission
	}
	won, err := s.store.ReserveHumanReviewRequestDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanReviewRequest(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeReviewRequestRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	outcome, err := s.callHumanEffect(ctx, nativeReviewRequestEffect{Kind: "set_review_request", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Request: p.Request}, cmd, p.Request.Repository, factoryservice.ErrHumanReviewRequestConflict)
	if err != nil {
		return empty, err
	}
	r := nativeReviewRequestRecord(c)
	switch outcome.State {
	case "applied":
		var receipt nativeReviewRequestReceipt
		protocol, method, selector := "github_rest_remove_review_request", "DELETE", "name_preflight_and_recheck"
		if c.Request.Requested {
			protocol, method, selector = "github_graphql_request_review", "POST", "node_id"
		}
		if outcome.Failure != nil || len(outcome.Receipt) > 4096 || nativeDecode(outcome.Receipt, &receipt) != nil || !nativeActor(receipt.Account) || receipt.Account.ExternalID != p.Account || receipt.Repository != p.Request.Repository || receipt.PullID != p.Request.PullID || receipt.Number != p.Request.Number || receipt.Requested == nil || *receipt.Requested != p.Request.Requested || receipt.Target.Kind != p.Request.Target.Kind || receipt.Target.ExternalID != p.Request.Target.ExternalID || !nativeObservedText(receipt.Target.NodeID, 1024, false) || !nativeObservedText(receipt.RepositoryNodeID, 1024, false) || !nativeObservedText(receipt.PullNodeID, 1024, false) || receipt.TargetSelector != selector || !nativeCreationDigest(receipt.SelectorSHA256) || !nativeCreationDigest(receipt.BeforeSHA256) || !nativeCreationDigest(receipt.AfterSHA256) || receipt.UnrelatedRequestsPreserved == nil || *receipt.UnrelatedRequestsPreserved > 1000 || receipt.ProviderResponse.Protocol != protocol || receipt.ProviderResponse.Method != method || receipt.ProviderResponse.Status != 200 || !nativeCreationDigest(receipt.ProviderResponse.ResponseSHA256) {
			return empty, factoryservice.ErrHumanReviewRequestConflict
		}
		if p.Request.Target.Kind == "team" && receipt.Target.NodeID != p.Request.Target.ExternalID {
			return empty, factoryservice.ErrHumanReviewRequestConflict
		}
		if c.Request.Requested {
			var actor struct {
				ExternalID string `json:"external_id"`
				NodeID     string `json:"node_id"`
				Login      string `json:"login"`
			}
			if nativeDecode(receipt.Actor, &actor) != nil || actor.ExternalID != p.Account || !nativeObservedText(actor.NodeID, 1024, false) || !nativeObservedText(actor.Login, 512, false) {
				return empty, factoryservice.ErrHumanReviewRequestConflict
			}
		} else if string(receipt.Actor) != "null" {
			return empty, factoryservice.ErrHumanReviewRequestConflict
		}
		if receipt.ProviderResponse.RequestID != nil && !nativeObservedText(*receipt.ProviderResponse.RequestID, 256, false) {
			return empty, factoryservice.ErrHumanReviewRequestConflict
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_review_request_applied"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanReviewRequestUnavailable
		}
	}
	return r, r.Validate()
}
func (s nativeHumanReviewRequests) CancelHumanReviewRequest(ctx context.Context, c factory.HumanReviewRequestCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanReviewRequestDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native reviewer cancellation unavailable")
	}
	return *o.Record, nil
}
