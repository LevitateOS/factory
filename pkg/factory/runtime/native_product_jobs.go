package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Boundary facts are decoded by the defining Rust provider. Go validates the
// captured relationships and maps them into the common product records.
type nativeWorkflow struct {
	ExternalID         string                            `json:"external_id"`
	RunNumber          string                            `json:"run_number"`
	Attempt            string                            `json:"attempt"`
	WorkflowID         string                            `json:"workflow_id"`
	Name               *string                           `json:"name"`
	Path               string                            `json:"path"`
	Head               collaboration.CommitID            `json:"head"`
	HeadBranch         *string                           `json:"head_branch"`
	HeadRepository     *collaboration.ObservedRepository `json:"head_repository"`
	Event              string                            `json:"event"`
	ProviderStatus     *string                           `json:"provider_status"`
	ProviderConclusion *string                           `json:"provider_conclusion"`
	State              string                            `json:"state"`
	Conclusion         string                            `json:"conclusion"`
	CreatedAt          *time.Time                        `json:"created_at"`
	UpdatedAt          *time.Time                        `json:"updated_at"`
	StartedAt          *time.Time                        `json:"started_at"`
	Actor              *collaboration.ObservedActor      `json:"actor"`
	TriggeringActor    *collaboration.ObservedActor      `json:"triggering_actor"`
	DetailsURL         *string                           `json:"details_url"`
}
type nativeJob struct {
	ExternalID         string                 `json:"external_id"`
	Name               string                 `json:"name"`
	Head               collaboration.CommitID `json:"head"`
	RunID              string                 `json:"run_id"`
	Attempt            string                 `json:"attempt"`
	ProviderStatus     *string                `json:"provider_status"`
	ProviderConclusion *string                `json:"provider_conclusion"`
	State              string                 `json:"state"`
	Conclusion         string                 `json:"conclusion"`
	StartedAt          *time.Time             `json:"started_at"`
	CompletedAt        *time.Time             `json:"completed_at"`
	DetailsURL         *string                `json:"details_url"`
	CheckRunURL        *string                `json:"check_run_url"`
}
type nativeJobCursor struct {
	Kind       string                 `json:"kind"`
	Repository string                 `json:"repository"`
	Number     int64                  `json:"number"`
	Head       collaboration.CommitID `json:"head"`
	Limit      int                    `json:"limit"`
	RunPage    int                    `json:"run_page"`
	JobPage    int                    `json:"job_page"`
	Run        string                 `json:"run"`
	Attempt    string                 `json:"attempt"`
	Total      int64                  `json:"total"`
}

func (s nativeCollaborationReads) jobPull(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64) (collaboration.PullRequest, error) {
	page, err := s.ReadPulls(ctx, actor, repo, factoryservice.CollaborationPullQuery{Number: number, Limit: 1})
	if err != nil {
		return collaboration.PullRequest{}, nativeMissing(err, factoryservice.ErrChecksMissing)
	}
	if len(page.Items) != 1 {
		return collaboration.PullRequest{}, errNativeRead
	}
	return page.Items[0], nil
}
func jobReadError(err error) error {
	var failure *NativePortError
	if errors.As(err, &failure) {
		if failure.Status == 409 {
			return factoryservice.ErrChecksChanged
		}
		if failure.Status == 413 {
			return factoryservice.ErrChecksTooLarge
		}
	}
	return nativeMissing(err, factoryservice.ErrChecksMissing)
}

func sameNativeWorkflow(a, b nativeWorkflow) bool {
	// List and attempt responses can use different display URLs and times.
	// Compare the captured identity/source, then project the actual attempt's
	// state. Mutable display metadata is not a new run or product authority.
	return a.ExternalID == b.ExternalID && a.RunNumber == b.RunNumber && a.Attempt == b.Attempt && a.WorkflowID == b.WorkflowID && a.Path == b.Path && a.Head == b.Head && a.Event == b.Event && reflect.DeepEqual(a.HeadBranch, b.HeadBranch) && reflect.DeepEqual(a.HeadRepository, b.HeadRepository)
}

func (s nativeCollaborationReads) readJobs(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q factoryservice.ChecksQuery) (_ collaboration.PullChecks, runErr error) {
	stage, started := "pull_before", time.Now()
	defer func() {
		if runErr != nil {
			code, status := "invalid_observation", 0
			var failure *NativePortError
			if errors.As(runErr, &failure) {
				code, status = failure.Code, failure.Status
			} else if ctx.Err() != nil {
				code = "operation_retired_or_deadline"
			} else if errors.Is(runErr, factoryservice.ErrChecksChanged) {
				code = "snapshot_changed"
			} else if errors.Is(runErr, factoryservice.ErrChecksQuery) {
				code = "invalid_cursor"
			}
			// Fixed stage and bounded port code only; no resource, URL, body,
			// credential, SQL error or private provider metadata enters logs.
			log.Printf("native job observation refused: stage=%s code=%s status=%d elapsed_ms=%d", stage, code, status, time.Since(started).Milliseconds())
		}
	}()
	var empty collaboration.PullChecks
	before, err := s.jobPull(ctx, actor, repo, number)
	if err != nil {
		return empty, err
	}
	if before.Head.Commit == nil || !collaboration.ValidCommitID(*before.Head.Commit) || q.Head != "" && q.Head != *before.Head.Commit {
		return empty, factoryservice.ErrChecksChanged
	}
	c := nativeJobCursor{Kind: "workflow_jobs", Repository: s.identity.SelectedRepository, Number: number, Head: *before.Head.Commit, Limit: q.Limit, RunPage: 1, JobPage: 1}
	if q.Cursor != "" {
		encoded, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		var supplied nativeJobCursor
		if err != nil || len(q.Cursor) > 4096 || nativeDecode(encoded, &supplied) != nil || supplied.Kind != c.Kind || supplied.Repository != c.Repository || supplied.Number != number || supplied.Head != c.Head || supplied.Limit != q.Limit || supplied.RunPage < 1 || supplied.RunPage > 1_000_000 || supplied.JobPage < 1 || supplied.JobPage > 1_000_000 || supplied.Total < 0 ||
			!(supplied.Run == "" && supplied.Attempt == "" && supplied.JobPage == 1 || nativeRemoteID(supplied.Run) && nativeRemoteID(supplied.Attempt)) {
			return empty, factoryservice.ErrChecksQuery
		}
		c = supplied
	}
	head := string(c.Head)
	stage = "workflow_runs"
	var runs struct {
		Repository string                 `json:"repository"`
		Name       string                 `json:"name"`
		Head       collaboration.CommitID `json:"head"`
		Items      []nativeWorkflow       `json:"items"`
		NextPage   int                    `json:"next_page"`
		Total      int64                  `json:"total"`
	}
	if err = s.read(ctx, nativeRead{Kind: "workflow_runs", Repository: c.Repository, Page: uint32(c.RunPage), Limit: 1, Head: &head}, &runs); err != nil {
		return empty, jobReadError(err)
	}
	stage = "workflow_binding"
	if runs.Repository != c.Repository || runs.Head != c.Head || runs.Items == nil || !validNativePage(runs.NextPage, c.RunPage, 1, len(runs.Items), false) || runs.Total < int64(len(runs.Items)) || q.Cursor != "" && runs.Total != c.Total || len(runs.Items) == 0 && (c.RunPage != 1 || runs.Total != 0) {
		return empty, factoryservice.ErrChecksChanged
	}
	c.Total = runs.Total
	items := []collaboration.Check{}
	next := ""
	var jobFacts any
	if len(runs.Items) == 1 {
		run := runs.Items[0]
		if !nativeRemoteID(run.ExternalID) || !nativeRemoteID(run.RunNumber) || !nativeRemoteID(run.Attempt) || !nativeRemoteID(run.WorkflowID) || run.Head != c.Head || run.Path == "" || run.Event == "" || c.Run != "" && (c.Run != run.ExternalID || c.Attempt != run.Attempt) {
			return empty, factoryservice.ErrChecksChanged
		}
		var jobs struct {
			Repository string         `json:"repository"`
			Name       string         `json:"name"`
			Run        nativeWorkflow `json:"run"`
			Items      []nativeJob    `json:"items"`
			NextPage   int            `json:"next_page"`
			Total      int64          `json:"total"`
		}
		stage = "workflow_jobs"
		if err = s.read(ctx, nativeRead{Kind: "workflow_run_jobs", Repository: c.Repository, RunID: run.ExternalID, Attempt: run.Attempt, Page: uint32(c.JobPage), Limit: uint32(c.Limit), Head: &head}, &jobs); err != nil {
			return empty, jobReadError(err)
		}
		stage = "job_binding"
		if jobs.Repository != c.Repository || jobs.Name != runs.Name || !sameNativeWorkflow(jobs.Run, run) || jobs.Items == nil || !validNativePage(jobs.NextPage, c.JobPage, c.Limit, len(jobs.Items), false) || jobs.Total < int64(len(jobs.Items)) {
			return empty, factoryservice.ErrChecksChanged
		}
		run = jobs.Run
		stage = "job_projection"
		seen := map[string]bool{}
		for _, job := range jobs.Items {
			if !nativeRemoteID(job.ExternalID) || seen[job.ExternalID] || job.RunID != run.ExternalID || job.Attempt != run.Attempt || job.Head != c.Head || job.Name == "" {
				return empty, errNativeRead
			}
			seen[job.ExternalID] = true
			items = append(items, collaboration.Check{Repository: repo, Commit: c.Head, Kind: "job", ExternalID: "job:" + job.ExternalID, Name: job.Name, Observation: s.observation(job),
				ProviderState: deref(job.ProviderStatus), State: job.State, Conclusion: job.Conclusion, DetailsURL: deref(job.DetailsURL),
				Job: &collaboration.CheckJob{RunID: run.ExternalID, RunNumber: run.RunNumber, Workflow: run.Path, Ref: deref(run.HeadBranch), Event: run.Event, RunState: deref(run.ProviderStatus), JobID: job.ExternalID, Attempt: run.Attempt, StartedAt: job.StartedAt, StoppedAt: job.CompletedAt}})
		}
		if jobs.NextPage != 0 {
			c.JobPage, c.Run, c.Attempt = jobs.NextPage, run.ExternalID, run.Attempt
		} else if runs.NextPage != 0 {
			c.RunPage, c.JobPage, c.Run, c.Attempt = runs.NextPage, 1, "", ""
		}
		if jobs.NextPage != 0 || runs.NextPage != 0 {
			encoded, _ := json.Marshal(c)
			next = base64.RawURLEncoding.EncodeToString(encoded)
		}
		jobFacts = jobs
	}
	stage = "pull_after"
	after, err := s.jobPull(ctx, actor, repo, number)
	if err != nil {
		return empty, err
	}
	if after.Ref != before.Ref || !reflect.DeepEqual(after.Head, before.Head) || !reflect.DeepEqual(after.Base, before.Base) {
		return empty, factoryservice.ErrChecksChanged
	}
	stage = "mapping"
	if _, err = s.mapRead(ctx, runs.Repository, runs.Name, nil, nil); err != nil {
		return empty, err
	}
	obs := s.observation(struct{ Runs, Jobs any }{runs, jobFacts})
	return collaboration.PullChecks{Kind: "job", Pull: before.Ref, Commit: c.Head, Observation: obs, Checks: nativePage(obs, items, next), MissingRequired: []string{}, InvalidRequired: []string{}, StatusGate: "unknown"}, nil
}
