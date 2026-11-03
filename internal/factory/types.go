// Package factory owns supervised software-work run records and operator
// command validation. It does not execute containers, persist rows or
// duplicate Forgejo collaboration. ST02 removed the manual attempt admission
// and fixed repair/merge lifecycle; runs stand alone, keyed by execution ID.
package factory

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/levitateos/sodaos/internal/project"
)

type Outcome string

const (
	Succeeded  Outcome = "succeeded"
	Failed     Outcome = "failed"
	Cancelled  Outcome = "cancelled"
	NeedsHuman Outcome = "needs-human"
)

var (
	identifier = regexp.MustCompile(`^[a-f0-9]{32}$`)
	commit     = regexp.MustCompile(`^[a-f0-9]{40}$`)
	role       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

func NewID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw[:])
}
func ValidID(id string) bool     { return identifier.MatchString(id) }
func ValidCommit(id string) bool { return commit.MatchString(id) }

// ValidProjectID reuses the canonical Project identity; factory records never
// invent a second Project DTO.
func ValidProjectID(id string) bool { return project.ValidID(id) }

func validOutcome(outcome Outcome) bool {
	return outcome == "" || outcome == Succeeded || outcome == Failed || outcome == Cancelled || outcome == NeedsHuman
}

// Command types served by the private operator endpoint.
const (
	CommandStatus    = "status"
	CommandStop      = "stop"
	CommandReconcile = "reconcile"
)

// Command is one admitted command. The client generates ID; the coordinator
// records the normalized digest and durable outcome. Same ID with a changed
// payload conflicts instead of executing twice. Its payload carries the
// admitted settings or owner-action fields.
type Command struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Target    string `json:"target,omitempty"`
	Principal string `json:"principal"`
	Digest    string `json:"digest"`
	Payload   string `json:"payload,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Created   string `json:"created,omitempty"`
	Finished  string `json:"finished,omitempty"`
}

func (c Command) Validate() error {
	if !ValidID(c.ID) {
		return errors.New("invalid command identity")
	}
	if !SettingsCommandType(c.Type) && c.Type != CommandHumanComment && c.Type != CommandHumanEdit && c.Type != CommandHumanIssueCreate && c.Type != CommandHumanPullCreate && c.Type != CommandHumanLock && c.Type != CommandHumanState && c.Type != CommandHumanReview && c.Type != CommandHumanInline && c.Type != CommandHumanThread && c.Type != CommandHumanReviewRequest && c.Type != CommandHumanMerge {
		return errors.New("command is not a durable mutation")
	}
	payloadLimit := 8192
	if c.Type == CommandHumanComment {
		payloadLimit = MaxHumanCommentCaptureBytes
	}
	if c.Type == CommandHumanEdit {
		payloadLimit = MaxHumanEditCaptureBytes
	}
	if c.Type == CommandHumanIssueCreate {
		payloadLimit = MaxHumanIssueCreateCaptureBytes
	}
	if c.Type == CommandHumanLock {
		payloadLimit = MaxHumanLockCaptureBytes
	}
	if c.Type == CommandHumanReview {
		payloadLimit = MaxHumanReviewCaptureBytes
	}
	if c.Type == CommandHumanInline {
		payloadLimit = MaxHumanInlineCaptureBytes
	}
	if c.Type == CommandHumanThread {
		payloadLimit = MaxHumanThreadCaptureBytes
	}
	if c.Type == CommandHumanReviewRequest {
		payloadLimit = MaxHumanReviewRequestCaptureBytes
	}
	if c.Type == CommandHumanMerge {
		payloadLimit = MaxHumanMergeCaptureBytes
	}
	if c.Type == CommandHumanPullCreate {
		payloadLimit = MaxHumanPullCreateCaptureBytes
	}
	if c.Target == "" || len(c.Target) > 256 || c.Payload == "" || len(c.Payload) > payloadLimit {
		return errors.New("durable command requires its target and bounded normalized payload")
	}
	if c.Principal == "" || len(c.Principal) > 128 {
		return errors.New("invalid command principal")
	}
	if c.Digest != SettingsDigest(c.Type, c.Target, c.Payload) {
		return errors.New("command digest differs from its payload")
	}
	if len(c.Outcome) > 64<<10 {
		return errors.New("command outcome exceeds retained output limit")
	}
	return nil
}
