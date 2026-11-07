package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// NativeJudgments binds product judgments to explicit repository funding. It
// never borrows Coding/Review funding or a server key.
type NativeJudgments struct{ NativeExecution }

var _ control.PriorityChooser = NativeJudgments{}

// Choose uses the configured judgment source only when every candidate belongs
// to one repository. Cross-repository spending has no selected funding owner.
func (s NativeJudgments) Choose(ctx context.Context, request factory.PriorityRequest) (factory.PriorityAnswer, error) {
	var empty factory.PriorityAnswer
	if len(request.Candidates) < 2 || len(request.Candidates) > 255 || request.Rubric != factory.PriorityRubric {
		return empty, control.ErrPriorityUnavailable
	}
	repository := request.Repository
	options := make(map[string]any, len(request.Candidates))
	for _, candidate := range request.Candidates {
		if repository <= 0 || candidate.Control.Repository != repository || candidate.Acceptance.Repository != repository || candidate.ID == "" {
			return empty, control.ErrPriorityUnavailable
		}
		if _, duplicate := options[candidate.ID]; duplicate {
			return empty, control.ErrPriorityUnavailable
		}
		options[candidate.ID] = candidate
	}
	fingerprint, err := factory.JudgmentFingerprint(factory.JudgmentNextWork, request, request.Rubric, options)
	if err != nil {
		return empty, err
	}
	return s.SelectScoped(ctx, repository, "priority:"+fingerprint, request, request.Rubric, options)
}

func (s NativeJudgments) Select(context.Context, any, string, map[string]any) (factory.PriorityAnswer, error) {
	return factory.PriorityAnswer{}, control.ErrPriorityUnavailable
}

func (s NativeJudgments) SelectScoped(ctx context.Context, repository int64, subject string, state any, question string, options map[string]any) (factory.PriorityAnswer, error) {
	if repository <= 0 || subject == "" || len(subject) > 256 {
		return factory.PriorityAnswer{}, control.ErrPriorityUnavailable
	}
	chooser := nativeRepositoryJudgment{NativeExecution: s.NativeExecution, repository: repository, subject: subject}
	chooser.admit = func(ctx context.Context) error {
		account, err := (NativeInputSource{s.NativeExecution}).account(ctx, strconv.FormatInt(repository, 10), factory.OpRefPublish)
		if err != nil {
			return err
		}
		return account.visible(ctx, account.binding.Actor.ID, account.repository.ID)
	}
	return chooser.Select(ctx, state, question, options)
}

// The background owner settles completed purpose calls and retires calls whose
// posts left the normal scan. Missing evidence retains the original reservation.
func (s NativeJudgments) ReconcilePostJudgments(ctx context.Context) {
	commands, err := s.Store.PendingNativeJudgments(ctx)
	if err != nil {
		return
	}
	for _, cmd := range commands {
		if ctx.Err() != nil {
			return
		}
		var capture factory.NativeJudgmentCapture
		if nativeDecode([]byte(cmd.Payload), &capture) != nil || capture.Profile != s.Config.Profile {
			continue
		}
		cancel := s.Store.CheckNativeJudgmentSource(ctx, capture) != nil
		if capture.WorkerCapability != "" {
			cap, err := s.Store.GetMCPCapability(ctx, capture.WorkerCapability)
			if err != nil || cap.ScopeDigest() != capture.WorkerScope || cap.State != factory.MCPCapabilityActive || cap.Expired(time.Now()) || s.Store.CheckMCPWorkActive(ctx, cap) != nil {
				cancel = true
			}
		}
		if capture.SourcePost != "" {
			post, err := s.Store.FeedPost(ctx, capture.SourcePost)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err == nil && !post.Record.Deleted && !post.Record.Revoked && post.Record.Revision == capture.SourceRevision {
				continue
			}
			cancel = true
		}
		var reply nativeJudgmentReply
		if nativeHostCall(ctx, s.Config, s.Instance, "/api/native/execution", map[string]any{"operation": "inspect_judgment", "id": capture.ID, "digest": cmd.Digest, "cancel": cancel}, &reply) != nil || reply.State != "complete" || reply.Result == nil {
			continue
		}
		_, _ = finishNativeJudgment(ctx, s.NativeExecution, capture, cmd, reply.Result, nil)
	}
}

func (s NativeJudgments) JudgePost(ctx context.Context, input factoryservice.PostJudgeInput) (factoryservice.PostJudgment, error) {
	chooser := nativeRepositoryJudgment{NativeExecution: s.NativeExecution, repository: input.SourcePost.Record.Repository, subject: input.SourcePost.Record.ID + ":" + strconv.FormatInt(input.SourcePost.Record.Revision, 10), sourcePost: input.SourcePost.Record.ID, sourceRevision: input.SourcePost.Record.Revision}
	chooser.admit = func(ctx context.Context) error {
		effects := NewNativeIssueEffects(NativeInputSource{s.NativeExecution})
		_, raw, err := effects.Grant(ctx, input.SourcePost.Record.ID, input.SourcePost.Record.Revision)
		if err != nil {
			return err
		}
		proof, err := effects.proof(raw)
		if err != nil {
			return err
		}
		_, err = effects.savedAccount(ctx, int64(proof.Scope.Repository), proof.Scope.Actor, proof.Connection)
		return err
	}
	return (&factoryservice.ChoicePostJudge{Chooser: chooser}).JudgePost(ctx, input)
}

type nativeRepositoryJudgment struct {
	NativeExecution
	repository     int64
	subject        string
	sourcePost     string
	sourceRevision int64
	admit          func(context.Context) error
	worker         *factory.MCPCapability
}

type nativeJudgmentProcess struct {
	Kind             string  `json:"kind"`
	Version          int     `json:"version"`
	Run              string  `json:"run"`
	Service          string  `json:"service"`
	Sequence         uint64  `json:"sequence"`
	Live             *bool   `json:"live"`
	ExecutableSHA256 string  `json:"executable_sha256"`
	PromptSHA256     string  `json:"prompt_sha256"`
	Invocation       string  `json:"invocation"`
	SystemInstance   string  `json:"system_instance"`
	StartIdentity    string  `json:"start_identity"`
	PID              int64   `json:"pid"`
	UID              int64   `json:"uid"`
	StartedUnixMS    int64   `json:"started_unix_ms"`
	StoppedUnixMS    *int64  `json:"stopped_unix_ms,omitempty"`
	Finished         bool    `json:"finished"`
	TreeRetired      bool    `json:"tree_retired"`
	OutputComplete   bool    `json:"output_complete"`
	OutputLength     int     `json:"output_length"`
	OutputSHA256     *string `json:"output_sha256"`
	ExitCode         *int    `json:"exit_code"`
	ExitSignal       *int    `json:"exit_signal"`
	Reason           *string `json:"reason"`
}

type nativeJudgmentResult struct {
	ID      string                `json:"id"`
	Digest  string                `json:"digest"`
	Process nativeJudgmentProcess `json:"process"`
	Output  *string               `json:"output"`
}
type nativeJudgmentReply struct {
	State  string                `json:"state"`
	Result *nativeJudgmentResult `json:"result,omitempty"`
}
type nativeJudgmentOutcome struct {
	Answer  *factory.PriorityAnswer `json:"answer,omitempty"`
	Output  json.RawMessage         `json:"output,omitempty"`
	Process nativeJudgmentProcess   `json:"process"`
}

func (s nativeRepositoryJudgment) Select(ctx context.Context, state any, question string, choices map[string]any) (factory.PriorityAnswer, error) {
	var empty factory.PriorityAnswer
	request := factory.PriorityRequest{}
	optionKeys := make([]string, 0, len(choices))
	for id := range choices {
		optionKeys = append(optionKeys, id)
		request.Candidates = append(request.Candidates, factory.PriorityCandidate{ID: id})
	}
	if len(choices) < 2 || len(choices) > 255 {
		return empty, control.ErrPriorityUnavailable
	}
	prompt, err := factory.NativeJudgmentPrompt(state, question, choices)
	if err != nil {
		return empty, err
	}
	return s.selectPrompt(ctx, prompt, optionKeys, request)
}

func (s nativeRepositoryJudgment) selectPrompt(ctx context.Context, prompt string, optionKeys []string, request factory.PriorityRequest) (factory.PriorityAnswer, error) {
	var empty factory.PriorityAnswer
	outcome, err := s.infer(ctx, prompt, factory.NativeJudgmentOptionsDigest(optionKeys), "", &request)
	if err != nil {
		return empty, err
	}
	if outcome.Answer == nil || outcome.Answer.Validate(request) != nil {
		return empty, control.ErrPriorityUnavailable
	}
	return *outcome.Answer, nil
}

func (s nativeRepositoryJudgment) infer(ctx context.Context, prompt, optionsDigest, outputKind string, request *factory.PriorityRequest) (nativeJudgmentOutcome, error) {
	var empty nativeJudgmentOutcome
	sha := factory.MCPInputDigest([]byte(prompt))
	id := factory.NativeJudgmentID(s.Config.Profile, s.repository, s.subject)
	cmd, err := s.Store.FactoryCommand(ctx, id)
	var capture factory.NativeJudgmentCapture
	fresh := false
	if errors.Is(err, store.ErrNotFound) {
		if s.admit == nil || s.admit(ctx) != nil {
			return empty, factoryservice.ErrPostJudgmentPending
		}
		selection, tool, e := s.selection(ctx)
		if e != nil {
			return empty, e
		}
		capture = factory.NativeJudgmentCapture{ID: id, Profile: s.Config.Profile, Repository: s.repository, SourcePost: s.sourcePost, SourceRevision: s.sourceRevision, Selection: selection, PromptSHA256: sha, OptionsSHA256: optionsDigest, OutputKind: outputKind, ToolVersion: tool.Native.Version, ToolSHA256: tool.Native.SHA256, DeadlineUnixMS: time.Now().Add(30 * time.Second).UnixMilli()}
		if deadline, ok := ctx.Deadline(); ok && deadline.UnixMilli() < capture.DeadlineUnixMS {
			capture.DeadlineUnixMS = deadline.UnixMilli()
		}
		if s.worker != nil {
			capture.WorkerCapability, capture.WorkerScope = s.worker.ID, s.worker.ScopeDigest()
			if deadline := time.Unix(s.worker.DeadlineUnix, 0).UnixMilli(); deadline < capture.DeadlineUnixMS {
				capture.DeadlineUnixMS = deadline
			}
		}
		cmd, fresh, err = s.Store.BeginNativeJudgment(ctx, capture)
		if errors.Is(err, store.ErrNativeJudgmentSource) || errors.Is(err, store.ErrNotFound) {
			return empty, factoryservice.ErrPostJudgmentPending
		}
	}
	if err != nil {
		return empty, err
	}
	if nativeDecode([]byte(cmd.Payload), &capture) != nil || capture.ID != id || capture.Repository != s.repository || capture.Profile != s.Config.Profile {
		return empty, control.ErrPriorityUnavailable
	}
	canonical, e := capture.Command()
	if e != nil || !reflect.DeepEqual([]string{cmd.Type, cmd.Target, cmd.Principal, cmd.Digest}, []string{canonical.Type, canonical.Target, canonical.Principal, canonical.Digest}) {
		return empty, control.ErrPriorityUnavailable
	}
	current := s.Store.CheckNativeJudgmentSource(ctx, capture)
	if current == nil && (s.admit == nil || s.admit(ctx) != nil) {
		current = factoryservice.ErrCapturedIssueSource
	}
	matchingInput := capture.PromptSHA256 == sha && capture.OutputKind == outputKind && (capture.OptionsSHA256 == optionsDigest || (outputKind == "" && capture.OptionsSHA256 == ""))
	if cmd.Finished != "" {
		if !matchingInput {
			return empty, control.ErrPriorityUnavailable
		}
		if current != nil {
			return empty, factoryservice.ErrPostJudgmentPending
		}
		var saved nativeJudgmentOutcome
		if nativeDecode([]byte(cmd.Outcome), &saved) != nil {
			return empty, control.ErrPriorityUnavailable
		}
		return saved, nil
	}
	var reply nativeJudgmentReply
	if fresh && current == nil {
		profile := factory.EffectiveExecutionProfile(&capture.Selection.Profile)
		err = nativeHostCall(ctx, s.Config, s.Instance, "/api/native/execution", map[string]any{"operation": "begin_judgment", "request": map[string]any{
			"id": id, "digest": cmd.Digest, "connection": capture.Selection.Account, "tool_version": capture.ToolVersion, "tool_sha256": capture.ToolSHA256,
			"prompt_sha256": sha, "harness": map[string]any{"model": capture.Selection.Model, "effort": profile.Effort, "contributor": profile.Contributor}, "deadline_unix_ms": capture.DeadlineUnixMS, "prompt": prompt}}, &reply)
	} else {
		err = nativeHostCall(ctx, s.Config, s.Instance, "/api/native/execution", map[string]any{"operation": "inspect_judgment", "id": id, "digest": cmd.Digest, "cancel": current != nil || !matchingInput}, &reply)
	}
	if err != nil || reply.State == "pending" || reply.State == "unknown" {
		return empty, factoryservice.ErrPostJudgmentPending
	}
	if reply.State != "complete" || reply.Result == nil {
		return empty, control.ErrPriorityUnavailable
	}
	var originalOptions *factory.PriorityRequest
	if matchingInput {
		originalOptions = request
	}
	outcome, err := finishNativeJudgment(ctx, s.NativeExecution, capture, cmd, reply.Result, originalOptions)
	if err != nil {
		return empty, err
	}
	if !matchingInput {
		return empty, control.ErrPriorityUnavailable
	}
	if s.Store.CheckNativeJudgmentSource(ctx, capture) != nil || s.admit == nil || s.admit(ctx) != nil {
		return empty, factoryservice.ErrPostJudgmentPending
	}
	return outcome, nil
}

func finishNativeJudgment(ctx context.Context, native NativeExecution, capture factory.NativeJudgmentCapture, cmd factory.Command, r *nativeJudgmentResult, request *factory.PriorityRequest) (nativeJudgmentOutcome, error) {
	p := r.Process
	if r.ID != capture.ID || r.Digest != cmd.Digest || p.Kind != "factory_muse_process" || p.Version != 1 || p.Run != capture.ID ||
		!factory.ValidID(p.Service) || !factory.ValidID(p.Invocation) || p.ExecutableSHA256 != capture.ToolSHA256 || p.PromptSHA256 != capture.PromptSHA256 ||
		p.UID <= 0 || p.PID <= 0 || p.StartedUnixMS <= 0 || p.StoppedUnixMS == nil || *p.StoppedUnixMS < p.StartedUnixMS ||
		!p.Finished || !p.TreeRetired || !p.OutputComplete || p.Live == nil || *p.Live {
		return nativeJudgmentOutcome{}, control.ErrPriorityUnavailable
	}
	outcome := nativeJudgmentOutcome{Process: p}
	if p.ExitCode != nil && *p.ExitCode == 0 && p.Reason != nil && *p.Reason == "exited" && r.Output != nil {
		if capture.OutputKind == "consultation" {
			// Leave room for process evidence in the existing 64 KiB journal
			// outcome. Oversized or malformed answers still settle actual usage.
			if len(*r.Output) <= 48*1024 && json.Valid([]byte(*r.Output)) {
				outcome.Output = json.RawMessage(*r.Output)
			}
		} else {
			var answer struct {
				Selected      string             `json:"selected"`
				Confidence    float64            `json:"confidence"`
				Probabilities map[string]float64 `json:"probabilities"`
			}
			if nativeDecode([]byte(*r.Output), &answer) == nil {
				keys := make([]string, 0, len(answer.Probabilities))
				for key := range answer.Probabilities {
					keys = append(keys, key)
				}
				boundOptions := capture.OptionsSHA256 != "" && factory.NativeJudgmentOptionsDigest(keys) == capture.OptionsSHA256
				if request == nil && capture.SourcePost == "" && boundOptions {
					request = &factory.PriorityRequest{}
					for _, key := range keys {
						request.Candidates = append(request.Candidates, factory.PriorityCandidate{ID: key})
					}
				}
				selected := factory.PriorityAnswer{Selected: answer.Selected, Confidence: answer.Confidence, Probabilities: answer.Probabilities, Model: capture.Selection.Model}
				if request != nil && (capture.OptionsSHA256 == "" || boundOptions) && selected.Validate(*request) == nil {
					outcome.Answer = &selected
				}
			}
		}
	}
	started, stopped := time.UnixMilli(p.StartedUnixMS).UTC(), time.UnixMilli(*p.StoppedUnixMS).UTC()
	minutes := int((stopped.Sub(started) + time.Minute - time.Nanosecond) / time.Minute)
	raw, _ := json.Marshal(outcome)
	if err := native.Store.CompleteNativeJudgment(ctx, capture, raw, factory.Usage{RunID: capture.ID, Repository: capture.Repository, Connection: capture.Selection.Account, StartedAt: started, EndedAt: stopped, Minutes: minutes}); err != nil {
		return nativeJudgmentOutcome{}, err
	}
	return outcome, nil
}

func (s nativeRepositoryJudgment) selection(ctx context.Context) (factory.RoleSelection, execution.ToolObservation, error) {
	policy, e := s.Store.RepositoryPolicy(ctx, s.repository)
	if e != nil || policy.Judgments == nil {
		return factory.RoleSelection{}, execution.ToolObservation{}, factoryservice.ErrPostJudgmentPending
	}
	source, e := s.MuseLoginSource(ctx)
	if e != nil || !source.JudgmentAvailable || source.Connection != policy.Judgments.Account || !slices.Contains(source.JudgmentModels, policy.Judgments.Model) {
		return factory.RoleSelection{}, execution.ToolObservation{}, factoryservice.ErrPostJudgmentPending
	}
	tool, e := s.InspectTool(ctx, policy.Judgments.Harness)
	if e != nil || tool.Native == nil {
		return factory.RoleSelection{}, execution.ToolObservation{}, factoryservice.ErrPostJudgmentPending
	}
	return *policy.Judgments, tool, nil
}
