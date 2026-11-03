package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/levitateos/sodaos/internal/factory"
)

// The native plan remains authoritative. Publication shares its transaction
// and exact revision identity; it does not admit the plan or claim execution.
func recordInvestigationPlanFeedTx(ctx context.Context, tx *sql.Tx, plan factory.InvestigationPlan) error {
	var repository, issue int64
	err := tx.QueryRowContext(ctx, `SELECT repository,issue FROM factory_assignments WHERE id=$1`, plan.AttemptRoot).Scan(&repository, &issue)
	if errors.Is(err, sql.ErrNoRows) {
		// Unbound historical/imported plans have no authorized feed scope.
		return nil
	}
	if err != nil {
		return err
	}
	content := factory.FeedCommunication{
		ComponentID: "F11", Title: "Architecture plan recorded", Summary: TruncateFeedSummary(plan.Approach),
		Decision: plan.Approach, Scope: fmt.Sprintf("Issue %d · plan generation %d", issue, plan.Generation),
		Rationale:    "Recorded by the native plan revision operation. Plan admission, implementation and verification remain separate records.",
		EvidenceRefs: []string{"plan:" + plan.ID, "plan_digest:" + plan.Digest, "assignment:" + plan.AttemptRoot, "commit:" + plan.SourceCommit},
		PlanSource:   &factory.FeedPlanSource{AttemptRoot: plan.AttemptRoot, Plan: plan.ID, Digest: plan.Digest, Source: plan.SourceCommit, Generation: plan.Generation},
	}
	if len(plan.Findings) > 0 {
		content.Rationale += "\n\n" + TruncateFeedSummary(strings.Join(plan.Findings, "\n"))
	}
	for _, alternative := range plan.Rejected {
		if len(content.Alternatives) == 24 {
			break
		}
		content.Alternatives = append(content.Alternatives, TruncateFeedSummary(alternative.Approach+": "+alternative.Reason))
	}
	for _, fact := range plan.Unresolved {
		if len(content.Remaining) == 24 {
			break
		}
		content.Remaining = append(content.Remaining, TruncateFeedSummary(fact))
	}
	return recordFactoryFeedEventTx(ctx, tx, repository, issue, plan.AttemptRoot, "plan-revision:"+plan.AttemptRoot+":"+plan.ID, "plan:recorded", content, 0)
}
