package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func isWorkbenchReminder(reminder app.Reminder) bool {
	return reminder.ScheduleSpec != nil && reminder.ScheduleSpec.WorkbenchOwned
}

// prepareWorkbenchAdvance consumes exactly one occurrence. The next occurrence
// is committed before execution starts, so a crash cannot lose a recurring
// definition or put this occurrence back into a runnable queue.
func prepareWorkbenchAdvance(current app.Reminder, next time.Time, occurrence app.ReminderDelivery, now time.Time) (app.Reminder, app.ReminderDelivery, error) {
	if !isWorkbenchReminder(current) || current.Status != "pending" || strings.TrimSpace(occurrence.ID) == "" || occurrence.ReminderID != current.ID ||
		(occurrence.Status != "missed" && occurrence.Status != "submitted") || (!next.IsZero() && !next.After(current.DueTime)) {
		return app.Reminder{}, app.ReminderDelivery{}, errors.New("invalid workbench occurrence advancement")
	}
	current.Status = occurrence.Status
	if !next.IsZero() {
		current.Status, current.DueTime = "pending", next
	}
	current.LastDeliveryID = occurrence.ID
	current.UpdatedAt = nextRepositoryTime(now, current.UpdatedAt)
	current.DeliveryAttempt = 0
	return normalizeReminder(current), prepareReminderDelivery(occurrence, now), nil
}

func (s *MemoryStore) AdvanceWorkbenchSchedule(ctx context.Context, id string, expectedUpdatedAt, next time.Time, occurrence app.ReminderDelivery) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationWorkbenchScheduleAdvance, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationWorkbenchScheduleAdvance, ctx); err != nil {
		return app.Reminder{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationWorkbenchScheduleAdvance, ctx); err != nil {
		return app.Reminder{}, err
	}
	current, exists := s.reminders[id]
	_, duplicate := s.reminderDelivery[occurrence.ID]
	if !exists || current.Status != "pending" || !current.UpdatedAt.Equal(postgresTime(expectedUpdatedAt)) || duplicate {
		return app.Reminder{}, storeError(ctx, OperationWorkbenchScheduleAdvance, StoreErrorConflict, ErrReminderConflict)
	}
	updated, record, err := prepareWorkbenchAdvance(current, next, occurrence, time.Now().UTC())
	if err != nil {
		return app.Reminder{}, storeError(ctx, OperationWorkbenchScheduleAdvance, StoreErrorInvalid, err)
	}
	s.reminders[id], s.reminderDelivery[record.ID] = cloneReminder(updated), record
	s.appendAuditLocked("schedule_occurrence."+record.Status, updated.SessionID, updated.RunID, "workbench", record.ID, map[string]any{"schedule_id": id, "occurrence_id": record.ID, "due_time": current.DueTime})
	s.appendEventLocked("schedule_occurrence."+record.Status, updated.SessionID, updated.RunID, record)
	return cloneReminder(updated), nil
}

func (s *FileStore) AdvanceWorkbenchSchedule(ctx context.Context, id string, expectedUpdatedAt, next time.Time, occurrence app.ReminderDelivery) (app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationWorkbenchScheduleAdvance, fileAdmissionCapacity)
	if err != nil {
		return app.Reminder{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationWorkbenchScheduleAdvance, func(ctx context.Context) (app.Reminder, error) {
		return s.inner.AdvanceWorkbenchSchedule(ctx, id, expectedUpdatedAt, next, occurrence)
	})
}

func (s *PostgresStore) AdvanceWorkbenchSchedule(ctx context.Context, id string, expectedUpdatedAt, next time.Time, occurrence app.ReminderDelivery) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationWorkbenchScheduleAdvance, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationWorkbenchScheduleAdvance, ctx); err != nil {
		return app.Reminder{}, err
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationWorkbenchScheduleAdvance, s.schedulePostgres)
	if err != nil {
		return app.Reminder{}, err
	}
	defer releasePostgresSession(session, release)
	current, err := scanReminder(transaction.QueryRow(ctx, `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		FROM reminders WHERE id = $1 AND status = 'pending' AND updated_at = $2 FOR UPDATE
	`, id, postgresTime(expectedUpdatedAt)))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Reminder{}, schedulePostgresBusinessError(ctx, OperationWorkbenchScheduleAdvance, StoreErrorConflict, session, transaction, release, ErrReminderConflict)
	}
	if err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationWorkbenchScheduleAdvance, session, transaction, release, err)
	}
	updated, record, err := prepareWorkbenchAdvance(current, next, occurrence, time.Now().UTC())
	if err != nil {
		return app.Reminder{}, schedulePostgresBusinessError(ctx, OperationWorkbenchScheduleAdvance, StoreErrorInvalid, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `UPDATE reminders SET status = $2, due_time = $3, updated_at = $4, last_delivery_id = $5, delivery_attempt = 0 WHERE id = $1`,
		id, updated.Status, updated.DueTime, updated.UpdatedAt, record.ID); err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationWorkbenchScheduleAdvance, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO reminder_deliveries
		(id, reminder_id, channel, provider, recipient, status, provider_status, error, retry_state, attempt, sent_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		record.ID, record.ReminderID, record.Channel, record.Provider, record.Recipient, record.Status, record.ProviderStatus, record.Error, record.RetryState, record.Attempt, zeroTimeToNil(record.SentAt), record.CreatedAt); err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationWorkbenchScheduleAdvance, session, transaction, release, err)
	}
	if err := appendReminderDeliveryLifecycle(transaction, ctx, record); err != nil {
		return app.Reminder{}, finishSchedulePostgresStatement(ctx, OperationWorkbenchScheduleAdvance, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return updated, storeError(ctx, OperationWorkbenchScheduleAdvance, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneReminder(updated), nil
}
