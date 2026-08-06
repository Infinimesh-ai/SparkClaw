package iscpbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGatewayClientAllowsEmptyTokenForLoopbackNoAuthGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected authorization header %q", got)
		}
		var request Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(newResponse(request, "ok", DefaultManifest(), nil, nil, time.Now().UTC()))
	}))
	defer server.Close()
	client, err := NewGatewayClient(GatewayClientOptions{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	response, err := client.Dispatch(context.Background(), Request{
		ProtocolVersion: ProtocolVersion, Type: TypeCapabilitiesDescribe,
		RequestID: "describe-no-auth", EndpointID: "bridge-peer",
		IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil || response.Status != "ok" {
		t.Fatalf("no-auth dispatch = %#v, %v", response, err)
	}
}

func TestBridgeConfigAllowsNoGatewayTokenFile(t *testing.T) {
	config := Config{
		Profile: ProfileLocalLab, IdentityDirectory: "/identity",
		IdentityKeyBackend: IdentityKeyBackendFile, EnrollmentFile: "/enrollment",
		Gateway: GatewayConfig{BaseURL: "http://127.0.0.1:18789"},
	}
	if err := config.normalizeAndValidate(); err != nil {
		t.Fatalf("no-auth loopback config was rejected: %v", err)
	}
	if token, err := config.LoadGatewayToken(); err != nil || token != "" {
		t.Fatalf("optional Gateway token = %q, %v", token, err)
	}
}

func TestPublishedSchemaContainsAllRequestTypes(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	schemaPath := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../../../packages/protocol/iscp-bridge.v1.schema.json"))
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("published Bridge schema is not valid JSON")
	}
	for requestType := range supportedRequestTypes {
		if !strings.Contains(string(raw), `"`+requestType+`"`) {
			t.Fatalf("published Bridge schema is missing %q", requestType)
		}
	}
}

func TestProductionConfigRejectsFileIdentityKey(t *testing.T) {
	config := Config{
		Profile: ProfileProduction, IdentityDirectory: "/identity", IdentityKeyBackend: IdentityKeyBackendFile,
		EnrollmentFile: "/enrollment", Gateway: GatewayConfig{TokenFile: "/token"},
	}
	if err := config.normalizeAndValidate(); err == nil {
		t.Fatal("production config accepted file identity key backend")
	}
}

func TestMockHandlerRequiresClientToken(t *testing.T) {
	if _, err := NewMockHandler(&GatewayClient{}, ""); err == nil {
		t.Fatal("mock handler accepted an empty client token")
	}
	for _, address := range []string{"0.0.0.0:18792", "192.0.2.1:18792"} {
		if err := ValidateMockListenAddress(address); err == nil {
			t.Fatalf("mock accepted non-loopback address %q", address)
		}
	}
}
