package messagecontrol

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestScheduleRegistryBackfillsLegacyReminderAsLiteral(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSession("Web")
	legacy := st.SaveReminder(app.Reminder{ID: "rem_legacy", SessionID: session.ID, Text: "legacy text", DueTime: time.Now().Add(time.Hour), Timezone: "UTC", Channel: "web", DedupeKey: "legacy", Status: "pending"})
	schedule, ok := NewScheduleRegistry(st).Get(t.Context(), app.ScheduleID(legacy.ID))
	if !ok {
		t.Fatal("legacy reminder was not exposed as a schedule")
	}
	if schedule.Spec.Payload.Mode != app.SchedulePayloadLiteral || schedule.Spec.Payload.Content.Parts[0].Text != "legacy text" {
		t.Fatalf("unexpected legacy projection: %#v", schedule)
	}
	if schedule.Spec.ReturnRoute.EndpointID != WebEndpointID(session.ID) {
		t.Fatalf("unexpected legacy return route: %#v", schedule.Spec.ReturnRoute)
	}
}

func TestScheduleRegistryPersistsRequestSpecInFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := st.CreateSession("Timer")
	now := time.Now().UTC()
	schedule := app.MessageSchedule{
		ID: "sched_request", SessionID: session.ID, DueTime: now.Add(time.Hour), Timezone: "UTC", DedupeKey: "request", Status: "pending", CreatedAt: now, UpdatedAt: now,
		Spec: app.ScheduleSpec{
			SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
			Payload:                app.SchedulePayload{Mode: app.SchedulePayloadRequest, Content: textContent("request", "search the web tomorrow")},
			ReturnRoute:            app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: WebEndpointID(session.ID)},
			Authorization:          app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
			ExpectedCapabilityPath: []app.CapabilityID{"browser"},
		},
	}
	if _, err := NewScheduleRegistry(st).Save(t.Context(), schedule); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := NewScheduleRegistry(reloaded).Get(t.Context(), schedule.ID)
	if !ok || got.Spec.Payload.Mode != app.SchedulePayloadRequest || got.Spec.ExpectedCapabilityPath[0] != "browser" {
		t.Fatalf("request schedule did not round trip: %#v", got)
	}
}

func TestScheduleRegistryRejectsCrossOwnerReturnEndpoint(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSessionWithScope("Owner A", "owner-a", t.TempDir(), "webchat", false)
	binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "bind_owner_b", OwnerID: "owner-b", Channel: "future", Status: "active"})
	now := time.Now().UTC()
	schedule := app.MessageSchedule{
		ID: "sched_cross_owner", SessionID: session.ID, DueTime: now.Add(time.Hour), Timezone: "UTC", DedupeKey: "cross", Status: "pending", CreatedAt: now, UpdatedAt: now,
		Spec: app.ScheduleSpec{
			SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: "owner-a", ActorID: "owner-a",
			Payload:       app.SchedulePayload{Mode: app.SchedulePayloadLiteral, Content: textContent("cross", "private")},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: app.EndpointID(binding.ID)},
			Authorization: app.MessageAuthorization{PrincipalID: "owner-a"},
		},
	}
	if _, err := NewScheduleRegistry(st).Save(t.Context(), schedule); err == nil {
		t.Fatal("expected cross-owner endpoint to be rejected")
	}
}
