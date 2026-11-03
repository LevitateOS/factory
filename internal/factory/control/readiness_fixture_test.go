package control

import (
	"context"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

const (
	readinessTitleDigest   = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	readinessContentDigest = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	readinessCommentDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type fakeEvidenceSource struct {
	evidence map[string]AcceptanceEvidence
	errs     map[string]error
	calls    map[string]int
}

func (f *fakeEvidenceSource) ReadAcceptanceEvidence(_ context.Context, repository, issue string, _ []string) (AcceptanceEvidence, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	key := repository + "/" + issue
	f.calls[key]++
	if err, ok := f.errs[key]; ok {
		return AcceptanceEvidence{}, err
	}
	evidence, ok := f.evidence[key]
	if !ok {
		return AcceptanceEvidence{}, refuseAcceptance(RefusalSnapshotUnavailable)
	}
	return evidence, nil
}

type fakeObserver struct {
	revision int64
	idle     bool
	revErr   error
}

func (f *fakeObserver) ObserveNativeRevision(context.Context) (int64, bool, error) {
	if f.revErr != nil {
		return 0, false, f.revErr
	}
	return f.revision, f.idle, nil
}

func readinessView(issue string) AcceptanceIssueView {
	return AcceptanceIssueView{
		Index: issue, TitleOccurrence: factory.OriginalOccurrence, LifecycleOccurrence: factory.OriginalOccurrence, TitleDigest: readinessTitleDigest, ContentDigest: readinessContentDigest,
		PosterID: "5", Visible: true,
	}
}

func readinessEvidence(revision int64, view AcceptanceIssueView, comments []AcceptanceComment, edges []AcceptanceEdge) AcceptanceEvidence {
	return AcceptanceEvidence{Issue: view, Comments: comments, Dependencies: edges, Revision: revision}
}

func readinessDecision(id string, repository int64, issue string, revision int64) factory.Acceptance {
	return factory.Acceptance{
		ID: id, Repository: repository, IssueIndex: issue,
		Approver: 5, NativeRev: revision, TitleOccurrence: factory.OriginalOccurrence,
		TitleDigest: readinessTitleDigest, ContentDigest: readinessContentDigest,
	}
}

func readinessHint(delivery string, repository, issue int64) IntakeHint {
	return IntakeHint{Delivery: delivery, Repository: repository, Issue: issue}
}

func mustAdmit(t *testing.T, c *Coordinator, decision factory.Acceptance) {
	t.Helper()
	if err := c.Store.AdmitAcceptanceDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
}

func findBlocker(blockers []factory.Blocker, code string) *factory.Blocker {
	for i := range blockers {
		if blockers[i].Code == code {
			return &blockers[i]
		}
	}
	return nil
}
