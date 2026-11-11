package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

func (p NativePublication) observeCorrectionPull(ctx context.Context, account *nativeProductAccount, w factory.PublicationWork, publication factory.Publication, refs nativePublicationRefs) error {
	if target := publication.PullTarget(); target.HeadRepository != publication.Repository {
		policy, err := p.Store.RepositoryPolicy(ctx, publication.Repository)
		if err != nil {
			return err
		}
		observed, err := (NativeReviews{p.NativeInputSource}).ObserveReview(ctx, factory.ReviewWork{Repository: publication.Repository, HeadRepository: target.HeadRepository, ActorID: policy.Review.ActorID, PRNumber: publication.PRNumber, PRID: publication.PRID, IssueID: target.IssueID, PRAuthorID: target.AuthorID, HeadRef: publication.PublicationRef(), BaseRef: w.ComparisonRef, HeadOID: refs.Tip, BaseOID: refs.Comparison})
		if err != nil {
			return err
		}
		if observed.HeadRepository != target.HeadRepository || observed.HeadOID != refs.Tip || observed.BaseOID != refs.Comparison {
			return errNativeRead
		}
		return nil
	}
	var raw struct {
		Repository     string                            `json:"repository"`
		Number         int64                             `json:"number"`
		PullID         string                            `json:"pull_id"`
		IssueID        string                            `json:"issue_id"`
		Author         collaboration.ObservedActor       `json:"author"`
		HeadRepository *collaboration.ObservedRepository `json:"head_repository,omitempty"`
		HeadRef        string                            `json:"head_ref"`
		BaseRef        string                            `json:"base_ref"`
		HeadTip        string                            `json:"head_tip"`
		BaseTip        string                            `json:"base_tip"`
	}
	if err := account.read(ctx, nativeRead{Kind: "automation_review", Repository: account.repository.ExternalID, Number: uint32(w.CorrectionNumber)}, &raw); err != nil {
		return err
	}
	actor, err := p.Store.CollaborationActor(ctx, collaboration.ActorID(w.CorrectionAuthor))
	if err != nil {
		return err
	}
	if account.binding.Host.Provider == collaboration.GitHub && (raw.HeadRepository == nil || raw.HeadRepository.ExternalID != account.repository.ExternalID) {
		return errNativeRead
	}
	if raw.Repository != account.repository.ExternalID || raw.Number != publication.PRNumber || raw.PullID != strconv.FormatInt(publication.PRID, 10) || raw.IssueID != strconv.FormatInt(publication.PullTarget().IssueID, 10) || raw.Author.ExternalID != actor.ExternalID || raw.HeadRef != publication.PublicationRef() || raw.BaseRef != w.ComparisonRef || raw.HeadTip != refs.Tip || raw.BaseTip != refs.Comparison {
		return errNativeRead
	}
	return nil
}
func (p NativePublication) decodeRefCapture(ctx context.Context, account *nativeProductAccount, c factory.RefPublicationCapture) (nativeBranchCreatePreparation, error) {
	var prep nativeBranchCreatePreparation
	if _, err := c.Command(); err != nil {
		return prep, err
	}
	if nativeDecode(c.ProviderIntent, &prep) != nil || prep.Version != 3 || prep.Transfer == nil || !prep.Transfer.valid(c.Request) || prep.Transfer.ExpectedOld != c.ExpectedOld || prep.Branch.Repository != account.repository.ExternalID || prep.Branch.Name != c.Request.Name || prep.Branch.Commit != string(c.Request.Commit) || prep.Account != account.identity.Account.ExternalID || c.Authority != account.humanAuthority() || c.Request.Actor != account.binding.Actor.ID || c.Request.Repository != account.repository.ID || c.Request.Host != account.binding.Host.ID {
		return prep, errNativeReadScope
	}
	if !prep.Transfer.controllerMatches(account) {
		return prep, errNativeReadScope
	}
	if prep.Transfer.ComparisonRepository != "" && prep.Transfer.ComparisonRepository == prep.Branch.Repository {
		return prep, errNativeReadScope
	}
	return prep, account.current(ctx)
}
func nativeRefPublicationRecord(c factory.RefPublicationCapture) collaboration.OperationRecord {
	cmd, _ := c.Command()
	return collaboration.OperationRecord{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, Binding: &collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.PublishRef, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}, Effect: collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
}

func (p NativePublication) correctionBinding(ctx context.Context, account *nativeProductAccount, w factory.PublicationWork) (*nativeBranchCorrection, error) {
	publication, err := p.Store.PublicationByAssignment(ctx, w.AssignmentID)
	if err != nil {
		return nil, err
	}
	if publication.ID != w.Publication || publication.PRNumber != w.CorrectionNumber || publication.PRID <= 0 || publication.PullTarget().IssueID <= 0 {
		return nil, errNativeReadScope
	}
	fork := publication.PullTarget().HeadRepository != publication.Repository
	if account.binding.Host.Provider != collaboration.Fountain && !fork {
		return nil, nil
	}
	author, err := p.Store.CollaborationActor(ctx, collaboration.ActorID(w.CorrectionAuthor))
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(author.ExternalID, 10, 64)
	if err != nil || id <= 0 || author.Host != account.binding.Host.ID {
		return nil, errNativeReadScope
	}
	correction := &nativeBranchCorrection{Number: w.CorrectionNumber, ExpectedAuthorID: id, ExpectedPRID: publication.PRID, ExpectedIssueID: publication.PullTarget().IssueID}
	if fork {
		if account.binding.Host.Provider != collaboration.GitHub || publication.External == nil || publication.External.ForkConsent != publication.External.CommandID || int64(account.repository.ID) != publication.PullTarget().HeadRepository {
			return nil, errNativeReadScope
		}
		base, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(publication.Repository))
		if err != nil || base.Host != account.binding.Host.ID {
			return nil, errNativeReadScope
		}
		correction.Repository, correction.ForkConsent = base.ExternalID, publication.External.ForkConsent
	}
	return correction, nil
}

func (p NativePublication) submitCorrectionRef(ctx context.Context, w factory.PublicationWork) (factory.OperationOutcome, error) {
	var empty factory.OperationOutcome
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil || !prior.NotObserved {
		return prior, err
	}
	account, err := p.admitSubmit(ctx, w, factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	q, err := p.branchRequest(ctx, w)
	if err != nil {
		return empty, err
	}
	correction, err := p.correctionBinding(ctx, account, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.RefPublicationCapture(ctx, q, w.ExpectedOld)
	if errors.Is(err, store.ErrNotFound) {
		observed, e := p.ObservePublication(ctx, w)
		if e != nil {
			return empty, e
		}
		if observed.TargetTip != w.ExpectedOld || observed.Comparison != w.ComparisonOID || observed.NativeRev != w.NativeRev {
			return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
		}
		refs, e := p.refs(ctx, account, w)
		if e != nil {
			return empty, e
		}
		if refs.Tip != w.ExpectedOld || refs.Comparison != w.ComparisonOID {
			return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
		}
		sum := sha256.Sum256(w.Bundle)
		prep := nativeBranchCreatePreparation{Version: 3, Account: account.identity.Account.ExternalID, NotAfter: nativePublicationDeadline(w.NotAfter), Branch: nativeBranchCreateWrite{Repository: refs.Repository, RepositoryNodeID: refs.RepositoryNodeID, Name: q.Name, Commit: string(q.Commit)}, Transfer: &nativeBranchTransfer{Base: w.Run.InputSHA, ComparisonBranch: refs.ComparisonBranch, ComparisonOID: w.ComparisonOID, ComparisonRepository: refs.ComparisonRepository, BundleSHA256: hex.EncodeToString(sum[:]), ExpectedOld: w.ExpectedOld}}
		if err = prep.Transfer.captureController(account, &prep.NotAfter); err != nil {
			return empty, err
		}
		prep.Transfer.PullRequest = correction
		encoded, e := json.Marshal(prep)
		if e != nil {
			return empty, e
		}
		c = factory.RefPublicationCapture{Request: q, ExpectedOld: w.ExpectedOld, Authority: account.humanAuthority(), OperationID: q.ID, ProviderIntent: encoded}
		c, _, err = p.Store.CaptureRefPublication(ctx, c)
	}
	if err != nil {
		return empty, err
	}
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	lookup, err := p.Store.LookupHumanCommandDispatch(ctx, cmd)
	if err != nil {
		return empty, err
	}
	if lookup.Record != nil {
		return nativePublicationOutcome(w, factory.OpRefPublish, lookup), nil
	}
	if _, err = p.Store.ObserveRefPublication(ctx, c, lookup); err != nil {
		return empty, err
	}
	account, err = p.admitSubmit(ctx, w, factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	prep, err := p.decodeRefCapture(ctx, account, c)
	if err != nil {
		return empty, err
	}
	// Old captures retain their original meaning. Missing correction authority
	// cannot be added to a historical command before a delayed first dispatch.
	if correction != nil && (prep.Transfer.PullRequest == nil || *prep.Transfer.PullRequest != *correction) || correction == nil && prep.Transfer.PullRequest != nil {
		return empty, errNativeReadScope
	}
	if err = prep.Transfer.admitController(ctx, account, factory.OpRefPublish); err != nil {
		return empty, err
	}
	sum := sha256.Sum256(w.Bundle)
	if (prep.NotAfter <= 0 || prep.NotAfter > w.NotAfter) || prep.Transfer.Base != w.Run.InputSHA || prep.Transfer.ComparisonOID != w.ComparisonOID || prep.Transfer.BundleSHA256 != hex.EncodeToString(sum[:]) {
		return empty, errNativeRead
	}
	won, err := p.Store.ReserveRefPublicationDispatch(ctx, c)
	if err != nil {
		return empty, err
	}
	if !won {
		return p.LookupOp(ctx, w.OperationID)
	}
	write := nativeBranchPublishWrite{Repository: prep.Branch.Repository, Branch: prep.Branch.Name, ExpectedOld: c.ExpectedOld, Candidate: prep.Branch.Commit, nativeBranchTransfer: *prep.Transfer, Bundle: w.Bundle}
	reply, err := account.callHumanEffect(ctx, nativeBranchPublishEffect{Kind: "publish_branch", CommandID: c.OperationID, Digest: cmd.Digest, NotAfter: prep.NotAfter, Branch: write}, cmd, prep.Branch.Repository, errNativeRead)
	if err != nil {
		return empty, err
	}
	record, err := nativeRefPublicationOutcome(c, prep, reply)
	if err != nil {
		return empty, err
	}
	lookup = collaboration.OperationLookup{Host: q.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &record, Reason: record.Reason}
	lookup, err = p.Store.ObserveRefPublication(ctx, c, lookup)
	return nativePublicationOutcome(w, factory.OpRefPublish, lookup), err
}

func nativeRefPublicationOutcome(c factory.RefPublicationCapture, prep nativeBranchCreatePreparation, reply nativeCreationReply) (collaboration.OperationRecord, error) {
	var empty collaboration.OperationRecord
	record := nativeRefPublicationRecord(c)
	if reply.State == "applied" {
		if reply.Failure != nil {
			return empty, errNativeRead
		}
		if _, err := validateNativeBranchTransferReceipt(c.Request, prep, reply.Receipt); err != nil {
			return empty, err
		}
		record.Effect, record.Completion, record.Reason = collaboration.EffectCommitted, collaboration.CompletionComplete, "native_ref_published"
		record.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: nativeCreationSHA(c.ProviderIntent), Payload: reply.Receipt}
	} else if !reply.applyFailure(&record) {
		return empty, errNativeRead
	}
	return record, record.Validate()
}
