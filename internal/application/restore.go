package application

import "errors"

// RestoreOperation reserves a fresh data identity before any native effects.
// Retrying uses the same immutable intent until it is completed or aborted.
type RestoreOperation struct {
	ID                string `json:"id"`
	Snapshot          string `json:"snapshot"`
	Previous          Spec   `json:"previous"`
	Next              Spec   `json:"next"`
	ExpectedContainer string `json:"expected_container"`
	State             string `json:"state"`
}

func (s Spec) Restoration(next Spec) error {
	if s.Data == next.Data {
		return errors.New("restore requires fresh application data")
	}
	base := s
	base.Data = next.Data
	if err := base.Replacement(next); err != nil {
		return err
	}
	if s.ArtifactSHA256 != next.ArtifactSHA256 || s.BundleSHA256 != next.BundleSHA256 || s.ToolsSHA256 != next.ToolsSHA256 || s.RuntimeImage != next.RuntimeImage || s.Image != next.Image {
		return errors.New("restore requires the identical admitted application bundle")
	}
	return nil
}

func (r RestoreOperation) Validate() error {
	if !identifier.MatchString(r.ID) || !identifier.MatchString(r.Snapshot) || !digest.MatchString(r.ExpectedContainer) {
		return errors.New("invalid application restore identity")
	}
	if r.State != "pending" && r.State != "complete" && r.State != "aborted" {
		return errors.New("invalid application restore state")
	}
	return r.Previous.Restoration(r.Next)
}
