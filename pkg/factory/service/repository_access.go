package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// RepositoryAccess assembles current settings powers and private usage. Viewer
// supplies host authority/membership facts; Connections supplies only connection
// IDs owned by the admitted actor. A failed ownership read withholds all usage.
// Every save still requires admission at its owning command, independently.
type RepositoryAccess struct {
	Store       *store.Store
	Visibility  WorkVisibility
	Viewer      func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (RepositoryViewer, error)
	Connections func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (map[string]bool, error)
	Preparation func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (*RepositoryPreparation, error)
	Authority   func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (factory.EffectiveAuthority, error)
}

var (
	ErrRepositoryAccessInvalid     = errors.New("repository access scope is invalid")
	ErrRepositoryAccessUnavailable = errors.New("repository access runtime is unavailable")
	ErrRepositoryAccessVisibility  = errors.New("current repository access visibility is required")
	ErrRepositoryAccessViewer      = errors.New("repository viewer authority is unavailable")
	ErrRepositoryAccessPreparation = errors.New("repository access preparation is unavailable")
)

func (s RepositoryAccess) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (*RepositoryAccessView, error) {
	if actor < 1 || repository < 1 {
		return nil, ErrRepositoryAccessInvalid
	}
	if s.Store == nil || s.Visibility == nil || s.Viewer == nil || s.Connections == nil || s.Preparation == nil || s.Authority == nil {
		return nil, ErrRepositoryAccessUnavailable
	}
	authorize := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Visibility(ctx, actor, repository); err != nil {
			return errors.Join(ErrRepositoryAccessVisibility, err)
		}
		return ctx.Err()
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	viewer, err := s.Viewer(ctx, actor, repository)
	if err != nil {
		return nil, errors.Join(ErrRepositoryAccessViewer, err)
	}
	sponsorships, err := s.Store.Sponsorships(ctx, int64(repository))
	if err != nil {
		return nil, err
	}
	editable := accessEditableInput{Viewer: viewer}
	for _, sponsorship := range sponsorships {
		if sponsorship.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		editable.Connections = append(editable.Connections, sponsorship.Connection)
	}
	owned, err := s.Connections(ctx, actor, repository)
	if err == nil {
		editable.IdentityOK, editable.Owned = true, owned
	}
	view := &RepositoryAccessView{Viewer: viewer, Editable: accessEditableDTO(editable),
		Repository: strconv.FormatInt(int64(repository), 10), ObservedUnix: time.Now().Unix(),
		RequiredActs: []accessActionView{}, Usage: []accessUsageView{}}
	view.Effective, err = s.Authority(ctx, actor, repository)
	if err != nil {
		return nil, err
	}
	view.RequiredActs = accessRequiredActions(view.Effective.Missing)
	if viewer.LocalOwner {
		for i, action := range view.RequiredActs {
			switch action.Setting {
			case "policy", "operator_grant", "capacity", "environment_grant":
				view.RequiredActs[i].ResolveBy = "local_owner"
			}
		}
	}
	view.Preparation, err = s.Preparation(ctx, actor, repository)
	if err != nil {
		return nil, errors.Join(ErrRepositoryAccessPreparation, err)
	}
	if view.Preparation != nil && view.Preparation.Project == "" {
		return nil, ErrRepositoryAccessPreparation
	}
	for _, sponsorship := range sponsorships {
		if !editable.IdentityOK {
			view.Usage = append(view.Usage, accessUsageView{Connection: sponsorship.Connection, Unit: "minutes", Status: accessUsageIdentityDown})
			continue
		}
		in := accessUsageInput{Connection: sponsorship.Connection, Owned: editable.Owned[sponsorship.Connection], Budget: int64(sponsorship.AllowanceMinutes)}
		if in.Owned {
			if total, err := s.Store.UsageTotal(ctx, int64(repository), sponsorship.Connection); err == nil {
				in.Used, in.UsedOK = total, true
			}
		}
		view.Usage = append(view.Usage, accessUsageDTO(in))
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	return view, nil
}
