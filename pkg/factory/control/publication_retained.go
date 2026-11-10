package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) RetryUnsentReview(ctx context.Context, command, principal string, retry factory.ReviewRetry) (any, error) {
	if c.Store == nil || c.Reviews == nil || retry.Validate() != nil || !factory.ValidID(command) {
		return nil, store.ErrRetainedPublicationIneligible
	}
	receipt := struct {
		CommandID string `json:"command_id"`
		Retry factory.ReviewRetry `json:"review_retry"`
	}{command, retry}
	if prior, err := c.Store.FactoryCommand(ctx, command); err == nil {
		var capture struct { Retry factory.ReviewRetry `json:"retry"` }
		if prior.Type != factory.CommandRetryReview || prior.Principal != principal || prior.Target != "publication/"+retry.Publication ||
			prior.Finished == "" || json.Unmarshal([]byte(prior.Payload), &capture) != nil || capture.Retry != retry {
			return nil, store.ErrCommandConflict
		}
		return receipt, nil
	} else if !errors.Is(err, store.ErrNotFound) { return nil, err }
	offer, err := c.Store.ReviewRetryOffer(ctx, retry.Publication)
	if err != nil { return nil, err }
	if offer != retry { return nil, store.ErrAdmissionChanged }
	p, prior, err := c.Store.PublicationReviewOperation(ctx, retry.Operation)
	if err != nil { return nil, err }
	if p.ID != retry.Publication || prior.RunID != retry.Run || !prior.DefinitiveNoSend() {
		return nil, store.ErrRetainedPublicationIneligible
	}
	// A causal native no-send is required. Missing journal/lookup evidence is
	// never permission to replace the original command or repeat its model.
	observed, err := c.Reviews.LookupOp(ctx, prior.Work.OperationID)
	if err != nil { return nil, err }
	if !sameOperationOutcome(prior.Outcome, observed) { return nil, store.ErrAdmissionChanged }
	w := prior.Work
	w.OperationID = "review-retry-"+command
	target, err := c.Reviews.ObserveReview(ctx, w)
	if err != nil { return nil, err }
	now := time.Now()
	w.NativeRev, w.NotAfter = target.NativeRev, now.Add(5*time.Minute).Unix()
	if _, err = c.Store.RecordReviewRetry(ctx, command, principal, retry, w, now); err != nil { return nil, err }
	// The ordinary reconciliation owner dispatches this registered operation.
	// Lost API replies return this same admission; they never extend its clock.
	return receipt, nil
}

func (c *Coordinator) FundRetainedCandidate(ctx context.Context, command, principal string, funding factory.RetainedCandidateFunding) (factory.RetainedCandidateFundingReceipt, error) {
	if c.Store == nil || c.Publication == nil || c.Host == nil || c.Broker == nil {
		return factory.RetainedCandidateFundingReceipt{}, store.ErrRetainedPublicationIneligible
	}
	if _, err := c.Store.FactoryCommand(ctx, command); err == nil {
		return c.Store.FundRetainedCandidate(ctx, command, principal, funding, time.Now())
	} else if !errors.Is(err, store.ErrNotFound) {
		return factory.RetainedCandidateFundingReceipt{}, err
	}
	offer, err := c.Store.RetainedCandidateFundingOffer(ctx, funding.Publication)
	if err != nil {
		return factory.RetainedCandidateFundingReceipt{}, err
	}
	if funding.Validate() != nil || funding.Limits.ActiveMinutes > offer.Limits.ActiveMinutes || funding.Limits.ReviewRounds > offer.Limits.ReviewRounds || funding.Limits.CorrectionCycles > offer.Limits.CorrectionCycles || funding.Limits.MaxChildren > offer.Limits.MaxChildren {
		return factory.RetainedCandidateFundingReceipt{}, store.ErrRetainedPublicationIneligible
	}
	offer.Limits = funding.Limits
	if !reflect.DeepEqual(offer, funding) {
		return factory.RetainedCandidateFundingReceipt{}, store.ErrRetainedPublicationIneligible
	}
	p, err := c.Store.PublicationByID(ctx, funding.Publication)
	if err != nil {
		return factory.RetainedCandidateFundingReceipt{}, err
	}
	owner, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil || !owner.PublicationOwnerReady() {
		return factory.RetainedCandidateFundingReceipt{}, store.ErrRetainedPublicationIneligible
	}
	if funding.PreviousRoot != "" {
		if c.Reviews == nil || p.PRCreate.Work == nil {
			return factory.RetainedCandidateFundingReceipt{}, store.ErrRetainedPublicationIneligible
		}
		policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
		if err != nil {
			return factory.RetainedCandidateFundingReceipt{}, err
		}
		// This is a current authenticated read, not a new provider submission.
		// Head, base, canonical PR identity and author must still match the
		// retained complete creation receipt before fresh inference is funded.
		_, err = c.Reviews.ObserveReview(ctx, factory.ReviewWork{Repository: p.Repository, ActorID: policy.Review.ActorID,
			PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PullTarget().IssueID, PRAuthorID: p.PullTarget().AuthorID,
			HeadRef: p.PullTarget().HeadRef, BaseRef: p.PullTarget().BaseRef, HeadOID: p.Candidate, BaseOID: p.PullTarget().BaseOID})
		if err != nil {
			return factory.RetainedCandidateFundingReceipt{}, err
		}
	}
	// Retire the original known custody before the new allowance can admit
	// work. Lost replies resume these same native tombstones, never a new send.
	origin, err := c.Store.PublicationExecutionOwner(ctx, p, owner)
	if err != nil {
		return factory.RetainedCandidateFundingReceipt{}, err
	}
	if err = c.stopCandidatePreparationsForRoot(ctx, p.Repository, p.ProjectID, p.Issue, origin.ID); err != nil {
		return factory.RetainedCandidateFundingReceipt{}, err
	}
	if owner.ID != origin.ID {
		if err = c.stopCandidatePreparationsForRoot(ctx, p.Repository, p.ProjectID, p.Issue, owner.ID); err != nil {
			return factory.RetainedCandidateFundingReceipt{}, err
		}
	}
	return c.Store.FundRetainedCandidate(ctx, command, principal, funding, time.Now())
}

// PublishRetainedCandidate authorizes a new branch and PR, never a second send
// of the old operation. It neither renews the closed attempt nor starts models.
func (c *Coordinator) PublishRetainedCandidate(ctx context.Context, commandID, principal, publicationID string) (factory.Publication, error) {
	if c.Store == nil || c.Publication == nil || !factory.ValidID(commandID) || !factory.ValidID(publicationID) {
		return factory.Publication{}, store.ErrRetainedPublicationIneligible
	}
	payload, _ := json.Marshal(struct {
		Publication string `json:"publication"`
	}{publicationID})
	cmd := factory.Command{ID: commandID, Type: factory.CommandPublishRetained, Target: "publication/" + publicationID, Principal: principal, Payload: string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	if err := cmd.Validate(); err != nil {
		return factory.Publication{}, err
	}
	if prior, err := c.Store.FactoryCommand(ctx, commandID); err == nil {
		if prior.Type != cmd.Type || prior.Target != cmd.Target || prior.Principal != cmd.Principal || prior.Digest != cmd.Digest || prior.Finished == "" {
			return factory.Publication{}, store.ErrCommandConflict
		}
		return c.Store.PublicationByAssignment(ctx, commandID)
	} else if err != store.ErrNotFound {
		return factory.Publication{}, err
	}
	original, err := c.Store.PublicationByID(ctx, publicationID)
	if err != nil {
		return factory.Publication{}, err
	}
	owner, err := c.Store.Assignment(ctx, original.AssignmentID)
	if err != nil || owner.Validate() != nil || owner.RetainedPublication != nil {
		return factory.Publication{}, fmt.Errorf("%w: original execution owner is unavailable", store.ErrRetainedPublicationIneligible)
	}
	current, err := c.currentPublicationAuthority(ctx, original, true)
	if err != nil {
		return factory.Publication{}, err
	}
	// Capture new standing authority only under this explicit command. The
	// earlier operation retains its original revisions and deadline forever.
	status, err := c.AcceptanceStatus(ctx, original.Repository, strconv.FormatInt(original.Issue, 10))
	policy, policyErr := c.Store.RepositoryPolicy(ctx, original.Repository)
	if err != nil || policyErr != nil || !status.Valid || status.Acceptance == nil || status.Acceptance.ID != original.Acceptance || policy.TargetBranch != original.TargetBranch {
		return factory.Publication{}, fmt.Errorf("%w: accepted inputs or target branch changed", store.ErrRetainedPublicationIneligible)
	}
	now := time.Now()
	a := factory.Assignment{ID: commandID, AttemptRoot: commandID, PublicationAssignment: commandID,
		Subject:   factory.ExecutionSubject{Kind: factory.SubjectIssue, Issue: &factory.IssueSubject{Issue: original.Issue, Acceptance: original.Acceptance}},
		ProjectID: original.ProjectID, Role: project.RoleCoder, Authority: current, ActorID: owner.ActorID,
		Acceptance: original.Acceptance, Preparation: original.Preparation, SourceCommit: original.BaseSHA,
		Stage: factory.AssignmentRetained, NativeRev: status.Revision, CreatedUnix: now.Unix(),
		RetainedPublication: &factory.RetainedPublication{Publication: original.ID, Assignment: owner.ID, Run: original.Run, Candidate: original.Candidate, Connection: owner.Connection, NotAfter: now.Add(5 * time.Minute).Unix()}}
	p := factory.Publication{ID: commandID, AssignmentID: commandID, ProjectID: original.ProjectID, Role: project.RoleCoder,
		Authority: current, Acceptance: original.Acceptance, Preparation: original.Preparation, Run: original.Run,
		Candidate: original.Candidate, BaseSHA: original.BaseSHA, TargetBranch: original.TargetBranch, Stage: factory.PublicationOpen,
		Repository: original.Repository, Issue: original.Issue, CreatedUnix: now.Unix(),
		Publish: factory.PublicationOperation{Kind: factory.OpRefPublish}, PRCreate: factory.PublicationOperation{Kind: factory.OpPRCreate}}
	a.Repository, a.Issue = p.Repository, p.Issue
	return c.Store.RecordRetainedPublicationCommand(ctx, cmd, a, p, now)
}
