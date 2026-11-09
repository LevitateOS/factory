package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) FundRetainedCandidate(ctx context.Context, command, principal string, funding factory.RetainedCandidateFunding) (factory.RetainedCandidateFundingReceipt, error) {
	if c.Store == nil || c.Publication == nil || c.Host == nil || c.Broker == nil {
		return factory.RetainedCandidateFundingReceipt{}, store.ErrRetainedPublicationIneligible
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
