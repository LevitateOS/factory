package control

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/levitateos/sodaos/internal/factory"
)

// AcceptedResolveOperationID derives the deterministic identity for one
// accepted finding's intended thread resolution: publication, cycle and
// finding key. New cycles are distinct; replay of a cycle is identical, so
// FW22 persists the intent before the only native write and reconciles by
// lookup instead of resubmitting.
func AcceptedResolveOperationID(publicationID, cycleID, findingKey string) string {
	sum := sha256.Sum256([]byte("soda-factory-accepted-resolve-v1\x00" + publicationID + "\x00" + cycleID + "\x00" + findingKey))
	return hex.EncodeToString(sum[:])[:32]
}

// AcceptedResolution is one accepted fix's intended native resolution: the
// finding key it answers, the native root comment carrying that finding, and
// the conditional resolve request to submit. A zero root comment means the
// finding has no native thread (non-line findings live in the review summary
// only): nothing resolves, and reconciliation treats it as vacuous.
type AcceptedResolution struct {
	FindingKey     string
	Request        factory.ResolveRequest
	OperationID    string
	NoNativeThread bool
}

// PlanAcceptedResolutions derives the intended thread resolutions for one
// admitted correction BEFORE any native write. Every fixed finding with a
// native comment mapping produces exactly one resolve request, validated
// against its recorded thread evidence; fixed findings without a native
// thread resolve vacuously. Unfixed findings never produce a request. Each
// request is checked by the same evidence gate the native operation enforces
// (binding, independent actor, recorded root, unchanged conversation), so a
// planned request that later fails readback refuses instead of resolving a
// changed conversation.
func PlanAcceptedResolutions(publication factory.Publication, admission CorrectionAdmission, mappings []factory.ReviewCommentMapping, evidenceByRoot map[int64]factory.ThreadEvidence, binding factory.ActorBindingRef, prAuthorID int64, headRef, baseRef, headOID, baseOID string, repository, prNumber, prID int64) ([]AcceptedResolution, error) {
	if admission.CycleID == "" || admission.SetDigest == "" || !factory.ValidID(admission.RunID) {
		return nil, errors.New("accepted resolutions need their admitted correction")
	}
	if !factory.ValidCommit(headOID) || !factory.ValidCommit(baseOID) || headOID == baseOID {
		return nil, errors.New("accepted resolutions need their exact head over a verified base")
	}
	if admission.NewCandidate != headOID {
		return nil, errors.New("accepted resolutions answer a superseded head")
	}
	commentByKey := make(map[string]int64, len(mappings))
	for _, mapping := range mappings {
		if err := mapping.Validate(); err != nil {
			return nil, err
		}
		if _, dup := commentByKey[mapping.Key]; dup {
			return nil, errors.New("native comment mapping repeats its finding key")
		}
		commentByKey[mapping.Key] = mapping.CommentID
	}
	planned := make([]AcceptedResolution, 0, len(admission.Fixed))
	for _, key := range admission.Fixed {
		root, ok := commentByKey[key]
		if !ok {
			planned = append(planned, AcceptedResolution{
				FindingKey: key, NoNativeThread: true,
				OperationID: AcceptedResolveOperationID(publication.ID, admission.CycleID, key),
			})
			continue
		}
		evidence, ok := evidenceByRoot[root]
		if !ok {
			return nil, errors.New("accepted fix lacks its recorded thread evidence")
		}
		request := factory.ResolveRequest{
			HeadRef: headRef, BaseRef: baseRef, HeadOID: headOID, BaseOID: baseOID,
			Digest: evidence.Digest, Repository: repository, PRNumber: prNumber, PRID: prID,
			RootCommentID: root, ActorID: binding.ActorID, Resolved: true,
		}
		if err := request.ValidateForEvidence(binding, prAuthorID, evidence); err != nil {
			return nil, err
		}
		planned = append(planned, AcceptedResolution{
			FindingKey: key, Request: request,
			OperationID: AcceptedResolveOperationID(publication.ID, admission.CycleID, key),
		})
	}
	return planned, nil
}

// JudgmentAcceptance is FW19's opaque proof that funded judgment accepted the
// admitted correction set. FW21 owns the decision shape; FW19 only binds it:
// the decision must exist, and its finding-set digest must still match the
// admitted set, or stale semantic evidence invalidates the reconciliation.
type JudgmentAcceptance struct {
	DecisionID    string
	FindingDigest string
}

// Validate checks the acceptance binding. An empty decision never authorizes
// reconciliation: confirmed native resolved state alone is never semantic
// proof, and readback never re-admits to judgment.
func (a JudgmentAcceptance) Validate(setDigest string) error {
	if strings.TrimSpace(a.DecisionID) == "" || len(a.DecisionID) > 128 {
		return errors.New("native resolution needs its judgment acceptance")
	}
	if !factory.ValidDigest(a.FindingDigest) || a.FindingDigest != setDigest {
		return errors.New("judgment acceptance answers a changed finding set")
	}
	return nil
}

// ResolutionReconciliation is the outcome of reconciling accepted fixes
// against their exact native readback: the finding keys confirmed resolved
// and the keys still waiting. Pending resolution never reports merge-ready:
// callers gate the merge on an empty wait list plus their own authority
// checks (FW22).
type ResolutionReconciliation struct {
	Resolved []string `json:"resolved,omitempty"`
	Waits    []string `json:"waits,omitempty"`
}

// ReconcileAcceptedResolutions reconciles one accepted correction's intended
// resolutions against their exact readback confirmations (one per finding
// key with a native thread, true only when the N adapter confirmed resolved
// with an unchanged conversation digest). Findings without a native thread
// resolve vacuously. Every fixed finding is accounted for exactly once:
// confirmed keys resolve, unconfirmed keys wait. Without a bound judgment
// acceptance nothing resolves, however complete the native state looks.
func ReconcileAcceptedResolutions(acceptance JudgmentAcceptance, admission CorrectionAdmission, planned []AcceptedResolution, confirmed map[string]bool) (ResolutionReconciliation, error) {
	var empty ResolutionReconciliation
	if err := acceptance.Validate(admission.SetDigest); err != nil {
		return empty, err
	}
	return reconcilePlannedResolutions(admission, planned, confirmed)
}

// reconcilePlannedResolutions shares the exact readback accounting behind
// both the opaque judgment binding and the union: every fixed finding is
// accounted for exactly once, confirmed keys resolve and unconfirmed keys
// wait. Pure bookkeeping on either path; it spends no funded call.
func reconcilePlannedResolutions(admission CorrectionAdmission, planned []AcceptedResolution, confirmed map[string]bool) (ResolutionReconciliation, error) {
	var empty ResolutionReconciliation
	if len(planned) != len(admission.Fixed) {
		return empty, errors.New("accepted resolutions omit an accepted fix")
	}
	seen := make(map[string]bool, len(planned))
	outcome := ResolutionReconciliation{}
	for _, item := range planned {
		if !item.NoNativeThread && item.Request.RootCommentID <= 0 {
			return empty, errors.New("accepted resolution lacks its native root")
		}
		fixed := false
		for _, key := range admission.Fixed {
			if key == item.FindingKey {
				fixed = true
				break
			}
		}
		if !fixed {
			return empty, errors.New("accepted resolution answers an unaccepted finding")
		}
		if seen[item.FindingKey] {
			return empty, errors.New("accepted resolution repeats its finding key")
		}
		seen[item.FindingKey] = true
		if item.NoNativeThread || confirmed[item.FindingKey] {
			outcome.Resolved = append(outcome.Resolved, item.FindingKey)
			continue
		}
		outcome.Waits = append(outcome.Waits, item.FindingKey)
	}
	for _, key := range admission.Fixed {
		if !seen[key] {
			return empty, errors.New("accepted resolutions omit an accepted fix")
		}
	}
	return outcome, nil
}
