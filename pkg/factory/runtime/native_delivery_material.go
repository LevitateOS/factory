package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

var errNativeDeliveryMaterial = &factory.PublicationWait{Reason: "native_delivery_material_unavailable"}

type nativeReviewOpinions struct {
	Repository string  `json:"repository"`
	PullID     string  `json:"pull_id"`
	Number     int64   `json:"number"`
	Head       string  `json:"head"`
	Base       string  `json:"base"`
	Decision   *string `json:"decision"`
	Items      []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"items"`
}

// Both delivery material and automatic merge admission consume the same native
// current-writer projection. Keep these reads outside the bounded mutation RPC.
func (s nativeCollaborationReads) readReviewOpinions(ctx context.Context, pull nativeSubjectRef, head, base string) (nativeReviewOpinions, error) {
	var opinions nativeReviewOpinions
	if err := s.read(ctx, nativeRead{Kind: "delivery_opinions", Repository: s.repository.ExternalID, Number: uint32(pull.Number)}, &opinions); err != nil {
		return opinions, err
	}
	if opinions.Repository != s.repository.ExternalID || opinions.PullID != pull.ExternalID || opinions.Number != pull.Number || opinions.Head != head || opinions.Base != base || opinions.Items == nil || len(opinions.Items) > 256 {
		return opinions, errNativeRead
	}
	seen := map[string]bool{}
	for _, opinion := range opinions.Items {
		if !nativeRemoteID(opinion.ID) || seen[opinion.ID] {
			return opinions, errNativeRead
		}
		seen[opinion.ID] = true
		switch opinion.State {
		case "approved", "changes_requested", "commented", "dismissed":
		default:
			return opinions, errNativeRead
		}
	}
	return opinions, nil
}

// ReadDeliveryMaterial reuses the authenticated connector's complete pages.
// Matching traversals are bounded observations, not an atomic GitHub snapshot.
// Acceptance and provider merge admission are checked on both sides; merge still
// requires its own current checks and exact-head operation admission. The
// aggregate deadline covers both traversals; individual connector calls retain
// their existing request/credential-lease deadlines.
func (p NativeMerges) ReadDeliveryMaterial(ctx context.Context, w factory.MergeWork) (factory.DeliveryNativeMaterial, error) {
	var empty factory.DeliveryNativeMaterial
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if w.ValidateTarget() != nil || !nativeIndex(w.PRNumber) {
		return empty, errNativeDeliveryMaterial
	}
	policy, err := p.Store.RepositoryPolicy(bounded, w.Repository)
	if err != nil {
		return empty, err
	}
	beforeInput, err := p.ReadAcceptanceEvidence(bounded, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "accepted input", err)
	}
	account, err := p.account(bounded, strconv.FormatInt(w.Repository, 10), factory.OpMerge)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "account binding", err)
	}
	if int64(account.binding.Actor.ID) != w.ActorID || policy.Merge.NativeConnection != account.binding.Connection.ID || policy.Merge.ActorID != w.ActorID {
		return empty, errNativeDeliveryMaterial
	}
	before, err := p.observeProviderMerge(bounded, w, account)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "provider readiness", err)
	}
	first, err := nativeDeliveryDiscussion(bounded, nativeCollaborationReads{account}, w)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "first discussion traversal", err)
	}
	second, err := nativeDeliveryDiscussion(bounded, nativeCollaborationReads{account}, w)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "second discussion traversal", err)
	}
	if !reflect.DeepEqual(first, second) {
		return empty, errNativeDeliveryMaterial
	}
	after, err := p.observeProviderMerge(bounded, w, account)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "provider recheck", err)
	}
	if before.ProviderComplete != after.ProviderComplete || before.ProviderEligible != after.ProviderEligible || before.MethodAvailable != after.MethodAvailable {
		return empty, errNativeDeliveryMaterial
	}
	if err := account.current(bounded); err != nil {
		return empty, err
	}
	afterInput, err := p.ReadAcceptanceEvidence(bounded, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "accepted input recheck", err)
	}
	if beforeInput.Revision != afterInput.Revision {
		return empty, errNativeDeliveryMaterial
	}
	currentPolicy, err := p.Store.RepositoryPolicy(bounded, w.Repository)
	if err != nil {
		return empty, err
	}
	if currentPolicy.Revision != policy.Revision {
		return empty, errNativeDeliveryMaterial
	}
	first.Revision = afterInput.Revision
	first.PolicyRevision = policy.Revision
	first.Eligible = after.ProviderComplete && after.ProviderEligible && after.MethodAvailable
	if first.ReviewDecision != nil && (*first.ReviewDecision == "CHANGES_REQUESTED" || *first.ReviewDecision == "REVIEW_REQUIRED") {
		first.Eligible = false
	}
	if !first.Eligible {
		first.Blocker = "native_merge_requirements_unavailable"
		if after.ProviderComplete && after.MethodAvailable && first.ReviewDecision != nil {
			switch *first.ReviewDecision {
			case "CHANGES_REQUESTED":
				first.Blocker = "requested_changes"
			case "REVIEW_REQUIRED":
				first.Blocker = "approvals"
			}
		}
	}
	return first, nil
}

// The aggregate bound includes catalogue entries and every nested member. A
// truncated page, repeated identity/cursor or oversized traversal supplies no
// partial material to the judge.
func nativeDeliveryPages[T any](ctx context.Context, remaining *int, load func(context.Context, string) (collaboration.Page[T], error), identity func(T) string) ([]T, error) {
	items := []T{}
	seen, cursors := map[string]bool{}, map[string]bool{}
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := load(ctx, cursor)
		if err != nil {
			return nil, err
		}
		if page.Observation.State != collaboration.ReadAvailable || page.Items == nil || page.Complete != (page.Next == "") || len(page.Items) > *remaining {
			return nil, errNativeDeliveryMaterial
		}
		*remaining -= len(page.Items)
		for _, item := range page.Items {
			id := identity(item)
			if id == "" || seen[id] {
				return nil, errNativeDeliveryMaterial
			}
			seen[id] = true
			items = append(items, item)
		}
		if page.Complete {
			return items, nil
		}
		if len(page.Items) == 0 || page.Next == cursor || cursors[page.Next] {
			return nil, errNativeDeliveryMaterial
		}
		cursors[page.Next], cursor = true, page.Next
	}
}

func nativeDeliveryActor(actor *collaboration.ActorRef) string {
	if actor == nil {
		return ""
	}
	return strconv.FormatInt(int64(actor.ID), 10)
}

func nativeDeliveryDiscussion(ctx context.Context, reads nativeCollaborationReads, w factory.MergeWork) (factory.DeliveryNativeMaterial, error) {
	material := factory.DeliveryNativeMaterial{Repository: w.Repository, PRID: w.PRID, IssueID: w.IssueID, Head: w.HeadOID, Base: w.BaseOID, Comments: []factory.DeliveryComment{}, Reviews: []factory.DeliveryNativeReview{}, Threads: []factory.DeliveryThread{}, BlockingReviewIDs: []string{}}
	actor, repo := reads.binding.Actor.ID, reads.repository.ID
	pulls, err := reads.ReadPulls(ctx, actor, repo, factoryservice.CollaborationPullQuery{Number: w.PRNumber, Limit: 1})
	if err != nil {
		return material, nativeDeliveryReadFailure(ctx, "pull", err)
	}
	if len(pulls.Items) != 1 || !pulls.Complete {
		return material, errNativeDeliveryMaterial
	}
	pull := pulls.Items[0]
	if pull.Ref.ExternalID != strconv.FormatInt(w.PRID, 10) || pull.State != "open" || pull.Draft == nil || *pull.Draft || pull.Head.Commit == nil || pull.Base.Commit == nil || string(*pull.Head.Commit) != w.HeadOID || string(*pull.Base.Commit) != w.BaseOID || pull.Head.Name != strings.TrimPrefix(w.HeadRef, "refs/heads/") || pull.Base.Name != strings.TrimPrefix(w.BaseRef, "refs/heads/") || nativeDeliveryActor(pull.Author) != strconv.FormatInt(w.PRAuthorID, 10) {
		return material, errNativeDeliveryMaterial
	}
	material.Title, material.Body = pull.Title, pull.Body
	remaining := 256
	comments, err := nativeDeliveryPages(ctx, &remaining, func(ctx context.Context, cursor string) (collaboration.Page[collaboration.PullComment], error) {
		return reads.ReadPullComments(ctx, actor, repo, w.PRNumber, factoryservice.DiscussionQuery{Cursor: cursor, Limit: 50})
	}, func(c collaboration.PullComment) string { return c.Comment.ExternalID })
	if err != nil {
		return material, nativeDeliveryReadFailure(ctx, "comments", err)
	}
	for _, c := range comments {
		if c.Pull.ExternalID != pull.Ref.ExternalID {
			return material, errNativeDeliveryMaterial
		}
		material.Comments = append(material.Comments, factory.DeliveryComment{ID: c.Comment.ExternalID, PosterID: nativeDeliveryActor(c.Comment.Author), Type: "pull_comment", Content: c.Comment.Body, Digest: inputDigest([]byte(c.Comment.Body)), UpdatedAt: c.Comment.UpdatedAt})
	}
	reviews, err := nativeDeliveryPages(ctx, &remaining, func(ctx context.Context, cursor string) (collaboration.Page[collaboration.Review], error) {
		return reads.ReadReviews(ctx, actor, repo, w.PRNumber, factoryservice.ReviewQuery{Cursor: cursor, Limit: 50})
	}, func(r collaboration.Review) string { return r.ExternalID })
	if err != nil {
		return material, nativeDeliveryReadFailure(ctx, "reviews", err)
	}
	for _, r := range reviews {
		if r.Pull.ExternalID != pull.Ref.ExternalID || r.State == "unknown" {
			return material, errNativeDeliveryMaterial
		}
		material.Reviews = append(material.Reviews, factory.DeliveryNativeReview{ID: r.ExternalID, ActorID: nativeDeliveryActor(r.Author), Type: r.State, Commit: string(r.Commit), Content: r.Body, Digest: inputDigest([]byte(r.Body)), Official: r.Official, Stale: r.Stale, Dismissed: r.State == "dismissed"})
	}
	opinions, err := reads.readReviewOpinions(ctx, nativeSubjectRef{Kind: "pull", ExternalID: pull.Ref.ExternalID, Number: w.PRNumber}, w.HeadOID, w.BaseOID)
	if err != nil {
		return material, nativeDeliveryReadFailure(ctx, "current review opinions", err)
	}
	material.ReviewDecision = opinions.Decision
	for _, opinion := range opinions.Items {
		matched := false
		for _, review := range material.Reviews {
			if review.ID == opinion.ID && review.Type == opinion.State {
				matched = true
				break
			}
		}
		if !matched {
			return material, errNativeDeliveryMaterial
		}
		if opinions.Decision != nil && *opinions.Decision == "CHANGES_REQUESTED" && opinion.State == "changes_requested" {
			material.BlockingReviewIDs = append(material.BlockingReviewIDs, opinion.ID)
		}
	}
	if opinions.Decision != nil && *opinions.Decision == "CHANGES_REQUESTED" && len(material.BlockingReviewIDs) == 0 {
		return material, errNativeDeliveryMaterial
	}
	sort.Strings(material.BlockingReviewIDs)
	threads, err := nativeDeliveryPages(ctx, &remaining, func(ctx context.Context, cursor string) (collaboration.Page[collaboration.ReviewThread], error) {
		return reads.ReadThreads(ctx, actor, repo, w.PRNumber, factoryservice.ThreadQuery{Cursor: cursor, Limit: 20})
	}, func(t collaboration.ReviewThread) string { return t.ExternalID })
	if err != nil {
		return material, nativeDeliveryReadFailure(ctx, "threads", err)
	}
	for _, thread := range threads {
		if thread.Pull.ExternalID != pull.Ref.ExternalID || (thread.Resolution != "resolved" && thread.Resolution != "unresolved") {
			return material, errNativeDeliveryMaterial
		}
		members, err := nativeDeliveryPages(ctx, &remaining, func(ctx context.Context, cursor string) (collaboration.Page[collaboration.ReviewComment], error) {
			if cursor == "" {
				return thread.Comments, nil
			}
			page, err := reads.ReadThreads(ctx, actor, repo, w.PRNumber, factoryservice.ThreadQuery{ThreadID: thread.ExternalID, Cursor: cursor, Limit: 10})
			if err != nil {
				return collaboration.Page[collaboration.ReviewComment]{}, err
			}
			if len(page.Items) != 1 || page.Items[0].Resolution != thread.Resolution || !reflect.DeepEqual(page.Items[0].RecordedLocation, thread.RecordedLocation) || !reflect.DeepEqual(page.Items[0].Outdated, thread.Outdated) {
				return collaboration.Page[collaboration.ReviewComment]{}, errNativeDeliveryMaterial
			}
			return page.Items[0].Comments, nil
		}, func(c collaboration.ReviewComment) string { return c.Comment.ExternalID })
		if err != nil {
			return material, nativeDeliveryReadFailure(ctx, "thread members", err)
		}
		if len(members) == 0 {
			return material, errNativeDeliveryMaterial
		}
		t := factory.DeliveryThread{ProviderID: thread.ExternalID, Outdated: thread.Outdated, RootID: members[0].Comment.ExternalID, HeadCommit: w.HeadOID, Resolved: thread.Resolution == "resolved", Members: []factory.DeliveryThreadMember{}}
		if location := thread.RecordedLocation; location != nil {
			t.Path, t.Side, t.Line = location.Path, location.Side, location.Line
			if location.Commit != nil {
				t.OriginalCommit = string(*location.Commit)
			}
		}
		if members[0].ReviewID != nil {
			t.ReviewID = *members[0].ReviewID
		}
		bodies := []factory.DeliveryComment{}
		for _, member := range members {
			c := factory.DeliveryComment{ID: member.Comment.ExternalID, PosterID: nativeDeliveryActor(member.Comment.Author), Type: "review_comment", Content: member.Comment.Body, Digest: inputDigest([]byte(member.Comment.Body)), UpdatedAt: member.Comment.UpdatedAt}
			if member.ReviewID != nil {
				c.ReviewID = *member.ReviewID
			}
			bodies = append(bodies, c)
			t.Members = append(t.Members, factory.DeliveryThreadMember{ID: c.ID})
		}
		// GitHub resolves threads, not individual comments, and supplies no edit
		// counter or original base anchor. Keep those unknown; their absence may
		// not authorize the legacy per-comment resolution operation.
		raw, err := json.Marshal(struct {
			Thread factory.DeliveryThread
			Bodies []factory.DeliveryComment
		}{t, bodies})
		if err != nil {
			return material, err
		}
		t.Digest = inputDigest(raw)
		material.Comments = append(material.Comments, bodies...)
		material.Threads = append(material.Threads, t)
	}
	raw, err := json.Marshal(material)
	if err != nil || len(raw) > 256*1024 {
		return material, errNativeDeliveryMaterial
	}
	return material, nil
}

func nativeDeliveryReadFailure(ctx context.Context, stage string, err error) error {
	return fmt.Errorf("delivery %s: %w", stage, errors.Join(err, ctx.Err()))
}
