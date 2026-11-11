package control

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// publicationAuthority validates Soda policy and accepted input at the native
// revision that the later ref observation must match.
func (c *Coordinator) publicationAuthority(ctx context.Context, p factory.Publication, report *PublishReport) (factory.RepositoryPolicy, int64, bool) {
	var empty factory.RepositoryPolicy
	owner, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return empty, 0, false
	}
	retained := owner.RetainedPublication != nil
	if retained && (owner.Validate() != nil || owner.RetainedPublication.Run != p.Run || owner.RetainedPublication.Candidate != p.Candidate) {
		publicationWait(report, p.ID, "retained_candidate_changed")
		return empty, 0, false
	}
	current, err := c.currentPublicationAuthority(ctx, p, retained)
	if err != nil {
		publicationWait(report, p.ID, "authority_ineffective")
		return empty, 0, false
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return empty, 0, false
	}
	if !current.SameGrants(p.Authority) || policy.TargetBranch != p.TargetBranch {
		publicationWait(report, p.ID, "authority_changed")
		return empty, 0, false
	}
	status, err := c.AcceptanceStatus(ctx, p.Repository, strconv.FormatInt(p.Issue, 10))
	if err != nil {
		publicationWait(report, p.ID, "accepted_inputs_unavailable")
		return empty, 0, false
	}
	if !status.Valid || status.Acceptance == nil || status.Acceptance.ID != p.Acceptance {
		publicationWait(report, p.ID, "accepted_inputs_changed")
		return empty, 0, false
	}
	return policy, status.Revision, true
}

// A sealed candidate needs no live worker preparation to be exported. This
// exception applies only to the explicit non-executing publication command;
// all standing grants, source consent, dispatch and accepted inputs still apply.
func (c *Coordinator) currentPublicationAuthority(ctx context.Context, p factory.Publication, retained bool) (factory.AuthorityRef, error) {
	if p.External != nil && p.External.Automatic {
		policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
		if err != nil {
			return factory.AuthorityRef{}, err
		}
		request, _ := p.AutomaticExternalAdoption(policy)
		reader, ok := c.Reviews.(automaticExternalPullSource)
		if !ok {
			return factory.AuthorityRef{}, ErrIneffectiveAuthority
		}
		if err = reader.CheckAutomaticExternalPull(ctx, request); err != nil {
			return factory.AuthorityRef{}, err
		}
	}
	in, err := c.Store.FactoryAuthorityInput(ctx, p.Repository)
	if err != nil {
		return factory.AuthorityRef{}, err
	}
	if retained {
		in.PreparationReady = true
	}
	effective := factory.EvaluateAuthority(in)
	if !effective.Effective {
		return factory.AuthorityRef{}, ErrIneffectiveAuthority
	}
	current := effective.Authority
	current.RequirementsID, err = c.Store.RequirementHead(ctx, p.ProjectID)
	if err != nil {
		return factory.AuthorityRef{}, err
	}
	current.ApprovalID, err = c.Store.ApprovalHead(ctx, p.ProjectID)
	if err != nil {
		return factory.AuthorityRef{}, err
	}
	if retained && (current.RequirementsID != p.Authority.RequirementsID || current.ApprovalID != p.Authority.ApprovalID || current.SponsorshipConnection != p.Authority.SponsorshipConnection) {
		return factory.AuthorityRef{}, errors.Join(store.ErrRetainedPublicationIneligible, ErrIneffectiveAuthority)
	}
	return current, nil
}

func (c *Coordinator) publicationWork(a factory.Assignment, p factory.Publication, run factory.Run, bundle []byte, policy factory.RepositoryPolicy, kind string) factory.PublicationWork {
	actor, old := policy.Publish.ActorID, "absent"
	if kind == factory.OpPRCreate {
		actor, old = policy.Create.ActorID, p.Candidate
	}
	return factory.PublicationWork{
		Bundle: bundle, AssignmentID: a.ID, Publication: p.ID, RunID: p.Run, Run: run,
		Candidate: p.Candidate, BaseSHA: p.BaseSHA, TargetBranch: p.TargetBranch,
		OperationID: factory.PublicationOperationID(p.ID, kind, 1), AuthRevision: factory.AuthRevisionFor(a.ID, p.ID, p.Revision),
		ExpectedOld: old, ComparisonRef: p.TargetBranch, PRTitle: factory.PRTitleFor(a.Issue),
		PRBody:     factory.PRBodyFor(a.Issue, a.ID, a.Acceptance, p.Run, p.Candidate, a.SourceCommit),
		Repository: p.Repository, Issue: p.Issue, ActorID: actor,
	}
}
