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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

const browserControlTestAPIToken = "browser-control-owner-api-token"

type browserControlEndpointController struct {
	status      browsercontrol.Status
	saveErr     error
	checkErr    error
	removeErr   error
	savedToken  []byte
	saveCalls   int
	checkCalls  int
	removeCalls int
}

func (c *browserControlEndpointController) Status(context.Context) browsercontrol.Status {
	return c.status
}

func (c *browserControlEndpointController) SaveToken(_ context.Context, token []byte) (browsercontrol.Status, error) {
	c.saveCalls++
	c.savedToken = append(c.savedToken[:0], token...)
	return c.status, c.saveErr
}

func (c *browserControlEndpointController) Check(context.Context) (browsercontrol.Status, error) {
	c.checkCalls++
	return c.status, c.checkErr
}

func (c *browserControlEndpointController) Remove(context.Context) (browsercontrol.Status, error) {
	c.removeCalls++
	return c.status, c.removeErr
}

func TestBrowserControlAPIIsAuthenticatedAndRedacted(t *testing.T) {
	validatedAt := time.Date(2026, 9, 4, 10, 30, 0, 0, time.UTC)
	controller := &browserControlEndpointController{status: browsercontrol.Status{
		Configured: true, State: browsercontrol.StateReady, ProfileID: "default", CredentialGeneration: 3,
		ControllerGeneration: 11, SessionGeneration: 7, PageGeneration: 1, LastValidatedAt: validatedAt,
		Versions: browsercontrol.Versions{
			Client: "playwright-mcp", ClientVersion: "0.0.80",
			PlaywrightVersion: "1.63.0-alpha-2026-08-31", BrowserChannel: "chrome",
		},
	}}
	server := newBrowserControlEndpointTestServer(t, controller, true)

	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/browser/extension"},
		{method: http.MethodPut, path: "/api/browser/extension/token", body: `{"token":"qualification-token-value"}`},
		{method: http.MethodPost, path: "/api/browser/extension/check", body: `{}`},
		{method: http.MethodDelete, path: "/api/browser/extension/token"},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s returned %d: %s", test.method, test.path, response.Code, response.Body.String())
		}
	}

	const secret = "browser-extension-secret-canary"
	put := authenticatedBrowserControlRequest(http.MethodPut, "/api/browser/extension/token", `{"token":"`+secret+`"}`)
	putResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK || controller.saveCalls != 1 || string(controller.savedToken) != secret {
		t.Fatalf("save returned %d calls=%d token=%q body=%s", putResponse.Code, controller.saveCalls, controller.savedToken, putResponse.Body.String())
	}
	if putResponse.Header().Get("Cache-Control") != "no-store" || strings.Contains(putResponse.Body.String(), secret) || strings.Contains(putResponse.Body.String(), "token") {
		t.Fatalf("save response exposed credential material or caching: headers=%v body=%s", putResponse.Header(), putResponse.Body.String())
	}

	get := authenticatedBrowserControlRequest(http.MethodGet, "/api/browser/extension", "")
	getResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("status returned %d: %s", getResponse.Code, getResponse.Body.String())
	}
	var status map[string]any
	if err := json.NewDecoder(getResponse.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["configured"] != true || status["profile_id"] != "default" || status["last_validated_at"] != validatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected public browser status: %#v", status)
	}
	if _, exists := status["controller_socket"]; exists {
		t.Fatalf("controller socket leaked: %#v", status)
	}
}

func TestBrowserControlAPIRejectsQueriesAndInvalidJSON(t *testing.T) {
	controller := &browserControlEndpointController{status: browsercontrol.Status{State: browsercontrol.StateNotConfigured, ProfileID: "default"}}
	server := newBrowserControlEndpointTestServer(t, controller, true)
	const secret = "query-secret-canary"

	for _, test := range []struct {
		name        string
		path        string
		body        string
		contentType string
	}{
		{name: "query", path: "/api/browser/extension/token?token=" + secret, body: `{"token":"qualification-token-value"}`},
		{name: "unknown field", path: "/api/browser/extension/token", body: `{"token":"qualification-token-value","profile":"default"}`},
		{name: "trailing JSON", path: "/api/browser/extension/token", body: `{"token":"qualification-token-value"} {}`},
		{name: "oversized", path: "/api/browser/extension/token", body: `{"token":"` + strings.Repeat("x", maxBrowserControlRequestBytes) + `"}`},
		{name: "non-object", path: "/api/browser/extension/token", body: `null`},
		{name: "wrong content type", path: "/api/browser/extension/token", body: `{"token":"qualification-token-value"}`, contentType: "text/plain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedBrowserControlRequest(http.MethodPut, test.path, test.body)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"browser_control_invalid_request"`) {
				t.Fatalf("invalid request returned %d: %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("invalid response leaked query secret: %s", response.Body.String())
			}
		})
	}
	if controller.saveCalls != 0 {
		t.Fatalf("invalid requests reached controller %d times", controller.saveCalls)
	}
}

func TestBrowserControlAPITypedFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		path       string
		body       string
		error      error
		wantStatus int
		wantCode   string
	}{
		{name: "not configured", method: http.MethodPost, path: "/api/browser/extension/check", body: `{}`, error: &browsercontrol.Error{Code: browsercontrol.CodeNotConfigured}, wantStatus: http.StatusConflict, wantCode: browsercontrol.CodeNotConfigured},
		{name: "extension rejected", method: http.MethodPut, path: "/api/browser/extension/token", body: `{"token":"qualification-token-value"}`, error: &browsercontrol.Error{Code: browsercontrol.CodeExtensionRejected}, wantStatus: http.StatusUnprocessableEntity, wantCode: browsercontrol.CodeExtensionRejected},
		{name: "extension unavailable", method: http.MethodPut, path: "/api/browser/extension/token", body: `{"token":"qualification-token-value"}`, error: &browsercontrol.Error{Code: browsercontrol.CodeExtensionUnavailable, Retryable: true}, wantStatus: http.StatusServiceUnavailable, wantCode: browsercontrol.CodeExtensionUnavailable},
		{name: "controller unavailable", method: http.MethodPost, path: "/api/browser/extension/check", body: `{}`, error: &browsercontrol.Error{Code: browsercontrol.CodeControllerUnavailable, Retryable: true}, wantStatus: http.StatusServiceUnavailable, wantCode: browsercontrol.CodeControllerUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := &browserControlEndpointController{status: browsercontrol.Status{State: browsercontrol.StateNeedsAttention, ProfileID: "default"}}
			switch test.method {
			case http.MethodPut:
				controller.saveErr = test.error
			default:
				controller.checkErr = test.error
			}
			server := newBrowserControlEndpointTestServer(t, controller, true)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, authenticatedBrowserControlRequest(test.method, test.path, test.body))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) || strings.Contains(response.Body.String(), "private") {
				t.Fatalf("typed failure returned %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBrowserControlAPIRequiresConfiguredGatewayAuthentication(t *testing.T) {
	controller := &browserControlEndpointController{status: browsercontrol.Status{State: browsercontrol.StateNotConfigured, ProfileID: "default"}}
	server := newBrowserControlEndpointTestServer(t, controller, false)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/browser/extension", nil))
	if response.Code != http.StatusServiceUnavailable || controller.saveCalls != 0 || controller.checkCalls != 0 || controller.removeCalls != 0 {
		t.Fatalf("auth-disabled browser control returned %d: %s", response.Code, response.Body.String())
	}
}

func authenticatedBrowserControlRequest(method, path, body string) *http.Request {
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+browserControlTestAPIToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func newBrowserControlEndpointTestServer(t *testing.T, controller BrowserControlController, authenticated bool) *Server {
	t.Helper()
	cfg := testConfig(t.TempDir())
	if authenticated {
		cfg.Gateway.APIToken = browserControlTestAPIToken
	} else {
		cfg.Gateway.APIToken = ""
		cfg.Gateway.PairingRequired = false
	}
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	return New(cfg, st, tools, runtime, WithBrowserControlController(controller))
}
