package control

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"

	"github.com/levitateos/factory/pkg/factory/execution"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) deliveryAdmission(ctx context.Context, candidate store.DeliveredIntentCandidate) (factory.ExecutionSubject, string, []byte, error) {
	var none factory.ExecutionSubject
	revision := candidate.Revision
	policy, err := c.Store.RepositoryPolicy(ctx, revision.Repository)
	if err != nil {
		return none, "", nil, err
	}
	workspace, _, err := c.Store.ExecutionWorkspace(ctx, collaboration.RepositoryID(revision.Repository))
	if err != nil {
		return none, "", nil, err
	}
	native, err := c.DispatchReads.ReadDispatchInputs(ctx, strconv.FormatInt(revision.Repository, 10), strconv.FormatInt(candidate.FirstIssue, 10), nil, policy.TargetBranch)
	if err != nil {
		return none, "", nil, err
	}
	if native.TipRef != policy.TargetBranch || !factory.ValidCommit(native.Tip) {
		return none, "", nil, errors.New("integrated application source is unavailable")
	}
	preparations, err := c.Store.ProjectPreparations(ctx, workspace)
	if err != nil {
		return none, "", nil, err
	}
	requirements, err := c.Store.RequirementHead(ctx, workspace)
	if err != nil {
		return none, "", nil, err
	}
	approval, err := c.Store.ApprovalHead(ctx, workspace)
	if err != nil {
		return none, "", nil, err
	}
	for _, stored := range preparations {
		p := stored.Preparation
		if p.Role != project.RoleReviewer || p.SourceCommit != native.Tip || p.Requirements.ID != requirements || p.Approval.ID != approval || !stored.State.Ready {
			continue
		}
		request := project.FactoryPreparationContextRequest{Project: p.Project, ID: p.ID, SourceCommit: p.SourceCommit, ApprovedBase: p.Requirements.SourceCommit, DiffBase: p.SourceCommit, Candidate: p.SourceCommit, NotAfter: time.Now().Add(20 * time.Second)}
		scoped := execution.PreparationContextRequest{Input: request, Role: p.Role, Native: stored.Native}
		observed, err := c.Host.ReadWorkspaceContext(ctx, scoped)
		material := observed.Material
		if err != nil {
			continue
		}
		if observed.ValidateFor(scoped) != nil {
			continue
		}
		manifest, _, err := c.adoptedDeliveryCheck(ctx, revision, material.Check)
		if err != nil {
			return none, "", nil, err
		}
		if !manifest.BindsIntent(revision) {
			continue
		}
		subject := factory.DeliverySubject{IntentID: revision.Intent, AcceptedRevision: revision.ID, ScopeDigest: revision.Digest(), SourceCommit: native.Tip, PreparationDigest: factory.DeliveryPreparationDigest(p), VerificationManifestDigest: manifest.Digest()}
		subject.DeliveryID = factory.DeliveryID(subject)
		execution := factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &subject}
		// Repeat the native receipt join used by adoption before spending a worker
		// session. Missing/failed assertions remain an actionable Factory event.
		evidence, err := c.ApplicationVerificationEvidence(ctx, factory.Assignment{Repository: revision.Repository, ProjectID: workspace, Phase: project.ExecutionVerify, Role: project.RoleReviewer, Subject: execution, SourceCommit: native.Tip, Preparation: p.ID})
		if err != nil {
			return none, "", nil, err
		}
		payload, err := json.Marshal(struct {
			Objective string                  `json:"objective"`
			Revision  factory.IntentRevision  `json:"accepted_scope"`
			Subject   factory.DeliverySubject `json:"delivery"`
			Evidence  VerifiedAppEvidence     `json:"native_check_evidence"`
		}{revision.Objective, revision, subject, evidence})
		if err != nil {
			return none, "", nil, err
		}
		prompt := append([]byte("You are Test Pilot for this integrated application. Read factory.context and the recorded native application-check evidence below. Read factory.verification_scope for exact delivered outcomes and retained observations; save actual journey steps and expected/observed behavior with factory.record_journey_result. Cite a saved observation as journey:<post ID> in finish evidence. Record only observations you actually made or inspected, not executed journeys inferred from assertion text. Observations do not verify the app or authorize new fixture execution. Inspect whether the approved assertions cover every accepted outcome. Use factory.finish_app_verification with the bound delivery ID, input digest from factory.context, outcome coverage and exact check receipt only when the evidence supports it. Missing or contradictory evidence must be reported as incomplete or failed. Do not change code or claim deployment.\n\n"), payload...)
		if len(prompt) > project.MaxFactoryPrompt {
			return none, "", nil, errors.New("application verification scope exceeds the worker prompt bound")
		}
		return execution, p.ID, prompt, nil
	}
	return none, "", nil, errors.New("A ready reviewer preparation at the integrated source needs an approved check.sh with soda-app-verification assertions for every accepted outcome.")
}

// Accepted and integrated scopes become one immutable delivery operation. The
// shared dispatch path records funding and its scoped MCP before native launch.
func (c *Coordinator) driveDeliveryVerifications(ctx context.Context) {
	if c == nil || c.Store == nil || c.Host == nil || c.DispatchReads == nil {
		return
	}
	candidates, next, err := c.Store.DeliveredIntentCandidates(ctx, c.deliveryIntentCursor, 16)
	if err != nil {
		return
	}
	if next == "" && c.deliveryIntentCursor != "" {
		candidates, next, err = c.Store.DeliveredIntentCandidates(ctx, "", 16)
		if err != nil {
			return
		}
	}
	c.deliveryIntentCursor = next
	occupancy, err := snapshotOccupancy(ctx, c.Store)
	if err != nil {
		return
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		subject, preparation, prompt, err := c.deliveryAdmission(ctx, candidate)
		if err != nil {
			_ = c.Store.RecordDeliveryAdmissionWait(ctx, candidate.Revision, err.Error())
			continue
		}
		seen, err := c.Store.DeliveryAlreadyAssigned(ctx, candidate.Revision.Repository, subject.Delivery.DeliveryID)
		if err != nil {
			continue
		}
		var retry *factory.RetryDecision
		if seen {
			decision, retryErr := c.Store.PendingDeliveryRetry(ctx, candidate.Revision.Repository, *subject.Delivery)
			if retryErr != nil {
				continue
			}
			retry = &decision
			subject.Delivery.DeliveryID = factory.DeliveryRetryID(*subject.Delivery, decision.CommandID)
			// The new subject changes only the explicit attempt identity. Keep
			// the prompt bound to that identity, not the earlier incomplete run.
			prompt, err = c.deliveryRetryPrompt(ctx, candidate, subject, preparation)
			if err != nil {
				continue
			}
		}
		report := newDispatchReport()
		dispatchTimeOnlyRetry(ctx, c.dispatchDeps(), &occupancy, candidate.Revision.Repository, subject, subject.Delivery.SourceCommit, prompt, preparation, retry, &report)
		for _, wait := range report.Waits {
			_ = c.Store.RecordDeliveryAdmissionWait(ctx, candidate.Revision, wait.Detail)
		}
		for _, failure := range report.Errors {
			_ = c.Store.RecordDeliveryAdmissionWait(ctx, candidate.Revision, failure.Detail)
		}
	}
}

func (c *Coordinator) deliveryRetryPrompt(ctx context.Context, candidate store.DeliveredIntentCandidate, subject factory.ExecutionSubject, preparation string) ([]byte, error) {
	workspace, _, err := c.Store.ExecutionWorkspace(ctx, collaboration.RepositoryID(candidate.Revision.Repository))
	if err != nil {
		return nil, err
	}
	evidence, err := c.ApplicationVerificationEvidence(ctx, factory.Assignment{Repository: candidate.Revision.Repository, ProjectID: workspace, Phase: project.ExecutionVerify, Role: project.RoleReviewer, Subject: subject, SourceCommit: subject.Delivery.SourceCommit, Preparation: preparation})
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		Objective string                  `json:"objective"`
		Revision  factory.IntentRevision  `json:"accepted_scope"`
		Subject   factory.DeliverySubject `json:"delivery"`
		Evidence  VerifiedAppEvidence     `json:"native_check_evidence"`
	}{candidate.Revision.Objective, candidate.Revision, *subject.Delivery, evidence})
	if err != nil {
		return nil, errors.New("application verification scope exceeds the worker prompt bound")
	}
	prompt := append([]byte("You are Test Pilot for this integrated application. This is an explicit owner Retry after an incomplete or failed verification. Inspect the approved assertions and exact native evidence below against every accepted outcome. Read factory.context for the current input digest and factory.verification_scope for delivered outcomes and retained observations. Save actual journey steps and expected/observed behavior with factory.record_journey_result; cite a saved observation as journey:<post ID> in finish evidence. Record only observations you actually made or inspected, not executed journeys inferred from assertion text. Recording an observation does not verify the app or authorize new fixture execution. Report findings truthfully through factory.finish_app_verification. Do not change code, claim deployment or infer missing coverage.\n\n"), payload...)
	if len(prompt) > project.MaxFactoryPrompt {
		return nil, errors.New("application verification scope exceeds the worker prompt bound")
	}
	return prompt, nil
}
