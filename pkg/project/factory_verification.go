package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	VerificationManifestVersion  = "project-check-v1"
	MaxVerificationManifest      = 64 * 1024
	VerificationProjectCheck     = "project.check"
	VerificationReceiptArtifact  = "project.check.receipt"
	VerificationPreparedFixture  = "project.preparation"
	MaxVerificationOutputExcerpt = 4 * 1024
)

// VerificationManifest is the exact run-bound native execution specification.
// The approved script lives in the protected preparation, never in model input.
// All outcome references share the one fixed check execution and receipt.
type VerificationManifest struct {
	Version               string                      `json:"version"`
	Run                   string                      `json:"run"`
	Project               string                      `json:"project"`
	Preparation           string                      `json:"preparation"`
	SourceCommit          string                      `json:"source_commit"`
	Deadline              time.Time                   `json:"deadline"`
	Intent                string                      `json:"intent"`
	AcceptedRevision      string                      `json:"accepted_revision"`
	ScopeDigest           string                      `json:"scope_digest"`
	AdoptedManifestDigest string                      `json:"adopted_manifest_digest"`
	Requirements          string                      `json:"requirements"`
	Approval              string                      `json:"approval"`
	PreparationDigest     string                      `json:"preparation_digest"`
	CheckDigest           string                      `json:"check_digest"`
	Outcomes              []string                    `json:"outcomes"`
	Checks                []VerificationManifestCheck `json:"checks"`
}

type VerificationManifestCheck struct {
	CatalogID string   `json:"catalog_id"`
	Outcomes  []string `json:"outcomes"`
	Artifacts []string `json:"artifacts"`
	Fixtures  []string `json:"fixtures"`
}

func (m VerificationManifest) Validate() error {
	if m.Version != VerificationManifestVersion || !ValidFactoryRunID(m.Run) || !ValidID(m.Project) || !ValidPreparationID(m.Preparation) || !ValidCommit(m.SourceCommit) || m.Deadline.IsZero() || !ValidDecisionID(m.Intent) || !ValidDecisionID(m.AcceptedRevision) || !ValidDigest(m.ScopeDigest) || !ValidDigest(m.AdoptedManifestDigest) || !ValidDecisionID(m.Requirements) || !ValidDecisionID(m.Approval) || !ValidDigest(m.PreparationDigest) || !ValidDigest(m.CheckDigest) {
		return errors.New("verification manifest lacks its exact approved run binding")
	}
	if len(m.Outcomes) < 1 || len(m.Outcomes) > 64 || len(m.Checks) != 1 {
		return errors.New("verification manifest lacks complete bounded check coverage")
	}
	seen := map[string]bool{}
	for _, outcome := range m.Outcomes {
		if !ValidDecisionID(outcome) || seen[outcome] {
			return errors.New("verification manifest repeats or invents an outcome")
		}
		seen[outcome] = true
	}
	check := m.Checks[0]
	if check.CatalogID != VerificationProjectCheck || len(check.Artifacts) != 1 || check.Artifacts[0] != VerificationReceiptArtifact || len(check.Fixtures) != 1 || check.Fixtures[0] != VerificationPreparedFixture || !slices.Equal(check.Outcomes, m.Outcomes) {
		return errors.New("verification manifest differs from its approved Project check")
	}
	return nil
}

func (m VerificationManifest) ValidateForRun(run FactoryRun) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if run.ExecutionPhase != ExecutionVerify || run.Role != RoleReviewer || m.Run != run.ID || m.Project != run.Project || m.Preparation != run.Preparation || m.SourceCommit != run.SourceCommit || !m.Deadline.Equal(run.Deadline) {
		return errors.New("verification manifest differs from its admitted run")
	}
	return nil
}

func MarshalVerificationManifest(m VerificationManifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err == nil && len(raw) > MaxVerificationManifest {
		return nil, errors.New("verification manifest exceeds its byte bound")
	}
	return raw, err
}

func ParseVerificationManifest(raw []byte) (VerificationManifest, error) {
	var m VerificationManifest
	if len(raw) == 0 || len(raw) > MaxVerificationManifest {
		return m, errors.New("verification manifest exceeds its byte bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return m, errors.New("verification manifest has trailing content")
	}
	return m, m.Validate()
}

// FactoryVerificationChecks addresses only the admitted run and its exact
// reserved native invocation. A retry observes that receipt, never another run.
type FactoryVerificationChecks struct {
	Project        string `json:"project"`
	Run            string `json:"run"`
	Incarnation    string `json:"incarnation"`
	ManifestDigest string `json:"manifest_digest"`
}

func (r FactoryVerificationChecks) Validate() error {
	if !ValidID(r.Project) || !ValidFactoryRunID(r.Run) || !ValidFactoryRunID(r.Incarnation) || !ValidDigest(r.ManifestDigest) {
		return errors.New("invalid verification checks identity")
	}
	return nil
}

type FactoryVerificationChecksState struct {
	Project                        string `json:"project"`
	Run                            string `json:"run"`
	Incarnation                    string `json:"incarnation"`
	ManifestDigest                 string `json:"manifest_digest"`
	Phase                          string `json:"phase"`
	SourceCommit                   string `json:"source_commit"`
	Preparation                    string `json:"preparation"`
	CheckDigest                    string `json:"check_digest"`
	ExitCode                       *int   `json:"exit_code,omitempty"`
	OutputDigest                   string `json:"output_digest,omitempty"`
	ApplicationBundleSHA256        string `json:"application_bundle_sha256,omitempty"`
	ApplicationPreviewBundleSHA256 string `json:"application_preview_bundle_sha256,omitempty"`
	// Only the approved native check's output is projected. Control characters
	// other than LF/TAB are escaped; the digest remains over native captured bytes.
	OutputExcerpt   string `json:"output_excerpt,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	Retirement      string `json:"retirement,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

func (s FactoryVerificationChecksState) ValidateFor(request FactoryVerificationChecks) error {
	for _, digest := range []string{s.ApplicationBundleSHA256, s.ApplicationPreviewBundleSHA256} {
		if digest != "" && (s.Phase != "passed" || !ValidDigest(digest)) {
			return errors.New("application build receipt requires a passed native check")
		}
	}
	if request.Validate() != nil || s.Project != request.Project || s.Run != request.Run || s.Incarnation != request.Incarnation || s.ManifestDigest != request.ManifestDigest || !ValidCommit(s.SourceCommit) || !ValidPreparationID(s.Preparation) || !ValidDigest(s.CheckDigest) || len(s.Reason) > 1024 {
		return errors.New("verification checks receipt differs from its admitted identity")
	}
	unsafeControl := strings.ContainsFunc(s.OutputExcerpt, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
	if len(s.OutputExcerpt) > MaxVerificationOutputExcerpt || !utf8.ValidString(s.OutputExcerpt) || unsafeControl || (s.OutputExcerpt != "" || s.OutputTruncated) && !ValidDigest(s.OutputDigest) {
		return errors.New("invalid verification checks output excerpt")
	}
	switch s.Phase {
	case "preparing", "running":
		if s.ExitCode != nil || s.OutputDigest != "" || s.OutputExcerpt != "" || s.OutputTruncated || s.Retirement != "" {
			return errors.New("pending verification checks carry terminal evidence")
		}
	case "passed":
		if s.ExitCode == nil || *s.ExitCode != 0 || !ValidDigest(s.OutputDigest) || s.Retirement != "confirmed" {
			return errors.New("passing verification checks lack native exit evidence")
		}
	case "failed", "stopped":
		if s.Retirement != "confirmed" && s.Retirement != "uncertain" {
			return errors.New("failed verification checks lack retirement evidence")
		}
	default:
		return errors.New("unknown verification checks phase")
	}
	if s.OutputDigest != "" && !ValidDigest(s.OutputDigest) {
		return errors.New("invalid verification checks output digest")
	}
	return nil
}
