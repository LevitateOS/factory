package service

// workCapabilityReasons are the bounded codes behind one action
// capability. Authority codes describe the viewer; state codes describe
// the recorded work. Admission revalidates at act time.
const (
	WorkWriteRequired        = "repository_write_required"
	WorkNotVisible           = "repository_not_visible"
	WorkAuthorityUnavailable = "native_authority_unavailable"
	workCapSession           = "session_changed"
	workCapNoRun             = "no_run"
	workCapUnsettled         = "run_unsettled"
	workCapSettled           = "run_settled"
	workCapRetryQueued       = "retry_queued"
	workCapMembership        = "membership_required"
	workCapDispatchOpen      = "dispatch_open"
	workCapDispatchShut      = "dispatch_closed"
)

type WorkCapability struct {
	Reason    string `json:"reason,omitempty"`
	Available bool   `json:"available"`
}

// WorkCapabilities reports the viewer's current intervention powers
// for one issue: read-only watch, run stop/retry/takeover and repository
// pause/resume. Each entry carries its refusal reason when unavailable;
// the owning lifecycle APIs remain authoritative at act time.
type WorkCapabilities struct {
	Watch    WorkCapability `json:"watch"`
	Stop     WorkCapability `json:"stop"`
	Retry    WorkCapability `json:"retry"`
	Takeover WorkCapability `json:"takeover"`
	Pause    WorkCapability `json:"pause"`
	Resume   WorkCapability `json:"resume"`
}

type workCapabilityInput struct {
	AuthorityReason string
	HasRun          bool
	RunSettled      bool
	RetryQueued     bool
	Member          bool
	DispatchOpen    bool
	SessionCurrent  bool
}

func workCapabilityGates(in workCapabilityInput) (mutating, attempt string) {
	mutating = in.AuthorityReason
	if mutating == "" && !in.SessionCurrent {
		mutating = workCapSession
	}
	attempt = mutating
	if attempt == "" {
		switch {
		case !in.HasRun:
			attempt = workCapNoRun
		case !in.RunSettled:
			attempt = workCapUnsettled
		}
	}
	return mutating, attempt
}

func workStopCapability(in workCapabilityInput, mutating string) WorkCapability {
	view := WorkCapability{Available: mutating == "" && in.HasRun && !in.RunSettled}
	switch {
	case mutating != "":
		view.Reason = mutating
	case !in.HasRun:
		view.Reason = workCapNoRun
	case in.RunSettled:
		view.Reason = workCapSettled
	}
	return view
}

func workRetryCapability(in workCapabilityInput, attempt string) WorkCapability {
	view := WorkCapability{Available: attempt == "" && !in.RetryQueued}
	switch {
	case attempt != "":
		view.Reason = attempt
	case in.RetryQueued:
		view.Reason = workCapRetryQueued
	}
	return view
}

func workTakeoverCapability(in workCapabilityInput, attempt string) WorkCapability {
	view := WorkCapability{Available: attempt == "" && in.Member}
	switch {
	case attempt != "":
		view.Reason = attempt
	case !in.Member:
		view.Reason = workCapMembership
	}
	return view
}

func workPauseCapability(in workCapabilityInput, mutating string) WorkCapability {
	view := WorkCapability{Available: mutating == "" && in.DispatchOpen}
	switch {
	case mutating != "":
		view.Reason = mutating
	case !in.DispatchOpen:
		view.Reason = workCapDispatchShut
	}
	return view
}

func workResumeCapability(in workCapabilityInput, mutating string) WorkCapability {
	view := WorkCapability{Available: mutating == "" && !in.DispatchOpen}
	switch {
	case mutating != "":
		view.Reason = mutating
	case in.DispatchOpen:
		view.Reason = workCapDispatchOpen
	}
	return view
}

func workCapabilitiesDTO(in workCapabilityInput) WorkCapabilities {
	view := WorkCapabilities{}
	view.Watch = WorkCapability{Available: in.AuthorityReason == "" || in.AuthorityReason == WorkWriteRequired}
	if !view.Watch.Available {
		view.Watch.Reason = in.AuthorityReason
	}
	if !in.SessionCurrent {
		view.Watch = WorkCapability{Reason: workCapSession}
	}
	mutating, attempt := workCapabilityGates(in)
	view.Stop = workStopCapability(in, mutating)
	view.Retry = workRetryCapability(in, attempt)
	view.Takeover = workTakeoverCapability(in, attempt)
	view.Pause = workPauseCapability(in, mutating)
	view.Resume = workResumeCapability(in, mutating)
	return view
}

// workRetryRequestView renders a queued explicit retry: the requested
// control, distinct from the confirmed attempt outcome it follows.
type workRetryRequestView struct {
	CommandID string `json:"command_id"`
	Reason    string `json:"reason"`
	Prior     string `json:"prior_run"`
	Queued    bool   `json:"queued"`
}

// WorkRequested carries requested controls that are not confirmed
// effects. Empty means no control request is outstanding.
type WorkRequested struct {
	Retry *workRetryRequestView `json:"retry,omitempty"`
}
