package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/application"
	"github.com/levitateos/sodaos/internal/factory"
)

// Packaging owns bytes; setup and replacement own app identity and data. A
// verified build can therefore exist before any application claims its origin.
const ApplicationBuildsDDL = `CREATE TABLE application_builds(seq BIGINT GENERATED ALWAYS AS IDENTITY,id TEXT PRIMARY KEY CHECK(id ~ '^[a-f0-9]{32}$'),repository BIGINT NOT NULL CHECK(repository>0),delivery_id TEXT NOT NULL,environment TEXT NOT NULL CHECK(environment IN ('company','preview')),data JSONB NOT NULL CHECK(octet_length(data::text)<=16384),setup_id TEXT REFERENCES application_setups(id),UNIQUE(repository,delivery_id,environment))`

type ApplicationBuild struct {
	ID         string                     `json:"id"`
	Repository int64                      `json:"repository"`
	Delivery   ApplicationFactoryDelivery `json:"delivery"`
	Artifact   application.BuildArtifact  `json:"artifact"`
	SetupID    string                     `json:"setup_id,omitempty"`
}

func scanApplicationBuild(row interface{ Scan(...any) error }) (ApplicationBuild, error) {
	var build ApplicationBuild
	var raw []byte
	var setup sql.NullString
	if err := row.Scan(&raw, &setup); err != nil {
		return build, err
	}
	if err := json.Unmarshal(raw, &build); err != nil {
		return build, err
	}
	build.SetupID = setup.String
	return build, nil
}

func (s *Store) ApplicationBuild(ctx context.Context, repository int64, id string) (ApplicationBuild, error) {
	return scanApplicationBuild(s.db.QueryRowContext(ctx, `SELECT data,setup_id FROM application_builds WHERE repository=$1 AND id=$2`, repository, id))
}

func (s *Store) ApplicationBuilds(ctx context.Context, repository int64, delivery string) ([]ApplicationBuild, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data,setup_id FROM application_builds WHERE repository=$1 AND ($2='' OR delivery_id=$2) ORDER BY seq DESC LIMIT 32`, repository, delivery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	builds := []ApplicationBuild{}
	for rows.Next() {
		build, err := scanApplicationBuild(rows)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}
	return builds, rows.Err()
}

func (s *Store) SaveApplicationBuild(ctx context.Context, repository int64, deliveryID string, artifact application.BuildArtifact) (ApplicationBuild, error) {
	request, delivery, err := s.VerifiedFactoryBuildInput(ctx, repository, deliveryID, artifact.Environment)
	if err != nil {
		return ApplicationBuild{}, err
	}
	if artifact.ValidateFor(request) != nil || artifact.SourceCommit != delivery.SourceCommit || artifact.BundleFileSHA256 != delivery.BundleFileSHA256 {
		return ApplicationBuild{}, errors.New("packaged artifact differs from verified delivery")
	}
	build := ApplicationBuild{ID: factory.MCPInputDigest([]byte("application-build-v1\n" + deliveryID + "\n" + artifact.Environment + "\n" + artifact.Image))[:32], Repository: repository, Delivery: delivery, Artifact: artifact}
	raw, err := json.Marshal(build)
	if err != nil {
		return build, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return build, err
	}
	defer tx.Rollback()
	if err = lockBuildIntent(ctx, tx, delivery); err != nil {
		return build, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO application_builds(id,repository,delivery_id,environment,data) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository,delivery_id,environment) DO NOTHING`, build.ID, repository, deliveryID, artifact.Environment, string(raw)); err != nil {
		return build, err
	}
	stored, err := scanApplicationBuild(tx.QueryRowContext(ctx, `SELECT data,setup_id FROM application_builds WHERE repository=$1 AND delivery_id=$2 AND environment=$3`, repository, deliveryID, artifact.Environment))
	if err != nil {
		return build, err
	}
	return stored, tx.Commit()
}

func lockBuildIntent(ctx context.Context, tx *sql.Tx, delivery ApplicationFactoryDelivery) error {
	var revision string
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM factory_intent_heads WHERE id=$1 FOR SHARE`, delivery.Intent).Scan(&revision); err != nil {
		return err
	}
	if revision != delivery.Revision {
		return ErrStaleRevision
	}
	return nil
}

// One durable setup per build. A lost response returns the same reservation,
// preserving its data, expiry and owner; it cannot create a second identity.
func (s *Store) CreateApplicationBuildSetup(ctx context.Context, repository int64, id string, administrator int64) (application.SetupRecord, error) {
	var empty application.SetupRecord
	build, err := s.ApplicationBuild(ctx, repository, id)
	if err != nil {
		return empty, err
	}
	// Recovery returns an already reserved operation even when newer product
	// work exists. Only creating a new reservation requires the current head.
	if build.SetupID != "" {
		return s.ApplicationSetup(ctx, build.SetupID)
	}
	_, delivery, err := s.VerifiedFactoryBuildInput(ctx, repository, build.Delivery.DeliveryID, build.Artifact.Environment)
	if err != nil {
		return empty, err
	}
	if delivery != build.Delivery {
		return empty, ErrStaleRevision
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	build, err = scanApplicationBuild(tx.QueryRowContext(ctx, `SELECT data,setup_id FROM application_builds WHERE repository=$1 AND id=$2 FOR UPDATE`, repository, id))
	if err != nil {
		return empty, err
	}
	if err = lockBuildIntent(ctx, tx, build.Delivery); err != nil {
		return empty, err
	}
	if build.SetupID != "" {
		record, err := scanApplicationSetup(tx.QueryRowContext(ctx, `SELECT data FROM application_setups WHERE id=$1`, build.SetupID))
		if err != nil {
			return empty, err
		}
		return record, tx.Commit()
	}
	if err = lockApplicationAdmissions(ctx, tx); err != nil {
		return empty, err
	}
	var port int
	err = tx.QueryRowContext(ctx, `SELECT p FROM generate_series(20000,29999) p WHERE NOT EXISTS(SELECT 1 FROM applications WHERE port=p) AND NOT EXISTS(SELECT 1 FROM application_setups WHERE port=p) ORDER BY p LIMIT 1`).Scan(&port)
	if err != nil {
		return empty, err
	}
	identities := make([]string, 3)
	for i := range identities {
		identities[i], err = application.NewIdentity()
		if err != nil {
			return empty, err
		}
	}
	record := application.SetupRecord{ID: identities[0], Application: build.Artifact.Setup(identities[1], identities[2], port), AdministratorID: administrator, ExpiresAt: time.Now().Add(30 * time.Minute).Unix(), State: "pending"}
	if record.Validate() != nil {
		return empty, errors.New("invalid build setup")
	}
	if err = reserveSetupNamespace(ctx, tx, record.Application); err != nil {
		return empty, err
	}
	if err = insertApplicationSetupTx(ctx, tx, record); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE application_builds SET setup_id=$1 WHERE id=$2`, record.ID, id); err != nil {
		return empty, err
	}
	return record, tx.Commit()
}

func (s *Store) ApplicationBuildImageProof(ctx context.Context, image string) string {
	var digest string
	if s.db.QueryRowContext(ctx, `SELECT data->'artifact'->>'bundle_file_sha256' FROM application_builds WHERE data->'artifact'->>'image'=$1 ORDER BY seq DESC LIMIT 1`, image).Scan(&digest) != nil {
		return ""
	}
	return digest
}
