package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
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
	if account.binding.Host.Provider == collaboration.GitHub {
		return s.readCompletionBatch(ctx, account, repository, cursor)
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

// The provider's opaque continuation stays bound to the exact captured account,
// repository and target. It carries read progress, never execution permission.
type nativeCompletionCursor struct {
	Version    int
	Connection NativeConnection
	Repository string
	Target     string
	Tip        string
	After      string
	Pages      int
	IDs        []string
}

func (s NativeInputSource) readCompletionBatch(ctx context.Context, account *nativeProductAccount, repository int64, cursor string) (control.IssueObservationPage, error) {
	empty := control.IssueObservationPage{}
	policy, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return empty, err
	}
	if !factory.ValidTargetBranch(policy.TargetBranch) {
		return empty, errNativeReadScope
	}
	state := nativeCompletionCursor{Version: 1, Connection: account.identity.Connection, Repository: account.repository.ExternalID, Target: policy.TargetBranch}
	if cursor != "" {
		if len(cursor) > 128<<10 || nativeDecode([]byte(cursor), &state) != nil || state.Version != 1 || state.Connection != account.identity.Connection || state.Repository != account.repository.ExternalID || state.Target != policy.TargetBranch || state.Pages < 1 || state.Pages >= 256 || len(state.IDs) > factory.MaxReassessmentIssues || len(state.After) > 4096 || state.After == "" {
			return empty, errors.New(control.HoldInventoryRevision)
		}
	}
	var raw struct {
		Repository string                `json:"repository"`
		Target     string                `json:"target_ref"`
		Tip        string                `json:"tip"`
		Items      []nativeInputSnapshot `json:"items"`
		Next       string                `json:"next_cursor"`
		Complete   bool                  `json:"complete"`
	}
	err = account.read(ctx, nativeRead{Kind: "completion_issues", Repository: state.Repository, TargetRef: state.Target, Cursor: state.After, Limit: 50}, &raw)
	if err != nil {
		if changedInventoryMember(err) {
			return empty, errors.New(control.HoldInventoryRevision)
		}
		return empty, err
	}
	if state.Tip != "" && raw.Tip != state.Tip {
		return empty, errors.New(control.HoldInventoryRevision)
	}
	if raw.Repository != state.Repository || raw.Target != state.Target || !factory.ValidCommit(raw.Tip) || raw.Items == nil || len(raw.Items) > 50 || raw.Complete != (raw.Next == "") || !raw.Complete && (raw.Next == state.After || len(raw.Items) == 0) || len(raw.Next) > 4096 {
		return empty, errNativeRead
	}
	page := control.IssueObservationPage{Issues: []control.NativeInventoryIssue{}, Complete: raw.Complete}
	seen := map[string]bool{}
	for _, id := range state.IDs {
		seen[id] = true
	}
	for _, item := range raw.Items {
		if item.Tip != raw.Tip || item.Issue.Closed == nil || *item.Issue.Closed || seen[item.Issue.ID] {
			return empty, errors.New(control.HoldInventoryRevision)
		}
		validated, _, _, err := (nativeIssueInputs{account}).acceptInputs(ctx, item, repository, item.Issue.Number, nil, state.Target)
		if err != nil {
			return empty, err
		}
		evidence, err := completionIssueEvidence(validated, account)
		if err != nil {
			return empty, err
		}
		page.Issues = append(page.Issues, control.NativeInventoryIssue{Index: item.Issue.Number, Data: string(evidence)})
		seen[item.Issue.ID] = true
		state.IDs = append(state.IDs, item.Issue.ID)
	}
	if len(state.IDs) > factory.MaxReassessmentIssues {
		return empty, errors.New(control.HoldInventoryOversize)
	}
	if !raw.Complete {
		state.After = raw.Next
		state.Tip = raw.Tip
		state.Pages++
		encoded, err := json.Marshal(state)
		if err != nil || len(encoded) > 128<<10 {
			return empty, errors.New(control.HoldInventoryOversize)
		}
		page.Next = string(encoded)
	}
	return page, account.current(ctx)
}
