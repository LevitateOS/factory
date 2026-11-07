package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

type nativeWorkspaceSetup struct {
	*nativeProductAccount
	coordinator *control.Coordinator
}

func setupHead(head string, err error) (string, error) {
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return head, err
}

func (b nativeWorkspaceSetup) setupGrant(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (project.EnvironmentGrant, string, error) {
	var empty project.EnvironmentGrant
	if err := b.localResourceAccess(ctx, actor, repository); err != nil {
		return empty, "", err
	}
	grant, err := b.store.EnvironmentGrant(ctx, int64(repository))
	if err != nil {
		return empty, "", err
	}
	workspace, native, err := b.store.ExecutionWorkspace(ctx, repository)
	if err != nil || native == nil || native.Profile != b.port.operation.config.Profile || grant.Project != workspace {
		return empty, "", execution.ErrPreparation
	}
	observed, err := b.repositoryObservation(ctx, actor, repository)
	if err != nil {
		return empty, "", err
	}
	target := "refs/heads/" + observed.DefaultBranch
	policy, err := b.store.RepositoryPolicy(ctx, int64(repository))
	if err == nil {
		target = policy.TargetBranch
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, "", err
	}
	if !factory.ValidTargetBranch(target) {
		return empty, "", execution.ErrPreparation
	}
	return grant, target, nil
}

type nativeSetupSource struct {
	Repository  string            `json:"repository"`
	Commit      string            `json:"commit"`
	Target      string            `json:"target_ref"`
	Files       map[string][]byte `json:"files"`
	SourceFiles map[string][]byte `json:"source_files"`
}

func setupPaths(inputs map[string]string) []string {
	paths := make([]string, 0, len(inputs))
	for name := range inputs {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

func (b nativeWorkspaceSetup) setupSource(ctx context.Context, commit, target string, paths []string) (nativeSetupSource, error) {
	var source nativeSetupSource
	if len(paths) > project.MaxSourceInputs {
		return source, execution.ErrPreparation
	}
	selected := make(map[string]bool, len(paths))
	for _, name := range paths {
		if !project.ValidFactoryContextPath(name) || !utf8.ValidString(name) || selected[name] {
			return source, execution.ErrPreparation
		}
		selected[name] = true
	}
	if err := b.read(ctx, nativeRead{Kind: "setup_source", Repository: b.repository.ExternalID, Commit: commit, TargetRef: target, Paths: paths}, &source); err != nil {
		return source, err
	}
	if source.Repository != b.repository.ExternalID || source.Commit != commit || source.Target != target {
		return source, errNativeReadScope
	}
	total := 0
	for name, contents := range source.SourceFiles {
		total += len(contents)
		if !selected[name] || len(contents) > project.MaxFactoryContextFileBytes || !utf8.Valid(contents) {
			return source, errNativeRead
		}
	}
	if total > project.MaxFactoryContextBytes {
		return source, errNativeRead
	}
	return source, nil
}

func (b nativeWorkspaceSetup) ReadSetup(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, paths []string) (factoryservice.WorkspaceSetup, error) {
	view := factoryservice.WorkspaceSetup{Files: map[string]string{}, SourceFiles: map[string]string{}, SourceInputs: map[string]string{}, SelectedPaths: []string{}, MissingPaths: []string{}, Tools: []string{"git", "bash"}, Preparations: []factoryservice.WorkspaceSetupPreparation{}}
	if err := b.localResourceAccess(ctx, actor, repository); err != nil {
		return view, err
	}
	grant, target, err := b.setupGrant(ctx, actor, repository)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, execution.ErrPreparation) || errors.Is(err, store.ErrAdmissionChanged) || errors.Is(err, factory.ErrPlacement) {
		view.Reason = "Select this computer as controller and allow isolated workspaces first."
		return view, nil
	}
	if err != nil {
		return view, err
	}
	view.EnvironmentRevision, view.Target = grant.Revision, target
	view.Requirement, err = setupHead(b.store.RequirementHead(ctx, grant.Project))
	if err != nil {
		return view, err
	}
	view.Approval, err = setupHead(b.store.ApprovalHead(ctx, grant.Project))
	if err != nil {
		return view, err
	}
	var saved *project.SetupRecipe
	if view.Requirement != "" {
		decision, err := b.store.RequirementDecision(ctx, view.Requirement)
		if err != nil {
			return view, err
		}
		saved = decision.Recipe
		if paths == nil && len(decision.SourceInputs) != 0 {
			paths = setupPaths(decision.SourceInputs)
		}
	}
	if paths == nil {
		paths = []string{"README.md"}
	}
	tip, err := b.targetTip(ctx, target)
	if err != nil {
		return view, err
	}
	source, err := b.setupSource(ctx, tip.Tip, target, paths)
	if err != nil {
		return view, err
	}
	view.SelectedPaths = append([]string{}, paths...)
	for _, name := range paths {
		contents, exists := source.SourceFiles[name]
		if !exists {
			view.MissingPaths = append(view.MissingPaths, name)
			continue
		}
		view.SourceFiles[name] = string(contents)
		sum := sha256.Sum256(contents)
		view.SourceInputs[name] = hex.EncodeToString(sum[:])
	}
	for name, contents := range source.Files {
		if !project.ValidApprovedName(name) || len(contents) > project.MaxApprovedFileSize || !utf8.Valid(contents) {
			return view, errNativeRead
		}
		view.Files[name] = string(contents)
	}
	if saved != nil {
		view.Files = map[string]string{}
		for name, contents := range saved.Files {
			view.Files[name] = string(contents)
		}
		view.Tools = saved.Tools
		view.SavedRecipe = true
	}
	view.Source, view.Available = tip.Tip, true
	preparations, err := b.store.StandingProjectPreparations(ctx, grant.Project)
	if err != nil {
		return view, err
	}
	for _, preparation := range preparations {
		if preparation.Preparation.Requirements.ID == view.Requirement && preparation.Preparation.Approval.ID == view.Approval {
			current := preparation.Native != nil && preparation.Native.Profile == b.port.operation.config.Profile && preparation.Native.InstanceID == b.port.operation.instance && b.store.AuthorizeStandingPreparation(ctx, preparation) == nil
			if current {
				observed, observeErr := b.coordinator.ObserveStandingPreparation(ctx, preparation)
				if observeErr == nil {
					preparation = observed
				} else {
					current = false
				}
			}
			state := preparation.State
			truncated := len(state.Output) > 32<<10
			if truncated {
				state.Output = state.Output[:32<<10]
				for !utf8.ValidString(state.Output) {
					state.Output = state.Output[:len(state.Output)-1]
				}
			}
			view.Preparations = append(view.Preparations, factoryservice.WorkspaceSetupPreparation{State: state, Current: current, OutputTruncated: truncated})
		}
	}
	return view, b.current(ctx)
}

func (b nativeWorkspaceSetup) ApproveSetup(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, in factoryservice.WorkspaceSetupApproval) (factoryservice.WorkspaceSetup, error) {
	var empty factoryservice.WorkspaceSetup
	if !factory.ValidID(in.ID) || !project.ValidCommit(in.Source) || !factory.ValidTargetBranch(in.Target) || len(in.Tools) > project.MaxPrepareTools || len(in.SourceInputs) == 0 || project.ValidateSourceInputs(in.SourceInputs) != nil {
		return empty, execution.ErrPreparation
	}
	files := make(map[string][]byte, len(in.Files))
	names := make([]string, 0, len(in.Files))
	for name, contents := range in.Files {
		if !utf8.ValidString(contents) {
			return empty, execution.ErrPreparation
		}
		files[name] = []byte(contents)
		names = append(names, name)
	}
	// Only file validation is needed before fetching source; no substitute bundle
	// is recorded or sent to the native owner.
	if err := (project.ApprovedSetup{Files: files}).ValidateFiles(); err != nil {
		return empty, errors.Join(execution.ErrPreparation, err)
	}
	for _, tool := range in.Tools {
		if !project.ValidToolName(tool) {
			return empty, execution.ErrPreparation
		}
	}
	grant, target, err := b.setupGrant(ctx, actor, repository)
	if err != nil {
		return empty, err
	}
	if grant.Revision != in.EnvironmentRevision || target != in.Target {
		return empty, store.ErrStaleRevision
	}
	paths := setupPaths(in.SourceInputs)
	preview, err := b.setupSource(ctx, in.Source, target, paths)
	if err != nil {
		return empty, err
	}
	observedInputs := make(map[string]string, len(preview.SourceFiles))
	for name, contents := range preview.SourceFiles {
		sum := sha256.Sum256(contents)
		observedInputs[name] = hex.EncodeToString(sum[:])
	}
	if !maps.Equal(observedInputs, in.SourceInputs) {
		return empty, store.ErrStaleRevision
	}
	identity := func(prefix, kind string) string {
		sum := sha256.Sum256([]byte("native-workspace-setup/v1:" + string(b.binding.Profile.ID) + ":" + strconv.FormatInt(int64(repository), 10) + ":" + in.ID + ":" + kind))
		return prefix + hex.EncodeToString(sum[:12])
	}
	requirement := project.RequirementDecision{ID: identity("d", "requirements"), Predecessor: in.Requirement, Project: grant.Project, SourceCommit: in.Source, SetupDigest: project.SetupDigestOf(files), InputsDigest: project.PreparationInputsDigestOf(names, in.SourceInputs, in.Tools), SourceInputs: in.SourceInputs, Approver: int64(actor), Native: grant.Native, Recipe: &project.SetupRecipe{Files: files, Tools: in.Tools}}
	// Consent binds the complete intended recipe and execution owner. Actual
	// setup/check exits and worker readiness come only from native observations.
	effects, err := json.Marshal(struct {
		Source       string
		SourceInputs map[string]string
		Files        map[string][]byte
		Tools        []string
		Native       *project.NativeEnvironment
	}{in.Source, in.SourceInputs, files, in.Tools, grant.Native})
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(effects)
	approval := project.ApprovalDecision{ID: identity("d", "approval"), Predecessor: in.Approval, Project: grant.Project, Requirement: requirement.ID, Approver: int64(actor), EffectsDigest: hex.EncodeToString(sum[:]), Reviewed: true, Native: grant.Native}
	account := b.nativeProductAccount
	var source struct {
		Repository string `json:"repository"`
		Base       string `json:"base"`
		Commit     string `json:"commit"`
		Target     string `json:"target"`
		SHA256     string `json:"sha256"`
		Bundle     []byte `json:"bundle"`
	}
	base := ""
	if err = account.read(ctx, nativeRead{Kind: "source_bundle", Repository: account.repository.ExternalID, Base: &base, Commit: in.Source, TargetRef: target}, &source); err != nil {
		return empty, err
	}
	bundleSum := sha256.Sum256(source.Bundle)
	if source.Repository != account.repository.ExternalID || source.Base != "" || source.Commit != in.Source || source.Target != target || len(source.Bundle) == 0 || len(source.Bundle) > project.MaxSourceBundle || source.SHA256 != hex.EncodeToString(bundleSum[:]) {
		return empty, errNativeReadScope
	}
	if err = b.store.AdmitRequirementDecision(ctx, requirement); err != nil {
		return empty, err
	}
	if err = b.store.AdmitApprovalDecision(ctx, approval); err != nil {
		return empty, err
	}
	requirementDepth, err := b.store.RequirementDepth(ctx, grant.Project)
	if err != nil {
		return empty, err
	}
	approvalDepth, err := b.store.ApprovalDepth(ctx, grant.Project)
	if err != nil {
		return empty, err
	}
	for _, role := range []string{project.RoleCoder, project.RoleReviewer} {
		prep := project.Preparation{ID: identity("f", role), Project: grant.Project, Role: role, Requirements: requirement.Ref(requirementDepth), Approval: approval.Ref(approvalDepth), SourceCommit: in.Source, SetupDigest: requirement.SetupDigest, Tools: in.Tools}
		ref := &execution.NativeWorkspaceRef{Profile: b.port.operation.config.Profile, InstanceID: b.port.operation.instance, WorktreeID: prep.ID}
		request := execution.StandingPreparationRequest{Native: ref, Input: project.Prepare{Preparation: prep, Setup: project.ApprovedSetup{Files: files, Bundle: source.Bundle}}}
		_, err = b.coordinator.StartPreparation(ctx, request, func(ctx context.Context) error {
			if err := b.localOwnerAccess(ctx, actor); err != nil {
				return err
			}
			current, err := b.store.EnvironmentGrant(ctx, int64(repository))
			if err != nil {
				return err
			}
			if !current.Active || current.Native == nil || *current.Native != *grant.Native || current.Revision != grant.Revision || current.Project != grant.Project {
				return store.ErrStaleRevision
			}
			if _, _, err = b.store.ExecutionWorkspace(ctx, repository); err != nil {
				return err
			}
			policy, err := b.store.RepositoryPolicy(ctx, int64(repository))
			if err == nil && policy.TargetBranch != target {
				return store.ErrStaleRevision
			}
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			return nil
		})
		if err != nil {
			return empty, err
		}
	}
	return b.ReadSetup(ctx, actor, repository, paths)
}
