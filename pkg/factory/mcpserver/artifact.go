package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
)

type ArtifactWriter interface {
	UploadWorkerArtifact(context.Context, factory.MCPCapability, []byte) (factory.FeedArtifact, string, error)
}

func (s *factoryCatalogServer) serveArtifact(ctx context.Context, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, factory.MCPToolPublishArtifact, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	writer := s.deps.Artifacts
	if writer == nil {
		return nil, nil, errors.New("artifact upload is unavailable")
	}
	raw, err := json.Marshal(args.Payload)
	if err != nil {
		return nil, nil, err
	}
	artifact, postID, err := writer.UploadWorkerArtifact(ctx, cap, raw)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{"artifact_id": artifact.ID, "next_offset": artifact.Size, "complete": postID != "", "post_id": postID}
	if postID != "" {
		out["digest"] = artifact.Digest
		out["artifact"] = artifact
	}
	return mcpJSONResult(out)
}
