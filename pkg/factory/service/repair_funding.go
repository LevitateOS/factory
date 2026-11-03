package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

type RepairFundingChange struct {
	CommandID         string `json:"command_id"`
	ExpectedRevision  int64  `json:"expected_revision"`
	InvestigatorRuns  int    `json:"investigator_runs"`
	ProposalJudgments int    `json:"proposal_judgments"`
	ActiveMinutes     int    `json:"active_minutes"`
	Effect            string `json:"effect"`
	Active            bool   `json:"active"`
}

type RepairFundingView struct {
	Revision int64                       `json:"revision"`
	Grant    *factory.RepairGrant        `json:"grant"`
	Charges  []factory.RepairCharge      `json:"charges"`
	Episodes []factory.RepairEpisodeView `json:"episodes"`
}

// RepairFunding uses the existing repair command/charge journal. The admitted
// canonical sponsor and authenticated host principal remain separate. Neither
// saving permission nor reading history grants custody or launches work.
type RepairFunding struct {
	Store          *store.Store
	Administration RequestWriteAccess
}

var (
	ErrRepairFundingUnavailable = errors.New("repair funding admission is unavailable")
	ErrRepairFundingAccess      = errors.New("current repository administration is required for repair funding")
	ErrRepairFundingInvalid     = errors.New("repair funding limits or scope are invalid")
	ErrRepairFundingCommand     = errors.New("repair funding command identity is invalid")
	ErrRepairFundingScope       = errors.New("repair funding journal scope differs from its repository")
	ErrRepairFundingIncrease    = errors.New("repair funding increase must state its effect")
	ErrRepairFundingSponsor     = errors.New("the recorded repair sponsor is required")
)

func (s RepairFunding) authorize(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if actor < 1 || repository < 1 {
		return ErrRepairFundingInvalid
	}
	if s.Store == nil || s.Administration == nil {
		return ErrRepairFundingUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Administration(ctx, actor, int64(repository)); err != nil {
		return errors.Join(ErrRepairFundingAccess, err)
	}
	return ctx.Err()
}

func (s RepairFunding) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (*RepairFundingView, error) {
	if err := s.authorize(ctx, actor, repository); err != nil {
		return nil, err
	}
	view := &RepairFundingView{Charges: []factory.RepairCharge{}, Episodes: []factory.RepairEpisodeView{}}
	grant, err := s.Store.RepairGrantCommand(ctx, int64(repository))
	if err == nil {
		if grant.Repository != int64(repository) || grant.Validate() != nil {
			return nil, ErrRepairFundingScope
		}
		view.Grant, view.Revision = &grant, grant.Revision+1
		charges, err := s.Store.RepairCharges(ctx, int64(repository))
		if err != nil {
			return nil, err
		}
		view.Charges = append(view.Charges, charges...)
		view.Episodes, err = s.Store.RepairEpisodeViews(ctx, int64(repository))
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err := s.authorize(ctx, actor, repository); err != nil {
		return nil, err
	}
	return view, nil
}

// Apply preserves a captured command's timestamp/head on exact replay. New
// revisions must explicitly increase limits without resetting prior consumption.
// The store's transaction remains the final predecessor/CAS/replay owner.
func (s RepairFunding) Apply(ctx context.Context, actor collaboration.ActorID, principal string, repository collaboration.RepositoryID, in RepairFundingChange) (factory.GrantResult, error) {
	var empty factory.GrantResult
	if !factory.ValidID(in.CommandID) {
		return empty, ErrRepairFundingCommand
	}
	if in.ExpectedRevision < 0 || principal == "" || len(principal) > 128 {
		return empty, ErrRepairFundingInvalid
	}
	if err := s.authorize(ctx, actor, repository); err != nil {
		return empty, err
	}
	grant := factory.RepairGrant{ID: in.CommandID, Repository: int64(repository), Sponsor: int64(actor), InvestigatorRuns: in.InvestigatorRuns,
		ProposalJudgments: in.ProposalJudgments, ActiveMinutes: in.ActiveMinutes, Effect: in.Effect, Active: in.Active, CreatedUnix: time.Now().Unix()}
	priorCommand, err := s.Store.FactoryCommand(ctx, in.CommandID)
	if err == nil {
		var prior factory.RepairGrant
		if priorCommand.Type != factory.CommandRepairGrant || priorCommand.Target != factory.RepairTarget(int64(repository)) || priorCommand.Principal != principal ||
			json.Unmarshal([]byte(priorCommand.Payload), &prior) != nil || prior.ID != in.CommandID || prior.Repository != int64(repository) || prior.Sponsor != int64(actor) || prior.Validate() != nil {
			return empty, store.ErrCommandConflict
		}
		grant.CreatedUnix, grant.Predecessor, grant.Revision = prior.CreatedUnix, prior.Predecessor, prior.Revision
		if in.ExpectedRevision != grant.Revision {
			return empty, store.ErrCommandConflict
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, err
	} else {
		current, err := s.Store.RepairGrantCommand(ctx, int64(repository))
		if err == nil {
			if current.Repository != int64(repository) || current.Validate() != nil {
				return empty, ErrRepairFundingScope
			}
			if in.ExpectedRevision != current.Revision+1 {
				return empty, store.ErrStaleRevision
			}
			if current.Sponsor != int64(actor) {
				return empty, ErrRepairFundingSponsor
			}
			if (grant.InvestigatorRuns > current.InvestigatorRuns || grant.ProposalJudgments > current.ProposalJudgments || grant.ActiveMinutes > current.ActiveMinutes) && grant.Effect == "" {
				return empty, ErrRepairFundingIncrease
			}
			grant.Revision, grant.Predecessor = current.Revision+1, current.ID
		} else if !errors.Is(err, store.ErrNotFound) {
			return empty, err
		} else if in.ExpectedRevision != 0 {
			return empty, store.ErrStaleRevision
		}
	}
	if grant.Validate() != nil {
		return empty, ErrRepairFundingInvalid
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		return empty, err
	}
	command := factory.Command{ID: in.CommandID, Type: factory.CommandRepairGrant, Target: factory.RepairTarget(int64(repository)), Principal: principal, Payload: string(raw)}
	command.Digest = factory.SettingsDigest(command.Type, command.Target, command.Payload)
	if err := s.authorize(ctx, actor, repository); err != nil {
		return empty, err
	}
	recorded, _, err := s.Store.ApplyRepairGrantCommand(ctx, command, grant, time.Now())
	if err != nil {
		return empty, err
	}
	return factory.GrantResult{CommandID: recorded.ID, Revision: grant.Revision + 1}, nil
}
