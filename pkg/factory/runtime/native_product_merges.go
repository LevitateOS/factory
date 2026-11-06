package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeMergeWrite struct {
	Repository       string `json:"repository"`
	RepositoryNodeID string `json:"repository_node_id"`
	PullID           string `json:"pull_id"`
	PullNodeID       string `json:"pull_node_id"`
	Number           uint32 `json:"number"`
	Head             string `json:"head"`
	Base             string `json:"base"`
	Method           string `json:"method"`
	Message          string `json:"message"`
}
type nativeMergePreparation struct {
	Version  int              `json:"version"`
	Account  string           `json:"account"`
	NotAfter int64            `json:"not_after"`
	Merge    nativeMergeWrite `json:"merge"`
}
type nativeMergeFacts struct {
	Repository       string          `json:"repository"`
	Name             string          `json:"name"`
	RepositoryNodeID string          `json:"repository_node_id"`
	PullID           string          `json:"pull_id"`
	PullNodeID       string          `json:"pull_node_id"`
	Number           uint32          `json:"number"`
	Head             string          `json:"head"`
	Base             string          `json:"base"`
	State            string          `json:"state"`
	Draft            *bool           `json:"draft"`
	Merged           *bool           `json:"merged"`
	Archived         *bool           `json:"archived"`
	Disabled         *bool           `json:"disabled"`
	Push             *bool           `json:"push"`
	Methods          map[string]bool `json:"methods"`
	HeadCondition    string          `json:"head_condition"`
	BaseCondition    string          `json:"base_condition"`
}
type nativeMergeEffect struct {
	Kind      string           `json:"kind"`
	CommandID string           `json:"command_id"`
	Digest    string           `json:"digest"`
	NotAfter  int64            `json:"not_after"`
	Merge     nativeMergeWrite `json:"merge"`
}
type nativeMergeReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	PullID           string                      `json:"pull_id"`
	PullNodeID       string                      `json:"pull_node_id"`
	Number           uint32                      `json:"number"`
	Head             string                      `json:"head"`
	Base             string                      `json:"base"`
	Method           string                      `json:"method"`
	MessageSHA256    string                      `json:"message_sha256"`
	WritingMatches   *bool                       `json:"writing_matches"`
	HeadCondition    string                      `json:"head_condition"`
	BaseCondition    string                      `json:"base_condition"`
	Commit           string                      `json:"commit"`
	MergedAt         *time.Time                  `json:"merged_at"`
	ProviderResponse nativeCreationResponse      `json:"provider_response"`
}
type nativeHumanMerges struct{ *nativeProductAccount }

var _ factoryservice.HumanMergeWriter = nativeHumanMerges{}

func (s nativeHumanMerges) admit(ctx context.Context, q factory.HumanMergeCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanMergeQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanMergeAdmission
	}
	return s.current(ctx)
}
func nativeMergeSupported(q factory.HumanMergeCommand) bool {
	return q.Method == "merge" || q.Method == "squash" || q.Method == "rebase" && q.Message == ""
}
func (s nativeHumanMerges) PrepareHumanMerge(ctx context.Context, q factory.HumanMergeCommand) (factory.HumanMergeCapture, error) {
	var empty factory.HumanMergeCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	if !nativeMergeSupported(q) {
		return empty, factoryservice.ErrHumanMergeUnavailable
	}
	if err := s.visible(ctx, q.Actor, q.Repository); err != nil {
		return empty, err
	}
	var f nativeMergeFacts
	if err := s.read(ctx, nativeRead{Kind: "merge_preparation", Repository: s.repository.ExternalID, Number: uint32(q.Number)}, &f); err != nil {
		return empty, err
	}
	if f.Repository != s.repository.ExternalID || !nativeRemoteID(f.PullID) || !nativeReviewNode(f.RepositoryNodeID, 1024) || !nativeReviewNode(f.PullNodeID, 1024) || f.Number != uint32(q.Number) || !collaboration.ValidCommitID(collaboration.CommitID(f.Head)) || !collaboration.ValidCommitID(collaboration.CommitID(f.Base)) || f.HeadCondition != "expected_head_oid" || f.BaseCondition != "bounded_preflight" {
		return empty, errNativeRead
	}
	if f.State != "open" || f.Draft == nil || *f.Draft || f.Merged == nil || *f.Merged || f.Archived == nil || *f.Archived || f.Disabled == nil || *f.Disabled || f.Push == nil || !*f.Push || !f.Methods[q.Method] {
		return empty, factoryservice.ErrHumanMergeUnavailable
	}
	if _, err := s.mapRead(ctx, f.Repository, f.Name, nil, nil); err != nil {
		return empty, err
	}
	p := nativeMergePreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Merge: nativeMergeWrite{Repository: f.Repository, RepositoryNodeID: f.RepositoryNodeID, PullID: f.PullID, PullNodeID: f.PullNodeID, Number: f.Number, Head: f.Head, Base: f.Base, Method: q.Method, Message: q.Message}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanMergeCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanMerges) decode(ctx context.Context, c factory.HumanMergeCapture) (nativeMergePreparation, error) {
	var p nativeMergePreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanMergeQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanMergeNamespace
	}
	if nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || c.OperationID != c.Request.ID || !nativeMergeSupported(c.Request) || p.Merge.Repository != s.repository.ExternalID || !nativeRemoteID(p.Merge.PullID) || !nativeReviewNode(p.Merge.RepositoryNodeID, 1024) || !nativeReviewNode(p.Merge.PullNodeID, 1024) || p.Merge.Number != uint32(c.Request.Number) || !collaboration.ValidCommitID(collaboration.CommitID(p.Merge.Head)) || !collaboration.ValidCommitID(collaboration.CommitID(p.Merge.Base)) || p.Merge.Method != c.Request.Method || p.Merge.Message != c.Request.Message {
		return p, factoryservice.ErrHumanMergeConflict
	}
	return p, nil
}
func (s nativeHumanMerges) LookupHumanMerge(ctx context.Context, c factory.HumanMergeCapture) (collaboration.OperationLookup, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationLookup{}, err
	}
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.store.LookupHumanCommandDispatch(ctx, cmd)
}
func nativeMergeRecord(c factory.HumanMergeCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.Merge, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanMerges) SubmitHumanMerge(ctx context.Context, c factory.HumanMergeCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanMergeAdmission
	}
	won, err := s.store.ReserveHumanMergeDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		o, err := s.LookupHumanMerge(ctx, c)
		if err != nil {
			return empty, err
		}
		if o.Record != nil {
			return *o.Record, nil
		}
		return nativeMergeRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	reply, err := s.callHumanEffect(ctx, nativeMergeEffect{Kind: "merge_pull", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Merge: p.Merge}, cmd, p.Merge.Repository, factoryservice.ErrHumanMergeConflict)
	if err != nil {
		return empty, err
	}
	r := nativeMergeRecord(c)
	if reply.State == "applied" {
		var receipt nativeMergeReceipt
		w := p.Merge
		if reply.Failure != nil || len(reply.Receipt) > 4096 || nativeDecode(reply.Receipt, &receipt) != nil || !nativeActor(receipt.Account) || receipt.Account.ExternalID != p.Account || receipt.Repository != w.Repository || receipt.RepositoryNodeID != w.RepositoryNodeID || receipt.PullID != w.PullID || receipt.PullNodeID != w.PullNodeID || receipt.Number != w.Number || receipt.Head != w.Head || receipt.Base != w.Base || receipt.Method != w.Method || receipt.MessageSHA256 != nativeCreationSHA([]byte(w.Message)) || receipt.WritingMatches == nil || receipt.HeadCondition != "expected_head_oid" || receipt.BaseCondition != "bounded_preflight" || !collaboration.ValidCommitID(collaboration.CommitID(receipt.Commit)) || receipt.MergedAt == nil || receipt.ProviderResponse.Protocol != "github_graphql_merge_pull" || receipt.ProviderResponse.Status != 200 || !nativeCreationDigest(receipt.ProviderResponse.ResponseSHA256) || receipt.ProviderResponse.RequestID != nil && !nativeObservedText(*receipt.ProviderResponse.RequestID, 256, false) {
			return empty, factoryservice.ErrHumanMergeConflict
		}
		r.Effect, r.Reason = collaboration.EffectCommitted, "native_pull_merged"
		if *receipt.WritingMatches {
			r.Completion = collaboration.CompletionComplete
		}
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: reply.Receipt}
	} else if !reply.applyFailure(&r) {
		return empty, factoryservice.ErrHumanMergeUnavailable
	}
	return r, r.Validate()
}
func (s nativeHumanMerges) CancelHumanMerge(ctx context.Context, c factory.HumanMergeCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanMergeDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, factoryservice.ErrHumanMergeUnavailable
	}
	return *o.Record, nil
}
