package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connector"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestConnectorAPIRequiresExplicitVersionedOptIn(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Tools.Notifications.Channels["alpha"] = config.NotificationChannelConfig{Enabled: false, Provider: "alpha-http"}
	st := store.NewMemoryStore()
	registry := connector.NewRegistry(cfg, st)
	if err := registry.Register(connector.Registration{
		Channel: "alpha", SetupKind: app.ConnectorSetupSecret, Binding: &genericBindingAdapter{},
	}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime, WithBindingRouter(registry.BindingRouter()), WithConnectorController(registry))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/connectors")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var listed struct {
		Connectors []app.ConnectorStatus `json:"connectors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	var initial app.ConnectorStatus
	for _, status := range listed.Connectors {
		if status.Channel == "alpha" {
			initial = status
		}
	}
	if initial.Channel == "" || initial.Enabled || initial.Version != 0 || initial.State != app.ConnectorStateDisabled {
		t.Fatalf("unexpected initial connector status: %#v", initial)
	}
	disabledBinding, err := http.Post(ts.URL+"/api/notification-bindings/alpha/start", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	disabledBinding.Body.Close()
	if disabledBinding.StatusCode != http.StatusConflict || len(st.ListNotificationBindings("alpha", "")) != 0 {
		t.Fatalf("disabled connector accepted binding: status=%d", disabledBinding.StatusCode)
	}

	patch := func(body string) (*http.Response, app.ConnectorStatus) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/connectors/alpha", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var status app.ConnectorStatus
		if response.StatusCode == http.StatusOK {
			if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
				t.Fatal(err)
			}
		}
		response.Body.Close()
		return response, status
	}

	enableResp, enabled := patch(`{"enabled":true,"expected_version":0}`)
	if enableResp.StatusCode != http.StatusOK || !enabled.Enabled || enabled.Version != 1 || enabled.State != app.ConnectorStateSetupRequired {
		t.Fatalf("connector enable failed: status=%d connector=%#v", enableResp.StatusCode, enabled)
	}
	configResp, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	var publicConfig struct {
		Tools struct {
			Notifications struct {
				Channels map[string]struct {
					Enabled         bool `json:"enabled"`
					OperatorEnabled bool `json:"operator_enabled"`
				} `json:"channels"`
			} `json:"notifications"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(configResp.Body).Decode(&publicConfig); err != nil {
		configResp.Body.Close()
		t.Fatal(err)
	}
	configResp.Body.Close()
	alphaConfig := publicConfig.Tools.Notifications.Channels["alpha"]
	if !alphaConfig.Enabled || alphaConfig.OperatorEnabled {
		t.Fatalf("public config did not separate owner state from configured default: %#v", alphaConfig)
	}
	enabledBinding, err := http.Post(ts.URL+"/api/notification-bindings/alpha/start", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	enabledBinding.Body.Close()
	if enabledBinding.StatusCode != http.StatusCreated || len(st.ListNotificationBindings("alpha", "")) != 1 {
		t.Fatalf("enabled connector rejected binding: status=%d", enabledBinding.StatusCode)
	}
	staleResp, _ := patch(`{"enabled":false,"expected_version":0}`)
	if staleResp.StatusCode != http.StatusConflict {
		t.Fatalf("stale connector update returned %d", staleResp.StatusCode)
	}
	disableResp, disabled := patch(`{"enabled":false,"expected_version":1}`)
	if disableResp.StatusCode != http.StatusOK || disabled.Enabled || disabled.Version != 2 || disabled.State != app.ConnectorStateDisabled {
		t.Fatalf("connector disable failed: status=%d connector=%#v", disableResp.StatusCode, disabled)
	}
}
