package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationconfig"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type integrationEndpointController struct {
	status        integrationconfig.Status
	infoInput     integrationconfig.AddInfoCredentialInput
	infoCalls     int
	deleteErr     error
	checkErr      error
	deleteCalls   int
	credentialID  string
	integrationID string
}

func (c *integrationEndpointController) List(context.Context) []integrationconfig.Status {
	return []integrationconfig.Status{c.status}
}

func (c *integrationEndpointController) Get(context.Context, string) (integrationconfig.Status, error) {
	return c.status, nil
}

func (c *integrationEndpointController) AddInfoCredential(_ context.Context, input integrationconfig.AddInfoCredentialInput) (integrationconfig.Status, error) {
	c.infoCalls++
	c.infoInput = input
	return c.status, nil
}

func (c *integrationEndpointController) AddLocalMindCredential(context.Context, integrationconfig.AddLocalMindCredentialInput) (integrationconfig.Status, error) {
	return c.status, nil
}

func (c *integrationEndpointController) Activate(context.Context, string, string, bool) (integrationconfig.Status, error) {
	return c.status, nil
}

func (c *integrationEndpointController) Check(_ context.Context, integrationID, credentialID string) (integrationconfig.Status, error) {
	c.integrationID = integrationID
	c.credentialID = credentialID
	return c.status, c.checkErr
}

func (c *integrationEndpointController) Delete(_ context.Context, integrationID, credentialID string) (integrationconfig.Status, error) {
	c.deleteCalls++
	c.integrationID = integrationID
	c.credentialID = credentialID
	return c.status, c.deleteErr
}

func TestInfoCredentialAPIResponseIsRedacted(t *testing.T) {
	controller := &integrationEndpointController{status: integrationconfig.Status{
		ID: integrationconfig.InfoID, Category: "connections", Credentials: []integrationconfig.CredentialSummary{{
			ID: "info_cred_1", Label: "Family Info", ValidatedAt: time.Now().UTC(), State: integrationconfig.StateReady,
		}},
	}}
	server := newIntegrationEndpointTestServer(t, controller)
	secret := "ilk_v1.lic_family.secret-value"
	request := httptest.NewRequest(http.MethodPost, "/api/integrations/infinimesh-info/credentials", bytes.NewBufferString(
		`{"label":"Family Info","license_id":"lic_family","license_key":"`+secret+`"}`,
	))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create credential returned %d: %s", response.Code, response.Body.String())
	}
	if controller.infoInput.LicenseKey != secret || controller.infoInput.LicenseID != "lic_family" {
		t.Fatalf("controller did not receive credential input: %#v", controller.infoInput)
	}
	if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), "license_key") || strings.Contains(response.Body.String(), "license_id") {
		t.Fatalf("credential response leaked secret material: %s", response.Body.String())
	}
}

func TestIntegrationCredentialAPIRejectsUnknownFieldsAndOversizedBodies(t *testing.T) {
	controller := &integrationEndpointController{status: integrationconfig.Status{ID: integrationconfig.InfoID}}
	server := newIntegrationEndpointTestServer(t, controller)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"label":"Info","license_id":"lic","license_key":"key","activate":true}`},
		{name: "body limit", body: `{"label":"` + strings.Repeat("x", maxIntegrationCredentialRequestBytes) + `","license_id":"lic","license_key":"key"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/integrations/infinimesh-info/credentials", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"credential_invalid"`) {
				t.Fatalf("invalid request returned %d: %s", response.Code, response.Body.String())
			}
		})
	}
	if controller.infoCalls != 0 {
		t.Fatalf("invalid requests reached controller %d times", controller.infoCalls)
	}
}

func TestIntegrationCredentialAPITypedFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		path       string
		controller *integrationEndpointController
		wantStatus int
		wantCode   string
	}{
		{
			name: "active delete conflict", method: http.MethodDelete,
			path:       "/api/integrations/infinimesh-info/credentials/info_cred_active",
			controller: &integrationEndpointController{deleteErr: &integrationconfig.Error{Code: "active_credential_replacement_required"}},
			wantStatus: http.StatusConflict, wantCode: "active_credential_replacement_required",
		},
		{
			name: "authentication failure", method: http.MethodPost,
			path:       "/api/integrations/localmind/credentials/localmind_cred_1/check",
			controller: &integrationEndpointController{checkErr: &integrationconfig.Error{Code: "credential_auth_failed"}},
			wantStatus: http.StatusUnauthorized, wantCode: "credential_auth_failed",
		},
		{
			name: "temporary check failure", method: http.MethodPost,
			path:       "/api/integrations/localmind/credentials/localmind_cred_1/check",
			controller: &integrationEndpointController{checkErr: &integrationconfig.Error{Code: "credential_check_unavailable", Retryable: true}},
			wantStatus: http.StatusBadGateway, wantCode: "credential_check_unavailable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newIntegrationEndpointTestServer(t, test.controller)
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("typed failure returned %d: %s", response.Code, response.Body.String())
			}
			var body struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != test.wantCode || body.Retryable != integrationconfig.ErrorRetryable(firstIntegrationControllerError(test.controller)) {
				t.Fatalf("typed failure body=%#v", body)
			}
		})
	}
}

func firstIntegrationControllerError(controller *integrationEndpointController) error {
	if controller.deleteErr != nil {
		return controller.deleteErr
	}
	return controller.checkErr
}

func newIntegrationEndpointTestServer(t *testing.T, controller IntegrationController) *Server {
	t.Helper()
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	return New(cfg, st, tools, runtime, WithIntegrationController(controller))
}
