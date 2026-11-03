package github

import (
	"bytes"
	"context"
	"strings"
	"unicode/utf8"

	sdk "github.com/google/go-github/v93/github"
	"github.com/sourcegraph/go-diff/diff"
)

const pullFileLimit = 3000 // GitHub's PR files endpoint ceiling, not CompareCommits' ceiling.
const pullPatchLimit = 16 << 10

// Provider-local file facts. Patch contains GitHub's hunk fragment, not a
// standalone diff with file headers. Paths are display/anchor data, never URLs
// to follow or local filesystem paths. Canonical mapping belongs to the consumer.
type PullFile struct {
	OldPath    string  `json:"old_path"`
	NewPath    string  `json:"new_path"`
	Change     string  `json:"change"`
	PatchState string  `json:"patch_state"`
	Patch      *string `json:"patch"`
	Additions  *int    `json:"additions"`
	Deletions  *int    `json:"deletions"`
}

type PullFilePage struct {
	Diff     PullDiffScope `json:"diff"`
	Items    []PullFile    `json:"items"`
	Page     int           `json:"page"`
	Limit    int           `json:"limit"`
	Total    int           `json:"total"`
	NextPage int           `json:"next_page"`
}

// PullFiles reads exactly one bounded page of the current PR diff. Discovery is
// allowed only on page one; later pages must supply the captured merge-base/head
// pair. The consumer must retain the same limit when following NextPage.
func (c *Client) PullFiles(ctx context.Context, repository int64, number, page, limit int, base, head string) (PullFilePage, error) {
	if repository < 1 || number < 1 || !validFilesQuery(page, limit, base, head) {
		return PullFilePage{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return PullFilePage{}, err
	}
	pull, err := c.readDiffPull(ctx, repository, owner, name, number)
	if err != nil {
		return PullFilePage{}, err
	}
	before := diffState(repository, pull)
	if head != "" && head != before.Head {
		return PullFilePage{}, ErrDiffChanged
	}
	if pull.ChangedFiles == nil || *pull.ChangedFiles < 0 {
		return PullFilePage{}, ErrUnavailable
	}
	total := *pull.ChangedFiles
	if total > pullFileLimit {
		return PullFilePage{}, ErrFilesTooLarge
	}
	offset := (page - 1) * limit
	if offset >= total && !(page == 1 && total == 0) {
		return PullFilePage{}, ErrInvalidRequest
	}
	scope, err := c.resolvePullDiff(ctx, owner, name, number, before, base)
	if err != nil {
		return PullFilePage{}, err
	}
	files, response, err := c.sdk.PullRequests.ListFiles(ctx, owner, name, number, &sdk.ListOptions{Page: page, PerPage: limit})
	info, err := result(ctx, response, err)
	if err != nil {
		return PullFilePage{}, err
	}
	// The live endpoint has no conditional-pair selector. Bracket its response
	// with the PR identity, full commits, base branch and changed-file count.
	after, err := c.readDiffPull(ctx, repository, owner, name, number)
	if err != nil {
		return PullFilePage{}, err
	}
	if diffState(repository, after) != before || after.ChangedFiles == nil || *after.ChangedFiles != total {
		return PullFilePage{}, ErrDiffChanged
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return PullFilePage{}, err
	}
	want := min(limit, total-offset)
	next := 0
	if offset+want < total {
		next = page + 1
	}
	if len(files) != want || info.NextPage != next || info.LastPage != 0 && info.LastPage != (total+limit-1)/limit {
		return PullFilePage{}, ErrInvalidResponse
	}
	result := PullFilePage{Diff: scope, Items: make([]PullFile, 0, len(files)), Page: page, Limit: limit, Total: total, NextPage: next}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		record, err := pullFile(file)
		if err != nil {
			return PullFilePage{}, err
		}
		if seen[file.GetFilename()] {
			return PullFilePage{}, ErrInvalidResponse
		}
		seen[file.GetFilename()] = true
		result.Items = append(result.Items, record)
	}
	if err := ctx.Err(); err != nil {
		return PullFilePage{}, err
	}
	return result, nil
}

func validFilesQuery(page, limit int, base, head string) bool {
	return page >= 1 && page <= pullFileLimit && limit >= 1 && limit <= 10 && validDiffPair(base, head) && (page == 1 || base != "")
}

func pullFile(file *sdk.CommitFile) (PullFile, error) {
	if file == nil || !filePath(file.GetFilename()) || file.Additions != nil && *file.Additions < 0 || file.Deletions != nil && *file.Deletions < 0 {
		return PullFile{}, ErrInvalidResponse
	}
	record := PullFile{Change: file.GetStatus(), Additions: file.Additions, Deletions: file.Deletions}
	switch record.Change {
	case "added":
		record.NewPath = file.GetFilename()
	case "removed":
		record.Change, record.OldPath = "deleted", file.GetFilename()
	case "renamed", "copied":
		if !filePath(file.GetPreviousFilename()) {
			return PullFile{}, ErrInvalidResponse
		}
		record.OldPath, record.NewPath = file.GetPreviousFilename(), file.GetFilename()
	case "modified":
		record.OldPath, record.NewPath = file.GetFilename(), file.GetFilename()
	default:
		record.Change, record.NewPath = "unknown", file.GetFilename()
	}
	record.PatchState, record.Patch = pullPatch(file)
	return record, nil
}

func filePath(path string) bool {
	return path != "" && len(path) <= 4096 && utf8.ValidString(path) && !strings.ContainsRune(path, 0)
}

func pullPatch(file *sdk.CommitFile) (string, *string) {
	// GitHub omits patches for both binary and large/unavailable text files.
	// Neither zero statistics nor an extension establishes that a file is binary.
	if file.Patch == nil || *file.Patch == "" || !utf8.ValidString(*file.Patch) {
		return "unavailable", nil
	}
	patch := *file.Patch
	if len(patch) > pullPatchLimit {
		end := pullPatchLimit
		for !utf8.RuneStart(patch[end]) {
			end--
		}
		patch = patch[:end]
		return "truncated", &patch
	}
	// The mature parser handles hunk syntax and no-newline markers. Parsing
	// alone accepts a short final hunk, so also check each declared span and the
	// provider's full additions/deletions (which catches entirely omitted hunks).
	hunks, err := diff.ParseHunksOptions([]byte(patch), diff.ParseOptions{KeepCR: true})
	if err != nil || len(hunks) == 0 || file.Additions == nil || file.Deletions == nil {
		return "unavailable", nil
	}
	added, deleted, short := 0, 0, false
	for _, hunk := range hunks {
		if hunk.OrigStartLine < 0 || hunk.NewStartLine < 0 || hunk.OrigLines < 0 || hunk.NewLines < 0 {
			return "unavailable", nil
		}
		oldLines, newLines := 0, 0
		for _, line := range bytes.Split(hunk.Body, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			switch line[0] {
			case '+':
				added++
				newLines++
			case '-':
				deleted++
				oldLines++
			case ' ':
				oldLines++
				newLines++
			default:
				return "unavailable", nil
			}
		}
		if oldLines > int(hunk.OrigLines) || newLines > int(hunk.NewLines) {
			return "unavailable", nil
		}
		short = short || oldLines < int(hunk.OrigLines) || newLines < int(hunk.NewLines)
	}
	if added > *file.Additions || deleted > *file.Deletions {
		return "unavailable", nil
	}
	if short || added < *file.Additions || deleted < *file.Deletions {
		return "truncated", &patch
	}
	return "complete", &patch
}
