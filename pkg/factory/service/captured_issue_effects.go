package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// IssueIntakeWriter reconciles the same captured proof/slot/step before applying
// its conditional mutation. An unknown reply must never choose another identity.
// The provider remains authoritative for proof authentication and live admission.
type IssueIntakeWriter interface {
	ApplyIssueIntake(context.Context, json.RawMessage, factory.IssueMakerMutation) (factory.IssueMakerReceipt, error)
}

// CapturedIssueEffects owns captured-source admission and the existing Issue
// Maker mutation journal. Decode interprets only server-retained provider proof;
// it does not authenticate client input. Native issuance belongs to the host.
type CapturedIssueEffects struct {
	Store  *store.Store
	Decode func(context.Context, json.RawMessage) (factory.IssueIntakeScope, error)
	Writer IssueIntakeWriter
}

var _ IssueMakerEffects = (*CapturedIssueEffects)(nil)

var (
	ErrCapturedIssueUnavailable = errors.New("captured issue delivery is unavailable")
	ErrCapturedIssueSource      = errors.New("captured issue source or role is no longer active")
	ErrCapturedIssueAuthority   = errors.New("captured issue authority is invalid or expired")
	ErrCapturedIssueReceipt     = errors.New("captured issue receipt does not match its request")
)

func (s *CapturedIssueEffects) Grant(ctx context.Context, id string, revision int64) (factory.IssueIntakeScope, json.RawMessage, error) {
	var empty factory.IssueIntakeScope
	if s == nil || s.Store == nil || s.Decode == nil {
		return empty, nil, ErrCapturedIssueUnavailable
	}
	if err := ctx.Err(); err != nil {
		return empty, nil, err
	}
	proof, err := s.Store.IssueIntakeGrant(ctx, id, revision)
	if err != nil {
		return empty, nil, err
	}
	if len(proof) == 0 || len(proof) > 8192 || !json.Valid(proof) {
		return empty, nil, ErrCapturedIssueAuthority
	}
	grant, err := s.Decode(ctx, bytes.Clone(proof))
	if err != nil {
		return empty, nil, err
	}
	if grant.Validate() != nil || grant.Source != id || grant.Revision != revision || grant.Expires <= time.Now().Unix() {
		return empty, nil, ErrCapturedIssueAuthority
	}
	post, err := s.Store.FeedPost(ctx, id)
	if err != nil {
		return empty, nil, err
	}
	captured, err := s.Store.PostUsesIssueMaker(ctx, id, revision)
	if err != nil {
		return empty, nil, err
	}
	if !captured || post.Record.ID != id || post.Record.Revision != revision || post.Record.Deleted || post.Record.Revoked ||
		int64(grant.Repository) != post.Record.Repository || strconv.FormatInt(int64(grant.Actor), 10) != post.Record.Author || grant.Digest != factory.MCPInputDigest([]byte(post.Body)) {
		return empty, nil, ErrCapturedIssueSource
	}
	if err := ctx.Err(); err != nil {
		return empty, nil, err
	}
	if grant.Expires <= time.Now().Unix() {
		return empty, nil, ErrCapturedIssueAuthority
	}
	return grant, proof, nil
}

func (s *CapturedIssueEffects) current(ctx context.Context, scope factory.IssueMakerScope) (factory.IssueIntakeScope, json.RawMessage, factory.IssueMakerProgress, error) {
	var empty factory.IssueMakerProgress
	grant, proof, err := s.Grant(ctx, scope.SourcePost, scope.Revision)
	if err != nil {
		return grant, nil, empty, err
	}
	progress, err := s.Store.PostIssueMaker(ctx, scope.SourcePost, scope.Revision)
	if err != nil {
		return grant, nil, empty, err
	}
	if progress.Validate() != nil || progress.Scope != scope || progress.State == "failed" || progress.State == "completed" {
		return factory.IssueIntakeScope{}, nil, empty, ErrCapturedIssueSource
	}
	if err := ctx.Err(); err != nil {
		return factory.IssueIntakeScope{}, nil, empty, err
	}
	return grant, proof, progress, nil
}

func (s *CapturedIssueEffects) AuthorizeIssueMaker(ctx context.Context, scope factory.IssueMakerScope) error {
	if s == nil || s.Writer == nil {
		return ErrCapturedIssueUnavailable
	}
	_, _, _, err := s.current(ctx, scope)
	return err
}

func (s *CapturedIssueEffects) RefineIssueMaker(ctx context.Context, scope factory.IssueMakerScope, draft factory.IssueDraft) error {
	if draft.Validate() != nil || draft.SourcePost != scope.SourcePost || draft.Revision != scope.Revision {
		return ErrCapturedIssueSource
	}
	_, err := s.Mutate(ctx, scope, "refine", "request", draft.Title, PostIssueBody(draft), "final-refinement")
	return err
}

func (s *CapturedIssueEffects) LookupPostIssue(ctx context.Context, repository int64, id string) (int64, error) {
	if s == nil || s.Store == nil || s.Writer == nil {
		return 0, ErrCapturedIssueUnavailable
	}
	post, err := s.Store.FeedPost(ctx, id)
	if err != nil {
		return 0, err
	}
	grant, _, err := s.Grant(ctx, id, post.Record.Revision)
	if err != nil {
		return 0, err
	}
	if int64(grant.Repository) != repository {
		return 0, ErrCapturedIssueSource
	}
	pipe, err := s.Store.PostIntake(ctx, id)
	if err != nil {
		return 0, err
	}
	if pipe.Revision != post.Record.Revision {
		return 0, ErrCapturedIssueSource
	}
	if pipe.Effect == factory.PostEffectVerified {
		return pipe.Issue, nil
	}
	// This requests reconciliation of the SAME captured slot through Apply,
	// not a claim that an external operation is absent. No new grant is issued.
	return 0, nil
}

func capturedReceipt(receipt factory.IssueMakerReceipt, grant factory.IssueIntakeScope, request factory.IssueMakerMutation) error {
	if receipt.Issue.Number <= 0 || !collaboration.ValidText(receipt.Issue.ExternalID, 256) || receipt.Issue.Kind != collaboration.SubjectIssue ||
		receipt.Issue.Repository != grant.Repository || receipt.Slot != request.Slot || receipt.Title != request.Title || receipt.BodyDigest != factory.MCPInputDigest([]byte(request.Body)) {
		return ErrCapturedIssueReceipt
	}
	return nil
}

func (s *CapturedIssueEffects) CreatePostIssue(ctx context.Context, draft factory.IssueDraft, repository int64) (int64, error) {
	if s == nil || s.Writer == nil {
		return 0, ErrCapturedIssueUnavailable
	}
	grant, _, err := s.Grant(ctx, draft.SourcePost, draft.Revision)
	if err != nil {
		return 0, err
	}
	if draft.Validate() != nil || int64(grant.Repository) != repository {
		return 0, ErrCapturedIssueSource
	}
	saved, err := s.Store.PostDraft(ctx, draft.SourcePost, draft.Revision)
	if err != nil {
		return 0, err
	}
	if saved != draft {
		return 0, ErrCapturedIssueSource
	}
	progress, err := s.Store.PostIssueMaker(ctx, draft.SourcePost, draft.Revision)
	if err != nil {
		return 0, err
	}
	grant, proof, _, err := s.current(ctx, progress.Scope)
	if err != nil {
		return 0, err
	}
	saved, err = s.Store.PostDraft(ctx, draft.SourcePost, draft.Revision)
	if err != nil {
		return 0, err
	}
	if saved != draft {
		return 0, ErrCapturedIssueSource
	}
	request := factory.IssueMakerMutation{Action: "create", Slot: 0, Step: 0, Title: draft.Title, Body: PostIssueBody(draft)}
	if err := request.Validate(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if grant.Expires <= time.Now().Unix() {
		return 0, ErrCapturedIssueAuthority
	}
	if err := s.Store.BeginPostIssueCreation(ctx, draft.SourcePost, draft.Revision); err != nil {
		return 0, err
	}
	receipt, err := s.Writer.ApplyIssueIntake(ctx, proof, request)
	if err != nil {
		return 0, err
	}
	if err := capturedReceipt(receipt, grant, request); err != nil {
		return 0, err
	}
	return receipt.Issue.Number, s.Store.RecordPostIssueCreation(ctx, draft.SourcePost, draft.Revision, receipt.Issue.Number)
}

func (s *CapturedIssueEffects) Mutate(ctx context.Context, scope factory.IssueMakerScope, action, key, title, body, operation string) (int64, error) {
	return s.mutate(ctx, scope, action, key, title, body, operation, nil)
}

func (s *CapturedIssueEffects) mutate(ctx context.Context, scope factory.IssueMakerScope, action, key, title, body, operation string, admittedWork func(context.Context) error) (int64, error) {
	if s == nil || s.Writer == nil {
		return 0, ErrCapturedIssueUnavailable
	}
	if !factory.ValidPlanKey(key) || strings.TrimSpace(title) == "" || len(title) > 255 || strings.TrimSpace(body) == "" || len(body) > 16384 || (action != "create" && action != "refine") {
		return 0, errors.New("issue needs a supported action, stable key, title (255 bytes) and body (16384 bytes)")
	}
	_, _, progress, err := s.current(ctx, scope)
	if err != nil {
		return 0, err
	}
	request := factory.IssueMakerMutation{Action: action, Title: title, Body: body}
	found := false
	for _, issue := range progress.Issues {
		if issue.Key == key {
			found = true
			request.ExpectedTitle, request.ExpectedBodyDigest = issue.Title, issue.BodyDigest
		}
	}
	if action == "refine" && !found {
		return 0, errors.New("Issue Maker may refine only issues it created for this request")
	}
	meaning, _ := json.Marshal([]string{action, key, title, body})
	request, err = s.Store.PrepareIssueMakerMutation(ctx, scope.Post, operation, factory.MCPInputDigest(meaning), key, request)
	if err != nil {
		return 0, err
	}
	// Slot/step allocation can block behind another transaction. Re-read source,
	// proof expiry and role state afterward before touching the external provider.
	grant, proof, _, err := s.current(ctx, scope)
	if err != nil {
		return 0, err
	}
	if admittedWork != nil {
		if err := admittedWork(ctx); err != nil {
			return 0, err
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if grant.Expires <= time.Now().Unix() {
		return 0, ErrCapturedIssueAuthority
	}
	receipt, err := s.Writer.ApplyIssueIntake(ctx, proof, request)
	if err != nil {
		return 0, err
	}
	if err := capturedReceipt(receipt, grant, request); err != nil {
		return 0, err
	}
	if err := s.Store.RecordIssueMakerIssue(ctx, scope, key, request.Slot, receipt.Issue.Number, title, body); err != nil {
		return 0, err
	}
	return receipt.Issue.Number, nil
}

func (s *CapturedIssueEffects) activeToolWork(ctx context.Context, cap factory.MCPCapability) error {
	if _, frozen, err := s.Store.TerminalMCPProposal(ctx, cap.AssignmentID); err != nil {
		return err
	} else if frozen {
		return store.ErrMCPSubmissionFrozen
	}
	return s.Store.CheckMCPWorkActive(ctx, cap)
}

// Tool retains the bound role's actual work admission independently of MCP's
// transport/schema checks. A caller-selected scope cannot replace its assignment.
func (s *CapturedIssueEffects) Tool(ctx context.Context, cap factory.MCPCapability, tool factory.MCPTool, payload map[string]any, submission string) (map[string]any, error) {
	if s == nil || s.Store == nil {
		return nil, ErrCapturedIssueUnavailable
	}
	if cap.Profile != factory.MCPProfileIssueMaker || (tool != factory.MCPToolCreateIssue && tool != factory.MCPToolRefineIssue) {
		return nil, errors.New("issue creation belongs to the assigned Issue Maker role")
	}
	key, keyOK := payload["key"].(string)
	title, titleOK := payload["title"].(string)
	body, bodyOK := payload["body"].(string)
	if len(payload) != 3 || !keyOK || !titleOK || !bodyOK {
		return nil, errors.New("issue tool needs only key, title and body strings")
	}
	if err := s.activeToolWork(ctx, cap); err != nil {
		return nil, err
	}
	a, err := s.Store.Assignment(ctx, cap.AssignmentID)
	if err != nil {
		return nil, err
	}
	if a.PlanningContext == nil || a.PlanningContext.IssueMaker == nil || a.Repository != cap.Repository || a.Run != cap.RunID {
		return nil, ErrCapturedIssueSource
	}
	scope := *a.PlanningContext.IssueMaker
	if err := scope.Validate(a.PlanningContext.Head); err != nil {
		return nil, err
	}
	comment, err := s.Store.FeedPost(ctx, scope.SourcePost)
	if err != nil || comment.Record.Repository != cap.Repository {
		return nil, ErrCapturedIssueSource
	}
	action, operation := "create", "create-"+key
	if tool == factory.MCPToolRefineIssue {
		action, operation = "refine", "refine-"+submission
	}
	index, err := s.mutate(ctx, scope, action, key, title, body, operation, func(ctx context.Context) error { return s.activeToolWork(ctx, cap) })
	if err != nil {
		return nil, err
	}
	return map[string]any{"issue": index, "key": key, "post_id": factory.IssueMakerReplyID(scope.Post, key), "parent_id": scope.Post}, nil
}
