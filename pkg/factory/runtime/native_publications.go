package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

// NativePublication uses canonical workflow work and the existing command
// journal. Rust owns object transfer, provider conditions and causal receipts.
type NativePublication struct{ NativeInputSource }

var _ control.PublicationExecutor = NativePublication{}

type nativePublicationRefs struct {
	Repository           string `json:"repository"`
	Name                 string `json:"name"`
	RepositoryNodeID     string `json:"repository_node_id"`
	Push                 *bool  `json:"push"`
	Archived             *bool  `json:"archived"`
	Disabled             *bool  `json:"disabled"`
	Branch               string `json:"branch"`
	Tip                  string `json:"tip"`
	ComparisonBranch     string `json:"comparison_branch"`
	Comparison           string `json:"comparison"`
	ComparisonRepository string `json:"comparison_repository,omitempty"`
}

func nativePublicationCommandID(profile, id string) string {
	sum := sha256.Sum256([]byte("factory.native.publication\x00" + profile + "\x00" + id))
	return hex.EncodeToString(sum[:16])
}

// Native mutation admission accepts at most five minutes. Bound a new capture
// by both that transport limit and the original workflow authority; replays
// always retain the already-captured deadline.
func nativePublicationDeadline(notAfter int64) int64 {
	return min(notAfter, time.Now().Add(5*time.Minute).Unix())
}
func (p NativePublication) registered(ctx context.Context, id string) (factory.Publication, factory.PublicationOperation, factory.PublicationWork, error) {
	var empty factory.PublicationWork
	publication, op, err := p.Store.PublicationOperation(ctx, id)
	if err != nil {
		return publication, op, empty, err
	}
	runID := op.RunID
	if runID == "" {
		runID = publication.Run
	}
	run, err := p.Store.FactoryRun(ctx, runID)
	if err != nil {
		return publication, op, empty, err
	}
	w := factory.PublicationWork{AssignmentID: publication.AssignmentID, Publication: publication.ID, RunID: runID, Run: run, BaseSHA: publication.BaseSHA, Issue: publication.Issue}
	if op.Work != nil {
		w = op.Work.Apply(w)
	}
	return publication, op, w, nil
}

// The workflow and its issue stay in the base repository. Only a separately
// consented external correction may target the captured fork's existing ref.
func (p NativePublication) branchRepository(ctx context.Context, w factory.PublicationWork) (collaboration.RepositoryRef, error) {
	repo, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(w.Repository))
	if err != nil {
		return repo, err
	}
	if w.CorrectionNumber == 0 {
		return repo, nil
	}
	publication, err := p.Store.PublicationByAssignment(ctx, w.AssignmentID)
	if err != nil || publication.Validate() != nil || publication.ID != w.Publication || publication.Repository != w.Repository || publication.PRNumber != w.CorrectionNumber {
		return repo, errNativeReadScope
	}
	target := publication.PullTarget()
	if target.HeadRepository == w.Repository {
		return repo, nil
	}
	if publication.External == nil || publication.External.ForkConsent != publication.External.CommandID || target.HeadRepository <= 0 {
		return repo, errNativeReadScope
	}
	host, err := p.Store.CollaborationHost(ctx, repo.Host)
	if err != nil || host.Provider != collaboration.GitHub {
		return repo, errNativeReadScope
	}
	fork, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(target.HeadRepository))
	if err != nil || fork.Host != repo.Host {
		return repo, errNativeReadScope
	}
	return fork, nil
}

func (p NativePublication) publicationAccount(ctx context.Context, w factory.PublicationWork, kind string) (*nativeProductAccount, error) {
	account, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), kind)
	if err != nil {
		return nil, err
	}
	repo, err := p.branchRepository(ctx, w)
	if err != nil {
		return nil, err
	}
	if repo.ID == account.repository.ID {
		return account, nil
	}
	return p.savedAccount(ctx, int64(repo.ID), account.binding.Actor.ID, account.binding.Connection.ID)
}

// AdmitCorrectionSource checks the actual fork destination before a Fixer can
// spend its bounded run. Publication still rechecks these facts before sending.
func (p NativePublication) AdmitCorrectionSource(ctx context.Context, publication factory.Publication) error {
	if publication.Validate() != nil || publication.External == nil || publication.External.ForkConsent != publication.External.CommandID || publication.PullTarget().HeadRepository == publication.Repository || publication.WithdrawRequested || publication.Stage != factory.PublicationPublished {
		return errNativeReadScope
	}
	policy, err := p.Store.RepositoryPolicy(ctx, publication.Repository)
	if err != nil {
		return err
	}
	w := factory.PublicationWork{AssignmentID: publication.AssignmentID, Publication: publication.ID, Repository: publication.Repository, ActorID: policy.Publish.ActorID, CorrectionNumber: publication.PRNumber, CorrectionAuthor: publication.PullTarget().AuthorID, ExpectedOld: publication.Candidate, ComparisonRef: publication.TargetBranch, ComparisonOID: publication.PullTarget().BaseOID}
	account, err := p.publicationAccount(ctx, w, factory.OpRefPublish)
	if err != nil {
		return err
	}
	refs, err := p.refs(ctx, account, w)
	if err != nil {
		return err
	}
	if refs.Tip != publication.Candidate || refs.Comparison != publication.PullTarget().BaseOID {
		return errNativeReadScope
	}
	return p.observeCorrectionPull(ctx, account, w, publication, refs)
}

func (p NativePublication) branchRequest(ctx context.Context, w factory.PublicationWork) (factory.HumanBranchCreateCommand, error) {
	repo, err := p.branchRepository(ctx, w)
	if err != nil {
		return factory.HumanBranchCreateCommand{}, err
	}
	name := factory.PublicationBranch(w.AssignmentID)
	if w.CorrectionNumber > 0 {
		publication, err := p.Store.PublicationByAssignment(ctx, w.AssignmentID)
		if err != nil || publication.ID != w.Publication || publication.PRNumber != w.CorrectionNumber {
			return factory.HumanBranchCreateCommand{}, errNativeReadScope
		}
		name = factory.RefHead(publication.PublicationRef())
	}
	q := factory.HumanBranchCreateCommand{ID: nativePublicationCommandID(string(p.Config.Profile), w.OperationID), Host: repo.Host, Repository: repo.ID, Actor: collaboration.ActorID(w.ActorID), Name: name, Commit: collaboration.CommitID(w.Candidate)}
	return q, q.Validate()
}
func (p NativePublication) pullRequest(ctx context.Context, w factory.PublicationWork) (factory.HumanPullCreateCommand, error) {
	branch, err := p.branchRequest(ctx, w)
	if err != nil {
		return factory.HumanPullCreateCommand{}, err
	}
	q := factory.HumanPullCreateCommand{ID: branch.ID, Host: branch.Host, Repository: branch.Repository, HeadRepository: branch.Repository, Actor: branch.Actor, BaseBranch: strings.TrimPrefix(w.ComparisonRef, "refs/heads/"), HeadBranch: branch.Name, Title: w.PRTitle, Body: w.PRBody}
	return q, q.Validate()
}
func (p NativePublication) refs(ctx context.Context, account *nativeProductAccount, w factory.PublicationWork) (nativePublicationRefs, error) {
	var raw nativePublicationRefs
	name := factory.PublicationBranch(w.AssignmentID)
	if w.CorrectionNumber > 0 {
		publication, err := p.Store.PublicationByAssignment(ctx, w.AssignmentID)
		if err != nil || publication.ID != w.Publication || publication.PRNumber != w.CorrectionNumber {
			return raw, errNativeReadScope
		}
		name = factory.RefHead(publication.PublicationRef())
	}
	comparison := strings.TrimPrefix(w.ComparisonRef, "refs/heads/")
	if !strings.HasPrefix(w.ComparisonRef, "refs/heads/") {
		return raw, errNativeReadScope
	}
	base, err := p.Store.CollaborationRepository(ctx, collaboration.RepositoryID(w.Repository))
	if err != nil {
		return raw, err
	}
	comparisonRepository := ""
	if base.ID != account.repository.ID {
		comparisonRepository = base.ExternalID
	}
	err = account.read(ctx, nativeRead{Kind: "publication_refs", Repository: account.repository.ExternalID, Name: name, BaseBranch: comparison, ComparisonRepository: comparisonRepository}, &raw)
	if err != nil {
		return raw, err
	}
	if raw.Repository != account.repository.ExternalID || raw.ComparisonRepository != comparisonRepository || raw.Branch != name || raw.ComparisonBranch != comparison || !nativeReviewNode(raw.RepositoryNodeID, 1024) || raw.Push == nil || !*raw.Push || raw.Archived == nil || *raw.Archived || raw.Disabled == nil || *raw.Disabled || !(raw.Tip == "" || factory.ValidCommit(raw.Tip)) || !factory.ValidCommit(raw.Comparison) {
		return raw, errNativeRead
	}
	_, err = account.mapRead(ctx, raw.Repository, raw.Name, nil, nil)
	return raw, err
}
func (p NativePublication) ObservePublication(ctx context.Context, w factory.PublicationWork) (factory.PublicationObservation, error) {
	var empty factory.PublicationObservation
	if w.ValidateObservation() != nil {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	publication, err := p.Store.PublicationByAssignment(ctx, w.AssignmentID)
	if err != nil {
		return empty, err
	}
	kind := factory.OpRefPublish
	correction := w.CorrectionNumber > 0
	if correction {
		registered := false
		for _, op := range publication.Corrections {
			if op.OperationID == w.OperationID && op.RunID == w.RunID && op.Work != nil && reflect.DeepEqual(*op.Work, w.Intent()) {
				registered = true
			}
		}
		if !registered && w.OperationID != factory.PublicationOperationID(publication.ID, factory.OpRefPublish, len(publication.Corrections)+2) {
			return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
		}
		if publication.Stage != factory.PublicationPublished || publication.PRNumber != w.CorrectionNumber || !publication.HasPullTarget() || publication.PullTarget().AuthorID != w.CorrectionAuthor || publication.Candidate != w.ExpectedOld {
			return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
		}
	} else {
		if w.OperationID == factory.PublicationOperationID(publication.ID, factory.OpPRCreate, 1) {
			kind = factory.OpPRCreate
		} else if w.OperationID != factory.PublicationOperationID(publication.ID, factory.OpRefPublish, 1) {
			return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
		}
		if publication.Run != w.RunID || publication.Candidate != w.Candidate {
			return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
		}
	}
	if publication.ID != w.Publication || publication.AssignmentID != w.AssignmentID || publication.Repository != w.Repository || publication.Issue != w.Issue || publication.BaseSHA != w.BaseSHA || publication.TargetBranch != w.ComparisonRef {
		return empty, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	account, err := p.publicationAccount(ctx, w, kind)
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
	raw, err := p.refs(ctx, account, w)
	if err != nil {
		return empty, err
	}
	if correction {
		if err = p.observeCorrectionPull(ctx, account, w, publication, raw); err != nil {
			return empty, err
		}
	}
	after, err := p.ReadAcceptanceEvidence(ctx, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
	if err != nil {
		return empty, err
	}
	if before.Revision != after.Revision {
		return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
	}
	return factory.PublicationObservation{TargetRef: publication.PublicationRef(), TargetTip: raw.Tip, Comparison: raw.Comparison, NativeRev: after.Revision, ObservedUnix: time.Now().Unix()}, nil
}
func nativePublicationOutcome(w factory.PublicationWork, kind string, o collaboration.OperationLookup) factory.OperationOutcome {
	if o.Record == nil {
		return factory.OperationOutcome{OperationID: w.OperationID, NotObserved: true}
	}
	r := o.Record
	result := factory.OperationOutcome{OperationID: w.OperationID, InstallationID: r.Authority, Kind: kind, ActorID: w.ActorID, RepositoryID: w.Repository, Effect: r.Effect, Cancellation: r.Cancellation, Completion: r.Completion, Reason: r.Reason}
	if r.Receipt != nil {
		result.Receipt = append([]byte(nil), r.Receipt.Payload...)
	}
	return result
}
func (p NativePublication) LookupOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	_, op, w, err := p.registered(ctx, id)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	if op.Work == nil {
		return factory.OperationOutcome{OperationID: id, NotObserved: true}, nil
	}
	var cmd factory.Command
	if op.Kind == factory.OpRefPublish && w.CorrectionNumber > 0 {
		q, e := p.branchRequest(ctx, w)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		c, e := p.Store.RefPublicationCapture(ctx, q, w.ExpectedOld)
		err = e
		if err == nil {
			cmd, err = c.Command()
		}
	} else if op.Kind == factory.OpRefPublish {
		q, e := p.branchRequest(ctx, w)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		c, e := p.Store.HumanBranchCreateCapture(ctx, q)
		err = e
		if err == nil {
			cmd, err = c.Command()
		}
	} else {
		q, e := p.pullRequest(ctx, w)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		c, e := p.Store.HumanPullCreateCapture(ctx, q)
		err = e
		if err == nil {
			cmd, err = c.Command()
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		if op.Effect != "" {
			return factory.OperationOutcome{}, errors.New("publication dispatch history is not in this native journal")
		}
		return factory.OperationOutcome{OperationID: id, NotObserved: true}, nil
	}
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	lookup, err := p.Store.LookupHumanCommandDispatch(ctx, cmd)
	if err == nil && lookup.Record != nil && lookup.Record.Effect == collaboration.EffectIndeterminate && lookup.Record.Receipt == nil {
		account, e := p.lookupPublicationAccount(ctx, w, op.Kind, cmd)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		switch cmd.Type {
		case factory.CommandHumanBranchCreate:
			c, e := factory.DecodeHumanBranchCreateCapture(cmd)
			if e != nil {
				return factory.OperationOutcome{}, e
			}
			lookup, err = (nativeHumanBranchCreates{account}).LookupHumanBranchCreate(ctx, c)
		case factory.CommandHumanPullCreate:
			c, e := factory.DecodeHumanPullCreateCapture(cmd)
			if e != nil {
				return factory.OperationOutcome{}, e
			}
			lookup, err = (nativeHumanPullCreates{account}).LookupHumanPullCreate(ctx, c)
		case factory.CommandRefPublish:
			c, e := factory.DecodeRefPublicationCapture(cmd)
			if e != nil {
				return factory.OperationOutcome{}, e
			}
			prep, e := p.decodeRefCapture(ctx, account, c)
			if e != nil {
				return factory.OperationOutcome{}, e
			}
			lookup, err = account.recoverHumanEffect(ctx, cmd, prep.NotAfter, prep.Branch.Repository, lookup, func(reply nativeCreationReply) (collaboration.OperationRecord, error) {
				return nativeRefPublicationOutcome(c, prep, reply)
			}, func(observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return p.Store.ObserveRefPublication(ctx, c, observation)
			})
		}
	}
	return nativePublicationOutcome(w, op.Kind, lookup), err
}

// Historical lookup uses the exact captured native connection and host grant.
// An expired/withdrawn publication may be observed without selecting new policy
// authority, while only admission for a new send requires an active grant.
func (p NativePublication) lookupPublicationAccount(ctx context.Context, w factory.PublicationWork, kind string, cmd factory.Command) (*nativeProductAccount, error) {
	var intent json.RawMessage
	var authority nativePublicationAuthority
	if cmd.Type == factory.CommandHumanPullCreate {
		c, err := factory.DecodeHumanPullCreateCapture(cmd)
		if err != nil {
			return nil, err
		}
		var prep nativePullCreatePreparation
		if nativeDecode(c.ProviderIntent, &prep) != nil {
			return nil, errNativeRead
		}
		authority = prep.Pull.nativePublicationAuthority
	}
	if cmd.Type == factory.CommandHumanBranchCreate {
		c, err := factory.DecodeHumanBranchCreateCapture(cmd)
		if err != nil {
			return nil, err
		}
		intent = c.ProviderIntent
	} else if cmd.Type == factory.CommandRefPublish {
		c, err := factory.DecodeRefPublicationCapture(cmd)
		if err != nil {
			return nil, err
		}
		intent = c.ProviderIntent
	}
	if len(intent) > 0 {
		var prep nativeBranchCreatePreparation
		if nativeDecode(intent, &prep) != nil {
			return nil, errNativeRead
		}
		if prep.Transfer != nil && prep.Transfer.ControllerGrantID != "" {
			authority = prep.Transfer.nativePublicationAuthority
		}
	}
	if authority.ControllerGrantID != "" {
		if !factory.ValidID(authority.ControllerGrantID) || !collaboration.ValidID(string(authority.SourceConnection)) {
			return nil, errNativeReadScope
		}
		account, err := p.savedAccount(ctx, w.Repository, collaboration.ActorID(w.ActorID), authority.SourceConnection)
		if err == nil && account.binding.Host.Provider != collaboration.Fountain {
			return nil, errNativeReadScope
		}
		return account, err
	}
	return p.publicationAccount(ctx, w, kind)
}
func (p NativePublication) admitSubmit(ctx context.Context, w factory.PublicationWork, kind string) (*nativeProductAccount, error) {
	if w.Validate() != nil {
		return nil, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	publication, op, saved, err := p.registered(ctx, w.OperationID)
	if err != nil {
		return nil, err
	}
	if !(w.CorrectionNumber == 0 && publication.Stage == factory.PublicationOpen || w.CorrectionNumber > 0 && publication.Stage == factory.PublicationPublished) || publication.WithdrawRequested || op.Kind != kind || op.Work == nil || !reflect.DeepEqual(*op.Work, w.Intent()) || op.RunID != "" && op.RunID != w.RunID || op.Attempts != 1 || time.Now().Unix() >= w.NotAfter {
		return nil, &factory.PublicationWait{Reason: "publication_not_admitted"}
	}
	if w.AssignmentID != publication.AssignmentID || w.RunID != saved.RunID || w.Run.InputSHA != saved.Run.InputSHA || w.Run.ProjectID != saved.Run.ProjectID || w.Run.Role != saved.Run.Role || !saved.Run.Reconciled {
		return nil, &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	policy, err := p.Store.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return nil, err
	}
	open, _, _, err := p.Store.DispatchState(ctx, w.Repository)
	if err != nil {
		return nil, err
	}
	if !open || !policy.Enabled || policy.Paused {
		return nil, &factory.PublicationWait{Reason: "dispatch_closed"}
	}
	account, err := p.publicationAccount(ctx, w, kind)
	if err != nil {
		return nil, err
	}
	if int64(account.binding.Actor.ID) != w.ActorID {
		return nil, &factory.PublicationRefusal{Reason: "authority_lost"}
	}
	return account, nil
}
func (p NativePublication) SubmitPublish(ctx context.Context, w factory.PublicationWork) (factory.OperationOutcome, error) {
	var empty factory.OperationOutcome
	if w.CorrectionNumber > 0 {
		return p.submitCorrectionRef(ctx, w)
	}
	account, err := p.admitSubmit(ctx, w, factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	q, err := p.branchRequest(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanBranchCreateCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		observed, e := p.ObservePublication(ctx, w)
		if e != nil {
			return empty, e
		}
		if observed.TargetTip != "" || observed.Comparison != w.ComparisonOID || observed.NativeRev != w.NativeRev {
			return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
		}
		raw, e := p.refs(ctx, account, w)
		if e != nil {
			return empty, e
		}
		if raw.Tip != "" || raw.Comparison != w.ComparisonOID {
			return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
		}
		sum := sha256.Sum256(w.Bundle)
		prep := nativeBranchCreatePreparation{Version: 2, Account: account.identity.Account.ExternalID, NotAfter: nativePublicationDeadline(w.NotAfter), Branch: nativeBranchCreateWrite{Repository: raw.Repository, RepositoryNodeID: raw.RepositoryNodeID, Name: q.Name, Commit: string(q.Commit)}, Transfer: &nativeBranchTransfer{Base: w.Run.InputSHA, ComparisonBranch: raw.ComparisonBranch, ComparisonOID: w.ComparisonOID, BundleSHA256: hex.EncodeToString(sum[:])}}
		if err = prep.Transfer.captureController(account, &prep.NotAfter); err != nil {
			return empty, err
		}
		encoded, e := json.Marshal(prep)
		if e != nil {
			return empty, e
		}
		c = factory.HumanBranchCreateCapture{Request: q, Authority: account.humanAuthority(), OperationID: q.ID, ProviderIntent: encoded}
		c, _, err = p.Store.CaptureHumanBranchCreate(ctx, c)
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
	if _, err = p.Store.ObserveHumanBranchCreate(ctx, c, lookup); err != nil {
		return empty, err
	}
	account, err = p.admitSubmit(ctx, w, factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	record, err := (nativeHumanBranchCreates{account}).submit(ctx, c, w.Bundle)
	if err != nil {
		return empty, err
	}
	lookup = collaboration.OperationLookup{Host: q.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &record, Reason: record.Reason}
	lookup, err = p.Store.ObserveHumanBranchCreate(ctx, c, lookup)
	return nativePublicationOutcome(w, factory.OpRefPublish, lookup), err
}
func (p NativePublication) PushBranch(ctx context.Context, w factory.PublicationWork) (factory.OperationOutcome, error) {
	return p.LookupOp(ctx, w.OperationID)
}
func (p NativePublication) SubmitPRCreate(ctx context.Context, w factory.PublicationWork) (factory.OperationOutcome, error) {
	var empty factory.OperationOutcome
	account, err := p.admitSubmit(ctx, w, factory.OpPRCreate)
	if err != nil {
		return empty, err
	}
	q, err := p.pullRequest(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanPullCreateCapture(ctx, q)
	if errors.Is(err, store.ErrNotFound) {
		observed, e := p.ObservePublication(ctx, w)
		if e != nil {
			return empty, e
		}
		if observed.TargetTip != w.Candidate || observed.Comparison != w.ComparisonOID || observed.NativeRev != w.NativeRev {
			return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
		}
		writer := nativeHumanPullCreates{account}
		c, err = writer.PrepareHumanPullCreate(ctx, q)
		if err == nil {
			var prep nativePullCreatePreparation
			if nativeDecode(c.ProviderIntent, &prep) != nil || prep.Pull.BasePin.Commit != w.ComparisonOID || prep.Pull.HeadPin.Commit != w.Candidate {
				return empty, &factory.PublicationWait{Reason: "native_inputs_changed"}
			}
			prep.NotAfter = min(prep.NotAfter, nativePublicationDeadline(w.NotAfter))
			c.ProviderIntent, err = json.Marshal(prep)
		}
		if err == nil {
			c, _, err = p.Store.CaptureHumanPullCreate(ctx, c)
		}
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
		return nativePublicationOutcome(w, factory.OpPRCreate, lookup), nil
	}
	if _, err = p.Store.ObserveHumanPullCreate(ctx, c, lookup); err != nil {
		return empty, err
	}
	account, err = p.admitSubmit(ctx, w, factory.OpPRCreate)
	if err != nil {
		return empty, err
	}
	record, err := (nativeHumanPullCreates{account}).SubmitHumanPullCreate(ctx, c)
	if err != nil {
		return empty, err
	}
	lookup = collaboration.OperationLookup{Host: q.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &record, Reason: record.Reason}
	lookup, err = p.Store.ObserveHumanPullCreate(ctx, c, lookup)
	return nativePublicationOutcome(w, factory.OpPRCreate, lookup), err
}
func (p NativePublication) CancelOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	_, op, w, err := p.registered(ctx, id)
	if err != nil {
		return factory.OperationOutcome{}, err
	}
	if op.Work == nil {
		return factory.OperationOutcome{OperationID: id, NotObserved: true}, nil
	}
	var lookup collaboration.OperationLookup
	if op.Kind == factory.OpRefPublish && w.CorrectionNumber > 0 {
		q, e := p.branchRequest(ctx, w)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		capture, e := p.Store.RefPublicationCapture(ctx, q, w.ExpectedOld)
		if errors.Is(e, store.ErrNotFound) {
			actor, lookupErr := p.Store.CollaborationActor(ctx, q.Actor)
			if lookupErr != nil {
				return factory.OperationOutcome{}, lookupErr
			}
			host, lookupErr := p.Store.CollaborationHost(ctx, q.Host)
			if lookupErr != nil {
				return factory.OperationOutcome{}, lookupErr
			}
			capture = factory.RefPublicationCapture{Request: q, ExpectedOld: w.ExpectedOld, Authority: nativeHumanAuthority(p.Config.Profile, host, actor.ExternalID), OperationID: q.ID, ProviderIntent: []byte(`{"cancelled_before_preparation":true}`)}
			capture, _, e = p.Store.CaptureRefPublication(ctx, capture)
		}
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		lookup, err = p.Store.CancelRefPublicationDispatch(ctx, capture)
	} else if op.Kind == factory.OpRefPublish {
		q, e := p.branchRequest(ctx, w)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		c, e := p.Store.HumanBranchCreateCapture(ctx, q)
		if errors.Is(e, store.ErrNotFound) {
			actor, lookupErr := p.Store.CollaborationActor(ctx, q.Actor)
			if lookupErr != nil {
				return factory.OperationOutcome{}, lookupErr
			}
			host, lookupErr := p.Store.CollaborationHost(ctx, q.Host)
			if lookupErr != nil {
				return factory.OperationOutcome{}, lookupErr
			}
			c = factory.HumanBranchCreateCapture{Request: q, Authority: nativeHumanAuthority(p.Config.Profile, host, actor.ExternalID), OperationID: q.ID, ProviderIntent: []byte(`{"cancelled_before_preparation":true}`)}
			c, _, e = p.Store.CaptureHumanBranchCreate(ctx, c)
		}
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		lookup, err = p.Store.CancelHumanBranchCreateDispatch(ctx, c)
	} else {
		q, e := p.pullRequest(ctx, w)
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		c, e := p.Store.HumanPullCreateCapture(ctx, q)
		if errors.Is(e, store.ErrNotFound) {
			actor, lookupErr := p.Store.CollaborationActor(ctx, q.Actor)
			if lookupErr != nil {
				return factory.OperationOutcome{}, lookupErr
			}
			host, lookupErr := p.Store.CollaborationHost(ctx, q.Host)
			if lookupErr != nil {
				return factory.OperationOutcome{}, lookupErr
			}
			c = factory.HumanPullCreateCapture{Request: q, Authority: nativeHumanAuthority(p.Config.Profile, host, actor.ExternalID), OperationID: q.ID, ProviderIntent: []byte(`{"cancelled_before_preparation":true}`)}
			c, _, e = p.Store.CaptureHumanPullCreate(ctx, c)
		}
		if e != nil {
			return factory.OperationOutcome{}, e
		}
		lookup, err = p.Store.CancelHumanPullCreateDispatch(ctx, c)
	}
	return nativePublicationOutcome(w, op.Kind, lookup), err
}
func (p NativePublication) AdoptBranch(ctx context.Context, w factory.PublicationWork, out factory.OperationOutcome) (factory.BranchOutcome, error) {
	var empty factory.BranchOutcome
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(prior, out) || out.Completion != factory.OpCompletionComplete {
		return empty, errNativeRead
	}
	q, err := p.branchRequest(ctx, w)
	if err != nil {
		return empty, err
	}
	if w.CorrectionNumber > 0 {
		capture, e := p.Store.RefPublicationCapture(ctx, q, w.ExpectedOld)
		if e != nil {
			return empty, e
		}
		var prep nativeBranchCreatePreparation
		if nativeDecode(capture.ProviderIntent, &prep) != nil || prep.Version != 3 || (prep.NotAfter <= 0 || prep.NotAfter > w.NotAfter) || prep.Transfer == nil || prep.Transfer.ExpectedOld != w.ExpectedOld || prep.Transfer.Base != w.Run.InputSHA {
			return empty, errNativeRead
		}
		receipt, e := validateNativeBranchTransferReceipt(q, prep, out.Receipt)
		if e != nil || out.OperationID != w.OperationID || out.Kind != factory.OpRefPublish || out.Effect != factory.OpEffectCommitted || receipt.Comparison != w.ComparisonOID {
			return empty, errNativeRead
		}
		return factory.BranchOutcome{Operation: out, Ref: receipt.Branch, OldOID: receipt.Old, NewOID: receipt.New, Comparison: receipt.Comparison}, nil
	}
	c, err := p.Store.HumanBranchCreateCapture(ctx, q)
	if err != nil {
		return empty, err
	}
	var prep nativeBranchCreatePreparation
	if nativeDecode(c.ProviderIntent, &prep) != nil || (prep.NotAfter <= 0 || prep.NotAfter > w.NotAfter) {
		return empty, errNativeRead
	}
	raw, err := validateNativeBranchTransferReceipt(c.Request, prep, out.Receipt)
	if err != nil || out.OperationID != w.OperationID || out.Kind != factory.OpRefPublish || out.Effect != factory.OpEffectCommitted || raw.Comparison != w.ComparisonOID {
		return empty, errNativeRead
	}
	return factory.BranchOutcome{Operation: out, Ref: raw.Branch, OldOID: "absent", NewOID: raw.New, Comparison: raw.Comparison}, nil
}
func (p NativePublication) AdoptPRCreation(ctx context.Context, w factory.PublicationWork, out factory.OperationOutcome) (factory.PRCreationOutcome, error) {
	var empty factory.PRCreationOutcome
	prior, err := p.LookupOp(ctx, w.OperationID)
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(prior, out) || out.Completion != factory.OpCompletionComplete {
		return empty, errNativeRead
	}
	q, err := p.pullRequest(ctx, w)
	if err != nil {
		return empty, err
	}
	c, err := p.Store.HumanPullCreateCapture(ctx, q)
	if err != nil {
		return empty, err
	}
	var prep nativePullCreatePreparation
	if nativeDecode(c.ProviderIntent, &prep) != nil || (prep.NotAfter <= 0 || prep.NotAfter > w.NotAfter) {
		return empty, errNativeRead
	}
	raw, err := validateNativePullCreateReceipt(c, prep, out.Receipt)
	if err != nil || out.OperationID != w.OperationID || out.Kind != factory.OpPRCreate || out.Effect != factory.OpEffectCommitted || prep.Pull.BasePin.Commit != w.ComparisonOID || prep.Pull.HeadPin.Commit != w.Candidate {
		return empty, errNativeRead
	}
	_, operation, e := p.Store.PublicationOperation(ctx, w.OperationID)
	if e != nil {
		return empty, e
	}
	if operation.IssueID > 0 {
		prID, e := strconv.ParseInt(raw.Pull.ExternalID, 10, 64)
		if e != nil {
			return empty, e
		}
		if operation.PRNumber != raw.Pull.Number || operation.PRID != prID || operation.HeadRef != "refs/heads/"+q.HeadBranch || operation.BaseRef != "refs/heads/"+q.BaseBranch || operation.HeadOID != prep.Pull.HeadPin.Commit || operation.BaseOID != prep.Pull.BasePin.Commit {
			return empty, errNativeRead
		}
		return factory.PRCreationOutcome{Operation: out, HeadRef: operation.HeadRef, BaseRef: operation.BaseRef, HeadOID: operation.HeadOID, BaseOID: operation.BaseOID, PRNumber: operation.PRNumber, PRID: operation.PRID, IssueID: operation.IssueID, AuthorID: w.ActorID}, nil
	}
	if raw.HostOperation != nil {
		prID, _ := strconv.ParseInt(raw.Pull.ExternalID, 10, 64)
		issueID, _ := strconv.ParseInt(raw.Pull.IssueID, 10, 64)
		return factory.PRCreationOutcome{Operation: out, HeadRef: "refs/heads/" + q.HeadBranch, BaseRef: "refs/heads/" + q.BaseBranch, HeadOID: prep.Pull.HeadPin.Commit, BaseOID: prep.Pull.BasePin.Commit, PRNumber: raw.Pull.Number, PRID: prID, IssueID: issueID, AuthorID: w.ActorID}, nil
	}
	account, err := p.account(ctx, strconv.FormatInt(w.Repository, 10), factory.OpPRCreate)
	if err != nil {
		return empty, err
	}
	var issue struct {
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
	if err = account.read(ctx, nativeRead{Kind: "automation_review", Repository: account.repository.ExternalID, Number: uint32(raw.Pull.Number)}, &issue); err != nil {
		return empty, err
	}
	if account.binding.Host.Provider == collaboration.GitHub && (issue.HeadRepository == nil || issue.HeadRepository.ExternalID != account.repository.ExternalID) {
		return empty, errNativeRead
	}
	if issue.Repository != account.repository.ExternalID || issue.Number != raw.Pull.Number || issue.PullID != raw.Pull.ExternalID || !nativeRemoteID(issue.IssueID) || issue.Author.ExternalID != prep.Account || issue.HeadRef != "refs/heads/"+q.HeadBranch || issue.BaseRef != "refs/heads/"+q.BaseBranch || issue.HeadTip != w.Candidate || issue.BaseTip != w.ComparisonOID {
		return empty, errNativeRead
	}
	prID, err := strconv.ParseInt(raw.Pull.ExternalID, 10, 64)
	if err != nil {
		return empty, err
	}
	issueID, err := strconv.ParseInt(issue.IssueID, 10, 64)
	if err != nil {
		return empty, err
	}
	return factory.PRCreationOutcome{Operation: out, HeadRef: "refs/heads/" + q.HeadBranch, BaseRef: "refs/heads/" + q.BaseBranch, HeadOID: prep.Pull.HeadPin.Commit, BaseOID: prep.Pull.BasePin.Commit, PRNumber: raw.Pull.Number, PRID: prID, IssueID: issueID, AuthorID: w.ActorID}, nil
}
