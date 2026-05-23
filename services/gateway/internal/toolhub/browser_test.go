package toolhub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestExtractReadableText(t *testing.T) {
	title, text := extractReadableText("<html><head><title>Test Page</title></head><body><h1>Hello</h1><script>ignore()</script></body></html>", "text/html")
	if title != "Test Page" {
		t.Fatalf("unexpected title: %q", title)
	}
	if text != "Test Page Hello" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestBrowserReadRejectsLoopbackURL(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	if _, err := hub.Execute(context.Background(), "browser.read", map[string]any{"url": "http://127.0.0.1:8080"}, "s", "run"); err == nil {
		t.Fatal("expected loopback URL to be rejected")
	}
}

func TestBrowserReadAllowsExplicitFixtureHost(t *testing.T) {
	cfg := config.Default()
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	hub := New(cfg, store.NewMemoryStore())
	blocked, err := hub.isBlockedBrowserHost(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("explicitly allowlisted fixture host was blocked")
	}
}

func TestBrowserReadArchivesRawSnapshot(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>Snapshot</title></head><body>Archive this raw page.</body></html>"))
	}))
	defer page.Close()

	root := t.TempDir()
	cfg := config.Default()
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{"url": page.URL}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["snapshot_ref"] == "" || out["snapshot_object_key"] == "" {
		t.Fatalf("browser output missing snapshot reference: %#v", out)
	}
	objects := st.ListArtifactObjects(10)
	if !hasBrowserArtifactKind(objects, "browser_snapshot") {
		t.Fatalf("browser snapshot was not cataloged: %#v", objects)
	}
	snapshotPath := filepath.Join(cfg.Storage.ArtifactDir, cfg.Storage.ArtifactBucket, out["snapshot_object_key"].(string))
	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Archive this raw page.") {
		t.Fatalf("snapshot file did not contain raw page: %s", raw)
	}
}

func hasBrowserArtifactKind(objects []app.ArtifactObject, kind string) bool {
	for _, object := range objects {
		if object.Kind == kind {
			return true
		}
	}
	return false
}
