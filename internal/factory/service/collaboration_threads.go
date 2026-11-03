package service

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/collaboration"
)

var (
	ErrThreadQuery       = errors.New("invalid thread query")
	ErrThreadMissing     = errors.New("thread is not visible")
	ErrThreadUnavailable = errors.New("thread reads unavailable")
	ErrThreadChanged     = errors.New("thread placement changed")
	ErrThreadsTooLarge   = errors.New("thread catalogue exceeds read limit")
)

type ThreadQuery struct {
	ThreadID, Cursor string
	Limit            int
}

func (q ThreadQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 20 || q.ThreadID != "" && !collaboration.ValidText(q.ThreadID, 1024) || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) {
		return ErrThreadQuery
	}
	return nil
}

type CollaborationThreadSource interface {
	ReadThreads(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, ThreadQuery) (collaboration.Page[collaboration.ReviewThread], error)
}
type CollaborationThreads struct{ Source CollaborationThreadSource }

func (s CollaborationThreads) Read(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q ThreadQuery) (collaboration.Page[collaboration.ReviewThread], error) {
	var empty collaboration.Page[collaboration.ReviewThread]
	if actor < 1 || repo < 1 || number < 1 || q.Validate() != nil {
		return empty, ErrThreadQuery
	}
	if s.Source == nil {
		return empty, ErrThreadUnavailable
	}
	p, err := s.Source.ReadThreads(ctx, actor, repo, number, q)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if p.Items == nil || p.Observation.State != collaboration.ReadAvailable || p.Observation.Revision == "" || p.Complete != (p.Next == "") || p.Next != "" && (len(p.Items) == 0 || !collaboration.ValidText(p.Next, 4096)) || q.ThreadID == "" && len(p.Items) > q.Limit || q.ThreadID != "" && (len(p.Items) != 1 || p.Next != "") {
		return empty, ErrThreadUnavailable
	}
	seen := map[string]bool{}
	parent := ""
	for _, t := range p.Items {
		if t.ExternalID == "" || seen[t.ExternalID] || q.ThreadID != "" && t.ExternalID != q.ThreadID || t.Pull.Kind != collaboration.SubjectPull || t.Pull.Repository != repo || t.Pull.Number != number || t.Pull.ExternalID == "" || parent != "" && parent != t.Pull.ExternalID || t.Observation.State != collaboration.ReadAvailable || t.Observation.Revision == "" || !validThreadAnchor(t.Anchor, t.Pull) || !validThreadAnchor(t.CurrentAnchor, t.Pull) {
			return empty, ErrThreadUnavailable
		}
		parent = t.Pull.ExternalID
		seen[t.ExternalID] = true
		if l := t.RecordedLocation; l != nil {
			if l.Commit != nil && !collaboration.ValidCommitID(*l.Commit) || l.Path == "" || len(l.Path) > 4096 || !utf8.ValidString(l.Path) || l.Side != "old" && l.Side != "new" || l.Line < 1 || l.Line > 9007199254740991 {
				return empty, ErrThreadUnavailable
			}
		}
		switch t.Resolution {
		case "resolved", "unresolved", "unknown":
		default:
			return empty, ErrThreadUnavailable
		}
		c := t.Comments
		limit := 10
		if q.ThreadID != "" {
			limit = q.Limit
		}
		if c.Items == nil || len(c.Items) > limit || c.Complete != (c.Next == "") || c.Next != "" && (len(c.Items) == 0 || !collaboration.ValidText(c.Next, 4096)) || c.Observation.State != collaboration.ReadAvailable || c.Observation.Revision == "" {
			return empty, ErrThreadUnavailable
		}
		comments := map[string]bool{}
		for _, r := range c.Items {
			if r.ReviewID == "" || r.Comment.ExternalID == "" || comments[r.Comment.ExternalID] || r.Comment.Observation.State != collaboration.ReadAvailable || r.Comment.Observation.Revision == "" || !validThreadAnchor(r.Anchor, t.Pull) {
				return empty, ErrThreadUnavailable
			}
			comments[r.Comment.ExternalID] = true
		}
	}
	return p, nil
}
func validThreadAnchor(a *collaboration.ReviewAnchor, pull collaboration.PullRef) bool {
	if a == nil {
		return true
	}
	if a.Diff.Pull != pull || !collaboration.ValidCommitID(a.Diff.Base) || !collaboration.ValidCommitID(a.Diff.Head) || len(a.OldPath) > 4096 || len(a.NewPath) > 4096 || !utf8.ValidString(a.OldPath) || !utf8.ValidString(a.NewPath) || a.Side != "old" && a.Side != "new" || a.Side == "old" && a.OldPath == "" || a.Side == "new" && a.NewPath == "" {
		return false
	}
	return a.Scope == "file" && a.Lines == nil || a.Scope == "lines" && a.Lines != nil && a.Lines.Start > 0 && a.Lines.End >= a.Lines.Start
}
