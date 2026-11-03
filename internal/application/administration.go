package application

import "errors"

// Record grants one stable Soda/Forgejo administrator access to an application.
// Observation is nil until a matching native operation has been confirmed;
// failed or interrupted native calls must explicitly clear it to unknown.
type Record struct {
	Application     Spec         `json:"application"`
	AdministratorID int64        `json:"administrator_id"`
	Observation     *Observation `json:"observation"`
}

func (r Record) Validate() error {
	if err := r.Application.Validate(); err != nil {
		return err
	}
	if r.AdministratorID <= 0 {
		return errors.New("application administrator is required")
	}
	if r.Observation != nil {
		return r.Observation.Validate(r.Application)
	}
	return nil
}

func (r Record) Authorize(actorID int64) error {
	if actorID <= 0 || actorID != r.AdministratorID {
		return errors.New("application administration denied")
	}
	return r.Validate()
}

// Request is the native operation transport. The executor independently checks
// admitted image labels, private data and the current container generation.
type Request struct {
	Application       Spec              `json:"application"`
	Action            string            `json:"action"`
	ExpectedContainer string            `json:"expected_container"`
	Restore           *RestoreOperation `json:"restore,omitempty"`
}

func (r Request) Validate() error {
	if err := r.Application.Validate(); err != nil {
		return err
	}
	switch r.Action {
	case "install", "inspect", "verify_build", "start", "stop", "replace", "restore", "restore_abort":
	default:
		return errors.New("invalid application operation")
	}
	if !r.validGeneration() {
		return errors.New("invalid application container generation")
	}
	if r.Action == "restore" || r.Action == "restore_abort" {
		if r.Restore == nil || r.Restore.Validate() != nil || r.Restore.State != "pending" || r.ExpectedContainer != r.Restore.ExpectedContainer {
			return errors.New("invalid application restore intent")
		}
		expected := r.Restore.Next
		if r.Action == "restore_abort" {
			expected = r.Restore.Previous
		}
		if r.Application != expected {
			return errors.New("application differs from restore intent")
		}
	} else if r.Restore != nil {
		return errors.New("restore intent is not valid for this operation")
	}
	return nil
}

func (r Request) validGeneration() bool {
	if r.ExpectedContainer != "" {
		return r.Action != "install" && digest.MatchString(r.ExpectedContainer)
	}
	return r.Action == "install" || r.Action == "inspect"
}

// Observation records confirmed native state, never desired state. An empty
// container is allowed only for a stopped, absent application inspection.
type Observation struct {
	ID               string `json:"id"`
	Revision         int64  `json:"revision"`
	Container        string `json:"container"`
	Image            string `json:"image"`
	Running          bool   `json:"running"`
	Ready            bool   `json:"ready"`
	Origin           string `json:"origin"`
	BundleFileSHA256 string `json:"bundle_file_sha256,omitempty"`
}

func (o Observation) Validate(s Spec) error {
	if o.BundleFileSHA256 != "" && (!digest.MatchString(o.BundleFileSHA256) || o.Container == "") {
		return errors.New("application bundle readback lacks its native container")
	}
	if !o.matches(s) || !o.validState() {
		return errors.New("application observation differs from admitted references")
	}
	return nil
}

func (o Observation) matches(s Spec) bool {
	return o.ID == s.ID && o.Revision == s.Revision && o.Image == s.Image && o.Origin == s.Origin
}
func (o Observation) validState() bool {
	if o.Container == "" {
		return !o.Running && !o.Ready
	}
	return digest.MatchString(o.Container) && (!o.Ready || o.Running)
}

// SameObservation supports compare-and-swap publication of a native result.
// Nil is explicit unknown state, not a successful stopped observation.
func SameObservation(left, right *Observation) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
