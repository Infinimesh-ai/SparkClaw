package reminder

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func saveWorkbenchSchedule(t *testing.T, st testScheduleRepository, id string, due time.Time, recurrence string) app.MessageSchedule {
	t.Helper()
	session, err := st.CreateSessionWithScope(t.Context(), "Host schedule", app.DefaultOwnerID, "", "schedule", true)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := messagecontrol.NewScheduleRegistry(st).Save(t.Context(), app.MessageSchedule{
		ID: app.ScheduleID(id), SessionID: session.ID, DueTime: due, Timezone: "UTC", Recurrence: recurrence,
		DedupeKey: id, Status: "pending", Spec: app.ScheduleSpec{
			SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
			Payload:       app.SchedulePayload{Content: app.MessageContent{Parts: []app.MessagePart{{ID: id + ":text", Kind: app.MessagePartText, Text: "run once"}}}},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: messagecontrol.WebEndpointID(session.ID)},
			Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !schedule.Spec.WorkbenchOwned {
		t.Fatal("host origin was not frozen")
	}
	return schedule
}

func TestWorkbenchSchedulerOfflineAndRestartPermanentlyMissOccurrences(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var st testScheduleRepository = store.NewMemoryStore()
			path := filepath.Join(t.TempDir(), "workbench.json")
			if backend == "file" {
				file, err := store.NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				st = file
			}
			due := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
			schedule := saveWorkbenchSchedule(t, st, "offline", due, "minutely")
			now := due.Add(2*time.Minute + time.Second)
			var published []app.MessageEnvelope
			publisher := publisherFunc(func(_ context.Context, envelope app.MessageEnvelope) error {
				published = append(published, envelope)
				return nil
			})
			scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisher, 0)
			scheduler.now = func() time.Time { return now }
			if got := mustSchedulerTick(t, scheduler, t.Context()); len(got) != 0 || len(published) != 0 {
				t.Fatalf("startup caught up: %#v", got)
			}
			records := mustSchedulerDeliveries(t, st, string(schedule.ID))
			if len(records) != 3 {
				t.Fatalf("missed occurrences = %#v", records)
			}
			for _, record := range records {
				if record.Status != "missed" {
					t.Fatalf("record = %#v", record)
				}
			}
			if backend == "file" {
				file, err := store.NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				st = file
			}
			// Restart must still leave every past occurrence skipped.
			scheduler = NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisher, 0)
			scheduler.now = func() time.Time { return now }
			mustSchedulerTick(t, scheduler, t.Context())
			for now.Before(due.Add(3 * time.Minute)) {
				now = now.Add(5 * time.Second)
				mustSchedulerTick(t, scheduler, t.Context())
			}
			if len(published) != 1 || published[0].ID != workbenchOccurrenceID(app.MessageSchedule{ID: schedule.ID, DueTime: due.Add(3 * time.Minute)}) {
				t.Fatalf("future occurrence = %#v", published)
			}
			updated := mustSchedulerReminder(t, st, string(schedule.ID))
			if updated.Status != "pending" || !updated.DueTime.Equal(due.Add(4*time.Minute)) {
				t.Fatalf("future definition lost: %#v", updated)
			}
		})
	}
}

func TestWorkbenchSchedulerDetectsSleepAndAvailabilityGap(t *testing.T) {
	for _, gap := range []string{"sleep", "connection"} {
		t.Run(gap, func(t *testing.T) {
			st := store.NewMemoryStore()
			due := time.Now().UTC().Truncate(time.Microsecond)
			saveWorkbenchSchedule(t, st, "gap", due, "")
			now := due.Add(-time.Second)
			count := 0
			scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisherFunc(func(context.Context, app.MessageEnvelope) error { count++; return nil }), 0)
			scheduler.now = func() time.Time { return now }
			mustSchedulerTick(t, scheduler, t.Context())
			if gap == "sleep" {
				now = now.Add(time.Hour)
			} else {
				scheduler.publisher = nil
				now = due.Add(time.Second)
			}
			mustSchedulerTick(t, scheduler, t.Context())
			if count != 0 || mustSchedulerReminder(t, st, "gap").Status != "missed" {
				t.Fatal("offline occurrence was submitted")
			}
		})
	}
}

func TestWorkbenchSchedulerClaimsOnceAcrossConcurrentSchedulers(t *testing.T) {
	st := store.NewMemoryStore()
	due := time.Now().UTC().Truncate(time.Microsecond)
	schedule := saveWorkbenchSchedule(t, st, "concurrent", due, "")
	now := due.Add(-time.Second)
	var calls atomic.Int32
	publisher := publisherFunc(func(_ context.Context, envelope app.MessageEnvelope) error {
		if envelope.ID != workbenchOccurrenceID(schedule) || envelope.IdempotencyKey != envelope.ID {
			t.Errorf("identity changed: %#v", envelope)
		}
		calls.Add(1)
		return nil
	})
	var schedulers []*Scheduler
	for range 8 {
		s := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisher, 0)
		s.now = func() time.Time { return now }
		mustSchedulerTick(t, s, t.Context())
		schedulers = append(schedulers, s)
	}
	now = due.Add(time.Second)
	var workers sync.WaitGroup
	for _, s := range schedulers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := s.Tick(t.Context()); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("publishes = %d", calls.Load())
	}
}

func TestWorkbenchSchedulerNeverReplaysUnknownSubmissionAndKeepsFuture(t *testing.T) {
	st := store.NewMemoryStore()
	due := time.Now().UTC().Truncate(time.Microsecond)
	saveWorkbenchSchedule(t, st, "unknown", due, "minutely")
	now := due.Add(-time.Second)
	calls := 0
	publisher := publisherFunc(func(context.Context, app.MessageEnvelope) error {
		calls++
		return retryablePublishError{errors.New("lost response after admission")}
	})
	scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisher, 0)
	scheduler.now = func() time.Time { return now }
	mustSchedulerTick(t, scheduler, t.Context())
	now = due.Add(time.Second)
	mustSchedulerTick(t, scheduler, t.Context())
	if records := mustSchedulerDeliveries(t, st, "unknown"); len(records) != 1 || records[0].Status != "unknown" || records[0].RetryState != "reconcile" {
		t.Fatalf("records = %#v", records)
	}
	for range 4 {
		now = now.Add(time.Second)
		mustSchedulerTick(t, scheduler, t.Context())
	}
	if calls != 1 {
		t.Fatalf("uncertain submission was replayed %d times", calls)
	}
	if next := mustSchedulerReminder(t, st, "unknown"); next.Status != "pending" || !next.DueTime.Equal(due.Add(time.Minute)) {
		t.Fatalf("future = %#v", next)
	}
}
