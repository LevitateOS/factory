package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// The original committed creation receipt, rather than matching authorship or
// content, determines which exact issue this request is allowed to refine.
func (s *nativeIssueEffects) createdIssue(ctx context.Context, p nativeIssueIntakeProof, account *nativeProductAccount, slot int) (collaboration.IssueRef, error) {
	commands := factoryservice.HumanIssueCreates{Store: s.Store, Writer: nativeHumanIssueCreates{account}}
	request := s.createRequest(p, account, slot)
	outcome, err := commands.LookupCommand(ctx, request)
	if err != nil {
		return collaboration.IssueRef{}, err
	}
	return commands.CreatedIssue(ctx, request, outcome)
}

func (s *nativeIssueEffects) AuthorizeIssueMaker(ctx context.Context, scope factory.IssueMakerScope) error {
	if err := s.CapturedIssueEffects.AuthorizeIssueMaker(ctx, scope); err != nil {
		return err
	}
	_, raw, err := s.Grant(ctx, scope.SourcePost, scope.Revision)
	if err != nil {
		return err
	}
	proof, err := s.proof(raw)
	if err != nil {
		return err
	}
	account, err := s.savedAccount(ctx, int64(proof.Scope.Repository), proof.Scope.Actor, proof.Connection)
	if err != nil {
		return err
	}
	if account.identity.Account.ExternalID != proof.Account || account.repository.ExternalID != proof.Repository {
		return factoryservice.ErrCapturedIssueAuthority
	}
	issue, err := s.createdIssue(ctx, proof, account, 0)
	if err != nil {
		return err
	}
	if issue.Number != scope.Issue {
		return factoryservice.ErrCapturedIssueAuthority
	}
	capabilities, err := (nativeCollaborationReads{account}).ReadCapabilities(ctx, proof.Scope.Actor, proof.Scope.Repository, factoryservice.CapabilityQuery{Subject: "issue", Number: issue.Number})
	if err != nil {
		return err
	}
	if capabilities.Issue == nil || *capabilities.Issue != issue || capabilities.Actions[collaboration.CommentIssue].State != collaboration.Allowed {
		return factoryservice.ErrCapturedIssueUnavailable
	}
	// Provider reads must not extend the captured source's lifetime or reinstate a
	// withdrawn request before the role is admitted to paid execution.
	return s.currentSource(ctx, proof, raw)
}

func (s *nativeIssueEffects) refineIssue(ctx context.Context, proof nativeIssueIntakeProof, raw json.RawMessage, account *nativeProductAccount, mutation factory.IssueMakerMutation) (factory.IssueMakerReceipt, error) {
	var empty factory.IssueMakerReceipt
	key := sha256.Sum256([]byte("factory.native.issue-refine\x00" + string(proof.Profile) + "\x00" + proof.Scope.Source + "\x00" + strconv.Itoa(mutation.Slot) + "\x00" + strconv.Itoa(mutation.Step)))
	id := hex.EncodeToString(key[:16])
	prior, err := s.Store.FactoryCommand(ctx, id)
	if err == nil && prior.Type != factory.CommandHumanComment {
		// Never reinterpret a historical edit or its unknown send as a comment.
		return s.refineLegacyIssue(ctx, proof, raw, account, mutation, id)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return empty, err
	}
	issue, err := s.createdIssue(ctx, proof, account, mutation.Slot)
	if err != nil {
		return empty, err
	}
	request := factory.HumanCommentCommand{ID: id, Host: account.binding.Host.ID, Repository: proof.Scope.Repository, Actor: proof.Scope.Actor, Subject: "issue", Number: issue.Number,
		Body: "## Refinement: " + mutation.Title + "\n\n" + mutation.Body + "\n\n---\nRequest: " + proof.Scope.Source + " r" + strconv.FormatInt(proof.Scope.Revision, 10) + "; slot " + strconv.Itoa(mutation.Slot) + "; step " + strconv.Itoa(mutation.Step) + ". Existing issue text is preserved."}
	writer := nativeHumanComments{account}
	_, err = s.Store.HumanCommentCapture(ctx, request)
	if errors.Is(err, store.ErrNotFound) {
		capture, e := writer.PrepareHumanComment(ctx, request)
		if e != nil {
			return empty, e
		}
		var preparation nativeCommentPreparation
		if nativeDecode(capture.ProviderIntent, &preparation) != nil || preparation.Comment.SubjectID != issue.ExternalID {
			return empty, factoryservice.ErrCapturedIssueAuthority
		}
		preparation.NotAfter = min(preparation.NotAfter, proof.Scope.Expires)
		capture.ProviderIntent, err = json.Marshal(preparation)
		if err == nil {
			_, _, err = s.Store.CaptureHumanComment(ctx, capture)
		}
	}
	if err != nil {
		return empty, err
	}
	if err = s.currentSource(ctx, proof, raw); err != nil {
		return empty, err
	}
	outcome, err := (factoryservice.HumanComments{Store: s.Store, Writer: writer}).Submit(ctx, request)
	if err != nil {
		return empty, err
	}
	if outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted || outcome.Record.Completion != collaboration.CompletionComplete {
		return empty, factoryservice.ErrPostEffectUnknown
	}
	return factory.IssueMakerReceipt{Issue: issue, Slot: mutation.Slot, Title: mutation.Title, BodyDigest: factory.MCPInputDigest([]byte(mutation.Body))}, nil
}

func (s *nativeIssueEffects) refineLegacyIssue(ctx context.Context, proof nativeIssueIntakeProof, raw json.RawMessage, account *nativeProductAccount, mutation factory.IssueMakerMutation, id string) (factory.IssueMakerReceipt, error) {
	var empty factory.IssueMakerReceipt
	issue, err := s.createdIssue(ctx, proof, account, mutation.Slot)
	if err != nil {
		return empty, err
	}
	request := factory.HumanEditCommand{ID: id, Host: account.binding.Host.ID, Repository: proof.Scope.Repository, Actor: proof.Scope.Actor, Subject: "issue", Number: issue.Number, Title: &mutation.Title, Body: &mutation.Body, Expected: &factory.HumanEditBase{ExternalID: issue.ExternalID, Title: mutation.ExpectedTitle, BodyDigest: mutation.ExpectedBodyDigest}}
	writer := nativeHumanEdits{account}
	_, err = s.Store.HumanEditCapture(ctx, request)
	if err != nil {
		return empty, err
	}
	if err = s.currentSource(ctx, proof, raw); err != nil {
		return empty, err
	}
	outcome, err := (factoryservice.HumanEdits{Store: s.Store, Writer: writer}).Submit(ctx, request)
	if err != nil {
		return empty, err
	}
	if outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted || outcome.Record.Completion != collaboration.CompletionComplete {
		return empty, factoryservice.ErrPostEffectUnknown
	}
	return factory.IssueMakerReceipt{Issue: issue, Slot: mutation.Slot, Title: mutation.Title, BodyDigest: factory.MCPInputDigest([]byte(mutation.Body))}, nil
}
