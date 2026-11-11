package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// freshReassessmentAdoption compares complete current evidence with the
// original seal, normalizing only the exact successors, recorded native
// effects and adopted acceptances of this journal's active/committed
// prefix. Unrelated source, authority, scope, native issue or dependency
// changes still hold the remaining work. A fresh capture is never simply
// blessed as the new baseline after an effect.
func (c *Coordinator) freshReassessmentAdoption(ctx context.Context, deps CompletionDriveDeps, record store.ReassessmentRecord, progress reassessmentAdoptionProgress, report *CompletionDriveReport) bool {
	var saved *InventoryCollector
	if len(record.CollectorProgress) > 0 {
		var collector InventoryCollector
		if json.Unmarshal(record.CollectorProgress, &collector) == nil {
			saved = &collector
		}
	}
	fresh := bracketMaterialCollect(ctx, deps, record.Cycle.Repository, record.Cycle.Generation, saved)
	if fresh.hold != "" {
		record.CollectorProgress = nil
		if fresh.progress != nil {
			record.CollectorProgress, _ = json.Marshal(fresh.progress)
		}
		c.holdCompletion(ctx, record, fresh.hold, report, false)
		return false
	}
	if err := c.matchReassessmentEffects(ctx, record, progress, *fresh.sealed); err != nil {
		c.holdCompletion(ctx, record, "reassessment_inputs_changed_after_prefix", report, false)
		return false
	}
	return true
}

func evidenceDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (c *Coordinator) matchReassessmentEffects(ctx context.Context, record store.ReassessmentRecord, progress reassessmentAdoptionProgress, fresh SealedInventory) error {
	return c.matchGraphRepairEffects(ctx, record.Local, record.Evidence, record.EvidenceRevision, record.Cycle.CycleID, false, progress, fresh)
}

func (c *Coordinator) matchGraphRepairEffects(ctx context.Context, baseline factory.CompletionLocal, bodies []factory.CompletionIssueEvidence, nativeRevision int64, cycleID string, stall bool, progress reassessmentAdoptionProgress, fresh SealedInventory) error {
	if fresh.Local.SourceCommit != baseline.SourceCommit || fresh.Local.PreparationDigest != baseline.PreparationDigest || fresh.Local.AuthorityFingerprint != baseline.AuthorityFingerprint {
		return errors.New("reassessment source, preparation or authority changed")
	}
	material := *fresh.Local.Material
	material.Intents = append([]factory.CompletionIntentScope(nil), material.Intents...)
	material.Acceptances = append([]factory.Acceptance(nil), material.Acceptances...)
	material.Controls = append([]factory.IssueControl(nil), material.Controls...)
	expectedBodies := map[int64]factory.CompletionIssueEvidence{}
	for _, issue := range bodies {
		expectedBodies[issue.Index] = issue
	}
	newScopes := map[string]factory.CompletionIntentScope{}
	activated := map[string]bool{}
	newAcceptances := map[string]bool{}
	changedControls := map[int64]string{}
	nativeEffect := false
	for i, entry := range progress.Intents {
		step := progress.Journal.Steps[i]
		if step.State == reassessmentStepStaged {
			continue
		}
		generation, err := c.Store.IntentGeneration(ctx, entry.Revision.ID)
		if err != nil || generation.ProtectedGraphCycle() != cycleID || (generation.RepairCycle != "") != stall || generation.IntentDigest != entry.Revision.Digest() || generation.OperationID != step.OperationID || generation.LocalOnly != step.NoEffect {
			return errors.New("reassessment prefix lacks its exact generation")
		}
		bindings, err := c.Store.IntentTaskBindings(ctx, entry.Revision.ID)
		if err != nil {
			return err
		}
		var receipt factory.GraphReceiptFacts
		committed := generation.State == "committed" || generation.State == "completed"
		if committed && !generation.LocalOnly {
			receipt, err = c.Graph.DecodeGraphReceipt(ctx, generation.Receipt)
			if err != nil || receipt.Repository != entry.Revision.Repository || receipt.Actor != generation.ActorID {
				return errors.New("reassessment prefix receipt is unbound")
			}
			nativeEffect = true
		}
		issues := map[string]factory.GeneratedIssueIdentity{}
		if committed {
			issues, err = intentReceiptIssues(generation.SubmittedTasks, generation, receipt)
			if err != nil {
				return err
			}
		}
		copied := map[string]factory.IntentTaskBinding{}
		for _, binding := range generation.CopiedBindings {
			copied[binding.TaskKey] = binding
		}
		for _, binding := range bindings {
			if old, ok := copied[binding.TaskKey]; ok {
				if binding != old {
					return errors.New("reassessment copied binding changed")
				}
				continue
			}
			expected := issues[binding.TaskKey]
			if !committed || binding.AcceptanceID != generatedDecisionID("acceptance", entry.Revision.ID, binding.TaskKey) || binding.IssueID != expected.IssueID || binding.IssueIndex != expected.IssueIndex || binding.ContentVersion != expected.ContentVersion {
				return errors.New("reassessment adoption does not match its receipt")
			}
			newAcceptances[binding.AcceptanceID] = true
			changedControls[binding.IssueIndex] = binding.AcceptanceID
		}
		scope, err := factory.NewCompletionIntentScope(entry.Revision, factory.IntentAccepted, true, &generation, bindings)
		if err != nil {
			return err
		}
		newScopes[entry.Revision.ID] = scope
		activated[entry.Revision.Intent] = true
		if reader, ok := c.Graph.(IssueGraphPrefix); ok && !generation.LocalOnly {
			prefix, err := reader.ReadGraphPrefix(ctx, generation, entry.Revision.Repository)
			if err != nil || prefix.Repository != entry.Revision.Repository || prefix.Actor != generation.ActorID {
				return errors.New("reassessment graph prefix is unbound")
			}
			baseline := make([]factory.CompletionIssueEvidence, 0, len(expectedBodies))
			for _, body := range expectedBodies {
				baseline = append(baseline, body)
			}
			updated, err := prefix.ApplyTo(baseline)
			if err != nil {
				return err
			}
			for _, body := range updated {
				expectedBodies[body.Index] = body
			}
			if len(prefix.Effects) > 0 {
				nativeEffect = true
			}
			continue
		}
		if !committed || generation.LocalOnly {
			continue
		}
		facts, err := inspectGraphPacket(c.Graph, entry.Packet.Payload)
		if err != nil {
			return err
		}
		creates := map[string]collaboration.GraphCreate{}
		for _, create := range facts.Creates {
			creates[create.Key] = create
		}
		edits := facts.Edits
		for _, task := range generation.SubmittedTasks {
			if _, unchanged := copied[task.Key]; unchanged {
				continue
			}
			identity := issues[task.Key]
			body, existed := expectedBodies[identity.IssueIndex]
			if create, ok := creates[task.Key]; ok {
				body = factory.CompletionIssueEvidence{Index: identity.IssueIndex, Title: create.Title, Body: create.Body, TitleDigest: evidenceDigest(create.Title), ContentDigest: evidenceDigest(create.Body)}
			} else if edit, ok := edits[identity.IssueID]; ok {
				body.Title, body.Body, body.TitleDigest, body.ContentDigest = edit.Title, edit.Body, evidenceDigest(edit.Title), evidenceDigest(edit.Body)
			} else if !existed {
				return errors.New("reassessment prefix cannot invent an unchanged issue")
			}
			body.Dependencies = nil
			for _, dependency := range task.DependsOn {
				bound := issues[dependency]
				if bound.IssueID == 0 {
					return errors.New("reassessment prefix prerequisite is unbound")
				}
				body.Dependencies = append(body.Dependencies, strconv.FormatInt(bound.IssueID, 10))
			}
			sort.Slice(body.Dependencies, func(i, j int) bool {
				left, right := body.Dependencies[i], body.Dependencies[j]
				return len(left) < len(right) || len(left) == len(right) && left < right
			})
			expectedBodies[identity.IssueIndex] = body
		}
	}
	if !nativeEffect && fresh.Revision != nativeRevision {
		return errors.New("reassessment native revision moved without a recorded effect")
	}
	if len(expectedBodies) != len(fresh.Bodies) {
		return errors.New("reassessment native inventory changed outside its recorded effects")
	}
	for _, body := range fresh.Bodies {
		if expected, ok := expectedBodies[body.Index]; !ok || !sameEncodedValue(expected, body) {
			return errors.New("reassessment native evidence differs from its recorded effect")
		}
	}
	oldScopes := map[string]factory.CompletionIntentScope{}
	for _, scope := range baseline.Material.Intents {
		oldScopes[scope.RevisionID] = scope
	}
	intents := make([]factory.CompletionIntentScope, 0, len(oldScopes))
	seenNew := map[string]bool{}
	for _, scope := range material.Intents {
		if expected, ok := newScopes[scope.RevisionID]; ok {
			if !sameEncodedValue(scope, expected) {
				return errors.New("reassessment successor scope changed")
			}
			seenNew[scope.RevisionID] = true
			continue
		}
		if activated[scope.IntentID] {
			old, ok := oldScopes[scope.RevisionID]
			if !ok || scope.CurrentState != factory.IntentAccepted || scope.LatestAccepted {
				return errors.New("reassessment accepted predecessor chain changed")
			}
			scope.LatestAccepted = old.LatestAccepted
		}
		intents = append(intents, scope)
	}
	if len(seenNew) != len(newScopes) {
		return errors.New("reassessment activated head is missing")
	}
	material.Intents = intents
	acceptances := make([]factory.Acceptance, 0, len(material.Acceptances))
	for _, acceptance := range material.Acceptances {
		if !newAcceptances[acceptance.ID] {
			acceptances = append(acceptances, acceptance)
		}
	}
	material.Acceptances = acceptances
	controls := make([]factory.IssueControl, 0, len(material.Controls))
	oldControls := map[int64]factory.IssueControl{}
	for _, control := range baseline.Material.Controls {
		oldControls[control.Issue] = control
	}
	for _, control := range material.Controls {
		if expected, ok := changedControls[control.Issue]; ok && control.Acceptance == expected {
			if old, existed := oldControls[control.Issue]; existed {
				controls = append(controls, old)
			}
			continue
		}
		controls = append(controls, control)
	}
	material.Controls = controls
	local := fresh.Local
	local.Material = &material
	var err error
	local.ScopeDigest, err = material.Digest()
	if err != nil || !local.Equal(baseline) {
		return errors.New("reassessment local material changed outside its recorded effects")
	}
	return nil
}

func sameEncodedValue(left, right any) bool {
	a, err := json.Marshal(left)
	if err != nil {
		return false
	}
	b, err := json.Marshal(right)
	return err == nil && sameEncodedJSON(a, b)
}
