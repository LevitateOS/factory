package runtime

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeCapabilityFacts struct {
	IssuesOnly   bool   `json:"issues_only"`
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
		Kind          string                  `json:"kind"`
		ExternalID    string                  `json:"external_id"`
		Number        int64                   `json:"number"`
		State         string                  `json:"state"`
		Locked        bool                    `json:"locked"`
		CanUpdate     bool                    `json:"can_update"`
		CanClose      bool                    `json:"can_close"`
		CanReopen     bool                    `json:"can_reopen"`
		Own           bool                    `json:"own"`
		Draft         bool                    `json:"draft"`
		Merged        bool                    `json:"merged"`
		Head          *collaboration.CommitID `json:"head"`
		Base          *collaboration.CommitID `json:"base"`
		InlinePublish *bool                   `json:"inline_publish"`
		DraftReview   *bool                   `json:"draft_review"`
		MergeReady    bool                    `json:"merge_ready"`
	} `json:"subject"`
}

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
	var facts nativeCapabilityFacts
	pull := q.Subject == "pull"
	query := nativeRead{Kind: "write_capabilities", Repository: s.identity.SelectedRepository, Number: uint32(q.Number), WritePull: &pull}
	if err := s.read(ctx, query, &facts); err != nil {
		return result, nativeMissing(err, factoryservice.ErrCapabilitiesMissing)
	}
	return s.capabilityProjection(actor, repo, q, facts)
}

func (s nativeCollaborationReads) capabilityProjection(actor collaboration.ActorID, repo collaboration.RepositoryID, q factoryservice.CapabilityQuery, facts nativeCapabilityFacts) (collaboration.Capabilities, error) {
	var result collaboration.Capabilities
	pull := q.Subject == "pull"
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
	if facts.IssuesOnly {
		set(collaboration.Denied, "issue_only_scope", collaboration.ReadPulls, collaboration.ReadChecks, collaboration.ReadCheckJobs,
			collaboration.CreatePull, collaboration.EditPull, collaboration.CommentPull, collaboration.ReviewPull, collaboration.CommentReview,
			collaboration.ApproveReview, collaboration.RequestChangesReview, collaboration.ResolveThread, collaboration.MergePull)
	}
	write := facts.Writer
	if facts.Issues && facts.CreateIssues {
		set(collaboration.Allowed, "", collaboration.CreateIssue)
	} else {
		set(collaboration.Denied, "issues_unavailable", collaboration.CreateIssue)
	}
	if write && !facts.IssuesOnly {
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
			if target.State == "open" && !target.Merged && !target.Locked {
				set(collaboration.Allowed, "", collaboration.ReviewPull, collaboration.CommentReview)
				if !target.Own && !target.Draft {
					set(collaboration.Allowed, "", collaboration.ApproveReview, collaboration.RequestChangesReview)
				}
				if write && !target.Draft && (facts.Merge || facts.Squash || facts.Rebase) && target.MergeReady {
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
		if !target.Locked {
			set(collaboration.Allowed, "", comment)
		}
		if target.Locked {
			set(collaboration.Denied, "subject_locked", comment, collaboration.ReviewPull, collaboration.CommentReview, collaboration.ApproveReview, collaboration.RequestChangesReview)
		}
	}
	if facts.Archived || facts.Disabled {
		set(collaboration.Denied, "repository_read_only", writes...)
	}
	if target := facts.Subject; pull && target != nil {
		fact := result.Actions[collaboration.CommentReview]
		if fact.State == collaboration.Allowed {
			if target.DraftReview == nil {
				fact = collaboration.Capability{State: collaboration.Unknown, Reason: "pending_summary_unobserved"}
			} else if !*target.DraftReview {
				fact = collaboration.Capability{State: collaboration.Unavailable, Reason: "pending_summary_empty"}
			}
		}
		result.ReviewDraft = &fact
		publish := result.Actions[collaboration.CommentReview]
		if publish.State == collaboration.Allowed {
			if target.InlinePublish == nil {
				publish = collaboration.Capability{State: collaboration.Unknown, Reason: "pending_review_unobserved"}
			} else if !*target.InlinePublish {
				publish = collaboration.Capability{State: collaboration.Unavailable, Reason: "pending_review_exists"}
			}
		}
		result.InlinePublish = &publish
	}
	if target := facts.Subject; target != nil {
		result.StateActions = map[string]collaboration.Capability{}
		for action, allowed := range map[string]bool{"close": target.CanClose, "reopen": target.CanReopen} {
			fact := collaboration.Capability{State: collaboration.Denied, Reason: "provider_cannot_change_state"}
			if allowed {
				fact = collaboration.Capability{State: collaboration.Allowed}
			}
			if facts.Archived || facts.Disabled {
				fact = collaboration.Capability{State: collaboration.Denied, Reason: "repository_read_only"}
			}
			result.StateActions[action] = fact
		}
	}
	if pull {
		result.MergeMethods = map[string]collaboration.Capability{}
		current := result.Actions[collaboration.MergePull]
		for method, enabled := range map[string]bool{"merge": facts.Merge, "squash": facts.Squash, "rebase": facts.Rebase, "rebase-merge": false, "fast-forward-only": false} {
			fact := collaboration.Capability{State: collaboration.Unsupported, Reason: "merge_method_unsupported"}
			if enabled {
				fact = current
			}
			result.MergeMethods[method] = fact
		}
	}
	return result, nil
}
