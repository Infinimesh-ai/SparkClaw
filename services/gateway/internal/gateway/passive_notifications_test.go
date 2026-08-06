package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

func TestPassiveNotificationAPIScopesAndPersistsReadState(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	defaultNotification := gatewayTestNotification("notification-default", app.DefaultOwnerID)
	otherNotification := gatewayTestNotification("notification-other", "other-owner")
	if _, _, err := st.CreatePassiveNotification(defaultNotification); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreatePassiveNotification(otherNotification); err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(ts.URL + "/api/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var listed struct {
		Notifications []passiveNotificationView `json:"notifications"`
		UnreadCount   int                       `json:"unread_count"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Notifications) != 1 || listed.Notifications[0].ID != defaultNotification.ID || listed.UnreadCount != 1 {
		t.Fatalf("owner-scoped notifications = %#v", listed)
	}

	markRead, err := http.Post(ts.URL+"/api/notifications/"+defaultNotification.ID+"/read", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer markRead.Body.Close()
	if markRead.StatusCode != http.StatusOK {
		t.Fatalf("mark read returned %d", markRead.StatusCode)
	}
	reloaded, ok := st.GetPassiveNotification(app.DefaultOwnerID, defaultNotification.ID)
	if !ok || reloaded.ReadAt == nil {
		t.Fatalf("read state was not stored: %#v", reloaded)
	}
}

func TestPassiveNotificationGlobalStreamEmitsNewInboxItem(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/notifications/events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("stream content type = %q", got)
	}

	notification := gatewayTestNotification("notification-live", app.DefaultOwnerID)
	if _, _, err := st.CreatePassiveNotification(notification); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	var eventName, dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
		}
		if eventName != "" && dataLine != "" {
			break
		}
	}
	if eventName != "notification.created" {
		t.Fatalf("global stream event = %q, scanner error = %v", eventName, scanner.Err())
	}
	var view passiveNotificationView
	if err := json.Unmarshal([]byte(dataLine), &view); err != nil {
		t.Fatal(err)
	}
	if view.ID != notification.ID || view.DeepLink != notification.DeepLink {
		t.Fatalf("global stream notification = %#v", view)
	}
}

func gatewayTestNotification(id, ownerID string) app.PassiveNotification {
	now := time.Now().UTC()
	return app.PassiveNotification{
		ID: id, OwnerID: ownerID, EndpointID: "localmind-notifications-" + ownerID,
		IdempotencyKey: id, Fingerprint: "fingerprint-" + id, NotificationID: "delivery-" + id,
		Source: "localmind", Kind: app.PassiveNotificationKindDocumentMention,
		DeepLink: "https://localmind.example/workspace/doc", OccurredAt: now, CreatedAt: now,
	}
}
