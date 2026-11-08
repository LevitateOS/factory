package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// IssueIntakeRequest names the exact current source for a trusted issuer. It
// contains no provider bearer, session, expiry or execution/spending authority.
type IssueIntakeRequest struct {
	Source     string
	Revision   int64
	Actor      collaboration.ActorID
	Repository collaboration.RepositoryID
	Digest     string
}

// IssueIntakeCapture records provider-issued post authority in the existing
// intake journal. The host authenticates issuance and decodes the same proof;
// neither caller JSON nor stored metadata can issue authority by itself.
type IssueIntakeCapture struct {
	Posts     Posts
	Effects   *CapturedIssueEffects
	Authorize func(context.Context, IssueIntakeRequest) (factory.IssueIntakeScope, json.RawMessage, error)
}

func (s IssueIntakeCapture) source(ctx context.Context, actor collaboration.ActorID, id string, revision int64) (IssueIntakeRequest, error) {
	var empty IssueIntakeRequest
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	post, err := s.Posts.Store.FeedPost(ctx, id)
	if err != nil {
		return empty, err
	}
	if post.Record.ID != id || post.Record.Revision != revision || post.Record.Author != strconv.FormatInt(int64(actor), 10) || post.Record.Deleted || post.Record.Revoked {
		return empty, ErrCapturedIssueSource
	}
	if err := s.Posts.visible(ctx, actor, post.Record.Repository); err != nil {
		return empty, err
	}
	captured, err := s.Posts.Store.PostUsesIssueMaker(ctx, id, revision)
	if err != nil {
		return empty, err
	}
	if !captured {
		return empty, ErrCapturedIssueSource
	}
	return IssueIntakeRequest{Source: id, Revision: revision, Actor: actor, Repository: collaboration.RepositoryID(post.Record.Repository), Digest: factory.MCPInputDigest([]byte(post.Body))}, ctx.Err()
}

// Capture reuses a still-valid retained proof or issues one for this exact
// source. Unknown reads/decoding never imply absence or trigger new issuance.
// Proof bytes stay server-side; no issue mutation or worker/model is launched.
func (s IssueIntakeCapture) Capture(ctx context.Context, actor collaboration.ActorID, id string, revision int64) error {
	if s.Posts.Store == nil || s.Posts.Visibility == nil || s.Effects == nil || s.Effects.Store != s.Posts.Store || s.Effects.Decode == nil {
		return ErrCapturedIssueUnavailable
	}
	if actor <= 0 || !factory.ValidID(id) || revision < 0 {
		return ErrCapturedIssueSource
	}
	request, err := s.source(ctx, actor, id, revision)
	if err != nil {
		return err
	}
	if _, _, err := s.Effects.Grant(ctx, id, revision); err == nil {
		// Current permission may have changed during the retained proof read.
		if err := s.Posts.visible(ctx, actor, int64(request.Repository)); err != nil {
			return err
		}
		return ctx.Err()
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if s.Authorize == nil {
		return ErrCapturedIssueUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	issued, proof, err := s.Authorize(ctx, request)
	if err != nil {
		return err
	}
	if issued.Validate() != nil || issued.Source != request.Source || issued.Revision != request.Revision || issued.Actor != request.Actor || issued.Repository != request.Repository || issued.Digest != request.Digest || issued.Expires <= time.Now().Unix() || len(proof) == 0 || len(proof) > 8192 || !json.Valid(proof) {
		return ErrCapturedIssueAuthority
	}
	decoded, err := s.Effects.Decode(ctx, bytes.Clone(proof))
	if err != nil {
		return err
	}
	if decoded != issued {
		return ErrCapturedIssueAuthority
	}
	// Issuance can outlive a source edit, withdrawal or account switch. Re-read
	// the exact source and current resource permission before the store's CAS.
	current, err := s.source(ctx, actor, id, revision)
	if err != nil {
		return err
	}
	if current != request {
		return ErrCapturedIssueSource
	}
	if issued.Expires <= time.Now().Unix() {
		return ErrCapturedIssueAuthority
	}
	return s.Posts.Store.SaveIssueIntakeGrant(ctx, id, revision, issued, proof)
}
