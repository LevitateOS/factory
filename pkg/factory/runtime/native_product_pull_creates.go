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

type nativePullBranchPin struct {
	RepositoryNodeID string `json:"repository_node_id"`
	OwnerNodeID      string `json:"owner_node_id"`
	ReferenceNodeID  string `json:"reference_node_id"`
	Commit           string `json:"commit"`
}
type nativePullCreateWrite struct {
	HeadRepository      string              `json:"head_repository"`
	BaseBranch          string              `json:"base_branch"`
	HeadBranch          string              `json:"head_branch"`
	BasePin             nativePullBranchPin `json:"base_pin"`
	HeadPin             nativePullBranchPin `json:"head_pin"`
	AllowMaintainerEdit bool                `json:"allow_maintainer_edit"`
	Repository          string              `json:"repository"`
	Title               string              `json:"title"`
	Body                string              `json:"body"`
}
type nativePullCreatePreparation struct {
	Version  int                   `json:"version"`
	Account  string                `json:"account"`
	NotAfter int64                 `json:"not_after"`
	Pull     nativePullCreateWrite `json:"pull"`
}
type nativePullCreateEffect struct {
	Kind      string                `json:"kind"`
	CommandID string                `json:"command_id"`
	Digest    string                `json:"digest"`
	NotAfter  int64                 `json:"not_after"`
	Pull      nativePullCreateWrite `json:"pull"`
}
type nativePullCreateReceipt struct {
	Account        collaboration.ObservedActor `json:"account"`
	Repository     string                      `json:"repository"`
	HeadRepository string                      `json:"head_repository"`
	Base           struct {
		Branch string              `json:"branch"`
		Pin    nativePullBranchPin `json:"pin"`
	} `json:"base"`
	Head struct {
		Branch string              `json:"branch"`
		Pin    nativePullBranchPin `json:"pin"`
	} `json:"head"`
	BranchCondition string `json:"branch_condition"`
	Pull            struct {
		ExternalID          string            `json:"external_id"`
		NodeID              string            `json:"node_id"`
		Number              int64             `json:"number"`
		URL                 string            `json:"url"`
		Author              *nativePullAuthor `json:"author"`
		State               string            `json:"state"`
		Draft               *bool             `json:"draft"`
		AllowMaintainerEdit *bool             `json:"allow_maintainer_edit"`
		TitleSHA256         string            `json:"title_sha256"`
		BodySHA256          string            `json:"body_sha256"`
		CreatedAt           *time.Time        `json:"created_at"`
		UpdatedAt           *time.Time        `json:"updated_at"`
	} `json:"pull"`
	ProviderResponse nativeCreationResponse `json:"provider_response"`
}
type nativePullAuthor struct {
	ExternalID string `json:"external_id"`
	NodeID     string `json:"node_id"`
	Login      string `json:"login"`
}
type nativeHumanPullCreates struct{ *nativeProductAccount }

var _ factoryservice.HumanPullCreateWriter = nativeHumanPullCreates{}

func (s nativeHumanPullCreates) admit(ctx context.Context, q factory.HumanPullCreateCommand) error {
	if q.Validate() != nil {
		return factoryservice.ErrHumanPullCreateQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanPullCreateAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanPullCreates) PrepareHumanPullCreate(ctx context.Context, q factory.HumanPullCreateCommand) (factory.HumanPullCreateCapture, error) {
	var empty factory.HumanPullCreateCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	if err := s.visible(ctx, q.Actor, q.Repository); err != nil {
		return empty, err
	}
	head, err := s.store.CollaborationRepositoryOnHost(ctx, q.Host, q.HeadRepository)
	if err != nil {
		return empty, err
	}
	var branches nativePullBranches
	if err := s.read(ctx, nativeRead{Kind: "pull_branches", Repository: s.repository.ExternalID, HeadRepository: head.ExternalID, BaseBranch: q.BaseBranch, HeadBranch: q.HeadBranch}, &branches); err != nil {
		return empty, err
	}
	if !branches.Base.valid(s.repository.ExternalID, q.BaseBranch) || !branches.Head.valid(head.ExternalID, q.HeadBranch) {
		return empty, errNativeRead
	}
	// The selected head is a canonical reference, never an expansion of the
	// native primary repository scope. Rust independently observes that exact
	// numeric head and rechecks its rights/network immediately before sending.
	mapped, err := s.mapRead(ctx, branches.Base.Repository.ExternalID, branches.Base.Repository.Name, nil, []collaboration.ObservedRepository{{ExternalID: branches.Head.Repository.ExternalID, Name: branches.Head.Repository.Name}})
	if err != nil {
		return empty, err
	}
	if mapped.References[head.ExternalID].ID != q.HeadRepository {
		return empty, errNativeRead
	}
	p := nativePullCreatePreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Pull: nativePullCreateWrite{Repository: s.repository.ExternalID, HeadRepository: head.ExternalID, BaseBranch: q.BaseBranch, HeadBranch: q.HeadBranch, BasePin: branches.Base.pin(), HeadPin: branches.Head.pin(), Title: q.Title, Body: q.Body, AllowMaintainerEdit: q.AllowMaintainerEdit}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanPullCreateCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanPullCreates) decode(ctx context.Context, c factory.HumanPullCreateCapture) (nativePullCreatePreparation, error) {
	var p nativePullCreatePreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanPullCreateQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanPullCreateNamespace
	}
	head, err := s.store.CollaborationRepositoryOnHost(ctx, c.Request.Host, c.Request.HeadRepository)
	if err != nil {
		return p, err
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Pull.Repository != s.repository.ExternalID || p.Pull.HeadRepository != head.ExternalID || p.Pull.BaseBranch != c.Request.BaseBranch || p.Pull.HeadBranch != c.Request.HeadBranch || !p.Pull.BasePin.valid() || !p.Pull.HeadPin.valid() || p.Pull.BasePin.ReferenceNodeID == p.Pull.HeadPin.ReferenceNodeID || (p.Pull.Repository == p.Pull.HeadRepository) != (p.Pull.BasePin.RepositoryNodeID == p.Pull.HeadPin.RepositoryNodeID) || p.Pull.Repository == p.Pull.HeadRepository && p.Pull.BasePin.OwnerNodeID != p.Pull.HeadPin.OwnerNodeID || p.Pull.Title != c.Request.Title || p.Pull.Body != c.Request.Body || p.Pull.AllowMaintainerEdit != c.Request.AllowMaintainerEdit {
		return p, factoryservice.ErrHumanPullCreateConflict
	}
	return p, nil
}
func nativePullCreateRecord(c factory.HumanPullCreateCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.OpenPull, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanPullCreates) LookupHumanPullCreate(ctx context.Context, c factory.HumanPullCreateCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	if _, err := s.decode(ctx, c); err != nil {
		return empty, err
	}
	state, err := s.store.HumanPullCreateDispatchState(ctx, c)
	if err != nil {
		return empty, err
	}
	cmd, err := s.store.FactoryCommand(ctx, c.Request.ID)
	if err != nil {
		return empty, err
	}
	if cmd.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(cmd.Outcome), &prior) != nil || factory.ValidateHumanPullCreateObservation(c, prior) != nil {
			return empty, factory.ErrHumanPullCreateObservation
		}
		if prior.Record != nil {
			return prior, nil
		}
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupNotObserved, Reason: "local_dispatch_unclaimed"}
	if state != factory.DispatchUnclaimed {
		r := nativePullCreateRecord(c)
		if state == factory.DispatchLegacyUnknown {
			r.Reason = "legacy_dispatch_unknown"
		}
		o.State, o.Record = collaboration.LookupFound, &r
	}
	return o, nil
}
func validateNativePullCreateReceipt(c factory.HumanPullCreateCapture, p nativePullCreatePreparation, raw json.RawMessage) (nativePullCreateReceipt, error) {
	var r nativePullCreateReceipt
	if len(raw) > 4096 || nativeDecode(raw, &r) != nil || !nativeActor(r.Account) || r.Account.ExternalID != p.Account || r.Repository != p.Pull.Repository || r.HeadRepository != p.Pull.HeadRepository || r.Base.Branch != p.Pull.BaseBranch || r.Head.Branch != p.Pull.HeadBranch || r.Base.Pin != p.Pull.BasePin || r.Head.Pin != p.Pull.HeadPin || r.BranchCondition != "preflight_and_causal_reply" || !nativeRemoteID(r.Pull.ExternalID) || !nativeObservedText(r.Pull.NodeID, 1024, false) || r.Pull.Number <= 0 || r.Pull.Author == nil || !nativeActor(collaboration.ObservedActor{ExternalID: r.Pull.Author.ExternalID, Login: r.Pull.Author.Login}) || r.Pull.Author.ExternalID != p.Account || !nativeReviewNode(r.Pull.Author.NodeID, 1024) || r.Pull.State != "open" || r.Pull.Draft == nil || *r.Pull.Draft || r.Pull.AllowMaintainerEdit == nil || *r.Pull.AllowMaintainerEdit != p.Pull.AllowMaintainerEdit || r.Pull.TitleSHA256 != nativeCreationSHA([]byte(c.Request.Title)) || r.Pull.BodySHA256 != nativeCreationSHA([]byte(c.Request.Body)) || r.ProviderResponse.Protocol != "github_graphql_create_pull" || r.ProviderResponse.Status != 200 || !nativeCreationDigest(r.ProviderResponse.ResponseSHA256) {
		return r, factoryservice.ErrHumanPullCreateConflict
	}
	if r.ProviderResponse.RequestID != nil && !nativeObservedText(*r.ProviderResponse.RequestID, 256, false) {
		return r, factoryservice.ErrHumanPullCreateConflict
	}
	return r, nil
}
func (s nativeHumanPullCreates) SubmitHumanPullCreate(ctx context.Context, c factory.HumanPullCreateCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanPullCreateAdmission
	}
	won, err := s.store.ReserveHumanPullCreateDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanPullCreate(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativePullCreateRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	var outcome nativeCreationReply
	scope, err := s.port.call(ctx, nativePullCreateEffect{Kind: "create_pull", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Pull: p.Pull}, &outcome)
	if err != nil {
		return empty, err
	}
	if scope.Connection != s.identity.Connection || scope.Repository != p.Pull.Repository || outcome.CommandID != c.OperationID || outcome.Digest != cmd.Digest {
		return empty, factoryservice.ErrHumanPullCreateConflict
	}
	r := nativePullCreateRecord(c)
	switch outcome.State {
	case "created":
		if outcome.Failure != nil {
			return empty, factoryservice.ErrHumanPullCreateUnavailable
		}
		if _, err := validateNativePullCreateReceipt(c, p, outcome.Receipt); err != nil {
			return empty, err
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_pull_created"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	case "not_sent", "indeterminate":
		if string(outcome.Receipt) != "null" || outcome.Failure == nil || !nativeErrorCode(outcome.Failure.Code) || outcome.Failure.Status < 400 || outcome.Failure.Status > 599 {
			return empty, factoryservice.ErrHumanPullCreateUnavailable
		}
		r.Reason = outcome.Failure.Code
		if outcome.State == "not_sent" {
			r.Effect, r.Completion = collaboration.EffectNotCommitted, collaboration.CompletionComplete
		}
	default:
		return empty, factoryservice.ErrHumanPullCreateUnavailable
	}
	return r, r.Validate()
}
func (s nativeHumanPullCreates) CancelHumanPullCreate(ctx context.Context, c factory.HumanPullCreateCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanPullCreateDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native pull cancellation unavailable")
	}
	return *o.Record, nil
}
func (s nativeHumanPullCreates) ReadCreatedHumanPull(ctx context.Context, c factory.HumanPullCreateCapture, o collaboration.OperationLookup) (collaboration.PullRef, error) {
	var empty collaboration.PullRef
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanPullCreateObservation(c, o) != nil || o.Record == nil || o.Record.Effect != collaboration.EffectCommitted || o.Record.Receipt == nil || o.Record.Receipt.IntentDigest != nativeCreationSHA(c.ProviderIntent) {
		return empty, factoryservice.ErrHumanPullCreateQuery
	}
	r, err := validateNativePullCreateReceipt(c, p, o.Record.Receipt.Payload)
	if err != nil {
		return empty, err
	}
	page, err := (nativeCollaborationReads{s.nativeProductAccount}).ReadPulls(ctx, c.Request.Actor, c.Request.Repository, factoryservice.CollaborationPullQuery{Number: r.Pull.Number, Limit: 1})
	if err != nil {
		return empty, err
	}
	if len(page.Items) != 1 || page.Items[0].Ref.ExternalID != r.Pull.ExternalID || page.Items[0].Ref.Repository != c.Request.Repository || page.Items[0].Ref.Number != r.Pull.Number {
		return empty, factoryservice.ErrHumanPullCreateUnavailable
	}
	return page.Items[0].Ref, nil
}
