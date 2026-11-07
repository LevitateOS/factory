package service

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// WorkerSource binds file selection to the admitted assignment. Native code
// reads Git objects in its existing isolated preparation context service.
type WorkerSource struct {
	Store  *store.Store
	Reader execution.PreparationContextReader
}

func (s WorkerSource) context(ctx context.Context, admitted factory.MCPCapability, paths []string, directory *string) (execution.PreparationContext, error) {
	var empty execution.PreparationContext
	if s.Store == nil || s.Reader == nil {
		return empty, ErrWorkerUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	admission := WorkerAdmission{Store: s.Store}
	cap, assignment, run, err := admission.current(bounded, admitted)
	if err != nil {
		return empty, err
	}
	if cap.State != factory.MCPCapabilityActive || assignment.Stage != factory.AssignmentAssigned {
		return empty, ErrWorkerBinding
	}
	prep, err := s.Store.Preparation(bounded, assignment.Preparation)
	if err != nil {
		return empty, err
	}
	if prep.Preparation.Project != assignment.ProjectID || prep.Preparation.Role != assignment.Role || prep.Preparation.SourceCommit != assignment.SourceCommit || !prep.State.Ready || prep.State.Stopped || (prep.Native == nil) != (run.Native == nil) {
		return empty, ErrWorkerBinding
	}
	if prep.Native != nil && (prep.Native.Profile != run.Native.Provenance.Profile || prep.Native.InstanceID != run.Native.Provenance.InstanceID || prep.Native.WorktreeID != run.Native.Provenance.WorktreeID) {
		return empty, ErrWorkerBinding
	}
	deadline, _ := bounded.Deadline()
	if !run.Deadline.IsZero() && run.Deadline.Before(deadline) {
		deadline = run.Deadline
	}
	source := assignment.SourceCommit
	request := execution.PreparationContextRequest{Role: assignment.Role, Native: prep.Native,
		Input: project.FactoryPreparationContextRequest{Project: assignment.ProjectID, ID: assignment.Preparation,
			SourceCommit: source, ApprovedBase: source, DiffBase: source, Candidate: source, Paths: paths, Directory: directory, NotAfter: deadline}}
	if err := request.Validate(); err != nil {
		return empty, err
	}
	material, err := s.Reader.ReadWorkspaceContext(bounded, request)
	if err != nil {
		return empty, err
	}
	if err := material.ValidateFor(request); err != nil {
		return empty, err
	}
	if _, _, _, err := admission.current(bounded, cap); err != nil {
		return empty, err
	}
	return material, nil
}

func sourceResult(material execution.PreparationContext) map[string]any {
	files := make([]map[string]string, 0, len(material.Material.Files))
	for _, file := range material.Material.Files {
		files = append(files, map[string]string{"path": file.Path, "content": string(file.Content)})
	}
	return map[string]any{"source_commit": material.Material.SourceCommit, "files": files}
}

func (s WorkerSource) Read(ctx context.Context, admitted factory.MCPCapability, path string) (map[string]any, error) {
	material, err := s.context(ctx, admitted, []string{path}, nil)
	if err != nil {
		return nil, err
	}
	for _, file := range material.Material.Files {
		if file.Path == path {
			return sourceResult(material), nil
		}
	}
	return nil, errors.New("source file not found at assigned commit")
}

func (s WorkerSource) List(ctx context.Context, admitted factory.MCPCapability, path string) (map[string]any, error) {
	material, err := s.context(ctx, admitted, []string{}, &path)
	if err != nil {
		return nil, err
	}
	result := sourceResult(material)
	result["directory"], result["entries"] = path, material.Material.Entries
	if material.Material.Entries == nil {
		result["entries"] = []project.FactoryContextEntry{}
	}
	return result, nil
}
