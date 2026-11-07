package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

// NativeReviews binds automatic review to its registered workflow work. It uses
// the existing one-shot command journal and native creation owner, including
// their conservative unknown-outcome and cancellation behavior.
type NativeReviews struct{ NativeInputSource }

var _ control.ReviewExecutor = NativeReviews{}
var _ control.ReviewRecovery = NativeReviews{}

// ResumeReview uses the same native command reservation as a fresh submission.
// Missing/unclaimed capture is local proof of no send; reserved/unknown history
// returns its retained outcome and can never acquire another reservation.
func (p NativeReviews) ResumeReview(ctx context.Context, w factory.ReviewWork) (factory.OperationOutcome, error) {
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil || !prior.NotObserved {
		return prior, err
	}
	return p.SubmitReview(ctx, w)
}

func (p NativeReviews) request(ctx context.Context, w factory.ReviewWork) (factory.HumanReviewCommand, error) {
	var q factory.HumanReviewCommand
	repo, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(w.Repository))
	if err != nil {
		return q, err
	}
	host, err := p.Store.CollaborationHost(ctx, repo.Host)
	if err != nil {
		return q, err
	}
	if host.Provider != collaboration.GitHub || host.APIBase != "https://api.github.com" {
		return q, errNativeReadScope
	}
	event := map[string]factory.HumanReviewEvent{"COMMENT": factory.HumanReviewComment, "APPROVED": factory.HumanReviewApprove, "REQUEST_CHANGES": factory.HumanReviewRequestChanges}[w.Event]
	id := sha256.Sum256([]byte("factory.native.review\x00" + string(p.Config.Profile) + "\x00" + w.OperationID))
	q = factory.HumanReviewCommand{ID: hex.EncodeToString(id[:16]), Host: repo.Host, Repository: repo.ID, Actor: collaboration.ActorID(w.ActorID), Number: w.PRNumber, Body: w.Body, ExpectedHead: collaboration.CommitID(w.HeadOID), Event: event}
	return q, q.Validate()
}
func (p NativeReviews) registered(ctx context.Context, id string) (factory.Publication, factory.ReviewWork, error) {
	if p.Store == nil {
		return factory.Publication{}, factory.ReviewWork{}, errors.New("native review journal unavailable")
	}
	publication, op, err := p.Store.PublicationReviewOperation(ctx, id)
	return publication, op.Work, err
}
func (p NativeReviews) ObserveReview(ctx context.Context, w factory.ReviewWork) (factory.ReviewObservation, error) {
	var empty factory.ReviewObservation
	if w.ValidateTarget() != nil || !nativeIndex(w.PRNumber) {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	// Phased reviews render their typed verdict in the review body.
	// Historical inline work must keep waiting until its native batch owner exists.
	if len(w.Comments) != 0 {
		return empty, &factory.PublicationWait{Reason: "native_review_inline_unavailable"}
	}
	account, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpReviewSubmit)
	if err != nil {
		return empty, err
	}
	if int64(account.binding.Actor.ID) != w.ActorID {
		return empty, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	var raw struct {
		Repository string                      `json:"repository"`
		Number     int64                       `json:"number"`
		PullID     string                      `json:"pull_id"`
		IssueID    string                      `json:"issue_id"`
		Author     collaboration.ObservedActor `json:"author"`
		HeadRef    string                      `json:"head_ref"`
		BaseRef    string                      `json:"base_ref"`
		HeadTip    string                      `json:"head_tip"`
		BaseTip    string                      `json:"base_tip"`
	}
	if err = account.read(ctx, nativeRead{Kind: "automation_review", Repository: account.repository.ExternalID, Number: uint32(w.PRNumber)}, &raw); err != nil {
		return empty, err
	}
	if raw.Repository != account.repository.ExternalID || raw.Number != w.PRNumber || raw.PullID != strconv.FormatInt(w.PRID, 10) || raw.IssueID != strconv.FormatInt(w.IssueID, 10) || raw.HeadRef != w.HeadRef || raw.BaseRef != w.BaseRef || raw.HeadTip != w.HeadOID || raw.BaseTip != w.BaseOID || !nativeActor(raw.Author) {
		return empty, &factory.PublicationRefusal{Reason: "pr_mismatch"}
	}
	mapping, err := account.mapRead(ctx, raw.Repository, account.repository.Name, []collaboration.ObservedActor{raw.Author}, nil)
	if err != nil {
		return empty, err
	}
	if int64(mapping.Actors[raw.Author.ExternalID].ID) != w.PRAuthorID {
		return empty, &factory.PublicationRefusal{Reason: "pr_author_mismatch"}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return empty, err
	}
	revision, err := p.Store.ObserveNativeReview(ctx, w.Repository, w.PRNumber, inputDigest(encoded))
	if err != nil {
		return empty, err
	}
	return factory.ReviewObservation{NativeRev: revision, ObservedUnix: time.Now().Unix(), PRID: w.PRID, PRNumber: w.PRNumber, IssueID: w.IssueID, PRAuthorID: w.PRAuthorID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: raw.HeadTip, BaseOID: raw.BaseTip}, nil
}
func nativeReviewOutcome(w factory.ReviewWork, lookup collaboration.OperationLookup) factory.OperationOutcome {
	if lookup.Record == nil {
		return factory.OperationOutcome{OperationID: w.OperationID, NotObserved: true}
	}
	r := lookup.Record
	out := factory.OperationOutcome{OperationID: w.OperationID, InstallationID: r.Authority, Kind: factory.OpReviewSubmit, ActorID: w.ActorID, RepositoryID: w.Repository, Effect: string(r.Effect), Cancellation: string(r.Cancellation), Completion: string(r.Completion), Reason: r.Reason}
	if r.Receipt != nil {
		out.Receipt = append([]byte(nil), r.Receipt.Payload...)
	}
	return out
}
func (p NativeReviews) LookupOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	publication, w, err := p.registered(ctx, id)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	c, err := p.Store.HumanReviewCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		for _, op := range publication.ReviewOperations {
			if op.Work.OperationID == id && op.Outcome.OperationID != "" {
				return factory.OperationOutcome{}, errors.New("review dispatch history is not in this native journal")
			}
		}
		return factory.OperationOutcome{OperationID: id, NotObserved: true}, nil
	}
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	cmd, err := c.Command()
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	lookup, err := p.Store.LookupHumanCommandDispatch(ctx, cmd)
	return nativeReviewOutcome(w, lookup), err
}
func (p NativeReviews) SubmitReview(ctx context.Context, w factory.ReviewWork) (factory.OperationOutcome, error) {
	var empty factory.OperationOutcome
	publication, saved, err := p.registered(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !factory.ReviewWorkEqual(w, saved) || w.Validate() != nil || time.Now().Unix() >= w.NotAfter || publication.Stage != factory.PublicationPublished || publication.WithdrawRequested || publication.Candidate != w.HeadOID {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !prior.NotObserved {
		return prior, nil
	}
	policy, err := p.Store.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return empty, err
	}
	if !policy.Enabled || policy.Paused {
		return empty, &factory.PublicationWait{Reason: "policy_inactive"}
	}
	if err := nativeReviewVoteAuthority(policy, w); err != nil {
		return empty, err
	}
	open, _, _, err := p.Store.DispatchState(ctx, w.Repository)
	if err != nil {
		return empty, err
	}
	if !open {
		return empty, &factory.PublicationWait{Reason: "dispatch_closed"}
	}
	if _, err = p.ObserveReview(ctx, w); err != nil {
		return empty, err
	}
	account, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpReviewSubmit)
	if err != nil {
		return empty, err
	}
	if int64(account.binding.Actor.ID) != w.ActorID || policy.Review.NativeConnection != account.binding.Connection.ID || policy.Review.NativeProfile != p.Config.Profile {
		return empty, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanReviewCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		c, err = (nativeHumanNewReviews{account}).PrepareHumanReview(ctx, q)
		if err != nil {
			return empty, err
		}
		var version struct {
			Version int `json:"version"`
		}
		if json.Unmarshal(c.ProviderIntent, &version) != nil {
			return empty, errNativeRead
		}
		if version.Version != 1 {
			return empty, &factory.PublicationWait{Reason: "existing_pending_review"}
		}
		var prepared nativeReviewCreatePreparation
		if nativeDecode(c.ProviderIntent, &prepared) != nil {
			return empty, errNativeRead
		}
		prepared.NotAfter = min(prepared.NotAfter, w.NotAfter, time.Now().Add(5*time.Minute).Unix())
		prepared.Review.Base = w.BaseOID
		c.ProviderIntent, err = json.Marshal(prepared)
		if err != nil {
			return empty, err
		}
		c, _, err = p.Store.CaptureHumanReview(ctx, c)
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
		return nativeReviewOutcome(w, lookup), nil
	}
	var prepared nativeReviewCreatePreparation
	if nativeDecode(c.ProviderIntent, &prepared) != nil || prepared.Review.Base != w.BaseOID {
		return empty, &factory.PublicationWait{Reason: "native_review_base_unbound"}
	}
	if _, err = p.Store.ObserveHumanReview(ctx, c, lookup); err != nil {
		return empty, err
	}
	open, _, _, err = p.Store.DispatchState(ctx, w.Repository)
	if err != nil {
		return empty, err
	}
	if !open {
		return empty, &factory.PublicationWait{Reason: "dispatch_closed"}
	}
	live, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpReviewSubmit)
	if err != nil {
		return empty, err
	}
	if live.binding.Connection != account.binding.Connection {
		return empty, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	policy, err = p.Store.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return empty, err
	}
	if !policy.Enabled || policy.Paused {
		return empty, &factory.PublicationWait{Reason: "policy_inactive"}
	}
	if policy.Review.ActorID != w.ActorID || policy.Review.NativeConnection != live.binding.Connection.ID || policy.Review.NativeProfile != p.Config.Profile {
		return empty, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	if err := nativeReviewVoteAuthority(policy, w); err != nil {
		return empty, err
	}
	record, err := (nativeHumanNewReviews{account}).SubmitHumanReview(ctx, c)
	if err != nil {
		return empty, err
	}
	lookup = collaboration.OperationLookup{Host: q.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &record, Reason: record.Reason}
	lookup, err = p.Store.ObserveHumanReview(ctx, c, lookup)
	return nativeReviewOutcome(w, lookup), err
}

func nativeReviewVoteAuthority(policy factory.RepositoryPolicy, w factory.ReviewWork) error {
	if w.Event == "COMMENT" {
		return nil
	}
	if policy.AutomaticReviewEvent(w.PRAuthorID, w.Event) != w.Event || w.VoteBinding == nil || *w.VoteBinding != policy.Review {
		return &factory.PublicationRefusal{Reason: "automation_reviewer_not_authorized"}
	}
	return nil
}
func (p NativeReviews) CancelOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	var empty factory.OperationOutcome
	_, w, err := p.registered(ctx, id)
	if err != nil {
		return empty, err
	}
	if _, err = p.LookupOp(ctx, id); err != nil {
		return empty, err
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanReviewCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		actor, e := p.Store.CollaborationActor(ctx, q.Actor)
		if e != nil {
			return empty, e
		}
		c = factory.HumanReviewCapture{Request: q, Authority: "native-github:" + string(p.Config.Profile) + ":" + actor.ExternalID, OperationID: q.ID, ProviderIntent: []byte(`{"cancelled_before_preparation":true}`)}
		c, _, err = p.Store.CaptureHumanReview(ctx, c)
	}
	if err != nil {
		return empty, err
	}
	lookup, err := p.Store.CancelHumanReviewDispatch(ctx, c)
	return nativeReviewOutcome(w, lookup), err
}
func (p NativeReviews) AdoptReview(ctx context.Context, w factory.ReviewWork, outcome factory.OperationOutcome) (factory.ReviewOutcome, error) {
	var empty factory.ReviewOutcome
	_, saved, err := p.registered(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !factory.ReviewWorkEqual(w, saved) || len(w.Comments) != 0 {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(prior, outcome) || outcome.Effect != factory.OpEffectCommitted || outcome.Completion != factory.OpCompletionComplete {
		return empty, errors.New("native review has no attributable completed receipt")
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanReviewCapture(ctx, q)
	if err != nil {
		return empty, err
	}
	var prepared nativeReviewCreatePreparation
	if nativeDecode(c.ProviderIntent, &prepared) != nil {
		return empty, errNativeRead
	}
	receipt, err := validateNativeReviewCreateReceipt(c, prepared, outcome.Receipt)
	if err != nil {
		return empty, err
	}
	id, err := strconv.ParseInt(receipt.Review.ExternalID, 10, 64)
	if err != nil {
		return empty, err
	}
	return factory.ReviewOutcome{Operation: outcome, ReviewID: id, ReviewerID: w.ActorID, PRID: w.PRID, PRNumber: w.PRNumber, IssueID: w.IssueID, HeadOID: w.HeadOID, BaseOID: receipt.Base, CommitID: receipt.Review.Commit, MergeBaseOID: receipt.DiffBase, Event: w.Event}, nil
}
