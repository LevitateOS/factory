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

type nativeIssueCreateWrite struct {
	Repository string `json:"repository"`
	Title      string `json:"title"`
	Body       string `json:"body"`
}
type nativeIssueCreatePreparation struct {
	Version  int                    `json:"version"`
	Account  string                 `json:"account"`
	NotAfter int64                  `json:"not_after"`
	Issue    nativeIssueCreateWrite `json:"issue"`
}
type nativeIssueCreateEffect struct {
	Kind      string                 `json:"kind"`
	CommandID string                 `json:"command_id"`
	Digest    string                 `json:"digest"`
	NotAfter  int64                  `json:"not_after"`
	Issue     nativeIssueCreateWrite `json:"issue"`
}
type nativeIssueCreateReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	Issue            struct {
		ExternalID   string                       `json:"external_id"`
		NodeID       string                       `json:"node_id"`
		Number       int64                        `json:"number"`
		URL          string                       `json:"url"`
		Author       *collaboration.ObservedActor `json:"author"`
		AuthorNodeID string                       `json:"author_node_id"`
		TitleSHA256  string                       `json:"title_sha256"`
		BodySHA256   string                       `json:"body_sha256"`
		CreatedAt    *time.Time                   `json:"created_at"`
		UpdatedAt    *time.Time                   `json:"updated_at"`
	} `json:"issue"`
	ProviderResponse nativeCreationResponse `json:"provider_response"`
}
type nativeHumanIssueCreates struct{ *nativeProductAccount }

var _ factoryservice.HumanIssueCreateWriter = nativeHumanIssueCreates{}

func (s nativeHumanIssueCreates) admit(ctx context.Context, q factory.HumanIssueCreateCommand) error {
	if q.Validate() != nil {
		return factoryservice.ErrHumanIssueCreateQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanIssueCreateAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanIssueCreates) PrepareHumanIssueCreate(ctx context.Context, q factory.HumanIssueCreateCommand) (factory.HumanIssueCreateCapture, error) {
	var empty factory.HumanIssueCreateCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	if err := s.visible(ctx, q.Actor, q.Repository); err != nil {
		return empty, err
	}
	p := nativeIssueCreatePreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Issue: nativeIssueCreateWrite{Repository: s.repository.ExternalID, Title: q.Title, Body: q.Body}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanIssueCreateCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanIssueCreates) decode(ctx context.Context, c factory.HumanIssueCreateCapture) (nativeIssueCreatePreparation, error) {
	var p nativeIssueCreatePreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanIssueCreateQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanIssueCreateNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Issue.Repository != s.repository.ExternalID || p.Issue.Title != c.Request.Title || p.Issue.Body != c.Request.Body {
		return p, factoryservice.ErrHumanIssueCreateConflict
	}
	return p, nil
}
func nativeIssueCreateRecord(c factory.HumanIssueCreateCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.OpenIssue, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanIssueCreates) LookupHumanIssueCreate(ctx context.Context, c factory.HumanIssueCreateCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	if _, err := s.decode(ctx, c); err != nil {
		return empty, err
	}
	state, err := s.store.HumanIssueCreateDispatchState(ctx, c)
	if err != nil {
		return empty, err
	}
	cmd, err := s.store.FactoryCommand(ctx, c.Request.ID)
	if err != nil {
		return empty, err
	}
	if cmd.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(cmd.Outcome), &prior) != nil || factory.ValidateHumanIssueCreateObservation(c, prior) != nil {
			return empty, factory.ErrHumanIssueCreateObservation
		}
		if prior.Record != nil {
			return prior, nil
		}
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupNotObserved, Reason: "local_dispatch_unclaimed"}
	if state != factory.DispatchUnclaimed {
		r := nativeIssueCreateRecord(c)
		if state == factory.DispatchLegacyUnknown {
			r.Reason = "legacy_dispatch_unknown"
		}
		o.State, o.Record = collaboration.LookupFound, &r
	}
	return o, nil
}
func validateNativeIssueCreateReceipt(c factory.HumanIssueCreateCapture, p nativeIssueCreatePreparation, raw json.RawMessage) (nativeIssueCreateReceipt, error) {
	var r nativeIssueCreateReceipt
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != p.Issue.Repository || !nativeObservedText(r.RepositoryNodeID, 1024, false) || !nativeRemoteID(r.Issue.ExternalID) || !nativeObservedText(r.Issue.NodeID, 1024, false) || r.Issue.Number <= 0 || r.Issue.Author == nil || !nativeActor(*r.Issue.Author) || r.Issue.Author.ExternalID != p.Account || !nativeObservedText(r.Issue.AuthorNodeID, 1024, false) || r.Issue.TitleSHA256 != nativeCreationSHA([]byte(c.Request.Title)) || r.Issue.BodySHA256 != nativeCreationSHA([]byte(c.Request.Body)) || r.ProviderResponse.Protocol != "github_graphql_create_issue" || r.ProviderResponse.Status != 200 || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) {
		return r, factoryservice.ErrHumanIssueCreateConflict
	}
	if r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return r, factoryservice.ErrHumanIssueCreateConflict
	}
	return r, nil
}
func (s nativeHumanIssueCreates) SubmitHumanIssueCreate(ctx context.Context, c factory.HumanIssueCreateCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanIssueCreateAdmission
	}
	won, err := s.store.ReserveHumanIssueCreateDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanIssueCreate(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeIssueCreateRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	var outcome nativeCreationReply
	scope, err := s.port.call(ctx, nativeIssueCreateEffect{Kind: "create_issue", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Issue: p.Issue}, &outcome)
	if err != nil {
		return empty, err
	}
	if scope.Connection != s.identity.Connection || scope.Repository != p.Issue.Repository || outcome.CommandID != c.OperationID || outcome.Digest != cmd.Digest {
		return empty, factoryservice.ErrHumanIssueCreateConflict
	}
	r := nativeIssueCreateRecord(c)
	switch outcome.State {
	case "created":
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanIssueCreateUnavailable
		}
		if _, err := validateNativeIssueCreateReceipt(c, p, outcome.Receipt); err != nil {
			return empty, err
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_issue_created"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	case "not_sent", "indeterminate":
		if string(outcome.Receipt) != "null" || outcome.Failure == nil || !nativeErrorCode(outcome.Failure.Code) || outcome.Failure.Status < 400 || outcome.Failure.Status > 599 {
			return empty, factoryservice.ErrHumanIssueCreateUnavailable
		}
		r.Reason = outcome.Failure.Code
		if outcome.State == "not_sent" {
			r.Effect, r.Completion = collaboration.EffectNotCommitted, collaboration.CompletionComplete
		}
	default:
		return empty, factoryservice.ErrHumanIssueCreateUnavailable
	}
	return r, r.Validate()
}
func (s nativeHumanIssueCreates) CancelHumanIssueCreate(ctx context.Context, c factory.HumanIssueCreateCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanIssueCreateDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native issue cancellation unavailable")
	}
	return *o.Record, nil
}
func (s nativeHumanIssueCreates) ReadCreatedHumanIssue(ctx context.Context, c factory.HumanIssueCreateCapture, o collaboration.OperationLookup) (collaboration.IssueRef, error) {
	var empty collaboration.IssueRef
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanIssueCreateObservation(c, o) != nil || o.Record == nil || o.Record.Effect != collaboration.EffectCommitted || o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return empty, factoryservice.ErrHumanIssueCreateQuery
	}
	r, err := validateNativeIssueCreateReceipt(c, p, o.Record.Receipt.Payload)
	if err != nil {
		return empty, err
	}
	page, err := (nativeCollaborationReads{s.nativeProductAccount}).ReadIssues(ctx, c.Request.Actor, c.Request.Repository, factoryservice.CollaborationIssueQuery{Number: r.Issue.Number, Limit: 1})
	if err != nil {
		return empty, err
	}
	if len(page.Items) != 1 || page.Items[0].Ref.ExternalID != r.Issue.ExternalID || page.Items[0].Ref.Repository != c.Request.Repository || page.Items[0].Ref.Number != r.Issue.Number {
		return empty, factoryservice.ErrHumanIssueCreateUnavailable
	}
	return page.Items[0].Ref, nil
}
