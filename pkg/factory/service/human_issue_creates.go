package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanIssueCreateQuery       = errors.New("invalid human issue creation request")
	ErrHumanIssueCreateAdmission   = errors.New("current human issue creation admission required")
	ErrHumanIssueCreateConflict    = errors.New("human issue creation intent conflicts")
	ErrHumanIssueCreateUnavailable = errors.New("human issue creation provider unavailable")
	ErrHumanIssueCreateNamespace   = errors.New("captured issue creation installation differs from current provider")
)

// HumanIssueCreateWriter operates on the exact saved capture. Preparation is a
// read, never authority: the application must commit it before Submit. Providers
// reconcile/replay that identity without duplicate creation; they cannot reprepare
// or project actionable absence for a possibly submitted command without proof.
type HumanIssueCreateWriter interface {
	PrepareHumanIssueCreate(context.Context, factory.HumanIssueCreateCommand) (factory.HumanIssueCreateCapture, error)
	SubmitHumanIssueCreate(context.Context, factory.HumanIssueCreateCapture) (collaboration.OperationRecord, error)
	LookupHumanIssueCreate(context.Context, factory.HumanIssueCreateCapture) (collaboration.OperationLookup, error)
	CancelHumanIssueCreate(context.Context, factory.HumanIssueCreateCapture) (collaboration.OperationRecord, error)
	// ReadCreatedHumanIssue projects only an authenticated committed receipt and
	// rechecks current issue visibility. It never searches by original writing.
	ReadCreatedHumanIssue(context.Context, factory.HumanIssueCreateCapture, collaboration.OperationLookup) (collaboration.IssueRef, error)
}

// HumanIssueCreates runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanIssueCreates struct {
	Store  *store.Store
	Writer HumanIssueCreateWriter
}

func (a Application) HumanIssueCreates(writer HumanIssueCreateWriter) HumanIssueCreates {
	return HumanIssueCreates{Store: a.Store, Writer: writer}
}

// command resolves original server-retained writing only within the admitted
// host/actor/repository. Public lookup never accepts provider preparation.
func (s HumanIssueCreates) command(ctx context.Context, scope factory.HumanIssueCreateCommand) (factory.HumanIssueCreateCommand, error) {
	if scope.ValidateScope() != nil || scope.Title != "" || scope.Body != "" {
		return factory.HumanIssueCreateCommand{}, ErrHumanIssueCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanIssueCreateCommand{}, ErrHumanIssueCreateUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanIssueCreateCommand{}, err
	}
	c, err := factory.DecodeHumanIssueCreateCapture(cmd)
	if err != nil {
		return factory.HumanIssueCreateCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Title, actual.Body = "", ""
	if !actual.SameRequest(scope) {
		return factory.HumanIssueCreateCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanIssueCreates) LookupCommand(ctx context.Context, scope factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanIssueCreates) CancelCommand(ctx context.Context, scope factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

// CreatedIssue resolves the retained capture in the caller's exact scope. A
// failed destination read cannot erase the separately recorded committed effect.
func (s HumanIssueCreates) CreatedIssue(ctx context.Context, scope factory.HumanIssueCreateCommand, outcome collaboration.OperationLookup) (collaboration.IssueRef, error) {
	var empty collaboration.IssueRef
	q, err := s.command(ctx, scope)
	if err != nil {
		return empty, err
	}
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanIssueCreateObservation(c, outcome) != nil || outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted {
		return empty, ErrHumanIssueCreateQuery
	}
	issue, err := s.Writer.ReadCreatedHumanIssue(ctx, c, outcome)
	if err != nil {
		return empty, err
	}
	if ctx.Err() != nil || issue.Kind != collaboration.SubjectIssue || issue.Repository != q.Repository || issue.Number <= 0 || !collaboration.ValidText(issue.ExternalID, 512) {
		return empty, ErrHumanIssueCreateUnavailable
	}
	return issue, nil
}

func (s HumanIssueCreates) capture(ctx context.Context, q factory.HumanIssueCreateCommand, prepare bool) (factory.HumanIssueCreateCapture, error) {
	var empty factory.HumanIssueCreateCapture
	if q.Validate() != nil {
		return empty, ErrHumanIssueCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanIssueCreateUnavailable
	}
	c, err := s.Store.HumanIssueCreateCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanIssueCreate(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanIssueCreateCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanIssueCreateConflict
	}
	c, _, err = s.Store.CaptureHumanIssueCreate(ctx, c)
	return c, err
}

func (s HumanIssueCreates) observe(ctx context.Context, c factory.HumanIssueCreateCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanIssueCreate(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanIssueCreate(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanIssueCreates) unknown(ctx context.Context, c factory.HumanIssueCreateCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanIssueCreate(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanIssueCreates) save(ctx context.Context, c factory.HumanIssueCreateCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanIssueCreate(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanIssueCreates) Submit(ctx context.Context, q factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanIssueCreate(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanIssueCreates) Lookup(ctx context.Context, q factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanIssueCreates) Cancel(ctx context.Context, q factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanIssueCreate(ctx, c)
	return s.save(ctx, c, r, err)
}
