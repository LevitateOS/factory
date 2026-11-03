package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Typed task-event stream bounds (D05). The per-run journal is bounded;
// the read path pages it in whole-event responses under the existing
// 64 KiB response cap.
const (
	// MaxTaskEventBytes bounds one encoded event.
	MaxTaskEventBytes = 16 * 1024
	// MaxTaskEventsPerRead bounds one paged response.
	MaxTaskEventsPerRead = 64
	// MinTaskEventsBytes and MaxTaskEventsBytes bound one read request.
	MinTaskEventsBytes = 1
	MaxTaskEventsBytes = 24*1024 - 256
	// MaxTaskJournalEvents and MaxTaskJournalBytes bound the per-run
	// journal, reserving two terminal events. Exhaustion stops admission
	// with a terminal reason; events are never dropped.
	MaxTaskJournalEvents = 512
	MaxTaskJournalBytes  = 8 << 20
)

// Task event types. Ordinary prose never advances task state; only these
// typed observations do.
const (
	TaskEventChildStartIntent = "child_start_intent"
	TaskEventChildStarted     = "child_started"
	TaskEventChildReported    = "child_reported"
	TaskEventParentStarted    = "parent_task_started"
	TaskEventParentOutput     = "parent_output_observed"
	TaskEventCheckpointReady  = "checkpoint_prepared"
	TaskEventTaskCommitted    = "task_committed"
	TaskEventTaskFailed       = "task_failed"
	TaskEventRunStopped       = "run_stopped"
)

// ValidTaskEventType reports whether event type is a typed task observation.
func ValidTaskEventType(eventType string) bool {
	switch eventType {
	case TaskEventChildStartIntent, TaskEventChildStarted,
		TaskEventChildReported, TaskEventParentStarted, TaskEventParentOutput, TaskEventCheckpointReady,
		TaskEventTaskCommitted, TaskEventTaskFailed,
		TaskEventRunStopped:
		return true
	default:
		return false
	}
}

// TaskEvent is one typed native observation inside an admitted parent run.
// Sequence numbers start at 1 and advance contiguously; PrevDigest chains
// each event to its predecessor (all zeroes for sequence 1) so replay is
// verifiable and gaps refuse. TaskKey names the plan task; ChildID names
// the native child for child-scoped types and stays empty otherwise.
// Detail carries the bounded typed payload (exit code, output digest,
// check/tree/commit evidence or stop reason); it is never prose.
type TaskEvent struct {
	Sequence   int64  `json:"sequence"`
	PrevDigest string `json:"prev_digest"`
	Type       string `json:"type"`
	TaskKey    string `json:"task_key"`
	ChildID    string `json:"child_id,omitempty"`
	Invocation string `json:"invocation"`
	PlanDigest string `json:"plan_digest"`
	Detail     string `json:"detail,omitempty"`
}

// TaskEventDigest returns the chain digest of one event's canonical
// encoding. The executor records it; readers recompute it to verify the
// journal instead of trusting stored hashes.
func TaskEventDigest(event TaskEvent) string {
	canonical, err := json.Marshal(struct {
		Sequence   int64  `json:"sequence"`
		PrevDigest string `json:"prev_digest"`
		Type       string `json:"type"`
		TaskKey    string `json:"task_key"`
		ChildID    string `json:"child_id,omitempty"`
		Invocation string `json:"invocation"`
		PlanDigest string `json:"plan_digest"`
		Detail     string `json:"detail,omitempty"`
	}{
		Sequence: event.Sequence, PrevDigest: event.PrevDigest,
		Type: event.Type, TaskKey: event.TaskKey, ChildID: event.ChildID,
		Invocation: event.Invocation, PlanDigest: event.PlanDigest,
		Detail: event.Detail,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// Validate checks one event's shape. Chain continuity (sequence and
// PrevDigest against the journal head) is enforced at append, not here.
func (e TaskEvent) validateChain() error {
	if e.Sequence < 1 {
		return errors.New("task event sequence is invalid")
	}
	if e.PrevDigest != "" && !ValidDigest(e.PrevDigest) {
		return errors.New("task event chain digest is invalid")
	}
	if (e.Sequence == 1) != (e.PrevDigest == "") {
		return errors.New("task event chain origin is invalid")
	}
	return nil
}

func validTaskKeyRune(c byte, first bool) bool {
	if first {
		return c >= 'a' && c <= 'z'
	}
	return c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func (e TaskEvent) validateTaskKey() error {
	if len(e.TaskKey) == 0 || len(e.TaskKey) > 32 {
		return errors.New("task event task key is invalid")
	}
	for i := 0; i < len(e.TaskKey); i++ {
		if !validTaskKeyRune(e.TaskKey[i], i == 0) {
			return errors.New("task event task key is invalid")
		}
	}
	return nil
}

func (e TaskEvent) validatePayload() error {
	if e.ChildID != "" && !validNativeTaskChild(e.ChildID) {
		return errors.New("task event child identity is invalid")
	}
	if !ValidInvocation(e.Invocation) {
		return errors.New("task event invocation is invalid")
	}
	if !ValidDigest(e.PlanDigest) {
		return errors.New("task event plan digest is invalid")
	}
	if len(e.Detail) > MaxTaskEventBytes {
		return errors.New("task event detail exceeds its bound")
	}
	return nil
}

func validNativeTaskChild(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range []byte(id) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':' {
			continue
		}
		return false
	}
	return true
}

func (e TaskEvent) Validate() error {
	if err := e.validateChain(); err != nil {
		return err
	}
	if !ValidTaskEventType(e.Type) {
		return errors.New("unknown task event type")
	}
	if err := e.validateTaskKey(); err != nil {
		return err
	}
	return e.validatePayload()
}

// FactoryTaskEvents pages one admitted run's typed task journal. It is
// read-only: polling it never mutates checkpoints. AfterSequence is the
// last sequence the caller holds (0 for the head); ByteLimit bounds the
// response body.
type FactoryTaskEvents struct {
	Project       string `json:"project"`
	ID            string `json:"id"`
	AfterSequence int64  `json:"after_sequence"`
	ByteLimit     int64  `json:"byte_limit"`
}

func (r FactoryTaskEvents) Validate() error {
	if !ValidID(r.Project) || !ValidFactoryRunID(r.ID) {
		return errors.New("invalid factory run address")
	}
	if r.AfterSequence < 0 {
		return errors.New("invalid task events cursor")
	}
	if r.ByteLimit < MinTaskEventsBytes || r.ByteLimit > MaxTaskEventsBytes {
		return errors.New("invalid task events read bound")
	}
	return nil
}

// FactoryTaskEventsState is one paged journal response: whole events only,
// NextSequence addressing the first undelivered event, and Terminal set
// once run_stopped is recorded. A response too small for its next event
// is a bounded size error without advancing the cursor.
type FactoryTaskEventsState struct {
	ID           string      `json:"id"`
	Project      string      `json:"project"`
	NextSequence int64       `json:"next_sequence"`
	Terminal     bool        `json:"terminal"`
	Events       []TaskEvent `json:"events"`
}

func (s FactoryTaskEventsState) Validate() error {
	if !ValidID(s.Project) || !ValidFactoryRunID(s.ID) {
		return errors.New("invalid factory run address")
	}
	if s.NextSequence < 1 {
		return errors.New("invalid task events cursor")
	}
	if len(s.Events) > MaxTaskEventsPerRead {
		return errors.New("task events response exceeds its count bound")
	}
	for _, event := range s.Events {
		if err := event.Validate(); err != nil {
			return err
		}
	}
	return nil
}
