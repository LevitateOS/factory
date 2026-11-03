package factory

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// FeedPostRetryCommand is one admitted author's exact retry intent. It is not
// authority: the serving caller must check current repository/author access.
type FeedPostRetryCommand struct {
	ID               string
	Post             string
	Repository       int64
	Author           string
	ExpectedRevision int64
	Body             string
}

// FeedPostRetryRequest carries client intent; the server supplies author and post
// scope only after current admission. Repository stays a canonical decimal string.
type FeedPostRetryRequest struct {
	CommandID        string `json:"command_id"`
	Repository       int64  `json:"repository,string"`
	ExpectedRevision int64  `json:"expected_revision"`
	Body             string `json:"body"`
}

type feedPostRetryPayload struct {
	Post       string `json:"post_id"`
	Repository string `json:"repository"`
	Author     string `json:"author"`
	Revision   int64  `json:"expected_revision"`
	BodyDigest string `json:"body_digest"`
}

func (c FeedPostRetryCommand) Validate() error {
	actor, err := strconv.ParseInt(c.Author, 10, 64)
	if !ValidID(c.ID) || !ValidID(c.Post) || c.Repository <= 0 || err != nil || actor <= 0 || strconv.FormatInt(actor, 10) != c.Author || c.ExpectedRevision < 0 || c.ExpectedRevision == math.MaxInt64 {
		return errors.New("invalid post retry command scope")
	}
	// Empty retains the existing store API's unchanged-body shorthand.
	if c.Body != "" {
		return (PostRequest{Repository: c.Repository, Body: c.Body, IdempotencyKey: "retry"}).Validate()
	}
	return nil
}

// Command binds all semantic inputs, using a body digest so maximum-size and
// control-character writing cannot exceed the shared command payload bound.
func (c FeedPostRetryCommand) Command() (Command, error) {
	if err := c.Validate(); err != nil {
		return Command{}, err
	}
	payload, err := json.Marshal(feedPostRetryPayload{c.Post, strconv.FormatInt(c.Repository, 10), c.Author, c.ExpectedRevision, MCPInputDigest([]byte(c.Body))})
	if err != nil {
		return Command{}, err
	}
	cmd := Command{ID: c.ID, Type: CommandFeedPostRetry, Target: "post/" + c.Post, Principal: "actor/" + c.Author, Payload: string(payload)}
	cmd.Digest = SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, cmd.Validate()
}

// FeedPostRetryReceipt records only the atomic post/intake transition. It never
// claims proof capture, planning, publication or external work has completed.
type FeedPostRetryReceipt struct {
	CommandID        string `json:"command_id"`
	Post             string `json:"post_id"`
	Repository       string `json:"repository"`
	Author           string `json:"author"`
	ExpectedRevision int64  `json:"expected_revision"`
	Revision         int64  `json:"revision"`
	BodyDigest       string `json:"body_digest"`
}

func (r FeedPostRetryReceipt) Validate(c FeedPostRetryCommand) error {
	if c.Validate() != nil || r.CommandID != c.ID || r.Post != c.Post || r.Repository != strconv.FormatInt(c.Repository, 10) || r.Author != c.Author || r.ExpectedRevision != c.ExpectedRevision || (r.Revision != c.ExpectedRevision && r.Revision != c.ExpectedRevision+1) || !ValidDigest(r.BodyDigest) || (c.Body != "" && r.BodyDigest != MCPInputDigest([]byte(c.Body))) {
		return errors.New("post retry receipt does not match its command")
	}
	return nil
}

// ReadFeedPostRetryReceipt checks a retained ledger result against already
// admitted scope without reconstructing plaintext writing or executing a retry.
func ReadFeedPostRetryReceipt(command Command, post string, repository int64, author string) (FeedPostRetryReceipt, error) {
	var receipt FeedPostRetryReceipt
	var payload feedPostRetryPayload
	if err := command.Validate(); err != nil {
		return receipt, err
	}
	if command.Type != CommandFeedPostRetry || command.Target != "post/"+post || command.Principal != "actor/"+author || command.Finished == "" || json.Unmarshal([]byte(command.Payload), &payload) != nil || payload.Post != post || payload.Repository != strconv.FormatInt(repository, 10) || payload.Author != author || !ValidDigest(payload.BodyDigest) {
		return receipt, errors.New("post retry receipt is not committed in this scope")
	}
	if err := json.Unmarshal([]byte(command.Outcome), &receipt); err != nil {
		return receipt, err
	}
	request := FeedPostRetryCommand{ID: command.ID, Post: post, Repository: repository, Author: author, ExpectedRevision: payload.Revision}
	if err := receipt.Validate(request); err != nil {
		return receipt, err
	}
	if payload.BodyDigest != MCPInputDigest(nil) && payload.BodyDigest != receipt.BodyDigest {
		return receipt, errors.New("post retry receipt body does not match its command")
	}
	return receipt, nil
}
