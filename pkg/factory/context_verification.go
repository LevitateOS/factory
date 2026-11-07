package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// CurrentVerificationEvidence combines the native supervisor's approved fixture
// and receipt with canonical Factory delivery prerequisites. State is for
// controller adoption only, never the model projection.
type CurrentVerificationEvidence struct {
	Manifest              ApprovedAppCheck                        `json:"manifest"`
	ManifestDigest        string                                  `json:"manifest_digest"`
	PreparationDigest     string                                  `json:"preparation_digest"`
	SourceCommit          string                                  `json:"source_commit"`
	Receipt               VerificationCheckReceipt                `json:"check_receipt"`
	ApprovedContext       *ApprovedVerificationContext            `json:"approved_context,omitempty"`
	DeliveryPrerequisites *VerificationDeliveryPrerequisites      `json:"delivery_prerequisites,omitempty"`
	AdoptedManifest       *AdoptedVerificationManifest            `json:"adopted_manifest,omitempty"`
	ExecutionRequired     bool                                    `json:"execution_required,omitempty"`
	Execution             *project.FactoryVerificationChecksState `json:"verification_checks,omitempty"`
	ExecutionReceipt      *VerificationCheckReceipt               `json:"manifest_check_receipt,omitempty"`
	State                 execution.PreparationState              `json:"-"`
}

// VerificationDeliveryPrerequisites exposes the canonical Factory merge/closure
// records for the accepted task graph. These facts do not attest application
// behavior or subsequent operator deployment. A prior merged commit can differ
// from the current integrated source inspected by the approved assertions.
type VerificationDeliveryPrerequisites struct {
	IntentID         string                      `json:"intent_id"`
	AcceptedRevision string                      `json:"accepted_revision"`
	ScopeDigest      string                      `json:"scope_digest"`
	SourceCommit     string                      `json:"source_commit"`
	Tasks            []VerificationDeliveredTask `json:"tasks"`
}

type VerificationDeliveredTask struct {
	TaskKey      string          `json:"task_key"`
	OutcomeIDs   []string        `json:"outcome_ids"`
	IssueID      int64           `json:"issue_id,string"`
	IssueIndex   int64           `json:"issue_index,string"`
	AcceptanceID string          `json:"acceptance_id"`
	Completion   CompletionEvent `json:"completion"`
}

func (p VerificationDeliveryPrerequisites) ValidateFor(d DeliverySubject, outcomes []string) error {
	if p.IntentID != d.IntentID || p.AcceptedRevision != d.AcceptedRevision || p.ScopeDigest != d.ScopeDigest || p.SourceCommit != d.SourceCommit || len(p.Tasks) < 1 || len(p.Tasks) > 64 {
		return errors.New("delivery prerequisites differ from admitted scope")
	}
	allowed := make(map[string]bool, len(outcomes))
	for _, outcome := range outcomes {
		allowed[outcome] = true
	}
	tasks, issues := map[string]bool{}, map[int64]bool{}
	var repository int64
	for _, task := range p.Tasks {
		if !project.ValidDecisionID(task.TaskKey) || tasks[task.TaskKey] || task.IssueID <= 0 || task.IssueIndex <= 0 || issues[task.IssueIndex] || !project.ValidDecisionID(task.AcceptanceID) || task.Completion.Validate() != nil || task.Completion.Issue != task.IssueIndex || task.Completion.AcceptanceID != task.AcceptanceID || len(task.OutcomeIDs) < 1 || len(task.OutcomeIDs) > len(outcomes) {
			return errors.New("delivery prerequisite lacks its exact completed task")
		}
		if repository != 0 && repository != task.Completion.Repository {
			return errors.New("delivery prerequisites cross repositories")
		}
		repository = task.Completion.Repository
		tasks[task.TaskKey], issues[task.IssueIndex] = true, true
		selected := map[string]bool{}
		for _, outcome := range task.OutcomeIDs {
			if !allowed[outcome] || selected[outcome] {
				return errors.New("delivery prerequisite names a foreign outcome")
			}
			selected[outcome] = true
		}
	}
	return nil
}

// ApprovedVerificationContext projects the native reader's bounded UTF-8 source
// as text so Test Pilot can inspect actual assertions. Repository text remains
// untrusted input, and a passing receipt does not itself prove outcome coverage.
type ApprovedVerificationContext struct {
	Project      string                       `json:"project"`
	ID           string                       `json:"id"`
	Role         string                       `json:"role"`
	SourceCommit string                       `json:"source_commit"`
	ApprovedBase string                       `json:"approved_base"`
	DiffBase     string                       `json:"diff_base"`
	Candidate    string                       `json:"candidate"`
	Setup        string                       `json:"setup"`
	Check        string                       `json:"check"`
	Files        []ApprovedVerificationSource `json:"files"`
	Diff         string                       `json:"diff"`
}

type ApprovedVerificationSource struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func NewApprovedVerificationContext(c project.FactoryPreparationContext) *ApprovedVerificationContext {
	files := make([]ApprovedVerificationSource, len(c.Files))
	for i, file := range c.Files {
		files[i] = ApprovedVerificationSource{Path: file.Path, Content: string(file.Content)}
	}
	return &ApprovedVerificationContext{Project: c.Project, ID: c.ID, Role: c.Role, SourceCommit: c.SourceCommit, ApprovedBase: c.ApprovedBase, DiffBase: c.DiffBase, Candidate: c.Candidate, Setup: string(c.Setup), Check: string(c.Check), Files: files, Diff: string(c.Diff)}
}

func (c ApprovedVerificationContext) nativeContext() project.FactoryPreparationContext {
	files := make([]project.FactoryContextFile, len(c.Files))
	for i, file := range c.Files {
		files[i] = project.FactoryContextFile{Path: file.Path, Content: []byte(file.Content)}
	}
	return project.FactoryPreparationContext{Project: c.Project, ID: c.ID, Role: c.Role, SourceCommit: c.SourceCommit, ApprovedBase: c.ApprovedBase, DiffBase: c.DiffBase, Candidate: c.Candidate, Setup: []byte(c.Setup), Check: []byte(c.Check), Files: files, Diff: []byte(c.Diff)}
}

func (e CurrentVerificationEvidence) ValidateFor(d DeliverySubject) error {
	if e.Manifest.Validate() != nil || e.Manifest.Digest() != d.VerificationManifestDigest || e.ManifestDigest != d.VerificationManifestDigest || e.PreparationDigest != d.PreparationDigest || e.SourceCommit != d.SourceCommit || e.Manifest.IntentID != d.IntentID || e.Manifest.AcceptedRevision != d.AcceptedRevision || e.Receipt.CheckID != ApprovedAppCheckID || e.Receipt.Outcome != CheckReceiptPassed || e.Receipt.Validate() != nil {
		return errors.New("current native verification differs from admitted delivery")
	}
	if e.ApprovedContext == nil || e.ApprovedContext.nativeContext().ValidateSelectedContent(project.RoleReviewer) != nil || e.ApprovedContext.SourceCommit != d.SourceCommit || e.ApprovedContext.ApprovedBase != d.SourceCommit || e.ApprovedContext.DiffBase != d.SourceCommit || e.ApprovedContext.Candidate != d.SourceCommit {
		return errors.New("approved verification assertions differ from admitted source")
	}
	if e.AdoptedManifest != nil {
		m := e.AdoptedManifest
		if !e.ExecutionRequired || m.Intent != d.IntentID || m.AcceptedRevision != d.AcceptedRevision || m.ScopeDigest != d.ScopeDigest || m.Catalog.Validate() != nil || m.Catalog.CheckDigest != MCPInputDigest([]byte(e.ApprovedContext.Check)) || m.Proposal.ValidateOutcomes(IntentRevision{EffectiveOutcomes: e.Manifest.Outcomes}) != nil || ValidateManifestProposal(m.Proposal, m.Catalog) != nil {
			return errors.New("approved assertions differ from adopted verification definitions")
		}
		if e.Execution != nil {
			s := e.Execution
			r := project.FactoryVerificationChecks{Project: e.ApprovedContext.Project, Run: s.Run, Incarnation: s.Incarnation, ManifestDigest: s.ManifestDigest}
			if s.ValidateFor(r) != nil || s.Preparation != e.ApprovedContext.ID || s.SourceCommit != d.SourceCommit || s.CheckDigest != m.Catalog.CheckDigest {
				return errors.New("native verification execution differs from its admitted check")
			}
		}
		if e.ExecutionReceipt != nil && (e.Execution == nil || e.Execution.Phase != "passed" || e.ExecutionReceipt.CheckID != ApprovedAppCheckID || e.ExecutionReceipt.Outcome != CheckReceiptPassed || e.ExecutionReceipt.ArtifactDigest != e.Execution.OutputDigest || e.ExecutionReceipt.Validate() != nil) {
			return errors.New("verification execution receipt lacks a native passing check")
		}
	} else {
		manifest, err := ParseApprovedAppCheck([]byte(e.ApprovedContext.Check))
		if err != nil || manifest.Digest() != d.VerificationManifestDigest || e.ExecutionRequired || e.Execution != nil || e.ExecutionReceipt != nil {
			return errors.New("approved verification assertions differ from admitted manifest")
		}
	}
	if e.DeliveryPrerequisites == nil || e.DeliveryPrerequisites.ValidateFor(d, e.Manifest.Outcomes) != nil {
		return errors.New("current verification lacks exact delivery prerequisites")
	}
	return nil
}
