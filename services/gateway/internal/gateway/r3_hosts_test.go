package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3browser"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Exercise the installed-client HTTP boundary rather than a Broker-only fixture.
func TestR3InstalledClientControlHTTPMatchesClosedRequestShapes(t *testing.T) {
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
	instance := New(cfg, backend, tools, agent.Runtime{}, WithR3Executions(filepath.Join(root, "r3"), nil))
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
	var grant r3browser.Grant
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
	e := r3execution.Envelope{SchemaVersion: 1, DeploymentID: cfg.Gateway.DeploymentID, OwnerID: "owner-host", ClientID: "host-client", InstallationID: installation, ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []r3execution.Message{{Role: "user", Content: "synthetic future task"}}}
	contextJSON, _ := json.Marshal(e)
	leaseJSON, _ := json.Marshal(map[string]any{"schema_version": 1, "due_at": time.Now().UTC().Add(time.Hour), "context": string(contextJSON), "digest": r3execution.Digest(contextJSON)})
	if code, body := request("/api/r3/schedules/lease", string(leaseJSON), installation); code != 200 || !bytes.Contains(body, []byte(`"state":"leased"`)) {
		t.Fatalf("schedule registration %d %s", code, body)
	}
	if code, _ := request("/api/r3/schedules/"+e.RequestID+"/renew", `{}`, installation); code != 200 {
		t.Fatal("renew", code)
	}
	if code, _ := request("/api/r3/schedules/"+e.RequestID+"/cancel", `{}`, installation); code != 200 {
		t.Fatal("cancel", code)
	}

}
