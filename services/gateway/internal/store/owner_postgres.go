package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const ownerProfileSelectSQL = `SELECT id, source, external_ref, workspace_root, default_channel, default_binding_id,
	display_name, email, preferences, created_at, updated_at FROM owners`

func (s *PostgresStore) GetOwnerProfile(ctx context.Context) (app.OwnerProfile, error) {
	profile, found, err := s.getOwnerProfileByID(ctx, OperationOwnerProfileGet, app.DefaultOwnerID)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	if !found {
		return app.OwnerProfile{}, storeError(ctx, OperationOwnerProfileGet, StoreErrorCorrupt, errors.New("default owner profile is missing"))
	}
	return profile, nil
}

func (s *PostgresStore) UpdateOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	profile.ID = app.DefaultOwnerID
	return s.saveOwnerProfile(ctx, OperationOwnerProfileUpdate, profile)
}

func (s *PostgresStore) GetOwnerProfileByID(ctx context.Context, id string) (app.OwnerProfile, bool, error) {
	return s.getOwnerProfileByID(ctx, OperationOwnerProfileGetByID, id)
}

func (s *PostgresStore) getOwnerProfileByID(ctx context.Context, operation StoreOperation, id string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	id = normalizeOwnerProfileID(id)
	session, err := s.ownerPostgres.Acquire(ctx)
	if err != nil {
		return app.OwnerProfile{}, false, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.OwnerProfile{}, false, classifyPostgresPreTransaction(operation, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerAdvisoryKey(id)); err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return app.OwnerProfile{}, false, classifyPostgresReadError(operation, ctx, err)
	}
	profile, err := scanOwnerProfile(transaction.QueryRow(ctx, ownerProfileSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := transaction.Commit(ctx); err != nil {
			release = false
			return app.OwnerProfile{}, false, classifyPostgresReadError(operation, ctx, errors.Join(err, session.Terminate(ctx)))
		}
		return app.OwnerProfile{}, false, nil
	}
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return app.OwnerProfile{}, false, classifyPostgresOwnerReadError(operation, ctx, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return app.OwnerProfile{}, false, classifyPostgresReadError(operation, ctx, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneOwnerProfile(profile), true, nil
}

func (s *PostgresStore) SaveOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	return s.saveOwnerProfile(ctx, OperationOwnerProfileSave, profile)
}

func (s *PostgresStore) saveOwnerProfile(ctx context.Context, operation StoreOperation, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	profile.ID = normalizeOwnerProfileID(profile.ID)
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	session, err := s.ownerPostgres.Acquire(ctx)
	if err != nil {
		return app.OwnerProfile{}, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err != nil {
		return app.OwnerProfile{}, classifyPostgresPreTransaction(operation, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerAdvisoryKey(profile.ID)); err != nil {
		return app.OwnerProfile{}, finishPostgresOwnerPreCandidate(ctx, operation, session, transaction, &release, err)
	}
	current, err := scanOwnerProfile(transaction.QueryRow(ctx, ownerProfileSelectSQL+` WHERE id=$1`, profile.ID))
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return app.OwnerProfile{}, finishPostgresOwnerPreCandidate(ctx, operation, session, transaction, &release, err)
	}
	candidate := prepareOwnerProfile(profile, current, exists, s.ownerNow(), s.ownerWriteHighWater[profile.ID])
	s.ownerWriteHighWater[candidate.ID] = candidate.UpdatedAt
	if _, err := transaction.Exec(ctx, `
		INSERT INTO owners (id,source,external_ref,workspace_root,default_channel,default_binding_id,
			display_name,email,preferences,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO UPDATE SET source=EXCLUDED.source, external_ref=EXCLUDED.external_ref,
			workspace_root=EXCLUDED.workspace_root, default_channel=EXCLUDED.default_channel,
			default_binding_id=EXCLUDED.default_binding_id, display_name=EXCLUDED.display_name,
			email=EXCLUDED.email, preferences=EXCLUDED.preferences, updated_at=EXCLUDED.updated_at
	`, candidate.ID, candidate.Source, candidate.ExternalRef, candidate.WorkspaceRoot,
		candidate.DefaultChannel, candidate.DefaultBindingID, candidate.DisplayName, candidate.Email,
		mustJSON(candidate.Preferences), candidate.CreatedAt, candidate.UpdatedAt); err != nil {
		return finishPostgresOwnerStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields)
		VALUES ($1,$2,'owner_profile.updated',NULL,NULL,'owner',$3,$4)
	`, app.NewID("audit"), candidate.UpdatedAt, candidate.DisplayName, optionalJSON(ownerProfileAuditFields(candidate))); err != nil {
		return finishPostgresOwnerStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO events (id,happened_at,type,session_id,run_id,payload)
		VALUES ($1,$2,'owner_profile.updated',NULL,NULL,$3)
	`, app.NewID("evt"), candidate.UpdatedAt, mustJSON(candidate)); err != nil {
		return finishPostgresOwnerStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneOwnerProfile(candidate), nil
}

func finishPostgresOwnerPreCandidate(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	var postgresError *pgconn.PgError
	definite := errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) || errors.Is(cause, errOwnerPreferencesDecode)
	if !definite {
		*release = false
		return storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
	}
	cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
	if errors.Is(cause, errOwnerPreferencesDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	if postgresError != nil {
		return storeError(ctx, operation, StoreErrorInternal, cause)
	}
	return classifyPostgresPreTransaction(operation, ctx, cause)
}

func finishPostgresOwnerStatement(ctx context.Context, operation StoreOperation, candidate app.OwnerProfile, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (app.OwnerProfile, error) {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		rollbackErr := transaction.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			*release = false
			rollbackErr = errors.Join(rollbackErr, session.Terminate(ctx))
		}
		joined := errors.Join(cause, rollbackErr)
		if postgresError != nil {
			return app.OwnerProfile{}, storeError(ctx, operation, StoreErrorInternal, joined)
		}
		return app.OwnerProfile{}, classifyPostgresPreTransaction(operation, ctx, joined)
	}
	*release = false
	return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func (s *PostgresStore) ListOwnerProfiles(ctx context.Context) ([]app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileList, s.operationTimeouts)
	defer cancel()
	rows, err := s.ownerPostgres.Query(ctx, ownerProfileSelectSQL+` ORDER BY updated_at DESC, id ASC`)
	if err != nil {
		return nil, classifyPostgresReadError(OperationOwnerProfileList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.OwnerProfile, 0)
	for rows.Next() {
		profile, err := scanOwnerProfile(rows)
		if err != nil {
			return nil, classifyPostgresOwnerReadError(OperationOwnerProfileList, ctx, err)
		}
		out = append(out, cloneOwnerProfile(profile))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationOwnerProfileList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) FindOwnerProfileByExternalRef(ctx context.Context, source, externalRef string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileFindExternalRef, s.operationTimeouts)
	defer cancel()
	source = strings.TrimSpace(source)
	externalRef = strings.TrimSpace(externalRef)
	if source == "" || externalRef == "" {
		return app.OwnerProfile{}, false, nil
	}
	profile, err := scanOwnerProfile(s.ownerPostgres.QueryRow(ctx, ownerProfileSelectSQL+`
		WHERE source=$1 AND external_ref=$2 ORDER BY updated_at DESC, id ASC LIMIT 1`, source, externalRef))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.OwnerProfile{}, false, nil
	}
	if err != nil {
		return app.OwnerProfile{}, false, classifyPostgresOwnerReadError(OperationOwnerProfileFindExternalRef, ctx, err)
	}
	return cloneOwnerProfile(profile), true, nil
}

func ownerAdvisoryKey(id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/owner/v1\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}
