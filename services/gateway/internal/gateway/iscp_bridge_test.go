package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestBridgeDispatchFailsClosedWithoutGatewayAuthOrBridgeToken(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	request := bridgeDescribeRequest(t)

	for _, path := range []string{"/api/bridge/v1/dispatch", "/api/bridge/v1/mcp/dispatch"} {
		noAuth := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(request))
		noAuth.RemoteAddr = "127.0.0.1:44000"
		noAuthResponse := httptest.NewRecorder()
		server.Handler().ServeHTTP(noAuthResponse, noAuth)
		if noAuthResponse.Code != http.StatusServiceUnavailable {
			t.Fatalf("no-auth loopback %s returned %d, want 503: %s", path, noAuthResponse.Code, noAuthResponse.Body.String())
		}
	}
}

func TestBridgeDispatchAuthenticatesGatewayBearerAndRejectsRemoteClients(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Gateway.APIToken = "bridge-test-token"
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	request := bridgeDescribeRequest(t)

	remote := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/dispatch", bytes.NewReader(request))
	remote.RemoteAddr = "192.0.2.10:44000"
	remote.Header.Set("X-Forwarded-For", "127.0.0.1")
	remote.Header.Set("Authorization", "Bearer bridge-test-token")
	remoteResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("remote Bridge request returned %d", remoteResponse.Code)
	}

	anonymous := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/dispatch", bytes.NewReader(request))
	anonymous.RemoteAddr = "127.0.0.1:44000"
	anonymousResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(anonymousResponse, anonymous)
	if anonymousResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated Bridge request returned %d, want 401: %s", anonymousResponse.Code, anonymousResponse.Body.String())
	}

	local := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/dispatch", bytes.NewReader(request))
	local.RemoteAddr = "127.0.0.1:44000"
	local.Header.Set("Authorization", "Bearer bridge-test-token")
	localResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(localResponse, local)
	if localResponse.Code != http.StatusOK {
		t.Fatalf("authenticated local Bridge request returned %d: %s", localResponse.Code, localResponse.Body.String())
	}
	var response iscpbridge.Response
	if err := json.Unmarshal(localResponse.Body.Bytes(), &response); err != nil || response.Status != "ok" {
		t.Fatalf("invalid Bridge response: %#v err=%v", response, err)
	}
}

func TestBridgeDispatchRequiresExactBridgeTokenWhenConfigured(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Gateway.APIToken = "owner-api-token"
	cfg.Gateway.BridgeToken = "dedicated-bridge-token"
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	request := bridgeDescribeRequest(t)

	send := func(bearer string) *httptest.ResponseRecorder {
		bridgeRequest := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/dispatch", bytes.NewReader(request))
		bridgeRequest.RemoteAddr = "127.0.0.1:44000"
		if bearer != "" {
			bridgeRequest.Header.Set("Authorization", "Bearer "+bearer)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, bridgeRequest)
		return response
	}

	if response := send(""); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing bridge token returned %d, want 401: %s", response.Code, response.Body.String())
	}
	if response := send("wrong-token"); response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bridge token returned %d, want 401: %s", response.Code, response.Body.String())
	}
	// The bridge token is the exclusive bridge credential: the owner API
	// token does not open the bridge surface.
	if response := send("owner-api-token"); response.Code != http.StatusUnauthorized {
		t.Fatalf("gateway API token opened the bridge route: %d %s", response.Code, response.Body.String())
	}
	response := send("dedicated-bridge-token")
	if response.Code != http.StatusOK {
		t.Fatalf("dedicated bridge token returned %d: %s", response.Code, response.Body.String())
	}
	var decoded iscpbridge.Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.Status != "ok" {
		t.Fatalf("invalid Bridge response: %#v err=%v", decoded, err)
	}

	// The dedicated credential works in the no-auth posture as well.
	cfg.Gateway.APIToken = ""
	server = New(cfg, st, tools, runtime)
	if response := send("dedicated-bridge-token"); response.Code != http.StatusOK {
		t.Fatalf("bridge token in no-auth posture returned %d: %s", response.Code, response.Body.String())
	}
	// But it does not grant access to the owner API surface.
	owner := httptest.NewRequest(http.MethodGet, "/api/mcp-access/tickets", nil)
	owner.RemoteAddr = "127.0.0.1:44000"
	owner.Header.Set("Authorization", "Bearer dedicated-bridge-token")
	cfg.Gateway.APIToken = "owner-api-token"
	server = New(cfg, st, tools, runtime)
	ownerResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(ownerResponse, owner)
	if ownerResponse.Code != http.StatusUnauthorized {
		t.Fatalf("bridge token opened the owner API surface: %d %s", ownerResponse.Code, ownerResponse.Body.String())
	}
}

func TestMCPBridgeDispatchRejectsSpoofedRemoteLoopback(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Gateway.APIToken = "bridge-test-token"
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)

	request := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/mcp/dispatch", bytes.NewBufferString(`{}`))
	request.RemoteAddr = "192.0.2.20:44000"
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	request.Header.Set("Authorization", "Bearer bridge-test-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("spoofed remote MCP Bridge request returned %d: %s", response.Code, response.Body.String())
	}
}

func bridgeDescribeRequest(t *testing.T) []byte {
	t.Helper()
	now := time.Now().UTC()
	raw, err := json.Marshal(iscpbridge.Request{
		ProtocolVersion: iscpbridge.ProtocolVersion,
		Type:            iscpbridge.TypeCapabilitiesDescribe,
		RequestID:       "request-capabilities",
		EndpointID:      "app-device",
		IssuedAt:        now,
		ExpiresAt:       now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
