package factory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// ApprovedAppCheck is an explicit whole-app assertion in the administrator's
// protected check.sh, not a worker claim or an ordinary setup success. The
// native preparation executor runs that exact approved script on the delivery
// source. Each listed outcome is asserted by the script's zero exit status.
// External fixture grants and arbitrary check catalogs are not implied.
type ApprovedAppCheck struct {
	IntentID         string   `json:"intent_id"`
	AcceptedRevision string   `json:"accepted_revision"`
	Outcomes         []string `json:"outcomes"`
}

const (
	ApprovedAppCheckPrefix = "# soda-app-verification: "
	ApprovedAppCheckID     = project.VerificationProjectCheck
)

// ApplicationBundleGuidance is conditional execution guidance, not a scope
// classifier or permission to change protected preparation inputs.
const ApplicationBundleGuidance = "When the accepted product is a Soda-hosted Canlang application, its useful delivery includes the canonical can-soda build output at .soda/application/bundle.json in the approved project.check checkout. For the requested isolated preview, also build and check .soda/application/preview/bundle.json from that source with its own declared HTTPS origin and independent environment/resource configuration. Preview and company origins must differ; never rewrite an already checked bundle or share live data/owner credentials. Plan and implement the application source, deployment descriptor and environment configuration needed for the protected check.sh to produce that regular file after real outcome checks pass. Compile the actual integrated source; never copy an unrelated bundle or write a success-shaped fixture. Use the intended canonical HTTPS CAN_AUTH_ORIGIN from the admitted product/configuration; if it is unknown, ask rather than inventing an address. For an update preserve the admitted origin and compatible storage identities, with no implicit schema migration. The native helper retains the exact bytes only after that approved check succeeds and retires. A protected script change requires its existing administrator approval; workers cannot replace the approved catalog or bypass source/check binding. Product Lead proposes verification_manifest_proposal only from the existing approved catalog and carries missing build/configuration prerequisites as unresolved. No bundle is required for non-application work. A passed check and a packaged image grant no deployment, first-owner identity, start, access or release authority."

func ParseApprovedAppCheck(script []byte) (ApprovedAppCheck, error) {
	var manifest ApprovedAppCheck
	found := false
	for _, line := range strings.Split(string(script), "\n") {
		if !strings.HasPrefix(line, ApprovedAppCheckPrefix) {
			continue
		}
		if found {
			return manifest, errors.New("approved check declares multiple verification manifests")
		}
		found = true
		decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimPrefix(line, ApprovedAppCheckPrefix)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&manifest); err != nil {
			return manifest, errors.New("approved app verification manifest is invalid")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return manifest, errors.New("approved app verification manifest has trailing content")
		}
	}
	if !found {
		return manifest, errors.New("approved check does not declare application outcomes")
	}
	return manifest, manifest.Validate()
}

func (manifest ApprovedAppCheck) Validate() error {
	if !project.ValidDecisionID(manifest.IntentID) || !project.ValidDecisionID(manifest.AcceptedRevision) || len(manifest.Outcomes) == 0 || len(manifest.Outcomes) > MCPMaxManifestOutcomes {
		return errors.New("approved app verification manifest lacks its accepted scope")
	}
	seen := map[string]bool{}
	for _, outcome := range manifest.Outcomes {
		if !project.ValidDecisionID(outcome) || seen[outcome] {
			return errors.New("invalid or duplicate approved application outcome")
		}
		seen[outcome] = true
	}
	return nil
}

func (m ApprovedAppCheck) Digest() string {
	raw, _ := json.Marshal(m)
	return MCPInputDigest(raw)
}

func (m ApprovedAppCheck) BindsIntent(revision IntentRevision) bool {
	want, got := slices.Clone(ResolvedScope(revision)), slices.Clone(m.Outcomes)
	slices.Sort(want)
	slices.Sort(got)
	return revision.State == "accepted" && m.IntentID == revision.Intent && m.AcceptedRevision == revision.ID && slices.Equal(want, got)
}

// DeliveryPreparationDigest identifies immutable preparation inputs, excluding
// the observation revision which advances when native state is refreshed.
func DeliveryPreparationDigest(preparation project.Preparation) string {
	preparation.Revision = 0
	raw, _ := json.Marshal(preparation)
	return MCPInputDigest(raw)
}
