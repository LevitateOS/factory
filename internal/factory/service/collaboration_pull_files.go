package service

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/collaboration"
)

var (
	ErrPullFilesQuery       = errors.New("invalid pull files query")
	ErrPullFilesMissing     = errors.New("pull files are not visible")
	ErrPullFilesUnavailable = errors.New("pull files unavailable")
	ErrPullFilesChanged     = errors.New("pull diff changed")
	ErrPullFilesTooLarge    = errors.New("pull file list exceeds read limit")
)

type PullFilesQuery struct {
	Base   collaboration.CommitID
	Head   collaboration.CommitID
	Cursor string
	Limit  int
}

func (q PullFilesQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 10 || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) || !(q.Base == "" && q.Head == "" && q.Cursor == "" || collaboration.ValidCommitID(q.Base) && collaboration.ValidCommitID(q.Head)) {
		return ErrPullFilesQuery
	}
	return nil
}

type CollaborationPullFilesSource interface {
	ReadPullFiles(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, PullFilesQuery) (collaboration.PullFiles, error)
}
type CollaborationPullFiles struct{ Source CollaborationPullFilesSource }

func (s CollaborationPullFiles) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, number int64, q PullFilesQuery) (collaboration.PullFiles, error) {
	var empty collaboration.PullFiles
	if actor < 1 || repository < 1 || number < 1 || q.Validate() != nil {
		return empty, ErrPullFilesQuery
	}
	if s.Source == nil {
		return empty, ErrPullFilesUnavailable
	}
	result, err := s.Source.ReadPullFiles(ctx, actor, repository, number, q)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	diff, page := result.Diff, result.Files
	if diff.Pull.Kind != collaboration.SubjectPull || diff.Pull.Repository != repository || diff.Pull.Number != number || diff.Pull.ExternalID == "" || !collaboration.ValidCommitID(diff.Base) || !collaboration.ValidCommitID(diff.Head) || q.Base != "" && (diff.Base != q.Base || diff.Head != q.Head) || page.Items == nil || len(page.Items) > q.Limit || page.Complete != (page.Next == "") || page.Next != "" && (len(page.Items) == 0 || !collaboration.ValidText(page.Next, 4096)) || page.Observation.State != collaboration.ReadAvailable || page.Observation.Revision == "" {
		return empty, ErrPullFilesUnavailable
	}
	seen := map[string]bool{}
	for _, f := range page.Items {
		key := f.OldPath + "\x00" + f.NewPath
		if f.Diff != diff || seen[key] || f.OldPath == "" && f.NewPath == "" || len(f.OldPath) > 4096 || len(f.NewPath) > 4096 || !utf8.ValidString(f.OldPath) || !utf8.ValidString(f.NewPath) || f.Additions != nil && *f.Additions < 0 || f.Deletions != nil && *f.Deletions < 0 {
			return empty, ErrPullFilesUnavailable
		}
		seen[key] = true
		switch f.Change {
		case "added", "deleted", "modified", "renamed", "copied", "unknown":
		default:
			return empty, ErrPullFilesUnavailable
		}
		switch f.PatchState {
		case "complete", "truncated":
			if f.Patch == nil {
				return empty, ErrPullFilesUnavailable
			}
		case "binary", "unavailable", "unknown":
			if f.Patch != nil {
				return empty, ErrPullFilesUnavailable
			}
		default:
			return empty, ErrPullFilesUnavailable
		}
	}
	return result, nil
}
