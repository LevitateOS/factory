package control

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
)

// ProductJudgment returns a validated answer for the supplied options. The host
// supplies the explicitly selected provider/model/source and its admission.
type ProductJudgment interface {
	Select(context.Context, any, string, map[string]any) (factory.PriorityAnswer, error)
}

// ScopedProductJudgment lets a host with repository-selected funding bind a
// judgment to its canonical caller. Fixed server providers retain Select.
type ScopedProductJudgment interface {
	SelectScoped(context.Context, int64, string, any, string, map[string]any) (factory.PriorityAnswer, error)
}

func SelectProductJudgment(ctx context.Context, chooser ProductJudgment, repository int64, subject string, state any, question string, options map[string]any) (factory.PriorityAnswer, error) {
	if scoped, ok := chooser.(ScopedProductJudgment); ok {
		return scoped.SelectScoped(ctx, repository, subject, state, question, options)
	}
	if chooser == nil {
		return factory.PriorityAnswer{}, ErrPriorityUnavailable
	}
	return chooser.Select(ctx, state, question, options)
}

// PlanningJudgments carries the chooser shared by planning purposes.
type PlanningJudgments struct {
	Chooser ProductJudgment
}
