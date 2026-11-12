package runtime

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

var _ control.CheckObserver = NativeInputSource{}

type nativeCheckSnapshot struct {
	Repository       string                            `json:"repository"`
	Number           int64                             `json:"number"`
	PullID           string                            `json:"pull_id"`
	IssueID          string                            `json:"issue_id"`
	HeadRepository   *collaboration.ObservedRepository `json:"head_repository,omitempty"`
	HeadRef          string                            `json:"head_ref"`
	BaseRef          string                            `json:"base_ref"`
	EvaluationCommit string                            `json:"evaluation_commit"`
	HeadTip          string                            `json:"head_tip"`
	BaseTip          string                            `json:"base_tip"`
	Checks           []factory.ObservedCheck           `json:"checks"`
	Sources          []json.RawMessage                 `json:"sources"`
}

// ObserveChecks obtains current evidence using the policy's exact merge actor.
// Rust binds provider identities, tips and complete latest status/check sets;
// Go records the observation and applies the existing required-check verdict.
func (p NativeInputSource) ObserveChecks(ctx context.Context, target factory.CheckTarget, actorID int64) (factory.ObservedChecks, error) {
	var empty factory.ObservedChecks
	if target.Validate() != nil || !nativeIndex(target.PRNumber) || actorID <= 0 {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	account, err := p.account(ctx, strconv.FormatInt(target.Repository, 10), factory.OpMerge)
	if err != nil {
		return empty, err
	}
	if int64(account.binding.Actor.ID) != actorID {
		return empty, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	var raw nativeCheckSnapshot
	if err = account.read(ctx, nativeRead{Kind: "automation_checks", Repository: account.repository.ExternalID, Number: uint32(target.PRNumber)}, &raw); err != nil {
		return empty, err
	}
	return p.observeCheckSnapshot(ctx, target, account, raw)
}

func (p NativeInputSource) observeCheckSnapshot(ctx context.Context, target factory.CheckTarget, account *nativeProductAccount, raw nativeCheckSnapshot) (factory.ObservedChecks, error) {
	var empty factory.ObservedChecks
	if raw.Repository != account.repository.ExternalID || raw.Number != target.PRNumber || raw.PullID != strconv.FormatInt(target.PRID, 10) || raw.IssueID != strconv.FormatInt(target.IssueID, 10) || raw.HeadRef != target.HeadRef || raw.BaseRef != target.BaseRef || !factory.ValidCommit(raw.EvaluationCommit) || raw.Checks == nil || raw.Sources == nil || len(raw.Sources) > 200 {
		return empty, &factory.PublicationRefusal{Reason: "pr_mismatch"}
	}
	if raw.HeadRepository != nil {
		if _, err := account.mapRead(ctx, raw.Repository, account.repository.Name, nil, []collaboration.ObservedRepository{*raw.HeadRepository}); err != nil {
			return empty, err
		}
	} else if account.binding.Host.Provider == collaboration.GitHub {
		return empty, errNativeRead
	}
	observed := factory.ObservedChecks{NativeRev: 1, EvaluationCommit: raw.EvaluationCommit, Checks: raw.Checks, ObservedContexts: len(raw.Checks), HeadTip: raw.HeadTip, BaseTip: raw.BaseTip, Complete: true}
	if observed.Validate() != nil {
		return empty, errNativeRead
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return empty, errNativeRead
	}
	observed.NativeRev, err = p.Store.ObserveNativeChecks(ctx, target.Repository, target.PRNumber, inputDigest(encoded))
	if err != nil {
		return empty, err
	}
	if err := account.currentBackground(ctx, factory.OpMerge); err != nil {
		return empty, err
	}
	return observed, nil
}
