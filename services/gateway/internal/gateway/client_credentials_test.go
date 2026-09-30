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

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/gorilla/websocket"
)

func credentialServer(t *testing.T) (*Server, *store.MemoryStore) {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "test-deployment"
	repository := store.NewMemoryStore()
	tools := toolhub.New(cfg, repository)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	return New(cfg, repository, tools, runtime), repository
}

func credentialRequest(t *testing.T, method, url, token, key, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func registerCredential(t *testing.T, st *store.MemoryStore, id, token string) {
	t.Helper()
	if _, err := st.RegisterClient(t.Context(), app.Client{ID: id, OwnerID: "owner", ActorID: "owner", Name: id, TokenHash: hashSecret(token)}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalManagementCredentialCannotAuthenticateNetworkOrBusinessRoutes(t *testing.T) {
	s, st := credentialServer(t)
	const managerToken = "private-local-management-test-only-token"
	registerCredential(t, st, "revoked-user", "revoked-user-test-only-token")
	if _, err := st.RevokeClient(t.Context(), "revoked-user"); err != nil {
		t.Fatal(err)
	}
	network := httptest.NewServer(s.Handler())
	defer network.Close()
	local := httptest.NewServer(s.LocalManagementHandler("owner", "owner", "local_management_test", hashSecret(managerToken)))
	defer local.Close()
	for _, route := range []string{"/api/workbench/identity", "/api/clients", "/api/sessions"} {
		res := credentialRequest(t, "GET", network.URL+route, managerToken, "", "")
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("network route %s accepts management token: %d", route, res.StatusCode)
		}
	}
	res := credentialRequest(t, "GET", local.URL+"/api/sessions", managerToken, "", "")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("management business route status=%d", res.StatusCode)
	}
	res = credentialRequest(t, "GET", local.URL+"/api/workbench/identity", managerToken, "", "")
	var identity map[string]string
	if err := json.NewDecoder(res.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || identity["client_id"] != "local_management_test" {
		t.Fatalf("local identity=%#v status=%d", identity, res.StatusCode)
	}
	res = credentialRequest(t, "POST", local.URL+"/api/clients", managerToken, "local-recovery-0001", `{"client_name":"Replacement"}`)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("management issuance after all user devices revoked = %d", res.StatusCode)
	}
	old, found, err := st.GetClient(t.Context(), "revoked-user")
	if err != nil || !found || old.RevokedAt == nil {
		t.Fatalf("revoked identity revived: %#v %v", old, err)
	}
}

func TestClientRevocationClosesWorkbenchStreamAndLeavesOtherDeviceAvailable(t *testing.T) {
	s, st := credentialServer(t)
	registerCredential(t, st, "client-a", "client-a-test-only-token")
	registerCredential(t, st, "client-b", "client-b-test-only-token")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	stream := credentialRequest(t, "GET", ts.URL+"/api/workbench/events/stream", "client-a-test-only-token", "", "")
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	if block := readSSETestBlock(t, reader, time.Second); !strings.Contains(block, "invalidation") {
		t.Fatalf("initial stream: %q", block)
	}
	revoked := credentialRequest(t, "POST", ts.URL+"/api/clients/client-a/revoke", "client-b-test-only-token", "", `{}`)
	revoked.Body.Close()
	if revoked.StatusCode != 200 {
		t.Fatalf("revoke status=%d", revoked.StatusCode)
	}
	closed := make(chan error, 1)
	go func() { _, err := reader.ReadString('\n'); closed <- err }()
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("stream not closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revoked stream remained active")
	}
	for token, want := range map[string]int{"client-a-test-only-token": 401, "client-b-test-only-token": 200} {
		res := credentialRequest(t, "GET", ts.URL+"/api/workbench/identity", token, "", "")
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("identity status=%d want=%d", res.StatusCode, want)
		}
	}
}

func TestConnectionAttachmentRejectsPreviouslyRevokedClient(t *testing.T) {
	s, st := credentialServer(t)
	registerCredential(t, st, "client-race", "client-race-test-only-token")
	if _, err := st.RevokeClient(t.Context(), "client-race"); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := s.clientConnectionContext(t.Context(), "client-race")
	defer release()
	if err == nil || ctx.Err() == nil {
		t.Fatal("revoked attachment remained active")
	}
	if len(s.clientConnections.active) != 0 {
		t.Fatal("revoked attachment registry leak")
	}
}

func TestIssuanceExpiryAndRestartExposeExactUnrecoverableDeviceWithoutDuplication(t *testing.T) {
	for _, cause := range []string{"expiry", "restart"} {
		t.Run(cause, func(t *testing.T) {
			s, st := credentialServer(t)
			registerCredential(t, st, "issuer", "issuer-test-only-token")
			issue := func(server *Server) *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{"client_name":"New Mac"}`))
				req.Header.Set("Authorization", "Bearer issuer-test-only-token")
				req.Header.Set("Idempotency-Key", "issuance-expiry-0001")
				res := httptest.NewRecorder()
				server.Handler().ServeHTTP(res, req)
				return res
			}
			first := issue(s)
			if first.Code != 201 {
				t.Fatalf("first issue=%d %s", first.Code, first.Body.String())
			}
			if cause == "expiry" {
				s.clientIssuance.mu.Lock()
				s.clientIssuance.pending["owner\x00issuance-expiry-0001"].expiresAt = time.Now().Add(-time.Second)
				s.clientIssuance.mu.Unlock()
			} else {
				s = New(s.cfg, st, s.tools, s.runtime)
			}
			retry := issue(s)
			var result map[string]string
			if err := json.Unmarshal(retry.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if retry.Code != 409 || result["code"] != "CLIENT_CREDENTIAL_UNRECOVERABLE" || result["client_id"] != stableIssuedClientID("owner", "issuance-expiry-0001") {
				t.Fatalf("unrecoverable retry=%d %#v", retry.Code, result)
			}
			clients, err := st.ListClients(t.Context())
			if err != nil || len(clients) != 2 {
				t.Fatalf("duplicate clients=%#v %v", clients, err)
			}
		})
	}
}

func TestRevokedIssuedClientNeverRevealsCachedTokenAgain(t *testing.T) {
	s, st := credentialServer(t)
	registerCredential(t, st, "issuer", "issuer-test-only-token")
	issue := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{"client_name":"New Mac"}`))
		req.Header.Set("Authorization", "Bearer issuer-test-only-token")
		req.Header.Set("Idempotency-Key", "revoked-issued-0001")
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		return res
	}
	first := issue()
	if first.Code != 201 {
		t.Fatal(first.Code)
	}
	if _, err := st.RevokeClient(t.Context(), stableIssuedClientID("owner", "revoked-issued-0001")); err != nil {
		t.Fatal(err)
	}
	retry := issue()
	if retry.Code != 409 || strings.Contains(retry.Body.String(), `"token"`) || !strings.Contains(retry.Body.String(), "CLIENT_REVOKED") {
		t.Fatalf("revoked retry=%d %s", retry.Code, retry.Body.String())
	}
}

func TestRevocationClosesSpeechSocketAndInvalidatesUnusedTickets(t *testing.T) {
	s, st := credentialServer(t)
	s.cfg.Speech.Enabled = true
	s.cfg.Speech.Backend = "openai-http"
	s.cfg.Speech.Model = "test-asr"
	s.cfg.Speech.MaxAudioSeconds = 60
	registerCredential(t, st, "voice-user", "voice-user-test-only-token")
	registerCredential(t, st, "other-user", "other-user-test-only-token")
	var sessions []*fakeGatewayRealtimeSession
	s.speech = &fakeSpeechTranscriber{status: speech.Status{Enabled: true, Ready: true, SupportsStreaming: true}, startRealtime: func(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error) {
		rt := newFakeGatewayRealtimeSession()
		sessions = append(sessions, rt)
		return rt, nil
	}}
	record := storetest.MustCreateSession(t, st, "Voice")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	issue := func(requestID string) string {
		res := credentialRequest(t, "POST", ts.URL+"/api/speech/realtime-sessions", "voice-user-test-only-token", "", `{"session_id":"`+record.ID+`","request_id":"`+requestID+`","language":"auto"}`)
		defer res.Body.Close()
		if res.StatusCode != 201 {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("ticket=%d %s", res.StatusCode, body)
		}
		var payload struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return payload.URL
	}
	active := issue("voice-revocation-1")
	pending := issue("voice-revocation-2")
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+active, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	var event speech.RealtimeEvent
	if err := ws.ReadJSON(&event); err != nil || event.Event != "ready" {
		t.Fatalf("ready=%#v err=%v", event, err)
	}
	res := credentialRequest(t, "POST", ts.URL+"/api/clients/voice-user/revoke", "other-user-test-only-token", "", `{}`)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for count := 0; ; count++ {
		_, payload, readErr := ws.ReadMessage()
		if readErr != nil {
			break
		}
		if count > 2 || strings.Contains(string(payload), `"event":"partial"`) || strings.Contains(string(payload), `"event":"final"`) {
			t.Fatalf("revoked websocket emitted business content: %s", payload)
		}
	}
	for _, rt := range sessions {
		select {
		case <-rt.closed:
		case <-time.After(2 * time.Second):
			t.Fatal("revoked speech session remained active")
		}
	}
	res = credentialRequest(t, "GET", ts.URL+pending, "", "", "")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("revoked unused ticket=%d", res.StatusCode)
	}
}
