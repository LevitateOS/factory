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
	"github.com/levitateos/factory/pkg/store"
)

var _ control.CreationRecoverySource = NativeInputSource{}

type nativeCreationMember struct {
	ID          string
	Number      int64
	Title, Body string
}
type nativeCreationScan struct {
	Version    int
	Policy     int64
	Connection NativeConnection
	Repository string
	Cursor     string
	Members    []nativeCreationMember
	Confirming bool
	Confirmed  int
	Ready      bool
}

func creationMember(issue *collaboration.Issue) nativeCreationMember {
	return nativeCreationMember{ID: issue.Ref.ExternalID, Number: issue.Ref.Number, Title: inputDigest([]byte(issue.Title)), Body: inputDigest([]byte(issue.Body))}
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

// ReadCreations discovers visible original issues. Positions are stable provider
// issue numbers, not webhook/event history or a global provider revision.
func (s NativeInputSource) ReadCreations(ctx context.Context, repository, after int64, limit int) (result factory.CreationRecoveryBatch, err error) {
	defer func() { err = inventoryReadError(err) }()
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
	if !policy.Enabled || policy.Paused {
		return page, nil
	}
	account, err := s.account(ctx, strconv.FormatInt(repository, 10), factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	expected, err := s.Store.CreationRecoveryScanCursor(ctx, repository)
	if err != nil {
		return empty, err
	}
	fresh := func() nativeCreationScan {
		return nativeCreationScan{Version: 1, Policy: policy.Revision, Connection: account.identity.Connection, Repository: account.repository.ExternalID}
	}
	scan := fresh()
	if expected != "" {
		if json.Unmarshal([]byte(expected), &scan) != nil || scan.Version != 1 {
			return empty, errNativeReadScope
		}
		if scan.Policy != policy.Revision || scan.Connection != account.identity.Connection || scan.Repository != account.repository.ExternalID {
			scan = fresh()
		}
	}
	if len(scan.Members) > factory.MaxReassessmentIssues || scan.Confirmed < 0 || scan.Confirmed > len(scan.Members) {
		return empty, errNativeReadScope
	}
	page.ExpectedSourceCursor = expected
	page.ReachedRetainedEnd = false
	// Corroborate membership once across retained page steps before advancing the
	// numeric intake cursor. Mutable page-number boundaries cannot skip a member.
	for steps := 0; !scan.Ready && steps < 50; steps++ {
		listed, next, complete, e := account.scanIssue(ctx, scan.Cursor, "open")
		if e != nil {
			if e.Error() != control.HoldInventoryRevision {
				return empty, e
			}
			scan = fresh()
			break
		}
		if listed != nil {
			member := creationMember(listed)
			if scan.Confirming {
				if scan.Confirmed >= len(scan.Members) || scan.Members[scan.Confirmed] != member {
					scan = fresh()
					break
				}
				scan.Confirmed++
			} else {
				scan.Members = append(scan.Members, member)
				if len(scan.Members) > factory.MaxReassessmentIssues {
					return empty, errors.New(control.HoldInventoryOversize)
				}
			}
		}
		scan.Cursor = next
		if complete {
			if !scan.Confirming {
				scan.Confirming = true
			} else {
				if scan.Confirmed != len(scan.Members) {
					scan = fresh()
					break
				}
				sort.Slice(scan.Members, func(i, j int) bool { return scan.Members[i].Number < scan.Members[j].Number })
				scan.Ready = true
			}
		}
	}
	var listed *nativeCreationMember
	if scan.Ready {
		for i := range scan.Members {
			if scan.Members[i].Number > after {
				listed = &scan.Members[i]
				break
			}
		}
		page.ReachedRetainedEnd = listed == nil
	}
	for _, listed := range []*nativeCreationMember{listed} {
		if listed == nil || listed.Number <= after {
			continue
		}
		page.NextPosition = listed.Number
		// Existing explicit acceptance owns this issue. Discovery cannot replace it.
		if _, e := s.Store.AcceptanceHead(ctx, repository, listed.Number); e == nil {
			continue
		} else if !errors.Is(e, store.ErrNotFound) {
			return empty, e
		}
		raw, revision, _, e := (nativeIssueInputs{account}).readInputs(ctx, strconv.FormatInt(repository, 10), strconv.FormatInt(listed.Number, 10), nil, policy.TargetBranch)
		if e != nil {
			if changedInventoryMember(e) {
				page.NextPosition = after
				page.ReachedRetainedEnd = false
				scan = fresh()
				break
			}
			return empty, e
		}
		i := raw.Issue
		if i.ID != listed.ID || inputDigest([]byte(i.Title)) != listed.Title || inputDigest([]byte(i.Body)) != listed.Body || *i.Closed {
			page.NextPosition = after
			page.ReachedRetainedEnd = false
			scan = fresh()
			break
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
		delivery := "github-original-issue:" + i.ID
		if account.binding.Host.Provider == collaboration.Fountain {
			// Native issue IDs are local to one Fountain host. Preserve GitHub's
			// existing global IDs while keeping separate hosts' deliveries distinct.
			delivery = "fountain-original-issue:" + string(account.binding.Host.ID) + ":" + i.ID
		}
		page.Events = append(page.Events, factory.OriginalCreationEvent{Position: i.Number, Delivery: delivery, Repository: repository, Issue: i.Number, OriginalIssueID: id, Creator: int64(creator.ID), CreatorWrite: *permission.Write, CreatedUnix: i.CreatedAt.Unix(), TitleDigest: inputDigest([]byte(i.Title)), ContentDigest: inputDigest([]byte(i.Body)), Verified: true})
	}
	scanData := ""
	if !page.ReachedRetainedEnd && (len(scan.Members) > 0 || scan.Cursor != "" || scan.Confirming) {
		raw, e := json.Marshal(scan)
		if e != nil {
			return empty, e
		}
		scanData = string(raw)
		if len(scanData) > 3<<20 {
			return empty, errors.New(control.HoldInventoryOversize)
		}
	}
	page.SourceCursor = &scanData
	current, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return empty, err
	}
	if !current.Enabled || current.Paused || current.Revision != policy.Revision {
		return empty, errNativeReadScope
	}
	if err := account.currentBackground(ctx, factory.OpRefPublish); err != nil {
		return empty, err
	}
	return page, nil
}

func (s NativeInputSource) CheckInitialCreator(ctx context.Context, hint control.IntakeHint) error {
	policy, err := s.Store.RepositoryPolicy(ctx, hint.Repository)
	if err != nil {
		return err
	}
	if !policy.Enabled || policy.Paused {
		return store.ErrAdmissionChanged
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
	if permission.IssueID != strconv.FormatInt(hint.NativeIssueID, 10) {
		return errNativeReadScope
	}
	if !*permission.Write {
		return store.ErrAdmissionChanged
	}
	current, err := s.Store.RepositoryPolicy(ctx, hint.Repository)
	if err != nil {
		return err
	}
	if !current.Enabled || current.Paused || current.Revision != policy.Revision {
		return store.ErrAdmissionChanged
	}
	return account.currentBackground(ctx, factory.OpRefPublish)
}
