package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestExecutionRealWorkflowUsesOnlyClientContextAndNoLegacyPersistence(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	cfg.Gateway.DeploymentID = "r3-deployment"
	local := store.NewMemoryStore()
	const token = "synthetic-r3-device-token-that-is-long-enough"
	if _, err := local.RegisterClient(t.Context(), app.Client{ID: "r3-client", OwnerID: "owner-a", Name: "workbench", TokenHash: hashSecret(token)}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, local)
	defer tools.Close()
	runtime := agent.NewRuntime(local, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	instance := New(cfg, local, tools, runtime, WithExecutions(filepath.Join(root, "r3"), nil))
	instance.BindLifecycleContext(t.Context())
	server := httptest.NewServer(instance.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	const install = "11111111-1111-4111-8111-111111111111"
	do := func(method, path string, body []byte, credential string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("X-SparkClaw-Installation", install)
		req.Header.Set("X-R3-Digest", execution.Digest(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
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
	body := []byte(`{"schema_version":1,"installation_id":"` + install + `"}`)
	if code, _ := do("POST", "/api/r3/installations", body, token); code != 200 {
		t.Fatal("bind", code)
	}
	wrong := []byte(`{"schema_version":1,"installation_id":"22222222-2222-4222-8222-222222222222"}`)
	if code, _ := do("POST", "/api/r3/installations", wrong, token); code != 409 {
		t.Fatal("rebind", code)
	}
	e := execution.Envelope{SchemaVersion: 1, DeploymentID: cfg.Gateway.DeploymentID, OwnerID: "owner-a", ClientID: "r3-client", InstallationID: install, ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []execution.Message{{Role: "assistant", Content: "synthetic local history"}, {Role: "user", Content: "hello synthetic workbench private context"}}}
	raw, _ := json.Marshal(e)
	if code, output := do("POST", "/api/r3/executions", raw, token); code != 202 {
		t.Fatalf("submit %d %s", code, output)
	}
	instance.executions.Wait()
	code, raw := do("GET", "/api/r3/executions/"+e.RequestID, nil, token)
	var status execution.Status
	_ = json.Unmarshal(raw, &status)
	if code != 200 || status.State != "completed" || status.Result == nil {
		t.Fatalf("workflow: %d %s", code, raw)
	}
	sessions, err := local.ListSessions(t.Context())
	if err != nil || len(sessions) != 0 {
		t.Fatal("shared history leak", sessions, err)
	}
	for _, dir := range []string{cfg.Storage.TraceDir, cfg.Storage.ArtifactDir, cfg.Workspaces.DefaultRoot} {
		_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				data, _ := os.ReadFile(path)
				if strings.Contains(string(data), "synthetic workbench private context") {
					t.Errorf("persistent body leak: %s", path)
				}
			}
			return nil
		})
	}
	if code, _ = do("GET", "/api/r3/executions/"+e.RequestID, nil, "wrong-token"); code != 401 {
		t.Fatal("bad credential", code)
	}
	ack, _ := json.Marshal(map[string]any{"sequence": status.Result.Sequence, "digest": status.Result.Digest, "durable": true})
	if code, _ = do("POST", "/api/r3/executions/"+e.RequestID+"/ack", ack, token); code != 200 {
		t.Fatal("ACK", code)
	}
	if code, raw = do("GET", "/api/r3/executions/"+e.RequestID, nil, token); code != 200 || bytes.Contains(raw, []byte(`"result"`)) || !bytes.Contains(raw, []byte(`"state":"delivered"`)) {
		t.Fatalf("cleanup: %d %s", code, raw)
	}
	ledger, _ := os.ReadFile(filepath.Join(root, "r3", "control.json"))
	if bytes.Contains(ledger, []byte("private context")) || bytes.Contains(ledger, []byte("local history")) {
		t.Fatal("control ledger body leak")
	}
}
