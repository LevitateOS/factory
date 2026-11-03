package factory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Post resolution (R32.2/R32.3). Suggestions and answers are immutable
// feed posts; their disposition lives beside them, not inside them.
// resolve_post records exactly one disposition per post: a suggestion
// becomes applied or refused, an answer becomes incorporated when a
// worker adopts it. Anything else — questions, status signals,
// knowledge records — refuses, and a second disposition for the same
// post refuses so applied/refused/incorporated states survive
// exactly as recorded. Reads attach the recorded disposition; the
// pending state is the absence of one.
const (
	PostResolutionApplied      = "applied"
	PostResolutionRefused      = "refused"
	PostResolutionIncorporated = "incorporated"
)

// PostResolution is one resolve_post batch: the target post, its
// disposition and the reason behind it. A refusal must explain
// itself; the reason stays bounded either way.
type PostResolution struct {
	Post       string `json:"post"`
	Resolution string `json:"resolution"`
	Reason     string `json:"reason,omitempty"`
}

// DecodePostResolution decodes one resolve_post payload. Unknown
// fields refuse; the disposition value and reason bound check here,
// while the target kind match and uniqueness belong to the store,
// which sees the recorded posts.
func DecodePostResolution(tool MCPTool, raw []byte) (PostResolution, error) {
	var r PostResolution
	if tool != MCPToolResolvePost {
		return r, errors.New("unknown resolution operation")
	}
	if len(raw) > MCPMaxSubmissionPayload {
		return r, errors.New("resolution exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return r, errors.New("resolution must contain exactly one JSON object")
	}
	if !ValidID(r.Post) {
		return r, errors.New("resolution lacks its post")
	}
	switch r.Resolution {
	case PostResolutionApplied, PostResolutionRefused, PostResolutionIncorporated:
	default:
		return r, errors.New("unknown post resolution")
	}
	if len(r.Reason) > MCPMaxReasonChars {
		return r, errors.New("resolution reason outside its bound")
	}
	if r.Resolution == PostResolutionRefused && strings.TrimSpace(r.Reason) == "" {
		return r, errors.New("refused resolution must explain itself")
	}
	return r, nil
}

// ResolutionForPost validates one disposition against its target
// kind: suggestions apply or refuse, answers incorporate. Questions
// answer through replies and status signals speak for themselves;
// neither resolves.
func ResolutionForPost(kind, resolution string) error {
	switch kind {
	case "suggest":
		if resolution == PostResolutionApplied || resolution == PostResolutionRefused {
			return nil
		}
		return errors.New("suggestions only apply or refuse")
	case "answer":
		if resolution == PostResolutionIncorporated {
			return nil
		}
		return errors.New("answers only incorporate")
	default:
		return errors.New("only suggestions and answers resolve")
	}
}
