package project

import (
	"errors"
	"strings"
)

// FactoryHarnessPin is the staged appliance tool's wire and retained-packet
// identity: family, version, executable digest and exact container image. The
// host adapter translates inspection into a tagged execution.ToolObservation.
// Admission checks the selected family and captures the proved bundle exactly;
// already admitted runs keep those bytes when the current selection advances.
type FactoryHarnessPin struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Image   string `json:"image"`
}

// Validate rejects malformed or unpinned harness identities.
func (p FactoryHarnessPin) Validate() error {
	if !ValidHarnessFamily(p.Harness) || !ValidHarnessVersion(p.Version) {
		return errors.New("unsupported factory harness")
	}
	if !ValidDigest(p.SHA256) {
		return errors.New("invalid harness pin")
	}
	value, pinned := strings.CutPrefix(p.Image, "sha256:")
	if !pinned || !ValidDigest(value) {
		return errors.New("execution image is not pinned")
	}
	return nil
}
