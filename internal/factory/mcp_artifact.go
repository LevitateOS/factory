package factory

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

const (
	MaxFeedArtifactBytes = 8 * 1024 * 1024
	MaxFeedArtifactChunk = 32 * 1024
)

type FeedArtifact struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Href      string `json:"href"`
	Size      int    `json:"size"`
	Digest    string `json:"digest,omitempty"`
}
type ArtifactUpload struct {
	UploadID      string `json:"upload_id"`
	Name          string `json:"name"`
	MediaType     string `json:"media_type"`
	Offset        int    `json:"offset"`
	ContentBase64 string `json:"content_base64"`
	Final         bool   `json:"final"`
}

// ArtifactHref binds the existing relative read route to canonical scope.
// It does not admit a reader or grant access to the bytes.
func ArtifactHref(repository int64, id string) string {
	return "/api/factory/artifacts/" + id + "?repository=" + strconv.FormatInt(repository, 10)
}

func (a FeedArtifact) ValidateMetadata() error {
	if !ValidID(a.ID) || strings.TrimSpace(a.Name) == "" || len(a.Name) > 160 || strings.ContainsAny(a.Name, "/\\\r\n\x00") {
		return errors.New("invalid artifact identity or name")
	}
	switch a.MediaType {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "video/mp4", "video/webm", "audio/mpeg", "audio/ogg", "audio/wav", "text/plain", "text/markdown", "application/json", "application/pdf", "application/octet-stream":
		return nil
	default:
		return errors.New("unsupported artifact media type")
	}
}

func DecodeArtifactUpload(raw []byte) (ArtifactUpload, []byte, error) {
	var u ArtifactUpload
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&u); err != nil {
		return u, nil, err
	}
	if u.Offset < 0 || u.Offset > MaxFeedArtifactBytes {
		return u, nil, errors.New("invalid artifact identity, name or offset")
	}
	if err := (FeedArtifact{ID: u.UploadID, Name: u.Name, MediaType: u.MediaType}).ValidateMetadata(); err != nil {
		return u, nil, err
	}
	content, err := base64.StdEncoding.DecodeString(u.ContentBase64)
	if err != nil || len(content) == 0 || len(content) > MaxFeedArtifactChunk || u.Offset+len(content) > MaxFeedArtifactBytes {
		return u, nil, errors.New("artifact chunk must contain 1–32768 bytes; total at most 8 MiB")
	}
	return u, content, nil
}
