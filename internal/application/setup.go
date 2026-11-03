package application

import "errors"

// SetupSpec admits immutable bytes and an address before Canlang has created
// its owner. It deliberately cannot be used as a normal application Spec.
type SetupSpec struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	BundleSHA256   string `json:"bundle_sha256"`
	ToolsSHA256    string `json:"tools_sha256"`
	RuntimeImage   string `json:"runtime_image"`
	Image          string `json:"image"`
	Data           string `json:"data"`
	Origin         string `json:"origin"`
	Port           int    `json:"port"`
}

func (s SetupSpec) Bind(owner string) Spec {
	return Spec{ID: s.ID, Revision: 1, Name: s.Name, ArtifactSHA256: s.ArtifactSHA256, BundleSHA256: s.BundleSHA256, ToolsSHA256: s.ToolsSHA256, RuntimeImage: s.RuntimeImage, Image: s.Image, Data: s.Data, OwnerTeam: owner, Origin: s.Origin, Port: s.Port}
}
func (s SetupSpec) Validate() error {
	// The fixed value is used only to reuse reference validation. No grant is
	// created until a verified Canlang receipt supplies the actual team.
	return s.Bind("00000000-0000-0000-0000-000000000000").Validate()
}

type SetupRecord struct {
	ID              string            `json:"id"`
	Application     SetupSpec         `json:"application"`
	AdministratorID int64             `json:"administrator_id"`
	ExpiresAt       int64             `json:"expires_at"`
	State           string            `json:"state"`
	Observation     *SetupObservation `json:"observation"`
}
type SetupObservation struct {
	Container string `json:"container"`
	Running   bool   `json:"running"`
	Ready     bool   `json:"ready"`
}

func (o SetupObservation) Validate() error {
	if (o.Container == "" && (o.Running || o.Ready)) || (o.Container != "" && !digest.MatchString(o.Container)) || (o.Ready && !o.Running) {
		return errors.New("invalid setup generation")
	}
	return nil
}
func (s SetupRecord) Validate() error {
	if !identifier.MatchString(s.ID) || s.AdministratorID <= 0 || s.ExpiresAt <= 0 || (s.State != "pending" && s.State != "complete" && s.State != "aborted") {
		return errors.New("invalid application setup")
	}
	if err := s.Application.Validate(); err != nil {
		return err
	}
	if s.Observation != nil {
		return s.Observation.Validate()
	}
	return nil
}

type SetupRequest struct {
	Setup             SetupRecord `json:"setup"`
	Action            string      `json:"action"`
	ExpectedContainer string      `json:"expected_container"`
}

func (r SetupRequest) Validate() error {
	if err := r.Setup.Validate(); err != nil {
		return err
	}
	if r.Setup.State != "pending" {
		return errors.New("setup is already settled")
	}
	switch r.Action {
	case "begin":
		if r.ExpectedContainer != "" && !digest.MatchString(r.ExpectedContainer) {
			return errors.New("invalid setup generation")
		}
	case "inspect":
		if r.ExpectedContainer != "" && !digest.MatchString(r.ExpectedContainer) {
			return errors.New("invalid setup generation")
		}
	case "abort":
		if r.ExpectedContainer != "" && !digest.MatchString(r.ExpectedContainer) {
			return errors.New("invalid setup generation")
		}
	case "complete":
		if !digest.MatchString(r.ExpectedContainer) {
			return errors.New("setup requires its exact generation")
		}
	default:
		return errors.New("invalid setup operation")
	}
	return nil
}

type SetupReceipt struct {
	Schema             string `json:"schema"`
	Operation          string `json:"operation"`
	App                string `json:"app"`
	ArtifactDigest     string `json:"artifactDigest"`
	BundleDigest       string `json:"bundleDigest"`
	StorageFingerprint string `json:"storageFingerprint"`
	OwnerTeam          string `json:"ownerTeam"`
	CompletedAt        int64  `json:"completedAt"`
}

func (r SetupReceipt) Validate(s SetupRecord) error {
	if r.Schema != "canlang.soda.setup.v1" || r.Operation != s.ID || r.App != s.Application.Name || r.ArtifactDigest != s.Application.ArtifactSHA256 || r.BundleDigest != s.Application.BundleSHA256 || !digest.MatchString(r.StorageFingerprint) || !team.MatchString(r.OwnerTeam) || r.CompletedAt <= 0 || r.CompletedAt > s.ExpiresAt {
		return errors.New("setup receipt differs from admission")
	}
	return nil
}

type SetupResult struct {
	Operation   string           `json:"operation"`
	Observation SetupObservation `json:"observation"`
	SetupURL    string           `json:"setup_url,omitempty"`
	Receipt     *SetupReceipt    `json:"receipt,omitempty"`
	Application *Observation     `json:"application,omitempty"`
}
