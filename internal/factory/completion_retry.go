package factory

import "errors"

const (
	ActionCompletionRetry  = "retry_completion"
	CommandCompletionRetry = "retry-completion"
)

// CompletionRetryRequest names the retained attention cycle. The owner
// command supplies the successor identity; it cannot replace its evidence.
type CompletionRetryRequest struct {
	EventID            string `json:"event_id"`
	ExpectedPriorCycle string `json:"expected_prior_cycle"`
}

func (r CompletionRetryRequest) Validate() error {
	if !ValidID(r.EventID) || !ValidID(r.ExpectedPriorCycle) {
		return errors.New("invalid completion retry scope")
	}
	return nil
}

type CompletionRetryDecision struct {
	CommandID      string `json:"command_id"`
	EventID        string `json:"event_id"`
	PriorCycle     string `json:"prior_cycle"`
	SuccessorCycle string `json:"successor_cycle"`
	Repository     int64  `json:"repository,string"`
	Queued         bool   `json:"queued"`
}
