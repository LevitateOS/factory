package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func (s *nativeProductAccount) humanAuthority() string {
	// Same-account refresh/restart/reconsent preserves command ownership. Each
	// request independently admits current credential generation and repository.
	return "native-github:" + string(s.port.operation.config.Profile) + ":" + s.identity.Account.ExternalID
}
func nativeCreationSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func nativeCreationDigest(raw string) bool {
	b, e := hex.DecodeString(raw)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == raw
}
