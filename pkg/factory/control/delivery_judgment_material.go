package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

type deliveryMaterialSource interface {
	ReadDeliveryMaterial(context.Context, factory.MergeWork) (factory.DeliveryNativeMaterial, error)
}

// DeliveryMergeObservation finishes one material read's merge proof under its
// original context. It is request-owned and supplies no send authority.
type DeliveryMergeObservation func(factory.MergeWork) (factory.MergeObservation, error)

type deliveryMergeMaterialSource interface {
	ReadDeliveryMaterialForMerge(context.Context, factory.Publication, factory.MergeWork) (factory.DeliveryNativeMaterial, DeliveryMergeObservation, error)
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
	basis        deliveryReviewEvidence
	Input        factory.DeliveryFingerprintInput `json:"identity"`
	Accepted     DispatchInputs                   `json:"accepted"`
	PlanOrigin   string                           `json:"plan_origin,omitempty"`
	Plan         factory.InvestigationPlan        `json:"admitted_plan"`
	Findings     []factory.Finding                `json:"canonical_findings"`
	Review       ResolvedReviewVerdict            `json:"current_review"`
	ReviewSource factory.CurrentReviewHead        `json:"review_source"`
	Corrections  []factory.CorrectionJournal      `json:"corrections"`
	Native       factory.DeliveryNativeMaterial   `json:"native"`
	Source       deliverySourceMaterial           `json:"source"`
	Checks       factory.CheckAssessment          `json:"checks"`
	Final        *execution.CheckpointReceipt     `json:"native_final"`
}

// A correction answers an original inspection; it never moves that review's
// receipt to the corrected head. The paid decision binds both pieces instead.
type deliveryReviewEvidence struct {
	source     factory.Publication
	cycle      factory.ReviewCycle
	reviewer   factory.Assignment
	operation  factory.ReviewOperation
	review     ResolvedReviewVerdict
	correction *factory.CorrectionJournal
	corrector  factory.Assignment
}

func (c *Coordinator) deliveryReviewEvidence(ctx context.Context, p factory.Publication) (deliveryReviewEvidence, error) {
	var empty deliveryReviewEvidence
	if p.Validate() != nil || !p.HasPullTarget() {
		return empty, ErrDeliveryUnavailable
	}
	if op, found := currentReviewOperation(p); found {
		if !currentReviewOpReady(p, op) {
			return empty, ErrDeliveryUnavailable
		}
		a, err := c.Store.AssignmentByRun(ctx, op.RunID)
		if err != nil || !reviewAssignmentMatches(a, p) {
			return empty, ErrDeliveryUnavailable
		}
		review, ready, pending, refused := c.resolveCurrentReviewReport(ctx, p, op, a)
		if !ready || pending || refused || review.Review == nil {
			return empty, ErrDeliveryUnavailable
		}
		cycle, found := findReviewCycle(p, review.Review.CycleID)
		if !found {
			return empty, ErrDeliveryUnavailable
		}
		return deliveryReviewEvidence{source: p, cycle: cycle, reviewer: a, operation: op, review: review}, nil
	}
	if len(p.Corrections) == 0 {
		return empty, ErrDeliveryUnavailable
	}
	op := p.Corrections[len(p.Corrections)-1]
	if op.Work == nil || op.Effect != factory.OpEffectCommitted || op.Completion != factory.OpCompletionComplete || op.Work.Candidate != p.Candidate || op.Work.ComparisonOID != p.PullTarget().BaseOID {
		return empty, ErrDeliveryUnavailable
	}
	corrector, err := c.Store.AssignmentByRun(ctx, op.RunID)
	if err != nil || corrector.Validate() != nil || !corrector.SucceededReported() || corrector.Role != project.RoleCoder || corrector.Phase != project.ExecutionCorrect || corrector.PublicationAssignment != p.AssignmentID || corrector.SourceCommit != op.Work.ExpectedOld || corrector.Result.Candidate != p.Candidate || corrector.Repository != p.Repository || corrector.ProjectID != p.ProjectID || corrector.Issue != p.Issue || corrector.Acceptance != p.Acceptance {
		return empty, ErrDeliveryUnavailable
	}
	owner, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil || owner.AttemptRoot != corrector.AttemptRoot || owner.ActorID != corrector.ActorID || c.checkPriorRunJoins(ctx, p, corrector) != nil {
		return empty, ErrDeliveryUnavailable
	}
	var journal *factory.CorrectionJournal
	for i := len(p.CorrectionJournals) - 1; i >= 0; i-- {
		if p.CorrectionJournals[i].RunID == corrector.Run {
			value := p.CorrectionJournals[i]
			journal = &value
			break
		}
	}
	if journal == nil || journal.Validate() != nil || journal.Report.NewCandidate != p.Candidate || corrector.Result.PhaseResult == nil || corrector.Result.PhaseResult.Output == nil || !reflect.DeepEqual(corrector.Result.PhaseResult.Output.Correction, &journal.Report) {
		return empty, ErrDeliveryUnavailable
	}
	cycle, found := findReviewCycle(p, journal.CycleID)
	if !found || cycle.CorrectionAssignment != corrector.ID || cycle.Head != corrector.SourceCommit || cycle.BaseOID != p.PullTarget().BaseOID || cycle.AttemptRoot != corrector.AttemptRoot {
		return empty, ErrDeliveryUnavailable
	}
	for _, later := range p.ReviewCycles {
		// A separately admitted inspection retains its original recovery path.
		if later.Round > cycle.Round && later.Head == p.Candidate && later.CorrectionAssignment == "" && later.TriggerDecision != "funded-review-again" {
			return empty, ErrDeliveryUnavailable
		}
	}
	run, err := c.Store.FactoryRun(ctx, corrector.Run)
	if err != nil || !run.Reconciled || run.Outcome != factory.Succeeded || run.Native != nil && !run.Native.Settled() || len(run.ExecutionManifest) > 0 && (run.FinalCheckpoint == nil || run.FinalCheckpoint.Status != "committed" || run.FinalCheckpoint.Candidate != p.Candidate) {
		return empty, ErrDeliveryUnavailable
	}
	carried, err := c.reviewCarriedFindings(ctx, p, cycle)
	if err != nil {
		return empty, err
	}
	checks, ok := c.currentChildCheckAssessment(ctx, p)
	if !ok || checks == nil || checks.Verdict != factory.CheckPass {
		return empty, ErrDeliveryUnavailable
	}
	if _, err = AdmitCorrectionReportCanonical(cycle, corrector.Run, p.Candidate, carried, journal.Report, journal.Report.Evidence, checks); err != nil {
		return empty, err
	}
	for _, disposition := range journal.Report.Dispositions {
		if disposition.Disposition != factory.DispositionFixed && disposition.Disposition != factory.DispositionDeclinedOptional {
			return empty, ErrDeliveryUnavailable
		}
	}
	reviewer, err := c.Store.Assignment(ctx, corrector.ParentAssignment)
	if err != nil || !validCorrectReviewer(corrector, reviewer) || reviewer.SourceCommit != corrector.SourceCommit || reviewer.ActorID != corrector.ActorID {
		return empty, ErrDeliveryUnavailable
	}
	history, err := c.Store.PublicationReviewHistory(ctx, p)
	if err != nil {
		return empty, err
	}
	for i := len(history) - 1; i >= 0; i-- {
		source := history[i]
		if source.AssignmentID != reviewer.PublicationAssignment {
			continue
		}
		for j := len(source.ReviewOperations) - 1; j >= 0; j-- {
			reviewOp := source.ReviewOperations[j]
			if reviewOp.Work.HeadOID != reviewer.SourceCommit {
				continue
			}
			if reviewOp.RunID != reviewer.Run || reviewOp.Work.BaseOID != cycle.BaseOID || reviewOp.Outcome.Effect != factory.OpEffectCommitted || reviewOp.Outcome.Completion != factory.OpCompletionComplete || reviewOp.Work.Disposition() != "REQUEST_CHANGES" {
				return empty, ErrDeliveryUnavailable
			}
			review, err := resolveAssignmentVerdict(reviewer)
			if err != nil || review.Review == nil {
				return empty, ErrDeliveryUnavailable
			}
			original, ok := findReviewCycle(source, review.Review.CycleID)
			if !ok || original.ReviewerAssignment != reviewer.ID || c.checkPriorReviewBindings(ctx, source, original, reviewer) != nil {
				return empty, ErrDeliveryUnavailable
			}
			prior, err := c.reviewCarriedFindings(ctx, source, original)
			if err != nil || review.Review.Validate(prior) != nil {
				return empty, ErrDeliveryUnavailable
			}
			return deliveryReviewEvidence{source: source, cycle: cycle, reviewer: reviewer, operation: reviewOp, review: review, correction: journal, corrector: corrector}, nil
		}
		return empty, ErrDeliveryUnavailable
	}
	return empty, ErrDeliveryUnavailable
}

func deliveryJudgmentInput(state any, model string) (factory.DeliveryFingerprintInput, error) {
	material, ok := state.(*deliveryJudgmentMaterial)
	if !ok || material == nil || material.Input.Model != model {
		return factory.DeliveryFingerprintInput{}, errors.New("delivery judgment requires complete bound semantic material")
	}
	return material.Input, material.validate()
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
	origin := m.Input.AttemptRoot
	if m.PlanOrigin != "" {
		origin = m.PlanOrigin
	}
	if m.Input.Validate() != nil || m.Plan.Validate() != nil || m.Input.ScopeDigest != m.Plan.ScopeDigest ||
		origin != m.Plan.AttemptRoot || m.Native.Revision < 1 || m.Native.Head != m.Input.Head || m.Native.Base != m.Input.Base ||
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

func (c *Coordinator) deliveryMaterial(ctx context.Context, p factory.Publication, review ResolvedReviewVerdict, model string, observe *DeliveryMergeObservation, admitted *AcceptanceValidity) (*deliveryJudgmentMaterial, factory.ReviewCycle, factory.RepositoryPolicy, error) {
	var emptyCycle factory.ReviewCycle
	var emptyPolicy factory.RepositoryPolicy
	source, ok := c.Merges.(deliveryMaterialSource)
	if !ok || c.Host == nil || review.Review == nil || !p.HasPullTarget() {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	basis, err := c.deliveryReviewEvidence(ctx, p)
	if err != nil || !reflect.DeepEqual(basis.review, review) {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	cycle := basis.cycle
	owner, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	merge := factory.Merge{Authority: p.Authority, ID: p.ID, PublicationID: p.ID, ProjectID: p.ProjectID, AssignmentID: p.AssignmentID, Acceptance: p.Acceptance, Repository: p.Repository, Issue: p.Issue, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PullTarget().IssueID, PRAuthorID: p.PullTarget().AuthorID, ReviewerID: policy.Review.ActorID, HeadRef: p.PullTarget().HeadRef, BaseRef: p.PullTarget().BaseRef, HeadOID: p.Candidate, BaseOID: p.PullTarget().BaseOID}
	if owner.RetainedPublication != nil {
		merge, err = c.Store.MergeByPublication(ctx, p.ID)
		if err != nil || merge.AssignmentID != owner.ID || merge.HeadOID != p.Candidate || merge.BaseOID != p.PullTarget().BaseOID {
			return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
		}
	}
	gate := MergeReport{}
	var accepted AcceptanceValidity
	valid := false
	if admitted != nil {
		accepted, valid = *admitted, admitted.Valid
	} else {
		policy, accepted, valid = c.mergeAuthority(ctx, merge, &gate)
	}
	if !valid || accepted.Acceptance == nil || accepted.Acceptance.ID != p.Acceptance {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	revision := accepted.Revision
	checks, ok := c.currentChildCheckAssessment(ctx, p)
	if !ok || checks == nil || checks.Verdict != factory.CheckPass {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	reviewer := basis.reviewer
	reviewerRun, err := c.Store.FactoryRun(ctx, reviewer.Run)
	if err != nil || !reviewerRun.Reconciled || reviewerRun.Outcome != factory.Succeeded {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	deadline, err := c.Store.CapAttemptDeadline(ctx, owner.ID, time.Now().Add(30*time.Second).Unix())
	if err != nil || deadline <= time.Now().Unix() {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	attempt := &attemptPlan{acceptance: *accepted.Acceptance, policy: policy, projectID: p.ProjectID, deadline: time.Unix(deadline, 0)}
	if wait, failed := readAttemptInputs(ctx, c.dispatchDeps(), attempt, p.Repository, p.Issue); wait != nil || failed != nil || attempt.inputs.Revision != revision {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	origin, err := c.Store.PublicationExecutionOwner(ctx, p, owner)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	head, _, err := c.Store.PlanHead(ctx, origin.AttemptRoot)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	plan, err := c.Store.InvestigationPlan(ctx, origin.AttemptRoot, head)
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
	if basis.correction == nil {
		if err := c.processPriorReview(ctx, p, collector, cycle, reviewer); err != nil {
			return nil, emptyCycle, emptyPolicy, err
		}
	}
	carried = collector.snapshot()
	preparationID, contextRole := owner.Preparation, project.RoleCoder
	if p.External != nil {
		preparationID, contextRole = reviewer.Preparation, project.RoleReviewer
	}
	if basis.correction != nil {
		preparationID, contextRole = basis.corrector.Preparation, project.RoleCoder
	}
	preparation, err := c.Store.Preparation(ctx, preparationID)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	var sourceRun *project.FactoryExport
	if preparation.Native != nil && contextRole == project.RoleCoder && p.Candidate != preparation.Preparation.SourceCommit && p.Run != "" {
		sourceRun = &project.FactoryExport{Project: p.ProjectID, ID: p.Run, Role: project.RoleCoder, Preparation: preparationID, Candidate: p.Candidate}
	}
	repository, err := readRepositoryContext(ctx, c.Host, attempt, preparation, contextRole, owner.SourceCommit, p.CurrentDiffBase(), p.Candidate, p.External != nil && basis.correction == nil, sourceRun)
	if err != nil {
		return nil, emptyCycle, emptyPolicy, err
	}
	var native factory.DeliveryNativeMaterial
	if paired, ok := source.(deliveryMergeMaterialSource); observe != nil && ok {
		work := c.mergeWork(merge, policy)
		work.NativeRev = revision
		work.ReviewEvent = basis.operation.Work.Event
		native, *observe, err = paired.ReadDeliveryMaterialForMerge(ctx, p, work)
	} else {
		native, err = source.ReadDeliveryMaterial(ctx, c.mergeWork(merge, policy))
	}
	if err != nil || native.Revision != revision || native.PolicyRevision != 0 && native.PolicyRevision != policy.Revision {
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
		var current *factory.DeliveryNativeReview
		for i := range native.Reviews {
			if native.Reviews[i].ID == blocked {
				current = &native.Reviews[i]
				break
			}
		}
		// A human can share the configured review account. Only an original
		// Factory receipt and unchanged observed body identify an agent review.
		operations := append(append([]factory.ReviewOperation(nil), p.ReviewOperations...), basis.operation)
		for _, operation := range operations {
			if current == nil || current.ActorID != strconv.FormatInt(policy.Review.ActorID, 10) || c.Reviews == nil {
				break
			}
			if operation.Work.ActorID != policy.Review.ActorID || operation.Work.Repository != p.Repository || operation.Work.PRID != p.PRID || operation.Outcome.Effect != factory.OpEffectCommitted || operation.Outcome.Completion != factory.OpCompletionComplete {
				continue
			}
			if operation.Work.Event != "REQUEST_CHANGES" || current.Content != operation.Work.Body || current.Commit != operation.Work.HeadOID {
				continue
			}
			adopted, err := c.Reviews.AdoptReview(ctx, operation.Work, operation.Outcome)
			if err == nil && adopted.ReviewerID == policy.Review.ActorID && adopted.ReviewID > 0 && strconv.FormatInt(adopted.ReviewID, 10) == blocked {
				found = true
			}
		}
		if !found {
			return nil, emptyCycle, emptyPolicy, &factory.PublicationWait{Reason: "blocking_human_review"}
		}
	}
	material := &deliveryJudgmentMaterial{
		basis:    basis,
		Accepted: attempt.inputs, Plan: plan, Findings: carried, Review: review, Corrections: append([]factory.CorrectionJournal(nil), p.CorrectionJournals...), Native: native, Checks: *checks,
		ReviewSource: factory.CurrentReviewHead{CycleID: review.Review.CycleID, RunID: reviewer.Run, OperationID: basis.operation.Work.OperationID, HeadOID: basis.operation.Work.HeadOID, BaseOID: basis.operation.Work.BaseOID, Event: basis.operation.Work.Event, Operation: factory.CurrentReviewOperation{Effect: basis.operation.Outcome.Effect, Completion: basis.operation.Outcome.Completion, Cancellation: basis.operation.Outcome.Cancellation}},
		Source:       deliverySourceMaterial{Source: repository.SourceCommit, Base: repository.DiffBase, Head: repository.Candidate, Diff: string(repository.Diff), Setup: string(repository.Setup), Check: string(repository.Check)},
	}
	for _, file := range repository.Files {
		material.Source.Files = append(material.Source.Files, deliveryTextFile{Path: file.Path, Content: string(file.Content)})
	}
	if p.Run != "" {
		run, err := c.Store.FactoryRun(ctx, p.Run)
		if err != nil || !run.Reconciled || run.Outcome != factory.Succeeded {
			return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
		}
		if len(run.ExecutionManifest) != 0 && (run.FinalCheckpoint == nil || run.FinalCheckpoint.Status != "committed" || run.FinalCheckpoint.Candidate != p.Candidate) {
			return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
		}
		material.Final = run.FinalCheckpoint
	} else if p.External == nil {
		return nil, emptyCycle, emptyPolicy, ErrDeliveryUnavailable
	}
	if origin.AttemptRoot != owner.AttemptRoot {
		material.PlanOrigin = origin.AttemptRoot
	}
	// Poll time/revision fields are mutation guards, not semantic changes.
	semanticChecks := material.Checks
	semanticChecks.NativeRev, semanticChecks.Revision, semanticChecks.AssessedUnix = 0, 0, 0
	material.Input = factory.DeliveryFingerprintInput{
		AttemptRoot: owner.AttemptRoot, PublicationID: p.ID, Round: cycle.Round, Head: p.Candidate, Base: p.PullTarget().BaseOID, ScopeDigest: plan.ScopeDigest, Reviews: []string{reviewer.ID}, PolicyRevision: policy.Revision, Model: model, Rubric: factory.DeliveryRubric,
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
