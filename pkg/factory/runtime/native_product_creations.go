package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

type nativeCreationFailure struct {
	Status     int     `json:"status"`
	Code       string  `json:"code"`
	RetryAfter *uint64 `json:"retry_after"`
	Reset      *uint64 `json:"reset"`
}
type nativeCreationReply struct {
	CommandID string                 `json:"command_id"`
	Digest    string                 `json:"digest"`
	State     string                 `json:"state"`
	Receipt   json.RawMessage        `json:"receipt"`
	Failure   *nativeCreationFailure `json:"failure"`
}
type nativeCreationResponse struct {
	Protocol       string  `json:"protocol"`
	Status         int     `json:"status"`
	RequestID      *string `json:"request_id"`
	ResponseSHA256 string  `json:"response_sha256"`
}

func (s *nativeProductAccount) callHumanEffect(ctx context.Context, effect any, cmd factory.Command, repository string, conflict error) (nativeCreationReply, error) {
	var reply nativeCreationReply
	scope, err := s.port.call(ctx, effect, &reply)
	if err != nil {
		return reply, err
	}
	if scope.Connection != s.identity.Connection || scope.Repository != repository || reply.CommandID != cmd.ID || reply.Digest != cmd.Digest {
		return reply, conflict
	}
	return reply, nil
}

func (reply nativeCreationReply) applyFailure(record *collaboration.OperationRecord) bool {
	if reply.State != "not_sent" && reply.State != "indeterminate" || string(reply.Receipt) != "null" || reply.Failure == nil || !nativeErrorCode(reply.Failure.Code) || reply.Failure.Status < 400 || reply.Failure.Status > 599 {
		return false
	}
	record.Reason = reply.Failure.Code
	if reply.State == "not_sent" {
		record.Effect, record.Completion = collaboration.EffectNotCommitted, collaboration.CompletionComplete
	}
	return true
}

func (s *nativeProductAccount) humanAuthority() string {
	// Same-account refresh/restart/reconsent preserves command ownership. Each
	// request independently admits current credential generation and repository.
	return nativeHumanAuthority(s.port.operation.config.Profile, s.binding.Host, s.identity.Account.ExternalID)
}

func nativeHumanAuthority(profile collaboration.ProfileID, host collaboration.Host, account string) string {
	if host.Provider == collaboration.Fountain {
		return "native-fountain:" + string(profile) + ":" + host.APIBase + ":" + account
	}
	return "native-github:" + string(profile) + ":" + account
}
func nativeCreationSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func nativeCreationDigest(raw string) bool {
	b, e := hex.DecodeString(raw)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == raw
}
