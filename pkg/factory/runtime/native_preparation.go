package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/strictjson"
)

var _ execution.PreparationObserver = NativeExecution{}
var _ execution.PreparationContextReader = NativeExecution{}
var _ execution.PreparationLauncher = NativeExecution{}

func (p NativeExecution) CandidatePreparationAddress(workspace, id string) (execution.PreparationAddress, error) {
	address := execution.PreparationAddress{Workspace: workspace, ID: id, Native: &execution.NativeWorkspaceRef{Profile: p.Config.Profile, InstanceID: p.Instance, WorktreeID: id}}
	if !p.currentPreparationAddress(address) {
		return execution.PreparationAddress{}, execution.ErrPreparation
	}
	return address, nil
}

func (p NativeExecution) candidateRecipeMatches(source execution.PreparationRecord, wanted project.Preparation, refresh bool) bool {
	base := source.Preparation
	if source.Validate() != nil || source.Native == nil || source.Native.Profile != p.Config.Profile ||
		base.Project != wanted.Project || base.SetupDigest != wanted.SetupDigest || !base.Requirements.Equal(wanted.Requirements) ||
		base.Approval != wanted.Approval || !slices.Equal(base.Tools, wanted.Tools) {
		return false
	}
	if p.currentPreparationAddress(source.Address()) && source.State.Ready && !source.State.Stopped {
		return true
	}
	// The constructor independently checks the exact retained native journal.
	// A prior ready record can outlive its incarnation in the Go projection.
	return refresh && (source.RetiredRecipeCandidate() || source.State.Ready && !source.State.Stopped)
}

func (p NativeExecution) PrepareWorkspaceCandidate(ctx context.Context, request execution.CandidatePreparationRequest) (execution.PreparationState, error) {
	var empty execution.PreparationState
	if p.Store == nil || request.Validate() != nil || !p.currentPreparationAddress(request.Address()) || request.Input.Preparation.Credential != "" || !time.Now().Before(request.Input.Deadline) {
		return empty, execution.ErrPreparation
	}
	stored, err := p.Store.CandidatePreparation(ctx, request.Input.Preparation.ID)
	if err != nil {
		return empty, err
	}
	actual, wanted := request.Input.Preparation, stored.Preparation.Preparation
	actual.Revision, wanted.Revision = 0, 0
	if !reflect.DeepEqual(actual, wanted) || stored.Preparation.Native == nil || *stored.Preparation.Native != *request.Native || !reflect.DeepEqual(request.Input.SourceRun, stored.Admission.SourceRun) || !reflect.DeepEqual(request.Input.SourceMerge, stored.Admission.SourceMerge) || !reflect.DeepEqual(request.Input.SourcePull, stored.Admission.SourcePull) {
		return empty, execution.ErrPreparation
	}
	source, err := p.Store.Preparation(ctx, request.Input.SourcePreparation)
	if err != nil {
		return empty, err
	}
	base := source.Preparation
	refreshRecipe := request.Input.SourceRun != nil || request.Input.SourceMerge != nil || request.Input.SourcePull != nil || wanted.Role == project.RoleReviewer && stored.Admission.PublicationID != ""
	if !p.candidateRecipeMatches(source, wanted, refreshRecipe) {
		return empty, execution.ErrPreparation
	}
	if refreshRecipe {
		state, inspectErr := p.InspectWorkspacePreparation(ctx, source.Address())
		if inspectErr != nil || state.SourceBundleSHA256 != source.State.SourceBundleSHA256 {
			return empty, errors.Join(execution.ErrPreparation, inspectErr)
		}
		source.State = state
		if !p.candidateRecipeMatches(source, wanted, true) || !state.Ready && !source.RetiredRecipeCandidate() {
			return empty, execution.ErrPreparation
		}
	}
	notAfter, err := p.Store.AuthorizeCandidatePreparation(ctx, wanted.ID)
	if err != nil {
		return empty, err
	}
	if request.Input.Deadline.After(time.Unix(notAfter, 0)) {
		return empty, execution.ErrPreparation
	}
	bounded, cancel := context.WithDeadline(ctx, request.Input.Deadline)
	defer cancel()
	if request.Input.SourcePull != nil {
		bundleBase := base.SourceCommit
		if source.RetiredRecipeCandidate() {
			bundleBase = ""
		}
		request.Input.Bundle, err = p.publicationHeadBundle(bounded, *request.Input.SourcePull, bundleBase)
		if err != nil {
			return empty, err
		}
	} else if wanted.Role == project.RoleReviewer && source.RetiredRecipeCandidate() {
		publication, readErr := p.Store.PublicationByID(bounded, stored.Admission.PublicationID)
		if readErr != nil || publication.ProjectID != wanted.Project || publication.Candidate != wanted.SourceCommit || publication.Repository != stored.Admission.Repository {
			return empty, execution.ErrPreparation
		}
		request.Input.Bundle, err = p.publicationHeadBundle(bounded, *publicationPreparationSource(publication), "")
		if err != nil {
			return empty, err
		}
	} else if request.Input.SourceMerge != nil || request.Input.SourceRun != nil && source.RetiredRecipeCandidate() {
		merged := request.Input.SourceMerge
		if merged == nil {
			policy, readErr := p.Store.RepositoryPolicy(bounded, stored.Admission.Repository)
			if readErr != nil {
				return empty, readErr
			}
			bound, readErr := p.Store.MergedPreparationSource(bounded, stored.Admission.Repository, wanted.Project, policy.TargetBranch, wanted.SourceCommit)
			if readErr != nil || bound.Export != *request.Input.SourceRun || bound.Commit != request.Input.SourceRun.Candidate {
				return empty, execution.ErrPreparation
			}
			merged = &bound
		}
		bound, err := p.Store.MergedPreparationSource(bounded, stored.Admission.Repository, wanted.Project, merged.Target, wanted.SourceCommit)
		if err != nil || !reflect.DeepEqual(bound, *merged) {
			return empty, execution.ErrPreparation
		}
		account, err := (NativeInputSource{p}).account(bounded, strconv.FormatInt(merged.Repository, 10), factory.OpMerge)
		if err != nil {
			return empty, err
		}
		var fetched struct {
			Repository string `json:"repository"`
			Base       string `json:"base"`
			Commit     string `json:"commit"`
			Target     string `json:"target"`
			SHA256     string `json:"sha256"`
			Bundle     []byte `json:"bundle"`
		}
		bundleBase := base.SourceCommit
		if source.RetiredRecipeCandidate() {
			// A complete authenticated bundle lets the new preparation import
			// its source without reopening the retired protected checkout.
			bundleBase = ""
		}
		if err = account.read(bounded, nativeRead{Kind: "source_bundle", Repository: account.repository.ExternalID, Base: &bundleBase, Commit: merged.Commit, TargetRef: merged.Target}, &fetched); err != nil {
			return empty, err
		}
		sum := sha256.Sum256(fetched.Bundle)
		if fetched.Repository != account.repository.ExternalID || fetched.Base != bundleBase || fetched.Commit != wanted.SourceCommit || fetched.Target != merged.Target || len(fetched.Bundle) == 0 || len(fetched.Bundle) > project.MaxSourceBundle || fetched.SHA256 != hex.EncodeToString(sum[:]) {
			return empty, execution.ErrPreparation
		}
		request.Input.Bundle = fetched.Bundle
	} else if request.Input.SourceRun != nil {
		ref := request.Input.SourceRun
		exported, exportErr := p.ExportCandidate(bounded, execution.CandidateRequest{Address: execution.RunAddress{Workspace: ref.Project, Run: ref.ID}, Role: ref.Role, Preparation: ref.Preparation, Candidate: ref.Candidate})
		if exportErr != nil {
			return empty, exportErr
		}
		request.Input.Bundle = exported.Bundle
	} else if wanted.Role == project.RoleCoder && wanted.SourceCommit != base.SourceCommit {
		return empty, execution.ErrPreparation
	}
	request.Input.Preparation.Revision = 0
	controller, _, err := p.preparationController(ctx, stored.Preparation)
	if err != nil || request.Controller != nil && !reflect.DeepEqual(request.Controller, controller) {
		return empty, errors.Join(execution.ErrPreparation, err)
	}
	request.Controller = controller
	var state execution.PreparationState
	err = nativeHostCall(bounded, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                                `json:"operation"`
		Request   execution.CandidatePreparationRequest `json:"request"`
		Source    execution.PreparationAddress          `json:"source"`
	}{"candidate", request, source.Address()}, &state)
	if err != nil {
		return empty, err
	}
	if _, err = p.Store.AuthorizeCandidatePreparation(ctx, wanted.ID); err != nil {
		return empty, err
	}
	if err = state.ValidateFor(request.Address()); err != nil {
		return empty, err
	}
	if state.Role != wanted.Role || state.SourceCommit != wanted.SourceCommit || state.SetupDigest != wanted.SetupDigest {
		return empty, execution.ErrPreparation
	}
	return state, nil
}

func (p NativeExecution) ReadWorkspaceContext(ctx context.Context, request execution.PreparationContextRequest) (execution.PreparationContext, error) {
	var result execution.PreparationContext
	if p.Store == nil || request.Validate() != nil || len(request.Bundle) != 0 || request.Native == nil || request.Native.Profile != p.Config.Profile || !time.Now().Before(request.Input.NotAfter) {
		return result, execution.ErrPreparationContext
	}
	record, err := p.Store.Preparation(ctx, request.Input.ID)
	if err != nil {
		return result, err
	}
	if record.Validate() != nil || record.Native == nil || *record.Native != *request.Native || record.Preparation.Project != request.Input.Project || record.Preparation.Role != request.Role || record.Preparation.SourceCommit != request.Input.SourceCommit {
		return result, execution.ErrPreparationContext
	}
	bounded, cancel := context.WithDeadline(ctx, request.Input.NotAfter)
	defer cancel()
	if request.SourceRun != nil {
		// A settled run may need its captured recipe after restart. Inspect the
		// original native owner; confirmed retirement permits only this read,
		// never a new setup or execution under its old authority.
		state, inspectErr := p.InspectWorkspacePreparation(bounded, record.Address())
		if inspectErr != nil || state.SourceBundleSHA256 != record.State.SourceBundleSHA256 {
			return result, execution.ErrPreparationContext
		}
		record.State = state
	}
	if !(p.currentPreparationAddress(record.Address()) && record.State.Ready && !record.State.Stopped) && !(request.SourceRun != nil && record.RetiredRecipeCandidate()) {
		return result, execution.ErrPreparationContext
	}
	if ref := request.SourceRun; ref != nil {
		assignment, err := p.Store.AssignmentByRun(bounded, ref.ID)
		if err != nil || assignment.Validate() != nil || !assignment.SucceededReported() || assignment.Result == nil || assignment.Result.Candidate != ref.Candidate || assignment.ProjectID != ref.Project || assignment.Preparation != ref.Preparation || assignment.SourceCommit != request.Input.SourceCommit {
			return result, execution.ErrPreparationContext
		}
		run, err := p.Store.FactoryRun(bounded, ref.ID)
		if err != nil || run.Validate() != nil || !run.Reconciled || run.Outcome != factory.Succeeded || run.Native == nil || !run.Native.Settled() {
			return result, execution.ErrPreparationContext
		}
		exported, err := p.ExportCandidate(bounded, execution.CandidateRequest{Address: execution.RunAddress{Run: ref.ID, Workspace: ref.Project}, Role: ref.Role, Preparation: ref.Preparation, Candidate: ref.Candidate})
		if err != nil {
			return result, err
		}
		request.Bundle = exported.Bundle
	}
	// An empty selection still reads instructions and the candidate diff.
	// The native protocol represents that selection as an array, not null.
	if request.Input.Paths == nil {
		request.Input.Paths = []string{}
	}
	err = nativeHostCall(bounded, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                              `json:"operation"`
		Request   execution.PreparationContextRequest `json:"request"`
	}{"context", request}, &result)
	if err != nil {
		return execution.PreparationContext{}, err
	}
	if err = bounded.Err(); err != nil {
		return execution.PreparationContext{}, err
	}
	if err = result.ValidateFor(request); err != nil {
		return execution.PreparationContext{}, err
	}
	return result, nil
}

// Approval and current enrollment stay in Go. The native owner retains the
// source/setup bytes and reports its real isolated preparation observations.
func (p NativeExecution) PrepareWorkspace(ctx context.Context, request execution.StandingPreparationRequest) (execution.PreparationState, error) {
	var empty execution.PreparationState
	if p.Store == nil || request.Validate() != nil || !p.currentPreparationAddress(request.Address()) || request.Input.Preparation.Credential != "" {
		return empty, execution.ErrPreparation
	}
	record := execution.PreparationRecord{Preparation: request.Input.Preparation, Native: request.Native}
	if err := p.Store.AuthorizeStandingPreparation(ctx, record); err != nil {
		return empty, err
	}
	// Observation revision is a Go CAS counter, not part of the native job's
	// immutable inputs. Approval revisions and all actual inputs stay captured.
	request.Input.Preparation.Revision = 0
	controller, _, err := p.preparationController(ctx, record)
	if err != nil || request.Controller != nil && !reflect.DeepEqual(request.Controller, controller) {
		return empty, errors.Join(execution.ErrPreparation, err)
	}
	request.Controller = controller
	var state execution.PreparationState
	err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                               `json:"operation"`
		Request   execution.StandingPreparationRequest `json:"request"`
	}{"prepare", request}, &state)
	if err != nil {
		return empty, err
	}
	if err = p.Store.AuthorizeStandingPreparation(ctx, record); err != nil {
		return empty, err
	}
	if err = state.ValidateFor(request.Address()); err != nil {
		return empty, err
	}
	if state.SourceCommit != record.Preparation.SourceCommit || state.SetupDigest != record.Preparation.SetupDigest || state.Role != record.Preparation.Role {
		return empty, execution.ErrPreparation
	}
	return state, nil
}

func (p NativeExecution) preparationController(ctx context.Context, record execution.PreparationRecord) (*execution.PreparationController, *nativeProductAccount, error) {
	decision, err := p.Store.RequirementDecision(ctx, record.Preparation.Requirements.ID)
	if err != nil {
		return nil, nil, err
	}
	native := decision.Native
	if native == nil || record.Native == nil || native.Profile != record.Native.Profile {
		return nil, nil, execution.ErrPreparation
	}
	if native.ControllerGrantID == "" {
		return nil, nil, nil
	}
	repository, err := p.Store.RecordedWorkspaceRepository(ctx, record.Preparation.Project, true)
	if err != nil {
		return nil, nil, err
	}
	policy, err := p.Store.RepositoryPolicy(ctx, int64(repository))
	if err != nil || policy.Execution == nil || policy.Execution.Validate() != nil || policy.Execution.Kind != "execution.launch" || policy.Execution.NativeProfile != native.Profile || policy.Execution.NativeConnection != native.ControllerConnection || policy.Execution.ControllerGrantID != native.ControllerGrantID {
		return nil, nil, execution.ErrPreparation
	}
	connection, err := p.Store.CollaborationConnection(ctx, native.ControllerConnection)
	if err != nil {
		return nil, nil, err
	}
	if policy.Execution.ActorID != int64(connection.Actor) {
		return nil, nil, execution.ErrPreparation
	}
	account, err := (NativeInputSource{p}).savedAccount(ctx, int64(repository), connection.Actor, native.ControllerConnection)
	if err != nil {
		return nil, nil, err
	}
	grant, err := account.controllerGrant(ctx, repository, native.ControllerGrantID, "execution.launch")
	if err != nil || grant.Generation != native.Generation {
		return nil, nil, errors.Join(execution.ErrPreparation, err)
	}
	return &execution.PreparationController{Connection: string(native.ControllerConnection), GrantID: grant.GrantID, NotAfter: grant.NotAfter}, account, nil
}

// Recheck the original approved request after remote claim I/O and before the
// privileged constructor. This private route can neither approve nor renew it.
func nativePreparationAdmission(p NativeExecution) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/native/preparation/admission" || r.URL.RawQuery != "" || r.URL.ForceQuery ||
			len(r.Header.Values("X-Soda-Native-Instance")) != 1 || r.Header.Get("X-Soda-Native-Instance") != p.Instance {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_preparation_admission"})
			return
		}
		var input struct {
			Kind       string          `json:"kind"`
			Request    json.RawMessage `json:"request"`
			Origin     string          `json:"origin"`
			Actor      string          `json:"actor"`
			Repository string          `json:"repository"`
		}
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 2*1024*1024), &input) != nil {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_preparation_admission"})
			return
		}
		var record execution.PreparationRecord
		var captured *execution.PreparationController
		var err error
		switch input.Kind {
		case "standing":
			var request execution.StandingPreparationRequest
			if strictjson.Decode(bytes.NewReader(input.Request), &request) != nil || request.Validate() != nil || !p.currentPreparationAddress(request.Address()) {
				err = execution.ErrPreparation
				break
			}
			record = execution.PreparationRecord{Preparation: request.Input.Preparation, Native: request.Native}
			captured = request.Controller
			err = p.Store.AuthorizeStandingPreparation(r.Context(), record)
		case "candidate":
			var request execution.CandidatePreparationRequest
			if strictjson.Decode(bytes.NewReader(input.Request), &request) != nil {
				err = execution.ErrPreparation
				break
			}
			// This callback receives the complete, previously fetched native
			// payload. Validate its typed source separately from transported
			// bundle bytes; the public preparation entry still forbids a
			// caller-supplied coder archive.
			shape := request
			if shape.Input.SourceRun != nil || shape.Input.SourceMerge != nil || shape.Input.SourcePull != nil {
				shape.Input.Bundle = nil
			}
			if shape.Validate() != nil || !p.currentPreparationAddress(request.Address()) || len(request.Input.Bundle) > project.MaxSourceBundle {
				err = execution.ErrPreparation
				break
			}
			stored, readErr := p.Store.CandidatePreparation(r.Context(), request.Input.Preparation.ID)
			if readErr != nil {
				err = readErr
				break
			}
			record = stored.Preparation
			actual, wanted := request.Input.Preparation, record.Preparation
			actual.Revision, wanted.Revision = 0, 0
			if !reflect.DeepEqual(actual, wanted) || !reflect.DeepEqual(record.Native, request.Native) || !reflect.DeepEqual(stored.Admission.SourceRun, request.Input.SourceRun) || !reflect.DeepEqual(stored.Admission.SourceMerge, request.Input.SourceMerge) || !reflect.DeepEqual(stored.Admission.SourcePull, request.Input.SourcePull) {
				err = execution.ErrPreparation
				break
			}
			source, sourceErr := p.Store.Preparation(r.Context(), request.Input.SourcePreparation)
			if sourceErr != nil || !p.candidateRecipeMatches(source, wanted, request.Input.SourceRun != nil || request.Input.SourceMerge != nil || request.Input.SourcePull != nil || wanted.Role == project.RoleReviewer && stored.Admission.PublicationID != "") {
				err = execution.ErrPreparation
				break
			}
			deadline, admissionErr := p.Store.AuthorizeCandidatePreparation(r.Context(), record.Preparation.ID)
			err = admissionErr
			if request.Input.Deadline.After(time.Unix(deadline, 0)) || !time.Now().Before(request.Input.Deadline) {
				err = execution.ErrPreparation
			}
			captured = request.Controller
		default:
			err = execution.ErrPreparation
		}
		if err != nil {
			nativeResponse(w, 403, map[string]string{"error": "native_preparation_admission_unavailable"})
			return
		}
		expected, account, err := p.preparationController(r.Context(), record)
		origin, originErr := collaboration.CanonicalAPIBase(input.Origin)
		if err != nil || account == nil || captured == nil || !reflect.DeepEqual(captured, expected) || originErr != nil || account.binding.Host.APIBase != origin || account.binding.Actor.ExternalID != input.Actor || account.repository.ExternalID != input.Repository {
			nativeResponse(w, 403, map[string]string{"error": "native_preparation_scope_unavailable"})
			return
		}
		digest := sha256.Sum256(input.Request)
		nativeResponse(w, 200, map[string]string{"intent_digest": hex.EncodeToString(digest[:])})
	})
}
func (p NativeExecution) currentPreparationAddress(address execution.PreparationAddress) bool {
	return address.Validate() == nil && address.Native != nil && address.Native.Profile == p.Config.Profile && address.Native.InstanceID == p.Instance
}
func (p NativeExecution) InspectWorkspacePreparation(ctx context.Context, address execution.PreparationAddress) (execution.PreparationState, error) {
	return p.preparationObservation(ctx, address, "inspect")
}
func (p NativeExecution) RetireWorkspacePreparation(ctx context.Context, address execution.PreparationAddress) (execution.PreparationState, error) {
	return p.preparationObservation(ctx, address, "retire")
}
func (p NativeExecution) preparationObservation(ctx context.Context, address execution.PreparationAddress, operation string) (execution.PreparationState, error) {
	var empty execution.PreparationState
	if p.Store == nil || address.Validate() != nil || address.Native == nil || address.Native.Profile != p.Config.Profile {
		return empty, execution.ErrPreparation
	}
	record, err := p.Store.Preparation(ctx, address.ID)
	if err != nil {
		return empty, err
	}
	if record.Address().Workspace != address.Workspace || record.Native == nil || *record.Native != *address.Native {
		return empty, execution.ErrPreparation
	}
	var state execution.PreparationState
	err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                       `json:"operation"`
		Address   execution.PreparationAddress `json:"address"`
	}{operation, address}, &state)
	if err != nil {
		return empty, errors.Join(execution.ErrPreparationNotObserved, err)
	}
	if err = state.ValidateFor(address); err != nil {
		return empty, err
	}
	observed := record
	observed.State = state
	if err = observed.Validate(); err != nil {
		return empty, err
	}
	return state, nil
}
