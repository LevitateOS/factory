package control

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// VerifiedAppEvidence is independently read from the native helper's protected
// approval snapshot and check receipt. It is also useful to Test Pilot when
// constructing its finish report; the controller repeats the read on adoption.
type VerifiedAppEvidence = factory.CurrentVerificationEvidence

// ApplicationVerificationEvidence never executes model-provided commands. The
// single local catalog entry is the exact approved check.sh already executed by
// the native preparation supervisor. An ordinary preparation without explicit
// application assertions does not qualify.
func (c *Coordinator) ApplicationVerificationEvidence(ctx context.Context, a factory.Assignment) (VerifiedAppEvidence, error) {
	var evidence VerifiedAppEvidence
	if c == nil || c.Store == nil || c.Host == nil || c.DispatchReads == nil {
		return evidence, errors.New("native application verification is unavailable")
	}
	if a.Subject.Delivery == nil || a.Phase != project.ExecutionVerify || a.Role != project.RoleReviewer {
		return evidence, errors.New("assignment is not application verification")
	}
	revision, prerequisites, err := c.Store.ApplicationVerificationEvidenceInputs(ctx, a)
	if err != nil {
		return evidence, err
	}
	preparation, err := c.Store.Preparation(ctx, a.Preparation)
	if err != nil {
		return evidence, err
	}
	p := preparation.Preparation
	if p.Project != a.ProjectID || p.Role != project.RoleReviewer || p.SourceCommit != a.SourceCommit || factory.DeliveryPreparationDigest(p) != a.Subject.Delivery.PreparationDigest {
		return evidence, errors.New("application preparation differs from admitted delivery")
	}
	state, err := c.Host.InspectWorkspacePreparation(ctx, preparation.Address())
	if err != nil {
		return evidence, err
	}
	if state.ValidateFor(preparation.Address()) != nil || state.ID != p.ID || state.Project != p.Project || state.Role != p.Role || state.SourceCommit != p.SourceCommit || state.SetupDigest != p.SetupDigest || !state.Ready || state.Stopped || state.Phase != project.PrepareReady || state.CheckExit == nil || *state.CheckExit != 0 {
		return evidence, errors.New("approved application check has no native passing receipt")
	}
	// Keep the maintainer's selected paths, but inspect their actual integrated
	// contents from this exact ready checkout. The protected setup/check scripts
	// still come from the original native approval snapshot.
	material, err := c.ReadApplicationVerificationContext(ctx, preparation)
	if err != nil {
		return evidence, err
	}
	manifest, adopted, err := c.adoptedDeliveryCheck(ctx, revision, material.Check)
	if err != nil {
		return evidence, err
	}
	if !manifest.BindsIntent(revision) || manifest.Digest() != a.Subject.Delivery.VerificationManifestDigest {
		return evidence, errors.New("approved application check does not cover this accepted delivery")
	}
	policy, err := c.Store.RepositoryPolicy(ctx, a.Repository)
	if err != nil {
		return evidence, err
	}
	native, err := c.DispatchReads.ReadDispatchInputs(ctx, strconv.FormatInt(a.Repository, 10), strconv.FormatInt(prerequisites.Tasks[0].IssueIndex, 10), nil, policy.TargetBranch)
	if err != nil {
		return evidence, err
	}
	if native.TipRef != policy.TargetBranch || native.Tip != a.SourceCommit {
		return evidence, errors.New("integrated application source changed since verification admission")
	}
	evidence = VerifiedAppEvidence{
		Manifest: manifest, ManifestDigest: manifest.Digest(), PreparationDigest: factory.DeliveryPreparationDigest(p), SourceCommit: a.SourceCommit, ApprovedContext: factory.NewApprovedVerificationContext(material), DeliveryPrerequisites: &prerequisites, State: state, AdoptedManifest: adopted, ExecutionRequired: adopted != nil,
		Receipt: factory.VerificationCheckReceipt{CheckID: factory.ApprovedAppCheckID, Outcome: factory.CheckReceiptPassed, ArtifactDigest: factory.MCPInputDigest([]byte(state.Output)), LogExcerpt: "Native approved application check passed"},
	}
	if adopted != nil {
		if err := c.populateVerificationExecution(ctx, a, &evidence); err != nil {
			return evidence, err
		}
	}
	return evidence, evidence.ValidateFor(*a.Subject.Delivery)
}

// ReadApplicationVerificationContext reads the selected files at the integrated
// preparation commit. The original selection's digests describe its approved
// base; current evidence carries the actual integrated contents of those paths.
func (c *Coordinator) ReadApplicationVerificationContext(ctx context.Context, preparation execution.PreparationRecord) (project.FactoryPreparationContext, error) {
	var material project.FactoryPreparationContext
	if c == nil || c.Host == nil || preparation.Validate() != nil {
		return material, execution.ErrPreparationContext
	}
	p := preparation.Preparation
	paths := make([]string, 0, len(p.Requirements.SourceInputs))
	for name := range p.Requirements.SourceInputs {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	chunks, err := chunkApprovedSelections(paths)
	if err != nil {
		return material, err
	}
	if len(chunks) == 0 {
		chunks = [][]string{{}}
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	deadline, _ := bounded.Deadline()
	selected := make(map[string][]byte, len(paths))
	total := 0
	for index, chunk := range chunks {
		request := execution.PreparationContextRequest{Native: preparation.Native, Role: p.Role, Input: project.FactoryPreparationContextRequest{Project: p.Project, ID: p.ID, SourceCommit: p.SourceCommit, ApprovedBase: p.SourceCommit, DiffBase: p.SourceCommit, Candidate: p.SourceCommit, Paths: chunk, NotAfter: deadline}}
		if err := request.Validate(); err != nil {
			return material, err
		}
		observed, err := c.Host.ReadWorkspaceContext(bounded, request)
		if err != nil {
			return material, err
		}
		if err := observed.ValidateFor(request); err != nil {
			return material, err
		}
		part := observed.Material
		if index == 0 {
			material = part
			material.Files = nil
			total = len(part.Setup) + len(part.Check) + len(part.Diff)
		} else if !bytes.Equal(material.Setup, part.Setup) || !bytes.Equal(material.Check, part.Check) || !bytes.Equal(material.Diff, part.Diff) {
			return material, errors.New("verification context changed between source reads")
		}
		seenRead := make(map[string]bool, len(part.Files))
		for _, file := range part.Files {
			if seenRead[file.Path] {
				return material, errors.New("approved verification context repeats a source file")
			}
			seenRead[file.Path] = true
			if _, wanted := p.Requirements.SourceInputs[file.Path]; !wanted {
				continue
			}
			if prior, exists := selected[file.Path]; exists && !bytes.Equal(prior, file.Content) {
				return material, errors.New("verification source changed between reads")
			}
			if _, exists := selected[file.Path]; !exists {
				total += len(file.Content)
				if total > project.MaxFactoryContextBytes {
					return material, errors.New("approved verification context exceeds its text bound")
				}
			}
			selected[file.Path] = file.Content
		}
	}
	for _, name := range paths {
		content, exists := selected[name]
		if !exists {
			return material, errors.New("approved verification context omits a selected source file")
		}
		material.Files = append(material.Files, project.FactoryContextFile{Path: name, Content: content})
	}
	return material, material.ValidateSelectedContent(p.Role)
}

func (c *Coordinator) verifyApplicationReport(ctx context.Context, a factory.Assignment) error {
	if a.Stage != factory.AssignmentFinished || a.Outcome != factory.Succeeded || a.Result == nil || !a.Result.Reported || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Verification == nil {
		return errors.New("application verification has no completed report")
	}
	report := a.Result.PhaseResult.Output.Verification
	if err := a.Result.PhaseResult.BindAssignment(a); err != nil {
		return err
	}
	evidence, err := c.ApplicationVerificationEvidence(ctx, a)
	if err != nil {
		return err
	}
	receipt := evidence.Receipt
	if evidence.ExecutionRequired {
		if evidence.Execution == nil || evidence.Execution.Phase != "passed" || evidence.ExecutionReceipt == nil {
			return errors.New("adopted verification manifest has no exact native run receipt")
		}
		receipt = *evidence.ExecutionReceipt
	}
	if report.Outcome != factory.AppVerificationVerified || len(report.CheckReceipts) != 1 || report.CheckReceipts[0].CheckID != factory.ApprovedAppCheckID || report.CheckReceipts[0].Outcome != factory.CheckReceiptPassed || report.CheckReceipts[0].ArtifactDigest != receipt.ArtifactDigest {
		return errors.New("worker verification report differs from native check receipt")
	}
	covered := map[string]bool{}
	for _, coverage := range report.Coverage {
		if coverage.IntentID != evidence.Manifest.IntentID || covered[coverage.OutcomeID] {
			return errors.New("worker report does not cover the exact accepted application scope")
		}
		covered[coverage.OutcomeID] = true
	}
	if len(covered) != len(evidence.Manifest.Outcomes) {
		return errors.New("worker report does not cover the exact accepted application scope")
	}
	for _, outcome := range evidence.Manifest.Outcomes {
		if !covered[outcome] {
			return errors.New("worker report omits an accepted application outcome")
		}
	}
	return c.Store.RecordVerifiedApplication(ctx, a, evidence.Manifest, evidence.State)
}

// driveAppVerifications runs after MCP/run result settlement, including restart
// recovery. Missing approved assertions or stale/native evidence emits F09;
// only the complete independent join can produce the verified F10 milestone.
func (c *Coordinator) driveAppVerifications(ctx context.Context) {
	if c == nil || c.Store == nil {
		return
	}
	assignments, next, err := c.Store.PendingAppVerifications(ctx, c.appVerificationCursor, 16)
	if err != nil {
		return
	}
	if next == 0 && c.appVerificationCursor != 0 {
		assignments, next, err = c.Store.PendingAppVerifications(ctx, 0, 16)
		if err != nil {
			return
		}
	}
	c.appVerificationCursor = next
	for _, a := range assignments {
		if ctx.Err() != nil {
			return
		}
		if err := c.verifyApplicationReport(ctx, a); err != nil {
			_ = c.Store.RecordAppVerificationWait(ctx, a, err.Error())
		}
	}
}
