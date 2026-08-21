package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestScheduleStoreErrorProjection(t *testing.T) {
	privateCause := errors.New("private schedule backend path")
	tests := []struct {
		code       store.StoreErrorCode
		wantStatus int
		wantCopy   string
	}{
		{code: store.StoreErrorInvalid, wantStatus: http.StatusBadRequest, wantCopy: "schedule request is invalid"},
		{code: store.StoreErrorNotFound, wantStatus: http.StatusNotFound, wantCopy: "schedule record not found"},
		{code: store.StoreErrorConflict, wantStatus: http.StatusConflict, wantCopy: "schedule changed or is no longer available"},
		{code: store.StoreErrorCanceled, wantStatus: http.StatusRequestTimeout, wantCopy: "schedule request was canceled"},
		{code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantCopy: "schedule operation timed out"},
		{code: store.StoreErrorUnavailable, wantStatus: http.StatusServiceUnavailable, wantCopy: "schedule service is unavailable"},
		{code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantCopy: "schedule service is unavailable"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		writeScheduleStoreError(response, &store.StoreError{Code: test.code, Operation: store.OperationReminderList, Err: privateCause})
		if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantCopy) || strings.Contains(response.Body.String(), privateCause.Error()) {
			t.Fatalf("code=%q response=%d body=%s", test.code, response.Code, response.Body.String())
		}
	}
}

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
		if _, err := st.SaveReminder(t.Context(), reminder); err != nil {
			t.Fatal(err)
		}
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
	if decoded.Schedules[0].Title != "Daily check" || decoded.Schedules[0].Text != "Daily check" || decoded.Schedules[0].UpdatedAt.IsZero() {
		t.Fatalf("unexpected public projection: %#v", decoded.Schedules[0])
	}
	if decoded.Schedules[0].Endpoint.Status != "unavailable" || decoded.Schedules[0].Editable || !decoded.Schedules[0].Cancelable {
		t.Fatalf("unexpected unavailable endpoint controls: %#v", decoded.Schedules[0])
	}
}

func TestScheduleEndpointProjectionNamesWebAndThirdPartyReminderEndpoints(t *testing.T) {
	st := store.NewMemoryStore()
	webSession := storetest.MustCreateSession(t, st, "Web schedule chat")
	now := time.Now().UTC()
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-telegram", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		Channel: "telegram", Status: string(app.EndpointActive), DisplayName: "Work bot",
		ExternalUserID: "private-user-id", CreatedAt: now, UpdatedAt: now,
	})
	server := &Server{store: st}
	request := httptest.NewRequest(http.MethodGet, "/api/schedules", nil)
	for _, test := range []struct {
		name         string
		endpointID   app.EndpointID
		software     string
		account      string
		conversation string
	}{
		{name: "web", endpointID: messagecontrol.WebEndpointID(webSession.ID), software: "WebChat", conversation: webSession.Title},
		{name: "third party", endpointID: "binding-telegram", software: "Telegram", account: "Work bot", conversation: "Work bot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := server.publicScheduleEndpoint(request, app.MessageSchedule{
				SessionID: webSession.ID,
				Spec:      app.ScheduleSpec{ReturnRoute: app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: test.endpointID}},
			})
			if projection.Status != string(app.EndpointActive) || projection.SoftwareDisplayName != test.software ||
				projection.AccountDisplayName != test.account || projection.ConversationLabel != test.conversation {
				t.Fatalf("unexpected endpoint projection: %#v", projection)
			}
		})
	}
}

func testScheduleReminder(id, ownerID, actorID, status string, due time.Time, text string) app.Reminder {
	spec := app.ScheduleSpec{
		SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: ownerID, ActorID: actorID,
		Payload:       app.SchedulePayload{Content: app.MessageContent{Parts: []app.MessagePart{{ID: "part-1", Kind: app.MessagePartText, Text: text}}}},
		ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "private-endpoint"},
		Authorization: app.MessageAuthorization{PrincipalID: ownerID, Scope: []string{"credential-secret"}},
	}
	return app.Reminder{
		ID: id, SessionID: "session-1", Text: text, TextSummary: text, DueTime: due, Timezone: "Asia/Shanghai",
		Channel: "test", Recipient: "private-recipient", Status: status, CreatedAt: due.Add(-time.Hour), UpdatedAt: due.Add(-time.Hour), ScheduleSpec: &spec,
	}
}
