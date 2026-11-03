package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/control"
	"github.com/levitateos/sodaos/internal/project"
)

type AcceptanceWriter interface {
	AdmitAcceptance(context.Context, factory.Acceptance, control.AcceptanceAuthorization) (control.AcceptanceResult, error)
	WithdrawAcceptance(context.Context, int64, int64, string, int64, control.AcceptanceAuthorization) (control.WithdrawalResult, error)
}

type Acceptances struct {
	Writer      AcceptanceWriter
	Evidence    control.AcceptanceSource
	WriteAccess WorkVisibility
}

type AcceptanceWithdrawal struct {
	DecisionID string `json:"decision_id"`
}

var (
	ErrAcceptanceUnavailable  = errors.New("acceptance admission is unavailable")
	ErrAcceptanceAccess       = errors.New("current repository acceptance authority is required")
	ErrAcceptanceInvalid      = errors.New("acceptance must select its exact decision and revisions")
	ErrAcceptanceSource       = errors.New("acceptance source revision is invalid")
	ErrAcceptancePrerequisite = errors.New("acceptance prerequisite scope is invalid")
)

func acceptancePositiveID(value string) (int64, bool) {
	if len(value) == 0 || len(value) > 19 {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

func (s Acceptances) authorize(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if actor < 1 || repository < 1 {
		return ErrAcceptanceInvalid
	}
	if s.WriteAccess == nil {
		return ErrAcceptanceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.WriteAccess(ctx, actor, repository); err != nil {
		return errors.Join(ErrAcceptanceAccess, err)
	}
	return ctx.Err()
}

func (s Acceptances) Preview(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue string, comments []string) (*AcceptancePreview, error) {
	if _, valid := acceptancePositiveID(issue); !valid {
		return nil, ErrAcceptanceInvalid
	}
	if len(comments) > factory.MaxAcceptedSources {
		return nil, ErrAcceptanceSource
	}
	seen := map[string]bool{}
	for _, id := range comments {
		if _, valid := acceptancePositiveID(id); !valid || seen[id] {
			return nil, ErrAcceptanceSource
		}
		seen[id] = true
	}
	if s.Evidence == nil {
		return nil, ErrAcceptanceUnavailable
	}
	if err := s.authorize(ctx, actor, repository); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	evidence, err := s.Evidence.ReadAcceptanceEvidence(bounded, strconv.FormatInt(int64(repository), 10), issue, append([]string(nil), comments...))
	if err != nil {
		return nil, err
	}
	view, refusal := buildAcceptancePreview(int64(repository), issue, comments, evidence)
	if refusal != nil {
		return nil, refusal
	}
	if err := s.authorize(bounded, actor, repository); err != nil {
		return nil, err
	}
	return &view, nil
}

func (s Acceptances) Admit(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue string, in AcceptanceChange) (control.AcceptanceResult, error) {
	var empty control.AcceptanceResult
	if s.Writer == nil {
		return empty, ErrAcceptanceUnavailable
	}
	if _, valid := acceptancePositiveID(issue); !valid {
		return empty, ErrAcceptanceInvalid
	}
	if err := s.authorize(ctx, actor, repository); err != nil {
		return empty, err
	}
	sources, err := selectedAcceptanceSources(in.Sources)
	if err != nil {
		return empty, err
	}
	resolutions, err := selectedAcceptanceSources(in.Resolutions)
	if err != nil {
		return empty, err
	}
	var prerequisites []factory.AcceptedPrerequisite
	for _, p := range in.Prerequisites {
		repo, repoOK := acceptancePositiveID(p.EndpointRepo)
		index, indexOK := acceptancePositiveID(p.EndpointIssue)
		if !repoOK || !indexOK {
			return empty, ErrAcceptancePrerequisite
		}
		prerequisites = append(prerequisites, factory.AcceptedPrerequisite{Occurrence: p.Occurrence, DependsOn: p.DependsOn, PrereqAcceptance: p.PrereqAcceptance, EndpointRepo: repo, EndpointIssue: index, Outcome: p.Outcome})
	}
	decision := factory.Acceptance{ID: in.DecisionID, Predecessor: in.Predecessor, Repository: int64(repository), IssueIndex: issue, Approver: int64(actor), NativeRev: in.NativeRevision,
		TitleDigest: in.TitleDigest, TitleOccurrence: in.TitleOccurrence, ContentDigest: in.ContentDigest, ContentVersion: in.ContentVersion, Sources: sources, Prerequisites: prerequisites, Resolutions: resolutions}
	if decision.Validate() != nil {
		return empty, ErrAcceptanceInvalid
	}
	return s.Writer.AdmitAcceptance(ctx, decision, func(current context.Context) error { return s.authorize(current, actor, repository) })
}

func selectedAcceptanceSources(in []SelectedAcceptanceSource) ([]factory.SelectedSource, error) {
	var sources []factory.SelectedSource
	for _, selected := range in {
		source := factory.SelectedSource{ID: selected.ID, ContentVersion: selected.ContentVersion, Digest: selected.Digest}
		if source.Validate() != nil {
			return nil, ErrAcceptanceSource
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func (s Acceptances) Withdraw(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue string, in AcceptanceWithdrawal) (control.WithdrawalResult, error) {
	var empty control.WithdrawalResult
	index, valid := acceptancePositiveID(issue)
	if !valid || !project.ValidDecisionID(in.DecisionID) {
		return empty, ErrAcceptanceInvalid
	}
	if s.Writer == nil {
		return empty, ErrAcceptanceUnavailable
	}
	if err := s.authorize(ctx, actor, repository); err != nil {
		return empty, err
	}
	return s.Writer.WithdrawAcceptance(ctx, int64(repository), index, in.DecisionID, int64(actor), func(current context.Context) error { return s.authorize(current, actor, repository) })
}
