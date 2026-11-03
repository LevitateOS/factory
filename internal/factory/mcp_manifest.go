package factory

// D16 verification-manifest proposal/catalog-reference contract
// (FW38).
//
// Product Lead's D02 graph proposal carries a
// verification_manifest_proposal: outcome references, approved
// check-catalog IDs/parameters, artifact outputs and fixture
// requirements. Graph choice/adoption evaluates semantic outcome
// coverage and records the selected manifest digest with the accepted
// intent revision. FW40 consumes this contract for adoption; FW45
// consumes the adopted manifest for Test Pilot verification. This
// file owns the proposal, approved Project check snapshot and exact adopted
// binding. The separate trusted per-run executor must consume that binding.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// Catalog parameter bounds.
const (
	MCPMaxCatalogParams     = 16
	MCPMaxCatalogParamChars = 256
	MCPMaxManifestOutcomes  = 64
	MCPMaxManifestChecks    = 128
	MCPMaxManifestArtifacts = 64
	MCPMaxManifestFixtures  = 16
	MCPMaxCatalogIDChars    = 128
)

// ApprovedCheck is one approved check-catalog entry: actual argv,
// journey actions and assertions live behind the ID, never in
// model-generated executable strings.
type ApprovedCheck struct {
	// ID names the catalog entry, e.g. "web.journey.smoke.v3".
	ID string `json:"id"`
	// RequiredParams names the parameters a proposal must supply.
	RequiredParams []string `json:"required_params,omitempty"`
	// ParamValueChars bounds every parameter value length.
	ParamValueChars int      `json:"param_value_chars,omitempty"`
	AllowedParams   []string `json:"allowed_params,omitempty"`
	Artifacts       []string `json:"artifacts"`
	Fixtures        []string `json:"fixtures"`
}

const (
	ProjectCheckReceiptArtifact = project.VerificationReceiptArtifact
	ProjectCheckPreparedFixture = project.VerificationPreparedFixture
)

// ApprovedProjectCheckCatalog binds the existing approved Project check to its
// protected preparation. It grants no arbitrary executable, parameter, artifact
// path or external fixture authority.
type ApprovedProjectCheckCatalog struct {
	Project           string `json:"project"`
	Preparation       string `json:"preparation"`
	Requirements      string `json:"requirements"`
	Approval          string `json:"approval"`
	SourceCommit      string `json:"source_commit"`
	PreparationDigest string `json:"preparation_digest"`
	CheckDigest       string `json:"check_digest"`
}

func (c ApprovedProjectCheckCatalog) Validate() error {
	if !project.ValidID(c.Project) || !project.ValidPreparationID(c.Preparation) || !project.ValidDecisionID(c.Requirements) || !project.ValidDecisionID(c.Approval) || !ValidCommit(c.SourceCommit) || !ValidDigest(c.PreparationDigest) || !ValidDigest(c.CheckDigest) {
		return errors.New("approved Project check catalog lacks its exact preparation")
	}
	return nil
}

func (c ApprovedProjectCheckCatalog) LookupCheck(id string) (ApprovedCheck, bool) {
	if c.Validate() != nil || id != ApprovedAppCheckID {
		return ApprovedCheck{}, false
	}
	return ApprovedCheck{ID: ApprovedAppCheckID, Artifacts: []string{ProjectCheckReceiptArtifact}, Fixtures: []string{ProjectCheckPreparedFixture}}, true
}

// AdoptedVerificationManifest is recorded with the selected native graph. Its
// catalog snapshot is authority from the approved preparation, never model text.
// Native per-run execution must consume this binding before it can verify work.
type AdoptedVerificationManifest struct {
	Intent           string                       `json:"intent"`
	AcceptedRevision string                       `json:"accepted_revision"`
	ScopeDigest      string                       `json:"scope_digest"`
	Proposal         VerificationManifestProposal `json:"proposal"`
	Catalog          ApprovedProjectCheckCatalog  `json:"catalog"`
}

func (m AdoptedVerificationManifest) ValidateFor(r IntentRevision) error {
	if r.State != "accepted" || r.Validate() != nil || m.Intent != r.Intent || m.AcceptedRevision != r.ID || m.ScopeDigest != r.Digest() || m.Catalog.Validate() != nil {
		return errors.New("adopted verification manifest differs from accepted scope")
	}
	if err := m.Proposal.ValidateOutcomes(r); err != nil {
		return err
	}
	return ValidateManifestProposal(m.Proposal, m.Catalog)
}

func (m AdoptedVerificationManifest) Digest() string {
	raw, _ := json.Marshal(m)
	return MCPInputDigest(raw)
}

// ApprovedCatalog resolves catalog IDs to their approved entries.
// The protected Project preparation backs this interface in production.
type ApprovedCatalog interface {
	LookupCheck(id string) (ApprovedCheck, bool)
}

// ManifestCheckRef binds one accepted outcome to one approved check
// with concrete parameters, expected artifact outputs and fixture
// needs.
type ManifestCheckRef struct {
	Outcome   string `json:"outcome"`
	CatalogID string `json:"catalog_id"`
	// Params carries catalog parameters within catalog bounds.
	Params map[string]string `json:"params,omitempty"`
	// Artifacts names the artifact outputs the check must produce.
	Artifacts []string `json:"artifacts,omitempty"`
	// Fixtures names the fixture requirements the check needs.
	Fixtures []string `json:"fixtures,omitempty"`
}

// VerificationManifestProposal is Product Lead's in-scope planning
// payload: every accepted outcome maps to named required checks.
// A generated proposal cannot adopt its own verification
// definitions; adoption records the digest, never the authority.
type VerificationManifestProposal struct {
	Outcomes []string           `json:"outcomes"`
	Checks   []ManifestCheckRef `json:"checks"`
	// FixtureGrants names the fixture grants the manifest needs.
	// Missing coverage stays a named preparation gate, never silent.
	FixtureGrants []string `json:"fixture_grants,omitempty"`
}

func ParseVerificationManifestProposal(raw []byte) (VerificationManifestProposal, error) {
	var proposal VerificationManifestProposal
	if len(raw) > MCPMaxManifestBytes {
		return proposal, errors.New("manifest proposal exceeds its byte bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil {
		return proposal, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return proposal, errors.New("manifest proposal has trailing content")
	}
	return proposal, nil
}

func (p VerificationManifestProposal) Digest() string {
	raw, _ := json.Marshal(p)
	return MCPInputDigest(raw)
}

func (p VerificationManifestProposal) ValidateOutcomes(r IntentRevision) error {
	want := map[string]bool{}
	for _, outcome := range ResolvedScope(r) {
		want[outcome] = true
	}
	got, err := validateManifestOutcomes(p)
	if err != nil || len(want) == 0 || len(want) != len(got) {
		return errors.New("verification manifest does not cover the exact product outcomes")
	}
	for outcome := range got {
		if !want[outcome] {
			return errors.New("verification manifest names a foreign product outcome")
		}
	}
	return nil
}

// ValidateManifestProposal checks the proposal against the approved
// catalog and the accepted outcomes: complete outcome coverage with
// no duplicate/unknown/missing outcome, approved catalog IDs,
// parameter bounds, and artifact outputs plus fixture requirements
// present on every check.
func validateManifestOutcomes(proposal VerificationManifestProposal) (map[string]bool, error) {
	if len(proposal.Outcomes) == 0 {
		return nil, errors.New("manifest proposal names no outcome")
	}
	if len(proposal.Outcomes) > MCPMaxManifestOutcomes {
		return nil, errors.New("manifest proposal exceeds outcome bound")
	}
	accepted := make(map[string]bool, len(proposal.Outcomes))
	for _, outcome := range proposal.Outcomes {
		if outcome == "" || len(outcome) > MCPMaxRefChars {
			return nil, errors.New("invalid manifest outcome reference")
		}
		if accepted[outcome] {
			return nil, errors.New("duplicate manifest outcome")
		}
		accepted[outcome] = true
	}
	return accepted, nil
}

func validateManifestCheck(check *ManifestCheckRef, approved ApprovedCheck) error {
	if len(check.Artifacts) == 0 {
		return errors.New("manifest check names no artifact output")
	}
	if len(check.Artifacts) > MCPMaxManifestArtifacts {
		return errors.New("manifest check exceeds artifact bound")
	}
	for _, artifact := range check.Artifacts {
		if artifact == "" || len(artifact) > MCPMaxRefChars || strings.ContainsRune(artifact, 0) || !containsManifestRef(approved.Artifacts, artifact) {
			return errors.New("invalid manifest artifact reference")
		}
	}
	if len(check.Fixtures) > MCPMaxManifestFixtures {
		return errors.New("manifest check exceeds fixture bound")
	}
	for _, fixture := range check.Fixtures {
		if fixture == "" || len(fixture) > MCPMaxRefChars || strings.ContainsRune(fixture, 0) || !containsManifestRef(approved.Fixtures, fixture) {
			return errors.New("invalid manifest fixture requirement")
		}
	}
	for _, fixture := range approved.Fixtures {
		if !containsManifestRef(check.Fixtures, fixture) {
			return errors.New("manifest check omits its approved fixture requirement")
		}
	}
	for _, artifact := range approved.Artifacts {
		if !containsManifestRef(check.Artifacts, artifact) {
			return errors.New("manifest check omits its approved artifact output")
		}
	}
	return nil
}

func ValidateManifestProposal(proposal VerificationManifestProposal, catalog ApprovedCatalog) error {
	accepted, err := validateManifestOutcomes(proposal)
	if err != nil {
		return err
	}
	if len(proposal.Checks) == 0 {
		return errors.New("manifest proposal names no check")
	}
	if len(proposal.Checks) > MCPMaxManifestChecks {
		return errors.New("manifest proposal exceeds check bound")
	}
	if len(proposal.FixtureGrants) != 0 {
		return errors.New("external verification fixture grants are unavailable")
	}
	covered := make(map[string]bool, len(proposal.Outcomes))
	for i := range proposal.Checks {
		check := &proposal.Checks[i]
		if !accepted[check.Outcome] {
			return errors.New("manifest check references unknown outcome")
		}
		covered[check.Outcome] = true
		if err := validateCatalogRef(check.CatalogID, check.Params, catalog); err != nil {
			return err
		}
		approved, _ := catalog.LookupCheck(check.CatalogID)
		if err := validateManifestCheck(check, approved); err != nil {
			return err
		}
	}
	for outcome := range accepted {
		if !covered[outcome] {
			return errors.New("manifest proposal leaves outcome unverified: " + outcome)
		}
	}
	return nil
}

func validateCatalogRef(id string, params map[string]string, catalog ApprovedCatalog) error {
	if id == "" || len(id) > MCPMaxCatalogIDChars {
		return errors.New("invalid check catalog reference")
	}
	if catalog == nil {
		return errors.New("no approved check catalog")
	}
	approved, ok := catalog.LookupCheck(id)
	if !ok {
		return errors.New("check catalog entry not approved: " + id)
	}
	if len(params) > MCPMaxCatalogParams {
		return errors.New("check parameters exceed bound")
	}
	bound := approved.ParamValueChars
	if bound <= 0 || bound > MCPMaxCatalogParamChars {
		bound = MCPMaxCatalogParamChars
	}
	for key, value := range params {
		if key == "" || len(key) > MCPMaxRefChars || !containsManifestRef(approved.AllowedParams, key) {
			return errors.New("invalid check parameter name")
		}
		if len(value) > bound {
			return errors.New("check parameter exceeds catalog bound: " + key)
		}
	}
	for _, required := range approved.RequiredParams {
		if _, ok := params[required]; !ok {
			return errors.New("check misses required catalog parameter: " + required)
		}
	}
	return nil
}

func containsManifestRef(refs []string, want string) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}
