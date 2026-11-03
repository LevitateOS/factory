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

func (s Output) pump(ctx context.Context, conn *websocket.Conn, cursor int64) {
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
		case <-heart.C:
			if !s.heartbeat(ctx, conn) {
				return
			}
		case <-poll.C:
			next, done, ok := s.slice(ctx, conn, cursor, &last, &sent)
			cursor = next
			if done {
				return
			}
			if !ok {
				failures++
				if failures > 10 {
					_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "output_unavailable"})
					return
				}
				continue
			}
			failures = 0
		}
	}
}

func (s Output) slice(ctx context.Context, conn *websocket.Conn, cursor int64, last *factoryservice.RunOutputStatus, sent *bool) (int64, bool, bool) {
	if !s.Current(ctx) {
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
		return cursor, true, false
	}
	out, err := s.Attachment.Read(ctx, cursor)
	if err != nil {
		return cursor, closeOutputSlice(ctx, conn, err), false
	}
	if !s.Current(ctx) {
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
		return cursor, true, false
	}
	status := out.Status
	if !*sent || !status.Same(*last) {
		if writeOutputFrame(ctx, conn, status) != nil {
			return cursor, true, false
		}
		*last, *sent = status, true
	}
	if out.Output != nil {
		if writeOutputFrame(ctx, conn, out.Output) != nil {
			return cursor, true, false
		}
	}
	if out.Done {
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "eof"})
		return out.Next, true, true
	}
	return out.Next, false, true
}

// closeOutputSlice ends the attachment for a host read failure. Unknown
// runs and changed incarnations close with their reason; anything else is
// a transient miss the pump retries within its failure budget.
func closeOutputSlice(ctx context.Context, conn *websocket.Conn, err error) bool {
	switch {
	case errors.Is(err, factoryservice.ErrRunOutputMissing):
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "run_unknown"})
		return true
	case errors.Is(err, factoryservice.ErrRunOutputChanged):
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "incarnation_changed"})
		return true
	case errors.Is(err, factoryservice.ErrRunOutputAccess):
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
		return true
	case errors.Is(err, factoryservice.ErrRunOutputInvalid), errors.Is(err, factoryservice.ErrRunOutputCursor):
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "output_unavailable"})
		return true
	default:
		return false
	}
}

func (s Output) heartbeat(ctx context.Context, conn *websocket.Conn) bool {
	check, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	if !s.Current(check) || s.Attachment.Check(check) != nil {
		_ = writeOutputFrame(ctx, conn, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
		return false
	}
	return conn.Ping(check) == nil
}
