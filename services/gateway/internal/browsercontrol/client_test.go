package browsercontrol

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPControllerClientUsesUnixSocketAndStrictResponse(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "controller.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	const secret = "controller-client-secret-token"
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/validate-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var input struct {
			ProfileID string `json:"profile_id"`
			Token     string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if input.ProfileID != "default" || input.Token != secret {
			t.Errorf("unexpected request body: %#v", input)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(validationResult(55, 6, 1))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	client, err := NewHTTPControllerClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	result, err := client.ValidateToken(t.Context(), "default", []byte(secret))
	if err != nil || result.ControllerGeneration != 55 || result.SessionGeneration != 6 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHTTPControllerClientMapsSafeFailuresWithoutLeakingBody(t *testing.T) {
	const canary = "private-controller-diagnostic"
	client := serveControllerResponse(t, http.StatusServiceUnavailable, `{"error":"`+canary+`","code":"browser_busy","retryable":true}`)
	_, err := client.ValidateToken(t.Context(), "default", []byte("safe-test-token-value"))
	if ErrorCode(err) != CodeBusy || !ErrorRetryable(err) || strings.Contains(err.Error(), canary) {
		t.Fatalf("mapped error=%v code=%q", err, ErrorCode(err))
	}
}

func TestHTTPControllerClientPreservesExtensionUnavailableFailure(t *testing.T) {
	client := serveControllerResponse(t, http.StatusServiceUnavailable, `{"error":"browser extension is unavailable","code":"browser_extension_unavailable","retryable":true}`)
	_, err := client.ValidateToken(t.Context(), "default", []byte("safe-test-token-value"))
	if ErrorCode(err) != CodeExtensionUnavailable || !ErrorRetryable(err) {
		t.Fatalf("mapped error=%v code=%q retryable=%v", err, ErrorCode(err), ErrorRetryable(err))
	}
}

func TestHTTPControllerClientRejectsMalformedSuccess(t *testing.T) {
	client := serveControllerResponse(t, http.StatusOK, `{"schema_version":1,"state":"ready","profile_id":"default","controller_generation":1,"session_generation":1,"page_generation":1,"unknown":true}`)
	_, err := client.ValidateToken(t.Context(), "default", []byte("safe-test-token-value"))
	if ErrorCode(err) != CodeControllerUnavailable || !ErrorRetryable(err) {
		t.Fatalf("malformed response error=%v", err)
	}
}

func TestHTTPControllerClientRunsStrictSessionProtocol(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "controller.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	const secret = "controller-runtime-secret-token"
	expiresAt := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/acquire":
			var input struct {
				ProfileID            string `json:"profile_id"`
				Lane                 string `json:"lane"`
				TaskID               string `json:"task_id"`
				CredentialGeneration int64  `json:"credential_generation"`
				Token                string `json:"token"`
				WaitTimeoutMS        int64  `json:"wait_timeout_ms"`
				SessionTTLMS         int64  `json:"session_ttl_ms"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode acquire: %v", err)
			}
			if input.ProfileID != "default" || input.Lane != "mcp" || input.TaskID != "task-client" ||
				input.CredentialGeneration != 4 || input.Token != secret || input.WaitTimeoutMS != 250 || input.SessionTTLMS != 60_000 {
				t.Errorf("unexpected acquire: %#v", input)
			}
			_ = json.NewEncoder(w).Encode(SessionLease{
				SchemaVersion: 1, State: "acquired", ProfileID: "default", Lane: "mcp", SessionID: "session-client",
				CredentialGeneration: 4, ControllerGeneration: 9, SessionGeneration: 3, PageGeneration: 1,
				ExpiresAt: expiresAt,
			})
		case "/v1/execute":
			var input struct {
				SessionID            string         `json:"session_id"`
				ControllerGeneration int64          `json:"controller_generation"`
				SessionGeneration    int64          `json:"session_generation"`
				PageGeneration       int64          `json:"page_generation"`
				Operation            string         `json:"operation"`
				Arguments            map[string]any `json:"arguments"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode execute: %v", err)
			}
			if input.SessionID != "session-client" || input.ControllerGeneration != 9 ||
				input.SessionGeneration != 3 || input.PageGeneration != 1 || input.Operation != "tabs.list" {
				t.Errorf("unexpected execute: %#v", input)
			}
			_ = json.NewEncoder(w).Encode(ExecutionResult{
				SchemaVersion: 1, State: "completed", ProfileID: "default", Lane: "mcp", SessionID: "session-client",
				CredentialGeneration: 4, ControllerGeneration: 9, SessionGeneration: 3, PageGeneration: 1,
				Operation: "tabs.list", Result: json.RawMessage(`{"pages":[]}`),
			})
		case "/v1/release":
			_ = json.NewEncoder(w).Encode(ReleaseResult{
				SchemaVersion: 1, State: "released", ProfileID: "default",
				ControllerGeneration: 9, SessionGeneration: 3,
			})
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	client, err := NewHTTPControllerClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	lease, err := client.Acquire(t.Context(), AcquireRequest{
		ProfileID: "default", TaskID: "task-client", CredentialGeneration: 4,
		WaitTimeoutMS: 250, SessionTTLMS: 60_000,
	}, []byte(secret))
	if err != nil || lease.ExpiresAt != expiresAt {
		t.Fatalf("lease=%#v err=%v", lease, err)
	}
	executed, err := client.Execute(t.Context(), ExecuteRequest{Lease: lease, Operation: "tabs.list", Arguments: map[string]any{}})
	if err != nil || string(executed.Result) != `{"pages":[]}` {
		t.Fatalf("execution=%#v err=%v", executed, err)
	}
	if _, err := client.Release(t.Context(), ReleaseRequest{
		ProfileID: "default", SessionID: lease.SessionID,
		ControllerGeneration: lease.ControllerGeneration, SessionGeneration: lease.SessionGeneration,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPControllerClientRunsStrictScriptProtocol(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "controller.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	const secret = "controller-script-secret-token"
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/run-script":
			var input struct {
				ProfileID            string         `json:"profile_id"`
				TaskID               string         `json:"task_id"`
				CredentialGeneration int64          `json:"credential_generation"`
				Token                string         `json:"token"`
				Provider             string         `json:"provider"`
				Operation            string         `json:"operation"`
				ScriptID             string         `json:"script_id"`
				Revision             int            `json:"revision"`
				Input                map[string]any `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode run script: %v", err)
			}
			if input.ProfileID != "default" || input.TaskID != "email-script" ||
				input.CredentialGeneration != 4 || input.Token != secret || input.Provider != "gmail" ||
				input.Operation != "probe" || input.ScriptID != "gmail.login_probe" || input.Revision != 1 ||
				input.Input["operation"] != "probe" {
				t.Errorf("unexpected script request: %#v", input)
			}
			_ = json.NewEncoder(w).Encode(ScriptExecutionResult{
				SchemaVersion: 1, State: "completed", ProfileID: "default", Lane: "cli",
				Provider: "gmail", Operation: "probe", ScriptID: "gmail.login_probe", Revision: 1,
				SourceChecksum: `sha256:` + strings.Repeat("c", 64), CredentialGeneration: 4,
				ControllerGeneration: 9, SessionGeneration: 5,
				Result: json.RawMessage(`{"schema_version":1,"status":"ready","provider":"gmail"}`),
			})
		case "/v1/open-provider-login":
			var input struct {
				ProfileID string `json:"profile_id"`
				TaskID    string `json:"task_id"`
				Provider  string `json:"provider"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode open login: %v", err)
			}
			if input.ProfileID != "default" || input.TaskID != "email-login" || input.Provider != "gmail" {
				t.Errorf("unexpected login request: %#v", input)
			}
			_ = json.NewEncoder(w).Encode(OpenProviderLoginResult{
				SchemaVersion: 1, State: "opened", ProfileID: "default", Provider: "gmail",
				ControllerGeneration: 9, SessionGeneration: 6,
			})
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	client, err := NewHTTPControllerClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	result, err := client.RunScript(t.Context(), RunScriptRequest{
		ProfileID: "default", TaskID: "email-script", CredentialGeneration: 4,
		Provider: "gmail", Operation: "probe", ScriptID: "gmail.login_probe", Revision: 1,
		Input: map[string]any{"schema_version": 1, "operation": "probe"},
	}, []byte(secret))
	if err != nil || result.State != "completed" || result.SessionGeneration != 5 {
		t.Fatalf("script result=%#v err=%v", result, err)
	}
	opened, err := client.OpenProviderLogin(t.Context(), OpenProviderLoginRequest{
		ProfileID: "default", TaskID: "email-login", Provider: "gmail",
	})
	if err != nil || opened.State != "opened" || opened.SessionGeneration != 6 {
		t.Fatalf("login result=%#v err=%v", opened, err)
	}
}

func serveControllerResponse(t *testing.T, status int, body string) *HTTPControllerClient {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "controller.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	client, err := NewHTTPControllerClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}
