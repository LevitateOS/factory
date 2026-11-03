package factory

import "errors"

// ResolveWork is the immutable conditional native intent, persisted before
// submission. Resolution has its own enrolled credential on the independent
// reviewer account; review permission never substitutes for it. Repository,
// reviewer and PR-author fields are canonical; thread/PR IDs stay provider-owned.
type ResolveWork struct {
	Request        ResolveRequest  `json:"request"`
	Binding        ActorBindingRef `json:"binding"`
	PRAuthorID     int64           `json:"pr_author_id"`
	OperationID    string          `json:"operation_id"`
	AuthRevision   string          `json:"authorization_revision"`
	NativeRevision int64           `json:"native_revision"`
	NotAfter       int64           `json:"not_after"`
}

func (w ResolveWork) Validate() error {
	evidence := ThreadEvidence{Digest: w.Request.Digest, Repository: w.Request.Repository, PRNumber: w.Request.PRNumber, RootCommentID: w.Request.RootCommentID}
	if w.Request.ValidateForEvidence(w.Binding, w.PRAuthorID, evidence) != nil || !ValidID(w.OperationID) || w.AuthRevision == "" || len(w.AuthRevision) > 256 || w.NativeRevision < 1 || w.NotAfter < 1 {
		return errors.New("invalid conditional resolution work")
	}
	return nil
}

type DeliveryResolutionOperation struct {
	AcceptanceID   string           `json:"acceptance_id"`
	FindingKey     string           `json:"finding_key"`
	ResolvedDigest string           `json:"resolved_digest"`
	Work           ResolveWork      `json:"work"`
	Outcome        OperationOutcome `json:"outcome"`
	Confirmed      bool             `json:"confirmed"`
}

func (p Publication) validateDeliveryResolutions() error {
	if len(p.ResolutionOperations) > MaxPublicationReviewOperations*MaxOutstandingFindings {
		return errors.New("delivery resolution history exceeds its bound")
	}
	seen := map[string]bool{}
	for _, operation := range p.ResolutionOperations {
		w := operation.Work
		if w.Validate() != nil || !ValidFindingKey(operation.FindingKey) || !ValidDigest(operation.ResolvedDigest) || seen[w.OperationID] || w.Request.Repository != p.Repository || w.Request.PRID != p.PRID || w.Request.PRNumber != p.PRNumber {
			return errors.New("delivery resolution differs from its publication")
		}
		seen[w.OperationID] = true
		bound := false
		for _, acceptance := range p.DeliveryAcceptances {
			if acceptance.ID != operation.AcceptanceID || acceptance.Kind != AcceptanceJevAcceptance || acceptance.Tuple.Head != w.Request.HeadOID || acceptance.Tuple.Base != w.Request.BaseOID {
				continue
			}
			for _, id := range acceptance.ResolutionIntents {
				bound = bound || id == w.OperationID
			}
		}
		if !bound {
			return errors.New("delivery resolution lacks its funded acceptance")
		}
		o := operation.Outcome
		tombstone := o.Kind == "" && o.ActorID == 0 && o.RepositoryID == 0 && o.Effect == OpEffectNotCommitted && o.Cancellation == OpCancelCancelled && len(o.Receipt) == 0
		if o.OperationID != "" && (o.OperationID != w.OperationID || (!tombstone && (o.Kind != OpThreadResolve || o.ActorID != w.Request.ActorID || o.RepositoryID != w.Request.Repository)) || o.InstallationID == "" || !ValidOpEffect(o.Effect) || !ValidOpCancellation(o.Cancellation) || (o.Completion != "" && !ValidOpCompletion(o.Completion)) || len(o.Receipt) > 4096) {
			return errors.New("delivery resolution outcome differs from its immutable intent")
		}
		if operation.Confirmed && (o.Effect != OpEffectCommitted || o.Completion != OpCompletionComplete) {
			return errors.New("delivery resolution confirmation lacks its committed receipt")
		}
	}
	return nil
}
