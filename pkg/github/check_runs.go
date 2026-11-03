package github

import (
	"context"
	"strconv"
	"time"

	sdk "github.com/google/go-github/v93/github"
	"github.com/levitateos/factory/pkg/collaboration"
)

// Provider-local check-run observations. A check-run ID is not an Actions job
// or workflow-run ID. Status requirements and merge authority are separate.
type CheckRunRecord struct {
	ExternalID         string       `json:"external_id"`
	Name               string       `json:"name"`
	Head               string       `json:"head"`
	ProviderStatus     *string      `json:"provider_status"`
	ProviderConclusion *string      `json:"provider_conclusion"`
	State              string       `json:"state"`
	Conclusion         string       `json:"conclusion"`
	DetailsURL         *string      `json:"details_url"`
	StartedAt          *time.Time   `json:"started_at"`
	CompletedAt        *time.Time   `json:"completed_at"`
	App                *CheckApp    `json:"app"`
	Output             *CheckOutput `json:"output"`
}
type CheckApp struct {
	ExternalID string  `json:"external_id"`
	Slug       *string `json:"slug"`
	Name       *string `json:"name"`
}
type CheckOutput struct {
	Title            *string `json:"title"`
	Summary          *string `json:"summary"`
	Text             *string `json:"text"`
	AnnotationsCount *int    `json:"annotations_count"`
}
type CheckRunPage struct {
	Repository string            `json:"repository"`
	Name       string            `json:"name"`
	Pull       DiscussionSubject `json:"pull"`
	Head       string            `json:"head"`
	Items      []CheckRunRecord  `json:"items"`
	Total      *int              `json:"total"` // Provider window, not an all-time history count.
	NextPage   int               `json:"next_page"`
}

func validCheckHeadQuery(page, limit int, head string) bool {
	return pageQuery(page, limit) && (head == "" && page == 1 || collaboration.ValidCommitID(collaboration.CommitID(head)))
}

// PullCheckRuns reads one live provider page for a captured current PR head.
// Continuations require that full head; checks can still change on that head.
func (c *Client) PullCheckRuns(ctx context.Context, repository int64, number, page, limit int, head string) (CheckRunPage, error) {
	if repository < 1 || number < 1 || !validCheckHeadQuery(page, limit, head) {
		return CheckRunPage{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return CheckRunPage{}, err
	}
	before, err := c.checkHead(ctx, repository, owner, name, number)
	if err != nil {
		return CheckRunPage{}, err
	}
	if head != "" && head != before.Head {
		return CheckRunPage{}, ErrChecksChanged
	}
	filter := "all"
	checks, response, err := c.sdk.Checks.ListCheckRunsForRef(ctx, owner, name, before.Head, &sdk.ListCheckRunsOptions{Filter: &filter, ListOptions: sdk.ListOptions{Page: page, PerPage: limit}})
	info, err := result(ctx, response, err)
	if err != nil {
		return CheckRunPage{}, err
	}
	if checks == nil || checks.CheckRuns == nil || len(checks.CheckRuns) > limit || checks.Total != nil && *checks.Total < 0 || info.NextPage != 0 && (len(checks.CheckRuns) == 0 || info.NextPage <= page || info.NextPage > 1_000_000) {
		return CheckRunPage{}, ErrInvalidResponse
	}
	r := CheckRunPage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Pull: before.Pull, Head: before.Head, Items: make([]CheckRunRecord, 0, len(checks.CheckRuns)), Total: checks.Total, NextPage: info.NextPage}
	seen := map[int64]bool{}
	for _, check := range checks.CheckRuns {
		if check == nil || check.GetID() < 1 || seen[check.GetID()] || check.GetName() == "" || check.GetHeadSHA() != before.Head || check.GetURL() != "https://api.github.com/repos/"+owner+"/"+name+"/check-runs/"+strconv.FormatInt(check.GetID(), 10) {
			return CheckRunPage{}, ErrInvalidResponse
		}
		seen[check.GetID()] = true
		record := CheckRunRecord{ExternalID: strconv.FormatInt(check.GetID(), 10), Name: check.GetName(), Head: before.Head, ProviderStatus: check.Status, ProviderConclusion: check.Conclusion, State: "unknown", Conclusion: "unknown", DetailsURL: check.DetailsURL, StartedAt: issueTime(check.StartedAt), CompletedAt: issueTime(check.CompletedAt)}
		switch check.GetStatus() {
		case "queued", "waiting", "requested", "pending":
			record.State = "pending"
		case "in_progress":
			record.State = "running"
		case "completed":
			record.State = "completed"
		}
		switch check.GetConclusion() {
		case "success", "failure", "neutral", "skipped", "cancelled", "timed_out", "action_required", "stale":
			record.Conclusion = check.GetConclusion()
		}
		if app := check.App; app != nil {
			if app.GetID() < 1 {
				return CheckRunPage{}, ErrInvalidResponse
			}
			record.App = &CheckApp{ExternalID: strconv.FormatInt(app.GetID(), 10), Slug: app.Slug, Name: app.Name}
		}
		if output := check.Output; output != nil {
			if output.AnnotationsCount != nil && *output.AnnotationsCount < 0 {
				return CheckRunPage{}, ErrInvalidResponse
			}
			record.Output = &CheckOutput{Title: output.Title, Summary: output.Summary, Text: output.Text, AnnotationsCount: output.AnnotationsCount}
		}
		r.Items = append(r.Items, record)
	}
	after, err := c.checkHead(ctx, repository, owner, name, number)
	if err != nil {
		return CheckRunPage{}, err
	}
	if after != before {
		return CheckRunPage{}, ErrChecksChanged
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return CheckRunPage{}, err
	}
	return r, nil
}

type checkHead struct {
	Pull DiscussionSubject
	Head string
}

func (c *Client) checkHead(ctx context.Context, repository int64, owner, name string, number int) (checkHead, error) {
	pull, response, err := c.sdk.PullRequests.Get(ctx, owner, name, number)
	if _, err := result(ctx, response, err); err != nil {
		return checkHead{}, err
	}
	if !validPull(pull, repository, owner, name) || pull.GetNumber() != number {
		return checkHead{}, ErrInvalidResponse
	}
	if pull.Head == nil || !collaboration.ValidCommitID(collaboration.CommitID(pull.Head.GetSHA())) {
		return checkHead{}, ErrUnavailable
	}
	return checkHead{Pull: DiscussionSubject{Kind: "pull", ExternalID: strconv.FormatInt(pull.GetID(), 10), Number: number}, Head: pull.Head.GetSHA()}, nil
}
