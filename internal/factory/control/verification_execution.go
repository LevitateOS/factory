package control

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// adoptedDeliveryCheck resolves the existing accepted definition. The delegated
// outcome map needs no future accepted-revision marker in draft-time scripts.
// A nil adopted manifest retains the original explicit check.sh declaration.
func (c *Coordinator) adoptedDeliveryCheck(ctx context.Context, r factory.IntentRevision, script []byte) (factory.ApprovedAppCheck, *factory.AdoptedVerificationManifest, error) {
	g, err := c.Store.IntentGeneration(ctx, r.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return factory.ApprovedAppCheck{}, nil, err
	}
	if g.VerificationManifest == nil {
		check, err := factory.ParseApprovedAppCheck(script)
		if err == nil && !check.BindsIntent(r) {
			err = errors.New("approved check does not cover the accepted delivery")
		}
		return check, nil, err
	}
	m := g.VerificationManifest
	if m.ValidateFor(r) != nil || g.VerificationManifestDigest != m.Digest() || factory.MCPInputDigest(script) != m.Catalog.CheckDigest {
		return factory.ApprovedAppCheck{}, nil, errors.New("integrated approved check differs from adopted verification definitions")
	}
	check := factory.ApprovedAppCheck{IntentID: r.Intent, AcceptedRevision: r.ID, Outcomes: slices.Clone(m.Proposal.Outcomes)}
	return check, m, check.Validate()
}

func adoptVerificationChecks(run *factory.Run, state execution.RunObservation) error {
	if state.VerificationChecks == nil {
		return nil
	}
	m, err := project.ParseVerificationManifest(run.VerificationManifest)
	if err != nil {
		return err
	}
	request := project.FactoryVerificationChecks{Project: run.ProjectID, Run: run.ID, Incarnation: state.InvocationID(), ManifestDigest: project.ExecutionManifestDigest(run.VerificationManifest)}
	s := state.VerificationChecks
	if s.ValidateFor(request) != nil || s.Preparation != m.Preparation || s.SourceCommit != m.SourceCommit || s.CheckDigest != m.CheckDigest {
		return errors.New("native verification receipt differs from the admitted manifest")
	}
	if prior := run.VerificationChecks; prior != nil && (prior.Phase == "passed" || prior.Phase == "failed" || prior.Phase == "stopped") && !reflect.DeepEqual(prior, s) {
		return errors.New("native verification receipt changed after settlement")
	}
	copy := *s
	run.VerificationChecks = &copy
	return nil
}

func (c *Coordinator) populateVerificationExecution(ctx context.Context, a factory.Assignment, e *VerifiedAppEvidence) error {
	if a.Run == "" {
		return nil
	}
	run, err := c.Store.FactoryRun(ctx, a.Run)
	if err != nil {
		return err
	}
	m, err := project.ParseVerificationManifest(run.VerificationManifest)
	if err != nil || m.Run != a.Run || m.Project != a.ProjectID || m.Preparation != a.Preparation || m.SourceCommit != a.SourceCommit || !m.Deadline.Equal(run.Deadline) || m.Intent != a.Subject.Delivery.IntentID || m.AcceptedRevision != a.Subject.Delivery.AcceptedRevision || m.ScopeDigest != a.Subject.Delivery.ScopeDigest || m.AdoptedManifestDigest != e.AdoptedManifest.Digest() {
		return errors.New("verification run lacks its exact admitted native manifest")
	}
	state, err := c.Host.InspectRun(ctx, execution.RunAddress{Workspace: a.ProjectID, Run: a.Run})
	if errors.Is(err, execution.ErrRunNotObserved) && !run.HasExecutionIdentity() {
		return nil
	}
	if err != nil {
		return err
	}
	checks := state.VerificationChecks
	if checks == nil {
		if state.Live || state.CredentialDeliveryObserved() {
			return errors.New("worker started without its required manifest receipt")
		}
		return nil
	}
	request := project.FactoryVerificationChecks{Project: a.ProjectID, Run: a.Run, Incarnation: state.InvocationID(), ManifestDigest: project.ExecutionManifestDigest(run.VerificationManifest)}
	if checks.ValidateFor(request) != nil || checks.SourceCommit != m.SourceCommit || checks.Preparation != m.Preparation || checks.CheckDigest != m.CheckDigest {
		return errors.New("native manifest execution differs from admitted run")
	}
	e.Execution = checks
	if checks.Phase == "passed" {
		excerpt := checks.OutputExcerpt
		if excerpt == "" {
			excerpt = "Native admitted verification manifest passed; no output captured."
		} else if strings.TrimSpace(excerpt) == "" {
			excerpt = "Native admitted verification manifest passed; captured output contains only whitespace (see verification_checks.output_excerpt)."
		}
		e.ExecutionReceipt = &factory.VerificationCheckReceipt{CheckID: factory.ApprovedAppCheckID, Outcome: factory.CheckReceiptPassed, ArtifactDigest: checks.OutputDigest, LogExcerpt: excerpt}
	}
	return nil
}

// stageDeliveryVerificationManifest is called before the atomic dispatch packet.
// Its exact bytes remain on the recorded run through context holds and recovery.
func (c *Coordinator) stageDeliveryVerificationManifest(ctx context.Context, a factory.Assignment, run factory.Run) ([]byte, error) {
	if a.Subject.Kind != factory.SubjectDelivery {
		return nil, nil
	}
	generation, err := c.Store.IntentGeneration(ctx, a.Subject.Delivery.AcceptedRevision)
	if errors.Is(err, store.ErrNotFound) || err == nil && generation.VerificationManifest == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, _, err := c.Store.ApplicationVerificationEvidenceInputs(ctx, a)
	if err != nil {
		return nil, err
	}
	stored, err := c.Store.Preparation(ctx, a.Preparation)
	if err != nil {
		return nil, err
	}
	p := stored.Preparation
	request := project.FactoryPreparationContextRequest{Project: p.Project, ID: p.ID, SourceCommit: p.SourceCommit, ApprovedBase: p.SourceCommit, DiffBase: p.SourceCommit, Candidate: p.SourceCommit, NotAfter: time.Now().Add(20 * time.Second)}
	scoped := execution.PreparationContextRequest{Input: request, Role: p.Role, Native: stored.Native}
	observed, err := c.Host.ReadWorkspaceContext(ctx, scoped)
	material := observed.Material
	if err != nil {
		return nil, err
	}
	if err := observed.ValidateFor(scoped); err != nil {
		return nil, err
	}
	check, adopted, err := c.adoptedDeliveryCheck(ctx, r, material.Check)
	if err != nil {
		return nil, err
	}
	if adopted == nil {
		return nil, nil
	}
	if p.Project != a.ProjectID || p.Role != project.RoleReviewer || p.SourceCommit != a.SourceCommit || factory.DeliveryPreparationDigest(p) != a.Subject.Delivery.PreparationDigest || check.Digest() != a.Subject.Delivery.VerificationManifestDigest {
		return nil, errors.New("verification execution differs from admitted delivery")
	}
	m := project.VerificationManifest{Version: project.VerificationManifestVersion, Run: run.ID, Project: p.Project, Preparation: p.ID, SourceCommit: p.SourceCommit, Deadline: run.Deadline, Intent: r.Intent, AcceptedRevision: r.ID, ScopeDigest: r.Digest(), AdoptedManifestDigest: adopted.Digest(), Requirements: p.Requirements.ID, Approval: p.Approval.ID, PreparationDigest: factory.DeliveryPreparationDigest(p), CheckDigest: adopted.Catalog.CheckDigest, Outcomes: slices.Clone(check.Outcomes), Checks: []project.VerificationManifestCheck{{CatalogID: project.VerificationProjectCheck, Outcomes: slices.Clone(check.Outcomes), Artifacts: []string{project.VerificationReceiptArtifact}, Fixtures: []string{project.VerificationPreparedFixture}}}}
	return project.MarshalVerificationManifest(m)
}
