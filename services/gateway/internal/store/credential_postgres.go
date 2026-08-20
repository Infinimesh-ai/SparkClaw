package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const credentialSecretSelectSQL = `SELECT ref, kind, value, created_at, updated_at FROM credential_secrets`

func (s *PostgresStore) validateCredentialState(ctx context.Context) error {
	startupCtx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	rows, err := s.credentialPostgres.Query(startupCtx, credentialSecretSelectSQL)
	if err != nil {
		return fmt.Errorf("validate credentials: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		secret, err := scanCredentialSecret(rows)
		if err != nil {
			return fmt.Errorf("validate credentials: %w", err)
		}
		secret, err = normalizePersistedCredentialSecret(secret)
		if err != nil {
			return fmt.Errorf("validate credential state: %w", err)
		}
		s.credentialWriteHighWater[secret.Ref] = latestCredentialTime(secret)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate credentials: %w", err)
	}
	return nil
}

func (s *PostgresStore) SaveCredentialSecret(ctx context.Context, command CredentialSaveCommand) (app.CredentialSecret, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretSave, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	command, err := normalizeCredentialSaveCommand(command)
	if err != nil {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretSave, StoreErrorInvalid, err)
	}
	releaseCommand, err := s.acquireCredentialCommand(ctx, OperationCredentialSecretSave)
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginCredentialTransaction(ctx, OperationCredentialSecretSave, pgx.TxOptions{})
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, credentialAdvisoryKey(command.secret.Ref)); err != nil {
		return app.CredentialSecret{}, finishCredentialPreCandidate(ctx, OperationCredentialSecretSave, session, transaction, release, err)
	}
	current, err := scanCredentialSecret(transaction.QueryRow(ctx, credentialSecretSelectSQL+` WHERE ref=$1`, command.secret.Ref))
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return app.CredentialSecret{}, finishCredentialPreCandidate(ctx, OperationCredentialSecretSave, session, transaction, release, err)
	}
	if exists {
		current, err = normalizePersistedCredentialSecret(current)
		if err != nil {
			return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretSave, StoreErrorCorrupt, session, transaction, release, err)
		}
	}
	if command.mode == credentialSaveCreate {
		if exists {
			return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretSave, StoreErrorConflict, session, transaction, release, errors.New("credential already exists"))
		}
	} else if !exists || credentialSecretDigest(current) != command.expected {
		return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretSave, StoreErrorConflict, session, transaction, release, errors.New("credential changed"))
	}
	commandAt := nextRepositoryTime(s.credentialNow(), s.credentialWriteHighWater[command.secret.Ref], latestCredentialTime(current))
	candidate := command.secret
	if exists {
		candidate.CreatedAt = current.CreatedAt
		candidate.UpdatedAt = commandAt
	} else {
		candidate.CreatedAt = commandAt
		candidate.UpdatedAt = commandAt
	}
	s.credentialWriteHighWater[candidate.Ref] = commandAt
	mutationSQL := `INSERT INTO credential_secrets (ref,kind,value,created_at,updated_at) VALUES ($1,$2,$3,$4,$5)`
	if exists {
		mutationSQL = `UPDATE credential_secrets SET kind=$2,value=$3,created_at=$4,updated_at=$5 WHERE ref=$1`
	}
	statements := []struct {
		sql        string
		args       []any
		requireOne bool
	}{
		{mutationSQL, []any{candidate.Ref, candidate.Kind, candidate.Value, candidate.CreatedAt, candidate.UpdatedAt}, true},
		{`INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields)
		 VALUES ($1,$2,'credential_secret.saved',NULL,NULL,'gateway',$3,$4)`, []any{app.NewID("audit"), commandAt, candidate.Kind, optionalJSON(map[string]any{"ref": candidate.Ref, "kind": candidate.Kind})}, true},
	}
	for _, statement := range statements {
		tag, err := transaction.Exec(ctx, statement.sql, statement.args...)
		if err != nil {
			unknown, resultErr := finishCredentialStatement(ctx, OperationCredentialSecretSave, session, transaction, release, err)
			if unknown {
				return candidate, resultErr
			}
			return app.CredentialSecret{}, resultErr
		}
		if statement.requireOne && tag.RowsAffected() != 1 {
			return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretSave, StoreErrorConflict, session, transaction, release, errors.New("credential changed"))
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(OperationCredentialSecretSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func (s *PostgresStore) GetCredentialSecret(ctx context.Context, ref string) (app.CredentialSecret, bool, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretGet, ctx); err != nil {
		return app.CredentialSecret{}, false, err
	}
	ref = normalizeCredentialRef(ref)
	if ref == "" {
		return app.CredentialSecret{}, false, nil
	}
	session, transaction, release, err := s.beginCredentialTransaction(ctx, OperationCredentialSecretGet, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.CredentialSecret{}, false, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, credentialAdvisoryKey(ref)); err != nil {
		return app.CredentialSecret{}, false, finishCredentialRead(ctx, OperationCredentialSecretGet, session, transaction, release, err)
	}
	secret, err := scanCredentialSecret(transaction.QueryRow(ctx, credentialSecretSelectSQL+` WHERE ref=$1`, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := commitCredentialRead(ctx, OperationCredentialSecretGet, session, transaction, release); err != nil {
			return app.CredentialSecret{}, false, err
		}
		return app.CredentialSecret{}, false, nil
	}
	if err != nil {
		return app.CredentialSecret{}, false, finishCredentialRead(ctx, OperationCredentialSecretGet, session, transaction, release, err)
	}
	secret, err = normalizePersistedCredentialSecret(secret)
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, release, err)
		return app.CredentialSecret{}, false, storeError(OperationCredentialSecretGet, StoreErrorCorrupt, err)
	}
	if err := commitCredentialRead(ctx, OperationCredentialSecretGet, session, transaction, release); err != nil {
		return app.CredentialSecret{}, false, err
	}
	return secret, true, nil
}

func (s *PostgresStore) DeleteCredentialSecret(ctx context.Context, condition CredentialDeleteCondition) (app.CredentialSecret, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretDelete, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	condition, err := normalizeCredentialDeleteCondition(condition)
	if err != nil {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretDelete, StoreErrorInvalid, err)
	}
	releaseCommand, err := s.acquireCredentialCommand(ctx, OperationCredentialSecretDelete)
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginCredentialTransaction(ctx, OperationCredentialSecretDelete, pgx.TxOptions{})
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, credentialAdvisoryKey(condition.ref)); err != nil {
		return app.CredentialSecret{}, finishCredentialPreCandidate(ctx, OperationCredentialSecretDelete, session, transaction, release, err)
	}
	secret, err := scanCredentialSecret(transaction.QueryRow(ctx, credentialSecretSelectSQL+` WHERE ref=$1`, condition.ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretDelete, StoreErrorNotFound, session, transaction, release, errors.New("credential not found"))
	}
	if err != nil {
		return app.CredentialSecret{}, finishCredentialPreCandidate(ctx, OperationCredentialSecretDelete, session, transaction, release, err)
	}
	secret, err = normalizePersistedCredentialSecret(secret)
	if err != nil {
		return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretDelete, StoreErrorCorrupt, session, transaction, release, err)
	}
	if credentialSecretDigest(secret) != condition.expected {
		return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretDelete, StoreErrorConflict, session, transaction, release, errors.New("credential changed"))
	}
	commandAt := nextRepositoryTime(s.credentialNow(), s.credentialWriteHighWater[secret.Ref], latestCredentialTime(secret))
	s.credentialWriteHighWater[secret.Ref] = commandAt
	statements := []struct {
		sql        string
		args       []any
		requireOne bool
	}{
		{`DELETE FROM credential_secrets WHERE ref=$1`, []any{secret.Ref}, true},
		{`INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields)
		 VALUES ($1,$2,'credential_secret.deleted',NULL,NULL,'gateway','credential deleted',$3)`, []any{app.NewID("audit"), commandAt, optionalJSON(map[string]any{"ref": secret.Ref})}, true},
	}
	for _, statement := range statements {
		tag, err := transaction.Exec(ctx, statement.sql, statement.args...)
		if err != nil {
			unknown, resultErr := finishCredentialStatement(ctx, OperationCredentialSecretDelete, session, transaction, release, err)
			if unknown {
				return secret, resultErr
			}
			return app.CredentialSecret{}, resultErr
		}
		if statement.requireOne && tag.RowsAffected() != 1 {
			return app.CredentialSecret{}, credentialBusinessError(ctx, OperationCredentialSecretDelete, StoreErrorConflict, session, transaction, release, errors.New("credential changed"))
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return secret, storeError(OperationCredentialSecretDelete, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return secret, nil
}

func normalizeCredentialRef(ref string) string {
	return strings.TrimSpace(ref)
}

func (s *PostgresStore) acquireCredentialCommand(ctx context.Context, operation StoreOperation) (func(), error) {
	if err := s.credentialCommandGate.Acquire(ctx, 1); err != nil {
		if contextErr := operationContextError(operation, ctx); contextErr != nil {
			return nil, contextErr
		}
		return nil, storeError(operation, StoreErrorUnavailable, err)
	}
	if err := operationContextError(operation, ctx); err != nil {
		s.credentialCommandGate.Release(1)
		return nil, err
	}
	return func() { s.credentialCommandGate.Release(1) }, nil
}

func (s *PostgresStore) beginCredentialTransaction(ctx context.Context, operation StoreOperation, options pgx.TxOptions) (onboardingPostgresSession, onboardingPostgresTx, *bool, error) {
	session, err := s.credentialPostgres.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	transaction, err := session.Begin(ctx, options)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) || pgconn.SafeToRetry(err) {
			session.Release()
			if postgresError != nil {
				return nil, nil, nil, storeError(operation, StoreErrorInternal, err)
			}
			return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
		}
		return nil, nil, nil, storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return session, transaction, &release, nil
}

func commitCredentialRead(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool) error {
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return nil
}

func finishCredentialRead(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
		if postgresError != nil {
			return storeError(operation, StoreErrorInternal, cause)
		}
		return classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func finishCredentialPreCandidate(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
		if postgresError != nil {
			return storeError(operation, StoreErrorInternal, cause)
		}
		return classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func finishCredentialStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (bool, error) {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
		if postgresError != nil && postgresError.Code == "23505" {
			return false, storeError(operation, StoreErrorConflict, cause)
		}
		if postgresError != nil {
			return false, storeError(operation, StoreErrorInternal, cause)
		}
		return false, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return true, storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func credentialBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(operation, code, rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause))
}

func credentialAdvisoryKey(ref string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/credential/v1\x00" + ref))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}
