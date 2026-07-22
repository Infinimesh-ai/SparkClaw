package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestListCurrentSchedulesIsReadOnlyOwnerScopedProjection(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	for _, reminder := range []app.Reminder{
		testScheduleReminder("pending-visible", app.DefaultOwnerID, app.DefaultOwnerID, "pending", now.Add(time.Hour), "Daily check"),
		testScheduleReminder("sending-visible", app.DefaultOwnerID, app.DefaultOwnerID, "sending", now.Add(2*time.Hour), "Sending now"),
		testScheduleReminder("sent-hidden", app.DefaultOwnerID, app.DefaultOwnerID, "sent", now.Add(-time.Hour), "Already sent"),
		testScheduleReminder("owner-hidden", "other-owner", "other-owner", "pending", now.Add(3*time.Hour), "Private task"),
		testScheduleReminder("actor-hidden", app.DefaultOwnerID, "other-actor", "pending", now.Add(4*time.Hour), "Other actor task"),
	} {
		st.SaveReminder(reminder)
	}

	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/schedules")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list schedules returned %d: %s", resp.StatusCode, raw)
	}
	for _, secret := range []string{"private-recipient", "credential-secret", "private-endpoint", "Private task", "Other actor task", "Already sent"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("schedule projection leaked %q: %s", secret, raw)
		}
	}
	var decoded struct {
		Schedules []publicSchedule `json:"schedules"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Schedules) != 2 || decoded.Schedules[0].ID != "pending-visible" || decoded.Schedules[1].ID != "sending-visible" {
		t.Fatalf("unexpected active schedules: %#v", decoded.Schedules)
	}
	if decoded.Schedules[0].Title != "Daily check" || decoded.Schedules[0].PayloadMode != app.SchedulePayloadRequest {
		t.Fatalf("unexpected public projection: %#v", decoded.Schedules[0])
	}
}

func testScheduleReminder(id, ownerID, actorID, status string, due time.Time, text string) app.Reminder {
	spec := app.ScheduleSpec{
		SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: ownerID, ActorID: actorID,
		Payload:       app.SchedulePayload{Mode: app.SchedulePayloadRequest, Content: app.MessageContent{Parts: []app.MessagePart{{ID: "part-1", Kind: app.MessagePartText, Text: text}}}},
		ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "private-endpoint"},
		Authorization: app.MessageAuthorization{PrincipalID: ownerID, Scope: []string{"credential-secret"}},
	}
	return app.Reminder{
		ID: id, SessionID: "session-1", Text: text, TextSummary: text, DueTime: due, Timezone: "Asia/Shanghai",
		Channel: "test", Recipient: "private-recipient", Status: status, CreatedAt: due.Add(-time.Hour), UpdatedAt: due.Add(-time.Hour), ScheduleSpec: &spec,
	}
}
