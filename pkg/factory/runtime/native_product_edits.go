package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeEditWrite struct {
	Repository         string  `json:"repository"`
	Subject            string  `json:"subject"`
	SubjectID          string  `json:"subject_id"`
	Number             uint64  `json:"number"`
	Title              *string `json:"title"`
	Body               *string `json:"body"`
	OriginalTitle      string  `json:"original_title"`
	OriginalBodySHA256 string  `json:"original_body_sha256"`
	RequireUnchanged   bool    `json:"require_unchanged,omitempty"`
}
type nativeEditPreparation struct {
	Version  int             `json:"version"`
	Account  string          `json:"account"`
	NotAfter int64           `json:"not_after"`
	Edit     nativeEditWrite `json:"edit"`
}
type nativeEditEffect struct {
	Kind      string          `json:"kind"`
	CommandID string          `json:"command_id"`
	Digest    string          `json:"digest"`
	NotAfter  int64           `json:"not_after"`
	Edit      nativeEditWrite `json:"edit"`
}
type nativeEditReceipt struct {
	Account          collaboration.ObservedActor `json:"account"`
	Repository       string                      `json:"repository"`
	RepositoryNodeID string                      `json:"repository_node_id"`
	Subject          string                      `json:"subject"`
	SubjectID        string                      `json:"subject_id"`
	SubjectNodeID    string                      `json:"subject_node_id"`
	Number           uint64                      `json:"number"`
	EditedTitle      bool                        `json:"edited_title"`
	EditedBody       bool                        `json:"edited_body"`
	TitleSHA256      string                      `json:"title_sha256"`
	BodySHA256       string                      `json:"body_sha256"`
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
type nativeHumanEdits struct{ *nativeProductAccount }

var _ factoryservice.HumanEditWriter = nativeHumanEdits{}

func (s nativeHumanEdits) admit(ctx context.Context, q factory.HumanEditCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanEditQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanEditAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanEdits) PrepareHumanEdit(ctx context.Context, q factory.HumanEditCommand) (factory.HumanEditCapture, error) {
	var empty factory.HumanEditCapture
	if err := s.admit(ctx, q); err != nil {
		return empty, err
	}
	reads := nativeCollaborationReads{s.nativeProductAccount}
	var subject, title, body string
	if q.Subject == "issue" {
		page, err := reads.ReadIssues(ctx, q.Actor, q.Repository, factoryservice.CollaborationIssueQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return empty, err
		}
		if len(page.Items) != 1 {
			return empty, factoryservice.ErrHumanEditAdmission
		}
		subject, title, body = page.Items[0].Ref.ExternalID, page.Items[0].Title, page.Items[0].Body
	} else {
		page, err := reads.ReadPulls(ctx, q.Actor, q.Repository, factoryservice.CollaborationPullQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return empty, err
		}
		if len(page.Items) != 1 {
			return empty, factoryservice.ErrHumanEditAdmission
		}
		subject, title, body = page.Items[0].Ref.ExternalID, page.Items[0].Title, page.Items[0].Body
	}
	if !nativeRemoteID(subject) || len(title) > 65536 || strings.ContainsRune(title, 0) {
		return empty, factoryservice.ErrHumanEditUnavailable
	}
	p := nativeEditPreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Edit: nativeEditWrite{Repository: s.repository.ExternalID, Subject: q.Subject, SubjectID: subject, Number: uint64(q.Number), Title: q.Title, Body: q.Body, OriginalTitle: title, OriginalBodySHA256: nativeCreationSHA([]byte(body))}}
	raw, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	c := factory.HumanEditCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	if q.Expected != nil {
		c.Observed = &factory.HumanEditBase{ExternalID: subject, Title: title, BodyDigest: p.Edit.OriginalBodySHA256}
	}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanEdits) decode(ctx context.Context, c factory.HumanEditCapture) (nativeEditPreparation, error) {
	var p nativeEditPreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanEditQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanEditNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Edit.Repository != s.repository.ExternalID || p.Edit.Subject != c.Request.Subject || p.Edit.Number != uint64(c.Request.Number) || !nativeRemoteID(p.Edit.SubjectID) || len(p.Edit.OriginalTitle) > 65536 || strings.ContainsRune(p.Edit.OriginalTitle, 0) || !nativeCreationDigest(p.Edit.OriginalBodySHA256) {
		return p, factoryservice.ErrHumanEditConflict
	}
	fields := c.Request
	fields.Title, fields.Body = p.Edit.Title, p.Edit.Body
	if !fields.SameRequest(c.Request) {
		return p, factoryservice.ErrHumanEditConflict
	}
	if c.Observed != nil && *c.Observed != (factory.HumanEditBase{ExternalID: p.Edit.SubjectID, Title: p.Edit.OriginalTitle, BodyDigest: p.Edit.OriginalBodySHA256}) {
		return p, factoryservice.ErrHumanEditConflict
	}
	return p, nil
}
func nativeEditRecord(c factory.HumanEditCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.EditDiscussion, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanEdits) LookupHumanEdit(ctx context.Context, c factory.HumanEditCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	prior, err := s.store.LookupHumanCommandDispatch(ctx, cmd)
	if err != nil { return empty, err }
	return s.recoverFountainIssue(ctx,cmd,p.NotAfter,p.Edit.Repository,prior,nativeEditRecord(c),c.ProviderIntent,p.Account,"edit",p.Edit,
		func(o collaboration.OperationLookup)(collaboration.OperationLookup,error){return s.store.ObserveHumanEdit(ctx,c,o)})
}
func (s nativeHumanEdits) SubmitHumanEdit(ctx context.Context, c factory.HumanEditCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if !c.ExpectedCurrent() {
		return empty, factoryservice.ErrHumanEditBaseChanged
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanEditAdmission
	}
	won, err := s.store.ReserveHumanEditDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanEdit(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeEditRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	// This requirement derives from the immutable command, including historical
	// captures which predate the native flag. Lookup still adopts only original
	// receipts; an unknown dispatch never reaches this new-send boundary.
	p.Edit.RequireUnchanged = c.Request.Expected != nil
	outcome, err := s.callHumanEffect(ctx, nativeEditEffect{Kind: "edit_discussion", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Edit: p.Edit}, cmd, p.Edit.Repository, factoryservice.ErrHumanEditConflict)
	if err != nil {
		return empty, err
	}
	r := nativeEditRecord(c)
	if s.binding.Host.Provider == collaboration.Fountain {
		return fountainIssueOutcome(r,c.ProviderIntent,p.Account,p.Edit.Repository,"edit",p.Edit,outcome)
	}
	switch outcome.State {
	case "applied":
		var receipt nativeEditReceipt
		mutation := "updateIssue"
		if c.Request.Subject == "pull" {
			mutation = "updatePullRequest"
		}
		titleSHA := nativeCreationSHA([]byte(p.Edit.OriginalTitle))
		if c.Request.Title != nil {
			titleSHA = nativeCreationSHA([]byte(*c.Request.Title))
		}
		bodySHA := p.Edit.OriginalBodySHA256
		if c.Request.Body != nil {
			bodySHA = nativeCreationSHA([]byte(*c.Request.Body))
		}
		if outcome.Failure != nil || len(outcome.Receipt) > 4096 || nativeDecode(outcome.Receipt, &receipt) != nil || !nativeActor(receipt.Account) || receipt.Account.ExternalID != p.Account || receipt.Repository != p.Edit.Repository || receipt.Subject != p.Edit.Subject || receipt.SubjectID != p.Edit.SubjectID || receipt.Number != p.Edit.Number || receipt.EditedTitle != (c.Request.Title != nil) || receipt.EditedBody != (c.Request.Body != nil) || receipt.TitleSHA256 != titleSHA || receipt.BodySHA256 != bodySHA || !nativeObservedText(receipt.RepositoryNodeID, 1024, false) || !nativeObservedText(receipt.SubjectNodeID, 1024, false) || !nativeObservedText(receipt.URL, 2048, false) || receipt.ProviderResponse.Protocol != "github_graphql_discussion_edit" || receipt.ProviderResponse.Mutation != mutation || receipt.ProviderResponse.Status != 200 || !nativeCreationDigest(receipt.ProviderResponse.ResponseSHA256) {
			return empty, factoryservice.ErrHumanEditConflict
		}
		if receipt.ProviderResponse.RequestID != nil && !nativeObservedText(*receipt.ProviderResponse.RequestID, 256, false) {
			return empty, factoryservice.ErrHumanEditConflict
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_discussion_edited"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanEditUnavailable
		}
	}
	return r, r.Validate()
}
func (s nativeHumanEdits) CancelHumanEdit(ctx context.Context, c factory.HumanEditCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanEditDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native edit cancellation unavailable")
	}
	return *o.Record, nil
}
