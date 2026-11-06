package runtime

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// ExecutionAccount reads only the enrolled native account's current nonsecret
// credential binding. It grants neither repository access nor spending authority.
func (p NativeExecution) ExecutionAccount(ctx context.Context, connection string) (factory.NativeSponsorship, error) {
	var empty factory.NativeSponsorship
	if !collaboration.ValidID(connection) {
		return empty, factoryservice.ErrSponsorshipConnectionMissing
	}
	var observed struct {
		factory.NativeSponsorship
		Connection string `json:"connection"`
	}
	if err := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation  string `json:"operation"`
		Connection string `json:"connection"`
	}{"inspect_account", connection}, &observed); err != nil {
		return empty, factoryservice.ErrSponsorshipBrokerUnavailable
	}
	if observed.Connection != connection || observed.Profile != p.Config.Profile || observed.NativeSponsorship.Validate() != nil {
		return empty, factoryservice.ErrSponsorshipBrokerUnavailable
	}
	return observed.NativeSponsorship, ctx.Err()
}

func (b *nativeProductAccount) sponsorship(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, connection string) (factoryservice.SponsorshipCustody, error) {
	var empty factoryservice.SponsorshipCustody
	if err := b.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	workspace, native, err := b.store.ExecutionWorkspace(ctx, repository)
	if err != nil || native == nil || native.Profile != b.port.operation.config.Profile {
		return empty, factoryservice.ErrSponsorshipGrantUnavailable
	}
	account, err := (NativeExecution{Config: b.port.operation.config, Instance: b.port.operation.instance}).ExecutionAccount(ctx, connection)
	if err != nil {
		return empty, err
	}
	if err := b.current(ctx); err != nil {
		return empty, err
	}
	return factoryservice.SponsorshipCustody{Repository: repository, Connection: connection, Project: workspace, Native: &account}, nil
}
