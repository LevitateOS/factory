package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coder/websocket"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/strictjson"
)

// OutputHandshake selects an exact run and cursor within an admitted attachment.
// RepositoryID and SessionGeneration are host wire labels, not authority or a
// canonical identity mapping. Hosts validate them against their bound account.
type OutputHandshake struct {
	RunID             string `json:"run_id"`
	RepositoryID      string `json:"repository_id"`
	SessionGeneration string `json:"session_generation"`
	Cursor            int64  `json:"cursor"`
}

func readOutputHandshake(ctx context.Context, conn *websocket.Conn) (OutputHandshake, error) {
	var in OutputHandshake
	first, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	kind, body, err := conn.Read(first)
	if err != nil {
		return in, err
	}
	if kind != websocket.MessageText || len(body) > 4096 || strictjson.Decode(bytes.NewReader(body), &in) != nil {
		return in, errors.New("invalid handshake")
	}
	return in, nil
}

func writeOutputFrame(ctx context.Context, conn *websocket.Conn, frame any) error {
	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	write, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	return conn.Write(write, websocket.MessageText, body)
}

func refuseOutput(conn *websocket.Conn) {
	_ = conn.Close(websocket.StatusPolicyViolation, "factory view unavailable or authorization failed; use the exact run or reload this page")
}

// rejectOutputInput ends a read-only attachment the moment the viewer
// sends anything past the handshake: input, resize, signals and End frames
// cannot reach the factory process.
func rejectOutputInput(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "read-only factory stream; input, resize and End are rejected")
	}
}

// outputEmit writes one frame to the viewer's transport.
type outputEmit func(context.Context, any) error

func (s Output) pump(ctx context.Context, conn *websocket.Conn, cursor int64) {
	emit := func(ctx context.Context, frame any) error { return writeOutputFrame(ctx, conn, frame) }
	beat := func(ctx context.Context) bool {
		if !s.heartbeat(ctx, emit) {
			return false
		}
		check, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		return conn.Ping(check) == nil
	}
	pumpOutput(ctx, s.Attachment, s.Current, emit, beat, cursor, nil)
}

// pumpOutput is the one read loop over an admitted attachment for every
// transport. It ends on EOF, refusal, ctx or `end`; it never sends input.
func pumpOutput(ctx context.Context, attachment *factoryservice.OutputAttachment, current func(context.Context) bool,
	emit outputEmit, beat func(context.Context) bool, cursor int64, end <-chan time.Time) {
	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()
	heart := time.NewTicker(15 * time.Second)
	defer heart.Stop()
	var last factoryservice.RunOutputStatus
	sent := false
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-end:
			_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: "renew"})
			return
		case <-heart.C:
			if !beat(ctx) {
				return
			}
		case <-poll.C:
			next, done, ok := sliceOutput(ctx, attachment, current, emit, cursor, &last, &sent)
			cursor = next
			if done {
				return
			}
			if !ok {
				failures++
				if failures > 10 {
					_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: "output_unavailable"})
					return
				}
				continue
			}
			failures = 0
		}
	}
}

func sliceOutput(ctx context.Context, attachment *factoryservice.OutputAttachment, current func(context.Context) bool, emit outputEmit, cursor int64, last *factoryservice.RunOutputStatus, sent *bool) (int64, bool, bool) {
	if !current(ctx) {
		_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
		return cursor, true, false
	}
	out, err := attachment.Read(ctx, cursor)
	if err != nil {
		return cursor, closeOutputSlice(ctx, emit, err), false
	}
	if !current(ctx) {
		_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
		return cursor, true, false
	}
	status := out.Status
	if !*sent || !status.Same(*last) {
		if emit(ctx, status) != nil {
			return cursor, true, false
		}
		*last, *sent = status, true
	}
	if out.Output != nil {
		if emit(ctx, out.Output) != nil {
			return cursor, true, false
		}
	}
	if out.Done {
		_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: "eof"})
		return out.Next, true, true
	}
	return out.Next, false, true
}

// closeOutputSlice ends the attachment for a host read failure. Unknown
// runs and changed incarnations close with their reason; anything else is
// a transient miss the pump retries within its failure budget.
func closeOutputSlice(ctx context.Context, emit outputEmit, err error) bool {
	reason := ""
	switch {
	case errors.Is(err, factoryservice.ErrRunOutputMissing):
		reason = "run_unknown"
	case errors.Is(err, factoryservice.ErrRunOutputChanged):
		reason = "incarnation_changed"
	case errors.Is(err, factoryservice.ErrRunOutputAccess):
		reason = "authority_lost"
	case errors.Is(err, factoryservice.ErrRunOutputInvalid), errors.Is(err, factoryservice.ErrRunOutputCursor):
		reason = "output_unavailable"
	default:
		return false
	}
	_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: reason})
	return true
}

func (s Output) heartbeat(ctx context.Context, emit outputEmit) bool {
	check, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	if !s.Current(check) || s.Attachment.Check(check) != nil {
		_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
		return false
	}
	return true
}
