package runtime

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// These are current presentation facts. Every command independently prepares
// its exact target and rechecks admission before a provider effect.
func (s nativeCollaborationReads) ReadCapabilities(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, q factoryservice.CapabilityQuery) (collaboration.Capabilities, error) {
	var result collaboration.Capabilities
	if q.Validate() != nil || q.Number != 0 && !nativeIndex(q.Number) {
		return result, factoryservice.ErrCapabilitiesQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrCapabilitiesMissing); err != nil {
		return result, err
	}
	var facts struct {
		Repository   string `json:"repository"`
		Name         string `json:"name"`
		Archived     bool   `json:"archived"`
		Disabled     bool   `json:"disabled"`
		Issues       bool   `json:"issues_enabled"`
		CreateIssues bool   `json:"can_create_issues"`
		Writer       bool   `json:"writer"`
		Merge        bool   `json:"merge"`
		Squash       bool   `json:"squash"`
		Rebase       bool   `json:"rebase"`
		Subject      *struct {
			Kind       string                  `json:"kind"`
			ExternalID string                  `json:"external_id"`
			Number     int64                   `json:"number"`
			State      string                  `json:"state"`
			Locked     bool                    `json:"locked"`
			CanUpdate  bool                    `json:"can_update"`
			Own        bool                    `json:"own"`
			Draft      bool                    `json:"draft"`
			Merged     bool                    `json:"merged"`
			Head       *collaboration.CommitID `json:"head"`
			Base       *collaboration.CommitID `json:"base"`
			MergeReady bool                    `json:"merge_ready"`
		} `json:"subject"`
	}
	pull := q.Subject == "pull"
	query := nativeRead{Kind: "write_capabilities", Repository: s.identity.SelectedRepository, Number: uint32(q.Number), WritePull: &pull}
	if err := s.read(ctx, query, &facts); err != nil {
		return result, nativeMissing(err, factoryservice.ErrCapabilitiesMissing)
	}
	if facts.Repository != s.identity.SelectedRepository || facts.Name == "" || (q.Subject == "repository") != (facts.Subject == nil) {
		return result, errNativeRead
	}
	result = collaboration.Capabilities{Repository: repo, Actor: actor, Connection: s.binding.Connection.ID, Observation: s.observation(facts), Actions: map[collaboration.Action]collaboration.Capability{}}
	set := func(state collaboration.Availability, reason string, actions ...collaboration.Action) {
		for _, action := range actions {
			result.Actions[action] = collaboration.Capability{State: state, Reason: reason}
		}
	}
	writes := []collaboration.Action{collaboration.CreateIssue, collaboration.EditIssue, collaboration.CommentIssue, collaboration.CreatePull, collaboration.EditPull, collaboration.CommentPull, collaboration.ReviewPull, collaboration.CommentReview, collaboration.ApproveReview, collaboration.RequestChangesReview, collaboration.ResolveThread, collaboration.MergePull}
	set(collaboration.Unknown, "requires_target_preparation", writes...)
	set(collaboration.Allowed, "", collaboration.ReadIssues, collaboration.ReadPulls, collaboration.ReadChecks, collaboration.ReadCheckJobs)
	write := facts.Writer
	if facts.Issues && facts.CreateIssues {
		set(collaboration.Allowed, "", collaboration.CreateIssue)
	} else {
		set(collaboration.Denied, "issues_unavailable", collaboration.CreateIssue)
	}
	if write {
		set(collaboration.Allowed, "", collaboration.CreatePull)
	}
	if target := facts.Subject; target != nil {
		if target.Kind != q.Subject || target.Number != q.Number || !nativeRemoteID(target.ExternalID) {
			return collaboration.Capabilities{}, errNativeRead
		}
		edit, comment := collaboration.EditIssue, collaboration.CommentIssue
		if pull {
			if target.Head == nil || target.Base == nil || !collaboration.ValidCommitID(*target.Head) || !collaboration.ValidCommitID(*target.Base) {
				return collaboration.Capabilities{}, errNativeRead
			}
			result.Pull = &collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: target.ExternalID, Number: target.Number}
			result.Head, result.Base = target.Head, target.Base
			edit, comment = collaboration.EditPull, collaboration.CommentPull
			if write && target.State == "open" && !target.Merged && !target.Locked {
				set(collaboration.Allowed, "", collaboration.ReviewPull, collaboration.CommentReview)
				if !target.Own && !target.Draft {
					set(collaboration.Allowed, "", collaboration.ApproveReview, collaboration.RequestChangesReview)
				}
				if !target.Draft && (facts.Merge || facts.Squash || facts.Rebase) && target.MergeReady {
					set(collaboration.Allowed, "", collaboration.MergePull)
				}
			}
			if target.Own {
				set(collaboration.Denied, "cannot_review_own_pull", collaboration.ApproveReview, collaboration.RequestChangesReview)
			}
			if target.Merged || target.State != "open" {
				set(collaboration.Denied, "pull_closed", collaboration.ReviewPull, collaboration.CommentReview, collaboration.ApproveReview, collaboration.RequestChangesReview, collaboration.MergePull)
			}
		} else {
			result.Issue = &collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: repo, ExternalID: target.ExternalID, Number: target.Number}
		}
		if target.CanUpdate {
			set(collaboration.Allowed, "", edit)
		} else {
			set(collaboration.Denied, "provider_cannot_update", edit)
		}
		if write && !target.Locked {
			set(collaboration.Allowed, "", comment)
		}
		if target.Locked {
			set(collaboration.Denied, "subject_locked", comment, collaboration.ReviewPull, collaboration.CommentReview, collaboration.ApproveReview, collaboration.RequestChangesReview)
		}
	}
	if facts.Archived || facts.Disabled {
		set(collaboration.Denied, "repository_read_only", writes...)
	}
	return result, nil
}
