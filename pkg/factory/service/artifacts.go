package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// ArtifactSource returns only a complete artifact from the exact canonical
// repository. The store's upload/completion journal remains authoritative.
type ArtifactSource interface {
	FeedArtifact(context.Context, int64, string) (factory.FeedArtifact, []byte, error)
}

type Artifacts struct {
	Source     ArtifactSource
	Visibility WorkVisibility
}

type ArtifactRead struct {
	Artifact factory.FeedArtifact
	Content  []byte `json:"-"`
}

var (
	ErrArtifactUnavailable = errors.New("artifact reader is unavailable")
	ErrArtifactInvalid     = errors.New("invalid artifact identity or recorded content")
	ErrArtifactNotFound    = errors.New("artifact is unavailable")
	ErrArtifactVisibility  = errors.New("current artifact visibility is required")
)

func (s Artifacts) visible(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if actor < 1 || repository < 1 {
		return ErrArtifactInvalid
	}
	if s.Source == nil || s.Visibility == nil {
		return ErrArtifactUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Visibility(ctx, actor, repository); err != nil {
		return errors.Join(ErrArtifactVisibility, err)
	}
	return ctx.Err()
}

// Read returns verified retained bytes after current authority before/after I/O.
// It does not launch, fetch files from a runtime, complete an upload or publish.
func (s Artifacts) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, id string) (ArtifactRead, error) {
	var empty ArtifactRead
	if !factory.ValidID(id) {
		return empty, ErrArtifactInvalid
	}
	if err := s.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	artifact, data, err := s.Source.FeedArtifact(ctx, int64(repository), id)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrArtifactNotFound
	}
	if err != nil {
		return empty, err
	}
	if artifact.ID != id || artifact.Href != factory.ArtifactHref(int64(repository), id) || artifact.ValidateMetadata() != nil || len(data) == 0 || len(data) > factory.MaxFeedArtifactBytes || artifact.Size != len(data) || !factory.ValidDigest(artifact.Digest) {
		return empty, ErrArtifactInvalid
	}
	// Own the bytes checked below; an adapter may reuse its returned buffer.
	content := bytes.Clone(data)
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != artifact.Digest {
		return empty, ErrArtifactInvalid
	}
	if err := s.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	return ArtifactRead{Artifact: artifact, Content: content}, nil
}
