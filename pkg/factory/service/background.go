package service

import (
	"context"

	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

// Background continues already recorded requests and contribution sources over
// their existing journals. Stages supply admitted authoring/effect ports. Chooser
// resolves current availability without making a judgment or retaining a secret.
// Construction starts no work and needs no HTTP object or session.
type Background struct {
	Store   *store.Store
	Stages  PostCollaborators
	Chooser func() control.ProductJudgment
}

func (s Background) currentChooser() control.ProductJudgment {
	if s.Chooser == nil {
		return nil
	}
	return s.Chooser()
}

// Collaborators projects currently available stages for commands and readback.
// This copies configuration; it never routes, launches or claims a source.
func (s Background) Collaborators() *PostCollaborators {
	wired := s.Stages
	if chooser := s.currentChooser(); chooser != nil {
		wired.Judge = &ChoicePostJudge{Chooser: chooser}
		wired.Prepare = (PostContext{Posts: Posts{Store: s.Store}}).Prepare
	}
	return &wired
}

// Drive is the coordinator lifetime callback. Every pass is bounded by the
// existing post page and one contribution claim. Failures remain in their owning
// journals; a callback return never asserts that all requests completed.
func (s Background) Drive(ctx context.Context) {
	if s.Store == nil || ctx.Err() != nil {
		return
	}
	if pending, ok := s.Stages.Judge.(interface{ ReconcilePostJudgments(context.Context) }); ok {
		pending.ReconcilePostJudgments(ctx)
	}
	_ = DrivePendingPosts(ctx, s.Store, s.Collaborators(), PostDriveLimit)
	if ctx.Err() != nil {
		return
	}
	// A key may have been connected/withdrawn while post stages were doing I/O.
	// Re-resolve availability before a contribution can claim its source.
	_, _ = (Contributions{Store: s.Store, Chooser: s.currentChooser()}).Drive(ctx)
}
