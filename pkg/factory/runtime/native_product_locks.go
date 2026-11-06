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

type nativeLockWrite struct {
	Repository string `json:"repository"`
	Subject    string `json:"subject"`
	SubjectID  string `json:"subject_id"`
	Number     uint64 `json:"number"`
	Lock       bool   `json:"lock"`
	Reason     string `json:"reason"`
}
type nativeLockPreparation struct {
	Version  int             `json:"version"`
	Account  string          `json:"account"`
	NotAfter int64           `json:"not_after"`
	Lock     nativeLockWrite `json:"lock"`
}
type nativeLockEffect struct {
	Kind      string          `json:"kind"`
	CommandID string          `json:"command_id"`
	Digest    string          `json:"digest"`
	NotAfter  int64           `json:"not_after"`
	Lock      nativeLockWrite `json:"lock"`
}
type nativeLockReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	Subject          string                      `json:"subject"`
	SubjectID        string                      `json:"subject_id"`
	SubjectNodeID    string                      `json:"subject_node_id"`
	Number           uint64                      `json:"number"`
	Locked           *bool                       `json:"locked"`
	Reason           string                      `json:"reason"`
	ActiveLockReason json.RawMessage             `json:"active_lock_reason"`
	Actor            collaboration.ObservedActor `json:"actor"`
	ActorNodeID      string                      `json:"actor_node_id"`
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
type nativeHumanLocks struct{ *nativeProductAccount }

var _ factoryservice.HumanLockWriter = nativeHumanLocks{}

func (s nativeHumanLocks) admit(ctx context.Context, q factory.HumanLockCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanLockQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanLockAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanLocks) PrepareHumanLock(ctx context.Context, q factory.HumanLockCommand) (factory.HumanLockCapture, error) {
	var empty factory.HumanLockCapture
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
			return empty, factoryservice.ErrHumanLockAdmission
		}
		subject = page.Items[0].Ref.ExternalID
	} else {
		page, err := reads.ReadPulls(ctx, q.Actor, q.Repository, factoryservice.CollaborationPullQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return empty, err
		}
		if len(page.Items) != 1 {
			return empty, factoryservice.ErrHumanLockAdmission
		}
		subject = page.Items[0].Ref.ExternalID
	}
	if !nativeRemoteID(subject) {
		return empty, factoryservice.ErrHumanLockUnavailable
	}
	p := nativeLockPreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Lock: nativeLockWrite{Repository: s.repository.ExternalID, Subject: q.Subject, SubjectID: subject, Number: uint64(q.Number), Lock: q.Lock, Reason: q.Reason}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanLockCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanLocks) decode(ctx context.Context, c factory.HumanLockCapture) (nativeLockPreparation, error) {
	var p nativeLockPreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanLockQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanLockNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Lock.Repository != s.repository.ExternalID || p.Lock.Subject != c.Request.Subject || p.Lock.Number != uint64(c.Request.Number) || p.Lock.Lock != c.Request.Lock || p.Lock.Reason != c.Request.Reason || !nativeRemoteID(p.Lock.SubjectID) {
		return p, factoryservice.ErrHumanLockConflict
	}
	return p, nil
}
func nativeLockRecord(c factory.HumanLockCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ApplyDiscussionLock, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanLocks) LookupHumanLock(ctx context.Context, c factory.HumanLockCapture) (collaboration.OperationLookup, error) {
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
func (s nativeHumanLocks) SubmitHumanLock(ctx context.Context, c factory.HumanLockCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanLockAdmission
	}
	won, err := s.store.ReserveHumanLockDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanLock(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeLockRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	outcome, err := s.callHumanEffect(ctx, nativeLockEffect{Kind: "set_discussion_lock", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Lock: p.Lock}, cmd, p.Lock.Repository, factoryservice.ErrHumanLockConflict)
	if err != nil {
		return empty, err
	}
	r := nativeLockRecord(c)
	switch outcome.State {
	case "applied":
		var receipt nativeLockReceipt
		mutation := "unlockLockable"
		if c.Request.Lock {
			mutation = "lockLockable"
		}
		if outcome.Failure != nil || len(outcome.Receipt) > 4096 || nativeDecode(outcome.Receipt, &receipt) != nil || !nativeActor(receipt.Account) || receipt.Account.ExternalID != p.Account || receipt.Repository != p.Lock.Repository || receipt.Subject != p.Lock.Subject || receipt.SubjectID != p.Lock.SubjectID || receipt.Number != p.Lock.Number || receipt.Locked == nil || *receipt.Locked != c.Request.Lock || receipt.Reason != c.Request.Reason || !nativeActor(receipt.Actor) || receipt.Actor.ExternalID != p.Account || !nativeObservedText(receipt.ActorNodeID, 1024, false) || !nativeObservedText(receipt.RepositoryNodeID, 1024, false) || !nativeObservedText(receipt.SubjectNodeID, 1024, false) || !nativeObservedText(receipt.URL, 2048, false) || receipt.ProviderResponse.Protocol != "github_graphql_discussion_lock" || receipt.ProviderResponse.Mutation != mutation || receipt.ProviderResponse.Status != 200 || !nativeCreationDigest(receipt.ProviderResponse.ResponseSHA256) {
			return empty, factoryservice.ErrHumanLockConflict
		}
		// Rust owns enum mapping. Preserve the causal nullable metadata while
		// checking presence and whether a reason was requested at all.
		if string(receipt.ActiveLockReason) == "null" {
			if c.Request.Reason != "" {
				return empty, factoryservice.ErrHumanLockConflict
			}
		} else {
			var reason string
			if json.Unmarshal(receipt.ActiveLockReason, &reason) != nil || !nativeObservedText(reason, 32, false) || !c.Request.Lock || c.Request.Reason == "" {
				return empty, factoryservice.ErrHumanLockConflict
			}
		}
		if receipt.ProviderResponse.RequestID != nil && !nativeObservedText(*receipt.ProviderResponse.RequestID, 256, false) {
			return empty, factoryservice.ErrHumanLockConflict
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_lock_applied"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanLockUnavailable
		}
	}
	return r, r.Validate()
}
func (s nativeHumanLocks) CancelHumanLock(ctx context.Context, c factory.HumanLockCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanLockDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native lock cancellation unavailable")
	}
	return *o.Record, nil
}
