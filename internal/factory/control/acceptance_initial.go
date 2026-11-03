package control

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// AdmitInitialAcceptance admits only original authenticated native creation
// facts corroborated by the current exact issue identity and unchanged inputs.
// Normal native creation has no synthesized first-history row. The native
// issue ID determines replay identity; a later human head remains authoritative.
func (c *Coordinator) AdmitInitialAcceptance(ctx context.Context, hint IntakeHint) (factory.Acceptance, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	if err := hint.Validate(); err != nil || !hint.Created || hint.NativeIssueID <= 0 {
		return factory.Acceptance{}, errors.New("invalid initial acceptance scope")
	}
	if decision, done, err := c.replayInitialAcceptance(bounded, hint); done || err != nil {
		return decision, err
	}
	evidence, err := c.readInitialAcceptanceEvidence(bounded, hint)
	if err != nil {
		return factory.Acceptance{}, err
	}
	decision, err := c.buildInitialAcceptance(bounded, hint, evidence)
	if err != nil {
		return factory.Acceptance{}, err
	}
	if err := c.Store.AdmitAcceptanceDecision(bounded, decision); err != nil {
		return factory.Acceptance{}, err
	}
	return decision, nil
}

// replayInitialAcceptance replays the recorded decision behind an
// already-admitted initial acceptance head.
func (c *Coordinator) replayInitialAcceptance(ctx context.Context, hint IntakeHint) (factory.Acceptance, bool, error) {
	decisionID := factory.InitialAcceptanceID(hint.Repository, strconv.FormatInt(hint.NativeIssueID, 10))
	if head, err := c.Store.AcceptanceHead(ctx, hint.Repository, hint.Issue); err == nil {
		if head != decisionID {
			return factory.Acceptance{}, true, store.ErrStaleRevision
		}
		decision, err := c.Store.AcceptanceDecision(ctx, head)
		return decision, true, err
	} else if !errors.Is(err, store.ErrNotFound) {
		return factory.Acceptance{}, true, err
	}
	return factory.Acceptance{}, false, nil
}

// readInitialAcceptanceEvidence reads the native evidence behind one
// initial acceptance with its exact issue identity and unchanged
// creation facts.
func (c *Coordinator) readInitialAcceptanceEvidence(ctx context.Context, hint IntakeHint) (AcceptanceEvidence, error) {
	var empty AcceptanceEvidence
	if c.AcceptanceReads == nil {
		return empty, refuseAcceptance(RefusalSnapshotUnavailable)
	}
	issue := strconv.FormatInt(hint.Issue, 10)
	evidence, err := c.readAcceptanceEvidence(ctx, factory.Acceptance{Repository: hint.Repository, IssueIndex: issue})
	if err != nil {
		var refusal *AcceptanceRefusal
		if errors.As(err, &refusal) {
			return empty, refusal
		}
		return empty, err
	}
	view := evidence.Issue
	if view.Index != issue {
		return empty, refuseAcceptance(RefusalIncompleteEvidence)
	}
	if !view.Visible {
		return empty, refuseAcceptance(RefusalIssueHidden)
	}
	if !hint.CreationVerified || view.ID != strconv.FormatInt(hint.NativeIssueID, 10) ||
		view.PosterID != strconv.FormatInt(hint.Creator, 10) || view.CreatedUnix != hint.CreatedUnix ||
		view.TitleDigest != hint.TitleDigest || view.ContentDigest != hint.ContentDigest {
		return empty, refuseAcceptance(RefusalCreationUnverified)
	}
	if view.ContentVer != 0 || view.LifecycleOccurrence != factory.OriginalOccurrence {
		return empty, refuseAcceptance(RefusalCreationEdited)
	}
	if len(evidence.Dependencies) != 0 {
		return empty, refuseAcceptance(RefusalCreationBlocked)
	}
	return evidence, nil
}

// buildInitialAcceptance builds the validated initial decision behind
// verified evidence with a non-factory creator write.
func (c *Coordinator) buildInitialAcceptance(ctx context.Context, hint IntakeHint, evidence AcceptanceEvidence) (factory.Acceptance, error) {
	var empty factory.Acceptance
	policy, err := c.Store.RepositoryPolicy(ctx, hint.Repository)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return empty, refuseAcceptance(RefusalCreationUnauthorized)
		}
		return empty, err
	}
	if hint.Creator <= 0 {
		return empty, refuseAcceptance(RefusalCreationUnverified)
	}
	for _, ref := range []factory.ActorBindingRef{policy.Publish, policy.Create, policy.Review, policy.Merge} {
		if ref.ActorID == hint.Creator {
			return empty, refuseAcceptance(RefusalCreationFactory)
		}
	}
	if !hint.CreatorWrite {
		return empty, refuseAcceptance(RefusalCreationUnauthorized)
	}
	view := evidence.Issue
	decision := factory.Acceptance{
		ID:         factory.InitialAcceptanceID(hint.Repository, strconv.FormatInt(hint.NativeIssueID, 10)),
		Repository: hint.Repository, IssueIndex: strconv.FormatInt(hint.Issue, 10),
		Approver: hint.Creator, NativeRev: evidence.Revision, Initial: true,
		TitleDigest: view.TitleDigest, TitleOccurrence: view.TitleOccurrence, ContentDigest: view.ContentDigest, ContentVersion: view.ContentVer,
	}
	if err := decision.Validate(); err != nil {
		return empty, err
	}
	return decision, nil
}
