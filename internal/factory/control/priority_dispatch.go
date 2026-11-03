package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// PrioritySnapshotChanging reports a pass that restarted its durable
// inventory because the native revision or the local inputs moved.
// Stable inputs make progress instead; native unavailability stays
// priority_incomplete, never a subset election.
const PrioritySnapshotChanging = "priority_snapshot_changing"

func dispatchPriorityPass(ctx context.Context, deps DispatchDeps, report DispatchReport) DispatchReport {
	if deps.Priority == nil {
		return report
	}
	return dispatchPriorityInventoryPass(ctx, deps, report)
}

func priorityObserveRevision(ctx context.Context, deps DispatchDeps) (int64, bool, error) {
	if deps.ObserveRevision == nil {
		return 0, true, nil
	}
	return deps.ObserveRevision(ctx)
}

func priorityRefreshCandidate(ctx context.Context, deps DispatchDeps, control factory.IssueControl, scanRevision int64) (factory.IssueControl, bool, error) {
	if deps.RefreshCandidate == nil {
		return control, true, nil
	}
	return deps.RefreshCandidate(ctx, control, scanRevision)
}

// priorityMemberEligible mirrors the inventory membership predicate for
// one re-read control: queued members plus accepted authority-blocked
// members that may have become ready. Anything else leaves an exclusion
// record instead of stalling the scan.
func priorityMemberEligible(control factory.IssueControl) bool {
	if control.Readiness == factory.ReadinessQueued {
		return true
	}
	return control.Readiness == factory.ReadinessNotAuthorized &&
		control.Reason == factory.BlockerAuthorityMissing && control.Acceptance != ""
}

// dispatchPriorityInventoryPass advances one durable priority scan: it
// captures the complete ordered membership with its local digest under a
// single observed idle revision, resumes observation from the saved
// cursor within the pass bounds, and runs the existing tournament once
// the set is complete. Observed candidates persist, so later passes and
// restarts never repeat a full native scan and never truncate the set.
func dispatchPriorityInventoryPass(ctx context.Context, deps DispatchDeps, report DispatchReport) DispatchReport {
	nativeRev, idle, err := priorityObserveRevision(ctx, deps)
	if err != nil || !idle {
		report.Errors = append(report.Errors, DispatchError{Reason: "priority_incomplete", Detail: "native revision unavailable"})
		return report
	}
	inventory, err := deps.Store.CapturePriorityInventory(ctx)
	if err != nil {
		if errors.Is(err, store.ErrPriorityInventoryTooLarge) {
			report.Errors = append(report.Errors, DispatchError{Reason: "priority_incomplete", Detail: "complete candidate inventory unavailable"})
			return report
		}
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority inventory unavailable"})
		return report
	}
	confirmed, idle, err := priorityObserveRevision(ctx, deps)
	if err != nil || !idle {
		report.Errors = append(report.Errors, DispatchError{Reason: "priority_incomplete", Detail: "native revision unavailable"})
		return report
	}
	if confirmed != nativeRev {
		// An insertion between membership and digest must not disappear:
		// save nothing and restart next pass.
		report.Errors = append(report.Errors, DispatchError{Reason: PrioritySnapshotChanging, Detail: "native revision advanced during inventory capture"})
		return report
	}
	_, haveSaved, err := priorityScanState(ctx, deps)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority scan unreadable"})
		return report
	}
	resumed, err := deps.Store.SavePriorityScan(ctx, nativeRev, inventory)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority scan unrecorded"})
		return report
	}
	if haveSaved && !resumed {
		report.Errors = append(report.Errors, DispatchError{Reason: PrioritySnapshotChanging, Detail: "priority inventory changed; restarting durable scan"})
	}
	scan, err := deps.Store.LoadPriorityScan(ctx)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority scan unreadable"})
		return report
	}
	if scan.Cursor >= len(scan.Subjects) {
		return completePrioritySelection(ctx, deps, nativeRev, scan, report)
	}
	return observePriorityInventory(ctx, deps, nativeRev, scan, report)
}

// priorityScanState loads the saved scan, reporting whether one exists.
// A missing scan is a fresh start, not a failure.
func priorityScanState(ctx context.Context, deps DispatchDeps) (store.PriorityScanState, bool, error) {
	saved, err := deps.Store.LoadPriorityScan(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return store.PriorityScanState{}, false, nil
	}
	if err != nil {
		return store.PriorityScanState{}, false, err
	}
	return saved, true, nil
}

// observePriorityInventory observes members from the saved cursor. Fresh
// members cost no native revision check; stale members reassess through
// the bounded path. The pass persists every observation and its cursor,
// so exhaustion only pauses the scan instead of starving it.
func observePriorityInventory(ctx context.Context, deps DispatchDeps, nativeRev int64, scan store.PriorityScanState, report DispatchReport) DispatchReport {
	occupancy, err := snapshotOccupancy(ctx, deps.Store)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "capacity accounting unavailable"})
		return report
	}
	var observations []store.PriorityCandidateObservation
	cursor := scan.Cursor
	collect := deps
	collect.Refresh = nil
	var collected *store.PriorityCandidateObservation
	collect.collectPlan = func(plan *attemptPlan) {
		candidate := priorityCandidate(plan)
		data, marshalErr := json.Marshal(candidate)
		if marshalErr != nil {
			return
		}
		collected = &store.PriorityCandidateObservation{
			Subject:    store.PrioritySubjectKey(plan.control.Repository, plan.control.Issue),
			Repository: plan.control.Repository, Issue: plan.control.Issue,
			Candidate: data, Fingerprint: plan.control.Fingerprint,
		}
	}
	stopped := false
	for i := scan.Cursor; i < len(scan.Subjects) && !stopped; i++ {
		if ctx.Err() != nil {
			break
		}
		collected = nil
		subject := scan.Subjects[i]
		observation, advance, err := observePrioritySubject(ctx, collect, &occupancy, subject, nativeRev, &report)
		if err != nil {
			if len(observations) > 0 {
				if recordErr := deps.Store.RecordPriorityProgress(ctx, nativeRev, scan.LocalDigest, observations, cursor); recordErr != nil {
					report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority progress unrecorded"})
					return report
				}
			}
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority member unreadable"})
			return report
		}
		if !advance {
			stopped = true
			continue
		}
		if observation.Subject != "" {
			observations = append(observations, observation)
		} else if collected != nil {
			observations = append(observations, *collected)
		} else {
			stopped = true
			continue
		}
		cursor = i + 1
	}
	if len(observations) > 0 {
		if err := deps.Store.RecordPriorityProgress(ctx, nativeRev, scan.LocalDigest, observations, cursor); err != nil {
			if errors.Is(err, store.ErrStaleRevision) {
				report.Errors = append(report.Errors, DispatchError{Reason: PrioritySnapshotChanging, Detail: "priority inventory changed during observation"})
				return report
			}
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority progress unrecorded"})
			return report
		}
	}
	if cursor >= len(scan.Subjects) {
		updated, err := deps.Store.LoadPriorityScan(ctx)
		if err != nil {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority scan unreadable"})
			return report
		}
		return completePrioritySelection(ctx, deps, nativeRev, updated, report)
	}
	if cursor == scan.Cursor {
		report.Errors = append(report.Errors, DispatchError{Reason: "priority_incomplete", Detail: "candidate observation could not advance"})
	}
	return report
}

// observePrioritySubject observes one member. An observation with Subject
// set records a candidate (via the collect hook) or an exclusion;
// advance=false pauses the scan before the member so the next pass
// resumes it. A store read failure returns an error.
func observePrioritySubject(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, subject store.PriorityInventorySubject, nativeRev int64, report *DispatchReport) (store.PriorityCandidateObservation, bool, error) {
	key := store.PrioritySubjectKey(subject.Repository, subject.Issue)
	control, err := deps.Store.IssueControl(ctx, subject.Repository, subject.Issue)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.PriorityCandidateObservation{Subject: key, Repository: subject.Repository, Issue: subject.Issue, Exclusion: "control_missing"}, true, nil
		}
		return store.PriorityCandidateObservation{}, false, err
	}
	if !priorityMemberEligible(control) {
		return store.PriorityCandidateObservation{Subject: key, Repository: subject.Repository, Issue: subject.Issue, Exclusion: "not_eligible", Fingerprint: control.Fingerprint}, true, nil
	}
	refreshed, ok, err := priorityRefreshCandidate(ctx, deps, control, nativeRev)
	if err != nil || !ok {
		return store.PriorityCandidateObservation{}, false, nil
	}
	silent := newDispatchReport()
	dispatchOne(ctx, deps, occupancy, refreshed, &silent)
	// A completed admitted investigation continues its own attempt before
	// fresh-work selection. Keep that real launch in the pass report and
	// exclude the now-assigned issue from the tournament.
	if len(silent.Launched) > 0 {
		report.Launched = append(report.Launched, silent.Launched...)
		return store.PriorityCandidateObservation{Subject: key, Repository: subject.Repository, Issue: subject.Issue, Exclusion: WaitAssigned, Fingerprint: refreshed.Fingerprint}, true, nil
	}
	if len(silent.Errors) > 0 {
		return store.PriorityCandidateObservation{}, false, nil
	}
	if len(silent.Waits) > 0 {
		return store.PriorityCandidateObservation{Subject: key, Repository: subject.Repository, Issue: subject.Issue, Exclusion: silent.Waits[0].Reason, Fingerprint: refreshed.Fingerprint}, true, nil
	}
	return store.PriorityCandidateObservation{}, true, nil
}

// completePrioritySelection runs the existing complete tournament over
// the durably observed set, then admits only the selected investigation
// after rechecking the native revision, the full local digest and fresh
// selected-issue evidence. The set is never truncated to fit a pass and
// never re-observed: only the winner reads fresh evidence.
type prioritySelection struct {
	candidates   []factory.PriorityCandidate
	storedByID   map[string]factory.PriorityCandidate
	subjectsByID map[string]store.PriorityInventorySubject
}

func collectPriorityCandidates(scan store.PriorityScanState, report DispatchReport) (prioritySelection, DispatchReport, bool) {
	selection := prioritySelection{
		candidates:   make([]factory.PriorityCandidate, 0, len(scan.Subjects)),
		storedByID:   map[string]factory.PriorityCandidate{},
		subjectsByID: map[string]store.PriorityInventorySubject{},
	}
	for _, subject := range scan.Subjects {
		observation, ok := scan.Observations[store.PrioritySubjectKey(subject.Repository, subject.Issue)]
		if !ok {
			report.Errors = append(report.Errors, DispatchError{Reason: "priority_incomplete", Detail: "candidate observations incomplete"})
			return selection, report, false
		}
		if observation.Exclusion != "" {
			// Actual admission waits belong to the same dispatch report the
			// stall classifier consumes. A queued source/context wait is not
			// fresh ready work and must not retire an admitted diagnosis.
			if observation.Exclusion != "control_missing" && observation.Exclusion != "not_eligible" {
				report.Waits = append(report.Waits, DispatchWait{Repository: subject.Repository, Issue: subject.Issue, Reason: observation.Exclusion, Detail: "Current priority admission is waiting."})
			}
			continue
		}
		var candidate factory.PriorityCandidate
		if err := json.Unmarshal(observation.Candidate, &candidate); err != nil {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority candidate unreadable"})
			return selection, report, false
		}
		selection.candidates = append(selection.candidates, candidate)
		selection.storedByID[candidate.ID] = candidate
		selection.subjectsByID[candidate.ID] = subject
	}
	return selection, report, true
}

func confirmPrioritySnapshot(ctx context.Context, deps DispatchDeps, nativeRev int64, scan store.PriorityScanState, report DispatchReport) (int64, DispatchReport, bool) {
	// After judgment, recheck the revision and the full local digest, then
	// read fresh selected-issue evidence whose bracket also reports N.
	confirmed, idle, err := priorityObserveRevision(ctx, deps)
	if err != nil || !idle {
		report.Errors = append(report.Errors, DispatchError{Reason: "priority_incomplete", Detail: "native revision unavailable"})
		return 0, report, false
	}
	if confirmed != nativeRev {
		_ = deps.Store.DeletePriorityScan(ctx)
		report.Errors = append(report.Errors, DispatchError{Reason: PrioritySnapshotChanging, Detail: "native revision advanced after judgment"})
		return 0, report, false
	}
	fresh, err := deps.Store.CapturePriorityInventory(ctx)
	if err != nil {
		if errors.Is(err, store.ErrPriorityInventoryTooLarge) {
			report.Errors = append(report.Errors, DispatchError{Reason: "priority_incomplete", Detail: "complete candidate inventory unavailable"})
			return 0, report, false
		}
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority inventory unavailable"})
		return 0, report, false
	}
	if fresh.LocalDigest != scan.LocalDigest || len(fresh.Subjects) != len(scan.Subjects) {
		_ = deps.Store.DeletePriorityScan(ctx)
		report.Errors = append(report.Errors, DispatchError{Reason: PrioritySnapshotChanging, Detail: "priority inputs changed after judgment"})
		return 0, report, false
	}
	return confirmed, report, true
}

func launchSelectedCandidate(ctx context.Context, deps DispatchDeps, selection prioritySelection, selected string, confirmed int64, report DispatchReport) DispatchReport {
	subject := selection.subjectsByID[selected]
	stored := selection.storedByID[selected]
	current, err := deps.Store.IssueControl(ctx, subject.Repository, subject.Issue)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			report.Waits = append(report.Waits, DispatchWait{Repository: subject.Repository, Issue: subject.Issue, Reason: WaitInputsChanged, Detail: "selected readiness changed during dispatch"})
			return report
		}
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "selected control unreadable"})
		return report
	}
	freshOccupancy, err := snapshotOccupancy(ctx, deps.Store)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "capacity accounting unavailable"})
		return report
	}
	before, _ := priorityExactSnapshot([]factory.PriorityCandidate{stored})
	final := deps
	final.collectPlan = nil
	final.checkPlan = func(plan *attemptPlan) bool {
		if plan.inputs.Revision != confirmed {
			return false
		}
		after, _ := priorityExactSnapshot([]factory.PriorityCandidate{priorityCandidate(plan)})
		return before == after
	}
	launched := len(report.Launched)
	dispatchOne(ctx, final, &freshOccupancy, current, &report)
	if len(report.Launched) > launched {
		if err := deps.Store.DeletePriorityScan(ctx); err != nil {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "priority scan unretired"})
		}
	}
	return report
}

func completePrioritySelection(ctx context.Context, deps DispatchDeps, nativeRev int64, scan store.PriorityScanState, report DispatchReport) DispatchReport {
	selection, report, ok := collectPriorityCandidates(scan, report)
	if !ok {
		return report
	}
	candidates := selection.candidates
	if len(candidates) == 0 {
		// Native availability can recover without changing either the
		// repository revision or local authority. Asynchronous preparation
		// readiness can likewise advance only on its next native read.
		// A completed empty scan
		// must retry these reads, rather than retaining a transient refusal
		// forever. The next scan still observes the complete inventory
		// within the existing per-pass cursor and RPC bounds.
		for _, observation := range scan.Observations {
			switch observation.Exclusion {
			case WaitSource, WaitHarness, WaitPreparation, WaitInputsUnavailable, WaitInputsBusy, WaitInputsStale, WaitInputsIncomplete:
				if err := deps.Store.DeletePriorityScan(ctx); err != nil {
					report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "transient priority scan unretired"})
				}
				return report
			}
		}
		return report
	}
	// D13 completion fence (FW31): a pending event excludes its
	// repository from selection with reassessment_pending waits. The
	// rest still elect, so one fenced repository never stalls the
	// global tournament. Observation above already happened and
	// continues; this split only governs selection.
	candidates, report = excludeFencedCandidates(ctx, deps, candidates, report)
	if len(candidates) == 0 {
		return report
	}
	selected, err := selectPriority(ctx, deps.Priority, candidates, time.Now())
	if err != nil {
		for _, candidate := range candidates {
			report.Waits = append(report.Waits, DispatchWait{Repository: candidate.Control.Repository, Issue: candidate.Control.Issue, Reason: "priority_unavailable", Detail: "finite priority selection is unavailable"})
		}
		return report
	}
	if selected == "" {
		return report
	}
	confirmed, report, ok := confirmPrioritySnapshot(ctx, deps, nativeRev, scan, report)
	if !ok {
		return report
	}
	return launchSelectedCandidate(ctx, deps, selection, selected, confirmed, report)
}

func priorityCandidate(plan *attemptPlan) factory.PriorityCandidate {
	candidate := factory.PriorityCandidate{
		ID:      fmt.Sprintf("%d/%d", plan.control.Repository, plan.control.Issue),
		Control: plan.control, Objective: plan.inputs.Issue.Title + "\n" + plan.inputs.Issue.Body,
		Acceptance:       plan.acceptance,
		AcceptedSections: append(promptSections(plan.acceptance.Sources, plan.inputs.Comments), promptSections(plan.acceptance.Resolutions, plan.inputs.Comments)...),
		Authority:        plan.effective.Authority, Preparation: plan.prep.Preparation.ID,
		PreparationRevision: plan.prep.Preparation.Revision, Source: plan.inputs.Tip, GateRevision: plan.gateRev, PlannedMinutes: plan.planned,
		PromptDigest: plan.promptSHA,
	}
	return candidate
}

func priorityExactSnapshot(candidates []factory.PriorityCandidate) (string, error) {
	data, err := json.Marshal(candidates)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
