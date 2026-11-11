package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// externalPullBundle uses the existing authenticated source connector. It does
// not execute the checkout, renew the publication or infer a native send from
// the provider's current state. Both PR observations must match the admitted
// head/base and canonical identities before these bytes reach preparation.
func (p NativeExecution) externalPullBundle(ctx context.Context, source project.FactoryPullSource, base string) ([]byte, error) {
	publication, err := p.Store.PublicationByID(ctx, source.Publication)
	if err != nil || publication.Validate() != nil || publication.External == nil || publication.WithdrawRequested || publication.Stage != factory.PublicationPublished || !reflect.DeepEqual(publication.PullSource(), &source) || source.Validate() != nil {
		return nil, execution.ErrPreparation
	}
	policy, err := p.Store.RepositoryPolicy(ctx, source.Repository)
	if err != nil || policy.Revision != publication.External.Policy || policy.Review.NativeProfile != publication.External.Profile || policy.Review.NativeConnection != publication.External.Connection || publication.External.Profile != p.Config.Profile {
		return nil, execution.ErrPreparation
	}
	reader := NativeInputSource{p}
	work := factory.ReviewWork{Repository: source.Repository, HeadRepository: source.HeadRepository, ActorID: policy.Review.ActorID, PRNumber: source.Number, PRID: source.PullID, IssueID: source.IssueID, PRAuthorID: source.AuthorID, HeadRef: source.HeadRef, BaseRef: source.BaseRef, HeadOID: source.Head, BaseOID: source.Base}
	reviews := NativeReviews{reader}
	if _, err = reviews.ObserveReview(ctx, work); err != nil {
		return nil, err
	}
	account, err := reader.account(ctx, strconv.FormatInt(source.Repository, 10), factory.OpReviewSubmit)
	if err != nil {
		return nil, err
	}
	query := nativeRead{Kind: "source_bundle", Repository: account.repository.ExternalID, Base: &base, Commit: source.Head, TargetRef: source.HeadRef}
	if source.HeadRepository != source.Repository {
		if account.binding.Host.Provider != collaboration.GitHub || publication.External.ForkConsent != publication.ID || base != source.Base {
			return nil, execution.ErrPreparation
		}
		headRepository, err := p.Store.CollaborationRepositoryOnHost(ctx, account.binding.Host.ID, collaboration.RepositoryID(source.HeadRepository))
		if err != nil {
			return nil, err
		}
		query.Number, query.HeadRepository = uint32(source.Number), headRepository.ExternalID
	}
	var fetched struct {
		Repository     string `json:"repository"`
		Number         int64  `json:"number,omitempty"`
		HeadRepository string `json:"head_repository,omitempty"`
		Base           string `json:"base"`
		Commit         string `json:"commit"`
		Target         string `json:"target"`
		SHA256         string `json:"sha256"`
		Bundle         []byte `json:"bundle"`
	}
	if err = account.read(ctx, query, &fetched); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(fetched.Bundle)
	if fetched.Repository != account.repository.ExternalID || fetched.Base != base || fetched.Commit != source.Head || fetched.Target != source.HeadRef || len(fetched.Bundle) == 0 || len(fetched.Bundle) > project.MaxSourceBundle || fetched.SHA256 != hex.EncodeToString(sum[:]) {
		return nil, execution.ErrPreparation
	}
	if query.Number != 0 && (fetched.Number != source.Number || fetched.HeadRepository != query.HeadRepository) {
		return nil, execution.ErrPreparation
	}
	if _, err = reviews.ObserveReview(ctx, work); err != nil {
		return nil, err
	}
	current, err := p.Store.PublicationByID(ctx, source.Publication)
	if err != nil || current.WithdrawRequested || current.Stage != factory.PublicationPublished || !reflect.DeepEqual(current.PullSource(), &source) {
		return nil, execution.ErrPreparation
	}
	return fetched.Bundle, nil
}
