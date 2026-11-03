package project

import "errors"

func ValidTaskKey(key string) bool {
	return (TaskEvent{TaskKey: key}).validateTaskKey() == nil
}

// FactoryTaskCheckpoint is the appliance checkpoint wire request. The worker
// loads ownership and argv from its exact retained manifest. Factory retains
// runtime-neutral execution.CheckpointRequest/CheckpointReceipt records.
type FactoryTaskCheckpoint struct {
	Project        string `json:"project"`
	ID             string `json:"id"`
	Container      string `json:"container"`
	Invocation     string `json:"invocation"`
	ManifestDigest string `json:"manifest_digest"`
	TaskKey        string `json:"task_key,omitempty"`
	OutputDigest   string `json:"output_digest,omitempty"`
	ExpectedHead   string `json:"expected_head"`
}

func (r FactoryTaskCheckpoint) Validate(final bool) error {
	if !ValidID(r.Project) || !ValidFactoryRunID(r.ID) || !ValidDigest(r.Container) ||
		!ValidInvocation(r.Invocation) || !ValidDigest(r.ManifestDigest) || !ValidCommit(r.ExpectedHead) {
		return errors.New("checkpoint lacks its exact execution binding")
	}
	if final {
		if r.TaskKey != "" || r.OutputDigest != "" {
			return errors.New("final candidate cannot nominate task output")
		}
	} else if !ValidTaskKey(r.TaskKey) || !ValidDigest(r.OutputDigest) {
		return errors.New("checkpoint lacks its exact task output")
	}
	return nil
}

type CheckpointCheckResult struct {
	ID        string `json:"id"`
	Passed    bool   `json:"passed"`
	LogDigest string `json:"log_digest"`
}

type FactoryTaskCheckpointState struct {
	Request        FactoryTaskCheckpoint   `json:"request"`
	FinalCandidate bool                    `json:"final_candidate"`
	Status         string                  `json:"status"`
	Candidate      string                  `json:"candidate,omitempty"`
	Tree           string                  `json:"tree,omitempty"`
	ReceiptDigest  string                  `json:"receipt_digest,omitempty"`
	Checks         []CheckpointCheckResult `json:"checks,omitempty"`
	Reason         string                  `json:"reason,omitempty"`
}

func (s FactoryTaskCheckpointState) ValidateFor(request FactoryTaskCheckpoint, final bool) error {
	if err := request.Validate(final); err != nil {
		return err
	}
	if s.Request != request || s.FinalCandidate != final || len(s.Reason) > 256 || len(s.Checks) > 16 {
		return errors.New("native checkpoint response changed its binding")
	}
	switch s.Status {
	case "pending", "failed", "busy":
	case "committed":
		if !ValidCommit(s.Candidate) || !ValidCommit(s.Tree) || !ValidDigest(s.ReceiptDigest) || final && s.Candidate != request.ExpectedHead {
			return errors.New("native checkpoint lacks its verified candidate receipt")
		}
	default:
		return errors.New("native checkpoint status is unknown")
	}
	if s.Candidate != "" && !ValidCommit(s.Candidate) || s.Tree != "" && !ValidCommit(s.Tree) || s.ReceiptDigest != "" && !ValidDigest(s.ReceiptDigest) {
		return errors.New("native checkpoint receipt is malformed")
	}
	seen := map[string]bool{}
	for _, check := range s.Checks {
		if !ValidTaskKey(check.ID) || seen[check.ID] || !ValidDigest(check.LogDigest) {
			return errors.New("native checkpoint check evidence is malformed")
		}
		seen[check.ID] = true
	}
	return nil
}
