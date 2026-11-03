package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s Acceptances) applicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrAcceptanceAccess):
		s.authorityError(w, err)
	case errors.Is(err, factoryservice.ErrAcceptanceSource):
		writeError(w, 400, "invalid_source", "Selected sources must name an exact native comment revision.")
	case errors.Is(err, factoryservice.ErrAcceptancePrerequisite):
		writeError(w, 400, "invalid_prerequisite", "Prerequisites must name their endpoint repository and issue.")
	case errors.Is(err, factoryservice.ErrAcceptanceInvalid):
		writeError(w, 400, "invalid_acceptance", "Acceptance must name its decision and exact selected revisions.")
	default:
		AcceptanceDecisionError(w, err)
	}
}

// AcceptanceDecisionError maps acceptance failures to status codes.
// Refused or changed inputs conflict with 409; unavailable evidence or
// service reports 503, never a validity verdict.
func AcceptanceDecisionError(w http.ResponseWriter, err error) {
	var refusal *control.AcceptanceRefusal
	switch {
	case errors.As(err, &refusal):
		acceptanceRefusalError(w, refusal)
	default:
		CommandError(w, err)
	}
}

func acceptanceRefusalError(w http.ResponseWriter, refusal *control.AcceptanceRefusal) {
	body := map[string]any{"error": refusal.Reason, "message": acceptanceRefusalMessage(refusal.Reason)}
	switch refusal.Reason {
	case control.RefusalIncompleteEvidence, control.RefusalNativeBusy, control.RefusalSnapshotUnavailable:
		writeJSON(w, 503, body)
	default:
		writeJSON(w, 409, body)
	}
}

func acceptanceRefusalMessage(reason string) string {
	switch reason {
	case control.RefusalStaleEvidence:
		return "Native state changed during verification; refresh and retry."
	case control.RefusalIncompleteEvidence:
		return "Native evidence is incomplete; nothing was recorded."
	case control.RefusalNativeBusy:
		return "Native writers are busy; retry after they settle."
	case control.RefusalSnapshotUnavailable:
		return "Native evidence is unavailable; nothing was recorded."
	case control.RefusalIssueHidden:
		return "The bound actor cannot see the target issue."
	case control.RefusalObjectiveChanged:
		return "The issue title or body changed; accept the new revision."
	case control.RefusalSourceMissing:
		return "A selected comment no longer exists; accept the current set."
	case control.RefusalSourceHidden:
		return "A selected comment is no longer visible."
	case control.RefusalSourceChanged:
		return "A selected comment changed; accept the new revision."
	case control.RefusalEdgeHidden:
		return "A prerequisite edge is no longer visible."
	case control.RefusalEdgeChanged:
		return "The prerequisite edge set changed; adopt the revised set."
	case control.RefusalPrereqUnknown:
		return "The named prerequisite acceptance does not exist."
	case control.RefusalPrereqStale:
		return "The prerequisite acceptance moved; adopt the current head."
	case control.RefusalCreationUnverified:
		return "Creation provenance is unverified; adopt explicitly."
	case control.RefusalCreationEdited:
		return "The issue changed after creation; adopt explicitly."
	case control.RefusalCreationBlocked:
		return "The issue has prerequisites; adopt explicitly."
	case control.RefusalCreationFactory:
		return "Factory-created issues need explicit human adoption."
	default:
		return "Creation needs standing policy and a code-write creator."
	}
}

func acceptancePreviewReadError(w http.ResponseWriter, err error) {
	var refusal *control.AcceptanceRefusal
	if errors.As(err, &refusal) {
		acceptanceRefusalError(w, refusal)
		return
	}
	writeError(w, 503, "native_evidence_unavailable", "Current native acceptance evidence is unavailable.")
}

func parseAcceptancePreviewCommentIDs(raw string) ([]string, bool) {
	if len(raw) > 8192 {
		return nil, false
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, false
	}
	for key := range values {
		if key != "comment_id" {
			return nil, false
		}
	}
	ids := values["comment_id"]
	if len(ids) > factory.MaxAcceptedSources {
		return nil, false
	}
	seen := make(map[string]struct{}, len(ids))
	for i, value := range ids {
		id, ok := positiveID(value)
		if !ok {
			return nil, false
		}
		canonical := strconv.FormatInt(id, 10)
		if _, exists := seen[canonical]; exists {
			return nil, false
		}
		seen[canonical] = struct{}{}
		ids[i] = canonical
	}
	return ids, true
}
