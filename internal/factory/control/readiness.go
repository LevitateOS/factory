package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// ReadinessObservation supplies the cheap native revision check used to
// bracket readiness evidence. Evidence reads stay on AcceptanceSource; this
// interface never authorizes work.
type ReadinessObservation interface {
	ObserveNativeRevision(ctx context.Context) (revision int64, idle bool, err error)
}

// IntakeHint is one authenticated native event hint: the delivery identity
// for deduplication, the affected issue, and — for verified creation
// observations only — the creator and their code-write authority at event
// build time. Hints never carry evidence; assessment always re-reads
// authoritative native state before recording anything.
type IntakeHint struct {
	Delivery         string
	Repository       int64
	Issue            int64
	Creator          int64
	Created          bool
	CreatorWrite     bool
	CreationVerified bool
	NativeIssueID    int64
	CreatedUnix      int64
	TitleDigest      string
	ContentDigest    string
}

// Validate rejects malformed hints. Delivery IDs are native task UUIDs;
// creator fields ride creation observations only.
func (h IntakeHint) Validate() error {
	if err := checkIntakeDelivery(h); err != nil {
		return err
	}
	if h.Repository <= 0 || h.Issue <= 0 {
		return errors.New("invalid intake issue scope")
	}
	if err := checkIntakeCreator(h); err != nil {
		return err
	}
	return checkIntakeCreationFacts(h)
}

func checkIntakeDelivery(h IntakeHint) error {
	if h.Delivery == "" || len(h.Delivery) > 128 {
		return errors.New("invalid intake delivery identity")
	}
	for _, r := range h.Delivery {
		if r < 0x20 || r == 0x7f {
			return errors.New("invalid intake delivery identity")
		}
	}
	return nil
}

func checkIntakeCreator(h IntakeHint) error {
	if h.Created && h.Creator <= 0 {
		return errors.New("creation observation needs its creator")
	}
	if !h.Created && (h.Creator != 0 || h.CreatorWrite) {
		return errors.New("creator authority rides creation observations only")
	}
	return nil
}

func checkIntakeCreationFacts(h IntakeHint) error {
	if h.NativeIssueID < 0 || h.CreatedUnix < 0 || !intakeCreationDigest(h.TitleDigest) || !intakeCreationDigest(h.ContentDigest) {
		return errors.New("invalid native creation facts")
	}
	if !h.Created && (h.CreationVerified || h.NativeIssueID != 0 || h.CreatedUnix != 0 || h.TitleDigest != "" || h.ContentDigest != "") {
		return errors.New("creation facts ride creation observations only")
	}
	if h.CreationVerified && (h.NativeIssueID <= 0 || h.CreatedUnix <= 0 || h.TitleDigest == "" || h.ContentDigest == "") {
		return errors.New("incomplete verified creation facts")
	}
	return nil
}

// assessOutcome is one issue assessment: the recorded (or replayed)
// control, whether its inputs changed, and whether the target is a pull
// request, which carries no readiness record.
type assessOutcome struct {
	control       factory.IssueControl
	changed       bool
	skip          bool
	revision      int64
	authorityHash string
}

// ObserveIssueEvent durably retains an authenticated native event before
// attempting admission or assessment. Serving reconciliation resumes transient
// failures; duplicate deliveries cannot overwrite a later human decision.
func (c *Coordinator) ObserveIssueEvent(ctx context.Context, hint IntakeHint) (factory.IssueControl, bool, error) {
	workCtx, _, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	if err := hint.Validate(); err != nil {
		return factory.IssueControl{}, false, err
	}
	seen, err := c.Store.IntakeDeliverySeen(workCtx, hint.Delivery)
	if err != nil {
		return factory.IssueControl{}, false, err
	}
	if seen {
		_, _ = c.drainReadinessWork(workCtx, "")
		control, err := c.Store.IssueControl(workCtx, hint.Repository, hint.Issue)
		if errors.Is(err, store.ErrNotFound) {
			return factory.IssueControl{}, false, nil
		}
		return control, false, err
	}
	work := readinessCreationWork(hint)
	if hint.Created {
		err = c.Store.EnqueueReadinessCreation(workCtx, work)
	} else {
		err = c.Store.EnqueueReadinessWork(workCtx, work.ID, work.Delivery, work.Root)
	}
	if err != nil {
		return factory.IssueControl{}, false, err
	}
	drain, err := c.drainReadinessWork(workCtx, hint.Delivery)
	if !drain.targetDone {
		if err != nil {
			return factory.IssueControl{}, false, err
		}
		return factory.IssueControl{}, false, ErrReadinessPassPending
	}
	control, err := c.Store.IssueControl(workCtx, hint.Repository, hint.Issue)
	if errors.Is(err, store.ErrNotFound) {
		control = factory.IssueControl{}
		err = nil
	}
	if err != nil {
		return factory.IssueControl{}, false, err
	}
	c.dispatchAfterIntake(workCtx)
	return control, drain.targetChanged, nil
}

func readinessDeliverySourceID(delivery string) string {
	digest := sha256.Sum256([]byte(delivery))
	return "delivery:" + hex.EncodeToString(digest[:])
}

// assessOne assesses one issue against authoritative native evidence,
// current acceptance validity and effective authority. Native reads are
// bracketed snapshots through the acceptance source; store reads supply
// acceptance, authority and satisfaction memory. Stable refusals (hidden
// or over-bound evidence) record an explicit verdict; transient failures
// report an error without recording.
func (c *Coordinator) assessOne(ctx context.Context, repository, issue int64) (assessOutcome, error) {
	bounded, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	if c.AcceptanceReads == nil {
		return assessOutcome{}, refuseAcceptance(RefusalSnapshotUnavailable)
	}
	current, err := c.Store.IssueControl(bounded, repository, issue)
	hasCurrent := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return assessOutcome{}, err
	}
	authority, err := c.EffectiveAuthority(bounded, repository)
	if err != nil {
		return assessOutcome{}, err
	}
	head, herr := c.Store.AcceptanceHead(bounded, repository, issue)
	if herr != nil && !errors.Is(herr, store.ErrNotFound) {
		return assessOutcome{}, herr
	}
	assessor := issueAssessor{
		coordinator: c, repository: repository, issue: issue,
		authority: authority, authorityHash: factory.AuthorityFingerprint(authority),
		current: current, hasCurrent: hasCurrent,
	}
	if herr == nil {
		status, evidence, err := c.acceptanceStatus(bounded, repository, strconv.FormatInt(issue, 10))
		if err != nil {
			return assessor.obscured(bounded, head, err)
		}
		assessor.head = head
		assessor.status = &status
		assessor.evidence = &evidence
		assessor.revision = evidence.Revision
	} else {
		evidence, err := c.readAcceptanceEvidence(bounded, factory.Acceptance{
			Repository: repository, IssueIndex: strconv.FormatInt(issue, 10),
		})
		if err != nil {
			return assessor.obscured(bounded, "", err)
		}
		assessor.evidence = &evidence
		assessor.revision = evidence.Revision
	}
	outcome, err := assessor.assess(bounded)
	if err != nil {
		return outcome, err
	}
	outcome.revision = assessor.revision
	outcome.authorityHash = assessor.authorityHash
	return outcome, nil
}

// issueAssessor carries one issue's assessment inputs: the head decision
// and validity when an acceptance exists, the bracketed issue evidence
// always, and the recorded control for satisfaction memory.
type issueAssessor struct {
	coordinator   *Coordinator
	status        *AcceptanceValidity
	evidence      *AcceptanceEvidence
	current       factory.IssueControl
	authority     factory.EffectiveAuthority
	head          string
	authorityHash string
	repository    int64
	issue         int64
	hasCurrent    bool
	revision      int64
}

// obscured records an explicit verdict when the issue evidence itself is
// stably unreadable: hidden issues wait for factory visibility, and
// over-bound issues wait for a narrower scope. Transient bracket failures
// report an error instead so the caller retries with fresh reads.
func (a *issueAssessor) obscured(ctx context.Context, head string, err error) (assessOutcome, error) {
	var refusal *AcceptanceRefusal
	if !errors.As(err, &refusal) {
		return assessOutcome{}, err
	}
	var code, obscured string
	switch refusal.Reason {
	case RefusalIssueHidden:
		code, obscured = factory.BlockerIssueInaccessible, "hidden"
	case RefusalIncompleteEvidence:
		code, obscured = factory.BlockerEvidenceIncomplete, "incomplete"
	default:
		return assessOutcome{}, refusal
	}
	return a.obscuredVerdict(ctx, head, code, obscured)
}

// obscuredVerdict records the explicit verdict for stably unreadable
// issue evidence, carrying forward endpoint and satisfaction memory the
// failed read could not refresh.
func (a *issueAssessor) obscuredVerdict(ctx context.Context, head, code, obscured string) (assessOutcome, error) {
	var blockers []factory.Blocker
	if head == "" {
		blockers = append(blockers, acceptanceBlocker(factory.BlockerAcceptanceMissing, ""))
	}
	blockers = append(blockers, a.authorityBlockers()...)
	blockers = append(blockers, acceptanceBlocker(code, ""))
	verdict, reason := readinessVerdict(blockers)
	candidate := factory.IssueControl{
		Repository: a.repository, Issue: a.issue,
		Acceptance: head, Readiness: verdict, Reason: reason, Blockers: blockers,
		Authority: a.authorityHash,
		Fingerprint: factory.FingerprintInput{
			Acceptance: head, Authority: a.authorityHash, Obscured: obscured,
		}.Fingerprint(),
	}
	if a.hasCurrent {
		candidate.EndpointHeads = a.current.EndpointHeads
		candidate.Satisfied = carrySatisfaction(a.current.Satisfied, head)
	}
	return a.record(ctx, candidate)
}

func acceptanceBlocker(code, detail string) factory.Blocker {
	return factory.Blocker{Code: code, Detail: detail, Resolution: factory.BlockerResolution(code)}
}

func (a *issueAssessor) authorityBlockers() []factory.Blocker {
	var blockers []factory.Blocker
	for _, missing := range a.authority.Missing {
		blockers = append(blockers, acceptanceBlocker(factory.BlockerAuthorityMissing, missing))
	}
	return blockers
}

// readinessVerdict derives the classification from ordered blockers:
// acceptance and authority gaps deny authorization, prerequisite gaps
// block, and no blockers queue the issue for dispatch. The reason names
// the first acceptance or authority gap when one exists.
func readinessVerdict(blockers []factory.Blocker) (string, string) {
	if len(blockers) == 0 {
		return factory.ReadinessQueued, factory.ReasonEligible
	}
	for _, blocker := range blockers {
		switch blocker.Code {
		case factory.BlockerAcceptanceMissing, factory.BlockerAcceptanceInvalid,
			factory.BlockerAuthorityMissing, factory.BlockerIssueInaccessible:
			return factory.ReadinessNotAuthorized, blocker.Code
		}
	}
	return factory.ReadinessBlocked, blockers[0].Code
}

// assess runs the full assessment for readable issue evidence. Hidden
// issues without an acceptance record the inaccessible verdict directly;
// with a head, hidden evidence surfaces as an invalidation reason.
func (a *issueAssessor) assess(ctx context.Context) (assessOutcome, error) {
	if a.evidence.Issue.IsPull {
		return assessOutcome{skip: true}, nil
	}
	if a.head == "" && !a.evidence.Issue.Visible {
		return a.obscuredVerdict(ctx, "", factory.BlockerIssueInaccessible, "hidden")
	}
	var blockers []factory.Blocker
	var validity []string
	if a.head == "" {
		blockers = append(blockers, acceptanceBlocker(factory.BlockerAcceptanceMissing, ""))
	} else if a.status.Withdrawn {
		validity = []string{"withdrawn"}
		blockers = append(blockers, acceptanceBlocker(factory.BlockerAcceptanceInvalid, "withdrawn"))
	} else {
		validity = append([]string(nil), a.status.Reasons...)
		for _, reason := range a.status.Reasons {
			blockers = append(blockers, acceptanceBlocker(factory.BlockerAcceptanceInvalid, reason))
		}
	}
	authorityBlockers := a.authorityBlockers()
	fingerprint := factory.FingerprintInput{
		Acceptance: a.head, Authority: a.authorityHash, Validity: validity,
	}
	candidate := factory.IssueControl{
		Repository: a.repository, Issue: a.issue,
		Acceptance: a.head, Authority: a.authorityHash,
		NativeRev:     a.evidence.Revision,
		EndpointHeads: map[string]string{},
		Satisfied:     map[string]factory.PrereqSatisfaction{},
	}
	if a.head != "" && a.status.Valid && !a.status.Withdrawn {
		prereq, err := a.assessPrerequisites(ctx, a.status.Acceptance, &fingerprint)
		if err != nil {
			return assessOutcome{}, err
		}
		blockers = append(blockers, prereq.invalid...)
		blockers = append(blockers, authorityBlockers...)
		blockers = append(blockers, prereq.blockers...)
		candidate.EndpointHeads = prereq.heads
		candidate.Satisfied = prereq.satisfied
		cycle, exceeded, err := a.coordinator.findPrereqCycle(ctx, a.repository, a.issue)
		if err != nil {
			return assessOutcome{}, err
		}
		switch {
		case exceeded:
			blockers = append(blockers, acceptanceBlocker(factory.BlockerDependencyDepth, ""))
		case cycle != "":
			fingerprint.Cycle = cycle
			blockers = append(blockers, acceptanceBlocker(factory.BlockerCycle, cycle))
		}
	} else {
		blockers = append(blockers, authorityBlockers...)
		if a.hasCurrent {
			candidate.EndpointHeads = a.current.EndpointHeads
			candidate.Satisfied = carrySatisfaction(a.current.Satisfied, a.head)
		}
	}
	verdict, reason := readinessVerdict(blockers)
	candidate.Readiness, candidate.Reason, candidate.Blockers = verdict, reason, blockers
	candidate.Fingerprint = fingerprint.Fingerprint()
	return a.record(ctx, candidate)
}

// record stores one candidate assessment, returning the replayed record
// when its inputs are unchanged.
func (a *issueAssessor) record(ctx context.Context, candidate factory.IssueControl) (assessOutcome, error) {
	stored, changed, err := a.coordinator.Store.RecordIssueAssessment(ctx, candidate, time.Now())
	if err != nil {
		return assessOutcome{}, err
	}
	return assessOutcome{control: stored, changed: changed, revision: a.revision, authorityHash: a.authorityHash}, nil
}

// carrySatisfaction retains satisfaction memory bound to the current head.
// A new acceptance head starts fresh occurrence tracking.
func carrySatisfaction(satisfied map[string]factory.PrereqSatisfaction, head string) map[string]factory.PrereqSatisfaction {
	carried := map[string]factory.PrereqSatisfaction{}
	for occurrence, record := range satisfied {
		if record.Acceptance == head {
			carried[occurrence] = record
		}
	}
	return carried
}

func intakeCreationDigest(value string) bool {
	if value == "" {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func readinessCreationWork(hint IntakeHint) store.ReadinessWork {
	return store.ReadinessWork{
		ID: readinessDeliverySourceID(hint.Delivery), Delivery: hint.Delivery,
		Root:             factory.DependenceRef{Repository: hint.Repository, Issue: hint.Issue},
		CreationVerified: hint.CreationVerified, CreatorID: hint.Creator, CreatorWrite: hint.CreatorWrite,
		NativeIssueID: hint.NativeIssueID, CreatedUnix: hint.CreatedUnix, TitleDigest: hint.TitleDigest, ContentDigest: hint.ContentDigest,
	}
}
