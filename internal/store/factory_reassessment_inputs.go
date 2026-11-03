package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// CompletionIntentRevision resolves the immutable accepted revision
// already exported in a sealed completion inventory. It supplies the
// owning controller's full revision for successor CAS; no private head
// or unaccepted draft is selected by this read.
func (s *Store) CompletionIntentRevision(ctx context.Context, scope factory.CompletionIntentScope) (factory.IntentRevision, error) {
	var revision factory.IntentRevision
	if err := scope.Validate(); err != nil {
		return revision, err
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_intent_revisions WHERE id=$1 AND intent=$2`, scope.RevisionID, scope.IntentID).Scan(&raw); err != nil {
		return revision, err
	}
	if err := json.Unmarshal(raw, &revision); err != nil {
		return revision, err
	}
	if err := revision.Validate(); err != nil {
		return revision, err
	}
	if revision.State != "accepted" || revision.Repository != scope.Repository || revision.Digest() != scope.RevisionDigest {
		return factory.IntentRevision{}, errors.New("completion revision differs from its sealed accepted scope")
	}
	return revision, nil
}
