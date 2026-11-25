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
	// Each refinement comment reads the issue before it writes, so access is
	// not asked here. Reads must not extend the captured source's lifetime or reinstate a
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
	outcome, err := s.capturedComment(ctx, proof, raw, account, request, issue.ExternalID)
	if err != nil {
		return empty, err
	}
	if outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted || outcome.Record.Completion != collaboration.CompletionComplete {
		return empty, factoryservice.ErrPostEffectUnknown
	}
	return factory.IssueMakerReceipt{Issue: issue, Slot: mutation.Slot, Title: mutation.Title, BodyDigest: factory.MCPInputDigest([]byte(mutation.Body))}, nil
}

// capturedComment writes one comment as the captured author through the human
// comment journal. The first capture pins the provider preparation, expiring no
// later than the source authority; a repeat reconciles that same command and
// never prepares or sends again. A non-empty subject pins the issue identity.
func (s *nativeIssueEffects) capturedComment(ctx context.Context, proof nativeIssueIntakeProof, raw json.RawMessage, account *nativeProductAccount, request factory.HumanCommentCommand, subject string) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	writer := nativeHumanComments{account}
	_, err := s.Store.HumanCommentCapture(ctx, request)
	if errors.Is(err, store.ErrNotFound) {
		capture, e := writer.PrepareHumanComment(ctx, request)
		if e != nil {
			return empty, e
		}
		var preparation nativeCommentPreparation
		if nativeDecode(capture.ProviderIntent, &preparation) != nil || subject != "" && preparation.Comment.SubjectID != subject {
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
	return (factoryservice.HumanComments{Store: s.Store, Writer: writer}).Submit(ctx, request)
}

// DeliverPostFeedback adds an existing-work post to the issue JEV selected as
// the author's own comment, under the authority captured when the post was
// saved. One command per source revision makes a repeat reconcile the original
// send. A provider refusal settles that command; only a new revision sends again.
func (s *nativeIssueEffects) DeliverPostFeedback(ctx context.Context, comment store.FeedPostDetail, issue int64) error {
	if issue <= 0 {
		return factoryservice.ErrPostEffectSource
	}
	_, raw, err := s.Grant(ctx, comment.Record.ID, comment.Record.Revision)
	if err != nil {
		return err
	}
	proof, err := s.proof(raw)
	if err != nil {
		return err
	}
	if factory.MCPInputDigest([]byte(comment.Body)) != proof.Scope.Digest {
		return factoryservice.ErrPostEffectSource
	}
	account, err := s.savedAccount(ctx, int64(proof.Scope.Repository), proof.Scope.Actor, proof.Connection)
	if err != nil {
		return err
	}
	if account.identity.Account.ExternalID != proof.Account || account.repository.ExternalID != proof.Repository {
		return factoryservice.ErrCapturedIssueAuthority
	}
	key := sha256.Sum256([]byte("factory.native.post-feedback\x00" + string(proof.Profile) + "\x00" + proof.Scope.Source + "\x00" + strconv.FormatInt(proof.Scope.Revision, 10)))
	request := factory.HumanCommentCommand{ID: hex.EncodeToString(key[:16]), Host: account.binding.Host.ID, Repository: proof.Scope.Repository, Actor: proof.Scope.Actor, Subject: "issue", Number: issue,
		Body: comment.Body + "\n\n---\nFeedback from the project feed: request " + proof.Scope.Source + " r" + strconv.FormatInt(proof.Scope.Revision, 10) + "."}
	outcome, err := s.capturedComment(ctx, proof, raw, account, request, "")
	if err != nil {
		return err
	}
	switch {
	case outcome.Record != nil && outcome.Record.Effect == collaboration.EffectCommitted && outcome.Record.Completion == collaboration.CompletionComplete:
		return nil
	case outcome.Record != nil && outcome.Record.Effect == collaboration.EffectNotCommitted:
		return factoryservice.ErrPostFeedbackRefused
	}
	return factoryservice.ErrPostEffectUnknown
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
