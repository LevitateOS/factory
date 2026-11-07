package project

import (
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxFactoryContextPaths        = 12
	MaxFactoryContextFiles        = 12
	MaxFactoryContextPathBytes    = 256
	MaxFactoryContextEntries      = 256
	MaxFactorySourceFileBytes     = 1 << 30
	DefaultFactorySourcePageBytes = 8 * 1024
	// Approved source files may reach MaxApprovedFileSize (32 KiB); the
	// per-file bound matches so an approved file reads fully through one
	// bounded native call, with cap+1 oversize detection instead of
	// truncation. The aggregate bound matches MaxApprovedTotal so one
	// large file plus setup, check, instructions and diff still fit a
	// single bounded read. Larger whole-file selections refuse; explicit source
	// pages retain this bound on each returned fragment.
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
	Project      string                  `json:"project"`
	ID           string                  `json:"id"`
	SourceCommit string                  `json:"source_commit"`
	ApprovedBase string                  `json:"approved_base"`
	DiffBase     string                  `json:"diff_base"`
	Candidate    string                  `json:"candidate"`
	Paths        []string                `json:"paths"`
	Directory    *string                 `json:"directory,omitempty"`
	Page         *FactoryContextReadPage `json:"page,omitempty"`
	NotAfter     time.Time               `json:"not_after"`
}

func (r FactoryPreparationContextRequest) Validate() error {
	if !ValidID(r.Project) || !ValidPreparationID(r.ID) || !ValidCommit(r.SourceCommit) ||
		!ValidCommit(r.ApprovedBase) || !ValidCommit(r.DiffBase) || !ValidCommit(r.Candidate) ||
		r.NotAfter.IsZero() || len(r.Paths) > MaxFactoryContextPaths {
		return errors.New("invalid factory preparation context request")
	}
	seen := make(map[string]bool, len(r.Paths))
	if r.Directory != nil && *r.Directory != "" && !ValidFactoryContextPath(*r.Directory) {
		return errors.New("invalid factory preparation context directory")
	}
	for _, name := range r.Paths {
		if !ValidFactoryContextPath(name) || seen[name] {
			return errors.New("invalid factory preparation context path")
		}
		seen[name] = true
	}
	if r.Page != nil && (r.Page.Validate() != nil || r.Directory != nil || len(r.Paths) != 1 || r.Paths[0] != r.Page.Path) {
		return errors.New("invalid factory preparation file page")
	}
	return nil
}

type FactoryContextReadPage struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Limit  int    `json:"limit"`
}

func (r FactoryContextReadPage) Validate() error {
	if !ValidFactoryContextPath(r.Path) || r.Offset < 0 || r.Offset > MaxFactorySourceFileBytes || r.Limit < 4 || r.Limit > MaxFactoryContextFileBytes {
		return errors.New("invalid factory source file page")
	}
	return nil
}

type FactoryContextPage struct {
	Path       string `json:"path"`
	Offset     int64  `json:"offset"`
	TotalBytes int64  `json:"total_bytes"`
	NextOffset *int64 `json:"next_offset,omitempty"`
}

// FactoryContextFile is one approved repository file selected by native
// preparation policy.
type FactoryContextFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

type FactoryContextEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// FactoryPreparationContext is the bounded native-authorized prompt context.
// []byte fields use JSON base64 so arbitrary repository bytes cannot corrupt
// the wire envelope.
type FactoryPreparationContext struct {
	Project      string                `json:"project"`
	ID           string                `json:"id"`
	Role         string                `json:"role"`
	SourceCommit string                `json:"source_commit"`
	ApprovedBase string                `json:"approved_base"`
	DiffBase     string                `json:"diff_base"`
	Candidate    string                `json:"candidate"`
	Setup        []byte                `json:"setup"`
	Check        []byte                `json:"check"`
	Files        []FactoryContextFile  `json:"files"`
	Diff         []byte                `json:"diff"`
	Directory    *string               `json:"directory,omitempty"`
	Entries      []FactoryContextEntry `json:"entries,omitempty"`
	Page         *FactoryContextPage   `json:"page,omitempty"`
}

func (c FactoryPreparationContext) ValidateFor(r FactoryPreparationContextRequest, role string) error {
	if r.Validate() != nil || c.Project != r.Project || c.ID != r.ID || c.Role != role ||
		c.SourceCommit != r.SourceCommit || c.ApprovedBase != r.ApprovedBase ||
		c.DiffBase != r.DiffBase || c.Candidate != r.Candidate ||
		(c.Directory == nil) != (r.Directory == nil) || c.Directory != nil && *c.Directory != *r.Directory {
		return errors.New("native factory preparation context does not match its request")
	}
	if (c.Page == nil) != (r.Page == nil) || c.Page != nil && (c.Page.Path != r.Page.Path || c.Page.Offset != r.Page.Offset) {
		return errors.New("native source page does not match its request")
	}
	if r.Page != nil {
		for _, file := range c.Files {
			if file.Path == r.Page.Path && len(file.Content) > r.Page.Limit {
				return errors.New("native source page exceeds requested limit")
			}
		}
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
	if c.Page != nil {
		page := c.Page
		if c.Directory != nil || !ValidFactoryContextPath(page.Path) || page.Offset < 0 || page.TotalBytes < page.Offset || page.TotalBytes > MaxFactorySourceFileBytes {
			return errors.New("invalid native source page")
		}
		found := false
		for _, file := range c.Files {
			if file.Path != page.Path {
				continue
			}
			end := page.Offset + int64(len(file.Content))
			if found || end > page.TotalBytes || end < page.TotalBytes && (len(file.Content) == 0 || page.NextOffset == nil || *page.NextOffset != end) || end == page.TotalBytes && page.NextOffset != nil {
				return errors.New("invalid native source page continuation")
			}
			found = true
		}
		if !found {
			return errors.New("native source page file missing")
		}
	}
	if len(c.Entries) > MaxFactoryContextEntries || c.Directory == nil && len(c.Entries) != 0 || c.Directory != nil && *c.Directory != "" && !ValidFactoryContextPath(*c.Directory) {
		return errors.New("invalid native factory context directory")
	}
	seen := make(map[string]bool, len(c.Entries))
	for _, entry := range c.Entries {
		parent := path.Dir(entry.Path)
		if parent == "." {
			parent = ""
		}
		if c.Directory == nil || parent != *c.Directory || !ValidFactoryContextPath(entry.Path) || seen[entry.Path] {
			return errors.New("invalid native factory context entry")
		}
		switch entry.Kind {
		case "file", "directory", "symlink", "submodule":
		default:
			return errors.New("invalid native factory context entry kind")
		}
		seen[entry.Path] = true
		total += len(entry.Path) + len(entry.Kind)
	}
	for _, file := range c.Files {
		if !ValidFactoryContextPath(file.Path) || len(file.Content) > MaxFactoryContextFileBytes || !utf8.Valid(file.Content) {
			return errors.New("invalid native factory context file")
		}
		total += len(file.Content)
	}
	if total > MaxFactoryContextBytes || !utf8.Valid(c.Setup) || !utf8.Valid(c.Check) || !utf8.Valid(c.Diff) {
		return errors.New("native factory preparation context exceeds its text bound")
	}
	return nil
}

// ValidFactoryContextPath accepts a bounded canonical repository-relative path.
func ValidFactoryContextPath(name string) bool {
	return name != "" && len(name) <= MaxFactoryContextPathBytes && !strings.ContainsRune(name, '\\') &&
		!strings.HasPrefix(name, "/") && path.Clean(name) == name && name != "." && name != ".." &&
		!strings.HasPrefix(name, "../")
}
