// D16 whole-app verification subjects and typed result envelope. Test
// Pilot verifies a delivered accepted intent version; one in-scope
// Strategist repair proposal may answer each failed verification. This
// file owns the admission identity for both kinds and the parity result
// envelope R10 binds to the repair/verify phases. Extended D15/D16
// cause, coverage, adoption and receipt semantics belong to the R37-R40
// finish controls; the envelope here carries the identity, digest and
// outcome bindings every phase-role-subject-result matrix case needs.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/levitateos/factory/pkg/project"
)

// The fixed approved-check route shares one allowance across every accepted
// revision and explicit Retry of the same stable intent.
const (
	DefaultAppVerificationRuns          = 2
	DefaultAppVerificationActiveMinutes = 60
	MaxAppVerificationRunMinutes        = 30
	DefaultDeliveryRepairRuns           = 2
)

// DeliverySubject binds one app verification to its accepted intent
// version: the delivery identity, the accepted intent revision and scope,
// the source and preparation it was admitted against, and the approved
// verification manifest it must execute. Key `delivery:<delivery_id>`,
// phase `verify`, reviewer account, no publication or children.
type DeliverySubject struct {
	DeliveryID                 string `json:"delivery_id"`
	IntentID                   string `json:"intent_id"`
	AcceptedRevision           string `json:"accepted_revision"`
	ScopeDigest                string `json:"scope_digest"`
	SourceCommit               string `json:"source_commit"`
	PreparationDigest          string `json:"preparation_digest"`
	VerificationManifestDigest string `json:"verification_manifest_digest"`
}

// DeliveryKey names the delivery subject key space.
func DeliveryKey(id string) string { return "delivery:" + id }

// DeliveryID binds the initial verification to its immutable version inputs.
func DeliveryID(subject DeliverySubject) string {
	subject.DeliveryID = ""
	raw, _ := json.Marshal(subject)
	sum := sha256.Sum256(append([]byte("application-delivery\x00"), raw...))
	return hex.EncodeToString(sum[:16])
}

// DeliveryRetryID names a separately authorized attempt without changing the
// accepted version, preparation or manifest it verifies.
func DeliveryRetryID(subject DeliverySubject, command string) string {
	sum := sha256.Sum256([]byte("application-delivery-retry\x00" + DeliveryID(subject) + "\x00" + command))
	return hex.EncodeToString(sum[:16])
}

func (s DeliverySubject) Validate() error {
	if !ValidID(s.DeliveryID) {
		return errors.New("invalid delivery subject identity")
	}
	if !project.ValidDecisionID(s.IntentID) || !project.ValidDecisionID(s.AcceptedRevision) {
		return errors.New("invalid delivery subject intent revision")
	}
	if !ValidDigest(s.ScopeDigest) || !ValidDigest(s.PreparationDigest) ||
		!ValidDigest(s.VerificationManifestDigest) {
		return errors.New("delivery subject must bind its scope digests")
	}
	if !ValidCommit(s.SourceCommit) {
		return errors.New("invalid delivery subject source")
	}
	return nil
}

// DeliveryRepairSubject binds one verification repair to its verified
// delivery failure: the delivery, the exact failed verification run, the
// failure digest, the sealed inventory it may change, and its repair
// cycle. Key `delivery_repair:<delivery_id>:<failed_run>`, phase
// `repair`, Strategist profile/coder account, no publication or children.
type DeliveryRepairSubject struct {
	DeliveryID              string `json:"delivery_id"`
	FailedVerificationRunID string `json:"failed_verification_run_id"`
	FailureDigest           string `json:"failure_digest"`
	InventoryDigest         string `json:"inventory_digest"`
	CycleID                 string `json:"cycle_id"`
}

// DeliveryRepairKey names the delivery-repair subject key space.
func DeliveryRepairKey(delivery, failedRun string) string {
	return "delivery_repair:" + delivery + ":" + failedRun
}

func (s DeliveryRepairSubject) Validate() error {
	if !ValidID(s.DeliveryID) || !ValidID(s.CycleID) {
		return errors.New("invalid delivery repair subject identity")
	}
	if !ValidID(s.FailedVerificationRunID) {
		return errors.New("delivery repair subject lacks its failed verification run")
	}
	if !ValidDigest(s.FailureDigest) || !ValidDigest(s.InventoryDigest) {
		return errors.New("delivery repair subject must bind its failure evidence")
	}
	return nil
}

// DeliveryRepairCause is the exact authenticated failed verification retained
// before its one explicitly requested repair. It never names an incomplete
// report or derives a failure from a native process exit alone.
type DeliveryRepairCause struct {
	Repository              int64                 `json:"repository,string"`
	ProjectID               string                `json:"project_id"`
	FailedAssignmentID      string                `json:"failed_assignment_id"`
	FailedVerificationRunID string                `json:"failed_verification_run_id"`
	Delivery                DeliverySubject       `json:"delivery"`
	Report                  AppVerificationReport `json:"report"`
	FailureDigest           string                `json:"failure_digest"`
}

func DeliveryFailureDigest(report AppVerificationReport) string {
	raw, _ := json.Marshal(report)
	return MCPInputDigest(raw)
}

func DeliveryRepairCauseID(failedRun string) string {
	sum := sha256.Sum256([]byte("delivery-repair-cause-v1\x00" + failedRun))
	return hex.EncodeToString(sum[:16])
}

func DeliveryRepairCycleID(failedRun string) string {
	sum := sha256.Sum256([]byte("delivery-repair-cycle-v1\x00" + failedRun))
	return hex.EncodeToString(sum[:16])
}

func (c DeliveryRepairCause) Validate() error {
	if c.Repository <= 0 || !project.ValidID(c.ProjectID) || !ValidID(c.FailedAssignmentID) || !ValidID(c.FailedVerificationRunID) || c.Delivery.Validate() != nil || c.Report.Validate() != nil || c.Report.Outcome != AppVerificationFailed || c.Report.DeliveryID != c.Delivery.DeliveryID || c.FailureDigest != DeliveryFailureDigest(c.Report) {
		return errors.New("delivery repair requires its exact failed verification report")
	}
	return nil
}

type DeliveryRepairResult struct {
	RunID   string `json:"run_id"`
	CauseID string `json:"cause_id"`
	CycleID string `json:"cycle_id"`
	State   string `json:"state"`
}

type CurrentDeliveryRepairContext struct {
	Subject  DeliveryRepairSubject     `json:"subject"`
	Cause    DeliveryRepairCause       `json:"cause"`
	Local    CompletionLocal           `json:"local"`
	Issues   []CompletionIssueEvidence `json:"issues"`
	Complete bool                      `json:"complete"`
	State    string                    `json:"state"`
	Reason   string                    `json:"reason,omitempty"`
}

// CheckFailure names one exact failed check or outcome behind a repair
// cause or verification failure, with bounded detail. Causes reference
// exact failed checks and outcomes; they never carry a candidate.
type CheckFailure struct {
	CheckRef string `json:"check_ref"`
	Detail   string `json:"detail"`
}

func (f CheckFailure) Validate() error {
	if f.CheckRef == "" || len(f.CheckRef) > MaxPlanPathBytes || strings.ContainsRune(f.CheckRef, 0) {
		return errors.New("invalid failed check reference")
	}
	if !intentText(f.Detail, MaxPlanTextBytes) {
		return errors.New("invalid failed check detail")
	}
	return nil
}

// CheckReceiptPassed marks one trusted executor receipt as passed. A
// failed receipt reuses the established CheckFailed verdict: a required
// check reported a definitive non-pass state. No other receipt outcome
// is admitted.
const CheckReceiptPassed = "passed"

// VerificationCheckReceipt is one trusted executor receipt behind an app
// verification report: the admitted check, its outcome, the artifact
// digest where an artifact was produced, and bounded log evidence. Agent
// prose, screenshots alone or agent-supplied digests never substitute.
// A receipt is a claim until the R38 trusted-manifest join adopts it
// against host/store evidence.
type VerificationCheckReceipt struct {
	CheckID        string `json:"check_id"`
	Outcome        string `json:"outcome"`
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	LogExcerpt     string `json:"log_excerpt"`
}

func (r VerificationCheckReceipt) Validate() error {
	if r.CheckID == "" || len(r.CheckID) > MaxPlanPathBytes || strings.ContainsRune(r.CheckID, 0) {
		return errors.New("invalid verification check identity")
	}
	if r.Outcome != CheckReceiptPassed && r.Outcome != CheckFailed {
		return errors.New("invalid verification check outcome")
	}
	if r.ArtifactDigest != "" && !ValidDigest(r.ArtifactDigest) {
		return errors.New("invalid verification artifact digest")
	}
	if !intentText(r.LogExcerpt, MaxPlanTextBytes) {
		return errors.New("invalid verification log evidence")
	}
	return nil
}

// App verification outcomes: the exact delivery is verified, failed with
// recorded evidence, or incomplete and never finished.
const (
	AppVerificationVerified   = "verified"
	AppVerificationFailed     = "failed"
	AppVerificationIncomplete = "incomplete"
)

// ValidAppVerificationOutcome reports whether outcome is an admitted D16
// app verification outcome.
func ValidAppVerificationOutcome(outcome string) bool {
	switch outcome {
	case AppVerificationVerified, AppVerificationFailed, AppVerificationIncomplete:
		return true
	default:
		return false
	}
}

// AppVerificationReport is the typed output of a completed verify phase:
// the delivery it checked, the exact admitted input digest, and its
// outcome. It carries no candidate: verification never authorizes
// publication. Check/artifact receipts behind the outcome belong to the
// R38 trusted-manifest consumers; the envelope binds the result.
func validateVerificationEvidence(
	coverage []ReassessmentCoverage,
	receipts []VerificationCheckReceipt,
	artifacts []string,
	failures []CheckFailure,
	evidence []string,
) error {
	if len(coverage) > MaxReassessmentCoverage {
		return errors.New("verification coverage exceeds its bound")
	}
	covered := make(map[string]bool, len(coverage))
	for _, entry := range coverage {
		if err := entry.validate(); err != nil {
			return err
		}
		key := entry.IntentID + "\x00" + entry.OutcomeID
		if covered[key] {
			return errors.New("verification outcome covered twice")
		}
		covered[key] = true
	}
	if len(receipts) > MaxInspectedRefs {
		return errors.New("verification check receipts exceed their bound")
	}
	seen := make(map[string]bool, len(receipts))
	for _, receipt := range receipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
		if seen[receipt.CheckID] {
			return errors.New("verification check receipted twice")
		}
		seen[receipt.CheckID] = true
	}
	if len(artifacts) > MaxInspectedRefs {
		return errors.New("verification artifact refs exceed their bound")
	}
	for _, ref := range artifacts {
		if !ValidDigest(ref) {
			return errors.New("invalid verification artifact ref")
		}
	}
	if len(failures) > MaxInspectedRefs {
		return errors.New("verification failures exceed their bound")
	}
	for _, failure := range failures {
		if err := failure.Validate(); err != nil {
			return err
		}
	}
	if len(evidence) > MaxInspectedRefs {
		return errors.New("verification evidence outside its bound")
	}
	for _, ref := range evidence {
		if ref == "" || len(ref) > MaxPlanPathBytes || strings.ContainsRune(ref, 0) {
			return errors.New("invalid verification evidence ref")
		}
	}
	return nil
}

// AppVerificationReport is the typed output of a completed verify phase:
// the full D16 field set with delivery identity, admitted input digest,
// outcome, outcome coverage, trusted check receipts, artifact refs,
// failures and evidence refs. It carries no candidate: verification
// never authorizes publication. Receipt adoption belongs to the R38
// trusted-manifest consumers; the envelope binds the result.
type AppVerificationReport struct {
	DeliveryID    string                     `json:"delivery_id"`
	InputDigest   string                     `json:"input_digest"`
	Outcome       string                     `json:"outcome"`
	Coverage      []ReassessmentCoverage     `json:"coverage"`
	CheckReceipts []VerificationCheckReceipt `json:"check_receipts"`
	ArtifactRefs  []string                   `json:"artifact_refs"`
	Failures      []CheckFailure             `json:"failures"`
	EvidenceRefs  []string                   `json:"evidence_refs"`
}

func (r AppVerificationReport) Validate() error {
	if !ValidID(r.DeliveryID) {
		return errors.New("invalid app verification delivery")
	}
	if !ValidDigest(r.InputDigest) {
		return errors.New("app verification lacks its admitted input digest")
	}
	if !ValidAppVerificationOutcome(r.Outcome) {
		return errors.New("invalid app verification outcome")
	}
	// A verified delivery claims every required check with a receipt and
	// records no failure; a failed delivery claims at least one failure
	// with its receipts. Receipts are agent-supplied claims until the
	// R38 trusted-manifest join adopts them; they never qualify a
	// delivery on their own. Failure and incomplete never become finished.
	switch r.Outcome {
	case AppVerificationVerified:
		if len(r.CheckReceipts) == 0 || len(r.Failures) != 0 {
			return errors.New("verified delivery lacks its passing receipts")
		}
		for _, receipt := range r.CheckReceipts {
			if receipt.Outcome != CheckReceiptPassed {
				return errors.New("verified delivery carries a failing receipt")
			}
		}
	case AppVerificationFailed:
		if len(r.CheckReceipts) == 0 || len(r.Failures) == 0 {
			return errors.New("failed delivery lacks its failure evidence")
		}
	}
	return validateVerificationEvidence(
		r.Coverage, r.CheckReceipts, r.ArtifactRefs, r.Failures, r.EvidenceRefs,
	)
}

// DeliveryRepairReport is the typed output of a completed repair phase
// answering a delivery failure: the repair subject, the exact admitted
// input digest, and its outcome under the shared D15 repair rubric. It
// carries no candidate. Cause/coverage/adoption detail belongs to the
// R37/R40 repair consumers; the envelope binds the result.
// DeliveryRepairReport is the typed output of a completed repair phase
// answering a delivery failure: the full D16 field set with the exact
// bound subject, admitted input digest, inspected sources, causes,
// retained issues, intent changes, coverage, attention and outcome under
// the shared D15 repair rubric. It carries no candidate. Cause and
// coverage adoption detail belongs to the R37/R40 repair consumers; the
// envelope binds the result.
type DeliveryRepairReport struct {
	Subject              DeliveryRepairSubject        `json:"subject"`
	InputDigest          string                       `json:"input_digest"`
	Inspected            []InspectedSource            `json:"inspected_refs"`
	Causes               []CheckFailure               `json:"causes"`
	RetainedIssueIndexes []int64                      `json:"retained_issue_indexes"`
	IntentChanges        []ReassessmentIntentChange   `json:"intent_changes"`
	Coverage             []ReassessmentCoverage       `json:"coverage"`
	Attention            []ReassessmentAttentionEntry `json:"attention"`
	Outcome              string                       `json:"outcome"`
}

func (r DeliveryRepairReport) Validate() error {
	if err := r.Subject.Validate(); err != nil {
		return err
	}
	if !ValidDigest(r.InputDigest) {
		return errors.New("delivery repair report must bind its admitted input")
	}
	if !ValidRepairOutcome(r.Outcome) {
		return errors.New("invalid delivery repair outcome")
	}
	return validateRepairEvidence(
		r.Inspected, r.Causes, r.RetainedIssueIndexes,
		r.IntentChanges, r.Coverage, r.Attention,
	)
}
