package control

import (
	"context"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"

	"github.com/levitateos/factory/pkg/project"
)

var repositoryPathReference = regexp.MustCompile(`(?:[A-Za-z0-9._-]+/)+[A-Za-z0-9._-]+|[A-Za-z0-9_-]+\.(?:can|go|rs|py|js|ts|tsx|jsx|java|kt|c|h|cpp|md|toml|yaml|yml|json|sh|sql|html|css|lock)\b`)

func readRepositoryContext(ctx context.Context, host DispatchHost, plan *attemptPlan, record execution.PreparationRecord, role string, approvedBase, diffBase, candidate string, requirePreparedCandidate bool, sourceRun *project.FactoryExport) (*project.FactoryPreparationContext, error) {
	preparation := record.Preparation
	if host == nil || plan == nil || record.Validate() != nil || plan.projectID != preparation.Project || role != preparation.Role || !time.Now().Before(plan.deadline) {
		return nil, errors.New("native preparation context reader unavailable")
	}
	if requirePreparedCandidate && role == project.RoleCoder && candidate != preparation.SourceCommit {
		return nil, errors.New("initial candidate differs from its prepared source")
	}
	paths, err := acceptedRepositoryPathReferences(plan)
	if err != nil {
		return nil, err
	}
	request := project.FactoryPreparationContextRequest{
		Project: plan.projectID, ID: preparation.ID,
		SourceCommit: preparation.SourceCommit, ApprovedBase: approvedBase,
		DiffBase: diffBase, Candidate: candidate,
		Paths: paths, NotAfter: plan.deadline,
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithDeadline(ctx, plan.deadline)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	scoped := execution.PreparationContextRequest{Input: request, Role: role, Native: record.Native, SourceRun: sourceRun}
	result, err := host.ReadWorkspaceContext(bounded, scoped)
	if err != nil {
		return nil, err
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	if err = result.ValidateFor(scoped); err != nil {
		return nil, err
	}
	return &result.Material, nil
}

func acceptedRepositoryPathReferences(plan *attemptPlan) ([]string, error) {
	if plan == nil {
		return nil, errors.New("repository context path scope unavailable")
	}
	var texts []string
	texts = append(texts, plan.inputs.Issue.Title, plan.inputs.Issue.Body)
	for _, comment := range plan.inputs.Comments {
		texts = append(texts, comment.Content)
	}
	seen := make(map[string]bool)
	var paths []string
	for _, text := range texts {
		for _, candidate := range repositoryPathReference.FindAllString(text, -1) {
			if strings.HasPrefix(candidate, "../") || strings.HasPrefix(candidate, "./") || strings.HasPrefix(candidate, "/") {
				continue
			}
			clean := path.Clean(candidate)
			if clean == "." || strings.HasPrefix(clean, "../") || seen[clean] {
				continue
			}
			seen[clean] = true
			paths = append(paths, clean)
		}
	}
	sort.Strings(paths)
	if len(paths) > project.MaxFactoryContextPaths {
		return nil, errors.New("accepted repository path references exceed the context bound")
	}
	return paths, nil
}
