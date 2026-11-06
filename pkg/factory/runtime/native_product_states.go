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

type nativeStateWrite struct {
	Repository string `json:"repository"`
	Subject    string `json:"subject"`
	SubjectID  string `json:"subject_id"`
	Number     uint64 `json:"number"`
	Close      bool   `json:"close"`
}
type nativeStatePreparation struct {
	Version  int              `json:"version"`
	Account  string           `json:"account"`
	NotAfter int64            `json:"not_after"`
	State    nativeStateWrite `json:"state"`
}
type nativeStateEffect struct {
	Kind      string           `json:"kind"`
	CommandID string           `json:"command_id"`
	Digest    string           `json:"digest"`
	NotAfter  int64            `json:"not_after"`
	State     nativeStateWrite `json:"state"`
}
type nativeStateReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	Subject          string                      `json:"subject"`
	SubjectID        string                      `json:"subject_id"`
	SubjectNodeID    string                      `json:"subject_node_id"`
	Number           uint64                      `json:"number"`
	Close            bool                        `json:"close"`
	State            string                      `json:"state"`
	URL              string                      `json:"url"`
	UpdatedAt        *time.Time                  `json:"updated_at"`
	ProviderResponse struct {
		Protocol       string  `json:"protocol"`
		Mutation       string  `json:"mutation"`
		Status         int     `json:"status"`
		RequestID      *string `json:"request_id"`
		ResponseSHA256 string  `json:"response_sha256"`
	} `json:"provider_response"`
}
type nativeHumanStates struct{ *nativeProductAccount }

var _ factoryservice.HumanStateWriter = nativeHumanStates{}

func (s nativeHumanStates) admit(ctx context.Context, q factory.HumanStateCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanStateQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanStateAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanStates) PrepareHumanState(ctx context.Context, q factory.HumanStateCommand) (factory.HumanStateCapture, error) {
	var empty factory.HumanStateCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	reads := nativeCollaborationReads{s.nativeProductAccount}
	var subject string
	if q.Subject == "issue" {
		page, err := reads.ReadIssues(ctx, q.Actor, q.Repository, factoryservice.CollaborationIssueQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return empty, err
		}
		if len(page.Items) != 1 {
			return empty, factoryservice.ErrHumanStateAdmission
		}
		subject = page.Items[0].Ref.ExternalID
	} else {
		page, err := reads.ReadPulls(ctx, q.Actor, q.Repository, factoryservice.CollaborationPullQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return empty, err
		}
		if len(page.Items) != 1 {
			return empty, factoryservice.ErrHumanStateAdmission
		}
		subject = page.Items[0].Ref.ExternalID
	}
	if !nativeRemoteID(subject) {
		return empty, factoryservice.ErrHumanStateUnavailable
	}
	p := nativeStatePreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), State: nativeStateWrite{Repository: s.repository.ExternalID, Subject: q.Subject, SubjectID: subject, Number: uint64(q.Number), Close: q.Close}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanStateCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanStates) decode(ctx context.Context, c factory.HumanStateCapture) (nativeStatePreparation, error) {
	var p nativeStatePreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanStateQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanStateNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.State.Repository != s.repository.ExternalID || p.State.Subject != c.Request.Subject || p.State.Number != uint64(c.Request.Number) || p.State.Close != c.Request.Close || !nativeRemoteID(p.State.SubjectID) {
		return p, factoryservice.ErrHumanStateConflict
	}
	return p, nil
}
func nativeStateRecord(c factory.HumanStateCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ApplyDiscussionState, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanStates) LookupHumanState(ctx context.Context, c factory.HumanStateCapture) (collaboration.OperationLookup, error) {
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
func (s nativeHumanStates) SubmitHumanState(ctx context.Context, c factory.HumanStateCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanStateAdmission
	}
	won, err := s.store.ReserveHumanStateDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanState(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeStateRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	outcome, err := s.callHumanEffect(ctx, nativeStateEffect{Kind: "set_discussion_state", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, State: p.State}, cmd, p.State.Repository, factoryservice.ErrHumanStateConflict)
	if err != nil {
		return empty, err
	}
	r := nativeStateRecord(c)
	switch outcome.State {
	case "applied":
		var receipt nativeStateReceipt
		mutation := "reopenIssue"
		state := "OPEN"
		if c.Request.Subject == "pull" {
			mutation = "reopenPullRequest"
		}
		if c.Request.Close {
			state = "CLOSED"
			mutation = "closeIssue"
			if c.Request.Subject == "pull" {
				mutation = "closePullRequest"
			}
		}
		if outcome.Failure != nil || len(outcome.Receipt) > 4096 || nativeDecode(outcome.Receipt, &receipt) != nil || !nativeActor(receipt.Account) || receipt.Account.ExternalID != p.Account || receipt.Repository != p.State.Repository || receipt.Subject != p.State.Subject || receipt.SubjectID != p.State.SubjectID || receipt.Number != p.State.Number || receipt.Close != p.State.Close || receipt.State != state || !nativeObservedText(receipt.RepositoryNodeID, 1024, false) || !nativeObservedText(receipt.SubjectNodeID, 1024, false) || !nativeObservedText(receipt.URL, 2048, false) || receipt.ProviderResponse.Protocol != "github_graphql_discussion_state" || receipt.ProviderResponse.Mutation != mutation || receipt.ProviderResponse.Status != 200 || !nativeCreationDigest(receipt.ProviderResponse.ResponseSHA256) {
			return empty, factoryservice.ErrHumanStateConflict
		}
		if receipt.ProviderResponse.RequestID != nil && !nativeObservedText(*receipt.ProviderResponse.RequestID, 256, false) {
			return empty, factoryservice.ErrHumanStateConflict
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_state_applied"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanStateUnavailable
		}
	}
	return r, r.Validate()
}
func (s nativeHumanStates) CancelHumanState(ctx context.Context, c factory.HumanStateCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanStateDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native state cancellation unavailable")
	}
	return *o.Record, nil
}
