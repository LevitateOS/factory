package service

import (
	"context"
	"encoding/base64"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// RunOutput supplies a read-only runtime port over the existing bounded output
// record. Project in that record is an opaque workspace address. Native sources
// supply their own output owner and no appliance container/unit facts.
type RunOutput struct {
	Store  *store.Store
	Scope  func(context.Context, collaboration.ActorID, factory.Run) (collaboration.RepositoryID, error)
	Access func(context.Context, collaboration.ActorID, collaboration.RepositoryID, factory.Run) error
	Source func(context.Context, collaboration.ActorID, factory.Run, int64, int) (project.FactoryOutputState, error)
}

var (
	ErrRunOutputUnavailable = errors.New("run output runtime is unavailable")
	ErrRunOutputAccess      = errors.New("current run output authority is required")
	ErrRunOutputMissing     = errors.New("run output is not observed")
	ErrRunOutputChanged     = errors.New("run output scope or incarnation changed")
	ErrRunOutputInvalid     = errors.New("run output slice is invalid")
	ErrRunOutputCursor      = errors.New("run output cursor is invalid")
)

type OutputAttachment struct {
	owner                        RunOutput
	actor                        collaboration.ActorID
	repository                   collaboration.RepositoryID
	runID, workspace, invocation string
	kind                         collaboration.RuntimeKind
}

// RunID is the immutable run selected when opening this attachment. Transport
// uses it to refuse a handshake naming another run before any output read.
func (a *OutputAttachment) RunID() string { return a.runID }

func (s RunOutput) Open(ctx context.Context, actor collaboration.ActorID, runID string) (*OutputAttachment, error) {
	if actor < 1 || !factory.ValidID(runID) {
		return nil, factory.ErrNotFound
	}
	if s.Store == nil || s.Scope == nil || s.Access == nil || s.Source == nil {
		return nil, ErrRunOutputUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := s.Store.FactoryRun(ctx, runID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, factory.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if run.ID != runID {
		return nil, ErrRunOutputChanged
	}
	repository, err := s.Scope(ctx, actor, run)
	if err != nil {
		return nil, err
	}
	if repository < 1 {
		return nil, ErrRunOutputUnavailable
	}
	attachment := &OutputAttachment{owner: s, actor: actor, repository: repository, runID: runID, workspace: run.ProjectID, kind: run.RuntimeKind(), invocation: run.InvocationID()}
	if _, err := attachment.current(ctx); err != nil {
		return nil, err
	}
	return attachment, nil
}

// Check performs current scope/admission only; transport heartbeats never launch
// or resume work. An attachment is owned by one sequential reader.
func (a *OutputAttachment) Check(ctx context.Context) error {
	_, err := a.current(ctx)
	return err
}

func (a *OutputAttachment) current(ctx context.Context) (factory.Run, error) {
	var empty factory.Run
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	run, err := a.owner.Store.FactoryRun(ctx, a.runID)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrRunOutputMissing
	}
	if err != nil {
		return empty, err
	}
	if run.ID != a.runID || run.ProjectID != a.workspace || run.RuntimeKind() != a.kind {
		return empty, ErrRunOutputChanged
	}
	if invocation := run.InvocationID(); invocation != "" && a.invocation != "" && invocation != a.invocation {
		return empty, ErrRunOutputChanged
	}
	repository, err := a.owner.Scope(ctx, a.actor, run)
	if err != nil {
		return empty, err
	}
	if repository != a.repository {
		return empty, ErrRunOutputChanged
	}
	if err := a.owner.Access(ctx, a.actor, repository, run); err != nil {
		return empty, errors.Join(ErrRunOutputAccess, err)
	}
	if binding, err := a.owner.Store.FactoryRunView(ctx, a.runID); err == nil {
		if binding.RunID != a.runID || binding.Repository != int64(repository) {
			return empty, ErrRunOutputChanged
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return run, nil
}

type RunOutputStatus struct {
	Type       string `json:"type"`
	RunID      string `json:"run_id"`
	Phase      string `json:"phase"`
	Container  string `json:"container"`
	Unit       string `json:"unit"`
	Invocation string `json:"invocation"`
	Reason     string `json:"reason"`
	Live       bool   `json:"live"`
	Terminal   bool   `json:"terminal"`
	ExitCode   *int   `json:"exit_code"`
}

func (a RunOutputStatus) Same(b RunOutputStatus) bool {
	if a.ExitCode == nil || b.ExitCode == nil {
		if a.ExitCode != b.ExitCode {
			return false
		}
	} else if *a.ExitCode != *b.ExitCode {
		return false
	}
	a.ExitCode, b.ExitCode = nil, nil
	return a == b
}

type RunOutputFrame struct {
	Type      string `json:"type"`
	Data      string `json:"data"`
	Cursor    int64  `json:"cursor"`
	Next      int64  `json:"next"`
	Gap       bool   `json:"gap"`
	Truncated bool   `json:"truncated"`
}

type RunOutputClosed struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type RunOutputSlice struct {
	Status RunOutputStatus
	Output *RunOutputFrame
	Next   int64
	Done   bool
}

func (a *OutputAttachment) Read(ctx context.Context, cursor int64) (*RunOutputSlice, error) {
	if cursor < 0 || cursor > project.MaxFactoryOutputOffset {
		return nil, ErrRunOutputCursor
	}
	run, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	out, err := a.owner.Source(ctx, a.actor, run, cursor, project.MaxFactoryOutputRead)
	if err != nil {
		return nil, err
	}
	if out.ID != a.runID || out.Project != a.workspace || a.kind == collaboration.Native && (out.Container != "" || out.Unit != "") {
		return nil, ErrRunOutputChanged
	}
	expected := a.invocation
	if invocation := run.InvocationID(); invocation != "" {
		expected = invocation
	}
	if expected != "" && out.Invocation != expected {
		return nil, ErrRunOutputChanged
	}
	if !project.ValidFactoryPhase(out.Phase) || out.Live && out.Terminal || len(out.Container) > 512 || len(out.Unit) > 512 || len(out.Invocation) > 128 || len(out.Reason) > 4096 ||
		out.Total < 0 || out.Total > project.MaxFactoryOutputOffset || out.Offset < 0 || out.Next < out.Offset || out.Next > out.Total ||
		(out.Offset != cursor && !out.Gap && !out.Truncated) || len(out.Data) > base64.StdEncoding.EncodedLen(project.MaxFactoryOutputRead) {
		return nil, ErrRunOutputInvalid
	}
	data, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil || len(data) > project.MaxFactoryOutputRead || out.Next-out.Offset != int64(len(data)) {
		return nil, ErrRunOutputInvalid
	}
	// No bytes or status from a slow source escape after authority changes.
	latest, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	if invocation := latest.InvocationID(); invocation != "" && invocation != out.Invocation {
		return nil, ErrRunOutputChanged
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.invocation = out.Invocation
	view := &RunOutputSlice{Status: RunOutputStatus{Type: "status", RunID: a.runID, Phase: out.Phase, Container: out.Container, Unit: out.Unit, Invocation: out.Invocation, Reason: out.Reason, Live: out.Live, Terminal: out.Terminal, ExitCode: out.ExitCode},
		Next: out.Next, Done: out.Terminal && out.Next >= out.Total}
	if len(data) != 0 || out.Gap || out.Truncated {
		// Canonical base64 also bounds JSON escaping when a producer includes
		// ignored CR/LF bytes in its encoding. Raw bytes and cursors stay exact.
		view.Output = &RunOutputFrame{Type: "output", Data: base64.StdEncoding.EncodeToString(data), Cursor: out.Offset, Next: out.Next, Gap: out.Gap, Truncated: out.Truncated}
	}
	return view, nil
}
