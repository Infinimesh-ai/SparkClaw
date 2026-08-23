package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SaveArtifactObject(ctx context.Context, object app.ArtifactObject) (app.ArtifactObject, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataSave, ctx); err != nil {
		return app.ArtifactObject{}, err
	}
	object = prepareArtifactObject(object, time.Now().UTC())
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationArtifactMetadataSave, s.artifactMetadataPostgres)
	if err != nil {
		return app.ArtifactObject{}, err
	}
	defer releasePostgresSession(session, release)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO artifact_objects (
			id, kind, run_id, eval_id, session_id, backend, bucket, object_key, uri, path,
			content_type, bytes, created_at
		)
		VALUES ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), $6, nullif($7, ''), $8, $9, nullif($10, ''), $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			kind = EXCLUDED.kind,
			run_id = EXCLUDED.run_id,
			eval_id = EXCLUDED.eval_id,
			session_id = EXCLUDED.session_id,
			backend = EXCLUDED.backend,
			bucket = EXCLUDED.bucket,
			object_key = EXCLUDED.object_key,
			uri = EXCLUDED.uri,
			path = EXCLUDED.path,
			content_type = EXCLUDED.content_type,
			bytes = EXCLUDED.bytes,
			created_at = EXCLUDED.created_at
	`, object.ID, object.Kind, object.RunID, object.EvalID, object.SessionID, object.Backend, object.Bucket, object.Key, object.URI, object.Path, object.ContentType, object.Bytes, object.CreatedAt); err != nil {
		return app.ArtifactObject{}, finishArtifactMetadataPostgresStatement(ctx, session, transaction, release, err)
	}
	if err := appendArtifactMetadataLifecycle(transaction, ctx, object); err != nil {
		return app.ArtifactObject{}, finishArtifactMetadataPostgresStatement(ctx, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return object, storeError(ctx, OperationArtifactMetadataSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return object, nil
}

func appendArtifactMetadataLifecycle(transaction onboardingPostgresTx, ctx context.Context, object app.ArtifactObject) error {
	at := postgresTime(time.Now().UTC())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, 'artifact.saved', nullif($3, ''), nullif($4, ''), 'artifact-store', $5, $6)
	`, app.NewID("audit"), at, object.SessionID, object.RunID, object.URI, optionalJSON(map[string]any{
		"kind": object.Kind, "backend": object.Backend, "key": object.Key, "bytes": object.Bytes, "eval_id": object.EvalID,
	})); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, 'artifact.saved', nullif($3, ''), nullif($4, ''), $5)
	`, app.NewID("evt"), at, object.SessionID, object.RunID, mustJSON(object))
	return err
}

func (s *PostgresStore) ListArtifactObjects(ctx context.Context, limit int) ([]app.ArtifactObject, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataList, ctx); err != nil {
		return nil, err
	}
	query := `
		SELECT id, kind, coalesce(run_id, ''), coalesce(eval_id, ''), coalesce(session_id, ''),
			backend, coalesce(bucket, ''), object_key, uri, coalesce(path, ''), content_type, bytes, created_at
		FROM artifact_objects
		ORDER BY created_at DESC, id ASC
	`
	var rows onboardingPostgresRows
	var err error
	if limit > 0 {
		rows, err = s.artifactMetadataPostgres.Query(ctx, query+` LIMIT $1`, limit)
	} else {
		rows, err = s.artifactMetadataPostgres.Query(ctx, query)
	}
	if err != nil {
		return nil, classifyPostgresReadError(OperationArtifactMetadataList, ctx, err)
	}
	defer rows.Close()
	objects := []app.ArtifactObject{}
	for rows.Next() {
		object, err := scanArtifactObject(rows)
		if err != nil {
			return nil, classifyPostgresReadError(OperationArtifactMetadataList, ctx, err)
		}
		objects = append(objects, normalizeArtifactObject(object))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationArtifactMetadataList, ctx, err)
	}
	return objects, nil
}

func (s *PostgresStore) FindArtifactObjectByURI(ctx context.Context, uri, sessionID, runID string) (app.ArtifactObject, bool, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataFindByURI, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataFindByURI, ctx); err != nil {
		return app.ArtifactObject{}, false, err
	}
	row := s.artifactMetadataPostgres.QueryRow(ctx, `
		SELECT id, kind, coalesce(run_id, ''), coalesce(eval_id, ''), coalesce(session_id, ''),
			backend, coalesce(bucket, ''), object_key, uri, coalesce(path, ''), content_type, bytes, created_at
		FROM artifact_objects
		WHERE uri = $1
		  AND ($2 = '' OR session_id = $2)
		  AND ($3 = '' OR run_id = $3)
		ORDER BY created_at DESC, id ASC
		LIMIT 1
	`, uri, sessionID, runID)
	object, err := scanArtifactObject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ArtifactObject{}, false, nil
	}
	if err != nil {
		return app.ArtifactObject{}, false, classifyPostgresReadError(OperationArtifactMetadataFindByURI, ctx, err)
	}
	return normalizeArtifactObject(object), true, nil
}

func finishArtifactMetadataPostgresStatement(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyPostgresReadError(OperationArtifactMetadataSave, ctx, cause)
}
