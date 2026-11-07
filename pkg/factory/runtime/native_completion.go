package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
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

// ReadCompletionIssues returns one complete bounded observation, never an idle
// provider claim. Control compares two independent observations before sealing.
func (s NativeInputSource) ReadCompletionIssues(ctx context.Context, repository int64) ([]control.NativeInventoryIssue, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	account, err := s.account(ctx, strconv.FormatInt(repository, 10), factory.OpRefPublish)
	if err != nil {
		return nil, err
	}
	reads := nativeCollaborationReads{account}
	query := factoryservice.CollaborationIssueQuery{State: "open", Limit: 50}
	items := []collaboration.Issue{}
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page >= 256 {
			return nil, errors.New(control.HoldInventoryOversize)
		}
		observed, err := reads.ReadIssues(ctx, account.binding.Actor.ID, account.repository.ID, query)
		if err != nil {
			return nil, err
		}
		for _, issue := range observed.Items {
			if seen[issue.Ref.ExternalID] || issue.State != "open" {
				return nil, errNativeRead
			}
			seen[issue.Ref.ExternalID] = true
			items = append(items, issue)
			if len(items) > factory.MaxReassessmentIssues {
				return nil, errors.New(control.HoldInventoryOversize)
			}
		}
		if observed.Complete {
			if observed.Next != "" {
				return nil, errNativeRead
			}
			break
		}
		if observed.Next == "" || observed.Next == query.Cursor {
			return nil, errNativeRead
		}
		query.Cursor = observed.Next
	}
	result := make([]control.NativeInventoryIssue, 0, len(items))
	bytes := 0
	for _, listed := range items {
		raw, _, _, err := (nativeIssueInputs{account}).readInputs(ctx, strconv.FormatInt(repository, 10), strconv.FormatInt(listed.Ref.Number, 10), nil, "")
		if err != nil {
			return nil, err
		}
		issue := raw.Issue
		if issue.ID != listed.Ref.ExternalID || *issue.Closed {
			return nil, errors.New(control.HoldInventoryRevision)
		}
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
		bytes += len(encoded)
		if bytes > factory.MaxReassessmentEvidenceBytes {
			return nil, errors.New(control.HoldInventoryOversize)
		}
		result = append(result, control.NativeInventoryIssue{Index: body.Index, Data: string(encoded)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, account.current(ctx)
}
