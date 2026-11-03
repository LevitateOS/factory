package factory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/project"
)

const (
	MaxVerificationJourneyBytes = 8192
	MaxVerificationJourneys     = 32
	VerificationJourneyPageSize = 4
)

// VerificationJourneyResult is an attributed worker observation, never a
// trusted check receipt or a claim that Factory verified the application.
// Its post ID is the canonical journey identity.
type VerificationJourneyResult struct {
	DeliveryID     string   `json:"delivery_id"`
	InputDigest    string   `json:"input_digest"`
	SourceCommit   string   `json:"source_commit"`
	Preparation    string   `json:"preparation"`
	ManifestDigest string   `json:"manifest_digest"`
	OutcomeIDs     []string `json:"outcome_ids"`
	Name           string   `json:"name"`
	Steps          []string `json:"steps"`
	Expected       string   `json:"expected"`
	Observed       string   `json:"observed"`
	Result         string   `json:"result"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
	ArtifactIDs    []string `json:"artifact_ids,omitempty"`
	Limitations    []string `json:"limitations,omitempty"`
}

type VerificationJourneyRecord struct {
	ID          string                    `json:"id"`
	Sequence    int64                     `json:"sequence"`
	Observation VerificationJourneyResult `json:"observation"`
}

type VerificationScopeView struct {
	Assignment  string                      `json:"assignment"`
	Run         string                      `json:"run"`
	InputDigest string                      `json:"input_digest"`
	Delivery    DeliverySubject             `json:"delivery"`
	OutcomeIDs  []string                    `json:"outcome_ids"`
	Records     []VerificationJourneyRecord `json:"records"`
	Total       int                         `json:"total"`
	Limit       int                         `json:"limit"`
	Maximum     int                         `json:"maximum"`
	NextAfter   int64                       `json:"next_after,omitempty"`
	Complete    bool                        `json:"complete"`
}

func DecodeVerificationJourney(raw []byte) (VerificationJourneyResult, error) {
	var out VerificationJourneyResult
	if len(raw) == 0 || len(raw) > MaxVerificationJourneyBytes {
		return out, errors.New("journey observation must fit within 8 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return out, errors.New("journey observation has trailing content")
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func (o VerificationJourneyResult) Validate() error {
	if !ValidID(o.DeliveryID) || !ValidDigest(o.InputDigest) || !ValidCommit(o.SourceCommit) || !project.ValidPreparationID(o.Preparation) || !ValidDigest(o.ManifestDigest) {
		return errors.New("journey observation lacks its admitted delivery tuple")
	}
	text := func(s string, max int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
	}
	if !text(o.Name, 160) || !text(o.Expected, 1024) || !text(o.Observed, 1024) || len(o.Steps) < 1 || len(o.Steps) > 8 {
		return errors.New("journey requires bounded steps and expected/observed behavior")
	}
	for _, step := range o.Steps {
		if !text(step, 256) {
			return errors.New("journey step is outside its bound")
		}
	}
	switch o.Result {
	case "passed", "failed", "incomplete":
	default:
		return errors.New("journey result is only an observation, never verified")
	}
	if len(o.OutcomeIDs) < 1 || len(o.OutcomeIDs) > 64 || len(o.EvidenceRefs) > 16 || len(o.ArtifactIDs) > 8 || len(o.Limitations) > 8 {
		return errors.New("journey references exceed their bounds")
	}
	seen := map[string]bool{}
	for _, id := range o.OutcomeIDs {
		if !project.ValidDecisionID(id) || seen[id] {
			return errors.New("journey must name distinct accepted outcomes")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, id := range o.ArtifactIDs {
		if !ValidID(id) || seen[id] {
			return errors.New("journey artifact identity is invalid or repeated")
		}
		seen[id] = true
	}
	for _, ref := range append(append([]string(nil), o.EvidenceRefs...), o.Limitations...) {
		if !text(ref, 256) {
			return errors.New("journey evidence or limitation is outside its bound")
		}
	}
	return nil
}

// ValidateFor checks the admitted scope of a decoded or stored observation.
func (o VerificationJourneyResult) ValidateFor(cap MCPCapability, a Assignment, outcomes []string) error {
	if cap.Profile != MCPProfileTestPilot || a.Subject.Delivery == nil || a.Phase != project.ExecutionVerify || a.Role != project.RoleReviewer || a.Issue != 0 || a.ID != cap.AssignmentID || a.Run != cap.RunID || a.ProjectID != cap.Project || a.Repository != cap.Repository || a.Subject.SubjectKey(a.Repository) != cap.Subject || o.InputDigest != cap.ScopeDigest() {
		return errors.New("journey observation differs from this worker")
	}
	d := a.Subject.Delivery
	if o.DeliveryID != d.DeliveryID || o.SourceCommit != a.SourceCommit || o.SourceCommit != d.SourceCommit || o.Preparation != a.Preparation || o.ManifestDigest != d.VerificationManifestDigest {
		return errors.New("journey observation differs from the admitted delivery")
	}
	allowed := map[string]bool{}
	for _, id := range outcomes {
		allowed[id] = true
	}
	for _, id := range o.OutcomeIDs {
		if !allowed[id] {
			return errors.New("journey observation names a foreign accepted outcome")
		}
	}
	return nil
}

// VerificationJourneyReferences reserves journey:<post ID> for exact
// recorded observations. Other evidence references keep their existing meaning.
func VerificationJourneyReferences(refs []string) ([]string, error) {
	var ids []string
	seen := map[string]bool{}
	for _, ref := range refs {
		id, journey := strings.CutPrefix(ref, "journey:")
		if !journey {
			continue
		}
		if !ValidID(id) || seen[id] {
			return nil, errors.New("journey reference is invalid or repeated")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}
