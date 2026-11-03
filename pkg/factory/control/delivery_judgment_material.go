package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

type deliveryMaterialSource interface {
	ReadDeliveryMaterial(context.Context, factory.MergeWork) (factory.DeliveryNativeMaterial, error)
}

type deliveryTextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type deliverySourceMaterial struct {
	Source string             `json:"source"`
	Base   string             `json:"base"`
	Head   string             `json:"head"`
	Diff   string             `json:"diff"`
	Setup  string             `json:"setup"`
	Check  string             `json:"check"`
	Files  []deliveryTextFile `json:"files"`
}

// Complete transient judgment material. The persisted fingerprint/receipt
// binds all of these bytes; the model receives their content, not hashes.
type deliveryJudgmentMaterial struct {
	Input       factory.DeliveryFingerprintInput `json:"identity"`
	Accepted    DispatchInputs                   `json:"accepted"`
	Plan        factory.InvestigationPlan        `json:"admitted_plan"`
	Findings    []factory.Finding                `json:"canonical_findings"`
	Review      ResolvedReviewVerdict            `json:"current_review"`
	Corrections []factory.CorrectionJournal      `json:"corrections"`
	Native      factory.DeliveryNativeMaterial   `json:"native"`
	Source      deliverySourceMaterial           `json:"source"`
	Checks      factory.CheckAssessment          `json:"checks"`
	Final       *execution.CheckpointReceipt     `json:"native_final"`
}

func deliveryContentDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m *deliveryJudgmentMaterial) semanticDigest() string {
	copy := *m
	copy.Input = factory.DeliveryFingerprintInput{}
	copy.Accepted.Revision = 0
	copy.Native.Revision = 0
	copy.Checks.NativeRev, copy.Checks.Revision, copy.Checks.AssessedUnix = 0, 0, 0
	return deliveryContentDigest(copy)
}

func (m *deliveryJudgmentMaterial) validate() error {
	if m.Input.Validate() != nil || m.Plan.Validate() != nil || m.Input.ScopeDigest != m.Plan.ScopeDigest ||
		m.Input.AttemptRoot != m.Plan.AttemptRoot || m.Native.Revision < 1 || m.Native.Head != m.Input.Head || m.Native.Base != m.Input.Base ||
		m.Source.Head != m.Input.Head || m.Source.Diff == "" || m.Source.Setup == "" || m.Source.Check == "" ||
		m.Checks.Validate() != nil || m.Checks.Verdict != factory.CheckPass || m.Checks.HeadOID != m.Input.Head || m.Checks.BaseOID != m.Input.Base ||
		len(m.Input.Constraints) != 1 || m.Input.Constraints[0] != "complete-material-sha256:"+m.semanticDigest() {
		return errors.New("delivery semantic material is incomplete or differs from its fingerprint")
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > 256*1024 {
		return errors.New("complete delivery material exceeds its bound")
	}
	return nil
}

func (c *Coordinator) deliveryMaterial(ctx context.Context, p factory.Publication, review ResolvedReviewVerdict, model string) (*deliveryJudgmentMaterial, factory.ReviewCycle, factory.RepositoryPolicy, error) {
	var emptyCycle factory.ReviewCycle
	var emptyPolicy factory.RepositoryPolicy
	source, ok := c.Merges.(deliveryMaterialSource)
	if !ok || c.Host == nil || review.Review == nil || p.PRCreate.Work == nil {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	cycle, ok := findReviewCycle(p, review.Review.CycleID)
	if !ok || cycle.Head != p.Candidate || cycle.BaseOID != p.PRCreate.BaseOID {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	owner, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	merge := factory.Merge{Authority: p.Authority, ID: p.ID, ProjectID: p.ProjectID, AssignmentID: p.AssignmentID, Acceptance: p.Acceptance, Repository: p.Repository, Issue: p.Issue, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PRCreate.IssueID, PRAuthorID: p.PRCreate.Work.ActorID, ReviewerID: policy.Review.ActorID, HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef, HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID}
	gate := MergeReport{}
	policy, revision, valid := c.mergeAuthority(ctx, merge, &gate)
	if !valid {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	checks, ok := c.currentChildCheckAssessment(ctx, p)
	if !ok || checks == nil || checks.Verdict != factory.CheckPass {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	reviewer, err := c.Store.Assignment(ctx, cycle.ReviewerAssignment)
	if err != nil || reviewer.Stage != factory.AssignmentFinished || reviewer.Outcome != factory.Succeeded {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	reviewerRun, err := c.Store.FactoryRun(ctx, reviewer.Run)
	if err != nil || !reviewerRun.Reconciled || reviewerRun.Outcome != factory.Succeeded {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	status, err := c.AcceptanceStatus(ctx, p.Repository, strconv.FormatInt(p.Issue, 10))
	if err != nil || status.Acceptance == nil || !status.Valid || status.Acceptance.ID != p.Acceptance || status.Revision != revision {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	deadline, err := c.Store.CapAttemptDeadline(ctx, owner.ID, time.Now().Add(30*time.Second).Unix())
	if err != nil || deadline <= time.Now().Unix() {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	attempt := &attemptPlan{acceptance: *status.Acceptance, policy: policy, projectID: p.ProjectID, deadline: time.Unix(deadline, 0)}
	if wait, failed := readAttemptInputs(ctx, c.dispatchDeps(), attempt, p.Repository, p.Issue); wait != nil || failed != nil || attempt.inputs.Revision != revision {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	head, _, err := c.Store.PlanHead(ctx, owner.AttemptRoot)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	plan, err := c.Store.InvestigationPlan(ctx, owner.AttemptRoot, head)
	if err != nil || plan.Validate() != nil {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	carried, err := c.reviewCarriedFindings(ctx, p, cycle)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	collector := &carriedCollector{seen: map[string]bool{}, content: map[string]factory.Finding{}}
	for _, finding := range carried {
		if err := collector.add(finding); err != nil {
			return nil, emptyCycle, emptyPolicy, err
		}
	}
	if err := c.processPriorReview(ctx, p, collector, cycle, reviewer); err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	carried = collector.snapshot()
	preparation, err := c.Store.Preparation(ctx, owner.Preparation)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	repository, err := readRepositoryContext(ctx, c.Host, attempt, preparation, project.RoleCoder, owner.SourceCommit, p.CurrentDiffBase(), p.Candidate, false)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	native, err := source.ReadDeliveryMaterial(ctx, c.mergeWork(merge, policy))
	if err != nil || native.Revision != revision {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	if err := normalizeExpectedResolutions(p, &native); err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	// Native external blockers cannot be repaired by another semantic call.
	// Requests by this Factory's independent reviewer can proceed to the
	// bounded route decision; blocking human reviews always wait.
	if !native.Eligible && native.Blocker != "requested_changes" && native.Blocker != "approvals" {
		return nil, emptyCycle, emptyPolicy, &factory.PublicationWait{Reason: native.Blocker}
	}
	for _, blocked := range native.BlockingReviewIDs {
		found := false
		for _, item := range native.Reviews {
			if item.ID == blocked && item.ActorID == strconv.FormatInt(policy.Review.ActorID, 10) {
				found = true
			}
		}
		if !found {
			return nil, emptyCycle, emptyPolicy, &factory.PublicationWait{Reason: "blocking_human_review"}
		}
	}
	material := &deliveryJudgmentMaterial{
		Accepted: attempt.inputs, Plan: plan, Findings: carried, Review: review, Corrections: append([]factory.CorrectionJournal(nil), p.CorrectionJournals...), Native: native, Checks: *checks,
		Source: deliverySourceMaterial{Source: repository.SourceCommit, Base: repository.DiffBase, Head: repository.Candidate, Diff: string(repository.Diff), Setup: string(repository.Setup), Check: string(repository.Check)},
	}
	for _, file := range repository.Files {
		material.Source.Files = append(material.Source.Files, deliveryTextFile{Path: file.Path, Content: string(file.Content)})
	}
	run, err := c.Store.FactoryRun(ctx, p.Run)
	if err != nil || !run.Reconciled || run.Outcome != factory.Succeeded {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	if len(run.ExecutionManifest) != 0 && (run.FinalCheckpoint == nil || run.FinalCheckpoint.Status != "committed" || run.FinalCheckpoint.Candidate != p.Candidate) {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	material.Final = run.FinalCheckpoint
	// Poll time/revision fields are mutation guards, not semantic changes.
	semanticChecks := material.Checks
	semanticChecks.NativeRev, semanticChecks.Revision, semanticChecks.AssessedUnix = 0, 0, 0
	material.Input = factory.DeliveryFingerprintInput{
		AttemptRoot: owner.AttemptRoot, PublicationID: p.ID, Round: cycle.Round, Head: p.Candidate, Base: p.PRCreate.BaseOID, ScopeDigest: plan.ScopeDigest, Reviews: []string{cycle.ReviewerAssignment}, PolicyRevision: policy.Revision, Model: model, Rubric: factory.DeliveryRubric,
		FixDigest: deliveryContentDigest(material.Corrections), DiffDigest: deliveryContentDigest(material.Source.Diff), CheckDigest: deliveryContentDigest(semanticChecks), TreeDigest: deliveryContentDigest(struct {
			Head  string
			Final *execution.CheckpointReceipt
		}{p.Candidate, material.Final}),
	}
	semanticNative := native
	semanticNative.Revision = 0
	material.Input.Conversations = []string{deliveryContentDigest(semanticNative)}
	material.Input.Constraints = []string{"complete-material-sha256:" + material.semanticDigest()}
	return material, cycle, policy, nil
}
