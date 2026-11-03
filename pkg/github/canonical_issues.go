package github

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// CanonicalIssueSource lives only inside the current native credential operation.
// It uses the existing SDK checks and neutral service contract. Registered rows
// are identity observations, never remote access or execution authority.
type CanonicalIssueSource struct {
	Client  *Client
	Scope   NativeAccountScope
	Binding collaboration.ConnectionBinding
}

var _ factoryservice.CollaborationIssueSource = CanonicalIssueSource{}

func (s CanonicalIssueSource) ReadIssues(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, q factoryservice.CollaborationIssueQuery) (resultPage collaboration.Page[collaboration.Issue], readErr error) {
	// Preserve provider failure classification for native transport while also
	// satisfying the neutral HTTP/service consumer's missing/query semantics.
	defer func() {
		if errors.Is(readErr, ErrNotVisible) {
			readErr = errors.Join(factoryservice.ErrCollaborationIssueMissing, readErr)
		} else if errors.Is(readErr, ErrInvalidRequest) {
			readErr = errors.Join(factoryservice.ErrCollaborationIssueQuery, readErr)
		}
	}()
	var empty collaboration.Page[collaboration.Issue]
	b := s.Binding
	if q.Validate() != nil || actor < 1 || repository < 1 || actor != b.Actor.ID || int64(int(q.Number)) != q.Number {
		return empty, ErrInvalidRequest
	}
	page := 1
	if q.Cursor != "" {
		var err error
		page, err = strconv.Atoi(q.Cursor)
		if err != nil || page < 1 || page > 1_000_000 || strconv.Itoa(page) != q.Cursor {
			return empty, ErrInvalidRequest
		}
	}
	if s.Client == nil || !s.Scope.valid() || b.Profile.ID != s.Scope.Profile.ID ||
		b.Host.Provider != collaboration.GitHub || b.Host.APIBase != "https://api.github.com" ||
		b.Connection.Profile != b.Profile.ID || b.Connection.Host != b.Host.ID ||
		b.Connection.Actor != actor || b.Actor.Host != b.Host.ID {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The source also verifies identity when used through the neutral service;
	// construction or a persisted binding cannot stand in for a current account.
	user, _, err := s.Client.AuthenticatedUser(ctx)
	if err != nil {
		return empty, err
	}
	if strconv.FormatInt(user.GetID(), 10) != b.Actor.ExternalID {
		return empty, ErrCredentials
	}
	fresh, err := s.Scope.bind(ctx, string(b.Connection.ID), b.Actor.ExternalID, user.GetLogin())
	if err != nil {
		return empty, err
	}
	if fresh.Connection != b.Connection {
		return empty, collaboration.ErrIdentityConflict
	}
	repo, err := s.Scope.Registry.CollaborationRepositoryOnHost(ctx, b.Host.ID, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotVisible
	}
	if err != nil {
		return empty, err
	}
	remote, err := strconv.ParseInt(repo.ExternalID, 10, 64)
	if err != nil || remote < 1 || strconv.FormatInt(remote, 10) != repo.ExternalID {
		return empty, ErrInvalidResponse
	}
	var native IssuePage
	if q.Number > 0 {
		native, err = s.Client.Issue(ctx, remote, int(q.Number))
	} else {
		native, err = s.Client.IssueList(ctx, remote, page, q.Limit, q.State)
	}
	if err != nil {
		return empty, err
	}
	// Re-observe only after current provider repository/locator checks succeeded.
	observedRepo, err := s.Scope.Registry.ObserveCollaborationConnectionRepository(ctx, fresh.Connection, native.Repository, native.Name)
	if err != nil {
		return empty, err
	}
	if observedRepo.ID != repository {
		return empty, collaboration.ErrIdentityConflict
	}
	observed := time.Now().UTC()
	result := collaboration.Page[collaboration.Issue]{
		Observation: collaboration.Observation{State: collaboration.ReadAvailable, Revision: canonicalRevision(struct {
			Connection collaboration.ConnectionRef
			Repository collaboration.RepositoryID
			Query      factoryservice.CollaborationIssueQuery
			Page       IssuePage
		}{fresh.Connection, repository, q, native}), ObservedAt: observed},
		Items: make([]collaboration.Issue, 0, len(native.Items)), Complete: native.NextPage == 0,
	}
	if native.NextPage != 0 {
		result.Next = strconv.Itoa(native.NextPage)
	}
	for _, record := range native.Items {
		item := collaboration.Issue{
			Ref: collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: repository, ExternalID: record.ExternalID, Number: int64(record.Number)},
			Observation: collaboration.Observation{State: collaboration.ReadAvailable, Revision: canonicalRevision(struct {
				Connection collaboration.ConnectionRef
				Repository collaboration.RepositoryID
				Issue      IssueRecord
			}{fresh.Connection, repository, record}), ObservedAt: observed},
			Title: record.Title, Body: record.Body, State: record.State, Locked: record.Locked,
			CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, ClosedAt: record.ClosedAt,
		}
		if record.Assignees != nil {
			item.Assignees = []collaboration.ActorRef{}
		}
		if record.Labels != nil {
			item.Labels = []collaboration.Label{}
		}
		if record.Author != nil {
			author, err := s.Scope.Registry.ObserveCollaborationActor(ctx, b.Host.ID, record.Author.ExternalID, record.Author.Login)
			if err != nil {
				return empty, err
			}
			item.Author = &author
		}
		for _, raw := range record.Assignees {
			ref, err := s.Scope.Registry.ObserveCollaborationActor(ctx, b.Host.ID, raw.ExternalID, raw.Login)
			if err != nil {
				return empty, err
			}
			item.Assignees = append(item.Assignees, ref)
		}
		for _, raw := range record.Labels {
			item.Labels = append(item.Labels, collaboration.Label{ExternalID: raw.ExternalID, Name: raw.Name, Color: raw.Color})
		}
		// Permission capabilities remain unknown until their dedicated current
		// actor/resource source observes them. Read access cannot mint mutations.
		result.Items = append(result.Items, item)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return result, nil
}
