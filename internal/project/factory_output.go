package project

import "errors"

const (
	// MaxFactoryOutputRead bounds one output slice. Watchers resume by
	// cursor; no single read carries a whole session. The bound keeps one
	// base64 JSON frame within Fountain's 32768-byte proxy message limit:
	// 24320 raw bytes encode to 32428 with at most 93 envelope bytes.
	MaxFactoryOutputRead = 24*1024 - 256
	// MaxFactoryOutputWindow bounds the trailing window served when a
	// watcher attaches without a cursor. Older bytes report truncated.
	MaxFactoryOutputWindow = 256 * 1024
	// MaxFactoryOutputOffset bounds an output cursor. Offsets past the
	// recorded size report a gap instead of failing.
	MaxFactoryOutputOffset = 256 << 20
)

// FactoryOutput addresses one bounded slice of a run's recorded CLI output.
type FactoryOutput struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Offset  int64  `json:"offset"`
	Limit   int    `json:"limit"`
}

func (p FactoryOutput) Validate() error {
	if !ValidID(p.Project) || !ValidFactoryRunID(p.ID) {
		return errors.New("invalid factory run address")
	}
	if p.Offset < 0 || p.Offset > MaxFactoryOutputOffset {
		return errors.New("invalid output cursor")
	}
	if p.Limit < 1 || p.Limit > MaxFactoryOutputRead {
		return errors.New("invalid output read bound")
	}
	return nil
}

// FactoryOutputState is one observed output slice with its run/process
// identity. Data carries base64-encoded raw bytes so binary CLI output
// survives JSON transport; cursors are byte offsets chosen by the reader,
// never computed by the viewer. Truncated reports a cursor-zero attach that
// skipped older bytes; Gap reports a cursor past the recorded size.
type FactoryOutputState struct {
	ExitCode   *int   `json:"exit_code,omitempty"`
	Live       bool   `json:"live"`
	Terminal   bool   `json:"terminal"`
	Truncated  bool   `json:"truncated"`
	Gap        bool   `json:"gap"`
	ID         string `json:"id"`
	Project    string `json:"project"`
	Phase      string `json:"phase"`
	Container  string `json:"container"`
	Unit       string `json:"unit"`
	Invocation string `json:"invocation"`
	Total      int64  `json:"total"`
	Offset     int64  `json:"offset"`
	Next       int64  `json:"next"`
	Data       string `json:"data"`
	Reason     string `json:"reason,omitempty"`
}
