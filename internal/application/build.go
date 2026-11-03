package application

import (
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

type BuildRequest struct {
	project.FactoryVerificationChecks
	Environment string `json:"environment"`
}

func ValidBuildEnvironment(environment string) bool {
	return environment == "company" || environment == "preview"
}

func (r BuildRequest) Validate() error {
	if !ValidBuildEnvironment(r.Environment) {
		return errors.New("choose company or preview application build")
	}
	return r.FactoryVerificationChecks.Validate()
}

// BuildArtifact is a native image packaged from the exact retained bundle of
// one passed approved check. It owns no app data, account, setup or release.
type BuildArtifact struct {
	Project          string `json:"project"`
	Run              string `json:"run"`
	Incarnation      string `json:"incarnation"`
	ManifestDigest   string `json:"manifest_digest"`
	SourceCommit     string `json:"source_commit"`
	Environment      string `json:"environment"`
	Name             string `json:"name"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	BundleSHA256     string `json:"bundle_sha256"`
	BundleFileSHA256 string `json:"bundle_file_sha256"`
	StorageSHA256    string `json:"storage_sha256"`
	ToolsSHA256      string `json:"tools_sha256"`
	RuntimeImage     string `json:"runtime_image"`
	Image            string `json:"image"`
	Origin           string `json:"origin"`
}

func (b BuildArtifact) Setup(id, data string, port int) SetupSpec {
	return SetupSpec{ID: id, Name: b.Name, ArtifactSHA256: b.ArtifactSHA256, BundleSHA256: b.BundleSHA256, ToolsSHA256: b.ToolsSHA256, RuntimeImage: b.RuntimeImage, Image: b.Image, Data: data, Origin: b.Origin, Port: port}
}

func (b BuildArtifact) ValidateFor(request BuildRequest) error {
	if request.Validate() != nil || b.Environment != request.Environment || b.Project != request.Project || b.Run != request.Run || b.Incarnation != request.Incarnation || b.ManifestDigest != request.ManifestDigest || !project.ValidCommit(b.SourceCommit) || !digest.MatchString(b.BundleFileSHA256) || !digest.MatchString(b.StorageSHA256) {
		return errors.New("application build differs from its native verification receipt")
	}
	return b.Setup("a000000000000000000000000", "a000000000000000000000001", 20000).Validate()
}
