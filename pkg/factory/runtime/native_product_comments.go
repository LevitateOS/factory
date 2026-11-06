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

// Go retains the one command journal and admission decisions. This opaque
// credential-free preparation pins native semantic arguments, never HTTP URLs.
type nativeCommentWrite struct {
	Repository string `json:"repository"`
	Subject    string `json:"subject"`
	SubjectID  string `json:"subject_id"`
	Number     uint64 `json:"number"`
	Body       string `json:"body"`
}
type nativeCommentPreparation struct {
	Version  int                `json:"version"`
	Account  string             `json:"account"`
	NotAfter int64              `json:"not_after"`
	Comment  nativeCommentWrite `json:"comment"`
}
type nativeCommentEffect struct {
	Kind      string             `json:"kind"`
	CommandID string             `json:"command_id"`
	Digest    string             `json:"digest"`
	NotAfter  int64              `json:"not_after"`
	Comment   nativeCommentWrite `json:"comment"`
}
type nativeCommentReceipt struct {
	Account       collaboration.ObservedActor `json:"account"`
	Repository    string                      `json:"repository"`
	Subject       string                      `json:"subject"`
	SubjectID     string                      `json:"subject_id"`
	SubjectNodeID string                      `json:"subject_node_id"`
	Number        uint64                      `json:"number"`
	Comment       struct {
		ExternalID      string                       `json:"external_id"`
		NodeID          string                       `json:"node_id"`
		AuthorNodeID    string                       `json:"author_node_id"`
		Author          *collaboration.ObservedActor `json:"author"`
		CreatedAt       *time.Time                   `json:"created_at"`
		UpdatedAt       *time.Time                   `json:"updated_at"`
		MinimizedReason *string                      `json:"minimized_reason"`
		BodySHA256      string                       `json:"body_sha256"`
		URL             string                       `json:"url"`
		IssueURL        string                       `json:"issue_url"`
	} `json:"comment"`
	ProviderResponse struct {
		Protocol       string  `json:"protocol"`
		Status         int     `json:"status"`
		RequestID      *string `json:"request_id"`
		ResponseSHA256 string  `json:"response_sha256"`
	} `json:"provider_response"`
}
type nativeHumanComments struct{ *nativeProductAccount }

var _ factoryservice.HumanCommentWriter = nativeHumanComments{}

func (s nativeHumanComments) admit(ctx context.Context, q factory.HumanCommentCommand) error {
	if q.Validate() != nil || !nativeIndex(q.Number) {
		return factoryservice.ErrHumanCommentQuery
	}
	if q.Host != s.binding.Host.ID || q.Actor != s.binding.Actor.ID || s.repository == nil || q.Repository != s.repository.ID {
		return factoryservice.ErrHumanCommentAdmission
	}
	return s.current(ctx)
}
func (s nativeHumanComments) PrepareHumanComment(ctx context.Context, q factory.HumanCommentCommand) (factory.HumanCommentCapture, error) {
	var empty factory.HumanCommentCapture
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
		if len(page.Items) != 1 || page.Items[0].Locked == nil || *page.Items[0].Locked {
			return empty, factoryservice.ErrHumanCommentAdmission
		}
		subject = page.Items[0].Ref.ExternalID
	} else {
		page, err := reads.ReadPulls(ctx, q.Actor, q.Repository, factoryservice.CollaborationPullQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return empty, err
		}
		if len(page.Items) != 1 {
			return empty, factoryservice.ErrHumanCommentAdmission
		}
		subject = page.Items[0].Ref.ExternalID
	}
	if !nativeRemoteID(subject) {
		return empty, factoryservice.ErrHumanCommentUnavailable
	}
	intent := nativeCommentPreparation{Version: 1, Account: s.identity.Account.ExternalID, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Comment: nativeCommentWrite{Repository: s.repository.ExternalID, Subject: q.Subject, SubjectID: subject, Number: uint64(q.Number), Body: q.Body}}
	raw, err := json.Marshal(intent)
	if err != nil {
		return empty, err
	}
	c := factory.HumanCommentCapture{Request: q, Authority: s.humanAuthority(), OperationID: q.ID, ProviderIntent: raw}
	_, err = c.Command()
	return c, err
}
func (s nativeHumanComments) decode(ctx context.Context, c factory.HumanCommentCapture) (nativeCommentPreparation, error) {
	var p nativeCommentPreparation
	if _, err := c.Command(); err != nil {
		return p, factoryservice.ErrHumanCommentQuery
	}
	if err := s.admit(ctx, c.Request); err != nil {
		return p, err
	}
	if c.Authority != s.humanAuthority() {
		return p, factoryservice.ErrHumanCommentNamespace
	}
	if c.OperationID != c.Request.ID || nativeDecode(c.ProviderIntent, &p) != nil || p.Version != 1 || p.Account != s.identity.Account.ExternalID || p.NotAfter <= 0 || p.Comment.Repository != s.repository.ExternalID || p.Comment.Subject != c.Request.Subject || p.Comment.Number != uint64(c.Request.Number) || p.Comment.Body != c.Request.Body || !nativeRemoteID(p.Comment.SubjectID) {
		return p, factoryservice.ErrHumanCommentConflict
	}
	return p, nil
}
func (s nativeHumanComments) LookupHumanComment(ctx context.Context, c factory.HumanCommentCapture) (collaboration.OperationLookup, error) {
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
func nativeCommentRecord(c factory.HumanCommentCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateComment, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}
func (s nativeHumanComments) SubmitHumanComment(ctx context.Context, c factory.HumanCommentCapture) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	p, err := s.decode(ctx, c)
	if err != nil {
		return empty, err
	}
	if time.Now().Unix() >= p.NotAfter {
		return empty, factoryservice.ErrHumanCommentAdmission
	}
	won, err := s.store.ReserveHumanCommentDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		prior, err := s.LookupHumanComment(ctx, c)
		if err != nil {
			return empty, err
		}
		if prior.Record != nil {
			return *prior.Record, nil
		}
		return nativeCommentRecord(c), nil
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	outcome, err := s.callHumanEffect(ctx, nativeCommentEffect{Kind: "create_comment", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: p.NotAfter, Comment: p.Comment}, cmd, p.Comment.Repository, factoryservice.ErrHumanCommentConflict)
	if err != nil {
		return empty, err
	}
	r := nativeCommentRecord(c)
	switch outcome.State {
	case "created":
		if outcome.Failure != nil || len(outcome.Receipt) > 4096 {
			return empty, factoryservice.ErrHumanCommentUnavailable
		}
		var receipt nativeCommentReceipt
		if nativeDecode(outcome.Receipt, &receipt) != nil || !nativeActor(receipt.Account) || receipt.Account.ExternalID != p.Account || receipt.Repository != p.Comment.Repository || receipt.Subject != p.Comment.Subject || receipt.SubjectID != p.Comment.SubjectID || receipt.Number != p.Comment.Number || !nativeRemoteID(receipt.Comment.ExternalID) || receipt.Comment.Author == nil || !nativeActor(*receipt.Comment.Author) || receipt.Comment.Author.ExternalID != p.Account || receipt.Comment.BodySHA256 != nativeCreationSHA([]byte(c.Request.Body)) || receipt.ProviderResponse.Status != 200 || receipt.ProviderResponse.Protocol != "github_graphql_add_comment" || !nativeObservedText(receipt.SubjectNodeID, 1024, false) || !nativeObservedText(receipt.Comment.NodeID, 1024, false) || !nativeObservedText(receipt.Comment.AuthorNodeID, 1024, false) || !nativeCreationDigest(receipt.ProviderResponse.ResponseSHA256) {
			return empty, factoryservice.ErrHumanCommentConflict
		}
		if receipt.ProviderResponse.RequestID != nil && !nativeObservedText(*receipt.ProviderResponse.RequestID, 256, false) {
			return empty, factoryservice.ErrHumanCommentConflict
		}
		r.Effect, r.Completion, r.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_comment_created"
		r.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: outcome.Receipt}
	default:
		if !outcome.applyFailure(&r) {
			return empty, factoryservice.ErrHumanCommentUnavailable
		}
	}
	return r, r.Validate()
}
func (s nativeHumanComments) CancelHumanComment(ctx context.Context, c factory.HumanCommentCapture) (collaboration.OperationRecord, error) {
	if _, err := s.decode(ctx, c); err != nil {
		return collaboration.OperationRecord{}, err
	}
	o, err := s.store.CancelHumanCommentDispatch(ctx, c)
	if err != nil {
		return collaboration.OperationRecord{}, err
	}
	if o.Record == nil {
		return collaboration.OperationRecord{}, errors.New("native comment cancellation unavailable")
	}
	return *o.Record, nil
}
