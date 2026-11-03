package control

import (
	"context"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
)

// publicationAuthority validates Soda policy and accepted input at the native
// revision that the later ref observation must match.
func (c *Coordinator) publicationAuthority(ctx context.Context, p factory.Publication, report *PublishReport) (factory.RepositoryPolicy, int64, bool) {
	var empty factory.RepositoryPolicy
	effective, err := c.EffectiveAuthority(ctx, p.Repository)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return empty, 0, false
	}
	if !effective.Effective {
		publicationWait(report, p.ID, "authority_ineffective")
		return empty, 0, false
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return empty, 0, false
	}
	current := effective.Authority
	current.RequirementsID, err = c.Store.RequirementHead(ctx, p.ProjectID)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return empty, 0, false
	}
	current.ApprovalID, err = c.Store.ApprovalHead(ctx, p.ProjectID)
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
		PRBody:     factory.PRBodyFor(a.Issue, a.ID, a.Acceptance, a.Run, p.Candidate, a.SourceCommit),
		Repository: p.Repository, Issue: p.Issue, ActorID: actor,
	}
}
