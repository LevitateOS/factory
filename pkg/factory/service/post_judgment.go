package service

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

// ChoicePostJudge translates the post-routing policy through the same Choice
// port used by contribution reviews. Transport, model and credentials belong to
// its supplied chooser. A routing answer grants no authority or dispatch.
type ChoicePostJudge struct{ Chooser control.ProductJudgment }

func (j *ChoicePostJudge) JudgePost(ctx context.Context, input PostJudgeInput) (PostJudgment, error) {
	var empty PostJudgment
	if j == nil || j.Chooser == nil {
		return empty, control.ErrPriorityUnavailable
	}
	choices := PostJudgmentChoices(input)
	answer, err := j.Chooser.Select(ctx, PostJudgeState(input), PostRouteRubric, choices)
	if err != nil {
		return empty, err
	}
	judgment, err := PostJudgmentFromSelection(answer.Selected, input.Candidates)
	if err != nil {
		return empty, err
	}
	judgment.Routing = PostRouting(answer, PostChoiceKeys(input.Candidates), input.Candidates)
	return judgment, nil
}

func PostJudgmentChoices(input PostJudgeInput) map[string]any {
	keys := PostChoiceKeys(input.Candidates)
	choices := make(map[string]any, len(keys))
	for _, key := range keys {
		switch key {
		case factory.PostRouteNewIssue:
			choices[key] = "A concrete new deliverable or change that no listed issue already covers."
		case factory.PostRouteDiscussion:
			choices[key] = "Conversation or acknowledgement without a request to change the product."
		default:
			if question, ok := PostClarificationQuestion(key); ok {
				choices[key] = "The comment requires an answer to: " + question
			} else {
				choices[key] = "Continue the specific listed issue only if its objective covers this requested result."
			}
		}
	}
	return choices
}
