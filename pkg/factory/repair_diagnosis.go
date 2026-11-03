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
	MaxRepairDiagnosisBytes = 8192
	MaxRepairDiagnoses      = MaxRepairObservations
)

// RepairDiagnosis is an attributed worker report of cause evidence and
// uncertainty. Its post ID names the observation, never an
// accepted repair, native fact, judgment or changed graph.
type RepairDiagnosis struct {
	Subject         string         `json:"subject"`
	InputDigest     string         `json:"input_digest"`
	SourceCommit    string         `json:"source_commit"`
	InventoryDigest string         `json:"inventory_digest"`
	Title           string         `json:"title"`
	Summary         string         `json:"summary"`
	State           string         `json:"state"`
	Causes          []CheckFailure `json:"causes"`
	EvidenceRefs    []string       `json:"evidence_refs"`
	Alternatives    []string       `json:"alternatives"`
	Uncertainty     []string       `json:"uncertainty"`
	ArtifactIDs     []string       `json:"artifact_ids,omitempty"`
}

func DecodeRepairDiagnosis(raw []byte) (RepairDiagnosis, error) {
	var out RepairDiagnosis
	if len(raw) == 0 || len(raw) > MaxRepairDiagnosisBytes {
		return out, errors.New("repair diagnosis must fit within 8 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return out, errors.New("repair diagnosis has trailing content")
	}
	return out, out.Validate()
}

func (d RepairDiagnosis) Validate() error {
	text := func(s string, limit int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
	}
	if !text(d.Subject, MCPMaxRefChars) || !ValidDigest(d.InputDigest) || !ValidCommit(d.SourceCommit) || !ValidDigest(d.InventoryDigest) || !text(d.Title, 160) || !text(d.Summary, 1024) {
		return errors.New("repair diagnosis lacks its bound subject and summary")
	}
	switch d.State {
	case "suspected", "supported", "contradicted", "incomplete":
	default:
		return errors.New("diagnosis state describes reported causal evidence, never adoption")
	}
	if len(d.Causes) < 1 || len(d.Causes) > 8 || len(d.EvidenceRefs) > 16 || len(d.Alternatives) > 8 || len(d.Uncertainty) > 8 || len(d.ArtifactIDs) > 8 {
		return errors.New("repair diagnosis exceeds its content bounds")
	}
	seen := map[string]bool{}
	for _, cause := range d.Causes {
		if !text(cause.CheckRef, 256) || !text(cause.Detail, 1024) || seen[cause.CheckRef] {
			return errors.New("repair diagnosis cause is invalid or repeated")
		}
		seen[cause.CheckRef] = true
	}
	for _, refs := range [][]string{d.EvidenceRefs, d.Alternatives, d.Uncertainty} {
		for _, ref := range refs {
			if !text(ref, 256) {
				return errors.New("repair diagnosis evidence, alternative or uncertainty exceeds its bound")
			}
		}
	}
	seen = map[string]bool{}
	for _, id := range d.ArtifactIDs {
		if !ValidID(id) || seen[id] {
			return errors.New("repair diagnosis artifact is invalid or repeated")
		}
		seen[id] = true
	}
	return nil
}

func (d RepairDiagnosis) ValidateFor(cap MCPCapability, a Assignment, current CurrentContext) error {
	if cap.Profile != MCPProfileStrategist || a.Role != project.RoleCoder || a.Issue != 0 || a.ID != cap.AssignmentID || a.Run != cap.RunID || a.Repository != cap.Repository || a.ProjectID != cap.Project || a.Subject.SubjectKey(a.Repository) != cap.Subject || d.Subject != cap.Subject || d.InputDigest != cap.ScopeDigest() || d.SourceCommit != a.SourceCommit {
		return errors.New("diagnosis differs from its admitted Strategist")
	}
	var inventory, source string
	switch {
	case a.Subject.Completion != nil && current.Role.Strategy != nil && current.Role.Strategy.Complete:
		inventory, source = current.Role.Strategy.InventoryDigest, current.Role.Strategy.Local.SourceCommit
	case a.Subject.Stall != nil && current.Role.Repair != nil && current.Role.Repair.Complete:
		inventory, source = current.Role.Repair.InventoryDigest, current.Role.Repair.Local.SourceCommit
	case a.Subject.DeliveryRepair != nil && current.Role.DeliveryRepair != nil && current.Role.DeliveryRepair.Complete:
		inventory, source = current.Role.DeliveryRepair.Subject.InventoryDigest, current.Role.DeliveryRepair.Local.SourceCommit
	default:
		return errors.New("diagnosis lacks its complete admitted cause")
	}
	if d.InventoryDigest != inventory || d.SourceCommit != source {
		return errors.New("diagnosis differs from its sealed inventory or source")
	}
	return nil
}
