package control

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

func sealIssueObservations(repository, generation int64, local factory.CompletionLocal, first []NativeInventoryIssue) bracketResult {
	running := foldInventoryPage([32]byte{}, repository, generation, first)
	// The retained numeric field is an observation token, not a remote clock.
	// The seal also binds the complete 256-bit content chain and exact bodies.
	revision := int64(binary.BigEndian.Uint64(running[:8]) & 0x7fffffffffffffff)
	// Zero names missing evidence in the retained record. The complete empty
	// observation has a zero chain, but still needs a positive observation token.
	if revision == 0 {
		revision = 1
	}
	g := inventoryGather{repository: repository, generation: generation, local: local, revision: revision, next: InventoryCollector{Issues: first, Running: running, Local: local, Revision: revision}}
	for _, issue := range first {
		g.evidence += len(issue.Data)
	}
	sealed, _, _, err := g.sealInventory()
	if err != nil {
		return bracketResult{hold: err.Error()}
	}
	return bracketResult{sealed: sealed}
}

// IssueObservationPage is a bounded evidence step for a source without an
// atomic repository clock. Its cursor resumes reads; it grants no authority.
type IssueObservationPage struct {
	Issues   []NativeInventoryIssue
	Next     string
	Complete bool
}

type IssueObservationProgress struct {
	NotBefore  int64 `json:",omitempty"`
	Version    int
	Generation int64
	Cursor     string
	Confirming bool
	Confirmed  int
	Pages      int
}

func bracketPagedIssueObservations(ctx context.Context, deps CompletionDriveDeps, repository, generation int64, saved *InventoryCollector) bracketResult {
	now := time.Now().Unix()
	if saved != nil && saved.Observation != nil && saved.Observation.Generation == generation && saved.Observation.NotBefore > now && saved.Observation.NotBefore <= now+idleCreationPollSeconds {
		return bracketResult{hold: HoldInventoryUnavailable, progress: saved}
	}
	if !chargeBracketCapturesAndObserves(ctx) {
		return bracketResult{hold: HoldInventoryInProgress, progress: saved}
	}
	local, err := deps.CaptureLocal(ctx, repository)
	if err != nil || local.Validate() != nil {
		return bracketResult{hold: HoldInventoryUnavailable, progress: saved}
	}
	collector := saved
	if collector == nil || collector.Observation == nil || collector.Observation.Version != 1 || collector.Observation.Generation != generation || !collector.Local.Equal(local) {
		collector = &InventoryCollector{Local: local, Issues: []NativeInventoryIssue{}, Observation: &IssueObservationProgress{Version: 1, Generation: generation}}
	}
	progress := collector.Observation
	if progress.Confirmed < 0 || progress.Confirmed > len(collector.Issues) || progress.Pages < 0 || checkInventoryIssues(collector.Issues, 0, 0, 0) != nil {
		return bracketResult{hold: HoldInventoryPage}
	}
	for steps := 0; steps < readinessMaxPages; steps++ {
		if budget := readinessBudgetFrom(ctx); budget != nil && budget.pages.Add(1) > readinessMaxPages {
			budget.pages.Add(-1)
			break
		}
		page, err := deps.ReadPage(ctx, repository, progress.Cursor)
		if err != nil {
			hold := routeInventoryPageError(err).Error()
			if hold == HoldInventoryRevision {
				return bracketResult{hold: hold}
			}
			if !errors.Is(err, ErrReadinessPassPending) && !errors.Is(err, context.Canceled) {
				progress.NotBefore = time.Now().Unix() + idleCreationPollSeconds
			}
			return bracketResult{hold: hold, progress: collector}
		}
		if page.Issues == nil || page.Complete != (page.Next == "") || !page.Complete && page.Next == progress.Cursor {
			return bracketResult{hold: HoldInventoryPage}
		}
		progress.Pages++
		if progress.Pages > 2*(maxCompletionInventoryPages+factory.MaxReassessmentIssues) {
			return bracketResult{hold: HoldInventoryOversize}
		}
		if progress.Confirming {
			end := progress.Confirmed + len(page.Issues)
			if end > len(collector.Issues) || len(page.Issues) > 0 && !reflect.DeepEqual(page.Issues, collector.Issues[progress.Confirmed:end]) {
				return bracketResult{hold: HoldInventoryRevision}
			}
			progress.Confirmed = end
		} else {
			var last int64
			if len(collector.Issues) > 0 {
				last = collector.Issues[len(collector.Issues)-1].Index
			}
			evidence := 0
			for _, issue := range collector.Issues {
				evidence += len(issue.Data)
			}
			if err := checkInventoryIssues(page.Issues, last, len(collector.Issues), evidence); err != nil {
				return bracketResult{hold: err.Error()}
			}
			collector.Issues = append(collector.Issues, page.Issues...)
		}
		progress.Cursor = page.Next
		if !page.Complete {
			continue
		}
		if !progress.Confirming {
			progress.Confirming = true
			continue
		}
		if progress.Confirmed != len(collector.Issues) {
			return bracketResult{hold: HoldInventoryRevision}
		}
		confirmed, err := deps.CaptureLocal(ctx, repository)
		if err != nil {
			progress.Cursor, progress.Confirmed = "", 0
			return bracketResult{hold: HoldInventoryUnavailable, progress: collector}
		}
		if !collector.Local.Equal(confirmed) {
			return bracketResult{hold: HoldInventoryMaterial}
		}
		return sealIssueObservations(repository, generation, collector.Local, collector.Issues)
	}
	return bracketResult{hold: HoldInventoryInProgress, progress: collector}
}
