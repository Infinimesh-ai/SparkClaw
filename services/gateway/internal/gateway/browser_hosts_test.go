package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Exercise the installed-client HTTP boundary rather than a Broker-only fixture.
func TestExecutionInstalledClientControlHTTPMatchesClosedRequestShapes(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "r3-host-http"
	backend := store.NewMemoryStore()
	const token = "synthetic-r3-host-http-token-long-enough"
	_, err := backend.RegisterClient(t.Context(), app.Client{ID: "host-client", OwnerID: "owner-host", Name: "fixture", TokenHash: hashSecret(token)})
	if err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, backend)
	defer tools.Close()
	instance := New(cfg, backend, tools, agent.Runtime{}, WithExecutions(filepath.Join(root, "r3"), nil))
	instance.BindLifecycleContext(t.Context())
	server := httptest.NewServer(instance.Handler())
	defer server.Close()
	const installation = "11111111-1111-4111-8111-111111111111"
	request := func(route, body, install string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+route, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SparkClaw-Installation", install)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, raw
	}
	if code, _ := request("/api/r3/hosts/grants", `{}`, installation); code != 403 {
		t.Fatal("unbound install", code)
	}
	if code, _ := request("/api/r3/installations", `{"schema_version":1,"installation_id":"`+installation+`"}`, ""); code != 200 {
		t.Fatal("bind", code)
	}
	if code, _ := request("/api/r3/hosts/grants", `{}`, ""); code != 403 {
		t.Fatal("missing install header", code)
	}
	if code, _ := request("/api/r3/hosts/grants", `{"installation_id":"`+installation+`"}`, installation); code != 400 {
		t.Fatal("body broadening", code)
	}
	code, raw := request("/api/r3/hosts/grants", `{}`, installation)
	var grant browserhost.Grant
	if err := json.Unmarshal(raw, &grant); err != nil || code != 200 || grant.HostID == "" {
		t.Fatalf("grant: %d %s %v", code, raw, err)
	}
	if code, _ := request("/api/r3/hosts/"+grant.HostID+"/revoke", `{}`, installation); code != 200 {
		t.Fatal("revoke", code)
	}
	if code, _ := request("/api/r3/hosts/reconcile", `{"installation_id":"`+installation+`","command_id":"fixture","digest":"bad","outcome":"observed_completed"}`, installation); code != 400 {
		t.Fatal("reconcile broadening", code)
	}
	for _, invalid := range []string{`null`, `{} {}`, `{"unsafe":true}`} {
		if code, _ := request("/api/r3/hosts/grants", invalid, installation); code != 400 {
			t.Fatal("invalid grant shape", invalid, code)
		}
	}
	// Scheduled work is owned by the host scheduler; the removed installed
	// lease endpoints must not accept or retain future task context.
	for _, route := range []string{"/api/r3/schedules/lease", "/api/r3/schedules/request/renew", "/api/r3/schedules/request/cancel"} {
		if code, body := request(route, `{}`, installation); code != http.StatusNotFound {
			t.Fatalf("removed lease route %s: %d %s", route, code, body)
		}
	}
}
