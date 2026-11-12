package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

var errNativeDeliveryMaterial = &factory.PublicationWait{Reason: "native_delivery_material_unavailable"}

type nativeDeliveryMergeCapture struct {
	account        *nativeProductAccount
	policyRevision int64
	observation    factory.MergeObservation
}

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
	return s.reviewOpinionProjection(opinions, pull, head, base)
}

func (s nativeCollaborationReads) reviewOpinionProjection(opinions nativeReviewOpinions, pull nativeSubjectRef, head, base string) (nativeReviewOpinions, error) {
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
	return p.readDeliveryMaterial(ctx, w, nil)
}

// ReadDeliveryMaterialForMerge retains only this read's final provider
// projection. The continuation still performs the normal causal review and
// accepted-input proof after authorization; submission always observes afresh.
func (p NativeMerges) ReadDeliveryMaterialForMerge(ctx context.Context, publication factory.Publication, w factory.MergeWork) (factory.DeliveryNativeMaterial, control.DeliveryMergeObservation, error) {
	var empty factory.DeliveryNativeMaterial
	current, err := p.Store.PublicationByID(ctx, publication.ID)
	if err != nil || publication.Validate() != nil || !reflect.DeepEqual(current, publication) || w.PublicationID != publication.ID || w.Repository != publication.Repository || w.Issue != publication.Issue || w.HeadOID != publication.Candidate || w.BaseOID != publication.PullTarget().BaseOID {
		return empty, nil, errNativeDeliveryMaterial
	}
	// Retain the original aggregate deadline, never start a new allowance when
	// the caller finishes local fingerprint/authorization work.
	deadline := time.Now().Add(2 * time.Minute)
	var capture nativeDeliveryMergeCapture
	material, err := p.readDeliveryMaterial(ctx, w, &capture)
	if err != nil {
		return empty, nil, err
	}
	var used atomic.Bool
	return material, func(work factory.MergeWork) (factory.MergeObservation, error) {
		var none factory.MergeObservation
		if !used.CompareAndSwap(false, true) || ctx.Err() != nil {
			return none, errNativeDeliveryMaterial
		}
		bounded, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		// Collection has no journal generation. All provider, source and
		// account facts must match. Allocation adds journal keys; the opening
		// native revision is independently matched by the closing input read.
		bound := work
		bound.MergeID, bound.OperationID, bound.AuthRevision = w.MergeID, w.OperationID, w.AuthRevision
		bound.NativeRev = w.NativeRev
		if bound != w {
			return none, errNativeDeliveryMaterial
		}
		current, err := p.Store.PublicationByID(bounded, publication.ID)
		if err != nil || !reflect.DeepEqual(current, publication) {
			return none, errNativeDeliveryMaterial
		}
		observed, err := p.observeMerge(bounded, work, func(ctx context.Context, _ factory.MergeWork, current *nativeProductAccount) (factory.MergeObservation, error) {
			currentPolicy, err := p.Store.RepositoryPolicy(ctx, w.Repository)
			if err != nil || currentPolicy.Revision != capture.policyRevision || !sameNativeAccount(current.identity, capture.account.identity) || !reflect.DeepEqual(current.binding, capture.account.binding) || !reflect.DeepEqual(current.repository, capture.account.repository) {
				return none, errNativeDeliveryMaterial
			}
			return capture.observation, nil
		})
		if err != nil {
			return none, err
		}
		current, err = p.Store.PublicationByID(bounded, publication.ID)
		if err != nil || !reflect.DeepEqual(current, publication) || observed.NativeRev != material.Revision {
			return none, errNativeDeliveryMaterial
		}
		currentPolicy, err := p.Store.RepositoryPolicy(bounded, w.Repository)
		if err != nil || currentPolicy.Revision != capture.policyRevision {
			return none, errNativeDeliveryMaterial
		}
		if factory.ValidID(publication.ID) {
			log.Printf("native merge material observation reused publication=%q", publication.ID)
		}
		return observed, nil
	}, nil
}

func (p NativeMerges) readDeliveryMaterial(ctx context.Context, w factory.MergeWork, capture *nativeDeliveryMergeCapture) (factory.DeliveryNativeMaterial, error) {
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
	// Merge admission already read the selected accepted sources. Its revision
	// brackets this request's complete collection; the closing input read below
	// must still match. Public material reads retain their own opening observation.
	beforeInput := control.AcceptanceEvidence{Revision: w.NativeRev}
	if capture == nil || w.NativeRev < 1 {
		beforeInput, err = p.ReadAcceptanceEvidence(bounded, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
		if err != nil {
			return empty, nativeDeliveryReadFailure(bounded, "accepted input", err)
		}
	}
	account, err := p.account(bounded, strconv.FormatInt(w.Repository, 10), factory.OpMerge)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "account binding", err)
	}
	if int64(account.binding.Actor.ID) != w.ActorID || policy.Merge.NativeConnection != account.binding.Connection.ID || policy.Merge.ActorID != w.ActorID {
		return empty, errNativeDeliveryMaterial
	}
	var first factory.DeliveryNativeMaterial
	if account.binding.Host.Provider == collaboration.GitHub {
		first, err = nativeGitHubDeliveryDiscussion(bounded, account, w)
	} else {
		first, err = nativeDeliveryDiscussion(bounded, nativeCollaborationReads{account}, w)
	}
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "first discussion traversal", err)
	}
	// GitHub's compound read pairs complete traversals inside the native
	// credential/deadline owner. Other hosts retain their existing leaf bracket.
	if account.binding.Host.Provider != collaboration.GitHub {
		second, err := nativeDeliveryDiscussion(bounded, nativeCollaborationReads{account}, w)
		if err != nil {
			return empty, nativeDeliveryReadFailure(bounded, "second discussion traversal", err)
		}
		if !reflect.DeepEqual(first, second) {
			return empty, errNativeDeliveryMaterial
		}
	}
	var closingOpinions nativeReviewOpinions
	after, err := p.observeProviderMergeWithOpinions(bounded, w, account, &closingOpinions)
	if err != nil {
		return empty, nativeDeliveryReadFailure(bounded, "provider recheck", err)
	}
	if account.binding.Host.Provider == collaboration.GitHub {
		if err := nativeDeliveryOpinions(&first, closingOpinions); err != nil {
			return empty, err
		}
	}
	// The two complete discussion traversals above bind the exact pull tuple.
	// One closing provider observation already contains paired complete
	// check/protection snapshots. A second pair before collecting discussion
	// adds no authority: spending and submission both wait for this closing
	// observation, and submission performs its own fresh provider admission.
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
	if capture != nil {
		*capture = nativeDeliveryMergeCapture{account: account, policyRevision: policy.Revision, observation: after}
	}
	if raw, err := json.Marshal(first); err != nil || len(raw) > 256*1024 {
		return empty, errNativeDeliveryMaterial
	}
	return first, nil
}

// A compound native read returns the same page payloads consumed by the leaf
// projectors. This request-local adapter avoids another connector traversal;
// it is discarded before the independent closing permission/check observation.
type nativeDiscussionProjection map[string]json.RawMessage

func (p nativeDiscussionProjection) read(ctx context.Context, query nativeRead, body any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := json.Marshal(query)
	if err != nil {
		return errNativeRead
	}
	raw, found := p[string(key)]
	if !found {
		return errNativeRead
	}
	delete(p, string(key))
	if nativeDecode(raw, body) != nil {
		return errNativeRead
	}
	return ctx.Err()
}

func nativeGitHubDeliveryDiscussion(ctx context.Context, account *nativeProductAccount, w factory.MergeWork) (factory.DeliveryNativeMaterial, error) {
	var empty factory.DeliveryNativeMaterial
	var captured struct {
		Pages []struct {
			Query   nativeRead      `json:"query"`
			Payload json.RawMessage `json:"payload"`
		} `json:"pages"`
	}
	if err := account.read(ctx, nativeRead{Kind: "delivery_discussion", Repository: account.repository.ExternalID, Number: uint32(w.PRNumber), Head: &w.HeadOID, Base: &w.BaseOID}, &captured); err != nil {
		return empty, err
	}
	if len(captured.Pages) == 0 || len(captured.Pages) > 256 {
		return empty, errNativeRead
	}
	projection := nativeDiscussionProjection{}
	for _, page := range captured.Pages {
		q := page.Query
		if q.Repository != account.repository.ExternalID || q.Number != uint32(w.PRNumber) {
			return empty, errNativeReadScope
		}
		switch q.Kind {
		case "pull", "pull_comments", "pull_reviews", "pull_threads":
		default:
			return empty, errNativeRead
		}
		key, err := json.Marshal(q)
		if err != nil || len(page.Payload) == 0 || projection[string(key)] != nil {
			return empty, errNativeRead
		}
		projection[string(key)] = page.Payload
	}
	view := *account
	view.discussion = &projection
	material, err := nativeDeliveryDiscussion(ctx, nativeCollaborationReads{&view}, w)
	if err != nil {
		return empty, err
	}
	if len(projection) != 0 {
		return empty, errNativeRead
	}
	return material, nil
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
	if reads.binding.Host.Provider != collaboration.GitHub {
		opinions, err := reads.readReviewOpinions(ctx, nativeSubjectRef{Kind: "pull", ExternalID: pull.Ref.ExternalID, Number: w.PRNumber}, w.HeadOID, w.BaseOID)
		if err != nil {
			return material, nativeDeliveryReadFailure(ctx, "current review opinions", err)
		}
		if err := nativeDeliveryOpinions(&material, opinions); err != nil {
			return material, err
		}
	}
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

func nativeDeliveryOpinions(material *factory.DeliveryNativeMaterial, opinions nativeReviewOpinions) error {
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
			return errNativeDeliveryMaterial
		}
		if opinions.Decision != nil && *opinions.Decision == "CHANGES_REQUESTED" && opinion.State == "changes_requested" {
			material.BlockingReviewIDs = append(material.BlockingReviewIDs, opinion.ID)
		}
	}
	if opinions.Decision != nil && *opinions.Decision == "CHANGES_REQUESTED" && len(material.BlockingReviewIDs) == 0 {
		return errNativeDeliveryMaterial
	}
	sort.Strings(material.BlockingReviewIDs)
	return nil
}

func nativeDeliveryReadFailure(ctx context.Context, stage string, err error) error {
	return fmt.Errorf("delivery %s: %w", stage, errors.Join(err, ctx.Err()))
}
