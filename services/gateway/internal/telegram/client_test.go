package telegram

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientGetUpdatesAndDoesNotExposeTokenInErrors(t *testing.T) {
	const token = "123:secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/getUpdates" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"offset":42`) || !strings.Contains(string(body), `"timeout":30`) {
			t.Fatalf("unexpected request body %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":42,"message":{"message_id":7,"from":{"id":9,"first_name":"Lin"},"chat":{"id":9,"type":"private"},"text":"hello"}}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, token, server.Client())
	updates, err := client.GetUpdates(context.Background(), 42, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Message == nil || updates[0].Message.Text != "hello" {
		t.Fatalf("unexpected updates: %#v", updates)
	}

	client = NewClient("http://127.0.0.1:1", token, &http.Client{})
	_, err = client.GetMe(context.Background())
	if err == nil {
		t.Fatal("expected connection failure")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked token: %v", err)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Temporary() {
		t.Fatalf("transport error lost retry semantics: %#v", err)
	}
}

func TestClientReturnsTelegramRetryAfter(t *testing.T) {
	const token = "123:api-error-canary"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests for 123:api-error-canary","parameters":{"retry_after":3}}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, token, server.Client()).GetMe(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 429 || apiErr.RetryAfter.Seconds() != 3 {
		t.Fatalf("unexpected API error: %#v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("API error leaked token: %v", err)
	}
}

func TestClientPreservesHTTPStatusForNonJSONErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream unavailable"))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "123:non-json-canary", server.Client()).GetMe(context.Background())
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected non-JSON API error: %#v", err)
	}
}

func TestClientTypesUnexpectedSuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "123:unexpected-response-canary", server.Client()).GetMe(context.Background())
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusOK {
		t.Fatalf("unexpected successful-response error: %#v", err)
	}
}

func TestClientDownloadFileEnforcesLimitAndUsesAtomicDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("12345"))
	}))
	defer server.Close()
	client := NewClient(server.URL, "token", server.Client())
	destination := filepath.Join(t.TempDir(), "nested", "file.txt")
	if _, err := client.DownloadFile(context.Background(), "docs/file.txt", destination, 4); err == nil {
		t.Fatal("expected download limit error")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial destination should not exist: %v", err)
	}
	if n, err := client.DownloadFile(context.Background(), "docs/file.txt", destination, 5); err != nil || n != 5 {
		t.Fatalf("download failed: n=%d err=%v", n, err)
	}
}

func TestClientRejectsOversizeUploadBeforeRequest(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "large-photo.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxPhotoUploadBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(server.URL, "token", server.Client()).SendPhoto(context.Background(), 1, 0, path, ""); err == nil {
		t.Fatal("expected oversize photo rejection")
	}
	if called {
		t.Fatal("oversize upload reached Telegram server")
	}
}
