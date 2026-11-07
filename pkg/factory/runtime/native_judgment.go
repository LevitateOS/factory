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
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// NativePostJudgments binds the existing routing policy to the source selected
// for this repository. It does not borrow Coding/Review funding or a server key.
type NativePostJudgments struct{ NativeExecution }

// The existing background owner also retires calls whose source left its normal
// pending-post scan. Missing evidence retains the original reservation.
func (s NativePostJudgments) ReconcilePostJudgments(ctx context.Context) {
	commands, err := s.Store.PendingNativeJudgments(ctx)
	if err != nil {
		return
	}
	for _, cmd := range commands {
		if ctx.Err() != nil {
			return
		}
		var capture factory.NativeJudgmentCapture
		if nativeDecode([]byte(cmd.Payload), &capture) != nil || capture.Profile != s.Config.Profile || capture.SourcePost == "" {
			continue
		}
		post, err := s.Store.FeedPost(ctx, capture.SourcePost)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err == nil && !post.Record.Deleted && !post.Record.Revoked && post.Record.Revision == capture.SourceRevision {
			continue
		}
		var reply nativeJudgmentReply
		if nativeHostCall(ctx, s.Config, s.Instance, "/api/native/execution", map[string]any{"operation": "inspect_judgment", "id": capture.ID, "digest": cmd.Digest, "cancel": true}, &reply) != nil || reply.State != "complete" || reply.Result == nil {
			continue
		}
		_, _ = finishNativeJudgment(ctx, s.NativeExecution, capture, cmd, reply.Result, nil)
	}
}

func (s NativePostJudgments) JudgePost(ctx context.Context, input factoryservice.PostJudgeInput) (factoryservice.PostJudgment, error) {
	chooser := nativeRepositoryJudgment{NativeExecution: s.NativeExecution, repository: input.SourcePost.Record.Repository, subject: input.SourcePost.Record.ID + ":" + strconv.FormatInt(input.SourcePost.Record.Revision, 10), sourcePost: input.SourcePost.Record.ID, sourceRevision: input.SourcePost.Record.Revision}
	chooser.admit = func(ctx context.Context) error {
		effects := newNativeIssueEffects(NativeInputSource{s.NativeExecution})
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
	Process nativeJudgmentProcess   `json:"process"`
}

func (s nativeRepositoryJudgment) Select(ctx context.Context, state any, question string, choices map[string]any) (factory.PriorityAnswer, error) {
	var empty factory.PriorityAnswer
	request := factory.PriorityRequest{}
	for id := range choices {
		request.Candidates = append(request.Candidates, factory.PriorityCandidate{ID: id})
	}
	if len(choices) < 2 || len(choices) > 255 {
		return empty, control.ErrPriorityUnavailable
	}
	prompt, err := factory.NativeJudgmentPrompt(state, question, choices)
	if err != nil {
		return empty, err
	}
	sha := factory.MCPInputDigest([]byte(prompt))
	id := factory.NativeJudgmentID(s.Config.Profile, s.repository, s.subject)
	cmd, err := s.Store.FactoryCommand(ctx, id)
	var capture factory.NativeJudgmentCapture
	fresh := false
	if errors.Is(err, store.ErrNotFound) {
		if s.admit == nil || s.admit(ctx) != nil {
			return empty, factoryservice.ErrPostJudgmentPending
		}
		policy, e := s.Store.RepositoryPolicy(ctx, s.repository)
		if e != nil || policy.Judgments == nil {
			return empty, factoryservice.ErrPostJudgmentPending
		}
		source, e := s.MuseLoginSource(ctx)
		if e != nil || !source.JudgmentAvailable || source.Connection != policy.Judgments.Account || !slices.Contains(source.JudgmentModels, policy.Judgments.Model) {
			return empty, factoryservice.ErrPostJudgmentPending
		}
		tool, e := s.InspectTool(ctx, policy.Judgments.Harness)
		if e != nil || tool.Native == nil {
			return empty, factoryservice.ErrPostJudgmentPending
		}
		capture = factory.NativeJudgmentCapture{ID: id, Profile: s.Config.Profile, Repository: s.repository, SourcePost: s.sourcePost, SourceRevision: s.sourceRevision, Selection: *policy.Judgments, PromptSHA256: sha, ToolVersion: tool.Native.Version, ToolSHA256: tool.Native.SHA256, DeadlineUnixMS: time.Now().Add(30 * time.Second).UnixMilli()}
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
	matchingInput := capture.PromptSHA256 == sha
	if cmd.Finished != "" {
		if !matchingInput {
			return empty, control.ErrPriorityUnavailable
		}
		if current != nil {
			return empty, factoryservice.ErrPostJudgmentPending
		}
		var saved nativeJudgmentOutcome
		if nativeDecode([]byte(cmd.Outcome), &saved) != nil || saved.Answer == nil || saved.Answer.Validate(request) != nil {
			return empty, control.ErrPriorityUnavailable
		}
		return *saved.Answer, nil
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
		originalOptions = &request
	}
	outcome, err := finishNativeJudgment(ctx, s.NativeExecution, capture, cmd, reply.Result, originalOptions)
	if err != nil {
		return empty, err
	}
	if s.Store.CheckNativeJudgmentSource(ctx, capture) != nil || s.admit == nil || s.admit(ctx) != nil {
		return empty, factoryservice.ErrPostJudgmentPending
	}
	if outcome.Answer == nil {
		return empty, control.ErrPriorityUnavailable
	}
	return *outcome.Answer, nil
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
	if request != nil && p.ExitCode != nil && *p.ExitCode == 0 && p.Reason != nil && *p.Reason == "exited" && r.Output != nil {
		var answer struct {
			Selected      string             `json:"selected"`
			Confidence    float64            `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
		}
		if nativeDecode([]byte(*r.Output), &answer) == nil {
			selected := factory.PriorityAnswer{Selected: answer.Selected, Confidence: answer.Confidence, Probabilities: answer.Probabilities, Model: capture.Selection.Model}
			if selected.Validate(*request) == nil {
				outcome.Answer = &selected
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
