package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const clientSelectSQL = `SELECT id, owner_id, actor_id, name, token_hash, created_at, last_seen_at, revoked_at FROM clients`
const pairingCodeSelectSQL = `SELECT id, code_hash, status, expires_at, created_at, claimed_at, coalesce(client_id, '') FROM pairing_codes`

func (s *PostgresStore) validateClientState(ctx context.Context) error {
	startupCtx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	clients := map[string]app.Client{}
	rows, err := s.clientPostgres.Query(startupCtx, clientSelectSQL)
	if err != nil {
		return fmt.Errorf("validate clients: %w", err)
	}
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("validate clients: %w", err)
		}
		clients[client.ID] = client
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("validate clients: %w", err)
	}
	rows.Close()
	pairings := map[string]app.PairingCode{}
	rows, err = s.clientPostgres.Query(startupCtx, pairingCodeSelectSQL)
	if err != nil {
		return fmt.Errorf("validate pairing codes: %w", err)
	}
	for rows.Next() {
		code, err := scanPairingCode(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("validate pairing codes: %w", err)
		}
		pairings[code.ID] = code
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("validate pairing codes: %w", err)
	}
	rows.Close()
	if err := normalizeAndValidatePersistedClientsAndPairings(clients, pairings); err != nil {
		return fmt.Errorf("validate client state: %w", err)
	}
	for id, client := range clients {
		s.clientWriteHighWater[id] = latestClientTime(client)
	}
	for id, code := range pairings {
		s.pairingWriteHighWater[id] = latestPairingTime(code)
	}
	return nil
}

func latestClientTime(client app.Client) time.Time {
	latest := client.CreatedAt
	if client.LastSeenAt != nil && client.LastSeenAt.After(latest) {
		latest = *client.LastSeenAt
	}
	if client.RevokedAt != nil && client.RevokedAt.After(latest) {
		latest = *client.RevokedAt
	}
	return latest
}

func latestPairingTime(code app.PairingCode) time.Time {
	latest := code.CreatedAt
	if code.ClaimedAt != nil && code.ClaimedAt.After(latest) {
		latest = *code.ClaimedAt
	}
	return latest
}

func normalizePostgresClient(client app.Client) (app.Client, error) {
	if strings.TrimSpace(client.OwnerID) == "" {
		client.OwnerID = app.DefaultOwnerID
	}
	if strings.TrimSpace(client.ActorID) == "" {
		client.ActorID = client.OwnerID
	}
	if err := validatePersistedClient(client); err != nil {
		return app.Client{}, err
	}
	return cloneClient(client), nil
}

func (s *PostgresStore) GetClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientGet, ctx); err != nil {
		return app.Client{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.Client{}, false, nil
	}
	session, transaction, release, err := s.beginClientTransaction(ctx, OperationClientGet, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.Client{}, false, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, clientAdvisoryKey(id)); err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, release, err)
		return app.Client{}, false, classifyPostgresReadError(OperationClientGet, ctx, err)
	}
	client, err := scanClient(transaction.QueryRow(ctx, clientSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := commitClientRead(ctx, OperationClientGet, session, transaction, release); err != nil {
			return app.Client{}, false, err
		}
		return app.Client{}, false, nil
	}
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, release, err)
		return app.Client{}, false, classifyPostgresReadError(OperationClientGet, ctx, err)
	}
	client, err = normalizePostgresClient(client)
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, release, err)
		return app.Client{}, false, storeError(OperationClientGet, StoreErrorCorrupt, err)
	}
	if err := commitClientRead(ctx, OperationClientGet, session, transaction, release); err != nil {
		return app.Client{}, false, err
	}
	return client, true, nil
}

func (s *PostgresStore) ListClients(ctx context.Context) ([]app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationClientList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.clientPostgres.Query(ctx, clientSelectSQL+` ORDER BY created_at DESC, id ASC`)
	if err != nil {
		return nil, classifyPostgresReadError(OperationClientList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.Client, 0)
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			return nil, classifyPostgresReadError(OperationClientList, ctx, err)
		}
		client, err = normalizePostgresClient(client)
		if err != nil {
			return nil, storeError(OperationClientList, StoreErrorCorrupt, err)
		}
		out = append(out, client)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationClientList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) FindClientByTokenHash(ctx context.Context, tokenHash string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientFindTokenHash, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientFindTokenHash, ctx); err != nil {
		return app.Client{}, false, err
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return app.Client{}, false, nil
	}
	client, err := scanClient(s.clientPostgres.QueryRow(ctx, clientSelectSQL+` WHERE token_hash=$1 AND revoked_at IS NULL`, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Client{}, false, nil
	}
	if err != nil {
		return app.Client{}, false, classifyPostgresReadError(OperationClientFindTokenHash, ctx, err)
	}
	client, err = normalizePostgresClient(client)
	if err != nil {
		return app.Client{}, false, storeError(OperationClientFindTokenHash, StoreErrorCorrupt, err)
	}
	return client, true, nil
}

func (s *PostgresStore) RevokeClient(ctx context.Context, id string) (app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationClientRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientRevoke, ctx); err != nil {
		return app.Client{}, err
	}
	id = strings.TrimSpace(id)
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	session, transaction, release, err := s.beginClientTransaction(ctx, OperationClientRevoke, pgx.TxOptions{})
	if err != nil {
		return app.Client{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, clientAdvisoryKey(id)); err != nil {
		return app.Client{}, finishClientPreCandidate(ctx, OperationClientRevoke, session, transaction, release, err)
	}
	client, err := scanClient(transaction.QueryRow(ctx, clientSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Client{}, clientBusinessError(ctx, OperationClientRevoke, StoreErrorNotFound, session, transaction, release, errors.New("client not found"))
	}
	if err != nil {
		return app.Client{}, finishClientPreCandidate(ctx, OperationClientRevoke, session, transaction, release, err)
	}
	client, err = normalizePostgresClient(client)
	if err != nil {
		return app.Client{}, clientBusinessError(ctx, OperationClientRevoke, StoreErrorCorrupt, session, transaction, release, err)
	}
	commandAt := nextRepositoryTime(s.clientNow(), s.clientWriteHighWater[id], latestClientTime(client))
	client.RevokedAt = cloneTimePointer(&commandAt)
	s.clientWriteHighWater[id] = commandAt
	statements := []struct {
		sql  string
		args []any
	}{
		{`UPDATE clients SET revoked_at=$2 WHERE id=$1`, []any{id, commandAt}},
		{`INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields) VALUES ($1,$2,'client.revoked',NULL,NULL,'owner',$3,$4)`, []any{app.NewID("audit"), commandAt, client.Name, optionalJSON(map[string]any{"client_id": client.ID})}},
		{`INSERT INTO events (id,happened_at,type,session_id,run_id,payload) VALUES ($1,$2,'client.revoked',NULL,NULL,$3)`, []any{app.NewID("evt"), commandAt, mustJSON(client)}},
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.sql, statement.args...); err != nil {
			unknown, resultErr := finishClientStatement(ctx, OperationClientRevoke, session, transaction, release, err)
			if unknown {
				return cloneClient(client), resultErr
			}
			return app.Client{}, resultErr
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return cloneClient(client), storeError(OperationClientRevoke, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneClient(client), nil
}

func (s *PostgresStore) TouchClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientTouch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientTouch, ctx); err != nil {
		return app.Client{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.Client{}, false, nil
	}
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	session, transaction, release, err := s.beginClientTransaction(ctx, OperationClientTouch, pgx.TxOptions{})
	if err != nil {
		return app.Client{}, false, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, clientAdvisoryKey(id)); err != nil {
		return app.Client{}, false, finishClientPreCandidate(ctx, OperationClientTouch, session, transaction, release, err)
	}
	client, err := scanClient(transaction.QueryRow(ctx, clientSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := commitClientRead(ctx, OperationClientTouch, session, transaction, release); err != nil {
			return app.Client{}, false, err
		}
		return app.Client{}, false, nil
	}
	if err != nil {
		return app.Client{}, false, finishClientPreCandidate(ctx, OperationClientTouch, session, transaction, release, err)
	}
	client, err = normalizePostgresClient(client)
	if err != nil {
		return app.Client{}, false, clientBusinessError(ctx, OperationClientTouch, StoreErrorCorrupt, session, transaction, release, err)
	}
	if client.RevokedAt != nil {
		if err := commitClientRead(ctx, OperationClientTouch, session, transaction, release); err != nil {
			return app.Client{}, false, err
		}
		return app.Client{}, false, nil
	}
	commandAt := nextRepositoryTime(s.clientNow(), s.clientWriteHighWater[id], latestClientTime(client))
	client.LastSeenAt = cloneTimePointer(&commandAt)
	s.clientWriteHighWater[id] = commandAt
	if _, err := transaction.Exec(ctx, `UPDATE clients SET last_seen_at=$2 WHERE id=$1 AND revoked_at IS NULL`, id, commandAt); err != nil {
		unknown, resultErr := finishClientStatement(ctx, OperationClientTouch, session, transaction, release, err)
		if unknown {
			return cloneClient(client), true, resultErr
		}
		return app.Client{}, false, resultErr
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return cloneClient(client), true, storeError(OperationClientTouch, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneClient(client), true, nil
}

func (s *PostgresStore) SavePairingCode(ctx context.Context, code app.PairingCode) (app.PairingCode, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeSave, ctx); err != nil {
		return app.PairingCode{}, err
	}
	code, err := normalizePairingSave(code)
	if err != nil {
		return app.PairingCode{}, storeError(OperationPairingCodeSave, StoreErrorInvalid, err)
	}
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	session, transaction, release, err := s.beginClientTransaction(ctx, OperationPairingCodeSave, pgx.TxOptions{})
	if err != nil {
		return app.PairingCode{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairingAdvisoryKey(code.ID)); err != nil {
		return app.PairingCode{}, finishClientPreCandidate(ctx, OperationPairingCodeSave, session, transaction, release, err)
	}
	existing, err := scanPairingCode(transaction.QueryRow(ctx, pairingCodeSelectSQL+` WHERE id=$1`, code.ID))
	if err == nil {
		validationErr, readErr := s.validatePairingInTransaction(ctx, transaction, existing)
		if readErr != nil {
			return app.PairingCode{}, finishClientPreCandidate(ctx, OperationPairingCodeSave, session, transaction, release, readErr)
		}
		if validationErr != nil {
			return app.PairingCode{}, clientBusinessError(ctx, OperationPairingCodeSave, StoreErrorCorrupt, session, transaction, release, validationErr)
		}
		return app.PairingCode{}, clientBusinessError(ctx, OperationPairingCodeSave, StoreErrorConflict, session, transaction, release, errors.New("pairing ID already exists"))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return app.PairingCode{}, finishClientPreCandidate(ctx, OperationPairingCodeSave, session, transaction, release, err)
	}
	createdAt := nextRepositoryTime(s.clientNow(), s.pairingWriteHighWater[code.ID])
	code.CreatedAt = createdAt
	s.pairingWriteHighWater[code.ID] = createdAt
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO pairing_codes (id,code_hash,status,expires_at,created_at,claimed_at,client_id) VALUES ($1,$2,$3,$4,$5,NULL,NULL)`, []any{code.ID, code.CodeHash, code.Status, code.ExpiresAt, code.CreatedAt}},
		{`INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields) VALUES ($1,$2,'pairing_code.created',NULL,NULL,'gateway','Pairing code created',$3)`, []any{app.NewID("audit"), createdAt, optionalJSON(map[string]any{"pairing_id": code.ID})}},
		{`INSERT INTO events (id,happened_at,type,session_id,run_id,payload) VALUES ($1,$2,'pairing_code.created',NULL,NULL,$3)`, []any{app.NewID("evt"), createdAt, mustJSON(code)}},
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.sql, statement.args...); err != nil {
			unknown, resultErr := finishClientStatement(ctx, OperationPairingCodeSave, session, transaction, release, err)
			if unknown {
				return clonePairingCode(code), resultErr
			}
			return app.PairingCode{}, resultErr
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return clonePairingCode(code), storeError(OperationPairingCodeSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return clonePairingCode(code), nil
}

func (s *PostgresStore) GetPairingCode(ctx context.Context, id string) (app.PairingCode, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeGet, ctx); err != nil {
		return app.PairingCode{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.PairingCode{}, false, nil
	}
	session, transaction, release, err := s.beginClientTransaction(ctx, OperationPairingCodeGet, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return app.PairingCode{}, false, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairingAdvisoryKey(id)); err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, release, err)
		return app.PairingCode{}, false, classifyPostgresReadError(OperationPairingCodeGet, ctx, err)
	}
	code, err := scanPairingCode(transaction.QueryRow(ctx, pairingCodeSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := commitClientRead(ctx, OperationPairingCodeGet, session, transaction, release); err != nil {
			return app.PairingCode{}, false, err
		}
		return app.PairingCode{}, false, nil
	}
	if err != nil {
		err = rollbackPostgresOnboardingRead(ctx, session, transaction, release, err)
		return app.PairingCode{}, false, classifyPostgresReadError(OperationPairingCodeGet, ctx, err)
	}
	validationErr, readErr := s.validatePairingInTransaction(ctx, transaction, code)
	if readErr != nil {
		readErr = rollbackPostgresOnboardingRead(ctx, session, transaction, release, readErr)
		return app.PairingCode{}, false, classifyPostgresReadError(OperationPairingCodeGet, ctx, readErr)
	}
	if validationErr != nil {
		validationErr = rollbackPostgresOnboardingRead(ctx, session, transaction, release, validationErr)
		return app.PairingCode{}, false, storeError(OperationPairingCodeGet, StoreErrorCorrupt, validationErr)
	}
	if err := commitClientRead(ctx, OperationPairingCodeGet, session, transaction, release); err != nil {
		return app.PairingCode{}, false, err
	}
	return clonePairingCode(code), true, nil
}

func (s *PostgresStore) ClaimPairingCode(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeClaim, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeClaim, ctx); err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	client, err := normalizeClaimClient(client)
	if err != nil {
		return app.PairingCode{}, app.Client{}, storeError(OperationPairingCodeClaim, StoreErrorInvalid, err)
	}
	id = strings.TrimSpace(id)
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	session, transaction, release, err := s.beginClientTransaction(ctx, OperationPairingCodeClaim, pgx.TxOptions{})
	if err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairingAdvisoryKey(id)); err != nil {
		return app.PairingCode{}, app.Client{}, finishClientPreCandidate(ctx, OperationPairingCodeClaim, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, clientAdvisoryKey(client.ID)); err != nil {
		return app.PairingCode{}, app.Client{}, finishClientPreCandidate(ctx, OperationPairingCodeClaim, session, transaction, release, err)
	}
	code, err := scanPairingCode(transaction.QueryRow(ctx, pairingCodeSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.PairingCode{}, app.Client{}, clientBusinessError(ctx, OperationPairingCodeClaim, StoreErrorNotFound, session, transaction, release, errors.New("pairing code not found"))
	}
	if err != nil {
		return app.PairingCode{}, app.Client{}, finishClientPreCandidate(ctx, OperationPairingCodeClaim, session, transaction, release, err)
	}
	validationErr, readErr := s.validatePairingInTransaction(ctx, transaction, code)
	if readErr != nil {
		return app.PairingCode{}, app.Client{}, finishClientPreCandidate(ctx, OperationPairingCodeClaim, session, transaction, release, readErr)
	}
	if validationErr != nil {
		return app.PairingCode{}, app.Client{}, clientBusinessError(ctx, OperationPairingCodeClaim, StoreErrorCorrupt, session, transaction, release, validationErr)
	}
	now := postgresTime(s.clientNow())
	if code.Status != "pending" || strings.TrimSpace(code.CodeHash) == "" || !code.ExpiresAt.After(now) {
		return app.PairingCode{}, app.Client{}, clientBusinessError(ctx, OperationPairingCodeClaim, StoreErrorConflict, session, transaction, release, errors.New("pairing code is not claimable"))
	}
	existing, err := scanClient(transaction.QueryRow(ctx, clientSelectSQL+` WHERE id=$1 OR token_hash=$2 LIMIT 1`, client.ID, client.TokenHash))
	if err == nil {
		if _, err := normalizePostgresClient(existing); err != nil {
			return app.PairingCode{}, app.Client{}, clientBusinessError(ctx, OperationPairingCodeClaim, StoreErrorCorrupt, session, transaction, release, err)
		}
		return app.PairingCode{}, app.Client{}, clientBusinessError(ctx, OperationPairingCodeClaim, StoreErrorConflict, session, transaction, release, errors.New("client ID or token hash already exists"))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return app.PairingCode{}, app.Client{}, finishClientPreCandidate(ctx, OperationPairingCodeClaim, session, transaction, release, err)
	}
	commandAt := nextRepositoryTime(now, s.pairingWriteHighWater[id], s.clientWriteHighWater[client.ID], latestPairingTime(code))
	client.CreatedAt = commandAt
	code.Status = "claimed"
	code.ClaimedAt = cloneTimePointer(&commandAt)
	code.ClientID = client.ID
	s.clientWriteHighWater[client.ID] = commandAt
	s.pairingWriteHighWater[id] = commandAt
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clients (id,owner_id,actor_id,name,token_hash,created_at,last_seen_at,revoked_at) VALUES ($1,$2,$3,$4,$5,$6,NULL,NULL)`, []any{client.ID, client.OwnerID, client.ActorID, client.Name, client.TokenHash, commandAt}},
		{`UPDATE pairing_codes SET status='claimed',claimed_at=$2,client_id=$3 WHERE id=$1`, []any{code.ID, commandAt, client.ID}},
		{`INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields) VALUES ($1,$2,'client.saved',NULL,NULL,'gateway',$3,$4)`, []any{app.NewID("audit"), commandAt, client.Name, optionalJSON(map[string]any{"client_id": client.ID})}},
		{`INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields) VALUES ($1,$2,'pairing_code.claimed',NULL,NULL,'gateway','Pairing code claimed',$3)`, []any{app.NewID("audit"), commandAt, optionalJSON(map[string]any{"pairing_id": code.ID, "client_id": client.ID})}},
		{`INSERT INTO events (id,happened_at,type,session_id,run_id,payload) VALUES ($1,$2,'client.saved',NULL,NULL,$3)`, []any{app.NewID("evt"), commandAt, mustJSON(client)}},
		{`INSERT INTO events (id,happened_at,type,session_id,run_id,payload) VALUES ($1,$2,'pairing_code.claimed',NULL,NULL,$3)`, []any{app.NewID("evt"), commandAt, mustJSON(code)}},
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.sql, statement.args...); err != nil {
			unknown, resultErr := finishClientStatement(ctx, OperationPairingCodeClaim, session, transaction, release, err)
			if unknown {
				return clonePairingCode(code), cloneClient(client), resultErr
			}
			return app.PairingCode{}, app.Client{}, resultErr
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return clonePairingCode(code), cloneClient(client), storeError(OperationPairingCodeClaim, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return clonePairingCode(code), cloneClient(client), nil
}

func (s *PostgresStore) validatePairingInTransaction(ctx context.Context, transaction onboardingPostgresTx, code app.PairingCode) (validationErr, readErr error) {
	clients := map[string]app.Client{}
	if code.Status == "claimed" && code.ClientID != "" {
		client, err := scanClient(transaction.QueryRow(ctx, clientSelectSQL+` WHERE id=$1`, code.ClientID))
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("claimed pairing code references a missing client"), nil
		}
		if err != nil {
			return nil, err
		}
		client, err = normalizePostgresClient(client)
		if err != nil {
			return err, nil
		}
		clients[client.ID] = client
	}
	return validatePersistedPairingCode(code, clients), nil
}

func (s *PostgresStore) beginClientTransaction(ctx context.Context, operation StoreOperation, options pgx.TxOptions) (onboardingPostgresSession, onboardingPostgresTx, *bool, error) {
	session, err := s.clientPostgres.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	transaction, err := session.Begin(ctx, options)
	if err != nil {
		session.Release()
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	return session, transaction, &release, nil
}

func commitClientRead(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool) error {
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return classifyPostgresReadError(operation, ctx, errors.Join(err, session.Terminate(ctx)))
	}
	return nil
}

func finishClientPreCandidate(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
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

func finishClientStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (bool, error) {
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

func clientBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(operation, code, rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause))
}

func clientAdvisoryKey(id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/client/v1\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func pairingAdvisoryKey(id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/pairing/v1\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}
