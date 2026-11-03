package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// The diagnosis lives on its attributed post. Recording causal claims grants
// no judgment or adoption authority.
func recordRepairDiagnosisPostTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, sub factory.MCPSubmission) (string, error) {
	d, err := factory.DecodeRepairDiagnosis(sub.Payload)
	if err != nil {
		return "", err
	}
	a, current, err := repairObservationScopeTx(ctx, tx, cap)
	if err != nil {
		return "", err
	}
	if err := d.ValidateFor(cap, a, current); err != nil {
		return "", err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_feed_posts WHERE data->>'assignment'=$1 AND data->'communication'->>'kind'='diagnosis'`, a.ID).Scan(&count); err != nil {
		return "", err
	}
	if count >= factory.MaxRepairDiagnoses {
		return "", errors.New("diagnosis record limit reached; finish remains available")
	}
	content := factory.FeedCommunication{Kind: "diagnosis", Title: d.Title, Summary: "Recorded diagnosis (" + d.State + "). Graph and readiness are unchanged.\n" + d.Summary, Scope: d.Subject, EvidenceRefs: append([]string{}, d.EvidenceRefs...), Alternatives: append([]string{}, d.Alternatives...), Remaining: append([]string{}, d.Uncertainty...)}
	for _, cause := range d.Causes {
		content.Items = append(content.Items, factory.FeedCommunicationItem{ID: cause.CheckRef, Label: cause.CheckRef, Detail: cause.Detail, State: "reported_" + d.State})
	}
	postID := factory.MCPInputDigest([]byte(a.ID + "\n" + sub.SubmissionID))[:32]
	content.EvidenceRefs = append(content.EvidenceRefs, d.SourceCommit, "input:"+d.InputDigest, "inventory:"+d.InventoryDigest, "diagnosis:"+postID)
	for _, id := range d.ArtifactIDs {
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_artifacts WHERE id=$1 AND repository=$2 AND assignment=$3 AND complete)`, id, cap.Repository, a.ID).Scan(&owned); err != nil {
			return "", err
		}
		if !owned {
			return "", errors.New("diagnosis artifact is not a complete artifact of this assignment")
		}
		content.Artifacts = append(content.Artifacts, factory.FeedArtifact{ID: id})
	}
	if err := resolveCommunicationArtifactsTx(ctx, tx, cap.Repository, &content); err != nil {
		return "", err
	}
	return recordWorkerPostTx(ctx, tx, cap, sub, content)
}
