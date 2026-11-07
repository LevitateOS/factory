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

func (s WorkerSource) Read(ctx context.Context, admitted factory.MCPCapability, path string) (map[string]any, error) {
	if s.Store == nil || s.Reader == nil {
		return nil, ErrWorkerUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	admission := WorkerAdmission{Store: s.Store}
	cap, assignment, run, err := admission.current(bounded, admitted)
	if err != nil {
		return nil, err
	}
	if cap.State != factory.MCPCapabilityActive || assignment.Stage != factory.AssignmentAssigned {
		return nil, ErrWorkerBinding
	}
	prep, err := s.Store.Preparation(bounded, assignment.Preparation)
	if err != nil {
		return nil, err
	}
	if prep.Preparation.Project != assignment.ProjectID || prep.Preparation.Role != assignment.Role || prep.Preparation.SourceCommit != assignment.SourceCommit || !prep.State.Ready || prep.State.Stopped || (prep.Native == nil) != (run.Native == nil) {
		return nil, ErrWorkerBinding
	}
	if prep.Native != nil && (prep.Native.Profile != run.Native.Provenance.Profile || prep.Native.InstanceID != run.Native.Provenance.InstanceID || prep.Native.WorktreeID != run.Native.Provenance.WorktreeID) {
		return nil, ErrWorkerBinding
	}
	deadline, _ := bounded.Deadline()
	if !run.Deadline.IsZero() && run.Deadline.Before(deadline) {
		deadline = run.Deadline
	}
	source := assignment.SourceCommit
	request := execution.PreparationContextRequest{Role: assignment.Role, Native: prep.Native,
		Input: project.FactoryPreparationContextRequest{Project: assignment.ProjectID, ID: assignment.Preparation,
			SourceCommit: source, ApprovedBase: source, DiffBase: source, Candidate: source, Paths: []string{path}, NotAfter: deadline}}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	material, err := s.Reader.ReadWorkspaceContext(bounded, request)
	if err != nil {
		return nil, err
	}
	if err := material.ValidateFor(request); err != nil {
		return nil, err
	}
	files := make([]map[string]string, 0, len(material.Material.Files))
	found := false
	for _, file := range material.Material.Files {
		found = found || file.Path == path
		files = append(files, map[string]string{"path": file.Path, "content": string(file.Content)})
	}
	if !found {
		return nil, errors.New("source file not found at assigned commit")
	}
	if _, _, _, err := admission.current(bounded, cap); err != nil {
		return nil, err
	}
	return map[string]any{"source_commit": source, "files": files}, nil
}
