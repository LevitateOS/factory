package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

func initializeArtifactSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_feed_artifacts(id TEXT PRIMARY KEY,repository BIGINT NOT NULL,assignment TEXT NOT NULL,name TEXT NOT NULL,media_type TEXT NOT NULL,content BYTEA NOT NULL,complete BOOLEAN NOT NULL DEFAULT FALSE,digest TEXT,CHECK(octet_length(content)<=8388608),CHECK(digest IS NULL OR digest ~ '^[a-f0-9]{64}$'))`)
	return err
}

func artifactID(cap factory.MCPCapability, upload string) string {
	return factory.MCPInputDigest([]byte("artifact\n" + cap.AssignmentID + "\n" + upload))[:32]
}

// UploadWorkerArtifact keeps upload progress in the artifact row. Finalization
// publishes its post atomically; chunks never enter the submission journal.
func (s *Store) UploadWorkerArtifact(ctx context.Context, bound factory.MCPCapability, payload []byte) (factory.FeedArtifact, string, error) {
	var artifact factory.FeedArtifact
	u, chunk, err := factory.DecodeArtifactUpload(payload)
	if err != nil {
		return artifact, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return artifact, "", err
	}
	defer tx.Rollback()
	cap, err := lockMCPCapabilityTx(ctx, tx, bound.ID, bound.AssignmentID)
	if err != nil {
		return artifact, "", err
	}
	if !factory.MCPToolAdmitted(cap.Profile, factory.MCPToolPublishArtifact) {
		return artifact, "", errors.New("artifact upload is not admitted for this worker")
	}
	if cap.Expired(time.Now()) {
		return artifact, "", ErrMCPSubmissionCapabilityExpired
	}
	if finished, err := assignmentFinishedTx(ctx, tx, cap.AssignmentID); err != nil {
		return artifact, "", err
	} else if finished {
		return artifact, "", ErrMCPSubmissionAssignmentFinished
	}
	id := artifactID(cap, u.UploadID)
	if u.Offset == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_feed_artifacts WHERE assignment=$1`, cap.AssignmentID).Scan(&count); err != nil {
			return artifact, "", err
		}
		if count >= 32 {
			return artifact, "", errors.New("at most 32 artifacts per assignment")
		}
		var result sql.Result
		result, err = tx.ExecContext(ctx, `INSERT INTO factory_feed_artifacts(id,repository,assignment,name,media_type,content,complete) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, id, cap.Repository, cap.AssignmentID, u.Name, u.MediaType, chunk, u.Final)
		if err == nil {
			if n, _ := result.RowsAffected(); n != 1 {
				return artifact, "", errors.New("artifact upload already exists; use the next upload offset")
			}
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE factory_feed_artifacts SET content=content || $1,complete=$2 WHERE id=$3 AND repository=$4 AND assignment=$5 AND name=$6 AND media_type=$7 AND octet_length(content)=$8 AND NOT complete`, chunk, u.Final, id, cap.Repository, cap.AssignmentID, u.Name, u.MediaType, u.Offset)
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return artifact, "", errors.New("artifact upload offset or metadata changed")
			}
		}
	}
	if err != nil {
		return artifact, "", err
	}
	artifact = factory.FeedArtifact{ID: id, Name: u.Name, MediaType: u.MediaType, Size: u.Offset + len(chunk), Href: factory.ArtifactHref(cap.Repository, id)}
	var postID string
	if u.Final {
		// The digest attests the complete bytes the worker uploaded;
		// finish citations reference this value so settlement can
		// resolve them back to this assignment's artifact.
		var content []byte
		if err := tx.QueryRowContext(ctx, `SELECT content FROM factory_feed_artifacts WHERE id=$1 AND repository=$2 AND complete`, id, cap.Repository).Scan(&content); err != nil {
			return artifact, "", err
		}
		digest := sha256.Sum256(content)
		artifact.Digest = hex.EncodeToString(digest[:])
		if _, err := tx.ExecContext(ctx, `UPDATE factory_feed_artifacts SET digest=$1 WHERE id=$2 AND repository=$3`, artifact.Digest, id, cap.Repository); err != nil {
			return artifact, "", err
		}
		feedContent := factory.FeedCommunication{Kind: "artifact", Title: artifact.Name, Summary: fmt.Sprintf("Artifact available · %d bytes", artifact.Size), Artifacts: []factory.FeedArtifact{artifact}}
		post := factory.MCPSubmission{AssignmentID: cap.AssignmentID, SubmissionID: u.UploadID, Tool: factory.MCPToolPublishArtifact}
		postID, err = recordWorkerPostTx(ctx, tx, cap, post, feedContent)
		if err != nil {
			return artifact, "", err
		}
	}
	return artifact, postID, tx.Commit()
}

func resolveCommunicationArtifactsTx(ctx context.Context, tx *sql.Tx, repo int64, content *factory.FeedCommunication) error {
	for i, ref := range content.Artifacts {
		a := factory.FeedArtifact{ID: ref.ID, Href: factory.ArtifactHref(repo, ref.ID)}
		if err := tx.QueryRowContext(ctx, `SELECT name,media_type,octet_length(content),COALESCE(digest,'') FROM factory_feed_artifacts WHERE id=$1 AND repository=$2 AND complete`, ref.ID, repo).Scan(&a.Name, &a.MediaType, &a.Size, &a.Digest); err != nil {
			return errors.New("artifact is unavailable in this project")
		}
		content.Artifacts[i] = a
	}
	return nil
}

// ResolveArtifactDigest maps one cited content digest to a complete
// artifact of the assignment. A digest with no complete row here is
// unknown, foreign or unfinished, and the caller must refuse it.
func (s *Store) ResolveArtifactDigest(ctx context.Context, repository int64, assignment, digest string) (string, error) {
	if repository < 1 || !factory.ValidID(assignment) || !factory.ValidDigest(digest) {
		return "", errors.New("artifact citation is invalid")
	}
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM factory_feed_artifacts WHERE repository=$1 AND assignment=$2 AND digest=$3 AND complete`, repository, assignment, digest).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return id, nil
}

func (s *Store) FeedArtifact(ctx context.Context, repo int64, id string) (factory.FeedArtifact, []byte, error) {
	a := factory.FeedArtifact{ID: id, Href: factory.ArtifactHref(repo, id)}
	var content []byte
	err := s.db.QueryRowContext(ctx, `SELECT name,media_type,content,COALESCE(digest,'') FROM factory_feed_artifacts WHERE id=$1 AND repository=$2 AND complete`, id, repo).Scan(&a.Name, &a.MediaType, &content, &a.Digest)
	a.Size = len(content)
	return a, content, err
}
