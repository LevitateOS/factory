package project

import (
	"errors"
	"path"
	"strings"
	"unicode"
)

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
	TaskID         string `json:"task_id"`
	Operation      string `json:"operation,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
	Path           string `json:"path,omitempty"`
	Offset         int    `json:"offset,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Expected       string `json:"expected,omitempty"`
	Replacement    string `json:"replacement,omitempty"`
	ExpectedDigest string `json:"expected_digest,omitempty"`
	Executable     *bool  `json:"executable,omitempty"`
}

func (a FactoryNativeTaskArguments) ValidateWorkspace() error {
	if !ValidTaskKey(a.TaskID) || a.Path == "" || len(a.Path) > 1024 || path.Clean(a.Path) != a.Path || path.IsAbs(a.Path) || strings.ContainsAny(a.Path, "\\*?[]{}") || strings.IndexFunc(a.Path, unicode.IsControl) >= 0 {
		return errors.New("workspace path or task is invalid")
	}
	for _, part := range strings.Split(a.Path, "/") {
		if part == "." || part == ".." || part == ".git" || part == ".soda-home" {
			return errors.New("workspace path is reserved or escapes its root")
		}
	}
	if a.Offset < 0 || a.Offset > 32*1024*1024 || len(a.Expected) > 32768 || len(a.Replacement) > 32768 {
		return errors.New("workspace text operation exceeds its bound")
	}
	if a.Operation != "executable" && a.Executable != nil {
		return errors.New("executable flag requires its file operation")
	}
	switch a.Operation {
	case "read":
		if a.Limit < 4 || a.Limit > 32768 || a.Expected != "" || a.Replacement != "" || a.ExpectedDigest != "" {
			return errors.New("workspace read is invalid")
		}
	case "edit":
		if !ValidInvocation(a.OperationID) {
			return errors.New("workspace edit lacks its operation identity")
		}
		if a.Limit != 0 || a.ExpectedDigest != "" {
			return errors.New("workspace edit does not accept limit or expected_digest; omit both, read the current text, and supply offset, expected and replacement with a fresh submission_id; this refused edit changed nothing")
		}
	case "mkdir", "remove":
		if !ValidInvocation(a.OperationID) || a.Offset != 0 || a.Limit != 0 || a.Expected != "" || a.Replacement != "" || a.Operation == "remove" && !ValidDigest(a.ExpectedDigest) || a.Operation == "mkdir" && a.ExpectedDigest != "" {
			return errors.New("workspace filesystem operation is invalid")
		}
	case "executable":
		if !ValidInvocation(a.OperationID) || a.Offset != 0 || a.Limit != 0 || a.Expected != "" || a.Replacement != "" || !ValidDigest(a.ExpectedDigest) || a.Executable == nil {
			return errors.New("workspace executable operation lacks its exact file binding")
		}
	default:
		return errors.New("unsupported workspace operation")
	}
	return nil
}

type FactoryNativeTaskState struct {
	Result map[string]any `json:"result"`
}

func (r FactoryNativeTask) Validate() error {
	if !ValidID(r.Project) || !ValidFactoryRunID(r.Run) || !ValidInvocation(r.InvocationID) || !ValidDigest(r.ManifestDigest) || !ValidTaskKey(r.Arguments.TaskID) {
		return errors.New("native task request lacks its exact execution binding")
	}
	if r.Action == "workspace" {
		return r.Arguments.ValidateWorkspace()
	}
	if r.Arguments != (FactoryNativeTaskArguments{TaskID: r.Arguments.TaskID}) || r.Action != "start_task" && r.Action != "release_output" {
		return errors.New("unsupported native task action")
	}
	return nil
}
