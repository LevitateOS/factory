package control

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

type deliveryProductionChooser interface {
	DeliveryChooser
	DeliveryModel() string
}

func normalizeExpectedResolutions(p factory.Publication, native *factory.DeliveryNativeMaterial) error {
	for _, operation := range p.ResolutionOperations {
		if operation.Work.Request.HeadOID != native.Head || operation.Work.Request.BaseOID != native.Base || operation.Outcome.Effect != factory.OpEffectCommitted {
			continue
		}
		for index := range native.Threads {
			thread := &native.Threads[index]
			if thread.RootID != strconv.FormatInt(operation.Work.Request.RootCommentID, 10) {
				continue
			}
			if !thread.Resolved || thread.ResolverID != strconv.FormatInt(operation.Work.Request.ActorID, 10) || thread.Digest != operation.ResolvedDigest {
				return errors.New("accepted conversation changed after native resolution")
			}
			// This exact native consequence was named before the write.
			// Undo only that root's resolver mark for fingerprint reuse;
			// any reply/edit/unresolve has already changed the digest.
			thread.Digest, thread.Resolved, thread.ResolverID = operation.Work.Request.Digest, false, ""
			if len(thread.Members) == 0 || thread.Members[0].ID != thread.RootID {
				return errors.New("resolved conversation root is missing")
			}
			thread.Members[0].Resolved = false
		}
	}
	return nil
}

func resolvedConversationDigest(thread factory.DeliveryThread, comments []factory.DeliveryComment) (string, error) {
	prior, err := thread.ConversationDigest(comments, false)
	if err != nil || prior != thread.Digest || thread.Resolved {
		return "", ErrDeliveryUnavailable
	}
	return thread.ConversationDigest(comments, true)
}

// plannedDeliveryResolutions uses only mechanically admitted fixed findings
// and mappings adopted from exact committed review receipts. A favorable
// judgment never nominates a human thread or invents a root mapping.
func (c *Coordinator) plannedDeliveryResolutions(ctx context.Context, p factory.Publication, cycle factory.ReviewCycle, material *deliveryJudgmentMaterial, policy factory.RepositoryPolicy) ([]factory.DeliveryResolutionOperation, error) {
	fixed := map[string]bool{}
	for _, journal := range p.CorrectionJournals {
		if journal.Report.NewCandidate != p.Candidate {
			continue
		}
		for _, disposition := range journal.Report.Dispositions {
			fixed[disposition.Key] = disposition.Disposition == factory.DispositionFixed
		}
	}
	roots := map[int64]string{}
	for _, operation := range p.ReviewOperations {
		if operation.Outcome.Effect != factory.OpEffectCommitted || operation.Outcome.Completion != factory.OpCompletionComplete {
			continue
		}
		if c.Reviews == nil {
			return nil, ErrDeliveryUnavailable
		}
		adopted, err := c.Reviews.AdoptReview(ctx, operation.Work, operation.Outcome)
		if err != nil {
			return nil, err
		}
		for _, mapping := range adopted.Comments {
			if fixed[mapping.Key] {
				roots[mapping.CommentID] = mapping.Key
			}
		}
	}
	var planned []factory.DeliveryResolutionOperation
	for _, thread := range material.Native.Threads {
		root, _ := strconv.ParseInt(thread.RootID, 10, 64)
		key := roots[root]
		if key == "" || thread.Resolved {
			continue
		}
		binding, err := policy.ResolveBinding()
		if err != nil || c.Resolutions == nil || binding.ActorID != policy.Review.ActorID {
			return nil, &factory.PublicationWait{Reason: "resolve_binding_unavailable"}
		}
		resolved, err := resolvedConversationDigest(thread, material.Native.Comments)
		if err != nil {
			return nil, err
		}
		id := AcceptedResolveOperationID(p.ID, cycle.ID, key)
		for _, prior := range planned {
			if prior.Work.OperationID == id {
				return nil, errors.New("finding has more than one native resolution root")
			}
		}
		request := factory.ResolveRequest{Repository: p.Repository, PRNumber: p.PRNumber, PRID: p.PRID, RootCommentID: root, ActorID: binding.ActorID, HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef, HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID, Digest: thread.Digest, Resolved: true}
		planned = append(planned, factory.DeliveryResolutionOperation{FindingKey: key, ResolvedDigest: resolved, Work: factory.ResolveWork{Request: request, Binding: binding, PRAuthorID: p.PRCreate.Work.ActorID, OperationID: id}})
	}
	return planned, nil
}

func (c *Coordinator) advisePublicationDelivery(ctx context.Context, p factory.Publication, review ResolvedReviewVerdict) (factory.DeliveryDecision, string, error) {
	if c.Planning == nil {
		return factory.DeliveryDecision{}, "", ErrDeliveryUnavailable
	}
	chooser, ok := c.Planning.Chooser.(deliveryProductionChooser)
	if !ok {
		return factory.DeliveryDecision{}, "", ErrDeliveryUnavailable
	}
	material, cycle, policy, err := c.deliveryMaterial(ctx, p, review, chooser.DeliveryModel())
	if err != nil {
		return factory.DeliveryDecision{}, "", err
	}
	planned, err := c.plannedDeliveryResolutions(ctx, p, cycle, material, policy)
	if err != nil {
		return factory.DeliveryDecision{}, "", err
	}
	if err := material.validate(); err != nil {
		return factory.DeliveryDecision{}, "", err
	}
	fingerprint, _ := factory.DeliveryFingerprint(material.Input)
	requestID := deliveryContentDigest("soda-funded-delivery-v1:" + fingerprint)[:32]
	allowance, err := c.Store.AttemptAllowance(ctx, p.Repository, p.Issue)
	if err != nil || allowance.RootAssignment != cycle.AttemptRoot || allowance.Closed || !allowance.Active {
		return factory.DeliveryDecision{}, "", ErrDeliveryUnavailable
	}
	decision, _, err := c.AdviseDelivery(ctx, DeliveryAdvice{Input: material.Input, State: material, Repository: p.Repository, Issue: p.Issue, AttemptRoot: cycle.AttemptRoot, ReviewsFit: allowance.ReviewFits(p.Candidate, false, time.Now()), ObservedNativeRevision: material.Native.Revision, Chooser: chooser, Funding: c.Store})
	if err != nil || decision.Effective != factory.DeliveryEffectiveReady {
		return decision, requestID, err
	}
	var intents []string
	for _, operation := range planned {
		intents = append(intents, operation.Work.OperationID)
	}
	updated, acceptance, err := AdmitJevAcceptance(JevAdmissionInput{Publication: p, Cycle: cycle, Carried: material.Findings, FingerprintInput: material.Input, Fingerprint: fingerprint, Decision: decision, DecisionID: requestID, ScopeDigest: material.Plan.ScopeDigest, PolicyRevision: strconv.FormatInt(policy.Revision, 10), CheckDigest: factory.ChecksDigest(policy.Checks), DiffBase: p.CurrentDiffBase(), ResolutionIntents: intents})
	if err != nil {
		return factory.DeliveryDecision{}, requestID, err
	}
	if len(updated.DeliveryAcceptances) != len(p.DeliveryAcceptances) {
		updated.Revision++
		if err := c.Store.UpdatePublication(ctx, updated); err != nil {
			return factory.DeliveryDecision{}, requestID, err
		}
		// Re-read exact material on the next pass before any native effect.
		return decision, requestID, nil
	}
	for _, intended := range planned {
		found := false
		for index := range p.ResolutionOperations {
			operation := &p.ResolutionOperations[index]
			if operation.Work.OperationID != intended.Work.OperationID {
				continue
			}
			found = true
			if !operation.Confirmed {
				observed, lookupErr := c.Resolutions.LookupResolution(ctx, operation.Work.OperationID)
				if lookupErr == nil && observed.NotObserved && time.Now().Unix() < operation.Work.NotAfter {
					// The exact immutable native operation is idempotent. A
					// crash before initial submission can replay these same
					// bytes only after fresh authority/material still match.
					outcome, submitErr := c.Resolutions.SubmitResolution(ctx, operation.Work)
					if submitErr != nil || outcome.NotObserved {
						return factory.DeliveryDecision{}, requestID, ErrDeliveryUnavailable
					}
					operation.Outcome = outcome
					p.Revision++
					if err := c.Store.UpdatePublication(ctx, p); err != nil {
						return factory.DeliveryDecision{}, requestID, err
					}
					return decision, requestID, nil
				}
				if err := c.reconcileDeliveryResolution(ctx, &p, index, false); err != nil {
					return factory.DeliveryDecision{}, requestID, err
				}
				return decision, requestID, nil
			}
		}
		if found {
			continue
		}
		intended.AcceptanceID = acceptance.ID
		intended.Work.AuthRevision = "delivery:" + acceptance.ID
		intended.Work.NativeRevision = material.Native.Revision
		intended.Work.NotAfter, err = c.Store.CapAttemptDeadline(ctx, p.AssignmentID, time.Now().Add(30*time.Second).Unix())
		if err != nil {
			return factory.DeliveryDecision{}, requestID, err
		}
		p.ResolutionOperations = append(p.ResolutionOperations, intended)
		p.Revision++
		if err := c.Store.UpdatePublication(ctx, p); err != nil {
			return factory.DeliveryDecision{}, requestID, err
		}
		outcome, err := c.Resolutions.SubmitResolution(ctx, intended.Work)
		if err == nil && !outcome.NotObserved {
			p.ResolutionOperations[len(p.ResolutionOperations)-1].Outcome = outcome
			p.Revision++
			err = c.Store.UpdatePublication(ctx, p)
		}
		return decision, requestID, err
	}
	return decision, requestID, nil
}

func (c *Coordinator) reconcileDeliveryResolution(ctx context.Context, p *factory.Publication, index int, cancel bool) error {
	if c.Resolutions == nil {
		return ErrDeliveryUnavailable
	}
	operation := p.ResolutionOperations[index]
	outcome, err := c.Resolutions.LookupResolution(ctx, operation.Work.OperationID)
	if cancel && (err != nil || outcome.NotObserved || outcome.Effect != factory.OpEffectCommitted && outcome.Effect != factory.OpEffectNotCommitted) {
		outcome, err = c.Resolutions.CancelResolution(ctx, operation.Work.OperationID)
	}
	if err != nil || outcome.NotObserved {
		return ErrDeliveryUnavailable
	}
	operation.Outcome = outcome
	if !cancel && outcome.Effect == factory.OpEffectCommitted && outcome.Completion == factory.OpCompletionComplete {
		confirmed, confirmErr := c.Resolutions.ConfirmResolution(ctx, operation)
		err = confirmErr
		if err != nil {
			return err
		}
		if confirmed {
			operation.Confirmed = true
		} else if operation.Confirmed {
			return errors.New("confirmed native resolution changed")
		}
	}
	if !reflect.DeepEqual(p.ResolutionOperations[index], operation) {
		p.ResolutionOperations[index] = operation
		p.Revision++
		return c.Store.UpdatePublication(ctx, *p)
	}
	return nil
}

// currentFundedDeliveryAcceptance re-composes the same semantic evidence on
// merge admission. Expected resolution effects may advance native revision;
// any other change prevents reuse without another valid decision.
func (c *Coordinator) currentFundedDeliveryAcceptance(ctx context.Context, p factory.Publication) (*factory.DeliveryAcceptance, factory.DeliveryDecision, error) {
	review, ready, pending, refused := c.currentReviewReport(ctx, p)
	if !ready || pending || refused || review.Review == nil || c.Planning == nil {
		return nil, factory.DeliveryDecision{}, nil
	}
	chooser, ok := c.Planning.Chooser.(deliveryProductionChooser)
	if !ok {
		return nil, factory.DeliveryDecision{}, ErrDeliveryUnavailable
	}
	material, cycle, _, err := c.deliveryMaterial(ctx, p, review, chooser.DeliveryModel())
	if err != nil {
		return nil, factory.DeliveryDecision{}, err
	}
	if err := material.validate(); err != nil {
		return nil, factory.DeliveryDecision{}, err
	}
	fingerprint, err := factory.DeliveryFingerprint(material.Input)
	if err != nil {
		return nil, factory.DeliveryDecision{}, err
	}
	receipt, err := c.Store.DeliveryJudgment(ctx, fingerprint)
	if err != nil || !receipt.CurrentFor(fingerprint) || receipt.Decision.Effective != factory.DeliveryEffectiveReady {
		return nil, factory.DeliveryDecision{}, ErrDeliveryUnavailable
	}
	for _, acceptance := range p.DeliveryAcceptances {
		if acceptance.Kind != factory.AcceptanceJevAcceptance || acceptance.Round != cycle.Round || acceptance.DecisionID != deliveryContentDigest("soda-funded-delivery-v1:" + fingerprint)[:32] {
			continue
		}
		for _, intent := range acceptance.ResolutionIntents {
			confirmed := false
			for _, operation := range p.ResolutionOperations {
				if operation.Work.OperationID == intent && operation.AcceptanceID == acceptance.ID && operation.Confirmed {
					confirmed = true
				}
			}
			if !confirmed {
				return nil, factory.DeliveryDecision{}, &factory.PublicationWait{Reason: "resolution_pending"}
			}
		}
		return &acceptance, receipt.Decision, nil
	}
	return nil, factory.DeliveryDecision{}, ErrDeliveryUnavailable
}
