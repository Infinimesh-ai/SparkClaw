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
