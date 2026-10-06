package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func exerciseWorkbenchScheduleContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	due := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	input := scheduleContractReminder("workbench-occurrence", "pending", due, due.Add(-time.Hour))
	input.ScheduleSpec.WorkbenchOwned = true
	input.Recurrence = "minutely"
	original := mustSaveReminder(t, repository, input)
	for _, got := range mustClaimDueReminders(t, repository, due.Add(time.Hour), due.Add(time.Hour), 100) {
		if got.ID == original.ID {
			t.Fatalf("service timer claimed workbench occurrence: %#v", got)
		}
	}
	record := app.ReminderDelivery{ID: "env_workbench-occurrence_1791280800000000000", ReminderID: original.ID, Status: "missed", Provider: "workbench"}
	updated, err := repository.AdvanceWorkbenchSchedule(t.Context(), original.ID, original.UpdatedAt, due.Add(time.Minute), record)
	if err != nil || updated.Status != "pending" || !updated.DueTime.Equal(due.Add(time.Minute)) {
		t.Fatalf("advance = %#v err=%v", updated, err)
	}
	if _, err := repository.AdvanceWorkbenchSchedule(t.Context(), original.ID, original.UpdatedAt, due.Add(time.Minute), record); !errors.Is(err, ErrReminderConflict) {
		t.Fatalf("duplicate advance err=%v", err)
	}
	if restart != nil {
		repository = restart()
	}
	persisted, ok := mustGetReminder(t, repository, original.ID)
	if !ok || !persisted.DueTime.Equal(updated.DueTime) || !persisted.ScheduleSpec.WorkbenchOwned {
		t.Fatalf("restart lost definition: %#v", persisted)
	}
	if records := mustListReminderDeliveries(t, repository, original.ID); len(records) != 1 || records[0].Status != "missed" {
		t.Fatalf("atomic ledger = %#v", records)
	}
	// Another workbench's ordinary records cannot leak into the host-only scan.
	other := scheduleContractReminder("service-pending", "pending", due, due.Add(-time.Hour))
	mustSaveReminder(t, repository, other)
	filtered := mustListReminders(t, repository, app.ReminderFilter{WorkbenchOnly: true, Status: "pending", Limit: 100})
	if len(filtered) != 1 || filtered[0].ID != original.ID {
		t.Fatalf("origin filter = %#v", filtered)
	}
	record.ID, record.Status = "env_workbench-occurrence_1791280860000000000", "submitted"
	advanced, err := repository.AdvanceWorkbenchSchedule(t.Context(), persisted.ID, persisted.UpdatedAt, due.Add(2*time.Minute), record)
	if err != nil {
		t.Fatal(err)
	}
	// A delayed result must not overwrite the future occurrence's due/status.
	record.Status = "sent"
	mustSaveReminderDelivery(t, repository, record)
	result, _ := mustGetReminder(t, repository, original.ID)
	if result.Status != "pending" || !result.DueTime.Equal(advanced.DueTime) {
		t.Fatalf("result overwrote future definition: %#v", result)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.AdvanceWorkbenchSchedule(ctx, result.ID, result.UpdatedAt, due.Add(3*time.Minute), record); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled advance = %v", err)
	}
}

func TestFileWorkbenchOccurrenceRollbackRestoresDefinitionAndLedger(t *testing.T) {
	st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC()
	input := scheduleContractReminder("file-occurrence", "pending", due, due.Add(-time.Hour))
	input.ScheduleSpec.WorkbenchOwned = true
	saved := mustSaveReminder(t, st, input)
	before := st.captureFileRollback()
	st.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	_, err = st.AdvanceWorkbenchSchedule(t.Context(), saved.ID, saved.UpdatedAt, due.Add(time.Hour), app.ReminderDelivery{ID: "occurrence", ReminderID: saved.ID, Status: "submitted"})
	if StoreErrorCodeOf(err) != StoreErrorDurability {
		t.Fatalf("advance failure = %v", err)
	}
	if after := st.captureFileRollback(); !reflect.DeepEqual(before, after) {
		t.Fatal("failed commit retained claim, occurrence ledger, audit, or event")
	}
}

func TestPostgresWorkbenchOccurrenceTransactionFailures(t *testing.T) {
	due := time.Now().UTC().Truncate(time.Microsecond)
	current := scheduleContractReminder("pg-occurrence", "pending", due, due.Add(-time.Hour))
	current.ScheduleSpec.WorkbenchOwned = true
	for _, stage := range []string{"ok", "update", "ledger", "commit"} {
		t.Run(stage, func(t *testing.T) {
			tx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{reminderPostgresRow(current, nil)}}
			switch stage {
			case "update":
				tx.execErrors = map[int]error{0: safePostgresRetryError{errors.New("update unavailable")}}
			case "ledger":
				tx.execErrors = map[int]error{1: safePostgresRetryError{errors.New("ledger unavailable")}}
			case "commit":
				tx.commitErr = errors.New("commit response lost")
			}
			st, _, session := newFakeSchedulePostgresStore(tx)
			_, err := st.AdvanceWorkbenchSchedule(t.Context(), current.ID, current.UpdatedAt, due.Add(time.Hour), app.ReminderDelivery{ID: "occurrence", ReminderID: current.ID, Status: "submitted"})
			switch stage {
			case "ok":
				if err != nil || tx.commits != 1 || len(tx.execSQL) != 4 {
					t.Fatalf("err=%v exec=%v commit=%d", err, tx.execSQL, tx.commits)
				}
			case "commit":
				if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || session.terminates != 1 {
					t.Fatalf("err=%v terminate=%d", err, session.terminates)
				}
			default:
				if err == nil || tx.rollbacks != 1 || tx.commits != 0 {
					t.Fatalf("err=%v rollback=%d commit=%d", err, tx.rollbacks, tx.commits)
				}
			}
		})
	}
}
