package factory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// RepairDraft preserves the worker's complete typed proposal. It is neither a
// terminal result nor an approved graph. Its post ID is the draft ID.
type RepairDraft struct {
	Report        json.RawMessage `json:"report"`
	DiagnosisRefs []string        `json:"diagnosis_refs,omitempty"`
}

type RepairDraftContent struct {
	Changes         []ReassessmentIntentChange
	Outcome         string
	SourceCommit    string
	InventoryDigest string
}

type repairDraftReport struct {
	completion *ReassessmentReport
	stall      *StallRepairReport
	delivery   *DeliveryRepairReport
}

func decodeRepairDraftValue(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("repair draft has trailing content")
	}
	return nil
}

func (d RepairDraft) typedReport() (repairDraftReport, error) {
	var out repairDraftReport
	var fields map[string]json.RawMessage
	if json.Unmarshal(d.Report, &fields) != nil || fields == nil {
		return out, errors.New("repair draft needs its complete typed report")
	}
	switch {
	case fields["subject"] != nil:
		r := &DeliveryRepairReport{}
		if err := decodeRepairDraftValue(d.Report, r); err != nil {
			return out, err
		}
		out.delivery = r
		return out, r.Validate()
	case fields["stall_id"] != nil:
		r := &StallRepairReport{}
		if err := decodeRepairDraftValue(d.Report, r); err != nil {
			return out, err
		}
		out.stall = r
		return out, r.Validate()
	default:
		r := &ReassessmentReport{}
		if err := decodeRepairDraftValue(d.Report, r); err != nil {
			return out, err
		}
		out.completion = r
		return out, r.Validate()
	}
}

func DecodeRepairDraft(raw []byte) (RepairDraft, error) {
	var out RepairDraft
	if len(raw) == 0 || len(raw) > MCPMaxSubmissionPayload {
		return out, errors.New("repair draft must fit within 64 KiB")
	}
	if err := decodeRepairDraftValue(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (d RepairDraft) DiagnosisIDs() ([]string, error) {
	if len(d.DiagnosisRefs) > 16 {
		return nil, errors.New("repair draft has too many diagnosis references")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, ref := range d.DiagnosisRefs {
		id, ok := strings.CutPrefix(ref, "diagnosis:")
		if !ok || !ValidID(id) || seen[id] {
			return nil, errors.New("repair draft diagnosis reference is invalid or repeated")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// ValidateFor binds a draft to the currently admitted cause. Structural graph
// suitability remains a preview/paid-adoption decision, never draft authority.
func (d RepairDraft) ValidateFor(cap MCPCapability, a Assignment, current CurrentContext) (RepairDraftContent, error) {
	var out RepairDraftContent
	r, err := d.typedReport()
	if err != nil {
		return out, err
	}
	if cap.Profile != MCPProfileStrategist || a.ID != cap.AssignmentID || a.Run != cap.RunID || a.Repository != cap.Repository || a.ProjectID != cap.Project || a.Subject.SubjectKey(a.Repository) != cap.Subject || a.SourceCommit != cap.SourceDigest {
		return out, errors.New("draft differs from its admitted Strategist")
	}
	var inspected []InspectedSource
	switch {
	case a.Subject.Completion != nil && current.Role.Strategy != nil && current.Role.Strategy.Complete && r.completion != nil:
		bound, report := current.Role.Strategy, r.completion
		digest, err := bound.Local.Digest()
		if err != nil || report.CompletionID != bound.CompletionID || report.CycleID != bound.CycleID || report.InventoryDigest != bound.InventoryDigest || report.SourceCommit != bound.Local.SourceCommit || report.PreparationDigest != bound.Local.PreparationDigest || report.LocalInputDigest != digest {
			return out, errors.New("draft differs from its admitted completion")
		}
		out = RepairDraftContent{Changes: report.IntentChanges, Outcome: "proposal", SourceCommit: report.SourceCommit, InventoryDigest: report.InventoryDigest}
		inspected = report.Inspected
	case a.Subject.Stall != nil && current.Role.Repair != nil && current.Role.Repair.Complete && r.stall != nil:
		bound, report := current.Role.Repair, r.stall
		if report.StallID != bound.StallID || report.CycleID != bound.CycleID || report.InventoryDigest != bound.InventoryDigest || report.SourceCommit != bound.Local.SourceCommit || report.PreparationDigest != bound.Local.PreparationDigest {
			return out, errors.New("draft differs from its admitted stall")
		}
		out = RepairDraftContent{Changes: report.IntentChanges, Outcome: report.Outcome, SourceCommit: report.SourceCommit, InventoryDigest: report.InventoryDigest}
		inspected = report.Inspected
	case a.Subject.DeliveryRepair != nil && current.Role.DeliveryRepair != nil && current.Role.DeliveryRepair.Complete && r.delivery != nil:
		bound, report := current.Role.DeliveryRepair, r.delivery
		if report.Subject != bound.Subject || report.Subject != *a.Subject.DeliveryRepair || report.InputDigest != cap.ScopeDigest() {
			return out, errors.New("draft differs from its admitted failed delivery")
		}
		out = RepairDraftContent{Changes: report.IntentChanges, Outcome: report.Outcome, SourceCommit: bound.Local.SourceCommit, InventoryDigest: report.Subject.InventoryDigest}
		inspected = report.Inspected
	default:
		return out, errors.New("draft lacks its complete admitted cause")
	}
	if out.SourceCommit != a.SourceCommit {
		return out, errors.New("draft source differs from its investigator")
	}
	for _, ref := range inspected {
		if ref.Commit != out.SourceCommit {
			return out, errors.New("draft inspected another source")
		}
	}
	return out, nil
}
