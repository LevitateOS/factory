package httpapi

import (
	"context"

	"github.com/coder/websocket"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
)

// Output serves one read-only stream on an already admitted WebSocket. The host
// owns origin/channel admission, upgrade and peer registration/draining. The
// attachment owns canonical actor/repository/run admission; Handshake binds its
// host's repository label and account generation. It must never grant authority.
type Output struct {
	Attachment *factoryservice.OutputAttachment
	Handshake  func(OutputHandshake) bool
	Current    func(context.Context) bool
}

// Serve owns closing conn and joining its input reader. It starts no runtime,
// process, listener or work and never sends viewer input to the output source.
func (s Output) Serve(parent context.Context, conn *websocket.Conn) {
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	closePeer := context.AfterFunc(ctx, func() { conn.CloseNow() })
	defer closePeer()
	conn.SetReadLimit(32768)
	if s.Attachment == nil || s.Handshake == nil || s.Current == nil || !s.Current(ctx) {
		refuseOutput(conn)
		return
	}
	in, err := readOutputHandshake(ctx, conn)
	if err != nil || in.RunID != s.Attachment.RunID() || in.Cursor < 0 || in.Cursor > project.MaxFactoryOutputOffset || !s.Handshake(in) || !s.Current(ctx) || s.Attachment.Check(ctx) != nil {
		refuseOutput(conn)
		return
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		rejectOutputInput(ctx, cancel, conn)
	}()
	defer func() {
		cancel()
		conn.CloseNow()
		<-readerDone
	}()
	s.pump(ctx, conn, in.Cursor)
}
