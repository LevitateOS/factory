package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// externalPullBundle uses the existing authenticated source connector. It does
// not execute the checkout, renew the publication or infer a native send from
// the provider's current state. Both PR observations must match the admitted
// head/base and canonical identities before these bytes reach preparation.
func (p NativeExecution) externalPullBundle(ctx context.Context, source project.FactoryPullSource, base string) ([]byte,error) {
	publication,err:=p.Store.PublicationByID(ctx,source.Publication)
	if err!=nil || publication.Validate()!=nil || publication.External==nil || publication.WithdrawRequested || publication.Stage!=factory.PublicationPublished || !reflect.DeepEqual(publication.PullSource(),&source) || source.HeadRepository!=source.Repository || source.Validate()!=nil {
		return nil,execution.ErrPreparation
	}
	policy,err:=p.Store.RepositoryPolicy(ctx,source.Repository)
	if err!=nil || policy.Revision!=publication.External.Policy || policy.Review.NativeProfile!=publication.External.Profile || policy.Review.NativeConnection!=publication.External.Connection || publication.External.Profile!=p.Config.Profile {
		return nil,execution.ErrPreparation
	}
	reader:=NativeInputSource{p}
	work:=factory.ReviewWork{Repository:source.Repository,ActorID:policy.Review.ActorID,PRNumber:source.Number,PRID:source.PullID,IssueID:source.IssueID,PRAuthorID:source.AuthorID,HeadRef:source.HeadRef,BaseRef:source.BaseRef,HeadOID:source.Head,BaseOID:source.Base}
	reviews:=NativeReviews{reader}
	if _,err=reviews.ObserveReview(ctx,work); err!=nil { return nil,err }
	account,err:=reader.account(ctx,strconv.FormatInt(source.Repository,10),factory.OpReviewSubmit)
	if err!=nil { return nil,err }
	var fetched struct {
		Repository string `json:"repository"`
		Base string `json:"base"`
		Commit string `json:"commit"`
		Target string `json:"target"`
		SHA256 string `json:"sha256"`
		Bundle []byte `json:"bundle"`
	}
	if err=account.read(ctx,nativeRead{Kind:"source_bundle",Repository:account.repository.ExternalID,Base:&base,Commit:source.Head,TargetRef:source.HeadRef},&fetched); err!=nil { return nil,err }
	sum:=sha256.Sum256(fetched.Bundle)
	if fetched.Repository!=account.repository.ExternalID || fetched.Base!=base || fetched.Commit!=source.Head || fetched.Target!=source.HeadRef || len(fetched.Bundle)==0 || len(fetched.Bundle)>project.MaxSourceBundle || fetched.SHA256!=hex.EncodeToString(sum[:]) {
		return nil,execution.ErrPreparation
	}
	if _,err=reviews.ObserveReview(ctx,work); err!=nil { return nil,err }
	current,err:=p.Store.PublicationByID(ctx,source.Publication)
	if err!=nil || current.WithdrawRequested || current.Stage!=factory.PublicationPublished || !reflect.DeepEqual(current.PullSource(),&source) { return nil,execution.ErrPreparation }
	return fetched.Bundle,nil
}
