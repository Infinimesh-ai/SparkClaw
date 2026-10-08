package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestWorkbenchIdentityRequiresClientBearerAndReturnsBoundIdentity(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "deployment-test"
	repository := store.NewMemoryStore()
	const token = "desktop-client-token-that-is-long-enough"
	if _, err := repository.RegisterClient(t.Context(), app.Client{
		ID: "client-desktop", OwnerID: "owner-a", ActorID: "owner-a", Name: "Desktop", TokenHash: hashSecret(token),
	}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, repository)
	defer tools.Close()
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := httptest.NewServer(New(cfg, repository, tools, runtime).Handler())
	defer server.Close()

	unauthenticated, err := http.Get(server.URL + "/api/workbench/identity")
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthenticated.StatusCode)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/workbench/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("identity status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	var identity map[string]string
	if err := json.NewDecoder(response.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identity["deployment_id"] != "deployment-test" || identity["owner_id"] != "owner-a" || identity["client_id"] != "client-desktop" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}

func TestWorkbenchClientIssuanceIsOwnerBoundAndIdempotent(t *testing.T) {
	cfg, repository, server := newWorkbenchTestServer(t)
	defer server.Close()
	const issuerToken = "issuer-client-token-that-is-long-enough"
	if _, err := repository.RegisterClient(t.Context(), app.Client{
		ID: "client-issuer", OwnerID: "owner-a", ActorID: "actor-a", Name: "Issuer", TokenHash: hashSecret(issuerToken),
	}); err != nil {
		t.Fatal(err)
	}

	issue := func(name string) (*http.Response, map[string]any) {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/clients", bytes.NewBufferString(`{"client_name":`+strconv.Quote(name)+`}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+issuerToken)
		request.Header.Set("Idempotency-Key", "web-client-request-0001")
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		return response, payload
	}

	first, firstPayload := issue("LAN Browser")
	if first.StatusCode != http.StatusCreated || first.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("first issuance status=%d cache=%q payload=%#v", first.StatusCode, first.Header.Get("Cache-Control"), firstPayload)
	}
	issuedToken, _ := firstPayload["token"].(string)
	client, _ := firstPayload["client"].(map[string]any)
	if len(issuedToken) < 32 || client["owner_id"] != "owner-a" || client["actor_id"] != "actor-a" {
		t.Fatalf("issued credential is not principal-bound: %#v", firstPayload)
	}
	second, secondPayload := issue("LAN Browser")
	if second.StatusCode != http.StatusOK || secondPayload["token"] != issuedToken {
		t.Fatalf("idempotent replay status=%d payload=%#v", second.StatusCode, secondPayload)
	}
	clients, err := repository.ListClients(t.Context())
	if err != nil || len(clients) != 2 {
		t.Fatalf("clients after replay = %#v, %v", clients, err)
	}

	identityRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/workbench/identity", nil)
	identityRequest.Header.Set("Authorization", "Bearer "+issuedToken)
	identityResponse, err := http.DefaultClient.Do(identityRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer identityResponse.Body.Close()
	var identity map[string]string
	if err := json.NewDecoder(identityResponse.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identityResponse.StatusCode != http.StatusOK || identity["deployment_id"] != cfg.Gateway.DeploymentID || identity["owner_id"] != "owner-a" {
		t.Fatalf("issued token identity status=%d identity=%#v", identityResponse.StatusCode, identity)
	}

	conflict, _ := issue("Different Browser")
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("changed replay status = %d, want 409", conflict.StatusCode)
	}
}

func TestWorkbenchClientIssuanceRestartDoesNotCreateDuplicateCredential(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	repository := store.NewMemoryStore()
	const issuerToken = "restart-issuer-client-token-that-is-long-enough"
	if _, err := repository.RegisterClient(t.Context(), app.Client{
		ID: "client-restart-issuer", OwnerID: "owner-a", ActorID: "actor-a", Name: "Issuer", TokenHash: hashSecret(issuerToken),
	}); err != nil {
		t.Fatal(err)
	}
	start := func() *httptest.Server {
		tools := toolhub.New(cfg, repository)
		runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
		server := httptest.NewServer(New(cfg, repository, tools, runtime).Handler())
		t.Cleanup(func() { server.Close(); _ = tools.Close() })
		return server
	}
	issue := func(server *httptest.Server) *http.Response {
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/clients", strings.NewReader(`{"client_name":"Restart Browser"}`))
		request.Header.Set("Authorization", "Bearer "+issuerToken)
		request.Header.Set("Idempotency-Key", "restart-client-request-0001")
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	first := issue(start())
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first issuance status=%d", first.StatusCode)
	}
	replayedAfterRestart := issue(start())
	replayedAfterRestart.Body.Close()
	if replayedAfterRestart.StatusCode != http.StatusConflict {
		t.Fatalf("restart replay status=%d, want 409", replayedAfterRestart.StatusCode)
	}
	clients, err := repository.ListClients(t.Context())
	if err != nil || len(clients) != 2 {
		t.Fatalf("restart replay created a duplicate: clients=%#v err=%v", clients, err)
	}
}

func TestWorkbenchClientIssuanceRejectsForgedLocalHeaders(t *testing.T) {
	_, _, server := newWorkbenchTestServer(t)
	defer server.Close()
	for _, headers := range []map[string]string{
		{"Host": "127.0.0.1:18790", "Origin": "http://127.0.0.1:18790"},
		{"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1", "X-SparkClaw-Electron": "true"},
	} {
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/clients", strings.NewReader(`{"client_name":"Forged"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "forged-local-request")
		for name, value := range headers {
			if name == "Host" {
				request.Host = value
			} else {
				request.Header.Set(name, value)
			}
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
			t.Fatalf("forged local headers status=%d, want authentication rejection", response.StatusCode)
		}
	}
}

func TestWorkbenchInvalidationStreamResyncsAndPublishesPersistedOwnerEvents(t *testing.T) {
	_, repository, server := newWorkbenchTestServer(t)
	defer server.Close()
	const token = "event-client-token-that-is-long-enough"
	if _, err := repository.RegisterClient(t.Context(), app.Client{
		ID: "client-events", OwnerID: "owner-a", ActorID: "owner-a", Name: "Events", TokenHash: hashSecret(token),
	}); err != nil {
		t.Fatal(err)
	}

	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(requestContext, http.MethodGet, server.URL+"/api/workbench/events/stream", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	initial := readSSETestBlock(t, reader, 2*time.Second)
	if !strings.Contains(initial, `"reason":"resync"`) {
		t.Fatalf("initial event = %q, want resync", initial)
	}
	if _, err := repository.CreateSessionWithScope(t.Context(), "Shared", "owner-a", "", "web", false); err != nil {
		t.Fatal(err)
	}
	update := ""
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(update, `"category":"sessions"`) && time.Now().Before(deadline) {
		update = readSSETestBlock(t, reader, time.Until(deadline))
	}
	if !strings.Contains(update, `"category":"sessions"`) || strings.Contains(update, `"reason":"resync"`) {
		t.Fatalf("persisted update event = %q", update)
	}
	if _, err := repository.BindEmailMailbox(t.Context(), store.EmailBindCommand{
		EmailCommand: store.EmailCommand{OwnerID: "owner-a", CommandKey: "email-background-0001"},
		Provider:     app.EmailProviderGmail,
		Address:      "owner-a@example.com",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
	emailUpdate := readSSECategory(t, reader, "email", 2*time.Second)
	if strings.Contains(emailUpdate, `"reason":"resync"`) {
		t.Fatalf("email background update unexpectedly required resync: %q", emailUpdate)
	}
	if _, created, err := repository.CreatePassiveNotification(t.Context(), app.PassiveNotification{
		OwnerID: "owner-a", EndpointID: "endpoint-a", IdempotencyKey: "notification-background-0001",
		Fingerprint: "fingerprint-a", Source: "iscp", Kind: app.PassiveNotificationKindDocumentMention,
	}); err != nil || !created {
		t.Fatalf("create passive notification: created=%v err=%v", created, err)
	}
	readSSECategory(t, reader, "notifications", 2*time.Second)
}

func TestWorkbenchEventHubIsolatesOwnersAndResyncsSlowConsumers(t *testing.T) {
	hub := newWorkbenchEventHub()
	ownerAID, ownerAEvents, admitted := hub.subscribe("owner-a")
	if !admitted {
		t.Fatal("owner A subscription was rejected")
	}
	defer hub.unsubscribe(ownerAID)
	ownerBID, ownerBEvents, admitted := hub.subscribe("owner-b")
	if !admitted {
		t.Fatal("owner B subscription was rejected")
	}
	defer hub.unsubscribe(ownerBID)
	<-ownerAEvents
	<-ownerBEvents

	hub.publish("owner-a", "sessions", "session-a")
	select {
	case event := <-ownerAEvents:
		if event.Category != "sessions" || event.ResourceID != "session-a" {
			t.Fatalf("owner A event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("owner A did not receive its event")
	}
	select {
	case event := <-ownerBEvents:
		t.Fatalf("owner B received owner A event: %#v", event)
	default:
	}

	for index := 0; index < workbenchEventQueueSize+4; index++ {
		hub.publish("owner-a", "conversation", strconv.Itoa(index))
	}
	foundResync := false
	for len(ownerAEvents) > 0 {
		if event := <-ownerAEvents; event.Reason == "resync" && event.Category == "all" {
			foundResync = true
		}
	}
	if !foundResync {
		t.Fatal("slow consumer overflow did not receive a resync marker")
	}
}

func TestWorkbenchEventHubBoundsStreamsPerOwner(t *testing.T) {
	hub := newWorkbenchEventHub()
	ids := make([]uint64, 0, workbenchEventMaxPerOwner)
	for index := 0; index < workbenchEventMaxPerOwner; index++ {
		id, _, admitted := hub.subscribe("owner-a")
		if !admitted {
			t.Fatalf("subscription %d was rejected", index)
		}
		ids = append(ids, id)
	}
	if _, _, admitted := hub.subscribe("owner-a"); admitted {
		t.Fatal("subscription above the per-Owner limit was admitted")
	}
	if _, _, admitted := hub.subscribe("owner-b"); !admitted {
		t.Fatal("another Owner was incorrectly subject to owner A's limit")
	}
	for _, id := range ids {
		hub.unsubscribe(id)
	}
}

func TestSessionRoutesDeriveOwnerFromAuthenticatedClient(t *testing.T) {
	_, repository, server := newWorkbenchTestServer(t)
	defer server.Close()
	const ownerAToken = "owner-a-client-token-that-is-long-enough"
	const ownerBToken = "owner-b-client-token-that-is-long-enough"
	for _, candidate := range []app.Client{
		{ID: "client-a", OwnerID: "owner-a", ActorID: "owner-a", Name: "A", TokenHash: hashSecret(ownerAToken)},
		{ID: "client-b", OwnerID: "owner-b", ActorID: "owner-b", Name: "B", TokenHash: hashSecret(ownerBToken)},
	} {
		if _, err := repository.RegisterClient(t.Context(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := repository.CreateSessionWithScope(t.Context(), "Private", "owner-b", "", "webchat", false)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/sessions/" + foreign.ID,
		"/api/sessions/" + foreign.ID + "/messages",
		"/api/sessions/" + foreign.ID + "/events",
		"/api/sessions/" + foreign.ID + "/tool-calls",
		"/api/sessions/" + foreign.ID + "/model-calls",
		"/api/sessions/" + foreign.ID + "/audit",
		"/api/sessions/" + foreign.ID + "/episodes",
	} {
		request, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		request.Header.Set("Authorization", "Bearer "+ownerAToken)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("cross-owner %s status=%d, want 404", path, response.StatusCode)
		}
	}

	create, _ := http.NewRequest(http.MethodPost, server.URL+"/api/sessions", strings.NewReader(`{"title":"Wrong","owner_id":"owner-b"}`))
	create.Header.Set("Authorization", "Bearer "+ownerAToken)
	create.Header.Set("Content-Type", "application/json")
	createResponse, err := http.DefaultClient.Do(create)
	if err != nil {
		t.Fatal(err)
	}
	createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-owner create status=%d, want 403", createResponse.StatusCode)
	}

	list, _ := http.NewRequest(http.MethodGet, server.URL+"/api/sessions?owner_id=owner-b", nil)
	list.Header.Set("Authorization", "Bearer "+ownerAToken)
	listResponse, err := http.DefaultClient.Do(list)
	if err != nil {
		t.Fatal(err)
	}
	defer listResponse.Body.Close()
	var payload struct {
		Sessions []app.Session `json:"sessions"`
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Sessions) != 0 {
		t.Fatalf("query owner override exposed sessions: %#v", payload.Sessions)
	}
}

func TestOwnerProfilesAndMemoriesAreScopedToAuthenticatedClient(t *testing.T) {
	_, repository, server := newWorkbenchTestServer(t)
	defer server.Close()
	const ownerAToken = "owner-a-scope-token-that-is-long-enough"
	const ownerBToken = "owner-b-scope-token-that-is-long-enough"
	for _, candidate := range []app.Client{
		{ID: "client-scope-a", OwnerID: "owner-a", ActorID: "owner-a", Name: "A", TokenHash: hashSecret(ownerAToken)},
		{ID: "client-scope-b", OwnerID: "owner-b", ActorID: "owner-b", Name: "B", TokenHash: hashSecret(ownerBToken)},
	} {
		if _, err := repository.RegisterClient(t.Context(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range []app.OwnerProfile{
		{ID: "owner-a", DisplayName: "Owner A", Preferences: map[string]string{}},
		{ID: "owner-b", DisplayName: "Owner B", Preferences: map[string]string{}},
	} {
		if _, err := repository.SaveOwnerProfile(t.Context(), profile); err != nil {
			t.Fatal(err)
		}
	}
	ownerBSession, err := repository.CreateSessionWithScope(t.Context(), "B", "owner-b", "", "webchat", false)
	if err != nil {
		t.Fatal(err)
	}
	ownerBRun, err := repository.SaveRun(t.Context(), app.AgentRun{
		ID: "run-owner-b", SessionID: ownerBSession.ID, State: "completed", StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptedCandidate, err := repository.AddMemoryCandidate(t.Context(), app.MemoryCandidate{
		ID: "candidate-owner-b-accepted", SessionID: ownerBSession.ID, RunID: ownerBRun.ID,
		Kind: "profile", Content: "private owner B memory", Sensitivity: "normal", Status: "pending", Reason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, ownerBMemory, err := repository.ResolveMemoryCandidate(t.Context(), acceptedCandidate.ID, "accepted")
	if err != nil || ownerBMemory == nil {
		t.Fatalf("resolve owner B memory: %#v err=%v", ownerBMemory, err)
	}
	pendingCandidate, err := repository.AddMemoryCandidate(t.Context(), app.MemoryCandidate{
		ID: "candidate-owner-b-pending", SessionID: ownerBSession.ID, RunID: ownerBRun.ID,
		Kind: "profile", Content: "pending owner B memory", Sensitivity: "normal", Status: "pending", Reason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, path, body string) (*http.Response, map[string]any) {
		request, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+ownerAToken)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		payload := map[string]any{}
		_ = json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		return response, payload
	}
	ownerResponse, ownerPayload := do(http.MethodGet, "/api/owner", "")
	if ownerResponse.StatusCode != http.StatusOK || ownerPayload["id"] != "owner-a" {
		t.Fatalf("owner profile status=%d payload=%#v", ownerResponse.StatusCode, ownerPayload)
	}
	profilesResponse, profilesPayload := do(http.MethodGet, "/api/profiles", "")
	profiles, _ := profilesPayload["profiles"].([]any)
	if profilesResponse.StatusCode != http.StatusOK || len(profiles) != 1 || profiles[0].(map[string]any)["id"] != "owner-a" {
		t.Fatalf("visible profiles status=%d payload=%#v", profilesResponse.StatusCode, profilesPayload)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/profiles/owner-b", ""},
		{http.MethodPatch, "/api/profiles/owner-b", `{"display_name":"Changed","preferences":{}}`},
		{http.MethodPost, "/api/memory-candidates/" + pendingCandidate.ID + "/accept", "{}"},
		{http.MethodPost, "/api/memories/" + ownerBMemory.ID + "/update", `{"kind":"profile","content":"changed"}`},
		{http.MethodPost, "/api/memories/" + ownerBMemory.ID + "/delete", "{}"},
	} {
		response, _ := do(request.method, request.path, request.body)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("cross-owner %s %s status=%d, want 404", request.method, request.path, response.StatusCode)
		}
	}
	memoriesResponse, memoriesPayload := do(http.MethodGet, "/api/memories", "")
	if memoriesResponse.StatusCode != http.StatusOK || len(memoriesPayload["memories"].([]any)) != 0 {
		t.Fatalf("cross-owner memories leaked: %#v", memoriesPayload)
	}
	candidatesResponse, candidatesPayload := do(http.MethodGet, "/api/memory-candidates", "")
	if candidatesResponse.StatusCode != http.StatusOK || len(candidatesPayload["memory_candidates"].([]any)) != 0 {
		t.Fatalf("cross-owner candidates leaked: %#v", candidatesPayload)
	}
}

func TestApprovalsFeedbackAndDocumentsAreScopedToAuthenticatedClient(t *testing.T) {
	_, repository, server := newWorkbenchTestServer(t)
	defer server.Close()
	const ownerAToken = "owner-a-resource-token-that-is-long-enough"
	const ownerBToken = "owner-b-resource-token-that-is-long-enough"
	for _, candidate := range []app.Client{
		{ID: "client-resource-a", OwnerID: "owner-a", ActorID: "owner-a", Name: "A", TokenHash: hashSecret(ownerAToken)},
		{ID: "client-resource-b", OwnerID: "owner-b", ActorID: "owner-b", Name: "B", TokenHash: hashSecret(ownerBToken)},
	} {
		if _, err := repository.RegisterClient(t.Context(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	ownerARoot := filepath.Join(t.TempDir(), "owner-a")
	ownerBRoot := filepath.Join(t.TempDir(), "owner-b")
	for _, profile := range []app.OwnerProfile{
		{ID: "owner-a", DisplayName: "Owner A", WorkspaceRoot: ownerARoot, Preferences: map[string]string{}},
		{ID: "owner-b", DisplayName: "Owner B", WorkspaceRoot: ownerBRoot, Preferences: map[string]string{}},
	} {
		if _, err := repository.SaveOwnerProfile(t.Context(), profile); err != nil {
			t.Fatal(err)
		}
	}
	ownerBSession, err := repository.CreateSessionWithScope(t.Context(), "Private B", "owner-b", ownerBRoot, "webchat", false)
	if err != nil {
		t.Fatal(err)
	}
	ownerBRun, err := repository.SaveRun(t.Context(), app.AgentRun{
		ID: "run-resource-owner-b", SessionID: ownerBSession.ID, State: "completed", StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	approval := app.Approval{
		ID: "approval-resource-owner-b", Source: app.ApprovalSourceTool, SessionID: ownerBSession.ID,
		RunID: ownerBRun.ID, Tool: "workspace.read", Risk: app.RiskRead,
		Status: app.ApprovalStatusPending, Summary: "private approval", CreatedAt: time.Now().UTC(),
	}
	if _, err := repository.SaveApproval(t.Context(), approval); err != nil {
		t.Fatal(err)
	}
	toolCall, err := repository.SaveToolCall(t.Context(), app.ToolCall{
		ID: "tool-call-resource-owner-b", SessionID: ownerBSession.ID, RunID: ownerBRun.ID,
		Tool: "workspace.read", Status: app.ToolCallStatusCompleted, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	privateFile := filepath.Join(ownerBRoot, "uploads", "private.txt")
	if err := os.MkdirAll(filepath.Dir(privateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateFile, []byte("owner B only"), 0o644); err != nil {
		t.Fatal(err)
	}

	do := func(method, path, body, contentType string) *http.Response {
		request, requestErr := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+ownerAToken)
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return response
	}
	list := do(http.MethodGet, "/api/approvals", "", "")
	var listed struct {
		Approvals []app.Approval `json:"approvals"`
	}
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil {
		list.Body.Close()
		t.Fatal(err)
	}
	list.Body.Close()
	if list.StatusCode != http.StatusOK || len(listed.Approvals) != 0 {
		t.Fatalf("cross-owner approvals leaked: status=%d approvals=%#v", list.StatusCode, listed.Approvals)
	}
	for _, request := range []struct{ method, path, body, contentType string }{
		{http.MethodPost, "/api/approvals/" + approval.ID + "/approve", `{}`, "application/json"},
		{http.MethodPost, "/api/approvals/" + approval.ID + "/reject", `{}`, "application/json"},
		{http.MethodPost, "/api/approvals/" + approval.ID + "/modify", `{"arguments":{"path":"other"}}`, "application/json"},
		{http.MethodGet, "/api/runs/" + ownerBRun.ID + "/feedback", "", ""},
		{http.MethodPost, "/api/runs/" + ownerBRun.ID + "/feedback", `{"rating":"up"}`, "application/json"},
		{http.MethodGet, "/api/documents/available?session_id=" + ownerBSession.ID, "", ""},
		{http.MethodGet, "/api/documents/file?path=uploads%2Fprivate.txt&session_id=" + ownerBSession.ID, "", ""},
		{http.MethodGet, "/api/tool-calls/" + toolCall.ID, "", ""},
		{http.MethodPost, "/api/tools/workspace.read/invoke", `{"session_id":"` + ownerBSession.ID + `","args":{}}`, "application/json"},
	} {
		response := do(request.method, request.path, request.body, request.contentType)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("cross-owner %s %s status=%d, want 404", request.method, request.path, response.StatusCode)
		}
	}

	var uploadBody bytes.Buffer
	upload := multipart.NewWriter(&uploadBody)
	part, err := upload.CreateFormFile("file", "private.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("must not be written")); err != nil {
		t.Fatal(err)
	}
	if err := upload.WriteField("session_id", ownerBSession.ID); err != nil {
		t.Fatal(err)
	}
	if err := upload.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/documents/upload", &uploadBody)
	uploadRequest.Header.Set("Authorization", "Bearer "+ownerAToken)
	uploadRequest.Header.Set("Content-Type", upload.FormDataContentType())
	uploadResponse, err := http.DefaultClient.Do(uploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	uploadResponse.Body.Close()
	if uploadResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-owner upload status=%d, want 404", uploadResponse.StatusCode)
	}
}

func TestSessionMessageAdmissionRejectsConcurrentSubmission(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	repository := store.NewMemoryStore()
	session, err := repository.CreateSession(t.Context(), "Concurrent")
	if err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, repository)
	defer tools.Close()
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, repository, tools, runtime)
	started := make(chan struct{})
	release := make(chan struct{})
	executionDone := make(chan struct{})
	server.streamMessage = func(_ context.Context, sessionID, _ string, _ []agent.MessageAttachment, _ app.MessageIngressContext, _ agent.StreamHandler) (agent.Result, error) {
		close(started)
		<-release
		close(executionDone)
		return agent.Result{Run: app.AgentRun{SessionID: sessionID}, Message: app.Message{SessionID: sessionID}}, nil
	}

	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstRequest := httptest.NewRequest(http.MethodPost, "/api/sessions/"+session.ID+"/messages/stream", strings.NewReader(`{"request_id":"11111111-1111-4111-8111-111111111111","content":"first"}`)).WithContext(firstContext)
	firstRequest.SetPathValue("id", session.ID)
	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		server.postMessageStream(firstResponse, firstRequest)
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first message was not admitted")
	}
	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnected stream handler did not return")
	}

	secondRequest := httptest.NewRequest(http.MethodPost, "/api/sessions/"+session.ID+"/messages/stream", strings.NewReader(`{"request_id":"22222222-2222-4222-8222-222222222222","content":"second"}`))
	secondRequest.SetPathValue("id", session.ID)
	secondResponse := httptest.NewRecorder()
	server.postMessageStream(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusConflict {
		t.Fatalf("concurrent message status=%d body=%s", secondResponse.Code, secondResponse.Body.String())
	}
	close(release)
	select {
	case <-executionDone:
	case <-time.After(2 * time.Second):
		t.Fatal("detached message execution did not finish")
	}
}

func newWorkbenchTestServer(t *testing.T) (config.Config, *store.MemoryStore, *httptest.Server) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "deployment-test"
	repository := store.NewMemoryStore()
	tools := toolhub.New(cfg, repository)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	gateway := New(cfg, repository, tools, runtime)
	lifecycle, cancel := context.WithCancel(context.Background())
	gateway.BindLifecycleContext(lifecycle)
	t.Cleanup(cancel)
	return cfg, repository, httptest.NewServer(gateway.Handler())
}

func readSSETestBlock(t *testing.T, reader *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	result := make(chan string, 1)
	go func() {
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				result <- ""
				return
			}
			if strings.TrimSpace(line) == "" {
				result <- strings.Join(lines, "")
				return
			}
			lines = append(lines, line)
		}
	}()
	select {
	case block := <-result:
		return block
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE event")
		return ""
	}
}

func readSSECategory(t *testing.T, reader *bufio.Reader, category string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		block := readSSETestBlock(t, reader, time.Until(deadline))
		if strings.Contains(block, `"category":"`+category+`"`) {
			return block
		}
	}
	t.Fatalf("timed out waiting for %s invalidation", category)
	return ""
}
