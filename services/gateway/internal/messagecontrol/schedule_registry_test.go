package messagecontrol

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestScheduleRegistryPersistsSpecInFileStore(t *testing.T) {
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
			Payload:       app.SchedulePayload{Content: textContent("request", "search the web tomorrow")},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: WebEndpointID(session.ID)},
			Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
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
	if !ok || got.Spec.Payload.Content.Parts[0].Text != "search the web tomorrow" {
		t.Fatalf("schedule did not round trip: %#v", got)
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
			Payload:       app.SchedulePayload{Content: textContent("cross", "private")},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: app.EndpointID(binding.ID)},
			Authorization: app.MessageAuthorization{PrincipalID: "owner-a"},
		},
	}
	if _, err := NewScheduleRegistry(st).Save(t.Context(), schedule); err == nil {
		t.Fatal("expected cross-owner endpoint to be rejected")
	}
}

func textContent(prefix, text string) app.MessageContent {
	return app.MessageContent{Parts: []app.MessagePart{{ID: prefix + ":text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: text}}}
}
