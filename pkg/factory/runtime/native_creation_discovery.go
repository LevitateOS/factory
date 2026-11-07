package runtime

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

var _ control.CreationRecoverySource = NativeInputSource{}

// openIssues is the complete bounded membership observation shared by intake
// and completion. Provider cursors stay inside the authenticated reader.
func (s *nativeProductAccount) openIssues(ctx context.Context) ([]collaboration.Issue, error) {
	query := factoryservice.CollaborationIssueQuery{State: "open", Limit: 50}
	items := []collaboration.Issue{}
	seen := map[string]bool{}
	seenNumbers := map[int64]bool{}
	bytes := 0
	for page := 0; ; page++ {
		if page >= 256 {
			return nil, errors.New(control.HoldInventoryOversize)
		}
		observed, err := (nativeCollaborationReads{s}).ReadIssues(ctx, s.binding.Actor.ID, s.repository.ID, query)
		if err != nil {
			return nil, err
		}
		for _, issue := range observed.Items {
			if seen[issue.Ref.ExternalID] || seenNumbers[issue.Ref.Number] || issue.State != "open" {
				return nil, errNativeRead
			}
			seen[issue.Ref.ExternalID] = true
			seenNumbers[issue.Ref.Number] = true
			bytes += len(issue.Title) + len(issue.Body)
			items = append(items, issue)
			if len(items) > factory.MaxReassessmentIssues || bytes > 2<<20 {
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
	sort.Slice(items, func(i, j int) bool { return items[i].Ref.Number < items[j].Ref.Number })
	return items, nil
}

type nativeCreatorPermission struct {
	Repository string `json:"repository"`
	IssueID    string `json:"issue_id"`
	Number     int64  `json:"number"`
	Creator    string `json:"creator"`
	Write      *bool  `json:"write"`
}

func (s *nativeProductAccount) creatorPermission(ctx context.Context, number int64, creator string) (nativeCreatorPermission, error) {
	var result nativeCreatorPermission
	if !nativeIndex(number) || !nativeRemoteID(creator) {
		return result, errNativeReadScope
	}
	err := s.read(ctx, nativeRead{Kind: "issue_creator_permission", Repository: s.repository.ExternalID, Number: uint32(number), Creator: creator}, &result)
	if err == nil && (result.Repository != s.repository.ExternalID || result.Number != number || result.Creator != creator || !nativeRemoteID(result.IssueID) || result.Write == nil) {
		err = errNativeRead
	}
	return result, err
}

// ReadCreations discovers visible original issues. Positions are stable GitHub
// issue numbers, not webhook/event history or a global provider revision.
func (s NativeInputSource) ReadCreations(ctx context.Context, repository, after int64, limit int) (factory.CreationRecoveryBatch, error) {
	var empty factory.CreationRecoveryBatch
	if repository <= 0 || after < 0 || limit < 1 || limit > 16 {
		return empty, errNativeReadScope
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	policy, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return empty, err
	}
	page := factory.CreationRecoveryBatch{Repository: repository, Events: []factory.OriginalCreationEvent{}, NextPosition: after, RetentionLimited: true, ProvenanceComplete: true, ReachedRetainedEnd: true}
	if !policy.Enabled {
		return page, nil
	}
	account, err := s.account(ctx, strconv.FormatInt(repository, 10), factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	before, err := account.openIssues(ctx)
	if err != nil {
		return empty, err
	}
	visited := 0
	for _, listed := range before {
		if listed.Ref.Number <= after {
			continue
		}
		if visited == limit {
			page.ReachedRetainedEnd = false
			break
		}
		visited++
		page.NextPosition = listed.Ref.Number
		// Existing explicit acceptance owns this issue. Discovery cannot replace it.
		if _, e := s.Store.AcceptanceHead(ctx, repository, listed.Ref.Number); e == nil {
			continue
		} else if !errors.Is(e, store.ErrNotFound) {
			return empty, e
		}
		raw, revision, _, e := (nativeIssueInputs{account}).readInputs(ctx, strconv.FormatInt(repository, 10), strconv.FormatInt(listed.Ref.Number, 10), nil, policy.TargetBranch)
		if e != nil {
			return empty, e
		}
		i := raw.Issue
		if i.ID != listed.Ref.ExternalID || i.Title != listed.Title || i.Body != listed.Body || *i.Closed {
			return empty, errors.New(control.HoldInventoryRevision)
		}
		// These are confirmed ineligible for original-creation intake, not missing
		// history treated as a new objective. Explicit acceptance stays available.
		if *i.Version != 0 || i.TitleOccurrence != "original" || i.LifecycleOccurrence != "original" || *i.Locked || len(raw.Dependencies) != 0 {
			continue
		}
		permission, e := account.creatorPermission(ctx, i.Number, i.Author.ExternalID)
		if e != nil {
			return empty, e
		}
		if permission.IssueID != i.ID {
			return empty, errNativeReadScope
		}
		confirmed, fresh, _, e := (nativeIssueInputs{account}).readInputs(ctx, strconv.FormatInt(repository, 10), strconv.FormatInt(i.Number, 10), nil, policy.TargetBranch)
		if e != nil {
			return empty, e
		}
		if fresh != revision || confirmed.Issue.Author.ExternalID != i.Author.ExternalID {
			return empty, errors.New(control.HoldInventoryRevision)
		}
		creator, e := s.Store.ObserveCollaborationActor(ctx, account.binding.Host.ID, i.Author.ExternalID, i.Author.Login)
		if e != nil {
			return empty, e
		}
		id, e := strconv.ParseInt(i.ID, 10, 64)
		if e != nil {
			return empty, errNativeRead
		}
		page.Events = append(page.Events, factory.OriginalCreationEvent{Position: i.Number, Delivery: "github-original-issue:" + i.ID, Repository: repository, Issue: i.Number, OriginalIssueID: id, Creator: int64(creator.ID), CreatorWrite: *permission.Write, CreatedUnix: i.CreatedAt.Unix(), TitleDigest: inputDigest([]byte(i.Title)), ContentDigest: inputDigest([]byte(i.Body)), Verified: true})
	}
	confirm, err := account.openIssues(ctx)
	if err != nil {
		return empty, err
	}
	// Observation IDs are local read metadata; compare only provider issue facts.
	for index := range before {
		before[index].Observation = collaboration.Observation{}
	}
	for index := range confirm {
		confirm[index].Observation = collaboration.Observation{}
	}
	if !reflect.DeepEqual(before, confirm) {
		return empty, errors.New(control.HoldInventoryRevision)
	}
	current, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return empty, err
	}
	if current.Revision != policy.Revision {
		return empty, errNativeReadScope
	}
	return page, account.current(ctx)
}

func (s NativeInputSource) CheckInitialCreator(ctx context.Context, hint control.IntakeHint) error {
	policy, err := s.Store.RepositoryPolicy(ctx, hint.Repository)
	if err != nil {
		return err
	}
	if !policy.Enabled {
		return &control.AcceptanceRefusal{Reason: control.RefusalCreationUnauthorized}
	}
	account, err := s.account(ctx, strconv.FormatInt(hint.Repository, 10), factory.OpRefPublish)
	if err != nil {
		return err
	}
	creator, err := s.Store.CollaborationActor(ctx, collaboration.ActorID(hint.Creator))
	if err != nil {
		return err
	}
	if creator.Host != account.binding.Host.ID {
		return errNativeReadScope
	}
	permission, err := account.creatorPermission(ctx, hint.Issue, creator.ExternalID)
	if err != nil {
		return err
	}
	if permission.IssueID != strconv.FormatInt(hint.NativeIssueID, 10) || !*permission.Write {
		return &control.AcceptanceRefusal{Reason: control.RefusalCreationUnauthorized}
	}
	current, err := s.Store.RepositoryPolicy(ctx, hint.Repository)
	if err != nil {
		return err
	}
	if !current.Enabled || current.Revision != policy.Revision {
		return &control.AcceptanceRefusal{Reason: control.RefusalCreationUnauthorized}
	}
	return account.current(ctx)
}
