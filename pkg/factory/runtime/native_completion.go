package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

type nativeTargetTip struct {
	Repository string `json:"repository"`
	TargetRef  string `json:"target_ref"`
	Tip        string `json:"tip"`
}

func (s nativeProductAccount) targetTip(ctx context.Context, target string) (nativeTargetTip, error) {
	var tip nativeTargetTip
	if !factory.ValidTargetBranch(target) {
		return tip, errNativeReadScope
	}
	err := s.read(ctx, nativeRead{Kind: "target_tip", Repository: s.repository.ExternalID, TargetRef: target}, &tip)
	if err == nil && (tip.Repository != s.repository.ExternalID || tip.TargetRef != target || !factory.ValidCommit(tip.Tip)) {
		err = errNativeRead
	}
	return tip, err
}

// CaptureCompletionLocal brackets the existing SQL material with the selected
// account's current target tip. Recorded preparation alone is not a current tip.
func (s NativeInputSource) CaptureCompletionLocal(ctx context.Context, repository int64) (factory.CompletionLocal, error) {
	var empty factory.CompletionLocal
	authority, err := s.Store.CompletionSourceContext(ctx, repository)
	if err != nil {
		return empty, err
	}
	account, err := s.account(ctx, strconv.FormatInt(repository, 10), factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	before, err := account.targetTip(ctx, authority.TargetBranch)
	if err != nil {
		return empty, err
	}
	local, err := s.Store.CaptureCompletionLocalAtSource(ctx, repository, authority, before.Tip)
	if err != nil {
		return empty, err
	}
	after, err := account.targetTip(ctx, authority.TargetBranch)
	if err != nil {
		return empty, err
	}
	if before != after {
		return empty, errors.New(control.HoldInventoryMaterial)
	}
	return local, account.current(ctx)
}

func completionIssueEvidence(raw nativeInputSnapshot, account *nativeProductAccount) ([]byte, error) {
	issue := raw.Issue
	body := factory.CompletionIssueEvidence{Index: issue.Number, Title: issue.Title, Body: issue.Body, TitleDigest: inputDigest([]byte(issue.Title)), ContentDigest: inputDigest([]byte(issue.Body))}
	for _, dep := range raw.Dependencies {
		if dep.Repository != account.repository.ExternalID {
			return nil, errors.New(control.HoldInventoryUnavailable)
		}
		body.Dependencies = append(body.Dependencies, dep.DependsOn)
	}
	sort.Slice(body.Dependencies, func(i, j int) bool {
		a, b := body.Dependencies[i], body.Dependencies[j]
		return len(a) < len(b) || len(a) == len(b) && a < b
	})
	if err := body.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	return encoded, nil
}

// ReadCompletionIssuePage advances one non-authorizing inventory step. Control
// persists its cursor and compares two entire independent observations.
func (s NativeInputSource) ReadCompletionIssuePage(ctx context.Context, repository int64, cursor string) (result control.IssueObservationPage, err error) {
	defer func() { err = inventoryReadError(err) }()
	empty := control.IssueObservationPage{}
	account, err := s.account(ctx, strconv.FormatInt(repository, 10), factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	listed, next, complete, err := account.scanIssue(ctx, cursor, "open")
	if err != nil {
		return empty, err
	}
	page := control.IssueObservationPage{Issues: []control.NativeInventoryIssue{}, Next: next, Complete: complete}
	if listed != nil {
		raw, _, _, err := (nativeIssueInputs{account}).readInputs(ctx, strconv.FormatInt(repository, 10), strconv.FormatInt(listed.Ref.Number, 10), nil, "")
		if err != nil {
			if changedInventoryMember(err) {
				return empty, errors.New(control.HoldInventoryRevision)
			}
			return empty, err
		}
		if raw.Issue.ID != listed.Ref.ExternalID || *raw.Issue.Closed || raw.Issue.Title != listed.Title || raw.Issue.Body != listed.Body {
			return empty, errors.New(control.HoldInventoryRevision)
		}
		encoded, err := completionIssueEvidence(raw, account)
		if err != nil {
			return empty, err
		}
		page.Issues = append(page.Issues, control.NativeInventoryIssue{Index: listed.Ref.Number, Data: string(encoded)})
	}
	return page, account.current(ctx)
}
