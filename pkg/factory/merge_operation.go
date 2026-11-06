package factory

import (
	"errors"
	"fmt"
)

// MergeOperation is one persisted conditional merge. Work is its
// immutable intent, recorded before submission. Effect, Cancellation
// and Completion mirror the latest native observations; Reason carries
// the bounded native reason code; Receipt carries the bounded raw
// native receipt. Link fields are adopted only from this operation's
// own committed receipt and verified against the submitted intent.
type MergeOperation struct {
	Work           *MergeIntent `json:"work,omitempty"`
	OperationID    string       `json:"operation_id"`
	InstallationID string       `json:"installation_id,omitempty"`
	ActorID        int64        `json:"actor_id,omitempty"`
	RepositoryID   int64        `json:"repository_id,omitempty"`
	Kind           string       `json:"kind"`
	Effect         string       `json:"effect,omitempty"`
	Cancellation   string       `json:"cancellation,omitempty"`
	Completion     string       `json:"completion,omitempty"`
	Reason         string       `json:"reason,omitempty"`
	Receipt        string       `json:"receipt,omitempty"`
	HeadRef        string       `json:"head_ref,omitempty"`
	BaseRef        string       `json:"base_ref,omitempty"`
	HeadOID        string       `json:"head_oid,omitempty"`
	BaseOID        string       `json:"base_oid,omitempty"`
	MergedCommit   string       `json:"merged_commit,omitempty"`
	Attempts       int          `json:"attempts"`
	PRNumber       int64        `json:"pr_number,omitempty"`
	PRID           int64        `json:"pr_id,omitempty"`
	IssueID        int64        `json:"issue_id,omitempty"`
	UpdatedUnix    int64        `json:"updated_unix"`
}

// Validate rejects malformed operation records. An unsubmitted operation
// carries its kind and no observations; a submitted one carries its
// exact identity, attempt count and record time. The effect stays empty
// while its submit is in flight.
func (o MergeOperation) Validate() error {
	if o.Kind != OpMerge {
		return errors.New("invalid merge operation kind")
	}
	if o.Attempts < 0 || o.Attempts > MaxMergeAttempts {
		return errors.New("invalid merge attempt count")
	}
	if o.Attempts == 0 {
		return o.checkUnsubmitted()
	}
	if err := o.checkSubmitted(); err != nil {
		return err
	}
	return o.validateLinks()
}

func (o MergeOperation) checkUnsubmitted() error {
	if o.Work != nil || o.OperationID != "" || o.InstallationID != "" || o.ActorID != 0 || o.RepositoryID != 0 || o.Effect != "" || o.Cancellation != "" ||
		o.Completion != "" || o.Reason != "" || o.Receipt != "" || o.UpdatedUnix != 0 {
		return errors.New("unsubmitted operation carries no observations")
	}
	return o.validateLinks()
}

func (o MergeOperation) checkSubmitted() error {
	if !ValidPublicationOperationID(o.OperationID) {
		return errors.New("invalid merge operation identity")
	}
	if o.Work == nil || o.Work.OperationID != o.OperationID {
		return errors.New("merge operation lacks its immutable intent")
	}
	if err := o.Work.Validate(); err != nil {
		return err
	}
	// An empty effect records a submit in flight: the identity is
	// persisted before its submit so a crash reconciles by lookup and a
	// withdrawal cancels by the same identity. The next pass reconciles
	// it before anything else.
	if o.Effect != "" && !ValidOpEffect(o.Effect) {
		return errors.New("submitted operation effect is unknown")
	}
	if o.UpdatedUnix <= 0 {
		return errors.New("submitted operation lacks its record time")
	}
	if o.Cancellation != "" && !ValidOpCancellation(o.Cancellation) {
		return errors.New("invalid merge cancellation state")
	}
	if o.Completion != "" && !ValidOpCompletion(o.Completion) {
		return errors.New("invalid merge completion state")
	}
	if len(o.Reason) > 64 || len(o.Receipt) > MaxPublicationReceipt {
		return errors.New("merge observation exceeds bounds")
	}
	return nil
}

func (o MergeOperation) validateLinks() error {
	if err := o.checkMergePRPairing(); err != nil {
		return err
	}
	return o.checkMergeSnapshot()
}

func (o MergeOperation) checkMergePRPairing() error {
	if o.PRNumber < 0 || o.PRID < 0 || o.IssueID < 0 {
		return errors.New("invalid merge native links")
	}
	if (o.PRNumber == 0) != (o.PRID == 0) || (o.PRNumber == 0) != (o.IssueID == 0) {
		return errors.New("incomplete merge PR links")
	}
	// A committed merge may await adoption: links arrive only from its
	// own verified receipt. An uncommitted merge carries no links, and
	// partial links never stand.
	if o.Effect != OpEffectCommitted && o.PRNumber != 0 {
		return errors.New("uncommitted merge carries no PR links")
	}
	return nil
}

func (o MergeOperation) checkMergeSnapshot() error {
	if (o.HeadRef != "" && !ValidTargetBranch(o.HeadRef)) || (o.BaseRef != "" && !ValidTargetBranch(o.BaseRef)) {
		return errors.New("invalid merge snapshot refs")
	}
	if (o.HeadOID != "" && !ValidCommit(o.HeadOID)) || (o.BaseOID != "" && !ValidCommit(o.BaseOID)) {
		return errors.New("invalid merge snapshot tips")
	}
	if o.MergedCommit != "" && !ValidCommit(o.MergedCommit) {
		return errors.New("invalid merge realized commit")
	}
	if o.PRNumber != 0 && (o.HeadRef == "" || o.BaseRef == "" || o.HeadOID == "" || o.BaseOID == "" || o.MergedCommit == "") {
		return errors.New("merge PR links lack their snapshot")
	}
	if o.MergedCommit != "" && (o.Work == nil || !o.Work.AcceptsResult(o.MergedCommit)) {
		return errors.New("merge realized commit differs from its method and intent")
	}
	return nil
}

// MergeOperationID derives the deterministic identity for one merge.
// The identity is persisted before its submit so a lost reply
// reconciles by lookup instead of resubmitting blindly; terminal
// identities are never reused.
func MergeOperationID(publicationID string, attempt int) string {
	return fmt.Sprintf("soda-%s-merge-%d", publicationID, attempt)
}

// MergeWork is one executor call's exact bound inputs: the native PR
// identity, the exact candidate and verified base, the independent
// reviewer behind the approval, the policy-bound merge actor, the
// bracketed observation, and the persisted operation identity the call
// must use. No credential or credential-adjacent value enters the work
// record; secrets travel in restricted files outside it. Repository, ActorID,
// PRAuthorID and ReviewerID are canonical Factory keys.
type MergeWork struct {
	Method             string
	MergeID            string
	PublicationID      string
	OperationID        string
	AuthRevision       string
	HeadRef            string
	BaseRef            string
	HeadOID            string
	BaseOID            string
	Repository         int64
	Issue              int64
	PRNumber           int64
	PRID               int64
	IssueID            int64
	PRAuthorID         int64
	ReviewerID         int64
	ActorID            int64
	NativeRev          int64
	NotAfter           int64
	AssessmentRevision int64
	ReviewID           int64
	ReviewEvent        string
}

// MergeIntent contains only the immutable native inputs.
type MergeIntent struct {
	Method             string `json:"merge_method,omitempty"`
	OperationID        string `json:"operation_id"`
	AuthRevision       string `json:"authorization_revision"`
	HeadRef            string `json:"head_ref"`
	BaseRef            string `json:"base_ref"`
	HeadOID            string `json:"head_oid"`
	BaseOID            string `json:"base_oid"`
	Repository         int64  `json:"repository,string"`
	ActorID            int64  `json:"actor_id,string"`
	PRNumber           int64  `json:"pr_number,string"`
	PRID               int64  `json:"pr_id,string"`
	IssueID            int64  `json:"issue_id,string"`
	PRAuthorID         int64  `json:"pr_author_id,string"`
	ReviewerID         int64  `json:"reviewer_id,string"`
	NativeRev          int64  `json:"native_revision"`
	NotAfter           int64  `json:"not_after"`
	AssessmentRevision int64  `json:"assessment_revision"`
	ReviewID           int64  `json:"review_id"`
	ReviewEvent        string `json:"review_event,omitempty"`
}

// Intent takes the credential-free native inputs from an executor call.
func (w MergeWork) Intent() MergeIntent {
	return MergeIntent{
		Method:      w.Method,
		OperationID: w.OperationID, AuthRevision: w.AuthRevision,
		HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID,
		Repository: w.Repository, ActorID: w.ActorID, PRNumber: w.PRNumber, PRID: w.PRID,
		IssueID: w.IssueID, PRAuthorID: w.PRAuthorID, ReviewerID: w.ReviewerID,
		NativeRev: w.NativeRev, NotAfter: w.NotAfter,
		AssessmentRevision: w.AssessmentRevision, ReviewID: w.ReviewID, ReviewEvent: w.ReviewEvent,
	}
}

// Apply restores an exact native intent without changing local inputs.
func (i MergeIntent) Apply(w MergeWork) MergeWork {
	w.Method = i.Method
	w.OperationID, w.AuthRevision = i.OperationID, i.AuthRevision
	w.HeadRef, w.BaseRef, w.HeadOID, w.BaseOID = i.HeadRef, i.BaseRef, i.HeadOID, i.BaseOID
	w.Repository, w.ActorID, w.PRNumber, w.PRID = i.Repository, i.ActorID, i.PRNumber, i.PRID
	w.IssueID, w.PRAuthorID, w.ReviewerID = i.IssueID, i.PRAuthorID, i.ReviewerID
	w.NativeRev, w.NotAfter = i.NativeRev, i.NotAfter
	w.AssessmentRevision, w.ReviewID = i.AssessmentRevision, i.ReviewID
	w.ReviewEvent = i.ReviewEvent
	return w
}

// MergeMethod preserves the meaning of historical intents without this field.
// New policy captures always carry the explicitly selected method.
func (i MergeIntent) MergeMethod() string {
	if i.Method == "" {
		return MergeFastForward
	}
	return i.Method
}

// AcceptsResult validates commit identity after adoption of this operation's own
// native receipt. Non-fast-forward methods can produce a different commit.
func (i MergeIntent) AcceptsResult(commit string) bool {
	return ValidAutomaticMergeMethod(i.MergeMethod()) && ValidCommit(commit) &&
		(i.MergeMethod() != MergeFastForward || commit == i.HeadOID)
}

// ValidateTarget checks the exact merge target before evidence or
// operation identity exist. Agent independence belongs to the recorded review
// assignment; provider account identity alone cannot establish it.
func (w MergeWork) ValidateTarget() error {
	if !ValidAutomaticMergeMethod(w.Intent().MergeMethod()) {
		return errors.New("unsupported merge method")
	}
	if w.Repository <= 0 || w.Issue <= 0 || w.PRNumber <= 0 || w.PRID <= 0 || w.IssueID <= 0 {
		return errors.New("invalid merge target scope")
	}
	if w.ActorID <= 0 || w.PRAuthorID <= 0 || w.ReviewerID <= 0 {
		return errors.New("merge review evidence lacks its account identity")
	}
	if !ValidTargetBranch(w.HeadRef) || !ValidTargetBranch(w.BaseRef) || w.HeadRef == w.BaseRef {
		return errors.New("invalid merge refs")
	}
	if !ValidCommit(w.HeadOID) || !ValidCommit(w.BaseOID) || w.HeadOID == w.BaseOID {
		return errors.New("merge candidate is not a fresh exact commit")
	}
	return nil
}

// Validate rejects incomplete immutable native intents.
func (i MergeIntent) Validate() error { return i.validate(true) }

func (i MergeIntent) validate(observed bool) error {
	if err := i.checkMergeIntentScope(); err != nil {
		return err
	}
	return i.checkMergeIntentBinding(observed)
}

func (i MergeIntent) checkMergeIntentScope() error {
	if !ValidAutomaticMergeMethod(i.MergeMethod()) {
		return errors.New("unsupported merge method")
	}
	if !ValidTargetBranch(i.HeadRef) || !ValidTargetBranch(i.BaseRef) || i.HeadRef == i.BaseRef {
		return errors.New("invalid merge refs")
	}
	if !ValidCommit(i.HeadOID) || !ValidCommit(i.BaseOID) || i.HeadOID == i.BaseOID {
		return errors.New("merge candidate is not a fresh exact commit")
	}
	if !ValidPublicationOperationID(i.OperationID) {
		return errors.New("invalid merge operation identity")
	}
	if i.Repository <= 0 || i.ActorID <= 0 || i.PRNumber <= 0 || i.PRID <= 0 || i.IssueID <= 0 {
		return errors.New("invalid merge work scope")
	}
	if i.PRAuthorID <= 0 || i.ReviewerID <= 0 {
		return errors.New("merge review evidence lacks its account identity")
	}
	return nil
}

func (i MergeIntent) checkMergeIntentBinding(observed bool) error {
	if i.ReviewerID == i.PRAuthorID {
		if i.ReviewEvent != "COMMENT" {
			return errors.New("shared-account merge requires its comment review receipt")
		}
	} else if i.ReviewEvent != "" && i.ReviewEvent != "APPROVED" {
		return errors.New("merge review is not an approval")
	}
	if i.AuthRevision == "" || len(i.AuthRevision) > 512 {
		return errors.New("invalid merge authorization revision")
	}
	if i.NativeRev < 0 || i.NotAfter < 0 || (observed && (i.NativeRev == 0 || i.NotAfter == 0)) {
		return errors.New("invalid merge observation binding")
	}
	if observed && (i.AssessmentRevision <= 0 || i.ReviewID <= 0) {
		return errors.New("merge intent lacks its bound check and review evidence")
	}
	if i.AssessmentRevision < 0 || i.ReviewID < 0 {
		return errors.New("invalid merge evidence binding")
	}
	return nil
}

// ValidateObservation checks target and identity inputs before native
// evidence is observed; only the native revision, deadline and evidence
// bindings may be absent.
func (w MergeWork) ValidateObservation() error {
	if !ValidID(w.MergeID) || !ValidID(w.PublicationID) || w.Issue <= 0 {
		return errors.New("invalid merge work identity")
	}
	return w.Intent().validate(false)
}

// MaxMergeGenerations bounds the immutable authorization generations per
// publication (D04). Reaching the cap is a visible intervention; never
// create another PR to escape it. Replacement never resets any attempt
// allowance: the ledger carries no allowance state at all.
const MaxMergeGenerations = 32

// MergeGenerationOperationID derives the deterministic native operation
// identity for one authorization generation from publication plus
// generation. Content is immutable; terminal identities are never reused,
// and a lost reply reconciles by lookup instead of resubmitting.
func MergeGenerationOperationID(publicationID string, generation int) string {
	return fmt.Sprintf("soda-%s-merge-generation-%d", publicationID, generation)
}

// MergeOperationIDForGeneration derives the submit identity for one row's
// current authorization generation. Generation one keeps its historic
// first-attempt identity; later generations use the generation
// derivation. Every generation drives a distinct native operation, so a
// crash or lost reply reconciles by exact lookup without resubmitting
// and a replacement never reuses a finalized identity.
func MergeOperationIDForGeneration(publicationID string, generation int) string {
	if generation <= 1 {
		return MergeOperationID(publicationID, 1)
	}
	return MergeGenerationOperationID(publicationID, generation)
}

// MergeGenerationSnapshot is one finalized prior authorization
// generation (D09/H3): its immutable authorization, its native
// operation with intent, observations, receipts and terminal effect,
// and its terminal stage, outcome, reason and finish time. Snapshots
// are flat: they never nest another history, so the ledger stays one
// row with one current authority and bounded immutable priors. The
// exact tuple and authority travel inside the snapshot authorization's
// generation.
type MergeGenerationSnapshot struct {
	Authorization MergeAuthorization `json:"authorization"`
	Operation     MergeOperation     `json:"operation"`
	Stage         string             `json:"stage"`
	Outcome       Outcome            `json:"outcome,omitempty"`
	Reason        string             `json:"reason,omitempty"`
	FinishedUnix  int64              `json:"finished_unix,omitempty"`
}

// SnapshotMergeGeneration captures the finalized predecessor behind a
// replacement: the row's exact current authorization, operation and
// terminal bookkeeping. Only a finalized predecessor snapshots; an
// open, pending or committed one never enters history.
func SnapshotMergeGeneration(auth MergeAuthorization, op MergeOperation, stage string, outcome Outcome, reason string, finishedUnix int64) MergeGenerationSnapshot {
	return MergeGenerationSnapshot{
		Authorization: auth, Operation: op,
		Stage: stage, Outcome: outcome, Reason: reason, FinishedUnix: finishedUnix,
	}
}

// Validate checks one history snapshot: a bound generation, a recorded
// operation, a terminal stage matching its outcome, and a predecessor
// proof behind the replacement it already justified. Uncertainty never
// enters history.
func (s MergeGenerationSnapshot) Validate() error {
	if !s.Authorization.Bound() {
		return errors.New("merge history snapshot lacks its authorization")
	}
	if err := s.Authorization.Generation.Validate(); err != nil {
		return err
	}
	if err := s.Operation.Validate(); err != nil {
		return err
	}
	switch s.Stage {
	case MergeWithdrawn:
		if s.Outcome != Cancelled || s.Reason != MergeReasonWithdrawn || s.FinishedUnix <= 0 {
			return errors.New("withdrawn merge history lacks its outcome")
		}
	case MergeFailed:
		if s.Outcome != Failed || s.Reason != MergeReasonRefused || s.FinishedUnix <= 0 {
			return errors.New("refused merge history lacks its outcome")
		}
	default:
		return errors.New("merge history snapshot is not a finalized predecessor")
	}
	if _, err := MergePredecessorProofOf(s.Authorization.Generation.Generation, s.Operation); err != nil {
		return err
	}
	return nil
}

// MergePredecessorProofOf derives the replacement proof from one stored
// predecessor operation. A confirmed cancellation proves replaceability
// whatever bookkeeping reason it carries; otherwise the recorded
// deterministic refusal reason is the proof, with the native idle
// marker normalized away. Pending, indeterminate, unobserved, reasonless
// and committed predecessors prove nothing and forbid replacement.
func MergePredecessorProofOf(generation int, op MergeOperation) (MergePredecessorProof, error) {
	var empty MergePredecessorProof
	proof := MergePredecessorProof{Generation: generation}
	switch {
	case op.Effect == OpEffectNotCommitted && op.Cancellation == OpCancelCancelled:
		proof.Effect, proof.Cancellation = op.Effect, op.Cancellation
	case op.Effect == OpEffectNotCommitted && (op.Cancellation == "" || op.Cancellation == OpCancelNone) && op.Reason != "":
		proof.Effect, proof.NoEffectRefusal = op.Effect, op.Reason
	default:
		return empty, errors.New("uncertain merge predecessor forbids replacement")
	}
	if err := proof.Validate(); err != nil {
		return empty, err
	}
	return proof, nil
}

// MergeGeneration is one merge authorization generation (D09): decision,
// review round, semantic fingerprint, exact head/base/check evidence,
// authority, observed native revision and deadline, plus the derived
// native operation identity. FW37 generations additionally capture the
// D07 delivery-acceptance union behind the admission: acceptance kind/ID,
// attempt root and originating receipt. Candidate, base, authority,
// deadline and native-operation fields belong to each generation;
// history is append-only and prior generations are never rewritten.
type MergeGeneration struct {
	Authority          AuthorityRef `json:"authority"`
	PublicationID      string       `json:"publication_id"`
	DecisionID         string       `json:"decision_id"`
	NativeOperationID  string       `json:"native_operation_id"`
	Fingerprint        string       `json:"fingerprint"`
	Head               string       `json:"head"`
	Base               string       `json:"base"`
	DiffBase           string       `json:"diff_base"`
	ChecksDigest       string       `json:"checks_digest"`
	AcceptanceKind     string       `json:"acceptance_kind,omitempty"`
	AcceptanceID       string       `json:"acceptance_id,omitempty"`
	AttemptRoot        string       `json:"attempt_root,omitempty"`
	OriginReceipt      string       `json:"origin_receipt,omitempty"`
	Generation         int          `json:"generation"`
	ReviewRound        int          `json:"review_round"`
	AssessmentRevision int64        `json:"assessment_revision"`
	ObservedNativeRev  int64        `json:"observed_native_revision"`
	NotAfter           int64        `json:"not_after"`
}

// UnionBound reports whether the generation carries its D07 delivery
// acceptance: FW37 registrations bind the union, while earlier committed
// rows bound only the funded decision and keep validating on that path.
func (g MergeGeneration) UnionBound() bool { return g.AcceptanceID != "" }

// Validate checks the immutable generation content.
func (g MergeGeneration) Validate() error {
	if !ValidID(g.PublicationID) {
		return errors.New("invalid merge generation identity")
	}
	if !g.UnionBound() && !ValidID(g.DecisionID) {
		return errors.New("invalid merge generation identity")
	}
	if err := g.validateUnionShape(); err != nil {
		return err
	}
	if g.Generation < 1 {
		return errors.New("invalid merge generation number")
	}
	if g.NativeOperationID != MergeGenerationOperationID(g.PublicationID, g.Generation) {
		return errors.New("merge generation operation differs from its derivation")
	}
	if !ValidDigest(g.Fingerprint) {
		return errors.New("merge generation lacks its semantic fingerprint")
	}
	if !ValidCommit(g.Head) || !ValidCommit(g.Base) || !ValidCommit(g.DiffBase) || g.Head == g.Base {
		return errors.New("merge generation candidate is not an exact head over a verified base")
	}
	if g.ReviewRound < 1 {
		return errors.New("invalid merge generation review round")
	}
	if g.AssessmentRevision <= 0 || !ValidDigest(g.ChecksDigest) {
		return errors.New("merge generation lacks its bound check evidence")
	}
	if g.Authority.Policy <= 0 {
		return errors.New("merge generation lacks its admitted policy authority")
	}
	if g.ObservedNativeRev <= 0 || g.NotAfter <= 0 {
		return errors.New("invalid merge generation observation binding")
	}
	return nil
}

// validateUnionShape checks the FW37 acceptance capture when present.
// Absence keeps the earlier funded-decision-only shape: committed rows
// predate the union and never gain it retroactively. A clean generation
// spends no funded decision; a funded generation names it.
func (g MergeGeneration) validateUnionShape() error {
	if !g.UnionBound() {
		return nil
	}
	switch g.AcceptanceKind {
	case AcceptanceCleanReview:
		if g.DecisionID != "" {
			return errors.New("clean merge generation spends no delivery judgment")
		}
	case AcceptanceJevAcceptance:
		if !ValidID(g.DecisionID) {
			return errors.New("funded merge generation lacks its decision")
		}
	default:
		return errors.New("invalid merge generation acceptance kind")
	}
	if !ValidID(g.AcceptanceID) || !ValidID(g.AttemptRoot) || !ValidID(g.OriginReceipt) {
		return errors.New("merge generation lacks its acceptance binding")
	}
	return nil
}

// MergePredecessorProof is the confirmed predecessor effect behind a
// replacement: either the predecessor proven not_committed with confirmed
// cancellation, or a deterministic no-effect refusal. Pending,
// indeterminate or committed predecessors prove nothing and forbid
// replacement; a committed predecessor is adopted, never replaced.
type MergePredecessorProof struct {
	Generation      int    `json:"generation"`
	Effect          string `json:"effect,omitempty"`
	Cancellation    string `json:"cancellation,omitempty"`
	NoEffectRefusal string `json:"no_effect_refusal,omitempty"`
}

// Validate checks the proof shape. Uncertainty never proves replaceability.
func (p MergePredecessorProof) Validate() error {
	if p.Generation < 1 {
		return errors.New("invalid merge predecessor proof generation")
	}
	cancelled := p.Effect == OpEffectNotCommitted && p.Cancellation == OpCancelCancelled && p.NoEffectRefusal == ""
	refused := p.Effect == OpEffectNotCommitted && p.Cancellation == "" && p.NoEffectRefusal != "" && len(p.NoEffectRefusal) <= 64
	if !cancelled && !refused {
		return errors.New("uncertain merge predecessor forbids replacement")
	}
	return nil
}

// MergeLedger is one publication's factory_merges ledger (D09): stable
// publication/repository/issue identity, one current authorization
// generation and bounded immutable prior-generation history. Adoption
// marks the terminal observation of a committed merge. Store rows
// persist this ledger as live current-generation fields plus bounded
// MergeGenerationSnapshot history; UpdateMerge enforces this contract
// on every write.
type MergeLedger struct {
	PublicationID string            `json:"publication_id"`
	History       []MergeGeneration `json:"history,omitempty"`
	Current       MergeGeneration   `json:"current,omitempty"`
	AdoptedCommit string            `json:"adopted_commit,omitempty"`
	Repository    int64             `json:"repository,string"`
	Issue         int64             `json:"issue,string"`
	Adopted       bool              `json:"adopted"`
}

// Validate checks append-only history: consecutive generations from one,
// stable identity everywhere, the current generation next in line and the
// D04 history ceiling over current plus history. An empty ledger and an
// adopted terminal ledger both validate.
func (l MergeLedger) Validate() error {
	if !ValidID(l.PublicationID) || l.Repository <= 0 || l.Issue <= 0 {
		return errors.New("invalid merge ledger identity")
	}
	for i, generation := range l.History {
		if err := generation.Validate(); err != nil {
			return err
		}
		if generation.Generation != i+1 || generation.PublicationID != l.PublicationID {
			return errors.New("merge ledger history is not append-only")
		}
	}
	total := len(l.History)
	if l.Current.Generation == 0 {
		if l.Current != (MergeGeneration{}) {
			return errors.New("merge ledger carries a cleared current generation")
		}
	} else {
		if err := l.Current.Validate(); err != nil {
			return err
		}
		if l.Current.Generation != len(l.History)+1 || l.Current.PublicationID != l.PublicationID {
			return errors.New("merge ledger current generation breaks its history")
		}
		total++
	}
	if total > MaxMergeGenerations {
		return errors.New("merge generation history exceeds its bound")
	}
	if l.Adopted {
		if l.Current.Generation == 0 || !ValidCommit(l.AdoptedCommit) || l.AdoptedCommit != l.Current.Head {
			return errors.New("adopted merge lacks its realized head")
		}
	} else if l.AdoptedCommit != "" {
		return errors.New("unadopted merge carries no realized commit")
	}
	return nil
}

// MergeAuthorization is one merge row's admitted authorization (D09):
// the generation seeking admission, the current applicable
// ready_to_merge decision over its exact fingerprint, and the producer's
// attestation that every accepted fix resolved and read back confirmed.
// The row carries the binding immutably once registered; every new native
// mutation re-verifies it through CheckMergeDecisionGate against current
// authority before spending. An unbound row is pending authorization,
// never admitted: it reconciles recorded effects but submits nothing.
type MergeAuthorization struct {
	Generation           MergeGeneration  `json:"generation"`
	Decision             DeliveryDecision `json:"decision"`
	ResolutionsConfirmed bool             `json:"resolutions_confirmed"`
}

// Bound reports whether the row carries an authorization at all. The
// row holds exactly one current authorization; prior generations rest
// in its bounded immutable history, never beside it.
func (a MergeAuthorization) Bound() bool { return a.Generation.Generation != 0 }

// Validate enforces the registration-time decision gate against the
// generation's own authority: a valid generation, a valid
// effective ready_to_merge decision over its exact fingerprint,
// confirmed accepted resolutions and matching authority. Union-bound
// generations check the union shape instead: a clean binding carries no
// funded decision, a funded binding carries its ready decision, and both
// carry confirmed resolutions. Full record binding against the journaled
// acceptance runs in CheckMergeUnionBinding, which sees the publication.
// Ledger position (generation == history length + 1) is a row-level
// check: the store row validates it through its MergeLedger view.
func (a MergeAuthorization) Validate() error {
	if !a.Bound() {
		return errors.New("merge authorization is not bound")
	}
	if a.Generation.Generation < 1 {
		return errors.New("invalid merge authorization generation")
	}
	if a.Generation.UnionBound() {
		return a.validateUnionShape()
	}
	return CheckMergeDecisionGate(MergeDecisionInput{
		Generation: a.Generation, Decision: a.Decision,
		CurrentAuthority: a.Generation.Authority, ResolutionsConfirmed: a.ResolutionsConfirmed,
	})
}

// deliveryDecisionZero reports whether no funded decision travels here:
// a clean union binding spends no delivery judgment at all.
func deliveryDecisionZero(d DeliveryDecision) bool {
	return d.Effective == "" && d.Answer.Selected == "" && d.Answer.Fingerprint == ""
}

// validateUnionShape checks the union binding without its journaled
// acceptance record: kind/decision presence plus confirmed resolutions.
// The record cross-check belongs to CheckMergeUnionBinding.
func (a MergeAuthorization) validateUnionShape() error {
	if err := a.Generation.Validate(); err != nil {
		return err
	}
	switch a.Generation.AcceptanceKind {
	case AcceptanceCleanReview:
		if !deliveryDecisionZero(a.Decision) {
			return errors.New("clean merge authorization spends no delivery judgment")
		}
	case AcceptanceJevAcceptance:
		if err := a.Decision.Validate(); err != nil {
			return err
		}
		if a.Decision.Effective != DeliveryEffectiveReady {
			return errors.New("merge decision is not ready_to_merge")
		}
		if a.Decision.Answer.Fingerprint != a.Generation.Fingerprint {
			return errors.New("merge decision judged different evidence")
		}
	default:
		return errors.New("invalid merge authorization acceptance kind")
	}
	if !a.ResolutionsConfirmed {
		return errors.New("merge authorization lacks confirmed accepted resolutions")
	}
	return nil
}

// MergeDecisionInput gathers the single decision gate (D09) behind every
// new mutation path: the generation seeking admission, the current
// applicable ready_to_merge decision, confirmed accepted resolutions and
// the current authority. Publication progress, MergePass, mergeOne and
// reconciliation (FW22) all call CheckMergeDecisionGate; recovery
// observes the recorded operation instead of entering the gate.
type MergeDecisionInput struct {
	Generation           MergeGeneration  `json:"generation"`
	Decision             DeliveryDecision `json:"decision"`
	CurrentAuthority     AuthorityRef     `json:"current_authority"`
	ResolutionsConfirmed bool             `json:"resolutions_confirmed"`
}

// CheckMergeDecisionGate enforces the common gate: a valid generation, a
// valid effective ready_to_merge decision over the generation's exact
// fingerprint, confirmed accepted resolutions and unchanged authority.
// Recovery of an already-committed merge never enters this gate and
// consumes no new Jev call.
func CheckMergeDecisionGate(in MergeDecisionInput) error {
	if err := in.Generation.Validate(); err != nil {
		return err
	}
	if err := in.Decision.Validate(); err != nil {
		return err
	}
	if in.Decision.Effective != DeliveryEffectiveReady {
		return errors.New("merge decision is not ready_to_merge")
	}
	if in.Decision.Answer.Fingerprint != in.Generation.Fingerprint {
		return errors.New("merge decision judged different evidence")
	}
	if !in.ResolutionsConfirmed {
		return errors.New("merge decision lacks confirmed accepted resolutions")
	}
	if in.CurrentAuthority != in.Generation.Authority {
		return errors.New("merge authority changed under its authorization")
	}
	return nil
}

// MergeUnionBindingInput gathers the FW37 union gate behind every new
// mutation path: the publication carrying the journaled acceptance, the
// current applicable acceptance record, the generation seeking admission,
// the funded decision behind a funded binding (zero for a clean binding),
// confirmed accepted resolutions and the current authority. Recovery of
// an already-committed merge never enters this gate and consumes no new
// funded call.
type MergeUnionBindingInput struct {
	Publication          Publication
	Acceptance           DeliveryAcceptance
	Generation           MergeGeneration
	Decision             DeliveryDecision
	ResolutionsConfirmed bool
	CurrentAuthority     AuthorityRef
}

// CheckMergeUnionBinding enforces the common union gate: a valid
// journaled acceptance bound to the publication's exact current
// candidate and base, the generation's exact acceptance capture, the
// kind-matching decision evidence and confirmed accepted resolutions
// under unchanged authority. Both variants pass these identical gates;
// a stale or absent union refuses every new mutation, however complete
// the native state looks.
func CheckMergeUnionBinding(in MergeUnionBindingInput) error {
	if err := in.Acceptance.Validate(); err != nil {
		return err
	}
	if err := in.Generation.Validate(); err != nil {
		return err
	}
	if !in.Generation.UnionBound() {
		return errors.New("merge generation lacks its acceptance binding")
	}
	if err := checkUnionAcceptanceCapture(in); err != nil {
		return err
	}
	if err := checkUnionHeadCurrency(in); err != nil {
		return err
	}
	if err := checkUnionDecisionKind(in); err != nil {
		return err
	}
	if !in.ResolutionsConfirmed {
		return errors.New("merge binding lacks confirmed accepted resolutions")
	}
	if in.CurrentAuthority != in.Generation.Authority {
		return errors.New("merge authority changed under its authorization")
	}
	return nil
}

func checkUnionAcceptanceCapture(in MergeUnionBindingInput) error {
	if in.Acceptance.PublicationID != in.Publication.ID {
		return errors.New("delivery acceptance belongs to another publication")
	}
	if in.Acceptance.ID != in.Generation.AcceptanceID || in.Acceptance.Kind != in.Generation.AcceptanceKind ||
		in.Acceptance.AttemptRoot != in.Generation.AttemptRoot || in.Acceptance.OriginReceipt != in.Generation.OriginReceipt {
		return errors.New("merge generation answers another acceptance")
	}
	if in.Acceptance.ID != DeliveryAcceptanceID(in.Publication.ID, in.Acceptance.AttemptRoot, in.Acceptance.Round, in.Acceptance.Kind) {
		return errors.New("delivery acceptance identity differs from its derivation")
	}
	if in.Acceptance.Round != in.Generation.ReviewRound {
		return errors.New("merge generation answers another review round")
	}
	if in.Acceptance.Tuple.Head != in.Generation.Head || in.Acceptance.Tuple.Base != in.Generation.Base ||
		in.Acceptance.Tuple.DiffBase != in.Generation.DiffBase {
		return errors.New("merge generation answers another candidate")
	}
	return nil
}

func checkUnionHeadCurrency(in MergeUnionBindingInput) error {
	// Current applicability: the acceptance must still answer the
	// publication's exact current candidate over its recorded base. A
	// superseded acceptance never rides a new mutation.
	if in.Acceptance.Tuple.Head != in.Publication.Candidate || in.Acceptance.Tuple.Base != in.Publication.PRCreate.BaseOID {
		return errors.New("delivery acceptance answers a superseded tuple")
	}
	return nil
}

func checkUnionDecisionKind(in MergeUnionBindingInput) error {
	switch in.Acceptance.Kind {
	case AcceptanceCleanReview:
		if !deliveryDecisionZero(in.Decision) {
			return errors.New("clean merge binding spends no delivery judgment")
		}
		if len(in.Acceptance.ResolutionIntents) != 0 {
			return errors.New("clean review resolves no native thread")
		}
	case AcceptanceJevAcceptance:
		if err := in.Decision.Validate(); err != nil {
			return err
		}
		if in.Decision.Effective != DeliveryEffectiveReady {
			return errors.New("merge decision is not ready_to_merge")
		}
		if in.Decision.Answer.Fingerprint != in.Generation.Fingerprint {
			return errors.New("merge decision judged different evidence")
		}
		if in.Acceptance.DecisionID != in.Generation.DecisionID {
			return errors.New("funded acceptance differs from its generation decision")
		}
	default:
		return errors.New("invalid delivery acceptance kind")
	}
	return nil
}

// Validate adds the exact native observation needed before submission.
func (w MergeWork) Validate() error {
	if err := w.ValidateObservation(); err != nil {
		return err
	}
	return w.Intent().Validate()
}
