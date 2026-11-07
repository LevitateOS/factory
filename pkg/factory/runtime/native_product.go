package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryhttp "github.com/levitateos/factory/pkg/factory/httpapi"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

var errNativeWriteDenied = errors.New("native repository write denied")

// BindNativeProduct composes the shared product only from a live native effect
// port. Retained registry rows and HTTP request selectors cannot establish an
// account or expand its native repository scope. Construction starts no work.
func BindNativeProduct(ctx context.Context, application factoryservice.Application) (factoryhttp.Product, error) {
	var empty factoryhttp.Product
	if application.Store == nil || application.Coordinator == nil {
		return empty, errors.New("native product application unavailable")
	}
	port, err := NativeRequestPort(ctx)
	if err != nil {
		return empty, err
	}
	identity, err := port.Identity(ctx)
	if err != nil {
		return empty, err
	}
	batch := collaboration.ConnectionObservationBatch{Connection: nativeConnectionObservation(port, identity)}
	if identity.SelectedRepository != "" {
		observed, err := port.Repository(ctx, identity.SelectedRepository)
		if err != nil {
			return empty, err
		}
		if !sameNativeAccount(identity, observed.NativeIdentity) {
			return empty, errors.New("native account changed")
		}
		// Use the freshest provider-observed display name. Identity is its stable
		// account ID, not a login or repository name.
		identity = observed.NativeIdentity
		batch.Connection = nativeConnectionObservation(port, identity)
		batch.Repositories = []collaboration.ObservedRepository{{ExternalID: observed.Repository.ExternalID, Name: observed.Repository.Name}}
	}
	mapping, err := application.Store.ApplyCollaborationObservation(ctx, batch)
	if err != nil {
		return empty, err
	}
	bound := &nativeProductAccount{store: application.Store, port: port, identity: identity, binding: mapping.Binding}
	if identity.SelectedRepository != "" {
		if len(mapping.Repositories) != 1 || mapping.Repositories[0].ExternalID != identity.SelectedRepository || mapping.Repositories[0].Host != mapping.Binding.Host.ID {
			return empty, errors.New("native repository binding unavailable")
		}
		selected := mapping.Repositories[0]
		bound.repository = &selected
	}
	if err := bound.current(ctx); err != nil {
		return empty, err
	}
	posts := factoryservice.Posts{Store: application.Store, Visibility: func(ctx context.Context, actor collaboration.ActorID, repository int64) error {
		return bound.visible(ctx, actor, collaboration.RepositoryID(repository))
	}, WriteAccess: bound.writeAccess}
	product := factoryhttp.Product{Store: application.Store, Host: bound.binding.Host.ID, Actor: bound.binding.Actor.ID,
		Principal: strconv.FormatInt(int64(bound.binding.Actor.ID), 10), AuthorName: identity.Account.Login,
		Current: func(w http.ResponseWriter, r *http.Request) bool {
			if bound.current(r.Context()) == nil {
				return true
			}
			nativeProductUnavailable(w)
			return false
		},
		Feed:     factoryhttp.Feed{Service: factoryservice.Feed{Store: application.Store}, Posts: posts, Visibility: bound.visibility},
		Requests: factoryhttp.Requests{Service: factoryservice.Requests{Store: application.Store, Setup: bound.requestSetup, WriteAccess: bound.writeAccess}, Posts: posts, AuthorityError: nativeProductResourceError, VisibilityError: nativeProductResourceError},
		Work:     factoryhttp.Work{Reads: factoryservice.WorkReads{Store: application.Store, Visibility: bound.visible}, Issues: factoryservice.Issues{Store: application.Store, Visibility: bound.visible}, VisibilityError: nativeProductResourceError},
		Repositories: factoryhttp.Repositories{Store: application.Store, Visibility: bound.visible,
			Access: factoryservice.RepositoryAccess{Store: application.Store, Visibility: bound.visible,
				Viewer: bound.repositoryViewer, Preparation: bound.preparationStatus, Authority: bound.recordedAuthority,
				Connections: bound.spendingConnections},
			Status: factoryservice.RepositoryStatus{Store: application.Store, Visibility: bound.visible,
				Bindings: bound.policyBindings, PolicyOptions: bound.policyOptions, SpendingSources: bound.spendingSources, Preparation: bound.preparationStatus, Authority: bound.recordedAuthority},
			VisibilityError: nativeProductResourceError, AuthorityError: nativeProductResourceError},
		Runs:    factoryhttp.Runs{Store: application.Store, Visibility: bound.visible, Status: factoryservice.RunStatus{Store: application.Store}, VisibilityError: nativeProductResourceError, AuthorityError: nativeProductResourceError},
		Records: factoryhttp.Records{Library: factoryservice.Library{Source: application.Store, Visibility: bound.visible}},
	}
	reads := nativeCollaborationReads{bound}
	product.CollaborationIssues = factoryhttp.CollaborationIssues{Service: factoryservice.CollaborationIssues{Source: reads}}
	product.CollaborationPulls = factoryhttp.CollaborationPulls{Service: factoryservice.CollaborationPulls{Source: reads}}
	product.Discussions = factoryhttp.CollaborationDiscussions{Service: factoryservice.CollaborationDiscussions{Source: reads}}
	product.Reviews = factoryhttp.CollaborationReviews{Service: factoryservice.CollaborationReviews{Source: reads}}
	product.Threads = factoryhttp.CollaborationThreads{Service: factoryservice.CollaborationThreads{Source: reads}}
	product.PullFiles = factoryhttp.CollaborationPullFiles{Service: factoryservice.CollaborationPullFiles{Source: reads}}
	product.PullCommits = factoryhttp.CollaborationPullCommits{Service: factoryservice.CollaborationPullCommits{Source: reads}}
	product.Checks = factoryhttp.CollaborationChecks{Service: factoryservice.CollaborationChecks{Source: reads}}
	product.RepositoryCatalog = factoryhttp.CollaborationRepositories{Service: factoryservice.CollaborationRepositories{Source: reads}}
	product.CapabilityReads = factoryhttp.CollaborationCapabilities{Service: factoryservice.CollaborationCapabilities{Source: reads}}
	product.HumanComments = factoryhttp.HumanComments{Service: application.HumanComments(nativeHumanComments{bound})}
	product.HumanIssueCreates = factoryhttp.HumanIssueCreates{Service: application.HumanIssueCreates(nativeHumanIssueCreates{bound})}
	product.HumanBranchCreates = factoryhttp.HumanBranchCreates{Service: application.HumanBranchCreates(nativeHumanBranchCreates{bound})}
	product.HumanPullCreates = factoryhttp.HumanPullCreates{Service: application.HumanPullCreates(nativeHumanPullCreates{bound})}
	product.HumanReviews = factoryhttp.HumanReviews{Service: application.HumanReviews(nativeHumanReviews{bound})}
	product.HumanMerges = factoryhttp.HumanMerges{Service: application.HumanMerges(nativeHumanMerges{bound})}
	product.HumanInlines = factoryhttp.HumanInlines{Service: application.HumanInlines(nativeHumanInlines{bound})}
	product.HumanStates = factoryhttp.HumanStates{Service: application.HumanStates(nativeHumanStates{bound})}
	product.HumanEdits = factoryhttp.HumanEdits{Service: application.HumanEdits(nativeHumanEdits{bound})}
	product.HumanLocks = factoryhttp.HumanLocks{Service: application.HumanLocks(nativeHumanLocks{bound})}
	threads := application.HumanThreads(nativeHumanThreads{bound})
	threads.Reader = nativeHumanThreads{bound}
	product.HumanThreads = factoryhttp.HumanThreads{Service: threads}
	product.HumanReviewRequests = factoryhttp.HumanReviewRequests{Service: application.HumanReviewRequests(nativeHumanReviewRequests{bound})}
	product.ReviewRequestReads = factoryhttp.CollaborationReviewRequests{Service: factoryservice.CollaborationReviewRequests{Source: reads}}
	product.Repositories.Placements = factoryservice.Placements{Store: application.Store, Host: bound.binding.Host.ID,
		Runtime:     collaboration.RuntimeRef{ID: collaboration.RuntimeID(port.operation.config.Profile), Profile: port.operation.config.Profile, Kind: collaboration.Native, Name: "This computer"},
		AdminAccess: bound.localResourceAccess}
	product.Repositories.Runtime = factoryservice.RuntimeGrants{Writer: application.Coordinator, OperatorAccess: bound.localOwnerAccess,
		Visibility: bound.visible, EnvironmentAccess: bound.localResourceAccess, NativeEnvironment: bound.nativeEnvironment}
	product.Repositories.Sponsorships = factoryservice.Sponsorships{Store: application.Store, Writer: application.Coordinator, Visibility: bound.visible, Custody: bound.sponsorship}
	product.Repositories.Policies = factoryservice.Policies{Writer: application.Coordinator, BindingAccess: bound.policyBinding,
		WriteAccess: func(ctx context.Context, actor collaboration.ActorID, repository int64) error {
			return bound.localResourceAccess(ctx, actor, collaboration.RepositoryID(repository))
		}}
	// Capture/judgment, setup and native execution are
	// separate admitted dependencies. Their existing shared handlers explicitly
	// report unavailable. In particular, no feed intake or coordinator is started
	// by logging in, by reading stored work, or by saving an owner-private draft.
	return product, nil
}

func nativeProductUnavailable(w http.ResponseWriter) {
	nativeProductError(w, http.StatusServiceUnavailable, "authority_unavailable", "Current Factory account admission is unavailable.")
}
func nativeProductResourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrRepositoryHidden):
		nativeProductError(w, 404, "repository_not_found", "Repository is not visible.")
	case errors.Is(err, factoryservice.ErrRequestWriteUnavailable):
		nativeProductError(w, 503, "authority_unavailable", "Current repository write authority is unavailable.")
	case errors.Is(err, errNativeWriteDenied):
		nativeProductError(w, 403, "repository_write_required", "Current repository write access is required.")
	case errors.Is(err, factoryservice.ErrPlacementAdminRequired):
		nativeProductError(w, 403, "repository_admin_required", "Current repository administration access is required.")
	default:
		nativeProductUnavailable(w)
	}
}
func nativeProductError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func nativeConnectionObservation(port *NativeCollaboration, identity NativeIdentity) collaboration.ConnectionObservation {
	return collaboration.ConnectionObservation{Profile: collaboration.Profile{ID: port.operation.config.Profile, Name: "This computer"},
		Provider: collaboration.GitHub, APIBase: "https://api.github.com", Connection: collaboration.ConnectionID(identity.Connection.Connection),
		Account: identity.Account.ExternalID, Login: identity.Account.Login}
}
func sameNativeAccount(a, b NativeIdentity) bool {
	return a.Connection == b.Connection && a.Account.ExternalID == b.Account.ExternalID && a.SelectedRepository == b.SelectedRepository
}

type nativeProductAccount struct {
	store      *store.Store
	port       *NativeCollaboration
	identity   NativeIdentity
	binding    collaboration.ConnectionBinding
	repository *collaboration.RepositoryRef
}

func (b *nativeProductAccount) current(ctx context.Context) error {
	connection, err := b.port.Current(ctx)
	if err != nil {
		return err
	}
	if connection != b.identity.Connection {
		return errors.New("native account retired")
	}
	registered, err := b.store.CollaborationConnection(ctx, b.binding.Connection.ID)
	if err != nil {
		return err
	}
	if registered != b.binding.Connection {
		return errors.New("native canonical account changed")
	}
	return ctx.Err()
}
func (b *nativeProductAccount) repositoryObservation(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (NativeRepository, error) {
	var empty NativeRepository
	if actor != b.binding.Actor.ID || b.repository == nil || repository != b.repository.ID {
		return empty, factoryservice.ErrRepositoryHidden
	}
	registered, err := b.store.CollaborationRepositoryOnHost(ctx, b.binding.Host.ID, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, factoryservice.ErrRepositoryHidden
	}
	if err != nil {
		return empty, err
	}
	if registered.ExternalID != b.identity.SelectedRepository {
		return empty, factoryservice.ErrRepositoryHidden
	}
	observed, err := b.port.Repository(ctx, registered.ExternalID)
	if err != nil {
		var failure *NativePortError
		if errors.As(err, &failure) && (failure.Status == 403 && failure.Code == "native_repository_scope" || failure.Status == 404 && failure.Code == "github_not_visible") {
			return empty, factoryservice.ErrRepositoryHidden
		}
		return empty, err
	}
	if !sameNativeAccount(b.identity, observed.NativeIdentity) {
		return empty, errors.New("native account changed")
	}
	_, err = b.store.ApplyCollaborationObservation(ctx, collaboration.ConnectionObservationBatch{
		Connection: nativeConnectionObservation(b.port, observed.NativeIdentity), Repository: repository,
		Repositories: []collaboration.ObservedRepository{{ExternalID: observed.Repository.ExternalID, Name: observed.Repository.Name}},
	})
	if err != nil {
		return empty, err
	}
	return observed.Repository, ctx.Err()
}
func (b *nativeProductAccount) visibility(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (string, error) {
	observed, err := b.repositoryObservation(ctx, actor, repository)
	return observed.Name, err
}
func (b *nativeProductAccount) visible(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	_, err := b.visibility(ctx, actor, repository)
	return err
}
func (b *nativeProductAccount) writeAccess(ctx context.Context, actor collaboration.ActorID, repository int64) error {
	observed, err := b.repositoryObservation(ctx, actor, collaboration.RepositoryID(repository))
	if err != nil {
		return err
	}
	if observed.Push == nil {
		return factoryservice.ErrRequestWriteUnavailable
	}
	if !*observed.Push {
		return errNativeWriteDenied
	}
	return ctx.Err()
}
