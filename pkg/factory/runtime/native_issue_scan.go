package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// One retained source page and its remaining members. This is observation
// progress, not authority: every step acquires the current account again.
type nativeIssueScan struct {
	Version    int
	Connection NativeConnection
	Repository string
	State      string
	Pending    []collaboration.Issue
	Next       string
	Complete   bool
	Pages      int
	Numbers    []int64
	Seen       []string
	Bytes      int
}

func inventoryReadError(err error) error {
	var failure *NativePortError
	if errors.As(err, &failure) && failure.Code == "native_operation_budget" {
		return control.ErrReadinessPassPending
	}
	if errors.As(err, &failure) && failure.Status == 413 {
		return errors.New(control.HoldInventoryOversize)
	}
	return err
}

func changedInventoryMember(err error) bool {
	var failure *NativePortError
	return errors.As(err, &failure) && (failure.Status == 404 || failure.Status == 409)
}

func (s *nativeProductAccount) scanIssue(ctx context.Context, cursor, state string) (*collaboration.Issue, string, bool, error) {
	scan := nativeIssueScan{Version: 2, Connection: s.identity.Connection, Repository: s.repository.ExternalID, State: state}
	if cursor != "" {
		if len(cursor) > 3<<20 || json.Unmarshal([]byte(cursor), &scan) != nil || scan.Version != 2 || scan.Connection != s.identity.Connection || scan.Repository != s.repository.ExternalID || scan.State != state || len(scan.Pending) > 50 || scan.Pages < 1 || scan.Pages > 256 || len(scan.Numbers) != len(scan.Seen) || len(scan.Seen) > factory.MaxReassessmentIssues || scan.Bytes < 0 || scan.Bytes > 2<<20 {
			return nil, "", false, errors.New(control.HoldInventoryRevision)
		}
	}
	if len(scan.Pending) == 0 && !scan.Complete {
		if scan.Pages >= 256 {
			return nil, "", false, errors.New(control.HoldInventoryOversize)
		}
		previous := scan.Next
		page, err := (nativeCollaborationReads{s}).ReadIssues(ctx, s.binding.Actor.ID, s.repository.ID, factoryservice.CollaborationIssueQuery{State: state, Limit: 50, Cursor: previous})
		if err != nil {
			return nil, "", false, err
		}
		scan.Pages++
		scan.Next, scan.Complete = page.Next, page.Complete
		if page.Complete != (page.Next == "") || !page.Complete && page.Next == previous {
			return nil, "", false, errNativeRead
		}
		seen := map[string]bool{}
		for _, id := range scan.Seen {
			seen[id] = true
		}
		numbers := map[int64]bool{}
		for _, n := range scan.Numbers {
			numbers[n] = true
		}
		for _, issue := range page.Items {
			if issue.Ref.Number <= 0 || numbers[issue.Ref.Number] || seen[issue.Ref.ExternalID] {
				return nil, "", false, errors.New(control.HoldInventoryRevision)
			}
			numbers[issue.Ref.Number] = true
			scan.Numbers = append(scan.Numbers, issue.Ref.Number)
			seen[issue.Ref.ExternalID] = true
			scan.Seen = append(scan.Seen, issue.Ref.ExternalID)
			scan.Bytes += len(issue.Title) + len(issue.Body)
			if len(scan.Seen) > factory.MaxReassessmentIssues || scan.Bytes > 2<<20 {
				return nil, "", false, errors.New(control.HoldInventoryOversize)
			}
		}
		scan.Pending = page.Items
	}
	var issue *collaboration.Issue
	if len(scan.Pending) > 0 {
		item := scan.Pending[0]
		issue = &item
		scan.Pending = scan.Pending[1:]
	}
	complete := scan.Complete && len(scan.Pending) == 0
	if complete {
		return issue, "", true, s.current(ctx)
	}
	raw, err := json.Marshal(scan)
	if err != nil || len(raw) > 3<<20 {
		return nil, "", false, errors.New(control.HoldInventoryOversize)
	}
	return issue, string(raw), false, s.current(ctx)
}
