package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestScheduleRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend
			var restart func() testBackend
			switch backend {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				restart = func() testBackend {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			exerciseScheduleRepositoryContract(t, repository, restart)
		})
	}
}

func TestPostgresScheduleRepositoryConfiguredContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	exerciseScheduleRepositoryContract(t, repository, nil)
}

func exerciseScheduleRepositoryContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	base := time.Date(2026, 8, 21, 14, 30, 0, 123456789, time.FixedZone("contract", 8*60*60))
	firstInput := scheduleContractReminder("schedule-a", "pending", base.Add(time.Hour), base)
	first := mustSaveReminder(t, repository, firstInput)
	if first.CreatedAt.Location() != time.UTC || first.CreatedAt.Nanosecond() != 123456000 ||
		first.DueTime.Location() != time.UTC || first.DueTime.Nanosecond() != 123456000 {
		t.Fatalf("normalized reminder = %#v", first)
	}
	firstInput.ScheduleSpec.Authorization.Scope[0] = "mutated-input"
	firstInput.ScheduleSpec.Payload.Content.Parts[1].Resource.Attributes["source"] = "mutated-input"
	first.ScheduleSpec.Authorization.Scope[0] = "mutated-output"
	first.ScheduleSpec.Payload.Content.Parts[1].Resource.Attributes["source"] = "mutated-output"
	isolated, found := mustGetReminder(t, repository, first.ID)
	if !found || isolated.ScheduleSpec.Authorization.Scope[0] != "schedule:write" ||
		isolated.ScheduleSpec.Payload.Content.Parts[1].Resource.Attributes["source"] != "workspace" {
		t.Fatalf("nested ScheduleSpec alias escaped: %#v found=%t", isolated, found)
	}

	replacement := scheduleContractReminder(first.ID, "pending", base.Add(4*time.Hour), base.Add(10*time.Minute))
	replacement.SessionID = "replacement-session"
	replacement.RunID = "replacement-run"
	replacement.Text = "replacement"
	replacement.TextSummary = "replacement summary"
	replacement.Channel = "telegram"
	replacement.Recipient = "replacement-recipient"
	replacement.DeliveryAttempt = 4
	replaced := mustSaveReminder(t, repository, replacement)
	if replaced.SessionID != replacement.SessionID || replaced.RunID != replacement.RunID ||
		replaced.Text != replacement.Text || replaced.CreatedAt != postgresTime(replacement.CreatedAt) ||
		replaced.DeliveryAttempt != replacement.DeliveryAttempt {
		t.Fatalf("duplicate ID did not fully replace the reminder: %#v", replaced)
	}

	second := mustSaveReminder(t, repository, scheduleContractReminder("schedule-b", "pending", base.Add(2*time.Hour), base))
	mustSaveReminder(t, repository, scheduleContractReminder("schedule-sent", "sent", base.Add(3*time.Hour), base))
	from := base.Add(90 * time.Minute)
	to := base.Add(5 * time.Hour)
	filtered := mustListReminders(t, repository, app.ReminderFilter{Status: "pending", From: &from, To: &to, Limit: 2})
	if len(filtered) != 2 || filtered[0].ID != second.ID || filtered[1].ID != replaced.ID {
		t.Fatalf("schedule filter/order/limit = %#v", filtered)
	}
	if missing, ok := mustGetReminder(t, repository, "missing-schedule"); ok || missing.ID != "" {
		t.Fatalf("missing reminder = %#v found=%t", missing, ok)
	}
	if empty := mustListReminderDeliveries(t, repository, "missing-schedule"); empty == nil || len(empty) != 0 {
		t.Fatalf("missing reminder deliveries = %#v", empty)
	}

	updatedCandidate := second
	updatedCandidate.Text = "updated by CAS"
	updated, err := repository.UpdatePendingReminder(t.Context(), updatedCandidate, second.UpdatedAt)
	if err != nil || updated.Text != updatedCandidate.Text || !updated.UpdatedAt.After(second.UpdatedAt) {
		t.Fatalf("CAS update = %#v err=%v", updated, err)
	}
	if _, err := repository.UpdatePendingReminder(t.Context(), updatedCandidate, second.UpdatedAt); !errors.Is(err, ErrReminderConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("stale CAS error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	replaced.Status = "sent"
	mustSaveReminder(t, repository, replaced)
	updated.Status = "sent"
	mustSaveReminder(t, repository, updated)

	claimNow := postgresTime(base.Add(8 * time.Hour))
	staleBefore := claimNow.Add(-time.Hour)
	due := mustSaveReminder(t, repository, scheduleContractReminder("schedule-due", "pending", claimNow.Add(-time.Minute), base))
	stale := scheduleContractReminder("schedule-stale", "sending", claimNow.Add(-2*time.Minute), base)
	stale.UpdatedAt = staleBefore.Add(-time.Minute)
	stale = mustSaveReminder(t, repository, stale)
	fresh := scheduleContractReminder("schedule-fresh", "sending", claimNow.Add(-3*time.Minute), base)
	fresh.UpdatedAt = staleBefore.Add(time.Minute)
	mustSaveReminder(t, repository, fresh)
	mustSaveReminder(t, repository, scheduleContractReminder("schedule-future", "pending", claimNow.Add(time.Minute), base))
	claimed := mustClaimDueReminders(t, repository, claimNow, staleBefore, 2)
	if len(claimed) != 2 || claimed[0].ID != stale.ID || claimed[1].ID != due.ID ||
		claimed[0].Status != "sending" || claimed[0].UpdatedAt != claimNow || claimed[1].UpdatedAt != claimNow {
		t.Fatalf("due/stale claim = %#v", claimed)
	}

	delivery := mustSaveReminderDelivery(t, repository, app.ReminderDelivery{
		ID: "schedule-delivery", ReminderID: due.ID, Channel: "web", Provider: "local-web",
		Status: "sent", ProviderStatus: "accepted", Attempt: 2,
		CreatedAt: base.Add(9*time.Hour + 987*time.Nanosecond),
	})
	if delivery.SentAt.IsZero() || delivery.SentAt.Location() != time.UTC || delivery.CreatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("normalized delivery = %#v", delivery)
	}
	deliveredReminder, found := mustGetReminder(t, repository, due.ID)
	if !found || deliveredReminder.Status != "sent" || deliveredReminder.LastDeliveryID != delivery.ID ||
		deliveredReminder.DeliveryAttempt != delivery.Attempt || deliveredReminder.SentAt == nil || !deliveredReminder.SentAt.Equal(delivery.SentAt) {
		t.Fatalf("delivery did not update reminder = %#v found=%t", deliveredReminder, found)
	}
	if deliveries := mustListReminderDeliveries(t, repository, due.ID); len(deliveries) != 1 || deliveries[0].ID != delivery.ID {
		t.Fatalf("delivery list = %#v", deliveries)
	}
	failedReminder := mustSaveReminder(t, repository, scheduleContractReminder("schedule-failed", "sending", claimNow, base))
	failedDelivery := mustSaveReminderDelivery(t, repository, app.ReminderDelivery{
		ID: "schedule-failed-delivery", ReminderID: failedReminder.ID, Status: "failed", Error: "provider rejected", Attempt: 1,
	})
	if !failedDelivery.SentAt.IsZero() {
		t.Fatalf("failed delivery acquired sent time: %#v", failedDelivery)
	}
	if failedDeliveries := mustListReminderDeliveries(t, repository, failedReminder.ID); len(failedDeliveries) != 1 || !failedDeliveries[0].SentAt.IsZero() {
		t.Fatalf("failed delivery list = %#v", failedDeliveries)
	}
	if _, err := repository.SaveReminderDelivery(t.Context(), app.ReminderDelivery{ID: "orphan-delivery", ReminderID: "missing", Status: "failed"}); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("orphan delivery error = %v code=%q", err, StoreErrorCodeOf(err))
	}

	auditTypes := map[string]bool{}
	for _, event := range mustListAudit(t, repository, "") {
		auditTypes[event.Type] = true
	}
	for _, required := range []string{"reminder.pending", "reminder_delivery.sent"} {
		if !auditTypes[required] {
			t.Fatalf("missing lifecycle audit %q in %#v", required, auditTypes)
		}
	}

	assertCanceledScheduleOperations(t, repository, replaced)
	if restart != nil {
		reloaded := restart()
		persisted, ok := mustGetReminder(t, reloaded, due.ID)
		if !ok || persisted.Status != "sent" || persisted.SentAt == nil || !persisted.SentAt.Equal(delivery.SentAt) {
			t.Fatalf("restarted reminder = %#v found=%t", persisted, ok)
		}
		persistedDeliveries := mustListReminderDeliveries(t, reloaded, due.ID)
		if len(persistedDeliveries) != 1 || persistedDeliveries[0].CreatedAt != delivery.CreatedAt || persistedDeliveries[0].SentAt != delivery.SentAt {
			t.Fatalf("restarted deliveries = %#v", persistedDeliveries)
		}
	}
}

func scheduleContractReminder(id, status string, due, created time.Time) app.Reminder {
	spec := app.ScheduleSpec{
		SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: "owner-schedule", ActorID: "actor-schedule",
		Payload: app.SchedulePayload{Content: app.MessageContent{Parts: []app.MessagePart{
			{ID: "text", Kind: app.MessagePartText, Text: "scheduled content"},
			{ID: "resource", Kind: app.MessagePartFile, Resource: &app.ResourceRef{Kind: "document", Ref: "doc-1", Attributes: map[string]string{"source": "workspace"}}},
		}}},
		ReturnRoute:   app.ReturnRoute{Mode: app.ReturnNowhere},
		Authorization: app.MessageAuthorization{PrincipalID: "owner-schedule", Scope: []string{"schedule:write"}},
	}
	return app.Reminder{
		ID: id, Text: "scheduled content", DueTime: due, Timezone: "Asia/Shanghai", Status: status,
		CreatedAt: created, UpdatedAt: created, ScheduleSpec: &spec,
	}
}

func assertCanceledScheduleOperations(t *testing.T, repository ScheduleRepository, reminder app.Reminder) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks := []struct {
		name string
		call func() error
	}{
		{name: "save", call: func() error { _, err := repository.SaveReminder(ctx, reminder); return err }},
		{name: "update", call: func() error {
			_, err := repository.UpdatePendingReminder(ctx, reminder, reminder.UpdatedAt)
			return err
		}},
		{name: "get", call: func() error { _, _, err := repository.GetReminder(ctx, reminder.ID); return err }},
		{name: "list", call: func() error { _, err := repository.ListReminders(ctx, app.ReminderFilter{}); return err }},
		{name: "claim", call: func() error { _, err := repository.ClaimDueReminders(ctx, time.Now(), time.Now(), 1); return err }},
		{name: "save delivery", call: func() error {
			_, err := repository.SaveReminderDelivery(ctx, app.ReminderDelivery{ReminderID: reminder.ID})
			return err
		}},
		{name: "list deliveries", call: func() error { _, err := repository.ListReminderDeliveries(ctx, reminder.ID); return err }},
	}
	for _, check := range checks {
		t.Run("canceled "+check.name, func(t *testing.T) {
			if err := check.call(); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("error = %v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}
}

func TestFileScheduleRepositoryDefiniteFailuresRestoreAggregate(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *FileStore) func() error
	}{
		{name: "save", setup: func(t *testing.T, repository *FileStore) func() error {
			return func() error {
				_, err := repository.SaveReminder(t.Context(), scheduleContractReminder("file-save", "pending", time.Now().Add(time.Hour), time.Now()))
				return err
			}
		}},
		{name: "update", setup: func(t *testing.T, repository *FileStore) func() error {
			stored := mustSaveReminder(t, repository, scheduleContractReminder("file-update", "pending", time.Now().Add(time.Hour), time.Now()))
			stored.Text = "updated"
			return func() error {
				_, err := repository.UpdatePendingReminder(t.Context(), stored, stored.UpdatedAt)
				return err
			}
		}},
		{name: "delivery", setup: func(t *testing.T, repository *FileStore) func() error {
			stored := mustSaveReminder(t, repository, scheduleContractReminder("file-delivery", "sending", time.Now().Add(-time.Hour), time.Now()))
			return func() error {
				_, err := repository.SaveReminderDelivery(t.Context(), app.ReminderDelivery{ID: "file-delivery-result", ReminderID: stored.ID, Status: "sent"})
				return err
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			command := test.setup(t, repository)
			before := repository.captureFileRollback()
			repository.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
			if err := command(); StoreErrorCodeOf(err) != StoreErrorDurability || !errorsIsFileCommitInjected(err) {
				t.Fatalf("error=%v code=%q", err, StoreErrorCodeOf(err))
			}
			if after := repository.captureFileRollback(); !reflect.DeepEqual(after, before) {
				t.Fatal("failed schedule command retained record, delivery, audit, or event state")
			}
		})
	}
}

type fakeSchedulePostgresOps struct {
	*fakeConnectorPostgresOps
	row onboardingPostgresRow
}

func (o *fakeSchedulePostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	if o.row == nil {
		return fakeConnectorPostgresRow{}
	}
	return o.row
}

func newFakeSchedulePostgresStore(transaction *fakeConnectorPostgresTx) (*PostgresStore, *fakeConnectorPostgresOps, *fakeConnectorPostgresSession) {
	session := &fakeConnectorPostgresSession{transaction: transaction}
	operations := &fakeConnectorPostgresOps{session: session}
	return &PostgresStore{operationTimeouts: defaultOperationTimeouts, schedulePostgres: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: operations}}, operations, session
}

func reminderPostgresRow(reminder app.Reminder, rawSpec []byte) fakeConnectorPostgresRow {
	if rawSpec == nil && reminder.ScheduleSpec != nil {
		rawSpec, _ = json.Marshal(reminder.ScheduleSpec)
	}
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		stringsOut := []string{reminder.ID, reminder.SessionID, reminder.RunID, reminder.Text, reminder.TextSummary,
			reminder.Timezone, reminder.Channel, reminder.Recipient, reminder.RecipientBinding, reminder.BindingID,
			reminder.CredentialRef, reminder.BaseURL, reminder.Recurrence, reminder.DedupeKey, reminder.Status,
			reminder.LastDeliveryID, reminder.LastError}
		*(destinations[0].(*string)) = stringsOut[0]
		*(destinations[1].(*string)) = stringsOut[1]
		*(destinations[2].(*string)) = stringsOut[2]
		*(destinations[3].(*string)) = stringsOut[3]
		*(destinations[4].(*string)) = stringsOut[4]
		*(destinations[5].(*time.Time)) = reminder.DueTime
		for index, value := range stringsOut[5:] {
			*(destinations[index+6].(*string)) = value
		}
		*(destinations[18].(*time.Time)) = reminder.CreatedAt
		*(destinations[19].(*time.Time)) = reminder.UpdatedAt
		*(destinations[20].(**time.Time)) = cloneTimePointer(reminder.SentAt)
		*(destinations[21].(**time.Time)) = cloneTimePointer(reminder.CanceledAt)
		*(destinations[22].(*int)) = reminder.DeliveryAttempt
		*(destinations[23].(*[]byte)) = append([]byte(nil), rawSpec...)
		return nil
	}}
}

func reminderDeliveryPostgresRow(delivery app.ReminderDelivery) fakeConnectorPostgresRow {
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		values := []string{delivery.ID, delivery.ReminderID, delivery.Channel, delivery.Provider, delivery.Recipient,
			delivery.Status, delivery.ProviderStatus, delivery.Error, delivery.RetryState}
		for index, value := range values {
			*(destinations[index].(*string)) = value
		}
		*(destinations[9].(*int)) = delivery.Attempt
		if delivery.SentAt.IsZero() {
			*(destinations[10].(**time.Time)) = nil
		} else {
			sentAt := delivery.SentAt
			*(destinations[10].(**time.Time)) = &sentAt
		}
		*(destinations[11].(*time.Time)) = delivery.CreatedAt
		return nil
	}}
}

func TestPostgresScheduleWritesUseLifecycleTransactions(t *testing.T) {
	base := time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)
	reminder := scheduleContractReminder("postgres-schedule", "pending", base.Add(time.Hour), base)

	t.Run("save", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{}
		repository, _, session := newFakeSchedulePostgresStore(tx)
		stored, err := repository.SaveReminder(t.Context(), reminder)
		if err != nil || stored.ID != reminder.ID || tx.commits != 1 || tx.rollbacks != 0 || session.releases != 1 {
			t.Fatalf("stored=%#v err=%v commit=%d rollback=%d release=%d", stored, err, tx.commits, tx.rollbacks, session.releases)
		}
		joined := strings.Join(tx.execSQL, "\n")
		for _, required := range []string{"reminders", "audit_events", "events"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("save transaction omitted %q: %v", required, tx.execSQL)
			}
		}
	})

	for index, stage := range []string{"reminder", "audit", "event"} {
		t.Run("save rollback "+stage, func(t *testing.T) {
			tx := &fakeConnectorPostgresTx{execErrors: map[int]error{index: safePostgresRetryError{errors.New("not sent")}}}
			repository, _, session := newFakeSchedulePostgresStore(tx)
			candidate, err := repository.SaveReminder(t.Context(), reminder)
			if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.rollbacks != 1 || tx.commits != 0 || session.releases != 1 {
				t.Fatalf("candidate=%#v err=%v code=%q commit=%d rollback=%d release=%d", candidate, err, StoreErrorCodeOf(err), tx.commits, tx.rollbacks, session.releases)
			}
		})
	}

	t.Run("update", func(t *testing.T) {
		updated := reminder
		updated.Text = "updated"
		updated.UpdatedAt = base.Add(time.Minute)
		tx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{reminderPostgresRow(updated, nil)}}
		repository, _, session := newFakeSchedulePostgresStore(tx)
		stored, err := repository.UpdatePendingReminder(t.Context(), updated, reminder.UpdatedAt)
		if err != nil || stored.Text != "updated" || len(tx.rowSQL) != 1 || len(tx.execSQL) != 2 || tx.commits != 1 || session.releases != 1 ||
			!strings.Contains(tx.execSQL[0], "audit_events") || !strings.Contains(tx.execSQL[1], "events") {
			t.Fatalf("stored=%#v err=%v rows=%v exec=%v commit=%d release=%d", stored, err, tx.rowSQL, tx.execSQL, tx.commits, session.releases)
		}
	})

	t.Run("update conflict", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{}
		repository, _, session := newFakeSchedulePostgresStore(tx)
		candidate, err := repository.UpdatePendingReminder(t.Context(), reminder, reminder.UpdatedAt)
		if candidate.ID != "" || !errors.Is(err, ErrReminderConflict) || StoreErrorCodeOf(err) != StoreErrorConflict || tx.rollbacks != 1 || session.releases != 1 {
			t.Fatalf("candidate=%#v err=%v code=%q rollback=%d release=%d", candidate, err, StoreErrorCodeOf(err), tx.rollbacks, session.releases)
		}
	})

	t.Run("delivery", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{}
		repository, _, session := newFakeSchedulePostgresStore(tx)
		delivery, err := repository.SaveReminderDelivery(t.Context(), app.ReminderDelivery{ID: "delivery", ReminderID: reminder.ID, Status: "sent", Attempt: 1})
		if err != nil || delivery.ID != "delivery" || len(tx.execSQL) != 4 || tx.commits != 1 || session.releases != 1 {
			t.Fatalf("delivery=%#v err=%v exec=%v commit=%d release=%d", delivery, err, tx.execSQL, tx.commits, session.releases)
		}
		joined := strings.Join(tx.execSQL, "\n")
		for _, required := range []string{"UPDATE reminders", "reminder_deliveries", "audit_events", "events"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("delivery transaction omitted %q: %v", required, tx.execSQL)
			}
		}
	})

	t.Run("delivery missing reminder", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{execTags: map[int]pgconn.CommandTag{0: pgconn.NewCommandTag("UPDATE 0")}}
		repository, _, session := newFakeSchedulePostgresStore(tx)
		candidate, err := repository.SaveReminderDelivery(t.Context(), app.ReminderDelivery{ID: "orphan", ReminderID: "missing", Status: "failed"})
		if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorNotFound || tx.rollbacks != 1 || session.releases != 1 {
			t.Fatalf("candidate=%#v err=%v code=%q rollback=%d release=%d", candidate, err, StoreErrorCodeOf(err), tx.rollbacks, session.releases)
		}
	})

	for index, stage := range []string{"reminder", "delivery", "audit", "event"} {
		t.Run("delivery rollback "+stage, func(t *testing.T) {
			tx := &fakeConnectorPostgresTx{execErrors: map[int]error{index: safePostgresRetryError{errors.New("not sent")}}}
			repository, _, session := newFakeSchedulePostgresStore(tx)
			candidate, err := repository.SaveReminderDelivery(t.Context(), app.ReminderDelivery{ID: "delivery", ReminderID: reminder.ID, Status: "failed"})
			if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.rollbacks != 1 || tx.commits != 0 || session.releases != 1 {
				t.Fatalf("candidate=%#v err=%v code=%q commit=%d rollback=%d release=%d", candidate, err, StoreErrorCodeOf(err), tx.commits, tx.rollbacks, session.releases)
			}
		})
	}
}

func TestPostgresScheduleReadsClassifyQueryScanRowsAndCorruptJSON(t *testing.T) {
	sentinel := errors.New("backend failed")
	base := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	reminder := scheduleContractReminder("read-schedule", "pending", base.Add(time.Hour), base)
	delivery := app.ReminderDelivery{ID: "read-delivery", ReminderID: reminder.ID, Status: "sent", SentAt: base, CreatedAt: base}

	t.Run("get missing", func(t *testing.T) {
		operations := &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{}, row: fakeConnectorPostgresRow{}}
		repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, schedulePostgres: operations}
		if value, found, err := repository.GetReminder(t.Context(), "missing"); err != nil || found || value.ID != "" {
			t.Fatalf("value=%#v found=%t err=%v", value, found, err)
		}
	})

	tests := []struct {
		name string
		ops  *fakeSchedulePostgresOps
		call func(*PostgresStore) error
		code StoreErrorCode
	}{
		{name: "get scan", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{}, row: fakeConnectorPostgresRow{scan: func(...any) error { return sentinel }}}, call: func(repository *PostgresStore) error {
			_, _, err := repository.GetReminder(t.Context(), reminder.ID)
			return err
		}, code: StoreErrorUnavailable},
		{name: "get corrupt", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{}, row: reminderPostgresRow(reminder, []byte("{"))}, call: func(repository *PostgresStore) error {
			_, _, err := repository.GetReminder(t.Context(), reminder.ID)
			return err
		}, code: StoreErrorCorrupt},
		{name: "list query", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{err: sentinel}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ListReminders(t.Context(), app.ReminderFilter{})
			return err
		}, code: StoreErrorUnavailable},
		{name: "list scan", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{scanErr: sentinel, rows: []fakeConnectorPostgresRow{{}}}}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ListReminders(t.Context(), app.ReminderFilter{})
			return err
		}, code: StoreErrorUnavailable},
		{name: "list rows", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{err: sentinel}}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ListReminders(t.Context(), app.ReminderFilter{})
			return err
		}, code: StoreErrorUnavailable},
		{name: "list corrupt", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{reminderPostgresRow(reminder, []byte("{"))}}}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ListReminders(t.Context(), app.ReminderFilter{})
			return err
		}, code: StoreErrorCorrupt},
		{name: "claim query", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{err: sentinel}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ClaimDueReminders(t.Context(), base, base, 1)
			return err
		}, code: StoreErrorUnavailable},
		{name: "claim rows", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{err: sentinel}}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ClaimDueReminders(t.Context(), base, base, 1)
			return err
		}, code: StoreErrorUnavailable},
		{name: "delivery query", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{err: sentinel}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ListReminderDeliveries(t.Context(), reminder.ID)
			return err
		}, code: StoreErrorUnavailable},
		{
			name: "delivery scan",
			ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{
				queryResults: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{
					scanErr: sentinel, rows: []fakeConnectorPostgresRow{reminderDeliveryPostgresRow(delivery)},
				}}},
			}},
			call: func(repository *PostgresStore) error {
				_, err := repository.ListReminderDeliveries(t.Context(), reminder.ID)
				return err
			},
			code: StoreErrorUnavailable,
		},
		{name: "delivery rows", ops: &fakeSchedulePostgresOps{fakeConnectorPostgresOps: &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{err: sentinel}}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.ListReminderDeliveries(t.Context(), reminder.ID)
			return err
		}, code: StoreErrorUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, schedulePostgres: test.ops}
			if err := test.call(repository); StoreErrorCodeOf(err) != test.code {
				t.Fatalf("error=%v code=%q want=%q", err, StoreErrorCodeOf(err), test.code)
			}
		})
	}
}
