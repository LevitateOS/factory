package control

import (
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// MergeNativeGate is the re-evaluated native mergeable + reviewable
// expression behind one merge judgment (T24/T25). It carries the fresh
// native verdict only; reads perform no mutation and grant none. Tests
// supply mergeableGate / blockedGate fixtures; production callers
// re-evaluate from the FW17 read adapters before any funded call.
type MergeNativeGate struct {
	Eligible         bool
	Blocker          string
	Approved         bool
	ChangesRequested bool
	HeadOID          string
	BaseOID          string
	Visible          bool
	Complete         bool
}

// Validate checks the gate shape: exact tips plus the bounded blocker
// vocabulary (empty means eligible). Hidden/incomplete evidence is
// shaped here and waits at gate time; it never validates away.
func (g MergeNativeGate) Validate() error {
	if !factory.ValidCommit(g.HeadOID) || !factory.ValidCommit(g.BaseOID) || g.HeadOID == g.BaseOID {
		return errors.New("merge gate lacks its exact head over a verified base")
	}
	if g.Eligible {
		if g.Blocker != "" {
			return errors.New("eligible merge carries its blocker")
		}
		return nil
	}
	switch g.Blocker {
	case "merged", "closed",
		"permission", "work_in_progress",
		"not_mergeable", "checking",
		"status_checks", "approvals",
		"requested_changes", "review_requests",
		"outdated_branch", "protected_files",
		"signature", "dependency",
		"branch_protection":
		return nil
	default:
		return errors.New("merge gate blocker is unknown")
	}
}

// mergeGateProtected reports whether a native blocker refuses before any
// funded call (revoked or protected: human intervention, never an
// automatic retry on the same evidence).
func mergeGateProtected(blocker string) bool {
	switch blocker {
	case "merged", "closed",
		"permission", "protected_files",
		"branch_protection", "signature":
		return true
	default:
		return false
	}
}

// MergeJudgmentGateInput gathers the single T24/T25 pre-spend gate: the
// eligible publication, the applicable admitted correction, its fresh
// review cycle, the accepted-resolution plan with its exact readback,
// the bounded authorization generation seeking admission, and the
// re-evaluated native mergeable + reviewable expression. D13
// completion/merge joints stay open: this gate never finishes,
// fences or completes.
type MergeJudgmentGateInput struct {
	Publication factory.Publication
	Admission   CorrectionAdmission
	Cycle       factory.ReviewCycle
	Acceptance  JudgmentAcceptance
	Planned     []AcceptedResolution
	Confirmed   map[string]bool
	Generation  factory.MergeGeneration
	Native      MergeNativeGate
}

// mergeGateBase returns the publication's verified base: the recorded PR
// creation base when linked, else the accepted base SHA.
func mergeGateBase(p factory.Publication) string {
	if p.PRCreate.Work != nil && factory.ValidCommit(p.PRCreate.BaseOID) {
		return p.PRCreate.BaseOID
	}
	return p.BaseSHA
}

// CheckMergeJudgmentReady enforces the deterministic T24/T25 gate before
// any provider spend. Every branch below is cheap: malformed inputs
// refuse, superseded or protected merges refuse, and transient or
// incomplete evidence waits. A nil error means the caller may spend one
// funded delivery call; any error means it must not.
func CheckMergeJudgmentReady(in MergeJudgmentGateInput) error {
	if err := in.Generation.Validate(); err != nil {
		return err
	}
	if err := in.Cycle.Validate(); err != nil {
		return err
	}
	if err := in.Native.Validate(); err != nil {
		return err
	}
	base, err := checkJudgmentPublication(in)
	if err != nil {
		return err
	}
	if err := checkJudgmentAdmission(in); err != nil {
		return err
	}
	if err := checkJudgmentCycle(in, base); err != nil {
		return err
	}
	if err := checkJudgmentResolutions(in); err != nil {
		return err
	}
	return checkJudgmentNative(in)
}

// checkJudgmentPublication requires an eligible merge: a published,
// linked PR on a fresh exact candidate, returning its verified base.
func checkJudgmentPublication(in MergeJudgmentGateInput) (string, error) {
	switch in.Publication.Stage {
	case factory.PublicationPublished:
	case factory.PublicationWithdrawn, factory.PublicationFailed, factory.PublicationFenced:
		return "", &factory.PublicationRefusal{Reason: "publication_" + in.Publication.Stage}
	default:
		return "", &factory.PublicationWait{Reason: "publication_not_published"}
	}
	if in.Publication.PRNumber <= 0 || in.Publication.PRID <= 0 {
		return "", &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	base := mergeGateBase(in.Publication)
	if !factory.ValidCommit(in.Publication.Candidate) || !factory.ValidCommit(base) ||
		in.Publication.Candidate == base {
		return "", &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	if in.Generation.PublicationID != in.Publication.ID {
		return "", &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	if in.Generation.Head != in.Publication.Candidate {
		return "", &factory.PublicationRefusal{Reason: "superseded_head"}
	}
	if in.Generation.Base != base {
		return "", &factory.PublicationRefusal{Reason: "superseded_base"}
	}
	if !factory.ValidDigest(in.Generation.Fingerprint) {
		return "", &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	return base, nil
}

// checkJudgmentAdmission requires the applicable fix: the admission
// answers this cycle's exact finding set on the current head.
// Unresolved findings wait for correction; a superseded correction
// refuses without spending.
func checkJudgmentAdmission(in MergeJudgmentGateInput) error {
	if in.Admission.CycleID == "" || in.Admission.CycleID != in.Cycle.ID {
		return &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	if in.Admission.SetDigest != in.Cycle.FindingDigest {
		return &factory.PublicationRefusal{Reason: "judgment_stale"}
	}
	if in.Admission.NewCandidate != in.Publication.Candidate {
		return &factory.PublicationRefusal{Reason: "superseded_head"}
	}
	if len(in.Admission.Unresolved) > 0 {
		return &factory.PublicationWait{Reason: "correction_pending"}
	}
	return nil
}

// checkJudgmentCycle requires a fresh re-review: the recorded cycle
// binds this publication, head, base and finding set. A cycle for
// another head waits for its fresh reviewer; a digest mismatch refuses
// as stale evidence.
func checkJudgmentCycle(in MergeJudgmentGateInput, base string) error {
	if in.Cycle.PublicationID != in.Publication.ID {
		return &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	if in.Cycle.Head != in.Publication.Candidate || in.Cycle.BaseOID != base {
		return &factory.PublicationWait{Reason: "re_review_pending"}
	}
	if in.Cycle.FindingDigest != in.Admission.SetDigest {
		return &factory.PublicationRefusal{Reason: "judgment_stale"}
	}
	return nil
}

// checkJudgmentResolutions requires the accepted resolution: the
// opaque judgment binding must still match the admitted set, and every
// accepted fix must read back confirmed. Unconfirmed fixes wait;
// without a binding nothing resolves however complete the native
// state looks.
func checkJudgmentResolutions(in MergeJudgmentGateInput) error {
	if err := in.Acceptance.Validate(in.Admission.SetDigest); err != nil {
		return &factory.PublicationRefusal{Reason: "judgment_stale"}
	}
	outcome, err := ReconcileAcceptedResolutions(in.Acceptance, in.Admission, in.Planned, in.Confirmed)
	if err != nil {
		return &factory.PublicationRefusal{Reason: "invalid_work"}
	}
	if len(outcome.Waits) > 0 {
		return &factory.PublicationWait{Reason: "resolution_pending"}
	}
	return nil
}

// checkJudgmentNative requires the re-evaluated mergeable +
// reviewable expression: fresh native tips still match the generation;
// hidden or incomplete evidence blocks without spending. A blocking
// independent review waits for its correction; revoked or protected
// merges refuse before any funded call.
func checkJudgmentNative(in MergeJudgmentGateInput) error {
	if !in.Native.Visible || !in.Native.Complete {
		return &factory.PublicationWait{Reason: "merge_evidence_unavailable"}
	}
	if in.Native.HeadOID != in.Generation.Head {
		return &factory.PublicationRefusal{Reason: "superseded_head"}
	}
	if in.Native.BaseOID != in.Generation.Base {
		return &factory.PublicationRefusal{Reason: "superseded_base"}
	}
	if in.Native.ChangesRequested {
		return &factory.PublicationWait{Reason: "changes_requested"}
	}
	if !in.Native.Approved {
		return &factory.PublicationWait{Reason: "approval_missing"}
	}
	if !in.Native.Eligible {
		if mergeGateProtected(in.Native.Blocker) {
			return &factory.PublicationRefusal{Reason: in.Native.Blocker}
		}
		return &factory.PublicationWait{Reason: in.Native.Blocker}
	}
	return nil
}
