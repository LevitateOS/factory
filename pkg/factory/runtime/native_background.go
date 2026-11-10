package runtime

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

// NativeInputSource resolves the policy's exact saved provider account. Rust
// reacquires its finite custody lease and verifies the remote account on reads;
// a registered identity or cached observation supplies no provider authority.
type NativeInputSource struct{ NativeExecution }

func (p NativeInputSource) account(ctx context.Context, repository, kind string) (*nativeProductAccount, error) {
	var empty *nativeProductAccount
	id, err := strconv.ParseInt(repository, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != repository || p.Store == nil {
		return empty, errNativeReadScope
	}
	policy, err := p.Store.RepositoryPolicy(ctx, id)
	if err != nil {
		return empty, err
	}
	ref := policy.Publish
	if kind == factory.OpPRCreate {
		ref = policy.Create
	} else if kind == factory.OpMerge {
		ref = policy.Merge
	} else if kind == factory.OpReviewSubmit {
		ref = policy.Review
	} else if kind != factory.OpRefPublish {
		return empty, errNativeReadScope
	}
	if ref.Validate() != nil || ref.Kind != kind || ref.NativeProfile != p.Config.Profile || ref.NativeConnection == "" || ref.TokenID != 0 {
		return empty, errors.New("native background account is not configured")
	}
	account, err := p.savedAccount(ctx, id, collaboration.ActorID(ref.ActorID), ref.NativeConnection)
	if err == nil && account.binding.Host.Provider == collaboration.Fountain {
		var grant nativeControllerGrant
		grant, err = account.controllerGrant(ctx, collaboration.RepositoryID(id), ref.ControllerGrantID, kind)
		if err == nil {
			account.controller = &grant
		}
	}
	return account, err
}

// savedAccount keeps one canonical mapping/current-custody owner for policy
// work and explicitly captured native requests. It never selects a fallback.
func (p NativeInputSource) savedAccount(ctx context.Context, repository int64, actorID collaboration.ActorID, connectionID collaboration.ConnectionID) (*nativeProductAccount, error) {
	var empty *nativeProductAccount
	if p.Store == nil || repository <= 0 || actorID <= 0 || !collaboration.ValidID(string(connectionID)) {
		return empty, errNativeReadScope
	}
	connection, err := p.Store.CollaborationConnection(ctx, connectionID)
	if err != nil {
		return empty, err
	}
	host, err := p.Store.CollaborationHost(ctx, connection.Host)
	if err != nil {
		return empty, err
	}
	actor, err := p.Store.CollaborationActor(ctx, connection.Actor)
	if err != nil {
		return empty, err
	}
	repo, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(repository))
	if err != nil {
		return empty, err
	}
	profile, err := p.Store.CollaborationProfile(ctx, p.Config.Profile)
	if err != nil {
		return empty, err
	}
	if host.Provider != collaboration.GitHub && host.Provider != collaboration.Fountain || host.Provider == collaboration.GitHub && host.APIBase != "https://api.github.com" || connection.Profile != p.Config.Profile || connection.Actor != actorID || actor.Host != host.ID || repo.Host != host.ID || !nativeRemoteID(actor.ExternalID) || !nativeRemoteID(repo.ExternalID) {
		return empty, errNativeReadScope
	}
	scope := &nativeScope{Connection: NativeConnection{Connection: string(connection.ID), Provider: "github", Origin: "https://github.com", Account: actor.ExternalID}, Repository: repo.ExternalID}
	if host.Provider == collaboration.Fountain {
		scope.Connection.Provider, scope.Connection.Origin, scope.FountainRepository = "fountain", host.APIBase, repo.Name
		if !nativeConnectionValid(scope.Connection) {
			return empty, errNativeReadScope
		}
	}
	port := &NativeCollaboration{operation: nativeOperation{config: p.Config, instance: p.Instance}, background: scope}
	bound := &nativeProductAccount{store: p.Store, port: port, identity: NativeIdentity{Connection: scope.Connection, Account: collaboration.ObservedActor{ExternalID: actor.ExternalID, Login: actor.Login}, SelectedRepository: repo.ExternalID},
		binding: collaboration.ConnectionBinding{Host: host, Profile: profile, Actor: actor, Connection: connection}, repository: &repo}
	if err = bound.current(ctx); err != nil {
		return empty, err
	}
	return bound, nil
}
func (p NativeInputSource) ReadAcceptanceEvidence(ctx context.Context, repository, issue string, comments []string) (control.AcceptanceEvidence, error) {
	source, err := p.account(ctx, repository, factory.OpRefPublish)
	if err != nil {
		return control.AcceptanceEvidence{}, err
	}
	return (nativeIssueInputs{source}).ReadAcceptanceEvidence(ctx, repository, issue, comments)
}
func (p NativeInputSource) ReadDispatchInputs(ctx context.Context, repository, issue string, comments []string, target string) (control.DispatchInputs, error) {
	source, err := p.account(ctx, repository, factory.OpRefPublish)
	if err != nil {
		return control.DispatchInputs{}, err
	}
	return (nativeIssueInputs{source}).ReadDispatchInputs(ctx, repository, issue, comments, target)
}
