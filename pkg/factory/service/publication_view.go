package service

import (
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
)

// PublicationOperations renders one assignment's publication:
// its stage and outcome, the exact linked PR once creation commits,
// and each conditional operation's effect with its bounded native
// reason. Absent until publication starts.
type PublicationOperations struct {
	PublishCompletion    string `json:"publish_completion,omitempty"`
	PublishCancellation  string `json:"publish_cancellation,omitempty"`
	PRCreateCompletion   string `json:"pr_create_completion,omitempty"`
	PRCreateCancellation string `json:"pr_create_cancellation,omitempty"`
	WithdrawRequested    bool   `json:"withdraw_requested"`
	PublishEffect        string `json:"publish_effect"`
	PublishReason        string `json:"publish_reason,omitempty"`
	PRCreateEffect       string `json:"pr_create_effect"`
	PRCreateReason       string `json:"pr_create_reason,omitempty"`
	Stage                string `json:"stage"`
	Reason               string `json:"reason,omitempty"`
	PRNumber             string `json:"pr_number,omitempty"`
	PRID                 string `json:"pr_id,omitempty"`
	PublishOperation     string `json:"publish_operation,omitempty"`
	PRCreateOp           string `json:"pr_create_operation,omitempty"`
}

func PublicationOperationsView(p factory.Publication) *PublicationOperations {
	rendered := &PublicationOperations{
		PublishCompletion: p.Publish.Completion, PublishCancellation: p.Publish.Cancellation,
		PRCreateCompletion: p.PRCreate.Completion, PRCreateCancellation: p.PRCreate.Cancellation,
		WithdrawRequested: p.WithdrawRequested,
		PublishEffect:     p.Publish.Effect, PublishReason: p.Publish.Reason,
		PRCreateEffect: p.PRCreate.Effect, PRCreateReason: p.PRCreate.Reason,
		Stage: p.Stage, Reason: p.Reason,
		PublishOperation: p.Publish.OperationID, PRCreateOp: p.PRCreate.OperationID,
	}
	if p.PRNumber != 0 {
		rendered.PRNumber = strconv.FormatInt(p.PRNumber, 10)
		rendered.PRID = strconv.FormatInt(p.PRID, 10)
	}
	return rendered
}
