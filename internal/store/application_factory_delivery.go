package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/application"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// ApplicationFactoryDelivery is a spend-free projection of two native byte
// observations and the canonical F10 event. It grants no release or app access.
type ApplicationFactoryDelivery struct {
	State            string `json:"state"`
	Environment      string `json:"environment"`
	Intent           string `json:"intent"`
	Revision         string `json:"revision"`
	DeliveryID       string `json:"delivery_id"`
	Run              string `json:"run"`
	SourceCommit     string `json:"source_commit"`
	BundleFileSHA256 string `json:"bundle_file_sha256"`
}

type factoryApplicationBuildInput struct {
	Request          application.BuildRequest
	SourceCommit     string
	BundleFileSHA256 string
	Intent           string
	Revision         string
}

// VerifiedFactoryBuildInput admits packaging only from the canonical current
// F10 and its retired native check output, never a caller's claimed digest.
func (s *Store) VerifiedFactoryBuildInput(ctx context.Context, repository int64, deliveryID, environment string) (application.BuildRequest, ApplicationFactoryDelivery, error) {
	in, err := s.verifiedFactoryBuildInput(ctx, repository, deliveryID, environment)
	if err != nil {
		return application.BuildRequest{}, ApplicationFactoryDelivery{}, err
	}
	return in.Request, ApplicationFactoryDelivery{State: "verified", Environment: environment, Intent: in.Intent, Revision: in.Revision, DeliveryID: deliveryID, Run: in.Request.Run, SourceCommit: in.SourceCommit, BundleFileSHA256: in.BundleFileSHA256}, nil
}

func (s *Store) verifiedFactoryBuildInput(ctx context.Context, repository int64, deliveryID, environment string) (factoryApplicationBuildInput, error) {
	var out factoryApplicationBuildInput
	if repository <= 0 || !factory.ValidID(deliveryID) || !application.ValidBuildEnvironment(environment) {
		return out, errors.New("invalid factory application delivery")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var assignmentRaw, runRaw, revisionRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT a.data,r.data,i.data FROM factory_assignments a JOIN factory_runs r ON r.id=a.run
		JOIN factory_intent_heads h ON h.id=a.data->'subject'->'delivery'->>'intent_id' AND h.revision=a.data->'subject'->'delivery'->>'accepted_revision'
		JOIN factory_intent_revisions i ON i.id=h.revision
		WHERE a.repository=$1 AND a.data->'subject'->'delivery'->>'delivery_id'=$2 AND a.stage='finished'
		AND a.data->>'phase'='verify' AND a.data->>'outcome'='succeeded'
		AND EXISTS(SELECT 1 FROM factory_feed_posts p WHERE p.repository=a.repository AND p.data->>'assignment'=a.id AND p.data->>'event_identity'=$2 AND p.data->'communication'->>'component_id'='F10')
		ORDER BY a.seq DESC LIMIT 1 FOR SHARE OF a,r,h`, repository, deliveryID).Scan(&assignmentRaw, &runRaw, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	var a factory.Assignment
	var run factory.Run
	var revision factory.IntentRevision
	for _, item := range []struct {
		raw   []byte
		value any
	}{{assignmentRaw, &a}, {runRaw, &run}, {revisionRaw, &revision}} {
		if err := json.Unmarshal(item.raw, item.value); err != nil {
			return out, err
		}
	}
	if a.Validate() != nil || run.Validate() != nil || revision.Validate() != nil || a.Subject.Delivery == nil || a.Run != run.ID || a.Repository != repository || revision.Repository != repository || revision.State != "accepted" {
		return out, errors.New("application build delivery is invalid")
	}
	delivery, checks := a.Subject.Delivery, run.VerificationChecks
	if delivery.IntentID != revision.Intent || delivery.AcceptedRevision != revision.ID || delivery.ScopeDigest != revision.Digest() || delivery.SourceCommit != run.InputSHA || checks == nil {
		return out, errors.New("verified delivery has no native application bundle")
	}
	digest := checks.ApplicationBundleSHA256
	if environment == "preview" {
		digest = checks.ApplicationPreviewBundleSHA256
	}
	if !factory.ValidDigest(digest) {
		return out, errors.New("verified delivery has no requested environment bundle")
	}
	output, err := checkVerifiedManifestExecutionTx(ctx, tx, a)
	if err != nil {
		return out, err
	}
	if output == "" {
		return out, errors.New("verified delivery has no bound native check")
	}
	out = factoryApplicationBuildInput{Request: application.BuildRequest{FactoryVerificationChecks: project.FactoryVerificationChecks{Project: checks.Project, Run: checks.Run, Incarnation: checks.Incarnation, ManifestDigest: checks.ManifestDigest}, Environment: environment}, SourceCommit: run.InputSHA, BundleFileSHA256: digest, Intent: revision.Intent, Revision: revision.ID}
	return out, tx.Commit()
}

// VerifiedApplicationDelivery never accepts an asserted source, event or hash.
// The application image readback and approved-check output must already have
// been observed by their native owners. Its immutable accepted revision remains
// evidence for a running older image after the intent head advances; it grants
// no fresh packaging or release authority. Missing receipts remain unknown.
func (s *Store) VerifiedApplicationDelivery(ctx context.Context, id string) (*ApplicationFactoryDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	app, err := scanApplication(tx.QueryRowContext(ctx, `SELECT r.spec,a.administrator_id,a.observation FROM applications a JOIN application_revisions r ON r.application_id=a.id AND r.revision=a.revision WHERE a.id=$1 FOR SHARE OF a`, id))
	if err != nil {
		return nil, err
	}
	if app.Observation == nil || app.Observation.BundleFileSHA256 == "" {
		return nil, nil
	}
	var repository int64
	if err := tx.QueryRowContext(ctx, `SELECT repository FROM application_projects WHERE application_id=$1 FOR SHARE`, id).Scan(&repository); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var assignmentRaw, runRaw, revisionRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT a.data,r.data,i.data FROM factory_assignments a
		JOIN factory_runs r ON r.id=a.run
		JOIN factory_intent_revisions i ON i.id=a.data->'subject'->'delivery'->>'accepted_revision'
		WHERE a.repository=$1 AND a.stage='finished' AND a.data->>'phase'='verify' AND a.data->>'outcome'='succeeded'
		AND (r.data->'verification_checks'->>'application_bundle_sha256'=$2 OR r.data->'verification_checks'->>'application_preview_bundle_sha256'=$2)
		AND EXISTS(SELECT 1 FROM factory_feed_posts p WHERE p.repository=a.repository AND p.data->>'assignment'=a.id
		AND p.data->>'event_identity'=a.data->'subject'->'delivery'->>'delivery_id' AND p.data->'communication'->>'component_id'='F10')
		ORDER BY a.seq DESC LIMIT 1 FOR SHARE OF a,r,i`, repository, app.Observation.BundleFileSHA256).Scan(&assignmentRaw, &runRaw, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a factory.Assignment
	var run factory.Run
	var revision factory.IntentRevision
	for _, item := range []struct {
		raw   []byte
		value any
	}{{assignmentRaw, &a}, {runRaw, &run}, {revisionRaw, &revision}} {
		if err := json.Unmarshal(item.raw, item.value); err != nil {
			return nil, err
		}
	}
	if a.Validate() != nil || run.Validate() != nil || revision.Validate() != nil || a.Subject.Delivery == nil || a.Run != run.ID || a.Repository != repository || revision.Repository != repository || revision.State != "accepted" {
		return nil, errors.New("application delivery binding is invalid")
	}
	delivery := a.Subject.Delivery
	checks := run.VerificationChecks
	if delivery.IntentID != revision.Intent || delivery.AcceptedRevision != revision.ID || delivery.ScopeDigest != revision.Digest() || delivery.SourceCommit != run.InputSHA || checks == nil {
		return nil, errors.New("application delivery differs from its native build receipt")
	}
	output, err := checkVerifiedManifestExecutionTx(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, nil
	}
	environment := "company"
	if checks.ApplicationPreviewBundleSHA256 == app.Observation.BundleFileSHA256 {
		if checks.ApplicationBundleSHA256 == app.Observation.BundleFileSHA256 {
			return nil, errors.New("preview and company bundle identities overlap")
		}
		environment = "preview"
	} else if checks.ApplicationBundleSHA256 != app.Observation.BundleFileSHA256 {
		return nil, errors.New("application bytes have no native environment")
	}
	result := &ApplicationFactoryDelivery{State: "verified", Environment: environment, Intent: revision.Intent, Revision: revision.ID, DeliveryID: delivery.DeliveryID, Run: run.ID, SourceCommit: run.InputSHA, BundleFileSHA256: app.Observation.BundleFileSHA256}
	return result, tx.Commit()
}
