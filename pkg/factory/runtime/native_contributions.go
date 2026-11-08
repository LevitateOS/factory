package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

var _ service.ContributionJudgments = NativeJudgments{}

func (s NativeJudgments) ContributionsReady(ctx context.Context, review factory.ContributionReview) (bool, error) {
	for _, role := range review.Roles {
		cap, err := s.Store.GetMCPCapability(ctx, role.Capability)
		if err != nil {
			return false, err
		}
		if cap.Repository != review.Repository || cap.AssignmentID != role.Assignment || cap.Profile != role.Profile || cap.Subject != role.Subject {
			return false, service.ErrWorkerBinding
		}
		if _, err := (service.WorkerAdmission{Store: s.Store, Inspect: s.InspectRun}).Prepare(ctx, cap); err != nil {
			return false, err
		}
	}
	current, err := s.Store.ContributionContext(ctx, review.Source)
	if err != nil || !reflect.DeepEqual(current, review) {
		return false, errors.Join(err, store.ErrStaleRevision)
	}
	selection, _, err := (nativeRepositoryJudgment{NativeExecution: s.NativeExecution, repository: review.Repository}).selection(ctx)
	if errors.Is(err, service.ErrPostJudgmentPending) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = s.Store.NativeJudgmentReady(ctx, factory.NativeJudgmentCapture{Profile: s.Config.Profile, Repository: review.Repository, Selection: selection}, len(review.Roles))
	if errors.Is(err, store.ErrNativeJudgmentSource) || errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s NativeJudgments) JudgeContributions(ctx context.Context, review factory.ContributionReview) ([]factory.ContributionCall, error) {
	calls := []factory.ContributionCall{}
	// One pending call is admitted per repository/source. Keep the existing
	// three independent formulations together, then assess the next role.
	for _, role := range review.Roles {
		cap, err := s.Store.GetMCPCapability(ctx, role.Capability)
		if err != nil {
			return calls, err
		}
		if cap.Repository != review.Repository || cap.AssignmentID != role.Assignment || cap.Profile != role.Profile || cap.Subject != role.Subject {
			return calls, service.ErrWorkerBinding
		}
		chooser := nativeRepositoryJudgment{NativeExecution: s.NativeExecution, repository: review.Repository, subject: "contribution:" + factory.ContributionReviewInvitationID(review) + ":" + cap.ID, worker: &cap}
		chooser.admit = func(ctx context.Context) error {
			if _, err := (service.WorkerAdmission{Store: s.Store, Inspect: s.InspectRun}).Prepare(ctx, cap); err != nil {
				return err
			}
			current, err := s.Store.ContributionContext(ctx, review.Source)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(current, review) {
				return store.ErrStaleRevision
			}
			return nil
		}
		questions := make(map[string]factory.SystemOneWorkerQuestion, len(factory.ContributionQuestions))
		for i, question := range factory.ContributionQuestions {
			questions[strconv.Itoa(i)] = factory.SystemOneWorkerQuestion{Instructions: question, Criteria: factory.ContributionOptions()}
		}
		result, err := (nativeWorkerJudgments{chooser}).EvaluateQuestions(ctx, "choice", factory.ContributionJudgmentState(review, role), questions)
		if err != nil {
			return calls, err
		}
		var answers map[string]struct {
			Choice        string             `json:"choice"`
			Confidence    float64            `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
			Type          string             `json:"type"`
		}
		raw, err := json.Marshal(result["answers"])
		if err != nil {
			return calls, err
		}
		if err := json.Unmarshal(raw, &answers); err != nil {
			return calls, err
		}
		model, _ := result["model"].(string)
		for i, question := range factory.ContributionQuestions {
			answer := answers[strconv.Itoa(i)]
			calls = append(calls, factory.ContributionCall{Assignment: role.Assignment, Question: question, Answer: factory.PriorityAnswer{Selected: answer.Choice, Confidence: answer.Confidence, Probabilities: answer.Probabilities, Model: model}})
		}
	}
	return calls, nil
}
