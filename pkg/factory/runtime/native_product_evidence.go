package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type nativeSubjectRef struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
	Number     int64  `json:"number"`
}
type nativeDiff struct {
	Repository string                 `json:"repository"`
	Name       string                 `json:"name"`
	Pull       nativeSubjectRef       `json:"pull"`
	Base       collaboration.CommitID `json:"base"`
	Head       collaboration.CommitID `json:"head"`
	PullBase   collaboration.CommitID `json:"pull_base"`
	BaseBranch string                 `json:"base_branch"`
}

func (s nativeCollaborationReads) ReadPullFiles(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.PullFilesQuery) (collaboration.PullFiles, error) {
	var result collaboration.PullFiles
	if q.Validate() != nil || !nativeIndex(number) {
		return result, factoryservice.ErrPullFilesQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrPullFilesMissing); err != nil {
		return result, err
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: "pull_files", Repository: s.identity.SelectedRepository, Number: number, Base: string(q.Base), Head: string(q.Head), Limit: q.Limit})
	if err != nil || c.Page > 3000 {
		return result, factoryservice.ErrPullFilesQuery
	}
	var raw struct {
		Diff     nativeDiff                  `json:"diff"`
		Items    []collaboration.ChangedFile `json:"items"`
		Page     int                         `json:"page"`
		Limit    int                         `json:"limit"`
		Total    int                         `json:"total"`
		NextPage int                         `json:"next_page"`
	}
	base, head := string(q.Base), string(q.Head)
	if err = s.read(ctx, nativeRead{Kind: "pull_files", Repository: c.Repository, Number: uint32(number), Page: uint32(c.Page), Limit: uint32(c.Limit), Base: &base, Head: &head}, &raw); err != nil {
		var failure *NativePortError
		if errors.As(err, &failure) {
			if failure.Status == 409 {
				return result, factoryservice.ErrPullFilesChanged
			}
			if failure.Status == 413 {
				return result, factoryservice.ErrPullFilesTooLarge
			}
		}
		return result, nativeMissing(err, factoryservice.ErrPullFilesMissing)
	}
	d := raw.Diff
	if d.Repository != c.Repository || d.Pull.Kind != "pull" || !nativeRemoteID(d.Pull.ExternalID) || d.Pull.Number != number || !collaboration.ValidCommitID(d.Base) || !collaboration.ValidCommitID(d.Head) || !collaboration.ValidCommitID(d.PullBase) || d.BaseBranch == "" || q.Base != "" && (d.Base != q.Base || d.Head != q.Head) || raw.Items == nil || raw.Page != c.Page || raw.Limit != c.Limit || raw.Total < 0 || raw.Total > 3000 {
		return result, errNativeRead
	}
	start := (c.Page - 1) * c.Limit
	if start >= raw.Total && !(c.Page == 1 && raw.Total == 0) {
		return result, errNativeRead
	}
	count := min(c.Limit, raw.Total-start)
	next := 0
	if start+count < raw.Total {
		next = c.Page + 1
	}
	if len(raw.Items) != count || raw.NextPage != next {
		return result, errNativeRead
	}
	for _, file := range raw.Items {
		if file.Diff != (collaboration.DiffRef{}) {
			return result, errNativeRead
		}
	}
	if _, err := s.mapRead(ctx, d.Repository, d.Name, nil, nil); err != nil {
		return result, err
	}
	diff := collaboration.DiffRef{Pull: collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: d.Pull.ExternalID, Number: number}, Base: d.Base, Head: d.Head}
	for index := range raw.Items {
		raw.Items[index].Diff = diff
	}
	c.Base, c.Head = string(d.Base), string(d.Head)
	result = collaboration.PullFiles{Diff: diff, Files: nativePage(s.observation(raw), raw.Items, cursorEncode(c, next))}
	return result, nil
}

type nativeCheckRun struct {
	ExternalID         string                 `json:"external_id"`
	Name               string                 `json:"name"`
	Head               collaboration.CommitID `json:"head"`
	ProviderStatus     *string                `json:"provider_status"`
	ProviderConclusion *string                `json:"provider_conclusion"`
	State              string                 `json:"state"`
	Conclusion         string                 `json:"conclusion"`
	DetailsURL         *string                `json:"details_url"`
	StartedAt          *string                `json:"started_at"`
	CompletedAt        *string                `json:"completed_at"`
	App                *struct {
		ExternalID string  `json:"external_id"`
		Slug       *string `json:"slug"`
		Name       *string `json:"name"`
	} `json:"app"`
	Output *struct {
		Title            *string `json:"title"`
		Summary          *string `json:"summary"`
		Text             *string `json:"text"`
		AnnotationsCount *int64  `json:"annotations_count"`
	} `json:"output"`
}
type nativeCommitStatus struct {
	ExternalID    string                       `json:"external_id"`
	Head          collaboration.CommitID       `json:"head"`
	Context       *string                      `json:"context"`
	ProviderState *string                      `json:"provider_state"`
	State         string                       `json:"state"`
	CheckState    string                       `json:"check_state"`
	Conclusion    string                       `json:"conclusion"`
	Description   *string                      `json:"description"`
	TargetURL     *string                      `json:"target_url"`
	Creator       *collaboration.ObservedActor `json:"creator"`
	CreatedAt     *string                      `json:"created_at"`
	UpdatedAt     *string                      `json:"updated_at"`
}
type nativeCheckPage struct {
	Repository string                 `json:"repository"`
	Name       string                 `json:"name"`
	Pull       nativeSubjectRef       `json:"pull"`
	Head       collaboration.CommitID `json:"head"`
	Items      []json.RawMessage      `json:"items"`
	NextPage   int                    `json:"next_page"`
	Total      *int64                 `json:"total,omitempty"`
}

func (s nativeCollaborationReads) ReadChecks(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.ChecksQuery) (collaboration.PullChecks, error) {
	var result collaboration.PullChecks
	if q.Validate() != nil || !nativeIndex(number) {
		return result, factoryservice.ErrChecksQuery
	}
	if err := s.admitRead(ctx, actor, repo, factoryservice.ErrChecksMissing); err != nil {
		return result, err
	}
	if q.Kind == "job" {
		return result, factoryservice.ErrChecksUnavailable
	}
	kind := "pull_check_runs"
	if q.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil || len(q.Cursor) > 4096 {
			return result, factoryservice.ErrChecksQuery
		}
		var supplied nativeCursor
		if nativeDecode(data, &supplied) != nil || !(supplied.Kind == kind || supplied.Kind == "pull_commit_statuses") {
			return result, factoryservice.ErrChecksQuery
		}
		kind = supplied.Kind
	}
	c, err := cursorDecode(q.Cursor, nativeCursor{Kind: kind, Repository: s.identity.SelectedRepository, Number: number, Head: string(q.Head), Limit: q.Limit})
	if err != nil {
		return result, factoryservice.ErrChecksQuery
	}
	var raw nativeCheckPage
	var items []collaboration.Check
	var actors []collaboration.ObservedActor
	for {
		head := c.Head
		if err = s.read(ctx, nativeRead{Kind: c.Kind, Repository: c.Repository, Number: uint32(number), Page: uint32(c.Page), Limit: uint32(c.Limit), Head: &head}, &raw); err != nil {
			var failure *NativePortError
			if errors.As(err, &failure) && failure.Status == 409 {
				return result, factoryservice.ErrChecksChanged
			}
			return result, nativeMissing(err, factoryservice.ErrChecksMissing)
		}
		if raw.Repository != c.Repository || raw.Pull.Kind != "pull" || raw.Pull.Number != number || !nativeRemoteID(raw.Pull.ExternalID) || !collaboration.ValidCommitID(raw.Head) || c.Head != "" && c.Head != string(raw.Head) || raw.Items == nil || !validNativePage(raw.NextPage, c.Page, c.Limit, len(raw.Items), false) || raw.Total != nil && *raw.Total < 0 {
			return result, errNativeRead
		}
		c.Head = string(raw.Head)
		if c.Kind == "pull_check_runs" && len(raw.Items) == 0 && raw.NextPage == 0 {
			c.Kind = "pull_commit_statuses"
			c.Page = 1
			raw = nativeCheckPage{}
			continue
		}
		break
	}
	seen := map[string]bool{}
	for _, encoded := range raw.Items {
		item := collaboration.Check{Repository: repo, Commit: raw.Head, Kind: "status"}
		if c.Kind == "pull_check_runs" {
			var check nativeCheckRun
			if nativeDecode(encoded, &check) != nil || !nativeRemoteID(check.ExternalID) || check.Head != raw.Head || check.Name == "" {
				return result, errNativeRead
			}
			item.ExternalID = "check:" + check.ExternalID
			item.Name = check.Name
			item.ProviderState = deref(check.ProviderStatus)
			item.State = check.State
			item.Conclusion = check.Conclusion
			item.DetailsURL = deref(check.DetailsURL)
			if check.Output != nil {
				item.Description = deref(check.Output.Summary)
			}
		} else {
			var status nativeCommitStatus
			if nativeDecode(encoded, &status) != nil || !nativeRemoteID(status.ExternalID) || status.Head != raw.Head || deref(status.Context) == "" {
				return result, errNativeRead
			}
			item.ExternalID = "status:" + status.ExternalID
			item.Name = *status.Context
			item.ProviderState = deref(status.ProviderState)
			item.State = status.CheckState
			item.Conclusion = status.Conclusion
			item.Description = deref(status.Description)
			item.DetailsURL = deref(status.TargetURL)
			actors = appendNativeActor(actors, status.Creator)
		}
		if seen[item.ExternalID] {
			return result, errNativeRead
		}
		seen[item.ExternalID] = true
		item.Observation = s.observation(encoded)
		items = append(items, item)
	}
	if _, err := s.mapRead(ctx, raw.Repository, raw.Name, actors, nil); err != nil {
		return result, err
	}
	next := raw.NextPage
	if c.Kind == "pull_check_runs" && next == 0 {
		c.Kind = "pull_commit_statuses"
		next = 1
	}
	obs := s.observation(raw)
	result = collaboration.PullChecks{Kind: "status", Pull: collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: raw.Pull.ExternalID, Number: number}, Commit: raw.Head, Observation: obs,
		Checks: nativePage(obs, items, cursorEncode(c, next)), MissingRequired: []string{}, InvalidRequired: []string{}, StatusGate: "unknown"}
	// A complete observation page is not evidence about unobserved branch rules,
	// workflow jobs, requiredness, merge eligibility or a successful CI gate.
	return result, nil
}
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
