package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SaveReminder(ctx context.Context, reminder app.Reminder) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderSave, ctx); err != nil {
		return app.Reminder{}, err
	}
	reminder = prepareReminder(reminder, time.Now().UTC())
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationReminderSave, s.schedulePostgres)
	if err != nil {
		return app.Reminder{}, err
	}
	defer releasePostgresSession(session, release)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO reminders (
			id, session_id, run_id, text, text_summary, due_time, timezone, channel, recipient,
			recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status,
			last_delivery_id, last_error, created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		)
		VALUES ($1, nullif($2, ''), nullif($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		ON CONFLICT (id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			run_id = EXCLUDED.run_id,
			text = EXCLUDED.text,
			text_summary = EXCLUDED.text_summary,
			due_time = EXCLUDED.due_time,
			timezone = EXCLUDED.timezone,
			channel = EXCLUDED.channel,
			recipient = EXCLUDED.recipient,
			recipient_binding = EXCLUDED.recipient_binding,
			binding_id = EXCLUDED.binding_id,
			credential_ref = EXCLUDED.credential_ref,
			base_url = EXCLUDED.base_url,
			recurrence = EXCLUDED.recurrence,
			dedupe_key = EXCLUDED.dedupe_key,
			status = EXCLUDED.status,
			last_delivery_id = EXCLUDED.last_delivery_id,
			last_error = EXCLUDED.last_error,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at,
			sent_at = EXCLUDED.sent_at,
			canceled_at = EXCLUDED.canceled_at,
			delivery_attempt = EXCLUDED.delivery_attempt,
			schedule_spec = EXCLUDED.schedule_spec
	`, reminder.ID, reminder.SessionID, reminder.RunID, reminder.Text, reminder.TextSummary, reminder.DueTime, reminder.Timezone, reminder.Channel, reminder.Recipient,
		reminder.RecipientBinding, reminder.BindingID, reminder.CredentialRef, reminder.BaseURL, reminder.Recurrence, reminder.DedupeKey, reminder.Status, reminder.LastDeliveryID, reminder.LastError, reminder.CreatedAt, reminder.UpdatedAt,
		reminder.SentAt, reminder.CanceledAt, reminder.DeliveryAttempt, mustJSON(reminder.ScheduleSpec)); err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationReminderSave, session, transaction, release, err)
	}
	if err := appendReminderLifecycle(transaction, ctx, reminder); err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationReminderSave, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return reminder, storeError(ctx, OperationReminderSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneReminder(reminder), nil
}

func (s *PostgresStore) UpdatePendingReminder(ctx context.Context, reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderUpdatePending, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderUpdatePending, ctx); err != nil {
		return app.Reminder{}, err
	}
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = time.Now().UTC()
	}
	reminder.UpdatedAt = nextRepositoryTime(reminder.UpdatedAt, postgresTime(expectedUpdatedAt))
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	reminder = normalizeReminder(reminder)
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationReminderUpdatePending, s.schedulePostgres)
	if err != nil {
		return app.Reminder{}, err
	}
	defer releasePostgresSession(session, release)
	row := transaction.QueryRow(ctx, `
		UPDATE reminders SET
			session_id = nullif($2, ''), run_id = nullif($3, ''), text = $4, text_summary = $5,
			due_time = $6, timezone = $7, channel = $8, recipient = $9,
			recipient_binding = $10, binding_id = $11, credential_ref = $12, base_url = $13,
			recurrence = $14, dedupe_key = $15, status = $16, last_delivery_id = $17,
			last_error = $18, updated_at = $19, sent_at = $20, canceled_at = $21,
			delivery_attempt = $22, schedule_spec = $23
		WHERE id = $1 AND status = 'pending' AND updated_at = $24
		RETURNING id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
	`, reminder.ID, reminder.SessionID, reminder.RunID, reminder.Text, reminder.TextSummary,
		reminder.DueTime, reminder.Timezone, reminder.Channel, reminder.Recipient,
		reminder.RecipientBinding, reminder.BindingID, reminder.CredentialRef, reminder.BaseURL,
		reminder.Recurrence, reminder.DedupeKey, reminder.Status, reminder.LastDeliveryID,
		reminder.LastError, reminder.UpdatedAt, reminder.SentAt, reminder.CanceledAt,
		reminder.DeliveryAttempt, mustJSON(reminder.ScheduleSpec), postgresTime(expectedUpdatedAt))
	updated, err := scanReminder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Reminder{}, schedulePostgresBusinessError(ctx, OperationReminderUpdatePending, StoreErrorConflict, session, transaction, release, ErrReminderConflict)
	}
	if err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationReminderUpdatePending, session, transaction, release, err)
	}
	updated = normalizeReminder(updated)
	if err := appendReminderLifecycle(transaction, ctx, updated); err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationReminderUpdatePending, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return updated, storeError(ctx, OperationReminderUpdatePending, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneReminder(updated), nil
}

func (s *PostgresStore) GetReminder(ctx context.Context, id string) (app.Reminder, bool, error) {
	ctx, cancel := operationContext(ctx, OperationReminderGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderGet, ctx); err != nil {
		return app.Reminder{}, false, err
	}
	row := s.schedulePostgres.QueryRow(ctx, `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		FROM reminders
		WHERE id = $1
	`, id)
	reminder, err := scanReminder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Reminder{}, false, nil
	}
	if err != nil {
		return app.Reminder{}, false, classifySchedulePostgresError(OperationReminderGet, ctx, err)
	}
	return cloneReminder(normalizeReminder(reminder)), true, nil
}

func (s *PostgresStore) ListReminders(ctx context.Context, filter app.ReminderFilter) ([]app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderList, ctx); err != nil {
		return nil, err
	}
	limit := normalizeReminderQueryLimit(filter.Limit)
	var from, to any
	if filter.From != nil {
		from = postgresTime(*filter.From)
	}
	if filter.To != nil {
		to = postgresTime(*filter.To)
	}
	rows, err := s.schedulePostgres.Query(ctx, `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		FROM reminders
		WHERE ($1 = '' OR status = $1)
			AND ($2::timestamptz IS NULL OR due_time >= $2::timestamptz)
			AND ($3::timestamptz IS NULL OR due_time <= $3::timestamptz)
		ORDER BY due_time ASC, id ASC
		LIMIT $4
	`, filter.Status, from, to, limit)
	if err != nil {
		return nil, classifySchedulePostgresError(OperationReminderList, ctx, err)
	}
	defer rows.Close()
	out := []app.Reminder{}
	for rows.Next() {
		reminder, err := scanReminder(rows)
		if err != nil {
			return nil, classifySchedulePostgresError(OperationReminderList, ctx, err)
		}
		out = append(out, cloneReminder(normalizeReminder(reminder)))
	}
	if err := rows.Err(); err != nil {
		return nil, classifySchedulePostgresError(OperationReminderList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) ClaimDueReminders(ctx context.Context, now, staleBefore time.Time, limit int) ([]app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderClaimDue, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderClaimDue, ctx); err != nil {
		return nil, err
	}
	limit = normalizeReminderQueryLimit(limit)
	now = postgresTime(now)
	staleBefore = postgresTime(staleBefore)
	rows, err := s.schedulePostgres.Query(ctx, `
		UPDATE reminders
		SET status = 'sending', updated_at = $1
		WHERE id IN (
			SELECT id FROM reminders
			WHERE (status = 'pending' AND due_time <= $1)
				OR (status = 'sending' AND updated_at <= $2)
			ORDER BY due_time ASC, id ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
	`, now, staleBefore, limit)
	if err != nil {
		return nil, classifySchedulePostgresError(OperationReminderClaimDue, ctx, err)
	}
	defer rows.Close()
	out := []app.Reminder{}
	for rows.Next() {
		reminder, err := scanReminder(rows)
		if err != nil {
			return nil, classifySchedulePostgresError(OperationReminderClaimDue, ctx, err)
		}
		out = append(out, cloneReminder(normalizeReminder(reminder)))
	}
	if err := rows.Err(); err != nil {
		return nil, classifySchedulePostgresError(OperationReminderClaimDue, ctx, err)
	}
	sortReminders(out)
	return out, nil
}

func (s *PostgresStore) SaveReminderDelivery(ctx context.Context, delivery app.ReminderDelivery) (app.ReminderDelivery, error) {
	ctx, cancel := operationContext(ctx, OperationReminderDeliverySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderDeliverySave, ctx); err != nil {
		return app.ReminderDelivery{}, err
	}
	now := postgresTime(time.Now().UTC())
	delivery = prepareReminderDelivery(delivery, now)
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationReminderDeliverySave, s.schedulePostgres)
	if err != nil {
		return app.ReminderDelivery{}, err
	}
	defer releasePostgresSession(session, release)
	result, err := transaction.Exec(ctx, `
		UPDATE reminders
		SET last_delivery_id = $1,
			last_error = $2,
			status = CASE WHEN $3 = 'sent' THEN 'sent' WHEN $3 = 'failed' THEN 'failed' ELSE status END,
			sent_at = CASE WHEN $3 = 'sent' THEN $4 ELSE sent_at END,
			delivery_attempt = $5,
			updated_at = GREATEST($6, updated_at + interval '1 microsecond')
		WHERE id = $7
	`, delivery.ID, delivery.Error, delivery.Status, zeroTimeToNil(delivery.SentAt), delivery.Attempt, now, delivery.ReminderID)
	if err != nil {
		return app.ReminderDelivery{}, finishSchedulePostgresStatement(ctx, OperationReminderDeliverySave, session, transaction, release, err)
	}
	if result.RowsAffected() != 1 {
		return app.ReminderDelivery{}, schedulePostgresBusinessError(ctx, OperationReminderDeliverySave, StoreErrorNotFound, session, transaction, release, errors.New("reminder not found"))
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO reminder_deliveries (
			id, reminder_id, channel, provider, recipient, status, provider_status, error,
			retry_state, attempt, sent_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			reminder_id = EXCLUDED.reminder_id,
			channel = EXCLUDED.channel,
			provider = EXCLUDED.provider,
			recipient = EXCLUDED.recipient,
			status = EXCLUDED.status,
			provider_status = EXCLUDED.provider_status,
			error = EXCLUDED.error,
			retry_state = EXCLUDED.retry_state,
			attempt = EXCLUDED.attempt,
			sent_at = EXCLUDED.sent_at,
			created_at = EXCLUDED.created_at
	`, delivery.ID, delivery.ReminderID, delivery.Channel, delivery.Provider, delivery.Recipient, delivery.Status, delivery.ProviderStatus, delivery.Error,
		delivery.RetryState, delivery.Attempt, zeroTimeToNil(delivery.SentAt), delivery.CreatedAt); err != nil {
		return app.ReminderDelivery{}, finishSchedulePostgresStatement(ctx, OperationReminderDeliverySave, session, transaction, release, err)
	}
	if err := appendReminderDeliveryLifecycle(transaction, ctx, delivery); err != nil {
		return app.ReminderDelivery{}, finishSchedulePostgresStatement(ctx, OperationReminderDeliverySave, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return delivery, storeError(ctx, OperationReminderDeliverySave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return delivery, nil
}

func (s *PostgresStore) ListReminderDeliveries(ctx context.Context, reminderID string) ([]app.ReminderDelivery, error) {
	ctx, cancel := operationContext(ctx, OperationReminderDeliveryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderDeliveryList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.schedulePostgres.Query(ctx, `
		SELECT id, reminder_id, channel, provider, recipient, status, provider_status, error,
			retry_state, attempt, sent_at, created_at
		FROM reminder_deliveries
		WHERE $1 = '' OR reminder_id = $1
		ORDER BY created_at ASC, id ASC
	`, reminderID)
	if err != nil {
		return nil, classifySchedulePostgresError(OperationReminderDeliveryList, ctx, err)
	}
	defer rows.Close()
	out := []app.ReminderDelivery{}
	for rows.Next() {
		delivery, err := scanReminderDelivery(rows)
		if err != nil {
			return nil, classifySchedulePostgresError(OperationReminderDeliveryList, ctx, err)
		}
		out = append(out, normalizeReminderDelivery(delivery))
	}
	if err := rows.Err(); err != nil {
		return nil, classifySchedulePostgresError(OperationReminderDeliveryList, ctx, err)
	}
	return out, nil
}

func appendReminderLifecycle(transaction onboardingPostgresTx, ctx context.Context, reminder app.Reminder) error {
	eventType := "reminder." + reminder.Status
	at := postgresTime(time.Now().UTC())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), at, eventType, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, optionalJSON(map[string]any{
		"reminder_id": reminder.ID, "due_time": reminder.DueTime.Format(time.RFC3339), "channel": reminder.Channel,
	})); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), at, eventType, reminder.SessionID, reminder.RunID, mustJSON(reminder))
	return err
}

func appendReminderDeliveryLifecycle(transaction onboardingPostgresTx, ctx context.Context, delivery app.ReminderDelivery) error {
	eventType := "reminder_delivery." + delivery.Status
	at := postgresTime(time.Now().UTC())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), at, eventType, "", delivery.ReminderID, "scheduler", delivery.ProviderStatus, optionalJSON(map[string]any{
		"delivery_id": delivery.ID, "reminder_id": delivery.ReminderID, "channel": delivery.Channel,
		"provider": delivery.Provider, "attempt": delivery.Attempt,
	})); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), at, eventType, "", delivery.ReminderID, mustJSON(delivery))
	return err
}

func finishSchedulePostgresStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifySchedulePostgresError(operation, ctx, cause)
}

func schedulePostgresBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(ctx, operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func classifySchedulePostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errReminderScheduleSpecJSONDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}
