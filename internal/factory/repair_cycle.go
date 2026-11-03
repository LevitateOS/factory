package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
)

const (
	RepairCollecting    = "collecting"
	RepairReady         = "ready"
	RepairInvestigating = "investigating"
	RepairAssessing     = "assessing"
	RepairApplying      = "applying"
	RepairAdopted       = "adopted"
	RepairAttention     = "attention"
	RepairStale         = "stale"
)

func RepairCycleID(episode string, cycle int) string {
	sum := sha256.Sum256([]byte("repair-cycle-v1\x00" + episode + "\x00" + strconv.Itoa(cycle)))
	return hex.EncodeToString(sum[:16])
}

func RepairChargeID(episode, kind string, cycle int) string {
	sum := sha256.Sum256([]byte("repair-charge-v1\x00" + episode + "\x00" + kind + "\x00" + strconv.Itoa(cycle)))
	return hex.EncodeToString(sum[:16])
}

// StallRepairCycle retains the complete verified input and exactly one native
// worker binding beside its immutable stall observation. Collection progress
// stays private; CurrentRepairContext exposes only sealed model-safe inputs.
type StallRepairCycle struct {
	ID                  string                    `json:"id"`
	Number              int                       `json:"number"`
	State               string                    `json:"state"`
	InventoryDigest     string                    `json:"inventory_digest,omitempty"`
	NativeRevision      int64                     `json:"native_revision,omitempty"`
	Local               CompletionLocal           `json:"local"`
	Issues              []CompletionIssueEvidence `json:"issues"`
	CollectorProgress   json.RawMessage           `json:"collector_progress,omitempty"`
	AssignmentID        string                    `json:"assignment_id,omitempty"`
	RunID               string                    `json:"run_id,omitempty"`
	Reason              string                    `json:"reason,omitempty"`
	JudgmentFingerprint string                    `json:"judgment_fingerprint,omitempty"`
	ProposalOption      string                    `json:"proposal_option,omitempty"`
	ProposalProbability float64                   `json:"proposal_probability,omitempty"`
	ProposalConfidence  float64                   `json:"proposal_confidence,omitempty"`
	AdoptionProgress    json.RawMessage           `json:"adoption_progress,omitempty"`
}

type StallRepairEpisode struct {
	Revision int64              `json:"revision"`
	Cycles   []StallRepairCycle `json:"cycles"`
}

func (c StallRepairCycle) ValidateFor(event StallEvent) error {
	if event.Validate() != nil || c.Number < 1 || c.Number > DefaultRepairCyclesPerEpisode || c.ID != RepairCycleID(event.EpisodeID, c.Number) || len(c.Reason) > 256 {
		return errors.New("invalid repair cycle binding")
	}
	switch c.State {
	case RepairCollecting, RepairAttention, RepairStale:
	case RepairReady, RepairInvestigating, RepairAssessing, RepairApplying, RepairAdopted:
		if !ValidDigest(c.InventoryDigest) || c.NativeRevision < 1 || c.Local.Validate() != nil || c.Local.Material.Repository != event.Repository || c.Issues == nil {
			return errors.New("repair cycle lacks its complete sealed input")
		}
		var prior int64
		for _, body := range c.Issues {
			if body.Validate() != nil || body.Index <= prior {
				return errors.New("repair sealed bodies are invalid or unordered")
			}
			prior = body.Index
		}
	default:
		return errors.New("unknown repair cycle state")
	}
	if c.JudgmentFingerprint != "" && !ValidDigest(c.JudgmentFingerprint) {
		return errors.New("invalid repair judgment fingerprint")
	}
	if c.State == RepairApplying || c.State == RepairAdopted {
		if !ValidReassessmentProposal(c.ProposalOption, c.ProposalProbability, c.ProposalConfidence) || !json.Valid(c.AdoptionProgress) {
			return errors.New("repair adoption lacks its qualified proposal and journal")
		}
	}
	if c.AssignmentID != "" || c.RunID != "" {
		if c.AssignmentID != c.ID || c.RunID != RepairChargeID(event.EpisodeID, RepairChargeRun, c.Number) {
			return errors.New("repair cycle changed its sole worker identity")
		}
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > MaxReassessmentEvidenceBytes+MaxCompletionMaterialBytes+65536 {
		return errors.New("repair cycle exceeds retained input bound")
	}
	return nil
}

func RepairTransitionAllowed(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case RepairCollecting:
		return to == RepairReady || to == RepairAttention || to == RepairStale
	case RepairReady:
		return to == RepairInvestigating || to == RepairAttention || to == RepairStale
	case RepairInvestigating:
		return to == RepairAssessing || to == RepairAttention || to == RepairStale
	case RepairAssessing:
		return to == RepairApplying || to == RepairAttention || to == RepairStale
	case RepairApplying:
		return to == RepairAdopted
	case RepairAttention, RepairAdopted:
		return to == RepairStale
	}
	return false
}

type CurrentRepairContext struct {
	StallID         string                    `json:"stall_id"`
	EpisodeID       string                    `json:"episode_id"`
	CycleID         string                    `json:"cycle_id"`
	Cycle           int                       `json:"cycle"`
	Cause           string                    `json:"cause"`
	InventoryDigest string                    `json:"inventory_digest"`
	NativeRevision  int64                     `json:"native_revision"`
	Local           CompletionLocal           `json:"local"`
	Issues          []CompletionIssueEvidence `json:"issues"`
	ChargeID        string                    `json:"charge_id"`
	Complete        bool                      `json:"complete"`
	State           string                    `json:"state"`
	Reason          string                    `json:"reason,omitempty"`
	PriorCycles     []CurrentRepairCycle      `json:"prior_cycles"`
	Budget          *CurrentRepairBudget      `json:"budget,omitempty"`
}

type CurrentRepairCycle struct {
	ID              string `json:"id"`
	Number          int    `json:"number"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	InventoryDigest string `json:"inventory_digest"`
}

type CurrentRepairBudget struct {
	CyclesRemaining        int `json:"cycles_remaining"`
	RunsRemaining          int `json:"runs_remaining"`
	JudgmentsRemaining     int `json:"judgments_remaining"`
	ActiveMinutesRemaining int `json:"active_minutes_remaining"`
}

// RepairFactsDigest excludes generated graph identity, dependency rewrites and
// attempt bookkeeping. A later automatic run needs a changed approved source or
// preparation, changed native manual evidence, or a newly verified merge.
func (c StallRepairCycle) RepairFactsDigest() (string, error) {
	if c.Local.Validate() != nil || c.Local.Material == nil || c.Issues == nil {
		return "", errors.New("repair facts are not sealed")
	}
	generated := map[int64]bool{}
	for _, scope := range c.Local.Material.Intents {
		for _, binding := range scope.Bindings {
			generated[binding.IssueIndex] = true
		}
	}
	facts := []string{"source:" + c.Local.SourceCommit, "preparation:" + c.Local.PreparationDigest}
	for _, body := range c.Issues {
		if !generated[body.Index] {
			facts = append(facts, "manual:"+body.TitleDigest+":"+body.ContentDigest)
		}
	}
	for _, merge := range c.Local.Material.Merges {
		if merge.Stage == MergeMerged && ValidCommit(merge.MergedCommit) && merge.OriginalClosingOccurrence != "" {
			facts = append(facts, "merged:"+merge.MergedCommit+":"+merge.OriginalClosingOccurrence)
		}
	}
	sort.Strings(facts)
	// Replacing native identities with otherwise identical facts is not new evidence.
	facts = slices.Compact(facts)
	raw, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// RepairEpisodeView exposes bounded lifecycle facts, never private collector
// cursors or complete accepted/native source material through settings reads.
type RepairEpisodeView struct {
	EventID   string            `json:"event_id"`
	EpisodeID string            `json:"episode_id"`
	Reason    string            `json:"reason"`
	Cycles    []RepairCycleView `json:"cycles"`
}

type RepairCycleView struct {
	ID              string `json:"id"`
	Number          int    `json:"number"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	AssignmentID    string `json:"assignment_id,omitempty"`
	InventoryDigest string `json:"inventory_digest,omitempty"`
}
