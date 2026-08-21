package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestPassiveNotificationStoreErrorProjection(t *testing.T) {
	privateCause := errors.New("private passive notification backend path")
	tests := []struct {
		code       store.StoreErrorCode
		wantStatus int
		wantCopy   string
	}{
		{code: store.StoreErrorInvalid, wantStatus: http.StatusBadRequest, wantCopy: "notification request is invalid"},
		{code: store.StoreErrorNotFound, wantStatus: http.StatusNotFound, wantCopy: "notification was not found"},
		{code: store.StoreErrorConflict, wantStatus: http.StatusConflict, wantCopy: "notification conflicts with existing state"},
		{code: store.StoreErrorCanceled, wantStatus: http.StatusRequestTimeout, wantCopy: "notification request was canceled"},
		{code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantCopy: "notification operation timed out"},
		{code: store.StoreErrorUnavailable, wantStatus: http.StatusServiceUnavailable, wantCopy: "notification service is unavailable"},
		{code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantCopy: "notification service is unavailable"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		writePassiveNotificationStoreError(response, &store.StoreError{Code: test.code, Operation: store.OperationPassiveNotificationList, Err: privateCause})
		if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantCopy) || strings.Contains(response.Body.String(), privateCause.Error()) {
			t.Fatalf("code=%q response=%d body=%s", test.code, response.Code, response.Body.String())
		}
	}
}

func TestPassiveNotificationAPIScopesAndPersistsReadState(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	defaultNotification := gatewayTestNotification("notification-default", app.DefaultOwnerID)
	otherNotification := gatewayTestNotification("notification-other", "other-owner")
	if _, _, err := st.CreatePassiveNotification(t.Context(), defaultNotification); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreatePassiveNotification(t.Context(), otherNotification); err != nil {
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
	reloaded, ok, err := st.GetPassiveNotification(t.Context(), app.DefaultOwnerID, defaultNotification.ID)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, _, err := st.CreatePassiveNotification(t.Context(), notification); err != nil {
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

// countingNotificationStore counts ListPassiveNotifications calls so tests can
// prove the SSE loop short-circuits on an unchanged revision.
type countingNotificationStore struct {
	store.Store
	listCalls atomic.Int64
}

func (s *countingNotificationStore) ListPassiveNotifications(ctx context.Context, ownerID, after string, limit int) ([]app.PassiveNotification, error) {
	s.listCalls.Add(1)
	return s.Store.ListPassiveNotifications(ctx, ownerID, after, limit)
}

func TestPassiveNotificationStreamSkipsListingWhenRevisionUnchanged(t *testing.T) {
	previous := passiveNotificationPollInterval
	passiveNotificationPollInterval = 10 * time.Millisecond
	defer func() { passiveNotificationPollInterval = previous }()

	cfg := testConfig(t.TempDir())
	inner := store.NewMemoryStore()
	st := &countingNotificationStore{Store: inner}
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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

	time.Sleep(150 * time.Millisecond)
	idleCalls := st.listCalls.Load()
	if idleCalls > 3 {
		// Cursor seeding plus the initial send account for at most two calls;
		// ~15 poll ticks elapsed and none of them may list an unchanged inbox.
		t.Fatalf("idle stream listed %d times", idleCalls)
	}

	notification := gatewayTestNotification("notification-counter", app.DefaultOwnerID)
	if _, _, err := st.CreatePassiveNotification(t.Context(), notification); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	received := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: notification.created") {
			received = true
			break
		}
	}
	if !received {
		t.Fatalf("stream did not deliver after revision bump: %v", scanner.Err())
	}
	if st.listCalls.Load() <= idleCalls {
		t.Fatal("revision bump did not trigger a list")
	}
}

func TestPassiveNotificationStreamCapsConcurrentSubscribersPerOwner(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	open := func(ctx context.Context) *http.Response {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/notifications/events/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	var held []*http.Response
	for i := 0; i < maxPassiveNotificationStreamsPerOwner; i++ {
		response := open(ctx)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("stream %d status = %d", i, response.StatusCode)
		}
		held = append(held, response)
	}
	overflow := open(ctx)
	if overflow.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("overflow stream status = %d, want 429", overflow.StatusCode)
	}
	overflow.Body.Close()

	// Releasing a slot admits a new subscriber again.
	cancel()
	for _, response := range held {
		response.Body.Close()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		retry := open(t.Context())
		if retry.StatusCode == http.StatusOK {
			retry.Body.Close()
			break
		}
		retry.Body.Close()
		if time.Now().After(deadline) {
			t.Fatal("stream slots were not released after disconnect")
		}
		time.Sleep(20 * time.Millisecond)
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
