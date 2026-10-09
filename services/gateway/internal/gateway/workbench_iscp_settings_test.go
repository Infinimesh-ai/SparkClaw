package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connector"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationconfig"
)

func TestISCPDomainConnectorChangesEffectiveRuntimeAndReplaysOnce(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	server.cfg.Tools.Notifications.Channels["alpha"] = config.NotificationChannelConfig{Enabled: false, Provider: "alpha-http"}
	registry := connector.NewRegistry(server.cfg, repo)
	if err := registry.Register(connector.Registration{Channel: "alpha", SetupKind: app.ConnectorSetupSecret, Binding: &genericBindingAdapter{}}); err != nil {
		t.Fatal(err)
	}
	server.connectors = registry
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest("settings.connectors.patch", []byte(`{"enabled":true,"expected_version":0}`))
	request.Params = map[string]string{"channel": "alpha"}
	result := handler(domainTestContext(t), request)
	if result.Status != 200 || !registry.Enabled("iscp-owner", "alpha") {
		t.Fatalf("not effective %+v", result)
	}
	if got := handler(domainTestContext(t), request); got.Status != 200 || !bytes.Equal(got.Body, result.Body) {
		t.Fatalf("replay %+v", got)
	}
	current, _ := registry.Status(t.Context(), "iscp-owner", "alpha")
	if current.Version != 1 {
		t.Fatalf("repeated side effect %+v", current)
	}
}
func TestISCPDomainCredentialPersistsWithoutSecretEchoAndActivatesRuntime(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	var checks atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/info/tokens/issue":
			json.NewEncoder(w).Encode(map[string]any{"epoch": time.Now().UTC().Format("2006-01-02"), "issued_tokens": []any{map[string]any{"type": "info.basic", "token_mode": "internal_opaque", "token": "isolated-check", "expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}}, "quota_remaining": map[string]int{"info.basic": 9}})
		case "/v1/info/query":
			checks.Add(1)
			var input struct {
				RequestID string `json:"request_id"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			json.NewEncoder(w).Encode(map[string]any{"request_id": input.RequestID, "status": "ok", "answer_context": map[string]any{"summary": "ok", "key_facts": []any{}, "freshness": map[string]any{"status": "current", "staleness_risk": "low"}}, "sources": []any{}, "usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	server.cfg.Plugins.Entries.InfinimeshInfo.Config.BaseURL = provider.URL
	vault := credential.New(repo, credential.Options{Key: base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))})
	controller := integrationconfig.New(server.cfg, vault, repo, server.tools, nil)
	controller.Initialize(t.Context())
	server.integrations = controller
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	status, _ := controller.Get(ctx, integrationconfig.InfoID)
	request := domainTestRequest("settings.credentials.add", []byte(`{"label":"isolated","license_id":"lic_test","license_key":"ilk_v1.lic_test.private-canary"}`))
	request.Params = map[string]string{"integration_id": integrationconfig.InfoID}
	request.ExpectedRevision = integrationconfig.StatusRevision(status)
	saved := handler(ctx, request)
	if saved.Status != 200 || bytes.Contains(saved.Body, []byte("private-canary")) || checks.Load() != 1 {
		t.Fatalf("save %+v checks=%d", saved, checks.Load())
	}
	replay := handler(ctx, request)
	if replay.Status != 200 || checks.Load() != 1 {
		t.Fatalf("repeated credential validation %+v", replay)
	}
	status, _ = controller.Get(ctx, integrationconfig.InfoID)
	if len(status.Credentials) != 1 {
		t.Fatalf("credential count %+v", status)
	}
	body, _ := json.Marshal(map[string]string{"credential_id": status.Credentials[0].ID})
	activate := domainTestRequest("settings.credentials.activate", body)
	activate.Params = request.Params
	activate.ExpectedRevision = integrationconfig.StatusRevision(status)
	active := handler(ctx, activate)
	if active.Status != 200 || !server.tools.InfoConfigured() {
		t.Fatalf("runtime not activated %+v", active)
	}
	restored := integrationconfig.New(server.cfg, vault, repo, server.tools, nil)
	restored.Initialize(ctx)
	persisted, _ := restored.Get(ctx, integrationconfig.InfoID)
	if !persisted.Configured || persisted.ActiveCredentialID != status.Credentials[0].ID {
		t.Fatalf("restart lost selection %+v", persisted)
	}
}
