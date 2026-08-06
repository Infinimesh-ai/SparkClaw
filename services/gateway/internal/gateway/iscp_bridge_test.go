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

func TestBridgeDispatchSupportsNoAuthLoopbackAndRejectsRemoteClients(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	request := bridgeDescribeRequest(t)

	noAuth := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/dispatch", bytes.NewReader(request))
	noAuth.RemoteAddr = "127.0.0.1:44000"
	noAuthResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(noAuthResponse, noAuth)
	if noAuthResponse.Code != http.StatusOK {
		t.Fatalf("no-auth loopback Gateway returned %d: %s", noAuthResponse.Code, noAuthResponse.Body.String())
	}

	cfg.Gateway.APIToken = "bridge-test-token"
	server = New(cfg, st, tools, runtime)
	remote := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/dispatch", bytes.NewReader(request))
	remote.RemoteAddr = "192.0.2.10:44000"
	remote.Header.Set("Authorization", "Bearer bridge-test-token")
	remoteResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(remoteResponse, remote)
	if remoteResponse.Code != http.StatusForbidden {
		t.Fatalf("remote Bridge request returned %d", remoteResponse.Code)
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
