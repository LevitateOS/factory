package factory

import (
	"errors"
	"strconv"
)

// IssueMakerScope binds the intake role to a single submitted request. It is
// recorded in the planning input, rather than inferred from a role label.
type IssueMakerScope struct {
	SourcePost string `json:"source_post"`
	Revision   int64  `json:"revision"`
	Post       string `json:"post"`
	Issue      int64  `json:"issue"`
}

func (s IssueMakerScope) Validate(head IntentRevision) error {
	if !ValidID(s.SourcePost) || s.Revision < 0 || s.Post != IssueMakerPostID(s.SourcePost, s.Revision) || s.Issue <= 0 ||
		head.Intent != PostDecisionID("intent", s.SourcePost, s.Revision) ||
		head.ID != PostDecisionID("revision", s.SourcePost, s.Revision) {
		return errors.New("issue maker input does not bind its source request")
	}
	return nil
}

// IssueMakerProgress is the durable state of the role's own feed post. Native
// issue receipts and the comments presenting them are committed together.
type IssueMakerProgress struct {
	Scope   IssueMakerScope   `json:"scope"`
	State   string            `json:"state"`
	Detail  string            `json:"detail"`
	Checked int64             `json:"checked"`
	Run     string            `json:"run,omitempty"`
	Issues  []IssueMakerIssue `json:"issues,omitempty"`
}

func (p IssueMakerProgress) Validate() error {
	if !ValidID(p.Scope.SourcePost) || p.Scope.Revision < 0 || p.Scope.Post != IssueMakerPostID(p.Scope.SourcePost, p.Scope.Revision) || p.Scope.Issue < 0 || p.Run != "" && !ValidID(p.Run) {
		return errors.New("invalid Issue Maker progress identity")
	}
	switch p.State {
	case "queued", "creating", "waiting", "running", "settling", "unknown", "completed", "failed":
		return nil
	default:
		return errors.New("unknown Issue Maker state")
	}
}

type IssueMakerIssue struct {
	Slot       int    `json:"slot"`
	Key        string `json:"key"`
	Index      int64  `json:"index"`
	Title      string `json:"title"`
	BodyDigest string `json:"body_digest"`
}

func IssueMakerPostID(comment string, revision int64) string {
	return MCPInputDigest([]byte("issue-maker/post/" + comment + "/" + strconv.FormatInt(revision, 10)))[:32]
}

func IssueMakerReplyID(post, key string) string {
	return MCPInputDigest([]byte("issue-maker/issue/" + post + "/" + key))[:32]
}
