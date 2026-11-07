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

// PlanningJudgments carries the chooser shared by planning purposes.
type PlanningJudgments struct {
	Chooser ProductJudgment
}
