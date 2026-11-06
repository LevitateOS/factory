package runtime

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s nativeCollaborationReads) ReadPullCommits(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.PullCommitsQuery) (collaboration.PullCommits, error) {
	var result collaboration.PullCommits
	if q.Validate() != nil || !nativeIndex(number) {
		return result, factoryservice.ErrPullCommitsQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrPullCommitsMissing); err != nil {
		return result, err
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: "pull_commits", Repository: s.identity.SelectedRepository, Number: number, Head: string(q.Head), Limit: q.Limit})
	if err != nil || c.Page > 1 && c.Root == "" {
		return result, factoryservice.ErrPullCommitsQuery
	}
	var raw struct {
		Repository string                     `json:"repository"`
		Name       string                     `json:"name"`
		Pull       nativeSubjectRef           `json:"pull"`
		Head       collaboration.CommitID     `json:"head"`
		Items      []collaboration.PullCommit `json:"items"`
		NextCursor string                     `json:"next_cursor"`
	}
	head := string(q.Head)
	if err = s.read(ctx, nativeRead{Kind: "pull_commits", Repository: c.Repository, Number: uint32(number), Limit: uint32(c.Limit), Head: &head, Before: c.Root}, &raw); err != nil {
		var failure *NativePortError
		if errors.As(err, &failure) && failure.Status == 409 {
			return result, factoryservice.ErrPullCommitsChanged
		}
		return result, nativeMissing(err, factoryservice.ErrPullCommitsMissing)
	}
	if raw.Repository != c.Repository || raw.Pull.Kind != "pull" || !nativeRemoteID(raw.Pull.ExternalID) || raw.Pull.Number != number || raw.Head != q.Head || raw.Items == nil || len(raw.Items) > q.Limit || len(raw.NextCursor) > 1024 || raw.NextCursor != "" && (len(raw.Items) == 0 || raw.NextCursor == c.Root) {
		return result, errNativeRead
	}
	if _, err := s.mapRead(ctx, raw.Repository, raw.Name, nil, nil); err != nil {
		return result, err
	}
	next := ""
	if raw.NextCursor != "" {
		c.Root = raw.NextCursor
		next = cursorEncode(c, c.Page+1)
	}
	result = collaboration.PullCommits{Pull: collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: raw.Pull.ExternalID, Number: number}, Head: raw.Head, Commits: nativePage(s.observation(raw), raw.Items, next)}
	return result, nil
}
