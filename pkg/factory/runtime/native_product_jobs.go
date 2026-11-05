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
	Kind         string                 `json:"kind"`
	Repository   string                 `json:"repository"`
	Number       int64                  `json:"number"`
	Head         collaboration.CommitID `json:"head"`
	Base         collaboration.CommitID `json:"base"`
	Branch       string                 `json:"branch"`
	Limit        int                    `json:"limit"`
	RunPage      int                    `json:"run_page"`
	JobPage      int                    `json:"job_page"`
	Run          string                 `json:"run"`
	Attempt      string                 `json:"attempt"`
	Total        int64                  `json:"total"`
	JobTotal     int64                  `json:"job_total"`
	PreviousJobs []string               `json:"previous_jobs"`
	PreviousRun  string                 `json:"previous_run"`
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
	stage, started := "cursor", time.Now()
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
	c := nativeJobCursor{Kind: "workflow_jobs", Repository: s.identity.SelectedRepository, Number: number, Head: q.Head, Limit: q.Limit, RunPage: 1, JobPage: 1}
	if q.Cursor != "" {
		encoded, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		var supplied nativeJobCursor
		if err != nil || len(q.Cursor) > 4096 || nativeDecode(encoded, &supplied) != nil || supplied.Kind != c.Kind || supplied.Repository != c.Repository || supplied.Number != number || supplied.Head != c.Head || !collaboration.ValidCommitID(supplied.Base) || !collaboration.ValidText(supplied.Branch, 4096) || supplied.Limit != q.Limit || supplied.RunPage < 1 || supplied.RunPage > 1000 || supplied.JobPage < 1 || supplied.JobPage > 1_000_000 || supplied.Total < 1 || supplied.Total > 1000 || supplied.JobTotal < 0 || len(supplied.PreviousJobs) > q.Limit || supplied.PreviousRun != "" && !nativeRemoteID(supplied.PreviousRun) || supplied.RunPage > 1 && supplied.PreviousRun == "" ||
			!(supplied.Run == "" && supplied.Attempt == "" && supplied.JobPage == 1 && supplied.RunPage > 1 && supplied.JobTotal == 0 && len(supplied.PreviousJobs) == 0 || nativeRemoteID(supplied.Run) && nativeRemoteID(supplied.Attempt) && supplied.JobPage > 1 && supplied.JobTotal > 0 && len(supplied.PreviousJobs) > 0) {
			return empty, factoryservice.ErrChecksQuery
		}
		seen := map[string]bool{}
		for _, id := range supplied.PreviousJobs {
			if !nativeRemoteID(id) || seen[id] {
				return empty, factoryservice.ErrChecksQuery
			}
			seen[id] = true
		}
		c = supplied
	}
	head := string(c.Head)
	stage = "workflow_runs"
	type workflowPage struct {
		Repository string                 `json:"repository"`
		Name       string                 `json:"name"`
		Head       collaboration.CommitID `json:"head"`
		Items      []nativeWorkflow       `json:"items"`
		NextPage   int                    `json:"next_page"`
		Total      int64                  `json:"total"`
	}
	type jobPage struct {
		Repository string         `json:"repository"`
		Name       string         `json:"name"`
		Run        nativeWorkflow `json:"run"`
		Items      []nativeJob    `json:"items"`
		NextPage   int            `json:"next_page"`
		Total      int64          `json:"total"`
	}
	var raw struct {
		Repository string                 `json:"repository"`
		Name       string                 `json:"name"`
		Pull       nativeSubjectRef       `json:"pull"`
		Head       collaboration.CommitID `json:"head"`
		Base       collaboration.CommitID `json:"base"`
		Branch     string                 `json:"branch"`
		Runs       workflowPage           `json:"runs"`
		Jobs       *jobPage               `json:"jobs"`
	}
	if err := s.read(ctx, nativeRead{Kind: "pull_workflow_jobs", Repository: c.Repository, Number: uint32(number), Page: uint32(c.JobPage), RunPage: uint32(c.RunPage), Limit: uint32(c.Limit), RunID: c.Run, Attempt: c.Attempt, Head: &head}, &raw); err != nil {
		return empty, jobReadError(err)
	}
	if raw.Repository != c.Repository || raw.Pull.Kind != "pull" || raw.Pull.Number != number || !nativeRemoteID(raw.Pull.ExternalID) || !collaboration.ValidCommitID(raw.Head) || !collaboration.ValidCommitID(raw.Base) || !collaboration.ValidText(raw.Branch, 4096) || c.Head != "" && raw.Head != c.Head {
		return empty, errNativeRead
	}
	c.Head = raw.Head
	if c.Base != "" && (c.Base != raw.Base || c.Branch != raw.Branch) {
		return empty, factoryservice.ErrChecksChanged
	}
	c.Base, c.Branch = raw.Base, raw.Branch
	runs := raw.Runs
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
		if !nativeRemoteID(run.ExternalID) || !nativeRemoteID(run.RunNumber) || !nativeRemoteID(run.Attempt) || !nativeRemoteID(run.WorkflowID) || run.Head != c.Head || run.Path == "" || run.Event == "" || c.Run != "" && (c.Run != run.ExternalID || c.Attempt != run.Attempt) || c.PreviousRun == run.ExternalID {
			return empty, factoryservice.ErrChecksChanged
		}
		jobs := raw.Jobs
		stage = "job_binding"
		if jobs == nil || jobs.Repository != c.Repository || jobs.Name != runs.Name || !sameNativeWorkflow(jobs.Run, run) || jobs.Items == nil || !validNativePage(jobs.NextPage, c.JobPage, c.Limit, len(jobs.Items), false) || jobs.Total < int64(len(jobs.Items)) || c.JobPage > 1 && jobs.Total != c.JobTotal {
			return empty, factoryservice.ErrChecksChanged
		}
		run = jobs.Run
		stage = "job_projection"
		seen := map[string]bool{}
		previous := map[string]bool{}
		for _, id := range c.PreviousJobs {
			previous[id] = true
		}
		previousJobs := []string{}
		for _, job := range jobs.Items {
			if previous[job.ExternalID] {
				return empty, factoryservice.ErrChecksChanged
			}
			if !nativeRemoteID(job.ExternalID) || seen[job.ExternalID] || job.RunID != run.ExternalID || job.Attempt != run.Attempt || job.Head != c.Head || job.Name == "" {
				return empty, errNativeRead
			}
			seen[job.ExternalID] = true
			previousJobs = append(previousJobs, job.ExternalID)
			items = append(items, collaboration.Check{Repository: repo, Commit: c.Head, Kind: "job", ExternalID: "job:" + job.ExternalID, Name: job.Name, Observation: s.observation(job),
				ProviderState: deref(job.ProviderStatus), State: job.State, Conclusion: job.Conclusion, DetailsURL: deref(job.DetailsURL),
				Job: &collaboration.CheckJob{RunID: run.ExternalID, RunNumber: run.RunNumber, Workflow: run.Path, Ref: deref(run.HeadBranch), Event: run.Event, RunState: deref(run.ProviderStatus), JobID: job.ExternalID, Attempt: run.Attempt, StartedAt: job.StartedAt, StoppedAt: job.CompletedAt}})
		}
		if jobs.NextPage != 0 {
			c.JobPage, c.Run, c.Attempt = jobs.NextPage, run.ExternalID, run.Attempt
			c.JobTotal, c.PreviousJobs = jobs.Total, previousJobs
		} else if runs.NextPage != 0 {
			c.RunPage, c.JobPage, c.Run, c.Attempt = runs.NextPage, 1, "", ""
			c.PreviousRun, c.JobTotal, c.PreviousJobs = run.ExternalID, 0, nil
		}
		if jobs.NextPage != 0 || runs.NextPage != 0 {
			encoded, _ := json.Marshal(c)
			next = base64.RawURLEncoding.EncodeToString(encoded)
			if len(next) > 4096 {
				return empty, factoryservice.ErrChecksTooLarge
			}
		}
		jobFacts = jobs
	} else if raw.Jobs != nil {
		return empty, errNativeRead
	}
	stage = "mapping"
	if _, err := s.mapRead(ctx, runs.Repository, runs.Name, nil, nil); err != nil {
		return empty, err
	}
	obs := s.observation(struct{ Runs, Jobs any }{runs, jobFacts})
	return collaboration.PullChecks{Kind: "job", Pull: collaboration.PullRef{Kind: collaboration.SubjectPull, Repository: repo, ExternalID: raw.Pull.ExternalID, Number: number}, Commit: c.Head, Observation: obs, Checks: nativePage(obs, items, next), MissingRequired: []string{}, InvalidRequired: []string{}, StatusGate: "unknown"}, nil
}
