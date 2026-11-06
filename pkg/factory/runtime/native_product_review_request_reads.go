package runtime

import (
	"context"
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeRequestedReviewer struct {
	Kind         string  `json:"kind"`
	ExternalID   *string `json:"external_id"`
	NodeID       *string `json:"node_id"`
	DatabaseID   *string `json:"database_id"`
	Login        *string `json:"login"`
	Name         *string `json:"name"`
	Slug         *string `json:"slug"`
	Type         *string `json:"type"`
	Organization *struct {
		NodeID string `json:"node_id"`
		Login  string `json:"login"`
	} `json:"organization"`
}
type nativeReviewRequestsSnapshot struct {
	Repository       string `json:"repository"`
	Name             string `json:"name"`
	RepositoryNodeID string `json:"repository_node_id"`
	OwnerNodeID      string `json:"owner_node_id"`
	Pull             struct {
		Kind       string `json:"kind"`
		ExternalID string `json:"external_id"`
		NodeID     string `json:"node_id"`
		Number     int64  `json:"number"`
	} `json:"pull"`
	Items []struct {
		RequestID   string          `json:"request_id"`
		AsCodeOwner *bool           `json:"as_code_owner"`
		Reviewer    json.RawMessage `json:"reviewer"`
	} `json:"items"`
	AllTargetsVisible *bool           `json:"all_targets_visible"`
	SelectedTarget    json.RawMessage `json:"selected_target"`
	Requested         json.RawMessage `json:"requested"`
	Limits            *struct {
		Teams   *bool `json:"teams"`
		Maximum *int  `json:"maximum"`
	} `json:"limits"`
}

func nativeReviewNode(value string, bound int) bool {
	if !nativeObservedText(value, bound, false) {
		return false
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func nativeReviewerText(value *string, bound int) bool {
	return value != nil && nativeObservedText(*value, bound, false)
}

func (s nativeCollaborationReads) LookupReviewer(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, kind, name string) (collaboration.ReviewerLookup, error) {
	var empty collaboration.ReviewerLookup
	if !nativeIndex(number) {
		return empty, factoryservice.ErrReviewRequestReadQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrReviewRequestReadMissing); err != nil {
		return empty, err
	}
	var raw struct {
		Repository string                       `json:"repository"`
		Name       string                       `json:"name"`
		Pull       nativeSubjectRef             `json:"pull"`
		Actor      *collaboration.ObservedActor `json:"actor"`
		Team       *collaboration.ObservedTeam  `json:"team"`
	}
	if err := s.read(ctx, nativeRead{Kind: "reviewer_lookup", Repository: s.repository.ExternalID, Number: uint32(number), ReviewerKind: kind, Name: name}, &raw); err != nil {
		return empty, nativeMissing(err, factoryservice.ErrReviewRequestReadMissing)
	}
	if raw.Repository != s.repository.ExternalID || !nativeObservedText(raw.Name, 512, false) || raw.Pull.Kind != "pull" || raw.Pull.Number != number || !nativeRemoteID(raw.Pull.ExternalID) {
		return empty, errNativeRead
	}
	actors := []collaboration.ObservedActor{}
	teams := []collaboration.ObservedTeam{}
	if kind == "user" {
		if raw.Team != nil || raw.Actor == nil || !nativeActor(*raw.Actor) {
			return empty, errNativeRead
		}
		actors = append(actors, *raw.Actor)
	} else if kind == "team" {
		if raw.Actor != nil || raw.Team == nil || !(nativeReviewerTarget{Kind: "team", ExternalID: raw.Team.ExternalID}).valid() || !nativeObservedText(raw.Team.Name, 512, false) {
			return empty, errNativeRead
		}
		teams = append(teams, *raw.Team)
	} else {
		return empty, factoryservice.ErrReviewRequestReadQuery
	}
	mapping, err := s.store.ApplyCollaborationObservation(ctx, collaboration.ConnectionObservationBatch{Connection: nativeConnectionObservation(s.port, s.identity), Repository: repo, Repositories: []collaboration.ObservedRepository{{ExternalID: raw.Repository, Name: raw.Name}}, Actors: actors, Teams: teams})
	if err != nil {
		return empty, err
	}
	result := collaboration.ReviewerLookup{Pull: collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: raw.Pull.ExternalID, Number: number}, Observation: s.observation(raw)}
	if raw.Actor != nil {
		ref := mapping.Actors[raw.Actor.ExternalID]
		result.Actor = &ref
	}
	if raw.Team != nil {
		ref := mapping.Teams[raw.Team.ExternalID]
		result.Team = &ref
	}
	return result, nil
}

func (s nativeCollaborationReads) ReadReviewRequests(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64) (collaboration.PullReviewRequests, error) {
	var empty collaboration.PullReviewRequests
	if !nativeIndex(number) {
		return empty, factoryservice.ErrReviewRequestReadQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrReviewRequestReadMissing); err != nil {
		return empty, err
	}
	var raw nativeReviewRequestsSnapshot
	if err := s.read(ctx, nativeRead{Kind: "pull_review_requests", Repository: s.repository.ExternalID, Number: uint32(number)}, &raw); err != nil {
		return empty, nativeMissing(err, factoryservice.ErrReviewRequestReadMissing)
	}
	if raw.Limits == nil || raw.Limits.Teams == nil || raw.Limits.Maximum == nil || *raw.Limits.Maximum < 0 || *raw.Limits.Maximum > 1000 {
		return empty, errNativeRead
	}
	if raw.Repository != s.repository.ExternalID || !nativeObservedText(raw.Name, 512, false) || !nativeReviewNode(raw.RepositoryNodeID, 1024) || !nativeReviewNode(raw.OwnerNodeID, 1024) || raw.Pull.Kind != "pull" || raw.Pull.Number != number || !nativeRemoteID(raw.Pull.ExternalID) || !nativeReviewNode(raw.Pull.NodeID, 1024) || raw.Items == nil || len(raw.Items) > 1000 || raw.AllTargetsVisible == nil || string(raw.SelectedTarget) != "null" || string(raw.Requested) != "null" {
		return empty, errNativeRead
	}
	items := make([]collaboration.ReviewRequest, 0, len(raw.Items))
	actors := []collaboration.ObservedActor{}
	teams := []collaboration.ObservedTeam{}
	ids, nodes, actorIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	hidden := false
	for _, r := range raw.Items {
		if !nativeReviewNode(r.RequestID, 1024) || ids[r.RequestID] || r.AsCodeOwner == nil || len(r.Reviewer) == 0 {
			return empty, errNativeRead
		}
		ids[r.RequestID] = true
		item := collaboration.ReviewRequest{ExternalID: r.RequestID, AsCodeOwner: *r.AsCodeOwner}
		if string(r.Reviewer) == "null" {
			hidden = true
			items = append(items, item)
			continue
		}
		var target nativeRequestedReviewer
		if nativeDecode(r.Reviewer, &target) != nil {
			return empty, errNativeRead
		}
		node := ""
		if target.NodeID == nil {
			hidden = true
		} else {
			node = *target.NodeID
			if !nativeReviewNode(node, 1024) || nodes[node] {
				return empty, errNativeRead
			}
			nodes[node] = true
		}
		out := &collaboration.RequestedReviewer{Kind: target.Kind, ExternalID: target.ExternalID, NodeID: target.NodeID}
		switch target.Kind {
		case "user", "bot":
			if target.NodeID == nil || !nativeReviewerText(target.Login, 256) || target.Name != nil || target.Slug != nil || target.Type != nil || target.DatabaseID != nil || target.Organization != nil {
				return empty, errNativeRead
			}
			out.Label = *target.Login
			if target.ExternalID != nil {
				if !nativeRemoteID(*target.ExternalID) || actorIDs[*target.ExternalID] {
					return empty, errNativeRead
				}
				actorIDs[*target.ExternalID] = true
				actors = append(actors, collaboration.ObservedActor{ExternalID: *target.ExternalID, Login: out.Label})
			}
		case "team":
			if target.ExternalID == nil || *target.ExternalID != node || !(nativeReviewerTarget{Kind: "team", ExternalID: node}).valid() || !nativeReviewerText(target.Name, 512) || !nativeReviewerText(target.Slug, 512) || target.Login != nil || target.Type != nil || target.DatabaseID != nil && !nativeRemoteID(*target.DatabaseID) || target.Organization == nil || target.Organization.NodeID != raw.OwnerNodeID || !nativeObservedText(target.Organization.Login, 256, false) {
				return empty, errNativeRead
			}
			out.Label = *target.Name
			teams = append(teams, collaboration.ObservedTeam{ExternalID: *target.ExternalID, Name: out.Label})
		case "other":
			if !nativeReviewerText(target.Type, 128) || target.ExternalID != nil || target.Login != nil || target.Name != nil || target.Slug != nil || target.DatabaseID != nil || target.Organization != nil {
				return empty, errNativeRead
			}
			out.Label, out.ProviderKind = *target.Type, *target.Type
		default:
			return empty, errNativeRead
		}
		item.Reviewer = out
		items = append(items, item)
	}
	if *raw.AllTargetsVisible == hidden {
		return empty, errNativeRead
	}
	// Validate all provider facts before entering the one canonical transaction.
	// Unknown numeric IDs stay unmapped; Node IDs and nullable slots are retained.
	mapping, err := s.store.ApplyCollaborationObservation(ctx, collaboration.ConnectionObservationBatch{Connection: nativeConnectionObservation(s.port, s.identity), Repository: repo, Repositories: []collaboration.ObservedRepository{{ExternalID: raw.Repository, Name: raw.Name}}, Actors: actors, Teams: teams})
	if err != nil {
		return empty, err
	}
	for i := range items {
		t := items[i].Reviewer
		if t == nil || t.ExternalID == nil {
			continue
		}
		if t.Kind == "team" {
			ref := mapping.Teams[*t.ExternalID]
			t.Team = &ref
		} else {
			ref := mapping.Actors[*t.ExternalID]
			t.Actor = &ref
		}
	}
	return collaboration.PullReviewRequests{Pull: collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: raw.Pull.ExternalID, Number: number}, Observation: s.observation(raw), Items: items, AllTargetsVisible: *raw.AllTargetsVisible,
		Limits: &collaboration.ReviewRequestLimits{Teams: *raw.Limits.Teams, Maximum: *raw.Limits.Maximum}}, nil
}
