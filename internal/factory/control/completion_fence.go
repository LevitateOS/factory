package control

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// WaitReassessmentPending reports an issue that did not launch because
// the repository completion fence is closed: at least one completion
// event has no released cycle yet. Dependency satisfaction stays
// truthful and already-running work, review/correction, cancellation
// and reconciliation proceed; only new investigation/implementation
// admission waits. Other repositories continue normally.
const WaitReassessmentPending = "reassessment_pending"

// dispatchFenced reports whether the repository fence is closed: any
// completion generation without a released cycle. Results memoize in
// the pass cache when present so one pass reads each repository once;
// the atomic packet transaction stays authoritative for admission, so a
// pass that observed an open fence still refuses a completion that
// lands first.
func dispatchFenced(ctx context.Context, deps DispatchDeps, repository int64) (bool, error) {
	if deps.FenceCache != nil {
		if fenced, ok := deps.FenceCache[repository]; ok {
			return fenced, nil
		}
	}
	read := deps.ReadCompletionFence
	if read == nil {
		if deps.Store == nil {
			return false, errors.New("completion fence unreadable")
		}
		read = deps.Store.CompletionPending
	}
	pending, err := read(ctx, repository)
	if err != nil {
		return false, err
	}
	fenced := len(pending) > 0
	if deps.FenceCache != nil {
		deps.FenceCache[repository] = fenced
	}
	return fenced, nil
}

// splitFencedCandidates excludes candidates of fenced repositories: they
// wait with reassessment_pending. Remaining candidates elect through
// the tournament, so one fenced repository never stalls global
// scheduling. Observation already happened upstream and continues:
// this split only governs selection and admission.
func splitFencedCandidates(candidates []factory.PriorityCandidate, fenced map[int64]bool) (open []factory.PriorityCandidate, waits []DispatchWait) {
	for _, candidate := range candidates {
		if fenced[candidate.Control.Repository] {
			waits = append(waits, DispatchWait{Repository: candidate.Control.Repository, Issue: candidate.Control.Issue, Reason: WaitReassessmentPending, Detail: "completion fence holds new admissions until the reassessment decision releases"})
			continue
		}
		open = append(open, candidate)
	}
	return open, waits
}

// fenceTournamentRepos reads the fence once per repository behind one
// selection. An unreadable fence fails the selection safely: the pass
// reports the store error and a later pass retries instead of electing
// through a possibly closed fence.
func fenceTournamentRepos(ctx context.Context, deps DispatchDeps, candidates []factory.PriorityCandidate) (map[int64]bool, error) {
	fenced := make(map[int64]bool)
	for _, candidate := range candidates {
		repository := candidate.Control.Repository
		if _, seen := fenced[repository]; seen {
			continue
		}
		closed, err := dispatchFenced(ctx, deps, repository)
		if err != nil {
			return nil, err
		}
		fenced[repository] = closed
	}
	return fenced, nil
}

// excludeFencedCandidates applies the fence to one tournament: fenced
// repositories leave selection with waits while the rest still elect.
// A fully fenced set reports waits only, never an error and never a
// global stall.
func excludeFencedCandidates(ctx context.Context, deps DispatchDeps, candidates []factory.PriorityCandidate, report DispatchReport) ([]factory.PriorityCandidate, DispatchReport) {
	fenced, err := fenceTournamentRepos(ctx, deps, candidates)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "completion fence unreadable"})
		return nil, report
	}
	open, waits := splitFencedCandidates(candidates, fenced)
	report.Waits = append(report.Waits, waits...)
	return open, report
}

// checkDispatchFence waits one planned attempt behind a closed fence.
// It runs after planning so readiness observation, harness probes and
// candidate collection keep their existing shape; only admission waits.
// The atomic packet stays authoritative: a completion that lands after
// this check still refuses at admission through ErrCompletionFenced.
func checkDispatchFence(ctx context.Context, deps DispatchDeps, repository, issue int64, report *DispatchReport) bool {
	fenced, err := dispatchFenced(ctx, deps, repository)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrStore, Detail: "completion fence unreadable"})
		return true
	}
	if fenced {
		report.Waits = append(report.Waits, DispatchWait{Repository: repository, Issue: issue, Reason: WaitReassessmentPending, Detail: "completion fence holds new admissions until the reassessment decision releases"})
		return true
	}
	return false
}

// reportCompletionFence maps an atomic packet fence refusal to its
// wait. The refusal proves a completion committed first; the issue
// waits for the release instead of launching. No capacity was consumed,
// so occupancy needs no refresh.
func reportCompletionFence(repository, issue int64, report *DispatchReport) {
	report.Waits = append(report.Waits, DispatchWait{Repository: repository, Issue: issue, Reason: WaitReassessmentPending, Detail: "completion fence closed during dispatch"})
}
