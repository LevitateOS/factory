package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// NativeMerges joins the canonical merge intent to the existing provider command
// journal. Recovery/adoption read that journal without acquiring credentials.
type NativeMerges struct{ NativeInputSource }

var _ control.MergeExecutor = NativeMerges{}

// CheckUnsentMerge reads the command journal before a new merge preparation.
// Normal LookupOp requires a registered operation; an undriven row has none.
// Any existing command, including an unknown or pending capture, refuses.
func (p NativeMerges) CheckUnsentMerge(ctx context.Context, w factory.MergeWork) error {
	q, err := p.request(ctx, w)
	if err != nil {
		return err
	}
	if _, err = p.Store.FactoryCommand(ctx, q.ID); errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return store.ErrMergeConflict
}

func (p NativeMerges) request(ctx context.Context, w factory.MergeWork) (factory.HumanMergeCommand, error) {
	repo, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(w.Repository))
	if err != nil {
		return factory.HumanMergeCommand{}, err
	}
	message := ""
	if w.Method != "rebase" {
		message = fmt.Sprintf("Merge Factory pull request #%d", w.PRNumber)
	}
	q := factory.HumanMergeCommand{ID: nativePublicationCommandID(string(p.Config.Profile), w.OperationID), Host: repo.Host, Repository: repo.ID, Actor: collaboration.ActorID(w.ActorID), Number: w.PRNumber, Method: w.Method, Message: message, ExpectedHead: collaboration.CommitID(w.HeadOID)}
	return q, q.Validate()
}

func mergeWorkOf(m factory.Merge) factory.MergeWork {
	w := factory.MergeWork{MergeID: m.ID, PublicationID: m.PublicationID, Issue: m.Issue}
	if m.Operation.Work != nil {
		w = m.Operation.Work.Apply(w)
	}
	return w
}

func (p NativeMerges) registered(ctx context.Context, id string) (factory.Merge, factory.MergeWork, error) {
	m, err := p.Store.MergeByOperation(ctx, id)
	if err != nil {
		return m, factory.MergeWork{}, err
	}
	if m.Operation.Work == nil {
		return m, factory.MergeWork{}, errNativeRead
	}
	return m, mergeWorkOf(m), nil
}

func nativeMergeOutcome(w factory.MergeWork, lookup collaboration.OperationLookup) factory.OperationOutcome {
	if lookup.Record == nil {
		return factory.OperationOutcome{OperationID: w.OperationID, NotObserved: true}
	}
	r := lookup.Record
	out := factory.OperationOutcome{OperationID: w.OperationID, InstallationID: r.Authority, Kind: factory.OpMerge, ActorID: w.ActorID, RepositoryID: w.Repository, Effect: string(r.Effect), Cancellation: string(r.Cancellation), Completion: string(r.Completion), Reason: r.Reason}
	if r.Receipt != nil {
		out.Receipt = append([]byte(nil), r.Receipt.Payload...)
	}
	return out
}

func (p NativeMerges) LookupOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	m, w, err := p.registered(ctx, id)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	c, err := p.Store.HumanMergeCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		if m.Operation.Effect != "" {
			return factory.OperationOutcome{}, errors.New("merge dispatch history is not in this native journal")
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
	if err == nil && lookup.Record != nil && lookup.Record.Effect == collaboration.EffectIndeterminate && lookup.Record.Receipt == nil {
		account, e := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpMerge)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		lookup, err = (nativeHumanMerges{account}).LookupHumanMerge(ctx, c)
	}
	return nativeMergeOutcome(w, lookup), err
}

func (p NativeMerges) ObserveMerge(ctx context.Context, w factory.MergeWork) (factory.MergeObservation, error) {
	var empty factory.MergeObservation
	if w.ValidateTarget() != nil || !nativeIndex(w.PRNumber) {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	if w.Method == factory.MergeFastForward {
		return empty, &factory.PublicationWait{Reason: "native_merge_method_unavailable"}
	}
	account, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpMerge)
	if err != nil {
		return empty, err
	}
	if int64(account.binding.Actor.ID) != w.ActorID {
		return empty, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	before, err := p.ReadAcceptanceEvidence(ctx, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
	if err != nil {
		return empty, err
	}
	publication, err := p.Store.PublicationByID(ctx, w.PublicationID)
	if err != nil {
		return empty, err
	}
	var review factory.ReviewOutcome
	for i := len(publication.ReviewOperations) - 1; i >= 0; i-- {
		op := publication.ReviewOperations[i]
		if op.Work.ActorID == w.ReviewerID && op.Work.PRID == w.PRID && op.Work.HeadOID == w.HeadOID && op.Work.BaseOID == w.BaseOID && op.Work.Disposition() == "APPROVED" {
			if _, err = (NativeReviews{p.NativeInputSource}).ObserveReview(ctx, op.Work); err != nil {
				return empty, err
			}
			review, err = (NativeReviews{p.NativeInputSource}).AdoptReview(ctx, op.Work, op.Outcome)
			if err != nil {
				return empty, err
			}
			break
		}
	}
	if review.ReviewID <= 0 || review.Event != w.ReviewEvent || w.ReviewID != 0 && w.ReviewID != review.ReviewID {
		return empty, &factory.PublicationWait{Reason: "agent_review_pending"}
	}
	reviews, err := (nativeCollaborationReads{account}).ReadReviews(ctx, account.binding.Actor.ID, account.repository.ID, w.PRNumber, factoryservice.ReviewQuery{ReviewID: strconv.FormatInt(review.ReviewID, 10), Limit: 1})
	if err != nil {
		return empty, err
	}
	expectedState := "approved"
	if w.ReviewEvent == "COMMENT" {
		expectedState = "commented"
	}
	if len(reviews.Items) != 1 || reviews.Items[0].Pull.ExternalID != strconv.FormatInt(w.PRID, 10) || int64(reviews.Items[0].Author.ID) != w.ReviewerID || reviews.Items[0].Commit != collaboration.CommitID(w.HeadOID) || reviews.Items[0].State != expectedState {
		return empty, &factory.PublicationWait{Reason: "review_evidence_mismatch"}
	}
	observed, err := p.observeProviderMerge(ctx, w, account)
	if err != nil {
		return empty, err
	}
	after, err := p.ReadAcceptanceEvidence(ctx, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
	if err != nil {
		return empty, err
	}
	if before.Revision != after.Revision {
		return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
	}
	observed.NativeRev = after.Revision
	observed.ReviewID, observed.ReviewEvent = review.ReviewID, review.Event
	return observed, nil
}

func (p NativeMerges) admit(ctx context.Context, w factory.MergeWork) (*nativeProductAccount, error) {
	m, saved, err := p.registered(ctx, w.OperationID)
	if err != nil {
		return nil, err
	}
	if w.Intent().Validate() != nil || !reflect.DeepEqual(w, saved) || m.Stage != factory.MergeOpen || m.WithdrawRequested || m.Operation.Attempts != 1 || !m.Authorization.Bound() || time.Now().Unix() >= w.NotAfter {
		return nil, &factory.PublicationWait{Reason: "merge_not_admitted"}
	}
	policy, err := p.Store.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return nil, err
	}
	open, _, _, err := p.Store.DispatchState(ctx, w.Repository)
	if err != nil {
		return nil, err
	}
	if !open || !policy.Enabled || policy.Paused || policy.MergeMethod != w.Method {
		return nil, &factory.PublicationWait{Reason: "dispatch_closed"}
	}
	account, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpMerge)
	if err != nil {
		return nil, err
	}
	if int64(account.binding.Actor.ID) != w.ActorID {
		return nil, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	return account, nil
}

func (p NativeMerges) SubmitMerge(ctx context.Context, w factory.MergeWork) (factory.OperationOutcome, error) {
	var empty factory.OperationOutcome
	_, saved, err := p.registered(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(w, saved) {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil || !prior.NotObserved {
		return prior, err
	}
	account, err := p.admit(ctx, w)
	if err != nil {
		return empty, err
	}
	observed, err := p.ObserveMerge(ctx, w)
	if err != nil {
		return empty, err
	}
	if observed.NativeRev != w.NativeRev || observed.ReviewID != w.ReviewID || !observed.MethodAvailable || !observed.ProviderComplete || !observed.ProviderEligible {
		return empty, &factory.PublicationWait{Reason: "provider_merge_requirements_pending"}
	}
	policy, err := p.Store.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return empty, err
	}
	assessment, err := p.Store.CheckAssessment(ctx, w.Repository, w.PRNumber)
	if err != nil {
		return empty, err
	}
	if err = factory.VerifyMergeCheckEvidence(w, assessment, policy); err != nil {
		return empty, &factory.PublicationWait{Reason: "check_evidence_stale"}
	}
	target := factory.CheckTarget{Repository: w.Repository, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID}
	fresh, err := factory.VerifyChecks(target, factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}, policy, observed.Checks, time.Now().Unix())
	if err != nil || fresh.Verdict != factory.CheckPass {
		return empty, &factory.PublicationWait{Reason: "check_evidence_pending"}
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanMergeCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		c, err = (nativeHumanMerges{account}).PrepareHumanMerge(ctx, q)
		if err != nil {
			return empty, err
		}
		var prep nativeMergePreparation
		if nativeDecode(c.ProviderIntent, &prep) != nil || prep.Merge.PullID != strconv.FormatInt(w.PRID, 10) || prep.Merge.Head != w.HeadOID || prep.Merge.Base != w.BaseOID {
			return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
		}
		prep.NotAfter = w.NotAfter
		c.ProviderIntent, err = json.Marshal(prep)
		if err == nil {
			c, _, err = p.Store.CaptureHumanMerge(ctx, c)
		}
	}
	if err != nil {
		return empty, err
	}
	var prep nativeMergePreparation
	if nativeDecode(c.ProviderIntent, &prep) != nil || prep.NotAfter != w.NotAfter || prep.Merge.Head != w.HeadOID || prep.Merge.Base != w.BaseOID {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	if account, err = p.admit(ctx, w); err != nil {
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
		return nativeMergeOutcome(w, lookup), nil
	}
	if _, err = p.Store.ObserveHumanMerge(ctx, c, lookup); err != nil {
		return empty, err
	}
	record, err := (nativeHumanMerges{account}).SubmitHumanMerge(ctx, c)
	if err != nil {
		return empty, err
	}
	lookup, err = p.Store.ObserveHumanMerge(ctx, c, collaboration.OperationLookup{Host: q.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &record, Reason: record.Reason})
	return nativeMergeOutcome(w, lookup), err
}

func (p NativeMerges) CancelOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	_, w, err := p.registered(ctx, id)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	c, err := p.Store.HumanMergeCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		actor, e := p.Store.CollaborationActor(ctx, q.Actor)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		c = factory.HumanMergeCapture{Request: q, Authority: "native-github:" + string(p.Config.Profile) + ":" + actor.ExternalID, OperationID: q.ID, ProviderIntent: []byte(`{"cancelled_before_preparation":true}`)}
		c, _, err = p.Store.CaptureHumanMerge(ctx, c)
	}
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	lookup, err := p.Store.CancelHumanMergeDispatch(ctx, c)
	return nativeMergeOutcome(w, lookup), err
}

func (p NativeMerges) AdoptMerge(ctx context.Context, w factory.MergeWork, out factory.OperationOutcome) (factory.MergeOutcome, error) {
	var empty factory.MergeOutcome
	_, saved, err := p.registered(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(w, saved) || !reflect.DeepEqual(prior, out) || out.Effect != factory.OpEffectCommitted || out.Completion != factory.OpCompletionComplete {
		return empty, errNativeRead
	}
	q, err := p.request(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanMergeCapture(ctx, q)
	if err != nil {
		return empty, err
	}
	var prep nativeMergePreparation
	if nativeDecode(c.ProviderIntent, &prep) != nil || prep.NotAfter != w.NotAfter || prep.Merge.PullID != strconv.FormatInt(w.PRID, 10) || prep.Merge.Head != w.HeadOID || prep.Merge.Base != w.BaseOID || prep.Merge.Method != w.Method || prep.Merge.Message != q.Message {
		return empty, errNativeRead
	}
	receipt, err := validateNativeMergeReceipt(prep, out.Receipt)
	if err != nil || receipt.WritingMatches == nil || !*receipt.WritingMatches {
		return empty, errNativeRead
	}
	return factory.MergeOutcome{Method: w.Method, Operation: out, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID, MergedCommit: receipt.Commit, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, ActorID: w.ActorID}, nil
}

func (p NativeMerges) ObserveCompletion(ctx context.Context, w factory.MergeWork) (factory.MergeConfirmation, error) {
	var empty factory.MergeConfirmation
	out, err := p.LookupOp(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	adopted, err := p.AdoptMerge(ctx, w, out)
	if err != nil {
		return empty, err
	}
	account, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpMerge)
	if err != nil {
		return empty, err
	}
	var raw struct {
		Repository  string                      `json:"repository"`
		Number      int64                       `json:"number"`
		PullID      string                      `json:"pull_id"`
		IssueID     string                      `json:"issue_id"`
		Head        string                      `json:"head"`
		HeadRef     string                      `json:"head_ref"`
		BaseRef     string                      `json:"base_ref"`
		Commit      string                      `json:"commit"`
		BaseTip     string                      `json:"base_tip"`
		Contains    bool                        `json:"base_contains"`
		Merger      collaboration.ObservedActor `json:"merger"`
		MergedAt    *time.Time                  `json:"merged_at"`
		ClosedAt    *time.Time                  `json:"closed_at"`
		IssueClosed bool                        `json:"issue_closed"`
	}
	if err = account.read(ctx, nativeRead{Kind: "merge_completion", Repository: account.repository.ExternalID, Number: uint32(w.PRNumber)}, &raw); err != nil {
		return empty, fmt.Errorf("merge completion provider read: %w", err)
	}
	if raw.Repository != account.repository.ExternalID || raw.Number != w.PRNumber || raw.PullID != strconv.FormatInt(w.PRID, 10) || raw.IssueID != strconv.FormatInt(w.IssueID, 10) || raw.Head != w.HeadOID || raw.HeadRef != w.HeadRef || raw.BaseRef != w.BaseRef || raw.Commit != adopted.MergedCommit || !nativeActor(raw.Merger) || raw.MergedAt == nil || raw.ClosedAt == nil || !factory.ValidCommit(raw.BaseTip) {
		return empty, errNativeRead
	}
	mapped, err := account.mapRead(ctx, raw.Repository, account.repository.Name, []collaboration.ObservedActor{raw.Merger}, nil)
	if err != nil {
		return empty, err
	}
	original, err := (nativeIssueInputs{account}).ReadAcceptanceEvidence(ctx, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
	if err != nil {
		return empty, fmt.Errorf("merge original issue closure: %w", err)
	}
	return factory.MergeConfirmation{MergedCommit: raw.Commit, BaseTip: raw.BaseTip, BaseContainsMergedCommit: raw.Contains, MergerID: int64(mapped.Actors[raw.Merger.ExternalID].ID), MergedUnix: raw.MergedAt.Unix(), ClosedUnix: raw.ClosedAt.Unix(), NativeRev: original.Revision, ObservedUnix: time.Now().Unix(), IssueClosed: raw.IssueClosed, OriginalClosed: original.Issue.Closed, OriginalClosingOccurrence: original.Issue.ClosingOccurrence, OriginalClosedUnix: original.Issue.ClosedUnix}, nil
}

// observeProviderMerge reads provider facts without asserting a registered
// Factory review. Workflow admission remains in ObserveMerge and the coordinator.
func (p NativeMerges) observeProviderMerge(ctx context.Context, w factory.MergeWork, account *nativeProductAccount) (factory.MergeObservation, error) {
	var empty factory.MergeObservation
	var facts nativeMergeFacts
	if err := account.read(ctx, nativeRead{Kind: "merge_preparation", Repository: account.repository.ExternalID, Number: uint32(w.PRNumber)}, &facts); err != nil {
		return empty, err
	}
	if facts.PullID != strconv.FormatInt(w.PRID, 10) || facts.Head != w.HeadOID || facts.Base != w.BaseOID {
		return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
	}
	reads := nativeCollaborationReads{account}
	gate, err := reads.readRequiredChecks(ctx, account.repository.ID, nativeSubjectRef{Kind: "pull", ExternalID: facts.PullID, Number: w.PRNumber}, collaboration.CommitID(w.HeadOID), collaboration.CommitID(w.BaseOID), w.BaseRef[len("refs/heads/"):])
	if err != nil {
		return empty, fmt.Errorf("merge provider rules: %w", err)
	}
	summary := gate.Protection
	complete := summary != nil && summary.RequirementsComplete != nil && *summary.RequirementsComplete && summary.RequiredApprovals != nil && *summary.RequiredApprovals == 0
	if summary != nil {
		for _, flag := range []*bool{summary.RequireCodeOwnerReviews, summary.RequireLastPushApproval, summary.RequireSigned, summary.RequireLinearHistory, summary.RequireResolvedThreads, summary.RequireUpToDate} {
			complete = complete && flag != nil && !*flag
		}
	}
	checks, err := p.ObserveChecks(ctx, factory.CheckTarget{Repository: w.Repository, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID}, w.ActorID)
	if err != nil {
		return empty, err
	}
	caps, err := reads.ReadCapabilities(ctx, account.binding.Actor.ID, account.repository.ID, factoryservice.CapabilityQuery{Subject: "pull", Number: w.PRNumber})
	if err != nil {
		return empty, fmt.Errorf("merge capabilities: %w", err)
	}
	if caps.Pull == nil || caps.Pull.ExternalID != facts.PullID || caps.Head == nil || string(*caps.Head) != w.HeadOID || caps.Base == nil || string(*caps.Base) != w.BaseOID || checks.HeadTip != w.HeadOID || checks.BaseTip != w.BaseOID {
		return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
	}
	opinions, err := reads.readReviewOpinions(ctx, nativeSubjectRef{Kind: "pull", ExternalID: facts.PullID, Number: w.PRNumber}, w.HeadOID, w.BaseOID)
	if err != nil {
		return empty, err
	}
	veto := false
	for _, opinion := range opinions.Items {
		veto = veto || opinion.State == "changes_requested"
	}
	available := facts.Methods[w.Method] && facts.State == "open" && facts.Draft != nil && !*facts.Draft && facts.Merged != nil && !*facts.Merged && facts.Push != nil && *facts.Push && facts.Archived != nil && !*facts.Archived && facts.Disabled != nil && !*facts.Disabled
	return factory.MergeObservation{Method: w.Method, MethodAvailable: available, Checks: checks, ObservedUnix: time.Now().Unix(), PRID: w.PRID, PRNumber: w.PRNumber, IssueID: w.IssueID, PRAuthorID: w.PRAuthorID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID, ReviewerID: w.ReviewerID, ReviewID: w.ReviewID, ReviewEvent: w.ReviewEvent, ProviderComplete: complete, ProviderEligible: complete && !veto && caps.Actions[collaboration.MergePull].State == collaboration.Allowed && (gate.StatusGate == "success" || gate.StatusGate == "not_required")}, nil
}
