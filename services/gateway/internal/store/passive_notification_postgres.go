package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreatePassiveNotification(ctx context.Context, notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationCreate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationCreate, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	var err error
	notification, err = preparePassiveNotification(notification, time.Now().UTC())
	if err != nil {
		return app.PassiveNotification{}, false, storeError(ctx, OperationPassiveNotificationCreate, StoreErrorInvalid, err)
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationPassiveNotificationCreate, s.passiveNotificationPostgres)
	if err != nil {
		return app.PassiveNotification{}, false, err
	}
	defer releasePostgresSession(session, release)
	row := transaction.QueryRow(ctx, `
		INSERT INTO passive_notifications (
			id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING
		RETURNING id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		          source, kind, deep_link, occurred_at, read_at, created_at, updated_at
	`, notification.ID, notification.OwnerID, notification.EndpointID, notification.IdempotencyKey,
		notification.Fingerprint, notification.NotificationID, notification.Source, notification.Kind,
		notification.DeepLink, notification.OccurredAt, notification.ReadAt, notification.CreatedAt, notification.UpdatedAt)
	inserted, err := scanPassiveNotification(row)
	if err == nil {
		inserted = normalizePassiveNotification(inserted)
		if err := appendPassiveNotificationAudit(transaction, ctx, "notification.received", notification.OwnerID, notification.Source, map[string]any{
			"notification_id": notification.ID, "endpoint_id": notification.EndpointID, "kind": notification.Kind,
		}); err != nil {
			return app.PassiveNotification{}, false, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationCreate, session, transaction, release, err)
		}
		if err := transaction.Commit(ctx); err != nil {
			*release = false
			return clonePassiveNotification(inserted), true, storeError(ctx, OperationPassiveNotificationCreate, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
		}
		s.bumpPassiveNotificationRev(notification.OwnerID)
		return clonePassiveNotification(inserted), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return app.PassiveNotification{}, false, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationCreate, session, transaction, release, err)
	}
	existingRow := transaction.QueryRow(ctx, `
		SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		FROM passive_notifications WHERE endpoint_id = $1 AND idempotency_key = $2
	`, notification.EndpointID, notification.IdempotencyKey)
	existing, err := scanPassiveNotification(existingRow)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err = scanPassiveNotification(transaction.QueryRow(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications WHERE id = $1
		`, notification.ID))
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.PassiveNotification{}, false, passiveNotificationPostgresBusinessError(ctx, OperationPassiveNotificationCreate, StoreErrorConflict, session, transaction, release, ErrPassiveNotificationConflict)
		}
		return app.PassiveNotification{}, false, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationCreate, session, transaction, release, err)
	}
	if !passiveNotificationsEqualForReplay(existing, notification) {
		return app.PassiveNotification{}, false, passiveNotificationPostgresBusinessError(ctx, OperationPassiveNotificationCreate, StoreErrorConflict, session, transaction, release, ErrPassiveNotificationConflict)
	}
	if rollbackErr := rollbackPostgresTransaction(ctx, session, transaction, release, nil); rollbackErr != nil {
		return app.PassiveNotification{}, false, classifyPassiveNotificationPostgresError(OperationPassiveNotificationCreate, ctx, rollbackErr)
	}
	return clonePassiveNotification(normalizePassiveNotification(existing)), false, nil
}

func (s *PostgresStore) GetPassiveNotification(ctx context.Context, ownerID, id string) (app.PassiveNotification, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationGet, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	row := s.passiveNotificationPostgres.QueryRow(ctx, `
		SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		FROM passive_notifications WHERE owner_id = $1 AND id = $2
	`, ownerID, id)
	notification, err := scanPassiveNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.PassiveNotification{}, false, nil
	}
	if err != nil {
		return app.PassiveNotification{}, false, classifyPassiveNotificationPostgresError(OperationPassiveNotificationGet, ctx, err)
	}
	return clonePassiveNotification(normalizePassiveNotification(notification)), true, nil
}

func (s *PostgresStore) ListPassiveNotifications(ctx context.Context, ownerID, after string, limit int) ([]app.PassiveNotification, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationList, ctx); err != nil {
		return nil, err
	}
	limit = normalizePassiveNotificationLimit(limit)
	var rows onboardingPostgresRows
	var err error
	if after == "" {
		rows, err = s.passiveNotificationPostgres.Query(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications WHERE owner_id = $1
			ORDER BY created_at DESC, id DESC LIMIT $2
		`, ownerID, limit)
	} else {
		cursor, cursorErr := scanPassiveNotification(s.passiveNotificationPostgres.QueryRow(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications WHERE owner_id = $1 AND id = $2
		`, ownerID, after))
		if errors.Is(cursorErr, pgx.ErrNoRows) {
			return []app.PassiveNotification{}, nil
		}
		if cursorErr != nil {
			return nil, classifyPassiveNotificationPostgresError(OperationPassiveNotificationList, ctx, cursorErr)
		}
		rows, err = s.passiveNotificationPostgres.Query(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications
			WHERE owner_id = $1 AND (created_at > $2 OR (created_at = $2 AND id > $3))
			ORDER BY created_at ASC, id ASC LIMIT $4
		`, ownerID, cursor.CreatedAt, cursor.ID, limit)
	}
	if err != nil {
		return nil, classifyPassiveNotificationPostgresError(OperationPassiveNotificationList, ctx, err)
	}
	defer rows.Close()
	out := []app.PassiveNotification{}
	for rows.Next() {
		notification, err := scanPassiveNotification(rows)
		if err != nil {
			return nil, classifyPassiveNotificationPostgresError(OperationPassiveNotificationList, ctx, err)
		}
		out = append(out, clonePassiveNotification(normalizePassiveNotification(notification)))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPassiveNotificationPostgresError(OperationPassiveNotificationList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) CountUnreadPassiveNotifications(ctx context.Context, ownerID string) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationCount, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationCount, ctx); err != nil {
		return 0, err
	}
	var count int
	if err := s.passiveNotificationPostgres.QueryRow(ctx, `
		SELECT COUNT(*) FROM passive_notifications WHERE owner_id = $1 AND read_at IS NULL
	`, ownerID).Scan(&count); err != nil {
		return 0, classifyPassiveNotificationPostgresError(OperationPassiveNotificationCount, ctx, err)
	}
	return count, nil
}

func (s *PostgresStore) MarkPassiveNotificationRead(ctx context.Context, ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationMarkRead, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationMarkRead, ctx); err != nil {
		return app.PassiveNotification{}, err
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	}
	readAt = postgresTime(readAt)
	row := s.passiveNotificationPostgres.QueryRow(ctx, `
		WITH current AS (
			SELECT id, read_at IS NULL AS changed
			FROM passive_notifications WHERE owner_id = $1 AND id = $2
			FOR UPDATE
		), updated AS (
			UPDATE passive_notifications AS notification
			SET read_at = COALESCE(notification.read_at, $3),
				updated_at = CASE WHEN notification.read_at IS NULL THEN $3 ELSE notification.updated_at END
			FROM current WHERE notification.id = current.id
			RETURNING notification.id, notification.owner_id, notification.endpoint_id,
				notification.idempotency_key, notification.fingerprint, notification.notification_id,
				notification.source, notification.kind, notification.deep_link, notification.occurred_at,
				notification.read_at, notification.created_at, notification.updated_at
		)
		SELECT updated.*, current.changed FROM updated JOIN current ON updated.id = current.id
	`, ownerID, id, readAt)
	notification, changed, err := scanPassiveNotificationReadResult(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.PassiveNotification{}, storeError(ctx, OperationPassiveNotificationMarkRead, StoreErrorNotFound, ErrPassiveNotificationNotFound)
	}
	if err != nil {
		return app.PassiveNotification{}, classifyPassiveNotificationPostgresError(OperationPassiveNotificationMarkRead, ctx, err)
	}
	if changed {
		s.bumpPassiveNotificationRev(ownerID)
	}
	return clonePassiveNotification(normalizePassiveNotification(notification)), nil
}

func (s *PostgresStore) MarkAllPassiveNotificationsRead(ctx context.Context, ownerID string, readAt time.Time) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationMarkAll, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationMarkAll, ctx); err != nil {
		return 0, err
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	}
	readAt = postgresTime(readAt)
	result, err := s.passiveNotificationPostgres.Exec(ctx, `
		UPDATE passive_notifications SET read_at = $2, updated_at = $2
		WHERE owner_id = $1 AND read_at IS NULL
	`, ownerID, readAt)
	if err != nil {
		return 0, classifyPassiveNotificationPostgresError(OperationPassiveNotificationMarkAll, ctx, err)
	}
	if result.RowsAffected() > 0 {
		s.bumpPassiveNotificationRev(ownerID)
	}
	return int(result.RowsAffected()), nil
}

func (s *PostgresStore) PrunePassiveNotifications(ctx context.Context, cutoff time.Time, maxPerOwner int) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationPrune, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationPrune, ctx); err != nil {
		return 0, err
	}
	cutoff = postgresTime(cutoff)
	if cutoff.IsZero() && maxPerOwner <= 0 {
		return 0, nil
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationPassiveNotificationPrune, s.passiveNotificationPostgres)
	if err != nil {
		return 0, err
	}
	defer releasePostgresSession(session, release)
	removedByOwner := map[string]int{}
	if !cutoff.IsZero() {
		rows, err := transaction.Query(ctx, `
			DELETE FROM passive_notifications WHERE created_at < $1 RETURNING owner_id
		`, cutoff)
		if err != nil {
			return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
		}
		for rows.Next() {
			var ownerID string
			if err := rows.Scan(&ownerID); err != nil {
				rows.Close()
				return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
			}
			removedByOwner[ownerID]++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
		}
		rows.Close()
	}
	if maxPerOwner > 0 {
		type ownerExcess struct {
			ownerID string
			excess  int
		}
		var over []ownerExcess
		ownerRows, err := transaction.Query(ctx, `
			SELECT owner_id, COUNT(*) FROM passive_notifications
			GROUP BY owner_id HAVING COUNT(*) > $1
		`, maxPerOwner)
		if err != nil {
			return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
		}
		for ownerRows.Next() {
			var ownerID string
			var count int
			if err := ownerRows.Scan(&ownerID, &count); err != nil {
				ownerRows.Close()
				return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
			}
			over = append(over, ownerExcess{ownerID: ownerID, excess: count - maxPerOwner})
		}
		if err := ownerRows.Err(); err != nil {
			ownerRows.Close()
			return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
		}
		ownerRows.Close()
		for _, entry := range over {
			// Evict read notifications oldest-first before unread ones so an
			// over-cap inbox keeps the newest unread records.
			result, err := transaction.Exec(ctx, `
				DELETE FROM passive_notifications WHERE id IN (
					SELECT id FROM passive_notifications WHERE owner_id = $1
					ORDER BY (read_at IS NOT NULL) DESC, created_at ASC, id ASC
					LIMIT $2
				)
			`, entry.ownerID, entry.excess)
			if err != nil {
				return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
			}
			if result.RowsAffected() > 0 {
				removedByOwner[entry.ownerID] += int(result.RowsAffected())
			}
		}
	}
	removed := 0
	for ownerID, count := range removedByOwner {
		removed += count
		if err := appendPassiveNotificationAudit(transaction, ctx, "notification.pruned", "notification-retention", ownerID, map[string]any{
			"removed":       count,
			"max_per_owner": maxPerOwner,
			"cutoff":        cutoff.UTC().Format(time.RFC3339),
		}); err != nil {
			return 0, finishPassiveNotificationPostgresStatement(ctx, OperationPassiveNotificationPrune, session, transaction, release, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return removed, storeError(ctx, OperationPassiveNotificationPrune, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	for ownerID := range removedByOwner {
		s.bumpPassiveNotificationRev(ownerID)
	}
	return removed, nil
}

func (s *PostgresStore) PassiveNotificationRevision(ctx context.Context, ownerID string) (uint64, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationRevision, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationRevision, ctx); err != nil {
		return 0, err
	}
	s.passiveRevMu.Lock()
	defer s.passiveRevMu.Unlock()
	if err := operationContextError(OperationPassiveNotificationRevision, ctx); err != nil {
		return 0, err
	}
	return s.passiveNotificationRevs[ownerID], nil
}

func (s *PostgresStore) bumpPassiveNotificationRev(ownerID string) {
	s.passiveRevMu.Lock()
	defer s.passiveRevMu.Unlock()
	s.passiveNotificationRevs[ownerID]++
}

func appendPassiveNotificationAudit(transaction onboardingPostgresTx, ctx context.Context, eventType, actor, summary string, fields map[string]any) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, NULL, NULL, $4, $5, $6)
	`, app.NewID("audit"), postgresTime(time.Now().UTC()), eventType, actor, summary, optionalJSON(fields))
	return err
}

func finishPassiveNotificationPostgresStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyPassiveNotificationPostgresError(operation, ctx, cause)
}

func passiveNotificationPostgresBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(ctx, operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func classifyPassiveNotificationPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	return classifyPostgresReadError(operation, ctx, cause)
}
