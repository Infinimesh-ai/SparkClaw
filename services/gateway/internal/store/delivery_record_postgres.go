package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) SaveMessageReceive(ctx context.Context, record app.MessageReceiveRecord) (app.MessageReceiveRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveSave, ctx); err != nil {
		return app.MessageReceiveRecord{}, err
	}
	candidate, err := prepareMessageReceive(record, app.MessageReceiveRecord{}, time.Now().UTC())
	if err != nil {
		return app.MessageReceiveRecord{}, storeError(ctx, OperationMessageReceiveSave, StoreErrorInvalid, err)
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationMessageReceiveSave, s.deliveryRecordPostgres)
	if err != nil {
		return app.MessageReceiveRecord{}, err
	}
	defer releasePostgresSession(session, release)
	if err := lockDeliveryRecordKeys(ctx, transaction, "receive", candidate.ID, string(candidate.SourceEndpointID)+"\x00"+candidate.NativeMessageID); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageReceiveSave, candidate, session, transaction, release, err)
	}
	byID, foundByID, err := queryMessageReceiveTransaction(ctx, transaction, `SELECT record FROM message_receive_records WHERE id = $1 FOR UPDATE`, candidate.ID)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageReceiveSave, candidate, session, transaction, release, err)
	}
	byKey, foundByKey, err := queryMessageReceiveTransaction(ctx, transaction, `
		SELECT record FROM message_receive_records
		WHERE source_endpoint_id = $1 AND native_message_id = $2 FOR UPDATE
	`, candidate.SourceEndpointID, candidate.NativeMessageID)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageReceiveSave, candidate, session, transaction, release, err)
	}
	if foundByID && foundByKey && byID.ID != byKey.ID {
		return app.MessageReceiveRecord{}, deliveryRecordPostgresBusinessError(ctx, OperationMessageReceiveSave, StoreErrorConflict, session, transaction, release, ErrMessageReceiveConflict)
	}
	current := byKey
	if !foundByKey {
		current = byID
	}
	candidate, err = prepareMessageReceive(candidate, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrMessageReceiveConflict) {
			code = StoreErrorConflict
		}
		return app.MessageReceiveRecord{}, deliveryRecordPostgresBusinessError(ctx, OperationMessageReceiveSave, code, session, transaction, release, err)
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageReceiveSave, candidate, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO message_receive_records (id, owner_id, actor_id, source_endpoint_id, native_message_id, status, record, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id, actor_id = EXCLUDED.actor_id,
			source_endpoint_id = EXCLUDED.source_endpoint_id, native_message_id = EXCLUDED.native_message_id,
			status = EXCLUDED.status, record = EXCLUDED.record, updated_at = EXCLUDED.updated_at
	`, candidate.ID, candidate.OwnerID, candidate.ActorID, candidate.SourceEndpointID, candidate.NativeMessageID,
		candidate.Status, raw, candidate.UpdatedAt); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageReceiveSave, candidate, session, transaction, release, err)
	}
	if err := appendDeliveryRecordAudit(transaction, ctx, "message.receive."+candidate.Status, "", candidate.LinkedRunID,
		"gateway", candidate.ProviderKey, map[string]any{"receive_id": candidate.ID, "endpoint_id": candidate.SourceEndpointID}); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageReceiveSave, candidate, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(ctx, OperationMessageReceiveSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneMessageReceive(candidate), nil
}

func (s *PostgresStore) GetMessageReceive(ctx context.Context, id string) (app.MessageReceiveRecord, bool, error) {
	return s.queryMessageReceive(ctx, OperationMessageReceiveGet, `SELECT record FROM message_receive_records WHERE id = $1`, strings.TrimSpace(id))
}

func (s *PostgresStore) FindMessageReceive(ctx context.Context, sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool, error) {
	return s.queryMessageReceive(ctx, OperationMessageReceiveFind,
		`SELECT record FROM message_receive_records WHERE source_endpoint_id = $1 AND native_message_id = $2`,
		app.EndpointID(strings.TrimSpace(string(sourceEndpointID))), strings.TrimSpace(nativeMessageID))
}

func (s *PostgresStore) queryMessageReceive(ctx context.Context, operation StoreOperation, query string, args ...any) (app.MessageReceiveRecord, bool, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	record, err := scanMessageReceiveJSON(s.deliveryRecordPostgres.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MessageReceiveRecord{}, false, nil
	}
	if err != nil {
		return app.MessageReceiveRecord{}, false, classifyDeliveryRecordPostgresError(operation, ctx, err)
	}
	return cloneMessageReceive(record), true, nil
}

func (s *PostgresStore) ListMessageReceives(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageReceiveRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.deliveryRecordPostgres.Query(ctx, `
		SELECT record FROM message_receive_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR actor_id = $2)
		ORDER BY updated_at DESC, id ASC LIMIT $3
	`, strings.TrimSpace(ownerID), strings.TrimSpace(actorID), normalizeDeliveryRecordLimit(limit))
	if err != nil {
		return nil, classifyDeliveryRecordPostgresError(OperationMessageReceiveList, ctx, err)
	}
	defer rows.Close()
	out := []app.MessageReceiveRecord{}
	for rows.Next() {
		record, err := scanMessageReceiveJSON(rows)
		if err != nil {
			return nil, classifyDeliveryRecordPostgresError(OperationMessageReceiveList, ctx, err)
		}
		out = append(out, cloneMessageReceive(record))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDeliveryRecordPostgresError(OperationMessageReceiveList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveMessageDelivery(ctx context.Context, record app.MessageDeliveryRecord) (app.MessageDeliveryRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliverySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliverySave, ctx); err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	candidate, err := prepareMessageDelivery(record, app.MessageDeliveryRecord{}, time.Now().UTC())
	if err != nil {
		return app.MessageDeliveryRecord{}, storeError(ctx, OperationMessageDeliverySave, StoreErrorInvalid, err)
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationMessageDeliverySave, s.deliveryRecordPostgres)
	if err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	defer releasePostgresSession(session, release)
	key := candidate.OwnerID + "\x00" + candidate.ActorID + "\x00" + candidate.Request.IdempotencyKey
	if err := lockDeliveryRecordKeys(ctx, transaction, "delivery", string(candidate.ID), key); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageDeliverySave, candidate, session, transaction, release, err)
	}
	byID, foundByID, err := queryMessageDeliveryTransaction(ctx, transaction, `SELECT record FROM message_delivery_records WHERE id = $1 FOR UPDATE`, candidate.ID)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageDeliverySave, candidate, session, transaction, release, err)
	}
	byKey, foundByKey, err := queryMessageDeliveryTransaction(ctx, transaction, `
		SELECT record FROM message_delivery_records
		WHERE owner_id = $1 AND actor_id = $2 AND idempotency_key = $3 FOR UPDATE
	`, candidate.OwnerID, candidate.ActorID, candidate.Request.IdempotencyKey)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageDeliverySave, candidate, session, transaction, release, err)
	}
	if foundByID && foundByKey && byID.ID != byKey.ID {
		return app.MessageDeliveryRecord{}, deliveryRecordPostgresBusinessError(ctx, OperationMessageDeliverySave, StoreErrorConflict, session, transaction, release, ErrMessageDeliveryConflict)
	}
	current := byKey
	if !foundByKey {
		current = byID
	}
	if current.ID != "" && current.ID != candidate.ID {
		if !messageDeliveryIdentityEqual(current, candidate) {
			return app.MessageDeliveryRecord{}, deliveryRecordPostgresBusinessError(ctx, OperationMessageDeliverySave, StoreErrorConflict, session, transaction, release, ErrMessageDeliveryConflict)
		}
		if err := rollbackPostgresTransaction(ctx, session, transaction, release, nil); err != nil {
			return app.MessageDeliveryRecord{}, classifyDeliveryRecordPostgresError(OperationMessageDeliverySave, ctx, err)
		}
		return cloneMessageDelivery(current), nil
	}
	candidate, err = prepareMessageDelivery(candidate, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrMessageDeliveryConflict) {
			code = StoreErrorConflict
		}
		return app.MessageDeliveryRecord{}, deliveryRecordPostgresBusinessError(ctx, OperationMessageDeliverySave, code, session, transaction, release, err)
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageDeliverySave, candidate, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO message_delivery_records (id, owner_id, actor_id, idempotency_key, content_digest, status, record, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, record = EXCLUDED.record, updated_at = EXCLUDED.updated_at
	`, candidate.ID, candidate.OwnerID, candidate.ActorID, candidate.Request.IdempotencyKey,
		candidate.ContentDigest, candidate.Status, raw, candidate.UpdatedAt); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageDeliverySave, candidate, session, transaction, release, err)
	}
	if err := appendDeliveryRecordAudit(transaction, ctx, "message.send."+string(candidate.Status), "", candidate.Request.RunID,
		candidate.ActorID, candidate.SoftwareDisplayName, map[string]any{"delivery_id": candidate.ID, "endpoint_id": candidate.Request.Target, "origin": candidate.Origin}); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationMessageDeliverySave, candidate, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(ctx, OperationMessageDeliverySave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneMessageDelivery(candidate), nil
}

func (s *PostgresStore) GetMessageDelivery(ctx context.Context, id app.DeliveryID) (app.MessageDeliveryRecord, bool, error) {
	return s.queryMessageDelivery(ctx, OperationMessageDeliveryGet, `SELECT record FROM message_delivery_records WHERE id = $1`, strings.TrimSpace(string(id)))
}

func (s *PostgresStore) FindMessageDeliveryByIdempotency(ctx context.Context, ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool, error) {
	return s.queryMessageDelivery(ctx, OperationMessageDeliveryFind,
		`SELECT record FROM message_delivery_records WHERE owner_id = $1 AND actor_id = $2 AND idempotency_key = $3`,
		strings.TrimSpace(ownerID), strings.TrimSpace(actorID), strings.TrimSpace(idempotencyKey))
}

func (s *PostgresStore) queryMessageDelivery(ctx context.Context, operation StoreOperation, query string, args ...any) (app.MessageDeliveryRecord, bool, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	record, err := scanMessageDeliveryJSON(s.deliveryRecordPostgres.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MessageDeliveryRecord{}, false, nil
	}
	if err != nil {
		return app.MessageDeliveryRecord{}, false, classifyDeliveryRecordPostgresError(operation, ctx, err)
	}
	return cloneMessageDelivery(record), true, nil
}

func (s *PostgresStore) ListMessageDeliveries(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageDeliveryRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliveryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliveryList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.deliveryRecordPostgres.Query(ctx, `
		SELECT record FROM message_delivery_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR actor_id = $2)
		ORDER BY updated_at DESC, id ASC LIMIT $3
	`, strings.TrimSpace(ownerID), strings.TrimSpace(actorID), normalizeDeliveryRecordLimit(limit))
	if err != nil {
		return nil, classifyDeliveryRecordPostgresError(OperationMessageDeliveryList, ctx, err)
	}
	defer rows.Close()
	out := []app.MessageDeliveryRecord{}
	for rows.Next() {
		record, err := scanMessageDeliveryJSON(rows)
		if err != nil {
			return nil, classifyDeliveryRecordPostgresError(OperationMessageDeliveryList, ctx, err)
		}
		out = append(out, cloneMessageDelivery(record))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDeliveryRecordPostgresError(OperationMessageDeliveryList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveChannelInboxUpdate(ctx context.Context, update app.ChannelInboxUpdate) (app.ChannelInboxUpdate, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateSave, ctx); err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	candidate, err := prepareChannelInboxUpdate(update, app.ChannelInboxUpdate{}, time.Now().UTC())
	if err != nil {
		return app.ChannelInboxUpdate{}, storeError(ctx, OperationChannelInboxUpdateSave, StoreErrorInvalid, err)
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationChannelInboxUpdateSave, s.deliveryRecordPostgres)
	if err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	defer releasePostgresSession(session, release)
	if err := lockDeliveryRecordKeys(ctx, transaction, "inbox", candidate.ID, candidate.BindingID+"\x00"+candidate.ExternalID); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationChannelInboxUpdateSave, candidate, session, transaction, release, err)
	}
	byID, foundByID, err := queryChannelInboxUpdateTransaction(ctx, transaction, `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE id = $1 FOR UPDATE
	`, candidate.ID)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationChannelInboxUpdateSave, candidate, session, transaction, release, err)
	}
	byKey, foundByKey, err := queryChannelInboxUpdateTransaction(ctx, transaction, `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE binding_id = $1 AND external_id = $2 FOR UPDATE
	`, candidate.BindingID, candidate.ExternalID)
	if err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationChannelInboxUpdateSave, candidate, session, transaction, release, err)
	}
	if foundByID && foundByKey && byID.ID != byKey.ID {
		return app.ChannelInboxUpdate{}, deliveryRecordPostgresBusinessError(ctx, OperationChannelInboxUpdateSave, StoreErrorConflict, session, transaction, release, ErrChannelInboxUpdateConflict)
	}
	current := byKey
	if !foundByKey {
		current = byID
	}
	if current.ID != "" && current.ID != candidate.ID {
		if current.BindingID != candidate.BindingID || current.ExternalID != candidate.ExternalID || current.Channel != candidate.Channel {
			return app.ChannelInboxUpdate{}, deliveryRecordPostgresBusinessError(ctx, OperationChannelInboxUpdateSave, StoreErrorConflict, session, transaction, release, ErrChannelInboxUpdateConflict)
		}
		if err := rollbackPostgresTransaction(ctx, session, transaction, release, nil); err != nil {
			return app.ChannelInboxUpdate{}, classifyDeliveryRecordPostgresError(OperationChannelInboxUpdateSave, ctx, err)
		}
		return cloneChannelInboxUpdate(current), nil
	}
	candidate, err = prepareChannelInboxUpdate(candidate, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrChannelInboxUpdateConflict) {
			code = StoreErrorConflict
		}
		return app.ChannelInboxUpdate{}, deliveryRecordPostgresBusinessError(ctx, OperationChannelInboxUpdateSave, code, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO channel_inbox_updates (
			id, binding_id, channel, external_id, chat_key, payload, status, attempts,
			available_at, last_error, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO UPDATE SET
			chat_key = EXCLUDED.chat_key, payload = EXCLUDED.payload, status = EXCLUDED.status,
			attempts = EXCLUDED.attempts, available_at = EXCLUDED.available_at,
			last_error = EXCLUDED.last_error, updated_at = EXCLUDED.updated_at
	`, candidate.ID, candidate.BindingID, candidate.Channel, candidate.ExternalID, candidate.ChatKey,
		mustJSONRaw(candidate.Payload), candidate.Status, candidate.Attempts, candidate.AvailableAt,
		candidate.LastError, candidate.CreatedAt, candidate.UpdatedAt); err != nil {
		return finishDeliveryRecordPostgresStatement(ctx, OperationChannelInboxUpdateSave, candidate, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(ctx, OperationChannelInboxUpdateSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneChannelInboxUpdate(candidate), nil
}

func (s *PostgresStore) GetChannelInboxUpdate(ctx context.Context, id string) (app.ChannelInboxUpdate, bool, error) {
	return s.queryChannelInboxUpdate(ctx, OperationChannelInboxUpdateGet, `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE id = $1
	`, strings.TrimSpace(id))
}

func (s *PostgresStore) FindChannelInboxUpdate(ctx context.Context, bindingID, externalID string) (app.ChannelInboxUpdate, bool, error) {
	return s.queryChannelInboxUpdate(ctx, OperationChannelInboxUpdateFind, `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE binding_id = $1 AND external_id = $2
	`, strings.TrimSpace(bindingID), strings.TrimSpace(externalID))
}

func (s *PostgresStore) queryChannelInboxUpdate(ctx context.Context, operation StoreOperation, query string, args ...any) (app.ChannelInboxUpdate, bool, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	update, err := scanChannelInboxUpdate(s.deliveryRecordPostgres.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ChannelInboxUpdate{}, false, nil
	}
	if err != nil {
		return app.ChannelInboxUpdate{}, false, classifyDeliveryRecordPostgresError(operation, ctx, err)
	}
	return cloneChannelInboxUpdate(update), true, nil
}

func (s *PostgresStore) ListChannelInboxUpdates(ctx context.Context, channel, status string, readyBefore time.Time, limit int) ([]app.ChannelInboxUpdate, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateList, ctx); err != nil {
		return nil, err
	}
	query := `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates
		WHERE ($1 = '' OR channel = $1) AND ($2 = '' OR status = $2)`
	args := []any{strings.ToLower(strings.TrimSpace(channel)), strings.TrimSpace(status)}
	if !readyBefore.IsZero() {
		query += ` AND available_at <= $3 ORDER BY created_at ASC, id ASC LIMIT $4`
		args = append(args, postgresTime(readyBefore), normalizeDeliveryRecordLimit(limit))
	} else {
		query += ` ORDER BY created_at ASC, id ASC LIMIT $3`
		args = append(args, normalizeDeliveryRecordLimit(limit))
	}
	rows, err := s.deliveryRecordPostgres.Query(ctx, query, args...)
	if err != nil {
		return nil, classifyDeliveryRecordPostgresError(OperationChannelInboxUpdateList, ctx, err)
	}
	defer rows.Close()
	out := []app.ChannelInboxUpdate{}
	for rows.Next() {
		update, err := scanChannelInboxUpdate(rows)
		if err != nil {
			return nil, classifyDeliveryRecordPostgresError(OperationChannelInboxUpdateList, ctx, err)
		}
		out = append(out, cloneChannelInboxUpdate(update))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDeliveryRecordPostgresError(OperationChannelInboxUpdateList, ctx, err)
	}
	return out, nil
}

func scanMessageReceiveJSON(row onboardingPostgresRow) (app.MessageReceiveRecord, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return app.MessageReceiveRecord{}, err
	}
	var record app.MessageReceiveRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return app.MessageReceiveRecord{}, errors.Join(errMessageReceiveJSONDecode, err)
	}
	return cloneMessageReceive(record), nil
}

func queryMessageReceiveTransaction(ctx context.Context, transaction onboardingPostgresTx, query string, args ...any) (app.MessageReceiveRecord, bool, error) {
	record, err := scanMessageReceiveJSON(transaction.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MessageReceiveRecord{}, false, nil
	}
	return record, err == nil, err
}

func scanMessageDeliveryJSON(row onboardingPostgresRow) (app.MessageDeliveryRecord, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	var record app.MessageDeliveryRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return app.MessageDeliveryRecord{}, errors.Join(errMessageDeliveryJSONDecode, err)
	}
	return cloneMessageDelivery(record), nil
}

func queryMessageDeliveryTransaction(ctx context.Context, transaction onboardingPostgresTx, query string, args ...any) (app.MessageDeliveryRecord, bool, error) {
	record, err := scanMessageDeliveryJSON(transaction.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MessageDeliveryRecord{}, false, nil
	}
	return record, err == nil, err
}

func queryChannelInboxUpdateTransaction(ctx context.Context, transaction onboardingPostgresTx, query string, args ...any) (app.ChannelInboxUpdate, bool, error) {
	update, err := scanChannelInboxUpdate(transaction.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ChannelInboxUpdate{}, false, nil
	}
	return update, err == nil, err
}

func appendDeliveryRecordAudit(transaction onboardingPostgresTx, ctx context.Context, eventType, sessionID, runID, actor, summary string, fields map[string]any) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8)
	`, app.NewID("audit"), postgresTime(time.Now().UTC()), eventType, sessionID, runID, actor, summary, optionalJSON(fields))
	return err
}

func lockDeliveryRecordKeys(ctx context.Context, transaction onboardingPostgresTx, domain string, values ...string) error {
	keys := make([]int64, 0, len(values))
	seen := map[int64]struct{}{}
	for _, value := range values {
		digest := sha256.Sum256([]byte("sparkclaw/store/delivery-record/v1\x00" + domain + "\x00" + value))
		key := int64(binary.BigEndian.Uint64(digest[:8]))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
			return err
		}
	}
	return nil
}

func finishDeliveryRecordPostgresStatement[T any](ctx context.Context, operation StoreOperation, candidate T, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (T, error) {
	var zero T
	var postgresError *pgconn.PgError
	definite := errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) ||
		errors.Is(cause, errMessageReceiveJSONDecode) || errors.Is(cause, errMessageDeliveryJSONDecode) || errors.Is(cause, errChannelInboxPayloadDecode)
	if definite {
		cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
		return zero, classifyDeliveryRecordPostgresError(operation, ctx, cause)
	}
	*release = false
	return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func deliveryRecordPostgresBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(ctx, operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func classifyDeliveryRecordPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errMessageReceiveJSONDecode) || errors.Is(cause, errMessageDeliveryJSONDecode) || errors.Is(cause, errChannelInboxPayloadDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) {
		if postgresError.Code == "23505" {
			return storeError(ctx, operation, StoreErrorConflict, errors.Join(deliveryRecordConflictForOperation(operation), cause))
		}
		return storeError(ctx, operation, StoreErrorInternal, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func deliveryRecordConflictForOperation(operation StoreOperation) error {
	switch operation {
	case OperationMessageReceiveSave:
		return ErrMessageReceiveConflict
	case OperationMessageDeliverySave:
		return ErrMessageDeliveryConflict
	case OperationChannelInboxUpdateSave:
		return ErrChannelInboxUpdateConflict
	default:
		return errors.New("delivery record conflicts with persisted state")
	}
}
