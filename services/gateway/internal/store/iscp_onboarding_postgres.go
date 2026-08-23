package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const onboardingAdvisoryNamespace = "sparkclaw:iscp-onboarding:v1:"

type onboardingPostgresRow interface {
	Scan(...any) error
}

type onboardingPostgresRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

type onboardingPostgresTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) onboardingPostgresRow
	Query(context.Context, string, ...any) (onboardingPostgresRows, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type onboardingPostgresSession interface {
	Begin(context.Context, pgx.TxOptions) (onboardingPostgresTx, error)
	Release()
	Terminate(context.Context) error
}

type onboardingPostgresOps interface {
	Acquire(context.Context) (onboardingPostgresSession, error)
}

type pgxOnboardingPostgresOps struct {
	pool *pgxpool.Pool
}

func (o pgxOnboardingPostgresOps) Acquire(ctx context.Context) (onboardingPostgresSession, error) {
	connection, err := o.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &pgxOnboardingPostgresSession{connection: connection}, nil
}

type pgxOnboardingPostgresSession struct {
	connection *pgxpool.Conn
}

func (s *pgxOnboardingPostgresSession) Begin(ctx context.Context, options pgx.TxOptions) (onboardingPostgresTx, error) {
	transaction, err := s.connection.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return pgxOnboardingPostgresTx{transaction: transaction}, nil
}

func (s *pgxOnboardingPostgresSession) Release() {
	if s.connection != nil {
		s.connection.Release()
		s.connection = nil
	}
}

func (s *pgxOnboardingPostgresSession) Terminate(ctx context.Context) error {
	if s.connection == nil {
		return nil
	}
	raw := s.connection.Hijack()
	s.connection = nil
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return raw.PgConn().Close(closeCtx)
}

type pgxOnboardingPostgresTx struct {
	transaction pgx.Tx
}

func (t pgxOnboardingPostgresTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return t.transaction.Exec(ctx, sql, arguments...)
}

func (t pgxOnboardingPostgresTx) QueryRow(ctx context.Context, sql string, arguments ...any) onboardingPostgresRow {
	return t.transaction.QueryRow(ctx, sql, arguments...)
}

func (t pgxOnboardingPostgresTx) Query(ctx context.Context, sql string, arguments ...any) (onboardingPostgresRows, error) {
	return t.transaction.Query(ctx, sql, arguments...)
}

func (t pgxOnboardingPostgresTx) Commit(ctx context.Context) error {
	return t.transaction.Commit(ctx)
}

func (t pgxOnboardingPostgresTx) Rollback(ctx context.Context) error {
	return t.transaction.Rollback(ctx)
}

func (s *PostgresStore) SaveISCPOnboarding(ctx context.Context, onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	ctx, cancel := operationContext(ctx, OperationISCPOnboardingSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationISCPOnboardingSave, ctx); err != nil {
		return app.ISCPOnboarding{}, err
	}
	onboarding, err := normalizeISCPOnboarding(onboarding, time.Now().UTC())
	if err != nil {
		return app.ISCPOnboarding{}, storeError(ctx, OperationISCPOnboardingSave, StoreErrorInvalid, err)
	}
	session, err := s.onboardingPostgres.Acquire(ctx)
	if err != nil {
		return app.ISCPOnboarding{}, classifyPostgresPreTransaction(OperationISCPOnboardingSave, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err != nil {
		return app.ISCPOnboarding{}, classifyPostgresPreTransaction(OperationISCPOnboardingSave, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, onboardingAdvisoryKey(onboarding.ID)); err != nil {
		return app.ISCPOnboarding{}, finishPostgresOnboardingStatement(ctx, session, transaction, &release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO iscp_onboardings (id,owner_id,domain_id,authority_ref,ticket_id,status,created_at,payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, onboarding.ID, onboarding.OwnerID, onboarding.DomainID, onboarding.AuthorityRef, onboarding.TicketID,
		onboarding.Status, onboarding.CreatedAt, mustJSON(onboarding)); err != nil {
		return app.ISCPOnboarding{}, finishPostgresOnboardingStatement(ctx, session, transaction, &release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		closeErr := session.Terminate(ctx)
		return app.ISCPOnboarding{}, storeError(ctx, OperationISCPOnboardingSave, StoreErrorUnknownOutcome, errors.Join(err, closeErr))
	}
	return onboarding, nil
}

func (s *PostgresStore) GetISCPOnboarding(ctx context.Context, id string) (app.ISCPOnboarding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationISCPOnboardingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationISCPOnboardingGet, ctx); err != nil {
		return app.ISCPOnboarding{}, false, err
	}
	session, err := s.onboardingPostgres.Acquire(ctx)
	if err != nil {
		return app.ISCPOnboarding{}, false, classifyPostgresPreTransaction(OperationISCPOnboardingGet, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.ISCPOnboarding{}, false, classifyPostgresPreTransaction(OperationISCPOnboardingGet, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, onboardingAdvisoryKey(id)); err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return app.ISCPOnboarding{}, false, classifyPostgresReadError(OperationISCPOnboardingGet, ctx, err)
	}
	var raw []byte
	err = transaction.QueryRow(ctx, `SELECT payload FROM iscp_onboardings WHERE id=$1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := transaction.Commit(ctx); err != nil {
			release = false
			err = errors.Join(err, session.Terminate(ctx))
			return app.ISCPOnboarding{}, false, classifyPostgresReadError(OperationISCPOnboardingGet, ctx, err)
		}
		return app.ISCPOnboarding{}, false, nil
	}
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return app.ISCPOnboarding{}, false, classifyPostgresReadError(OperationISCPOnboardingGet, ctx, err)
	}
	onboarding, err := decodeISCPOnboarding(raw, id)
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return app.ISCPOnboarding{}, false, storeError(ctx, OperationISCPOnboardingGet, StoreErrorCorrupt, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		err = errors.Join(err, session.Terminate(ctx))
		return app.ISCPOnboarding{}, false, classifyPostgresReadError(OperationISCPOnboardingGet, ctx, err)
	}
	return onboarding, true, nil
}

func (s *PostgresStore) ListISCPOnboardings(ctx context.Context, ownerID string) ([]app.ISCPOnboarding, error) {
	ctx, cancel := operationContext(ctx, OperationISCPOnboardingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationISCPOnboardingList, ctx); err != nil {
		return nil, err
	}
	session, err := s.onboardingPostgres.Acquire(ctx)
	if err != nil {
		return nil, classifyPostgresPreTransaction(OperationISCPOnboardingList, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, classifyPostgresPreTransaction(OperationISCPOnboardingList, ctx, err)
	}
	rows, err := transaction.Query(ctx, `
		SELECT id, owner_id, payload FROM iscp_onboardings
		WHERE ($1='' OR owner_id=$1)
		ORDER BY created_at DESC, id ASC
	`, ownerID)
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return nil, classifyPostgresReadError(OperationISCPOnboardingList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.ISCPOnboarding, 0)
	for rows.Next() {
		var rowID, rowOwnerID string
		var raw []byte
		if err := rows.Scan(&rowID, &rowOwnerID, &raw); err != nil {
			rows.Close()
			err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
			return nil, classifyPostgresReadError(OperationISCPOnboardingList, ctx, err)
		}
		onboarding, err := decodeISCPOnboarding(raw, rowID)
		if err != nil {
			rows.Close()
			err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
			return nil, storeError(ctx, OperationISCPOnboardingList, StoreErrorCorrupt, err)
		}
		if onboarding.OwnerID != rowOwnerID || (ownerID != "" && rowOwnerID != ownerID) {
			rows.Close()
			err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, errors.New("onboarding payload owner does not match indexed owner"))
			return nil, storeError(ctx, OperationISCPOnboardingList, StoreErrorCorrupt, err)
		}
		out = append(out, onboarding)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, &release, err)
		return nil, classifyPostgresReadError(OperationISCPOnboardingList, ctx, err)
	}
	rows.Close()
	if err := transaction.Commit(ctx); err != nil {
		release = false
		err = errors.Join(err, session.Terminate(ctx))
		return nil, classifyPostgresReadError(OperationISCPOnboardingList, ctx, err)
	}
	return out, nil
}

func onboardingAdvisoryKey(id string) int64 {
	digest := sha256.Sum256([]byte(onboardingAdvisoryNamespace + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func finishPostgresOnboardingStatement(
	ctx context.Context,
	session onboardingPostgresSession,
	transaction onboardingPostgresTx,
	release *bool,
	cause error,
) error {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		rollbackErr := transaction.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			*release = false
			rollbackErr = errors.Join(rollbackErr, session.Terminate(ctx))
		}
		joined := errors.Join(cause, rollbackErr)
		if postgresError != nil && postgresError.Code == "23505" {
			return storeError(ctx, OperationISCPOnboardingSave, StoreErrorConflict, errors.Join(ErrISCPOnboardingConflict, joined))
		}
		if postgresError != nil {
			return storeError(ctx, OperationISCPOnboardingSave, StoreErrorInternal, joined)
		}
		return classifyPostgresPreTransaction(OperationISCPOnboardingSave, ctx, joined)
	}
	*release = false
	closeErr := session.Terminate(ctx)
	return storeError(ctx, OperationISCPOnboardingSave, StoreErrorUnknownOutcome, errors.Join(cause, closeErr))
}

func classifyPostgresPreTransaction(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return contextStoreError(operation, ctx, cause)
	}
	return storeError(ctx, operation, StoreErrorUnavailable, cause)
}

func classifyPostgresReadError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return contextStoreError(operation, ctx, cause)
	}
	return storeError(ctx, operation, StoreErrorUnavailable, cause)
}

func rollbackPostgresOnboardingRead(
	ctx context.Context,
	session onboardingPostgresSession,
	transaction onboardingPostgresTx,
	release *bool,
	cause error,
) error {
	if rollbackErr := transaction.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
		*release = false
		return errors.Join(cause, rollbackErr, session.Terminate(ctx))
	}
	return cause
}

func decodeISCPOnboarding(raw []byte, expectedID string) (app.ISCPOnboarding, error) {
	var onboarding app.ISCPOnboarding
	if err := json.Unmarshal(raw, &onboarding); err != nil {
		return app.ISCPOnboarding{}, err
	}
	normalized, err := normalizeISCPOnboarding(onboarding, time.Now().UTC())
	if err != nil {
		return app.ISCPOnboarding{}, err
	}
	if expectedID != "" && normalized.ID != expectedID {
		return app.ISCPOnboarding{}, fmt.Errorf("onboarding payload ID %q does not match row ID", normalized.ID)
	}
	return normalized, nil
}
