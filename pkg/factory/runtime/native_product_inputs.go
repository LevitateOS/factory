package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

type nativeIssueInputs struct{ *nativeProductAccount }
type nativeInputSnapshot struct {
	Issue struct {
		ID                  string                      `json:"id"`
		Number              int64                       `json:"number"`
		Title               string                      `json:"title"`
		Body                string                      `json:"body"`
		Version             *int                        `json:"version"`
		TitleOccurrence     string                      `json:"title_occurrence"`
		LifecycleOccurrence string                      `json:"lifecycle_occurrence"`
		ClosingOccurrence   string                      `json:"closing_occurrence"`
		Author              collaboration.ObservedActor `json:"author"`
		CreatedAt           *time.Time                  `json:"created_at"`
		ClosedAt            *time.Time                  `json:"closed_at"`
		Closed              *bool                       `json:"closed"`
		Locked              *bool                       `json:"locked"`
	} `json:"issue"`
	Comments []struct {
		ID      string `json:"id"`
		Body    string `json:"body"`
		Version *int   `json:"version"`
	} `json:"comments"`
	Dependencies []struct {
		Occurrence string `json:"occurrence"`
		DependsOn  string `json:"depends_on"`
		Repository string `json:"repository"`
		Number     int64  `json:"number"`
	} `json:"dependencies"`
	Tip    string `json:"tip"`
	TipRef string `json:"tip_ref"`
}

func inputDigest(text []byte) string { sum := sha256.Sum256(text); return hex.EncodeToString(sum[:]) }

func (s nativeIssueInputs) readInputs(ctx context.Context, repository, index string, comments []string, target string) (nativeInputSnapshot, int64, map[string]string, error) {
	var raw nativeInputSnapshot
	repo, err := strconv.ParseInt(repository, 10, 64)
	number, numberErr := strconv.ParseInt(index, 10, 32)
	if err != nil || numberErr != nil || repo <= 0 || number <= 0 || strconv.FormatInt(repo, 10) != repository || strconv.FormatInt(number, 10) != index || len(comments) > factory.MaxAcceptedSources || s.repository == nil || int64(s.repository.ID) != repo {
		return raw, 0, nil, errNativeReadScope
	}
	seen := map[string]bool{}
	for _, id := range comments {
		if !nativeRemoteID(id) || seen[id] {
			return raw, 0, nil, errNativeRead
		}
		seen[id] = true
	}
	observed, err := s.repositoryObservation(ctx, s.binding.Actor.ID, collaboration.RepositoryID(repo))
	if err != nil {
		return raw, 0, nil, err
	}
	if target == "" {
		target = "refs/heads/" + observed.DefaultBranch
	}
	if !factory.ValidTargetBranch(target) {
		return raw, 0, nil, errNativeRead
	}
	if err = s.read(ctx, nativeRead{Kind: "automation_inputs", Repository: s.repository.ExternalID, Number: uint32(number), Comments: comments, TargetRef: target}, &raw); err != nil {
		return raw, 0, nil, err
	}
	i := raw.Issue
	if i.Number != number || !nativeRemoteID(i.ID) || i.Title == "" || len(i.Title) > 4096 || len(i.Body) > 65536 || i.Version == nil || *i.Version < 0 || !nativeActor(i.Author) || i.CreatedAt == nil || i.CreatedAt.IsZero() || i.Closed == nil || i.Locked == nil || *i.Closed != (i.ClosedAt != nil) || raw.Comments == nil || len(raw.Comments) != len(comments) || raw.Dependencies == nil || len(raw.Dependencies) > 50 || raw.TipRef != target || !factory.ValidCommit(raw.Tip) {
		return raw, 0, nil, errNativeRead
	}
	occurrences := []string{}
	for _, id := range []string{i.TitleOccurrence, i.LifecycleOccurrence} {
		if id != "original" {
			if !nativeReviewNode(id, 1024) {
				return raw, 0, nil, errNativeRead
			}
			occurrences = append(occurrences, id)
		}
	}
	if i.ClosingOccurrence != "" {
		if !nativeReviewNode(i.ClosingOccurrence, 1024) {
			return raw, 0, nil, errNativeRead
		}
		occurrences = append(occurrences, i.ClosingOccurrence)
	}
	seen = map[string]bool{}
	for _, edge := range raw.Dependencies {
		if !nativeRemoteID(edge.DependsOn) || !nativeRemoteID(edge.Repository) || edge.Number <= 0 || !nativeReviewNode(edge.Occurrence, 1024) || seen[edge.DependsOn] {
			return raw, 0, nil, errNativeRead
		}
		seen[edge.DependsOn] = true
		occurrences = append(occurrences, edge.Occurrence)
	}
	for n, comment := range raw.Comments {
		if comment.ID != comments[n] || comment.Version == nil || *comment.Version < 0 || len(comment.Body) > 65536 {
			return raw, 0, nil, errNativeRead
		}
	}
	// Login labels do not change an accepted objective's revision.
	semantic := i
	semantic.Author.Login = ""
	encoded, err := json.Marshal(struct {
		Issue        any
		Dependencies any
	}{semantic, raw.Dependencies})
	if err != nil {
		return raw, 0, nil, err
	}
	revision, mapped, err := s.store.ObserveNativeIssueInputs(ctx, repo, number, inputDigest(encoded), occurrences)
	if err == nil {
		err = s.current(ctx)
	}
	return raw, revision, mapped, err
}

func occurrenceID(id string, mapped map[string]string) string {
	if id == "" || id == "original" {
		return id
	}
	return mapped[id]
}

func (s nativeIssueInputs) ReadAcceptanceEvidence(ctx context.Context, repository, index string, comments []string) (control.AcceptanceEvidence, error) {
	raw, revision, mapped, err := s.readInputs(ctx, repository, index, comments, "")
	if err != nil {
		return control.AcceptanceEvidence{}, err
	}
	i := raw.Issue
	actor, err := s.store.ObserveCollaborationActor(ctx, s.binding.Host.ID, i.Author.ExternalID, i.Author.Login)
	if err != nil {
		return control.AcceptanceEvidence{}, err
	}
	view := control.AcceptanceEvidence{Revision: revision, Comments: []control.AcceptanceComment{}, Dependencies: []control.AcceptanceEdge{}, Issue: control.AcceptanceIssueView{
		ID: i.ID, Index: index, TitleDigest: inputDigest([]byte(i.Title)), ContentDigest: inputDigest([]byte(i.Body)), ContentVer: *i.Version,
		TitleOccurrence: occurrenceID(i.TitleOccurrence, mapped), LifecycleOccurrence: occurrenceID(i.LifecycleOccurrence, mapped), ClosingOccurrence: occurrenceID(i.ClosingOccurrence, mapped),
		PosterID: strconv.FormatInt(int64(actor.ID), 10), CreatedUnix: i.CreatedAt.Unix(), Visible: true, Verified: true, FirstCreated: *i.Version == 0 && i.TitleOccurrence == "original" && i.LifecycleOccurrence == "original", Closed: *i.Closed}}
	if i.ClosedAt != nil {
		view.Issue.ClosedUnix = i.ClosedAt.Unix()
	}
	for _, c := range raw.Comments {
		view.Comments = append(view.Comments, control.AcceptanceComment{ID: c.ID, Digest: inputDigest([]byte(c.Body)), ContentVer: *c.Version, Visible: true})
	}
	for _, e := range raw.Dependencies {
		view.Dependencies = append(view.Dependencies, control.AcceptanceEdge{Occurrence: mapped[e.Occurrence], DependsOn: e.DependsOn, Visible: true})
	}
	return view, ctx.Err()
}

func (s nativeIssueInputs) ReadDispatchInputs(ctx context.Context, repository, index string, comments []string, target string) (control.DispatchInputs, error) {
	raw, revision, mapped, err := s.readInputs(ctx, repository, index, comments, target)
	if err != nil {
		return control.DispatchInputs{}, err
	}
	i := raw.Issue
	view := control.DispatchInputs{Revision: revision, Tip: raw.Tip, TipRef: raw.TipRef, Comments: []control.DispatchComment{}, Issue: control.DispatchIssue{Title: i.Title, Body: i.Body, TitleDigest: inputDigest([]byte(i.Title)), ContentDigest: inputDigest([]byte(i.Body)), ContentVer: *i.Version, TitleOccurrence: occurrenceID(i.TitleOccurrence, mapped), ClosingOccurrence: occurrenceID(i.ClosingOccurrence, mapped), LifecycleOccurrence: occurrenceID(i.LifecycleOccurrence, mapped), Visible: true, Closed: *i.Closed, Locked: *i.Locked}}
	if i.ClosedAt != nil {
		view.Issue.ClosedUnix = i.ClosedAt.Unix()
	}
	for _, c := range raw.Comments {
		view.Comments = append(view.Comments, control.DispatchComment{ID: c.ID, Content: c.Body, Digest: inputDigest([]byte(c.Body)), ContentVer: *c.Version, Visible: true})
	}
	return view, ctx.Err()
}

type nativeAcceptanceWriter struct {
	*control.Coordinator
	source control.AcceptanceSource
}

func (w nativeAcceptanceWriter) AdmitAcceptance(ctx context.Context, decision factory.Acceptance, authorize control.AcceptanceAuthorization) (control.AcceptanceResult, error) {
	if w.Coordinator == nil || w.source == nil {
		return control.AcceptanceResult{}, errors.New("native acceptance source unavailable")
	}
	return w.Coordinator.AdmitAcceptanceUsing(ctx, w.source, decision, authorize)
}
