package runtime

import (
	"context"
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

type nativeWriteOutcomeEffect struct {
	Kind      string `json:"kind"`
	CommandID string `json:"command_id"`
	Digest    string `json:"digest"`
	NotAfter  int64  `json:"not_after"`
}

// Recovery reads only the original native causal outcome. Its absence leaves
// the existing product reservation uncertain; no recovery path dispatches.
func (s *nativeProductAccount) recoverHumanEffect(ctx context.Context, cmd factory.Command, notAfter int64, repository string, prior collaboration.OperationLookup, adopt func(nativeCreationReply) (collaboration.OperationRecord, error), observe func(collaboration.OperationLookup) (collaboration.OperationLookup, error)) (collaboration.OperationLookup, error) {
	if prior.Record == nil || prior.Record.Effect != collaboration.EffectIndeterminate || prior.Record.Receipt != nil {
		return prior, nil
	}
	var raw json.RawMessage
	scope, err := s.port.call(ctx, nativeWriteOutcomeEffect{Kind: "write_outcome", CommandID: cmd.ID, Digest: cmd.Digest, NotAfter: notAfter}, &raw)
	if err != nil {
		return prior, err
	}
	if scope.Connection != s.identity.Connection || scope.Repository != repository {
		return prior, errNativeReadScope
	}
	if string(raw) == "null" {
		return prior, nil
	}
	var reply nativeCreationReply
	if nativeDecode(raw, &reply) != nil || reply.CommandID != cmd.ID || reply.Digest != cmd.Digest {
		return prior, errNativeRead
	}
	record, err := adopt(reply)
	if err != nil {
		return prior, err
	}
	result := prior
	result.Record = &record
	result.Reason = record.Reason
	return observe(result)
}
