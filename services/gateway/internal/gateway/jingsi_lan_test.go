package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestJingSiLANIsDisabledByDefault(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	for _, path := range []string{
		"/api/jingsi/v0/readyz",
		"/api/jingsi/v0/readyz?debug=1",
		"/api/client-events/v0?after=ce_invalid&after=ce_invalid",
	} {
		response, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("disabled JingSi route %s returned %d", path, response.StatusCode)
		}
	}
}

func TestJingSiLANHeadCatchUpFiltersConfiguredSession(t *testing.T) {
	server, st, session, ts := newJingSiTestServer(t)
	_ = server
	storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "assistant", Content: "before first connection"})

	var head struct {
		Version string `json:"version"`
		Cursor  string `json:"cursor"`
	}
	getJSON(t, ts.URL+"/api/client-events/v0/head", &head)
	if head.Version != jingsiEventVersion || !strings.HasPrefix(head.Cursor, "evt_") {
		t.Fatalf("unexpected head: %#v", head)
	}

	other := storetest.MustCreateSession(t, st, "other")
	storetest.MustAddMessage(t, st, app.Message{SessionID: other.ID, Role: "assistant", Content: "must stay hidden"})
	wanted := storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "assistant", Content: "idle update"})

	response, err := http.Get(ts.URL + "/api/client-events/v0?after=" + head.Cursor + "&limit=100")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("catch-up returned %d: %s", response.StatusCode, raw)
	}
	if bytes.Contains(raw, []byte("session_id")) || bytes.Contains(raw, []byte(other.ID)) {
		t.Fatalf("catch-up leaked a session reference: %s", raw)
	}
	var page struct {
		Events []struct {
			Cursor  string `json:"cursor"`
			Message struct {
				ID   string `json:"id"`
				Role string `json:"role"`
				Text string `json:"text"`
			} `json:"message"`
		} `json:"events"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Message.ID != wanted.ID || page.Events[0].Message.Text != "idle update" || page.Events[0].Message.Role != "assistant" {
		t.Fatalf("unexpected projected page: %#v", page)
	}
	if page.NextCursor != page.Events[0].Cursor || page.HasMore {
		t.Fatalf("unexpected page cursor: %#v", page)
	}
}

func TestJingSiLANRejectsWrongSessionCursor(t *testing.T) {
	_, st, _, ts := newJingSiTestServer(t)
	other := storetest.MustCreateSession(t, st, "other")
	storetest.MustAddMessage(t, st, app.Message{SessionID: other.ID, Role: "user", Content: "other"})
	cursor, err := st.MessageEventHead(t.Context(), other.ID)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(ts.URL + "/api/client-events/v0?after=" + cursor)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict || payload["code"] != "cursor_reset_required" {
		t.Fatalf("wrong-session cursor returned %d %#v", response.StatusCode, payload)
	}
}

func TestJingSiLANRejectsMessagePayloadFromAnotherSession(t *testing.T) {
	server, _, session, _ := newJingSiTestServer(t)
	event := app.Event{
		ID:        "evt_mismatched",
		Type:      "message.created",
		SessionID: session.ID,
		Payload: app.Message{
			ID:        "m_other",
			SessionID: "sess_other",
			Role:      "assistant",
			Content:   "must stay hidden",
		},
	}

	if _, visible, err := server.projectJingSiEvent(event); err == nil || visible {
		t.Fatalf("mismatched message payload projected: visible=%v err=%v", visible, err)
	}
}

func TestJingSiLANEmptyHeadCursorIsBoundToConfiguredSession(t *testing.T) {
	server, st, session, ts := newJingSiTestServer(t)
	var head struct {
		Cursor string `json:"cursor"`
	}
	getJSON(t, ts.URL+"/api/client-events/v0/head", &head)
	if !strings.HasPrefix(head.Cursor, "ce_") || strings.Contains(head.Cursor, session.ID) {
		t.Fatalf("empty head cursor is not opaque: %q", head.Cursor)
	}

	other := storetest.MustCreateSessionWithScope(t, st, "other", app.DefaultOwnerID, server.cfg.Workspaces.DefaultRoot, "webchat", false)
	server.cfg.JingSiLAN.SessionID = other.ID
	response, err := http.Get(ts.URL + "/api/client-events/v0?after=" + head.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict || payload["code"] != "cursor_reset_required" {
		t.Fatalf("old empty-session cursor returned %d %#v", response.StatusCode, payload)
	}
}

func TestJingSiLANCatchUpAdvancesAcrossFilteredMessageRoles(t *testing.T) {
	_, st, session, ts := newJingSiTestServer(t)
	var head struct {
		Cursor string `json:"cursor"`
	}
	getJSON(t, ts.URL+"/api/client-events/v0/head", &head)
	storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "tool", Content: "internal result"})

	var page struct {
		Events     []jingSiClientEvent `json:"events"`
		NextCursor string              `json:"next_cursor"`
		HasMore    bool                `json:"has_more"`
	}
	getJSON(t, ts.URL+"/api/client-events/v0?after="+head.Cursor, &page)
	if len(page.Events) != 0 || page.NextCursor == head.Cursor || page.HasMore {
		t.Fatalf("filtered message did not advance page cursor: %#v", page)
	}

	wanted := storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "assistant", Content: "visible"})
	getJSON(t, ts.URL+"/api/client-events/v0?after="+page.NextCursor, &page)
	if len(page.Events) != 1 || page.Events[0].Message.ID != wanted.ID {
		t.Fatalf("visible message after filtered cursor = %#v", page)
	}
}

func TestJingSiLANSSEReceivesIdleMessage(t *testing.T) {
	_, st, session, ts := newJingSiTestServer(t)
	head := struct {
		Cursor string `json:"cursor"`
	}{}
	getJSON(t, ts.URL+"/api/client-events/v0/head", &head)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/client-events/v0/stream?after="+head.Cursor, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE returned %d", response.StatusCode)
	}
	wanted := storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "assistant", Content: "arrived while idle"})

	scanner := bufio.NewScanner(response.Body)
	var eventName, eventID, data string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			eventID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
		if eventID != "" && eventName != "" && data != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if eventName != "message.created" || eventID == "" || strings.Contains(data, "session_id") {
		t.Fatalf("unexpected SSE frame: id=%q event=%q data=%s", eventID, eventName, data)
	}
	var event jingSiClientEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		t.Fatal(err)
	}
	if event.Message.ID != wanted.ID || event.Message.Text != wanted.Content {
		t.Fatalf("unexpected SSE projection: %#v", event)
	}
}

func TestJingSiLANSendUsesConfiguredWebIngressAndStrictBody(t *testing.T) {
	server, _, session, ts := newJingSiTestServer(t)
	captured := make(chan struct {
		sessionID string
		content   string
		ingress   app.MessageIngressContext
	}, 1)
	server.streamMessage = func(_ context.Context, sessionID, content string, attachments []agent.MessageAttachment, ingress app.MessageIngressContext, _ agent.StreamHandler) (agent.Result, error) {
		if len(attachments) != 0 {
			t.Fatalf("JingSi send forwarded attachments: %#v", attachments)
		}
		captured <- struct {
			sessionID string
			content   string
			ingress   app.MessageIngressContext
		}{sessionID: sessionID, content: content, ingress: ingress}
		return agent.Result{Message: app.Message{ID: "m_assistant"}}, nil
	}

	response, err := http.Post(ts.URL+"/api/messages/stream", "application/json", strings.NewReader(`{"content":"  hello from phone  "}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || bytes.Contains(raw, []byte(session.ID)) || bytes.Contains(raw, []byte("session_id")) {
		t.Fatalf("send response = %d %s", response.StatusCode, raw)
	}
	got := <-captured
	if got.sessionID != session.ID || got.content != "  hello from phone  " || got.ingress.Source.Kind != app.MessageSourceWeb || got.ingress.ReturnRoute.Mode != app.ReturnToSource {
		t.Fatalf("send did not reuse configured Web ingress: %#v", got)
	}

	response, err = http.Post(ts.URL+"/api/messages/stream", "application/json", strings.NewReader(`{"content":"blocked","attachments":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown send field returned %d", response.StatusCode)
	}
	if len(captured) != 0 {
		t.Fatal("invalid send reached the runtime")
	}
}

func TestJingSiLANSendRejectsWrongMediaTypeAndOversizedText(t *testing.T) {
	server, _, _, ts := newJingSiTestServer(t)
	called := make(chan struct{}, 1)
	server.streamMessage = func(_ context.Context, _ string, _ string, _ []agent.MessageAttachment, _ app.MessageIngressContext, _ agent.StreamHandler) (agent.Result, error) {
		called <- struct{}{}
		return agent.Result{}, nil
	}

	response, err := http.Post(ts.URL+"/api/messages/stream", "text/plain", strings.NewReader(`{"content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong media type returned %d", response.StatusCode)
	}

	oversized := `{"content":"` + strings.Repeat("x", server.cfg.JingSiLAN.MaxMessageBytes+1) + `"}`
	response, err = http.Post(ts.URL+"/api/messages/stream", "application/json", strings.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized text returned %d", response.StatusCode)
	}
	if len(called) != 0 {
		t.Fatal("rejected send reached the runtime")
	}
}

func TestJingSiLANRejectsUnsupportedOrRepeatedQueryParameters(t *testing.T) {
	_, _, _, ts := newJingSiTestServer(t)
	for _, path := range []string{
		"/api/jingsi/v0/readyz?debug=1",
		"/api/client-events/v0/head?debug=1",
		"/api/client-events/v0?after=ce_invalid&after=ce_invalid",
		"/api/client-events/v0/stream?after=ce_invalid&after=ce_invalid",
	} {
		response, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET %s returned %d", path, response.StatusCode)
		}
	}
}

func TestJingSiLANRequiresVisibleDefaultOwnerWebChatSession(t *testing.T) {
	for _, session := range []app.Session{
		{ID: "missing"},
		{OwnerID: app.DefaultOwnerID, Source: "webchat", Hidden: true},
		{OwnerID: "other", Source: "webchat"},
		{OwnerID: app.DefaultOwnerID, Source: "telegram"},
	} {
		t.Run(session.ID+session.OwnerID+session.Source, func(t *testing.T) {
			cfg := testConfig(t.TempDir())
			st := store.NewMemoryStore()
			if session.ID == "" {
				created := storetest.MustCreateSessionWithScope(t, st, "invalid", session.OwnerID, "", session.Source, session.Hidden)
				session.ID = created.ID
			}
			cfg.JingSiLAN = config.JingSiLANConfig{Enabled: true, SessionID: session.ID, MaxMessageBytes: 1024}
			tools := toolhub.New(cfg, st)
			defer tools.Close()
			runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
			ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
			defer ts.Close()
			response, err := http.Get(ts.URL + "/api/jingsi/v0/readyz")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("invalid configured session returned %d", response.StatusCode)
			}
		})
	}
}

func newJingSiTestServer(t *testing.T) (*Server, *store.MemoryStore, app.Session, *httptest.Server) {
	t.Helper()
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "JingSi LAN", app.DefaultOwnerID, cfg.Workspaces.DefaultRoot, "webchat", false)
	cfg.JingSiLAN = config.JingSiLANConfig{Enabled: true, SessionID: session.ID, MaxMessageBytes: 64 << 10}
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return server, st, session, ts
}

func getJSON(t *testing.T, url string, destination any) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET %s returned %d: %s", url, response.StatusCode, raw)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func TestJingSiLANStreamConcurrencyIsBounded(t *testing.T) {
	srv, _, session, ts := newJingSiTestServer(t)
	for i := 0; i < maxPassiveNotificationStreamsPerOwner; i++ {
		if !srv.acquirePassiveNotificationStream(jingsiStreamKey(session.ID)) {
			t.Fatalf("stream slot %d unexpectedly denied", i)
		}
	}
	response, err := http.Get(ts.URL + "/api/client-events/v0/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("saturated stream budget returned %d, want 429", response.StatusCode)
	}
	srv.releasePassiveNotificationStream(jingsiStreamKey(session.ID))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/client-events/v0/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	freed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer freed.Body.Close()
	if freed.StatusCode != http.StatusOK {
		t.Fatalf("freed stream slot returned %d, want 200", freed.StatusCode)
	}
}
