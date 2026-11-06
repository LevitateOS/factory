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
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanIssueCreateCapture, error) {
			return s.Store.HumanIssueCreateCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanIssueCreateCapture, error) {
			return s.Writer.PrepareHumanIssueCreate(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanIssueCreateConflict
	}
	capture, _, err = s.Store.CaptureHumanIssueCreate(ctx, capture)
	return capture, err
}

func (s HumanIssueCreates) effect(capture factory.HumanIssueCreateCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanIssueCreate(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanIssueCreate(ctx, capture, observation)
		},
	}
}

func (s HumanIssueCreates) Submit(ctx context.Context, q factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanIssueCreate(ctx, capture)
	})
}

func (s HumanIssueCreates) Lookup(ctx context.Context, q factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanIssueCreates) Cancel(ctx context.Context, q factory.HumanIssueCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanIssueCreate(ctx, capture)
	})
}
