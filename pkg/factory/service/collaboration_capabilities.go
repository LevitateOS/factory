package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrCapabilitiesQuery       = errors.New("invalid capability query")
	ErrCapabilitiesMissing     = errors.New("capability resource is not visible")
	ErrCapabilitiesUnavailable = errors.New("capabilities unavailable")
	ErrCapabilitiesChanged     = errors.New("capabilities changed during read")
)

type CapabilityQuery struct {
	Subject string
	Number  int64
}

func (q CapabilityQuery) Validate() error {
	if !(q.Subject == "repository" && q.Number == 0 || (q.Subject == "issue" || q.Subject == "pull") && q.Number > 0) {
		return ErrCapabilitiesQuery
	}
	return nil
}

type CollaborationCapabilitySource interface {
	ReadCapabilities(context.Context, collaboration.ActorID, collaboration.RepositoryID, CapabilityQuery) (collaboration.Capabilities, error)
}
type CollaborationCapabilities struct{ Source CollaborationCapabilitySource }

func (s CollaborationCapabilities) Read(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, q CapabilityQuery) (collaboration.Capabilities, error) {
	var empty collaboration.Capabilities
	if actor < 1 || repo < 1 || q.Validate() != nil {
		return empty, ErrCapabilitiesQuery
	}
	if s.Source == nil {
		return empty, ErrCapabilitiesUnavailable
	}
	r, err := s.Source.ReadCapabilities(ctx, actor, repo, q)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if r.Repository != repo || r.Actor != actor || r.Observation.State != collaboration.ReadAvailable || r.Observation.Revision == "" {
		return empty, ErrCapabilitiesUnavailable
	}
	if q.Subject != "pull" && (r.Pull != nil || r.Base != nil || r.Head != nil) || q.Subject != "issue" && r.Issue != nil {
		return empty, ErrCapabilitiesUnavailable
	}
	if q.Subject == "issue" && (r.Issue == nil || r.Issue.Kind != collaboration.SubjectIssue || r.Issue.Repository != repo || r.Issue.Number != q.Number || !collaboration.ValidText(r.Issue.ExternalID, 1024)) {
		return empty, ErrCapabilitiesUnavailable
	}
	if q.Subject == "pull" && (r.Pull == nil || r.Pull.Kind != collaboration.SubjectPull || r.Pull.Repository != repo || r.Pull.Number != q.Number || !collaboration.ValidText(r.Pull.ExternalID, 1024)) {
		return empty, ErrCapabilitiesUnavailable
	}
	if r.Head != nil && !collaboration.ValidCommitID(*r.Head) || r.Base != nil && !collaboration.ValidCommitID(*r.Base) {
		return empty, ErrCapabilitiesUnavailable
	}
	actions := []collaboration.Action{collaboration.ReadIssues, collaboration.CreateIssue, collaboration.EditIssue, collaboration.CommentIssue, collaboration.ReadPulls, collaboration.CreatePull, collaboration.EditPull, collaboration.CommentPull, collaboration.ReviewPull, collaboration.CommentReview, collaboration.ApproveReview, collaboration.RequestChangesReview, collaboration.ResolveThread, collaboration.MergePull, collaboration.ReadChecks, collaboration.ReadCheckJobs}
	if len(r.Actions) != len(actions) {
		return empty, ErrCapabilitiesUnavailable
	}
	for _, action := range actions {
		fact, ok := r.Actions[action]
		if !ok || fact.Reason != "" && !collaboration.ValidText(fact.Reason, 256) {
			return empty, ErrCapabilitiesUnavailable
		}
		switch fact.State {
		case collaboration.Allowed, collaboration.Denied, collaboration.Unsupported, collaboration.Unavailable, collaboration.Unknown:
		default:
			return empty, ErrCapabilitiesUnavailable
		}
	}
	return r, nil
}
