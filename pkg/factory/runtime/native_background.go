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

func (p NativeInputSource) source(ctx context.Context, repository string) (nativeIssueInputs, error) {
	var empty nativeIssueInputs
	id, err := strconv.ParseInt(repository, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != repository || p.Store == nil {
		return empty, errNativeReadScope
	}
	policy, err := p.Store.RepositoryPolicy(ctx, id)
	if err != nil {
		return empty, err
	}
	ref := policy.Publish
	if ref.Validate() != nil || ref.Kind != factory.OpRefPublish || ref.NativeProfile != p.Config.Profile || ref.NativeConnection == "" || ref.TokenID != 0 {
		return empty, errors.New("native background account is not configured")
	}
	connection, err := p.Store.CollaborationConnection(ctx, ref.NativeConnection)
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
	repo, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(id))
	if err != nil {
		return empty, err
	}
	profile, err := p.Store.CollaborationProfile(ctx, p.Config.Profile)
	if err != nil {
		return empty, err
	}
	if host.Provider != collaboration.GitHub || host.APIBase != "https://api.github.com" || connection.Profile != p.Config.Profile || connection.Actor != collaboration.ActorID(ref.ActorID) || actor.Host != host.ID || repo.Host != host.ID || !nativeRemoteID(actor.ExternalID) || !nativeRemoteID(repo.ExternalID) {
		return empty, errNativeReadScope
	}
	scope := &nativeScope{Connection: NativeConnection{Connection: string(connection.ID), Provider: "github", Origin: "https://github.com", Account: actor.ExternalID}, Repository: repo.ExternalID}
	port := &NativeCollaboration{operation: nativeOperation{config: p.Config, instance: p.Instance}, background: scope}
	bound := &nativeProductAccount{store: p.Store, port: port, identity: NativeIdentity{Connection: scope.Connection, Account: collaboration.ObservedActor{ExternalID: actor.ExternalID, Login: actor.Login}, SelectedRepository: repo.ExternalID},
		binding: collaboration.ConnectionBinding{Host: host, Profile: profile, Actor: actor, Connection: connection}, repository: &repo}
	if err = bound.current(ctx); err != nil {
		return empty, err
	}
	return nativeIssueInputs{bound}, nil
}
func (p NativeInputSource) ReadAcceptanceEvidence(ctx context.Context, repository, issue string, comments []string) (control.AcceptanceEvidence, error) {
	source, err := p.source(ctx, repository)
	if err != nil {
		return control.AcceptanceEvidence{}, err
	}
	return source.ReadAcceptanceEvidence(ctx, repository, issue, comments)
}
func (p NativeInputSource) ReadDispatchInputs(ctx context.Context, repository, issue string, comments []string, target string) (control.DispatchInputs, error) {
	source, err := p.source(ctx, repository)
	if err != nil {
		return control.DispatchInputs{}, err
	}
	return source.ReadDispatchInputs(ctx, repository, issue, comments, target)
}
