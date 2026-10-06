package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/gorilla/websocket"
)

const localTestSecret = "independent-local-webchat-test-secret-32bytes"

func TestLocalWebChatAuditAttributesMutationsWithoutRecordingSensitiveInputs(t *testing.T) {
	s, st := credentialServer(t)
	local := s.LocalWebChatHandler("owner", "owner", "local_webchat_test", hashSecret(localTestSecret))
	w := httptest.NewRecorder()
	local.ServeHTTP(w, localRequest("GET", "/api/workbench/identity", ""))
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if hasGatewayAuditType(mustGatewayListAudit(t, st, ""), "local_webchat.mutation_admitted") {
		t.Fatal("read request produced a mutation audit")
	}
	w = httptest.NewRecorder()
	local.ServeHTTP(w, localRequest("POST", "/api/sessions?private_query=never-save-query", `{"title":"never-save-body"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: %d %s", w.Code, w.Body.String())
	}
	var admitted []app.AuditEvent
	for _, event := range mustGatewayListAudit(t, st, "") {
		if event.Type == "local_webchat.mutation_admitted" {
			admitted = append(admitted, event)
		}
	}
	if len(admitted) != 1 {
		t.Fatalf("want one admission event, got %#v", admitted)
	}
	event := admitted[0]
	if event.Actor != "owner" || event.Fields["auth_method"] != "local" || event.Fields["local_access_id"] != "local_webchat_test" || event.Fields["owner_id"] != "owner" || event.Fields["route"] != "POST /api/sessions" {
		t.Fatalf("incorrect local attribution: %#v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{localTestSecret, "never-save-body", "never-save-query"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("admission audit persisted private request input")
		}
	}
}

func TestLocalWebChatPrincipalCannotInheritLegacyGlobalOwnerPrivileges(t *testing.T) {
	s, st := credentialServer(t)
	for _, id := range []string{"owner-a", "owner-b"} {
		if _, err := st.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: id, DisplayName: id, Preferences: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
	}
	foreign := storetest.MustCreateSessionWithScope(t, st, "Private", "owner-b", "", "webchat", false)
	approval := app.Approval{ID: "private-approval", Source: app.ApprovalSourceTool, SessionID: foreign.ID, Tool: "workspace.read", Status: app.ApprovalStatusPending, Risk: app.RiskRead, CreatedAt: time.Now().UTC()}
	if _, err := st.SaveApproval(t.Context(), approval); err != nil {
		t.Fatal(err)
	}
	local := s.LocalWebChatHandler("owner-a", "owner-a", "local_webchat_a", hashSecret(localTestSecret))
	for _, item := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/sessions/" + foreign.ID, "", 404},
		{"GET", "/api/sessions/" + foreign.ID + "/messages", "", 404},
		{"DELETE", "/api/sessions/" + foreign.ID, "", 404},
		{"POST", "/api/sessions", `{"owner_id":"owner-b","title":"Wrong"}`, 403},
		{"GET", "/api/profiles/owner-b", "", 404},
		{"PATCH", "/api/profiles/owner-b", `{"display_name":"Wrong"}`, 404},
		{"POST", "/api/approvals/" + approval.ID + "/approve", "{}", 404},
		{"POST", "/api/approvals/" + approval.ID + "/reject", "{}", 404},
		{"GET", "/api/documents/available?session_id=" + foreign.ID, "", 404},
		{"GET", "/api/documents/available?session_id=nonexistent", "", 404},
		{"GET", "/api/documents/file?session_id=" + foreign.ID + "&path=uploads/secret.txt", "", 404},
	} {
		w := httptest.NewRecorder()
		local.ServeHTTP(w, localRequest(item.method, item.path, item.body))
		if w.Code != item.status {
			t.Errorf("%s %s: %d, want %d; %s", item.method, item.path, w.Code, item.status, w.Body.String())
		}
	}
	for _, path := range []string{"/api/owner", "/api/profiles"} {
		w := httptest.NewRecorder()
		local.ServeHTTP(w, localRequest("GET", path, ""))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"owner-a"`) || strings.Contains(w.Body.String(), `"id":"owner-b"`) || strings.Contains(w.Body.String(), `"id":"owner"`) {
			t.Errorf("wrong local profile at %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	local.ServeHTTP(w, localRequest("GET", "/api/approvals", ""))
	if w.Code != 200 || strings.Contains(w.Body.String(), approval.ID) {
		t.Fatalf("foreign approval leaked: %s", w.Body.String())
	}
	request := localRequest("GET", "/api/documents/available", "")
	request = request.WithContext(context.WithValue(request.Context(), requestPrincipalContextKey{}, requestPrincipal{OwnerID: "owner-a", ActorID: "owner-a", LocalAccessID: "local_webchat_a", Authenticated: true}))
	if _, visible, err := s.workspaceRootForRequest(t.Context(), request, ""); err != nil || visible {
		t.Fatalf("local owner inherited global workspace: visible=%v err=%v", visible, err)
	}
	if visible, err := s.approvalVisibleToRequest(t.Context(), request, app.Approval{}); err != nil || visible {
		t.Fatalf("ownerless approval exposed: visible=%v err=%v", visible, err)
	}
}

func localRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://localhost:18794"+path, strings.NewReader(body))
	r.Header.Set(localWebChatIngressHeader, localTestSecret)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestLocalWebChatAuthorityIsPrivateAndDeviceIndependent(t *testing.T) {
	s, st := credentialServer(t)
	registerCredential(t, st, "old", "old-device-token-that-is-long-enough")
	if _, err := st.RevokeClient(t.Context(), "old"); err != nil {
		t.Fatal(err)
	}
	local := s.LocalWebChatHandler("owner", "owner", "local_webchat_test", hashSecret(localTestSecret))
	for _, path := range []string{"/api/workbench/identity", "/api/sessions", "/api/clients"} {
		r := localRequest("GET", path, "")
		w := httptest.NewRecorder()
		local.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("local %s: %d %s", path, w.Code, w.Body.String())
		}
		if path == "/api/workbench/identity" {
			var identity map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &identity); err != nil {
				t.Fatal(err)
			}
			if identity["access_mode"] != "local" || identity["client_id"] != "" || identity["local_access_id"] != "local_webchat_test" || identity["owner_id"] != "owner" {
				t.Fatalf("identity: %#v", identity)
			}
		}
		// Even the real private secret and spoofed source headers grant no TCP authority.
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("X-SparkClaw-Local-WebChat", "1")
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("network accepts private authority at %s: %d", path, w.Code)
		}
	}
	r := localRequest("POST", "/api/clients", `{"client_name":"Recovery"}`)
	r.Header.Set("Idempotency-Key", "local-recovery-00001")
	w := httptest.NewRecorder()
	local.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("local recovery: %d %s", w.Code, w.Body.String())
	}
	old, found, err := st.GetClient(t.Context(), "old")
	if err != nil || !found || old.RevokedAt == nil {
		t.Fatal("revoked Client was revived")
	}
	if _, found, _ := st.GetClient(t.Context(), "local_webchat_test"); found {
		t.Fatal("local authority became a Client")
	}
}

func TestLocalWebChatRejectsExplicitCredentialsAndExcludedRoutes(t *testing.T) {
	s, st := credentialServer(t)
	registerCredential(t, st, "old", "old-device-token-that-is-long-enough")
	_, _ = st.RevokeClient(t.Context(), "old")
	local := s.LocalWebChatHandler("owner", "owner", "local_webchat_test", hashSecret(localTestSecret))
	for _, auth := range []string{"", "Basic x", "Bearer invalid", "Bearer old-device-token-that-is-long-enough"} {
		r := localRequest("GET", "/api/sessions", "")
		r.Header["Authorization"] = []string{auth}
		w := httptest.NewRecorder()
		local.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("explicit auth %q fell back: %d", auth, w.Code)
		}
	}
	for _, path := range []string{"/mcp", "/api/bridge/v1/dispatch", "/api/jingsi/v0/messages/stream", "/api/r3/installations", "/api/r3/hosts/grants", "/api/pairing/start", "/api/future-business-route"} {
		w := httptest.NewRecorder()
		local.ServeHTTP(w, localRequest("POST", path, "{}"))
		if w.Code != 404 {
			t.Fatalf("excluded %s: %d", path, w.Code)
		}
	}
	registerCredential(t, st, "valid", "valid-device-token-that-is-long-enough")
	r := localRequest("GET", "/api/workbench/identity", "")
	r.Header.Set("Authorization", "Bearer valid-device-token-that-is-long-enough")
	w := httptest.NewRecorder()
	local.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"access_mode":"credential"`) || !strings.Contains(w.Body.String(), `"client_id":"valid"`) {
		t.Fatalf("explicit valid identity: %d %s", w.Code, w.Body.String())
	}
}

func TestLocalWebChatEventsSurviveDeviceRevocationAndCancelWithTransport(t *testing.T) {
	s, st := credentialServer(t)
	registerCredential(t, st, "device", "device-token-that-is-long-enough")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	local := s.LocalWebChatHandler("owner", "owner", "local_webchat_test", hashSecret(localTestSecret))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { local.ServeHTTP(w, r.WithContext(ctx)) }))
	defer ts.Close()
	r, _ := http.NewRequest("GET", ts.URL+"/api/workbench/events/stream", nil)
	r.Header.Set(localWebChatIngressHeader, localTestSecret)
	res, err := ts.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("local SSE: %d", res.StatusCode)
	}
	reader := bufio.NewReader(res.Body)
	readEvent := func() string {
		t.Helper()
		var data strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			data.WriteString(line)
			if line == "\n" {
				return data.String()
			}
		}
	}
	_ = readEvent()
	_, _ = st.RevokeClient(t.Context(), "device")
	s.cancelClientConnections("device")
	s.workbenchEvents.publish("owner", "clients", "device")
	if event := readEvent(); !strings.Contains(event, `"resource_id":"device"`) {
		t.Fatal(event)
	}
	cancel()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(reader); done <- err }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("local stream survived transport cancellation")
	}
}

func TestLocalSpeechTicketCannotMoveBetweenEntrancesOrBeBurnedRemotely(t *testing.T) {
	s, st := credentialServer(t)
	s.cfg.Speech.Enabled = true
	s.cfg.Speech.Backend = "openai-http"
	realtime := newFakeGatewayRealtimeSession()
	s.speech = &fakeSpeechTranscriber{startRealtime: func(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error) { return realtime, nil }}
	record := storetest.MustCreateSession(t, st, "Local voice")
	local := httptest.NewServer(s.LocalWebChatHandler("owner", "owner", "local_webchat_test", hashSecret(localTestSecret)))
	defer local.Close()
	remote := httptest.NewServer(s.Handler())
	defer remote.Close()
	r, _ := http.NewRequest("POST", local.URL+"/api/speech/realtime-sessions", strings.NewReader(`{"session_id":"`+record.ID+`","request_id":"local-voice-1","language":"auto"}`))
	r.Header.Set(localWebChatIngressHeader, localTestSecret)
	res, err := local.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var issued struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("ticket: %d", res.StatusCode)
	}
	for _, target := range []string{remote.URL, local.URL} {
		headers := http.Header{}
		headers.Set(localWebChatIngressHeader, localTestSecret)
		headers.Set("Origin", "http://localhost:9999")
		ws, denied, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(target, "http")+issued.URL, headers)
		if ws != nil {
			ws.Close()
		}
		if err == nil || denied == nil || (denied.StatusCode != 401 && denied.StatusCode != 403) {
			t.Fatalf("copied ticket accepted: %v %#v", err, denied)
		}
		denied.Body.Close()
	}
	headers := http.Header{localWebChatIngressHeader: []string{localTestSecret}, "Origin": []string{local.URL}}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(local.URL, "http")+issued.URL, headers)
	if err != nil {
		t.Fatalf("legitimate ticket was consumed remotely: %v", err)
	}
	defer ws.Close()
	var event speech.RealtimeEvent
	if err := ws.ReadJSON(&event); err != nil || event.Event != "ready" {
		t.Fatalf("speech ready: %#v %v", event, err)
	}
}
