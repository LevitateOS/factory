package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/levitateos/factory/pkg/factory"
)

func TestFactoryArtifactChunksPublishOnlyWhenComplete(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	cap := activateMCPCapability(t, ctx, s, testMCPCapabilityRow())
	upload := factory.NewID()
	send := func(offset int, content string, final bool) (factory.FeedArtifact, string, error) {
		raw, _ := json.Marshal(factory.ArtifactUpload{UploadID: upload, Name: "sample.txt", MediaType: "text/plain", Offset: offset, ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)), Final: final})
		return s.UploadWorkerArtifact(ctx, cap, raw)
	}
	partial, postID, err := send(0, "hello ", false)
	if err != nil || postID != "" || partial.ID == "" || partial.Size != 6 {
		t.Fatalf("first: %+v %v", partial, err)
	}
	if _, _, err = s.FeedArtifact(ctx, cap.Repository, partial.ID); err == nil {
		t.Fatal("incomplete upload visible")
	}
	if _, _, err = send(0, "hello ", false); err == nil {
		t.Fatal("old offset was accepted twice")
	}
	if _, _, err = send(5, "wrong", true); err == nil {
		t.Fatal("wrong offset accepted")
	}
	done, postID, err := send(6, "world", true)
	if err != nil || postID == "" {
		t.Fatalf("finish: %+v %v", done, err)
	}
	a, content, err := s.FeedArtifact(ctx, cap.Repository, done.ID)
	if err != nil || !bytes.Equal(content, []byte("hello world")) || a.MediaType != "text/plain" {
		t.Fatalf("content: %+v %q %v", a, content, err)
	}
	if _, _, err = s.FeedArtifact(ctx, cap.Repository+1, done.ID); err == nil {
		t.Fatal("foreign repo artifact visible")
	}
	detail, err := s.FeedPost(ctx, postID)
	if err != nil || len(detail.Payload.Communication.Artifacts) != 1 || detail.Payload.Communication.Artifacts[0].Href != a.Href {
		t.Fatalf("artifact feed: %+v %v", detail, err)
	}
	if _, _, err = send(6, "world", true); err == nil {
		t.Fatal("completed upload accepted more bytes")
	}
	if count, err := s.MCPSubmissionCount(ctx, cap.AssignmentID); err != nil || count != 0 {
		t.Fatal("artifact bytes acquired a duplicate MCP journal record", count, err)
	}
	raw, _ := json.Marshal(map[string]any{"title": "Screenshot", "summary": "Attached output", "completed": []string{"preview"}, "artifacts": []map[string]any{{"id": a.ID, "href": "https://untrusted.invalid/a", "name": "forged"}}})
	sub := testMCPSubmissionRow(cap, factory.MCPToolPartial, raw)
	linked, err := s.RecordWorkerPost(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	detail, err = s.FeedPost(ctx, linked.Record.ID)
	if err != nil || detail.Payload.Communication.Artifacts[0].Href != a.Href || detail.Payload.Communication.Artifacts[0].Name != "sample.txt" {
		t.Fatal("artifact metadata not server-resolved")
	}
}
