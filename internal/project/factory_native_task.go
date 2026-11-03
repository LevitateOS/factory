package project

import "errors"

// FactoryNativeTask addresses the live native task controller. The authenticated
// Factory transport supplies the execution binding; a worker supplies only its
// selected task. This is a request to the existing task owner, not a new grant.
type FactoryNativeTask struct {
	Project        string                     `json:"project"`
	Run            string                     `json:"run"`
	InvocationID   string                     `json:"invocation_id"`
	ManifestDigest string                     `json:"manifest_digest"`
	Action         string                     `json:"action"`
	Arguments      FactoryNativeTaskArguments `json:"arguments"`
}

type FactoryNativeTaskArguments struct {
	TaskID string `json:"task_id"`
}

type FactoryNativeTaskState struct {
	Result map[string]any `json:"result"`
}

func (r FactoryNativeTask) Validate() error {
	if !ValidID(r.Project) || !ValidFactoryRunID(r.Run) || !ValidInvocation(r.InvocationID) || !ValidDigest(r.ManifestDigest) || !ValidTaskKey(r.Arguments.TaskID) {
		return errors.New("native task request lacks its exact execution binding")
	}
	if r.Action != "start_task" && r.Action != "release_output" {
		return errors.New("unsupported native task action")
	}
	return nil
}
