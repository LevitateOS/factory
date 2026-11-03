package control

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// AuthorizeMerge binds the first and only authorization generation to a
// recorded, still-undriven open merge row (D09 registration). The binding
// enforces the FW37 union gate — the journaled current applicable D07
// delivery acceptance, confirmed accepted resolutions and matching
// authority — so it can never authorize another candidate. A bound row
// is never rebound.
func (c *Coordinator) AuthorizeMerge(ctx context.Context, publicationID string, auth factory.MergeAuthorization) error {
	m, err := c.Store.MergeByPublication(ctx, publicationID)
	if err != nil {
		return err
	}
	if err := auth.Validate(); err != nil {
		return err
	}
	// Only the first generation binds, once, on an unbound row.
	if m.Authorization.Bound() || len(m.History) != 0 || auth.Generation.Generation != 1 {
		return errors.New("merge authorization generation needs its ledger history")
	}
	return c.authorizeFirstGeneration(ctx, m, auth)
}

// authorizeFirstGeneration binds the ready first-generation
// authorization to a recorded, still-undriven open row.
func (c *Coordinator) authorizeFirstGeneration(ctx context.Context, m factory.Merge, auth factory.MergeAuthorization) error {
	if m.Stage != factory.MergeOpen {
		return errors.New("merge authorization needs its open row")
	}
	if m.Authorization.Bound() {
		return errors.New("merge authorization already bound")
	}
	if auth.Generation.PublicationID != m.PublicationID ||
		auth.Generation.Head != m.HeadOID || auth.Generation.Base != m.BaseOID {
		return errors.New("merge authorization answers another candidate")
	}
	if auth.Generation.Authority != m.Authority {
		return errors.New("merge authority changed under its authorization")
	}
	if m.Operation.Work != nil || m.Operation.Attempts != 0 {
		return errors.New("merge authorization precedes its first drive")
	}
	if !auth.Generation.UnionBound() {
		return errors.New("merge authorization lacks its acceptance binding")
	}
	p, err := c.Store.PublicationByAssignment(ctx, m.AssignmentID)
	if err != nil {
		return errors.New("merge authorization lacks its publication")
	}
	acceptance, ok := journaledAcceptance(p, auth.Generation.AcceptanceID)
	if !ok {
		return errors.New("delivery acceptance unrecorded for its merge binding")
	}
	if err := factory.CheckMergeUnionBinding(factory.MergeUnionBindingInput{
		Publication: p, Acceptance: acceptance, Generation: auth.Generation,
		Decision: auth.Decision, ResolutionsConfirmed: auth.ResolutionsConfirmed,
		CurrentAuthority: m.Authority,
	}); err != nil {
		return err
	}
	m.Authorization = auth
	m.Revision++
	return c.Store.UpdateMerge(ctx, m)
}

// journaledAcceptance returns the publication's journaled acceptance with
// the given identity. Journals are append-only, so a missing identity
// never appears later for the same binding.
func journaledAcceptance(p factory.Publication, id string) (factory.DeliveryAcceptance, bool) {
	for _, acceptance := range p.DeliveryAcceptances {
		if acceptance.ID == id {
			return acceptance, true
		}
	}
	return factory.DeliveryAcceptance{}, false
}

// mergeAuthorizationGate enforces the common union gate before any new
// native mutation on the merge path. Publication progress, MergePass,
// mergeOne and reconciliation all pass through it; recovery of an
// already-recorded operation (lookup, receipt adoption, completion and
// cancellation) never enters it. An unbound row waits for its
// authorization instead of submitting; a stale binding waits instead of
// merging another candidate. Union-bound rows re-verify their journaled
// acceptance on every pass; earlier funded-decision-only bindings keep
// their legacy gate so committed effects reconcile untouched. No branch
// spends or mutates.
func mergeAuthorizationGate(m factory.Merge, p factory.Publication) error {
	if !m.Authorization.Bound() {
		return &factory.PublicationWait{Reason: "merge_judgment_unavailable"}
	}
	auth := m.Authorization
	if auth.Generation.Head != m.HeadOID {
		return &factory.PublicationWait{Reason: "superseded_head"}
	}
	if auth.Generation.Base != m.BaseOID {
		return &factory.PublicationWait{Reason: "superseded_base"}
	}
	if auth.Generation.UnionBound() {
		acceptance, ok := journaledAcceptance(p, auth.Generation.AcceptanceID)
		if !ok {
			return &factory.PublicationWait{Reason: "merge_authorization_stale"}
		}
		// The row authority was just verified current, so this
		// re-verifies the immutable union binding against it: the
		// journaled current applicable acceptance behind the exact
		// generation with confirmed accepted resolutions.
		if err := factory.CheckMergeUnionBinding(factory.MergeUnionBindingInput{
			Publication: p, Acceptance: acceptance, Generation: auth.Generation,
			Decision: auth.Decision, ResolutionsConfirmed: auth.ResolutionsConfirmed,
			CurrentAuthority: m.Authority,
		}); err != nil {
			return &factory.PublicationWait{Reason: "merge_authorization_stale"}
		}
		return nil
	}
	// The row authority was just verified current, so this re-verifies
	// the immutable binding against it: a ready decision over the exact
	// generation fingerprint with confirmed accepted resolutions.
	if err := factory.CheckMergeDecisionGate(factory.MergeDecisionInput{
		Generation: auth.Generation, Decision: auth.Decision,
		CurrentAuthority: m.Authority, ResolutionsConfirmed: auth.ResolutionsConfirmed,
	}); err != nil {
		return &factory.PublicationWait{Reason: "merge_authorization_stale"}
	}
	return nil
}
