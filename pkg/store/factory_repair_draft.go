package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
)

func recordRepairDraftPostTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, sub factory.MCPSubmission) (string, error) {
	draft, err := factory.DecodeRepairDraft(sub.Payload)
	if err != nil {
		return "", err
	}
	a, current, err := repairObservationScopeTx(ctx, tx, cap)
	if err != nil {
		return "", err
	}
	proposal, err := draft.ValidateFor(cap, a, current)
	if err != nil {
		return "", err
	}
	ids, err := draft.DiagnosisIDs()
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data->'diagnosis' FROM factory_feed_posts WHERE id=$1 AND repository=$2 AND data->>'assignment'=$3 AND data->>'run'=$4 AND data->'communication'->>'kind'='diagnosis' FOR SHARE`, id, a.Repository, a.ID, a.Run).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("draft diagnosis is not a recorded observation of this assignment")
			}
			return "", err
		}
		var diagnosis factory.RepairDiagnosis
		if json.Unmarshal(raw, &diagnosis) != nil || diagnosis.ValidateFor(cap, a, current) != nil {
			return "", errors.New("draft diagnosis differs from its canonical cause and input")
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_feed_posts WHERE data->>'assignment'=$1 AND data->'communication'->>'kind'='repair_draft'`, a.ID).Scan(&count); err != nil {
		return "", err
	}
	if count >= factory.MaxRepairObservations {
		return "", errors.New("repair draft limit reached; finish remains available")
	}
	content := factory.FeedCommunication{Kind: "repair_draft", Title: "Proposed repair graph", Summary: "Recorded draft (" + proposal.Outcome + "). Not submitted for judgment or adopted; graph and readiness are unchanged.", Scope: cap.Subject, Before: "Sealed inventory " + proposal.InventoryDigest, After: "Proposed tasks only; use preview for deterministic adoption feedback.", EvidenceRefs: append([]string{}, draft.DiagnosisRefs...)}
	postID := factory.MCPInputDigest([]byte(a.ID + "\n" + sub.SubmissionID))[:32]
	content.EvidenceRefs = append(content.EvidenceRefs, proposal.SourceCommit, "input:"+cap.ScopeDigest(), "inventory:"+proposal.InventoryDigest, "draft:"+postID)
	rows, abbreviated := 0, false
	appendPreview := func(item factory.FeedCommunicationItem) {
		rows++
		if len(content.Items) >= feedPayloadCap {
			return
		}
		label, detail := feedText(item.Label), feedText(item.Detail)
		abbreviated = abbreviated || label != item.Label || detail != item.Detail || len(item.DependsOn) > feedPayloadCap
		item.Label, item.Detail = label, detail
		if len(item.DependsOn) > feedPayloadCap {
			item.DependsOn = item.DependsOn[:feedPayloadCap]
		}
		content.Items = append(content.Items, item)
	}
	for _, change := range proposal.Changes {
		appendPreview(factory.FeedCommunicationItem{ID: change.ExpectedRevision, Label: change.IntentID, Detail: change.Reason, State: "proposed_revision"})
		for _, task := range change.Tasks {
			appendPreview(factory.FeedCommunicationItem{ID: task.Key, DependsOn: append([]string{}, task.DependsOn...), Label: task.Objective, Detail: "Outcomes: " + strings.Join(task.Outcomes, "; ") + "\nAccepted ideas: " + strings.Join(task.IdeaIDs, ", "), State: "proposed"})
		}
	}
	content.Remaining = []string{fmt.Sprintf("Preview shows %d of %d proposed graph rows. Read the complete report through draft:%s.", len(content.Items), rows, postID)}
	if abbreviated {
		content.Remaining = append(content.Remaining, "Long text or prerequisite lists are abbreviated in this preview; the recorded draft retains every byte.")
	}
	return recordWorkerPostTx(ctx, tx, cap, sub, content)
}
