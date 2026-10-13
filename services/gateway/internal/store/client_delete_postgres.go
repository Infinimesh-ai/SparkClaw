package store

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) DeleteClient(ctx context.Context, id string) (app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationClientDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientDelete, ctx); err != nil {
		return app.Client{}, err
	}
	id = strings.TrimSpace(id)
	releaseCommand, err := s.acquireClientCommand(ctx, OperationClientDelete)
	if err != nil {
		return app.Client{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginClientTransaction(ctx, OperationClientDelete, pgx.TxOptions{})
	if err != nil {
		return app.Client{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, clientAdvisoryKey(id)); err != nil {
		return app.Client{}, finishClientPreCandidate(ctx, OperationClientDelete, session, transaction, release, err)
	}
	client, err := scanClient(transaction.QueryRow(ctx, clientSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Client{}, clientBusinessError(ctx, OperationClientDelete, StoreErrorNotFound, session, transaction, release, errors.New("client not found"))
	}
	if err != nil {
		return app.Client{}, finishClientPreCandidate(ctx, OperationClientDelete, session, transaction, release, err)
	}
	client, err = normalizePostgresClient(client)
	if err != nil {
		return app.Client{}, clientBusinessError(ctx, OperationClientDelete, StoreErrorCorrupt, session, transaction, release, err)
	}
	if client.RevokedAt == nil {
		return app.Client{}, clientBusinessError(ctx, OperationClientDelete, StoreErrorConflict, session, transaction, release, errors.New("only revoked clients can be deleted"))
	}
	if client.DeletedAt != nil {
		if err := commitClientRead(ctx, OperationClientDelete, session, transaction, release); err != nil {
			return app.Client{}, err
		}
		return cloneClient(client), nil
	}
	commandAt := nextRepositoryTime(s.clientNow(), s.clientWriteHighWater[id], latestClientTime(client))
	client.Name, client.LastSeenAt, client.DeletedAt = "", nil, cloneTimePointer(&commandAt)
	s.clientWriteHighWater[id] = commandAt
	statements := []struct {
		sql  string
		args []any
	}{
		{`UPDATE clients SET name='', last_seen_at=NULL, deleted_at=$2 WHERE id=$1`, []any{id, commandAt}},
		{`INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields) VALUES ($1,$2,'client.deleted',NULL,NULL,'owner',$3,$4)`, []any{app.NewID("audit"), commandAt, "Revoked client deleted", optionalJSON(map[string]any{"client_id": client.ID})}},
		{`INSERT INTO events (id,happened_at,type,session_id,run_id,payload) VALUES ($1,$2,'client.deleted',NULL,NULL,$3)`, []any{app.NewID("evt"), commandAt, mustJSON(client)}},
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.sql, statement.args...); err != nil {
			unknown, resultErr := finishClientStatement(ctx, OperationClientDelete, session, transaction, release, err)
			if unknown {
				return cloneClient(client), resultErr
			}
			return app.Client{}, resultErr
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return cloneClient(client), storeError(ctx, OperationClientDelete, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneClient(client), nil
}
