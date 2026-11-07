package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// This is server-retained native request admission, not a GitHub-signed grant
// or a credential. The private product binding captures it; each write separately
// reacquires the captured connection through Rust's authenticated connector.
type nativeIssueIntakeProof struct {
	Version    int                        `json:"version"`
	Profile    collaboration.ProfileID    `json:"profile"`
	Connection collaboration.ConnectionID `json:"connection"`
	Account    string                     `json:"account"`
	Repository string                     `json:"repository"`
	Scope      factory.IssueIntakeScope   `json:"scope"`
}

type nativeIssueEffects struct {
	*factoryservice.CapturedIssueEffects
	NativeInputSource
}

func NewNativeIssueEffects(inputs NativeInputSource) *nativeIssueEffects {
	s := &nativeIssueEffects{NativeInputSource: inputs}
	s.CapturedIssueEffects = &factoryservice.CapturedIssueEffects{Store: inputs.Store, Decode: s.decodeScope, Writer: s}
	return s
}

func (s *nativeIssueEffects) proof(raw json.RawMessage) (nativeIssueIntakeProof, error) {
	var p nativeIssueIntakeProof
	if nativeDecode(raw, &p) != nil || p.Version != 1 || p.Profile != s.Config.Profile || !collaboration.ValidID(string(p.Connection)) || !nativeRemoteID(p.Account) || !nativeRemoteID(p.Repository) || p.Scope.Validate() != nil {
		return p, factoryservice.ErrCapturedIssueAuthority
	}
	return p, nil
}

func (s *nativeIssueEffects) decodeScope(_ context.Context, raw json.RawMessage) (factory.IssueIntakeScope, error) {
	p, err := s.proof(raw)
	return p.Scope, err
}

func (s *nativeIssueEffects) authorize(ctx context.Context, bound *nativeProductAccount, q factoryservice.IssueIntakeRequest) (factory.IssueIntakeScope, json.RawMessage, error) {
	var empty factory.IssueIntakeScope
	if bound.repository == nil || bound.binding.Profile.ID != s.Config.Profile || q.Actor != bound.binding.Actor.ID || q.Repository != bound.repository.ID {
		return empty, nil, factoryservice.ErrCapturedIssueSource
	}
	if err := bound.visible(ctx, q.Actor, q.Repository); err != nil {
		return empty, nil, err
	}
	scope := factory.IssueIntakeScope{Source: q.Source, Revision: q.Revision, Actor: q.Actor, Repository: q.Repository, Digest: q.Digest, Expires: time.Now().Add(5 * time.Minute).Unix()}
	if scope.Validate() != nil {
		return empty, nil, factoryservice.ErrCapturedIssueAuthority
	}
	raw, err := json.Marshal(nativeIssueIntakeProof{Version: 1, Profile: s.Config.Profile, Connection: bound.binding.Connection.ID, Account: bound.identity.Account.ExternalID, Repository: bound.repository.ExternalID, Scope: scope})
	return scope, raw, err
}

func (s *nativeIssueEffects) currentSource(ctx context.Context, p nativeIssueIntakeProof, raw json.RawMessage) error {
	saved, err := s.Store.IssueIntakeGrant(ctx, p.Scope.Source, p.Scope.Revision)
	if err != nil {
		return err
	}
	post, err := s.Store.FeedPost(ctx, p.Scope.Source)
	if err != nil {
		return err
	}
	if !bytes.Equal(saved, raw) || p.Scope.Expires <= time.Now().Unix() || post.Record.Deleted || post.Record.Revoked || post.Record.Revision != p.Scope.Revision || post.Record.Repository != int64(p.Scope.Repository) || post.Record.Author != strconv.FormatInt(int64(p.Scope.Actor), 10) || factory.MCPInputDigest([]byte(post.Body)) != p.Scope.Digest {
		return factoryservice.ErrCapturedIssueSource
	}
	return nil
}

func (s *nativeIssueEffects) ApplyIssueIntake(ctx context.Context, raw json.RawMessage, mutation factory.IssueMakerMutation) (factory.IssueMakerReceipt, error) {
	var empty factory.IssueMakerReceipt
	p, err := s.proof(raw)
	if err != nil {
		return empty, err
	}
	if mutation.Validate() != nil {
		return empty, factoryservice.ErrCapturedIssueUnavailable
	}
	if err := s.currentSource(ctx, p, raw); err != nil {
		return empty, err
	}
	account, err := s.savedAccount(ctx, int64(p.Scope.Repository), p.Scope.Actor, p.Connection)
	if err != nil {
		return empty, err
	}
	if account.identity.Account.ExternalID != p.Account || account.repository.ExternalID != p.Repository {
		return empty, factoryservice.ErrCapturedIssueAuthority
	}
	if mutation.Action == "refine" {
		return s.refineIssue(ctx, p, raw, account, mutation)
	}
	request := s.createRequest(p, account, mutation.Slot)
	request.Title, request.Body = mutation.Title, mutation.Body
	writer := nativeHumanIssueCreates{account}
	_, err = s.Store.HumanIssueCreateCapture(ctx, request)
	if errors.Is(err, store.ErrNotFound) {
		capture, captureErr := writer.PrepareHumanIssueCreate(ctx, request)
		if captureErr != nil {
			return empty, captureErr
		}
		var preparation nativeIssueCreatePreparation
		if nativeDecode(capture.ProviderIntent, &preparation) != nil {
			return empty, factoryservice.ErrCapturedIssueAuthority
		}
		preparation.NotAfter = min(preparation.NotAfter, p.Scope.Expires)
		capture.ProviderIntent, err = json.Marshal(preparation)
		if err == nil {
			_, _, err = s.Store.CaptureHumanIssueCreate(ctx, capture)
		}
	}
	if err != nil {
		return empty, err
	}
	if err := s.currentSource(ctx, p, raw); err != nil {
		return empty, err
	}
	commands := factoryservice.HumanIssueCreates{Store: s.Store, Writer: writer}
	outcome, err := commands.Submit(ctx, request)
	if err != nil {
		return empty, err
	}
	if outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted {
		return empty, factoryservice.ErrPostEffectUnknown
	}
	request.Title, request.Body = "", ""
	issue, err := commands.CreatedIssue(ctx, request, outcome)
	if err != nil {
		return empty, err
	}
	return factory.IssueMakerReceipt{Issue: issue, Slot: mutation.Slot, Title: mutation.Title, BodyDigest: factory.MCPInputDigest([]byte(mutation.Body))}, nil
}

func (s *nativeIssueEffects) createRequest(p nativeIssueIntakeProof, account *nativeProductAccount, slot int) factory.HumanIssueCreateCommand {
	key := sha256.Sum256([]byte("factory.native.issue-intake\x00" + string(p.Profile) + "\x00" + p.Scope.Source + "\x00" + strconv.Itoa(slot)))
	return factory.HumanIssueCreateCommand{ID: hex.EncodeToString(key[:16]), Host: account.binding.Host.ID, Repository: p.Scope.Repository, Actor: p.Scope.Actor}
}
