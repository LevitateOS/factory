package runtime

import (
	"context"
	"errors"
	"strings"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeCollaborationReads struct{ *nativeProductAccount }

func (s nativeCollaborationReads) ReadIssues(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, q factoryservice.CollaborationIssueQuery) (collaboration.Page[collaboration.Issue], error) {
	var empty collaboration.Page[collaboration.Issue]
	if q.Validate() != nil || q.Number != 0 && !nativeIndex(q.Number) {
		return empty, factoryservice.ErrCollaborationIssueQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrCollaborationIssueMissing); err != nil {
		return empty, err
	}
	kind := "issues"
	if q.Number > 0 {
		kind = "issue"
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: kind, Repository: s.identity.SelectedRepository, Number: q.Number, State: q.State, Limit: q.Limit})
	if err != nil {
		return empty, factoryservice.ErrCollaborationIssueQuery
	}
	query := nativeRead{Kind: kind, Repository: c.Repository, Number: uint32(q.Number)}
	if q.Number == 0 {
		query.Page, query.Limit, query.State = uint32(c.Page), uint32(c.Limit), &q.State
	}
	var raw collaboration.ObservedIssuePage
	if err := s.read(ctx, query, &raw); err != nil {
		return empty, nativeMissing(err, factoryservice.ErrCollaborationIssueMissing)
	}
	observation := nativeObservation{Kind: "issues", Connection: s.identity.Connection.Connection, Account: s.identity.Account, Repository: decimalRepository(repo), Page: c.Page, Limit: c.Limit, Number: q.Number, State: q.State, Issues: &raw}
	if raw.Repository != c.Repository || observation.validate() != nil {
		return empty, errNativeRead
	}
	actors := []collaboration.ObservedActor{}
	for _, item := range raw.Items {
		actors = appendNativeActor(actors, item.Author)
		actors = append(actors, item.Assignees...)
	}
	mapping, err := s.mapRead(ctx, raw.Repository, raw.Name, actors, nil)
	if err != nil {
		return empty, err
	}
	// Reuse the defining canonical issue projector, also used by the native
	// observation command. Its numeric provider cursor stays internal here.
	projected, err := (nativeObservedIssues{mapping: mapping, query: q, page: raw}).ReadIssues(ctx, actor, repo, q)
	if err != nil {
		return empty, err
	}
	projected.Next = cursorEncode(c, raw.NextPage)
	projected.Complete = projected.Next == ""
	return projected, nil
}
func (s nativeCollaborationReads) ReadPulls(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, q factoryservice.CollaborationPullQuery) (collaboration.Page[collaboration.PullRequest], error) {
	var empty collaboration.Page[collaboration.PullRequest]
	if q.Validate() != nil || q.Number != 0 && !nativeIndex(q.Number) {
		return empty, factoryservice.ErrCollaborationPullQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrCollaborationPullMissing); err != nil {
		return empty, err
	}
	kind := "pulls"
	if q.Number > 0 {
		kind = "pull"
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: kind, Repository: s.identity.SelectedRepository, Number: q.Number, State: q.State, Limit: q.Limit})
	if err != nil {
		return empty, factoryservice.ErrCollaborationPullQuery
	}
	query := nativeRead{Kind: kind, Repository: c.Repository, Number: uint32(q.Number)}
	if q.Number == 0 {
		query.Page, query.Limit, query.State = uint32(c.Page), uint32(c.Limit), &q.State
	}
	var raw struct {
		Repository string       `json:"repository"`
		Name       string       `json:"name"`
		Items      []nativePull `json:"items"`
		NextPage   int          `json:"next_page"`
	}
	if err := s.read(ctx, query, &raw); err != nil {
		return empty, nativeMissing(err, factoryservice.ErrCollaborationPullMissing)
	}
	if raw.Repository != c.Repository || raw.Items == nil || !validNativePage(raw.NextPage, c.Page, c.Limit, len(raw.Items), false) || q.Number > 0 && (len(raw.Items) != 1 || raw.NextPage != 0) {
		return empty, errNativeRead
	}
	var actors []collaboration.ObservedActor
	var refs []collaboration.ObservedRepository
	ids := map[string]bool{}
	numbers := map[int64]bool{}
	for _, item := range raw.Items {
		a, r, err := nativePullFacts(item)
		if err != nil || ids[item.ExternalID] || numbers[item.Number] || q.Number > 0 && item.Number != q.Number {
			return empty, errNativeRead
		}
		if q.Number == 0 && q.State != "all" {
			open := q.State == "" || q.State == "open"
			if open != (item.State == "open") {
				return empty, errNativeRead
			}
		}
		ids[item.ExternalID], numbers[item.Number] = true, true
		actors = append(actors, a...)
		refs = append(refs, r...)
	}
	mapping, err := s.mapRead(ctx, raw.Repository, raw.Name, actors, refs)
	if err != nil {
		return empty, err
	}
	obs := s.observation(raw)
	items := make([]collaboration.PullRequest, 0, len(raw.Items))
	for _, item := range raw.Items {
		items = append(items, collaboration.PullRequest{Ref: nativePullRef(item.ObservedIssue, repo), Observation: s.observation(item), Title: item.Title, Body: item.Body, State: item.State, Draft: item.Draft,
			Author: nativeActorRef(mapping, item.Author), Base: nativeBranchRef(item.Base, mapping), Head: nativeBranchRef(item.Head, mapping), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, MergedAt: item.MergedAt})
	}
	return nativePage(obs, items, cursorEncode(c, raw.NextPage)), nil
}
func (s nativeCollaborationReads) ReadRepositories(ctx context.Context, actor collaboration.ActorID, q factoryservice.RepositoryCatalogQuery) (collaboration.Page[collaboration.RepositoryRef], error) {
	var empty collaboration.Page[collaboration.RepositoryRef]
	if q.Validate() != nil || q.Cursor != "" {
		return empty, factoryservice.ErrRepositoryCatalogQuery
	}
	if actor != s.binding.Actor.ID {
		return empty, factoryservice.ErrRepositoryCatalogMissing
	}
	items := []collaboration.RepositoryRef{}
	if s.repository != nil {
		observed, err := s.repositoryObservation(ctx, actor, s.repository.ID)
		if err != nil {
			return empty, err
		}
		if q.Query == "" || strings.Contains(strings.ToLower(observed.Name), strings.ToLower(q.Query)) {
			ref := *s.repository
			ref.Name = observed.Name
			items = append(items, ref)
		}
	}
	// Complete applies to the finite native operation scope. Account-wide
	// provider discovery is the native catalogue used to establish that scope.
	return nativePage(s.observation(items), items, ""), nil
}
func (s nativeCollaborationReads) ReadCapabilities(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, q factoryservice.CapabilityQuery) (collaboration.Capabilities, error) {
	var result collaboration.Capabilities
	if q.Validate() != nil {
		return result, factoryservice.ErrCapabilitiesQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrCapabilitiesMissing); err != nil {
		return result, err
	}
	result = collaboration.Capabilities{Repository: repo, Actor: actor, Connection: s.binding.Connection.ID, Actions: map[collaboration.Action]collaboration.Capability{}}
	actions := []collaboration.Action{collaboration.ReadIssues, collaboration.CreateIssue, collaboration.EditIssue, collaboration.CommentIssue, collaboration.ReadPulls, collaboration.CreatePull, collaboration.EditPull, collaboration.CommentPull, collaboration.ReviewPull, collaboration.CommentReview, collaboration.ApproveReview, collaboration.RequestChangesReview, collaboration.ResolveThread, collaboration.MergePull, collaboration.ReadChecks, collaboration.ReadCheckJobs}
	for _, action := range actions {
		result.Actions[action] = collaboration.Capability{State: collaboration.Unavailable, Reason: "native_mutation_ports_unavailable"}
	}
	for _, action := range []collaboration.Action{collaboration.ReadIssues, collaboration.ReadPulls, collaboration.ReadChecks} {
		result.Actions[action] = collaboration.Capability{State: collaboration.Unknown, Reason: "read_requires_current_observation"}
	}
	result.Actions[collaboration.ReadCheckJobs] = collaboration.Capability{State: collaboration.Unavailable, Reason: "native_job_reader_unavailable"}
	switch q.Subject {
	case "issue":
		page, err := s.ReadIssues(ctx, actor, repo, factoryservice.CollaborationIssueQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return collaboration.Capabilities{}, err
		}
		ref := page.Items[0].Ref
		result.Issue = &ref
		result.Actions[collaboration.ReadIssues] = collaboration.Capability{State: collaboration.Allowed}
		result.Observation = s.observation(page)
	case "pull":
		page, err := s.ReadPulls(ctx, actor, repo, factoryservice.CollaborationPullQuery{Number: q.Number, Limit: 1})
		if err != nil {
			return collaboration.Capabilities{}, err
		}
		pull := page.Items[0]
		result.Pull = &pull.Ref
		result.Head = pull.Head.Commit
		result.Base = pull.Base.Commit
		result.Actions[collaboration.ReadPulls] = collaboration.Capability{State: collaboration.Allowed}
		result.Observation = s.observation(page)
	default:
		raw, err := s.repositoryObservation(ctx, actor, repo)
		if err != nil {
			return collaboration.Capabilities{}, err
		}
		result.Observation = s.observation(raw)
	}
	return result, nil
}
func nativeMissing(err, missing error) error {
	var failure *NativePortError
	if errors.As(err, &failure) && failure.Status == 404 {
		return missing
	}
	return err
}
