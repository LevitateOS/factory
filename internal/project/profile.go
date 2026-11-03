// Profile is the immutable creation identity shared by storage, the
// dashboard and the privileged project boundary. It is not an image
// registry or runtime selector.
package project

import (
	"errors"
	"regexp"
	"strings"

	"github.com/levitateos/sodaos/internal/strictjson"
)

const RockyHeadless = "rocky-headless"

type Profile struct {
	ID           string `json:"id"`
	Distribution string `json:"distribution"`
	Version      string `json:"version"`
	Interface    string `json:"interface"`
	Architecture string `json:"architecture"`
	Image        string `json:"image"`
	Revision     string `json:"revision"`
}

var (
	digest   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	revision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	version  = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3}){0,2}$`)
)

func (p Profile) Validate() error {
	if p.ID != RockyHeadless || p.Distribution != "rocky" || p.Interface != "headless" || !version.MatchString(p.Version) || (p.Architecture != "amd64" && p.Architecture != "arm64") || !digest.MatchString(p.Image) || !revision.MatchString(p.Revision) {
		return errors.New("unsupported or incomplete project creation profile")
	}
	return nil
}

func Decode(raw string) (*Profile, error) {
	if len(raw) > 1024 {
		return nil, errors.New("oversized project profile")
	}
	var p Profile
	if err := strictjson.Decode(strings.NewReader(raw), &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}
