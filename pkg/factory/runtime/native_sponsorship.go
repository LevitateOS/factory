package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// ExecutionAccount reads the current native authentication source, without
// copying credentials or granting repository access or spending authority.
func (p NativeExecution) ExecutionAccount(ctx context.Context, connection string) (factory.NativeSponsorship, error) {
	var empty factory.NativeSponsorship
	if !collaboration.ValidID(connection) {
		return empty, factoryservice.ErrSponsorshipConnectionMissing
	}
	if source, err := p.MuseLoginSource(ctx); err == nil && source.Connection == connection {
		return source.Native, nil
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

func (p NativeExecution) MuseLoginSource(ctx context.Context) (factoryservice.SpendingSource, error) {
	var source factoryservice.SpendingSource
	if err := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation string `json:"operation"`
	}{"inspect_muse_login"}, &source); err != nil {
		return source, err
	}
	if !collaboration.ValidID(source.Connection) || source.Native.Profile != p.Config.Profile || source.Native.Authentication != factory.NativeMuseLogin || source.Native.Validate() != nil || source.Label != "This Muse login" {
		return factoryservice.SpendingSource{}, factoryservice.ErrSponsorshipBrokerUnavailable
	}
	if source.ToolStatus == nil {
		source.ExecutionAvailable = false
		source.JudgmentAvailable = false
	}
	if status := source.ToolStatus; status != nil {
		validVersion := func(version string) bool {
			return len(version) <= 32 && strings.IndexFunc(version, func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
			}) == -1
		}
		valid := validVersion(status.UpstreamVersion) && validVersion(status.SelectedVersion) && status.CheckedAtUnix >= 0 && status.CheckedAtUnix <= time.Now().Add(5*time.Second).Unix() && len(status.Error) <= 256 &&
			(status.Origin == "" || status.Origin == "installed" || status.Origin == "factory_cache")
		switch status.State {
		case "ready":
			valid = valid && status.SelectedVersion != "" && status.UpstreamVersion == status.SelectedVersion && status.CheckedAtUnix > 0 && status.Origin != "" && status.Error == ""
		case "checking":
		case "downloading":
			valid = valid && status.UpstreamVersion != ""
		case "unavailable":
			valid = valid && status.Error != ""
		default:
			valid = false
		}
		if !valid || (source.ExecutionAvailable || source.JudgmentAvailable) && status.State != "ready" {
			return factoryservice.SpendingSource{}, factoryservice.ErrSponsorshipBrokerUnavailable
		}
	}
	if len(source.JudgmentModels) > 32 {
		return factoryservice.SpendingSource{}, factoryservice.ErrSponsorshipBrokerUnavailable
	}
	for _, model := range source.JudgmentModels {
		if model == "" || len(model) > 128 || strings.IndexFunc(model, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return factoryservice.SpendingSource{}, factoryservice.ErrSponsorshipBrokerUnavailable
		}
	}
	return source, ctx.Err()
}

func (p NativeExecution) NativeSourceAvailable(ctx context.Context, authentication string) (bool, error) {
	if authentication != factory.NativeMuseLogin {
		return false, factoryservice.ErrSponsorshipBrokerUnavailable
	}
	source, err := p.MuseLoginSource(ctx)
	return source.ExecutionAvailable, err
}

func (b *nativeProductAccount) spendingSources(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) ([]factoryservice.SpendingSource, error) {
	if err := b.localResourceAccess(ctx, actor, repository); err != nil {
		return nil, err
	}
	source, err := (NativeExecution{Config: b.port.operation.config, Instance: b.port.operation.instance}).MuseLoginSource(ctx)
	if err != nil {
		return nil, err
	}
	// The same read-only workspace/placement check used by sponsorship admission.
	// An unavailable observation never becomes permission to save an allowance.
	_, workspace, workspaceErr := b.store.ExecutionWorkspace(ctx, repository)
	source.AllowanceAvailable = workspaceErr == nil && workspace != nil && workspace.Profile == source.Native.Profile
	if err = b.current(ctx); err != nil {
		return nil, err
	}
	return []factoryservice.SpendingSource{source}, nil
}

func (b *nativeProductAccount) spendingConnections(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (map[string]bool, error) {
	sources, err := b.spendingSources(ctx, actor, repository)
	if err != nil {
		return nil, err
	}
	owned := make(map[string]bool, len(sources))
	for _, source := range sources {
		owned[source.Connection] = true
	}
	return owned, nil
}

func (b *nativeProductAccount) sponsorship(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, connection string) (factoryservice.SponsorshipCustody, error) {
	var empty factoryservice.SponsorshipCustody
	if err := b.localResourceAccess(ctx, actor, repository); err != nil {
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
