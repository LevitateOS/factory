package project

import (
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxFactoryContextPaths     = 12
	MaxFactoryContextFiles     = 12
	MaxFactoryContextPathBytes = 256
	// Approved source files may reach MaxApprovedFileSize (32 KiB); the
	// per-file bound matches so an approved file reads fully through one
	// bounded native call, with cap+1 oversize detection instead of
	// truncation. The aggregate bound matches MaxApprovedTotal so one
	// large file plus setup, check, instructions and diff still fit a
	// single bounded read. Anything larger fails truthfully.
	MaxFactoryContextFileBytes = 32 * 1024
	MaxFactoryContextBytes     = 128 * 1024

	// FactoryContextSelectedPathsPerRead sizes client selections, not native
	// authority. Reserve file-sized slots for setup, check and root AGENTS.md.
	// Entrypoints and expanded ancestor instructions still undergo the actual
	// native aggregate validation; an oversized closure cannot be read partially.
	FactoryContextSelectedPathsPerRead = MaxFactoryContextBytes/MaxFactoryContextFileBytes - 2 - 1
)

// FactoryPreparationContextRequest selects repository material for one exact
// prepared checkout and candidate diff. Repository bytes remain transient.
type FactoryPreparationContextRequest struct {
	Project      string    `json:"project"`
	ID           string    `json:"id"`
	SourceCommit string    `json:"source_commit"`
	ApprovedBase string    `json:"approved_base"`
	DiffBase     string    `json:"diff_base"`
	Candidate    string    `json:"candidate"`
	Paths        []string  `json:"paths"`
	NotAfter     time.Time `json:"not_after"`
}

func (r FactoryPreparationContextRequest) Validate() error {
	if !ValidID(r.Project) || !ValidPreparationID(r.ID) || !ValidCommit(r.SourceCommit) ||
		!ValidCommit(r.ApprovedBase) || !ValidCommit(r.DiffBase) || !ValidCommit(r.Candidate) ||
		r.NotAfter.IsZero() || len(r.Paths) > MaxFactoryContextPaths {
		return errors.New("invalid factory preparation context request")
	}
	seen := make(map[string]bool, len(r.Paths))
	for _, name := range r.Paths {
		if !validFactoryContextPath(name) || seen[name] {
			return errors.New("invalid factory preparation context path")
		}
		seen[name] = true
	}
	return nil
}

// FactoryContextFile is one approved repository file selected by native
// preparation policy.
type FactoryContextFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// FactoryPreparationContext is the bounded native-authorized prompt context.
// []byte fields use JSON base64 so arbitrary repository bytes cannot corrupt
// the wire envelope.
type FactoryPreparationContext struct {
	Project      string               `json:"project"`
	ID           string               `json:"id"`
	Role         string               `json:"role"`
	SourceCommit string               `json:"source_commit"`
	ApprovedBase string               `json:"approved_base"`
	DiffBase     string               `json:"diff_base"`
	Candidate    string               `json:"candidate"`
	Setup        []byte               `json:"setup"`
	Check        []byte               `json:"check"`
	Files        []FactoryContextFile `json:"files"`
	Diff         []byte               `json:"diff"`
}

func (c FactoryPreparationContext) ValidateFor(r FactoryPreparationContextRequest, role string) error {
	if r.Validate() != nil || c.Project != r.Project || c.ID != r.ID || c.Role != role ||
		c.SourceCommit != r.SourceCommit || c.ApprovedBase != r.ApprovedBase ||
		c.DiffBase != r.DiffBase || c.Candidate != r.Candidate {
		return errors.New("native factory preparation context does not match its request")
	}
	return c.ValidateContent(role)
}

func (c FactoryPreparationContext) ValidateContent(role string) error {
	if !ValidID(c.Project) || !ValidPreparationID(c.ID) || c.Role != role || !ValidFactoryRole(c.Role) ||
		!ValidCommit(c.SourceCommit) || !ValidCommit(c.ApprovedBase) || !ValidCommit(c.DiffBase) || !ValidCommit(c.Candidate) ||
		len(c.Setup) == 0 || len(c.Check) == 0 || len(c.Files) > MaxFactoryContextFiles {
		return errors.New("invalid native factory preparation context")
	}
	total := len(c.Setup) + len(c.Check) + len(c.Diff)
	for _, file := range c.Files {
		if !validFactoryContextPath(file.Path) || len(file.Content) > MaxFactoryContextFileBytes || !utf8.Valid(file.Content) {
			return errors.New("invalid native factory context file")
		}
		total += len(file.Content)
	}
	if total > MaxFactoryContextBytes || !utf8.Valid(c.Setup) || !utf8.Valid(c.Check) || !utf8.Valid(c.Diff) {
		return errors.New("native factory preparation context exceeds its text bound")
	}
	return nil
}

func validFactoryContextPath(name string) bool {
	return name != "" && len(name) <= MaxFactoryContextPathBytes && !strings.ContainsRune(name, '\\') &&
		!strings.HasPrefix(name, "/") && path.Clean(name) == name && name != "." && name != ".." &&
		!strings.HasPrefix(name, "../")
}
