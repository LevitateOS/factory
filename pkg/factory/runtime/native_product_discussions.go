package runtime

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeDiscussion struct {
	Repository string           `json:"repository"`
	Name       string           `json:"name"`
	Subject    nativeSubjectRef `json:"subject"`
	Items      []nativeComment  `json:"items"`
	NextPage   int              `json:"next_page"`
}

func (s nativeCollaborationReads) comments(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.DiscussionQuery, pull bool) (nativeDiscussion, nativeCursor, collaboration.ObservedIssue, collaboration.ConnectionObservationMapping, error) {
	var raw nativeDiscussion
	var subject collaboration.ObservedIssue
	var mapping collaboration.ConnectionObservationMapping
	kind := "issue_comments"
	if pull {
		kind = "pull_comments"
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: kind, Repository: s.identity.SelectedRepository, Number: number, Limit: q.Limit})
	if err != nil || q.Validate() != nil || !nativeIndex(number) {
		return raw, c, subject, mapping, factoryservice.ErrDiscussionQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrDiscussionMissing); err != nil {
		return raw, c, subject, mapping, err
	}
	if err = s.read(ctx, nativeRead{Kind: kind, Repository: c.Repository, Number: uint32(number), Page: uint32(c.Page), Limit: uint32(c.Limit)}, &raw); err != nil {
		return raw, c, subject, mapping, nativeMissing(err, factoryservice.ErrDiscussionMissing)
	}
	var actors []collaboration.ObservedActor
	expectedKind := "issue"
	if pull {
		expectedKind = "pull"
	}
	subject = collaboration.ObservedIssue{ExternalID: raw.Subject.ExternalID, Number: raw.Subject.Number}
	if raw.Subject.Kind != expectedKind || subject.Number != number || !nativeRemoteID(subject.ExternalID) || raw.Repository != c.Repository || raw.Items == nil || !validNativePage(raw.NextPage, c.Page, c.Limit, len(raw.Items), false) {
		return raw, c, subject, mapping, errNativeRead
	}
	seen := map[string]bool{}
	for _, comment := range raw.Items {
		if !nativeRemoteID(comment.ExternalID) || seen[comment.ExternalID] {
			return raw, c, subject, mapping, errNativeRead
		}
		seen[comment.ExternalID] = true
		actors = appendNativeActor(actors, comment.Author)
	}
	mapping, err = s.mapRead(ctx, raw.Repository, raw.Name, actors, nil)
	return raw, c, subject, mapping, err
}
func (s nativeCollaborationReads) comment(raw nativeComment, mapping collaboration.ConnectionObservationMapping) collaboration.Comment {
	return collaboration.Comment{ExternalID: raw.ExternalID, Observation: s.observation(raw), Author: nativeActorRef(mapping, raw.Author), Body: raw.Body, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt}
}
func (s nativeCollaborationReads) ReadIssueComments(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.DiscussionQuery) (collaboration.Page[collaboration.IssueComment], error) {
	raw, c, parent, mapping, err := s.comments(ctx, actor, repo, number, q, false)
	if err != nil {
		return collaboration.Page[collaboration.IssueComment]{}, err
	}
	items := make([]collaboration.IssueComment, 0, len(raw.Items))
	for _, comment := range raw.Items {
		items = append(items, collaboration.IssueComment{Issue: nativeIssueRef(parent, repo), Comment: s.comment(comment, mapping)})
	}
	return nativePage(s.observation(raw), items, cursorEncode(c, raw.NextPage)), nil
}
func (s nativeCollaborationReads) ReadPullComments(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.DiscussionQuery) (collaboration.Page[collaboration.PullComment], error) {
	raw, c, parent, mapping, err := s.comments(ctx, actor, repo, number, q, true)
	if err != nil {
		return collaboration.Page[collaboration.PullComment]{}, err
	}
	items := make([]collaboration.PullComment, 0, len(raw.Items))
	for _, comment := range raw.Items {
		items = append(items, collaboration.PullComment{Pull: nativePullRef(parent, repo), Comment: s.comment(comment, mapping)})
	}
	return nativePage(s.observation(raw), items, cursorEncode(c, raw.NextPage)), nil
}
func (s nativeCollaborationReads) ReadReviews(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.ReviewQuery) (collaboration.Page[collaboration.Review], error) {
	var empty collaboration.Page[collaboration.Review]
	if q.Validate() != nil || !nativeIndex(number) || q.ReviewID != "" && !nativeRemoteID(q.ReviewID) {
		return empty, factoryservice.ErrReviewQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrReviewMissing); err != nil {
		return empty, err
	}
	kind := "pull_reviews"
	if q.ReviewID != "" {
		kind = "pull_review"
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: kind, Repository: s.identity.SelectedRepository, Number: number, Root: q.ReviewID, Limit: q.Limit})
	if err != nil {
		return empty, factoryservice.ErrReviewQuery
	}
	query := nativeRead{Kind: kind, Repository: c.Repository, Number: uint32(number), ReviewID: q.ReviewID}
	if q.ReviewID == "" {
		query.Page, query.Limit = uint32(c.Page), uint32(c.Limit)
	}
	var raw struct {
		Repository string           `json:"repository"`
		Name       string           `json:"name"`
		Pull       nativeSubjectRef `json:"pull"`
		Items      []nativeReview   `json:"items"`
		NextPage   int              `json:"next_page"`
	}
	if err = s.read(ctx, query, &raw); err != nil {
		return empty, nativeMissing(err, factoryservice.ErrReviewMissing)
	}
	var actors []collaboration.ObservedActor
	if raw.Pull.Kind != "pull" || !nativeRemoteID(raw.Pull.ExternalID) || raw.Pull.Number != number || raw.Repository != c.Repository || raw.Items == nil || !validNativePage(raw.NextPage, c.Page, c.Limit, len(raw.Items), false) || q.ReviewID != "" && (len(raw.Items) != 1 || raw.NextPage != 0) {
		return empty, errNativeRead
	}
	seen := map[string]bool{}
	for _, review := range raw.Items {
		if !nativeRemoteID(review.ExternalID) || seen[review.ExternalID] || q.ReviewID != "" && review.ExternalID != q.ReviewID || review.Commit != nil && !collaboration.ValidCommitID(*review.Commit) || review.State == "pending" && review.SubmittedAt != nil {
			return empty, errNativeRead
		}
		switch review.State {
		case "pending", "approved", "changes_requested", "commented", "dismissed", "unknown":
		default:
			return empty, errNativeRead
		}
		seen[review.ExternalID] = true
		actors = appendNativeActor(actors, review.Author)
	}
	mapping, err := s.mapRead(ctx, raw.Repository, raw.Name, actors, nil)
	if err != nil {
		return empty, err
	}
	items := make([]collaboration.Review, 0, len(raw.Items))
	for _, review := range raw.Items {
		item := collaboration.Review{ExternalID: review.ExternalID, Pull: collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: raw.Pull.ExternalID, Number: number}, Observation: s.observation(review), Author: nativeActorRef(mapping, review.Author), Body: review.Body, State: review.State, SubmittedAt: review.SubmittedAt}
		item.Official, item.Stale = review.Official, review.Stale
		if review.Commit != nil {
			item.Commit = *review.Commit
		}
		items = append(items, item)
	}
	return nativePage(s.observation(raw), items, cursorEncode(c, raw.NextPage)), nil
}

type nativeThread struct {
	ExternalID         string                        `json:"external_id"`
	RecordedLocation   *collaboration.ReviewLocation `json:"recorded_location"`
	Resolution         string                        `json:"resolution"`
	Outdated           *bool                         `json:"outdated"`
	ViewerCanResolve   *bool                         `json:"viewer_can_resolve"`
	ViewerCanUnresolve *bool                         `json:"viewer_can_unresolve"`
	Comments           struct {
		Items    []nativeReviewComment `json:"items"`
		NextPage int                   `json:"next_page"`
	} `json:"comments"`
}

func (s nativeCollaborationReads) ReadThreads(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.ThreadQuery) (collaboration.Page[collaboration.ReviewThread], error) {
	var empty collaboration.Page[collaboration.ReviewThread]
	if q.Validate() != nil || !nativeIndex(number) || q.ThreadID != "" && !nativeThreadID(q.ThreadID) {
		return empty, factoryservice.ErrThreadQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrThreadMissing); err != nil {
		return empty, err
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: "pull_review_threads_v1", Repository: s.identity.SelectedRepository, Number: number, Root: q.ThreadID, Limit: q.Limit})
	if err != nil {
		return empty, factoryservice.ErrThreadQuery
	}
	var raw struct {
		Repository string           `json:"repository"`
		Name       string           `json:"name"`
		Pull       nativeSubjectRef `json:"pull"`
		Items      []nativeThread   `json:"items"`
		NextPage   int              `json:"next_page"`
	}
	if err = s.read(ctx, nativeRead{Kind: "pull_threads", Repository: c.Repository, Number: uint32(number), Page: uint32(c.Page), Limit: uint32(c.Limit), ThreadID: &q.ThreadID}, &raw); err != nil {
		var failure *NativePortError
		if errors.As(err, &failure) {
			if failure.Status == 409 {
				return empty, factoryservice.ErrThreadChanged
			}
			if failure.Status == 413 {
				return empty, factoryservice.ErrThreadsTooLarge
			}
		}
		return empty, nativeMissing(err, factoryservice.ErrThreadMissing)
	}
	var actors []collaboration.ObservedActor
	if raw.Pull.Kind != "pull" || !nativeRemoteID(raw.Pull.ExternalID) || raw.Pull.Number != number || raw.Repository != c.Repository || raw.Items == nil || !validNativePage(raw.NextPage, c.Page, c.Limit, len(raw.Items), false) || q.ThreadID != "" && (len(raw.Items) != 1 || raw.NextPage != 0) {
		return empty, errNativeRead
	}
	for _, thread := range raw.Items {
		limit, page := 10, 1
		if q.ThreadID != "" {
			limit, page = c.Limit, c.Page
		}
		if !nativeThreadID(thread.ExternalID) || thread.Outdated == nil || thread.ViewerCanResolve == nil || thread.ViewerCanUnresolve == nil || q.ThreadID != "" && thread.ExternalID != q.ThreadID || thread.Comments.Items == nil || !validNativePage(thread.Comments.NextPage, page, limit, len(thread.Comments.Items), false) {
			return empty, errNativeRead
		}
		for _, comment := range thread.Comments.Items {
			if !nativeRemoteID(comment.ExternalID) || comment.Body == nil || comment.ReviewID != nil && !nativeRemoteID(*comment.ReviewID) {
				return empty, errNativeRead
			}
			actors = appendNativeActor(actors, comment.Author)
		}
	}
	mapping, err := s.mapRead(ctx, raw.Repository, raw.Name, actors, nil)
	if err != nil {
		return empty, err
	}
	obs := s.observation(raw)
	items := make([]collaboration.ReviewThread, 0, len(raw.Items))
	pull := collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: raw.Pull.ExternalID, Number: number}
	for _, thread := range raw.Items {
		comments := make([]collaboration.ReviewComment, 0, len(thread.Comments.Items))
		for _, comment := range thread.Comments.Items {
			body := ""
			if comment.Body != nil {
				body = *comment.Body
			}
			comments = append(comments, collaboration.ReviewComment{ReviewID: comment.ReviewID, Comment: collaboration.Comment{ExternalID: comment.ExternalID, Observation: s.observation(comment), Author: nativeActorRef(mapping, comment.Author), Body: body, CreatedAt: comment.CreatedAt, UpdatedAt: comment.UpdatedAt}})
		}
		commentCursor := c
		commentCursor.Root = thread.ExternalID
		if q.ThreadID == "" {
			commentCursor.Limit = 10
		}
		// ResolveThread describes changing the current resolution in either
		// direction. These native viewer facts are presentation, not admission.
		change := collaboration.Capability{State: collaboration.Unknown, Reason: "thread_resolution_unknown"}
		if thread.Resolution == "resolved" || thread.Resolution == "unresolved" {
			canChange := *thread.ViewerCanResolve
			if thread.Resolution == "resolved" {
				canChange = *thread.ViewerCanUnresolve
			}
			change = collaboration.Capability{State: collaboration.Denied, Reason: "provider_cannot_change_resolution"}
			if canChange {
				change = collaboration.Capability{State: collaboration.Allowed}
			}
		}
		capabilities := &collaboration.Capabilities{Repository: repo, Actor: actor, Connection: s.binding.Connection.ID, Pull: &pull, Observation: s.observation(thread), Actions: map[collaboration.Action]collaboration.Capability{collaboration.ResolveThread: change}}
		items = append(items, collaboration.ReviewThread{ExternalID: thread.ExternalID, Pull: pull, Observation: s.observation(thread), RecordedLocation: thread.RecordedLocation, Resolution: thread.Resolution, Outdated: thread.Outdated,
			Comments: nativePage(obs, comments, cursorEncode(commentCursor, thread.Comments.NextPage)), Capabilities: capabilities})
	}
	return nativePage(obs, items, cursorEncode(c, raw.NextPage)), nil
}

// GitHub thread selectors are opaque printable Node IDs. Historical decimal
// root-comment selectors cannot acquire the new thread identity by remapping.
func nativeThreadID(value string) bool {
	if value == "" || len(value) > 1024 {
		return false
	}
	nonDigit := false
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
		nonDigit = nonDigit || b < '0' || b > '9'
	}
	return nonDigit
}
