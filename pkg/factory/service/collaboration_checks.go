package service

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrChecksQuery       = errors.New("invalid check query")
	ErrChecksMissing     = errors.New("checks are not visible")
	ErrChecksUnavailable = errors.New("check reads unavailable")
	ErrChecksChanged     = errors.New("checks or protection changed")
	ErrChecksTooLarge    = errors.New("checks exceed read limit")
)

type ChecksQuery struct {
	Kind   string
	Head   collaboration.CommitID
	Cursor string
	Limit  int
}

func (q ChecksQuery) Validate() error {
	if q.Kind != "" && q.Kind != "status" && q.Kind != "job" || q.Limit < 1 || q.Limit > 50 || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) || !(q.Head == "" && q.Cursor == "" || collaboration.ValidCommitID(q.Head)) {
		return ErrChecksQuery
	}
	return nil
}

type CollaborationCheckSource interface {
	ReadChecks(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, ChecksQuery) (collaboration.PullChecks, error)
}
type CollaborationChecks struct{ Source CollaborationCheckSource }

func (s CollaborationChecks) Read(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q ChecksQuery) (collaboration.PullChecks, error) {
	var empty collaboration.PullChecks
	if actor < 1 || repo < 1 || number < 1 || q.Validate() != nil {
		return empty, ErrChecksQuery
	}
	if s.Source == nil {
		return empty, ErrChecksUnavailable
	}
	r, err := s.Source.ReadChecks(ctx, actor, repo, number, q)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	p := r.Checks
	kind := q.Kind
	if kind == "" {
		kind = "status"
	}
	if r.Kind != kind {
		return empty, ErrChecksUnavailable
	}
	if r.Pull.Kind != collaboration.SubjectPull || r.Pull.Repository != repo || r.Pull.Number != number || r.Pull.ExternalID == "" || !collaboration.ValidCommitID(r.Commit) || q.Head != "" && r.Commit != q.Head || r.Observation.State != collaboration.ReadAvailable || r.Observation.Revision == "" || p.Items == nil || len(p.Items) > q.Limit || p.Complete != (p.Next == "") || p.Next != "" && (kind != "job" && len(p.Items) == 0 || !collaboration.ValidText(p.Next, 4096)) || p.Observation.State != collaboration.ReadAvailable || p.Observation.Revision == "" || r.MissingRequired == nil || r.InvalidRequired == nil {
		return empty, ErrChecksUnavailable
	}
	switch r.StatusGate {
	case "not_required", "pending", "success", "failure", "error", "warning", "unknown":
	default:
		return empty, ErrChecksUnavailable
	}
	if b := r.Protection; b != nil {
		if b.Repository != repo || b.Branch == "" || b.Observation.State != collaboration.ReadAvailable || b.Observation.Revision == "" || b.RequiredContexts == nil || b.RequiredApprovals != nil && (*b.RequiredApprovals < 0 || *b.RequiredApprovals > 9007199254740991) {
			return empty, ErrChecksUnavailable
		}
	}
	seen := map[string]bool{}
	for _, c := range p.Items {
		if c.Kind != kind {
			return empty, ErrChecksUnavailable
		}
		if kind == "job" {
			j := c.Job
			if j == nil || !checkPositiveNumber(j.RunID) || !checkPositiveNumber(j.RunNumber) || !(j.Attempt == "0" || checkPositiveNumber(j.Attempt)) || j.WorkflowCommit != "" && !collaboration.ValidCommitID(j.WorkflowCommit) || c.Required != nil {
				return empty, ErrChecksUnavailable
			}
		} else if c.Job != nil {
			return empty, ErrChecksUnavailable
		}
		if c.ExternalID == "" || seen[c.ExternalID] || c.Repository != repo || c.Commit != r.Commit || c.Name == "" || c.Kind != "status" && c.Kind != "job" || c.Observation.State != collaboration.ReadAvailable || c.Observation.Revision == "" {
			return empty, ErrChecksUnavailable
		}
		seen[c.ExternalID] = true
		switch c.State {
		case "pending", "running", "completed", "unknown":
		default:
			return empty, ErrChecksUnavailable
		}
		switch c.Conclusion {
		case "success", "failure", "neutral", "skipped", "cancelled", "timed_out", "action_required", "stale", "unknown":
		default:
			return empty, ErrChecksUnavailable
		}
		if c.DetailsURL != "" {
			u, err := url.Parse(c.DetailsURL)
			if err != nil || u.Host == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
				return empty, ErrChecksUnavailable
			}
		}
	}
	return r, nil
}
func checkPositiveNumber(raw string) bool {
	n, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}
