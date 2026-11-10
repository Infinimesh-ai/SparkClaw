package gateway

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Exercise the installed-client HTTP boundary rather than a Broker-only fixture.
func TestExecutionInstalledClientControlHTTPMatchesClosedRequestShapes(t *testing.T) {
	// macOS exposes its default temporary directory through /var, a symlink.
	// Use the physical fixture directory so browser-control path hardening does
	// not mask the HTTP shape checks this test exercises.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "execution-host-http"
	backend := store.NewMemoryStore()
	const token = "synthetic-execution-host-http-token-long-enough"
	_, err = backend.RegisterClient(t.Context(), app.Client{ID: "host-client", OwnerID: "owner-host", Name: "fixture", TokenHash: hashSecret(token)})
	if err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, backend)
	defer tools.Close()
	instance := New(cfg, backend, tools, agent.Runtime{}, WithExecutions(filepath.Join(root, "execution"), nil))
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
	if code, _ := request("/api/v1/installations", `{"schema_version":1,"installation_id":"`+installation+`"}`, ""); code != 200 {
		t.Fatal("bind", code)
	}
	for _, path := range []string{"grants", "connect", "fences", "reconcile", "old-host/revoke"} {
		route := "/api/v1/browser/hosts/" + path
		if code, body := request(route, `{}`, installation); code != http.StatusNotFound {
			t.Fatalf("removed browser host route %s: %d %s", route, code, body)
		}
	}
	// Scheduled work is owned by the host scheduler; the removed installed
	// lease endpoints must not accept or retain future task context.
	for _, route := range []string{"/api/v1/schedules/lease", "/api/v1/schedules/request/renew", "/api/v1/schedules/request/cancel"} {
		if code, body := request(route, `{}`, installation); code != http.StatusNotFound {
			t.Fatalf("removed lease route %s: %d %s", route, code, body)
		}
	}
}
