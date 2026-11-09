package runtime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/strictjson"
)

// Only the authenticated native connector supplies this fresh central fact.
// The browser cannot select a Fountain placement in the standalone store.
func nativeFountainPlacement(p NativeExecution) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/native/collaboration/placement" || r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("X-Soda-Native-Instance")) != 1 || r.Header.Get("X-Soda-Native-Instance") != p.Instance {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_placement"})
			return
		}
		var input struct {
			Connection string                 `json:"connection"`
			Origin     string                 `json:"origin"`
			Actor      string                 `json:"actor"`
			Login      string                 `json:"login"`
			Repository string                 `json:"repository"`
			Name       string                 `json:"name"`
			Enrollment string                 `json:"enrollment"`
			Runtime    string                 `json:"runtime"`
			OwnerKind  string                 `json:"owner_kind"`
			OwnerID    string                 `json:"owner_id"`
			Generation int64                  `json:"generation,string"`
			State      factory.PlacementState `json:"state"`
			Revision   collaboration.Revision `json:"revision"`
		}
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 8192), &input) != nil || input.Runtime != string(p.Config.Profile) || input.OwnerKind != "desktop" || !collaboration.ValidID(input.Enrollment) || input.OwnerID != input.Enrollment {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_placement"})
			return
		}
		mapping, err := p.Store.ApplyCollaborationObservation(r.Context(), collaboration.ConnectionObservationBatch{
			Connection:   collaboration.ConnectionObservation{Profile: collaboration.Profile{ID: p.Config.Profile, Name: "This computer"}, Provider: collaboration.Fountain, APIBase: input.Origin, Connection: collaboration.ConnectionID(input.Connection), Account: input.Actor, Login: input.Login},
			Repositories: []collaboration.ObservedRepository{{ExternalID: input.Repository, Name: input.Name}},
		})
		if err != nil || len(mapping.Repositories) != 1 {
			nativeResponse(w, 409, map[string]string{"error": "canonical_placement_unavailable"})
			return
		}
		placement := factory.Placement{Host: mapping.Binding.Host.ID, Repository: mapping.Repositories[0].ID, Runtime: collaboration.RuntimeID(input.Runtime), Generation: input.Generation, State: input.State, Coordination: factory.FountainCentral, SelectedBy: mapping.Binding.Actor.ID, Revision: input.Revision, UpdatedAt: time.Now().UTC()}
		if err = p.Store.ObserveFountainPlacement(r.Context(), placement, p.Config.Profile); err != nil {
			nativeResponse(w, 409, map[string]string{"error": "central_placement_unavailable"})
			return
		}
		nativeResponse(w, 200, map[string]string{"revision": string(placement.Revision)})
	})
}

type NativeObservationStore interface {
	ApplyCollaborationObservation(context.Context, collaboration.ConnectionObservationBatch) (collaboration.ConnectionObservationMapping, error)
}

// The profile/provider/API locator come from native composition, never a webview
// selection. The caller must freshly verify these bounded nonsecret facts.
func NewNativeObservationHandler(profile collaboration.Profile, provider collaboration.Provider, base string, db NativeObservationStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/native/collaboration/observe" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || db == nil {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_observation"})
			return
		}
		// No legacy credential-carrying native route or header is accepted.
		if len(r.Header.Values("X-Soda-GitHub-Credential")) != 0 || len(r.Header.Values("X-Soda-GitHub-Refresh")) != 0 {
			nativeResponse(w, 400, map[string]string{"error": "credential_free_observation_required"})
			return
		}
		var q nativeObservation
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 2<<20), &q) != nil || q.validate() != nil {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_observation"})
			return
		}
		batch := collaboration.ConnectionObservationBatch{Connection: collaboration.ConnectionObservation{Profile: profile, Provider: provider, APIBase: base, Connection: collaboration.ConnectionID(q.Connection), Account: q.Account.ExternalID, Login: q.Account.Login}}
		if q.Repository != "" {
			id, _ := strconv.ParseInt(q.Repository, 10, 64)
			batch.Repository = collaboration.RepositoryID(id)
		}
		if q.Kind == "repositories" {
			batch.Repositories = q.Items
		}
		if q.Kind == "issues" {
			batch.Repositories = []collaboration.ObservedRepository{{ExternalID: q.Issues.Repository, Name: q.Issues.Name}}
			for _, item := range q.Issues.Items {
				if item.Author != nil {
					batch.Actors = append(batch.Actors, *item.Author)
				}
				batch.Actors = append(batch.Actors, item.Assignees...)
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		mapped, err := db.ApplyCollaborationObservation(ctx, batch)
		if err != nil {
			status, code := 503, "canonical_account_unavailable"
			if errors.Is(err, collaboration.ErrIdentityConflict) || errors.Is(err, collaboration.ErrUnboundLedger) {
				status, code = 409, "canonical_account_conflict"
			} else if errors.Is(err, sql.ErrNoRows) {
				status, code = 404, "canonical_repository_missing"
			}
			nativeResponse(w, status, map[string]string{"error": code})
			return
		}
		result := map[string]any{"binding": mapped.Binding}
		switch q.Kind {
		case "resolve_repository":
			result["repository"] = mapped.Repository
		case "repositories":
			next := ""
			if q.NextPage != 0 {
				next = strconv.Itoa(q.NextPage)
			}
			result["repositories"] = collaboration.Page[collaboration.RepositoryRef]{Observation: collaboration.Observation{State: collaboration.ReadAvailable, Revision: nativeObservationRevision(struct {
				Connection        collaboration.ConnectionRef
				Items             []collaboration.RepositoryRef
				Page, Limit, Next int
			}{mapped.Binding.Connection, mapped.Repositories, q.Page, q.Limit, q.NextPage}), ObservedAt: time.Now().UTC()}, Items: mapped.Repositories, Next: next, Complete: next == ""}
		case "issues":
			query := factoryservice.CollaborationIssueQuery{Limit: q.Limit, Number: q.Number, State: q.State}
			if q.Page != 1 && q.Number == 0 {
				query.Cursor = strconv.Itoa(q.Page)
			}
			page, err := (factoryservice.CollaborationIssues{Source: nativeObservedIssues{mapping: mapped, query: query, page: *q.Issues}}).Read(ctx, mapped.Binding.Actor.ID, batch.Repository, query)
			if err != nil {
				nativeResponse(w, 503, map[string]string{"error": "canonical_issue_unavailable"})
				return
			}
			result["issues"] = page
		}
		if ctx.Err() != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
}

type nativeObservation struct {
	Kind       string                             `json:"kind"`
	Connection string                             `json:"connection"`
	Account    collaboration.ObservedActor        `json:"account"`
	Repository string                             `json:"repository,omitempty"`
	Page       int                                `json:"page,omitempty"`
	Limit      int                                `json:"limit,omitempty"`
	NextPage   int                                `json:"next_page,omitempty"`
	Number     int64                              `json:"number,omitempty"`
	State      string                             `json:"state,omitempty"`
	Items      []collaboration.ObservedRepository `json:"items,omitempty"`
	Issues     *collaboration.ObservedIssuePage   `json:"issues,omitempty"`
}

func (q nativeObservation) validate() error {
	invalid := errors.New("invalid native observation")
	if !collaboration.ValidID(q.Connection) || !collaboration.ValidText(q.Account.ExternalID, 512) || !collaboration.ValidText(q.Account.Login, 512) {
		return invalid
	}
	if q.Repository != "" {
		id, err := strconv.ParseInt(q.Repository, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != q.Repository {
			return invalid
		}
	}
	page := func(next int) bool {
		return q.Page >= 1 && q.Page <= 1_000_000 && q.Limit >= 1 && q.Limit <= 50 && (next == 0 || next > q.Page && next <= 1_000_000)
	}
	switch q.Kind {
	case "binding":
		if q.Repository != "" || q.Page != 0 || q.Limit != 0 || q.NextPage != 0 || q.Number != 0 || q.State != "" || q.Items != nil || q.Issues != nil {
			return invalid
		}
	case "resolve_repository":
		if q.Repository == "" || q.Page != 0 || q.Limit != 0 || q.NextPage != 0 || q.Number != 0 || q.State != "" || q.Items != nil || q.Issues != nil {
			return invalid
		}
	case "repositories":
		if q.Repository != "" || !page(q.NextPage) || q.Number != 0 || q.State != "" || q.Items == nil || len(q.Items) > q.Limit || q.Issues != nil {
			return invalid
		}
		seen := map[string]bool{}
		for _, item := range q.Items {
			if !collaboration.ValidText(item.ExternalID, 512) || !collaboration.ValidText(item.Name, 512) || seen[item.ExternalID] {
				return invalid
			}
			seen[item.ExternalID] = true
		}
	case "issues":
		if q.Repository == "" || q.Items != nil || q.NextPage != 0 || q.Issues == nil || !page(q.Issues.NextPage) || q.Issues.Items == nil || len(q.Issues.Items) > q.Limit || !collaboration.ValidText(q.Issues.Repository, 512) || !collaboration.ValidText(q.Issues.Name, 512) {
			return invalid
		}
		query := factoryservice.CollaborationIssueQuery{Limit: q.Limit, Number: q.Number, State: q.State}
		if q.Number == 0 && q.Page != 1 {
			query.Cursor = strconv.Itoa(q.Page)
		}
		if query.Validate() != nil || q.Number > 0 && (q.Page != 1 || q.Issues.NextPage != 0 || len(q.Issues.Items) != 1) {
			return invalid
		}
		ids := map[string]bool{}
		numbers := map[int64]bool{}
		for _, item := range q.Issues.Items {
			if !collaboration.ValidText(item.ExternalID, 512) || item.Number < 1 || ids[item.ExternalID] || numbers[item.Number] || item.State != "open" && item.State != "closed" || q.Number > 0 && item.Number != q.Number || len(item.Assignees) > 1000 || len(item.Labels) > 1000 {
				return invalid
			}
			if q.Number == 0 && q.State != "all" {
				state := q.State
				if state == "" {
					state = "open"
				}
				if item.State != state {
					return invalid
				}
			}
			ids[item.ExternalID], numbers[item.Number] = true, true
			actors := item.Assignees
			if item.Author != nil {
				actors = append(append([]collaboration.ObservedActor{}, actors...), *item.Author)
			}
			for _, actor := range actors {
				if !collaboration.ValidText(actor.ExternalID, 512) || !collaboration.ValidText(actor.Login, 512) {
					return invalid
				}
			}
			for _, label := range item.Labels {
				if !collaboration.ValidText(label.ExternalID, 512) || label.Name == "" {
					return invalid
				}
			}
		}
	default:
		return invalid
	}
	return nil
}
func nativeObservationRevision(facts any) collaboration.Revision {
	encoded, _ := json.Marshal(facts)
	digest := sha256.Sum256(encoded)
	return collaboration.Revision(hex.EncodeToString(digest[:]))
}

// The service consumes already-observed nonsecret facts. It performs no provider
// I/O or SQL and cannot turn a registry association into an access capability.
type nativeObservedIssues struct {
	mapping collaboration.ConnectionObservationMapping
	query   factoryservice.CollaborationIssueQuery
	page    collaboration.ObservedIssuePage
}

func (s nativeObservedIssues) ReadIssues(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, q factoryservice.CollaborationIssueQuery) (collaboration.Page[collaboration.Issue], error) {
	var empty collaboration.Page[collaboration.Issue]
	b := s.mapping.Binding
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	if q != s.query || q.Validate() != nil || actor != b.Actor.ID || s.mapping.Repository == nil || repository != s.mapping.Repository.ID {
		return empty, factoryservice.ErrCollaborationIssueQuery
	}
	observed := time.Now().UTC()
	result := collaboration.Page[collaboration.Issue]{Observation: collaboration.Observation{State: collaboration.ReadAvailable, Revision: nativeObservationRevision(struct {
		Connection collaboration.ConnectionRef
		Repository collaboration.RepositoryID
		Query      factoryservice.CollaborationIssueQuery
		Page       collaboration.ObservedIssuePage
	}{b.Connection, repository, q, s.page}), ObservedAt: observed}, Items: []collaboration.Issue{}, Complete: s.page.NextPage == 0}
	if s.page.NextPage != 0 {
		result.Next = strconv.Itoa(s.page.NextPage)
	}
	for _, raw := range s.page.Items {
		item := collaboration.Issue{Ref: collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: repository, ExternalID: raw.ExternalID, Number: raw.Number}, Observation: collaboration.Observation{State: collaboration.ReadAvailable, Revision: nativeObservationRevision(struct {
			Connection collaboration.ConnectionRef
			Repository collaboration.RepositoryID
			Issue      collaboration.ObservedIssue
		}{b.Connection, repository, raw}), ObservedAt: observed}, Title: raw.Title, Body: raw.Body, State: raw.State, Locked: raw.Locked, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt, ClosedAt: raw.ClosedAt}
		if raw.Author != nil {
			author := s.mapping.Actors[raw.Author.ExternalID]
			item.Author = &author
		}
		if raw.Assignees != nil {
			item.Assignees = []collaboration.ActorRef{}
		}
		for _, actor := range raw.Assignees {
			item.Assignees = append(item.Assignees, s.mapping.Actors[actor.ExternalID])
		}
		if raw.Labels != nil {
			item.Labels = []collaboration.Label{}
		}
		for _, label := range raw.Labels {
			item.Labels = append(item.Labels, collaboration.Label{ExternalID: label.ExternalID, Name: label.Name, Color: label.Color})
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}
