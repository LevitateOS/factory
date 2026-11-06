package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/store"
)

// Recover a concurrent winner only after preparation fails. A successful
// preparation still faces its typed request match before the store commits it.
func loadOrPrepareHumanCapture[C any](ctx context.Context, allowPreparation bool, load, prepare func(context.Context) (C, error)) (C, bool, error) {
	capture, err := load(ctx)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !allowPreparation {
		return capture, false, err
	}
	capture, err = prepare(ctx)
	if err == nil {
		return capture, true, nil
	}
	var empty C
	raced, lookupErr := load(ctx)
	if lookupErr == nil {
		return raced, false, nil
	}
	if !errors.Is(lookupErr, store.ErrNotFound) {
		return empty, false, lookupErr
	}
	return empty, false, err
}

// One already captured operation binds current provider reconciliation to its
// durable observation sink. This owner neither prepares nor selects new intent.
type humanEffect struct {
	binding collaboration.OperationLookup
	lookup  func(context.Context) (collaboration.OperationLookup, error)
	persist func(context.Context, collaboration.OperationLookup) (collaboration.OperationLookup, error)
}

func (e humanEffect) reconcile(ctx context.Context) (collaboration.OperationLookup, error) {
	observation, err := e.lookup(ctx)
	if err != nil {
		e.unknown(ctx)
		return collaboration.OperationLookup{}, err
	}
	return e.persist(ctx, observation)
}

// Losing a provider reply proves neither delivery nor no effect. Cancellation
// may prevent this best-effort write; the original capture remains recoverable.
func (e humanEffect) unknown(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	observation := e.binding
	observation.State = collaboration.LookupUnknown
	observation.Reason = "provider_reply_unavailable"
	_, _ = e.persist(ctx, observation)
}

func (e humanEffect) save(ctx context.Context, record collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		e.unknown(ctx)
		return collaboration.OperationLookup{}, err
	}
	observation := e.binding
	observation.State, observation.Record = collaboration.LookupFound, &record
	return e.persist(ctx, observation)
}

// Only live, admitted not-observed reconciliation can send the saved intent.
// Known primary effects retain their independently unfinished completion.
func (e humanEffect) submit(ctx context.Context, action func(context.Context) (collaboration.OperationRecord, error)) (collaboration.OperationLookup, error) {
	observation, err := e.reconcile(ctx)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	if observation.Record != nil || observation.State != collaboration.LookupNotObserved {
		return observation, nil
	}
	record, err := action(ctx)
	return e.save(ctx, record, err)
}

// A known primary needs no new cancellation. Unknown delivery without a record
// also cannot authorize an action; the same saved operation stays reconcilable.
func (e humanEffect) cancel(ctx context.Context, action func(context.Context) (collaboration.OperationRecord, error)) (collaboration.OperationLookup, error) {
	observation, err := e.reconcile(ctx)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	if observation.Record != nil && (observation.Record.Effect == collaboration.EffectCommitted || observation.Record.Effect == collaboration.EffectNotCommitted) {
		return observation, nil
	}
	if observation.Record == nil && observation.State != collaboration.LookupNotObserved {
		return observation, nil
	}
	record, err := action(ctx)
	return e.save(ctx, record, err)
}
