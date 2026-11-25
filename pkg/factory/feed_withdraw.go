package factory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Withdrawing a post (slice 5.5). An author may withdraw their own post,
// optionally naming a later post of theirs that corrects it. The withdrawal
// is a new append-only post; the original row is never changed or hidden.
// Reads serve the original with Withdrawn, derived from that record (store
// feedWithdrawalSQL): it stays in the timeline, no longer waits for anyone and
// its question can no longer be answered. An agent withdraws through
// factory.withdraw, its own role's post in the same repository; the person
// through the feed posts endpoint, their own post. Nobody withdraws another
// author's post, and a post is withdrawn at most once.

// FeedWithdrawal is the submitted withdrawal, stored on its record.
type FeedWithdrawal struct {
	Post       string `json:"post"`
	Reason     string `json:"reason"`
	Correction string `json:"correction,omitempty"`
}

// FeedWithdrawn is served on a withdrawn post: who withdrew it, why, the
// correcting post if one was named, and when.
type FeedWithdrawn struct {
	By         string `json:"by"`
	Reason     string `json:"reason"`
	Correction string `json:"correction,omitempty"`
	At         int64  `json:"at"`
}

var (
	// ErrPostWithdrawn refuses a second withdrawal and an answer to a
	// withdrawn question.
	ErrPostWithdrawn = errors.New("the post was withdrawn by its author")
	// ErrWithdrawNotAuthor refuses withdrawing another author's post.
	ErrWithdrawNotAuthor = errors.New("only a post's own author can withdraw it")
	// ErrWithdrawalInvalid refuses a withdrawal whose post or correction does
	// not qualify.
	ErrWithdrawalInvalid = errors.New("invalid withdrawal")
)

// Validate admits one submitted withdrawal's shape. The store checks the
// recorded posts: authorship, order and that the post is not yet withdrawn.
func (w FeedWithdrawal) Validate() error {
	if !postID.MatchString(w.Post) {
		return errors.Join(ErrWithdrawalInvalid, errors.New("withdraw names the post to withdraw"))
	}
	if w.Correction != "" && (!postID.MatchString(w.Correction) || w.Correction == w.Post) {
		return errors.Join(ErrWithdrawalInvalid, errors.New("correction names another, later post"))
	}
	if err := plainText(w.Reason, "withdrawal reason", 400, true); err != nil {
		return errors.Join(ErrWithdrawalInvalid, err)
	}
	return nil
}

// DecodeWithdrawal decodes one factory.withdraw payload: exactly the
// withdrawal object, unknown fields refused.
func DecodeWithdrawal(tool MCPTool, raw []byte) (FeedWithdrawal, error) {
	var w FeedWithdrawal
	if tool != MCPToolWithdraw {
		return w, errors.New("not a withdrawal")
	}
	if len(raw) > MCPMaxCommunicationBytes {
		return w, errors.New("withdrawal exceeds 8 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return w, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return w, errors.New("withdrawal must contain exactly one JSON object")
	}
	return w, w.Validate()
}
