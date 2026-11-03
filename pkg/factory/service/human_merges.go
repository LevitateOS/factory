package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanMergeQuery       = errors.New("invalid human merge request")
	ErrHumanMergeAdmission   = errors.New("current human merge admission required")
	ErrHumanMergeConflict    = errors.New("human merge intent conflicts")
	ErrHumanMergeUnavailable = errors.New("human merge provider unavailable")
	ErrHumanMergeNamespace   = errors.New("captured merge installation differs from current provider")
)

// HumanMergeWriter consumes exact journaled preparation for ordinary merges and
// manual marking. Preparation is a read, not authority. Commit its returned
// capture before dispatch, and reconcile lost replies using that same capture.
// Lookup/cancel require current human admission even after permission loss.
type HumanMergeWriter interface {
	PrepareHumanMerge(context.Context, factory.HumanMergeCommand) (factory.HumanMergeCapture, error)
	SubmitHumanMerge(context.Context, factory.HumanMergeCapture) (collaboration.OperationRecord, error)
	LookupHumanMerge(context.Context, factory.HumanMergeCapture) (collaboration.OperationLookup, error)
	CancelHumanMerge(context.Context, factory.HumanMergeCapture) (collaboration.OperationRecord, error)
}

// HumanMerges runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanMerges struct {
	Store  *store.Store
	Writer HumanMergeWriter
}

func (a Application) HumanMerges(writer HumanMergeWriter) HumanMerges {
	return HumanMerges{Store: a.Store, Writer: writer}
}

// command resolves the original method/message or selected commit only within
// its admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanMerges) command(ctx context.Context, scope factory.HumanMergeCommand) (factory.HumanMergeCommand, error) {
	if scope.ValidateScope() != nil || scope.Method != "" || scope.Message != "" || scope.CommitOID != "" {
		return factory.HumanMergeCommand{}, ErrHumanMergeQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanMergeCommand{}, ErrHumanMergeUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanMergeCommand{}, err
	}
	c, err := factory.DecodeHumanMergeCapture(cmd)
	if err != nil {
		return factory.HumanMergeCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Method, actual.Message, actual.CommitOID = "", "", ""
	if !actual.SameRequest(scope) {
		return factory.HumanMergeCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanMerges) LookupCommand(ctx context.Context, scope factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanMerges) CancelCommand(ctx context.Context, scope factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanMerges) capture(ctx context.Context, q factory.HumanMergeCommand, prepare bool) (factory.HumanMergeCapture, error) {
	var empty factory.HumanMergeCapture
	if q.Validate() != nil {
		return empty, ErrHumanMergeQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanMergeUnavailable
	}
	c, err := s.Store.HumanMergeCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanMerge(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanMergeCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanMergeConflict
	}
	c, _, err = s.Store.CaptureHumanMerge(ctx, c)
	return c, err
}

func (s HumanMerges) observe(ctx context.Context, c factory.HumanMergeCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanMerge(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanMerge(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanMerges) unknown(ctx context.Context, c factory.HumanMergeCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanMerge(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanMerges) save(ctx context.Context, c factory.HumanMergeCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanMerge(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanMerges) Submit(ctx context.Context, q factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	o, err := s.observe(ctx, c)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	if o.Record != nil || o.State != collaboration.LookupNotObserved {
		return o, nil
	}
	r, err := s.Writer.SubmitHumanMerge(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanMerges) Lookup(ctx context.Context, q factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanMerges) Cancel(ctx context.Context, q factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	o, err := s.observe(ctx, c)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	if o.Record != nil && (o.Record.Effect == collaboration.EffectCommitted || o.Record.Effect == collaboration.EffectNotCommitted) {
		return o, nil
	}
	if o.Record == nil && o.State != collaboration.LookupNotObserved {
		return o, nil
	}
	r, err := s.Writer.CancelHumanMerge(ctx, c)
	return s.save(ctx, c, r, err)
}
