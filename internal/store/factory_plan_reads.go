package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
)

// MaxPriorityComparisons bounds one priority comparison page; receipts
// can carry large candidate sets, so pages stay small.
const MaxPriorityComparisons = 20

// DefaultPriorityComparisons is the comparison page size when the caller
// names no bound.
const DefaultPriorityComparisons = 5

// PriorityComparisonCursor pages completed priority receipts newest-first.
// The zero cursor reads the first page; later pages echo the previous
// page's trailing (created_unix, snapshot).
type PriorityComparisonCursor struct {
	BeforeSnapshot string
	BeforeUnix     int64
}

func validatePriorityCursor(cursor PriorityComparisonCursor) error {
	if cursor.BeforeSnapshot == "" && cursor.BeforeUnix <= 0 {
		return nil
	}
	if cursor.BeforeUnix <= 0 || !factory.ValidDigest(cursor.BeforeSnapshot) {
		return errors.New("invalid priority comparison cursor")
	}
	return nil
}

// PriorityReceiptsForIssue returns completed priority receipts naming one
// native issue, newest first, with a has-more flag for cursor paging.
// Receipts are keyed by candidate-set snapshot in the journal; this read
// discovers the recent comparisons behind one issue without rebuilding
// the eligible set, judging options or dispatching work.
func (s *Store) PriorityReceiptsForIssue(ctx context.Context, repository, issue int64, cursor PriorityComparisonCursor, limit int) ([]factory.PriorityReceipt, bool, error) {
	if repository <= 0 || issue <= 0 {
		return nil, false, errors.New("invalid priority comparison scope")
	}
	if limit < 1 || limit > MaxPriorityComparisons {
		return nil, false, errors.New("invalid priority comparison bound")
	}
	if err := validatePriorityCursor(cursor); err != nil {
		return nil, false, err
	}
	filter, err := json.Marshal([]map[string]any{{
		"control": map[string]string{
			"repository": strconv.FormatInt(repository, 10),
			"issue":      strconv.FormatInt(issue, 10),
		},
	}})
	if err != nil {
		return nil, false, err
	}
	query := `SELECT data FROM factory_priority_judgments WHERE data IS NOT NULL AND data->'candidates' @> $1 ORDER BY (data->>'created_unix')::bigint DESC,snapshot DESC LIMIT $2`
	args := []any{string(filter), limit + 1}
	if cursor.BeforeSnapshot != "" {
		query = `SELECT data FROM factory_priority_judgments WHERE data IS NOT NULL AND data->'candidates' @> $1 AND ((data->>'created_unix')::bigint,snapshot) < ($2,$3) ORDER BY (data->>'created_unix')::bigint DESC,snapshot DESC LIMIT $4`
		args = []any{string(filter), cursor.BeforeUnix, cursor.BeforeSnapshot, limit + 1}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	receipts := []factory.PriorityReceipt{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, false, err
		}
		var receipt factory.PriorityReceipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return nil, false, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(receipts) > limit {
		return receipts[:limit], true, nil
	}
	return receipts, false, nil
}
